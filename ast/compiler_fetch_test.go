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

// setupFetchTestDB creates a test database with data for fetch testing
func setupFetchTestDB(t *testing.T) (*driver.Driver, string) {
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
	dbName := fmt.Sprintf("go_ast_fetch_%s", strings.ToLower(sanitized))

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

// TestFetchAttribute verifies FetchAttribute compilation
func TestFetchAttribute(t *testing.T) {
	drv, dbName := setupFetchTestDB(t)
	c := &ast.Compiler{}

	matchClause := ast.Match(
		ast.Entity("$p", "person", ast.Has("name", ast.Str("Alice"))),
	)

	fetchClause := ast.Fetch(
		ast.FetchAttr("person_name", "$p", "name"),
		ast.FetchAttr("person_email", "$p", "email"),
	)

	query, err := c.CompileBatch([]ast.QueryNode{matchClause, fetchClause}, "")
	if err != nil {
		t.Fatalf("compile error: %v", err)
	}

	t.Logf("FetchAttribute query:\n%s", query)

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

	if name, ok := results[0]["person_name"].(string); !ok || name != "Alice" {
		t.Errorf("expected person_name 'Alice', got %v", results[0]["person_name"])
	}

	if email, ok := results[0]["person_email"].(string); !ok || email != "alice@test.com" {
		t.Errorf("expected person_email 'alice@test.com', got %v", results[0]["person_email"])
	}
}

// TestFetchVariable verifies FetchVariable compilation
func TestFetchVariable(t *testing.T) {
	drv, dbName := setupFetchTestDB(t)
	c := &ast.Compiler{}

	matchClause := ast.Match(
		ast.Entity("$p", "person"),
		ast.HasPattern{ThingVar: "$p", AttrType: "name", AttrVar: "$n"},
	)

	fetchClause := ast.Fetch(
		ast.FetchVar("person_name", "$n"),
	)

	query, err := c.CompileBatch([]ast.QueryNode{matchClause, fetchClause}, "")
	if err != nil {
		t.Fatalf("compile error: %v", err)
	}

	t.Logf("FetchVariable query:\n%s", query)

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

	if name, ok := results[0]["person_name"].(string); !ok || name != "Alice" {
		t.Errorf("expected person_name 'Alice', got %v", results[0]["person_name"])
	}
}

// TestFetchFunction verifies FetchFunction compilation
func TestFetchFunction(t *testing.T) {
	drv, dbName := setupFetchTestDB(t)
	c := &ast.Compiler{}

	matchClause := ast.Match(
		ast.Entity("$p", "person", ast.Has("name", ast.Str("Alice"))),
	)

	fetchClause := ast.Fetch(
		ast.FetchFunc("person_iid", "iid", "$p"),
	)

	query, err := c.CompileBatch([]ast.QueryNode{matchClause, fetchClause}, "")
	if err != nil {
		t.Fatalf("compile error: %v", err)
	}

	t.Logf("FetchFunction query:\n%s", query)

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

	if iid, ok := results[0]["person_iid"].(string); !ok || !strings.HasPrefix(iid, "0x") {
		t.Errorf("expected IID starting with '0x', got %v", results[0]["person_iid"])
	}
}

// TestFetchNestedWildcard verifies FetchNestedWildcard compilation
func TestFetchNestedWildcard(t *testing.T) {
	drv, dbName := setupFetchTestDB(t)
	c := &ast.Compiler{}

	matchClause := ast.Match(
		ast.Entity("$p", "person", ast.Has("name", ast.Str("Alice"))),
	)

	fetchClause := ast.Fetch(
		ast.FetchNestedWildcard{Key: "all_attrs", Var: "$p"},
	)

	query, err := c.CompileBatch([]ast.QueryNode{matchClause, fetchClause}, "")
	if err != nil {
		t.Fatalf("compile error: %v", err)
	}

	t.Logf("FetchNestedWildcard query:\n%s", query)

	// Verify syntax contains braces
	if !strings.Contains(query, "{ $p.* }") {
		t.Errorf("expected query to contain '{ $p.* }', got:\n%s", query)
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

	// Verify nested structure
	if allAttrs, ok := results[0]["all_attrs"].(map[string]any); ok {
		if name, ok := allAttrs["name"].(string); !ok || name != "Alice" {
			t.Errorf("expected name 'Alice' in nested attrs, got %v", allAttrs["name"])
		}
		if email, ok := allAttrs["email"].(string); !ok || email != "alice@test.com" {
			t.Errorf("expected email 'alice@test.com' in nested attrs, got %v", allAttrs["email"])
		}
	} else {
		t.Errorf("expected all_attrs to be map[string]any, got %T: %v", results[0]["all_attrs"], results[0]["all_attrs"])
	}
}

// TestFetchRawString verifies raw string fetch items
func TestFetchRawString(t *testing.T) {
	drv, dbName := setupFetchTestDB(t)
	c := &ast.Compiler{}

	matchClause := ast.Match(
		ast.Entity("$p", "person", ast.Has("name", ast.Str("Alice"))),
	)

	// Use raw string in fetch
	fetchClause := ast.FetchClause{
		Items: []any{
			`"custom_name": $p.name`,
			`"custom_email": $p.email`,
		},
	}

	query, err := c.CompileBatch([]ast.QueryNode{matchClause, fetchClause}, "")
	if err != nil {
		t.Fatalf("compile error: %v", err)
	}

	t.Logf("Raw string fetch query:\n%s", query)

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

	if name, ok := results[0]["custom_name"].(string); !ok || name != "Alice" {
		t.Errorf("expected custom_name 'Alice', got %v", results[0]["custom_name"])
	}
}

// TestMultipleFetchItems verifies multiple different fetch item types in one query
func TestMultipleFetchItems(t *testing.T) {
	drv, dbName := setupFetchTestDB(t)
	c := &ast.Compiler{}

	matchClause := ast.Match(
		ast.Entity("$p", "person", ast.Has("name", ast.Str("Alice"))),
		ast.HasPattern{ThingVar: "$p", AttrType: "email", AttrVar: "$e"},
	)

	fetchClause := ast.Fetch(
		ast.FetchAttr("attr_name", "$p", "name"),       // FetchAttribute
		ast.FetchVar("var_email", "$e"),                // FetchVariable
		ast.FetchFunc("func_iid", "iid", "$p"),         // FetchFunction
		ast.FetchNestedWildcard{Key: "all", Var: "$p"}, // FetchNestedWildcard
	)

	query, err := c.CompileBatch([]ast.QueryNode{matchClause, fetchClause}, "")
	if err != nil {
		t.Fatalf("compile error: %v", err)
	}

	t.Logf("Multiple fetch items query:\n%s", query)

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

	// Verify each fetch type
	if name, ok := result["attr_name"].(string); !ok || name != "Alice" {
		t.Errorf("FetchAttribute failed: expected 'Alice', got %v", result["attr_name"])
	}

	if email, ok := result["var_email"].(string); !ok || email != "alice@test.com" {
		t.Errorf("FetchVariable failed: expected 'alice@test.com', got %v", result["var_email"])
	}

	if iid, ok := result["func_iid"].(string); !ok || !strings.HasPrefix(iid, "0x") {
		t.Errorf("FetchFunction failed: expected IID with '0x', got %v", result["func_iid"])
	}

	if allAttrs, ok := result["all"].(map[string]any); !ok {
		t.Errorf("FetchNestedWildcard failed: expected map, got %T", result["all"])
	} else if name, ok := allAttrs["name"].(string); !ok || name != "Alice" {
		t.Errorf("FetchNestedWildcard failed: expected nested name 'Alice', got %v", allAttrs["name"])
	}
}
