package tqlgen

import (
	"bytes"
	"strings"
	"testing"
)

func TestBuildDTOData_EmptyPackage(t *testing.T) {
	data := BuildDTOData(&ParsedSchema{}, DTOConfig{})
	if data.PackageName != "" {
		t.Errorf("expected empty, got %q", data.PackageName)
	}
}

func TestBuildDTOData_EntityOut(t *testing.T) {
	schema := &ParsedSchema{
		Attributes: []AttributeSpec{
			{Name: "name", ValueType: "string"},
			{Name: "age", ValueType: "integer"},
		},
		Entities: []EntitySpec{
			{Name: "person", Owns: []OwnsSpec{
				{Attribute: "name", Key: true},
				{Attribute: "age"},
			}},
		},
	}
	data := BuildDTOData(schema, DTOConfig{PackageName: "dto", UseAcronyms: true})

	if len(data.Entities) != 1 {
		t.Fatalf("expected 1 entity, got %d", len(data.Entities))
	}
	e := data.Entities[0]
	if e.GoName != "Person" {
		t.Errorf("expected Person, got %s", e.GoName)
	}
	if e.TypeName != "person" {
		t.Errorf("expected person, got %s", e.TypeName)
	}

	// Out: all fields are pointers by default (StrictOut=false)
	if len(e.OutFields) != 2 {
		t.Fatalf("expected 2 out fields, got %d", len(e.OutFields))
	}
	for _, f := range e.OutFields {
		if !strings.HasPrefix(f.GoType, "*") {
			t.Errorf("out field %s should be pointer, got %s", f.GoName, f.GoType)
		}
	}

	// Create: name is required (@key), age is optional
	if len(e.CreateFields) != 2 {
		t.Fatalf("expected 2 create fields, got %d", len(e.CreateFields))
	}
	nameField := findField(e.CreateFields, "Name")
	if nameField == nil {
		t.Fatal("missing Name field")
	}
	if strings.HasPrefix(nameField.GoType, "*") {
		t.Error("Name should be required (non-pointer) in Create")
	}
	ageField := findField(e.CreateFields, "Age")
	if ageField == nil {
		t.Fatal("missing Age field")
	}
	if !strings.HasPrefix(ageField.GoType, "*") {
		t.Error("Age should be optional (pointer) in Create")
	}

	// Patch: all pointer
	for _, f := range e.PatchFields {
		if !strings.HasPrefix(f.GoType, "*") {
			t.Errorf("patch field %s should be pointer, got %s", f.GoName, f.GoType)
		}
	}
}

func TestBuildDTOData_StrictOut(t *testing.T) {
	schema := &ParsedSchema{
		Attributes: []AttributeSpec{
			{Name: "name", ValueType: "string"},
			{Name: "age", ValueType: "integer"},
		},
		Entities: []EntitySpec{
			{Name: "person", Owns: []OwnsSpec{
				{Attribute: "name", Key: true},
				{Attribute: "age"},
			}},
		},
	}
	data := BuildDTOData(schema, DTOConfig{PackageName: "dto", StrictOut: true})

	e := data.Entities[0]
	nameField := findField(e.OutFields, "Name")
	if nameField == nil {
		t.Fatal("missing Name")
	}
	if strings.HasPrefix(nameField.GoType, "*") {
		t.Error("Name should be non-pointer in StrictOut")
	}
	ageField := findField(e.OutFields, "Age")
	if ageField == nil {
		t.Fatal("missing Age")
	}
	if !strings.HasPrefix(ageField.GoType, "*") {
		t.Error("Age should be pointer (optional) even in StrictOut")
	}
}

func TestBuildDTOData_SkipAbstract(t *testing.T) {
	schema := &ParsedSchema{
		Entities: []EntitySpec{
			{Name: "artifact", Abstract: true},
			{Name: "task"},
		},
	}
	data := BuildDTOData(schema, DTOConfig{PackageName: "dto", SkipAbstract: true})

	if len(data.Entities) != 1 {
		t.Fatalf("expected 1 entity, got %d", len(data.Entities))
	}
	if data.Entities[0].GoName != "Task" {
		t.Errorf("expected Task, got %s", data.Entities[0].GoName)
	}
}

func TestBuildDTOData_ExcludeEntities(t *testing.T) {
	schema := &ParsedSchema{
		Entities: []EntitySpec{
			{Name: "person"},
			{Name: "internal-counter"},
		},
	}
	data := BuildDTOData(schema, DTOConfig{
		PackageName:     "dto",
		ExcludeEntities: []string{"internal-counter"},
	})

	if len(data.Entities) != 1 {
		t.Fatalf("expected 1 entity, got %d", len(data.Entities))
	}
}

func TestBuildDTOData_Relations(t *testing.T) {
	schema := &ParsedSchema{
		Attributes: []AttributeSpec{
			{Name: "score", ValueType: "double"},
		},
		Entities: []EntitySpec{
			{Name: "person", Plays: []PlaysSpec{{Relation: "knows", Role: "knower"}}},
		},
		Relations: []RelationSpec{
			{Name: "knows",
				Relates: []RelatesSpec{{Role: "knower"}, {Role: "known"}},
				Owns:    []OwnsSpec{{Attribute: "score"}},
			},
		},
	}
	data := BuildDTOData(schema, DTOConfig{PackageName: "dto", UseAcronyms: true})

	if len(data.Relations) != 1 {
		t.Fatalf("expected 1 relation, got %d", len(data.Relations))
	}
	r := data.Relations[0]
	if r.GoName != "Knows" {
		t.Errorf("expected Knows, got %s", r.GoName)
	}
	if len(r.Roles) != 2 {
		t.Fatalf("expected 2 roles, got %d", len(r.Roles))
	}
	if r.Roles[0].OutName != "KnowerID" {
		t.Errorf("expected KnowerID, got %s", r.Roles[0].OutName)
	}
	if r.Roles[0].CreateName != "KnowerID" {
		t.Errorf("expected KnowerID, got %s", r.Roles[0].CreateName)
	}
	if len(r.OutFields) != 1 {
		t.Fatalf("expected 1 out field, got %d", len(r.OutFields))
	}
}

func TestBuildDTOData_BaseStructEmbed(t *testing.T) {
	schema := &ParsedSchema{
		Attributes: []AttributeSpec{
			{Name: "name", ValueType: "string"},
			{Name: "status", ValueType: "string"},
			{Name: "priority", ValueType: "integer"},
		},
		Entities: []EntitySpec{
			{Name: "artifact", Abstract: true, Owns: []OwnsSpec{
				{Attribute: "name", Key: true},
				{Attribute: "status"},
			}},
			{Name: "task", Parent: "artifact", Owns: []OwnsSpec{
				{Attribute: "name", Key: true},
				{Attribute: "status"},
				{Attribute: "priority"},
			}},
		},
	}
	schema.AccumulateInheritance()

	data := BuildDTOData(schema, DTOConfig{
		PackageName:  "dto",
		SkipAbstract: true,
		UseAcronyms:  true,
		BaseStructs: []BaseStructConfig{
			{
				SourceEntity:   "artifact",
				BaseName:       "BaseArtifact",
				InheritedAttrs: []string{"name", "status"},
			},
		},
	})

	// Base struct should be generated
	if len(data.BaseStructs) != 1 {
		t.Fatalf("expected 1 base struct, got %d", len(data.BaseStructs))
	}
	if data.BaseStructs[0].BaseName != "BaseArtifact" {
		t.Errorf("expected BaseArtifact, got %s", data.BaseStructs[0].BaseName)
	}

	// Task should embed and skip inherited attrs
	if len(data.Entities) != 1 {
		t.Fatalf("expected 1 entity, got %d", len(data.Entities))
	}
	task := data.Entities[0]
	if task.EmbedOut != "BaseArtifactOut" {
		t.Errorf("expected BaseArtifactOut embed, got %q", task.EmbedOut)
	}
	// Only priority should be in task's own fields (name and status inherited)
	if len(task.OutFields) != 1 {
		t.Fatalf("expected 1 out field (priority), got %d: %v", len(task.OutFields), task.OutFields)
	}
	if task.OutFields[0].GoName != "Priority" {
		t.Errorf("expected Priority, got %s", task.OutFields[0].GoName)
	}
}

func TestBuildDTOData_FieldOverride(t *testing.T) {
	schema := &ParsedSchema{
		Attributes: []AttributeSpec{
			{Name: "email", ValueType: "string"},
		},
		Entities: []EntitySpec{
			{Name: "person", Owns: []OwnsSpec{{Attribute: "email", Key: true}}},
		},
	}
	reqFalse := false
	data := BuildDTOData(schema, DTOConfig{
		PackageName: "dto",
		EntityFieldOverrides: []EntityFieldOverride{
			{Entity: "person", Field: "email", Variant: "create", Required: &reqFalse},
		},
	})

	e := data.Entities[0]
	emailField := findField(e.CreateFields, "Email")
	if emailField == nil {
		t.Fatal("missing Email")
	}
	if !strings.HasPrefix(emailField.GoType, "*") {
		t.Error("Email should be optional (pointer) in Create after override")
	}
}

func TestBuildDTOData_ConcreteTypes(t *testing.T) {
	schema := &ParsedSchema{
		Entities: []EntitySpec{
			{Name: "artifact", Abstract: true},
			{Name: "task"},
			{Name: "epic"},
		},
		Relations: []RelationSpec{
			{Name: "depends-on"},
		},
	}
	data := BuildDTOData(schema, DTOConfig{PackageName: "dto", UseAcronyms: true})

	if len(data.ConcreteEntities) != 2 {
		t.Errorf("expected 2 concrete entities, got %d", len(data.ConcreteEntities))
	}
	if len(data.ConcreteRelations) != 1 {
		t.Errorf("expected 1 concrete relation, got %d", len(data.ConcreteRelations))
	}
}

func TestBuildDTOData_DatetimeImport(t *testing.T) {
	schema := &ParsedSchema{
		Attributes: []AttributeSpec{
			{Name: "created-at", ValueType: "datetime"},
		},
		Entities: []EntitySpec{
			{Name: "task", Owns: []OwnsSpec{{Attribute: "created-at"}}},
		},
	}
	data := BuildDTOData(schema, DTOConfig{PackageName: "dto"})
	if !data.NeedsTime {
		t.Error("expected NeedsTime=true for datetime attribute")
	}
}

func TestBuildDTOData_HyphenNaming(t *testing.T) {
	schema := &ParsedSchema{
		Attributes: []AttributeSpec{
			{Name: "display-id", ValueType: "string"},
		},
		Entities: []EntitySpec{
			{Name: "user-story", Owns: []OwnsSpec{{Attribute: "display-id", Key: true}}},
		},
	}
	data := BuildDTOData(schema, DTOConfig{PackageName: "dto", UseAcronyms: true})

	e := data.Entities[0]
	if e.GoName != "UserStory" {
		t.Errorf("expected UserStory, got %s", e.GoName)
	}
	f := findField(e.CreateFields, "DisplayID")
	if f == nil {
		t.Errorf("expected DisplayID field, got fields: %v", fieldNames(e.CreateFields))
	}
}

func TestBuildDTOData_CardinalityRequired(t *testing.T) {
	schema := &ParsedSchema{
		Attributes: []AttributeSpec{
			{Name: "score", ValueType: "double"},
		},
		Entities: []EntitySpec{
			{Name: "review", Owns: []OwnsSpec{{Attribute: "score", Card: "1"}}},
		},
	}
	data := BuildDTOData(schema, DTOConfig{PackageName: "dto"})

	e := data.Entities[0]
	f := findField(e.CreateFields, "Score")
	if f == nil {
		t.Fatal("missing Score")
	}
	if strings.HasPrefix(f.GoType, "*") {
		t.Error("Score with @card(1) should be required (non-pointer)")
	}
}

func TestRenderDTO(t *testing.T) {
	schema := &ParsedSchema{
		Attributes: []AttributeSpec{
			{Name: "name", ValueType: "string"},
			{Name: "score", ValueType: "double"},
		},
		Entities: []EntitySpec{
			{Name: "person", Owns: []OwnsSpec{{Attribute: "name", Key: true}}},
		},
		Relations: []RelationSpec{
			{Name: "knows",
				Relates: []RelatesSpec{{Role: "knower"}, {Role: "known"}},
				Owns:    []OwnsSpec{{Attribute: "score"}},
			},
		},
	}
	data := BuildDTOData(schema, DTOConfig{PackageName: "dto", UseAcronyms: true})

	var buf bytes.Buffer
	if err := RenderDTO(&buf, data); err != nil {
		t.Fatal(err)
	}

	out := buf.String()
	checks := map[string]string{
		"package":         "package dto",
		"header":          "Code generated by tqlgen",
		"entity out":      "type PersonOut struct",
		"entity create":   "type PersonCreate struct",
		"entity patch":    "type PersonPatch struct",
		"relation out":    "type KnowsOut struct",
		"relation create": "type KnowsCreate struct",
		"type field":      `Type string`,
		"id field":        `ID string`,
		"TypeName method":  `func (PersonOut) TypeName()`,
		"EntityOut iface": "type EntityOut interface",
		"json tag":        `json:"name"`,
		"role out":        "KnowerID",
		"role create":     "KnowerID",
	}
	for name, want := range checks {
		if !strings.Contains(out, want) {
			t.Errorf("missing %s: expected %q in output", name, want)
		}
	}
}

func TestRenderDTO_SkipRelationOut(t *testing.T) {
	schema := &ParsedSchema{
		Relations: []RelationSpec{
			{Name: "knows", Relates: []RelatesSpec{{Role: "knower"}, {Role: "known"}}},
		},
	}
	data := BuildDTOData(schema, DTOConfig{PackageName: "dto", SkipRelationOut: true})

	var buf bytes.Buffer
	if err := RenderDTO(&buf, data); err != nil {
		t.Fatal(err)
	}

	out := buf.String()
	if strings.Contains(out, "KnowsOut") {
		t.Error("should not contain KnowsOut when SkipRelationOut=true")
	}
	if !strings.Contains(out, "KnowsCreate") {
		t.Error("should still contain KnowsCreate")
	}
}

func TestBuildDTOData_CompositeEntities(t *testing.T) {
	schema := &ParsedSchema{
		Attributes: []AttributeSpec{
			{Name: "name", ValueType: "string"},
			{Name: "priority", ValueType: "integer"},
			{Name: "story-points", ValueType: "double"},
		},
		Entities: []EntitySpec{
			{Name: "task", Owns: []OwnsSpec{{Attribute: "name", Key: true}, {Attribute: "priority"}}},
			{Name: "epic", Owns: []OwnsSpec{{Attribute: "name", Key: true}, {Attribute: "story-points"}}},
		},
	}
	data := BuildDTOData(schema, DTOConfig{
		PackageName: "dto",
		UseAcronyms: true,
		CompositeEntities: []CompositeEntityConfig{
			{Name: "ArtifactDTO", Entities: []string{"task", "epic"}, TypeName: "artifact"},
		},
	})

	if len(data.Composites) != 1 {
		t.Fatalf("expected 1 composite, got %d", len(data.Composites))
	}
	c := data.Composites[0]
	if c.GoName != "ArtifactDTO" {
		t.Errorf("expected ArtifactDTO, got %s", c.GoName)
	}
	if c.TypeName != "artifact" {
		t.Errorf("expected artifact, got %s", c.TypeName)
	}
	// Should have 3 fields (name, priority, story-points), deduplicated
	if len(c.Fields) != 3 {
		t.Errorf("expected 3 fields, got %d: %v", len(c.Fields), fieldNames(c.Fields))
	}
	// All composite fields should be pointers
	for _, f := range c.Fields {
		if !strings.HasPrefix(f.GoType, "*") {
			t.Errorf("composite field %s should be pointer, got %s", f.GoName, f.GoType)
		}
	}
}

func TestBuildDTOData_CustomInterfaceNames(t *testing.T) {
	schema := &ParsedSchema{
		Entities: []EntitySpec{{Name: "person"}},
	}
	data := BuildDTOData(schema, DTOConfig{
		PackageName:    "dto",
		EntityOutName:  "MyEntityOut",
		EntityPatchName: "MyEntityPatch",
	})

	if data.EntityOutName != "MyEntityOut" {
		t.Errorf("expected MyEntityOut, got %s", data.EntityOutName)
	}
	if data.EntityPatchName != "MyEntityPatch" {
		t.Errorf("expected MyEntityPatch, got %s", data.EntityPatchName)
	}
	// Defaults should still work for unset ones
	if data.EntityCreateName != "EntityCreate" {
		t.Errorf("expected EntityCreate default, got %s", data.EntityCreateName)
	}

	var buf bytes.Buffer
	if err := RenderDTO(&buf, data); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, "type MyEntityOut interface") {
		t.Error("expected MyEntityOut interface in output")
	}
	if !strings.Contains(out, "type MyEntityPatch interface") {
		t.Error("expected MyEntityPatch interface in output")
	}
}

func TestBuildDTOData_RelationCreateEmbed(t *testing.T) {
	schema := &ParsedSchema{
		Relations: []RelationSpec{
			{Name: "knows", Relates: []RelatesSpec{{Role: "knower"}, {Role: "known"}}},
		},
	}
	data := BuildDTOData(schema, DTOConfig{
		PackageName:         "dto",
		RelationCreateEmbed: "BaseRelCreate",
	})

	var buf bytes.Buffer
	if err := RenderDTO(&buf, data); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, "BaseRelCreate") {
		t.Error("expected BaseRelCreate embed in relation create struct")
	}
}

func TestRenderDTO_CompositeInOutput(t *testing.T) {
	schema := &ParsedSchema{
		Attributes: []AttributeSpec{
			{Name: "name", ValueType: "string"},
		},
		Entities: []EntitySpec{
			{Name: "task", Owns: []OwnsSpec{{Attribute: "name"}}},
		},
	}
	data := BuildDTOData(schema, DTOConfig{
		PackageName: "dto",
		CompositeEntities: []CompositeEntityConfig{
			{Name: "ArtifactDTO", Entities: []string{"task"}, TypeName: "artifact"},
		},
	})

	var buf bytes.Buffer
	if err := RenderDTO(&buf, data); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, "type ArtifactDTOOut struct") {
		t.Error("expected ArtifactDTOOut struct in output")
	}
	if !strings.Contains(out, `func (ArtifactDTOOut) TypeName()`) {
		t.Error("expected TypeName method on composite")
	}
}

// --- helpers ---

func findField(fields []dtoFieldCtx, goName string) *dtoFieldCtx {
	for i := range fields {
		if fields[i].GoName == goName {
			return &fields[i]
		}
	}
	return nil
}

func fieldNames(fields []dtoFieldCtx) []string {
	names := make([]string, len(fields))
	for i, f := range fields {
		names[i] = f.GoName
	}
	return names
}
