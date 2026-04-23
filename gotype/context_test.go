package gotype

import (
	"context"
	"testing"
)

type contextAwareMockConn struct {
	*mockConn
	lastCtx    context.Context
	lastDBName string
	lastTxType int
}

func (m *contextAwareMockConn) TransactionContext(ctx context.Context, dbName string, txType int) (Tx, error) {
	m.lastCtx = ctx
	m.lastDBName = dbName
	m.lastTxType = txType
	return m.Transaction(dbName, txType)
}

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

func TestDatabase_TransactionContext_UsesContextAwareConn(t *testing.T) {
	tx := &mockTx{}
	conn := &contextAwareMockConn{
		mockConn: &mockConn{txs: []*mockTx{tx}},
	}
	db := NewDatabase(conn, "test_db")

	ctx := context.WithValue(context.Background(), "k", "v")
	gotTx, err := db.TransactionContext(ctx, WriteTransaction)
	if err != nil {
		t.Fatalf("TransactionContext failed: %v", err)
	}
	if gotTx != tx {
		t.Fatalf("TransactionContext returned unexpected tx: got %#v want %#v", gotTx, tx)
	}
	if conn.lastCtx != ctx {
		t.Fatal("TransactionContext did not forward ctx to conn")
	}
	if conn.lastDBName != "test_db" {
		t.Fatalf("TransactionContext used wrong db name: %q", conn.lastDBName)
	}
	if conn.lastTxType != int(WriteTransaction) {
		t.Fatalf("TransactionContext used wrong tx type: %d", conn.lastTxType)
	}
}

func TestDatabase_BeginContext_UsesContextAwareConn(t *testing.T) {
	tx := &mockTx{}
	conn := &contextAwareMockConn{
		mockConn: &mockConn{txs: []*mockTx{tx}},
	}
	db := NewDatabase(conn, "test_db")

	ctx := context.WithValue(context.Background(), "k", "v")
	tc, err := db.BeginContext(ctx, ReadTransaction)
	if err != nil {
		t.Fatalf("BeginContext failed: %v", err)
	}
	defer tc.Close()

	if conn.lastCtx != ctx {
		t.Fatal("BeginContext did not forward ctx to conn")
	}
	if tc.Tx() != tx {
		t.Fatalf("BeginContext returned unexpected tx: got %#v want %#v", tc.Tx(), tx)
	}
}
