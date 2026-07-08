package gotype

import (
	"context"
	"strings"
	"testing"

	"github.com/CaliLuke/go-typeql/internal/typeqlcheck"
	"github.com/CaliLuke/go-typeql/tqlgen"
)

// assertTypeQL validates generated TypeQL with the official typeql-check CLI
// (soft dependency — see internal/typeqlcheck). knownIssue marks output that
// is currently invalid because of an open bug: the case is skipped while the
// bug exists and fails loudly once the output becomes valid, so the marker
// must be removed when the issue is fixed.
func assertTypeQL(t *testing.T, label, query, knownIssue string) {
	t.Helper()
	if knownIssue == "" {
		typeqlcheck.AssertValid(t, label, query)
		return
	}
	if !typeqlcheck.Available() {
		return
	}
	if err := typeqlcheck.Validate(query); err != nil {
		t.Skipf("%s: known invalid output (%s): %v", label, knownIssue, err)
	}
	t.Errorf("%s: output is now valid TypeQL — %s appears fixed, remove the knownIssue marker", label, knownIssue)
}

// TestTypeQLSyntax_CRUDQueries drives the Manager through the mock connection
// and pipes every query it generated through typeql-check.
func TestTypeQLSyntax_CRUDQueries(t *testing.T) {
	registerTestTypes(t)

	t.Run("insert", func(t *testing.T) {
		writeTx := &mockTx{responses: [][]map[string]any{{{"_iid": "0xABC123"}}}}
		conn := &mockConn{txs: []*mockTx{writeTx}}
		mgr := MustNewManager[testPerson](NewDatabase(conn, "test_db"))

		age := 30
		if err := mgr.Insert(context.Background(), &testPerson{Name: "Alice", Email: "a@example.com", Age: &age}); err != nil {
			t.Fatalf("Insert failed: %v", err)
		}
		for i, q := range writeTx.queries {
			assertTypeQL(t, "insert query", q, "")
			_ = i
		}
	})

	t.Run("update scalar attributes", func(t *testing.T) {
		writeTx := &mockTx{responses: [][]map[string]any{nil}}
		conn := &mockConn{txs: []*mockTx{writeTx}}
		mgr := MustNewManager[testPerson](NewDatabase(conn, "test_db"))

		age := 31
		p := &testPerson{Name: "Alice", Email: "new@example.com", Age: &age}
		p.SetIID("0xABC123")
		if err := mgr.Update(context.Background(), p); err != nil {
			t.Fatalf("Update failed: %v", err)
		}
		for _, q := range writeTx.queries {
			assertTypeQL(t, "update query", q, "")
		}
	})

	t.Run("delete", func(t *testing.T) {
		writeTx := &mockTx{}
		conn := &mockConn{txs: []*mockTx{writeTx}}
		mgr := MustNewManager[testPerson](NewDatabase(conn, "test_db"))

		p := &testPerson{Name: "Alice"}
		p.SetIID("0xABC123")
		if err := mgr.Delete(context.Background(), p); err != nil {
			t.Fatalf("Delete failed: %v", err)
		}
		for _, q := range writeTx.queries {
			assertTypeQL(t, "delete query", q, "")
		}
	})

	t.Run("filtered query with sort and limit", func(t *testing.T) {
		readTx := &mockTx{}
		conn := &mockConn{txs: []*mockTx{readTx}}
		mgr := MustNewManager[testPerson](NewDatabase(conn, "test_db"))

		_, err := mgr.Query().
			Filter(Gt("age", 20)).
			Filter(Contains("email", "example")).
			OrderAsc("name").
			Limit(10).
			All(context.Background())
		if err != nil {
			t.Fatalf("query failed: %v", err)
		}
		for _, q := range readTx.queries {
			assertTypeQL(t, "filtered query", q, "")
		}
	})

	t.Run("count", func(t *testing.T) {
		readTx := &mockTx{responses: [][]map[string]any{{{"count": int64(2)}}}}
		conn := &mockConn{txs: []*mockTx{readTx}}
		mgr := MustNewManager[testPerson](NewDatabase(conn, "test_db"))

		if _, err := mgr.Query().Filter(Eq("name", "Alice")).Count(context.Background()); err != nil {
			t.Fatalf("count failed: %v", err)
		}
		for _, q := range readTx.queries {
			assertTypeQL(t, "count query", q, "")
		}
	})

	t.Run("filtered delete with distinct pipeline", func(t *testing.T) {
		writeTx := &mockTx{responses: [][]map[string]any{{{"count": int64(1)}}, nil}}
		conn := &mockConn{txs: []*mockTx{writeTx}}
		mgr := MustNewManager[testPerson](NewDatabase(conn, "test_db"))

		if _, err := mgr.Query().Filter(Gt("age", 20)).Delete(context.Background()); err != nil {
			t.Fatalf("query delete failed: %v", err)
		}
		for _, q := range writeTx.queries {
			assertTypeQL(t, "query delete", q, "")
		}
	})

	t.Run("bulk update", func(t *testing.T) {
		writeTx := &mockTx{responses: [][]map[string]any{{{"count": int64(1)}}, nil}}
		conn := &mockConn{txs: []*mockTx{writeTx}}
		mgr := MustNewManager[testPerson](NewDatabase(conn, "test_db"))

		if _, err := mgr.Query().Filter(Eq("name", "Alice")).Update(context.Background(), map[string]any{
			"email": "bulk@example.com",
			"age":   40,
		}); err != nil {
			t.Fatalf("bulk update failed: %v", err)
		}
		for _, q := range writeTx.queries {
			assertTypeQL(t, "bulk update query", q, "")
		}
	})

	t.Run("update multi-valued attribute", func(t *testing.T) {
		ClearRegistry()
		MustRegister[testTagged]()
		writeTx := &mockTx{}
		conn := &mockConn{txs: []*mockTx{writeTx}}
		mgr := MustNewManager[testTagged](NewDatabase(conn, "test_db"))

		e := &testTagged{Label: "doc1", Tags: []string{"alpha", "beta"}}
		e.SetIID("0xABC123")
		if err := mgr.Update(context.Background(), e); err != nil {
			t.Fatalf("update failed: %v", err)
		}
		for _, q := range writeTx.queries {
			assertTypeQL(t, "multi-valued update query", q, "")
		}
	})

	t.Run("relation insert with slice role players", func(t *testing.T) {
		ClearRegistry()
		MustRegister[testPerson]()
		MustRegister[testTeam]()
		writeTx := &mockTx{responses: [][]map[string]any{{{"_iid": "0xREL1"}}}}
		conn := &mockConn{txs: []*mockTx{writeTx}}
		mgr := MustNewManager[testTeam](NewDatabase(conn, "test_db"))

		team := &testTeam{
			Members: []*testPerson{
				{Name: "Alice", Email: "a@example.com"},
				{Name: "Bob", Email: "b@example.com"},
			},
			Squad: "alpha",
		}
		if err := mgr.Insert(context.Background(), team); err != nil {
			t.Fatalf("relation insert failed: %v", err)
		}
		for _, q := range writeTx.queries {
			assertTypeQL(t, "multi-player relation insert", q, "")
		}
	})
}

// TestTypeQLSyntax_GeneratedSchema validates the full registry-derived schema.
func TestTypeQLSyntax_GeneratedSchema(t *testing.T) {
	registerTestTypes(t)
	schema := GenerateSchema()
	if !strings.Contains(schema, "define") {
		t.Fatalf("unexpected schema output:\n%s", schema)
	}
	assertTypeQL(t, "GenerateSchema", schema, "")
}

// TestTypeQLSyntax_SupertypeSchema validates the `entity child sub parent`
// shape emitted for models declaring a sub: tag (issue #91).
func TestTypeQLSyntax_SupertypeSchema(t *testing.T) {
	ClearRegistry()
	MustRegister[zzzParentModel]()
	MustRegister[aaaChildModel]()
	assertTypeQL(t, "GenerateSchema with sub clause", GenerateSchema(), "")
}

// TestTypeQLSyntax_MigrationStatements validates the statements the diff-based
// migration path would execute against a live server.
func TestTypeQLSyntax_MigrationStatements(t *testing.T) {
	registerTestTypes(t)

	t.Run("additive plan from empty schema", func(t *testing.T) {
		diff := DiffSchemaFromRegistry(&tqlgen.ParsedSchema{})
		stmts := diff.GenerateMigration()
		if len(stmts) == 0 {
			t.Fatal("expected additive statements from an empty current schema")
		}
		for _, s := range stmts {
			assertTypeQL(t, "additive migration statement", s, "")
		}
	})

	t.Run("destructive statements", func(t *testing.T) {
		diff := &SchemaDiff{RemoveTypes: []string{"obsolete"}}
		stmts := diff.GenerateMigrationWithOpts(WithDestructive())
		if len(stmts) == 0 {
			t.Fatal("expected destructive statements")
		}
		for _, s := range stmts {
			assertTypeQL(t, "destructive migration statement", s, "")
		}
	})

	t.Run("operation forward and rollback statements", func(t *testing.T) {
		ops := []Operation{
			AddAttribute{Name: "email", ValueType: "string"},
			AddEntity{Name: "person"},
			AddRelation{Name: "employment"},
			AddOwnership{Owner: "person", Attribute: "email"},
			AddRole{Relation: "employment", Role: "employee"},
			AddRolePlayer{Entity: "person", Relation: "employment", Role: "employee"},
			RemoveAttribute{Name: "email"},
			RemoveEntity{Name: "person"},
			RemoveRelation{Name: "employment"},
			RemoveOwnership{Owner: "person", Attribute: "email"},
			RemoveRole{Relation: "employment", Role: "employee"},
			RemoveRolePlayer{Entity: "person", Relation: "employment", Role: "employee"},
		}
		for _, op := range ops {
			assertTypeQL(t, "forward op", op.ToTypeQL(), "")
			if op.IsReversible() {
				assertTypeQL(t, "rollback op", op.RollbackTypeQL(), "")
			}
		}
	})
}
