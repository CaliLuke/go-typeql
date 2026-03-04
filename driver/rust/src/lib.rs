// typedb-go-ffi: C FFI wrapper around typedb-driver for Go CGo bindings.
//
// Design: All functions use opaque pointers. Errors are returned via
// out-parameters (*mut *mut c_char) that receive error message strings.
// Query results are returned as a single MessagePack-encoded byte buffer
// via typedb_transaction_query().

use std::ffi::{c_char, CStr, CString};
use std::ptr::null_mut;
use std::sync::{Once, OnceLock};
use std::time::{Duration, Instant};

use serde_json::json;

use typedb_driver::{
    answer::QueryAnswer,
    concept::{Concept, value::ValueType},
    Credentials, DriverOptions, Promise, QueryOptions, Transaction, TransactionOptions,
    TransactionType, TypeDBDriver,
};

/// Convert a TypeDB Concept to a clean JSON value instead of Rust Debug strings.
fn concept_to_json(concept: &Concept) -> serde_json::Value {
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
        obj.insert("_kind".into(), json!(concept.get_category().name().to_lowercase()));
        obj.insert("_type".into(), json!(concept.get_label()));
        obj.insert("_iid".into(), json!(format!("{}", iid)));
        return serde_json::Value::Object(obj);
    }
    // Types (EntityType, RelationType, etc.)
    if concept.is_type() {
        let mut obj = serde_json::Map::new();
        obj.insert("_kind".into(), json!(concept.get_category().name().to_lowercase()));
        obj.insert("_label".into(), json!(concept.get_label()));
        return serde_json::Value::Object(obj);
    }
    // Fallback
    json!(format!("{:?}", concept))
}

// ---------------------------------------------------------------------------
// Error handling via out-parameters
// ---------------------------------------------------------------------------

/// Sets error message via out-parameter.
/// If err_out is null, the error is silently dropped.
fn set_error(err_out: *mut *mut c_char, err: impl std::fmt::Display) {
    if err_out.is_null() {
        return;
    }
    let msg = err.to_string();
    match CString::new(msg) {
        Ok(cstr) => unsafe { *err_out = cstr.into_raw() },
        Err(_) => unsafe { *err_out = null_mut() },
    }
}

// ---------------------------------------------------------------------------
// String helpers
// ---------------------------------------------------------------------------

fn c_str(ptr: *const c_char) -> &'static str {
    assert!(!ptr.is_null());
    unsafe { CStr::from_ptr(ptr).to_str().unwrap_or("") }
}

fn to_c_string(s: String) -> *mut c_char {
    CString::new(s).unwrap_or_default().into_raw()
}

/// Free a string returned by this library.
#[no_mangle]
pub extern "C" fn typedb_free_string(s: *mut c_char) {
    if !s.is_null() {
        unsafe { drop(CString::from_raw(s)) }
    }
}

/// Free a byte buffer returned by query functions.
/// The caller must pass both the pointer and the length that were returned.
#[no_mangle]
pub extern "C" fn typedb_free_bytes(ptr: *mut u8, len: usize) {
    if !ptr.is_null() && len > 0 {
        unsafe {
            drop(Vec::from_raw_parts(ptr, len, len));
        }
    }
}

// ---------------------------------------------------------------------------
// Logging
// ---------------------------------------------------------------------------

/// Initialize TypeDB driver logging. Call once at startup.
#[no_mangle]
pub extern "C" fn typedb_init_logging() {
    static INIT: Once = Once::new();
    INIT.call_once(|| {});
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
        "match" | "insert" | "delete" | "update" | "define" | "undefine" | "fetch" | "reduce" => first,
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

/// Create credentials. Caller must free with typedb_credentials_drop.
#[no_mangle]
pub extern "C" fn typedb_credentials_new(
    username: *const c_char,
    password: *const c_char,
) -> *mut Credentials {
    Box::into_raw(Box::new(Credentials::new(c_str(username), c_str(password))))
}

/// Free credentials.
#[no_mangle]
pub extern "C" fn typedb_credentials_drop(creds: *mut Credentials) {
    if !creds.is_null() {
        unsafe { drop(Box::from_raw(creds)) }
    }
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
    let ca_path = if tls_root_ca.is_null() {
        None
    } else {
        Some(std::path::Path::new(c_str(tls_root_ca)))
    };
    match DriverOptions::new(is_tls_enabled, ca_path) {
        Ok(opts) => Box::into_raw(Box::new(opts)),
        Err(e) => {
            set_error(err_out, e);
            null_mut()
        }
    }
}

/// Free driver options.
#[no_mangle]
pub extern "C" fn typedb_driver_options_drop(opts: *mut DriverOptions) {
    if !opts.is_null() {
        unsafe { drop(Box::from_raw(opts)) }
    }
}

// ---------------------------------------------------------------------------
// Driver (connection)
// ---------------------------------------------------------------------------

/// Open a connection to TypeDB. Returns null on error.
/// Caller must free with typedb_driver_close.
#[no_mangle]
pub extern "C" fn typedb_driver_open(
    address: *const c_char,
    credentials: *const Credentials,
    options: *const DriverOptions,
    err_out: *mut *mut c_char,
) -> *mut TypeDBDriver {
    let start = Instant::now();
    let address_value = c_str(address).to_string();
    rust_debug_log(
        "ffi.typedb_driver_open.enter",
        vec![("address", address_value.clone())],
    );

    let creds = unsafe { &*credentials };
    let opts = unsafe { &*options };
    match TypeDBDriver::new_with_description(c_str(address), creds.clone(), opts.clone(), "go") {
        Ok(driver) => {
            rust_debug_log_timed(
                "ffi.typedb_driver_open.exit",
                start,
                vec![("address", address_value), ("result", "ok".to_string())],
            );
            Box::into_raw(Box::new(driver))
        }
        Err(e) => {
            rust_debug_log_timed(
                "ffi.typedb_driver_open.exit",
                start,
                vec![
                    ("address", address_value),
                    ("result", "error".to_string()),
                    ("error", e.to_string()),
                ],
            );
            set_error(err_out, e);
            null_mut()
        }
    }
}

/// Check if driver is open.
#[no_mangle]
pub extern "C" fn typedb_driver_is_open(driver: *const TypeDBDriver) -> bool {
    if driver.is_null() {
        return false;
    }
    unsafe { &*driver }.is_open()
}

/// Close and free the driver.
#[no_mangle]
pub extern "C" fn typedb_driver_close(driver: *mut TypeDBDriver) {
    if !driver.is_null() {
        let d = unsafe { Box::from_raw(driver) };
        let _ = d.force_close();
    }
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
    let d = unsafe { &*driver };
    match d.databases().all() {
        Ok(dbs) => {
            let names: Vec<String> = dbs.iter().map(|db| db.name().to_owned()).collect();
            to_c_string(serde_json::to_string(&names).unwrap_or_else(|_| "[]".to_string()))
        }
        Err(e) => {
            set_error(err_out, e);
            null_mut()
        }
    }
}

/// Create a database.
#[no_mangle]
pub extern "C" fn typedb_databases_create(
    driver: *mut TypeDBDriver,
    name: *const c_char,
    err_out: *mut *mut c_char,
) {
    let d = unsafe { &*driver };
    if let Err(e) = d.databases().create(c_str(name)) {
        set_error(err_out, e);
    }
}

/// Check if a database exists.
#[no_mangle]
pub extern "C" fn typedb_databases_contains(
    driver: *mut TypeDBDriver,
    name: *const c_char,
    err_out: *mut *mut c_char,
) -> bool {
    let d = unsafe { &*driver };
    match d.databases().contains(c_str(name)) {
        Ok(v) => v,
        Err(e) => {
            set_error(err_out, e);
            false
        }
    }
}

/// Get database schema. Returns a TypeQL define query string.
/// Caller must free with typedb_free_string.
#[no_mangle]
pub extern "C" fn typedb_database_schema(
    driver: *mut TypeDBDriver,
    name: *const c_char,
    err_out: *mut *mut c_char,
) -> *mut c_char {
    let d = unsafe { &*driver };
    match d.databases().get(c_str(name)) {
        Ok(db) => match db.schema() {
            Ok(schema) => to_c_string(schema),
            Err(e) => {
                set_error(err_out, e);
                null_mut()
            }
        },
        Err(e) => {
            set_error(err_out, e);
            null_mut()
        }
    }
}

/// Delete a database.
#[no_mangle]
pub extern "C" fn typedb_database_delete(
    driver: *mut TypeDBDriver,
    name: *const c_char,
    err_out: *mut *mut c_char,
) {
    let d = unsafe { &*driver };
    match d.databases().get(c_str(name)) {
        Ok(db) => {
            if let Err(e) = db.delete() {
                set_error(err_out, e);
            }
        }
        Err(e) => {
            set_error(err_out, e);
        }
    }
}

// ---------------------------------------------------------------------------
// TransactionOptions
// ---------------------------------------------------------------------------

/// Create default transaction options. Caller must free with typedb_transaction_options_drop.
#[no_mangle]
pub extern "C" fn typedb_transaction_options_new() -> *mut TransactionOptions {
    Box::into_raw(Box::new(TransactionOptions::new()))
}

/// Set transaction timeout in milliseconds.
#[no_mangle]
pub extern "C" fn typedb_transaction_options_set_timeout(
    opts: *mut TransactionOptions,
    timeout_millis: i64,
) {
    let o = unsafe { &mut *opts };
    o.transaction_timeout = Some(Duration::from_millis(timeout_millis as u64));
}

/// Set schema lock acquire timeout in milliseconds.
#[no_mangle]
pub extern "C" fn typedb_transaction_options_set_schema_lock_timeout(
    opts: *mut TransactionOptions,
    timeout_millis: i64,
) {
    let o = unsafe { &mut *opts };
    o.schema_lock_acquire_timeout = Some(Duration::from_millis(timeout_millis as u64));
}

/// Free transaction options.
#[no_mangle]
pub extern "C" fn typedb_transaction_options_drop(opts: *mut TransactionOptions) {
    if !opts.is_null() {
        unsafe { drop(Box::from_raw(opts)) }
    }
}

// ---------------------------------------------------------------------------
// QueryOptions
// ---------------------------------------------------------------------------

/// Create default query options. Caller must free with typedb_query_options_new.
#[no_mangle]
pub extern "C" fn typedb_query_options_new() -> *mut QueryOptions {
    Box::into_raw(Box::new(QueryOptions::new()))
}

/// Set include_instance_types option.
#[no_mangle]
pub extern "C" fn typedb_query_options_set_include_instance_types(
    opts: *mut QueryOptions,
    include: bool,
) {
    let o = unsafe { &mut *opts };
    o.include_instance_types = Some(include);
}

/// Set prefetch_size option.
#[no_mangle]
pub extern "C" fn typedb_query_options_set_prefetch_size(
    opts: *mut QueryOptions,
    size: i64,
) {
    let o = unsafe { &mut *opts };
    o.prefetch_size = Some(size as u64);
}

/// Free query options.
#[no_mangle]
pub extern "C" fn typedb_query_options_drop(opts: *mut QueryOptions) {
    if !opts.is_null() {
        unsafe { drop(Box::from_raw(opts)) }
    }
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
    let start = Instant::now();
    let db_name = c_str(database_name).to_string();
    rust_debug_log(
        "ffi.typedb_transaction_open.enter",
        vec![
            ("db", db_name.clone()),
            ("tx_type", transaction_type.to_string()),
        ],
    );

    let d = unsafe { &*driver };
    let tt = to_transaction_type(transaction_type);
    let opts = if options.is_null() {
        TransactionOptions::new()
    } else {
        unsafe { *(&*options) }
    };
    match d.transaction_with_options(c_str(database_name), tt, opts) {
        Ok(txn) => {
            rust_debug_log_timed(
                "ffi.typedb_transaction_open.exit",
                start,
                vec![
                    ("db", db_name),
                    ("tx_type", transaction_type.to_string()),
                    ("result", "ok".to_string()),
                ],
            );
            Box::into_raw(Box::new(txn))
        }
        Err(e) => {
            rust_debug_log_timed(
                "ffi.typedb_transaction_open.exit",
                start,
                vec![
                    ("db", db_name),
                    ("tx_type", transaction_type.to_string()),
                    ("result", "error".to_string()),
                    ("error", e.to_string()),
                ],
            );
            set_error(err_out, e);
            null_mut()
        }
    }
}

/// Check if transaction is open.
#[no_mangle]
pub extern "C" fn typedb_transaction_is_open(txn: *const Transaction) -> bool {
    if txn.is_null() {
        return false;
    }
    unsafe { &*txn }.is_open()
}

/// Execute a query and return results as a MessagePack-encoded byte buffer.
/// The buffer contains a msgpack array of maps (one per result row/document).
/// out_len receives the byte length of the buffer.
/// Returns null on error or for OK answers (out_len set to 0).
/// Caller must free with typedb_free_bytes.
#[no_mangle]
pub extern "C" fn typedb_transaction_query(
    txn: *mut Transaction,
    query: *const c_char,
    options: *const QueryOptions,
    out_len: *mut usize,
    err_out: *mut *mut c_char,
) -> *mut u8 {
    let start = Instant::now();
    let query_text = c_str(query);
    let op = query_op(query_text);
    let fingerprint = query_fingerprint(query_text);
    rust_debug_log(
        "ffi.typedb_transaction_query.enter",
        vec![
            ("query_op", op.clone()),
            ("query_fingerprint", fingerprint.clone()),
            ("query_len", query_text.len().to_string()),
        ],
    );

    let t = unsafe { &*txn };
    let opts = if options.is_null() {
        QueryOptions::new()
    } else {
        unsafe { *(&*options) }
    };

    let promise = t.query_with_options(c_str(query), opts);
    let answer: QueryAnswer = match promise.resolve() {
        Ok(a) => a,
        Err(e) => {
            rust_debug_log_timed(
                "ffi.typedb_transaction_query.exit",
                start,
                vec![
                    ("query_op", op.clone()),
                    ("query_fingerprint", fingerprint.clone()),
                    ("query_len", query_text.len().to_string()),
                    ("result", "error".to_string()),
                    ("error", e.to_string()),
                ],
            );
            set_error(err_out, e);
            return null_mut();
        }
    };

    match collect_answer_to_msgpack(answer) {
        Ok(bytes) => {
            let rows_or_bytes = bytes.len();
            rust_debug_log_timed(
                "ffi.typedb_transaction_query.exit",
                start,
                vec![
                    ("query_op", op),
                    ("query_fingerprint", fingerprint),
                    ("query_len", query_text.len().to_string()),
                    ("result", "ok".to_string()),
                    ("bytes", rows_or_bytes.to_string()),
                ],
            );
            vec_to_raw(bytes, out_len)
        }
        Err(e) => {
            rust_debug_log_timed(
                "ffi.typedb_transaction_query.exit",
                start,
                vec![
                    ("query_op", op),
                    ("query_fingerprint", fingerprint),
                    ("query_len", query_text.len().to_string()),
                    ("result", "error".to_string()),
                    ("error", e.clone()),
                ],
            );
            set_error(err_out, e);
            null_mut()
        }
    }
}

/// Convert a Vec<u8> into a raw pointer + length for FFI.
/// Sets *out_len. Returns null for empty vecs (out_len = 0).
fn vec_to_raw(bytes: Vec<u8>, out_len: *mut usize) -> *mut u8 {
    if bytes.is_empty() {
        if !out_len.is_null() {
            unsafe { *out_len = 0; }
        }
        return null_mut();
    }
    let len = bytes.len();
    let mut boxed = bytes.into_boxed_slice();
    let ptr = boxed.as_mut_ptr();
    std::mem::forget(boxed);
    if !out_len.is_null() {
        unsafe { *out_len = len; }
    }
    ptr
}

/// Helper: collect query answer into msgpack bytes.
fn collect_answer_to_msgpack(answer: QueryAnswer) -> Result<Vec<u8>, String> {
    let rows = collect_answer_to_values(answer)?;
    if rows.is_empty() {
        return Ok(vec![]);
    }
    rmp_serde::to_vec(&rows).map_err(|e| format!("msgpack encode error: {}", e))
}

/// Helper: collect query answer into Vec<serde_json::Value>.
fn collect_answer_to_values(answer: QueryAnswer) -> Result<Vec<serde_json::Value>, String> {
    if answer.is_ok() {
        return Ok(vec![]);
    }

    if answer.is_document_stream() {
        let mut docs: Vec<serde_json::Value> = Vec::new();
        for doc_result in answer.into_documents() {
            match doc_result {
                Ok(doc) => {
                    let json_val = doc.into_json();
                    // Convert typedb JSON to serde_json::Value
                    let json_str = serde_json::to_string(&json_val)
                        .unwrap_or_else(|_| json_val.to_string());
                    let val: serde_json::Value = serde_json::from_str(&json_str)
                        .unwrap_or(serde_json::Value::Null);
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
                                obj.insert(name.clone(), concept_to_json(concept));
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
    let start = Instant::now();
    rust_debug_log("ffi.typedb_transaction_commit.enter", vec![]);
    if txn.is_null() {
        rust_debug_log_timed(
            "ffi.typedb_transaction_commit.exit",
            start,
            vec![("result", "nil_txn".to_string())],
        );
        return;
    }
    let t = unsafe { Box::from_raw(txn) };
    if let Err(e) = t.commit().resolve() {
        rust_debug_log_timed(
            "ffi.typedb_transaction_commit.exit",
            start,
            vec![("result", "error".to_string()), ("error", e.to_string())],
        );
        set_error(err_out, e);
        return;
    }
    rust_debug_log_timed(
        "ffi.typedb_transaction_commit.exit",
        start,
        vec![("result", "ok".to_string())],
    );
}

/// Rollback the transaction.
#[no_mangle]
pub extern "C" fn typedb_transaction_rollback(txn: *const Transaction, err_out: *mut *mut c_char) {
    let start = Instant::now();
    rust_debug_log("ffi.typedb_transaction_rollback.enter", vec![]);
    if txn.is_null() {
        rust_debug_log_timed(
            "ffi.typedb_transaction_rollback.exit",
            start,
            vec![("result", "nil_txn".to_string())],
        );
        return;
    }
    let t = unsafe { &*txn };
    if let Err(e) = t.rollback().resolve() {
        rust_debug_log_timed(
            "ffi.typedb_transaction_rollback.exit",
            start,
            vec![("result", "error".to_string()), ("error", e.to_string())],
        );
        set_error(err_out, e);
        return;
    }
    rust_debug_log_timed(
        "ffi.typedb_transaction_rollback.exit",
        start,
        vec![("result", "ok".to_string())],
    );
}

/// Close and free the transaction without committing.
#[no_mangle]
pub extern "C" fn typedb_transaction_close(txn: *mut Transaction) {
    let start = Instant::now();
    rust_debug_log("ffi.typedb_transaction_close.enter", vec![]);
    if !txn.is_null() {
        unsafe { drop(Box::from_raw(txn)) };
        rust_debug_log_timed(
            "ffi.typedb_transaction_close.exit",
            start,
            vec![("result", "ok".to_string())],
        );
        return;
    }
    rust_debug_log_timed(
        "ffi.typedb_transaction_close.exit",
        start,
        vec![("result", "nil_txn".to_string())],
    );
}
