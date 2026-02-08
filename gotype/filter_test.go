package gotype

import (
	"strings"
	"testing"
)

func TestEq(t *testing.T) {
	f := Eq("name", "Alice")
	patterns := f.ToPatterns("e")
	joined := strings.Join(patterns, " ")
	assertContains(t, joined, "$e has name $e__name;")
	assertContains(t, joined, `$e__name == "Alice";`)
}

func TestNeq(t *testing.T) {
	f := Neq("name", "Bob")
	patterns := f.ToPatterns("e")
	joined := strings.Join(patterns, " ")
	assertContains(t, joined, `$e__name != "Bob"`)
}

func TestGt(t *testing.T) {
	f := Gt("age", 30)
	patterns := f.ToPatterns("e")
	joined := strings.Join(patterns, " ")
	assertContains(t, joined, "$e has age $e__age;")
	assertContains(t, joined, "$e__age > 30;")
}

func TestGte(t *testing.T) {
	f := Gte("age", 30)
	joined := strings.Join(f.ToPatterns("e"), " ")
	assertContains(t, joined, "$e__age >= 30;")
}

func TestLt(t *testing.T) {
	f := Lt("age", 20)
	joined := strings.Join(f.ToPatterns("e"), " ")
	assertContains(t, joined, "$e__age < 20;")
}

func TestLte(t *testing.T) {
	f := Lte("age", 20)
	joined := strings.Join(f.ToPatterns("e"), " ")
	assertContains(t, joined, "$e__age <= 20;")
}

func TestContains(t *testing.T) {
	f := Contains("name", "Ali")
	joined := strings.Join(f.ToPatterns("e"), " ")
	assertContains(t, joined, "$e has name $e__name;")
	assertContains(t, joined, `$e__name contains "Ali";`)
}

func TestLike(t *testing.T) {
	f := Like("email", ".*@example\\.com")
	joined := strings.Join(f.ToPatterns("e"), " ")
	assertContains(t, joined, "$e has email $e__email;")
	assertContains(t, joined, "like")
}

func TestHasAttr(t *testing.T) {
	f := HasAttr("age")
	patterns := f.ToPatterns("e")
	if len(patterns) != 1 {
		t.Fatalf("expected 1 pattern, got %d", len(patterns))
	}
	assertContains(t, patterns[0], "$e has age")
	assertNotContains(t, patterns[0], "not")
}

func TestNotHasAttr(t *testing.T) {
	f := NotHasAttr("age")
	patterns := f.ToPatterns("e")
	if len(patterns) != 1 {
		t.Fatalf("expected 1 pattern, got %d", len(patterns))
	}
	assertContains(t, patterns[0], "not {")
	assertContains(t, patterns[0], "$e has age")
}

func TestByIID(t *testing.T) {
	f := ByIID("0x1234abcd")
	patterns := f.ToPatterns("e")
	if len(patterns) != 1 {
		t.Fatalf("expected 1 pattern, got %d", len(patterns))
	}
	assertEqual(t, "$e iid 0x1234abcd;", patterns[0])
}

func TestAnd(t *testing.T) {
	f := And(Eq("name", "Alice"), Gt("age", 25))
	patterns := f.ToPatterns("e")
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
	patterns := f.ToPatterns("e")
	if len(patterns) != 1 {
		t.Fatalf("expected 1 pattern, got %d", len(patterns))
	}
	assertContains(t, patterns[0], "or")
	assertContains(t, patterns[0], `"Alice"`)
	assertContains(t, patterns[0], `"Bob"`)
}

func TestNot(t *testing.T) {
	f := Not(Eq("name", "Alice"))
	patterns := f.ToPatterns("e")
	if len(patterns) != 1 {
		t.Fatalf("expected 1 pattern, got %d", len(patterns))
	}
	assertContains(t, patterns[0], "not {")
}

func TestIn(t *testing.T) {
	f := In("name", []any{"Alice", "Bob", "Carol"})
	patterns := f.ToPatterns("e")
	joined := strings.Join(patterns, " ")
	assertContains(t, joined, "$e has name $e__name;")
	assertContains(t, joined, `$e__name == "Alice"`)
	assertContains(t, joined, `$e__name == "Bob"`)
	assertContains(t, joined, `$e__name == "Carol"`)
	assertContains(t, joined, "or")
}

func TestIn_Empty(t *testing.T) {
	f := In("name", []any{})
	patterns := f.ToPatterns("e")
	joined := strings.Join(patterns, " ")
	// Empty set should produce a contradiction
	assertContains(t, joined, "iid 0xFFFFFFFFFFFFFFFF")
}

func TestNotIn(t *testing.T) {
	f := NotIn("name", []any{"Alice", "Bob"})
	patterns := f.ToPatterns("e")
	joined := strings.Join(patterns, " ")
	assertContains(t, joined, "not {")
	assertContains(t, joined, `"Alice"`)
	assertContains(t, joined, `"Bob"`)
}

func TestNotIn_Empty(t *testing.T) {
	f := NotIn("name", []any{})
	patterns := f.ToPatterns("e")
	// NOT IN empty set → always true, no patterns
	if len(patterns) != 0 {
		t.Errorf("expected no patterns for NotIn with empty set, got %d", len(patterns))
	}
}

func TestRange(t *testing.T) {
	f := Range("age", 18, 65)
	patterns := f.ToPatterns("e")
	joined := strings.Join(patterns, " ")
	assertContains(t, joined, "$e has age $e__age;")
	assertContains(t, joined, "$e__age >= 18;")
	assertContains(t, joined, "$e__age <= 65;")
}

func TestRegex(t *testing.T) {
	f := Regex("email", ".*@example\\.com")
	patterns := f.ToPatterns("e")
	joined := strings.Join(patterns, " ")
	assertContains(t, joined, "$e has email $e__email;")
	assertContains(t, joined, "like")
	assertContains(t, joined, "@example")
}

func TestStartswith(t *testing.T) {
	f := Startswith("name", "Ali")
	patterns := f.ToPatterns("e")
	joined := strings.Join(patterns, " ")
	assertContains(t, joined, "$e has name $e__name;")
	assertContains(t, joined, "like")
	assertContains(t, joined, `"Ali.*"`)
}

func TestRolePlayer(t *testing.T) {
	f := RolePlayer("employee", Eq("name", "Alice"))
	patterns := f.ToPatterns("r")
	joined := strings.Join(patterns, " ")
	assertContains(t, joined, "$r links (employee: $employee);")
	assertContains(t, joined, "$employee has name $employee__name;")
	assertContains(t, joined, `$employee__name == "Alice";`)
}

func TestRolePlayer_Nested(t *testing.T) {
	f := RolePlayer("employer", And(Eq("name", "TechCorp"), Eq("industry", "Tech")))
	patterns := f.ToPatterns("r")
	joined := strings.Join(patterns, " ")
	assertContains(t, joined, "$r links (employer: $employer);")
	assertContains(t, joined, "$employer has name $employer__name;")
	assertContains(t, joined, "$employer has industry $employer__industry;")
}

func TestSanitizeVar_Hyphens(t *testing.T) {
	f := Eq("start-date", "2024-01-15")
	patterns := f.ToPatterns("e")
	joined := strings.Join(patterns, " ")
	// Hyphens in attribute names should be sanitized in variable names
	assertContains(t, joined, "$e__start_date")
	// But NOT in the attribute name in the has clause
	assertContains(t, joined, "has start-date")
}
