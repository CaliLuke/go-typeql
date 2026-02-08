#ifndef TYPEDB_FFI_H
#define TYPEDB_FFI_H

#include <stdlib.h>
#include <stdbool.h>

// String management
extern void typedb_free_string(char* s);

// Byte buffer management
extern void typedb_free_bytes(unsigned char* ptr, size_t len);

// Logging
extern void typedb_init_logging();

// Credentials
extern void* typedb_credentials_new(const char* username, const char* password);
extern void typedb_credentials_drop(void* creds);

// DriverOptions
extern void* typedb_driver_options_new(bool is_tls_enabled, const char* tls_root_ca, char** err_out);
extern void typedb_driver_options_drop(void* opts);

// Driver
extern void* typedb_driver_open(const char* address, const void* credentials, const void* options, char** err_out);
extern bool typedb_driver_is_open(const void* driver);
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

// Transaction
extern void* typedb_transaction_open(void* driver, const char* database_name, int transaction_type, const void* options, char** err_out);
extern bool typedb_transaction_is_open(const void* txn);
extern unsigned char* typedb_transaction_query(void* txn, const char* query, const void* options, size_t* out_len, char** err_out);
extern void typedb_transaction_commit(void* txn, char** err_out);
extern void typedb_transaction_rollback(const void* txn, char** err_out);
extern void typedb_transaction_close(void* txn);

// Async query
extern void* typedb_transaction_query_async(void* txn, const char* query, const void* options, char** err_out);
extern bool typedb_future_is_ready(const void* future);
extern unsigned char* typedb_future_resolve(void* future, size_t* out_len, char** err_out);
extern void typedb_future_abort(void* future);
extern void typedb_future_drop(void* future);

#endif
