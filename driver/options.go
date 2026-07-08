//go:build cgo && typedb

package driver

// #include "typedb_ffi.h"
import "C"
import "unsafe"

// TransactionOptions provides configuration for tuning transaction behavior,
// such as timeouts and schema locking.
type TransactionOptions struct {
	ptr unsafe.Pointer
}

// NewTransactionOptions creates a new set of transaction options with default values.
func NewTransactionOptions() *TransactionOptions {
	return &TransactionOptions{ptr: unsafe.Pointer(C.typedb_transaction_options_new())}
}

// SetTimeout sets the overall transaction timeout in milliseconds.
// If the transaction exceeds this duration, it will be automatically rolled back.
func (o *TransactionOptions) SetTimeout(millis int64) *TransactionOptions {
	C.typedb_transaction_options_set_timeout(o.ptr, C.longlong(millis))
	return o
}

// SetSchemaLockTimeout sets the timeout for acquiring a schema lock in milliseconds.
// This is relevant for Schema transactions.
func (o *TransactionOptions) SetSchemaLockTimeout(millis int64) *TransactionOptions {
	C.typedb_transaction_options_set_schema_lock_timeout(o.ptr, C.longlong(millis))
	return o
}

// Close releases the resources associated with the TransactionOptions object.
func (o *TransactionOptions) Close() {
	if o.ptr != nil {
		C.typedb_transaction_options_drop(o.ptr)
		o.ptr = nil
	}
}

// QueryOptions provides configuration for fine-tuning query execution behavior.
type QueryOptions struct {
	ptr            unsafe.Pointer
	conceptHandles bool
}

// NewQueryOptions creates a new set of query options with default values.
func NewQueryOptions() *QueryOptions {
	return &QueryOptions{ptr: unsafe.Pointer(C.typedb_query_options_new())}
}

// SetIncludeInstanceTypes specifies whether the server should include type information
// for each concept returned in the query results.
func (o *QueryOptions) SetIncludeInstanceTypes(include bool) *QueryOptions {
	C.typedb_query_options_set_include_instance_types(o.ptr, C.bool(include))
	return o
}

// SetPrefetchSize specifies the number of additional result rows to prefetch from the server.
// Increasing this can improve performance for large result sets.
func (o *QueryOptions) SetPrefetchSize(size int64) *QueryOptions {
	C.typedb_query_options_set_prefetch_size(o.ptr, C.longlong(size))
	return o
}

// SetConceptHandles controls whether entity and relation concepts in row
// results carry opaque concept handles (retrievable with AsConcept) that can
// be passed back to the server via ConceptGiven.
//
// Handles are registered in a process-global registry and stay valid across
// transactions until released, so every handle obtained this way must be
// freed with Concept.Release (or ReleaseAllConcepts) once it is no longer
// needed; otherwise the registry grows for the life of the process. Queries
// executed without this option never register handles.
func (o *QueryOptions) SetConceptHandles(enable bool) *QueryOptions {
	o.conceptHandles = enable
	return o
}

// Close releases the resources associated with the QueryOptions object.
func (o *QueryOptions) Close() {
	if o.ptr != nil {
		C.typedb_query_options_drop(o.ptr)
		o.ptr = nil
	}
}
