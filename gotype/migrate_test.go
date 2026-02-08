package gotype

import (
	"strings"
	"testing"

	"github.com/CaliLuke/go-typeql/tqlgen"
)

func TestDiffSchema_Empty(t *testing.T) {
	desired := &tqlgen.ParsedSchema{}
	current := &tqlgen.ParsedSchema{}
	diff := DiffSchema(desired, current)
	if !diff.IsEmpty() {
		t.Error("expected empty diff")
	}
	if diff.Summary() != "schema is up to date" {
		t.Errorf("unexpected summary: %q", diff.Summary())
	}
}

func TestDiffSchema_NewAttribute(t *testing.T) {
	desired := &tqlgen.ParsedSchema{
		Attributes: []tqlgen.AttributeSpec{
			{Name: "name", ValueType: "string"},
			{Name: "age", ValueType: "integer"},
		},
	}
	current := &tqlgen.ParsedSchema{
		Attributes: []tqlgen.AttributeSpec{
			{Name: "name", ValueType: "string"},
		},
	}
	diff := DiffSchema(desired, current)

	if len(diff.AddAttributes) != 1 {
		t.Fatalf("expected 1 new attribute, got %d", len(diff.AddAttributes))
	}
	if diff.AddAttributes[0].Name != "age" {
		t.Errorf("expected age, got %s", diff.AddAttributes[0].Name)
	}
}

func TestDiffSchema_NewEntity(t *testing.T) {
	desired := &tqlgen.ParsedSchema{
		Entities: []tqlgen.EntitySpec{
			{
				Name: "person",
				Owns: []tqlgen.OwnsSpec{
					{Attribute: "name", Key: true},
				},
			},
		},
	}
	current := &tqlgen.ParsedSchema{}
	diff := DiffSchema(desired, current)

	if len(diff.AddEntities) != 1 {
		t.Fatalf("expected 1 new entity, got %d", len(diff.AddEntities))
	}
	assertContains(t, diff.AddEntities[0].TypeQL, "entity person")
	assertContains(t, diff.AddEntities[0].TypeQL, "owns name @key")
}

func TestDiffSchema_NewOwns(t *testing.T) {
	desired := &tqlgen.ParsedSchema{
		Entities: []tqlgen.EntitySpec{
			{
				Name: "person",
				Owns: []tqlgen.OwnsSpec{
					{Attribute: "name", Key: true},
					{Attribute: "email", Unique: true},
				},
			},
		},
	}
	current := &tqlgen.ParsedSchema{
		Entities: []tqlgen.EntitySpec{
			{
				Name: "person",
				Owns: []tqlgen.OwnsSpec{
					{Attribute: "name", Key: true},
				},
			},
		},
	}
	diff := DiffSchema(desired, current)

	if len(diff.AddEntities) != 0 {
		t.Errorf("expected 0 new entities, got %d", len(diff.AddEntities))
	}
	if len(diff.AddOwns) != 1 {
		t.Fatalf("expected 1 new owns, got %d", len(diff.AddOwns))
	}
	if diff.AddOwns[0].Attribute != "email" {
		t.Errorf("expected email, got %s", diff.AddOwns[0].Attribute)
	}
	if diff.AddOwns[0].TypeName != "person" {
		t.Errorf("expected person, got %s", diff.AddOwns[0].TypeName)
	}
}

func TestDiffSchema_NewRelation(t *testing.T) {
	desired := &tqlgen.ParsedSchema{
		Relations: []tqlgen.RelationSpec{
			{
				Name: "employment",
				Relates: []tqlgen.RelatesSpec{
					{Role: "employee", Card: "1"},
					{Role: "employer", Card: "1"},
				},
			},
		},
	}
	current := &tqlgen.ParsedSchema{}
	diff := DiffSchema(desired, current)

	if len(diff.AddRelations) != 1 {
		t.Fatalf("expected 1 new relation, got %d", len(diff.AddRelations))
	}
	assertContains(t, diff.AddRelations[0].TypeQL, "relation employment")
}

func TestDiffSchema_DetectsRemoval(t *testing.T) {
	desired := &tqlgen.ParsedSchema{}
	current := &tqlgen.ParsedSchema{
		Entities: []tqlgen.EntitySpec{
			{Name: "old_entity"},
		},
	}
	diff := DiffSchema(desired, current)

	if len(diff.RemoveTypes) != 1 {
		t.Fatalf("expected 1 removal, got %d", len(diff.RemoveTypes))
	}
	if diff.RemoveTypes[0] != "old_entity" {
		t.Errorf("expected old_entity, got %s", diff.RemoveTypes[0])
	}
	assertContains(t, diff.Summary(), "WARNING")
	assertContains(t, diff.Summary(), "old_entity")
}

func TestGenerateMigration(t *testing.T) {
	diff := &SchemaDiff{
		AddAttributes: []AttrChange{
			{Name: "age", ValueType: "integer"},
		},
		AddEntities: []TypeChange{
			{TypeQL: "entity person,\n    owns name @key;"},
		},
		AddOwns: []OwnsChange{
			{TypeName: "person", Attribute: "email", Annots: "@unique"},
		},
	}

	stmts := diff.GenerateMigration()
	if len(stmts) != 3 {
		t.Fatalf("expected 3 statements, got %d", len(stmts))
	}

	assertContains(t, stmts[0], "attribute age, value integer")
	assertContains(t, stmts[1], "entity person")
	assertContains(t, stmts[2], "person owns email @unique")
}

func TestIntrospectSchemaFromString_Empty(t *testing.T) {
	schema, err := IntrospectSchemaFromString("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(schema.Attributes) != 0 || len(schema.Entities) != 0 {
		t.Error("expected empty schema")
	}
}

func TestIntrospectSchemaFromString_Valid(t *testing.T) {
	input := `define
attribute name, value string;
entity person,
    owns name @key;
`
	schema, err := IntrospectSchemaFromString(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(schema.Attributes) != 1 {
		t.Errorf("expected 1 attribute, got %d", len(schema.Attributes))
	}
	if len(schema.Entities) != 1 {
		t.Errorf("expected 1 entity, got %d", len(schema.Entities))
	}
}

func TestDiffSchemaFromRegistry(t *testing.T) {
	registerTestTypes(t)

	// Empty DB → everything needs to be added
	empty := &tqlgen.ParsedSchema{}
	diff := DiffSchemaFromRegistry(empty)

	if diff.IsEmpty() {
		t.Fatal("expected non-empty diff against empty DB")
	}

	// Should have attributes and entity/relation types
	if len(diff.AddAttributes) == 0 {
		t.Error("expected new attributes")
	}
	if len(diff.AddEntities) == 0 {
		t.Error("expected new entities")
	}
	if len(diff.AddRelations) == 0 {
		t.Error("expected new relations")
	}

	// Summary should mention additions
	summary := diff.Summary()
	if !strings.Contains(summary, "attribute") {
		t.Error("expected attributes in summary")
	}
}

func TestRegistryToParseSchema(t *testing.T) {
	registerTestTypes(t)
	schema := registryToParseSchema()

	// Should have attributes from testPerson (name, email, age)
	attrNames := make(map[string]bool)
	for _, a := range schema.Attributes {
		attrNames[a.Name] = true
	}
	for _, want := range []string{"name", "email", "age"} {
		if !attrNames[want] {
			t.Errorf("missing attribute %q", want)
		}
	}

	// Should have entity for testPerson
	entityNames := make(map[string]bool)
	for _, e := range schema.Entities {
		entityNames[e.Name] = true
	}
	if !entityNames["test-person"] {
		t.Error("missing entity test-person")
	}

	// Should have relation for testEmployment
	relNames := make(map[string]bool)
	for _, r := range schema.Relations {
		relNames[r.Name] = true
	}
	if !relNames["test-employment"] {
		t.Error("missing relation test-employment")
	}
}

func TestSchemaDiff_Summary(t *testing.T) {
	diff := &SchemaDiff{
		AddAttributes: []AttrChange{{Name: "x", ValueType: "string"}},
		AddEntities:   []TypeChange{{TypeQL: "entity foo;"}},
		RemoveTypes:   []string{"old_thing"},
	}

	summary := diff.Summary()
	assertContains(t, summary, "add 1 attribute")
	assertContains(t, summary, "add 1 entity")
	assertContains(t, summary, "WARNING")
	assertContains(t, summary, "old_thing")
}
