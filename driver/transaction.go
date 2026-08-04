//go:build cgo && typedb

package driver

// #include "typedb_ffi.h"
import "C"
import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"math"
	"runtime"
	"sync"
	"time"
	"unsafe"

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
//
// Lifecycle contract: whoever opens a transaction ends it exactly once via
// Commit, Rollback, Close, CloseAsync, or CloseChecked. Two backstops exist
// for transactions that escape that contract:
//
//   - When a QueryWithContext call is cancelled, the transaction is
//     abandoned: the background goroutine that is still blocked in the
//     synchronous driver call frees the native handle once that call
//     returns, and every later lifecycle call (Commit, Rollback, Close,
//     CloseAsync, CloseChecked, IsOpen) returns immediately instead of
//     blocking behind it.
//   - A garbage-collection finalizer frees the native handle of a
//     transaction that was never ended, logging a leak warning.
type Transaction struct {
	ptr unsafe.Pointer
	mu  sync.Mutex // serializes FFI calls on the single-threaded native handle

	// stateMu guards the abandonment bookkeeping below. It is a cheap
	// secondary lock that is never held across FFI calls, so lifecycle fast
	// paths can consult it without waiting for an in-flight query holding mu.
	stateMu   sync.Mutex
	abandoned bool
	ctxCalls  int // in-flight QueryWithContext background calls

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
	runtime.SetFinalizer(t, (*Transaction).finalize)
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

// finalize is a garbage-collection backstop for transactions that were never
// committed, rolled back, or closed. Deterministic cleanup remains the
// contract; the finalizer reports the leak and frees the native handle
// (through the async close worker when possible) so that the server-side
// transaction is not left open until the server times it out.
func (t *Transaction) finalize() {
	job := t.detachCloseJob(time.Now(), nil)
	if job.ptr == nil {
		return
	}
	slog.Warn("typedb_go.tx.finalizer.leak", "tx_id", t.id, "db", t.dbName, "tx_type", int(t.txType))
	if t.closer.enqueue(job) {
		return
	}
	C.typedb_transaction_drop(job.ptr)
}

// abandon marks the transaction abandoned after a context cancellation while
// a background query call is still executing. Ownership of the native handle
// transfers to that in-flight goroutine, which frees the handle when the
// driver call returns; every later lifecycle call returns immediately. If no
// background call is in flight, abandon is a no-op and the normal close path
// stays responsible for the handle.
func (t *Transaction) abandon() {
	t.stateMu.Lock()
	if t.ctxCalls > 0 {
		t.abandoned = true
	}
	t.stateMu.Unlock()
}

func (t *Transaction) isAbandoned() bool {
	t.stateMu.Lock()
	defer t.stateMu.Unlock()
	return t.abandoned
}

// beginContextCall registers a background QueryWithContext call. It reports
// false if the transaction has already been abandoned.
func (t *Transaction) beginContextCall() bool {
	t.stateMu.Lock()
	defer t.stateMu.Unlock()
	if t.abandoned {
		return false
	}
	t.ctxCalls++
	return true
}

// finishContextCall unregisters a background QueryWithContext call. If the
// transaction was abandoned while the call ran and this is the last in-flight
// call, it frees the native handle the abandoning caller left behind.
func (t *Transaction) finishContextCall() {
	t.stateMu.Lock()
	t.ctxCalls--
	cleanup := t.abandoned && t.ctxCalls == 0
	t.stateMu.Unlock()
	if !cleanup {
		return
	}

	t.mu.Lock()
	job := transactionCloseJob{id: t.id, dbName: t.dbName, txType: t.txType, start: time.Now()}
	if t.ptr != nil {
		job.ptr = t.ptr
		t.ptr = nil
		t.markClosedLocked("abandoned")
	}
	t.mu.Unlock()

	if job.ptr == nil {
		return
	}
	logFFIDebug("tx.abandoned.close", "tx_id", t.id, "db", t.dbName, "tx_type", int(t.txType))
	if t.closer.enqueue(job) {
		return
	}
	C.typedb_transaction_drop(job.ptr)
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

// IsOpen returns true if the transaction is active and has not been committed,
// rolled back, closed, or abandoned by a cancelled QueryWithContext call.
func (t *Transaction) IsOpen() bool {
	if t.isAbandoned() {
		return false
	}
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

func (t *Transaction) query(query string, opts *QueryOptions, rows *GivenRows, logExtra ...any) ([]map[string]any, error) {
	var results []map[string]any
	err := t.queryDecoded(query, opts, rows, func(buf *C.uchar, outLen C.size_t) (int, error) {
		var decodeErr error
		results, decodeErr = decodeMsgpack(buf, outLen)
		return len(results), decodeErr
	}, logExtra...)
	return results, err
}

type queryDecoder func(buf *C.uchar, outLen C.size_t) (int, error)

const queryStreamChunkRows = 256

func (t *Transaction) queryDecoded(
	query string,
	opts *QueryOptions,
	rows *GivenRows,
	decode queryDecoder,
	logExtra ...any,
) error {
	start := time.Now()
	queryOp := queryOperation(query)
	queryFP := queryFingerprint(query)
	logFields := append([]any{"with_options", opts != nil, "with_rows", rows != nil}, logExtra...)

	if t.isAbandoned() {
		t.logQueryDuration(start, query, queryOp, queryFP, 0, 0, ErrTransactionAbandoned, logFields...)
		return ErrTransactionAbandoned
	}

	var rowsJSON []byte
	var err error
	if rows != nil {
		rowsJSON, err = rows.json()
		if err != nil {
			err = withQuery(err, query)
			t.logQueryDuration(start, query, queryOp, queryFP, 0, 0, err, logFields...)
			return err
		}
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	if t.ptr == nil {
		t.logQueryDuration(start, query, queryOp, queryFP, 0, 0, ErrNotConnected, logFields...)
		return ErrNotConnected
	}

	cQuery := C.CString(query)
	defer C.free(unsafe.Pointer(cQuery))

	var cOpts unsafe.Pointer
	registerConcepts := false
	if opts != nil {
		cOpts = opts.ptr
		registerConcepts = opts.conceptHandles
	}

	incActiveTxQuery("tx_id", t.id, "db", t.dbName, "tx_type", int(t.txType), "query_op", queryOp, "query_fingerprint", queryFP, "reason", "start")
	defer decActiveTxQuery("tx_id", t.id, "db", t.dbName, "tx_type", int(t.txType), "query_op", queryOp, "query_fingerprint", queryFP, "reason", "finish")

	var outLen C.size_t
	var queryErr *C.char
	var buf *C.uchar
	if rows != nil {
		cRows := C.CString(string(rowsJSON))
		defer C.free(unsafe.Pointer(cRows))
		buf = C.typedb_transaction_query_with_rows(t.ptr, cQuery, cOpts, cRows, C.bool(registerConcepts), &outLen, &queryErr)
	} else {
		buf = C.typedb_transaction_query(t.ptr, cQuery, cOpts, C.bool(registerConcepts), &outLen, &queryErr)
	}
	if buf == nil {
		if err := withQuery(getError(queryErr), query); err != nil {
			t.logQueryDuration(start, query, queryOp, queryFP, 0, 0, err, logFields...)
			return err
		}
		t.logQueryDuration(start, query, queryOp, queryFP, 0, 0, nil, logFields...)
		return nil
	}
	defer C.typedb_free_bytes((*C.uchar)(unsafe.Pointer(buf)), outLen)
	rowCount, err := decode(buf, outLen)
	err = withQuery(err, query)
	if err != nil {
		t.logQueryDuration(start, query, queryOp, queryFP, 0, int(outLen), err, logFields...)
		return err
	}
	t.logQueryDuration(start, query, queryOp, queryFP, rowCount, int(outLen), nil, logFields...)
	return nil
}

func (t *Transaction) queryEach(
	query string,
	fn func(rowCount int, row map[string]any) error,
	logExtra ...any,
) error {
	start := time.Now()
	queryOp := queryOperation(query)
	queryFP := queryFingerprint(query)
	logFields := append([]any{"row_consumer", true}, logExtra...)

	if t.isAbandoned() {
		t.logQueryDuration(start, query, queryOp, queryFP, 0, 0, ErrTransactionAbandoned, logFields...)
		return ErrTransactionAbandoned
	}

	t.mu.Lock()
	defer t.mu.Unlock()
	if t.ptr == nil {
		t.logQueryDuration(start, query, queryOp, queryFP, 0, 0, ErrNotConnected, logFields...)
		return ErrNotConnected
	}

	cQuery := C.CString(query)
	defer C.free(unsafe.Pointer(cQuery))

	incActiveTxQuery("tx_id", t.id, "db", t.dbName, "tx_type", int(t.txType), "query_op", queryOp, "query_fingerprint", queryFP, "reason", "start")
	defer decActiveTxQuery("tx_id", t.id, "db", t.dbName, "tx_type", int(t.txType), "query_op", queryOp, "query_fingerprint", queryFP, "reason", "finish")

	var queryErr *C.char
	stream := C.typedb_transaction_query_stream_open(t.ptr, cQuery, nil, false, &queryErr)
	if stream == nil {
		err := withQuery(getError(queryErr), query)
		if err == nil {
			err = withQuery(ErrNilPointer, query)
		}
		t.logQueryDuration(start, query, queryOp, queryFP, 0, 0, err, logFields...)
		return err
	}
	defer C.typedb_query_stream_drop(stream)

	rowCount := 0
	byteCount := 0
	for {
		var chunkRows C.size_t
		var done C.bool
		var outLen C.size_t
		var nextErr *C.char
		buf := C.typedb_query_stream_next(
			stream,
			C.size_t(queryStreamChunkRows),
			&chunkRows,
			&done,
			&outLen,
			&nextErr,
		)
		if err := withQuery(getError(nextErr), query); err != nil {
			t.logQueryDuration(start, query, queryOp, queryFP, rowCount, byteCount, err, logFields...)
			return err
		}

		if buf != nil {
			decodedRows, err := decodeMsgpackEach(buf, outLen, fn)
			C.typedb_free_bytes((*C.uchar)(unsafe.Pointer(buf)), outLen)
			if err != nil {
				err = withQuery(err, query)
				t.logQueryDuration(start, query, queryOp, queryFP, rowCount, byteCount, err, logFields...)
				return err
			}
			if decodedRows != int(chunkRows) {
				err := withQuery(fmt.Errorf("driver: decoded %d rows from a %d-row query chunk", decodedRows, uint64(chunkRows)), query)
				t.logQueryDuration(start, query, queryOp, queryFP, rowCount, byteCount, err, logFields...)
				return err
			}
			rowCount += decodedRows
			byteCount += int(outLen)
		} else if outLen != 0 || chunkRows != 0 {
			err := withQuery(ErrNilPointer, query)
			t.logQueryDuration(start, query, queryOp, queryFP, rowCount, byteCount, err, logFields...)
			return err
		}

		if bool(done) {
			t.logQueryDuration(start, query, queryOp, queryFP, rowCount, byteCount, nil, logFields...)
			return nil
		}
	}
}

// QueryEachWithContext executes a TypeQL query and calls fn for each result.
// The rowCount argument is the number of results in the current stream chunk.
// The driver reuses the row map, so fn must not retain it.
func (t *Transaction) QueryEachWithContext(
	ctx context.Context,
	query string,
	fn func(rowCount int, row map[string]any) error,
) error {
	if fn == nil {
		return fmt.Errorf("driver: row function must not be nil")
	}
	queryOp := queryOperation(query)
	queryFP := queryFingerprint(query)
	if deadline, ok := ctx.Deadline(); ok {
		logFFIDebug("tx.query_each_with_context.start", "tx_id", t.id, "db", t.dbName, "tx_type", int(t.txType), "query_len", len(query), "query_op", queryOp, "query_fingerprint", queryFP, "deadline_remaining_ms", time.Until(deadline).Milliseconds())
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	var callbackMu sync.Mutex
	consume := func(rowCount int, row map[string]any) error {
		callbackMu.Lock()
		defer callbackMu.Unlock()
		if err := ctx.Err(); err != nil {
			return err
		}
		return fn(rowCount, row)
	}
	run := func(logExtra ...any) error {
		return t.queryEach(query, consume, logExtra...)
	}
	if ctx.Done() == nil {
		return run()
	}
	if !t.beginContextCall() {
		return ErrTransactionAbandoned
	}

	ch := make(chan error, 1)
	go func() {
		err := run("with_context", true, "row_consumer", true)
		t.finishContextCall()
		ch <- err
	}()

	select {
	case <-ctx.Done():
		t.abandon()
		// Wait for an active callback. Later callbacks observe the cancelled
		// context before they call user code.
		callbackMu.Lock()
		callbackMu.Unlock()
		logFFIDebug("tx.query_each_with_context.cancelled", "tx_id", t.id, "db", t.dbName, "tx_type", int(t.txType), "query_len", len(query), "query_op", queryOp, "query_fingerprint", queryFP, "error", ctx.Err().Error())
		return ctx.Err()
	case err := <-ch:
		return err
	}
}

// QueryWithContext executes a TypeQL query with context cancellation support
// and default query options. It is equivalent to QueryWithContextAndOptions
// with nil options and rows; see that method for the cancellation semantics.
func (t *Transaction) QueryWithContext(ctx context.Context, query string) ([]map[string]any, error) {
	return t.QueryWithContextAndOptions(ctx, query, nil, nil)
}

// QueryWithContextAndOptions executes a TypeQL query with context cancellation
// support, query options, and optional typed input rows for a given stage.
// Nil opts and rows keep the driver defaults.
//
// Cancellation semantics are intentionally limited by the underlying Rust
// driver handle:
//   - The transaction handle is single-threaded, so every FFI call must hold
//     the transaction mutex for the duration of the synchronous C call.
//   - If ctx is cancelled, the caller is released early with ctx.Err() and the
//     transaction is abandoned. The in-flight FFI call continues in the
//     background until the driver returns, at which point the native handle
//     is freed through the async close worker.
//   - Once abandoned, Commit, Rollback, Close, CloseAsync, CloseChecked, and
//     IsOpen return immediately (with ErrTransactionAbandoned where an error
//     is returned) instead of blocking behind the in-flight call, so a
//     deferred Close after a cancelled query does not block.
//
// If ctx is cancelled, the background call may keep using opts and rows until
// the driver returns; do not call opts.Close until the transaction's pending
// closes have drained (see WaitForPendingCloses).
func (t *Transaction) QueryWithContextAndOptions(ctx context.Context, query string, opts *QueryOptions, rows *GivenRows) ([]map[string]any, error) {
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
		return t.query(query, opts, rows)
	}
	if !t.beginContextCall() {
		return nil, ErrTransactionAbandoned
	}

	type queryResult struct {
		results []map[string]any
		err     error
	}
	ch := make(chan queryResult, 1)

	// The goroutine exists only to let the caller stop waiting on a blocking
	// FFI call. It does not make the underlying driver operation
	// interruptible.
	go func() {
		results, err := t.query(query, opts, rows, "with_context", true)
		t.finishContextCall()
		ch <- queryResult{results: results, err: err}
	}()

	select {
	case <-ctx.Done():
		t.abandon()
		logFFIDebug("tx.query_with_context.cancelled", "tx_id", t.id, "db", t.dbName, "tx_type", int(t.txType), "query_len", len(query), "query_op", queryOp, "query_fingerprint", queryFP, "error", ctx.Err().Error())
		return nil, ctx.Err()
	case res := <-ch:
		return res.results, res.err
	}
}

// decodeMsgpack decodes a MessagePack byte buffer into a slice of maps.
// It decodes directly from the C buffer without an intermediate Go copy; the
// caller must keep the buffer alive until decodeMsgpack returns (the deferred
// typedb_free_bytes in the query path does). The msgpack decoder copies all
// decoded values into freshly allocated Go memory.
func decodeMsgpack(buf *C.uchar, outLen C.size_t) ([]map[string]any, error) {
	if err := checkMsgpackBufferLen(uint64(outLen)); err != nil {
		return nil, err
	}
	var view []byte
	if buf != nil && outLen > 0 {
		view = unsafe.Slice((*byte)(unsafe.Pointer(buf)), int(outLen))
	}
	return decodeMsgpackBytes(view)
}

func decodeMsgpackEach(
	buf *C.uchar,
	outLen C.size_t,
	fn func(rowCount int, row map[string]any) error,
) (int, error) {
	if err := checkMsgpackBufferLen(uint64(outLen)); err != nil {
		return 0, err
	}
	var view []byte
	if buf != nil && outLen > 0 {
		view = unsafe.Slice((*byte)(unsafe.Pointer(buf)), int(outLen))
	}
	return decodeMsgpackEachBytes(view, fn)
}

func checkMsgpackBufferLen(n uint64) error {
	if n > math.MaxInt {
		return &DriverError{Message: fmt.Sprintf("query result buffer of %d bytes exceeds the maximum decodable size", n)}
	}
	return nil
}

func decodeMsgpackBytes(data []byte) ([]map[string]any, error) {
	var reader bytes.Reader
	reader.Reset(data)

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

func decodeMsgpackEachBytes(
	data []byte,
	fn func(rowCount int, row map[string]any) error,
) (int, error) {
	var reader bytes.Reader
	reader.Reset(data)

	dec := msgpackDecoderPool.Get().(*msgpack.Decoder)
	defer msgpackDecoderPool.Put(dec)
	dec.Reset(&reader)
	dec.UseLooseInterfaceDecoding(true)

	rowCount, err := dec.DecodeArrayLen()
	if err != nil {
		return 0, &DriverError{Message: "failed to decode msgpack query results: " + err.Error()}
	}
	row := make(map[string]any)
	for range rowCount {
		clear(row)
		fieldCount, err := dec.DecodeMapLen()
		if err != nil {
			return 0, &DriverError{Message: "failed to decode msgpack query result: " + err.Error()}
		}
		for range fieldCount {
			key, err := dec.DecodeString()
			if err != nil {
				return 0, &DriverError{Message: "failed to decode msgpack query result key: " + err.Error()}
			}
			value, err := dec.DecodeInterfaceLoose()
			if err != nil {
				return 0, &DriverError{Message: "failed to decode msgpack query result value: " + err.Error()}
			}
			row[key] = value
		}
		if err := fn(rowCount, row); err != nil {
			return 0, err
		}
	}
	return rowCount, nil
}

// Commit persists the changes made in the transaction to the database.
// Whether Commit succeeds or fails, the underlying Rust transaction handle is
// consumed and cannot be reused, rolled back, or closed again meaningfully.
// Commit on a transaction abandoned by a cancelled QueryWithContext call
// returns ErrTransactionAbandoned immediately.
func (t *Transaction) Commit() error {
	start := time.Now()
	if t.isAbandoned() {
		logFFIDuration("tx.commit", start, "tx_id", t.id, "db", t.dbName, "tx_type", int(t.txType), "result", "error", "error", ErrTransactionAbandoned.Error())
		return ErrTransactionAbandoned
	}
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
// Rollback on a transaction abandoned by a cancelled QueryWithContext call
// returns ErrTransactionAbandoned immediately.
func (t *Transaction) Rollback() error {
	start := time.Now()
	if t.isAbandoned() {
		logFFIDuration("tx.rollback", start, "tx_id", t.id, "db", t.dbName, "tx_type", int(t.txType), "result", "error", "error", ErrTransactionAbandoned.Error())
		return ErrTransactionAbandoned
	}
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
// waiting for the checked TypeDB close to complete. If onDone is non-nil, it
// is called exactly once in every case:
//   - if the checked close is queued, when that close finishes, with its
//     result;
//   - if the close queue is full, with nil, after the transaction is dropped
//     locally (no checked close result is available);
//   - if the transaction was already committed, rolled back, closed, or
//     abandoned, with nil, before CloseAsync returns.
func (t *Transaction) CloseAsync(onDone func(error)) {
	start := time.Now()
	job := t.detachCloseJob(start, onDone)
	if job.ptr == nil {
		if onDone != nil {
			onDone(nil)
		}
		return
	}
	if t.closer.enqueue(job) {
		return
	}

	C.typedb_transaction_drop(job.ptr)
	if job.onDone != nil {
		job.onDone(nil)
	}
}

// CloseChecked terminates the transaction synchronously and returns the checked
// TypeDB close error, if any. It returns nil immediately when the transaction
// was already committed, rolled back, closed, or abandoned.
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

// detachCloseJob transfers ownership of the native handle to the caller for
// closing. It returns the zero job when there is nothing to close: the
// transaction already ended, or it was abandoned (in which case the in-flight
// query goroutine owns the handle and frees it when the driver call returns).
func (t *Transaction) detachCloseJob(start time.Time, onDone func(error)) transactionCloseJob {
	if t.isAbandoned() {
		return transactionCloseJob{}
	}

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
