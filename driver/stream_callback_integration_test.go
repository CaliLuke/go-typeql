//go:build cgo && typedb && integration

package driver

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sync/atomic"
	"testing"
	"time"

	"github.com/CaliLuke/go-typeql/v2/internal/typeqlcheck"
)

const callbackQuery = `match $p isa person, has name $name; fetch { "name": $name };`

// A child process bounds regressions even when both the query and Close deadlock.
func TestQueryEachCallbackSafety(t *testing.T) {
	const childEnv = "GO_TYPEQL_CALLBACK_TEST_CHILD"
	if os.Getenv(childEnv) != "1" {
		ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestQueryEachCallbackSafety$", "-test.v")
		cmd.Env = append(os.Environ(), childEnv+"=1")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("callback regression subprocess: %v (deadline: %v)\n%s", err, ctx.Err(), out)
		}
		return
	}
	for _, deadline := range []bool{false, true} {
		t.Run(fmt.Sprintf("deadline_%v", deadline), func(t *testing.T) {
			conn, name := setupCallbackDB(t)
			ctx := context.Background()
			if deadline {
				var cancel context.CancelFunc
				ctx, cancel = context.WithTimeout(ctx, 5*time.Second)
				defer cancel()
			}
			t.Run("reentry", func(t *testing.T) { testCallbackReentry(t, conn, name, ctx) })
			t.Run("close", func(t *testing.T) { testCallbackClose(t, conn, name, ctx) })
			t.Run("error", func(t *testing.T) {
				tx, err := conn.Transaction(name, Read)
				if err != nil {
					t.Fatal(err)
				}
				defer tx.Close()
				want := errors.New("consumer stopped")
				seen := 0
				err = tx.QueryEachWithContext(ctx, callbackQuery, func(int, map[string]any) error { seen++; return want })
				if !errors.Is(err, want) || seen != 1 {
					t.Fatalf("error=%v seen=%d", err, seen)
				}
				if _, err := tx.Query(callbackQuery); err != nil {
					t.Fatalf("query after callback error: %v", err)
				}
				if err := tx.CloseChecked(); err != nil {
					t.Fatal(err)
				}
			})
			if stats := conn.CleanupStats(); stats.NativeInUse != 0 {
				t.Fatalf("native handles retained: %+v", stats)
			}
		})
	}
	t.Run("cancellation", testCallbackCancellation)
	t.Run("abandoned_nested_call", testCallbackAbandonedNestedCall)
	t.Run("panic", testCallbackPanic)
}

func setupCallbackDB(t *testing.T) (*Driver, string) {
	t.Helper()
	const schema = `define attribute name, value string; entity person, owns name @key;`
	const insert = `given $n: string; insert $p isa person, has name == $n;`
	typeqlcheck.AssertValid(t, "callback schema", schema)
	typeqlcheck.AssertValid(t, "callback fixture insert", insert)
	typeqlcheck.AssertValid(t, "callback read", callbackQuery)
	conn, name := setupLifecycleDB(t, schema)
	tx, err := conn.Transaction(name, Write)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Close()
	if _, err := tx.QueryWithRows(insert, NewGivenRows("n").MustAdd(StringGiven("a")).MustAdd(StringGiven("b")).MustAdd(StringGiven("c"))); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	return conn, name
}

func testCallbackReentry(t *testing.T, conn *Driver, name string, ctx context.Context) {
	t.Helper()
	tx, err := conn.Transaction(name, Read)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Close()
	seen := 0
	err = tx.QueryEachWithContext(ctx, callbackQuery, func(int, map[string]any) error {
		seen++
		if !tx.IsOpen() {
			return errors.New("callback cannot inspect open transaction")
		}
		operations := []func() error{
			func() error { _, err := tx.Query(callbackQuery); return err },
			func() error { _, err := tx.QueryWithContext(ctx, callbackQuery); return err },
			func() error {
				return tx.QueryEachWithContext(ctx, callbackQuery, func(int, map[string]any) error { return errors.New("nested callback ran") })
			},
			tx.Commit, tx.Rollback, tx.CloseChecked,
		}
		for i, operation := range operations {
			if err := operation(); !errors.Is(err, ErrTransactionBusy) {
				return fmt.Errorf("reentry %d: got %v, want busy", i, err)
			}
		}
		return nil
	})
	if err != nil || seen != 3 {
		t.Fatalf("stream after reentry: seen=%d err=%v", seen, err)
	}
	if _, err := tx.Query(callbackQuery); err != nil {
		t.Fatalf("query after stream: %v", err)
	}
	if err := tx.CloseChecked(); err != nil {
		t.Fatal(err)
	}
}

func testCallbackClose(t *testing.T, conn *Driver, name string, ctx context.Context) {
	t.Helper()
	tx, err := conn.Transaction(name, Read)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Close()
	completed := make(chan error, 2)
	var inCallback atomic.Bool
	seen := 0
	err = tx.QueryEachWithContext(ctx, callbackQuery, func(int, map[string]any) error {
		seen++
		inCallback.Store(true)
		defer inCallback.Store(false)
		tx.Close()
		for range 2 {
			tx.CloseAsync(func(err error) {
				if inCallback.Load() {
					err = errors.New("native handle closed during callback")
				}
				completed <- err
			})
		}
		if tx.IsOpen() {
			return errors.New("close request left transaction logically open")
		}
		return nil
	})
	if !errors.Is(err, ErrNotConnected) || seen != 1 {
		t.Fatalf("close during stream: seen=%d err=%v", seen, err)
	}
	for range 2 {
		select {
		case err := <-completed:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("deferred close callback did not complete")
		}
	}
	if err := tx.CloseChecked(); err != nil {
		t.Fatal(err)
	}
}

func testCallbackCancellation(t *testing.T) {
	conn, name := setupCallbackDB(t)
	tx, err := conn.Transaction(name, Read)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	entered, release := make(chan struct{}), make(chan struct{})
	finished := make(chan error, 1)
	var seen atomic.Int32
	go func() {
		finished <- tx.QueryEachWithContext(ctx, callbackQuery, func(int, map[string]any) error {
			seen.Add(1)
			close(entered)
			<-release
			_ = tx.IsOpen() // Inspection must also return during cancellation.
			return nil
		})
	}()
	<-entered
	cancel()
	select {
	case err := <-finished:
		t.Fatalf("query returned before its callback: %v", err)
	case <-time.After(20 * time.Millisecond):
	}
	close(release)
	if err := <-finished; !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled stream: %v", err)
	}
	if err := conn.Databases().Delete(name); err != nil {
		t.Fatal(err)
	}
	// Driver.Close drains both abandoned calls and detached close jobs.
	conn.Close()
	if seen.Load() != 1 || conn.CleanupStats().NativeInUse != 0 {
		t.Fatalf("callback or native handle survived cancellation: seen=%d stats=%+v", seen.Load(), conn.CleanupStats())
	}
}

func testCallbackAbandonedNestedCall(t *testing.T) {
	conn, name := setupCallbackDB(t)
	tx, err := conn.Transaction(name, Read)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Close()
	err = tx.QueryEachWithContext(context.Background(), callbackQuery, func(int, map[string]any) error {
		// Reproduce a nested context call that loses the race to cancellation
		// and exits while this background-context stream still owns its handle.
		if !tx.beginContextCall() {
			return errors.New("nested context call was rejected")
		}
		tx.abandon()
		tx.finishContextCall()
		if conn.CleanupStats().NativeInUse != 1 {
			return errors.New("nested cancellation released the stream's handle")
		}
		return context.Canceled
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("abandoned nested call: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := WaitForPendingCloses(ctx); err != nil {
		t.Fatal(err)
	}
	if conn.CleanupStats().NativeInUse != 0 {
		t.Fatal("stream did not release the abandoned handle")
	}
}

func testCallbackPanic(t *testing.T) {
	conn, name := setupCallbackDB(t)
	tx, err := conn.Transaction(name, Read)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Close()
	func() {
		defer func() {
			if got := recover(); got != "consumer panic" {
				t.Fatalf("callback panic = %v", got)
			}
		}()
		_ = tx.QueryEachWithContext(context.Background(), callbackQuery, func(int, map[string]any) error { panic("consumer panic") })
	}()
	if _, err := tx.Query(callbackQuery); err != nil {
		t.Fatalf("query after recovered callback panic: %v", err)
	}
	if err := tx.CloseChecked(); err != nil {
		t.Fatal(err)
	}
}
