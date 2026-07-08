package ast

import (
	"math"
	"strings"
	"testing"
	"time"
)

// TestBuilders_CompleteInsertQuery demonstrates building a complete insert query.
func TestBuilders_CompleteInsertQuery(t *testing.T) {
	// Build: insert $p isa person, has name "Alice", has email "alice@example.com";
	query := Insert(
		IsaStmt("$thing", "person"),
		HasStmt("$thing", "name", Str("Alice")),
		HasStmt("$thing", "email", Str("alice@example.com")),
	)

	c := &Compiler{}
	result, err := c.compileClause(query)
	if err != nil {
		t.Fatalf("compile error: %v", err)
	}

	expected := `insert
$thing isa person;
$thing has name "Alice";
$thing has email "alice@example.com";`

	if result != expected {
		t.Errorf("Expected:\n%s\n\nGot:\n%s", expected, result)
	}
}

// TestBuilders_CompletePutQuery demonstrates building a complete put query.
func TestBuilders_CompletePutQuery(t *testing.T) {
	// Build: put $p isa person, has name "Bob", has email "bob@example.com";
	query := Put(
		IsaStmt("$thing", "person"),
		HasStmt("$thing", "name", Str("Bob")),
		HasStmt("$thing", "email", Str("bob@example.com")),
	)

	c := &Compiler{}
	result, err := c.compileClause(query)
	if err != nil {
		t.Fatalf("compile error: %v", err)
	}

	if !strings.HasPrefix(result, "put\n") {
		t.Errorf("Expected put clause, got: %s", result)
	}
	if !strings.Contains(result, `has name "Bob"`) {
		t.Errorf("Missing name attribute: %s", result)
	}
}

// TestBuilders_MatchWithConstraints demonstrates building a match query with constraints.
func TestBuilders_MatchWithConstraints(t *testing.T) {
	// Build: match $p isa person, has email "alice@example.com";
	query := Match(
		Entity("$p", "person", Has("email", Str("alice@example.com"))),
	)

	c := &Compiler{}
	result, err := c.compileClause(query)
	if err != nil {
		t.Fatalf("compile error: %v", err)
	}

	expected := `match
$p isa person, has email "alice@example.com";`

	if result != expected {
		t.Errorf("Expected:\n%s\n\nGot:\n%s", expected, result)
	}
}

// TestBuilders_MatchWithIID demonstrates building a match by IID.
func TestBuilders_MatchWithIID(t *testing.T) {
	// Build: match $p iid 0x123abc;
	query := Match(
		Entity("$p", "person", Iid("0x123abc")),
	)

	c := &Compiler{}
	result, err := c.compileClause(query)
	if err != nil {
		t.Fatalf("compile error: %v", err)
	}

	if !strings.Contains(result, "iid 0x123abc") {
		t.Errorf("Missing IID constraint: %s", result)
	}
}

// TestBuilders_FetchClause demonstrates building a fetch clause.
func TestBuilders_FetchClause(t *testing.T) {
	// Build: fetch { "_iid": iid($p), "name": $p.name, "email": $p.email };
	query := Fetch(
		FetchFunc("_iid", "iid", "$p"),
		FetchAttr("name", "$p", "name"),
		FetchAttr("email", "$p", "email"),
	)

	c := &Compiler{}
	result, err := c.compileClause(query)
	if err != nil {
		t.Fatalf("compile error: %v", err)
	}

	expected := `fetch {
  "_iid": iid($p),
  "name": $p.name,
  "email": $p.email
};`

	if result != expected {
		t.Errorf("Expected:\n%s\n\nGot:\n%s", expected, result)
	}
}

// TestBuilders_SelectClause demonstrates building a select clause.
func TestBuilders_SelectClause(t *testing.T) {
	query := Select("$did", "$name", "$status")

	c := &Compiler{}
	result, err := c.compileClause(query)
	if err != nil {
		t.Fatalf("compile error: %v", err)
	}

	expected := "select $did, $name, $status;"
	if result != expected {
		t.Errorf("Expected: %s, Got: %s", expected, result)
	}
}

// TestBuilders_SortClause demonstrates building a sort clause.
func TestBuilders_SortClause(t *testing.T) {
	query := Sort("$name", "asc")

	c := &Compiler{}
	result, err := c.compileClause(query)
	if err != nil {
		t.Fatalf("compile error: %v", err)
	}

	expected := "sort $name asc;"
	if result != expected {
		t.Errorf("Expected: %s, Got: %s", expected, result)
	}
}

// TestBuilders_OffsetClause demonstrates building an offset clause.
func TestBuilders_OffsetClause(t *testing.T) {
	query := Offset(10)

	c := &Compiler{}
	result, err := c.compileClause(query)
	if err != nil {
		t.Fatalf("compile error: %v", err)
	}

	expected := "offset 10;"
	if result != expected {
		t.Errorf("Expected: %s, Got: %s", expected, result)
	}
}

// TestBuilders_LimitClause demonstrates building a limit clause.
func TestBuilders_LimitClause(t *testing.T) {
	query := Limit(20)

	c := &Compiler{}
	result, err := c.compileClause(query)
	if err != nil {
		t.Fatalf("compile error: %v", err)
	}

	expected := "limit 20;"
	if result != expected {
		t.Errorf("Expected: %s, Got: %s", expected, result)
	}
}

// TestBuilders_RelationPattern demonstrates building a relation pattern with role players.
func TestBuilders_RelationPattern(t *testing.T) {
	// Build: match $e (employee: $p, employer: $c) isa employment;
	query := Match(
		Relation("$e", "employment", []RolePlayer{
			Role("employee", "$p"),
			Role("employer", "$c"),
		}),
	)

	c := &Compiler{}
	result, err := c.compileClause(query)
	if err != nil {
		t.Fatalf("compile error: %v", err)
	}

	if !strings.Contains(result, "employee: $p") {
		t.Errorf("Missing employee role: %s", result)
	}
	if !strings.Contains(result, "employer: $c") {
		t.Errorf("Missing employer role: %s", result)
	}
	if !strings.Contains(result, "isa employment") {
		t.Errorf("Missing isa constraint: %s", result)
	}
}

// TestBuilders_CompleteQuery demonstrates building a complete multi-clause query.
func TestBuilders_CompleteQuery(t *testing.T) {
	// This would be compiled as multiple separate clauses,
	// but demonstrates the API for building each part.

	// Match clause
	matchClause := Match(
		Entity("$p", "person", Has("age", Long(30))),
	)

	// Fetch clause
	fetchClause := Fetch(
		FetchFunc("_iid", "iid", "$p"),
		FetchAttrPath("name", "$p.name"),
	)

	// Sort clause
	sortClause := Sort("$p.name", "asc")

	// Offset and Limit
	offsetClause := Offset(10)
	limitClause := Limit(5)

	c := &Compiler{}

	// Compile each clause
	matchStr, _ := c.compileClause(matchClause)
	fetchStr, _ := c.compileClause(fetchClause)
	sortStr, _ := c.compileClause(sortClause)
	offsetStr, _ := c.compileClause(offsetClause)
	limitStr, _ := c.compileClause(limitClause)

	// Verify each part
	if !strings.Contains(matchStr, "match") {
		t.Error("Match clause missing")
	}
	if !strings.Contains(fetchStr, "fetch") {
		t.Error("Fetch clause missing")
	}
	if !strings.Contains(sortStr, "sort") {
		t.Error("Sort clause missing")
	}
	if !strings.Contains(offsetStr, "offset 10") {
		t.Error("Offset clause missing")
	}
	if !strings.Contains(limitStr, "limit 5") {
		t.Error("Limit clause missing")
	}
}

// TestBuilders_StrictIsaConstraint demonstrates using isa! (exact type match).
func TestBuilders_StrictIsaConstraint(t *testing.T) {
	// Build: match $p isa! person;
	query := Match(
		Entity("$p", "person", IsaExact("person")),
	)

	c := &Compiler{}
	result, err := c.compileClause(query)
	if err != nil {
		t.Fatalf("compile error: %v", err)
	}

	if !strings.Contains(result, "isa! person") {
		t.Errorf("Missing strict isa constraint: %s", result)
	}
}

// TestBuilders_LiteralTypes demonstrates different literal value types.
func TestBuilders_LiteralTypes(t *testing.T) {
	tests := []struct {
		name     string
		literal  LiteralValue
		expected string
	}{
		{"string", Str("hello"), `"hello"`},
		{"long", Long(42), "42"},
		{"double", Double(3.14), "3.14"},
		{"bool true", Bool(true), "true"},
		{"bool false", Bool(false), "false"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := FormatLiteral(tt.literal.Val, tt.literal.ValueType)
			if result != tt.expected {
				t.Errorf("Expected: %s, Got: %s", tt.expected, result)
			}
		})
	}
}

// TestBuilders_DeleteHasStatement demonstrates building a delete attribute statement.
func TestBuilders_DeleteHasStatement(t *testing.T) {
	// Build: match $p isa person, has name $old;
	//        delete $old of $p;
	matchClause := Match(
		Entity("$p", "person"),
		HasPattern{ThingVar: "$p", AttrType: "name", AttrVar: "$old"},
	)

	deleteClause := Delete(
		DeleteHas("$old", "$p"),
	)

	c := &Compiler{}
	matchStr, _ := c.Compile(matchClause)
	deleteStr, _ := c.Compile(deleteClause)

	if !strings.Contains(matchStr, "$p isa person") {
		t.Errorf("Match clause missing entity pattern: %s", matchStr)
	}
	if !strings.Contains(deleteStr, "$old of $p") {
		t.Errorf("Delete clause missing 'of' syntax: %s", deleteStr)
	}
}

// TestBuilders_CmpPattern demonstrates building a value comparison pattern.
func TestBuilders_CmpPattern(t *testing.T) {
	// Build: match $p isa person, has age $a; $a > 18;
	query := Match(
		Entity("$p", "person"),
		HasPattern{ThingVar: "$p", AttrType: "age", AttrVar: "$a"},
		Cmp("$a", ">", Long(18)),
	)

	c := &Compiler{}
	result, err := c.Compile(query)
	if err != nil {
		t.Fatalf("compile error: %v", err)
	}

	if !strings.Contains(result, "$a > 18") {
		t.Errorf("Missing comparison pattern: %s", result)
	}
}

// TestHas_BareGoValues is a regression test for issue #25: bare Go values in
// Has must be emitted as proper TypeQL literals, not spliced in verbatim.
func TestHas_BareGoValues(t *testing.T) {
	c := &Compiler{}
	query := Match(
		Entity("$p", "person",
			Has("name", "Alice"),
			Has("age", 42),
			Has("email", "$e"),
		),
	)

	result, err := c.Compile(query)
	if err != nil {
		t.Fatalf("compile error: %v", err)
	}

	if !strings.Contains(result, `has name "Alice"`) {
		t.Errorf("bare string should be quoted: %s", result)
	}
	if !strings.Contains(result, "has age 42") {
		t.Errorf("bare int should compile to an integer literal: %s", result)
	}
	if !strings.Contains(result, "has email $e") {
		t.Errorf("$-prefixed string should stay a variable reference: %s", result)
	}
}

// TestHas_InjectionEscaped is a regression test for issue #25: attacker-shaped
// strings must be quoted and escaped, never spliced into the query.
func TestHas_InjectionEscaped(t *testing.T) {
	c := &Compiler{}
	query := Match(
		Entity("$p", "person", Has("name", `x"; delete $p; match $q isa person, has name "y`)),
	)

	result, err := c.Compile(query)
	if err != nil {
		t.Fatalf("compile error: %v", err)
	}

	want := `has name "x\"; delete $p; match $q isa person, has name \"y"`
	if !strings.Contains(result, want) {
		t.Errorf("injection payload not escaped.\nwant substring: %s\ngot: %s", want, result)
	}
}

// TestCmp_BareGoValues is a regression test for issue #25 on the Cmp path.
func TestCmp_BareGoValues(t *testing.T) {
	c := &Compiler{}
	tests := []struct {
		name string
		node ValueComparisonPattern
		want string
	}{
		{"bare string quoted", Cmp("$name", "==", "Alice"), `$name == "Alice"`},
		{"bare int literal", Cmp("$age", ">", 18), "$age > 18"},
		{"variable reference", Cmp("$a", "==", "$b"), "$a == $b"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := c.Compile(tt.node)
			if err != nil {
				t.Fatalf("compile error: %v", err)
			}
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

// TestFuncCall_BareGoValueArgs is a regression test for issue #25 on
// FunctionCallValue.Args.
func TestFuncCall_BareGoValueArgs(t *testing.T) {
	c := &Compiler{}
	got, err := c.Compile(FuncCall("f", 42, "abc", "$x"))
	if err != nil {
		t.Fatalf("compile error: %v", err)
	}
	want := `f(42, "abc", $x)`
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// TestValueFromGo_IntegerWidths is a regression test for issue #64: all
// integer widths must compile to integer literals, not quoted strings.
func TestValueFromGo_IntegerWidths(t *testing.T) {
	c := &Compiler{}
	tests := []struct {
		name string
		val  any
		want string
	}{
		{"int", int(5), "5"},
		{"int8", int8(-8), "-8"},
		{"int16", int16(16), "16"},
		{"int32", int32(-32), "-32"},
		{"int64", int64(64), "64"},
		{"uint", uint(5), "5"},
		{"uint8", uint8(8), "8"},
		{"uint16", uint16(16), "16"},
		{"uint32", uint32(32), "32"},
		{"uint64", uint64(64), "64"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := c.Compile(ValueFromGo(tt.val))
			if err != nil {
				t.Fatalf("compile error: %v", err)
			}
			if got != tt.want {
				t.Errorf("ValueFromGo(%v): got %q, want %q", tt.val, got, tt.want)
			}
		})
	}
}

// TestValueFromGo_NamedTypes is a regression test for issue #64: named types
// must be formatted by kind, with single (not double) quoting/escaping.
func TestValueFromGo_NamedTypes(t *testing.T) {
	type myStr string
	type myInt int32
	type myFloat float64
	type myBool bool

	c := &Compiler{}
	tests := []struct {
		name string
		val  any
		want string
	}{
		{"named string single escape", myStr(`he said "hi"`), `"he said \"hi\""`},
		{"named int", myInt(7), "7"},
		{"named float", myFloat(2.5), "2.5"},
		{"named bool", myBool(true), "true"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := c.Compile(ValueFromGo(tt.val))
			if err != nil {
				t.Fatalf("compile error: %v", err)
			}
			if got != tt.want {
				t.Errorf("ValueFromGo(%v): got %q, want %q", tt.val, got, tt.want)
			}
		})
	}
}

// TestValueFromGo_Pointers is a regression test for issue #64: pointers are
// dereferenced; nil pointers fail at compile time.
func TestValueFromGo_Pointers(t *testing.T) {
	c := &Compiler{}

	n := 5
	got, err := c.Compile(ValueFromGo(&n))
	if err != nil {
		t.Fatalf("compile error: %v", err)
	}
	if got != "5" {
		t.Errorf("ValueFromGo(&5): got %q, want %q", got, "5")
	}

	var nilStr *string
	if _, err := c.Compile(ValueFromGo(nilStr)); err == nil {
		t.Error("expected compile error for nil *string, got nil")
	}
}

// TestValueFromGo_Nil is a regression test for issue #64: nil must fail at
// compile time instead of silently becoming the empty string.
func TestValueFromGo_Nil(t *testing.T) {
	c := &Compiler{}
	if _, err := c.Compile(ValueFromGo(nil)); err == nil {
		t.Error("expected compile error for nil, got nil")
	}
}

// TestValueFromGo_Uint64Overflow verifies unsigned values beyond int64 range
// fail at compile time instead of wrapping around.
func TestValueFromGo_Uint64Overflow(t *testing.T) {
	c := &Compiler{}
	if _, err := c.Compile(ValueFromGo(uint64(math.MaxInt64) + 1)); err == nil {
		t.Error("expected compile error for uint64 overflow, got nil")
	}
}

// TestValueFromGo_UnknownTypeFallback verifies the fallback formats unknown
// types exactly once (no double quoting/escaping, issue #64).
func TestValueFromGo_UnknownTypeFallback(t *testing.T) {
	type point struct{ X, Y int }

	c := &Compiler{}
	got, err := c.Compile(ValueFromGo(point{1, 2}))
	if err != nil {
		t.Fatalf("compile error: %v", err)
	}
	want := `"{1 2}"`
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// TestValueFromGo_Datetime verifies time.Time compiles to an unquoted naive
// (zone-less) datetime literal, not a datetime-tz literal (issue #66).
func TestValueFromGo_Datetime(t *testing.T) {
	c := &Compiler{}
	got, err := c.Compile(ValueFromGo(time.Date(2024, 1, 15, 10, 30, 0, 0, time.UTC)))
	if err != nil {
		t.Fatalf("compile error: %v", err)
	}
	want := "2024-01-15T10:30:00"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// TestValueFromGo_DatetimeNonUTC verifies a non-UTC time.Time converts to
// UTC before the zone is dropped, preserving the instant (issue #53).
func TestValueFromGo_DatetimeNonUTC(t *testing.T) {
	c := &Compiler{}
	loc := time.FixedZone("UTC+2", 2*60*60)
	got, err := c.Compile(ValueFromGo(time.Date(2024, 1, 15, 12, 30, 0, 500000000, loc)))
	if err != nil {
		t.Fatalf("compile error: %v", err)
	}
	want := "2024-01-15T10:30:00.5"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// TestFormatLiteral_TypeMismatchFallback is a regression test for issue #65:
// a mismatched value falls back to FormatGoValue instead of a zero value.
func TestFormatLiteral_TypeMismatchFallback(t *testing.T) {
	tests := []struct {
		name      string
		val       any
		valueType string
		want      string
	}{
		{"int as string keeps data", 42, "string", "42"},
		{"string as boolean keeps data", "true", "boolean", `"true"`},
		{"string as long keeps data", "abc", "long", `"abc"`},
		{"nil as string", nil, "string", "null"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := FormatLiteral(tt.val, tt.valueType)
			if got != tt.want {
				t.Errorf("FormatLiteral(%v, %q): got %q, want %q", tt.val, tt.valueType, got, tt.want)
			}
		})
	}
}

// TestCompile_LiteralTypeMismatchErrors is a regression test for issue #65:
// compiling a mismatched LiteralValue reports an error instead of silently
// emitting a zero value.
func TestCompile_LiteralTypeMismatchErrors(t *testing.T) {
	c := &Compiler{}
	tests := []struct {
		name string
		lit  LiteralValue
	}{
		{"int as string", Lit(42, "string")},
		{"string as boolean", Lit("true", "boolean")},
		{"string as long", Lit("abc", "long")},
		{"string as double", Lit("3.14", "double")},
		{"int as datetime", Lit(42, "datetime")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got, err := c.Compile(tt.lit); err == nil {
				t.Errorf("expected compile error, got %q", got)
			}
		})
	}
}

// TestFetchAttrPath_BareVariable is a regression test for issue #81: a path
// without a dot compiles to a plain variable fetch, not an invalid "$p."
func TestFetchAttrPath_BareVariable(t *testing.T) {
	c := &Compiler{}
	result, err := c.Compile(Fetch(FetchAttrPath("k", "$p")))
	if err != nil {
		t.Fatalf("compile error: %v", err)
	}
	if !strings.Contains(result, `"k": $p`) {
		t.Errorf("expected plain variable fetch: %s", result)
	}
	if strings.Contains(result, "$p.") {
		t.Errorf("unexpected trailing dot: %s", result)
	}
}

// TestFetchAttrPath_MultiDot verifies the path splits at the first dot, with
// no trailing dot in the output (issue #81).
func TestFetchAttrPath_MultiDot(t *testing.T) {
	fa := FetchAttrPath("k", "$p.a.b")
	if fa.Var != "$p" || fa.AttrName != "a.b" {
		t.Errorf("got Var=%q AttrName=%q, want Var=%q AttrName=%q", fa.Var, fa.AttrName, "$p", "a.b")
	}
}

// TestFetchKeys_Escaped is a regression test for issue #82: fetch keys are
// escaped when interpolated into quoted strings.
func TestFetchKeys_Escaped(t *testing.T) {
	c := &Compiler{}
	key := `weird"key\`
	wantKey := `"weird\"key\\":`

	items := []FetchItem{
		FetchAttr(key, "$p", "name"),
		FetchVar(key, "$p"),
		FetchAttributeList{Key: key, Var: "$p", AttrName: "name"},
		FetchFunc(key, "iid", "$p"),
		FetchWildcard{Key: key, Var: "$p"},
		FetchNestedWildcard{Key: key, Var: "$p"},
	}
	for _, item := range items {
		result, err := c.Compile(Fetch(item))
		if err != nil {
			t.Fatalf("compile error for %T: %v", item, err)
		}
		if !strings.Contains(result, wantKey) {
			t.Errorf("%T: key not escaped.\nwant substring: %s\ngot: %s", item, wantKey, result)
		}
	}
}

// TestEscapeString covers the single-pass escaper (issue #84).
func TestEscapeString(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"clean string unchanged", "hello world", "hello world"},
		{"all specials", "a\\b\"c\nd\re\tf", `a\\b\"c\nd\re\tf`},
		{"backslash before quote", `\"`, `\\\"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := EscapeString(tt.in); got != tt.want {
				t.Errorf("EscapeString(%q): got %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

// TestBuilders_OrPattern demonstrates building an or pattern.
func TestBuilders_OrPattern(t *testing.T) {
	// Build: match $p isa person;
	//        { $p has name "Alice"; } or { $p has name "Bob"; };
	query := Match(
		Entity("$p", "person"),
		Or(
			[]Pattern{HasPattern{ThingVar: "$p", AttrType: "name", AttrVar: "$n1"}},
			[]Pattern{HasPattern{ThingVar: "$p", AttrType: "name", AttrVar: "$n2"}},
		),
	)

	c := &Compiler{}
	result, err := c.Compile(query)
	if err != nil {
		t.Fatalf("compile error: %v", err)
	}

	if !strings.Contains(result, "or") {
		t.Errorf("Missing 'or' keyword: %s", result)
	}
}
