package gotype

import (
	"context"
	"testing"
)

func TestQueryWithContext_ReturnsResults(t *testing.T) {
	tx := &mockTx{
		responses: [][]map[string]any{
			{{"name": "alice"}},
		},
	}
	ctx := context.Background()
	results, err := tx.QueryWithContext(ctx, "match $e isa person;")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
}

func TestQueryWithContext_CancelledContextReturnsError(t *testing.T) {
	tx := &mockTx{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := tx.QueryWithContext(ctx, "match $e isa person;")
	if err == nil {
		t.Fatal("expected error for cancelled context")
	}
	if err != context.Canceled {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
	if len(tx.queries) != 0 {
		t.Fatal("expected no queries to be executed on cancelled context")
	}
}

func TestExecuteRead_UsesQueryWithContext(t *testing.T) {
	tx := &mockTx{
		responses: [][]map[string]any{
			{{"count": float64(1)}},
		},
	}
	conn := &mockConn{txs: []*mockTx{tx}}
	db := NewDatabase(conn, "test_db")

	ctx := context.Background()
	results, err := db.ExecuteRead(ctx, "match $e isa person; reduce $count = count;")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
}

func TestExecuteRead_CancelledContext(t *testing.T) {
	conn := &mockConn{txs: []*mockTx{{}}}
	db := NewDatabase(conn, "test_db")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := db.ExecuteRead(ctx, "match $e isa person;")
	if err == nil {
		t.Fatal("expected error for cancelled context")
	}
}
