//go:build cgo && typedb

package driver

// #include "typedb_ffi.h"
import "C"
import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"math"
	"runtime"
	"strings"
	"sync"
	"time"
	"unsafe"

	"github.com/CaliLuke/go-typeql/v3/given"
	"github.com/CaliLuke/go-typeql/v3/internal/perftrace"

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

	// The stream retains native ownership while callbacks run without mu.
	streaming            bool
	streamCloseRequested bool
	streamCloseCallbacks []func(error)

	// stateMu guards the abandonment bookkeeping below. It is a cheap
	// secondary lock that is never held across FFI calls, so lifecycle fast
	// paths can consult it without waiting for an in-flight query holding mu.
	stateMu   sync.Mutex
	abandoned bool
	ctxCalls  int // in-flight QueryWithContext background calls

	id                uint64
	dbName            string
	txType            TransactionType
	opened            bool
	owner             *Driver
	closer            *transactionCloseWorker
	lease             *nativeHandleLease
	slotOnce          sync.Once
	releaseNativeSlot func()

	// traceCtx parents the spans of calls that take no context (commit,
	// rollback, close, Query). It is nil unless perftrace is enabled.
	traceCtx context.Context
}

// A lease does not retain its Transaction, so finalizers can still run. It
// allows Driver.Close to claim a handle after its weak Transaction reference
// expires but before the finalizer gets scheduled on the finalizer goroutine.
type nativeHandleLease struct {
	mu      sync.Mutex
	ptr     unsafe.Pointer
	release func()
	dbName  string
	txType  TransactionType
}

func (l *nativeHandleLease) claim() unsafe.Pointer {
	l.mu.Lock()
	defer l.mu.Unlock()
	ptr := l.ptr
	l.ptr = nil
	return ptr
}

func (t *Transaction) releaseSlot() {
	if t.releaseNativeSlot != nil {
		t.slotOnce.Do(t.releaseNativeSlot)
	}
}

func (t *Transaction) claimNativeHandle() unsafe.Pointer {
	if t.lease != nil {
		return t.lease.claim()
	}
	return t.ptr
}

type transactionCloseJob struct {
	ptr       unsafe.Pointer
	id        uint64
	cleanupID uint64
	cleanup   *transactionCleanupTracker
	enqueued  time.Time
	dbName    string
	txType    TransactionType
	start     time.Time
	onDone    func(error)
	release   func()
	traceCtx  context.Context
}

func (job transactionCloseJob) releaseSlot() {
	if job.release != nil {
		job.release()
	}
}

type transactionCloseWorker struct {
	mu      sync.Mutex
	jobs    chan transactionCloseJob
	done    chan struct{}
	closed  bool
	cleanup *transactionCleanupTracker
}

const transactionCloseQueueSize = 1024

// TransactionCleanupStats is a per-driver snapshot of native transaction
// cleanup. Pending includes queued, running, and synchronous closes after the
// caller has relinquished the handle; it excludes still-running abandoned
// queries until their native handle can be detached. Durations are cumulative.
// QueueFullFallbacks includes failed admission after driver shutdown.
type TransactionCleanupStats struct {
	Pending            int
	Queued             int
	NativeInUse        int
	NativeCapacity     int
	OldestPendingAge   time.Duration
	NativeCompletions  uint64
	NativeFailures     uint64
	QueueFullFallbacks uint64
	// ReadDrops counts read transactions that Close or CloseAsync dropped
	// without a checked native close (see DriverOptions.DropReadClose).
	ReadDrops        uint64
	QueueWaitTotal   time.Duration
	NativeCloseTotal time.Duration
}

type transactionCleanupTracker struct {
	mu      sync.Mutex
	next    uint64
	started map[uint64]time.Time
	stats   TransactionCleanupStats
}

func newTransactionCleanupTracker() *transactionCleanupTracker {
	return &transactionCleanupTracker{started: make(map[uint64]time.Time)}
}

func (c *transactionCleanupTracker) begin(job *transactionCloseJob) {
	if c == nil || job.ptr == nil {
		return
	}
	c.mu.Lock()
	c.next++
	job.cleanupID = c.next
	job.cleanup = c
	c.started[job.cleanupID] = time.Now()
	c.stats.Pending++
	c.mu.Unlock()
}

func (c *transactionCleanupTracker) enqueued() {
	if c == nil {
		return
	}
	c.mu.Lock()
	c.stats.Queued++
	c.mu.Unlock()
}

func (c *transactionCleanupTracker) enqueueFailed() {
	if c == nil {
		return
	}
	c.mu.Lock()
	c.stats.Queued--
	c.mu.Unlock()
}

func (c *transactionCleanupTracker) dequeued(job transactionCloseJob) {
	if c == nil {
		return
	}
	c.mu.Lock()
	c.stats.Queued--
	c.stats.QueueWaitTotal += time.Since(job.enqueued)
	c.mu.Unlock()
}

// finishDrop records a read transaction dropped without a checked close.
func (c *transactionCleanupTracker) finishDrop(job transactionCloseJob) {
	if c == nil || job.cleanupID == 0 {
		return
	}
	c.mu.Lock()
	delete(c.started, job.cleanupID)
	c.stats.Pending--
	c.stats.ReadDrops++
	c.mu.Unlock()
}

func (c *transactionCleanupTracker) finish(job transactionCloseJob, native bool, err error, duration time.Duration, fallback bool) {
	if c == nil || job.cleanupID == 0 {
		return
	}
	c.mu.Lock()
	delete(c.started, job.cleanupID)
	c.stats.Pending--
	if native {
		c.stats.NativeCompletions++
		if err != nil {
			c.stats.NativeFailures++
		}
		c.stats.NativeCloseTotal += duration
	}
	if fallback {
		c.stats.QueueFullFallbacks++
	}
	c.mu.Unlock()
}

func (c *transactionCleanupTracker) snapshot() TransactionCleanupStats {
	if c == nil {
		return TransactionCleanupStats{}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	stats := c.stats
	now := time.Now()
	for _, started := range c.started {
		stats.OldestPendingAge = max(stats.OldestPendingAge, now.Sub(started))
	}
	return stats
}

// CleanupStats returns this driver's cleanup counts, including after Close
// drains its worker. Other drivers have independent counters.
func (d *Driver) CleanupStats() TransactionCleanupStats {
	stats := d.cleanup.snapshot()
	if d.nativeSlots != nil {
		stats.NativeCapacity = cap(d.nativeSlots)
		stats.NativeInUse = cap(d.nativeSlots) - len(d.nativeSlots)
	}
	return stats
}

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

// defaultCloseWorkers is the number of goroutines that run native closes when
// DriverOptions.CloseWorkers is zero. One worker caps throughput at one close
// per close round trip: queued closes then hold native-handle slots, and
// transaction opens wait for admission (benchmarks/reviews/2026-09-25).
const defaultCloseWorkers = 8

// closeWorkerCount resolves DriverOptions.CloseWorkers. With admission on,
// more workers than native-handle slots cannot run, because each queued
// close holds a slot.
func closeWorkerCount(requested, maxNative int) int {
	n := requested
	if n <= 0 {
		n = defaultCloseWorkers
	}
	if maxNative > 0 && n > maxNative {
		n = maxNative
	}
	return n
}

// newTransactionCloseWorker starts n goroutines that drain one close queue.
// Each job owns a distinct native handle, so jobs can run concurrently
// (formal/tla/TxHandle.tla models two workers).
func newTransactionCloseWorker(cleanup *transactionCleanupTracker, n int) *transactionCloseWorker {
	w := &transactionCloseWorker{
		jobs:    make(chan transactionCloseJob, transactionCloseQueueSize),
		done:    make(chan struct{}),
		cleanup: cleanup,
	}
	var wg sync.WaitGroup
	for range n {
		wg.Go(w.run)
	}
	go func() {
		wg.Wait()
		close(w.done)
	}()
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
	job.enqueued = time.Now()
	w.cleanup.enqueued()
	select {
	case w.jobs <- job:
		return true
	default:
		w.cleanup.enqueueFailed()
		pendingTransactionCloses.done()
		return false
	}
}

func (w *transactionCloseWorker) run() {
	for job := range w.jobs {
		w.cleanup.dequeued(job)
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
	err := closeNative(job, false)
	if job.onDone != nil {
		job.onDone(err)
	}
}

// closeNative runs the checked native close of a detached handle, records
// it, and releases the admission slot. A close worker calls it for queued
// jobs; CloseChecked calls it inline.
func closeNative(job transactionCloseJob, inline bool) error {
	start := time.Now()
	_, span := perftrace.Start(job.traceCtx, "typedb.tx.close")
	if span.Recording() {
		span.SetAttrs(
			perftrace.Int("typedb.tx.id", int(job.id)),
			perftrace.String("db.namespace", job.dbName),
			perftrace.Bool("typedb.tx.close.inline", inline),
			perftrace.Int("typedb.tx.close.delay_us", int(start.Sub(job.start).Microseconds())),
		)
	}
	var closeErr *C.char
	C.typedb_transaction_close(job.ptr, &closeErr)
	duration := time.Since(start)
	err := getError(closeErr)
	span.End(err)
	logTransactionClose(job, err)
	job.cleanup.finish(job, true, err, duration, false)
	job.releaseSlot()
	return err
}

func logTransactionClose(job transactionCloseJob, err error) {
	logFFIDurationLazy("tx.close", job.start, func() []any {
		fields := []any{"tx_id", job.id, "db", job.dbName, "tx_type", int(job.txType)}
		if err != nil {
			return append(fields, "result", "error", "error", err.Error())
		}
		return append(fields, "result", "ok")
	})
}

func (t *Transaction) logTransactionDuration(event string, start time.Time, err error) {
	logFFIDurationLazy(event, start, func() []any {
		fields := []any{"tx_id", t.id, "db", t.dbName, "tx_type", int(t.txType)}
		if err != nil {
			return append(fields, "result", "error", "error", err.Error())
		}
		return append(fields, "result", "ok")
	})
}

// WaitForPendingCloses waits for already accepted asynchronous transaction close
// jobs to finish. It is a drain point for tests and graceful shutdown; it does
// not stop close workers.
func WaitForPendingCloses(ctx context.Context) error {
	return pendingTransactionCloses.wait(ctx)
}

func newTransaction(ptr unsafe.Pointer, id uint64, dbName string, txType TransactionType, owner *Driver, closer *transactionCloseWorker) *Transaction {
	t := &Transaction{ptr: ptr, id: id, dbName: dbName, txType: txType, opened: true, owner: owner, closer: closer}
	incActiveTxOpenLazy(func() []any { return []any{"tx_id", id, "db", dbName, "tx_type", int(txType), "reason", "open"} })
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
	decActiveTxOpenLazy(func() []any { return []any{"tx_id", t.id, "db", t.dbName, "tx_type", int(t.txType), "reason", reason} })
}

// finalize is a garbage-collection backstop for transactions that were never
// committed, rolled back, or closed. Deterministic cleanup remains the
// contract; the finalizer reports the leak and frees the native handle
// (through the async close worker when possible) so that the server-side
// transaction is not left open until the server times it out.
func (t *Transaction) finalize() {
	job, err := t.detachCloseJob(time.Now(), nil)
	if err != nil || job.ptr == nil {
		return
	}
	slog.Warn("typedb_go.tx.finalizer.leak", "tx_id", t.id, "db", t.dbName, "tx_type", int(t.txType))
	if t.closer.enqueue(job) {
		return
	}
	C.typedb_transaction_drop(job.ptr)
	job.cleanup.finish(job, false, nil, 0, true)
	job.releaseSlot()
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
	// A background stream can be inside a callback with mu released. An
	// abandoned nested call must leave cleanup to that stream's owner.
	if t.streaming {
		t.mu.Unlock()
		return
	}
	job := transactionCloseJob{id: t.id, dbName: t.dbName, txType: t.txType, start: time.Now(), release: t.releaseSlot}
	if t.ptr != nil {
		job.ptr = t.claimNativeHandle()
		t.ptr = nil
		t.markClosedLocked("abandoned")
	}
	t.mu.Unlock()

	if job.ptr == nil {
		return
	}
	logFFIDebugLazy("tx.abandoned.close", func() []any { return []any{"tx_id", t.id, "db", t.dbName, "tx_type", int(t.txType)} })
	if t.closer != nil {
		t.closer.cleanup.begin(&job)
	}
	if t.closer.enqueue(job) {
		return
	}
	C.typedb_transaction_drop(job.ptr)
	job.cleanup.finish(job, false, nil, 0, true)
	job.releaseSlot()
}

type queryMetadata struct {
	query       string
	ctx         context.Context // caller context for spans; nil without one
	once        sync.Once
	operation   string
	fingerprint string
}

func (m *queryMetadata) values() (string, string) {
	m.once.Do(func() {
		m.operation = queryOperation(m.query)
		m.fingerprint = queryFingerprint(m.query)
	})
	return m.operation, m.fingerprint
}

func (t *Transaction) queryLogFields(meta *queryMetadata, extra ...any) []any {
	op, fp := meta.values()
	fields := []any{"tx_id", t.id, "db", t.dbName, "tx_type", int(t.txType), "query_len", len(meta.query), "query_op", op, "query_fingerprint", fp}
	return append(fields, extra...)
}

func (t *Transaction) logQueryDuration(start time.Time, meta *queryMetadata, rows int, byteCount int, err error, extra func() []any) {
	logFFIDurationLazy("tx.query", start, func() []any {
		fields := t.queryLogFields(meta)
		fields = append(fields, extra()...)
		if err != nil {
			return append(fields, "result", "error", "error", err.Error())
		}
		return append(fields, "result", "ok", "rows", rows, "bytes", byteCount)
	})
}

// IsOpen returns true if the transaction is active and has not been committed,
// rolled back, closed, or abandoned by a cancelled QueryWithContext call.
func (t *Transaction) IsOpen() bool {
	if t.isAbandoned() {
		return false
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.ptr == nil || t.streamCloseRequested {
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
func (t *Transaction) QueryWithRows(query string, rows given.Rows) ([]map[string]any, error) {
	return t.query(query, nil, rows)
}

// QueryWithOptionsAndRows executes a TypeQL query with query options and typed
// input rows for a given stage.
func (t *Transaction) QueryWithOptionsAndRows(query string, opts *QueryOptions, rows given.Rows) ([]map[string]any, error) {
	return t.query(query, opts, rows)
}

func (t *Transaction) query(query string, opts *QueryOptions, rows given.Rows, logExtra ...any) ([]map[string]any, error) {
	return t.queryWithMeta(&queryMetadata{query: query}, opts, rows, logExtra...)
}

func (t *Transaction) queryWithMeta(meta *queryMetadata, opts *QueryOptions, rows given.Rows, logExtra ...any) ([]map[string]any, error) {
	var results []map[string]any
	err := t.queryDecoded(meta, opts, rows, func(buf *C.uchar, outLen C.size_t) (int, error) {
		var decodeErr error
		results, decodeErr = decodeMsgpack(buf, outLen)
		return len(results), decodeErr
	}, logExtra...)
	return results, err
}

type queryDecoder func(buf *C.uchar, outLen C.size_t) (int, error)

const queryStreamChunkRows = 256

func (t *Transaction) queryDecoded(
	meta *queryMetadata,
	opts *QueryOptions,
	rows given.Rows,
	decode queryDecoder,
	logExtra ...any,
) (err error) {
	query := meta.query
	start := time.Now()
	ctx, span := t.startQuerySpan(meta, "typedb.tx.query")
	defer func() { span.End(err) }()
	if nilGivenRows(rows) {
		rows = nil
	}
	logFields := func() []any { return append([]any{"with_options", opts != nil, "with_rows", rows != nil}, logExtra...) }

	if t.isAbandoned() {
		t.logQueryDuration(start, meta, 0, 0, ErrTransactionAbandoned, logFields)
		return ErrTransactionAbandoned
	}

	var rowsJSON []byte
	if rows != nil {
		rowsJSON, err = rows.MarshalGivenRows()
		if err != nil {
			err = withQuery(err, query)
			t.logQueryDuration(start, meta, 0, 0, err, logFields)
			return err
		}
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	if t.streaming {
		return ErrTransactionBusy
	}
	if t.ptr == nil {
		t.logQueryDuration(start, meta, 0, 0, ErrNotConnected, logFields)
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

	incActiveTxQueryLazy(func() []any { return t.queryLogFields(meta, "reason", "start") })
	defer decActiveTxQueryLazy(func() []any { return t.queryLogFields(meta, "reason", "finish") })

	var outLen C.size_t
	var queryErr *C.char
	var buf *C.uchar
	_, ffiSpan := perftrace.Start(ctx, "typedb.ffi.query")
	if rows != nil {
		cRows := C.CString(string(rowsJSON))
		defer C.free(unsafe.Pointer(cRows))
		buf = C.typedb_transaction_query_with_rows(t.ptr, cQuery, cOpts, cRows, C.bool(registerConcepts), &outLen, &queryErr)
	} else {
		buf = C.typedb_transaction_query(t.ptr, cQuery, cOpts, C.bool(registerConcepts), &outLen, &queryErr)
	}
	if ffiSpan.Recording() {
		ffiSpan.SetAttrs(perftrace.Int("typedb.result.bytes", int(outLen)))
	}
	ffiSpan.End(nil)
	if buf == nil {
		if err := withQuery(getError(queryErr), query); err != nil {
			t.logQueryDuration(start, meta, 0, 0, err, logFields)
			return err
		}
		t.logQueryDuration(start, meta, 0, 0, nil, logFields)
		return nil
	}
	defer C.typedb_free_bytes((*C.uchar)(unsafe.Pointer(buf)), outLen)
	_, decodeSpan := perftrace.Start(ctx, "typedb.decode")
	rowCount, err := decode(buf, outLen)
	if decodeSpan.Recording() {
		decodeSpan.SetAttrs(perftrace.Int("typedb.result.rows", rowCount), perftrace.Int("typedb.result.bytes", int(outLen)))
		span.SetAttrs(perftrace.Int("typedb.result.rows", rowCount), perftrace.Int("typedb.result.bytes", int(outLen)))
	}
	decodeSpan.End(err)
	err = withQuery(err, query)
	if err != nil {
		t.logQueryDuration(start, meta, 0, int(outLen), err, logFields)
		return err
	}
	t.logQueryDuration(start, meta, rowCount, int(outLen), nil, logFields)
	return nil
}

func (t *Transaction) queryEach(
	query string,
	fn func(rowCount int, row map[string]any) error,
	logExtra ...any,
) error {
	return t.queryEachWithMeta(&queryMetadata{query: query}, fn, logExtra...)
}

func (t *Transaction) queryEachWithMeta(
	meta *queryMetadata,
	fn func(rowCount int, row map[string]any) error,
	logExtra ...any,
) error {
	return t.queryEachConfigured(meta, nil, queryStreamChunkRows, nil, fn, logExtra...)
}

// queryEachConfigured keeps the public streaming defaults unchanged while
// allowing in-package benchmarks to vary the FFI row limit and server prefetch
// independently. onChunk observes each native buffer before it is freed.
func (t *Transaction) queryEachConfigured(
	meta *queryMetadata,
	opts *QueryOptions,
	chunkLimit int,
	onChunk func(rows, bytes int),
	fn func(rowCount int, row map[string]any) error,
	logExtra ...any,
) (err error) {
	if chunkLimit <= 0 {
		return fmt.Errorf("driver: query stream chunk size must be positive")
	}
	query := meta.query
	start := time.Now()
	ctx, span := t.startQuerySpan(meta, "typedb.tx.query_stream")
	defer func() { span.End(err) }()
	logFields := func() []any { return append([]any{"row_consumer", true}, logExtra...) }

	if t.isAbandoned() {
		t.logQueryDuration(start, meta, 0, 0, ErrTransactionAbandoned, logFields)
		return ErrTransactionAbandoned
	}

	t.mu.Lock()
	if t.streaming {
		t.mu.Unlock()
		return ErrTransactionBusy
	}
	if t.ptr == nil {
		t.mu.Unlock()
		t.logQueryDuration(start, meta, 0, 0, ErrNotConnected, logFields)
		return ErrNotConnected
	}
	t.streaming = true
	defer t.finishStream()

	cQuery := C.CString(query)
	defer C.free(unsafe.Pointer(cQuery))

	incActiveTxQueryLazy(func() []any { return t.queryLogFields(meta, "reason", "start") })
	defer decActiveTxQueryLazy(func() []any { return t.queryLogFields(meta, "reason", "finish") })

	var queryErr *C.char
	var cOpts unsafe.Pointer
	if opts != nil {
		cOpts = opts.ptr
	}
	_, openSpan := perftrace.Start(ctx, "typedb.ffi.stream_open")
	stream := C.typedb_transaction_query_stream_open(t.ptr, cQuery, cOpts, false, &queryErr)
	openSpan.End(nil)
	if stream == nil {
		err := withQuery(getError(queryErr), query)
		if err == nil {
			err = withQuery(ErrNilPointer, query)
		}
		t.logQueryDuration(start, meta, 0, 0, err, logFields)
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
		_, nextSpan := perftrace.Start(ctx, "typedb.ffi.stream_next")
		buf := C.typedb_query_stream_next(
			stream,
			C.size_t(chunkLimit),
			&chunkRows,
			&done,
			&outLen,
			&nextErr,
		)
		if nextSpan.Recording() {
			nextSpan.SetAttrs(perftrace.Int("typedb.result.rows", int(chunkRows)), perftrace.Int("typedb.result.bytes", int(outLen)))
		}
		nextSpan.End(nil)
		if err := withQuery(getError(nextErr), query); err != nil {
			t.logQueryDuration(start, meta, rowCount, byteCount, err, logFields)
			return err
		}

		if buf != nil {
			if onChunk != nil {
				onChunk(int(chunkRows), int(outLen))
			}
			// The span includes the row callbacks, for example hydration.
			_, decodeSpan := perftrace.Start(ctx, "typedb.decode_consume")
			decodedRows, err := func() (int, error) {
				defer C.typedb_free_bytes(buf, outLen)
				return decodeMsgpackEach(buf, outLen, func(count int, row map[string]any) error {
					return t.consumeStreamRow(fn, count, row)
				})
			}()
			if decodeSpan.Recording() {
				decodeSpan.SetAttrs(perftrace.Int("typedb.result.rows", decodedRows))
			}
			decodeSpan.End(err)
			if err != nil {
				err = withQuery(err, query)
				t.logQueryDuration(start, meta, rowCount, byteCount, err, logFields)
				return err
			}
			if decodedRows != int(chunkRows) {
				err := withQuery(fmt.Errorf("driver: decoded %d rows from a %d-row query chunk", decodedRows, uint64(chunkRows)), query)
				t.logQueryDuration(start, meta, rowCount, byteCount, err, logFields)
				return err
			}
			rowCount += decodedRows
			byteCount += int(outLen)
		} else if outLen != 0 || chunkRows != 0 {
			err := withQuery(ErrNilPointer, query)
			t.logQueryDuration(start, meta, rowCount, byteCount, err, logFields)
			return err
		}

		if bool(done) {
			if span.Recording() {
				span.SetAttrs(perftrace.Int("typedb.result.rows", rowCount), perftrace.Int("typedb.result.bytes", byteCount))
			}
			t.logQueryDuration(start, meta, rowCount, byteCount, nil, logFields)
			return nil
		}
	}
}

// consumeStreamRow releases only the transaction mutex. The native stream
// stays owned by queryEachConfigured, and other mutating operations fail busy.
func (t *Transaction) consumeStreamRow(fn func(int, map[string]any) error, count int, row map[string]any) (err error) {
	if t.streamCloseRequested {
		return ErrNotConnected
	}
	t.mu.Unlock()
	defer func() {
		t.mu.Lock()
		if err == nil && t.streamCloseRequested {
			err = ErrNotConnected
		}
	}()
	return fn(count, row)
}

// finishStream runs with mu held, after the native stream has been dropped.
func (t *Transaction) finishStream() {
	t.streaming = false
	if !t.streamCloseRequested && !t.isAbandoned() {
		t.mu.Unlock()
		return
	}
	callbacks := t.streamCloseCallbacks
	t.streamCloseCallbacks = nil
	t.streamCloseRequested = false
	onDone := func(err error) {
		for _, callback := range callbacks {
			callback(err)
		}
	}
	job := t.detachCloseJobLocked(time.Now(), onDone)
	t.mu.Unlock()
	t.closeAsyncJob(job, onDone)
}

// QueryEachWithContext executes a TypeQL query and calls fn for each result.
// The rowCount argument is the number of results in the current stream chunk.
// The driver reuses the row map, so fn must not retain it.
// Callbacks can call IsOpen. Queries, Commit, Rollback, and CloseChecked on the
// same transaction return ErrTransactionBusy while the stream is active.
// Close and CloseAsync request a close after the current callback returns.
// The stream then stops with ErrNotConnected unless the callback returns an error.
// Cancellation waits for the current callback. No callback runs after this call returns.
func (t *Transaction) QueryEachWithContext(
	ctx context.Context,
	query string,
	fn func(rowCount int, row map[string]any) error,
) error {
	if fn == nil {
		return fmt.Errorf("driver: row function must not be nil")
	}
	meta := &queryMetadata{query: query, ctx: traceContext(ctx)}
	if deadline, ok := ctx.Deadline(); ok {
		logFFIDebugLazy("tx.query_each_with_context.start", func() []any {
			return t.queryLogFields(meta, "deadline_remaining_ms", time.Until(deadline).Milliseconds())
		})
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
		return t.queryEachWithMeta(meta, consume, logExtra...)
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
		logFFIDebugLazy("tx.query_each_with_context.cancelled", func() []any {
			return t.queryLogFields(meta, "error", ctx.Err().Error())
		})
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

// QueryWithContextAndRows executes a TypeQL query with context cancellation
// support and typed input rows for a given stage.
func (t *Transaction) QueryWithContextAndRows(ctx context.Context, query string, rows given.Rows) ([]map[string]any, error) {
	return t.QueryWithContextAndOptions(ctx, query, nil, rows)
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
func (t *Transaction) QueryWithContextAndOptions(ctx context.Context, query string, opts *QueryOptions, rows given.Rows) ([]map[string]any, error) {
	meta := &queryMetadata{query: query, ctx: traceContext(ctx)}
	if deadline, ok := ctx.Deadline(); ok {
		logFFIDebugLazy("tx.query_with_context.start", func() []any {
			return t.queryLogFields(meta, "deadline_remaining_ms", time.Until(deadline).Milliseconds())
		})
	}

	// Fast path: bail immediately if already cancelled
	if err := ctx.Err(); err != nil {
		logFFIDebugLazy("tx.query_with_context.cancelled", func() []any {
			return t.queryLogFields(meta, "error", err.Error())
		})
		return nil, err
	}
	if ctx.Done() == nil {
		return t.queryWithMeta(meta, opts, rows)
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
		results, err := t.queryWithMeta(meta, opts, rows, "with_context", true)
		t.finishContextCall()
		ch <- queryResult{results: results, err: err}
	}()

	select {
	case <-ctx.Done():
		t.abandon()
		logFFIDebugLazy("tx.query_with_context.cancelled", func() []any {
			return t.queryLogFields(meta, "error", ctx.Err().Error())
		})
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
	defer func() {
		// The map callback closes over this result buffer and its key cache.
		// Do not retain either through the pooled decoder.
		dec.Reset(nil)
		msgpackDecoderPool.Put(dec)
	}()
	dec.Reset(&reader)
	dec.UseLooseInterfaceDecoding(true)
	decodeKey := newMsgpackKeyDecoder(dec, &reader, data)
	decodeMap := func(d *msgpack.Decoder) (any, error) {
		n, err := d.DecodeMapLen()
		if err != nil || n < 0 {
			return map[string]any(nil), err
		}
		// A map32 header alone must not trigger a huge allocation. Each pair
		// needs at least two encoded bytes (key and value).
		row := make(map[string]any, min(n, 1_000_000, reader.Len()/2))
		for range n {
			key, err := decodeKey()
			if err != nil {
				return nil, err
			}
			value, err := d.DecodeInterfaceLoose()
			if err != nil {
				return nil, err
			}
			row[key] = value
		}
		return row, nil
	}
	dec.SetMapDecoder(decodeMap)

	n, err := dec.DecodeArrayLen()
	if err != nil {
		return nil, &DriverError{Message: "failed to decode msgpack query results: " + err.Error()}
	}
	if n < 0 {
		return nil, nil
	}
	results := make([]map[string]any, 0, min(n, 1_000_000, reader.Len()))
	for range n {
		row, err := decodeMap(dec)
		if err != nil {
			return nil, &DriverError{Message: "failed to decode msgpack query results: " + err.Error()}
		}
		results = append(results, row.(map[string]any))
	}
	return results, nil
}

// newMsgpackKeyDecoder interns a bounded number of field names per result
// buffer. Lookup strings may briefly alias native memory, but every returned
// string owns its bytes and remains valid after the caller frees the buffer.
func newMsgpackKeyDecoder(dec *msgpack.Decoder, reader *bytes.Reader, data []byte) func() (string, error) {
	keys := make(map[string]string)
	keyBytes := 0
	return func() (string, error) {
		n, err := dec.DecodeBytesLen()
		if err != nil {
			return "", err
		}
		if n <= 0 {
			return "", nil
		}
		if n > reader.Len() {
			return "", io.ErrUnexpectedEOF
		}
		start := len(data) - reader.Len()
		key := unsafe.String(&data[start], n)
		cached, ok := keys[key]
		if _, err := reader.Seek(int64(n), io.SeekCurrent); err != nil {
			return "", err
		}
		if ok {
			return cached, nil
		}
		owned := strings.Clone(key)
		if len(keys) < 256 && keyBytes+n <= 16<<10 {
			keys[owned] = owned
			keyBytes += n
		}
		return owned, nil
	}
}

func decodeMsgpackEachBytes(
	data []byte,
	fn func(rowCount int, row map[string]any) error,
) (int, error) {
	var reader bytes.Reader
	reader.Reset(data)

	dec := msgpackDecoderPool.Get().(*msgpack.Decoder)
	defer func() {
		dec.Reset(nil)
		msgpackDecoderPool.Put(dec)
	}()
	dec.Reset(&reader)
	dec.UseLooseInterfaceDecoding(true)
	decodeKey := newMsgpackKeyDecoder(dec, &reader, data)

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
			key, err := decodeKey()
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
// An active query stream returns ErrTransactionBusy without committing.
// Once the native commit starts, it consumes the underlying Rust transaction
// handle on success or failure. The transaction cannot be reused afterwards.
// Commit on a transaction abandoned by a cancelled QueryWithContext call
// returns ErrTransactionAbandoned immediately.
func (t *Transaction) Commit() (err error) {
	start := time.Now()
	span := t.startTxSpan("typedb.tx.commit")
	defer func() { span.End(err) }()
	if t.isAbandoned() {
		t.logTransactionDuration("tx.commit", start, ErrTransactionAbandoned)
		return ErrTransactionAbandoned
	}
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.streaming {
		return ErrTransactionBusy
	}
	if t.ptr == nil {
		t.logTransactionDuration("tx.commit", start, ErrNotConnected)
		return ErrNotConnected
	}

	var commitErr *C.char
	C.typedb_transaction_commit(t.ptr, &commitErr)
	t.claimNativeHandle()
	t.ptr = nil // consumed by commit
	t.markClosedLocked("commit")
	t.releaseSlot()
	if err := getError(commitErr); err != nil {
		t.logTransactionDuration("tx.commit", start, err)
		return err
	}
	t.logTransactionDuration("tx.commit", start, nil)
	return nil
}

// Rollback discards all changes made within the transaction.
// An active query stream returns ErrTransactionBusy without rolling back.
// Rollback on a transaction abandoned by a cancelled QueryWithContext call
// returns ErrTransactionAbandoned immediately.
func (t *Transaction) Rollback() (err error) {
	start := time.Now()
	span := t.startTxSpan("typedb.tx.rollback")
	defer func() { span.End(err) }()
	if t.isAbandoned() {
		t.logTransactionDuration("tx.rollback", start, ErrTransactionAbandoned)
		return ErrTransactionAbandoned
	}
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.streaming {
		return ErrTransactionBusy
	}
	if t.ptr == nil {
		t.logTransactionDuration("tx.rollback", start, ErrNotConnected)
		return ErrNotConnected
	}

	var rollbackErr *C.char
	C.typedb_transaction_rollback(t.ptr, &rollbackErr)
	if err := getError(rollbackErr); err != nil {
		t.logTransactionDuration("tx.rollback", start, err)
		return err
	}
	C.typedb_transaction_drop(t.ptr)
	t.claimNativeHandle()
	t.ptr = nil
	t.markClosedLocked("rollback")
	t.releaseSlot()
	t.logTransactionDuration("tx.rollback", start, nil)
	return nil
}

// Close terminates the transaction without committing any changes.
// It should be used in a 'defer' block to ensure resources are released.
// During a stream callback, it requests a close after the callback returns.
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
//
// During a stream callback, the close and onDone wait until the stream releases
// its native handle. Later rows are not delivered.
func (t *Transaction) CloseAsync(onDone func(error)) {
	start := time.Now()
	if t.isAbandoned() {
		if onDone != nil {
			onDone(nil)
		}
		return
	}
	t.mu.Lock()
	if t.streaming {
		t.streamCloseRequested = true
		if onDone != nil {
			t.streamCloseCallbacks = append(t.streamCloseCallbacks, onDone)
		}
		t.mu.Unlock()
		return
	}
	job := t.detachCloseJobLocked(start, onDone)
	t.mu.Unlock()
	t.closeAsyncJob(job, onDone)
}

func (t *Transaction) closeAsyncJob(job transactionCloseJob, onDone func(error)) {
	if job.ptr == nil {
		if onDone != nil {
			onDone(nil)
		}
		return
	}
	if job.txType == Read && t.owner != nil && t.owner.dropReadClose {
		dropReadTransaction(job)
		return
	}
	if t.closer.enqueue(job) {
		return
	}

	C.typedb_transaction_drop(job.ptr)
	job.cleanup.finish(job, false, nil, 0, true)
	job.releaseSlot()
	if job.onDone != nil {
		job.onDone(nil)
	}
}

// dropReadTransaction releases a detached read transaction without a checked
// close (DriverOptions.DropReadClose). The Rust driver sends the close to the
// server without waiting, so the admission slot returns at once.
func dropReadTransaction(job transactionCloseJob) {
	_, span := perftrace.Start(job.traceCtx, "typedb.tx.drop")
	if span.Recording() {
		span.SetAttrs(perftrace.Int("typedb.tx.id", int(job.id)), perftrace.String("db.namespace", job.dbName))
	}
	C.typedb_transaction_drop(job.ptr)
	span.End(nil)
	logTransactionClose(job, nil)
	job.cleanup.finishDrop(job)
	job.releaseSlot()
	if job.onDone != nil {
		job.onDone(nil)
	}
}

// CloseChecked terminates the transaction synchronously and returns the checked
// TypeDB close error, if any. It returns nil immediately when the transaction
// was already committed, rolled back, closed, or abandoned.
// It returns ErrTransactionBusy while a query stream is active.
func (t *Transaction) CloseChecked() error {
	start := time.Now()
	job, err := t.detachCloseJob(start, nil)
	if err != nil {
		return err
	}
	if job.ptr == nil {
		return nil
	}
	return closeNative(job, true)
}

// detachCloseJob transfers ownership of the native handle to the caller for
// closing. It returns the zero job when there is nothing to close: the
// transaction already ended, or it was abandoned (in which case the in-flight
// query goroutine owns the handle and frees it when the driver call returns).
func (t *Transaction) detachCloseJob(start time.Time, onDone func(error)) (transactionCloseJob, error) {
	if t.isAbandoned() {
		return transactionCloseJob{}, nil
	}

	t.mu.Lock()
	defer t.mu.Unlock()
	if t.streaming {
		return transactionCloseJob{}, ErrTransactionBusy
	}
	return t.detachCloseJobLocked(start, onDone), nil
}

func (t *Transaction) detachCloseJobLocked(start time.Time, onDone func(error)) transactionCloseJob {
	if t.ptr == nil {
		return transactionCloseJob{}
	}
	job := transactionCloseJob{
		ptr:      t.claimNativeHandle(),
		id:       t.id,
		dbName:   t.dbName,
		txType:   t.txType,
		start:    start,
		onDone:   onDone,
		release:  t.releaseSlot,
		traceCtx: t.traceCtx,
	}
	t.ptr = nil
	t.markClosedLocked("close")
	if t.closer != nil {
		t.closer.cleanup.begin(&job)
	}
	return job
}
