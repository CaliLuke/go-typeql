//go:build integration && cgo && typedb

package gotype_test

import (
	"context"
	"strings"
	"testing"

	"github.com/CaliLuke/go-typeql/ast"
	"github.com/CaliLuke/go-typeql/gotype"
)

// ---------------------------------------------------------------------------
// Test models
// ---------------------------------------------------------------------------

type Person struct {
	gotype.BaseEntity
	Name  string `typedb:"name,key"`
	Email string `typedb:"email,unique"`
	Age   *int   `typedb:"age,card=0..1"`
}

type Company struct {
	gotype.BaseEntity
	Name     string `typedb:"name,key"`
	Industry string `typedb:"industry"`
}

type Employment struct {
	gotype.BaseRelation
	Employee  *Person  `typedb:"role:employee"`
	Employer  *Company `typedb:"role:employer"`
	StartDate string   `typedb:"start-date"`
}

// ---------------------------------------------------------------------------
// Entity insert tests
// ---------------------------------------------------------------------------

func TestIntegration_InsertEntity(t *testing.T) {
	db := setupTestDBDefault(t)
	ctx := context.Background()

	mgr := gotype.NewManager[Person](db)

	alice := &Person{Name: "Alice", Email: "alice@example.com", Age: new(30)}
	assertInsert(t, ctx, mgr, alice)

	// After insert, IID should be populated (entity has a key field).
	if alice.GetIID() == "" {
		t.Error("expected IID to be populated after insert, got empty string")
	}
}

func TestIntegration_InsertAndFetchAll(t *testing.T) {
	db := setupTestDBDefault(t)
	ctx := context.Background()

	mgr := gotype.NewManager[Person](db)

	assertInsert(t, ctx, mgr, &Person{Name: "Alice", Email: "alice@example.com", Age: new(30)})

	results := assertCount(t, ctx, mgr, 1)
	if results[0].Name != "Alice" {
		t.Errorf("expected name Alice, got %q", results[0].Name)
	}
	if results[0].Email != "alice@example.com" {
		t.Errorf("expected email alice@example.com, got %q", results[0].Email)
	}
	if results[0].Age == nil || *results[0].Age != 30 {
		t.Errorf("expected age 30, got %v", results[0].Age)
	}
}

func TestIntegration_InsertAndGetByFilter(t *testing.T) {
	db := setupTestDBDefault(t)
	ctx := context.Background()

	mgr := gotype.NewManager[Person](db)

	assertInsert(t, ctx, mgr, &Person{Name: "Alice", Email: "alice@example.com", Age: new(30)})
	assertInsert(t, ctx, mgr, &Person{Name: "Bob", Email: "bob@example.com", Age: new(25)})

	result := assertGetOne(t, ctx, mgr, map[string]any{"name": "Alice"})
	if result.Name != "Alice" {
		t.Errorf("expected name Alice, got %q", result.Name)
	}
}

// ---------------------------------------------------------------------------
// Entity update tests
// ---------------------------------------------------------------------------

func TestIntegration_UpdateEntity(t *testing.T) {
	db := setupTestDBDefault(t)
	ctx := context.Background()

	mgr := gotype.NewManager[Person](db)

	assertInsert(t, ctx, mgr, &Person{Name: "Henry", Email: "henry@example.com", Age: new(25)})
	fetched := assertGetOne(t, ctx, mgr, map[string]any{"name": "Henry"})

	fetched.Email = "henry.new@example.com"
	assertUpdate(t, ctx, mgr, fetched)

	updated := assertGetOne(t, ctx, mgr, map[string]any{"name": "Henry"})
	if updated.Email != "henry.new@example.com" {
		t.Errorf("expected email henry.new@example.com, got %q", updated.Email)
	}
}

// ---------------------------------------------------------------------------
// Entity delete tests
// ---------------------------------------------------------------------------

func TestIntegration_DeleteEntity(t *testing.T) {
	db := setupTestDBDefault(t)
	ctx := context.Background()

	mgr := gotype.NewManager[Person](db)

	assertInsert(t, ctx, mgr, &Person{Name: "Jack", Email: "jack@example.com", Age: new(30)})
	fetched := assertGetOne(t, ctx, mgr, map[string]any{"name": "Jack"})

	assertDelete(t, ctx, mgr, fetched)
	assertCount(t, ctx, mgr, 0)
}

// ---------------------------------------------------------------------------
// Bulk insert tests
// ---------------------------------------------------------------------------

func TestIntegration_InsertMany(t *testing.T) {
	db := setupTestDBDefault(t)
	ctx := context.Background()

	mgr := gotype.NewManager[Person](db)

	persons := []*Person{
		{Name: "Bob", Email: "bob@example.com", Age: new(25)},
		{Name: "Charlie", Email: "charlie@example.com", Age: new(35)},
		{Name: "Diana", Email: "diana@example.com", Age: new(28)},
	}
	assertInsertMany(t, ctx, mgr, persons)

	all := assertCount(t, ctx, mgr, 3)

	names := make(map[string]bool)
	for _, p := range all {
		names[p.Name] = true
	}
	for _, expected := range []string{"Bob", "Charlie", "Diana"} {
		if !names[expected] {
			t.Errorf("expected to find %q in results", expected)
		}
	}
}

// ---------------------------------------------------------------------------
// Relation insert tests
// ---------------------------------------------------------------------------

func TestIntegration_InsertRelation(t *testing.T) {
	db := setupTestDBDefault(t)
	ctx := context.Background()

	personMgr := gotype.NewManager[Person](db)
	companyMgr := gotype.NewManager[Company](db)

	alice := insertAndGet(t, ctx, personMgr, &Person{Name: "Alice", Email: "alice@example.com"}, "name", "Alice")
	techcorp := insertAndGet(t, ctx, companyMgr, &Company{Name: "TechCorp", Industry: "Technology"}, "name", "TechCorp")

	employmentMgr := gotype.NewManager[Employment](db)
	employment := &Employment{
		Employee:  alice,
		Employer:  techcorp,
		StartDate: "2024-01-15",
	}
	assertInsert(t, ctx, employmentMgr, employment)

	allRelations := assertCount(t, ctx, employmentMgr, 1)
	if allRelations[0].StartDate != "2024-01-15" {
		t.Errorf("expected start-date 2024-01-15, got %q", allRelations[0].StartDate)
	}
}

// ---------------------------------------------------------------------------
// Optional attribute tests
// ---------------------------------------------------------------------------

func TestIntegration_OptionalAttributes(t *testing.T) {
	db := setupTestDBDefault(t)
	ctx := context.Background()

	mgr := gotype.NewManager[Person](db)

	assertInsert(t, ctx, mgr, &Person{Name: "Kate", Email: "kate@example.com", Age: nil})

	result := assertGetOne(t, ctx, mgr, map[string]any{"name": "Kate"})
	if result.Name != "Kate" {
		t.Errorf("expected name Kate, got %q", result.Name)
	}
	if result.Age != nil {
		t.Errorf("expected age nil, got %v", *result.Age)
	}
}

// ---------------------------------------------------------------------------
// Schema generation sanity check
// ---------------------------------------------------------------------------

func TestIntegration_SchemaGeneration(t *testing.T) {
	_ = gotype.Register[Person]()
	_ = gotype.Register[Company]()
	_ = gotype.Register[Employment]()

	schema := gotype.GenerateSchema()
	if schema == "" {
		t.Fatal("GenerateSchema returned empty string")
	}

	t.Logf("Generated schema:\n%s", schema)

	for _, want := range []string{
		"attribute name",
		"attribute email",
		"attribute age",
		"attribute industry",
		"attribute start-date",
		"entity person",
		"entity company",
		"relation employment",
		"relates employee",
		"relates employer",
		"@key",
		"@unique",
	} {
		if !strings.Contains(schema, want) {
			t.Errorf("schema missing expected substring %q", want)
		}
	}
}

// ---------------------------------------------------------------------------
// AST delete has statement integration test
// ---------------------------------------------------------------------------

func TestIntegration_AST_DeleteHasStatement(t *testing.T) {
	db := setupTestDBDefault(t)
	ctx := context.Background()

	mgr := gotype.NewManager[Person](db)

	// Insert person with age
	age := 42
	alice := &Person{Name: "Alice AST", Email: "alice-ast@example.com", Age: &age}
	assertInsert(t, ctx, mgr, alice)

	// Build delete query using AST to remove age attribute
	// match $p isa person, has email "alice-ast@example.com", has age $old;
	// delete $old of $p;
	c := &ast.Compiler{}
	matchClause := ast.Match(
		ast.Entity("$p", "person", ast.Has("email", ast.Str("alice-ast@example.com"))),
		ast.HasPattern{ThingVar: "$p", AttrType: "age", AttrVar: "$old"},
	)
	deleteClause := ast.Delete(
		ast.DeleteHas("$old", "$p"),
	)

	matchStr, err := c.Compile(matchClause)
	if err != nil {
		t.Fatalf("failed to compile match clause: %v", err)
	}
	deleteStr, err := c.Compile(deleteClause)
	if err != nil {
		t.Fatalf("failed to compile delete clause: %v", err)
	}

	query := matchStr + "\n" + deleteStr

	t.Logf("Executing delete query:\n%s", query)

	// Execute the delete query
	tx, err := db.Transaction(gotype.WriteTransaction)
	if err != nil {
		t.Fatalf("failed to start transaction: %v", err)
	}
	defer tx.Close()

	_, err = tx.Query(query)
	if err != nil {
		t.Fatalf("failed to execute delete query: %v", err)
	}

	if err := tx.Commit(); err != nil {
		t.Fatalf("failed to commit: %v", err)
	}

	// Verify age is now nil
	result := assertGetOne(t, ctx, mgr, map[string]any{"email": "alice-ast@example.com"})
	if result.Age != nil {
		t.Errorf("expected age to be nil after delete, got %v", *result.Age)
	}
}
