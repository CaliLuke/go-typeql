package gotype

import (
	"context"
	"strings"
	"testing"
)

func TestHashStatements(t *testing.T) {
	stmts := []string{
		"define attribute name, value string;",
		"define entity person, owns name @key;",
	}

	h1 := HashStatements(stmts)
	if len(h1) != 64 { // SHA-256 hex = 64 chars
		t.Errorf("expected 64-char hash, got %d: %q", len(h1), h1)
	}

	// Same input → same hash
	h2 := HashStatements(stmts)
	if h1 != h2 {
		t.Error("expected deterministic hash")
	}

	// Different input → different hash
	h3 := HashStatements([]string{"define attribute email, value string;"})
	if h1 == h3 {
		t.Error("expected different hash for different input")
	}
}

func TestMigrationState_EnsureSchema(t *testing.T) {
	tx := &mockTx{
		responses: [][]map[string]any{nil},
	}
	mock := &mockConn{txs: []*mockTx{tx}}
	db := NewDatabase(mock, "testdb")
	ms := NewMigrationState(db)

	err := ms.EnsureSchema(context.Background())
	if err != nil {
		t.Fatalf("EnsureSchema: %v", err)
	}

	if len(tx.queries) == 0 {
		t.Fatal("expected at least one query")
	}
	if !strings.Contains(tx.queries[0], "migration-record") {
		t.Errorf("expected migration schema, got %q", tx.queries[0])
	}
	if !tx.committed {
		t.Error("expected transaction to be committed")
	}
}

func TestMigrationState_IsApplied(t *testing.T) {
	tx := &mockTx{
		responses: [][]map[string]any{
			{{"count": map[string]any{"value": float64(1)}}},
		},
	}
	mock := &mockConn{txs: []*mockTx{tx}}
	db := NewDatabase(mock, "testdb")
	ms := NewMigrationState(db)

	applied, err := ms.IsApplied(context.Background(), "abc123")
	if err != nil {
		t.Fatalf("IsApplied: %v", err)
	}
	if !applied {
		t.Error("expected migration to be marked as applied")
	}
}

func TestMigrationState_IsApplied_NotFound(t *testing.T) {
	tx := &mockTx{
		responses: [][]map[string]any{
			{{"count": map[string]any{"value": float64(0)}}},
		},
	}
	mock := &mockConn{txs: []*mockTx{tx}}
	db := NewDatabase(mock, "testdb")
	ms := NewMigrationState(db)

	applied, err := ms.IsApplied(context.Background(), "nonexistent")
	if err != nil {
		t.Fatalf("IsApplied: %v", err)
	}
	if applied {
		t.Error("expected migration to NOT be applied")
	}
}

func TestMigrationState_Record(t *testing.T) {
	tx := &mockTx{
		responses: [][]map[string]any{nil},
	}
	mock := &mockConn{txs: []*mockTx{tx}}
	db := NewDatabase(mock, "testdb")
	ms := NewMigrationState(db)

	err := ms.Record(context.Background(), "abc123", "add 2 attributes")
	if err != nil {
		t.Fatalf("Record: %v", err)
	}

	if len(tx.queries) == 0 {
		t.Fatal("expected query")
	}
	q := tx.queries[0]
	if !strings.Contains(q, "migration-record") {
		t.Errorf("expected insert with migration-record, got %q", q)
	}
	if !strings.Contains(q, "abc123") {
		t.Errorf("expected hash in query, got %q", q)
	}
	if !strings.Contains(q, "add 2 attributes") {
		t.Errorf("expected summary in query, got %q", q)
	}
}

func TestEscapeTQL(t *testing.T) {
	tests := []struct {
		input, want string
	}{
		{"hello", "hello"},
		{`say "hi"`, `say \"hi\"`},
		{`path\to`, `path\\to`},
	}
	for _, tt := range tests {
		got := escapeTQL(tt.input)
		if got != tt.want {
			t.Errorf("escapeTQL(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}
