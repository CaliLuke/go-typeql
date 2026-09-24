package gotype

import (
	"context"
	"regexp"
	"strings"
	"testing"
)

// Tests for the filter compiler and the variable allocator (issue #138).
// Faults 1 to 6 are the faults of the issue, written with the new API.

// buildTestQuery builds the fetch query of filters on testPerson.
func buildTestQuery(t *testing.T, filters ...Filter) string {
	t.Helper()
	q, err := newScopeTestQuery(t).Filter(filters...).buildQuery()
	if err != nil {
		t.Fatalf("buildQuery: %v", err)
	}
	return q
}

// Fault 1: a role named "e" must not reuse the root variable.
func TestCompile_Fault1_RoleNamedRoot(t *testing.T) {
	registerTestTypes(t)
	q := buildTestQuery(t, RolePlayer("e", Eq("name", "A")))
	assertContains(t, q, "$e links (e: $e_2);")
	assertContains(t, q, "$e_2 has name $e_2__name;")
	assertTypeQL(t, "role named e", q, "")
}

// Fault 2: a Computed result never takes the name of an attribute variable.
func TestCompile_Fault2_ComputedAndAttribute(t *testing.T) {
	registerTestTypes(t)
	q := buildTestQuery(t, Gt("age", 1), Computed(Mul(Attr("age"), Literal(2)), ">", 10))
	assertContains(t, q, "$e has age $e__age;\n$e__age > 1;\nlet $result1 = ($e__age * 2);\n$result1 > 10;")
	assertTypeQL(t, "computed and attribute", q, "")
}

// Fault 3: a Computed result and a reduce output are different families, so
// they never share a variable even when their candidates are equal.
func TestCompile_Fault3_ComputedAndReduceOutput(t *testing.T) {
	registerTestTypes(t)
	tx := &mockTx{responses: [][]map[string]any{{{"result0": float64(1), "result1_2": float64(2)}}}}
	mgr := MustNewManager[testPerson](NewDatabase(&mockConn{txs: []*mockTx{tx}}, "test_db"))
	got, err := mgr.Query().Filter(Computed(Add(Attr("age"), Literal(1)), ">", 1)).
		Aggregate(context.Background(), AggregateSpec{Attr: "age", Fn: "sum"}, AggregateSpec{Attr: "age", Fn: "max"})
	if err != nil {
		t.Fatalf("Aggregate: %v", err)
	}
	q := tx.queries[0]
	assertContains(t, q, "let $result1 = ($e__age + 1);")
	assertContains(t, q, "reduce $result0 = sum($e__age), $result1_2 = max($e__age);")
	// The rows are read by the allocated names, including the suffix.
	if got["sum_age"] != 1 || got["max_age"] != 2 {
		t.Errorf("results = %v", got)
	}
	assertTypeQL(t, "computed and reduce output", q, "")
}

// Fault 4: an outer variable is not renamed inside a branch. With typed
// expressions, a Computed in a branch computes its own value, and the owner
// crosses into the branch.
func TestCompile_Fault4_BranchUsesOuterOwner(t *testing.T) {
	registerTestTypes(t)
	q := buildTestQuery(t,
		Computed(Mul(Attr("age"), Literal(2)), ">", 10),
		Or(Computed(Attr("age"), ">", 1), Eq("name", "x")),
	)
	assertContains(t, q, "{ $e has age $e_s2__age; let $result3 = $e_s2__age; $result3 > 1; } or ")
	assertTypeQL(t, "branch uses outer owner", q, "")
}

// Fault 5: Computed binds its attributes, with no companion filter.
func TestCompile_Fault5_ComputedBindsAttributes(t *testing.T) {
	registerTestTypes(t)
	q := buildTestQuery(t, Computed(Mul(Attr("age"), Attr("age")), ">", 100))
	assertContains(t, q, "$e has age $e__age;\nlet $result1 = ($e__age * $e__age);")
	assertTypeQL(t, "computed binds attributes", q, "")
}

// Fault 6: a role label with "_" gets a valid variable (no leading "_").
func TestCompile_Fault6_RoleLabelWithUnderscore(t *testing.T) {
	registerMultiValueTypes(t)
	mgr := MustNewManager[testTeam](NewDatabase(&mockConn{}, "test_db"))
	q, err := mgr.Query().Filter(RolePlayer("first_author", Eq("name", "B"))).buildQuery()
	if err != nil {
		t.Fatalf("buildQuery: %v", err)
	}
	assertContains(t, q, "$e links (first_author: $0first_uauthor);")
	assertTypeQL(t, "role label with _", q, "")
}

// R2: NotIn and NotHasAttr compile as Not scopes, so the negated body binds
// its own variable instead of the variable of an outer filter.
func TestCompile_R2_NegatedFiltersOpenScopes(t *testing.T) {
	registerTestTypes(t)
	q := buildTestQuery(t, Gt("age", 1), NotIn("age", []any{5, 6}))
	assertContains(t, q, "$e has age $e__age;\n$e__age > 1;\n")
	assertContains(t, q, "not { $e has age $e_s1__age; { $e_s1__age == 5; } or { $e_s1__age == 6; }; };")
	assertTypeQL(t, "negated filter scope", q, "")

	q = buildTestQuery(t, NotHasAttr("age"))
	assertContains(t, q, "not { $e has age $_; };")
}

// R2: the example of the issue. The not body means "no age value is 5".
func TestCompile_R2_NotBodyBindsOwnVariable(t *testing.T) {
	registerTestTypes(t)
	q := buildTestQuery(t, Gt("age", 1), Not(Eq("age", 5)))
	assertContains(t, q, "$e has age $e__age;\n$e__age > 1;\nnot { $e has age $e_s1__age; $e_s1__age == 5; };")
}

// R4: two RolePlayer filters on one role in one scope share the player.
func TestCompile_R4_SharedPlayer(t *testing.T) {
	joined := strings.Join(compilePatterns(And(
		RolePlayer("member", Eq("name", "a")),
		RolePlayer("member", Gt("age", 1)),
	)), "\n")
	if n := strings.Count(joined, "links (member:"); n != 1 {
		t.Fatalf("member linked %d times:\n%s", n, joined)
	}
	assertContains(t, joined, "$member has name $member__name;")
	assertContains(t, joined, "$member has age $member__age;")
}

// R5: one binding per attribute and scope, for comparisons, sorts, reduce
// specs, and Computed expressions.
func TestCompile_R5_OneBindingPerScope(t *testing.T) {
	registerTestTypes(t)
	q, err := newScopeTestQuery(t).
		Filter(Gt("age", 1), Lt("age", 50), Computed(Mul(Attr("age"), Literal(2)), "<", 90)).
		OrderAsc("age").
		buildQuery()
	if err != nil {
		t.Fatalf("buildQuery: %v", err)
	}
	if n := strings.Count(q, "$e has age "); n != 1 {
		t.Fatalf("age bound %d times:\n%s", n, q)
	}
	assertContains(t, q, "sort $e__age asc;")
	assertTypeQL(t, "one binding", q, "")
}

// R8: every generated variable is a valid TypeQL variable: a letter or digit,
// then letters, digits, "_", or "-".
var validVarRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]*$`)

func TestCompile_R8_ValidNames(t *testing.T) {
	alloc := newVarAlloc()
	for _, k := range []varKey{
		{family: famRoot},
		{family: famAttr, owner: "e", label: "first_name"},
		{family: famAttr, owner: "e", label: "a-b_c", scope: 3},
		{family: famPlayer, owner: "e", label: "first_author"},
		{family: famPlayer, owner: "e", label: "e"},
		{family: famPlayer, owner: "e", label: "a--"},
		{family: famComputed, label: "1"},
		{family: famReduce, label: "1"},
		{family: famOld, label: "0"},
	} {
		n, _ := alloc.name(k)
		if !validVarRe.MatchString(n) {
			t.Errorf("name %q for key %+v is not a valid TypeQL variable", n, k)
		}
	}
}

// R1: the same key keeps its name; distinct keys get distinct names, also
// when the candidates are equal.
func TestCompile_R1_Allocator(t *testing.T) {
	alloc := newVarAlloc()
	a, isNew := alloc.name(varKey{family: famComputed, label: "1"})
	if !isNew || a != "result1" {
		t.Fatalf("first allocation = %q, %v", a, isNew)
	}
	if b, isNew := alloc.name(varKey{family: famComputed, label: "1"}); isNew || b != a {
		t.Fatalf("same key = %q, %v", b, isNew)
	}
	if c, _ := alloc.name(varKey{family: famReduce, label: "1"}); c != "result1_2" {
		t.Fatalf("reduce output with the same candidate = %q, want result1_2", c)
	}
}

// Names that a builder writes itself are reserved, so no filter variable
// takes them (the fetch projection).
func TestCompile_ReservedNames(t *testing.T) {
	registerMultiValueTypes(t)
	mgr := MustNewManager[testTeam](NewDatabase(&mockConn{}, "test_db"))
	q, err := mgr.Query().Filter(RolePlayer("projection-type", Eq("name", "x"))).
		buildQueryWithFetch("$e isa! $projection_type;", "fetch { };", "projection_type")
	if err != nil {
		t.Fatalf("buildQueryWithFetch: %v", err)
	}
	assertContains(t, q, "$e links (projection-type: $projection_type_2);")
}

// The same filter tree gives the same text in every build.
func TestCompile_Deterministic(t *testing.T) {
	registerTestTypes(t)
	f := And(Or(Eq("name", "a"), Not(Gt("age", 1))), Computed(Abs(Attr("age")), ">", 1))
	first := buildTestQuery(t, f)
	for range 5 {
		if again := buildTestQuery(t, f); again != first {
			t.Fatalf("text differs between builds:\n%s\n---\n%s", first, again)
		}
	}
}

// Every expression constructor renders the TypeQL operator or built-in, and
// the query passes typeql-check.
func TestCompile_ExpressionConstructors(t *testing.T) {
	registerTestTypes(t)
	a, b := Attr("age"), Literal(2)
	for want, e := range map[string]Expr{
		"($e__age + 2)": Add(a, b), "($e__age - 2)": Sub(a, b), "($e__age * 2)": Mul(a, b),
		"($e__age / 2)": Div(a, b), "($e__age % 2)": Mod(a, b), "($e__age ^ 2)": Pow(a, b),
		"std::math::abs($e__age)": Abs(a), "std::math::ceil($e__age)": Ceil(a),
		"std::math::floor($e__age)": Floor(a), "std::math::round($e__age)": Round(a),
		"std::math::log10($e__age)": Log10(a), "std::string::len($e__age)": Length(a),
		"std::math::max($e__age, 2)": Max(a, b), "std::math::min($e__age, 2)": Min(a, b),
	} {
		q := buildTestQuery(t, Computed(e, ">", 0))
		assertContains(t, q, "let $result1 = "+want+";")
		assertTypeQL(t, want, q, "")
	}
}

// The Negated field of each attribute filter compiles as a Not scope (R2).
func TestCompile_NegatedFieldsOpenScopes(t *testing.T) {
	registerTestTypes(t)
	for name, f := range map[string]Filter{
		"comparison": &ComparisonFilter{Attr: "age", Op: ">", Value: 1, Negated: true},
		"string":     &StringFilter{Attr: "name", Op: "contains", Pattern: "a", Negated: true},
		"range":      &RangeFilter{Attr: "age", Min: 1, Max: 9, Negated: true},
		"regex":      &RegexFilter{Attr: "name", Pattern: "a.*", Negated: true},
	} {
		q := buildTestQuery(t, f)
		assertContains(t, q, "not { $e has ")
		assertContains(t, q, "_s1__")
		assertTypeQL(t, "negated "+name, q, "")
	}
}
