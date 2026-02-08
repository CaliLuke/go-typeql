//go:build integration && cgo && typedb

package gotype_test

import (
	"context"
	"strings"
	"testing"

	"github.com/CaliLuke/go-typeql/gotype"
)

// ---------------------------------------------------------------------------
// Schema diff tests (ported from Python test_diff.py, test_creation.py,
// test_relations.py)
// ---------------------------------------------------------------------------

func TestIntegration_SchemaDiff_NoChanges(t *testing.T) {
	// Ported from test_schema_diff_no_changes.
	// Comparing identical schemas shows no changes.
	db := setupTestDBDefault(t)
	ctx := context.Background()
	_ = ctx

	addr := dbAddress()
	schemaStr := getSchemaString(t, addr, db.Name())

	// Re-register same types.
	gotype.ClearRegistry()
	_ = gotype.Register[Person]()
	_ = gotype.Register[Company]()
	_ = gotype.Register[Employment]()

	current, err := gotype.IntrospectSchemaFromString(schemaStr)
	if err != nil {
		t.Fatalf("introspect: %v", err)
	}

	diff := gotype.DiffSchemaFromRegistry(current)
	if !diff.IsEmpty() {
		t.Errorf("expected no changes, got: %s", diff.Summary())
	}
	if !strings.Contains(diff.Summary(), "up to date") {
		t.Errorf("expected 'up to date' in summary, got: %s", diff.Summary())
	}
}

func TestIntegration_SchemaDiff_AddedEntity(t *testing.T) {
	// Ported from test_schema_diff_detect_added_entities.
	// Detect a newly added entity type.
	db := setupTestDBWith(t, func() {
		_ = gotype.Register[Person]()
	})

	addr := dbAddress()
	schemaStr := getSchemaString(t, addr, db.Name())

	// Add a new entity type.
	gotype.ClearRegistry()
	_ = gotype.Register[Person]()
	_ = gotype.Register[Company]()

	current, err := gotype.IntrospectSchemaFromString(schemaStr)
	if err != nil {
		t.Fatalf("introspect: %v", err)
	}

	diff := gotype.DiffSchemaFromRegistry(current)
	if diff.IsEmpty() {
		t.Error("expected non-empty diff (added Company)")
	}
	if len(diff.AddEntities) == 0 {
		t.Error("expected AddEntities to contain Company")
	}
}

func TestIntegration_SchemaDiff_AddedAttribute(t *testing.T) {
	// Ported from test_schema_diff_detect_added_attributes.
	// Detect a newly added attribute type.
	db := setupTestDBWith(t, func() {
		_ = gotype.Register[Person]()
	})

	addr := dbAddress()
	schemaStr := getSchemaString(t, addr, db.Name())

	// Add a new model with new attribute.
	gotype.ClearRegistry()
	_ = gotype.Register[Person]()
	_ = gotype.Register[MigratedPerson]() // has "nickname" attr

	current, err := gotype.IntrospectSchemaFromString(schemaStr)
	if err != nil {
		t.Fatalf("introspect: %v", err)
	}

	diff := gotype.DiffSchemaFromRegistry(current)
	if diff.IsEmpty() {
		t.Error("expected non-empty diff (added nickname)")
	}

	hasNickname := false
	for _, a := range diff.AddAttributes {
		if a.Name == "nickname" {
			hasNickname = true
		}
	}
	if !hasNickname {
		// Could be in AddOwns if attribute type already exists
		for _, o := range diff.AddOwns {
			if o.Attribute == "nickname" {
				hasNickname = true
			}
		}
	}
	if !hasNickname {
		t.Error("expected 'nickname' in diff (AddAttributes or AddOwns)")
	}
}

func TestIntegration_SchemaDiff_AddedRelation(t *testing.T) {
	// Ported from test_schema_diff_detect_added_relations.
	// Detect a newly added relation type.
	db := setupTestDBWith(t, func() {
		_ = gotype.Register[Person]()
		_ = gotype.Register[Company]()
	})

	addr := dbAddress()
	schemaStr := getSchemaString(t, addr, db.Name())

	// Add Employment relation.
	gotype.ClearRegistry()
	_ = gotype.Register[Person]()
	_ = gotype.Register[Company]()
	_ = gotype.Register[Employment]()

	current, err := gotype.IntrospectSchemaFromString(schemaStr)
	if err != nil {
		t.Fatalf("introspect: %v", err)
	}

	diff := gotype.DiffSchemaFromRegistry(current)
	if diff.IsEmpty() {
		t.Error("expected non-empty diff (added Employment)")
	}
	if len(diff.AddRelations) == 0 {
		t.Error("expected AddRelations to contain Employment")
	}
}

func TestIntegration_SchemaDiff_SummaryFormatting(t *testing.T) {
	// Ported from test_schema_diff_summary_formatting.
	// Verify diff summary contains relevant information.
	diff := &gotype.SchemaDiff{
		AddAttributes: []gotype.AttrChange{
			{Name: "nickname", ValueType: "string"},
			{Name: "score", ValueType: "double"},
		},
		AddEntities: []gotype.TypeChange{
			{TypeQL: "entity sensor, owns sensor-id @key;"},
		},
		AddRelations: []gotype.TypeChange{
			{TypeQL: "relation friendship, relates friend1, relates friend2;"},
		},
	}

	summary := diff.Summary()

	if !strings.Contains(summary, "nickname") {
		t.Errorf("summary should mention 'nickname', got: %s", summary)
	}
	if !strings.Contains(summary, "score") {
		t.Errorf("summary should mention 'score', got: %s", summary)
	}
	if !strings.Contains(summary, "2 attribute") {
		t.Errorf("summary should mention '2 attribute', got: %s", summary)
	}
	if !strings.Contains(summary, "1 entity") {
		t.Errorf("summary should mention '1 entity', got: %s", summary)
	}
	if !strings.Contains(summary, "1 relation") {
		t.Errorf("summary should mention '1 relation', got: %s", summary)
	}
}

func TestIntegration_Schema_HasRelationRoles(t *testing.T) {
	// Ported from test_schema_with_relations.
	// Verify generated schema includes relation roles.
	gotype.ClearRegistry()
	_ = gotype.Register[Person]()
	_ = gotype.Register[Company]()
	_ = gotype.Register[Employment]()

	schema := gotype.GenerateSchema()

	// Verify relation and roles are present.
	if !strings.Contains(schema, "relation employment") {
		t.Error("schema should contain 'relation employment'")
	}
	if !strings.Contains(schema, "relates employee") {
		t.Error("schema should contain 'relates employee'")
	}
	if !strings.Contains(schema, "relates employer") {
		t.Error("schema should contain 'relates employer'")
	}
	if !strings.Contains(schema, "owns start-date") {
		t.Error("schema should contain 'owns start-date'")
	}
	// Verify plays clauses (formatted as indented lines within entity block).
	if !strings.Contains(schema, "plays employment:employee") {
		t.Error("schema should contain plays employment:employee clause")
	}
	if !strings.Contains(schema, "plays employment:employer") {
		t.Error("schema should contain plays employment:employer clause")
	}
}

func TestIntegration_Schema_SafeUpdate(t *testing.T) {
	// Ported from test_schema_update_safe_changes.
	// Adding a new attribute (safe change) via migration should work.
	db := setupTestDBWith(t, func() {
		_ = gotype.Register[Person]()
	})
	ctx := context.Background()

	// Verify we can insert with original schema.
	mgr := gotype.NewManager[Person](db)
	assertInsert(t, ctx, mgr, &Person{Name: "Before", Email: "before@test.com"})

	// Migrate: add MigratedPerson which has extra "nickname" attribute.
	gotype.ClearRegistry()
	_ = gotype.Register[MigratedPerson]()

	diff, err := gotype.Migrate(ctx, db)
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if diff.IsEmpty() {
		t.Error("expected non-empty diff for safe update")
	}

	// Verify new attribute is usable.
	_, err = db.ExecuteWrite(ctx,
		`insert $e isa migrated-person, has name "After", has email "after@test.com", has nickname "Testy";`)
	if err != nil {
		t.Fatalf("insert with new attr: %v", err)
	}
}
