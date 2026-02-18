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
	data := BuildRegistryData(schema, RegistryConfig{PackageName: "g", SkipAbstract: true, UseAcronyms: true})

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
	data := BuildRegistryData(schema, RegistryConfig{PackageName: "g", SkipAbstract: false})

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
	data := BuildRegistryData(schema, RegistryConfig{PackageName: "g", UseAcronyms: true})

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
	data := BuildRegistryData(schema, RegistryConfig{PackageName: "g"})

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
	data := BuildRegistryData(schema, RegistryConfig{PackageName: "g", SkipAbstract: true})

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
	data := BuildRegistryData(schema, RegistryConfig{PackageName: "g"})

	if len(data.RelationSchema) != 1 {
		t.Fatalf("expected 1 relation schema entry, got %d", len(data.RelationSchema))
	}
	rs := data.RelationSchema[0]
	if len(rs.Roles) != 2 {
		t.Fatalf("expected 2 roles, got %d", len(rs.Roles))
	}
	if rs.Roles[0].RoleName != "actor" || rs.Roles[1].RoleName != "action" {
		t.Errorf("unexpected roles: %s, %s", rs.Roles[0].RoleName, rs.Roles[1].RoleName)
	}
	if len(rs.Roles[0].PlayerTypes) != 1 || rs.Roles[0].PlayerTypes[0] != "persona" {
		t.Errorf("unexpected role0 types: %v", rs.Roles[0].PlayerTypes)
	}
}

func TestBuildRegistryData_RelationSchemaCard(t *testing.T) {
	schema := &ParsedSchema{
		Entities: []EntitySpec{
			{Name: "person", Plays: []PlaysSpec{{Relation: "marriage", Role: "spouse"}}},
		},
		Relations: []RelationSpec{
			{Name: "marriage", Relates: []RelatesSpec{
				{Role: "spouse", Card: "2"},
			}},
		},
	}
	data := BuildRegistryData(schema, RegistryConfig{PackageName: "g"})
	if len(data.RelationSchema) != 1 {
		t.Fatalf("expected 1 relation schema, got %d", len(data.RelationSchema))
	}
	role := data.RelationSchema[0].Roles[0]
	if role.Card != "2" {
		t.Errorf("expected Card=%q, got %q", "2", role.Card)
	}
}

func TestCardMin(t *testing.T) {
	tests := []struct {
		input string
		want  int
	}{
		{"", 0},
		{"1", 1},
		{"0..1", 0},
		{"1..", 1},
		{"0..", 0},
		{"2", 2},
		{"2..5", 2},
		{"bad", 0},
	}
	for _, tc := range tests {
		got := cardMin(tc.input)
		if got != tc.want {
			t.Errorf("cardMin(%q) = %d, want %d", tc.input, got, tc.want)
		}
	}
}

func TestRenderRegistry_MinCard(t *testing.T) {
	schema := &ParsedSchema{
		Entities: []EntitySpec{
			{Name: "person", Plays: []PlaysSpec{{Relation: "marriage", Role: "spouse"}}},
		},
		Relations: []RelationSpec{
			{Name: "marriage", Relates: []RelatesSpec{
				{Role: "spouse", Card: "1.."},
			}},
		},
	}
	data := BuildRegistryData(schema, RegistryConfig{PackageName: "test"})
	var buf strings.Builder
	if err := RenderRegistry(&buf, data); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	// The generated code should contain MinCard=1 for the "spouse" role
	if !strings.Contains(out, `"spouse", []string{"person"}, 1`) {
		t.Errorf("expected MinCard=1 for spouse role in output, got:\n%s", out)
	}
}

func TestBuildRegistryData_RelationSchemaThreeRoles(t *testing.T) {
	schema := &ParsedSchema{
		Entities: []EntitySpec{
			{Name: "alice", Plays: []PlaysSpec{{Relation: "trio", Role: "first"}}},
			{Name: "bob", Plays: []PlaysSpec{{Relation: "trio", Role: "second"}}},
			{Name: "carol", Plays: []PlaysSpec{{Relation: "trio", Role: "third"}}},
		},
		Relations: []RelationSpec{
			{Name: "trio", Relates: []RelatesSpec{{Role: "first"}, {Role: "second"}, {Role: "third"}}},
		},
	}
	data := BuildRegistryData(schema, RegistryConfig{PackageName: "g"})

	if len(data.RelationSchema) != 1 {
		t.Fatalf("expected 1 relation schema entry, got %d", len(data.RelationSchema))
	}
	rs := data.RelationSchema[0]
	if len(rs.Roles) != 3 {
		t.Fatalf("expected 3 roles, got %d", len(rs.Roles))
	}
	if rs.Roles[2].RoleName != "third" {
		t.Errorf("expected third role 'third', got %s", rs.Roles[2].RoleName)
	}
	if len(rs.Roles[2].PlayerTypes) != 1 || rs.Roles[2].PlayerTypes[0] != "carol" {
		t.Errorf("unexpected third role types: %v", rs.Roles[2].PlayerTypes)
	}
}

func TestBuildRegistryData_SingleRole(t *testing.T) {
	schema := &ParsedSchema{
		Entities: []EntitySpec{
			{Name: "foo", Plays: []PlaysSpec{{Relation: "self-ref", Role: "member"}}},
		},
		Relations: []RelationSpec{
			{Name: "self-ref", Relates: []RelatesSpec{{Role: "member"}}},
		},
	}
	data := BuildRegistryData(schema, RegistryConfig{PackageName: "g"})

	if len(data.RelationSchema) != 1 {
		t.Fatalf("expected 1 relation schema entry, got %d", len(data.RelationSchema))
	}
	if len(data.RelationSchema[0].Roles) != 1 {
		t.Errorf("expected 1 role, got %d", len(data.RelationSchema[0].Roles))
	}
}

func TestBuildRegistryData_Enums(t *testing.T) {
	schema := &ParsedSchema{
		Attributes: []AttributeSpec{
			{Name: "status", ValueType: "string", Values: []string{"proposed", "accepted"}},
		},
	}
	data := BuildRegistryData(schema, RegistryConfig{PackageName: "g", Enums: true, UseAcronyms: true})

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
	data := BuildRegistryData(schema, RegistryConfig{PackageName: "g"})

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
	data := BuildRegistryData(schema, RegistryConfig{PackageName: "g", TypePrefix: "Entity", RelPrefix: "Relation"})

	if data.EntityConstants[0].Name != "EntityFoo" {
		t.Errorf("expected EntityFoo, got %s", data.EntityConstants[0].Name)
	}
	if data.RelationConstants[0].Name != "RelationBar" {
		t.Errorf("expected RelationBar, got %s", data.RelationConstants[0].Name)
	}
}

func TestBuildRegistryData_EmptyPackageName(t *testing.T) {
	schema := &ParsedSchema{
		Entities: []EntitySpec{{Name: "foo"}},
	}
	data := BuildRegistryData(schema, RegistryConfig{})
	if data.PackageName != "" {
		t.Errorf("expected empty package name, got %q", data.PackageName)
	}
	if len(data.EntityConstants) != 0 {
		t.Errorf("expected no data with empty package name")
	}
}

func TestBuildRegistryData_EntityKeys(t *testing.T) {
	schema := &ParsedSchema{
		Attributes: []AttributeSpec{{Name: "email", ValueType: "string"}, {Name: "name", ValueType: "string"}},
		Entities: []EntitySpec{
			{Name: "person", Owns: []OwnsSpec{{Attribute: "email", Key: true}, {Attribute: "name"}}},
			{Name: "thing", Owns: []OwnsSpec{{Attribute: "name"}}},
		},
	}
	data := BuildRegistryData(schema, RegistryConfig{PackageName: "g"})

	if len(data.EntityKeys) != 1 {
		t.Fatalf("expected 1 entity key entry, got %d", len(data.EntityKeys))
	}
	if data.EntityKeys[0].Key != "person" {
		t.Errorf("expected key for 'person', got %q", data.EntityKeys[0].Key)
	}
	if len(data.EntityKeys[0].Values) != 1 || data.EntityKeys[0].Values[0] != "email" {
		t.Errorf("unexpected key values: %v", data.EntityKeys[0].Values)
	}
}

func TestBuildRegistryData_EntityAbstract(t *testing.T) {
	schema := &ParsedSchema{
		Entities: []EntitySpec{
			{Name: "artifact", Abstract: true},
			{Name: "base", Abstract: true},
			{Name: "person"},
		},
	}
	data := BuildRegistryData(schema, RegistryConfig{PackageName: "g"})

	if len(data.EntityAbstract) != 2 {
		t.Fatalf("expected 2 abstract entities, got %d", len(data.EntityAbstract))
	}
	if data.EntityAbstract[0] != "artifact" || data.EntityAbstract[1] != "base" {
		t.Errorf("unexpected abstract entities: %v", data.EntityAbstract)
	}
}

func TestBuildRegistryData_RelationAbstract(t *testing.T) {
	schema := &ParsedSchema{
		Relations: []RelationSpec{
			{Name: "base-rel", Abstract: true},
			{Name: "concrete"},
		},
	}
	data := BuildRegistryData(schema, RegistryConfig{PackageName: "g"})

	if len(data.RelationAbstract) != 1 {
		t.Fatalf("expected 1 abstract relation, got %d", len(data.RelationAbstract))
	}
	if data.RelationAbstract[0] != "base-rel" {
		t.Errorf("expected 'base-rel', got %q", data.RelationAbstract[0])
	}
}

func TestBuildRegistryData_RelationParents(t *testing.T) {
	schema := &ParsedSchema{
		Relations: []RelationSpec{
			{Name: "base-rel"},
			{Name: "child-rel", Parent: "base-rel"},
		},
	}
	data := BuildRegistryData(schema, RegistryConfig{PackageName: "g"})

	if len(data.RelationParents) != 1 {
		t.Fatalf("expected 1 relation parent entry, got %d", len(data.RelationParents))
	}
	if data.RelationParents[0].Key != "child-rel" || data.RelationParents[0].Value != "base-rel" {
		t.Errorf("unexpected relation parent: %+v", data.RelationParents[0])
	}
}

func TestBuildRegistryData_SchemaHash(t *testing.T) {
	data := BuildRegistryData(&ParsedSchema{}, RegistryConfig{
		PackageName: "g",
		SchemaText:  "define entity foo;",
	})

	if data.SchemaHash == "" {
		t.Fatal("expected non-empty schema hash")
	}
	if !strings.HasPrefix(data.SchemaHash, "sha256:") {
		t.Errorf("expected sha256: prefix, got %q", data.SchemaHash)
	}
	if len(data.SchemaHash) != len("sha256:")+16 {
		t.Errorf("expected 16 hex chars after prefix, got %q", data.SchemaHash)
	}
}

func TestBuildRegistryData_AttributeTypes(t *testing.T) {
	schema := &ParsedSchema{
		Attributes: []AttributeSpec{
			{Name: "z-attr", ValueType: "string"},
			{Name: "a-attr", ValueType: "long"},
		},
	}
	data := BuildRegistryData(schema, RegistryConfig{PackageName: "g"})

	if len(data.AttributeTypes) != 2 {
		t.Fatalf("expected 2 attribute types, got %d", len(data.AttributeTypes))
	}
	if data.AttributeTypes[0] != "a-attr" || data.AttributeTypes[1] != "z-attr" {
		t.Errorf("attribute types not sorted: %v", data.AttributeTypes)
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
		"AllAttributeTypes":    `AllAttributeTypes`,
		"EntityKeys":           `EntityKeys`,
		"EntityAbstract":       `EntityAbstract`,
		"RelationAbstract":     `RelationAbstract`,
		"RelationParents":      `RelationParents`,
		"GetEntityKeys":        `func GetEntityKeys`,
		"IsAbstractEntity":     `func IsAbstractEntity`,
		"GetRolePlayers":       `func GetRolePlayers`,
		"GetEntityAttributes":  `func GetEntityAttributes`,
		"GetRelationAttributes": `func GetRelationAttributes`,
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

	// Relation schema should use []RoleInfo not [2]RoleInfo
	if strings.Contains(out, `[2]RoleInfo`) {
		t.Error("relation schema should use []RoleInfo, not [2]RoleInfo")
	}
}

func TestRenderRegistry_Compilable(t *testing.T) {
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

	out := buf.String()
	if !strings.HasPrefix(out, "// Code generated by tqlgen; DO NOT EDIT.") {
		t.Error("missing generation header")
	}
}

func TestRenderRegistry_WithSchemaHash(t *testing.T) {
	data := BuildRegistryData(&ParsedSchema{
		Entities: []EntitySpec{{Name: "foo"}},
	}, RegistryConfig{
		PackageName: "g",
		SchemaText:  "define entity foo;",
	})

	var buf bytes.Buffer
	if err := RenderRegistry(&buf, data); err != nil {
		t.Fatal(err)
	}

	out := buf.String()
	if !strings.Contains(out, "SchemaHash") {
		t.Error("expected SchemaHash constant in output")
	}
	if !strings.Contains(out, "sha256:") {
		t.Error("expected sha256: prefix in SchemaHash")
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
	data := BuildRegistryData(schema, RegistryConfig{PackageName: "g"})

	rs := data.RelationSchema[0]
	for _, role := range rs.Roles {
		seen := map[string]int{}
		for _, pt := range role.PlayerTypes {
			seen[pt]++
			if seen[pt] > 1 {
				t.Errorf("duplicate player type %q in role %s", pt, role.RoleName)
			}
		}
	}
	// target role should have both persona and artifact
	targetRole := rs.Roles[1]
	if len(targetRole.PlayerTypes) != 2 {
		t.Errorf("expected 2 target player types, got %d: %v", len(targetRole.PlayerTypes), targetRole.PlayerTypes)
	}
}

func TestFilterMostSpecific_RemovesAncestors(t *testing.T) {
	schema := &ParsedSchema{
		Entities: []EntitySpec{
			{Name: "animal", Plays: []PlaysSpec{{Relation: "eats", Role: "eater"}}},
			{Name: "dog", Parent: "animal", Plays: []PlaysSpec{{Relation: "eats", Role: "eater"}}},
		},
		Relations: []RelationSpec{
			{Name: "eats", Relates: []RelatesSpec{{Role: "eater"}, {Role: "food"}}},
		},
	}
	data := BuildRegistryData(schema, RegistryConfig{PackageName: "g"})

	rs := data.RelationSchema[0]
	eaterRole := rs.Roles[0]
	// Should only have "dog", not "animal" (ancestor filtered out)
	if len(eaterRole.PlayerTypes) != 1 {
		t.Fatalf("expected 1 player type after filtering, got %d: %v", len(eaterRole.PlayerTypes), eaterRole.PlayerTypes)
	}
	if eaterRole.PlayerTypes[0] != "dog" {
		t.Errorf("expected 'dog', got %q", eaterRole.PlayerTypes[0])
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
	data := BuildRegistryData(schema, RegistryConfig{PackageName: "g"})

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

func TestBuildRegistryData_SchemaVersion(t *testing.T) {
	data := BuildRegistryData(&ParsedSchema{}, RegistryConfig{
		PackageName:   "g",
		SchemaVersion: "v2.3.1",
	})
	if data.SchemaVersion != "v2.3.1" {
		t.Errorf("expected v2.3.1, got %q", data.SchemaVersion)
	}
}

func TestBuildRegistryData_Annotations(t *testing.T) {
	schemaText := `define
# @prefix(PROJ)
entity project,
    owns name @key;
# @internal
entity secret-thing;
# @description A link
relation links,
    relates source,
    relates target;
attribute name, value string;
`
	schema, err := ParseSchema(schemaText)
	if err != nil {
		t.Fatal(err)
	}
	data := BuildRegistryData(schema, RegistryConfig{
		PackageName: "g",
		SchemaText:  schemaText,
	})

	if len(data.EntityAnnotations) != 2 {
		t.Fatalf("expected 2 entity annotations, got %d", len(data.EntityAnnotations))
	}
	// Check project has prefix=PROJ
	found := false
	for _, ea := range data.EntityAnnotations {
		if ea.Key == "project" {
			for _, kv := range ea.Values {
				if kv.Key == "prefix" && kv.Value == "PROJ" {
					found = true
				}
			}
		}
	}
	if !found {
		t.Error("expected project to have prefix=PROJ annotation")
	}
	if len(data.RelationAnnotations) != 1 {
		t.Fatalf("expected 1 relation annotation, got %d", len(data.RelationAnnotations))
	}
}

func TestBuildRegistryData_TypedConstants(t *testing.T) {
	schema := &ParsedSchema{
		Attributes: []AttributeSpec{{Name: "name", ValueType: "string"}},
		Entities:   []EntitySpec{{Name: "foo"}},
		Relations:  []RelationSpec{{Name: "bar"}},
	}
	data := BuildRegistryData(schema, RegistryConfig{
		PackageName:    "g",
		TypedConstants: true,
		UseAcronyms:    true,
	})
	if !data.TypedConstants {
		t.Error("expected TypedConstants=true")
	}
	if len(data.AttributeTypeConstants) != 1 {
		t.Fatalf("expected 1 attribute type constant, got %d", len(data.AttributeTypeConstants))
	}
	if data.AttributeTypeConstants[0].Name != "AttrName" {
		t.Errorf("expected AttrName, got %s", data.AttributeTypeConstants[0].Name)
	}

	var buf bytes.Buffer
	if err := RenderRegistry(&buf, data); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, "type EntityType string") {
		t.Error("expected typed EntityType declaration")
	}
	if !strings.Contains(out, "type RelationType string") {
		t.Error("expected typed RelationType declaration")
	}
	if !strings.Contains(out, "type AttributeType string") {
		t.Error("expected typed AttributeType declaration")
	}
	if !strings.Contains(out, "EntityType =") {
		t.Error("expected typed entity constant")
	}
	if !strings.Contains(out, "AttributeType =") {
		t.Error("expected typed attribute constant")
	}
}

func TestBuildRegistryData_JSONSchema(t *testing.T) {
	schema := &ParsedSchema{
		Attributes: []AttributeSpec{
			{Name: "name", ValueType: "string"},
			{Name: "age", ValueType: "long"},
			{Name: "active", ValueType: "boolean"},
		},
		Entities: []EntitySpec{
			{Name: "person", Owns: []OwnsSpec{
				{Attribute: "name", Key: true},
				{Attribute: "age"},
				{Attribute: "active"},
			}},
		},
	}
	data := BuildRegistryData(schema, RegistryConfig{
		PackageName: "g",
		JSONSchema:  true,
	})
	if len(data.EntityJSONSchema) != 1 {
		t.Fatalf("expected 1 JSON schema, got %d", len(data.EntityJSONSchema))
	}
	js := data.EntityJSONSchema[0]
	if js.TypeName != "person" {
		t.Errorf("expected 'person', got %q", js.TypeName)
	}
	if len(js.Properties) != 3 {
		t.Fatalf("expected 3 properties, got %d", len(js.Properties))
	}
	if len(js.Required) != 1 || js.Required[0] != "name" {
		t.Errorf("expected required=[name], got %v", js.Required)
	}
	// Check type mapping
	for _, p := range js.Properties {
		switch p.Name {
		case "name":
			if p.JSONType != "string" {
				t.Errorf("name should be string, got %s", p.JSONType)
			}
		case "age":
			if p.JSONType != "integer" {
				t.Errorf("age should be integer, got %s", p.JSONType)
			}
		case "active":
			if p.JSONType != "boolean" {
				t.Errorf("active should be boolean, got %s", p.JSONType)
			}
		}
	}
}

func TestRenderRegistry_SchemaVersion(t *testing.T) {
	data := BuildRegistryData(&ParsedSchema{
		Entities: []EntitySpec{{Name: "foo"}},
	}, RegistryConfig{
		PackageName:   "g",
		SchemaVersion: "v1.0.0",
		SchemaText:    "define entity foo;",
	})

	var buf bytes.Buffer
	if err := RenderRegistry(&buf, data); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, `SchemaVersion = "v1.0.0"`) {
		t.Error("expected SchemaVersion constant")
	}
	if !strings.Contains(out, "SchemaHash") {
		t.Error("expected SchemaHash constant")
	}
}

func TestRenderRegistry_GetRoleInfo(t *testing.T) {
	schema := &ParsedSchema{
		Entities: []EntitySpec{
			{Name: "person", Plays: []PlaysSpec{{Relation: "knows", Role: "knower"}}},
		},
		Relations: []RelationSpec{
			{Name: "knows", Relates: []RelatesSpec{{Role: "knower"}, {Role: "known"}}},
		},
	}
	data := BuildRegistryData(schema, RegistryConfig{PackageName: "g"})

	var buf bytes.Buffer
	if err := RenderRegistry(&buf, data); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, "func GetRoleInfo(") {
		t.Error("expected GetRoleInfo convenience function")
	}
}

func TestRenderRegistry_Annotations(t *testing.T) {
	schemaText := `define
# @prefix(PROJ)
entity project,
    owns name @key;
attribute name, value string;
`
	schema, err := ParseSchema(schemaText)
	if err != nil {
		t.Fatal(err)
	}
	data := BuildRegistryData(schema, RegistryConfig{
		PackageName: "g",
		SchemaText:  schemaText,
	})

	var buf bytes.Buffer
	if err := RenderRegistry(&buf, data); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, "EntityAnnotations") {
		t.Error("expected EntityAnnotations map in output")
	}
	if !strings.Contains(out, `"project"`) {
		t.Error("expected project key in annotations")
	}
}

func TestRenderRegistry_JSONSchema(t *testing.T) {
	schema := &ParsedSchema{
		Attributes: []AttributeSpec{
			{Name: "name", ValueType: "string"},
		},
		Entities: []EntitySpec{
			{Name: "person", Owns: []OwnsSpec{{Attribute: "name", Key: true}}},
		},
	}
	data := BuildRegistryData(schema, RegistryConfig{
		PackageName: "g",
		JSONSchema:  true,
	})

	var buf bytes.Buffer
	if err := RenderRegistry(&buf, data); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, "EntityTypeJSONSchema") {
		t.Error("expected EntityTypeJSONSchema in output")
	}
	if !strings.Contains(out, `"person"`) {
		t.Error("expected person in JSON schema")
	}
}

func TestToRegistryConst_Hyphens(t *testing.T) {
	got := toRegistryConst("Type", "user-story", false)
	if got != "TypeUserStory" {
		t.Errorf("expected TypeUserStory, got %s", got)
	}
	got2 := toRegistryConst("Rel", "has-id", true)
	if got2 != "RelHasID" {
		t.Errorf("expected RelHasID, got %s", got2)
	}
}
