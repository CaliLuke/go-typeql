//go:build cgo && typedb && integration

package driver

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"
)

func TestNativeAdmissionHeldThroughAsyncCleanup(t *testing.T) {
	conn, err := OpenWithOptions(closeBacklogTestAddr(), "admin", "password", DriverOptions{MaxNativeTransactions: 1})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	name := fmt.Sprintf("native_admission_%d", time.Now().UnixNano())
	if err := conn.Databases().Create(name); err != nil {
		t.Fatal(err)
	}
	defer conn.Databases().Delete(name)

	tx, err := conn.Transaction(name, Read)
	if err != nil {
		t.Fatal(err)
	}
	if got := conn.CleanupStats(); got.NativeInUse != 1 || got.NativeCapacity != 1 {
		t.Fatalf("open handle usage: %+v", got)
	}
	// Keep the real native handle in a deterministic queue without a worker.
	// It is closed below; no fake pointer ever crosses FFI.
	worker := &transactionCloseWorker{jobs: make(chan transactionCloseJob, 1), cleanup: conn.cleanup}
	tx.closer = worker
	tx.Close()
	defer func() {
		select {
		case job := <-worker.jobs:
			worker.cleanup.dequeued(job)
			runTransactionCloseJob(job)
			pendingTransactionCloses.done()
		default:
		}
	}()
	if got := conn.CleanupStats(); got.Pending != 1 || got.Queued != 1 || got.NativeInUse != 1 {
		t.Fatalf("detached handle released admission early: %+v", got)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := conn.TransactionWithContextAndOptions(ctx, name, Read, nil); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected cancellable admission wait, got %v", err)
	}
	job := <-worker.jobs
	worker.cleanup.dequeued(job)
	runTransactionCloseJob(job)
	pendingTransactionCloses.done()
	if got := conn.CleanupStats(); got.Pending != 0 || got.NativeInUse != 0 {
		t.Fatalf("native completion did not release admission: %+v", got)
	}
	next, err := conn.Transaction(name, Read)
	if err != nil {
		t.Fatalf("open after native cleanup: %v", err)
	}
	if err := next.CloseChecked(); err != nil {
		t.Fatal(err)
	}
	if got := conn.CleanupStats(); got.NativeInUse != 0 || got.NativeCompletions != 2 {
		t.Fatalf("checked close did not release admission: %+v", got)
	}
}

func TestNativeAdmissionWaiterWakesOnDriverClose(t *testing.T) {
	admin, err := OpenWithTLS(closeBacklogTestAddr(), "admin", "password", false, "")
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	name := fmt.Sprintf("native_close_waiter_%d", time.Now().UnixNano())
	if err := admin.Databases().Create(name); err != nil {
		t.Fatal(err)
	}
	defer admin.Databases().Delete(name)
	conn, err := OpenWithOptions(closeBacklogTestAddr(), "admin", "password", DriverOptions{MaxNativeTransactions: 1})
	if err != nil {
		t.Fatal(err)
	}
	tx, err := conn.Transaction(name, Read)
	if err != nil {
		conn.Close()
		t.Fatal(err)
	}
	result := make(chan error, 1)
	go func() {
		_, err := conn.TransactionWithContextAndOptions(context.Background(), name, Read, nil)
		result <- err
	}()
	conn.Close() // drains tx, wakes the waiting open, and releases its slot
	if err := <-result; !errors.Is(err, ErrNotConnected) {
		t.Fatalf("waiting open after driver close: %v", err)
	}
	if usage := conn.CleanupStats(); usage.NativeInUse != 0 || usage.Pending != 0 {
		t.Fatalf("shutdown retained native handles: %+v", usage)
	}
	if tx.IsOpen() {
		t.Fatal("driver close left transaction open")
	}
}

func TestDriverCloseWaitsForDetachedNativeOwner(t *testing.T) {
	conn, err := OpenWithTLS(closeBacklogTestAddr(), "admin", "password", false, "")
	if err != nil {
		t.Fatal(err)
	}
	// Model a detached CloseChecked or abandoned query after registry removal:
	// it still owns native work, but the driver's open-tx list cannot see it.
	conn.nativeWG.Add(1)
	done := make(chan struct{})
	go func() { conn.Close(); close(done) }()
	select {
	case <-done:
		t.Fatal("driver freed while detached native work remained")
	case <-time.After(20 * time.Millisecond):
	}
	conn.nativeWG.Done()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("driver did not finish after detached native work completed")
	}
}

func TestDriverCloseClaimsOrphanedNativeHandle(t *testing.T) {
	conn, err := OpenWithOptions(closeBacklogTestAddr(), "admin", "password", DriverOptions{MaxNativeTransactions: 1})
	if err != nil {
		t.Fatal(err)
	}
	name := fmt.Sprintf("orphaned_close_%d", time.Now().UnixNano())
	if err := conn.Databases().Create(name); err != nil {
		conn.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanup, err := OpenWithTLS(closeBacklogTestAddr(), "admin", "password", false, "")
		if err != nil {
			t.Errorf("open cleanup connection: %v", err)
			return
		}
		defer cleanup.Close()
		if err := cleanup.Databases().Delete(name); err != nil {
			t.Errorf("delete cleanup database: %v", err)
		}
	})
	tx, err := conn.Transaction(name, Read)
	if err != nil {
		conn.Close()
		t.Fatal(err)
	}
	// Simulate the interval after weak.Value becomes nil but before the
	// finalizer runs. Keeping tx reachable here also checks double-close safety.
	conn.txMu.Lock()
	delete(conn.txs, tx.id)
	conn.txMu.Unlock()
	done := make(chan struct{})
	go func() { conn.Close(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("shutdown waited for a missing weak transaction")
	}
	tx.Close()
	if got := conn.CleanupStats(); got.NativeInUse != 0 || got.Pending != 0 || got.NativeCompletions != 1 {
		t.Fatalf("orphaned native handle was not reclaimed once: %+v", got)
	}
}

func TestNativeAdmissionFailedOpensDrainOnShutdown(t *testing.T) {
	conn, err := OpenWithOptions(closeBacklogTestAddr(), "admin", "password", DriverOptions{MaxNativeTransactions: 2})
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if tx, err := conn.Transaction("no_such_database_for_admission", Read); err == nil {
				tx.Close()
				t.Error("unexpected successful open")
			}
		}()
	}
	conn.Close()
	wg.Wait()
	if usage := conn.CleanupStats(); usage.NativeInUse != 0 || usage.Pending != 0 {
		t.Fatalf("failed opens left admission occupied after shutdown: %+v", usage)
	}
}

// TestReadCloseDropsAndReleasesAdmission checks DropReadClose: Close drops the
// read transaction and returns its slot before Close returns, so the next open
// does not wait. A write transaction still uses the checked close.
func TestReadCloseDropsAndReleasesAdmission(t *testing.T) {
	conn, err := OpenWithOptions(closeBacklogTestAddr(), "admin", "password", DriverOptions{MaxNativeTransactions: 1, DropReadClose: true})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	name := fmt.Sprintf("read_drop_%d", time.Now().UnixNano())
	if err := conn.Databases().Create(name); err != nil {
		t.Fatal(err)
	}
	defer conn.Databases().Delete(name)

	for i := range 3 {
		tx, err := conn.Transaction(name, Read)
		if err != nil {
			t.Fatal(err)
		}
		tx.Close()
		if got := conn.CleanupStats(); got.NativeInUse != 0 || got.Pending != 0 || got.ReadDrops != uint64(i+1) {
			t.Fatalf("read close %d: %+v", i, got)
		}
	}
	// One slot, and it is free: an immediate open must not wait.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	write, err := conn.TransactionWithContextAndOptions(ctx, name, Write, nil)
	if err != nil {
		t.Fatalf("open after read drops: %v", err)
	}
	write.Close()
	if err := WaitForPendingCloses(ctx); err != nil {
		t.Fatal(err)
	}
	if got := conn.CleanupStats(); got.NativeCompletions != 1 || got.ReadDrops != 3 || got.NativeInUse != 0 {
		t.Fatalf("write close must stay checked: %+v", got)
	}
}
