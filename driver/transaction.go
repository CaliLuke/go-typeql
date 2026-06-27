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

var msgpackDecoderPool = sync.Pool{
	New: func() any {
		dec := msgpack.NewDecoder(bytes.NewReader(nil))
		dec.UseLooseInterfaceDecoding(true)
		return dec
	},
}

// Transaction represents an active unit of work in a TypeDB database.
// Transactions are used to execute queries and must be either committed or closed.
type Transaction struct {
	ptr    unsafe.Pointer
	mu     sync.Mutex
	id     uint64
	dbName string
	txType TransactionType
	opened bool
	owner  *Driver
	closer *transactionCloseWorker
}

type transactionCloseJob struct {
	ptr    unsafe.Pointer
	id     uint64
	dbName string
	txType TransactionType
	start  time.Time
	onDone func(error)
}

type transactionCloseWorker struct {
	mu     sync.Mutex
	jobs   chan transactionCloseJob
	done   chan struct{}
	closed bool
}

const transactionCloseQueueSize = 1024

type transactionCloseTracker struct {
	mu      sync.Mutex
	pending int
	zero    chan struct{}
}

var pendingTransactionCloses = newTransactionCloseTracker()

func newTransactionCloseTracker() *transactionCloseTracker {
	ch := make(chan struct{})
	close(ch)
	return &transactionCloseTracker{zero: ch}
}

func (t *transactionCloseTracker) add() {
	t.mu.Lock()
	if t.pending == 0 {
		t.zero = make(chan struct{})
	}
	t.pending++
	t.mu.Unlock()
}

func (t *transactionCloseTracker) done() {
	t.mu.Lock()
	t.pending--
	if t.pending == 0 {
		close(t.zero)
	}
	t.mu.Unlock()
}

func (t *transactionCloseTracker) wait(ctx context.Context) error {
	t.mu.Lock()
	zero := t.zero
	t.mu.Unlock()

	select {
	case <-zero:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func newTransactionCloseWorker() *transactionCloseWorker {
	w := &transactionCloseWorker{
		jobs: make(chan transactionCloseJob, transactionCloseQueueSize),
		done: make(chan struct{}),
	}
	go w.run()
	return w
}

func (w *transactionCloseWorker) enqueue(job transactionCloseJob) bool {
	if w == nil {
		return false
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return false
	}
	pendingTransactionCloses.add()
	select {
	case w.jobs <- job:
		return true
	default:
		pendingTransactionCloses.done()
		return false
	}
}

func (w *transactionCloseWorker) run() {
	defer close(w.done)
	for job := range w.jobs {
		runTransactionCloseJob(job)
		pendingTransactionCloses.done()
	}
}

func (w *transactionCloseWorker) close() {
	w.mu.Lock()
	if !w.closed {
		w.closed = true
		close(w.jobs)
	}
	w.mu.Unlock()
	<-w.done
}

func runTransactionCloseJob(job transactionCloseJob) {
	var closeErr *C.char
	C.typedb_transaction_close(job.ptr, &closeErr)
	err := getError(closeErr)
	logTransactionClose(job, err)
	if job.onDone != nil {
		job.onDone(err)
	}
}

func logTransactionClose(job transactionCloseJob, err error) {
	if err != nil {
		logFFIDuration("tx.close", job.start, "tx_id", job.id, "db", job.dbName, "tx_type", int(job.txType), "result", "error", "error", err.Error())
		return
	}
	logFFIDuration("tx.close", job.start, "tx_id", job.id, "db", job.dbName, "tx_type", int(job.txType), "result", "ok")
}

// WaitForPendingCloses waits for already accepted asynchronous transaction close
// jobs to finish. It is a drain point for tests and graceful shutdown; it does
// not stop close workers.
func WaitForPendingCloses(ctx context.Context) error {
	return pendingTransactionCloses.wait(ctx)
}

func newTransaction(ptr unsafe.Pointer, id uint64, dbName string, txType TransactionType, owner *Driver, closer *transactionCloseWorker) *Transaction {
	t := &Transaction{ptr: ptr, id: id, dbName: dbName, txType: txType, opened: true, owner: owner, closer: closer}
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
	if t.owner != nil {
		t.owner.unregisterTransaction(t.id)
	}
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

func (t *Transaction) logQueryDuration(start time.Time, query string, queryOp, queryFP string, rows int, byteCount int, err error, extra ...any) {
	fields := []any{
		"tx_id", t.id,
		"db", t.dbName,
		"tx_type", int(t.txType),
		"query_len", len(query),
		"query_op", queryOp,
		"query_fingerprint", queryFP,
	}
	fields = append(fields, extra...)
	if err != nil {
		fields = append(fields, "result", "error", "error", err.Error())
	} else {
		fields = append(fields, "result", "ok", "rows", rows, "bytes", byteCount)
	}
	logFFIDuration("tx.query", start, fields...)
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
	return t.query(query, opts, nil)
}

// QueryWithRows executes a TypeQL query with typed input rows for a given stage.
func (t *Transaction) QueryWithRows(query string, rows *GivenRows) ([]map[string]any, error) {
	return t.query(query, nil, rows)
}

// QueryWithOptionsAndRows executes a TypeQL query with query options and typed
// input rows for a given stage.
func (t *Transaction) QueryWithOptionsAndRows(query string, opts *QueryOptions, rows *GivenRows) ([]map[string]any, error) {
	return t.query(query, opts, rows)
}

func (t *Transaction) query(query string, opts *QueryOptions, rows *GivenRows) ([]map[string]any, error) {
	start := time.Now()
	queryOp := queryOperation(query)
	queryFP := queryFingerprint(query)
	var rowsJSON []byte
	var err error
	if rows != nil {
		rowsJSON, err = rows.json()
		if err != nil {
			err = withQuery(err, query)
			t.logQueryDuration(start, query, queryOp, queryFP, 0, 0, err, "with_options", opts != nil, "with_rows", true)
			return nil, err
		}
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	if t.ptr == nil {
		t.logQueryDuration(start, query, queryOp, queryFP, 0, 0, ErrNotConnected, "with_options", opts != nil, "with_rows", rows != nil)
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
	var buf *C.uchar
	if rows != nil {
		cRows := C.CString(string(rowsJSON))
		defer C.free(unsafe.Pointer(cRows))
		buf = C.typedb_transaction_query_with_rows(t.ptr, cQuery, cOpts, cRows, &outLen, &queryErr)
	} else {
		buf = C.typedb_transaction_query(t.ptr, cQuery, cOpts, &outLen, &queryErr)
	}
	if buf == nil {
		if err := withQuery(getError(queryErr), query); err != nil {
			t.logQueryDuration(start, query, queryOp, queryFP, 0, 0, err, "with_options", opts != nil, "with_rows", rows != nil)
			return nil, err
		}
		t.logQueryDuration(start, query, queryOp, queryFP, 0, 0, nil, "with_options", opts != nil, "with_rows", rows != nil)
		return nil, nil
	}
	defer C.typedb_free_bytes((*C.uchar)(unsafe.Pointer(buf)), outLen)
	results, err := decodeMsgpack(buf, outLen)
	err = withQuery(err, query)
	if err != nil {
		t.logQueryDuration(start, query, queryOp, queryFP, 0, int(outLen), err, "with_options", opts != nil, "with_rows", rows != nil)
		return nil, err
	}
	t.logQueryDuration(start, query, queryOp, queryFP, len(results), int(outLen), nil, "with_options", opts != nil, "with_rows", rows != nil)
	return results, nil
}

// QueryWithContext executes a TypeQL query with context cancellation support.
//
// Cancellation semantics are intentionally limited by the underlying Rust
// driver handle:
//   - The transaction handle is single-threaded, so every FFI call must hold
//     t.mu for the duration of the synchronous C call.
//   - If ctx is cancelled, only the caller's goroutine is released early with
//     ctx.Err(). The in-flight FFI call continues until the driver returns.
//   - Commit, Rollback, and Close will block behind any in-flight query until
//     that synchronous FFI call finishes.
//
// The goroutine exists only to let the caller stop waiting on a blocking FFI
// call. It does not make the underlying driver operation interruptible.
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
	if ctx.Done() == nil {
		return t.Query(query)
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
			t.logQueryDuration(start, query, queryOp, queryFP, 0, 0, ErrNotConnected, "with_context", true)
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
			if err := withQuery(getError(queryErr), query); err != nil {
				t.logQueryDuration(start, query, queryOp, queryFP, 0, 0, err, "with_context", true)
				ch <- queryResult{err: err}
				return
			}
			t.logQueryDuration(start, query, queryOp, queryFP, 0, 0, nil, "with_context", true)
			ch <- queryResult{}
			return
		}
		defer C.typedb_free_bytes((*C.uchar)(unsafe.Pointer(buf)), outLen)

		results, err := decodeMsgpack(buf, outLen)
		err = withQuery(err, query)
		t.logQueryDuration(start, query, queryOp, queryFP, len(results), int(outLen), err, "with_context", true)
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
	var reader bytes.Reader
	reader.Reset(goBytes)

	dec := msgpackDecoderPool.Get().(*msgpack.Decoder)
	defer msgpackDecoderPool.Put(dec)
	dec.Reset(&reader)
	dec.UseLooseInterfaceDecoding(true)

	var results []map[string]any
	if err := dec.Decode(&results); err != nil {
		return nil, &DriverError{Message: "failed to decode msgpack query results: " + err.Error()}
	}
	return results, nil
}

// Commit persists the changes made in the transaction to the database.
// Whether Commit succeeds or fails, the underlying Rust transaction handle is
// consumed and cannot be reused, rolled back, or closed again meaningfully.
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
	C.typedb_transaction_drop(t.ptr)
	t.ptr = nil
	t.markClosedLocked("rollback")
	logFFIDuration("tx.rollback", start, "tx_id", t.id, "db", t.dbName, "tx_type", int(t.txType), "result", "ok")
	return nil
}

// Close terminates the transaction without committing any changes.
// It should be used in a 'defer' block to ensure resources are released.
func (t *Transaction) Close() {
	t.CloseAsync(nil)
}

// CloseAsync terminates the transaction without committing and returns without
// waiting for the checked TypeDB close to complete. If onDone is non-nil and
// the checked close is queued, it is called exactly once when that close
// finishes. If the close queue is full, the transaction is dropped locally and
// onDone receives nil because no checked close result is available.
func (t *Transaction) CloseAsync(onDone func(error)) {
	start := time.Now()
	job := t.detachCloseJob(start, onDone)
	if job.ptr == nil {
		return
	}
	if t.closer != nil && t.closer.enqueue(job) {
		return
	}

	C.typedb_transaction_drop(job.ptr)
	if job.onDone != nil {
		job.onDone(nil)
	}
}

// CloseChecked terminates the transaction synchronously and returns the checked
// TypeDB close error, if any.
func (t *Transaction) CloseChecked() error {
	start := time.Now()
	job := t.detachCloseJob(start, nil)
	if job.ptr == nil {
		return nil
	}

	var closeErr *C.char
	C.typedb_transaction_close(job.ptr, &closeErr)
	err := getError(closeErr)
	logTransactionClose(job, err)
	return err
}

func (t *Transaction) detachCloseJob(start time.Time, onDone func(error)) transactionCloseJob {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.ptr == nil {
		return transactionCloseJob{}
	}
	job := transactionCloseJob{
		ptr:    t.ptr,
		id:     t.id,
		dbName: t.dbName,
		txType: t.txType,
		start:  start,
		onDone: onDone,
	}
	t.ptr = nil
	t.markClosedLocked("close")
	return job
}
