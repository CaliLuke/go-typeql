package gotype

import (
	"context"
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

func TestDiffSchema_DoesNotInferRename(t *testing.T) {
	desired := &tqlgen.ParsedSchema{Entities: []tqlgen.EntitySpec{{Name: "person"}}}
	current := &tqlgen.ParsedSchema{Entities: []tqlgen.EntitySpec{{Name: "old-person"}}}
	diff := DiffSchema(desired, current)

	if len(diff.AddEntities) != 1 || len(diff.RemoveTypes) != 1 {
		t.Fatalf("expected an add and a removal, got add=%v remove=%v", diff.AddEntities, diff.RemoveTypes)
	}
	for _, op := range diff.Operations() {
		if _, ok := op.(RenameOperation); ok {
			t.Errorf("DiffSchema inferred a rename operation: %#v", op)
		}
	}
}

func TestDiffSchema_NewEntityIncludesDocAndMetaAnnotations(t *testing.T) {
	desired := &tqlgen.ParsedSchema{
		Entities: []tqlgen.EntitySpec{
			{
				Name: "person",
				Doc:  "A person record.",
				Meta: []tqlgen.MetaSpec{
					{Key: "owner", Value: "identity"},
					{Key: "ui", Value: "person"},
				},
				Owns: []tqlgen.OwnsSpec{
					{Attribute: "name", Key: true, Doc: "Primary display name."},
				},
			},
		},
	}
	current := &tqlgen.ParsedSchema{}

	diff := DiffSchema(desired, current)

	if len(diff.AddEntities) != 1 {
		t.Fatalf("expected 1 new entity, got %d", len(diff.AddEntities))
	}
	assertContains(t, diff.AddEntities[0].TypeQL, `entity person @doc("A person record.") @meta("owner", "identity") @meta("ui", "person")`)
	assertContains(t, diff.AddEntities[0].TypeQL, `owns name @key @doc("Primary display name.")`)
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

func TestDiffSchema_NewOwnsIncludesDocAnnotation(t *testing.T) {
	desired := &tqlgen.ParsedSchema{
		Entities: []tqlgen.EntitySpec{
			{
				Name: "person",
				Owns: []tqlgen.OwnsSpec{
					{Attribute: "name", Key: true},
					{Attribute: "email", Unique: true, Doc: "Primary contact email."},
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

	if len(diff.AddOwns) != 1 {
		t.Fatalf("expected 1 new owns, got %d", len(diff.AddOwns))
	}
	if diff.AddOwns[0].Annots != `@unique @doc("Primary contact email.")` {
		t.Fatalf("Annots = %q", diff.AddOwns[0].Annots)
	}
}

func TestDiffSchema_DocAndMetaOnlyChangesDoNotChurn(t *testing.T) {
	desired := &tqlgen.ParsedSchema{
		Entities: []tqlgen.EntitySpec{
			{
				Name: "person",
				Doc:  "New person doc.",
				Meta: []tqlgen.MetaSpec{{Key: "owner", Value: "new-team"}},
				Owns: []tqlgen.OwnsSpec{
					{Attribute: "name", Key: true, Doc: "New name doc."},
				},
			},
		},
	}
	current := &tqlgen.ParsedSchema{
		Entities: []tqlgen.EntitySpec{
			{
				Name: "person",
				Doc:  "Old person doc.",
				Meta: []tqlgen.MetaSpec{{Key: "owner", Value: "old-team"}},
				Owns: []tqlgen.OwnsSpec{
					{Attribute: "name", Key: true, Doc: "Old name doc."},
				},
			},
		},
	}

	diff := DiffSchema(desired, current)

	if !diff.IsEmpty() {
		t.Fatalf("expected doc/meta-only changes to produce no diff, got: %s", diff.Summary())
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

// --- Deeper diff detection (issues #56, #57, #58, #34) ---

func TestDiffSchema_ValueTypeChangeReported(t *testing.T) {
	desired := &tqlgen.ParsedSchema{
		Attributes: []tqlgen.AttributeSpec{{Name: "age", ValueType: "string"}},
	}
	current := &tqlgen.ParsedSchema{
		Attributes: []tqlgen.AttributeSpec{{Name: "age", ValueType: "integer"}},
	}
	diff := DiffSchema(desired, current)

	if !diff.IsEmpty() {
		t.Errorf("value-type change is not applicable, expected empty diff, got: %s", diff.Summary())
	}
	if len(diff.Unsupported) != 1 {
		t.Fatalf("expected 1 unsupported change, got %d", len(diff.Unsupported))
	}
	if diff.Unsupported[0].Type != "type_change" {
		t.Errorf("expected type_change, got %q", diff.Unsupported[0].Type)
	}
	if !diff.HasBreakingChanges() {
		t.Error("unsupported change should count as breaking")
	}
	assertContains(t, diff.Summary(), "unsupported change")
	assertContains(t, diff.Summary(), "age")
}

func TestDiffSchema_RemoveOwnsPopulated(t *testing.T) {
	desired := &tqlgen.ParsedSchema{
		Entities: []tqlgen.EntitySpec{
			{Name: "person", Owns: []tqlgen.OwnsSpec{{Attribute: "name", Key: true}}},
		},
	}
	current := &tqlgen.ParsedSchema{
		Entities: []tqlgen.EntitySpec{
			{Name: "person", Owns: []tqlgen.OwnsSpec{
				{Attribute: "name", Key: true},
				{Attribute: "z-old"},
				{Attribute: "a-old"},
			}},
		},
	}
	diff := DiffSchema(desired, current)

	if len(diff.RemoveOwns) != 2 {
		t.Fatalf("expected 2 removed owns, got %d: %+v", len(diff.RemoveOwns), diff.RemoveOwns)
	}
	// Sorted for deterministic plans.
	if diff.RemoveOwns[0].Attribute != "a-old" || diff.RemoveOwns[1].Attribute != "z-old" {
		t.Errorf("expected sorted removals, got %+v", diff.RemoveOwns)
	}
	assertContains(t, diff.Summary(), "owns clause(s) in DB not in code")
	if !diff.HasBreakingChanges() {
		t.Error("expected breaking changes for removed owns")
	}
}

func TestDiffSchema_StaleAttributeRemoved(t *testing.T) {
	desired := &tqlgen.ParsedSchema{}
	current := &tqlgen.ParsedSchema{
		Attributes: []tqlgen.AttributeSpec{
			{Name: "z-stale", ValueType: "string"},
			{Name: "a-stale", ValueType: "integer"},
		},
	}
	diff := DiffSchema(desired, current)

	if len(diff.RemoveAttributes) != 2 {
		t.Fatalf("expected 2 stale attributes, got %d", len(diff.RemoveAttributes))
	}
	if diff.RemoveAttributes[0] != "a-stale" || diff.RemoveAttributes[1] != "z-stale" {
		t.Errorf("expected sorted stale attributes, got %v", diff.RemoveAttributes)
	}
	assertContains(t, diff.Summary(), "attribute(s) in DB not in code")

	// Destructive ops must remove attributes after types.
	diff.RemoveTypes = []string{"obsolete"}
	ops := diff.DestructiveOperations()
	if len(ops) != 3 {
		t.Fatalf("expected 3 destructive ops, got %d", len(ops))
	}
	if _, ok := ops[len(ops)-1].(RemoveAttribute); !ok {
		t.Errorf("expected attributes removed last, got %T", ops[len(ops)-1])
	}
}

func TestDiffSchema_ExcludesMigrationTrackingTypes(t *testing.T) {
	// A database that only contains go-typeql's own migration tracking
	// schema must diff as empty against an empty registry (issue #58).
	current := &tqlgen.ParsedSchema{}
	for _, trackingSchema := range []string{migrationSchemaSQL, seqMigrationSchemaSQL} {
		parsed, err := IntrospectSchemaFromString(trackingSchema)
		if err != nil {
			t.Fatalf("parse tracking schema: %v", err)
		}
		current.Attributes = append(current.Attributes, parsed.Attributes...)
		current.Entities = append(current.Entities, parsed.Entities...)
		current.Relations = append(current.Relations, parsed.Relations...)
	}
	if len(current.Attributes) == 0 || len(current.Entities) == 0 {
		t.Fatal("expected tracking schema to declare attributes and entities")
	}
	// Every type in the tracking schemas must be covered by the denylist,
	// otherwise a force sync would target its own history.
	for _, a := range current.Attributes {
		if !internalMigrationTypes[a.Name] {
			t.Errorf("tracking attribute %q missing from internalMigrationTypes", a.Name)
		}
	}
	for _, e := range current.Entities {
		if !internalMigrationTypes[e.Name] {
			t.Errorf("tracking entity %q missing from internalMigrationTypes", e.Name)
		}
	}

	diff := DiffSchema(&tqlgen.ParsedSchema{}, current)
	if !diff.IsEmpty() {
		t.Errorf("expected empty diff, got: %s", diff.Summary())
	}
	if diff.HasBreakingChanges() {
		t.Errorf("tracking types must never appear as breaking changes: %+v", diff.BreakingChanges())
	}
	if diff.Summary() != "schema is up to date" {
		t.Errorf("unexpected summary: %q", diff.Summary())
	}
}

func TestDiffSchema_CardChangeEmitsModifyOwns(t *testing.T) {
	desired := &tqlgen.ParsedSchema{
		Entities: []tqlgen.EntitySpec{
			{Name: "person", Owns: []tqlgen.OwnsSpec{{Attribute: "email", Card: "1..3"}}},
		},
	}
	current := &tqlgen.ParsedSchema{
		Entities: []tqlgen.EntitySpec{
			{Name: "person", Owns: []tqlgen.OwnsSpec{{Attribute: "email", Card: "0..1"}}},
		},
	}
	diff := DiffSchema(desired, current)

	if len(diff.ModifyOwns) != 1 {
		t.Fatalf("expected 1 modified owns, got %d", len(diff.ModifyOwns))
	}
	m := diff.ModifyOwns[0]
	if m.NewAnnots != "@card(1..3)" || m.OldAnnots != "@card(0..1)" {
		t.Errorf("unexpected annots: %+v", m)
	}
	stmts := diff.GenerateMigration()
	if len(stmts) != 1 {
		t.Fatalf("expected 1 statement, got %d: %v", len(stmts), stmts)
	}
	assertContains(t, stmts[0], "redefine person, owns email @card(1..3);")

	// The redefine must be a standalone query in the plan, not part of a
	// define block.
	plan := diff.Plan()
	if len(plan.Queries) != 1 || !strings.HasPrefix(plan.Queries[0], "redefine") {
		t.Errorf("expected standalone redefine query, got %v", plan.Queries)
	}
}

func TestDiffSchema_AnnotationTogglesUnsupported(t *testing.T) {
	desired := &tqlgen.ParsedSchema{
		Entities: []tqlgen.EntitySpec{
			{Name: "person", Owns: []tqlgen.OwnsSpec{
				{Attribute: "name", Key: true},
				{Attribute: "email", Unique: true},
				{Attribute: "nick", Card: "0..2"},
			}},
		},
	}
	current := &tqlgen.ParsedSchema{
		Entities: []tqlgen.EntitySpec{
			{Name: "person", Owns: []tqlgen.OwnsSpec{
				{Attribute: "name"},  // @key added in code
				{Attribute: "email"}, // @unique added in code
				{Attribute: "nick"},  // @card added in code
			}},
		},
	}
	diff := DiffSchema(desired, current)

	if !diff.IsEmpty() {
		t.Errorf("expected no applicable changes, got: %s", diff.Summary())
	}
	if len(diff.Unsupported) != 3 {
		t.Fatalf("expected 3 unsupported changes, got %d: %+v", len(diff.Unsupported), diff.Unsupported)
	}
	if !diff.HasBreakingChanges() {
		t.Error("expected breaking changes")
	}
}

func TestDiffSchema_CurrentOnlyCardIsIgnored(t *testing.T) {
	// The DB export may print cardinalities the Go model does not manage;
	// they must not produce diff churn.
	desired := &tqlgen.ParsedSchema{
		Entities: []tqlgen.EntitySpec{
			{Name: "person", Owns: []tqlgen.OwnsSpec{{Attribute: "name", Key: true}}},
		},
	}
	current := &tqlgen.ParsedSchema{
		Entities: []tqlgen.EntitySpec{
			{Name: "person", Owns: []tqlgen.OwnsSpec{{Attribute: "name", Key: true, Card: "1..1"}}},
		},
	}
	diff := DiffSchema(desired, current)
	if !diff.IsEmpty() || len(diff.Unsupported) != 0 {
		t.Errorf("expected clean diff, got: %s", diff.Summary())
	}
}

func TestDiffSchema_SupertypeAndAbstractChangesUnsupported(t *testing.T) {
	desired := &tqlgen.ParsedSchema{
		Entities: []tqlgen.EntitySpec{
			{Name: "person", Parent: "creature", Abstract: true},
		},
	}
	current := &tqlgen.ParsedSchema{
		Entities: []tqlgen.EntitySpec{
			{Name: "person"},
		},
	}
	diff := DiffSchema(desired, current)
	if len(diff.Unsupported) != 2 {
		t.Fatalf("expected 2 unsupported changes, got %d: %+v", len(diff.Unsupported), diff.Unsupported)
	}
	assertContains(t, diff.Summary(), "supertype")
	assertContains(t, diff.Summary(), "abstractness")
}

func TestDiffSchema_AddPlays(t *testing.T) {
	desired := &tqlgen.ParsedSchema{
		Entities: []tqlgen.EntitySpec{
			{Name: "person", Plays: []tqlgen.PlaysSpec{{Relation: "employment", Role: "employee"}}},
		},
	}
	current := &tqlgen.ParsedSchema{
		Entities: []tqlgen.EntitySpec{
			{Name: "person"},
		},
	}
	diff := DiffSchema(desired, current)

	if len(diff.AddPlays) != 1 {
		t.Fatalf("expected 1 plays addition, got %d", len(diff.AddPlays))
	}
	stmts := diff.GenerateMigration()
	if len(stmts) != 1 {
		t.Fatalf("expected 1 statement, got %v", stmts)
	}
	if stmts[0] != "define person plays employment:employee;" {
		t.Errorf("got %q", stmts[0])
	}
}

func TestDiffSchema_NewEntityIncludesPlays(t *testing.T) {
	desired := &tqlgen.ParsedSchema{
		Entities: []tqlgen.EntitySpec{
			{
				Name:  "person",
				Plays: []tqlgen.PlaysSpec{{Relation: "employment", Role: "employee"}},
				Owns:  []tqlgen.OwnsSpec{{Attribute: "name", Key: true}},
			},
		},
	}
	diff := DiffSchema(desired, &tqlgen.ParsedSchema{})
	if len(diff.AddEntities) != 1 {
		t.Fatalf("expected 1 new entity, got %d", len(diff.AddEntities))
	}
	assertContains(t, diff.AddEntities[0].TypeQL, "plays employment:employee")
}

func TestDiffSchema_SubtypeDefineKeepsAnnotationsOffSubClause(t *testing.T) {
	// TypeDB 3.x binds annotations placed after `sub <parent>` to the sub
	// clause itself and rejects them, so annotations must stay on the type
	// name and `sub` becomes a separate constraint (issue #32).
	desired := &tqlgen.ParsedSchema{
		Entities: []tqlgen.EntitySpec{
			{Name: "manager", Parent: "person", Abstract: true, Doc: "A manager."},
		},
	}
	diff := DiffSchema(desired, &tqlgen.ParsedSchema{})
	if len(diff.AddEntities) != 1 {
		t.Fatalf("expected 1 new entity, got %d", len(diff.AddEntities))
	}
	assertContains(t, diff.AddEntities[0].TypeQL, "entity manager @abstract @doc(\"A manager.\"),\n    sub person")
}

func TestRegistryToParseSchema_PopulatesPlays(t *testing.T) {
	registerTestTypes(t)
	schema := registryToParseSchema()

	var person *tqlgen.EntitySpec
	for i := range schema.Entities {
		if schema.Entities[i].Name == "test-person" {
			person = &schema.Entities[i]
		}
	}
	if person == nil {
		t.Fatal("missing entity test-person")
	}
	found := false
	for _, p := range person.Plays {
		if p.Relation == "test-employment" && p.Role == "employee" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected test-person to play test-employment:employee, got %+v", person.Plays)
	}
}

// --- Plans (issues #55, #62) ---

func TestSchemaDiff_Plan_BatchesStatements(t *testing.T) {
	registerTestTypes(t)
	diff := DiffSchemaFromRegistry(&tqlgen.ParsedSchema{})
	diff.RemoveTypes = append(diff.RemoveTypes, "obsolete")
	diff.RemoveOwns = append(diff.RemoveOwns, OwnsChange{TypeName: "kept", Attribute: "old"})
	diff.RemoveAttributes = append(diff.RemoveAttributes, "old")

	plan := diff.Plan(WithDestructive())
	if len(plan.Queries) != 2 {
		t.Fatalf("expected 1 define + 1 undefine query, got %d: %v", len(plan.Queries), plan.Queries)
	}
	if !strings.HasPrefix(plan.Queries[0], "define\n") {
		t.Errorf("first query should be the define block, got %q", plan.Queries[0])
	}
	if strings.Count(plan.Queries[0], "define") != 1 {
		t.Errorf("define block must contain a single define keyword:\n%s", plan.Queries[0])
	}
	assertContains(t, plan.Queries[0], "plays test-employment:employee")
	if !strings.HasPrefix(plan.Queries[1], "undefine\n") {
		t.Errorf("second query should be the undefine block, got %q", plan.Queries[1])
	}
	assertContains(t, plan.Queries[1], "owns old from kept;")
	assertContains(t, plan.Queries[1], "obsolete;")
	if len(plan.Statements) == 0 {
		t.Error("expected individual statements for review")
	}
	if plan.IsEmpty() {
		t.Error("plan should not be empty")
	}

	// Without WithDestructive the undefine block is omitted.
	additive := diff.Plan()
	if len(additive.Queries) != 1 {
		t.Fatalf("expected only the define block, got %v", additive.Queries)
	}
}

func TestPlanSchema_DoesNotExecute(t *testing.T) {
	registerTestTypes(t)
	// mockConn has no transactions: any execution attempt would fail with
	// "no more mock transactions".
	conn := &mockConn{schemaStr: ""}
	db := NewDatabase(conn, "test")

	plan, err := PlanSchema(context.Background(), db, WithForce())
	if err != nil {
		t.Fatalf("PlanSchema: %v", err)
	}
	if plan.IsEmpty() {
		t.Fatal("expected non-empty plan for empty database")
	}
	if plan.Diff == nil || plan.Diff.IsEmpty() {
		t.Error("plan should carry the diff")
	}
	if plan.Summary() == "schema is up to date" {
		t.Error("unexpected summary")
	}
}

// --- SyncSchema ---

func TestSyncSchema_SkipIfMatch(t *testing.T) {
	ClearRegistry()
	// Empty registry vs empty DB → diff is empty → skip
	conn := &mockConn{}
	db := NewDatabase(conn, "test")

	diff, err := SyncSchema(context.Background(), db, WithSkipIfExists())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !diff.IsEmpty() {
		t.Error("expected empty diff")
	}
}

func TestSyncSchema_SingleSchemaTransaction(t *testing.T) {
	registerTestTypes(t)
	tx := &mockTx{}
	conn := &mockConn{txs: []*mockTx{tx}, schemaStr: ""}
	db := NewDatabase(conn, "test")

	diff, err := SyncSchema(context.Background(), db)
	if err != nil {
		t.Fatalf("SyncSchema: %v", err)
	}
	if diff.IsEmpty() {
		t.Fatal("expected non-empty diff")
	}
	if len(tx.queries) != 1 {
		t.Fatalf("expected the whole additive plan in one query, got %d: %v", len(tx.queries), tx.queries)
	}
	if !strings.HasPrefix(tx.queries[0], "define\n") {
		t.Errorf("expected a single define block, got %q", tx.queries[0])
	}
	if !tx.committed {
		t.Error("expected the schema transaction to be committed")
	}
}

func TestSyncSchema_ForcePreservesTrackingTypes(t *testing.T) {
	ClearRegistry()
	tx := &mockTx{}
	conn := &mockConn{
		txs: []*mockTx{tx},
		schemaStr: `define
attribute migration-hash, value string;
attribute migration-summary, value string;
attribute migration-applied-at, value datetime;
entity migration-record,
    owns migration-hash @key,
    owns migration-summary,
    owns migration-applied-at;
attribute obsolete-attr, value string;
entity obsolete-entity,
    owns obsolete-attr;`,
	}
	db := NewDatabase(conn, "test")

	diff, err := SyncSchema(context.Background(), db, WithForce())
	if err != nil {
		t.Fatalf("SyncSchema: %v", err)
	}
	if len(diff.RemoveTypes) != 1 || diff.RemoveTypes[0] != "obsolete-entity" {
		t.Fatalf("expected only obsolete-entity in RemoveTypes, got %v", diff.RemoveTypes)
	}
	if len(tx.queries) != 1 {
		t.Fatalf("expected one undefine block, got %v", tx.queries)
	}
	assertContains(t, tx.queries[0], "obsolete-entity;")
	assertContains(t, tx.queries[0], "obsolete-attr;")
	if strings.Contains(tx.queries[0], "migration-record") ||
		strings.Contains(tx.queries[0], "migration-hash") {
		t.Errorf("force sync must never target its own migration history:\n%s", tx.queries[0])
	}
}

func TestSyncSchema_SkipIfExists_SkipsProvisionedDatabase(t *testing.T) {
	registerTestTypes(t)
	// The DB already has a user type; no transactions are available, so any
	// execution attempt would error.
	conn := &mockConn{schemaStr: `define
attribute name, value string;
entity something-else,
    owns name;`}
	db := NewDatabase(conn, "test")

	diff, err := SyncSchema(context.Background(), db, WithSkipIfExists())
	if err != nil {
		t.Fatalf("SyncSchema: %v", err)
	}
	if diff.IsEmpty() {
		t.Error("the skipped diff should still report the pending changes")
	}
}

func TestSyncSchema_SkipIfExists_AppliesToEmptyDatabase(t *testing.T) {
	registerTestTypes(t)
	tx := &mockTx{}
	conn := &mockConn{txs: []*mockTx{tx}, schemaStr: ""}
	db := NewDatabase(conn, "test")

	diff, err := SyncSchema(context.Background(), db, WithSkipIfExists())
	if err != nil {
		t.Fatalf("SyncSchema: %v", err)
	}
	if diff.IsEmpty() {
		t.Fatal("expected non-empty diff")
	}
	if len(tx.queries) != 1 || !tx.committed {
		t.Fatalf("expected the schema to be applied to the empty database, got %v", tx.queries)
	}
}

func TestSyncSchema_SkipIfExists_IgnoresTrackingTypes(t *testing.T) {
	registerTestTypes(t)
	// A database that only contains migration tracking types counts as
	// unprovisioned for WithSkipIfExists.
	tx := &mockTx{}
	conn := &mockConn{txs: []*mockTx{tx}, schemaStr: migrationSchemaSQL}
	db := NewDatabase(conn, "test")

	_, err := SyncSchema(context.Background(), db, WithSkipIfExists())
	if err != nil {
		t.Fatalf("SyncSchema: %v", err)
	}
	if len(tx.queries) != 1 {
		t.Fatalf("expected schema application, got %v", tx.queries)
	}
}
