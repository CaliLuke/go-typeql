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

// Test models
type Person struct {
	gotype.BaseEntity
	Name  string `typedb:"name,key"`
	Email string `typedb:"email,unique"`
}

type Company struct {
	gotype.BaseEntity
	Name string `typedb:"name,key"`
}

type Employment struct {
	gotype.BaseRelation
	Employee *Person  `typedb:"role:employee"`
	Employer *Company `typedb:"role:employer"`
}

func dbAddress() string {
	if addr := os.Getenv("TEST_DB_ADDRESS"); addr != "" {
		return addr
	}
	return "localhost:1729"
}

func setupTestDB(t *testing.T) (*driver.Driver, string) {
	t.Helper()

	addr := dbAddress()
	drv, err := driver.Open(addr, "admin", "password")
	if err != nil {
		t.Skipf("TypeDB not available at %s: %v", addr, err)
	}

	// Unique DB name per test
	sanitized := strings.NewReplacer("/", "_", " ", "_", ".", "_").Replace(t.Name())
	if len(sanitized) > 50 {
		sanitized = sanitized[:50]
	}
	dbName := fmt.Sprintf("go_ast_integ_%s", strings.ToLower(sanitized))

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

// TestTypelessRelation verifies that typeless relation patterns compile to
// valid TypeQL and execute successfully against TypeDB.
func TestTypelessRelation(t *testing.T) {
	drv, dbName := setupTestDB(t)

	// Insert test data: two persons and an employment relation
	tx, err := drv.Transaction(dbName, driver.Write)
	if err != nil {
		t.Fatalf("starting write transaction: %v", err)
	}

	insertQuery := `
		insert
		$alice isa person, has name "Alice", has email "alice@test.com";
		$bob isa person, has name "Bob", has email "bob@test.com";
		$acme isa company, has name "Acme Corp";
		(employee: $alice, employer: $acme) isa employment;
	`

	if _, err := tx.Query(insertQuery); err != nil {
		tx.Close()
		t.Fatalf("inserting test data: %v", err)
	}

	if err := tx.Commit(); err != nil {
		t.Fatalf("committing insert: %v", err)
	}

	// Now test typeless relation pattern: match any relation between Alice and any other entity
	c := &ast.Compiler{}

	tests := []struct {
		name        string
		pattern     ast.Pattern
		wantCompile string
	}{
		{
			name: "typeless relation with role players",
			pattern: ast.RelationPattern{
				Variable: "$r",
				TypeName: "",
				RolePlayers: []ast.RolePlayer{
					{Role: "employee", PlayerVar: "$alice"},
					{Role: "employer", PlayerVar: "$other"},
				},
			},
			wantCompile: "$r (employee: $alice, employer: $other)",
		},
		{
			name: "typed relation with role players",
			pattern: ast.RelationPattern{
				Variable: "$r",
				TypeName: "employment",
				RolePlayers: []ast.RolePlayer{
					{Role: "employee", PlayerVar: "$alice"},
					{Role: "employer", PlayerVar: "$other"},
				},
			},
			wantCompile: "$r isa employment (employee: $alice, employer: $other)",
		},
		{
			name: "relation with links role players",
			pattern: ast.RelationPattern{
				Variable: "$r",
				TypeName: "",
				RolePlayers: []ast.RolePlayer{
					{Role: "links", PlayerVar: "$alice"},
					{Role: "links", PlayerVar: "$other"},
				},
			},
			wantCompile: "$r, links ($alice), links ($other)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Compile the pattern
			matchClause := ast.MatchClause{
				Patterns: []ast.Pattern{
					ast.EntityPattern{Variable: "$alice", TypeName: "person"},
					ast.HasPattern{ThingVar: "$alice", AttrType: "name", AttrVar: "$n"},
					ast.ValueComparisonPattern{Var: "$n", Operator: "==", Value: ast.LiteralValue{Val: "Alice", ValueType: "string"}},
					tt.pattern,
				},
			}

			compiled, err := c.Compile(matchClause)
			if err != nil {
				t.Fatalf("compile error: %v", err)
			}

			t.Logf("Compiled query:\n%s", compiled)

			// Verify compiled output contains expected pattern
			if !strings.Contains(compiled, tt.wantCompile) {
				t.Errorf("compiled query does not contain expected pattern %q:\n%s", tt.wantCompile, compiled)
			}

			// Execute against TypeDB to verify syntax is valid
			tx, err := drv.Transaction(dbName, driver.Read)
			if err != nil {
				t.Fatalf("starting read transaction: %v", err)
			}
			defer tx.Close()

			query := compiled
			_, err = tx.Query(query)

			if err != nil {
				t.Errorf("unexpected error executing query: %v\nquery:\n%s", err, query)
			} else {
				t.Logf("✓ Query executed successfully")
			}
		})
	}
}

// TestTypelessRelationDiscovery tests using typeless relations to discover
// edges between nodes without knowing the relation type (auto-k use case).
func TestTypelessRelationDiscovery(t *testing.T) {
	drv, dbName := setupTestDB(t)

	// Insert test data
	tx, err := drv.Transaction(dbName, driver.Write)
	if err != nil {
		t.Fatalf("starting write transaction: %v", err)
	}

	insertQuery := `
		insert
		$alice isa person, has name "Alice", has email "alice@test.com";
		$bob isa person, has name "Bob", has email "bob@test.com";
		$acme isa company, has name "Acme Corp";
		(employee: $alice, employer: $acme) isa employment;
	`

	if _, err := tx.Query(insertQuery); err != nil {
		tx.Close()
		t.Fatalf("inserting test data: %v", err)
	}

	if err := tx.Commit(); err != nil {
		t.Fatalf("committing insert: %v", err)
	}

	// Discovery query: find any relation between Alice and other entities
	// This demonstrates the auto-k use case: discover edges without knowing type
	// Use reduce to count rather than fetch (TypeDB limitation with typeless patterns)
	c := &ast.Compiler{}

	matchClause := ast.Match(
		// Match Alice
		ast.Entity("$alice", "person", ast.Has("name", ast.Str("Alice"))),
		// Match any relation involving Alice (typeless!)
		ast.Relation("$r", "", []ast.RolePlayer{{Role: "employee", PlayerVar: "$alice"}}),
	)

	reduceClause := ast.ReduceClause{
		Assignments: []ast.ReduceAssignment{{Variable: "$count", Expression: "count($r)"}},
	}

	query, err := c.CompileBatch([]ast.QueryNode{matchClause, reduceClause}, "")
	if err != nil {
		t.Fatalf("compile error: %v", err)
	}

	t.Logf("Discovery query:\n%s", query)

	// Execute
	tx, err = drv.Transaction(dbName, driver.Read)
	if err != nil {
		t.Fatalf("starting read transaction: %v", err)
	}
	defer tx.Close()

	results, err := tx.Query(query)
	if err != nil {
		t.Fatalf("query execution failed: %v\nquery:\n%s", err, query)
	}

	t.Logf("✓ Discovery query executed successfully")
	t.Logf("Results: %+v", results)

	// Verify we found at least one relation (the employment we inserted)
	if len(results) == 0 {
		t.Error("expected count result")
	} else if count, ok := results[0]["count"]; ok {
		// Try different number types
		var numVal float64
		switch v := count.(type) {
		case float64:
			numVal = v
		case int:
			numVal = float64(v)
		case int64:
			numVal = float64(v)
		default:
			t.Errorf("unexpected count type %T: %+v", count, count)
			return
		}

		if numVal > 0 {
			t.Logf("✓ Discovered %.0f relation(s) using typeless pattern", numVal)
		} else {
			t.Error("expected count > 0")
		}
	} else {
		t.Errorf("missing 'count' key in results: %+v", results)
	}
}
