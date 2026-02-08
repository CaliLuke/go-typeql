package ast

import (
	"strings"
	"testing"
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
