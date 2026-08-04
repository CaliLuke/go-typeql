#ifndef TYPEDB_FFI_H
#define TYPEDB_FFI_H

#include <stdlib.h>
#include <stdbool.h>

// Panic safety: every function below contains Rust panics at the FFI
// boundary. A panic is reported through err_out (when the function has one)
// and the function returns its null/false/no-op default; it never aborts or
// unwinds into the calling process.
//
// Error strings written to *err_out are heap-allocated and must be freed with
// typedb_free_string. Messages never contain interior NUL bytes (they are
// escaped as "\0"), so a failed operation always yields a non-null *err_out.

// String management
extern void typedb_free_string(char* s);

// Byte buffer management
extern void typedb_free_bytes(unsigned char* ptr, size_t len);

// Logging.
// Installs a tracing subscriber (stderr) for the underlying typedb-driver
// crate. The filter comes from TYPEDB_GO_RUST_LOG, falling back to RUST_LOG;
// when neither is set logging stays off. Safe to call multiple times.
extern void typedb_init_logging();

// Credentials
extern void* typedb_credentials_new(const char* username, const char* password, char** err_out);
extern void typedb_credentials_drop(void* creds);

// DriverOptions
extern void* typedb_driver_options_new(bool is_tls_enabled, const char* tls_root_ca, char** err_out);
extern void typedb_driver_options_set_request_timeout(void* opts, long long timeout_millis);
extern void typedb_driver_options_set_primary_failover_retries(void* opts, size_t retries);
extern void typedb_driver_options_drop(void* opts);

// Driver.
// typedb_driver_open uses the address exactly as given (no localhost port
// rewriting). Use typedb_driver_open_addresses with a translation map when
// the server advertises an address that differs from the dialed one.
extern void* typedb_driver_open(const char* address, const void* credentials, const void* options, char** err_out);
extern void* typedb_driver_open_addresses(const char** public_addresses, const char** private_addresses, size_t address_count, const void* credentials, const void* options, char** err_out);
extern bool typedb_driver_is_open(const void* driver);
extern char* typedb_driver_server_version(void* driver, char** err_out);
extern void typedb_driver_close(void* driver);

// Database management
extern char* typedb_databases_all(void* driver, char** err_out);
extern void typedb_databases_create(void* driver, const char* name, char** err_out);
extern bool typedb_databases_contains(void* driver, const char* name, char** err_out);
extern char* typedb_database_schema(void* driver, const char* name, char** err_out);
extern void typedb_database_delete(void* driver, const char* name, char** err_out);

// TransactionOptions
extern void* typedb_transaction_options_new();
extern void typedb_transaction_options_set_timeout(void* opts, long long timeout_millis);
extern void typedb_transaction_options_set_schema_lock_timeout(void* opts, long long timeout_millis);
extern void typedb_transaction_options_drop(void* opts);

// QueryOptions
extern void* typedb_query_options_new();
extern void typedb_query_options_set_include_instance_types(void* opts, bool include);
extern void typedb_query_options_set_prefetch_size(void* opts, long long size);
extern void typedb_query_options_drop(void* opts);

// Concept handles.
//
// Ownership/lifetime contract:
//   - Entity/relation concepts in row results are registered in a
//     process-global registry ONLY when a query is executed with
//     register_concepts = true; each registered concept appears in the
//     result row as a "_concept_handle" string.
//   - The caller owns every handle so produced. A handle stays valid across
//     transactions (it captures the concept's identity, not a server cursor)
//     until it is released.
//   - Release handles with typedb_concept_drop (single handle; unknown or
//     null handles are ignored) or typedb_concepts_drop_all (drops every
//     handle in the process). Unreleased handles are held until process
//     exit, so long-running callers must release what they register.
//   - Queries executed with register_concepts = false never touch the
//     registry and emit no "_concept_handle" keys.
extern void typedb_concept_drop(const char* handle);
extern void typedb_concepts_drop_all();

// Transaction
extern void* typedb_transaction_open(void* driver, const char* database_name, int transaction_type, const void* options, char** err_out);
extern bool typedb_transaction_is_open(const void* txn);
extern unsigned char* typedb_transaction_query(void* txn, const char* query, const void* options, bool register_concepts, size_t* out_len, char** err_out);
extern unsigned char* typedb_transaction_query_with_rows(void* txn, const char* query, const void* options, const char* rows_json, bool register_concepts, size_t* out_len, char** err_out);
extern void* typedb_transaction_query_stream_open(void* txn, const char* query, const void* options, bool register_concepts, char** err_out);
extern unsigned char* typedb_query_stream_next(void* stream, size_t max_rows, size_t* out_row_count, bool* out_done, size_t* out_len, char** err_out);
extern void typedb_query_stream_drop(void* stream);
extern void typedb_transaction_commit(void* txn, char** err_out);
extern void typedb_transaction_rollback(const void* txn, char** err_out);
extern void typedb_transaction_close(void* txn, char** err_out);
extern void typedb_transaction_drop(void* txn);

#endif
