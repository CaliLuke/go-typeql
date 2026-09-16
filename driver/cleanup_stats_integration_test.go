//go:build cgo && typedb && integration

package driver

import (
	"context"
	"fmt"
	"testing"
	"time"
)

func TestCleanupStatsNativeLifecycle(t *testing.T) {
	conn, err := OpenWithTLS(closeBacklogTestAddr(), "admin", "password", false, "")
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	name := fmt.Sprintf("cleanup_stats_%d", time.Now().UnixNano())
	if err := conn.Databases().Create(name); err != nil {
		t.Fatal(err)
	}
	defer conn.Databases().Delete(name)

	async, err := conn.Transaction(name, Read)
	if err != nil {
		t.Fatal(err)
	}
	type completed struct {
		err   error
		stats TransactionCleanupStats
	}
	callback := make(chan completed, 1)
	async.CloseAsync(func(err error) { callback <- completed{err: err, stats: conn.CleanupStats()} })
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := WaitForPendingCloses(ctx); err != nil {
		t.Fatal(err)
	}
	observed := <-callback
	if observed.err != nil {
		t.Fatal(observed.err)
	}
	if observed.stats.Pending != 0 || observed.stats.NativeCompletions != 1 {
		t.Fatalf("callback observed unfinished cleanup: %+v", observed.stats)
	}
	first := conn.CleanupStats()
	if first.Pending != 0 || first.Queued != 0 || first.NativeCompletions != 1 || first.NativeFailures != 0 {
		t.Fatalf("async cleanup: %+v", first)
	}

	syncTx, err := conn.Transaction(name, Read)
	if err != nil {
		t.Fatal(err)
	}
	if err := syncTx.CloseChecked(); err != nil {
		t.Fatal(err)
	}
	second := conn.CleanupStats()
	if second.Pending != 0 || second.NativeCompletions != 2 || second.NativeCloseTotal <= first.NativeCloseTotal {
		t.Fatalf("synchronous cleanup: %+v (previous %+v)", second, first)
	}

	// Force admission failure without a fake native pointer. The transaction
	// still belongs to this driver; only its close worker is marked closed.
	fallback, err := conn.Transaction(name, Read)
	if err != nil {
		t.Fatal(err)
	}
	fallback.closer = &transactionCloseWorker{closed: true, cleanup: conn.cleanup}
	fallbackDone := make(chan TransactionCleanupStats, 1)
	fallback.CloseAsync(func(error) { fallbackDone <- conn.CleanupStats() })
	fallback.Close() // repeated close must not double-count the fallback
	if observed := <-fallbackDone; observed.Pending != 0 || observed.QueueFullFallbacks != 1 || observed.NativeCompletions != 2 {
		t.Fatalf("failed-admission callback counts: %+v", observed)
	}
	if final := conn.CleanupStats(); final.Pending != 0 || final.QueueFullFallbacks != 1 || final.NativeCompletions != 2 || final.NativeInUse != 0 {
		t.Fatalf("repeated close changed counts: %+v", final)
	}
}
