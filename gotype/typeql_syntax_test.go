package gotype

import (
	"context"
	"strings"
	"testing"
	"time"

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

// TestTypeQLSyntax_FilterEmissions validates the query text produced by the
// filter combinators changed in the filters & validation cluster (issues
// #46, #48, #85): scoped Or/Not branches over role players and computed
// variables, escaped Startswith prefixes, and the empty-In contradiction.
func TestTypeQLSyntax_FilterEmissions(t *testing.T) {
	runFiltered := func(t *testing.T, label string, filters ...Filter) {
		t.Helper()
		registerMultiValueTypes(t)
		readTx := &mockTx{}
		conn := &mockConn{txs: []*mockTx{readTx}}
		mgr := MustNewManager[testPerson](NewDatabase(conn, "test_db"))
		if _, err := mgr.Query().Filter(filters...).All(context.Background()); err != nil {
			t.Fatalf("%s query failed: %v", label, err)
		}
		for _, q := range readTx.queries {
			assertTypeQL(t, label, q, "")
		}
	}

	t.Run("empty In contradiction", func(t *testing.T) {
		runFiltered(t, "empty In query", In("name", nil))
	})

	t.Run("empty IIDIn contradiction", func(t *testing.T) {
		runFiltered(t, "empty IIDIn query", IIDIn())
	})

	t.Run("startswith with regex metacharacters", func(t *testing.T) {
		runFiltered(t, "startswith query", Startswith("email", "j.smith@corp.com"))
	})

	t.Run("or of computed filters", func(t *testing.T) {
		runFiltered(t, "or-computed query",
			Or(
				And(Gt("age", 0), Computed("doubled", ArithmeticExpr("e", "age", "*", "age"), ">", 100)),
				And(Gt("age", 0), Computed("doubled", ArithmeticExpr("e", "age", "*", "age"), "<", 10)),
			))
	})

	t.Run("not of empty In", func(t *testing.T) {
		runFiltered(t, "not-empty-in query", Not(In("name", nil)))
	})

	t.Run("sibling or filters over the same attribute", func(t *testing.T) {
		// Sibling or blocks share one per-query scope, so the branches of
		// both blocks bind distinct deterministic variables (_o1.._o4).
		runFiltered(t, "sibling-or query",
			Or(Eq("name", "Alice"), Eq("name", "Bob")),
			Or(Eq("name", "Carol"), Eq("name", "Dave")),
		)
	})

	t.Run("or nested inside not", func(t *testing.T) {
		runFiltered(t, "or-inside-not query",
			Not(Or(Eq("name", "Alice"), Eq("name", "Bob"))))
	})

	t.Run("or of role players", func(t *testing.T) {
		registerMultiValueTypes(t)
		readTx := &mockTx{}
		conn := &mockConn{txs: []*mockTx{readTx}}
		mgr := MustNewManager[testTeam](NewDatabase(conn, "test_db"))
		f := Or(
			RolePlayer("member", Eq("name", "Alice")),
			RolePlayer("member", Eq("name", "Bob")),
		)
		if _, err := mgr.Query().Filter(f).All(context.Background()); err != nil {
			t.Fatalf("or-roleplayer query failed: %v", err)
		}
		for _, q := range readTx.queries {
			assertTypeQL(t, "or-roleplayer query", q, "")
		}
	})
}

// TestTypeQLSyntax_DatetimeInsert validates the datetime literal shape the
// Manager emits for time.Time fields (issues #53/#66): naive UTC datetime
// literals, including midnight and non-UTC sub-second values.
func TestTypeQLSyntax_DatetimeInsert(t *testing.T) {
	ClearRegistry()
	MustRegister[entityWithDatetime]()

	values := []time.Time{
		time.Date(2024, 1, 15, 10, 30, 0, 0, time.UTC),
		time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC), // midnight must stay a datetime literal
		time.Date(2024, 1, 15, 12, 30, 0, 123456789, time.FixedZone("UTC+2", 2*60*60)),
	}
	for _, v := range values {
		writeTx := &mockTx{responses: [][]map[string]any{{{"_iid": "0xABC123"}}}}
		conn := &mockConn{txs: []*mockTx{writeTx}}
		mgr := MustNewManager[entityWithDatetime](NewDatabase(conn, "test_db"))

		if err := mgr.Insert(context.Background(), &entityWithDatetime{Name: "e", CreatedAt: v}); err != nil {
			t.Fatalf("Insert failed: %v", err)
		}
		for _, q := range writeTx.queries {
			assertTypeQL(t, "datetime insert query", q, "")
		}

		// Update path (delete-old/insert-new) formats datetimes via
		// FormatGoValue; both halves must agree on the literal shape.
		updateTx := &mockTx{responses: [][]map[string]any{nil}}
		conn = &mockConn{txs: []*mockTx{updateTx}}
		mgr = MustNewManager[entityWithDatetime](NewDatabase(conn, "test_db"))
		e := &entityWithDatetime{Name: "e", CreatedAt: v}
		e.SetIID("0xABC123")
		if err := mgr.Update(context.Background(), e); err != nil {
			t.Fatalf("Update failed: %v", err)
		}
		for _, q := range updateTx.queries {
			assertTypeQL(t, "datetime update query", q, "")
		}
	}
}

// TestTypeQLSyntax_DecimalQueries validates the decimal literal shapes emitted
// for fields declared with the value:decimal tag option: the schema define
// (`value decimal`), insert/update/put has-clauses (`has price 12.5dec`,
// integral values keep a fraction: `3.0dec`), key-match clauses, and filtered
// Get matches.
func TestTypeQLSyntax_DecimalQueries(t *testing.T) {
	registerDecimal := func() {
		ClearRegistry()
		MustRegister[decimalProduct]()
	}

	t.Run("schema define with value decimal", func(t *testing.T) {
		registerDecimal()
		MustRegister[decimalKeyed]()
		assertTypeQL(t, "decimal schema", GenerateSchema(), "")
	})

	t.Run("insert with decimal literals", func(t *testing.T) {
		registerDecimal()
		writeTx := &mockTx{responses: [][]map[string]any{{{"_iid": "0xABC123"}}}}
		conn := &mockConn{txs: []*mockTx{writeTx}}
		mgr := MustNewManager[decimalProduct](NewDatabase(conn, "test_db"))

		tier := 3.0 // integral value must still emit a fraction (3.0dec)
		p := &decimalProduct{SKU: "widget-1", Price: 12.5, Exact: "10.99", Tier: &tier, History: []float64{1.25}}
		if err := mgr.Insert(context.Background(), p); err != nil {
			t.Fatalf("Insert failed: %v", err)
		}
		for _, q := range writeTx.queries {
			assertTypeQL(t, "decimal insert query", q, "")
		}
	})

	t.Run("update with decimal literals", func(t *testing.T) {
		registerDecimal()
		writeTx := &mockTx{responses: [][]map[string]any{nil}}
		conn := &mockConn{txs: []*mockTx{writeTx}}
		mgr := MustNewManager[decimalProduct](NewDatabase(conn, "test_db"))

		p := &decimalProduct{SKU: "widget-1", Price: 42.0, Exact: "0.1"}
		p.SetIID("0xABC123")
		if err := mgr.Update(context.Background(), p); err != nil {
			t.Fatalf("Update failed: %v", err)
		}
		for _, q := range writeTx.queries {
			assertTypeQL(t, "decimal update query", q, "")
		}
	})

	t.Run("put with decimal key match", func(t *testing.T) {
		ClearRegistry()
		MustRegister[decimalKeyed]()
		writeTx := &mockTx{responses: [][]map[string]any{nil, {{"_iid": "0xABC123"}}}}
		conn := &mockConn{txs: []*mockTx{writeTx}}
		mgr := MustNewManager[decimalKeyed](NewDatabase(conn, "test_db"))

		if err := mgr.Put(context.Background(), &decimalKeyed{Rate: 9.75}); err != nil {
			t.Fatalf("Put failed: %v", err)
		}
		for _, q := range writeTx.queries {
			assertTypeQL(t, "decimal put query", q, "")
		}
	})

	t.Run("filtered get with decimal literal", func(t *testing.T) {
		registerDecimal()
		readTx := &mockTx{}
		conn := &mockConn{txs: []*mockTx{readTx}}
		mgr := MustNewManager[decimalProduct](NewDatabase(conn, "test_db"))

		if _, err := mgr.Get(context.Background(), map[string]any{"price": 12.5}); err != nil {
			t.Fatalf("Get failed: %v", err)
		}
		for _, q := range readTx.queries {
			assertTypeQL(t, "decimal filtered get query", q, "")
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
			AddEntity{Name: "manager", Parent: "person", Abstract: true},
			AddRelation{Name: "employment"},
			AddRelation{Name: "management", Parent: "employment", Abstract: true},
			AddOwnership{Owner: "person", Attribute: "email"},
			AddRole{Relation: "employment", Role: "employee"},
			AddRolePlayer{Entity: "person", Relation: "employment", Role: "employee"},
			ModifyOwnership{Owner: "person", Attribute: "email", OldAnnots: "@card(0..1)", NewAnnots: "@card(1..3)"},
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

	t.Run("batched additive plan queries", func(t *testing.T) {
		// The plan batches the whole additive statement stream — including
		// inline plays clauses — into a single define block (issues #55, #34).
		diff := DiffSchemaFromRegistry(&tqlgen.ParsedSchema{})
		plan := diff.Plan()
		if plan.IsEmpty() {
			t.Fatal("expected a non-empty plan from an empty current schema")
		}
		for _, q := range plan.Queries {
			assertTypeQL(t, "batched plan query", q, "")
		}
	})

	t.Run("batched destructive plan queries", func(t *testing.T) {
		diff := &SchemaDiff{
			AddAttributes: []AttrChange{{Name: "fresh-attr", ValueType: "string"}},
			ModifyOwns: []OwnsModify{
				{TypeName: "person", Attribute: "email", OldAnnots: "@card(0..1)", NewAnnots: "@card(1..3)"},
			},
			RemoveOwns:       []OwnsChange{{TypeName: "person", Attribute: "old-attr"}},
			RemoveTypes:      []string{"obsolete-entity", "obsolete-relation"},
			RemoveAttributes: []string{"old-attr"},
		}
		plan := diff.Plan(WithDestructive())
		if len(plan.Queries) != 3 {
			t.Fatalf("expected define + redefine + undefine queries, got %d: %v", len(plan.Queries), plan.Queries)
		}
		for _, q := range plan.Queries {
			assertTypeQL(t, "batched destructive plan query", q, "")
		}
	})

	t.Run("plays generation for existing player types", func(t *testing.T) {
		// An existing entity gaining a role in a new relation emits a
		// standalone plays statement (issue #34).
		diff := &SchemaDiff{
			AddPlays: []PlaysChange{{TypeName: "person", Relation: "employment", Role: "employee"}},
		}
		for _, s := range diff.GenerateMigration() {
			assertTypeQL(t, "plays migration statement", s, "")
		}
		for _, q := range diff.Plan().Queries {
			assertTypeQL(t, "plays plan query", q, "")
		}
	})

	t.Run("subtype define with annotations", func(t *testing.T) {
		// Annotations must attach to the type name, with `sub <parent>` as a
		// separate constraint — TypeDB 3.x rejects annotations placed after
		// sub (issue #32).
		diff := DiffSchema(&tqlgen.ParsedSchema{
			Entities: []tqlgen.EntitySpec{
				{Name: "base", Abstract: true},
				{
					Name:     "manager",
					Parent:   "base",
					Abstract: true,
					Doc:      "A manager.",
					Meta:     []tqlgen.MetaSpec{{Key: "owner", Value: "hr"}},
					Plays:    []tqlgen.PlaysSpec{{Relation: "employment", Role: "employee"}},
					Owns:     []tqlgen.OwnsSpec{{Attribute: "name", Key: true}},
				},
			},
			Relations: []tqlgen.RelationSpec{
				{Name: "base-rel", Abstract: true},
				{
					Name:    "employment",
					Parent:  "base-rel",
					Doc:     "An employment.",
					Relates: []tqlgen.RelatesSpec{{Role: "employee", Card: "0..2"}},
					Owns:    []tqlgen.OwnsSpec{{Attribute: "name"}},
				},
			},
		}, &tqlgen.ParsedSchema{})
		for _, q := range diff.Plan().Queries {
			assertTypeQL(t, "subtype define query", q, "")
		}
	})
}

// TestTypeQLSyntax_MigrationStateQueries validates the queries the diff-based
// migration state tracker emits, including the record insert that executes
// inside the same schema transaction as the plan (issue #55).
func TestTypeQLSyntax_MigrationStateQueries(t *testing.T) {
	assertTypeQL(t, "migration tracking schema", migrationSchemaSQL, "")
	assertTypeQL(t, "migration record insert", recordQuery("abc123", `add 1 attribute(s): "quoted"`), "")
}

func TestTypeQLSyntax_NativeRenameStatements(t *testing.T) {
	const checkerLag = "CaliLuke/go-typeql#9: typeql-check 3.12.0 lacks the TypeDB 3.12.2 rename grammar"
	tests := []struct {
		name  string
		query string
	}{
		{name: "entity forward", query: RenameEntity("old-person", "person").ToTypeQL()},
		{name: "entity rollback", query: RenameEntity("old-person", "person").RollbackTypeQL()},
		{name: "relation forward", query: RenameRelation("old-employment", "employment").ToTypeQL()},
		{name: "relation rollback", query: RenameRelation("old-employment", "employment").RollbackTypeQL()},
		{name: "attribute forward", query: RenameAttributeType("old-email", "email").ToTypeQL()},
		{name: "attribute rollback", query: RenameAttributeType("old-email", "email").RollbackTypeQL()},
		{name: "role forward", query: RenameRole("employment", "old-employee", "employee").ToTypeQL()},
		{name: "role rollback", query: RenameRole("employment", "old-employee", "employee").RollbackTypeQL()},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assertTypeQL(t, tt.name, tt.query, checkerLag)
		})
	}
}
