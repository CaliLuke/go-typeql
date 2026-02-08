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

// setupReduceTestDB creates a test database for reduce/let testing
func setupReduceTestDB(t *testing.T) (*driver.Driver, string) {
	t.Helper()

	addr := os.Getenv("TEST_DB_ADDRESS")
	if addr == "" {
		addr = "localhost:1729"
	}

	drv, err := driver.Open(addr, "admin", "password")
	if err != nil {
		t.Skipf("TypeDB not available at %s: %v", addr, err)
	}

	sanitized := strings.NewReplacer("/", "_", " ", "_", ".", "_").Replace(t.Name())
	if len(sanitized) > 50 {
		sanitized = sanitized[:50]
	}
	dbName := fmt.Sprintf("go_ast_reduce_%s", strings.ToLower(sanitized))

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

// TestReduceCount verifies reduce with count aggregation
func TestReduceCount(t *testing.T) {
	drv, dbName := setupReduceTestDB(t)
	c := &ast.Compiler{}

	matchClause := ast.Match(
		ast.Entity("$p", "person"),
	)

	reduceClause := ast.ReduceClause{
		Assignments: []ast.ReduceAssignment{
			{
				Variable:   "$count",
				Expression: ast.FuncCall("count", "$p"),
			},
		},
	}

	query, err := c.CompileBatch([]ast.QueryNode{matchClause, reduceClause}, "")
	if err != nil {
		t.Fatalf("compile error: %v", err)
	}

	t.Logf("Reduce count query:\n%s", query)

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
		t.Fatalf("expected 1 result, got %d", len(results))
	}

	// TypeDB reduce returns nested structure
	result := results[0]

	// Try different possible result structures
	var count float64
	if countVal, ok := result["count"]; ok {
		switch v := countVal.(type) {
		case float64:
			count = v
		case int:
			count = float64(v)
		case map[string]any:
			// Nested structure like {"value": 3}
			if val, ok := v["value"].(float64); ok {
				count = val
			} else if val, ok := v["value"].(int); ok {
				count = float64(val)
			}
		}
	}

	if count != 3 {
		t.Errorf("expected count 3, got %v (full result: %+v)", count, result)
	}
}

// TestReduceWithStringExpression verifies reduce with raw string expression
func TestReduceWithStringExpression(t *testing.T) {
	drv, dbName := setupReduceTestDB(t)
	c := &ast.Compiler{}

	matchClause := ast.Match(
		ast.Entity("$p", "person"),
	)

	reduceClause := ast.ReduceClause{
		Assignments: []ast.ReduceAssignment{
			{
				Variable:   "$total",
				Expression: "count($p)", // Raw string instead of AST node
			},
		},
	}

	query, err := c.CompileBatch([]ast.QueryNode{matchClause, reduceClause}, "")
	if err != nil {
		t.Fatalf("compile error: %v", err)
	}

	t.Logf("Reduce string expression query:\n%s", query)

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
		t.Fatalf("expected 1 result, got %d", len(results))
	}

	// Extract count from result
	result := results[0]
	var count float64

	if totalVal, ok := result["total"]; ok {
		switch v := totalVal.(type) {
		case float64:
			count = v
		case int:
			count = float64(v)
		case map[string]any:
			if val, ok := v["value"].(float64); ok {
				count = val
			} else if val, ok := v["value"].(int); ok {
				count = float64(val)
			}
		}
	}

	if count != 3 {
		t.Errorf("expected total 3, got %v (full result: %+v)", count, result)
	}
}

// TestReduceMultipleAggregations verifies reduce with multiple aggregation functions
func TestReduceMultipleAggregations(t *testing.T) {
	drv, dbName := setupReduceTestDB(t)
	c := &ast.Compiler{}

	// Need to match name attribute to variable for aggregations
	matchClause := ast.Match(
		ast.Entity("$p", "person"),
		ast.HasPattern{ThingVar: "$p", AttrType: "name", AttrVar: "$n"},
	)

	reduceClause := ast.ReduceClause{
		Assignments: []ast.ReduceAssignment{
			{Variable: "$count", Expression: "count($p)"},
			{Variable: "$min_name", Expression: "min($n)"},
			{Variable: "$max_name", Expression: "max($n)"},
		},
	}

	query, err := c.CompileBatch([]ast.QueryNode{matchClause, reduceClause}, "")
	if err != nil {
		t.Fatalf("compile error: %v", err)
	}

	t.Logf("Multiple aggregations query:\n%s", query)

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
		t.Fatalf("expected 1 result, got %d", len(results))
	}

	result := results[0]
	t.Logf("Aggregation results: %+v", result)

	// Verify we got all three aggregations
	if _, ok := result["count"]; !ok {
		t.Error("missing 'count' in results")
	}
	if _, ok := result["min_name"]; !ok {
		t.Error("missing 'min_name' in results")
	}
	if _, ok := result["max_name"]; !ok {
		t.Error("missing 'max_name' in results")
	}
}

// TestReduceWithGroupBy verifies reduce with group by
func TestReduceWithGroupBy(t *testing.T) {
	drv, dbName := setupReduceTestDB(t)
	c := &ast.Compiler{}

	// Insert additional data with a groupable attribute
	tx, err := drv.Transaction(dbName, driver.Write)
	if err != nil {
		t.Fatalf("starting write transaction: %v", err)
	}

	// For grouping, we need an attribute that varies - let's use email domain
	// But first let's just verify the syntax compiles
	tx.Close()

	matchClause := ast.Match(
		ast.Entity("$p", "person"),
		ast.HasPattern{ThingVar: "$p", AttrType: "name", AttrVar: "$n"},
	)

	reduceClause := ast.ReduceClause{
		Assignments: []ast.ReduceAssignment{
			{Variable: "$count", Expression: "count($p)"},
		},
		GroupBy: "$n", // Group by name (each person has unique name, so 3 groups)
	}

	query, err := c.CompileBatch([]ast.QueryNode{matchClause, reduceClause}, "")
	if err != nil {
		t.Fatalf("compile error: %v", err)
	}

	t.Logf("GroupBy query:\n%s", query)

	// Verify syntax
	if !strings.Contains(query, "groupby $n") {
		t.Errorf("expected query to contain 'groupby $n', got:\n%s", query)
	}

	tx, err = drv.Transaction(dbName, driver.Read)
	if err != nil {
		t.Fatalf("starting read transaction: %v", err)
	}
	defer tx.Close()

	results, err := tx.Query(query)
	if err != nil {
		t.Fatalf("query execution failed: %v\nquery:\n%s", err, query)
	}

	// Should have 3 groups (one per person)
	if len(results) != 3 {
		t.Errorf("expected 3 groups, got %d", len(results))
	}
}

// TestCompileBatch verifies compile_batch method
func TestCompileBatch(t *testing.T) {
	drv, dbName := setupReduceTestDB(t)
	c := &ast.Compiler{}

	// Build multi-clause query
	matchClause := ast.Match(
		ast.Entity("$p", "person", ast.Has("name", ast.Str("Alice"))),
	)

	fetchClause := ast.Fetch(
		ast.FetchAttr("name", "$p", "name"),
		ast.FetchAttr("email", "$p", "email"),
	)

	// Use compile_batch to combine
	query, err := c.CompileBatch([]ast.QueryNode{matchClause, fetchClause}, "")
	if err != nil {
		t.Fatalf("compile error: %v", err)
	}

	t.Logf("Batch compiled query:\n%s", query)

	// Verify both clauses are present
	if !strings.Contains(query, "match") {
		t.Error("batch query missing 'match' clause")
	}
	if !strings.Contains(query, "fetch") {
		t.Error("batch query missing 'fetch' clause")
	}

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
		t.Fatalf("expected 1 result, got %d", len(results))
	}

	if name, ok := results[0]["name"].(string); !ok || name != "Alice" {
		t.Errorf("expected name 'Alice', got %v", results[0]["name"])
	}
}

// TestCompileBatchWithSeparator verifies compile_batch with custom separator
func TestCompileBatchWithSeparator(t *testing.T) {
	c := &ast.Compiler{}

	matchClause := ast.Match(
		ast.Entity("$p", "person"),
	)

	fetchClause := ast.Fetch(
		ast.FetchAttr("name", "$p", "name"),
	)

	// Test with default separator (newline)
	queryNewline, err := c.CompileBatch([]ast.QueryNode{matchClause, fetchClause}, "")
	if err != nil {
		t.Fatalf("compile error: %v", err)
	}

	// Test with custom separator (double newline)
	queryDouble, err := c.CompileBatch([]ast.QueryNode{matchClause, fetchClause}, "\n\n")
	if err != nil {
		t.Fatalf("compile error: %v", err)
	}

	t.Logf("Default separator query:\n%s", queryNewline)
	t.Logf("Double newline separator query:\n%s", queryDouble)

	// Verify the separator is used between clauses
	// Default should have "match\n...\nfetch" (clauses on separate lines)
	// Double should have "match\n...\n\nfetch" (extra blank line)
	if !strings.Contains(queryNewline, ";\n") {
		t.Error("expected newline separator between clauses")
	}

	// Double newline should have more newlines than default
	if strings.Count(queryDouble, "\n") <= strings.Count(queryNewline, "\n") {
		t.Error("expected more newlines with double separator")
	}
}
