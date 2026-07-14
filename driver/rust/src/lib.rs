// typedb-go-ffi: C FFI wrapper around typedb-driver for Go CGo bindings.
//
// Design: All functions use opaque pointers. Errors are returned via
// out-parameters (*mut *mut c_char) that receive error message strings.
// Query results are returned as a single MessagePack-encoded byte buffer
// via typedb_transaction_query().
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
use std::ffi::{c_char, CStr, CString};
use std::panic::{catch_unwind, AssertUnwindSafe};
use std::ptr::null_mut;
use std::str::FromStr;
use std::sync::{
    atomic::{AtomicU64, Ordering},
    Mutex, MutexGuard, Once, OnceLock,
};
use std::time::{Duration, Instant};

use chrono::{DateTime, FixedOffset, NaiveDate, NaiveDateTime};
use serde::Deserialize;
use serde_json::json;

use typedb_driver::{
    answer::QueryAnswer,
    concept::{
        value::{Decimal, Duration as TypeDBDuration, TimeZone, ValueType},
        Concept, Value,
    },
    given::{GivenRowEntry, GivenRows},
    Addresses, Credentials, DriverOptions, DriverTlsConfig, Promise, QueryOptions, Transaction,
    TransactionOptions, TransactionType, TypeDBDriver,
};

static NEXT_CONCEPT_HANDLE: AtomicU64 = AtomicU64::new(1);
static CONCEPT_REGISTRY: OnceLock<Mutex<HashMap<String, Concept>>> = OnceLock::new();

/// Convert a TypeDB Concept to a clean JSON value instead of Rust Debug strings.
///
/// When register_handle is true, entity/relation instances are registered in
/// the process-global concept registry and the resulting object carries a
/// "_concept_handle" key. The caller owns that handle and must release it via
/// typedb_concept_drop / typedb_concepts_drop_all.
fn concept_to_json(concept: &Concept, register_handle: bool) -> serde_json::Value {
    // Attributes & Values → extract the actual typed value
    if let Some(value) = concept.try_get_value() {
        return match value.get_type() {
            ValueType::Boolean => json!(value.get_boolean().unwrap()),
            ValueType::Integer => json!(value.get_integer().unwrap()),
            ValueType::Double => json!(value.get_double().unwrap()),
            ValueType::String => json!(value.get_string().unwrap()),
            ValueType::Decimal => json!(format!("{}", value.get_decimal().unwrap())),
            ValueType::Date => json!(format!("{}", value.get_date().unwrap())),
            ValueType::Datetime => json!(format!("{}", value.get_datetime().unwrap())),
            ValueType::DatetimeTZ => json!(format!("{}", value.get_datetime_tz().unwrap())),
            ValueType::Duration => json!(format!("{}", value.get_duration().unwrap())),
            ValueType::Struct(_) => json!(format!("{:?}", value.get_struct().unwrap())),
        };
    }
    // Entity/Relation instances → structured object with kind, type, iid
    if let Some(iid) = concept.try_get_iid() {
        let mut obj = serde_json::Map::new();
        obj.insert(
            "_kind".into(),
            json!(concept.get_category().name().to_lowercase()),
        );
        obj.insert("_type".into(), json!(concept.get_label()));
        obj.insert("_iid".into(), json!(format!("{}", iid)));
        if register_handle {
            obj.insert("_concept_handle".into(), json!(register_concept(concept)));
        }
        return serde_json::Value::Object(obj);
    }
    // Types (EntityType, RelationType, etc.)
    if concept.is_type() {
        let mut obj = serde_json::Map::new();
        obj.insert(
            "_kind".into(),
            json!(concept.get_category().name().to_lowercase()),
        );
        obj.insert("_label".into(), json!(concept.get_label()));
        return serde_json::Value::Object(obj);
    }
    // Fallback
    json!(format!("{:?}", concept))
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
#[no_mangle]
pub extern "C" fn typedb_concept_drop(handle: *const c_char) {
    ffi_call_unit(|| {
        if let Ok(handle) = c_str(handle) {
            registry_lock().remove(handle);
        }
    });
}

/// Release every concept handle registered by this process.
#[no_mangle]
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
    for (public, private) in public.into_iter().zip(private.into_iter()) {
        translation.insert(public, private);
    }
    Addresses::try_from_translation_str(translation).map_err(|e| e.to_string())
}

/// Free a string returned by this library.
#[no_mangle]
pub extern "C" fn typedb_free_string(s: *mut c_char) {
    ffi_call_unit(|| {
        if !s.is_null() {
            unsafe { drop(CString::from_raw(s)) }
        }
    });
}

/// Free a byte buffer returned by query functions.
/// The caller must pass both the pointer and the length that were returned.
#[no_mangle]
pub extern "C" fn typedb_free_bytes(ptr: *mut u8, len: usize) {
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
#[no_mangle]
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
#[no_mangle]
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
#[no_mangle]
pub extern "C" fn typedb_credentials_drop(creds: *mut Credentials) {
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
#[no_mangle]
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
#[no_mangle]
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
#[no_mangle]
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
#[no_mangle]
pub extern "C" fn typedb_driver_options_drop(opts: *mut DriverOptions) {
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
#[no_mangle]
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
#[no_mangle]
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
#[no_mangle]
pub extern "C" fn typedb_driver_is_open(driver: *const TypeDBDriver) -> bool {
    ffi_call(
        null_mut(),
        || false,
        || Ok(deref(driver, "driver")?.is_open()),
    )
}

/// Return server version as JSON: {"distribution":"TypeDB CE","version":"3.12.1"}.
/// Caller must free with typedb_free_string.
#[no_mangle]
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
#[no_mangle]
pub extern "C" fn typedb_driver_close(driver: *mut TypeDBDriver) {
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
#[no_mangle]
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
#[no_mangle]
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
#[no_mangle]
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
#[no_mangle]
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
#[no_mangle]
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
#[no_mangle]
pub extern "C" fn typedb_transaction_options_new() -> *mut TransactionOptions {
    ffi_call(null_mut(), null_mut, || {
        Ok(Box::into_raw(Box::new(TransactionOptions::new())))
    })
}

/// Set transaction timeout in milliseconds.
#[no_mangle]
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
#[no_mangle]
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
#[no_mangle]
pub extern "C" fn typedb_transaction_options_drop(opts: *mut TransactionOptions) {
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
#[no_mangle]
pub extern "C" fn typedb_query_options_new() -> *mut QueryOptions {
    ffi_call(null_mut(), null_mut, || {
        Ok(Box::into_raw(Box::new(QueryOptions::new())))
    })
}

/// Set include_instance_types option.
#[no_mangle]
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
#[no_mangle]
pub extern "C" fn typedb_query_options_set_prefetch_size(opts: *mut QueryOptions, size: i64) {
    ffi_call_unit(|| {
        if let Ok(o) = deref_mut(opts, "query options") {
            o.prefetch_size = Some(size.max(0) as u64);
        }
    });
}

/// Free query options.
#[no_mangle]
pub extern "C" fn typedb_query_options_drop(opts: *mut QueryOptions) {
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
#[no_mangle]
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
#[no_mangle]
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
#[no_mangle]
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
#[no_mangle]
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
    let rows = collect_answer_to_values(answer, register_concepts)?;
    if rows.is_empty() {
        return Ok(vec![]);
    }
    rmp_serde::to_vec(&rows).map_err(|e| format!("msgpack encode error: {}", e))
}

/// Helper: collect query answer into Vec<serde_json::Value>.
fn collect_answer_to_values(
    answer: QueryAnswer,
    register_concepts: bool,
) -> Result<Vec<serde_json::Value>, String> {
    if answer.is_ok() {
        return Ok(vec![]);
    }

    if answer.is_document_stream() {
        let mut docs: Vec<serde_json::Value> = Vec::new();
        for doc_result in answer.into_documents() {
            match doc_result {
                Ok(doc) => {
                    // Structural conversion via serde: single traversal, no
                    // intermediate JSON string round trip.
                    let val = serde_json::to_value(doc.into_json())
                        .map_err(|e| format!("document conversion error: {}", e))?;
                    docs.push(val);
                }
                Err(e) => return Err(e.to_string()),
            }
        }
        return Ok(docs);
    }

    if answer.is_row_stream() {
        let mut docs: Vec<serde_json::Value> = Vec::new();
        for row_result in answer.into_rows() {
            match row_result {
                Ok(row) => {
                    let col_names = row.get_column_names().to_vec();
                    let mut obj = serde_json::Map::new();
                    for (i, name) in col_names.iter().enumerate() {
                        match row.get_index(i) {
                            Ok(Some(concept)) => {
                                obj.insert(
                                    name.clone(),
                                    concept_to_json(concept, register_concepts),
                                );
                            }
                            Ok(None) => {
                                obj.insert(name.clone(), serde_json::Value::Null);
                            }
                            Err(e) => return Err(e.to_string()),
                        }
                    }
                    docs.push(serde_json::Value::Object(obj));
                }
                Err(e) => return Err(e.to_string()),
            }
        }
        return Ok(docs);
    }

    Ok(vec![])
}

/// Commit the transaction and free it.
#[no_mangle]
pub extern "C" fn typedb_transaction_commit(txn: *mut Transaction, err_out: *mut *mut c_char) {
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
#[no_mangle]
pub extern "C" fn typedb_transaction_rollback(txn: *const Transaction, err_out: *mut *mut c_char) {
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
#[no_mangle]
pub extern "C" fn typedb_transaction_close(txn: *mut Transaction, err_out: *mut *mut c_char) {
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
#[no_mangle]
pub extern "C" fn typedb_transaction_drop(txn: *mut Transaction) {
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

    fn take_error(err: *mut c_char) -> String {
        assert!(!err.is_null(), "expected an error message");
        let msg = unsafe { CStr::from_ptr(err) }
            .to_string_lossy()
            .into_owned();
        typedb_free_string(err);
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
}
