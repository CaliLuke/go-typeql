//go:build cgo && typedb

package driver

import (
	"context"
	"errors"
	"math"
	"testing"
	"time"
	"unsafe"

	"github.com/vmihailenco/msgpack/v5"
)

// fakeHandle returns a non-nil pointer that stands in for a native transaction
// handle in tests that never reach an FFI call.
func fakeHandle() unsafe.Pointer {
	return unsafe.Pointer(new(byte))
}

// TestCloseAsyncAlreadyClosedInvokesCallback covers issue #95: onDone must be
// called exactly once even when the transaction has nothing left to close.
func TestCloseAsyncAlreadyClosedInvokesCallback(t *testing.T) {
	tx := &Transaction{} // already closed: no native handle
	done := make(chan error, 1)
	tx.CloseAsync(func(err error) { done <- err })

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("expected nil close result for already-closed transaction, got: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("CloseAsync never invoked onDone for an already-closed transaction")
	}

	if err := tx.CloseChecked(); err != nil {
		t.Fatalf("CloseChecked on already-closed transaction: %v", err)
	}
	tx.Close() // must not panic or block
}

// TestCloseAsyncAbandonedInvokesCallback covers issues #42 and #95: closing an
// abandoned transaction must return immediately, invoke onDone, and leave the
// native handle to the in-flight query goroutine.
func TestCloseAsyncAbandonedInvokesCallback(t *testing.T) {
	tx := &Transaction{ptr: fakeHandle()}
	if !tx.beginContextCall() {
		t.Fatal("beginContextCall on a fresh transaction should succeed")
	}
	tx.abandon()
	if !tx.isAbandoned() {
		t.Fatal("transaction should be abandoned after abandon with an in-flight call")
	}

	done := make(chan error, 1)
	start := time.Now()
	tx.CloseAsync(func(err error) { done <- err })
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("CloseAsync on abandoned transaction took %s, expected immediate return", elapsed)
	}

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("expected nil close result for abandoned transaction, got: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("CloseAsync never invoked onDone for an abandoned transaction")
	}

	if tx.ptr == nil {
		t.Fatal("abandoned transaction handle must stay owned by the in-flight call, not the closer")
	}
	if err := tx.CloseChecked(); err != nil {
		t.Fatalf("CloseChecked on abandoned transaction: %v", err)
	}
}

// TestAbandonRequiresInFlightCall verifies that a cancellation observed after
// the background call already finished does not abandon the transaction (the
// normal close path stays responsible for the handle).
func TestAbandonRequiresInFlightCall(t *testing.T) {
	tx := &Transaction{ptr: fakeHandle()}

	tx.abandon()
	if tx.isAbandoned() {
		t.Fatal("abandon without an in-flight context call must be a no-op")
	}

	if !tx.beginContextCall() {
		t.Fatal("beginContextCall should succeed before abandonment")
	}
	tx.finishContextCall()
	tx.abandon()
	if tx.isAbandoned() {
		t.Fatal("abandon after the context call finished must be a no-op")
	}

	// The normal close path still owns the handle.
	job := tx.detachCloseJob(time.Now(), nil)
	if job.ptr == nil {
		t.Fatal("detachCloseJob should hand out the handle of a non-abandoned transaction")
	}
	if again := tx.detachCloseJob(time.Now(), nil); again.ptr != nil {
		t.Fatal("detachCloseJob must be idempotent")
	}
}

// TestAbandonedLifecycleFastPaths covers issue #42: once abandoned, lifecycle
// calls must return immediately instead of blocking behind the in-flight call.
func TestAbandonedLifecycleFastPaths(t *testing.T) {
	tx := &Transaction{ptr: fakeHandle()}
	if !tx.beginContextCall() {
		t.Fatal("beginContextCall should succeed")
	}
	tx.abandon()

	if err := tx.Commit(); !errors.Is(err, ErrTransactionAbandoned) {
		t.Fatalf("Commit on abandoned transaction: got %v, want ErrTransactionAbandoned", err)
	}
	if err := tx.Rollback(); !errors.Is(err, ErrTransactionAbandoned) {
		t.Fatalf("Rollback on abandoned transaction: got %v, want ErrTransactionAbandoned", err)
	}
	if tx.IsOpen() {
		t.Fatal("IsOpen on abandoned transaction must be false")
	}
	if _, err := tx.Query("match $x isa thing;"); !errors.Is(err, ErrTransactionAbandoned) {
		t.Fatalf("Query on abandoned transaction: got %v, want ErrTransactionAbandoned", err)
	}
	if _, err := tx.QueryWithContext(context.Background(), "match $x isa thing;"); !errors.Is(err, ErrTransactionAbandoned) {
		t.Fatalf("QueryWithContext (background ctx) on abandoned transaction: got %v, want ErrTransactionAbandoned", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if _, err := tx.QueryWithContext(ctx, "match $x isa thing;"); !errors.Is(err, ErrTransactionAbandoned) {
		t.Fatalf("QueryWithContext (cancellable ctx) on abandoned transaction: got %v, want ErrTransactionAbandoned", err)
	}
	if tx.beginContextCall() {
		t.Fatal("beginContextCall must fail on an abandoned transaction")
	}
}

// TestFinishContextCallFreesAbandonedHandle verifies that the last in-flight
// context call transfers the abandoned handle to the async close worker.
func TestFinishContextCallFreesAbandonedHandle(t *testing.T) {
	// A worker whose run loop is not started, so the enqueued job can be
	// inspected instead of being executed against the fake handle.
	worker := &transactionCloseWorker{
		jobs: make(chan transactionCloseJob, 1),
		done: make(chan struct{}),
	}
	handle := fakeHandle()
	tx := &Transaction{ptr: handle, closer: worker}

	if !tx.beginContextCall() {
		t.Fatal("beginContextCall should succeed")
	}
	if !tx.beginContextCall() {
		t.Fatal("second beginContextCall should succeed")
	}
	tx.abandon()

	tx.finishContextCall()
	if tx.ptr == nil {
		t.Fatal("handle must not be freed while another context call is still in flight")
	}

	tx.finishContextCall()
	if tx.ptr != nil {
		t.Fatal("last finishContextCall must detach the abandoned handle")
	}

	select {
	case job := <-worker.jobs:
		if job.ptr != handle {
			t.Fatalf("close job carries wrong handle: got %p, want %p", job.ptr, handle)
		}
		// Rebalance the global pending-close tracker: the job was accepted by
		// enqueue but this test consumes it instead of the worker loop.
		pendingTransactionCloses.done()
	default:
		t.Fatal("abandoned handle was not enqueued on the close worker")
	}
}

// TestQueryWithContextAndOptionsAlreadyCancelled verifies the pre-FFI fast
// path of the options-aware context query.
func TestQueryWithContextAndOptionsAlreadyCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	tx := &Transaction{}
	if _, err := tx.QueryWithContextAndOptions(ctx, "match $x isa thing;", nil, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got: %v", err)
	}
	if tx.isAbandoned() {
		t.Fatal("a pre-cancelled call must not abandon the transaction")
	}
}

// TestCheckMsgpackBufferLen covers issue #70: buffer lengths beyond the
// addressable int range must error instead of truncating through C.int.
func TestCheckMsgpackBufferLen(t *testing.T) {
	if err := checkMsgpackBufferLen(0); err != nil {
		t.Fatalf("zero-length buffer should be accepted: %v", err)
	}
	if err := checkMsgpackBufferLen(uint64(math.MaxInt32) + 1); err != nil {
		t.Fatalf("buffers past the old C.int limit must now be accepted: %v", err)
	}
	if err := checkMsgpackBufferLen(uint64(math.MaxInt)); err != nil {
		t.Fatalf("math.MaxInt bytes should pass the guard: %v", err)
	}
	if err := checkMsgpackBufferLen(uint64(math.MaxInt) + 1); err == nil {
		t.Fatal("buffer larger than math.MaxInt must be rejected")
	}
}

func TestDecodeMsgpackBytesRoundTrip(t *testing.T) {
	want := []map[string]any{
		{"name": "Alice", "age": int8(30)},
		{"name": "Bob", "value": map[string]any{"nested": true}},
	}
	data, err := msgpack.Marshal(want)
	if err != nil {
		t.Fatalf("marshal fixture: %v", err)
	}

	got, err := decodeMsgpackBytes(data)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got) != len(want) {
		t.Fatalf("row count: got %d, want %d", len(got), len(want))
	}
	if got[0]["name"] != "Alice" || got[1]["name"] != "Bob" {
		t.Fatalf("unexpected decode result: %#v", got)
	}

	if _, err := decodeMsgpackBytes([]byte{0xc1}); err == nil {
		t.Fatal("invalid msgpack input must return an error")
	}

	empty, err := decodeMsgpackBytes(nil)
	if err == nil && empty != nil {
		t.Fatalf("nil input should not decode to rows: %#v", empty)
	}
}
