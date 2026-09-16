//go:build cgo && typedb && integration

package driver

import (
	"context"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"
)

func closeBacklogTestAddr() string {
	if addr := os.Getenv("TEST_DB_ADDRESS"); addr != "" {
		return addr
	}
	return "localhost:1730"
}

func TestConcurrentReadCleanupAdmissionBound(t *testing.T) {
	conn, err := OpenWithTLS(closeBacklogTestAddr(), "admin", "password", false, "")
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	name := fmt.Sprintf("concurrent_cleanup_%d", time.Now().UnixNano())
	if err := conn.Databases().Create(name); err != nil {
		t.Fatal(err)
	}
	defer conn.Databases().Delete(name)
	const callers, iterations = 10, 25
	var wg sync.WaitGroup
	errors := make(chan error, callers)
	maxNative := 0
	var mu sync.Mutex
	for range callers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range iterations {
				tx, err := conn.Transaction(name, Read)
				if err != nil {
					errors <- err
					return
				}
				if _, err := tx.Query(`match attribute $a; fetch { "label": label($a) };`); err != nil {
					tx.Close()
					errors <- err
					return
				}
				tx.Close()
				mu.Lock()
				maxNative = max(maxNative, conn.CleanupStats().NativeInUse)
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	close(errors)
	for err := range errors {
		t.Error(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := WaitForPendingCloses(ctx); err != nil {
		t.Fatal(err)
	}
	if stats := conn.CleanupStats(); stats.Pending != 0 || stats.NativeInUse != 0 || maxNative > 16 {
		t.Fatalf("unfinished native handles exceeded bound or failed to drain: peak=%d final=%+v", maxNative, stats)
	}
}

func pendingCloseCount() int {
	pendingTransactionCloses.mu.Lock()
	defer pendingTransactionCloses.mu.Unlock()
	return pendingTransactionCloses.pending
}

func TestTransactionCloseBacklogUnderReadLoad(t *testing.T) {
	conn, err := OpenWithTLS(closeBacklogTestAddr(), "admin", "password", false, "")
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer conn.Close()

	dbName := fmt.Sprintf("close_backlog_%d", time.Now().UnixNano())
	dm := conn.Databases()
	_ = dm.Delete(dbName)
	if err := dm.Create(dbName); err != nil {
		t.Fatalf("create db: %v", err)
	}
	defer dm.Delete(dbName)

	tx, err := conn.Transaction(dbName, Schema)
	if err != nil {
		t.Fatalf("open schema tx: %v", err)
	}
	if _, err := tx.Query("define attribute name, value string;"); err != nil {
		tx.Close()
		t.Fatalf("define schema: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit schema: %v", err)
	}

	query := `match attribute $a; fetch { "label": label($a) };`
	const iterations = 200
	peak := 0
	peakNative := 0
	start := time.Now()
	for i := 0; i < iterations; i++ {
		tx, err := conn.Transaction(dbName, Read)
		if err != nil {
			t.Fatalf("open read tx %d: %v", i, err)
		}
		if _, err := tx.Query(query); err != nil {
			tx.Close()
			t.Fatalf("query %d: %v", i, err)
		}
		tx.Close()
		if pending := pendingCloseCount(); pending > peak {
			peak = pending
		}
		peakNative = max(peakNative, conn.CleanupStats().NativeInUse)
	}
	loopElapsed := time.Since(start)

	drainStart := time.Now()
	drainCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := WaitForPendingCloses(drainCtx); err != nil {
		t.Fatalf("drain pending closes: %v", err)
	}
	drainElapsed := time.Since(drainStart)
	if peak > 16 || peakNative > 16 || conn.CleanupStats().NativeInUse != 0 {
		t.Fatalf("native cleanup exceeded the 16-handle per-driver admission bound (pending=%d native=%d final=%+v)", peak, peakNative, conn.CleanupStats())
	}
	t.Logf("iterations=%d loop_elapsed=%s peak_pending=%d peak_native=%d drain_elapsed=%s drain_rate=%.1f closes/s",
		iterations,
		loopElapsed,
		peak,
		peakNative,
		drainElapsed,
		float64(iterations)/drainElapsed.Seconds(),
	)
}
