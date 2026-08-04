// typedb-go-ffi: C FFI wrapper around typedb-driver for Go CGo bindings.
//
// Design: All functions use opaque pointers. Errors are returned via
// out-parameters (*mut *mut c_char) that receive error message strings.
// Query results are returned as MessagePack, either as one buffer through
// typedb_transaction_query() or as bounded pull chunks through the query
// stream functions.
//
// Safety: every exported function wraps its body in catch_unwind (see
// ffi_call / ffi_call_unit). A Rust panic never unwinds across the C
// boundary; it is converted into an err_out message plus the function's
// null/false/no-op default return.
//
// Concept handles: entity/relation concepts are only registered in the
// process-global registry when a query explicitly requests it
// (register_concepts = true). Registered handles stay valid across
// transactions until released with typedb_concept_drop /
// typedb_concepts_drop_all.

use std::any::Any;
use std::collections::HashMap;
use std::ffi::{CStr, CString, c_char, c_void};
use std::panic::{AssertUnwindSafe, catch_unwind};
use std::ptr::null_mut;
use std::str::FromStr;
use std::sync::{
    Mutex, MutexGuard, Once, OnceLock,
    atomic::{AtomicU64, Ordering},
};
use std::time::{Duration, Instant};

use chrono::{DateTime, FixedOffset, NaiveDate, NaiveDateTime};
use serde::{
    Deserialize, Serialize, Serializer,
    ser::{Error as _, SerializeMap, SerializeSeq},
};
use serde_json::json;

use typedb_driver::{
    Addresses, Credentials, DriverOptions, DriverTlsConfig, Promise, QueryOptions, Transaction,
    TransactionOptions, TransactionType, TypeDBDriver,
    answer::{
        ConceptDocument, ConceptRow, QueryAnswer,
        concept_document::{Leaf, Node},
    },
    concept::{
        Concept, Kind, Value,
        value::{Decimal, Duration as TypeDBDuration, TimeZone, ValueType},
    },
    given::{GivenRowEntry, GivenRows},
};

static NEXT_CONCEPT_HANDLE: AtomicU64 = AtomicU64::new(1);
static CONCEPT_REGISTRY: OnceLock<Mutex<HashMap<String, Concept>>> = OnceLock::new();

struct QueryResultStream {
    answer: QueryAnswer,
    register_concepts: bool,
    finished: bool,
    pending: Option<QueryStreamResult>,
}

enum QueryStreamResult {
    Document(ConceptDocument),
    Row(ConceptRow),
}

/// Serialize a row concept directly to MessagePack without first building a
/// serde_json::Value tree.
struct RowConcept<'a> {
    concept: &'a Concept,
    register_handle: bool,
}

impl Serialize for RowConcept<'_> {
    fn serialize<S>(&self, serializer: S) -> Result<S::Ok, S::Error>
    where
        S: Serializer,
    {
        if let Some(value) = self.concept.try_get_value() {
            return RowValue(value).serialize(serializer);
        }
        if let Some(iid) = self.concept.try_get_iid() {
            let mut map =
                serializer.serialize_map(Some(if self.register_handle { 4 } else { 3 }))?;
            map.serialize_entry("_kind", concept_category_name(self.concept))?;
            map.serialize_entry("_type", self.concept.get_label())?;
            map.serialize_entry("_iid", &iid.to_string())?;
            if self.register_handle {
                map.serialize_entry("_concept_handle", &register_concept(self.concept))?;
            }
            return map.end();
        }
        if self.concept.is_type() {
            let mut map = serializer.serialize_map(Some(2))?;
            map.serialize_entry("_kind", concept_category_name(self.concept))?;
            map.serialize_entry("_label", self.concept.get_label())?;
            return map.end();
        }
        serializer.serialize_str(&format!("{:?}", self.concept))
    }
}

struct RowValue<'a>(&'a Value);

impl Serialize for RowValue<'_> {
    fn serialize<S>(&self, serializer: S) -> Result<S::Ok, S::Error>
    where
        S: Serializer,
    {
        match self.0 {
            Value::Boolean(value) => serializer.serialize_bool(*value),
            Value::Integer(value) => serializer.serialize_i64(*value),
            Value::Double(value) => serializer.serialize_f64(*value),
            Value::String(value) => serializer.serialize_str(value),
            Value::Decimal(value) => serializer.serialize_str(&value.to_string()),
            Value::Date(value) => serializer.serialize_str(&value.to_string()),
            Value::Datetime(value) => serializer.serialize_str(&value.to_string()),
            Value::DatetimeTZ(value) => serializer.serialize_str(&value.to_string()),
            Value::Duration(value) => serializer.serialize_str(&value.to_string()),
            Value::Struct(value, _) => serializer.serialize_str(&format!("{:?}", value)),
        }
    }
}

/// Serialize a fetch document directly from the driver's node tree. Calling
/// ConceptDocument::into_json would allocate a second recursive tree first.
struct Document<'a>(&'a ConceptDocument);

impl Serialize for Document<'_> {
    fn serialize<S>(&self, serializer: S) -> Result<S::Ok, S::Error>
    where
        S: Serializer,
    {
        match self.0.root.as_ref() {
            Some(root) => DocumentNode(root).serialize(serializer),
            None => serializer.serialize_none(),
        }
    }
}

struct DocumentNode<'a>(&'a Node);

impl Serialize for DocumentNode<'_> {
    fn serialize<S>(&self, serializer: S) -> Result<S::Ok, S::Error>
    where
        S: Serializer,
    {
        match self.0 {
            Node::Map(values) => {
                let mut map = serializer.serialize_map(Some(values.len()))?;
                for (name, value) in values {
                    map.serialize_entry(name, &DocumentNode(value))?;
                }
                map.end()
            }
            Node::List(values) => {
                let mut sequence = serializer.serialize_seq(Some(values.len()))?;
                for value in values {
                    sequence.serialize_element(&DocumentNode(value))?;
                }
                sequence.end()
            }
            Node::Leaf(value) => match value {
                Some(value) => DocumentLeaf(value).serialize(serializer),
                None => serializer.serialize_none(),
            },
        }
    }
}

struct DocumentLeaf<'a>(&'a Leaf);

impl Serialize for DocumentLeaf<'_> {
    fn serialize<S>(&self, serializer: S) -> Result<S::Ok, S::Error>
    where
        S: Serializer,
    {
        match self.0 {
            Leaf::Empty => serializer.serialize_none(),
            Leaf::Concept(concept) => serialize_document_concept(concept, serializer),
            Leaf::ValueType(value_type) => serializer.serialize_str(value_type.name()),
            Leaf::Kind(kind) => serializer.serialize_str(kind.name()),
        }
    }
}

fn serialize_document_concept<S>(concept: &Concept, serializer: S) -> Result<S::Ok, S::Error>
where
    S: Serializer,
{
    match concept {
        Concept::EntityType(type_) => {
            serialize_document_type(Kind::Entity, type_.label(), serializer)
        }
        Concept::RelationType(type_) => {
            serialize_document_type(Kind::Relation, type_.label(), serializer)
        }
        Concept::RoleType(type_) => serialize_document_type(Kind::Role, type_.label(), serializer),
        Concept::AttributeType(type_) => {
            let mut map = serializer.serialize_map(Some(3))?;
            map.serialize_entry("kind", Kind::Attribute.name())?;
            map.serialize_entry("label", type_.label())?;
            map.serialize_entry(
                "valueType",
                type_.value_type().map_or("none", ValueType::name),
            )?;
            map.end()
        }
        Concept::Attribute(_) | Concept::Value(_) => {
            DocumentValue(concept.try_get_value().expect("value concept")).serialize(serializer)
        }
        Concept::Entity(_) | Concept::Relation(_) => Err(S::Error::custom(
            "unexpected entity or relation concept in fetch response",
        )),
    }
}

fn serialize_document_type<S>(kind: Kind, label: &str, serializer: S) -> Result<S::Ok, S::Error>
where
    S: Serializer,
{
    let mut map = serializer.serialize_map(Some(2))?;
    map.serialize_entry("kind", kind.name())?;
    map.serialize_entry("label", label)?;
    map.end()
}

struct DocumentValue<'a>(&'a Value);

impl Serialize for DocumentValue<'_> {
    fn serialize<S>(&self, serializer: S) -> Result<S::Ok, S::Error>
    where
        S: Serializer,
    {
        match self.0 {
            Value::Boolean(value) => serializer.serialize_bool(*value),
            // Keep the typedb-driver JSON representation, which converts
            // document integers to JSON numbers before MessagePack encoding.
            Value::Integer(value) => serializer.serialize_f64(*value as f64),
            Value::Double(value) => serializer.serialize_f64(*value),
            Value::String(value) => serializer.serialize_str(value),
            Value::Decimal(_)
            | Value::Date(_)
            | Value::Datetime(_)
            | Value::DatetimeTZ(_)
            | Value::Duration(_) => serializer.serialize_str(&self.0.to_string()),
            Value::Struct(value, name) => {
                let mut map = serializer.serialize_map(Some(1))?;
                map.serialize_entry(name, &DocumentStruct(value))?;
                map.end()
            }
        }
    }
}

struct DocumentStruct<'a>(&'a typedb_driver::concept::value::Struct);

impl Serialize for DocumentStruct<'_> {
    fn serialize<S>(&self, serializer: S) -> Result<S::Ok, S::Error>
    where
        S: Serializer,
    {
        let mut map = serializer.serialize_map(Some(self.0.fields().len()))?;
        for (name, value) in self.0.fields() {
            match value {
                Some(value) => map.serialize_entry(name, &DocumentValue(value))?,
                None => map.serialize_entry(name, &Option::<()>::None)?,
            }
        }
        map.end()
    }
}

fn concept_category_name(concept: &Concept) -> &'static str {
    match concept {
        Concept::EntityType(_) => "entitytype",
        Concept::RelationType(_) => "relationtype",
        Concept::RoleType(_) => "roletype",
        Concept::AttributeType(_) => "attributetype",
        Concept::Entity(_) => "entity",
        Concept::Relation(_) => "relation",
        Concept::Attribute(_) => "attribute",
        Concept::Value(_) => "value",
    }
}

/// Lock the concept registry, recovering from poisoning instead of panicking.
/// The registry contains only plain data, so a panic while the lock was held
/// cannot leave it in a logically corrupt state.
fn registry_lock() -> MutexGuard<'static, HashMap<String, Concept>> {
    CONCEPT_REGISTRY
        .get_or_init(|| Mutex::new(HashMap::new()))
        .lock()
        .unwrap_or_else(|poisoned| poisoned.into_inner())
}

fn register_concept(concept: &Concept) -> String {
    let id = NEXT_CONCEPT_HANDLE.fetch_add(1, Ordering::Relaxed);
    let handle = format!("concept-{}", id);
    registry_lock().insert(handle.clone(), concept.clone());
    handle
}

fn get_registered_concept(handle: &str) -> Result<Concept, String> {
    registry_lock()
        .get(handle)
        .cloned()
        .ok_or_else(|| format!("unknown concept handle: {}", handle))
}

/// Release a single registered concept handle. Unknown handles are ignored.
#[unsafe(no_mangle)]
pub extern "C" fn typedb_concept_drop(handle: *const c_char) {
    ffi_call_unit(|| {
        if let Ok(handle) = c_str(handle) {
            registry_lock().remove(handle);
        }
    });
}

/// Release every concept handle registered by this process.
#[unsafe(no_mangle)]
pub extern "C" fn typedb_concepts_drop_all() {
    ffi_call_unit(|| {
        registry_lock().clear();
    });
}

// ---------------------------------------------------------------------------
// Error handling via out-parameters
// ---------------------------------------------------------------------------

/// Sets error message via out-parameter.
/// If err_out is null, the error is silently dropped.
/// Interior NUL bytes are escaped so the message is never swallowed.
fn set_error(err_out: *mut *mut c_char, err: impl std::fmt::Display) {
    if err_out.is_null() {
        return;
    }
    unsafe { *err_out = to_c_string(err.to_string()) }
}

fn panic_message(payload: Box<dyn Any + Send>) -> String {
    if let Some(s) = payload.downcast_ref::<&str>() {
        format!("panic in typedb-go-ffi: {}", s)
    } else if let Some(s) = payload.downcast_ref::<String>() {
        format!("panic in typedb-go-ffi: {}", s)
    } else {
        "panic in typedb-go-ffi".to_string()
    }
}

/// Run an exported function body, containing Rust panics at the FFI boundary.
/// Err results and panics become an err_out message plus the default value.
fn ffi_call<T>(
    err_out: *mut *mut c_char,
    default: impl FnOnce() -> T,
    body: impl FnOnce() -> Result<T, String>,
) -> T {
    match catch_unwind(AssertUnwindSafe(body)) {
        Ok(Ok(value)) => value,
        Ok(Err(message)) => {
            set_error(err_out, message);
            default()
        }
        Err(payload) => {
            set_error(err_out, panic_message(payload));
            default()
        }
    }
}

/// Contain panics in exported functions that have no error out-parameter.
/// The panic is reported to stderr (best effort) and otherwise swallowed.
fn ffi_call_unit(body: impl FnOnce()) {
    if let Err(payload) = catch_unwind(AssertUnwindSafe(body)) {
        let _ = catch_unwind(AssertUnwindSafe(|| {
            eprintln!("typedb_go_rust.panic {}", panic_message(payload));
        }));
    }
}

// ---------------------------------------------------------------------------
// String helpers
// ---------------------------------------------------------------------------

/// Borrow a NUL-terminated C string as a &str.
///
/// The returned borrow is tied to the caller-chosen lifetime 'a; the caller
/// must ensure ptr stays valid (and unmodified) for that lifetime. Null
/// pointers and invalid UTF-8 are reported as errors instead of panicking or
/// silently substituting an empty string.
fn c_str<'a>(ptr: *const c_char) -> Result<&'a str, String> {
    if ptr.is_null() {
        return Err("null string pointer passed to typedb-go-ffi".to_string());
    }
    unsafe { CStr::from_ptr(ptr) }
        .to_str()
        .map_err(|e| format!("invalid UTF-8 in string passed to typedb-go-ffi: {}", e))
}

/// Convert a Rust string into a heap-allocated C string for the Go side.
/// Interior NUL bytes are escaped as "\0" so content is never silently
/// replaced with an empty string. Caller frees with typedb_free_string.
fn to_c_string(s: String) -> *mut c_char {
    let sanitized = if s.contains('\0') {
        s.replace('\0', "\\0")
    } else {
        s
    };
    // Infallible: sanitized contains no NUL bytes.
    match CString::new(sanitized) {
        Ok(cstr) => cstr.into_raw(),
        Err(_) => unreachable!("NUL bytes were escaped above"),
    }
}

/// Dereference an opaque pointer received over the FFI, rejecting null.
fn deref<'a, T>(ptr: *const T, what: &str) -> Result<&'a T, String> {
    if ptr.is_null() {
        return Err(format!("null {} pointer passed to typedb-go-ffi", what));
    }
    Ok(unsafe { &*ptr })
}

/// Mutably dereference an opaque pointer received over the FFI, rejecting null.
fn deref_mut<'a, T>(ptr: *mut T, what: &str) -> Result<&'a mut T, String> {
    if ptr.is_null() {
        return Err(format!("null {} pointer passed to typedb-go-ffi", what));
    }
    Ok(unsafe { &mut *ptr })
}

fn c_string_slice<'a>(ptr: *const *const c_char, count: usize) -> Result<Vec<&'a str>, String> {
    if ptr.is_null() || count == 0 {
        return Ok(vec![]);
    }
    unsafe { std::slice::from_raw_parts(ptr, count) }
        .iter()
        .map(|s| c_str(*s))
        .collect()
}

fn addresses_from_ffi(
    public_addresses: *const *const c_char,
    private_addresses: *const *const c_char,
    count: usize,
) -> Result<Addresses, String> {
    let public = c_string_slice(public_addresses, count)?;
    if private_addresses.is_null() {
        return Addresses::try_from_addresses_str(public).map_err(|e| e.to_string());
    }

    let private = c_string_slice(private_addresses, count)?;
    let mut translation = HashMap::with_capacity(count);
    for (public, private) in public.into_iter().zip(private) {
        translation.insert(public, private);
    }
    Addresses::try_from_translation_str(translation).map_err(|e| e.to_string())
}

/// Free a string returned by this library.
///
/// # Safety
///
/// `s` must be null or a live pointer returned by this library that has not
/// already been freed.
#[unsafe(no_mangle)]
pub unsafe extern "C" fn typedb_free_string(s: *mut c_char) {
    ffi_call_unit(|| {
        if !s.is_null() {
            unsafe { drop(CString::from_raw(s)) }
        }
    });
}

/// Free a byte buffer returned by query functions.
/// The caller must pass both the pointer and the length that were returned.
///
/// # Safety
///
/// `ptr` must be null or a live buffer returned by this library, `len` must
/// be the original buffer length, and the buffer must not already be freed.
#[unsafe(no_mangle)]
pub unsafe extern "C" fn typedb_free_bytes(ptr: *mut u8, len: usize) {
    ffi_call_unit(|| {
        if !ptr.is_null() && len > 0 {
            unsafe {
                drop(Vec::from_raw_parts(ptr, len, len));
            }
        }
    });
}

// ---------------------------------------------------------------------------
// Logging
// ---------------------------------------------------------------------------

/// Initialize TypeDB driver logging. Call once at startup.
///
/// Installs a tracing subscriber (writing to stderr) so log events emitted by
/// the underlying typedb-driver crate become visible. The filter is read from
/// the TYPEDB_GO_RUST_LOG environment variable, falling back to RUST_LOG.
/// When neither is set, logging stays off.
#[unsafe(no_mangle)]
pub extern "C" fn typedb_init_logging() {
    ffi_call_unit(|| {
        static INIT: Once = Once::new();
        INIT.call_once(|| {
            let directives = std::env::var("TYPEDB_GO_RUST_LOG")
                .or_else(|_| std::env::var("RUST_LOG"))
                .unwrap_or_else(|_| "off".to_string());
            let filter = tracing_subscriber::EnvFilter::try_new(&directives)
                .unwrap_or_else(|_| tracing_subscriber::EnvFilter::new("off"));
            let _ = tracing_subscriber::fmt()
                .with_env_filter(filter)
                .with_writer(std::io::stderr)
                .try_init();
        });
    });
}

fn rust_debug_enabled() -> bool {
    static ENABLED: OnceLock<bool> = OnceLock::new();
    *ENABLED.get_or_init(|| {
        let value = std::env::var("TYPEDB_GO_DEBUG_RUST")
            .unwrap_or_default()
            .trim()
            .to_lowercase();
        matches!(value.as_str(), "1" | "true" | "yes" | "on" | "debug")
    })
}

fn rust_debug_log(event: &str, fields: Vec<(&'static str, String)>) {
    if !rust_debug_enabled() {
        return;
    }
    let mut msg = format!("typedb_go_rust.{}", event);
    for (key, value) in fields {
        msg.push(' ');
        msg.push_str(key);
        msg.push('=');
        msg.push_str(&value);
    }
    eprintln!("{}", msg);
}

fn rust_debug_log_timed(event: &str, start: Instant, mut fields: Vec<(&'static str, String)>) {
    if !rust_debug_enabled() {
        return;
    }
    fields.push(("elapsed_ms", start.elapsed().as_millis().to_string()));
    rust_debug_log(event, fields);
}

fn query_op(query: &str) -> String {
    let first = query
        .split_whitespace()
        .next()
        .unwrap_or("")
        .trim_matches(';')
        .to_lowercase();
    match first.as_str() {
        "given" | "match" | "insert" | "delete" | "update" | "define" | "undefine" | "fetch"
        | "reduce" => first,
        _ => "other".to_string(),
    }
}

fn query_fingerprint(query: &str) -> String {
    const OFFSET: u64 = 14695981039346656037;
    const PRIME: u64 = 1099511628211;
    let mut hash = OFFSET;
    for b in query.as_bytes() {
        hash ^= *b as u64;
        hash = hash.wrapping_mul(PRIME);
    }
    format!("{:016x}", hash)
}

// ---------------------------------------------------------------------------
// Credentials
// ---------------------------------------------------------------------------

/// Create credentials. Returns null and sets err_out on invalid input.
/// Caller must free with typedb_credentials_drop.
#[unsafe(no_mangle)]
pub extern "C" fn typedb_credentials_new(
    username: *const c_char,
    password: *const c_char,
    err_out: *mut *mut c_char,
) -> *mut Credentials {
    ffi_call(err_out, null_mut, || {
        let username = c_str(username)?;
        let password = c_str(password)?;
        Ok(Box::into_raw(Box::new(Credentials::new(
            username, password,
        ))))
    })
}

/// Free credentials.
///
/// # Safety
///
/// `creds` must be null or a live pointer returned by
/// `typedb_credentials_new` that has not already been freed.
#[unsafe(no_mangle)]
pub unsafe extern "C" fn typedb_credentials_drop(creds: *mut Credentials) {
    ffi_call_unit(|| {
        if !creds.is_null() {
            unsafe { drop(Box::from_raw(creds)) }
        }
    });
}

// ---------------------------------------------------------------------------
// DriverOptions
// ---------------------------------------------------------------------------

/// Create driver options. tls_root_ca can be null. Caller must free with typedb_driver_options_drop.
#[unsafe(no_mangle)]
pub extern "C" fn typedb_driver_options_new(
    is_tls_enabled: bool,
    tls_root_ca: *const c_char,
    err_out: *mut *mut c_char,
) -> *mut DriverOptions {
    ffi_call(err_out, null_mut, || {
        let tls_config = if !is_tls_enabled {
            DriverTlsConfig::disabled()
        } else if tls_root_ca.is_null() {
            DriverTlsConfig::enabled_with_native_root_ca()
        } else {
            DriverTlsConfig::enabled_with_root_ca(std::path::Path::new(c_str(tls_root_ca)?))
                .map_err(|e| e.to_string())?
        };
        Ok(Box::into_raw(Box::new(DriverOptions::new(tls_config))))
    })
}

/// Set driver request timeout in milliseconds.
#[unsafe(no_mangle)]
pub extern "C" fn typedb_driver_options_set_request_timeout(
    opts: *mut DriverOptions,
    timeout_millis: i64,
) {
    ffi_call_unit(|| {
        if let Ok(o) = deref_mut(opts, "driver options") {
            o.request_timeout = Duration::from_millis(timeout_millis.max(0) as u64);
        }
    });
}

/// Set primary failover retry count.
#[unsafe(no_mangle)]
pub extern "C" fn typedb_driver_options_set_primary_failover_retries(
    opts: *mut DriverOptions,
    retries: usize,
) {
    ffi_call_unit(|| {
        if let Ok(o) = deref_mut(opts, "driver options") {
            o.primary_failover_retries = retries;
        }
    });
}

/// Free driver options.
///
/// # Safety
///
/// `opts` must be null or a live pointer returned by
/// `typedb_driver_options_new` that has not already been freed.
#[unsafe(no_mangle)]
pub unsafe extern "C" fn typedb_driver_options_drop(opts: *mut DriverOptions) {
    ffi_call_unit(|| {
        if !opts.is_null() {
            unsafe { drop(Box::from_raw(opts)) }
        }
    });
}

// ---------------------------------------------------------------------------
// Driver (connection)
// ---------------------------------------------------------------------------

fn open_driver(
    addresses: Addresses,
    credentials: *const Credentials,
    options: *const DriverOptions,
) -> Result<*mut TypeDBDriver, String> {
    let creds = deref(credentials, "credentials")?;
    let opts = deref(options, "driver options")?;
    match TypeDBDriver::new_with_description(addresses, creds.clone(), opts.clone(), "go") {
        Ok(driver) => Ok(Box::into_raw(Box::new(driver))),
        Err(e) => Err(e.to_string()),
    }
}

/// Open a connection to TypeDB. Returns null on error.
///
/// The address is used exactly as given; no localhost port rewriting is
/// applied. Use typedb_driver_open_addresses with a translation map when the
/// server advertises an address that differs from the one clients dial.
///
/// Caller must free with typedb_driver_close.
#[unsafe(no_mangle)]
pub extern "C" fn typedb_driver_open(
    address: *const c_char,
    credentials: *const Credentials,
    options: *const DriverOptions,
    err_out: *mut *mut c_char,
) -> *mut TypeDBDriver {
    ffi_call(err_out, null_mut, || {
        let start = Instant::now();
        let address = c_str(address)?;
        rust_debug_log(
            "ffi.typedb_driver_open.enter",
            vec![("address", address.to_string())],
        );

        let result = Addresses::try_from_address_str(address)
            .map_err(|e| e.to_string())
            .and_then(|addresses| open_driver(addresses, credentials, options));
        match &result {
            Ok(_) => rust_debug_log_timed(
                "ffi.typedb_driver_open.exit",
                start,
                vec![
                    ("address", address.to_string()),
                    ("result", "ok".to_string()),
                ],
            ),
            Err(e) => rust_debug_log_timed(
                "ffi.typedb_driver_open.exit",
                start,
                vec![
                    ("address", address.to_string()),
                    ("result", "error".to_string()),
                    ("error", e.clone()),
                ],
            ),
        }
        result
    })
}

/// Open a connection to TypeDB with one or more addresses.
/// If private_addresses is non-null, it must have the same length as public_addresses
/// and is used as the driver's address translation map.
#[unsafe(no_mangle)]
pub extern "C" fn typedb_driver_open_addresses(
    public_addresses: *const *const c_char,
    private_addresses: *const *const c_char,
    address_count: usize,
    credentials: *const Credentials,
    options: *const DriverOptions,
    err_out: *mut *mut c_char,
) -> *mut TypeDBDriver {
    ffi_call(err_out, null_mut, || {
        let start = Instant::now();
        rust_debug_log(
            "ffi.typedb_driver_open_addresses.enter",
            vec![("address_count", address_count.to_string())],
        );

        let result = addresses_from_ffi(public_addresses, private_addresses, address_count)
            .and_then(|addresses| open_driver(addresses, credentials, options));
        match &result {
            Ok(_) => rust_debug_log_timed(
                "ffi.typedb_driver_open_addresses.exit",
                start,
                vec![
                    ("address_count", address_count.to_string()),
                    ("result", "ok".to_string()),
                ],
            ),
            Err(e) => rust_debug_log_timed(
                "ffi.typedb_driver_open_addresses.exit",
                start,
                vec![
                    ("address_count", address_count.to_string()),
                    ("result", "error".to_string()),
                    ("error", e.clone()),
                ],
            ),
        }
        result
    })
}

/// Check if driver is open.
#[unsafe(no_mangle)]
pub extern "C" fn typedb_driver_is_open(driver: *const TypeDBDriver) -> bool {
    ffi_call(
        null_mut(),
        || false,
        || Ok(deref(driver, "driver")?.is_open()),
    )
}

/// Return server version as JSON: {"distribution":"TypeDB CE","version":"3.12.1"}.
/// Caller must free with typedb_free_string.
#[unsafe(no_mangle)]
pub extern "C" fn typedb_driver_server_version(
    driver: *mut TypeDBDriver,
    err_out: *mut *mut c_char,
) -> *mut c_char {
    ffi_call(err_out, null_mut, || {
        let d = deref(driver, "driver")?;
        let version = d.server_version().map_err(|e| e.to_string())?;
        Ok(to_c_string(
            serde_json::to_string(&json!({
                "distribution": version.distribution(),
                "version": version.version(),
            }))
            .unwrap_or_else(|_| "{}".to_string()),
        ))
    })
}

/// Close and free the driver.
///
/// # Safety
///
/// `driver` must be null or a live pointer returned by a driver-open function
/// that has not already been closed or freed.
#[unsafe(no_mangle)]
pub unsafe extern "C" fn typedb_driver_close(driver: *mut TypeDBDriver) {
    ffi_call_unit(|| {
        if !driver.is_null() {
            let d = unsafe { Box::from_raw(driver) };
            let _ = d.force_close();
        }
    });
}

// ---------------------------------------------------------------------------
// Database management
// ---------------------------------------------------------------------------

/// List all databases. Returns a JSON array string: ["db1","db2",...].
/// Caller must free with typedb_free_string.
#[unsafe(no_mangle)]
pub extern "C" fn typedb_databases_all(
    driver: *mut TypeDBDriver,
    err_out: *mut *mut c_char,
) -> *mut c_char {
    ffi_call(err_out, null_mut, || {
        let d = deref(driver, "driver")?;
        let dbs = d.databases().all().map_err(|e| e.to_string())?;
        let names: Vec<String> = dbs.iter().map(|db| db.name().to_owned()).collect();
        Ok(to_c_string(
            serde_json::to_string(&names).unwrap_or_else(|_| "[]".to_string()),
        ))
    })
}

/// Create a database.
#[unsafe(no_mangle)]
pub extern "C" fn typedb_databases_create(
    driver: *mut TypeDBDriver,
    name: *const c_char,
    err_out: *mut *mut c_char,
) {
    ffi_call(
        err_out,
        || (),
        || {
            let d = deref(driver, "driver")?;
            d.databases()
                .create(c_str(name)?)
                .map_err(|e| e.to_string())
        },
    )
}

/// Check if a database exists.
#[unsafe(no_mangle)]
pub extern "C" fn typedb_databases_contains(
    driver: *mut TypeDBDriver,
    name: *const c_char,
    err_out: *mut *mut c_char,
) -> bool {
    ffi_call(
        err_out,
        || false,
        || {
            let d = deref(driver, "driver")?;
            d.databases()
                .contains(c_str(name)?)
                .map_err(|e| e.to_string())
        },
    )
}

/// Get database schema. Returns a TypeQL define query string.
/// Caller must free with typedb_free_string.
#[unsafe(no_mangle)]
pub extern "C" fn typedb_database_schema(
    driver: *mut TypeDBDriver,
    name: *const c_char,
    err_out: *mut *mut c_char,
) -> *mut c_char {
    ffi_call(err_out, null_mut, || {
        let d = deref(driver, "driver")?;
        let db = d.databases().get(c_str(name)?).map_err(|e| e.to_string())?;
        let schema = db.schema().map_err(|e| e.to_string())?;
        Ok(to_c_string(schema))
    })
}

/// Delete a database.
#[unsafe(no_mangle)]
pub extern "C" fn typedb_database_delete(
    driver: *mut TypeDBDriver,
    name: *const c_char,
    err_out: *mut *mut c_char,
) {
    ffi_call(
        err_out,
        || (),
        || {
            let d = deref(driver, "driver")?;
            let db = d.databases().get(c_str(name)?).map_err(|e| e.to_string())?;
            db.delete().map_err(|e| e.to_string())
        },
    )
}

// ---------------------------------------------------------------------------
// TransactionOptions
// ---------------------------------------------------------------------------

/// Create default transaction options. Caller must free with typedb_transaction_options_drop.
#[unsafe(no_mangle)]
pub extern "C" fn typedb_transaction_options_new() -> *mut TransactionOptions {
    ffi_call(null_mut(), null_mut, || {
        Ok(Box::into_raw(Box::new(TransactionOptions::new())))
    })
}

/// Set transaction timeout in milliseconds.
#[unsafe(no_mangle)]
pub extern "C" fn typedb_transaction_options_set_timeout(
    opts: *mut TransactionOptions,
    timeout_millis: i64,
) {
    ffi_call_unit(|| {
        if let Ok(o) = deref_mut(opts, "transaction options") {
            o.transaction_timeout = Some(Duration::from_millis(timeout_millis.max(0) as u64));
        }
    });
}

/// Set schema lock acquire timeout in milliseconds.
#[unsafe(no_mangle)]
pub extern "C" fn typedb_transaction_options_set_schema_lock_timeout(
    opts: *mut TransactionOptions,
    timeout_millis: i64,
) {
    ffi_call_unit(|| {
        if let Ok(o) = deref_mut(opts, "transaction options") {
            o.schema_lock_acquire_timeout =
                Some(Duration::from_millis(timeout_millis.max(0) as u64));
        }
    });
}

/// Free transaction options.
///
/// # Safety
///
/// `opts` must be null or a live pointer returned by
/// `typedb_transaction_options_new` that has not already been freed.
#[unsafe(no_mangle)]
pub unsafe extern "C" fn typedb_transaction_options_drop(opts: *mut TransactionOptions) {
    ffi_call_unit(|| {
        if !opts.is_null() {
            unsafe { drop(Box::from_raw(opts)) }
        }
    });
}

// ---------------------------------------------------------------------------
// QueryOptions
// ---------------------------------------------------------------------------

/// Create default query options. Caller must free with typedb_query_options_drop.
#[unsafe(no_mangle)]
pub extern "C" fn typedb_query_options_new() -> *mut QueryOptions {
    ffi_call(null_mut(), null_mut, || {
        Ok(Box::into_raw(Box::new(QueryOptions::new())))
    })
}

/// Set include_instance_types option.
#[unsafe(no_mangle)]
pub extern "C" fn typedb_query_options_set_include_instance_types(
    opts: *mut QueryOptions,
    include: bool,
) {
    ffi_call_unit(|| {
        if let Ok(o) = deref_mut(opts, "query options") {
            o.include_instance_types = Some(include);
        }
    });
}

/// Set prefetch_size option.
#[unsafe(no_mangle)]
pub extern "C" fn typedb_query_options_set_prefetch_size(opts: *mut QueryOptions, size: i64) {
    ffi_call_unit(|| {
        if let Ok(o) = deref_mut(opts, "query options") {
            o.prefetch_size = Some(size.max(0) as u64);
        }
    });
}

/// Free query options.
///
/// # Safety
///
/// `opts` must be null or a live pointer returned by
/// `typedb_query_options_new` that has not already been freed.
#[unsafe(no_mangle)]
pub unsafe extern "C" fn typedb_query_options_drop(opts: *mut QueryOptions) {
    ffi_call_unit(|| {
        if !opts.is_null() {
            unsafe { drop(Box::from_raw(opts)) }
        }
    });
}

// ---------------------------------------------------------------------------
// Transaction
// ---------------------------------------------------------------------------

fn to_transaction_type(t: i32) -> TransactionType {
    match t {
        0 => TransactionType::Read,
        1 => TransactionType::Write,
        2 => TransactionType::Schema,
        _ => TransactionType::Read,
    }
}

/// Open a transaction. Returns null on error.
/// transaction_type: 0=Read, 1=Write, 2=Schema
/// Caller must free with typedb_transaction_close.
#[unsafe(no_mangle)]
pub extern "C" fn typedb_transaction_open(
    driver: *mut TypeDBDriver,
    database_name: *const c_char,
    transaction_type: i32,
    options: *const TransactionOptions,
    err_out: *mut *mut c_char,
) -> *mut Transaction {
    ffi_call(err_out, null_mut, || {
        let start = Instant::now();
        let db_name = c_str(database_name)?;
        rust_debug_log(
            "ffi.typedb_transaction_open.enter",
            vec![
                ("db", db_name.to_string()),
                ("tx_type", transaction_type.to_string()),
            ],
        );

        let d = deref(driver, "driver")?;
        let tt = to_transaction_type(transaction_type);
        let opts = if options.is_null() {
            TransactionOptions::new()
        } else {
            *deref(options, "transaction options")?
        };
        let result = d
            .transaction_with_options(db_name, tt, opts)
            .map(|txn| Box::into_raw(Box::new(txn)))
            .map_err(|e| e.to_string());
        match &result {
            Ok(_) => rust_debug_log_timed(
                "ffi.typedb_transaction_open.exit",
                start,
                vec![
                    ("db", db_name.to_string()),
                    ("tx_type", transaction_type.to_string()),
                    ("result", "ok".to_string()),
                ],
            ),
            Err(e) => rust_debug_log_timed(
                "ffi.typedb_transaction_open.exit",
                start,
                vec![
                    ("db", db_name.to_string()),
                    ("tx_type", transaction_type.to_string()),
                    ("result", "error".to_string()),
                    ("error", e.clone()),
                ],
            ),
        }
        result
    })
}

/// Check if transaction is open.
#[unsafe(no_mangle)]
pub extern "C" fn typedb_transaction_is_open(txn: *const Transaction) -> bool {
    ffi_call(
        null_mut(),
        || false,
        || Ok(deref(txn, "transaction")?.is_open()),
    )
}

/// Execute a query and return results as a MessagePack-encoded byte buffer.
/// The buffer contains a msgpack array of maps (one per result row/document).
/// register_concepts controls whether entity/relation concepts in row results
/// are registered as reusable opaque handles (see the concept handle contract
/// in typedb_ffi.h). out_len receives the byte length of the buffer.
/// Returns null on error or for OK answers (out_len set to 0).
/// Caller must free with typedb_free_bytes.
#[unsafe(no_mangle)]
pub extern "C" fn typedb_transaction_query(
    txn: *mut Transaction,
    query: *const c_char,
    options: *const QueryOptions,
    register_concepts: bool,
    out_len: *mut usize,
    err_out: *mut *mut c_char,
) -> *mut u8 {
    ffi_call(err_out, null_mut, || {
        let query_text = c_str(query)?;
        run_logged_query(
            "typedb_transaction_query",
            txn,
            query_text,
            options,
            None,
            register_concepts,
            out_len,
        )
    })
}

/// Execute a query with given rows and return results as a MessagePack-encoded byte buffer.
#[unsafe(no_mangle)]
pub extern "C" fn typedb_transaction_query_with_rows(
    txn: *mut Transaction,
    query: *const c_char,
    options: *const QueryOptions,
    rows_json: *const c_char,
    register_concepts: bool,
    out_len: *mut usize,
    err_out: *mut *mut c_char,
) -> *mut u8 {
    ffi_call(err_out, null_mut, || {
        let query_text = c_str(query)?;
        let rows = parse_given_rows(c_str(rows_json)?)?;
        run_logged_query(
            "typedb_transaction_query_with_rows",
            txn,
            query_text,
            options,
            Some(rows),
            register_concepts,
            out_len,
        )
    })
}

/// Start a query and return a pull-based result stream.
/// The caller must release the stream with typedb_query_stream_drop.
///
/// # Safety
/// txn, query, options, and err_out must follow the pointer contracts in typedb_ffi.h.
#[unsafe(no_mangle)]
pub unsafe extern "C" fn typedb_transaction_query_stream_open(
    txn: *mut Transaction,
    query: *const c_char,
    options: *const QueryOptions,
    register_concepts: bool,
    err_out: *mut *mut c_char,
) -> *mut c_void {
    ffi_call(err_out, null_mut, || {
        let query_text = c_str(query)?;
        let answer = execute_query(txn, query_text, options, None)?;
        Ok(Box::into_raw(Box::new(QueryResultStream {
            answer,
            register_concepts,
            finished: false,
            pending: None,
        })) as *mut c_void)
    })
}

/// Pull at most max_rows results from a query stream as a MessagePack array.
/// The returned buffer must be released with typedb_free_bytes.
///
/// # Safety
/// stream and all output pointers must follow the pointer contracts in typedb_ffi.h.
#[unsafe(no_mangle)]
pub unsafe extern "C" fn typedb_query_stream_next(
    stream: *mut c_void,
    max_rows: usize,
    out_row_count: *mut usize,
    out_done: *mut bool,
    out_len: *mut usize,
    err_out: *mut *mut c_char,
) -> *mut u8 {
    if !out_row_count.is_null() {
        unsafe { *out_row_count = 0 };
    }
    if !out_done.is_null() {
        unsafe { *out_done = false };
    }
    ffi_call(err_out, null_mut, || {
        let stream = deref_mut(stream.cast::<QueryResultStream>(), "query result stream")?;
        let (bytes, row_count, done) = stream.next_chunk(max_rows)?;
        if !out_row_count.is_null() {
            unsafe { *out_row_count = row_count };
        }
        if !out_done.is_null() {
            unsafe { *out_done = done };
        }
        Ok(vec_to_raw(bytes, out_len))
    })
}

/// Release a pull-based query result stream.
///
/// # Safety
/// stream must be null or a pointer returned by typedb_transaction_query_stream_open.
#[unsafe(no_mangle)]
pub unsafe extern "C" fn typedb_query_stream_drop(stream: *mut c_void) {
    ffi_call_unit(|| {
        if !stream.is_null() {
            drop(unsafe { Box::from_raw(stream.cast::<QueryResultStream>()) });
        }
    });
}

/// Shared query execution path with enter/exit debug logging.
fn run_logged_query(
    event: &str,
    txn: *mut Transaction,
    query: &str,
    options: *const QueryOptions,
    rows: Option<GivenRows>,
    register_concepts: bool,
    out_len: *mut usize,
) -> Result<*mut u8, String> {
    let start = Instant::now();
    let op = query_op(query);
    let fingerprint = query_fingerprint(query);
    rust_debug_log(
        &format!("ffi.{}.enter", event),
        vec![
            ("query_op", op.clone()),
            ("query_fingerprint", fingerprint.clone()),
            ("query_len", query.len().to_string()),
        ],
    );

    let result = execute_query(txn, query, options, rows)
        .and_then(|answer| collect_answer_to_msgpack(answer, register_concepts));
    match &result {
        Ok(bytes) => rust_debug_log_timed(
            &format!("ffi.{}.exit", event),
            start,
            vec![
                ("query_op", op),
                ("query_fingerprint", fingerprint),
                ("query_len", query.len().to_string()),
                ("result", "ok".to_string()),
                ("bytes", bytes.len().to_string()),
            ],
        ),
        Err(e) => rust_debug_log_timed(
            &format!("ffi.{}.exit", event),
            start,
            vec![
                ("query_op", op),
                ("query_fingerprint", fingerprint),
                ("query_len", query.len().to_string()),
                ("result", "error".to_string()),
                ("error", e.clone()),
            ],
        ),
    }
    Ok(vec_to_raw(result?, out_len))
}

fn execute_query(
    txn: *mut Transaction,
    query: &str,
    options: *const QueryOptions,
    rows: Option<GivenRows>,
) -> Result<QueryAnswer, String> {
    let t = deref(txn, "transaction")?;
    let opts = if options.is_null() {
        QueryOptions::new()
    } else {
        *deref(options, "query options")?
    };
    let result = match rows {
        Some(rows) => t
            .query_with_options_and_rows(query, opts, Some(rows))
            .resolve(),
        None => t.query_with_options(query, opts).resolve(),
    };
    result.map_err(|e| e.to_string())
}

#[derive(Deserialize)]
struct FFIGivenRows {
    variables: Vec<String>,
    rows: Vec<Vec<FFIGivenValue>>,
}

#[derive(Deserialize)]
#[serde(tag = "type", content = "value", rename_all = "kebab-case")]
enum FFIGivenValue {
    Empty,
    Concept(String),
    Boolean(bool),
    Integer(i64),
    Double(f64),
    String(String),
    Decimal(String),
    Date(String),
    Datetime(String),
    DatetimeTz(String),
    Duration(String),
}

fn parse_given_rows(json: &str) -> Result<GivenRows, String> {
    let ffi_rows: FFIGivenRows =
        serde_json::from_str(json).map_err(|e| format!("invalid given rows JSON: {}", e))?;
    if ffi_rows.variables.is_empty() {
        return Err("given rows must declare at least one variable".to_string());
    }
    let mut rows = GivenRows::new(ffi_rows.variables, ffi_rows.rows.len());
    for row in ffi_rows.rows {
        let entries: Result<Vec<_>, _> = row.into_iter().map(given_value_to_entry).collect();
        rows.push_row(entries?).map_err(|e| e.to_string())?;
    }
    Ok(rows)
}

fn given_value_to_entry(value: FFIGivenValue) -> Result<GivenRowEntry, String> {
    match value {
        FFIGivenValue::Empty => Ok(GivenRowEntry::Empty),
        FFIGivenValue::Concept(handle) => {
            GivenRowEntry::try_from(get_registered_concept(&handle)?).map_err(|e| e.to_string())
        }
        FFIGivenValue::Boolean(value) => Ok(value.into()),
        FFIGivenValue::Integer(value) => Ok(value.into()),
        FFIGivenValue::Double(value) => Ok(value.into()),
        FFIGivenValue::String(value) => Ok(value.into()),
        FFIGivenValue::Decimal(value) => Decimal::from_str(&value)
            .map(Value::from)
            .map(GivenRowEntry::from)
            .map_err(|e| e.to_string()),
        FFIGivenValue::Date(value) => NaiveDate::parse_from_str(&value, "%Y-%m-%d")
            .map(Value::from)
            .map(GivenRowEntry::from)
            .map_err(|e| e.to_string()),
        FFIGivenValue::Datetime(value) => parse_naive_datetime(&value)
            .map(Value::from)
            .map(GivenRowEntry::from)
            .map_err(|e| e.to_string()),
        FFIGivenValue::DatetimeTz(value) => DateTime::parse_from_rfc3339(&value)
            .map(datetime_fixed_to_typedb)
            .map(Value::from)
            .map(GivenRowEntry::from)
            .map_err(|e| e.to_string()),
        FFIGivenValue::Duration(value) => TypeDBDuration::from_str(&value)
            .map(Value::from)
            .map(GivenRowEntry::from)
            .map_err(|_| format!("invalid duration: {}", value)),
    }
}

fn parse_naive_datetime(value: &str) -> Result<NaiveDateTime, chrono::ParseError> {
    NaiveDateTime::parse_from_str(value, "%Y-%m-%dT%H:%M:%S%.f")
        .or_else(|_| NaiveDateTime::parse_from_str(value, "%Y-%m-%d %H:%M:%S%.f"))
}

fn datetime_fixed_to_typedb(value: DateTime<FixedOffset>) -> DateTime<TimeZone> {
    let timezone = TimeZone::Fixed(*value.offset());
    value.with_timezone(&timezone)
}

/// Convert a Vec<u8> into a raw pointer + length for FFI.
/// Sets *out_len. Returns null for empty vecs (out_len = 0).
fn vec_to_raw(bytes: Vec<u8>, out_len: *mut usize) -> *mut u8 {
    if bytes.is_empty() {
        if !out_len.is_null() {
            unsafe {
                *out_len = 0;
            }
        }
        return null_mut();
    }
    let len = bytes.len();
    let mut boxed = bytes.into_boxed_slice();
    let ptr = boxed.as_mut_ptr();
    std::mem::forget(boxed);
    if !out_len.is_null() {
        unsafe {
            *out_len = len;
        }
    }
    ptr
}

/// Helper: collect query answer into msgpack bytes.
fn collect_answer_to_msgpack(
    answer: QueryAnswer,
    register_concepts: bool,
) -> Result<Vec<u8>, String> {
    let mut bytes = msgpack_array_buffer();
    let mut row_count = 0u32;

    match answer {
        QueryAnswer::Ok(_) => return Ok(vec![]),
        QueryAnswer::ConceptDocumentStream(_, stream) => {
            for doc_result in stream {
                append_document_msgpack(&mut bytes, doc_result.map_err(|e| e.to_string())?)?;
                row_count = increment_row_count(row_count)?;
            }
        }
        QueryAnswer::ConceptRowStream(_, stream) => {
            for row_result in stream {
                append_concept_row_msgpack(
                    &mut bytes,
                    row_result.map_err(|e| e.to_string())?,
                    register_concepts,
                )?;
                row_count = increment_row_count(row_count)?;
            }
        }
    }
    finish_msgpack_array(bytes, row_count)
}

impl QueryResultStream {
    fn next_chunk(&mut self, max_rows: usize) -> Result<(Vec<u8>, usize, bool), String> {
        if max_rows == 0 {
            return Err("query stream chunk size must be greater than zero".to_string());
        }
        if self.finished {
            return Ok((vec![], 0, true));
        }

        let mut bytes = msgpack_array_buffer();
        let mut row_count = 0u32;
        let chunk_limit = u32::try_from(max_rows).unwrap_or(u32::MAX);
        while row_count < chunk_limit {
            let Some(result) = self.take_next_result()? else {
                break;
            };
            append_query_stream_result(&mut bytes, result, self.register_concepts)?;
            row_count = increment_row_count(row_count)?;
        }

        if !self.finished && row_count == chunk_limit {
            self.pending = self.next_result()?;
        }

        let done = self.finished;
        let bytes = finish_msgpack_array(bytes, row_count)?;
        Ok((bytes, row_count as usize, done))
    }

    fn take_next_result(&mut self) -> Result<Option<QueryStreamResult>, String> {
        if let Some(result) = self.pending.take() {
            return Ok(Some(result));
        }
        self.next_result()
    }

    fn next_result(&mut self) -> Result<Option<QueryStreamResult>, String> {
        let result = match &mut self.answer {
            QueryAnswer::Ok(_) => None,
            QueryAnswer::ConceptDocumentStream(_, stream) => stream
                .next()
                .transpose()
                .map_err(|e| e.to_string())?
                .map(QueryStreamResult::Document),
            QueryAnswer::ConceptRowStream(_, stream) => stream
                .next()
                .transpose()
                .map_err(|e| e.to_string())?
                .map(QueryStreamResult::Row),
        };
        if result.is_none() {
            self.finished = true;
        }
        Ok(result)
    }
}

fn append_query_stream_result(
    bytes: &mut Vec<u8>,
    result: QueryStreamResult,
    register_concepts: bool,
) -> Result<(), String> {
    match result {
        QueryStreamResult::Document(document) => append_document_msgpack(bytes, document),
        QueryStreamResult::Row(row) => append_concept_row_msgpack(bytes, row, register_concepts),
    }
}

fn increment_row_count(row_count: u32) -> Result<u32, String> {
    row_count
        .checked_add(1)
        .ok_or_else(|| "query returned more than u32::MAX rows".to_string())
}

fn append_document_msgpack(bytes: &mut Vec<u8>, document: ConceptDocument) -> Result<(), String> {
    append_msgpack_row(bytes, &Document(&document))
}

fn append_concept_row_msgpack(
    bytes: &mut Vec<u8>,
    row: ConceptRow,
    register_concepts: bool,
) -> Result<(), String> {
    let col_names = row.get_column_names();
    if col_names.len() != row.row.len() {
        return Err(format!(
            "query row has {} columns but {} values",
            col_names.len(),
            row.row.len()
        ));
    }
    append_msgpack_map_header(bytes, col_names.len())?;
    for (name, concept) in col_names.iter().zip(&row.row) {
        append_msgpack_row(bytes, name)?;
        match concept {
            Some(concept) => append_msgpack_row(
                bytes,
                &RowConcept {
                    concept,
                    register_handle: register_concepts,
                },
            )?,
            None => append_msgpack_row(bytes, &Option::<()>::None)?,
        }
    }
    Ok(())
}

const MSGPACK_ARRAY32_HEADER_LEN: usize = 5;

fn msgpack_array_buffer() -> Vec<u8> {
    let mut bytes = Vec::with_capacity(4096);
    bytes.extend_from_slice(&[0xdd, 0, 0, 0, 0]);
    bytes
}

fn append_msgpack_row<T: Serialize + ?Sized>(bytes: &mut Vec<u8>, row: &T) -> Result<(), String> {
    rmp_serde::encode::write(bytes, row).map_err(|e| format!("msgpack encode error: {}", e))
}

fn append_msgpack_map_header(bytes: &mut Vec<u8>, field_count: usize) -> Result<(), String> {
    let field_count = u32::try_from(field_count)
        .map_err(|_| "query row returned more than u32::MAX fields".to_string())?;
    bytes.push(0xdf);
    bytes.extend_from_slice(&field_count.to_be_bytes());
    Ok(())
}

fn finish_msgpack_array(mut bytes: Vec<u8>, row_count: u32) -> Result<Vec<u8>, String> {
    if row_count == 0 {
        return Ok(vec![]);
    }
    bytes[1..MSGPACK_ARRAY32_HEADER_LEN].copy_from_slice(&row_count.to_be_bytes());
    Ok(bytes)
}

/// Commit the transaction and free it.
///
/// # Safety
///
/// `txn` must be null or a live transaction pointer that has not already been
/// ended. `err_out` must be null or point to writable storage for an error.
#[unsafe(no_mangle)]
pub unsafe extern "C" fn typedb_transaction_commit(
    txn: *mut Transaction,
    err_out: *mut *mut c_char,
) {
    ffi_call(
        err_out,
        || (),
        || {
            let start = Instant::now();
            rust_debug_log("ffi.typedb_transaction_commit.enter", vec![]);
            if txn.is_null() {
                rust_debug_log_timed(
                    "ffi.typedb_transaction_commit.exit",
                    start,
                    vec![("result", "nil_txn".to_string())],
                );
                return Ok(());
            }
            let t = unsafe { Box::from_raw(txn) };
            let result = t.commit().resolve().map_err(|e| e.to_string());
            match &result {
                Ok(()) => rust_debug_log_timed(
                    "ffi.typedb_transaction_commit.exit",
                    start,
                    vec![("result", "ok".to_string())],
                ),
                Err(e) => rust_debug_log_timed(
                    "ffi.typedb_transaction_commit.exit",
                    start,
                    vec![("result", "error".to_string()), ("error", e.clone())],
                ),
            }
            result
        },
    )
}

/// Rollback the transaction.
///
/// # Safety
///
/// `txn` must be null or point to a live transaction for the duration of this
/// call. `err_out` must be null or point to writable storage for an error.
#[unsafe(no_mangle)]
pub unsafe extern "C" fn typedb_transaction_rollback(
    txn: *const Transaction,
    err_out: *mut *mut c_char,
) {
    ffi_call(
        err_out,
        || (),
        || {
            let start = Instant::now();
            rust_debug_log("ffi.typedb_transaction_rollback.enter", vec![]);
            if txn.is_null() {
                rust_debug_log_timed(
                    "ffi.typedb_transaction_rollback.exit",
                    start,
                    vec![("result", "nil_txn".to_string())],
                );
                return Ok(());
            }
            let t = unsafe { &*txn };
            let result = t.rollback().resolve().map_err(|e| e.to_string());
            match &result {
                Ok(()) => rust_debug_log_timed(
                    "ffi.typedb_transaction_rollback.exit",
                    start,
                    vec![("result", "ok".to_string())],
                ),
                Err(e) => rust_debug_log_timed(
                    "ffi.typedb_transaction_rollback.exit",
                    start,
                    vec![("result", "error".to_string()), ("error", e.clone())],
                ),
            }
            result
        },
    )
}

/// Close and free the transaction without committing.
///
/// # Safety
///
/// `txn` must be null or a live transaction pointer that has not already been
/// ended. `err_out` must be null or point to writable storage for an error.
#[unsafe(no_mangle)]
pub unsafe extern "C" fn typedb_transaction_close(
    txn: *mut Transaction,
    err_out: *mut *mut c_char,
) {
    ffi_call(
        err_out,
        || (),
        || {
            let start = Instant::now();
            rust_debug_log("ffi.typedb_transaction_close.enter", vec![]);
            if txn.is_null() {
                rust_debug_log_timed(
                    "ffi.typedb_transaction_close.exit",
                    start,
                    vec![("result", "nil_txn".to_string())],
                );
                return Ok(());
            }

            let txn = unsafe { Box::from_raw(txn) };
            let result = txn.close().resolve().map_err(|e| e.to_string());
            match &result {
                Ok(()) => rust_debug_log_timed(
                    "ffi.typedb_transaction_close.exit",
                    start,
                    vec![("result", "ok".to_string())],
                ),
                Err(e) => rust_debug_log_timed(
                    "ffi.typedb_transaction_close.exit",
                    start,
                    vec![("result", "error".to_string()), ("error", e.clone())],
                ),
            }
            result
        },
    )
}

/// Drop the transaction locally without waiting for the checked close result.
///
/// # Safety
///
/// `txn` must be null or a live transaction pointer that has not already been
/// ended or freed.
#[unsafe(no_mangle)]
pub unsafe extern "C" fn typedb_transaction_drop(txn: *mut Transaction) {
    ffi_call_unit(|| {
        let start = Instant::now();
        rust_debug_log("ffi.typedb_transaction_drop.enter", vec![]);
        if txn.is_null() {
            rust_debug_log_timed(
                "ffi.typedb_transaction_drop.exit",
                start,
                vec![("result", "nil_txn".to_string())],
            );
            return;
        }

        drop(unsafe { Box::from_raw(txn) });
        rust_debug_log_timed(
            "ffi.typedb_transaction_drop.exit",
            start,
            vec![("result", "ok".to_string())],
        );
    });
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::sync::Arc;
    use typedb_driver::{
        Result as TypeDBResult,
        answer::{
            QueryType, concept_document::ConceptDocumentHeader, concept_row::ConceptRowHeader,
        },
        box_stream,
    };

    fn take_error(err: *mut c_char) -> String {
        assert!(!err.is_null(), "expected an error message");
        let msg = unsafe { CStr::from_ptr(err) }
            .to_string_lossy()
            .into_owned();
        unsafe { typedb_free_string(err) };
        msg
    }

    #[test]
    fn set_error_escapes_interior_nul() {
        let mut err: *mut c_char = null_mut();
        set_error(&mut err, "broken\0commit");
        let msg = take_error(err);
        assert_eq!(msg, "broken\\0commit");
    }

    #[test]
    fn to_c_string_escapes_interior_nul() {
        let ptr = to_c_string("a\0b".to_string());
        let msg = take_error(ptr);
        assert_eq!(msg, "a\\0b");
    }

    #[test]
    fn c_str_rejects_null_pointer() {
        let err = c_str(std::ptr::null()).unwrap_err();
        assert!(err.contains("null string pointer"), "got: {}", err);
    }

    #[test]
    fn c_str_rejects_invalid_utf8() {
        let bytes = CString::new(vec![0xffu8, 0xfe]).unwrap();
        let err = c_str(bytes.as_ptr()).unwrap_err();
        assert!(err.contains("invalid UTF-8"), "got: {}", err);
    }

    #[test]
    fn ffi_call_contains_panics() {
        let mut err: *mut c_char = null_mut();
        let value = ffi_call(
            &mut err,
            || 7i32,
            || -> Result<i32, String> { panic!("boom {}", 42) },
        );
        assert_eq!(value, 7);
        let msg = take_error(err);
        assert!(msg.contains("panic in typedb-go-ffi"), "got: {}", msg);
        assert!(msg.contains("boom 42"), "got: {}", msg);
    }

    #[test]
    fn ffi_call_unit_contains_panics() {
        ffi_call_unit(|| panic!("contained"));
    }

    #[test]
    fn registry_drop_removes_handle() {
        // A raw Concept is not constructible here; exercise the registry map
        // through the handle-string API with the drop entry points.
        assert!(get_registered_concept("concept-does-not-exist").is_err());
        let handle = CString::new("concept-does-not-exist").unwrap();
        typedb_concept_drop(handle.as_ptr()); // unknown handle: no panic
        typedb_concept_drop(std::ptr::null()); // null handle: no panic
        typedb_concepts_drop_all();
        assert!(registry_lock().is_empty());
    }

    #[test]
    fn null_transaction_query_returns_error_not_crash() {
        let query = CString::new("match $x isa thing;").unwrap();
        let mut out_len: usize = 0;
        let mut err: *mut c_char = null_mut();
        let buf = typedb_transaction_query(
            null_mut(),
            query.as_ptr(),
            std::ptr::null(),
            false,
            &mut out_len,
            &mut err,
        );
        assert!(buf.is_null());
        let msg = take_error(err);
        assert!(msg.contains("null transaction pointer"), "got: {}", msg);
    }

    #[test]
    fn null_query_stream_operations_return_error_not_crash() {
        let query = CString::new("match $x isa thing;").unwrap();
        let mut err: *mut c_char = null_mut();
        let stream = unsafe {
            typedb_transaction_query_stream_open(
                null_mut(),
                query.as_ptr(),
                std::ptr::null(),
                false,
                &mut err,
            )
        };
        assert!(stream.is_null());
        let msg = take_error(err);
        assert!(msg.contains("null transaction pointer"), "got: {}", msg);

        let mut row_count = 0;
        let mut done = false;
        let mut out_len = 0;
        let mut err: *mut c_char = null_mut();
        let bytes = unsafe {
            typedb_query_stream_next(
                null_mut(),
                1,
                &mut row_count,
                &mut done,
                &mut out_len,
                &mut err,
            )
        };
        assert!(bytes.is_null());
        let msg = take_error(err);
        assert!(
            msg.contains("null query result stream pointer"),
            "got: {}",
            msg
        );
        unsafe { typedb_query_stream_drop(null_mut()) };
    }

    #[test]
    fn null_driver_database_ops_return_error_not_crash() {
        let name = CString::new("db").unwrap();
        let mut err: *mut c_char = null_mut();
        typedb_databases_create(null_mut(), name.as_ptr(), &mut err);
        let msg = take_error(err);
        assert!(msg.contains("null driver pointer"), "got: {}", msg);

        let mut err: *mut c_char = null_mut();
        let version = typedb_driver_server_version(null_mut(), &mut err);
        assert!(version.is_null());
        let msg = take_error(err);
        assert!(msg.contains("null driver pointer"), "got: {}", msg);
    }

    #[test]
    fn credentials_new_rejects_invalid_utf8() {
        let bad = CString::new(vec![0xffu8]).unwrap();
        let pass = CString::new("password").unwrap();
        let mut err: *mut c_char = null_mut();
        let creds = typedb_credentials_new(bad.as_ptr(), pass.as_ptr(), &mut err);
        assert!(creds.is_null());
        let msg = take_error(err);
        assert!(msg.contains("invalid UTF-8"), "got: {}", msg);
    }

    #[test]
    fn init_logging_is_idempotent_and_contained() {
        typedb_init_logging();
        typedb_init_logging();
    }

    #[test]
    fn msgpack_array_encodes_rows_incrementally() {
        let rows = vec![json!({"name": "Alice"}), json!({"name": "Bob"})];
        let mut bytes = msgpack_array_buffer();
        for row in &rows {
            append_msgpack_row(&mut bytes, row).unwrap();
        }
        let bytes = finish_msgpack_array(bytes, rows.len() as u32).unwrap();
        let decoded: Vec<serde_json::Value> = rmp_serde::from_slice(&bytes).unwrap();
        assert_eq!(decoded, rows);
    }

    #[test]
    fn msgpack_array_keeps_empty_results_empty() {
        let bytes = finish_msgpack_array(msgpack_array_buffer(), 0).unwrap();
        assert!(bytes.is_empty());
    }

    #[test]
    fn msgpack_map_encodes_fields_incrementally() {
        let mut bytes = Vec::new();
        append_msgpack_map_header(&mut bytes, 2).unwrap();
        append_msgpack_row(&mut bytes, "name").unwrap();
        append_msgpack_row(&mut bytes, "Alice").unwrap();
        append_msgpack_row(&mut bytes, "age").unwrap();
        append_msgpack_row(&mut bytes, &30).unwrap();

        let decoded: HashMap<String, serde_json::Value> = rmp_serde::from_slice(&bytes).unwrap();
        assert_eq!(decoded["name"], json!("Alice"));
        assert_eq!(decoded["age"], json!(30));
    }

    #[test]
    fn document_serializer_matches_driver_json() {
        let header = Arc::new(ConceptDocumentHeader {
            query_type: QueryType::ReadQuery,
        });
        let root = Node::Map(HashMap::from([
            (
                "integer".to_string(),
                Node::Leaf(Some(Leaf::Concept(Concept::Value(Value::Integer(42))))),
            ),
            (
                "datetime".to_string(),
                Node::Leaf(Some(Leaf::Concept(Concept::Value(Value::Datetime(
                    NaiveDateTime::parse_from_str(
                        "2024-06-01T13:04:05.123456789",
                        "%Y-%m-%dT%H:%M:%S%.f",
                    )
                    .unwrap(),
                ))))),
            ),
            (
                "items".to_string(),
                Node::List(vec![
                    Node::Leaf(Some(Leaf::Concept(Concept::Value(Value::String(
                        "value".to_string(),
                    ))))),
                    Node::Leaf(None),
                ]),
            ),
            (
                "value-type".to_string(),
                Node::Leaf(Some(Leaf::ValueType(ValueType::DatetimeTZ))),
            ),
        ]));
        let document = ConceptDocument::new(header, Some(root));

        let expected_bytes = rmp_serde::to_vec(&document.clone().into_json()).unwrap();
        let expected: serde_json::Value = rmp_serde::from_slice(&expected_bytes).unwrap();
        let mut actual_bytes = Vec::new();
        append_document_msgpack(&mut actual_bytes, document).unwrap();
        let actual: serde_json::Value = rmp_serde::from_slice(&actual_bytes).unwrap();

        assert_eq!(actual, expected);
    }

    #[test]
    fn query_result_stream_splits_rows_into_bounded_chunks() {
        let header = Arc::new(ConceptRowHeader {
            column_names: vec!["value".to_string()],
            query_type: QueryType::ReadQuery,
            query_structure: None,
        });
        let mut rows: Vec<TypeDBResult<ConceptRow>> = Vec::with_capacity(130);
        for _ in 0..130 {
            rows.push(Ok(ConceptRow::new(header.clone(), vec![None], None)));
        }
        let answer = QueryAnswer::ConceptRowStream(header, box_stream(rows.into_iter()));
        let mut stream = QueryResultStream {
            answer,
            register_concepts: false,
            finished: false,
            pending: None,
        };

        let (first, first_count, first_done) = stream.next_chunk(128).unwrap();
        let first_rows: Vec<HashMap<String, serde_json::Value>> =
            rmp_serde::from_slice(&first).unwrap();
        assert_eq!(first_count, 128);
        assert_eq!(first_rows.len(), 128);
        assert!(!first_done);

        let (second, second_count, second_done) = stream.next_chunk(128).unwrap();
        let second_rows: Vec<HashMap<String, serde_json::Value>> =
            rmp_serde::from_slice(&second).unwrap();
        assert_eq!(second_count, 2);
        assert_eq!(second_rows.len(), 2);
        assert!(second_done);
    }

    #[test]
    fn query_result_stream_marks_an_exact_chunk_done() {
        let header = Arc::new(ConceptRowHeader {
            column_names: vec!["value".to_string()],
            query_type: QueryType::ReadQuery,
            query_structure: None,
        });
        let mut rows: Vec<TypeDBResult<ConceptRow>> = Vec::with_capacity(128);
        for _ in 0..128 {
            rows.push(Ok(ConceptRow::new(header.clone(), vec![None], None)));
        }
        let answer = QueryAnswer::ConceptRowStream(header, box_stream(rows.into_iter()));
        let mut stream = QueryResultStream {
            answer,
            register_concepts: false,
            finished: false,
            pending: None,
        };

        let (_, row_count, done) = stream.next_chunk(128).unwrap();
        assert_eq!(row_count, 128);
        assert!(done);
    }
}
