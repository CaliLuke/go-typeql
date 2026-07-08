package gotype

// Regression tests for the filters & input validation review cluster
// (issues #45, #46, #48, #50, #85).

import (
	"context"
	"regexp"
	"strings"
	"testing"
)

// --- Issue #45: identifiers and IIDs must be validated, not interpolated raw ---

func TestManager_GetByIID_RejectsInvalidIID(t *testing.T) {
	registerTestTypes(t)
	conn := &mockConn{} // no transactions: nothing must reach the server
	mgr := MustNewManager[testPerson](NewDatabase(conn, "test_db"))

	for _, iid := range []string{"0x1; delete $e;", "", "abc", "0x", "0xZZ"} {
		result, err := mgr.GetByIID(context.Background(), iid)
		if err == nil {
			t.Fatalf("GetByIID(%q): expected validation error, got nil", iid)
		}
		assertContains(t, err.Error(), "invalid IID")
		if result != nil {
			t.Errorf("GetByIID(%q): expected nil result", iid)
		}
	}
	if conn.idx != 0 {
		t.Errorf("expected no transactions to be opened, got %d", conn.idx)
	}
}

func TestManager_GetByIIDPolymorphic_RejectsInvalidIID(t *testing.T) {
	registerTestTypes(t)
	conn := &mockConn{}
	mgr := MustNewManager[testPerson](NewDatabase(conn, "test_db"))

	if _, _, err := mgr.GetByIIDPolymorphic(context.Background(), "0x1; delete $e;"); err == nil {
		t.Fatal("GetByIIDPolymorphic: expected validation error, got nil")
	}
	if _, _, err := mgr.GetByIIDPolymorphicAny(context.Background(), "0x1; delete $e;"); err == nil {
		t.Fatal("GetByIIDPolymorphicAny: expected validation error, got nil")
	}
}

func TestManager_Delete_Strict_RejectsInvalidIID(t *testing.T) {
	registerTestTypes(t)
	conn := &mockConn{}
	mgr := MustNewManager[testPerson](NewDatabase(conn, "test_db"))

	p := &testPerson{Name: "Alice", Email: "a@example.com"}
	p.SetIID("0x1; delete $e;")
	err := mgr.Delete(context.Background(), p, WithStrict())
	if err == nil {
		t.Fatal("expected validation error for injected IID in strict delete")
	}
	assertContains(t, err.Error(), "invalid IID")
}

func TestManager_Get_RejectsInvalidAttrName(t *testing.T) {
	registerTestTypes(t)
	conn := &mockConn{}
	mgr := MustNewManager[testPerson](NewDatabase(conn, "test_db"))

	_, err := mgr.Get(context.Background(), map[string]any{"name; delete $e": "x"})
	if err == nil {
		t.Fatal("expected validation error for injected attribute name")
	}
	assertContains(t, err.Error(), "invalid attribute name")
}

func TestQuery_Filter_RejectsInvalidAttrName(t *testing.T) {
	registerTestTypes(t)
	conn := &mockConn{}
	mgr := MustNewManager[testPerson](NewDatabase(conn, "test_db"))

	_, err := mgr.Query().Filter(Eq("age; delete $e", 30)).Execute(context.Background())
	if err == nil {
		t.Fatal("expected validation error for injected filter attribute name")
	}
	assertContains(t, err.Error(), "invalid attribute name")
}

func TestQuery_ByIIDFilter_RejectsInvalidIID(t *testing.T) {
	registerTestTypes(t)
	conn := &mockConn{}
	mgr := MustNewManager[testPerson](NewDatabase(conn, "test_db"))

	if _, err := mgr.Query().Filter(ByIID("0x1; delete $e;")).Execute(context.Background()); err == nil {
		t.Fatal("expected validation error for injected ByIID value")
	}
	if _, err := mgr.Query().Filter(IIDIn("0x12", "0x1; delete $e;")).Execute(context.Background()); err == nil {
		t.Fatal("expected validation error for injected IIDIn value")
	}
}

func TestQuery_OrderBy_RejectsInvalidAttr(t *testing.T) {
	registerTestTypes(t)
	conn := &mockConn{}
	mgr := MustNewManager[testPerson](NewDatabase(conn, "test_db"))

	_, err := mgr.Query().OrderAsc("name; delete $e").Execute(context.Background())
	if err == nil {
		t.Fatal("expected validation error for injected order-by attribute")
	}
	assertContains(t, err.Error(), "invalid attribute name")
}

func TestQuery_Update_RejectsInvalidAttrKey(t *testing.T) {
	registerTestTypes(t)
	conn := &mockConn{}
	mgr := MustNewManager[testPerson](NewDatabase(conn, "test_db"))

	_, err := mgr.Query().Update(context.Background(), map[string]any{"email; delete $e": "x"})
	if err == nil {
		t.Fatal("expected validation error for injected update attribute key")
	}
	assertContains(t, err.Error(), "invalid attribute name")
}

func TestAggregate_RejectsInvalidAttr(t *testing.T) {
	registerTestTypes(t)
	conn := &mockConn{}
	mgr := MustNewManager[testPerson](NewDatabase(conn, "test_db"))
	ctx := context.Background()

	if _, err := mgr.Query().Sum("age; delete $e").Execute(ctx); err == nil {
		t.Fatal("expected validation error for injected aggregate attribute")
	}
	if _, err := mgr.Query().Aggregate(ctx, AggregateSpec{Attr: "age; delete $e", Fn: "sum"}); err == nil {
		t.Fatal("expected validation error for injected multi-aggregate attribute")
	}
	if _, err := mgr.Query().GroupBy("name; delete $e").Aggregate(ctx, AggregateSpec{Attr: "age", Fn: "sum"}); err == nil {
		t.Fatal("expected validation error for injected group-by attribute")
	}
}

func TestValidateAttrName_AcceptsRealisticNames(t *testing.T) {
	for _, attr := range []string{"name", "start-date", "attr_2", "Email"} {
		if err := validateAttrName(attr); err != nil {
			t.Errorf("validateAttrName(%q) = %v, want nil", attr, err)
		}
	}
	for _, attr := range []string{"", "9lives", "$name", "name attr", "name;"} {
		if err := validateAttrName(attr); err == nil {
			t.Errorf("validateAttrName(%q) = nil, want error", attr)
		}
	}
}

func TestValidateIID_AcceptsHexIIDs(t *testing.T) {
	for _, iid := range []string{"0x1e00000000000000000001", "0xABCdef01"} {
		if err := validateIID(iid); err != nil {
			t.Errorf("validateIID(%q) = %v, want nil", iid, err)
		}
	}
	for _, iid := range []string{"", "0x", "1e22", "0x12g4", "0x1; delete $e;"} {
		if err := validateIID(iid); err == nil {
			t.Errorf("validateIID(%q) = nil, want error", iid)
		}
	}
}

// --- Issue #46: Startswith must treat the prefix as a literal, not a regex ---

func TestStartswith_EscapesRegexMetacharacters(t *testing.T) {
	f := Startswith("email", "j.smith@corp.com")
	joined := strings.Join(f.ToPatterns("e"), " ")
	// The dot must be escaped so "jasmith@corpXcom" prefixes cannot match.
	assertContains(t, joined, `like "j\\.smith@corp\\.com.*";`)
}

func TestStartswith_UnbalancedMetacharactersStayLiteral(t *testing.T) {
	// An unescaped "(" would be a server-side regex compile error.
	f := Startswith("name", "foo(bar[")
	joined := strings.Join(f.ToPatterns("e"), " ")
	assertContains(t, joined, `like "foo\\(bar\\[.*";`)
}

// --- Issue #48: Or/Not must scope role-player and computed variables ---

var rolePlayerVarRe = regexp.MustCompile(`\(member: (\$[A-Za-z0-9_]+)\)`)

func TestOr_RolePlayerBranchesScoped(t *testing.T) {
	f := Or(
		RolePlayer("member", Eq("name", "a")),
		RolePlayer("member", Eq("name", "b")),
	)
	patterns := f.ToPatterns("e")
	if len(patterns) != 1 {
		t.Fatalf("expected 1 pattern, got %d", len(patterns))
	}
	p := patterns[0]

	vars := rolePlayerVarRe.FindAllStringSubmatch(p, -1)
	if len(vars) != 2 {
		t.Fatalf("expected 2 role player links, got %d in:\n%s", len(vars), p)
	}
	left, right := vars[0][1], vars[1][1]
	if left == right {
		t.Errorf("or branches share role player variable %s:\n%s", left, p)
	}
	for _, v := range []string{left, right} {
		if v == "$member" {
			t.Errorf("role player variable %s was not scoped:\n%s", v, p)
		}
	}
	// Inner attribute variables must be scoped consistently with the link.
	assertContains(t, p, left+" has name "+left+"__name;")
	assertContains(t, p, right+" has name "+right+"__name;")
}

func TestNot_RolePlayerScoped(t *testing.T) {
	f := Not(RolePlayer("member", Eq("name", "a")))
	p := f.ToPatterns("e")[0]
	assertNotContains(t, p, "(member: $member)")
	assertContains(t, p, "not {")
	// The relation variable itself must stay unscoped.
	assertContains(t, p, "$e links (member: ")
}

func TestOr_ComputedBranchesScoped(t *testing.T) {
	f := Or(
		Computed("total", ArithmeticExpr("e", "price", "*", "quantity"), ">", 100),
		Computed("total", ArithmeticExpr("e", "price", "*", "quantity"), "<", 10),
	)
	p := f.ToPatterns("e")[0]
	assertNotContains(t, p, "let $total =")

	letVarRe := regexp.MustCompile(`let (\$[A-Za-z0-9_]+) =`)
	vars := letVarRe.FindAllStringSubmatch(p, -1)
	if len(vars) != 2 {
		t.Fatalf("expected 2 let assignments, got %d in:\n%s", len(vars), p)
	}
	if vars[0][1] == vars[1][1] {
		t.Errorf("or branches share computed variable %s:\n%s", vars[0][1], p)
	}
}

func TestOr_StringLiteralsUntouchedByScoping(t *testing.T) {
	// A value that looks like a variable reference must not be renamed.
	f := Or(Eq("name", "$e__name"), Eq("name", "b"))
	p := f.ToPatterns("e")[0]
	assertContains(t, p, `== "$e__name";`)
}

// --- Issue #50: non-scalar comparison values error instead of panicking ---

func TestComparisonFilter_Validate_NonScalar(t *testing.T) {
	err := validateFilters(Eq("age", []int{18, 21}))
	if err == nil {
		t.Fatal("expected validation error for slice comparison value")
	}
	assertContains(t, err.Error(), "requires a scalar value")
	assertContains(t, err.Error(), "use In")
}

func TestQuery_Execute_NonScalarFilterValueReturnsError(t *testing.T) {
	registerTestTypes(t)
	conn := &mockConn{}
	mgr := MustNewManager[testPerson](NewDatabase(conn, "test_db"))
	ctx := context.Background()

	// The classic "meant In" mistake must be an error from Execute, not a panic.
	q := mgr.Query().Filter(Eq("age", []int{18, 21}))
	if _, err := q.Execute(ctx); err == nil {
		t.Fatal("Execute: expected error for non-scalar comparison value")
	} else {
		assertContains(t, err.Error(), "requires a scalar value")
	}
	if _, err := q.Count(ctx); err == nil {
		t.Fatal("Count: expected error for non-scalar comparison value")
	}
	if _, err := q.Delete(ctx); err == nil {
		t.Fatal("Delete: expected error for non-scalar comparison value")
	}
}

func TestQuery_Execute_NestedInvalidFilterReturnsError(t *testing.T) {
	registerTestTypes(t)
	conn := &mockConn{}
	mgr := MustNewManager[testPerson](NewDatabase(conn, "test_db"))

	// Validation must recurse through combinators.
	f := And(Eq("name", "Alice"), Or(Gt("age", 20), Not(Eq("age", []int{1}))))
	if _, err := mgr.Query().Filter(f).Execute(context.Background()); err == nil {
		t.Fatal("expected error for invalid filter nested in combinators")
	}
}

// --- Issue #85: empty In/IIDIn use a structurally valid contradiction ---

func TestQuery_EmptyIn_BuildsValidContradiction(t *testing.T) {
	registerTestTypes(t)
	readTx := &mockTx{}
	conn := &mockConn{txs: []*mockTx{readTx}}
	mgr := MustNewManager[testPerson](NewDatabase(conn, "test_db"))

	results, err := mgr.Query().Filter(In("name", []any{})).Execute(context.Background())
	if err != nil {
		t.Fatalf("empty In query failed: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("expected no results, got %d", len(results))
	}
	if len(readTx.queries) != 1 {
		t.Fatalf("expected 1 query, got %d", len(readTx.queries))
	}
	assertContains(t, readTx.queries[0], "not { $e is $e; };")
	assertNotContains(t, readTx.queries[0], "0xFFFFFFFFFFFFFFFF")
}

// --- Deterministic or/not variable scoping (follow-up to issue #89) ---
//
// Scope suffixes used to come from a process-global counter, so the same
// logical query produced different TypeQL text depending on how many filters
// had run before it. Suffix numbering now restarts per built query (one
// varScope threaded through all filters via filterPatterns), while still
// keeping suffixes unique across ALL or/not blocks within one query.

func newScopeTestQuery(t *testing.T) *Query[testPerson] {
	t.Helper()
	mgr := MustNewManager[testPerson](NewDatabase(&mockConn{}, "test_db"))
	return mgr.Query()
}

func TestQuery_OrScoping_DeterministicAcrossBuilds(t *testing.T) {
	registerTestTypes(t)
	build := func() string {
		q, err := newScopeTestQuery(t).
			Filter(Or(Eq("name", "Alice"), Not(Gt("age", 30)))).
			buildQuery()
		if err != nil {
			t.Fatalf("buildQuery failed: %v", err)
		}
		return q
	}

	first := build()

	// Interleave an unrelated Or query: it must not shift the numbering of
	// later builds (the old global counter did exactly that).
	if _, err := newScopeTestQuery(t).
		Filter(Or(Eq("email", "a@example.com"), Eq("email", "b@example.com"))).
		buildQuery(); err != nil {
		t.Fatalf("interleaved buildQuery failed: %v", err)
	}

	second := build()
	if first != second {
		t.Errorf("same logical query produced different text:\nfirst:\n%s\nsecond:\n%s", first, second)
	}
	// Numbering starts at 1 for every query.
	assertContains(t, first, "$e_o1__name")
}

func TestQuery_SiblingOrFilters_DistinctScopeSuffixes(t *testing.T) {
	registerTestTypes(t)
	// Two sibling Or filters over the SAME attribute: with per-block-restarting
	// numbering both blocks would bind $e_o1__name, which TypeDB 3.x treats as
	// a shared variable of the enclosing scope. The shared per-query scope must
	// keep the suffixes distinct.
	query, err := newScopeTestQuery(t).
		Filter(
			Or(Eq("name", "Alice"), Eq("name", "Bob")),
			Or(Eq("name", "Carol"), Eq("name", "Dave")),
		).
		buildQuery()
	if err != nil {
		t.Fatalf("buildQuery failed: %v", err)
	}
	assertContains(t, query,
		`{ $e has name $e_o1__name; $e_o1__name == "Alice"; } or `+
			`{ $e has name $e_o2__name; $e_o2__name == "Bob"; };`)
	assertContains(t, query,
		`{ $e has name $e_o3__name; $e_o3__name == "Carol"; } or `+
			`{ $e has name $e_o4__name; $e_o4__name == "Dave"; };`)
}

func TestQuery_OrInsideNot_DistinctScopeSuffixes(t *testing.T) {
	registerTestTypes(t)
	query, err := newScopeTestQuery(t).
		Filter(Not(Or(Eq("name", "Alice"), Eq("name", "Bob")))).
		buildQuery()
	if err != nil {
		t.Fatalf("buildQuery failed: %v", err)
	}
	// The not block takes suffix 1; the nested or branches take 2 and 3 from
	// the same scope, then get re-scoped under the not prefix.
	assertContains(t, query, "not {")
	assertContains(t, query, `$e_n1__e_o2__name == "Alice";`)
	assertContains(t, query, `$e_n1__e_o3__name == "Bob";`)
}

func TestQuery_RolePlayerWrappingOr_ThreadsScope(t *testing.T) {
	registerMultiValueTypes(t)
	mgr := MustNewManager[testTeam](NewDatabase(&mockConn{}, "test_db"))
	// The RolePlayer's inner Or must consume suffixes from the query's scope,
	// so the sibling Or continues the numbering instead of colliding.
	query, err := mgr.Query().
		Filter(
			RolePlayer("member", Or(Eq("name", "Alice"), Eq("name", "Bob"))),
			Or(Eq("squad", "red"), Eq("squad", "blue")),
		).
		buildQuery()
	if err != nil {
		t.Fatalf("buildQuery failed: %v", err)
	}
	assertContains(t, query, "$e links (member: $member);")
	assertContains(t, query,
		`{ $member has name $member_o1__name; $member_o1__name == "Alice"; } or `+
			`{ $member has name $member_o2__name; $member_o2__name == "Bob"; };`)
	assertContains(t, query,
		`{ $e has squad $e_o3__squad; $e_o3__squad == "red"; } or `+
			`{ $e has squad $e_o4__squad; $e_o4__squad == "blue"; };`)
}

func TestOrFilter_StandaloneToPatterns_Deterministic(t *testing.T) {
	// A direct ToPatterns call (outside a query build) uses a fresh scope, so
	// repeated calls yield identical text.
	f := Or(Eq("name", "Alice"), Eq("name", "Bob"))
	first := strings.Join(f.ToPatterns("e"), "\n")
	second := strings.Join(f.ToPatterns("e"), "\n")
	if first != second {
		t.Errorf("standalone ToPatterns is not deterministic:\nfirst:\n%s\nsecond:\n%s", first, second)
	}
}
