//go:build cgo && typedb && integration

package driver

import (
	"context"
	"os"
	"testing"
	"time"
)

func testAddr() string {
	if addr := os.Getenv("TEST_DB_ADDRESS"); addr != "" {
		return addr
	}
	return "localhost:1729"
}

func TestQueryWithContext_BasicQuery(t *testing.T) {
	conn, err := OpenWithTLS(testAddr(), "admin", "password", false, "")
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer conn.Close()

	dbName := "test_ctx_basic"
	dm := conn.Databases()
	_ = dm.Delete(dbName)
	if err := dm.Create(dbName); err != nil {
		t.Fatalf("create db: %v", err)
	}
	defer dm.Delete(dbName)

	// Define a simple schema
	tx, err := conn.Transaction(dbName, 2) // schema
	if err != nil {
		t.Fatalf("open schema tx: %v", err)
	}
	_, err = tx.Query("define attribute name, value string;")
	if err != nil {
		t.Fatalf("define: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit schema: %v", err)
	}

	// Query with context — should succeed normally
	tx2, err := conn.Transaction(dbName, 0) // read
	if err != nil {
		t.Fatalf("open read tx: %v", err)
	}
	defer tx2.Close()

	ctx := context.Background()
	results, err := tx2.QueryWithContext(ctx, `match attribute $a; fetch { "label": label($a) };`)
	if err != nil {
		t.Fatalf("query with context: %v", err)
	}
	_ = results
}

func TestQueryWithContext_AlreadyCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	conn, err := OpenWithTLS(testAddr(), "admin", "password", false, "")
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer conn.Close()

	dbName := "test_ctx_cancelled"
	dm := conn.Databases()
	_ = dm.Delete(dbName)
	if err := dm.Create(dbName); err != nil {
		t.Fatalf("create db: %v", err)
	}
	defer dm.Delete(dbName)

	tx, err := conn.Transaction(dbName, 0)
	if err != nil {
		t.Fatalf("open tx: %v", err)
	}
	defer tx.Close()

	_, err = tx.QueryWithContext(ctx, "match attribute $a; fetch { $a };")
	if err == nil {
		t.Fatal("expected error for already-cancelled context")
	}
	if err != context.Canceled {
		t.Fatalf("expected context.Canceled, got: %v", err)
	}
}

func TestQueryWithContext_TimeoutCancellation(t *testing.T) {
	conn, err := OpenWithTLS(testAddr(), "admin", "password", false, "")
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer conn.Close()

	dbName := "test_ctx_timeout"
	dm := conn.Databases()
	_ = dm.Delete(dbName)
	if err := dm.Create(dbName); err != nil {
		t.Fatalf("create db: %v", err)
	}
	defer dm.Delete(dbName)

	tx, err := conn.Transaction(dbName, 0)
	if err != nil {
		t.Fatalf("open tx: %v", err)
	}
	defer tx.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err = tx.QueryWithContext(ctx, `match attribute $a; fetch { "label": label($a) };`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}
