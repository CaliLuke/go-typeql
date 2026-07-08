package gotype

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
)

// --- inferTxType ---

func TestInferTxType(t *testing.T) {
	tests := []struct {
		stmt string
		want string
	}{
		{"define attribute name, value string;", "schema"},
		{"DEFINE entity person;", "schema"},
		{"  define entity person;", "schema"},
		{"undefine attribute old-attr;", "schema"},
		{"redefine entity person;", "schema"},
		{"insert $p isa person;", "write"},
		{"match $p isa person; delete $p;", "write"},
		{"match $p isa person; update $p;", "write"},
		{"", "write"},
	}
	for _, tt := range tests {
		got := inferTxType(tt.stmt)
		if got != tt.want {
			t.Errorf("inferTxType(%q) = %q, want %q", tt.stmt, got, tt.want)
		}
	}
}

// --- TQLMigration ---

func TestTQLMigration_CreatesUpDown(t *testing.T) {
	m := TQLMigration("001_init", []string{"define attribute name, value string;"}, []string{"undefine attribute name;"})
	if m.Name != "001_init" {
		t.Errorf("Name = %q, want %q", m.Name, "001_init")
	}
	if m.Up == nil {
		t.Fatal("Up is nil")
	}
	if m.Down == nil {
		t.Fatal("Down is nil")
	}
}

func TestTQLMigration_NilDownWhenEmpty(t *testing.T) {
	m := TQLMigration("002_add", []string{"define attribute age, value long;"}, nil)
	if m.Down != nil {
		t.Error("Down should be nil when no down statements provided")
	}
}

func TestTQLMigration_UpExecutesStatements(t *testing.T) {
	// Schema tx for define, write tx for insert
	schemaTx := &mockTx{}
	writeTx := &mockTx{}
	conn := &mockConn{txs: []*mockTx{schemaTx, writeTx}}
	db := NewDatabase(conn, "test")

	m := TQLMigration("003_mixed", []string{
		"define attribute name, value string;",
		"insert $p isa person, has name \"Alice\";",
	}, nil)

	err := m.Up(context.Background(), db)
	if err != nil {
		t.Fatalf("Up failed: %v", err)
	}
	if len(schemaTx.queries) != 1 {
		t.Errorf("expected 1 schema query, got %d", len(schemaTx.queries))
	}
	if len(writeTx.queries) != 1 {
		t.Errorf("expected 1 write query, got %d", len(writeTx.queries))
	}
}

// --- ValidateSequentialMigrations ---

func TestValidateSequentialMigrations_Valid(t *testing.T) {
	noop := func(ctx context.Context, db *Database) error { return nil }
	migrations := []SequentialMigration{
		{Name: "001_first", Up: noop},
		{Name: "002_second", Up: noop},
	}
	issues := ValidateSequentialMigrations(migrations)
	for _, issue := range issues {
		if issue.Severity == "error" {
			t.Errorf("unexpected error: %s: %s", issue.Name, issue.Message)
		}
	}
}

func TestValidateSequentialMigrations_EmptyName(t *testing.T) {
	noop := func(ctx context.Context, db *Database) error { return nil }
	issues := ValidateSequentialMigrations([]SequentialMigration{
		{Name: "", Up: noop},
	})
	if len(issues) == 0 {
		t.Fatal("expected validation issue for empty name")
	}
	if issues[0].Severity != "error" {
		t.Errorf("severity = %q, want %q", issues[0].Severity, "error")
	}
}

func TestValidateSequentialMigrations_DuplicateNames(t *testing.T) {
	noop := func(ctx context.Context, db *Database) error { return nil }
	issues := ValidateSequentialMigrations([]SequentialMigration{
		{Name: "001_init", Up: noop},
		{Name: "001_init", Up: noop},
	})
	found := false
	for _, issue := range issues {
		if issue.Severity == "error" && strings.Contains(issue.Message, "duplicate") {
			found = true
		}
	}
	if !found {
		t.Error("expected duplicate name error")
	}
}

func TestValidateSequentialMigrations_NilUp(t *testing.T) {
	issues := ValidateSequentialMigrations([]SequentialMigration{
		{Name: "001_init", Up: nil},
	})
	found := false
	for _, issue := range issues {
		if issue.Severity == "error" && strings.Contains(issue.Message, "nil") {
			found = true
		}
	}
	if !found {
		t.Error("expected nil Up error")
	}
}

func TestValidateSequentialMigrations_UnsortedWarning(t *testing.T) {
	noop := func(ctx context.Context, db *Database) error { return nil }
	issues := ValidateSequentialMigrations([]SequentialMigration{
		{Name: "002_second", Up: noop},
		{Name: "001_first", Up: noop},
	})
	found := false
	for _, issue := range issues {
		if issue.Severity == "warning" && strings.Contains(issue.Message, "sorted") {
			found = true
		}
	}
	if !found {
		t.Error("expected unsorted warning")
	}
}

// --- RunSequentialMigrations ---

func TestRunSequentialMigrations_Sorting(t *testing.T) {
	var order []string
	makeMigration := func(name string) SequentialMigration {
		n := name
		return SequentialMigration{
			Name: n,
			Up: func(ctx context.Context, db *Database) error {
				order = append(order, n)
				return nil
			},
		}
	}

	// State: ensure schema (schema tx) + applied query (read tx) + per-migration write txs + record txs
	schemaTx := &mockTx{}
	readTx := &mockTx{responses: [][]map[string]any{nil}} // no applied migrations
	// Each migration Up calls ExecuteSchema or ExecuteWrite — but our test Up doesn't use db.
	// However Record calls ExecuteWrite, so we need write txs for that.
	recordTx1 := &mockTx{}
	recordTx2 := &mockTx{}
	recordTx3 := &mockTx{}
	conn := &mockConn{txs: []*mockTx{schemaTx, readTx, recordTx1, recordTx2, recordTx3}}
	db := NewDatabase(conn, "test")

	migrations := []SequentialMigration{
		makeMigration("003_third"),
		makeMigration("001_first"),
		makeMigration("002_second"),
	}

	applied, err := RunSequentialMigrations(context.Background(), db, migrations)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(applied) != 3 {
		t.Fatalf("expected 3 applied, got %d", len(applied))
	}

	// Verify sorted execution order
	expected := []string{"001_first", "002_second", "003_third"}
	for i, name := range expected {
		if order[i] != name {
			t.Errorf("order[%d] = %q, want %q", i, order[i], name)
		}
	}
}

func TestRunSequentialMigrations_SkipsApplied(t *testing.T) {
	var executed []string
	noop := func(name string) func(ctx context.Context, db *Database) error {
		return func(ctx context.Context, db *Database) error {
			executed = append(executed, name)
			return nil
		}
	}

	schemaTx := &mockTx{}
	readTx := &mockTx{responses: [][]map[string]any{
		// Applied query returns one record
		{{"name": map[string]any{"value": "001_init"}, "applied-at": map[string]any{"value": "2024-01-01T00:00:00Z"}}},
	}}
	recordTx := &mockTx{}
	conn := &mockConn{txs: []*mockTx{schemaTx, readTx, recordTx}}
	db := NewDatabase(conn, "test")

	migrations := []SequentialMigration{
		{Name: "001_init", Up: noop("001_init")},
		{Name: "002_add_email", Up: noop("002_add_email")},
	}

	applied, err := RunSequentialMigrations(context.Background(), db, migrations)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(applied) != 1 {
		t.Fatalf("expected 1 applied, got %d: %v", len(applied), applied)
	}
	if applied[0] != "002_add_email" {
		t.Errorf("expected 002_add_email, got %q", applied[0])
	}
	if len(executed) != 1 || executed[0] != "002_add_email" {
		t.Errorf("unexpected execution: %v", executed)
	}
}

func TestRunSequentialMigrations_DryRun(t *testing.T) {
	schemaTx := &mockTx{}
	readTx := &mockTx{responses: [][]map[string]any{nil}}
	conn := &mockConn{txs: []*mockTx{schemaTx, readTx}}
	db := NewDatabase(conn, "test")

	called := false
	migrations := []SequentialMigration{
		{Name: "001_init", Up: func(ctx context.Context, db *Database) error {
			called = true
			return nil
		}},
	}

	var logged []string
	applied, err := RunSequentialMigrations(context.Background(), db, migrations,
		WithSeqDryRun(),
		WithSeqLogger(func(msg string) { logged = append(logged, msg) }),
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if called {
		t.Error("Up was called in dry-run mode")
	}
	if len(applied) != 1 || applied[0] != "001_init" {
		t.Errorf("expected [001_init], got %v", applied)
	}
	if len(logged) != 1 {
		t.Errorf("expected 1 log message, got %d", len(logged))
	}
}

func TestRunSequentialMigrations_Target(t *testing.T) {
	var executed []string
	makeMigration := func(name string) SequentialMigration {
		n := name
		return SequentialMigration{
			Name: n,
			Up: func(ctx context.Context, db *Database) error {
				executed = append(executed, n)
				return nil
			},
		}
	}

	schemaTx := &mockTx{}
	readTx := &mockTx{responses: [][]map[string]any{nil}}
	recordTx1 := &mockTx{}
	recordTx2 := &mockTx{}
	conn := &mockConn{txs: []*mockTx{schemaTx, readTx, recordTx1, recordTx2}}
	db := NewDatabase(conn, "test")

	migrations := []SequentialMigration{
		makeMigration("001_first"),
		makeMigration("002_second"),
		makeMigration("003_third"),
	}

	applied, err := RunSequentialMigrations(context.Background(), db, migrations, WithSeqTarget("002_second"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(applied) != 2 {
		t.Fatalf("expected 2 applied, got %d: %v", len(applied), applied)
	}
	if len(executed) != 2 {
		t.Fatalf("expected 2 executed, got %d", len(executed))
	}
}

func TestRunSequentialMigrations_ValidationFails(t *testing.T) {
	noop := func(ctx context.Context, db *Database) error { return nil }
	migrations := []SequentialMigration{
		{Name: "001_init", Up: noop},
		{Name: "001_init", Up: noop}, // duplicate
	}

	_, err := RunSequentialMigrations(context.Background(), nil, migrations)
	if err == nil {
		t.Fatal("expected validation error")
	}
	if !strings.Contains(err.Error(), "duplicate") {
		t.Errorf("error should mention duplicate: %v", err)
	}
}

func TestRunSequentialMigrations_UpError(t *testing.T) {
	schemaTx := &mockTx{}
	readTx := &mockTx{responses: [][]map[string]any{nil}}
	recordTx := &mockTx{}
	conn := &mockConn{txs: []*mockTx{schemaTx, readTx, recordTx}}
	db := NewDatabase(conn, "test")

	migrations := []SequentialMigration{
		{Name: "001_ok", Up: func(ctx context.Context, db *Database) error { return nil }},
		{Name: "002_fail", Up: func(ctx context.Context, db *Database) error { return fmt.Errorf("boom") }},
	}

	applied, err := RunSequentialMigrations(context.Background(), db, migrations)
	if err == nil {
		t.Fatal("expected error")
	}
	var seqErr *SeqMigrationError
	if ok := errorAs(err, &seqErr); !ok {
		t.Fatalf("expected SeqMigrationError, got %T", err)
	}
	if seqErr.Name != "002_fail" {
		t.Errorf("error name = %q, want %q", seqErr.Name, "002_fail")
	}
	// Only the first migration should have been applied
	if len(applied) != 1 {
		t.Errorf("expected 1 applied, got %d", len(applied))
	}
}

// --- SeqMigrationStatus ---

func TestSeqMigrationStatus(t *testing.T) {
	schemaTx := &mockTx{}
	readTx := &mockTx{responses: [][]map[string]any{
		{{"name": map[string]any{"value": "001_init"}, "applied-at": map[string]any{"value": "2024-01-01T00:00:00Z"}}},
	}}
	conn := &mockConn{txs: []*mockTx{schemaTx, readTx}}
	db := NewDatabase(conn, "test")

	noop := func(ctx context.Context, db *Database) error { return nil }
	migrations := []SequentialMigration{
		{Name: "001_init", Up: noop},
		{Name: "002_add_email", Up: noop},
	}

	infos, err := SeqMigrationStatus(context.Background(), db, migrations)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(infos) != 2 {
		t.Fatalf("expected 2 infos, got %d", len(infos))
	}
	if !infos[0].Applied {
		t.Error("001_init should be applied")
	}
	if infos[1].Applied {
		t.Error("002_add_email should not be applied")
	}
}

// --- RollbackSequentialMigration ---

func TestRollbackSequentialMigration(t *testing.T) {
	var downCalled []string

	schemaTx := &mockTx{}
	readTx := &mockTx{responses: [][]map[string]any{
		{
			{"name": map[string]any{"value": "001_init"}, "applied-at": map[string]any{"value": "2024-01-01T00:00:00Z"}},
			{"name": map[string]any{"value": "002_add_email"}, "applied-at": map[string]any{"value": "2024-01-02T00:00:00Z"}},
		},
	}}
	deleteTx := &mockTx{} // for deleting 002_add_email record
	conn := &mockConn{txs: []*mockTx{schemaTx, readTx, deleteTx}}
	db := NewDatabase(conn, "test")

	migrations := []SequentialMigration{
		{Name: "001_init", Up: func(ctx context.Context, db *Database) error { return nil },
			Down: func(ctx context.Context, db *Database) error {
				downCalled = append(downCalled, "001_init")
				return nil
			}},
		{Name: "002_add_email", Up: func(ctx context.Context, db *Database) error { return nil },
			Down: func(ctx context.Context, db *Database) error {
				downCalled = append(downCalled, "002_add_email")
				return nil
			}},
	}

	rolledBack, err := RollbackSequentialMigration(context.Background(), db, migrations, 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(rolledBack) != 1 {
		t.Fatalf("expected 1 rolled back, got %d", len(rolledBack))
	}
	if rolledBack[0] != "002_add_email" {
		t.Errorf("expected 002_add_email, got %q", rolledBack[0])
	}
	if len(downCalled) != 1 || downCalled[0] != "002_add_email" {
		t.Errorf("unexpected down calls: %v", downCalled)
	}
}

func TestRollbackSequentialMigration_NoDown(t *testing.T) {
	schemaTx := &mockTx{}
	readTx := &mockTx{responses: [][]map[string]any{
		{{"name": map[string]any{"value": "001_init"}, "applied-at": map[string]any{"value": "2024-01-01T00:00:00Z"}}},
	}}
	conn := &mockConn{txs: []*mockTx{schemaTx, readTx}}
	db := NewDatabase(conn, "test")

	migrations := []SequentialMigration{
		{Name: "001_init", Up: func(ctx context.Context, db *Database) error { return nil }},
	}

	_, err := RollbackSequentialMigration(context.Background(), db, migrations, 1)
	if err == nil {
		t.Fatal("expected error for nil Down")
	}
	if !strings.Contains(err.Error(), "no Down") {
		t.Errorf("error should mention no Down: %v", err)
	}
}

// --- SeqMigrationError ---

func TestSeqMigrationError_Format(t *testing.T) {
	err := &SeqMigrationError{Name: "003_fail", Cause: fmt.Errorf("connection lost")}
	msg := err.Error()
	if !strings.Contains(msg, "003_fail") {
		t.Errorf("error should contain migration name: %s", msg)
	}
	if !strings.Contains(msg, "connection lost") {
		t.Errorf("error should contain cause: %s", msg)
	}
	if err.Unwrap() == nil {
		t.Error("Unwrap should return cause")
	}
}

// --- TQLStatements ---

func TestTQLMigration_PopulatesStatements(t *testing.T) {
	up := []string{"define attribute name, value string;", "define entity person;"}
	down := []string{"undefine entity person;"}
	m := TQLMigration("001", up, down)

	if m.Statements == nil {
		t.Fatal("Statements should not be nil")
	}
	if len(m.Statements.Up) != 2 {
		t.Errorf("expected 2 up statements, got %d", len(m.Statements.Up))
	}
	if len(m.Statements.Down) != 1 {
		t.Errorf("expected 1 down statement, got %d", len(m.Statements.Down))
	}

	// Verify they're copies
	up[0] = "mutated"
	if m.Statements.Up[0] == "mutated" {
		t.Error("Statements.Up should be a copy, not a reference")
	}
}

func TestTQLMigration_NilStatementsWhenBothEmpty(t *testing.T) {
	m := TQLMigration("001", nil, nil)
	if m.Statements != nil {
		t.Error("Statements should be nil when both up and down are empty")
	}
}

func TestCustomMigration_StatementsNil(t *testing.T) {
	m := SequentialMigration{
		Name: "001",
		Up:   func(ctx context.Context, db *Database) error { return nil },
	}
	if m.Statements != nil {
		t.Error("Statements should be nil for custom migrations")
	}
}

func TestRunSequentialMigrations_DryRunLogsStatements(t *testing.T) {
	schemaTx := &mockTx{}
	readTx := &mockTx{responses: [][]map[string]any{nil}}
	conn := &mockConn{txs: []*mockTx{schemaTx, readTx}}
	db := NewDatabase(conn, "test")

	m := TQLMigration("001_init", []string{"define attribute name, value string;", "define entity person;"}, nil)
	var logged []string
	_, err := RunSequentialMigrations(context.Background(), db, []SequentialMigration{m},
		WithSeqDryRun(),
		WithSeqLogger(func(msg string) { logged = append(logged, msg) }),
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// 1 "pending" + 2 statement lines
	if len(logged) != 3 {
		t.Errorf("expected 3 log messages, got %d: %v", len(logged), logged)
	}
}

func TestRunSequentialMigrations_DryRunCustomMigrationNoStatements(t *testing.T) {
	schemaTx := &mockTx{}
	readTx := &mockTx{responses: [][]map[string]any{nil}}
	conn := &mockConn{txs: []*mockTx{schemaTx, readTx}}
	db := NewDatabase(conn, "test")

	m := SequentialMigration{
		Name: "001_init",
		Up:   func(ctx context.Context, db *Database) error { return nil },
	}
	var logged []string
	_, err := RunSequentialMigrations(context.Background(), db, []SequentialMigration{m},
		WithSeqDryRun(),
		WithSeqLogger(func(msg string) { logged = append(logged, msg) }),
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(logged) != 1 {
		t.Errorf("expected 1 log message, got %d: %v", len(logged), logged)
	}
}

// --- StampSequentialMigrations ---

func TestStampSequentialMigrations_StampsAll(t *testing.T) {
	noop := func(ctx context.Context, db *Database) error { return nil }
	schemaTx := &mockTx{}
	readTx := &mockTx{responses: [][]map[string]any{nil}}
	recordTx1 := &mockTx{}
	recordTx2 := &mockTx{}
	recordTx3 := &mockTx{}
	conn := &mockConn{txs: []*mockTx{schemaTx, readTx, recordTx1, recordTx2, recordTx3}}
	db := NewDatabase(conn, "test")

	migrations := []SequentialMigration{
		{Name: "001_init", Up: noop},
		{Name: "002_second", Up: noop},
		{Name: "003_third", Up: noop},
	}

	stamped, err := StampSequentialMigrations(context.Background(), db, migrations)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(stamped) != 3 {
		t.Fatalf("expected 3 stamped, got %d", len(stamped))
	}
}

func TestStampSequentialMigrations_SkipsAlreadyApplied(t *testing.T) {
	noop := func(ctx context.Context, db *Database) error { return nil }
	schemaTx := &mockTx{}
	readTx := &mockTx{responses: [][]map[string]any{
		{{"name": map[string]any{"value": "001_init"}, "applied-at": map[string]any{"value": "2024-01-01T00:00:00Z"}}},
	}}
	recordTx1 := &mockTx{}
	recordTx2 := &mockTx{}
	conn := &mockConn{txs: []*mockTx{schemaTx, readTx, recordTx1, recordTx2}}
	db := NewDatabase(conn, "test")

	migrations := []SequentialMigration{
		{Name: "001_init", Up: noop},
		{Name: "002_second", Up: noop},
		{Name: "003_third", Up: noop},
	}

	stamped, err := StampSequentialMigrations(context.Background(), db, migrations)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(stamped) != 2 {
		t.Fatalf("expected 2 stamped, got %d: %v", len(stamped), stamped)
	}
}

func TestStampSequentialMigrations_DryRun(t *testing.T) {
	noop := func(ctx context.Context, db *Database) error { return nil }
	schemaTx := &mockTx{}
	readTx := &mockTx{responses: [][]map[string]any{nil}}
	conn := &mockConn{txs: []*mockTx{schemaTx, readTx}} // no write txs — dry run shouldn't need them
	db := NewDatabase(conn, "test")

	migrations := []SequentialMigration{
		{Name: "001_init", Up: noop},
		{Name: "002_second", Up: noop},
	}

	var logged []string
	stamped, err := StampSequentialMigrations(context.Background(), db, migrations,
		WithSeqDryRun(),
		WithSeqLogger(func(msg string) { logged = append(logged, msg) }),
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(stamped) != 2 {
		t.Fatalf("expected 2 stamped, got %d", len(stamped))
	}
	if len(logged) != 2 {
		t.Errorf("expected 2 log messages, got %d", len(logged))
	}
}

func TestStampSequentialMigrations_WithTarget(t *testing.T) {
	noop := func(ctx context.Context, db *Database) error { return nil }
	schemaTx := &mockTx{}
	readTx := &mockTx{responses: [][]map[string]any{nil}}
	recordTx1 := &mockTx{}
	recordTx2 := &mockTx{}
	conn := &mockConn{txs: []*mockTx{schemaTx, readTx, recordTx1, recordTx2}}
	db := NewDatabase(conn, "test")

	migrations := []SequentialMigration{
		{Name: "001_first", Up: noop},
		{Name: "002_second", Up: noop},
		{Name: "003_third", Up: noop},
	}

	stamped, err := StampSequentialMigrations(context.Background(), db, migrations, WithSeqTarget("002_second"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(stamped) != 2 {
		t.Fatalf("expected 2 stamped, got %d: %v", len(stamped), stamped)
	}
}

func TestStampSequentialMigrations_EmptySlice(t *testing.T) {
	stamped, err := StampSequentialMigrations(context.Background(), nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if stamped != nil {
		t.Errorf("expected nil, got %v", stamped)
	}
}

func TestStampSequentialMigrations_AllAlreadyApplied(t *testing.T) {
	noop := func(ctx context.Context, db *Database) error { return nil }
	schemaTx := &mockTx{}
	readTx := &mockTx{responses: [][]map[string]any{
		{
			{"name": map[string]any{"value": "001_init"}, "applied-at": map[string]any{"value": "2024-01-01T00:00:00Z"}},
			{"name": map[string]any{"value": "002_second"}, "applied-at": map[string]any{"value": "2024-01-02T00:00:00Z"}},
		},
	}}
	conn := &mockConn{txs: []*mockTx{schemaTx, readTx}}
	db := NewDatabase(conn, "test")

	migrations := []SequentialMigration{
		{Name: "001_init", Up: noop},
		{Name: "002_second", Up: noop},
	}

	stamped, err := StampSequentialMigrations(context.Background(), db, migrations)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(stamped) != 0 {
		t.Errorf("expected 0 stamped, got %d: %v", len(stamped), stamped)
	}
}

// --- Error path tests ---

// errMockTx always returns an error on Query.
type errMockTx struct {
	err error
}

func (m *errMockTx) Query(string) ([]map[string]any, error) { return nil, m.err }
func (m *errMockTx) QueryWithContext(_ context.Context, q string) ([]map[string]any, error) {
	return m.Query(q)
}
func (m *errMockTx) Commit() error   { return nil }
func (m *errMockTx) Rollback() error { return nil }
func (m *errMockTx) Close()          {}
func (m *errMockTx) IsOpen() bool    { return true }

// errMockConn returns errMockTx instances that fail.
type errMockConn struct {
	schemaTxErr error // error for schema tx (first tx)
	readTxErr   error // error for read tx (second tx)
	txIdx       int
}

func (m *errMockConn) Transaction(_ string, txType int) (Tx, error) {
	m.txIdx++
	if m.txIdx == 1 && m.schemaTxErr != nil {
		return &errMockTx{err: m.schemaTxErr}, nil
	}
	if m.txIdx == 2 && m.readTxErr != nil {
		return &errMockTx{err: m.readTxErr}, nil
	}
	return &mockTx{}, nil
}

func (m *errMockConn) Schema(string) (string, error)         { return "", nil }
func (m *errMockConn) DatabaseCreate(string) error           { return nil }
func (m *errMockConn) DatabaseDelete(string) error           { return nil }
func (m *errMockConn) DatabaseContains(string) (bool, error) { return true, nil }
func (m *errMockConn) DatabaseAll() ([]string, error)        { return nil, nil }
func (m *errMockConn) Close()                                {}
func (m *errMockConn) IsOpen() bool                          { return true }

func TestRunSequentialMigrations_EnsureSchemaError(t *testing.T) {
	conn := &errMockConn{schemaTxErr: fmt.Errorf("schema fail")}
	db := NewDatabase(conn, "test")
	noop := func(ctx context.Context, db *Database) error { return nil }

	_, err := RunSequentialMigrations(context.Background(), db, []SequentialMigration{
		{Name: "001", Up: noop},
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "ensure state schema") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestRunSequentialMigrations_AppliedQueryError(t *testing.T) {
	conn := &errMockConn{readTxErr: fmt.Errorf("read fail")}
	db := NewDatabase(conn, "test")
	noop := func(ctx context.Context, db *Database) error { return nil }

	_, err := RunSequentialMigrations(context.Background(), db, []SequentialMigration{
		{Name: "001", Up: noop},
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "query applied") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestStampSequentialMigrations_EnsureSchemaError(t *testing.T) {
	conn := &errMockConn{schemaTxErr: fmt.Errorf("schema fail")}
	db := NewDatabase(conn, "test")
	noop := func(ctx context.Context, db *Database) error { return nil }

	_, err := StampSequentialMigrations(context.Background(), db, []SequentialMigration{
		{Name: "001", Up: noop},
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "ensure state schema") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestStampSequentialMigrations_AppliedQueryError(t *testing.T) {
	conn := &errMockConn{readTxErr: fmt.Errorf("read fail")}
	db := NewDatabase(conn, "test")
	noop := func(ctx context.Context, db *Database) error { return nil }

	_, err := StampSequentialMigrations(context.Background(), db, []SequentialMigration{
		{Name: "001", Up: noop},
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "query applied") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestSeqMigrationStatus_EnsureSchemaError(t *testing.T) {
	conn := &errMockConn{schemaTxErr: fmt.Errorf("schema fail")}
	db := NewDatabase(conn, "test")
	noop := func(ctx context.Context, db *Database) error { return nil }

	_, err := SeqMigrationStatus(context.Background(), db, []SequentialMigration{
		{Name: "001", Up: noop},
	})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestRollbackSequentialMigration_ZeroSteps(t *testing.T) {
	result, err := RollbackSequentialMigration(context.Background(), nil, nil, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != nil {
		t.Errorf("expected nil, got %v", result)
	}
}

func TestRollbackSequentialMigration_EnsureSchemaError(t *testing.T) {
	conn := &errMockConn{schemaTxErr: fmt.Errorf("schema fail")}
	db := NewDatabase(conn, "test")

	_, err := RollbackSequentialMigration(context.Background(), db, nil, 1)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "ensure schema") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestRollbackSequentialMigration_AppliedQueryError(t *testing.T) {
	conn := &errMockConn{readTxErr: fmt.Errorf("read fail")}
	db := NewDatabase(conn, "test")

	_, err := RollbackSequentialMigration(context.Background(), db, nil, 1)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "query applied") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestRollbackSequentialMigration_MigrationNotFound(t *testing.T) {
	schemaTx := &mockTx{}
	readTx := &mockTx{responses: [][]map[string]any{
		{{"name": map[string]any{"value": "001_init"}, "applied-at": map[string]any{"value": "2024-01-01T00:00:00Z"}}},
	}}
	conn := &mockConn{txs: []*mockTx{schemaTx, readTx}}
	db := NewDatabase(conn, "test")

	// Provide empty migrations list — applied migration won't be found
	_, err := RollbackSequentialMigration(context.Background(), db, nil, 1)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestRollbackSequentialMigration_DownError(t *testing.T) {
	schemaTx := &mockTx{}
	readTx := &mockTx{responses: [][]map[string]any{
		{{"name": map[string]any{"value": "001_init"}, "applied-at": map[string]any{"value": "2024-01-01T00:00:00Z"}}},
	}}
	conn := &mockConn{txs: []*mockTx{schemaTx, readTx}}
	db := NewDatabase(conn, "test")

	migrations := []SequentialMigration{
		{Name: "001_init",
			Up:   func(ctx context.Context, db *Database) error { return nil },
			Down: func(ctx context.Context, db *Database) error { return fmt.Errorf("down boom") },
		},
	}

	_, err := RollbackSequentialMigration(context.Background(), db, migrations, 1)
	if err == nil {
		t.Fatal("expected error")
	}
	var seqErr *SeqMigrationError
	if ok := errorAs(err, &seqErr); !ok {
		t.Fatalf("expected SeqMigrationError, got %T: %v", err, err)
	}
}

func TestRollbackSequentialMigration_StepsExceedsApplied(t *testing.T) {
	schemaTx := &mockTx{}
	readTx := &mockTx{responses: [][]map[string]any{
		{{"name": map[string]any{"value": "001_init"}, "applied-at": map[string]any{"value": "2024-01-01T00:00:00Z"}}},
	}}
	deleteTx := &mockTx{}
	conn := &mockConn{txs: []*mockTx{schemaTx, readTx, deleteTx}}
	db := NewDatabase(conn, "test")

	migrations := []SequentialMigration{
		{Name: "001_init",
			Up:   func(ctx context.Context, db *Database) error { return nil },
			Down: func(ctx context.Context, db *Database) error { return nil },
		},
	}

	// Request 5 rollbacks but only 1 is applied — should clamp to 1
	rolledBack, err := RollbackSequentialMigration(context.Background(), db, migrations, 5)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(rolledBack) != 1 {
		t.Errorf("expected 1 rolled back, got %d", len(rolledBack))
	}
}

func TestTQLMigration_DownClosure(t *testing.T) {
	schemaTx := &mockTx{}
	conn := &mockConn{txs: []*mockTx{schemaTx}}
	db := NewDatabase(conn, "test")

	m := TQLMigration("001", []string{"define attribute name, value string;"}, []string{"undefine attribute name;"})
	err := m.Down(context.Background(), db)
	if err != nil {
		t.Fatalf("Down failed: %v", err)
	}
	if len(schemaTx.queries) != 1 {
		t.Errorf("expected 1 query, got %d", len(schemaTx.queries))
	}
}

func TestTQLMigration_UpErrorReturned(t *testing.T) {
	// errMockTx that fails on query
	conn := &errMockConn{schemaTxErr: fmt.Errorf("exec fail")}
	db := NewDatabase(conn, "test")

	m := TQLMigration("001", []string{"define attribute name, value string;"}, nil)
	err := m.Up(context.Background(), db)
	if err == nil {
		t.Fatal("expected error from Up")
	}
}

func TestTQLMigration_DownErrorReturned(t *testing.T) {
	conn := &errMockConn{schemaTxErr: fmt.Errorf("exec fail")}
	db := NewDatabase(conn, "test")

	m := TQLMigration("001", []string{"define attr;"}, []string{"undefine attr;"})
	err := m.Down(context.Background(), db)
	if err == nil {
		t.Fatal("expected error from Down")
	}
}

func TestStampSequentialMigrations_DryRunLogsStatements(t *testing.T) {
	schemaTx := &mockTx{}
	readTx := &mockTx{responses: [][]map[string]any{nil}}
	conn := &mockConn{txs: []*mockTx{schemaTx, readTx}}
	db := NewDatabase(conn, "test")

	m := TQLMigration("001_init", []string{"define attribute name, value string;", "define entity person;"}, nil)
	var logged []string
	_, err := StampSequentialMigrations(context.Background(), db, []SequentialMigration{m},
		WithSeqDryRun(),
		WithSeqLogger(func(msg string) { logged = append(logged, msg) }),
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// 1 "stamp" + 2 statement lines
	if len(logged) != 3 {
		t.Errorf("expected 3 log messages, got %d: %v", len(logged), logged)
	}
}

// --- MigrationChecksum ---

func TestMigrationChecksum_TQLMigration(t *testing.T) {
	m := TQLMigration("001", []string{"define attribute name, value string;"}, []string{"undefine attribute name;"})
	cs := MigrationChecksum(m)
	if len(cs) != 64 {
		t.Errorf("expected 64-char hex, got %d: %q", len(cs), cs)
	}
	// Deterministic
	if cs != MigrationChecksum(m) {
		t.Error("expected deterministic checksum")
	}
}

func TestMigrationChecksum_NilStatements(t *testing.T) {
	m := SequentialMigration{Name: "001", Up: func(ctx context.Context, db *Database) error { return nil }}
	cs := MigrationChecksum(m)
	if cs != "" {
		t.Errorf("expected empty checksum for nil Statements, got %q", cs)
	}
}

func TestMigrationChecksum_DifferentStatements(t *testing.T) {
	m1 := TQLMigration("001", []string{"define attribute name, value string;"}, nil)
	m2 := TQLMigration("001", []string{"define attribute email, value string;"}, nil)
	if MigrationChecksum(m1) == MigrationChecksum(m2) {
		t.Error("different statements should produce different checksums")
	}
}

func TestChecksumMismatchError(t *testing.T) {
	err := &ChecksumMismatchError{
		Name:     "001_init",
		Expected: "abcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890",
		Actual:   "1234567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef",
	}
	msg := err.Error()
	if !strings.Contains(msg, "001_init") {
		t.Errorf("expected migration name in error: %s", msg)
	}
	if !strings.Contains(msg, "checksum mismatch") {
		t.Errorf("expected 'checksum mismatch' in error: %s", msg)
	}
}

func TestRunSequentialMigrations_ChecksumMismatch(t *testing.T) {
	schemaTx := &mockTx{}
	readTx := &mockTx{responses: [][]map[string]any{
		{{"name": map[string]any{"value": "001_init"},
			"applied-at": map[string]any{"value": "2024-01-01T00:00:00Z"},
			"checksum":   map[string]any{"value": "wrongchecksum1234567890123456789012345678901234567890abcd"}}},
	}}
	conn := &mockConn{txs: []*mockTx{schemaTx, readTx}}
	db := NewDatabase(conn, "test")

	m := TQLMigration("001_init", []string{"define attribute name, value string;"}, nil)
	_, err := RunSequentialMigrations(context.Background(), db, []SequentialMigration{m})
	if err == nil {
		t.Fatal("expected checksum mismatch error")
	}
	if _, ok := errors.AsType[*ChecksumMismatchError](err); !ok {
		t.Fatalf("expected ChecksumMismatchError, got %T: %v", err, err)
	}
}

// --- #59: WithSeqTarget must not overshoot ---

func TestRunSequentialMigrations_TargetNotFound(t *testing.T) {
	var executed []string
	makeMigration := func(name string) SequentialMigration {
		n := name
		return SequentialMigration{
			Name: n,
			Up: func(ctx context.Context, db *Database) error {
				executed = append(executed, n)
				return nil
			},
		}
	}

	schemaTx := &mockTx{}
	readTx := &mockTx{responses: [][]map[string]any{nil}}
	conn := &mockConn{txs: []*mockTx{schemaTx, readTx}}
	db := NewDatabase(conn, "test")

	migrations := []SequentialMigration{
		makeMigration("001_first"),
		makeMigration("002_add_email"),
	}

	// Typo'd target: must error instead of silently applying everything.
	_, err := RunSequentialMigrations(context.Background(), db, migrations, WithSeqTarget("002_add_emial"))
	if err == nil {
		t.Fatal("expected error for unknown target")
	}
	if !strings.Contains(err.Error(), "002_add_emial") || !strings.Contains(err.Error(), "not found") {
		t.Errorf("error should name the missing target: %v", err)
	}
	if len(executed) != 0 {
		t.Errorf("no migration should run for an unknown target, executed: %v", executed)
	}
}

func TestRunSequentialMigrations_TargetNotFound_DryRun(t *testing.T) {
	schemaTx := &mockTx{}
	readTx := &mockTx{responses: [][]map[string]any{nil}}
	conn := &mockConn{txs: []*mockTx{schemaTx, readTx}}
	db := NewDatabase(conn, "test")

	noop := func(ctx context.Context, db *Database) error { return nil }
	migrations := []SequentialMigration{{Name: "001_first", Up: noop}}

	_, err := RunSequentialMigrations(context.Background(), db, migrations,
		WithSeqDryRun(), WithSeqTarget("nope"))
	if err == nil {
		t.Fatal("expected error for unknown target in dry-run mode")
	}
}

func TestRunSequentialMigrations_TargetAlreadyApplied(t *testing.T) {
	var executed []string
	makeMigration := func(name string) SequentialMigration {
		n := name
		return SequentialMigration{
			Name: n,
			Up: func(ctx context.Context, db *Database) error {
				executed = append(executed, n)
				return nil
			},
		}
	}

	schemaTx := &mockTx{}
	readTx := &mockTx{responses: [][]map[string]any{
		// 002_second is already applied; 001_first and 003_third are not.
		{{"name": map[string]any{"value": "002_second"}, "applied-at": map[string]any{"value": "2024-01-01T00:00:00Z"}}},
	}}
	recordTx := &mockTx{}
	conn := &mockConn{txs: []*mockTx{schemaTx, readTx, recordTx}}
	db := NewDatabase(conn, "test")

	migrations := []SequentialMigration{
		makeMigration("001_first"),
		makeMigration("002_second"),
		makeMigration("003_third"),
	}

	// Targeting the already-applied 002_second must stop there: 001_first
	// (pending, before the target) applies; 003_third must NOT.
	applied, err := RunSequentialMigrations(context.Background(), db, migrations, WithSeqTarget("002_second"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(applied) != 1 || applied[0] != "001_first" {
		t.Fatalf("expected [001_first], got %v", applied)
	}
	if len(executed) != 1 || executed[0] != "001_first" {
		t.Errorf("expected only 001_first to run, got %v", executed)
	}
}

func TestStampSequentialMigrations_TargetNotFound(t *testing.T) {
	noop := func(ctx context.Context, db *Database) error { return nil }
	schemaTx := &mockTx{}
	readTx := &mockTx{responses: [][]map[string]any{nil}}
	conn := &mockConn{txs: []*mockTx{schemaTx, readTx}}
	db := NewDatabase(conn, "test")

	migrations := []SequentialMigration{
		{Name: "001_first", Up: noop},
		{Name: "002_second", Up: noop},
	}

	_, err := StampSequentialMigrations(context.Background(), db, migrations, WithSeqTarget("002_secnod"))
	if err == nil {
		t.Fatal("expected error for unknown stamp target")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestStampSequentialMigrations_TargetAlreadyApplied(t *testing.T) {
	noop := func(ctx context.Context, db *Database) error { return nil }
	schemaTx := &mockTx{}
	readTx := &mockTx{responses: [][]map[string]any{
		{{"name": map[string]any{"value": "002_second"}, "applied-at": map[string]any{"value": "2024-01-01T00:00:00Z"}}},
	}}
	recordTx := &mockTx{}
	conn := &mockConn{txs: []*mockTx{schemaTx, readTx, recordTx}}
	db := NewDatabase(conn, "test")

	migrations := []SequentialMigration{
		{Name: "001_first", Up: noop},
		{Name: "002_second", Up: noop},
		{Name: "003_third", Up: noop},
	}

	stamped, err := StampSequentialMigrations(context.Background(), db, migrations, WithSeqTarget("002_second"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(stamped) != 1 || stamped[0] != "001_first" {
		t.Fatalf("expected [001_first], got %v", stamped)
	}
}

// --- #60: statement-based migrations apply and record atomically ---

// txTypeRecordingConn wraps mockConn and records the transaction type of
// each Transaction call, so tests can assert schema vs write routing.
type txTypeRecordingConn struct {
	mockConn
	txTypes []int
}

func (c *txTypeRecordingConn) Transaction(dbName string, txType int) (Tx, error) {
	c.txTypes = append(c.txTypes, txType)
	return c.mockConn.Transaction(dbName, txType)
}

func TestRunSequentialMigrations_StatementsApplyAndRecordInOneTx(t *testing.T) {
	schemaTx := &mockTx{}
	readTx := &mockTx{responses: [][]map[string]any{nil}}
	migrationTx := &mockTx{}
	conn := &txTypeRecordingConn{mockConn: mockConn{txs: []*mockTx{schemaTx, readTx, migrationTx}}}
	db := NewDatabase(conn, "test")

	m := TQLMigration("001_seed", []string{
		`insert $p isa person, has name "Alice";`,
		`insert $p isa person, has name "Bob";`,
	}, nil)

	applied, err := RunSequentialMigrations(context.Background(), db, []SequentialMigration{m})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(applied) != 1 {
		t.Fatalf("expected 1 applied, got %d", len(applied))
	}
	// Both statements AND the state record must be in the same transaction.
	if len(migrationTx.queries) != 3 {
		t.Fatalf("expected 3 queries in one tx (2 statements + record), got %d: %v",
			len(migrationTx.queries), migrationTx.queries)
	}
	if !strings.Contains(migrationTx.queries[2], "seq-migration-record") {
		t.Errorf("last query should insert the migration record, got: %s", migrationTx.queries[2])
	}
	if !migrationTx.committed {
		t.Error("migration transaction should be committed")
	}
	// All statements are data writes: must use a write transaction.
	if got := conn.txTypes[2]; got != int(WriteTransaction) {
		t.Errorf("expected write transaction (%d), got %d", int(WriteTransaction), got)
	}
}

func TestRunSequentialMigrations_MixedStatementsUseSchemaTx(t *testing.T) {
	schemaTx := &mockTx{}
	readTx := &mockTx{responses: [][]map[string]any{nil}}
	migrationTx := &mockTx{}
	conn := &txTypeRecordingConn{mockConn: mockConn{txs: []*mockTx{schemaTx, readTx, migrationTx}}}
	db := NewDatabase(conn, "test")

	m := TQLMigration("001_schema_and_data", []string{
		"define attribute name, value string;",
		`insert $p isa person, has name "Alice";`,
	}, nil)

	if _, err := RunSequentialMigrations(context.Background(), db, []SequentialMigration{m}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(migrationTx.queries) != 3 {
		t.Fatalf("expected 3 queries in one tx, got %d", len(migrationTx.queries))
	}
	// Any schema statement in the batch promotes the whole tx to schema type.
	if got := conn.txTypes[2]; got != int(SchemaTransaction) {
		t.Errorf("expected schema transaction (%d), got %d", int(SchemaTransaction), got)
	}
}

func TestRunSequentialMigrations_StatementFailureRecordsNothing(t *testing.T) {
	schemaTx := &mockTx{}
	readTx := &mockTx{responses: [][]map[string]any{nil}}
	failTx := &errMockTx{err: fmt.Errorf("statement boom")}
	conn := &errTxAfterConn{good: []*mockTx{schemaTx, readTx}, bad: failTx}
	db := NewDatabase(conn, "test")

	m := TQLMigration("001_seed", []string{`insert $p isa person, has name "Alice";`}, nil)

	applied, err := RunSequentialMigrations(context.Background(), db, []SequentialMigration{m})
	if err == nil {
		t.Fatal("expected error")
	}
	var seqErr *SeqMigrationError
	if ok := errorAs(err, &seqErr); !ok {
		t.Fatalf("expected SeqMigrationError, got %T: %v", err, err)
	}
	if len(applied) != 0 {
		t.Errorf("expected 0 applied, got %v", applied)
	}
}

// errTxAfterConn returns the preset good transactions first, then the bad one.
type errTxAfterConn struct {
	good []*mockTx
	bad  Tx
	idx  int
}

func (c *errTxAfterConn) Transaction(_ string, _ int) (Tx, error) {
	if c.idx < len(c.good) {
		tx := c.good[c.idx]
		c.idx++
		return tx, nil
	}
	return c.bad, nil
}

func (c *errTxAfterConn) Schema(string) (string, error)         { return "", nil }
func (c *errTxAfterConn) DatabaseCreate(string) error           { return nil }
func (c *errTxAfterConn) DatabaseDelete(string) error           { return nil }
func (c *errTxAfterConn) DatabaseContains(string) (bool, error) { return true, nil }
func (c *errTxAfterConn) DatabaseAll() ([]string, error)        { return nil, nil }
func (c *errTxAfterConn) Close()                                {}
func (c *errTxAfterConn) IsOpen() bool                          { return true }

func TestRunSequentialMigrations_CustomUpStillRecordsSeparately(t *testing.T) {
	// Custom Up functions cannot participate in the atomic path: the record
	// insert happens in its own write transaction after Up succeeds.
	upCalled := false
	schemaTx := &mockTx{}
	readTx := &mockTx{responses: [][]map[string]any{nil}}
	recordTx := &mockTx{}
	conn := &mockConn{txs: []*mockTx{schemaTx, readTx, recordTx}}
	db := NewDatabase(conn, "test")

	m := SequentialMigration{
		Name: "001_custom",
		Up: func(ctx context.Context, db *Database) error {
			upCalled = true
			return nil
		},
	}

	applied, err := RunSequentialMigrations(context.Background(), db, []SequentialMigration{m})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !upCalled {
		t.Error("custom Up should have been called")
	}
	if len(applied) != 1 {
		t.Fatalf("expected 1 applied, got %d", len(applied))
	}
	if len(recordTx.queries) != 1 || !strings.Contains(recordTx.queries[0], "seq-migration-record") {
		t.Errorf("expected a standalone record insert, got: %v", recordTx.queries)
	}
}

func TestRollbackSequentialMigration_StatementsDeleteRecordInOneTx(t *testing.T) {
	schemaTx := &mockTx{}
	readTx := &mockTx{responses: [][]map[string]any{
		{{"name": map[string]any{"value": "001_init"}, "applied-at": map[string]any{"value": "2024-01-01T00:00:00Z"}}},
	}}
	rollbackTx := &mockTx{}
	conn := &txTypeRecordingConn{mockConn: mockConn{txs: []*mockTx{schemaTx, readTx, rollbackTx}}}
	db := NewDatabase(conn, "test")

	m := TQLMigration("001_init",
		[]string{`insert $p isa person, has name "Alice";`},
		[]string{`match $p isa person, has name "Alice"; delete $p;`})

	rolledBack, err := RollbackSequentialMigration(context.Background(), db, []SequentialMigration{m}, 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(rolledBack) != 1 {
		t.Fatalf("expected 1 rolled back, got %d", len(rolledBack))
	}
	// Down statement and the record deletion must share one transaction.
	if len(rollbackTx.queries) != 2 {
		t.Fatalf("expected 2 queries in one tx (down + delete record), got %d: %v",
			len(rollbackTx.queries), rollbackTx.queries)
	}
	if !strings.Contains(rollbackTx.queries[1], "seq-migration-record") {
		t.Errorf("last query should delete the migration record, got: %s", rollbackTx.queries[1])
	}
	if !rollbackTx.committed {
		t.Error("rollback transaction should be committed")
	}
}

// --- #98: ChecksumMismatchError must not panic on short checksums ---

func TestChecksumMismatchError_ShortChecksum(t *testing.T) {
	err := &ChecksumMismatchError{
		Name:     "001_init",
		Expected: "abc", // hand-stamped or corrupted DB record
		Actual:   "1234567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef",
	}
	// Must not panic.
	msg := err.Error()
	if !strings.Contains(msg, "abc") {
		t.Errorf("short checksum should be printed verbatim: %s", msg)
	}
	if !strings.Contains(msg, "1234567890ab...") {
		t.Errorf("long checksum should be truncated to 12 chars: %s", msg)
	}

	// Both short, including empty.
	err = &ChecksumMismatchError{Name: "001", Expected: "", Actual: "x"}
	if msg = err.Error(); !strings.Contains(msg, "checksum mismatch") {
		t.Errorf("unexpected message: %s", msg)
	}
}

// --- #99: checksum must delimit statements ---

func TestMigrationChecksum_StatementBoundaries(t *testing.T) {
	// Same concatenated bytes, different statement boundaries.
	m1 := TQLMigration("001", []string{"define a;", "define b;"}, nil)
	m2 := TQLMigration("001", []string{"define a;define b;"}, nil)
	if MigrationChecksum(m1) == MigrationChecksum(m2) {
		t.Error("re-split Up statements must change the checksum")
	}

	// Text crossing the Up/Down group separator must not collide.
	m3 := TQLMigration("001", []string{"x|"}, nil)
	m4 := TQLMigration("001", []string{"x"}, []string{"|"})
	if MigrationChecksum(m3) == MigrationChecksum(m4) {
		t.Error("moving text between Up and Down must change the checksum")
	}
}

func TestMigrationChecksum_LegacyCollisions(t *testing.T) {
	// Demonstrate that the legacy format collided on these inputs — this is
	// exactly why the algorithm changed. If this fails, the legacy
	// reproduction has drifted from the historical format.
	m1 := TQLMigration("001", []string{"define a;", "define b;"}, nil)
	m2 := TQLMigration("001", []string{"define a;define b;"}, nil)
	if legacyMigrationChecksum(m1) != legacyMigrationChecksum(m2) {
		t.Error("legacy checksum reproduction changed; verification of old records would break")
	}
}

func TestVerifySeqChecksums_AcceptsLegacyChecksum(t *testing.T) {
	m := TQLMigration("001_init", []string{"define attribute name, value string;"}, nil)
	legacy := legacyMigrationChecksum(m)
	if legacy == MigrationChecksum(m) {
		t.Fatal("test premise broken: legacy and current formats should differ")
	}

	schemaTx := &mockTx{}
	readTx := &mockTx{responses: [][]map[string]any{
		{{"name": map[string]any{"value": "001_init"},
			"applied-at": map[string]any{"value": "2024-01-01T00:00:00Z"},
			"checksum":   map[string]any{"value": legacy}}},
	}}
	conn := &mockConn{txs: []*mockTx{schemaTx, readTx}}
	db := NewDatabase(conn, "test")

	// A record stamped by go-typeql <= v1.12.x must still verify cleanly.
	applied, err := RunSequentialMigrations(context.Background(), db, []SequentialMigration{m})
	if err != nil {
		t.Fatalf("legacy checksum should be accepted, got: %v", err)
	}
	if len(applied) != 0 {
		t.Errorf("expected 0 applied (already recorded), got %v", applied)
	}
}

func TestVerifySeqChecksums_NewFormatAccepted(t *testing.T) {
	m := TQLMigration("001_init", []string{"define attribute name, value string;"}, nil)

	schemaTx := &mockTx{}
	readTx := &mockTx{responses: [][]map[string]any{
		{{"name": map[string]any{"value": "001_init"},
			"applied-at": map[string]any{"value": "2024-01-01T00:00:00Z"},
			"checksum":   map[string]any{"value": MigrationChecksum(m)}}},
	}}
	conn := &mockConn{txs: []*mockTx{schemaTx, readTx}}
	db := NewDatabase(conn, "test")

	if _, err := RunSequentialMigrations(context.Background(), db, []SequentialMigration{m}); err != nil {
		t.Fatalf("current checksum should be accepted, got: %v", err)
	}
}

func TestRunSequentialMigrations_RecordsNewChecksumFormat(t *testing.T) {
	schemaTx := &mockTx{}
	readTx := &mockTx{responses: [][]map[string]any{nil}}
	migrationTx := &mockTx{}
	conn := &mockConn{txs: []*mockTx{schemaTx, readTx, migrationTx}}
	db := NewDatabase(conn, "test")

	m := TQLMigration("001_init", []string{`insert $p isa person, has name "A";`}, nil)
	if _, err := RunSequentialMigrations(context.Background(), db, []SequentialMigration{m}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	recordQuery := migrationTx.queries[len(migrationTx.queries)-1]
	if !strings.Contains(recordQuery, MigrationChecksum(m)) {
		t.Errorf("record should carry the new checksum format: %s", recordQuery)
	}
	if legacy := legacyMigrationChecksum(m); strings.Contains(recordQuery, legacy) {
		t.Errorf("record should not carry the legacy checksum: %s", recordQuery)
	}
}

// errorAs is a helper that wraps errors.As to work with generics in tests.
func errorAs(err error, target any) bool {
	// Use type assertion approach since we can't import errors in a simple way
	switch t := target.(type) {
	case **SeqMigrationError:
		for err != nil {
			if e, ok := err.(*SeqMigrationError); ok {
				*t = e
				return true
			}
			if u, ok := err.(interface{ Unwrap() error }); ok {
				err = u.Unwrap()
			} else {
				return false
			}
		}
	}
	return false
}
