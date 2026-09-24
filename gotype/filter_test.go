package gotype

import (
	"strings"
	"testing"
)

func TestEq(t *testing.T) {
	f := Eq("name", "Alice")
	patterns := compilePatterns(f)
	joined := strings.Join(patterns, " ")
	assertContains(t, joined, "$e has name $e__name;")
	assertContains(t, joined, `$e__name == "Alice";`)
}

// A non-scalar comparison value is a build error, not a panic: filters
// compile only through the query builders, which validate first (R7).
func TestEq_NonScalarIsBuildError(t *testing.T) {
	registerTestTypes(t)
	type badValue struct{ Name string }
	_, err := newScopeTestQuery(t).Filter(Eq("name", badValue{Name: "Alice"})).buildQuery()
	if err == nil || !strings.Contains(err.Error(), "requires a scalar value") {
		t.Fatalf("buildQuery error = %v, want a scalar-value error", err)
	}
}

func TestNeq(t *testing.T) {
	f := Neq("name", "Bob")
	patterns := compilePatterns(f)
	joined := strings.Join(patterns, " ")
	assertContains(t, joined, `$e__name != "Bob"`)
}

func TestGt(t *testing.T) {
	f := Gt("age", 30)
	patterns := compilePatterns(f)
	joined := strings.Join(patterns, " ")
	assertContains(t, joined, "$e has age $e__age;")
	assertContains(t, joined, "$e__age > 30;")
}

func TestGte(t *testing.T) {
	f := Gte("age", 30)
	joined := strings.Join(compilePatterns(f), " ")
	assertContains(t, joined, "$e__age >= 30;")
}

func TestLt(t *testing.T) {
	f := Lt("age", 20)
	joined := strings.Join(compilePatterns(f), " ")
	assertContains(t, joined, "$e__age < 20;")
}

func TestLte(t *testing.T) {
	f := Lte("age", 20)
	joined := strings.Join(compilePatterns(f), " ")
	assertContains(t, joined, "$e__age <= 20;")
}

func TestContains(t *testing.T) {
	f := Contains("name", "Ali")
	joined := strings.Join(compilePatterns(f), " ")
	assertContains(t, joined, "$e has name $e__name;")
	assertContains(t, joined, `$e__name contains "Ali";`)
}

func TestLike(t *testing.T) {
	f := Like("email", ".*@example\\.com")
	joined := strings.Join(compilePatterns(f), " ")
	assertContains(t, joined, "$e has email $e__email;")
	assertContains(t, joined, "like")
}

func TestHasAttr(t *testing.T) {
	f := HasAttr("age")
	patterns := compilePatterns(f)
	if len(patterns) != 1 {
		t.Fatalf("expected 1 pattern, got %d", len(patterns))
	}
	assertContains(t, patterns[0], "$e has age")
	assertNotContains(t, patterns[0], "not")
}

func TestNotHasAttr(t *testing.T) {
	f := NotHasAttr("age")
	patterns := compilePatterns(f)
	if len(patterns) != 1 {
		t.Fatalf("expected 1 pattern, got %d", len(patterns))
	}
	assertContains(t, patterns[0], "not {")
	assertContains(t, patterns[0], "$e has age")
}

func TestByIID(t *testing.T) {
	f := ByIID("0x1234abcd")
	patterns := compilePatterns(f)
	if len(patterns) != 1 {
		t.Fatalf("expected 1 pattern, got %d", len(patterns))
	}
	assertEqual(t, "$e iid 0x1234abcd;", patterns[0])
}

func TestAnd(t *testing.T) {
	f := And(Eq("name", "Alice"), Gt("age", 25))
	patterns := compilePatterns(f)
	joined := strings.Join(patterns, " ")
	assertContains(t, joined, "$e has name $e__name;")
	assertContains(t, joined, "$e has age $e__age;")
	assertContains(t, joined, `$e__name == "Alice";`)
	assertContains(t, joined, "$e__age > 25;")
}

func TestAnd_Flattens(t *testing.T) {
	f := And(Eq("name", "Alice"), And(Gt("age", 25), Lt("age", 50)))
	a := f.(*AndFilter)
	if len(a.Filters) != 3 {
		t.Errorf("expected 3 flattened filters, got %d", len(a.Filters))
	}
}

func TestOr(t *testing.T) {
	f := Or(Eq("name", "Alice"), Eq("name", "Bob"))
	patterns := compilePatterns(f)
	if len(patterns) != 1 {
		t.Fatalf("expected 1 pattern, got %d", len(patterns))
	}
	// Each branch is a child scope with an id from the query counter (R2);
	// numbering starts at 1 for each query.
	want := `{ $e has name $e_s1__name; $e_s1__name == "Alice"; } or ` +
		`{ $e has name $e_s2__name; $e_s2__name == "Bob"; };`
	assertEqual(t, want, patterns[0])
}

func TestNot(t *testing.T) {
	f := Not(Eq("name", "Alice"))
	patterns := compilePatterns(f)
	if len(patterns) != 1 {
		t.Fatalf("expected 1 pattern, got %d", len(patterns))
	}
	// The not body is a child scope (R2): it binds its own variable.
	assertEqual(t, `not { $e has name $e_s1__name; $e_s1__name == "Alice"; };`, patterns[0])
}

func TestIn(t *testing.T) {
	f := In("name", []any{"Alice", "Bob", "Carol"})
	patterns := compilePatterns(f)
	joined := strings.Join(patterns, " ")
	assertContains(t, joined, "$e has name $e__name;")
	assertContains(t, joined, `$e__name == "Alice"`)
	assertContains(t, joined, `$e__name == "Bob"`)
	assertContains(t, joined, `$e__name == "Carol"`)
	assertContains(t, joined, "or")
}

func TestIn_Empty(t *testing.T) {
	f := In("name", []any{})
	patterns := compilePatterns(f)
	joined := strings.Join(patterns, " ")
	// Empty set should produce a structurally valid contradiction, not a
	// fabricated IID literal (issue #85).
	assertContains(t, joined, "not { $e is $e; };")
	assertNotContains(t, joined, "0xFFFFFFFFFFFFFFFF")
}

func TestNotIn(t *testing.T) {
	f := NotIn("name", []any{"Alice", "Bob"})
	patterns := compilePatterns(f)
	joined := strings.Join(patterns, " ")
	assertContains(t, joined, "not {")
	assertContains(t, joined, `"Alice"`)
	assertContains(t, joined, `"Bob"`)
}

func TestNotIn_Empty(t *testing.T) {
	f := NotIn("name", []any{})
	patterns := compilePatterns(f)
	// NOT IN empty set → always true, no patterns
	if len(patterns) != 0 {
		t.Errorf("expected no patterns for NotIn with empty set, got %d", len(patterns))
	}
}

func TestRange(t *testing.T) {
	f := Range("age", 18, 65)
	patterns := compilePatterns(f)
	joined := strings.Join(patterns, " ")
	assertContains(t, joined, "$e has age $e__age;")
	assertContains(t, joined, "$e__age >= 18;")
	assertContains(t, joined, "$e__age <= 65;")
}

func TestRegex(t *testing.T) {
	f := Regex("email", ".*@example\\.com")
	patterns := compilePatterns(f)
	joined := strings.Join(patterns, " ")
	assertContains(t, joined, "$e has email $e__email;")
	assertContains(t, joined, "like")
	assertContains(t, joined, "@example")
}

func TestStartswith(t *testing.T) {
	f := Startswith("name", "Ali")
	patterns := compilePatterns(f)
	joined := strings.Join(patterns, " ")
	assertContains(t, joined, "$e has name $e__name;")
	assertContains(t, joined, "like")
	assertContains(t, joined, `"Ali.*"`)
}

func TestRolePlayer(t *testing.T) {
	f := RolePlayer("employee", Eq("name", "Alice"))
	patterns := compilePatternsFor("r", f)
	joined := strings.Join(patterns, " ")
	assertContains(t, joined, "$r links (employee: $employee);")
	assertContains(t, joined, "$employee has name $employee__name;")
	assertContains(t, joined, `$employee__name == "Alice";`)
}

func TestRolePlayer_Nested(t *testing.T) {
	f := RolePlayer("employer", And(Eq("name", "TechCorp"), Eq("industry", "Tech")))
	patterns := compilePatternsFor("r", f)
	joined := strings.Join(patterns, " ")
	assertContains(t, joined, "$r links (employer: $employer);")
	assertContains(t, joined, "$employer has name $employer__name;")
	assertContains(t, joined, "$employer has industry $employer__industry;")
}

func TestSanitizeVar_Hyphens(t *testing.T) {
	f := Eq("start-date", "2024-01-15")
	patterns := compilePatterns(f)
	joined := strings.Join(patterns, " ")
	// Hyphens in attribute names should be sanitized in variable names
	assertContains(t, joined, "$e__start_date")
	// But NOT in the attribute name in the has clause
	assertContains(t, joined, "has start-date")
}

// --- IIDIn filter ---

func TestIIDIn_Single(t *testing.T) {
	f := IIDIn("0x1234")
	patterns := compilePatterns(f)
	if len(patterns) != 1 {
		t.Fatalf("expected 1 pattern, got %d", len(patterns))
	}
	if patterns[0] != "$e iid 0x1234;" {
		t.Errorf("got %q", patterns[0])
	}
}

func TestIIDIn_Multiple(t *testing.T) {
	f := IIDIn("0x1234", "0x5678", "0x9abc")
	patterns := compilePatterns(f)
	if len(patterns) != 1 {
		t.Fatalf("expected 1 pattern, got %d", len(patterns))
	}
	joined := patterns[0]
	assertContains(t, joined, "{ $e iid 0x1234; }")
	assertContains(t, joined, "{ $e iid 0x5678; }")
	assertContains(t, joined, " or ")
}

// --- Computed expressions ---

// Computed compiles a typed expression: the compiler binds each attribute
// (fault 5) and allocates the result variable (R6).
func TestComputedFilter(t *testing.T) {
	f := Computed(Mul(Attr("price"), Attr("quantity")), ">", 100.0)
	patterns := compilePatterns(f)
	want := []string{
		"$e has price $e__price;",
		"$e has quantity $e__quantity;",
		"let $result1 = ($e__price * $e__quantity);",
		"$result1 > 100;",
	}
	if strings.Join(patterns, "\n") != strings.Join(want, "\n") {
		t.Fatalf("got:\n%s\nwant:\n%s", strings.Join(patterns, "\n"), strings.Join(want, "\n"))
	}
	assertTypeQL(t, "computed", "match $e isa test-person;\n"+strings.Join(patterns, "\n"), "")
}

// Attribute labels with hyphens use the readable variable form.
func TestComputedFilter_Hyphens(t *testing.T) {
	joined := strings.Join(compilePatterns(Computed(Add(Attr("unit-price"), Attr("shipping-cost")), ">", 0)), " ")
	assertContains(t, joined, "let $result1 = ($e__unit_price + $e__shipping_cost);")
}

func TestComputedFilter_Functions(t *testing.T) {
	joined := strings.Join(compilePatterns(Computed(Abs(Attr("balance")), ">", 1000)), " ")
	assertContains(t, joined, "$e has balance $e__balance;")
	assertContains(t, joined, "let $result1 = abs($e__balance);")
	joined = strings.Join(compilePatterns(Computed(Max(Attr("score"), Literal(2)), ">=", 2)), " ")
	assertContains(t, joined, "let $result1 = max($e__score, 2);")
}

// A Computed attribute shares the variable of a filter on the same attribute
// in the same scope (R3), so the attribute is bound once (R5).
func TestComputedFilter_SharesFilterAttribute(t *testing.T) {
	joined := strings.Join(compilePatterns(And(Gt("age", 0), Computed(Mul(Attr("age"), Literal(2)), ">", 10))), "\n")
	if n := strings.Count(joined, "$e has age "); n != 1 {
		t.Fatalf("age bound %d times:\n%s", n, joined)
	}
	assertContains(t, joined, "let $result1 = ($e__age * 2);")
}

func TestComputedFilter_InvalidExpression(t *testing.T) {
	registerTestTypes(t)
	for name, f := range map[string]Filter{
		"nil expr":     Computed(nil, ">", 1),
		"bad attr":     Computed(Attr("bad name"), ">", 1),
		"nil literal":  Computed(Literal(nil), ">", 1),
		"bad operator": Computed(Attr("age"), "<>", 1),
		"bad value":    Computed(Attr("age"), ">", []int{1}),
		"nil operand":  Computed(Mul(Attr("age"), nil), ">", 1),
	} {
		if _, err := newScopeTestQuery(t).Filter(f).buildQuery(); err == nil {
			t.Errorf("%s: expected a build error", name)
		}
	}
}

func TestIIDIn_Empty(t *testing.T) {
	f := IIDIn()
	patterns := compilePatterns(f)
	if len(patterns) != 1 {
		t.Fatalf("expected 1 pattern, got %d", len(patterns))
	}
	// Empty IIDIn should produce a structurally valid contradiction, not a
	// fabricated IID literal (issue #85).
	assertContains(t, patterns[0], "not { $e is $e; };")
	assertNotContains(t, patterns[0], "0xFFFFFFFFFFFFFFFF")
}
