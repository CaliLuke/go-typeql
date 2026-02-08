//go:build integration && cgo && typedb

package ast_test

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/CaliLuke/go-typeql/ast"
	"github.com/CaliLuke/go-typeql/driver"
	"github.com/CaliLuke/go-typeql/gotype"
)

// setupPatternsTestDB creates a test database with Person entities for pattern testing
func setupPatternsTestDB(t *testing.T) (*driver.Driver, string) {
	t.Helper()

	addr := os.Getenv("TEST_DB_ADDRESS")
	if addr == "" {
		addr = "localhost:1729"
	}

	drv, err := driver.Open(addr, "admin", "password")
	if err != nil {
		t.Skipf("TypeDB not available at %s: %v", addr, err)
	}

	// Unique DB name per test
	sanitized := strings.NewReplacer("/", "_", " ", "_", ".", "_").Replace(t.Name())
	if len(sanitized) > 50 {
		sanitized = sanitized[:50]
	}
	dbName := fmt.Sprintf("go_ast_patterns_%s", strings.ToLower(sanitized))

	dbm := drv.Databases()

	exists, err := dbm.Contains(dbName)
	if err != nil {
		drv.Close()
		t.Fatalf("checking database existence: %v", err)
	}
	if exists {
		if err := dbm.Delete(dbName); err != nil {
			drv.Close()
			t.Fatalf("deleting old test database: %v", err)
		}
	}

	if err := dbm.Create(dbName); err != nil {
		drv.Close()
		t.Fatalf("creating test database: %v", err)
	}

	// Register types and create schema
	gotype.ClearRegistry()
	_ = gotype.Register[Person]()
	_ = gotype.Register[Company]()
	_ = gotype.Register[Employment]()

	schema := gotype.GenerateSchema()

	tx, err := drv.Transaction(dbName, driver.Schema)
	if err != nil {
		drv.Close()
		t.Fatalf("starting schema transaction: %v", err)
	}

	if _, err := tx.Query(schema); err != nil {
		tx.Close()
		drv.Close()
		t.Fatalf("defining schema: %v\nschema:\n%s", err, schema)
	}

	if err := tx.Commit(); err != nil {
		drv.Close()
		t.Fatalf("committing schema: %v", err)
	}

	// Insert test data
	tx, err = drv.Transaction(dbName, driver.Write)
	if err != nil {
		drv.Close()
		t.Fatalf("starting write transaction: %v", err)
	}

	insertQuery := `
		insert
		$alice isa person, has name "Alice", has email "alice@test.com";
		$bob isa person, has name "Bob", has email "bob@test.com";
		$charlie isa person, has name "Charlie", has email "charlie@test.com";
		$acme isa company, has name "Acme Corp";
		(employee: $alice, employer: $acme) isa employment;
	`

	if _, err := tx.Query(insertQuery); err != nil {
		tx.Close()
		drv.Close()
		t.Fatalf("inserting test data: %v", err)
	}

	if err := tx.Commit(); err != nil {
		drv.Close()
		t.Fatalf("committing insert: %v", err)
	}

	t.Cleanup(func() {
		_ = dbm.Delete(dbName)
		drv.Close()
	})

	return drv, dbName
}

// TestNotPattern verifies Not pattern compilation and execution
func TestNotPattern(t *testing.T) {
	drv, dbName := setupPatternsTestDB(t)
	c := &ast.Compiler{}

	// Find persons who are NOT named Alice
	matchClause := ast.Match(
		ast.Entity("$p", "person"),
		ast.NotPattern{
			Patterns: []ast.Pattern{
				ast.HasPattern{ThingVar: "$p", AttrType: "name", AttrVar: "$n"},
				ast.ValueComparisonPattern{Var: "$n", Operator: "==", Value: ast.Str("Alice")},
			},
		},
	)

	fetchClause := ast.Fetch(
		ast.FetchAttr("name", "$p", "name"),
	)

	query, err := c.CompileBatch([]ast.QueryNode{matchClause, fetchClause}, "")
	if err != nil {
		t.Fatalf("compile error: %v", err)
	}

	t.Logf("Not pattern query:\n%s", query)

	tx, err := drv.Transaction(dbName, driver.Read)
	if err != nil {
		t.Fatalf("starting read transaction: %v", err)
	}
	defer tx.Close()

	results, err := tx.Query(query)
	if err != nil {
		t.Fatalf("query execution failed: %v\nquery:\n%s", err, query)
	}

	// Should get Bob and Charlie, not Alice
	if len(results) != 2 {
		t.Errorf("expected 2 results, got %d", len(results))
	}

	for _, r := range results {
		if name, ok := r["name"].(string); ok {
			if name == "Alice" {
				t.Error("Alice should be excluded by Not pattern")
			}
			if name != "Bob" && name != "Charlie" {
				t.Errorf("unexpected name: %s", name)
			}
		}
	}
}

// TestOrPattern verifies Or pattern compilation and execution
func TestOrPattern(t *testing.T) {
	drv, dbName := setupPatternsTestDB(t)
	c := &ast.Compiler{}

	// Find persons named Alice OR Bob
	matchClause := ast.Match(
		ast.Entity("$p", "person"),
		ast.HasPattern{ThingVar: "$p", AttrType: "name", AttrVar: "$n"},
		ast.OrPattern{
			Alternatives: [][]ast.Pattern{
				{ast.ValueComparisonPattern{Var: "$n", Operator: "==", Value: ast.Str("Alice")}},
				{ast.ValueComparisonPattern{Var: "$n", Operator: "==", Value: ast.Str("Bob")}},
			},
		},
	)

	fetchClause := ast.Fetch(
		ast.FetchVar("name", "$n"),
	)

	query, err := c.CompileBatch([]ast.QueryNode{matchClause, fetchClause}, "")
	if err != nil {
		t.Fatalf("compile error: %v", err)
	}

	t.Logf("Or pattern query:\n%s", query)

	tx, err := drv.Transaction(dbName, driver.Read)
	if err != nil {
		t.Fatalf("starting read transaction: %v", err)
	}
	defer tx.Close()

	results, err := tx.Query(query)
	if err != nil {
		t.Fatalf("query execution failed: %v\nquery:\n%s", err, query)
	}

	// Should get Alice and Bob
	if len(results) != 2 {
		t.Errorf("expected 2 results, got %d", len(results))
	}

	names := make(map[string]bool)
	for _, r := range results {
		if name, ok := r["name"].(string); ok {
			names[name] = true
		}
	}

	if !names["Alice"] || !names["Bob"] {
		t.Errorf("expected Alice and Bob, got: %v", names)
	}
	if names["Charlie"] {
		t.Error("Charlie should not be in results")
	}
}

// TestValueComparisonPattern verifies value comparison patterns
func TestValueComparisonPattern(t *testing.T) {
	drv, dbName := setupPatternsTestDB(t)
	c := &ast.Compiler{}

	tests := []struct {
		name     string
		operator string
		value    ast.Value
		wantLen  int
	}{
		{
			name:     "equals",
			operator: "==",
			value:    ast.Str("Alice"),
			wantLen:  1,
		},
		{
			name:     "not equals",
			operator: "!=",
			value:    ast.Str("Alice"),
			wantLen:  2, // Bob and Charlie
		},
		{
			name:     "contains",
			operator: "contains",
			value:    ast.Str("li"), // Alice and Charlie contain "li"
			wantLen:  2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			matchClause := ast.Match(
				ast.Entity("$p", "person"),
				ast.HasPattern{ThingVar: "$p", AttrType: "name", AttrVar: "$n"},
				ast.ValueComparisonPattern{Var: "$n", Operator: tt.operator, Value: tt.value},
			)

			fetchClause := ast.Fetch(
				ast.FetchVar("name", "$n"),
			)

			query, err := c.CompileBatch([]ast.QueryNode{matchClause, fetchClause}, "")
			if err != nil {
				t.Fatalf("compile error: %v", err)
			}

			t.Logf("Query:\n%s", query)

			tx, err := drv.Transaction(dbName, driver.Read)
			if err != nil {
				t.Fatalf("starting read transaction: %v", err)
			}
			defer tx.Close()

			results, err := tx.Query(query)
			if err != nil {
				t.Fatalf("query execution failed: %v\nquery:\n%s", err, query)
			}

			if len(results) != tt.wantLen {
				t.Errorf("expected %d results, got %d: %+v", tt.wantLen, len(results), results)
			}
		})
	}
}

// TestIidConstraint verifies IID constraint patterns
func TestIidConstraint(t *testing.T) {
	drv, dbName := setupPatternsTestDB(t)
	c := &ast.Compiler{}

	// First get Alice's IID
	tx, err := drv.Transaction(dbName, driver.Read)
	if err != nil {
		t.Fatalf("starting read transaction: %v", err)
	}

	getIIDQuery := `
		match $p isa person, has name "Alice";
		fetch { "iid": iid($p) };
	`

	results, err := tx.Query(getIIDQuery)
	tx.Close()
	if err != nil {
		t.Fatalf("getting Alice's IID failed: %v", err)
	}

	if len(results) != 1 {
		t.Fatalf("expected 1 result for Alice, got %d", len(results))
	}

	aliceIID, ok := results[0]["iid"].(string)
	if !ok || !strings.HasPrefix(aliceIID, "0x") {
		t.Fatalf("invalid IID format: %v", results[0]["iid"])
	}

	t.Logf("Alice's IID: %s", aliceIID)

	// Now match by IID using EntityPattern with IidConstraint
	matchClause := ast.Match(
		ast.Entity("$p", "person", ast.IidConstraint{IID: aliceIID}),
	)

	fetchClause := ast.Fetch(
		ast.FetchAttr("name", "$p", "name"),
	)

	query, err := c.CompileBatch([]ast.QueryNode{matchClause, fetchClause}, "")
	if err != nil {
		t.Fatalf("compile error: %v", err)
	}

	t.Logf("IID constraint query:\n%s", query)

	tx, err = drv.Transaction(dbName, driver.Read)
	if err != nil {
		t.Fatalf("starting read transaction: %v", err)
	}
	defer tx.Close()

	results, err = tx.Query(query)
	if err != nil {
		t.Fatalf("query execution failed: %v\nquery:\n%s", err, query)
	}

	if len(results) != 1 {
		t.Errorf("expected 1 result, got %d", len(results))
	}

	if name, ok := results[0]["name"].(string); !ok || name != "Alice" {
		t.Errorf("expected name 'Alice', got %v", results[0]["name"])
	}
}

// TestRawPattern verifies raw pattern passthrough
func TestRawPattern(t *testing.T) {
	drv, dbName := setupPatternsTestDB(t)
	c := &ast.Compiler{}

	matchClause := ast.Match(
		ast.RawPattern{Content: `$p isa person, has name "Alice"`},
	)

	fetchClause := ast.Fetch(
		ast.FetchAttr("name", "$p", "name"),
	)

	query, err := c.CompileBatch([]ast.QueryNode{matchClause, fetchClause}, "")
	if err != nil {
		t.Fatalf("compile error: %v", err)
	}

	t.Logf("Raw pattern query:\n%s", query)

	tx, err := drv.Transaction(dbName, driver.Read)
	if err != nil {
		t.Fatalf("starting read transaction: %v", err)
	}
	defer tx.Close()

	results, err := tx.Query(query)
	if err != nil {
		t.Fatalf("query execution failed: %v\nquery:\n%s", err, query)
	}

	if len(results) != 1 {
		t.Errorf("expected 1 result, got %d", len(results))
	}

	if name, ok := results[0]["name"].(string); !ok || name != "Alice" {
		t.Errorf("expected name 'Alice', got %v", results[0]["name"])
	}
}

// TestStrictIsaPattern verifies strict isa (isa!) vs regular isa
func TestStrictIsaPattern(t *testing.T) {
	drv, dbName := setupPatternsTestDB(t)
	c := &ast.Compiler{}

	tests := []struct {
		name     string
		isStrict bool
		wantType string
	}{
		{
			name:     "regular isa",
			isStrict: false,
			wantType: "isa",
		},
		{
			name:     "strict isa",
			isStrict: true,
			wantType: "isa!",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			matchClause := ast.Match(
				ast.EntityPattern{
					Variable: "$p",
					TypeName: "person",
					IsStrict: tt.isStrict,
				},
			)

			fetchClause := ast.Fetch(
				ast.FetchAttr("name", "$p", "name"),
			)

			query, err := c.CompileBatch([]ast.QueryNode{matchClause, fetchClause}, "")
			if err != nil {
				t.Fatalf("compile error: %v", err)
			}

			// Verify the query contains the correct isa type
			if !strings.Contains(query, tt.wantType) {
				t.Errorf("expected query to contain %q, got:\n%s", tt.wantType, query)
			}

			t.Logf("Query:\n%s", query)

			tx, err := drv.Transaction(dbName, driver.Read)
			if err != nil {
				t.Fatalf("starting read transaction: %v", err)
			}
			defer tx.Close()

			results, err := tx.Query(query)
			if err != nil {
				t.Fatalf("query execution failed: %v\nquery:\n%s", err, query)
			}

			// Both should return 3 persons (Alice, Bob, Charlie)
			if len(results) != 3 {
				t.Errorf("expected 3 results, got %d", len(results))
			}
		})
	}
}

// TestHasPattern verifies HasPattern compilation
func TestHasPattern(t *testing.T) {
	drv, dbName := setupPatternsTestDB(t)
	c := &ast.Compiler{}

	matchClause := ast.Match(
		ast.Entity("$p", "person"),
		ast.HasPattern{ThingVar: "$p", AttrType: "name", AttrVar: "$n"},
	)

	fetchClause := ast.Fetch(
		ast.FetchVar("name", "$n"),
	)

	query, err := c.CompileBatch([]ast.QueryNode{matchClause, fetchClause}, "")
	if err != nil {
		t.Fatalf("compile error: %v", err)
	}

	t.Logf("HasPattern query:\n%s", query)

	tx, err := drv.Transaction(dbName, driver.Read)
	if err != nil {
		t.Fatalf("starting read transaction: %v", err)
	}
	defer tx.Close()

	results, err := tx.Query(query)
	if err != nil {
		t.Fatalf("query execution failed: %v\nquery:\n%s", err, query)
	}

	if len(results) != 3 {
		t.Errorf("expected 3 results, got %d", len(results))
	}

	names := make(map[string]bool)
	for _, r := range results {
		if name, ok := r["name"].(string); ok {
			names[name] = true
		}
	}

	expectedNames := []string{"Alice", "Bob", "Charlie"}
	for _, name := range expectedNames {
		if !names[name] {
			t.Errorf("expected to find %s in results", name)
		}
	}
}
