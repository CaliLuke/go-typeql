//go:build cgo && typedb && integration

package driver

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

func setupLifecycleDB(t *testing.T, schema string) (*Driver, string) {
	t.Helper()
	conn, err := OpenWithTLS(testAddr(), "admin", "password", false, "")
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(conn.Close)

	dbName := fmt.Sprintf("lifecycle_%s_%d", strings.ToLower(strings.ReplaceAll(t.Name(), "/", "_")), time.Now().UnixNano())
	if len(dbName) > 60 {
		dbName = dbName[:60]
	}
	dm := conn.Databases()
	if err := dm.Create(dbName); err != nil {
		t.Fatalf("create db: %v", err)
	}
	t.Cleanup(func() { _ = dm.Delete(dbName) })

	if schema != "" {
		tx, err := conn.Transaction(dbName, Schema)
		if err != nil {
			t.Fatalf("open schema tx: %v", err)
		}
		if _, err := tx.Query(schema); err != nil {
			tx.Close()
			t.Fatalf("define schema: %v", err)
		}
		if err := tx.Commit(); err != nil {
			t.Fatalf("commit schema: %v", err)
		}
	}
	return conn, dbName
}

// TestQueryWithContextAndOptions_Integration verifies that cancellation and
// query options compose (issue #69): options are honored on the context path
// and rows can be supplied alongside a context.
func TestQueryWithContextAndOptions_Integration(t *testing.T) {
	conn, dbName := setupLifecycleDB(t, `
define
  attribute name, value string;
  entity person, owns name @key;
`)

	writeTx, err := conn.Transaction(dbName, Write)
	if err != nil {
		t.Fatalf("open write tx: %v", err)
	}
	rows := NewGivenRows("n").MustAdd(StringGiven("Alice")).MustAdd(StringGiven("Bob"))
	ctx := context.Background()
	if _, err := writeTx.QueryWithContextAndOptions(ctx, `
given $n: string;
insert $p isa person, has name == $n;
`, nil, rows); err != nil {
		t.Fatalf("insert with context and rows: %v", err)
	}
	if err := writeTx.Commit(); err != nil {
		t.Fatalf("commit write: %v", err)
	}

	readTx, err := conn.Transaction(dbName, Read)
	if err != nil {
		t.Fatalf("open read tx: %v", err)
	}
	defer readTx.Close()

	opts := NewQueryOptions().SetPrefetchSize(4).SetIncludeInstanceTypes(true)
	defer opts.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	results, err := readTx.QueryWithContextAndOptions(ctx, `
match $p isa person, has name $n;
fetch { "name": $n };
`, opts, nil)
	if err != nil {
		t.Fatalf("query with context and options: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 rows, got %d: %#v", len(results), results)
	}
}

// TestQueryWithContextCancelDoesNotBlockClose is the issue #42 regression:
// after a cancelled QueryWithContext, the deferred Close (and Commit) must
// return immediately instead of blocking behind the abandoned FFI call.
func TestQueryWithContextCancelDoesNotBlockClose(t *testing.T) {
	conn, dbName := setupLifecycleDB(t, `
define
  attribute name, value string;
  entity person, owns name @key;
`)

	writeTx, err := conn.Transaction(dbName, Write)
	if err != nil {
		t.Fatalf("open write tx: %v", err)
	}
	var b strings.Builder
	b.WriteString("insert\n")
	for i := 0; i < 100; i++ {
		fmt.Fprintf(&b, "$p%d isa person, has name \"person-%d\";\n", i, i)
	}
	if _, err := writeTx.Query(b.String()); err != nil {
		t.Fatalf("insert people: %v", err)
	}
	if err := writeTx.Commit(); err != nil {
		t.Fatalf("commit inserts: %v", err)
	}

	// Bound the abandoned server-side call so the pending-close drain below
	// stays fast even if the server keeps computing. Keep this short: the
	// abandoned aggregation keeps consuming server memory until the timeout
	// fires, and a heavier/longer variant OOM-killed the compose container.
	txOpts := NewTransactionOptions().SetTimeout(5_000)
	defer txOpts.Close()
	baselineOpen := activeTxOpen.Load()
	tx, err := conn.TransactionWithOptions(dbName, Read, txOpts)
	if err != nil {
		t.Fatalf("open read tx: %v", err)
	}

	// A deliberately heavy cartesian aggregation (100^3 = 1M combinations)
	// that cannot complete before the context deadline fires. Sized to be
	// slow, not huge: 300^3 exhausted the compose container's memory.
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err = tx.QueryWithContext(ctx, `
match $a isa person; $b isa person; $c isa person;
reduce $count = count;
`)
	if err == nil {
		tx.Close()
		t.Skip("server finished the heavy query before cancellation; cannot exercise abandonment")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected context.DeadlineExceeded, got: %v", err)
	}

	// Lifecycle calls must not block behind the abandoned in-flight call.
	start := time.Now()
	if err := tx.Commit(); !errors.Is(err, ErrTransactionAbandoned) {
		t.Fatalf("Commit after cancellation: got %v, want ErrTransactionAbandoned", err)
	}
	tx.Close()
	if tx.IsOpen() {
		t.Fatal("IsOpen must be false after abandonment")
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("post-cancellation lifecycle calls took %s; they must not block on the abandoned call", elapsed)
	}

	// The background goroutine frees the handle once the driver returns:
	// activeTxOpen falls back to its baseline when the abandoned handle is
	// detached, and the enqueued close job then drains through the worker.
	deadline := time.Now().Add(60 * time.Second)
	for activeTxOpen.Load() > baselineOpen {
		if time.Now().After(deadline) {
			t.Fatal("abandoned transaction handle was never freed by the background goroutine")
		}
		time.Sleep(100 * time.Millisecond)
	}
	drainCtx, drainCancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer drainCancel()
	if err := WaitForPendingCloses(drainCtx); err != nil {
		t.Fatalf("pending closes did not drain after abandonment: %v", err)
	}
}

// TestDriverCloseClosesOpenTransactions covers issue #67: Driver.Close must
// free transactions the caller forgot to close.
func TestDriverCloseClosesOpenTransactions(t *testing.T) {
	conn, err := OpenWithTLS(testAddr(), "admin", "password", false, "")
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	dbName := fmt.Sprintf("lifecycle_driver_close_%d", time.Now().UnixNano())
	dm := conn.Databases()
	if err := dm.Create(dbName); err != nil {
		conn.Close()
		t.Fatalf("create db: %v", err)
	}
	defer func() {
		cleanup, err := OpenWithTLS(testAddr(), "admin", "password", false, "")
		if err == nil {
			_ = cleanup.Databases().Delete(dbName)
			cleanup.Close()
		}
	}()

	tx1, err := conn.Transaction(dbName, Read)
	if err != nil {
		t.Fatalf("open tx1: %v", err)
	}
	tx2, err := conn.Transaction(dbName, Write)
	if err != nil {
		t.Fatalf("open tx2: %v", err)
	}

	open, err := conn.HasOpenTransactions(dbName)
	if err != nil || !open {
		t.Fatalf("expected open transactions before Close (open=%v err=%v)", open, err)
	}

	conn.Close()

	if tx1.IsOpen() || tx2.IsOpen() {
		t.Fatal("transactions must be closed after Driver.Close")
	}

	drainCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := WaitForPendingCloses(drainCtx); err != nil {
		t.Fatalf("pending closes did not drain after Driver.Close: %v", err)
	}
}

// TestTransactionFinalizerFreesLeakedHandle covers the issue #67 backstop: a
// transaction that goes out of scope without being ended is reclaimed by the
// garbage-collection finalizer.
func TestTransactionFinalizerFreesLeakedHandle(t *testing.T) {
	conn, dbName := setupLifecycleDB(t, "")

	baseline := activeTxOpen.Load()

	func() {
		tx, err := conn.Transaction(dbName, Read)
		if err != nil {
			t.Fatalf("open tx: %v", err)
		}
		_ = tx // leaked: no Close, Commit, or Rollback
	}()

	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		runtime.GC()
		if activeTxOpen.Load() <= baseline {
			drainCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			if err := WaitForPendingCloses(drainCtx); err != nil {
				t.Fatalf("pending closes did not drain after finalizer: %v", err)
			}
			open, err := conn.HasOpenTransactions(dbName)
			if err != nil {
				t.Fatalf("HasOpenTransactions: %v", err)
			}
			if open {
				t.Fatal("leaked transaction still registered after finalizer ran")
			}
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("leaked transaction was never reclaimed by the finalizer backstop")
}

// TestConcurrentDriverOperations covers issue #71: transaction opens and
// database management calls proceed concurrently instead of serializing on a
// driver-wide mutex.
func TestConcurrentDriverOperations(t *testing.T) {
	conn, dbName := setupLifecycleDB(t, "define attribute name, value string;")

	const workers = 8
	const iterations = 10
	var wg sync.WaitGroup
	errCh := make(chan error, workers*iterations*2)

	for w := 0; w < workers; w++ {
		wg.Go(func() {
			for i := 0; i < iterations; i++ {
				tx, err := conn.Transaction(dbName, Read)
				if err != nil {
					errCh <- fmt.Errorf("open tx: %w", err)
					return
				}
				if _, err := tx.Query(`match attribute $a; fetch { "label": label($a) };`); err != nil {
					errCh <- fmt.Errorf("query: %w", err)
				}
				tx.Close()
				if _, err := conn.Databases().Contains(dbName); err != nil {
					errCh <- fmt.Errorf("contains: %w", err)
					return
				}
				if !conn.IsOpen() {
					errCh <- fmt.Errorf("driver reported closed under concurrent load")
					return
				}
			}
		})
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Error(err)
	}

	drainCtx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if err := WaitForPendingCloses(drainCtx); err != nil {
		t.Fatalf("pending closes did not drain: %v", err)
	}
}
