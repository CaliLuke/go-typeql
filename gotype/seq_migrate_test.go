package gotype

import (
	"context"
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
