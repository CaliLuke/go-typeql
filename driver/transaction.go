//go:build cgo && typedb

package driver

// #include "typedb_ffi.h"
import "C"
import (
	"context"
	"runtime"
	"sync"
	"time"
	"unsafe"

	"bytes"

	"github.com/vmihailenco/msgpack/v5"
)

// Transaction represents an active unit of work in a TypeDB database.
// Transactions are used to execute queries and must be either committed or closed.
type Transaction struct {
	ptr    unsafe.Pointer
	mu     sync.Mutex
	id     uint64
	dbName string
	txType TransactionType
	opened bool
}

func newTransaction(ptr unsafe.Pointer, id uint64, dbName string, txType TransactionType) *Transaction {
	t := &Transaction{ptr: ptr, id: id, dbName: dbName, txType: txType, opened: true}
	incActiveTxOpen("tx_id", id, "db", dbName, "tx_type", int(txType), "reason", "open")
	if debugEnabled() {
		runtime.SetFinalizer(t, (*Transaction).debugLeakFinalizer)
	}
	return t
}

func (t *Transaction) markClosedLocked(reason string) {
	if !t.opened {
		return
	}
	t.opened = false
	decActiveTxOpen("tx_id", t.id, "db", t.dbName, "tx_type", int(t.txType), "reason", reason)
}

func (t *Transaction) debugLeakFinalizer() {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.ptr != nil {
		logFFIDebug("tx.finalizer.leak", "tx_id", t.id, "db", t.dbName, "tx_type", int(t.txType))
	}
	t.markClosedLocked("gc_finalizer")
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
	start := time.Now()
	queryOp := queryOperation(query)
	queryFP := queryFingerprint(query)
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.ptr == nil {
		logFFIDuration("tx.query", start, "tx_id", t.id, "db", t.dbName, "tx_type", int(t.txType), "query_len", len(query), "query_op", queryOp, "query_fingerprint", queryFP, "with_options", opts != nil, "result", "error", "error", ErrNotConnected.Error())
		return nil, ErrNotConnected
	}

	cQuery := C.CString(query)
	defer C.free(unsafe.Pointer(cQuery))

	var cOpts unsafe.Pointer
	if opts != nil {
		cOpts = opts.ptr
	}

	incActiveTxQuery("tx_id", t.id, "db", t.dbName, "tx_type", int(t.txType), "query_op", queryOp, "query_fingerprint", queryFP, "reason", "start")
	defer decActiveTxQuery("tx_id", t.id, "db", t.dbName, "tx_type", int(t.txType), "query_op", queryOp, "query_fingerprint", queryFP, "reason", "finish")

	var outLen C.size_t
	var queryErr *C.char
	buf := C.typedb_transaction_query(t.ptr, cQuery, cOpts, &outLen, &queryErr)
	if buf == nil {
		if err := getError(queryErr); err != nil {
			logFFIDuration("tx.query", start, "tx_id", t.id, "db", t.dbName, "tx_type", int(t.txType), "query_len", len(query), "query_op", queryOp, "query_fingerprint", queryFP, "with_options", opts != nil, "result", "error", "error", err.Error())
			return nil, err
		}
		logFFIDuration("tx.query", start, "tx_id", t.id, "db", t.dbName, "tx_type", int(t.txType), "query_len", len(query), "query_op", queryOp, "query_fingerprint", queryFP, "with_options", opts != nil, "result", "ok", "rows", 0, "bytes", 0)
		return nil, nil
	}
	defer C.typedb_free_bytes((*C.uchar)(unsafe.Pointer(buf)), outLen)
	results, err := decodeMsgpack(buf, outLen)
	if err != nil {
		logFFIDuration("tx.query", start, "tx_id", t.id, "db", t.dbName, "tx_type", int(t.txType), "query_len", len(query), "query_op", queryOp, "query_fingerprint", queryFP, "with_options", opts != nil, "result", "error", "error", err.Error())
		return nil, err
	}
	logFFIDuration("tx.query", start, "tx_id", t.id, "db", t.dbName, "tx_type", int(t.txType), "query_len", len(query), "query_op", queryOp, "query_fingerprint", queryFP, "with_options", opts != nil, "result", "ok", "rows", len(results), "bytes", int(outLen))
	return results, nil
}

// QueryWithContext executes a TypeQL query with context cancellation support.
// The query runs synchronously inside a goroutine while holding the transaction
// mutex for the entire FFI call. If the context is cancelled, ctx.Err() is
// returned but the underlying FFI call is allowed to finish naturally (C calls
// cannot be safely interrupted mid-flight).
func (t *Transaction) QueryWithContext(ctx context.Context, query string) ([]map[string]any, error) {
	queryOp := queryOperation(query)
	queryFP := queryFingerprint(query)
	if deadline, ok := ctx.Deadline(); ok {
		logFFIDebug("tx.query_with_context.start", "tx_id", t.id, "db", t.dbName, "tx_type", int(t.txType), "query_len", len(query), "query_op", queryOp, "query_fingerprint", queryFP, "deadline_remaining_ms", time.Until(deadline).Milliseconds())
	}

	// Fast path: bail immediately if already cancelled
	if err := ctx.Err(); err != nil {
		logFFIDebug("tx.query_with_context.cancelled", "tx_id", t.id, "db", t.dbName, "tx_type", int(t.txType), "query_len", len(query), "query_op", queryOp, "query_fingerprint", queryFP, "error", err.Error())
		return nil, err
	}

	type queryResult struct {
		results []map[string]any
		err     error
	}
	ch := make(chan queryResult, 1)

	go func() {
		start := time.Now()
		// Hold mutex for the entire sync FFI call
		t.mu.Lock()
		defer t.mu.Unlock()

		if t.ptr == nil {
			logFFIDuration("tx.query", start, "tx_id", t.id, "db", t.dbName, "tx_type", int(t.txType), "query_len", len(query), "query_op", queryOp, "query_fingerprint", queryFP, "with_context", true, "result", "error", "error", ErrNotConnected.Error())
			ch <- queryResult{err: ErrNotConnected}
			return
		}

		cQuery := C.CString(query)
		defer C.free(unsafe.Pointer(cQuery))

		incActiveTxQuery("tx_id", t.id, "db", t.dbName, "tx_type", int(t.txType), "query_op", queryOp, "query_fingerprint", queryFP, "reason", "start")
		defer decActiveTxQuery("tx_id", t.id, "db", t.dbName, "tx_type", int(t.txType), "query_op", queryOp, "query_fingerprint", queryFP, "reason", "finish")

		var outLen C.size_t
		var queryErr *C.char
		buf := C.typedb_transaction_query(t.ptr, cQuery, nil, &outLen, &queryErr)
		if buf == nil {
			if err := getError(queryErr); err != nil {
				logFFIDuration("tx.query", start, "tx_id", t.id, "db", t.dbName, "tx_type", int(t.txType), "query_len", len(query), "query_op", queryOp, "query_fingerprint", queryFP, "with_context", true, "result", "error", "error", err.Error())
				ch <- queryResult{err: err}
				return
			}
			logFFIDuration("tx.query", start, "tx_id", t.id, "db", t.dbName, "tx_type", int(t.txType), "query_len", len(query), "query_op", queryOp, "query_fingerprint", queryFP, "with_context", true, "result", "ok", "rows", 0, "bytes", 0)
			ch <- queryResult{}
			return
		}
		defer C.typedb_free_bytes((*C.uchar)(unsafe.Pointer(buf)), outLen)

		results, err := decodeMsgpack(buf, outLen)
		if err != nil {
			logFFIDuration("tx.query", start, "tx_id", t.id, "db", t.dbName, "tx_type", int(t.txType), "query_len", len(query), "query_op", queryOp, "query_fingerprint", queryFP, "with_context", true, "result", "error", "error", err.Error())
		} else {
			logFFIDuration("tx.query", start, "tx_id", t.id, "db", t.dbName, "tx_type", int(t.txType), "query_len", len(query), "query_op", queryOp, "query_fingerprint", queryFP, "with_context", true, "result", "ok", "rows", len(results), "bytes", int(outLen))
		}
		ch <- queryResult{results: results, err: err}
	}()

	select {
	case <-ctx.Done():
		logFFIDebug("tx.query_with_context.cancelled", "tx_id", t.id, "db", t.dbName, "tx_type", int(t.txType), "query_len", len(query), "query_op", queryOp, "query_fingerprint", queryFP, "error", ctx.Err().Error())
		return nil, ctx.Err()
	case res := <-ch:
		return res.results, res.err
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
	start := time.Now()
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.ptr == nil {
		logFFIDuration("tx.commit", start, "tx_id", t.id, "db", t.dbName, "tx_type", int(t.txType), "result", "error", "error", ErrNotConnected.Error())
		return ErrNotConnected
	}

	var commitErr *C.char
	C.typedb_transaction_commit(t.ptr, &commitErr)
	t.ptr = nil // consumed by commit
	t.markClosedLocked("commit")
	if err := getError(commitErr); err != nil {
		logFFIDuration("tx.commit", start, "tx_id", t.id, "db", t.dbName, "tx_type", int(t.txType), "result", "error", "error", err.Error())
		return err
	}
	logFFIDuration("tx.commit", start, "tx_id", t.id, "db", t.dbName, "tx_type", int(t.txType), "result", "ok")
	return nil
}

// Rollback discards all changes made within the transaction.
func (t *Transaction) Rollback() error {
	start := time.Now()
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.ptr == nil {
		logFFIDuration("tx.rollback", start, "tx_id", t.id, "db", t.dbName, "tx_type", int(t.txType), "result", "error", "error", ErrNotConnected.Error())
		return ErrNotConnected
	}

	var rollbackErr *C.char
	C.typedb_transaction_rollback(t.ptr, &rollbackErr)
	if err := getError(rollbackErr); err != nil {
		logFFIDuration("tx.rollback", start, "tx_id", t.id, "db", t.dbName, "tx_type", int(t.txType), "result", "error", "error", err.Error())
		return err
	}
	logFFIDuration("tx.rollback", start, "tx_id", t.id, "db", t.dbName, "tx_type", int(t.txType), "result", "ok")
	return nil
}

// Close terminates the transaction without committing any changes.
// It should be used in a 'defer' block to ensure resources are released.
func (t *Transaction) Close() {
	start := time.Now()
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.ptr != nil {
		C.typedb_transaction_close(t.ptr)
		t.ptr = nil
		t.markClosedLocked("close")
		logFFIDuration("tx.close", start, "tx_id", t.id, "db", t.dbName, "tx_type", int(t.txType), "result", "ok")
	}
}
