package tqlgen

import (
	"bytes"
	"strings"
	"testing"
)

func TestBuildRegistryData_EntityConstants(t *testing.T) {
	schema := &ParsedSchema{
		Entities: []EntitySpec{
			{Name: "artifact", Abstract: true},
			{Name: "persona"},
			{Name: "user_story"},
		},
	}
	data := BuildRegistryData(schema, RegistryConfig{SkipAbstract: true, UseAcronyms: true})

	if len(data.EntityConstants) != 2 {
		t.Fatalf("expected 2 entity constants, got %d", len(data.EntityConstants))
	}
	if data.EntityConstants[0].Name != "TypePersona" || data.EntityConstants[0].Value != "persona" {
		t.Errorf("unexpected first constant: %+v", data.EntityConstants[0])
	}
	if data.EntityConstants[1].Name != "TypeUserStory" {
		t.Errorf("unexpected second constant: %+v", data.EntityConstants[1])
	}
}

func TestBuildRegistryData_IncludesAbstract(t *testing.T) {
	schema := &ParsedSchema{
		Entities: []EntitySpec{
			{Name: "artifact", Abstract: true},
			{Name: "persona"},
		},
	}
	data := BuildRegistryData(schema, RegistryConfig{SkipAbstract: false})

	if len(data.EntityConstants) != 2 {
		t.Fatalf("expected 2 entity constants with SkipAbstract=false, got %d", len(data.EntityConstants))
	}
}

func TestBuildRegistryData_RelationConstants(t *testing.T) {
	schema := &ParsedSchema{
		Relations: []RelationSpec{
			{Name: "acts"},
			{Name: "satisfies"},
		},
	}
	data := BuildRegistryData(schema, RegistryConfig{UseAcronyms: true})

	if len(data.RelationConstants) != 2 {
		t.Fatalf("expected 2 relation constants, got %d", len(data.RelationConstants))
	}
	if data.RelationConstants[0].Name != "RelActs" {
		t.Errorf("expected RelActs, got %s", data.RelationConstants[0].Name)
	}
}

func TestBuildRegistryData_EntityParents(t *testing.T) {
	schema := &ParsedSchema{
		Entities: []EntitySpec{
			{Name: "artifact"},
			{Name: "persona", Parent: "artifact"},
		},
	}
	data := BuildRegistryData(schema, RegistryConfig{})

	if len(data.EntityParents) != 1 {
		t.Fatalf("expected 1 parent entry, got %d", len(data.EntityParents))
	}
	if data.EntityParents[0].Key != "persona" || data.EntityParents[0].Value != "artifact" {
		t.Errorf("unexpected parent: %+v", data.EntityParents[0])
	}
}

func TestBuildRegistryData_EntityAttributes(t *testing.T) {
	schema := &ParsedSchema{
		Attributes: []AttributeSpec{{Name: "name", ValueType: "string"}, {Name: "desc", ValueType: "string"}},
		Entities: []EntitySpec{
			{Name: "persona", Owns: []OwnsSpec{{Attribute: "desc"}, {Attribute: "name"}}},
		},
	}
	data := BuildRegistryData(schema, RegistryConfig{SkipAbstract: true})

	if len(data.EntityAttributes) != 1 {
		t.Fatalf("expected 1 entity attributes entry, got %d", len(data.EntityAttributes))
	}
	// Should be sorted
	if data.EntityAttributes[0].Values[0] != "desc" || data.EntityAttributes[0].Values[1] != "name" {
		t.Errorf("attributes not sorted: %v", data.EntityAttributes[0].Values)
	}
}

func TestBuildRegistryData_RelationSchema(t *testing.T) {
	schema := &ParsedSchema{
		Entities: []EntitySpec{
			{Name: "persona", Plays: []PlaysSpec{{Relation: "acts", Role: "actor"}}},
			{Name: "user_story", Plays: []PlaysSpec{{Relation: "acts", Role: "action"}}},
		},
		Relations: []RelationSpec{
			{Name: "acts", Relates: []RelatesSpec{{Role: "actor"}, {Role: "action"}}},
		},
	}
	data := BuildRegistryData(schema, RegistryConfig{})

	if len(data.RelationSchema) != 1 {
		t.Fatalf("expected 1 relation schema entry, got %d", len(data.RelationSchema))
	}
	rs := data.RelationSchema[0]
	if rs.Role0Name != "actor" || rs.Role1Name != "action" {
		t.Errorf("unexpected roles: %s, %s", rs.Role0Name, rs.Role1Name)
	}
	if len(rs.Role0Types) != 1 || rs.Role0Types[0] != "persona" {
		t.Errorf("unexpected role0 types: %v", rs.Role0Types)
	}
}

func TestBuildRegistryData_Enums(t *testing.T) {
	schema := &ParsedSchema{
		Attributes: []AttributeSpec{
			{Name: "status", ValueType: "string", Values: []string{"proposed", "accepted"}},
		},
	}
	data := BuildRegistryData(schema, RegistryConfig{Enums: true, UseAcronyms: true})

	if len(data.Enums) != 1 {
		t.Fatalf("expected 1 enum, got %d", len(data.Enums))
	}
	if data.Enums[0].Values[0].GoName != "StatusProposed" {
		t.Errorf("unexpected enum value name: %s", data.Enums[0].Values[0].GoName)
	}
}

func TestBuildRegistryData_SortedLists(t *testing.T) {
	schema := &ParsedSchema{
		Entities:  []EntitySpec{{Name: "zebra"}, {Name: "alpha"}},
		Relations: []RelationSpec{{Name: "zzz"}, {Name: "aaa"}},
	}
	data := BuildRegistryData(schema, RegistryConfig{})

	if data.AllEntityTypes[0] != "alpha" || data.AllEntityTypes[1] != "zebra" {
		t.Errorf("entities not sorted: %v", data.AllEntityTypes)
	}
	if data.AllRelationTypes[0] != "aaa" || data.AllRelationTypes[1] != "zzz" {
		t.Errorf("relations not sorted: %v", data.AllRelationTypes)
	}
}

func TestBuildRegistryData_CustomPrefixes(t *testing.T) {
	schema := &ParsedSchema{
		Entities:  []EntitySpec{{Name: "foo"}},
		Relations: []RelationSpec{{Name: "bar"}},
	}
	data := BuildRegistryData(schema, RegistryConfig{TypePrefix: "Entity", RelPrefix: "Relation"})

	if data.EntityConstants[0].Name != "EntityFoo" {
		t.Errorf("expected EntityFoo, got %s", data.EntityConstants[0].Name)
	}
	if data.RelationConstants[0].Name != "RelationBar" {
		t.Errorf("expected RelationBar, got %s", data.RelationConstants[0].Name)
	}
}

func TestRenderRegistry(t *testing.T) {
	schema := &ParsedSchema{
		Attributes: []AttributeSpec{
			{Name: "name", ValueType: "string"},
			{Name: "status", ValueType: "string", Values: []string{"proposed", "accepted"}},
		},
		Entities: []EntitySpec{
			{Name: "artifact", Abstract: true, Owns: []OwnsSpec{{Attribute: "name"}}},
			{Name: "persona", Parent: "artifact", Owns: []OwnsSpec{{Attribute: "status"}},
				Plays: []PlaysSpec{{Relation: "acts", Role: "actor"}}},
		},
		Relations: []RelationSpec{
			{Name: "acts", Relates: []RelatesSpec{{Role: "actor"}, {Role: "action"}},
				Owns: []OwnsSpec{{Attribute: "name"}}},
		},
	}
	schema.AccumulateInheritance()
	data := BuildRegistryData(schema, RegistryConfig{
		PackageName:  "graph",
		SkipAbstract: true,
		Enums:        true,
		UseAcronyms:  true,
	})

	var buf bytes.Buffer
	if err := RenderRegistry(&buf, data); err != nil {
		t.Fatal(err)
	}

	out := buf.String()

	checks := map[string]string{
		"package declaration":   "package graph",
		"entity constant":      `const TypePersona = "persona"`,
		"relation constant":    `const RelActs = "acts"`,
		"attribute value type":  `"name": "string"`,
		"entity parent":        `"persona": "artifact"`,
		"entity attributes":    `"persona":`,
		"attribute enum values": `"status":`,
		"enum constant":        `StatusProposed = "proposed"`,
		"relation schema":      `"acts":`,
		"relation attributes":  `RelationAttributes`,
		"RoleInfo type":        `type RoleInfo struct`,
		"AllEntityTypes":       `AllEntityTypes`,
		"AllRelationTypes":     `AllRelationTypes`,
	}
	for name, want := range checks {
		if !strings.Contains(out, want) {
			t.Errorf("missing %s: expected %q in output", name, want)
		}
	}

	// Abstract entity should NOT appear as a constant
	if strings.Contains(out, `TypeArtifact`) {
		t.Error("abstract entity should be skipped with SkipAbstract=true")
	}
}

func TestRenderRegistry_Compilable(t *testing.T) {
	// Verify the rendered output is valid Go by checking gofmt accepts it
	schema := &ParsedSchema{
		Attributes: []AttributeSpec{
			{Name: "name", ValueType: "string"},
			{Name: "age", ValueType: "integer"},
		},
		Entities: []EntitySpec{
			{Name: "person", Owns: []OwnsSpec{{Attribute: "name"}, {Attribute: "age"}}},
		},
		Relations: []RelationSpec{
			{Name: "knows", Relates: []RelatesSpec{{Role: "knower"}, {Role: "known"}}},
		},
	}
	data := BuildRegistryData(schema, RegistryConfig{PackageName: "testpkg"})

	var buf bytes.Buffer
	if err := RenderRegistry(&buf, data); err != nil {
		t.Fatal(err)
	}

	// Verify it starts with the generation header
	out := buf.String()
	if !strings.HasPrefix(out, "// Code generated by tqlgen; DO NOT EDIT.") {
		t.Error("missing generation header")
	}
}

func TestExtractAnnotations_ParenSyntax(t *testing.T) {
	input := `# @prefix(PROJ)
entity project;
# @internal
entity secret_thing;`

	result := ExtractAnnotations(input)

	if result["project"]["prefix"] != "PROJ" {
		t.Errorf("expected prefix PROJ, got %q", result["project"]["prefix"])
	}
	if result["secret_thing"]["internal"] != "" {
		t.Errorf("expected empty value for bare @internal, got %q", result["secret_thing"]["internal"])
	}
}

func TestFilterMostSpecific_Duplicates(t *testing.T) {
	schema := &ParsedSchema{
		Entities: []EntitySpec{
			{Name: "persona", Plays: []PlaysSpec{{Relation: "links", Role: "source"}, {Relation: "links", Role: "target"}}},
			{Name: "artifact", Plays: []PlaysSpec{{Relation: "links", Role: "target"}}},
		},
		Relations: []RelationSpec{
			{Name: "links", Relates: []RelatesSpec{{Role: "source"}, {Role: "target"}}},
		},
	}
	data := BuildRegistryData(schema, RegistryConfig{})

	rs := data.RelationSchema[0]
	// "target" role played by persona and artifact — persona should appear only once
	for _, types := range [][]string{rs.Role0Types, rs.Role1Types} {
		seen := map[string]int{}
		for _, pt := range types {
			seen[pt]++
			if seen[pt] > 1 {
				t.Errorf("duplicate player type %q in role types", pt)
			}
		}
	}
	if len(rs.Role1Types) != 2 {
		t.Errorf("expected 2 target player types, got %d: %v", len(rs.Role1Types), rs.Role1Types)
	}
}

func TestBuildRegistryData_RelationAttributes(t *testing.T) {
	schema := &ParsedSchema{
		Attributes: []AttributeSpec{
			{Name: "weight", ValueType: "double"},
			{Name: "label", ValueType: "string"},
		},
		Relations: []RelationSpec{
			{Name: "links", Owns: []OwnsSpec{{Attribute: "weight"}, {Attribute: "label"}}},
			{Name: "empty_rel"},
		},
	}
	data := BuildRegistryData(schema, RegistryConfig{})

	if len(data.RelationAttrs) != 1 {
		t.Fatalf("expected 1 relation attrs entry, got %d", len(data.RelationAttrs))
	}
	if data.RelationAttrs[0].Key != "links" {
		t.Errorf("expected key 'links', got %q", data.RelationAttrs[0].Key)
	}
	// Should be sorted
	if data.RelationAttrs[0].Values[0] != "label" || data.RelationAttrs[0].Values[1] != "weight" {
		t.Errorf("relation attrs not sorted: %v", data.RelationAttrs[0].Values)
	}
}

func TestToRegistryConst_NoAcronyms(t *testing.T) {
	got := toRegistryConst("Type", "user_id", false)
	if got != "TypeUserId" {
		t.Errorf("expected TypeUserId, got %s", got)
	}
	gotAcro := toRegistryConst("Type", "user_id", true)
	if gotAcro != "TypeUserID" {
		t.Errorf("expected TypeUserID, got %s", gotAcro)
	}
}
