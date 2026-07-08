package gotype

// Regression tests for the CRUD & query execution review cluster
// (issues #26, #27, #31, #47, #49, #86, #87, #88, #89, #90).

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// --- Test models with multi-valued fields ---

// testTagged has a multi-valued (slice) attribute.
type testTagged struct {
	BaseEntity
	Label string   `typedb:"doc-label,key"`
	Tags  []string `typedb:"tag,card=0.."`
}

// testTeam is a relation with a slice role field (multiple players of one role).
type testTeam struct {
	BaseRelation
	Members []*testPerson `typedb:"role:member"`
	Squad   string        `typedb:"squad"`
}

func registerMultiValueTypes(t *testing.T) {
	t.Helper()
	ClearRegistry()
	MustRegister[testPerson]()
	MustRegister[testCompany]()
	MustRegister[testEmployment]()
	MustRegister[testTagged]()
	MustRegister[testTeam]()
}

// --- Issue #26: Update must not stringify slice attributes ---

func TestManager_Update_MultiValuedSliceAttribute(t *testing.T) {
	registerMultiValueTypes(t)
	writeTx := &mockTx{}
	conn := &mockConn{txs: []*mockTx{writeTx}}
	mgr := MustNewManager[testTagged](NewDatabase(conn, "test_db"))

	e := &testTagged{Label: "doc1", Tags: []string{"alpha", "beta"}}
	e.SetIID("0xTAG1")
	if err := mgr.Update(context.Background(), e); err != nil {
		t.Fatalf("Update failed: %v", err)
	}

	if len(writeTx.queries) != 1 {
		t.Fatalf("expected 1 batched query, got %d", len(writeTx.queries))
	}
	q := writeTx.queries[0]
	// One has-clause per element, like the insert path.
	assertContains(t, q, `has tag "alpha"`)
	assertContains(t, q, `has tag "beta"`)
	// The stringified Go slice must never appear.
	assertNotContains(t, q, "[alpha beta]")
	assertNotContains(t, q, `"[`)
}

func TestManager_Update_EmptySliceDeletesOnly(t *testing.T) {
	registerMultiValueTypes(t)
	writeTx := &mockTx{}
	conn := &mockConn{txs: []*mockTx{writeTx}}
	mgr := MustNewManager[testTagged](NewDatabase(conn, "test_db"))

	e := &testTagged{Label: "doc1", Tags: nil}
	e.SetIID("0xTAG1")
	if err := mgr.Update(context.Background(), e); err != nil {
		t.Fatalf("Update failed: %v", err)
	}

	q := writeTx.queries[0]
	// Old values are still try-deleted...
	assertContains(t, q, "try { $e has tag $old0; };")
	assertContains(t, q, "delete")
	// ...but nothing is inserted for the empty slice.
	if parts := strings.SplitN(q, "insert", 2); len(parts) == 2 && strings.Contains(parts[1], "has tag") {
		t.Errorf("expected no insert for empty slice attribute, got:\n%s", q)
	}
	assertNotContains(t, q, "[]")
}

// --- Issue #31: relation insert with slice role players ---

func TestRelationStrategy_BuildInsertQuery_SliceRolePlayers(t *testing.T) {
	registerMultiValueTypes(t)
	info, _ := LookupType(typeOf[testTeam]())
	s := &relationStrategy{}

	team := &testTeam{
		Members: []*testPerson{
			{Name: "Alice", Email: "a@example.com"},
			{Name: "Bob", Email: "b@example.com"},
		},
		Squad: "alpha",
	}
	query, err := s.BuildInsertQuery(info, team, "r")
	if err != nil {
		t.Fatalf("BuildInsertQuery: %v", err)
	}

	assertContains(t, query, `$member0 isa test-person, has name "Alice"`)
	assertContains(t, query, `$member1 isa test-person, has name "Bob"`)
	assertContains(t, query, "member: $member0")
	assertContains(t, query, "member: $member1")
	assertContains(t, query, `has squad "alpha"`)
	assertNotContains(t, query, "links ()")
}

func TestRelationStrategy_BuildInsertQuery_NoPlayersErrors(t *testing.T) {
	registerMultiValueTypes(t)
	s := &relationStrategy{}

	// Slice role with no elements → no players at all.
	teamInfo, _ := LookupType(typeOf[testTeam]())
	if _, err := s.BuildInsertQuery(teamInfo, &testTeam{Squad: "empty"}, "r"); err == nil {
		t.Fatal("expected error for relation insert with no role players")
	} else {
		assertContains(t, err.Error(), "no role players")
	}

	// All pointer roles nil → same error instead of invalid `links ()`.
	empInfo, _ := LookupType(typeOf[testEmployment]())
	if _, err := s.BuildInsertQuery(empInfo, &testEmployment{}, "r"); err == nil {
		t.Fatal("expected error for relation insert with all-nil players")
	} else {
		assertContains(t, err.Error(), "no role players")
	}
}

func TestRelationStrategy_BuildInsertQuery_NilSliceElementErrors(t *testing.T) {
	registerMultiValueTypes(t)
	info, _ := LookupType(typeOf[testTeam]())
	s := &relationStrategy{}

	team := &testTeam{Members: []*testPerson{{Name: "Alice", Email: "a@example.com"}, nil}}
	_, err := s.BuildInsertQuery(info, team, "r")
	if err == nil {
		t.Fatal("expected error for nil slice role player")
	}
	assertContains(t, err.Error(), "is nil")
}

func TestRelationStrategy_BuildInsertQuery_UnregisteredPlayerErrors(t *testing.T) {
	registerMultiValueTypes(t)
	info, _ := LookupType(typeOf[testEmployment]())
	s := &relationStrategy{}

	// Wipe the registry entry for the player type, keeping the relation info.
	ClearRegistry()
	MustRegister[testCompany]()

	emp := &testEmployment{Employee: &testPerson{Name: "Alice"}, Employer: &testCompany{Name: "Acme"}}
	_, err := s.BuildInsertQuery(info, emp, "r")
	if err == nil {
		t.Fatal("expected error for unregistered role player type")
	}
	assertContains(t, err.Error(), "not registered")
}

// --- Issue #27: Query paths must honor a transaction-bound Manager ---

// boundTxFixture opens a TransactionContext over a mock conn holding exactly
// one transaction, so any operation escaping the bound tx fails with
// "no more mock transactions".
func boundTxFixture(t *testing.T, responses [][]map[string]any) (*Manager[testPerson], *mockTx, *mockConn) {
	t.Helper()
	tx := &mockTx{responses: responses}
	conn := &mockConn{txs: []*mockTx{tx}}
	db := NewDatabase(conn, "test_db")
	tc, err := db.Begin(WriteTransaction)
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	t.Cleanup(tc.Close)
	mgr, err := NewManagerWithTx[testPerson](tc)
	if err != nil {
		t.Fatalf("NewManagerWithTx: %v", err)
	}
	return mgr, tx, conn
}

func TestQuery_BoundTx_ReadPathsUseBoundTx(t *testing.T) {
	registerTestTypes(t)
	mgr, tx, conn := boundTxFixture(t, [][]map[string]any{
		nil,                                    // Execute
		{{"count": int64(1)}},                  // Count
		{{"result": float64(10)}},              // Sum
		{{"result0": float64(5)}},              // Aggregate
		{{"name": "x", "sum_age": float64(3)}}, // GroupBy
	})
	ctx := context.Background()

	if _, err := mgr.Query().All(ctx); err != nil {
		t.Fatalf("All: %v", err)
	}
	if _, err := mgr.Query().Count(ctx); err != nil {
		t.Fatalf("Count: %v", err)
	}
	if _, err := mgr.Query().Sum("age").Execute(ctx); err != nil {
		t.Fatalf("Sum: %v", err)
	}
	if _, err := mgr.Query().Aggregate(ctx, AggregateSpec{Attr: "age", Fn: "sum"}); err != nil {
		t.Fatalf("Aggregate: %v", err)
	}
	if _, err := mgr.Query().GroupBy("name").Aggregate(ctx, AggregateSpec{Attr: "age", Fn: "sum"}); err != nil {
		t.Fatalf("GroupBy.Aggregate: %v", err)
	}

	if len(tx.queries) != 5 {
		t.Fatalf("expected all 5 queries on the bound tx, got %d:\n%s", len(tx.queries), strings.Join(tx.queries, "\n---\n"))
	}
	if conn.idx != 1 {
		t.Errorf("expected no extra transactions beyond the bound one, got %d", conn.idx)
	}
}

func TestQuery_BoundTx_WritePathsUseBoundTxWithoutCommit(t *testing.T) {
	registerTestTypes(t)
	mgr, tx, conn := boundTxFixture(t, [][]map[string]any{
		{{"count": int64(1)}}, // Delete: count
		nil,                   // Delete: delete
		{{"count": int64(1)}}, // Update: count
		nil,                   // Update: batched update
		{{"_iid": "0x1", "name": "Alice", "email": "a@example.com"}}, // UpdateWith: fetch
		nil, // UpdateWith: per-instance update
	})
	ctx := context.Background()

	if _, err := mgr.Query().Filter(Eq("name", "Alice")).Delete(ctx); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := mgr.Query().Update(ctx, map[string]any{"email": "e@example.com"}); err != nil {
		t.Fatalf("Update: %v", err)
	}
	if _, err := mgr.Query().UpdateWith(ctx, func(p *testPerson) { p.Email = "f@example.com" }); err != nil {
		t.Fatalf("UpdateWith: %v", err)
	}

	if len(tx.queries) != 6 {
		t.Fatalf("expected 6 queries on the bound tx, got %d:\n%s", len(tx.queries), strings.Join(tx.queries, "\n---\n"))
	}
	if conn.idx != 1 {
		t.Errorf("expected no extra transactions beyond the bound one, got %d", conn.idx)
	}
	if tx.committed {
		t.Error("bound transaction must not be auto-committed by Query write paths")
	}
}

// --- Issue #47: Count/Delete count distinct instances ---

func TestQuery_Count_DeduplicatesInstances(t *testing.T) {
	registerTestTypes(t)
	readTx := &mockTx{responses: [][]map[string]any{{{"count": int64(1)}}}}
	conn := &mockConn{txs: []*mockTx{readTx}}
	mgr := MustNewManager[testPerson](NewDatabase(conn, "test_db"))

	if _, err := mgr.Query().Filter(Gt("age", 20)).Count(context.Background()); err != nil {
		t.Fatalf("Count: %v", err)
	}
	assertContains(t, readTx.queries[0], "select $e;\ndistinct;\nreduce $count = count($e);")
}

func TestQuery_Delete_DeduplicatesInstances(t *testing.T) {
	registerTestTypes(t)
	writeTx := &mockTx{responses: [][]map[string]any{{{"count": int64(1)}}, nil}}
	conn := &mockConn{txs: []*mockTx{writeTx}}
	mgr := MustNewManager[testPerson](NewDatabase(conn, "test_db"))

	if _, err := mgr.Query().Filter(Gt("age", 20)).Delete(context.Background()); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	assertContains(t, writeTx.queries[0], "select $e;\ndistinct;\nreduce $count = count($e);")
	assertContains(t, writeTx.queries[1], "select $e;\ndistinct;\ndelete $e;")
}

// --- Issue #49: First must not mutate the builder ---

func TestQuery_First_DoesNotMutateBuilder(t *testing.T) {
	registerTestTypes(t)
	firstTx := &mockTx{responses: [][]map[string]any{
		{{"_iid": "0x1", "name": "Alice", "email": "a@example.com"}},
	}}
	allTx := &mockTx{responses: [][]map[string]any{
		{
			{"_iid": "0x1", "name": "Alice", "email": "a@example.com"},
			{"_iid": "0x2", "name": "Bob", "email": "b@example.com"},
		},
	}}
	conn := &mockConn{txs: []*mockTx{firstTx, allTx}}
	mgr := MustNewManager[testPerson](NewDatabase(conn, "test_db"))

	q := mgr.Query()
	if _, err := q.First(context.Background()); err != nil {
		t.Fatalf("First: %v", err)
	}
	assertContains(t, firstTx.queries[0], "limit 1;")

	all, err := q.All(context.Background())
	if err != nil {
		t.Fatalf("All: %v", err)
	}
	assertNotContains(t, allTx.queries[0], "limit 1;")
	if len(all) != 2 {
		t.Fatalf("expected All after First to return 2 results, got %d", len(all))
	}
}

// --- Issue #86: write transactions honor ctx-aware acquisition ---

// ctxAwareConn records whether the context-aware transaction opener was used.
type ctxAwareConn struct {
	mockConn
	ctxOpens int
}

func (c *ctxAwareConn) TransactionContext(ctx context.Context, dbName string, txType int) (Tx, error) {
	c.ctxOpens++
	return c.Transaction(dbName, txType)
}

func TestManager_WriteTx_UsesContextAwareOpen(t *testing.T) {
	registerTestTypes(t)
	conn := &ctxAwareConn{mockConn: mockConn{txs: []*mockTx{{responses: [][]map[string]any{{{"_iid": "0x1"}}}}, {}, {}}}}
	db := NewDatabase(conn, "test_db")
	mgr := MustNewManager[testPerson](db)
	ctx := context.Background()

	if err := mgr.Insert(ctx, &testPerson{Name: "Alice", Email: "a@example.com"}); err != nil {
		t.Fatalf("Insert: %v", err)
	}
	p := &testPerson{Name: "Alice", Email: "a@example.com"}
	p.SetIID("0x1")
	if err := mgr.Update(ctx, p); err != nil {
		t.Fatalf("Update: %v", err)
	}
	mgr2 := MustNewManager[testPerson](db)
	if _, err := mgr2.Query().Filter(Eq("name", "Alice")).Delete(ctx); err != nil {
		t.Fatalf("Query.Delete: %v", err)
	}

	if conn.ctxOpens != 3 {
		t.Errorf("expected 3 context-aware transaction opens, got %d", conn.ctxOpens)
	}
}

// --- Issue #87: aggregate/count parse failures surface as errors ---

func TestQuery_Count_UnrecognizedShapeErrors(t *testing.T) {
	registerTestTypes(t)

	t.Run("unknown value string", func(t *testing.T) {
		readTx := &mockTx{responses: [][]map[string]any{{{"count": "Value(decimal: 5)"}}}}
		conn := &mockConn{txs: []*mockTx{readTx}}
		mgr := MustNewManager[testPerson](NewDatabase(conn, "test_db"))

		if _, err := mgr.Query().Count(context.Background()); err == nil {
			t.Fatal("expected error for unrecognized count value, got nil")
		}
	})

	t.Run("missing count key", func(t *testing.T) {
		readTx := &mockTx{responses: [][]map[string]any{{{"unexpected": int64(3)}}}}
		conn := &mockConn{txs: []*mockTx{readTx}}
		mgr := MustNewManager[testPerson](NewDatabase(conn, "test_db"))

		if _, err := mgr.Query().Count(context.Background()); err == nil {
			t.Fatal("expected error for missing count key, got nil")
		}
	})

	t.Run("exists propagates parse error", func(t *testing.T) {
		readTx := &mockTx{responses: [][]map[string]any{{{"count": "garbage"}}}}
		conn := &mockConn{txs: []*mockTx{readTx}}
		mgr := MustNewManager[testPerson](NewDatabase(conn, "test_db"))

		if _, err := mgr.Query().Exists(context.Background()); err == nil {
			t.Fatal("expected Exists to propagate parse error, got nil")
		}
	})
}

func TestQuery_Aggregate_UnrecognizedShapeErrors(t *testing.T) {
	registerTestTypes(t)

	t.Run("sum with garbage value", func(t *testing.T) {
		readTx := &mockTx{responses: [][]map[string]any{{{"result": "Value(decimal: 1.5)"}}}}
		conn := &mockConn{txs: []*mockTx{readTx}}
		mgr := MustNewManager[testPerson](NewDatabase(conn, "test_db"))

		if _, err := mgr.Query().Sum("age").Execute(context.Background()); err == nil {
			t.Fatal("expected error for unrecognized sum value, got nil")
		}
	})

	t.Run("multi aggregate with missing result var", func(t *testing.T) {
		readTx := &mockTx{responses: [][]map[string]any{{{"unrelated": float64(1)}}}}
		conn := &mockConn{txs: []*mockTx{readTx}}
		mgr := MustNewManager[testPerson](NewDatabase(conn, "test_db"))

		if _, err := mgr.Query().Aggregate(context.Background(), AggregateSpec{Attr: "age", Fn: "sum"}); err == nil {
			t.Fatal("expected error for missing aggregate result, got nil")
		}
	})
}

func TestQuery_Aggregate_NilValueIsZero(t *testing.T) {
	registerTestTypes(t)
	// Aggregates over an empty set may come back as an explicit null.
	readTx := &mockTx{responses: [][]map[string]any{{{"result": nil}}}}
	conn := &mockConn{txs: []*mockTx{readTx}}
	mgr := MustNewManager[testPerson](NewDatabase(conn, "test_db"))

	sum, err := mgr.Query().Sum("age").Execute(context.Background())
	if err != nil {
		t.Fatalf("Sum over empty set: %v", err)
	}
	if sum != 0 {
		t.Errorf("expected 0 for null aggregate, got %f", sum)
	}
}

// --- Issue #88: KeyAttributeError / NotFoundError / NotUniqueError are wired ---

func TestManager_Insert_MissingKeyReturnsKeyAttributeError(t *testing.T) {
	registerTestTypes(t)
	conn := &mockConn{}
	mgr := MustNewManager[testPerson](NewDatabase(conn, "test_db"))

	err := mgr.Insert(context.Background(), &testPerson{Email: "a@example.com"}) // Name (key) unset
	if err == nil {
		t.Fatal("expected error for missing key attribute")
	}
	var kerr *KeyAttributeError
	if !errors.As(err, &kerr) {
		t.Fatalf("expected *KeyAttributeError, got %T: %v", err, err)
	}
	if kerr.FieldName != "name" || kerr.EntityType != "test-person" || kerr.Operation != "insert" {
		t.Errorf("unexpected KeyAttributeError contents: %+v", kerr)
	}
	// No transaction may have been opened.
	if conn.idx != 0 {
		t.Errorf("expected no transaction for invalid insert, got %d", conn.idx)
	}
}

func TestManager_Put_MissingKeyReturnsKeyAttributeError(t *testing.T) {
	registerTestTypes(t)
	conn := &mockConn{}
	mgr := MustNewManager[testPerson](NewDatabase(conn, "test_db"))

	err := mgr.Put(context.Background(), &testPerson{Email: "a@example.com"})
	var kerr *KeyAttributeError
	if !errors.As(err, &kerr) {
		t.Fatalf("expected *KeyAttributeError, got %T: %v", err, err)
	}
	if kerr.Operation != "put" {
		t.Errorf("expected operation put, got %q", kerr.Operation)
	}
}

func TestManager_InsertMany_MissingKeyReturnsKeyAttributeError(t *testing.T) {
	registerTestTypes(t)
	conn := &mockConn{txs: []*mockTx{{}}}
	mgr := MustNewManager[testPerson](NewDatabase(conn, "test_db"))

	err := mgr.InsertMany(context.Background(), []*testPerson{
		{Name: "Alice", Email: "a@example.com"},
		{Email: "b@example.com"},
	})
	var kerr *KeyAttributeError
	if !errors.As(err, &kerr) {
		t.Fatalf("expected *KeyAttributeError, got %T: %v", err, err)
	}
	assertContains(t, err.Error(), "[1]")
}

func TestManager_PutMany_MissingKeyReturnsKeyAttributeError(t *testing.T) {
	registerTestTypes(t)
	conn := &mockConn{}
	mgr := MustNewManager[testPerson](NewDatabase(conn, "test_db"))

	err := mgr.PutMany(context.Background(), []*testPerson{{Email: "b@example.com"}})
	var kerr *KeyAttributeError
	if !errors.As(err, &kerr) {
		t.Fatalf("expected *KeyAttributeError, got %T: %v", err, err)
	}
	if conn.idx != 0 {
		t.Errorf("expected no transaction for invalid put_many, got %d", conn.idx)
	}
}

func TestManager_GetOne(t *testing.T) {
	registerTestTypes(t)

	t.Run("found", func(t *testing.T) {
		readTx := &mockTx{responses: [][]map[string]any{
			{{"_iid": "0x1", "name": "Alice", "email": "a@example.com"}},
		}}
		conn := &mockConn{txs: []*mockTx{readTx}}
		mgr := MustNewManager[testPerson](NewDatabase(conn, "test_db"))

		p, err := mgr.GetOne(context.Background(), map[string]any{"name": "Alice"})
		if err != nil {
			t.Fatalf("GetOne: %v", err)
		}
		if p == nil || p.Name != "Alice" {
			t.Fatalf("expected Alice, got %+v", p)
		}
	})

	t.Run("not found", func(t *testing.T) {
		readTx := &mockTx{responses: [][]map[string]any{nil}}
		conn := &mockConn{txs: []*mockTx{readTx}}
		mgr := MustNewManager[testPerson](NewDatabase(conn, "test_db"))

		_, err := mgr.GetOne(context.Background(), map[string]any{"name": "Nobody"})
		var nf *NotFoundError
		if !errors.As(err, &nf) {
			t.Fatalf("expected *NotFoundError, got %T: %v", err, err)
		}
		if nf.TypeName != "test-person" {
			t.Errorf("unexpected type name %q", nf.TypeName)
		}
	})

	t.Run("not unique", func(t *testing.T) {
		readTx := &mockTx{responses: [][]map[string]any{
			{
				{"_iid": "0x1", "name": "Alice", "email": "a@example.com"},
				{"_iid": "0x2", "name": "Alice", "email": "a2@example.com"},
			},
		}}
		conn := &mockConn{txs: []*mockTx{readTx}}
		mgr := MustNewManager[testPerson](NewDatabase(conn, "test_db"))

		_, err := mgr.GetOne(context.Background(), map[string]any{"name": "Alice"})
		var nu *NotUniqueError
		if !errors.As(err, &nu) {
			t.Fatalf("expected *NotUniqueError, got %T: %v", err, err)
		}
		if nu.Count != 2 {
			t.Errorf("expected count 2, got %d", nu.Count)
		}
	})
}

// --- Issue #89: deterministic query text from maps ---

func TestManager_Get_DeterministicFilterOrder(t *testing.T) {
	registerTestTypes(t)
	mgr := MustNewManager[testPerson](NewDatabase(&mockConn{}, "test_db"))

	filters := map[string]any{"name": "Alice", "email": "a@example.com", "age": 30}
	first, err := mgr.buildFilteredMatch("e", filters)
	if err != nil {
		t.Fatalf("buildFilteredMatch: %v", err)
	}
	for range 20 {
		next, err := mgr.buildFilteredMatch("e", filters)
		if err != nil {
			t.Fatalf("buildFilteredMatch: %v", err)
		}
		if next != first {
			t.Fatalf("query text differs between runs:\n%s\nvs\n%s", first, next)
		}
	}
	// Keys must be emitted in sorted order.
	if age, email, name := strings.Index(first, "has age"), strings.Index(first, "has email"), strings.Index(first, "has name"); age >= email || email >= name {
		t.Errorf("expected sorted attribute order (age < email < name):\n%s", first)
	}
}

func TestQuery_Update_DeterministicAttrOrder(t *testing.T) {
	registerTestTypes(t)

	build := func() string {
		writeTx := &mockTx{responses: [][]map[string]any{{{"count": int64(1)}}, nil}}
		conn := &mockConn{txs: []*mockTx{writeTx}}
		mgr := MustNewManager[testPerson](NewDatabase(conn, "test_db"))
		if _, err := mgr.Query().Update(context.Background(), map[string]any{
			"name":  "Zed",
			"email": "z@example.com",
			"age":   40,
		}); err != nil {
			t.Fatalf("Update: %v", err)
		}
		return writeTx.queries[1]
	}

	first := build()
	for range 20 {
		if next := build(); next != first {
			t.Fatalf("bulk update query text differs between runs:\n%s\nvs\n%s", first, next)
		}
	}
	if age, email, name := strings.Index(first, "has age"), strings.Index(first, "has email"), strings.Index(first, "has name"); age >= email || email >= name {
		t.Errorf("expected sorted attribute order (age < email < name):\n%s", first)
	}
}
