//go:build integration && cgo && typedb

package gotype_test

import (
	"context"
	"testing"

	"github.com/CaliLuke/go-typeql/driver"
	"github.com/CaliLuke/go-typeql/gotype"
)

func TestIntegration_ExecuteRead(t *testing.T) {
	db := setupTestDBDefault(t)
	ctx := context.Background()

	mgr := gotype.NewManager[Person](db)
	assertInsert(t, ctx, mgr, &Person{Name: "Alice", Email: "alice@example.com"})

	results, err := db.ExecuteRead(ctx, "match $e isa person; fetch { \"name\": $e.name };")
	if err != nil {
		t.Fatalf("ExecuteRead: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
}

func TestIntegration_ExecuteWrite(t *testing.T) {
	db := setupTestDBDefault(t)
	ctx := context.Background()

	_, err := db.ExecuteWrite(ctx, "insert $e isa person, has name \"WritePerson\", has email \"write@example.com\";")
	if err != nil {
		t.Fatalf("ExecuteWrite: %v", err)
	}

	mgr := gotype.NewManager[Person](db)
	assertGetOne(t, ctx, mgr, map[string]any{"name": "WritePerson"})
}

func TestIntegration_ExecuteSchema(t *testing.T) {
	db := setupTestDBDefault(t)
	ctx := context.Background()

	err := db.ExecuteSchema(ctx, "define attribute nickname, value string;")
	if err != nil {
		t.Fatalf("ExecuteSchema: %v", err)
	}
}

func TestIntegration_CommitMakesDataVisible(t *testing.T) {
	db := setupTestDBDefault(t)

	// Manually open a write tx, insert, commit.
	tx, err := db.Transaction(gotype.WriteTransaction)
	if err != nil {
		t.Fatalf("open tx: %v", err)
	}
	_, err = tx.Query("insert $e isa person, has name \"CommitTest\", has email \"commit@test.com\";")
	if err != nil {
		tx.Close()
		t.Fatalf("insert: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}
	tx.Close()

	// Now a read tx should see it.
	ctx := context.Background()
	mgr := gotype.NewManager[Person](db)
	assertGetOne(t, ctx, mgr, map[string]any{"name": "CommitTest"})
}

func TestIntegration_RollbackMakesDataInvisible(t *testing.T) {
	db := setupTestDBDefault(t)

	// Manually open a write tx, insert, rollback.
	tx, err := db.Transaction(gotype.WriteTransaction)
	if err != nil {
		t.Fatalf("open tx: %v", err)
	}
	_, err = tx.Query("insert $e isa person, has name \"RollbackTest\", has email \"rollback@test.com\";")
	if err != nil {
		tx.Close()
		t.Fatalf("insert: %v", err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatalf("rollback: %v", err)
	}
	tx.Close()

	// Read should see nothing.
	ctx := context.Background()
	mgr := gotype.NewManager[Person](db)
	results, err := mgr.Get(ctx, map[string]any{"name": "RollbackTest"})
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("expected 0 results after rollback, got %d", len(results))
	}
}

func TestIntegration_MultipleSequentialTransactions(t *testing.T) {
	db := setupTestDBDefault(t)
	ctx := context.Background()
	mgr := gotype.NewManager[Person](db)

	names := []string{"Seq1", "Seq2", "Seq3"}
	for i, name := range names {
		assertInsert(t, ctx, mgr, &Person{Name: name, Email: name + "@example.com", Age: new(20 + i)})
	}

	assertCount(t, ctx, mgr, 3)
}

func TestIntegration_DatabaseName(t *testing.T) {
	db := setupTestDBDefault(t)
	name := db.Name()
	if name == "" {
		t.Error("expected non-empty database name")
	}
}

func TestIntegration_ConnectionIsOpen(t *testing.T) {
	addr := dbAddress()
	drv, err := driver.Open(addr, "admin", "password")
	if err != nil {
		t.Skipf("TypeDB not available: %v", err)
	}

	conn := &driverAdapter{drv: drv}
	if !conn.IsOpen() {
		t.Error("expected connection to be open")
	}

	conn.Close()
	if conn.IsOpen() {
		t.Error("expected connection to be closed after Close()")
	}
}
