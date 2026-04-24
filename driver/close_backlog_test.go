//go:build cgo && typedb && integration

package driver

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"
)

func closeBacklogTestAddr() string {
	if addr := os.Getenv("TEST_DB_ADDRESS"); addr != "" {
		return addr
	}
	return "localhost:1730"
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
	}
	loopElapsed := time.Since(start)

	drainStart := time.Now()
	if err := WaitForPendingCloses(context.Background()); err != nil {
		t.Fatalf("drain pending closes: %v", err)
	}
	drainElapsed := time.Since(drainStart)
	t.Logf("iterations=%d loop_elapsed=%s peak_pending=%d drain_elapsed=%s drain_rate=%.1f closes/s",
		iterations,
		loopElapsed,
		peak,
		drainElapsed,
		float64(iterations)/drainElapsed.Seconds(),
	)
}
