package tqlgen

import (
	"bytes"
	"go/format"
	"strings"
	"testing"
)

// TestBuildDTOData_EmptyPackage is a regression test for issue #108: a
// missing PackageName used to silently produce empty data and an invalid
// generated file starting with "package ". The builder now populates the
// data regardless, and RenderDTO fails loudly.
func TestBuildDTOData_EmptyPackage(t *testing.T) {
	schema := &ParsedSchema{
		Entities: []EntitySpec{{Name: "person"}},
	}
	data := BuildDTOData(schema, DTOConfig{})
	if data.PackageName != "" {
		t.Errorf("expected empty, got %q", data.PackageName)
	}
	if len(data.Entities) != 1 {
		t.Errorf("expected data to be populated despite the missing package name, got %d entities", len(data.Entities))
	}

	err := RenderDTO(&bytes.Buffer{}, data)
	if err == nil {
		t.Fatal("RenderDTO succeeded with empty PackageName, want error")
	}
	if !strings.Contains(err.Error(), "PackageName") {
		t.Errorf("error %q should mention PackageName", err)
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
	if err := schema.AccumulateInheritance(); err != nil {
		t.Fatalf("AccumulateInheritance failed: %v", err)
	}

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
		"TypeName method": `func (PersonOut) TypeName()`,
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
		PackageName:     "dto",
		EntityOutName:   "MyEntityOut",
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

// TestRenderDTO_BaseStructPatchSinglePointer is a regression test for issue
// #79: the Patch template block used to prepend another "*" to the already
// pointered base-struct Out fields, emitting **string.
func TestRenderDTO_BaseStructPatchSinglePointer(t *testing.T) {
	schema := &ParsedSchema{
		Attributes: []AttributeSpec{
			{Name: "name", ValueType: "string"},
		},
		Entities: []EntitySpec{
			{Name: "artifact", Abstract: true, Owns: []OwnsSpec{{Attribute: "name", Key: true}}},
			{Name: "task", Parent: "artifact", Owns: []OwnsSpec{{Attribute: "name", Key: true}}},
		},
	}
	if err := schema.AccumulateInheritance(); err != nil {
		t.Fatalf("AccumulateInheritance failed: %v", err)
	}
	data := BuildDTOData(schema, DTOConfig{
		PackageName:  "dto",
		SkipAbstract: true,
		UseAcronyms:  true,
		BaseStructs: []BaseStructConfig{
			{
				SourceEntity:   "artifact",
				BaseName:       "BaseArtifact",
				InheritedAttrs: []string{"name"},
				ExtraFields:    map[string]string{"extra": "string", "note": "*string"},
			},
		},
	})

	var buf bytes.Buffer
	if err := RenderDTO(&buf, data); err != nil {
		t.Fatal(err)
	}
	out := buf.String()

	if strings.Contains(out, "**") {
		t.Errorf("generated DTOs contain a double pointer\n%s", out)
	}
	for _, want := range []string{
		"type BaseArtifactPatch struct {",
		"Name *string `json:\"name\"`",
		"Extra *string `json:\"extra\"`", // extra field gains exactly one pointer
		"Note *string `json:\"note\"`",   // already-pointer extra field stays single
	} {
		if !containsCode(out, want) {
			t.Errorf("missing %q in generated DTOs\n%s", want, out)
		}
	}

	compileGenerated(t, out)
}

// TestBuildDTOData_MultiValuedAttributes covers the DTO half of issue #75:
// list ownerships (owns attr[]) and cardinalities allowing more than one
// value must generate slice fields in Out/Create/Patch, not scalar pointers.
func TestBuildDTOData_MultiValuedAttributes(t *testing.T) {
	schema := &ParsedSchema{
		Attributes: []AttributeSpec{
			{Name: "tag", ValueType: "string"},
			{Name: "alias", ValueType: "string"},
			{Name: "nick", ValueType: "string"},
			{Name: "score", ValueType: "integer"},
		},
		Entities: []EntitySpec{
			{Name: "person", Owns: []OwnsSpec{
				{Attribute: "tag", Card: "0..5"},
				{Attribute: "alias", IsList: true},
				{Attribute: "nick", Card: "0..1"},
			}},
		},
		Relations: []RelationSpec{
			{Name: "rated",
				Relates: []RelatesSpec{{Role: "rater"}},
				Owns:    []OwnsSpec{{Attribute: "score", Card: "1.."}},
			},
		},
	}
	data := BuildDTOData(schema, DTOConfig{PackageName: "dto", UseAcronyms: true})

	e := data.Entities[0]
	for _, variant := range []struct {
		name   string
		fields []dtoFieldCtx
	}{
		{"Out", e.OutFields}, {"Create", e.CreateFields}, {"Patch", e.PatchFields},
	} {
		if f := findField(variant.fields, "Tag"); f == nil || f.GoType != "[]string" {
			t.Errorf("%s Tag: expected []string, got %+v", variant.name, f)
		}
		if f := findField(variant.fields, "Alias"); f == nil || f.GoType != "[]string" {
			t.Errorf("%s Alias: expected []string, got %+v", variant.name, f)
		}
		if f := findField(variant.fields, "Nick"); f == nil || f.GoType != "*string" {
			t.Errorf("%s Nick: expected *string, got %+v", variant.name, f)
		}
	}

	r := data.Relations[0]
	if f := findField(r.OutFields, "Score"); f == nil || f.GoType != "[]int64" {
		t.Errorf("relation Out Score: expected []int64, got %+v", f)
	}
	if f := findField(r.CreateFields, "Score"); f == nil || f.GoType != "[]int64" {
		t.Errorf("relation Create Score: expected []int64, got %+v", f)
	}

	var buf bytes.Buffer
	if err := RenderDTO(&buf, data); err != nil {
		t.Fatal(err)
	}
	compileGenerated(t, buf.String())
}

// TestRenderDTO_OutputIsGofmtFormatted is the DTO half of issue #107.
func TestRenderDTO_OutputIsGofmtFormatted(t *testing.T) {
	schema := &ParsedSchema{
		Attributes: []AttributeSpec{
			{Name: "name", ValueType: "string"},
			{Name: "created-at", ValueType: "datetime"},
		},
		Entities: []EntitySpec{
			{Name: "person", Owns: []OwnsSpec{{Attribute: "name", Key: true}, {Attribute: "created-at"}}},
		},
		Relations: []RelationSpec{
			{Name: "knows", Relates: []RelatesSpec{{Role: "knower"}, {Role: "known"}}},
		},
	}
	data := BuildDTOData(schema, DTOConfig{PackageName: "dto", UseAcronyms: true})

	var buf bytes.Buffer
	if err := RenderDTO(&buf, data); err != nil {
		t.Fatal(err)
	}
	out := buf.String()

	formatted, err := format.Source(buf.Bytes())
	if err != nil {
		t.Fatalf("generated DTO code is not valid Go: %v\n%s", err, out)
	}
	if string(formatted) != out {
		t.Errorf("RenderDTO output is not gofmt-formatted:\n--- got ---\n%s\n--- want ---\n%s", out, formatted)
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

// TestBuildDTOData_Structs covers the DTO half of TypeQL struct support:
// parsed struct definitions are emitted as plain Go value structs (no
// Out/Create/Patch triple), optional fields become pointers, and attributes
// whose value type is a struct reference the generated struct type instead of
// defaulting to string.
func TestBuildDTOData_Structs(t *testing.T) {
	schema := &ParsedSchema{
		Attributes: []AttributeSpec{
			{Name: "name", ValueType: "string"},
			{Name: "home-address", ValueType: "address"},
		},
		Entities: []EntitySpec{
			{Name: "person", Owns: []OwnsSpec{
				{Attribute: "name", Key: true},
				{Attribute: "home-address"},
			}},
		},
		Structs: []StructSpec{
			{Name: "contact", Fields: []StructFieldSpec{
				{Name: "home", ValueType: "address", Optional: true},
			}},
			{Name: "address", Fields: []StructFieldSpec{
				{Name: "street", ValueType: "string"},
				{Name: "zip", ValueType: "integer", Optional: true},
				{Name: "since", ValueType: "datetime", Optional: true},
			}},
		},
	}
	data := BuildDTOData(schema, DTOConfig{PackageName: "dto", UseAcronyms: true})

	if len(data.Structs) != 2 {
		t.Fatalf("expected 2 structs, got %d", len(data.Structs))
	}
	// Sorted by name: address before contact.
	if data.Structs[0].GoName != "Address" || data.Structs[1].GoName != "Contact" {
		t.Errorf("structs not sorted by name: %q, %q", data.Structs[0].GoName, data.Structs[1].GoName)
	}
	if !data.NeedsTime {
		t.Error("expected NeedsTime for the datetime struct field")
	}

	var buf bytes.Buffer
	if err := RenderDTO(&buf, data); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{
		`// Address is generated from TypeQL struct "address".`,
		"type Address struct {",
		"Street string `json:\"street\"`",
		"Zip *int64 `json:\"zip\"`",
		"Since *time.Time `json:\"since\"`",
		"type Contact struct {",
		"Home *Address `json:\"home\"`",                // struct-typed field references the generated struct
		"HomeAddress *Address `json:\"home-address\"`", // struct-valued attribute on the entity
	} {
		if !containsCode(out, want) {
			t.Errorf("missing %q in generated DTOs\n%s", want, out)
		}
	}

	compileGenerated(t, out)
}

// TestBuildDTOData_ListRoles covers the DTO half of list-role support: list
// roles (relates role[]) and roles whose cardinality allows more than one
// player carry a slice of IIDs/IDs in the relation Out/Create DTOs, while
// plain roles keep the scalar treatment.
func TestBuildDTOData_ListRoles(t *testing.T) {
	schema := &ParsedSchema{
		Attributes: []AttributeSpec{{Name: "name", ValueType: "string"}},
		Entities: []EntitySpec{
			{Name: "person", Owns: []OwnsSpec{{Attribute: "name", Key: true}}},
		},
		Relations: []RelationSpec{
			{Name: "team", Relates: []RelatesSpec{
				{Role: "member", IsList: true},
				{Role: "leader"},
				{Role: "participant", Card: "0.."},
			}},
		},
	}
	data := BuildDTOData(schema, DTOConfig{PackageName: "dto", UseAcronyms: true})

	var buf bytes.Buffer
	if err := RenderDTO(&buf, data); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{
		"MemberID []string `json:\"member_id\"`",           // list role, both Out and Create
		"LeaderID *string `json:\"leader_id\"`",            // plain role in Out
		"LeaderID string `json:\"leader_id\"`",             // plain role in Create
		"ParticipantID []string `json:\"participant_id\"`", // many-cardinality role
	} {
		if !containsCode(out, want) {
			t.Errorf("missing %q in generated DTOs\n%s", want, out)
		}
	}

	compileGenerated(t, out)
}
