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

// setupMutationsTestDB creates a minimal test database for mutation testing
func setupMutationsTestDB(t *testing.T) (*driver.Driver, string) {
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
	dbName := fmt.Sprintf("go_ast_mutations_%s", strings.ToLower(sanitized))

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

	t.Cleanup(func() {
		_ = dbm.Delete(dbName)
		drv.Close()
	})

	return drv, dbName
}

// TestInsertEntity verifies InsertClause compilation and execution
func TestInsertEntity(t *testing.T) {
	drv, dbName := setupMutationsTestDB(t)
	c := &ast.Compiler{}

	// Build insert query using AST
	insertClause := ast.Insert(
		ast.IsaStmt("$p", "person"),
		ast.HasStmt("$p", "name", ast.Str("David")),
		ast.HasStmt("$p", "email", ast.Str("david@test.com")),
	)

	query, err := c.Compile(insertClause)
	if err != nil {
		t.Fatalf("compile error: %v", err)
	}

	t.Logf("Insert query:\n%s", query)

	// Execute insert
	tx, err := drv.Transaction(dbName, driver.Write)
	if err != nil {
		t.Fatalf("starting write transaction: %v", err)
	}

	if _, err := tx.Query(query); err != nil {
		tx.Close()
		t.Fatalf("insert execution failed: %v\nquery:\n%s", err, query)
	}

	if err := tx.Commit(); err != nil {
		t.Fatalf("commit failed: %v", err)
	}

	// Verify insert with fetch
	verifyQuery := `
		match $p isa person, has name "David";
		fetch { "name": $p.name, "email": $p.email };
	`

	tx, err = drv.Transaction(dbName, driver.Read)
	if err != nil {
		t.Fatalf("starting read transaction: %v", err)
	}
	defer tx.Close()

	results, err := tx.Query(verifyQuery)
	if err != nil {
		t.Fatalf("verify query failed: %v", err)
	}

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}

	if name, ok := results[0]["name"].(string); !ok || name != "David" {
		t.Errorf("expected name 'David', got %v", results[0]["name"])
	}
	if email, ok := results[0]["email"].(string); !ok || email != "david@test.com" {
		t.Errorf("expected email 'david@test.com', got %v", results[0]["email"])
	}
}

// TestInsertRelationWithVariable verifies relation insertion with variable
func TestInsertRelationWithVariable(t *testing.T) {
	drv, dbName := setupMutationsTestDB(t)
	c := &ast.Compiler{}

	// First insert two persons
	insertPersons := `
		insert
		$alice isa person, has name "Alice", has email "alice@test.com";
		$bob isa person, has name "Bob", has email "bob@test.com";
	`

	tx, err := drv.Transaction(dbName, driver.Write)
	if err != nil {
		t.Fatalf("starting write transaction: %v", err)
	}

	if _, err := tx.Query(insertPersons); err != nil {
		tx.Close()
		t.Fatalf("inserting persons failed: %v", err)
	}

	if err := tx.Commit(); err != nil {
		t.Fatalf("commit failed: %v", err)
	}

	// Insert company
	insertCompany := `
		insert
		$acme isa company, has name "Acme Corp";
	`

	tx, err = drv.Transaction(dbName, driver.Write)
	if err != nil {
		t.Fatalf("starting write transaction: %v", err)
	}

	if _, err := tx.Query(insertCompany); err != nil {
		tx.Close()
		t.Fatalf("inserting company failed: %v", err)
	}

	if err := tx.Commit(); err != nil {
		t.Fatalf("commit failed: %v", err)
	}

	// Now insert employment relation using AST
	matchClause := ast.Match(
		ast.Entity("$alice", "person", ast.Has("name", ast.Str("Alice"))),
		ast.Entity("$acme", "company", ast.Has("name", ast.Str("Acme Corp"))),
	)

	insertClause := ast.Insert(
		ast.RelationStatement{
			Variable:        "$emp",
			TypeName:        "employment",
			IncludeVariable: true,
			RolePlayers: []ast.RolePlayer{
				{Role: "employee", PlayerVar: "$alice"},
				{Role: "employer", PlayerVar: "$acme"},
			},
		},
	)

	query, err := c.CompileBatch([]ast.QueryNode{matchClause, insertClause}, "")
	if err != nil {
		t.Fatalf("compile error: %v", err)
	}

	t.Logf("Insert relation query:\n%s", query)

	tx, err = drv.Transaction(dbName, driver.Write)
	if err != nil {
		t.Fatalf("starting write transaction: %v", err)
	}

	if _, err := tx.Query(query); err != nil {
		tx.Close()
		t.Fatalf("insert relation failed: %v\nquery:\n%s", err, query)
	}

	if err := tx.Commit(); err != nil {
		t.Fatalf("commit failed: %v", err)
	}

	// Verify employment exists
	verifyQuery := `
		match $e isa employment;
		fetch { "iid": iid($e) };
	`

	tx, err = drv.Transaction(dbName, driver.Read)
	if err != nil {
		t.Fatalf("starting read transaction: %v", err)
	}
	defer tx.Close()

	results, err := tx.Query(verifyQuery)
	if err != nil {
		t.Fatalf("verify query failed: %v", err)
	}

	if len(results) != 1 {
		t.Errorf("expected 1 employment relation, got %d", len(results))
	}
}

// TestInsertRelationWithoutVariable verifies relation insertion without variable (inline)
func TestInsertRelationWithoutVariable(t *testing.T) {
	drv, dbName := setupMutationsTestDB(t)
	c := &ast.Compiler{}

	// First insert persons
	insertPersons := `
		insert
		$alice isa person, has name "Alice", has email "alice@test.com";
		$bob isa person, has name "Bob", has email "bob@test.com";
	`

	tx, err := drv.Transaction(dbName, driver.Write)
	if err != nil {
		t.Fatalf("starting write transaction: %v", err)
	}

	if _, err := tx.Query(insertPersons); err != nil {
		tx.Close()
		t.Fatalf("inserting persons failed: %v", err)
	}

	if err := tx.Commit(); err != nil {
		t.Fatalf("commit failed: %v", err)
	}

	// Insert company
	insertCompany := `
		insert
		$acme isa company, has name "Acme Corp";
	`

	tx, err = drv.Transaction(dbName, driver.Write)
	if err != nil {
		t.Fatalf("starting write transaction: %v", err)
	}

	if _, err := tx.Query(insertCompany); err != nil {
		tx.Close()
		t.Fatalf("inserting company failed: %v", err)
	}

	if err := tx.Commit(); err != nil {
		t.Fatalf("commit failed: %v", err)
	}

	// Now insert employment using AST with include_variable=false (inline style)
	matchClause := ast.Match(
		ast.Entity("$alice", "person", ast.Has("name", ast.Str("Alice"))),
		ast.Entity("$acme", "company", ast.Has("name", ast.Str("Acme Corp"))),
	)

	insertClause := ast.Insert(
		ast.RelationStatement{
			Variable:        "$emp", // Not used when IncludeVariable=false
			TypeName:        "employment",
			IncludeVariable: false,
			RolePlayers: []ast.RolePlayer{
				{Role: "employee", PlayerVar: "$alice"},
				{Role: "employer", PlayerVar: "$acme"},
			},
		},
	)

	query, err := c.CompileBatch([]ast.QueryNode{matchClause, insertClause}, "")
	if err != nil {
		t.Fatalf("compile error: %v", err)
	}

	t.Logf("Insert relation (inline) query:\n%s", query)

	// Verify the query uses inline syntax (no $emp prefix)
	if strings.Contains(query, "$emp isa employment") {
		t.Error("query should not contain variable prefix when IncludeVariable=false")
	}

	tx, err = drv.Transaction(dbName, driver.Write)
	if err != nil {
		t.Fatalf("starting write transaction: %v", err)
	}

	if _, err := tx.Query(query); err != nil {
		tx.Close()
		t.Fatalf("insert relation failed: %v\nquery:\n%s", err, query)
	}

	if err := tx.Commit(); err != nil {
		t.Fatalf("commit failed: %v", err)
	}

	// Verify employment exists
	verifyQuery := `
		match $e isa employment;
		fetch { "iid": iid($e) };
	`

	tx, err = drv.Transaction(dbName, driver.Read)
	if err != nil {
		t.Fatalf("starting read transaction: %v", err)
	}
	defer tx.Close()

	results, err := tx.Query(verifyQuery)
	if err != nil {
		t.Fatalf("verify query failed: %v", err)
	}

	if len(results) != 1 {
		t.Errorf("expected 1 employment relation, got %d", len(results))
	}
}

// TestDeleteEntity verifies DeleteClause compilation and execution
func TestDeleteEntity(t *testing.T) {
	drv, dbName := setupMutationsTestDB(t)
	c := &ast.Compiler{}

	// First insert a person to delete
	insertQuery := `
		insert
		$charlie isa person, has name "Charlie", has email "charlie@test.com";
	`

	tx, err := drv.Transaction(dbName, driver.Write)
	if err != nil {
		t.Fatalf("starting write transaction: %v", err)
	}

	if _, err := tx.Query(insertQuery); err != nil {
		tx.Close()
		t.Fatalf("inserting person failed: %v", err)
	}

	if err := tx.Commit(); err != nil {
		t.Fatalf("commit failed: %v", err)
	}

	// Now delete Charlie using AST
	matchClause := ast.Match(
		ast.Entity("$p", "person", ast.Has("name", ast.Str("Charlie"))),
	)

	deleteClause := ast.Delete(
		ast.DeleteThingStatement{Variable: "$p"},
	)

	query, err := c.CompileBatch([]ast.QueryNode{matchClause, deleteClause}, "")
	if err != nil {
		t.Fatalf("compile error: %v", err)
	}

	t.Logf("Delete query:\n%s", query)

	tx, err = drv.Transaction(dbName, driver.Write)
	if err != nil {
		t.Fatalf("starting write transaction: %v", err)
	}

	if _, err := tx.Query(query); err != nil {
		tx.Close()
		t.Fatalf("delete execution failed: %v\nquery:\n%s", err, query)
	}

	if err := tx.Commit(); err != nil {
		t.Fatalf("commit failed: %v", err)
	}

	// Verify deletion
	verifyQuery := `
		match $p isa person, has name "Charlie";
		fetch { "name": $p.name };
	`

	tx, err = drv.Transaction(dbName, driver.Read)
	if err != nil {
		t.Fatalf("starting read transaction: %v", err)
	}
	defer tx.Close()

	results, err := tx.Query(verifyQuery)
	if err != nil {
		t.Fatalf("verify query failed: %v", err)
	}

	if len(results) != 0 {
		t.Errorf("expected 0 results after deletion, got %d", len(results))
	}
}

// TestUpdatePattern verifies match + delete + insert pattern for updates
func TestUpdatePattern(t *testing.T) {
	drv, dbName := setupMutationsTestDB(t)
	c := &ast.Compiler{}

	// Insert a person
	insertQuery := `
		insert
		$eve isa person, has name "Eve", has email "eve@old.com";
	`

	tx, err := drv.Transaction(dbName, driver.Write)
	if err != nil {
		t.Fatalf("starting write transaction: %v", err)
	}

	if _, err := tx.Query(insertQuery); err != nil {
		tx.Close()
		t.Fatalf("inserting person failed: %v", err)
	}

	if err := tx.Commit(); err != nil {
		t.Fatalf("commit failed: %v", err)
	}

	// Update email: match person, match old email attribute, delete old, insert new
	matchClause := ast.Match(
		ast.Entity("$p", "person", ast.Has("name", ast.Str("Eve"))),
		ast.HasPattern{ThingVar: "$p", AttrType: "email", AttrVar: "$old_email"},
	)

	deleteClause := ast.Delete(
		ast.DeleteHasStatement{AttrVar: "$old_email", OwnerVar: "$p"},
	)

	insertClause := ast.Insert(
		ast.HasStmt("$p", "email", ast.Str("eve@new.com")),
	)

	query, err := c.CompileBatch([]ast.QueryNode{matchClause, deleteClause, insertClause}, "")
	if err != nil {
		t.Fatalf("compile error: %v", err)
	}

	t.Logf("Update query:\n%s", query)

	tx, err = drv.Transaction(dbName, driver.Write)
	if err != nil {
		t.Fatalf("starting write transaction: %v", err)
	}

	if _, err := tx.Query(query); err != nil {
		tx.Close()
		t.Fatalf("update execution failed: %v\nquery:\n%s", err, query)
	}

	if err := tx.Commit(); err != nil {
		t.Fatalf("commit failed: %v", err)
	}

	// Verify update
	verifyQuery := `
		match $p isa person, has name "Eve";
		fetch { "email": $p.email };
	`

	tx, err = drv.Transaction(dbName, driver.Read)
	if err != nil {
		t.Fatalf("starting read transaction: %v", err)
	}
	defer tx.Close()

	results, err := tx.Query(verifyQuery)
	if err != nil {
		t.Fatalf("verify query failed: %v", err)
	}

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}

	if email, ok := results[0]["email"].(string); !ok || email != "eve@new.com" {
		t.Errorf("expected email 'eve@new.com', got %v", results[0]["email"])
	}
}
