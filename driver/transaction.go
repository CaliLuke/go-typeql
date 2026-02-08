//go:build cgo && typedb

package driver

// #include "typedb_ffi.h"
import "C"
import (
	"context"
	"sync"
	"unsafe"

	"bytes"

	"github.com/vmihailenco/msgpack/v5"
)

// Transaction represents an active unit of work in a TypeDB database.
// Transactions are used to execute queries and must be either committed or closed.
type Transaction struct {
	ptr unsafe.Pointer
	mu  sync.Mutex
}

// IsOpen returns true if the transaction is active and has not been committed, rolled back, or closed.
func (t *Transaction) IsOpen() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.ptr == nil {
		return false
	}
	return bool(C.typedb_transaction_is_open(t.ptr))
}

// Query executes a TypeQL query (match, insert, delete, update) within the transaction.
// It returns the results as a slice of maps, where each map represents a ConceptRow.
func (t *Transaction) Query(query string) ([]map[string]any, error) {
	return t.QueryWithOptions(query, nil)
}

// QueryWithOptions executes a TypeQL query with specific QueryOptions.
func (t *Transaction) QueryWithOptions(query string, opts *QueryOptions) ([]map[string]any, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.ptr == nil {
		return nil, ErrNotConnected
	}

	cQuery := C.CString(query)
	defer C.free(unsafe.Pointer(cQuery))

	var cOpts unsafe.Pointer
	if opts != nil {
		cOpts = opts.ptr
	}

	var outLen C.size_t
	var queryErr *C.char
	buf := C.typedb_transaction_query(t.ptr, cQuery, cOpts, &outLen, &queryErr)
	if buf == nil {
		if err := getError(queryErr); err != nil {
			return nil, err
		}
		return nil, nil
	}
	defer C.typedb_free_bytes((*C.uchar)(unsafe.Pointer(buf)), outLen)

	return decodeMsgpack(buf, outLen)
}

// QueryWithContext executes a TypeQL query with context cancellation support.
// If the context is cancelled while the query is in-flight, the FFI future is
// aborted and ctx.Err() is returned.
func (t *Transaction) QueryWithContext(ctx context.Context, query string) ([]map[string]any, error) {
	// Fast path: bail immediately if already cancelled
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	t.mu.Lock()
	if t.ptr == nil {
		t.mu.Unlock()
		return nil, ErrNotConnected
	}

	cQuery := C.CString(query)
	defer C.free(unsafe.Pointer(cQuery))

	var asyncErr *C.char
	future := C.typedb_transaction_query_async(t.ptr, cQuery, nil, &asyncErr)
	t.mu.Unlock() // release lock while waiting

	if future == nil {
		if err := getError(asyncErr); err != nil {
			return nil, err
		}
		return nil, nil
	}

	// Resolve in a goroutine so we can select on context cancellation
	type queryResult struct {
		buf    *C.uchar
		outLen C.size_t
		err    *C.char
	}
	ch := make(chan queryResult, 1)
	go func() {
		var resolveErr *C.char
		var outLen C.size_t
		buf := C.typedb_future_resolve(future, &outLen, &resolveErr)
		ch <- queryResult{buf: buf, outLen: outLen, err: resolveErr}
	}()

	select {
	case <-ctx.Done():
		C.typedb_future_abort(future)
		<-ch // drain goroutine after abort unblocks resolve
		return nil, ctx.Err()
	case res := <-ch:
		if res.buf == nil {
			if err := getError(res.err); err != nil {
				return nil, err
			}
			return nil, nil
		}
		defer C.typedb_free_bytes(res.buf, res.outLen)
		return decodeMsgpack((*C.uchar)(unsafe.Pointer(res.buf)), res.outLen)
	}
}

// decodeMsgpack decodes a MessagePack byte buffer into a slice of maps.
func decodeMsgpack(buf *C.uchar, outLen C.size_t) ([]map[string]any, error) {
	goBytes := C.GoBytes(unsafe.Pointer(buf), C.int(outLen))
	dec := msgpack.NewDecoder(bytes.NewReader(goBytes))
	dec.UseLooseInterfaceDecoding(true)
	var results []map[string]any
	if err := dec.Decode(&results); err != nil {
		return nil, &DriverError{Message: "failed to decode msgpack query results: " + err.Error()}
	}
	return results, nil
}

// Commit persists the changes made in the transaction to the database.
// After calling Commit, the transaction is closed and cannot be used further.
func (t *Transaction) Commit() error {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.ptr == nil {
		return ErrNotConnected
	}

	var commitErr *C.char
	C.typedb_transaction_commit(t.ptr, &commitErr)
	t.ptr = nil // consumed by commit
	return getError(commitErr)
}

// Rollback discards all changes made within the transaction.
func (t *Transaction) Rollback() error {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.ptr == nil {
		return ErrNotConnected
	}

	var rollbackErr *C.char
	C.typedb_transaction_rollback(t.ptr, &rollbackErr)
	return getError(rollbackErr)
}

// Close terminates the transaction without committing any changes.
// It should be used in a 'defer' block to ensure resources are released.
func (t *Transaction) Close() {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.ptr != nil {
		C.typedb_transaction_close(t.ptr)
		t.ptr = nil
	}
}
