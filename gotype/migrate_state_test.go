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

func TestMigrationState_Applied_ParsesDriverDatetimes(t *testing.T) {
	// The FFI driver returns zone-less datetime strings ("2006-01-02 15:04:05"
	// or "2006-01-02T15:04:05"), which RFC3339 never parses (issue #61).
	tx := &mockTx{
		responses: [][]map[string]any{{
			{"hash": "bbb", "summary": "second", "applied-at": "2026-07-07 10:30:00.123456789"},
			{"hash": "aaa", "summary": "first", "applied-at": "2026-07-06T08:00:00"},
		}},
	}
	mock := &mockConn{txs: []*mockTx{tx}}
	ms := NewMigrationState(NewDatabase(mock, "testdb"))

	records, err := ms.Applied(context.Background())
	if err != nil {
		t.Fatalf("Applied: %v", err)
	}
	if len(records) != 2 {
		t.Fatalf("expected 2 records, got %d", len(records))
	}
	for _, r := range records {
		if r.AppliedAt.IsZero() {
			t.Errorf("record %q has zero AppliedAt", r.Hash)
		}
	}
	// Ordered by application timestamp, which requires the parse to work.
	if records[0].Hash != "aaa" || records[1].Hash != "bbb" {
		t.Errorf("expected chronological order aaa,bbb got %q,%q", records[0].Hash, records[1].Hash)
	}
	if got := records[0].AppliedAt.UTC().Hour(); got != 8 {
		t.Errorf("zone-less values must parse as UTC, got hour %d", got)
	}
}

func TestMigrationState_Applied_PropagatesParseFailure(t *testing.T) {
	tx := &mockTx{
		responses: [][]map[string]any{{
			{"hash": "aaa", "summary": "first", "applied-at": "not-a-datetime"},
		}},
	}
	mock := &mockConn{txs: []*mockTx{tx}}
	ms := NewMigrationState(NewDatabase(mock, "testdb"))

	_, err := ms.Applied(context.Background())
	if err == nil {
		t.Fatal("expected an error for an unparseable applied-at value")
	}
	if !strings.Contains(err.Error(), "not-a-datetime") {
		t.Errorf("error should mention the offending value, got: %v", err)
	}
}

func TestMigrateWithStateFromSchema_AtomicApplyAndRecord(t *testing.T) {
	registerTestTypes(t)

	ensureTx := &mockTx{}                                                                               // EnsureSchema
	checkTx := &mockTx{responses: [][]map[string]any{{{"count": map[string]any{"value": float64(0)}}}}} // IsApplied
	applyTx := &mockTx{}                                                                                // plan + record, one tx
	mock := &mockConn{txs: []*mockTx{ensureTx, checkTx, applyTx}, schemaStr: ""}
	db := NewDatabase(mock, "testdb")

	diff, err := MigrateWithStateFromSchema(context.Background(), db, "")
	if err != nil {
		t.Fatalf("MigrateWithStateFromSchema: %v", err)
	}
	if diff.IsEmpty() {
		t.Fatal("expected non-empty diff")
	}
	if len(applyTx.queries) != 2 {
		t.Fatalf("expected define block + record insert in one transaction, got %d: %v", len(applyTx.queries), applyTx.queries)
	}
	if !strings.HasPrefix(applyTx.queries[0], "define\n") {
		t.Errorf("first query should be the batched define block, got %q", applyTx.queries[0])
	}
	if !strings.Contains(applyTx.queries[1], "insert") || !strings.Contains(applyTx.queries[1], "migration-record") {
		t.Errorf("second query should record the migration, got %q", applyTx.queries[1])
	}
	if !applyTx.committed {
		t.Error("expected apply transaction to be committed")
	}
}

func TestMigrateWithStateFromSchema_NoRecordForRemovalOnlyDiff(t *testing.T) {
	ClearRegistry()
	// DB has a type the (empty) registry no longer declares: the diff is
	// removal-only, produces no executable statements, and must not create a
	// junk migration record (issue #58).
	ensureTx := &mockTx{}
	mock := &mockConn{txs: []*mockTx{ensureTx}}
	db := NewDatabase(mock, "testdb")

	diff, err := MigrateWithStateFromSchema(context.Background(), db, `define
attribute stale, value string;
entity obsolete,
    owns stale;`)
	if err != nil {
		t.Fatalf("MigrateWithStateFromSchema: %v", err)
	}
	if diff.IsEmpty() {
		t.Fatal("removal-only diff should not be empty")
	}
	if mock.idx != 1 {
		t.Errorf("expected only the EnsureSchema transaction, got %d transactions", mock.idx)
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
