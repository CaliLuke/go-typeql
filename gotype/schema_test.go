package gotype

import (
	"strings"
	"testing"
)

func TestGenerateSchemaFor_Entity(t *testing.T) {
	ClearRegistry()
	MustRegister[TestPerson]()

	info, _ := Lookup("test-person")
	schema := GenerateSchemaFor(info)

	// Should contain attribute definitions
	if !strings.Contains(schema, "attribute name, value string;") {
		t.Error("missing attribute name definition")
	}
	if !strings.Contains(schema, "attribute email, value string;") {
		t.Error("missing attribute email definition")
	}
	if !strings.Contains(schema, "attribute age, value integer;") {
		t.Error("missing attribute age definition")
	}

	// Should contain entity definition
	if !strings.Contains(schema, "entity test-person") {
		t.Error("missing entity definition")
	}
	if !strings.Contains(schema, "owns name @key") {
		t.Error("missing owns name @key")
	}
	if !strings.Contains(schema, "owns email @unique") {
		t.Error("missing owns email @unique")
	}
	if !strings.Contains(schema, "owns age") {
		t.Error("missing owns age")
	}
}

type schemaDocPerson struct {
	BaseEntity
	Name string `typedb:"name,key" typedb_doc:"Primary display name."`
}

func (schemaDocPerson) SchemaDoc() string {
	return "A documented person type."
}

func (schemaDocPerson) SchemaMeta() map[string]string {
	return map[string]string{
		"ui":    "person",
		"owner": "identity",
	}
}

func TestGenerateSchemaFor_FieldDocAnnotation(t *testing.T) {
	ClearRegistry()
	MustRegister[schemaDocPerson]()

	info, _ := Lookup("schema-doc-person")
	schema := GenerateSchemaFor(info)

	if !strings.Contains(schema, `owns name @key @doc("Primary display name.")`) {
		t.Fatalf("missing field @doc annotation\n%s", schema)
	}
	if !strings.Contains(schema, `entity schema-doc-person @doc("A documented person type.")`) {
		t.Fatalf("missing type @doc annotation\n%s", schema)
	}
}

func TestGenerateSchemaFor_TypeMetaAnnotationsSorted(t *testing.T) {
	ClearRegistry()
	MustRegister[schemaDocPerson]()

	info, _ := Lookup("schema-doc-person")
	schema := GenerateSchemaFor(info)

	want := `entity schema-doc-person @doc("A documented person type.") @meta("owner", "identity") @meta("ui", "person")`
	if !strings.Contains(schema, want) {
		t.Fatalf("missing sorted type @meta annotations\nwant: %s\nschema:\n%s", want, schema)
	}
}

type schemaDocEscapedPerson struct {
	BaseEntity
	Name string `typedb:"name,key" typedb_doc:"Display, \"legal\" name."`
}

func TestGenerateSchemaFor_FieldDocAnnotationEscapesWithoutBreakingTypedbTag(t *testing.T) {
	ClearRegistry()
	MustRegister[schemaDocEscapedPerson]()

	info, _ := Lookup("schema-doc-escaped-person")
	schema := GenerateSchemaFor(info)

	if !strings.Contains(schema, `owns name @key @doc("Display, \"legal\" name.")`) {
		t.Fatalf("missing escaped field @doc annotation\n%s", schema)
	}
}

type schemaDocControlPerson struct {
	BaseEntity
	Name string `typedb:"name,key" typedb_doc:"Line one\nLine two"`
}

func (schemaDocControlPerson) SchemaDoc() string {
	return "Type line one\nType line two"
}

func (schemaDocControlPerson) SchemaMeta() map[string]string {
	return map[string]string{"note": "Meta line one\nMeta line two"}
}

func TestGenerateSchemaFor_DocAndMetaControlCharactersRoundTripThroughParser(t *testing.T) {
	ClearRegistry()
	MustRegister[schemaDocControlPerson]()

	info, _ := Lookup("schema-doc-control-person")
	schema := GenerateSchemaFor(info)

	parsed, err := IntrospectSchemaFromString(schema)
	if err != nil {
		t.Fatalf("IntrospectSchemaFromString: %v\nschema:\n%s", err, schema)
	}
	if len(parsed.Entities) != 1 {
		t.Fatalf("expected one entity, got %#v", parsed.Entities)
	}
	entity := parsed.Entities[0]
	if entity.Doc != "Type line one\nType line two" {
		t.Fatalf("entity Doc = %q", entity.Doc)
	}
	if got := entity.Meta; len(got) != 1 || got[0].Key != "note" || got[0].Value != "Meta line one\nMeta line two" {
		t.Fatalf("entity Meta = %#v", got)
	}
	if len(entity.Owns) != 1 || entity.Owns[0].Doc != "Line one\nLine two" {
		t.Fatalf("owns Doc = %#v", entity.Owns)
	}
}

func TestGenerateSchemaFor_Relation(t *testing.T) {
	ClearRegistry()
	MustRegister[TestEmployment]()

	info, _ := Lookup("test-employment")
	schema := GenerateSchemaFor(info)

	if !strings.Contains(schema, "relation test-employment") {
		t.Error("missing relation definition")
	}
	if !strings.Contains(schema, "relates employee") {
		t.Error("missing relates employee")
	}
	if !strings.Contains(schema, "relates employer") {
		t.Error("missing relates employer")
	}
	if !strings.Contains(schema, "owns salary") {
		t.Error("missing owns salary")
	}
}

func TestGenerateSchema_Multiple(t *testing.T) {
	ClearRegistry()
	MustRegister[TestPerson]()
	MustRegister[TestCompany]()

	schema := GenerateSchema()

	if !strings.HasPrefix(schema, "define\n") {
		t.Error("schema should start with 'define'")
	}
	if !strings.Contains(schema, "entity test-person") {
		t.Error("missing test-person")
	}
	if !strings.Contains(schema, "entity test-company") {
		t.Error("missing test-company")
	}
}

func TestFormatCardAnnotation(t *testing.T) {
	tests := []struct {
		name string
		min  *int
		max  *int
		want string
	}{
		{"nil nil", nil, nil, ""},
		{"0 unbounded", new(0), nil, "@card(0..)"},
		{"1 5", new(1), new(5), "@card(1..5)"},
		{"0 1", new(0), new(1), "@card(0..1)"},
		{"2 unbounded", new(2), nil, "@card(2..)"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatCardAnnotation(tt.min, tt.max)
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}
