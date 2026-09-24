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
	joined := strings.Join(compilePatterns(f), " ")
	// The dot must be escaped so "jasmith@corpXcom" prefixes cannot match.
	assertContains(t, joined, `like "j\\.smith@corp\\.com.*";`)
}

func TestStartswith_UnbalancedMetacharactersStayLiteral(t *testing.T) {
	// An unescaped "(" would be a server-side regex compile error.
	f := Startswith("name", "foo(bar[")
	joined := strings.Join(compilePatterns(f), " ")
	assertContains(t, joined, `like "foo\\(bar\\[.*";`)
}

// --- Issue #48: Or/Not must scope role-player and computed variables ---

var rolePlayerVarRe = regexp.MustCompile(`\(member: (\$[A-Za-z0-9_]+)\)`)

func TestOr_RolePlayerBranchesScoped(t *testing.T) {
	f := Or(
		RolePlayer("member", Eq("name", "a")),
		RolePlayer("member", Eq("name", "b")),
	)
	patterns := compilePatterns(f)
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
	// Each branch has its own player (a key per scope, R2), and the player's
	// attribute variable belongs to the same branch scope.
	assertContains(t, p, left+" has name "+left+"_s1__name;")
	assertContains(t, p, right+" has name "+right+"_s2__name;")
}

// A player in a not body is a different variable from a player of the same
// role outside it: the keys differ in scope, so the table gives distinct names.
func TestNot_RolePlayerScoped(t *testing.T) {
	f := And(RolePlayer("member", Eq("name", "b")), Not(RolePlayer("member", Eq("name", "a"))))
	joined := strings.Join(compilePatterns(f), "\n")
	vars := rolePlayerVarRe.FindAllStringSubmatch(joined, -1)
	if len(vars) != 2 || vars[0][1] == vars[1][1] {
		t.Fatalf("expected two distinct player variables in:\n%s", joined)
	}
	assertContains(t, joined, "not { $e links (member: "+vars[1][1]+");")
}

func TestOr_ComputedBranchesScoped(t *testing.T) {
	f := Or(
		Computed(Mul(Attr("price"), Attr("quantity")), ">", 100),
		Computed(Mul(Attr("price"), Attr("quantity")), "<", 10),
	)
	p := compilePatterns(f)[0]

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
	p := compilePatterns(f)[0]
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
	assertContains(t, first, "$e_s1__name")
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
		`{ $e has name $e_s1__name; $e_s1__name == "Alice"; } or `+
			`{ $e has name $e_s2__name; $e_s2__name == "Bob"; };`)
	assertContains(t, query,
		`{ $e has name $e_s3__name; $e_s3__name == "Carol"; } or `+
			`{ $e has name $e_s4__name; $e_s4__name == "Dave"; };`)
}

func TestQuery_OrInsideNot_DistinctScopeSuffixes(t *testing.T) {
	registerTestTypes(t)
	query, err := newScopeTestQuery(t).
		Filter(Not(Or(Eq("name", "Alice"), Eq("name", "Bob")))).
		buildQuery()
	if err != nil {
		t.Fatalf("buildQuery failed: %v", err)
	}
	// The not body takes scope 1; the nested or branches take 2 and 3 from the
	// same query counter.
	assertContains(t, query, "not {")
	assertContains(t, query, `$e_s2__name == "Alice";`)
	assertContains(t, query, `$e_s3__name == "Bob";`)
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
	// The player crosses into the branches as their owner (R2).
	assertContains(t, query,
		`{ $member has name $member_s1__name; $member_s1__name == "Alice"; } or `+
			`{ $member has name $member_s2__name; $member_s2__name == "Bob"; };`)
	assertContains(t, query,
		`{ $e has squad $e_s3__squad; $e_s3__squad == "red"; } or `+
			`{ $e has squad $e_s4__squad; $e_s4__squad == "blue"; };`)
}

func TestOrFilter_Compile_Deterministic(t *testing.T) {
	// Each compilation uses a fresh allocator, so repeated compilations of the
	// same filter tree yield identical text.
	f := Or(Eq("name", "Alice"), Eq("name", "Bob"))
	first := strings.Join(compilePatterns(f), "\n")
	second := strings.Join(compilePatterns(f), "\n")
	if first != second {
		t.Errorf("compilation is not deterministic:\nfirst:\n%s\nsecond:\n%s", first, second)
	}
}

// Distinct attribute labels must bind distinct variables: TypeQL treats a
// shared variable as an implicit equality, so filters on first-name and
// first_name used to require the two values to be equal and matched nothing.
func TestFilter_DistinctLabelsBindDistinctVariables(t *testing.T) {
	registerTestTypes(t)
	query, err := newScopeTestQuery(t).
		Filter(Eq("first-name", "Ann"), Eq("first_name", "Bob")).
		buildQuery()
	if err != nil {
		t.Fatalf("buildQuery failed: %v", err)
	}
	assertContains(t, query, "$e has first-name $e__first_name;\n$e__first_name == \"Ann\";")
	assertContains(t, query, "$e has first_name $e___first_uname;\n$e___first_uname == \"Bob\";")
}

// Existence filters bind the anonymous $_, which stays anonymous under or {}
// and not {} scoping, so they never constrain other filters on the attribute.
func TestFilter_ExistsBindsAnonymousVariable(t *testing.T) {
	registerTestTypes(t)
	query, err := newScopeTestQuery(t).
		Filter(Gt("age", 30), Not(Or(HasAttr("age"), HasAttr("name")))).
		buildQuery()
	if err != nil {
		t.Fatalf("buildQuery failed: %v", err)
	}
	assertContains(t, query, "{ $e has age $_; } or { $e has name $_; }")
	if strings.Contains(query, "__;") {
		t.Errorf("existence filter bound a named variable:\n%s", query)
	}
}

// A role and an attribute with the same label are different keys, so they
// get different variables, in the query scope and in a branch (R1).
func TestFilter_RoleAndAttributeWithSameLabel(t *testing.T) {
	joined := strings.Join(compilePatterns(Or(
		And(Eq("member", "x"), RolePlayer("member", Eq("name", "a"))),
		Eq("name", "b"),
	)), " ")
	assertContains(t, joined, "$e has member $e_s1__member;")
	assertContains(t, joined, "$e links (member: $member);")
}

// Aggregate function names are interpolated into the query, so unknown names
// are rejected before any query runs.
func TestAggregate_RejectsUnknownFunction(t *testing.T) {
	registerTestTypes(t)
	readTx := &mockTx{}
	mgr := MustNewManager[testPerson](NewDatabase(&mockConn{txs: []*mockTx{readTx}}, "test_db"))
	ctx := context.Background()
	bad := AggregateSpec{Attr: "age", Fn: "sum($e__age); match $x isa thing; reduce $y = count"}
	if _, err := mgr.Query().Aggregate(ctx, bad); err == nil {
		t.Error("Aggregate accepted an unknown function")
	}
	if _, err := mgr.Query().GroupBy("name").Aggregate(ctx, bad); err == nil {
		t.Error("GroupBy.Aggregate accepted an unknown function")
	}
	if len(readTx.queries) != 0 {
		t.Errorf("queries ran for an invalid aggregate: %v", readTx.queries)
	}
}

// avg and variance have no TypeQL reducer; both aggregate paths translate
// them (mean, and the square of std).
func TestAggregate_TranslatesAvgAndVariance(t *testing.T) {
	registerTestTypes(t)
	aggTx := &mockTx{responses: [][]map[string]any{{{"result0": float64(4), "result1": float64(3)}}}}
	groupTx := &mockTx{responses: [][]map[string]any{{{"e__name": "x", "result0": float64(4), "result1": float64(3)}}}}
	mgr := MustNewManager[testPerson](NewDatabase(&mockConn{txs: []*mockTx{aggTx, groupTx}}, "test_db"))
	ctx := context.Background()
	specs := []AggregateSpec{{Attr: "age", Fn: "avg"}, {Attr: "age", Fn: "variance"}}

	agg, err := mgr.Query().Aggregate(ctx, specs...)
	if err != nil {
		t.Fatalf("Aggregate: %v", err)
	}
	grouped, err := mgr.Query().GroupBy("name").Aggregate(ctx, specs...)
	if err != nil {
		t.Fatalf("GroupBy.Aggregate: %v", err)
	}
	for label, got := range map[string]map[string]float64{"aggregate": agg, "groupby": grouped["x"]} {
		if got["avg_age"] != 4 || got["variance_age"] != 9 {
			t.Errorf("%s results = %v, want avg_age 4 and variance_age 9", label, got)
		}
	}
	for _, q := range []string{aggTx.queries[0], groupTx.queries[0]} {
		assertContains(t, q, "$result0 = mean($e__age), $result1 = std($e__age)")
		assertTypeQL(t, "translated aggregate", q, "")
	}
}

// Role labels get the injective encoding too: first-author and first_author
// used to share one player variable, so the query could never match.
func TestRolePlayer_DistinctRoleLabelsBindDistinctVariables(t *testing.T) {
	patterns := strings.Join(compilePatterns(And(
		RolePlayer("first-author", Eq("name", "A")),
		RolePlayer("first_author", Eq("name", "B")),
	)), " ")
	// first_author uses the escaped root spelling: a variable cannot start
	// with "_", so the old "$_first_uauthor" was invalid TypeQL (fault 6).
	for _, want := range []string{
		"$e links (first-author: $first_author);",
		`$first_author__name == "A";`,
		"$e links (first_author: $0first_uauthor);",
		`$0first_uauthor__name == "B";`,
	} {
		assertContains(t, patterns, want)
	}
	assertTypeQL(t, "role labels with - and _", "match $e isa test-team;\n"+strings.ReplaceAll(patterns, "; ", ";\n"), "")
}
