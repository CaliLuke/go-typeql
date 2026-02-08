package tqlgen

import (
	"strings"
	"testing"
)

const testSchema = `define

attribute name, value string;
attribute age, value integer;
attribute email, value string @regex("^[^@]+@[^@]+$");
attribute status value string @values("active", "inactive");
attribute priority, value integer @range(1..5);
attribute start_date, value datetime;

entity person,
    owns name @key,
    owns email @unique,
    owns age @card(0..1),
    plays employment:employee;

entity artifact @abstract,
    owns name @key,
    owns status @card(0..1);

entity task sub artifact,
    owns priority @card(0..1),
    plays assignment:task;

entity company,
    owns name @key,
    plays employment:employer;

relation employment,
    relates employee @card(1),
    relates employer @card(1),
    owns start_date @card(0..1);

relation assignment,
    relates task @card(1..),
    relates assignee @card(1);
`

func TestParseSchema_Attributes(t *testing.T) {
	schema, err := ParseSchema(testSchema)
	if err != nil {
		t.Fatalf("ParseSchema failed: %v", err)
	}

	if len(schema.Attributes) != 6 {
		t.Fatalf("expected 6 attributes, got %d", len(schema.Attributes))
	}

	// Check basic attribute
	a := schema.Attributes[0]
	if a.Name != "name" || a.ValueType != "string" {
		t.Errorf("expected name:string, got %s:%s", a.Name, a.ValueType)
	}

	// Check regex attribute
	a = schema.Attributes[2]
	if a.Name != "email" {
		t.Errorf("expected email, got %s", a.Name)
	}
	if a.Regex == "" {
		t.Error("expected regex constraint on email")
	}

	// Check @values attribute
	a = schema.Attributes[3]
	if a.Name != "status" {
		t.Errorf("expected status, got %s", a.Name)
	}
	if len(a.Values) != 2 {
		t.Errorf("expected 2 values, got %d", len(a.Values))
	}

	// Check @range attribute
	a = schema.Attributes[4]
	if a.Name != "priority" {
		t.Errorf("expected priority, got %s", a.Name)
	}
	if a.RangeOp != "1..5" {
		t.Errorf("expected range 1..5, got %q", a.RangeOp)
	}

	// Check datetime
	a = schema.Attributes[5]
	if a.ValueType != "datetime" {
		t.Errorf("expected datetime, got %s", a.ValueType)
	}
}

func TestParseSchema_Entities(t *testing.T) {
	schema, err := ParseSchema(testSchema)
	if err != nil {
		t.Fatalf("ParseSchema failed: %v", err)
	}

	if len(schema.Entities) != 4 {
		t.Fatalf("expected 4 entities, got %d", len(schema.Entities))
	}

	// person: 3 owns, 1 plays
	person := schema.Entities[0]
	if person.Name != "person" {
		t.Errorf("expected person, got %s", person.Name)
	}
	if len(person.Owns) != 3 {
		t.Errorf("expected 3 owns, got %d", len(person.Owns))
	}
	if !person.Owns[0].Key {
		t.Error("expected name to be @key")
	}
	if !person.Owns[1].Unique {
		t.Error("expected email to be @unique")
	}
	if person.Owns[2].Card != "0..1" {
		t.Errorf("expected card=0..1, got %q", person.Owns[2].Card)
	}
	if len(person.Plays) != 1 {
		t.Errorf("expected 1 plays, got %d", len(person.Plays))
	}
	if person.Plays[0].Relation != "employment" || person.Plays[0].Role != "employee" {
		t.Errorf("expected employment:employee, got %s:%s", person.Plays[0].Relation, person.Plays[0].Role)
	}

	// artifact: abstract
	artifact := schema.Entities[1]
	if !artifact.Abstract {
		t.Error("expected artifact to be abstract")
	}

	// task: sub artifact
	task := schema.Entities[2]
	if task.Parent != "artifact" {
		t.Errorf("expected parent=artifact, got %q", task.Parent)
	}
}

func TestParseSchema_Relations(t *testing.T) {
	schema, err := ParseSchema(testSchema)
	if err != nil {
		t.Fatalf("ParseSchema failed: %v", err)
	}

	if len(schema.Relations) != 2 {
		t.Fatalf("expected 2 relations, got %d", len(schema.Relations))
	}

	// employment: 2 relates, 1 owns
	emp := schema.Relations[0]
	if emp.Name != "employment" {
		t.Errorf("expected employment, got %s", emp.Name)
	}
	if len(emp.Relates) != 2 {
		t.Errorf("expected 2 relates, got %d", len(emp.Relates))
	}
	if emp.Relates[0].Card != "1" {
		t.Errorf("expected card=1, got %q", emp.Relates[0].Card)
	}
	if len(emp.Owns) != 1 {
		t.Errorf("expected 1 owns, got %d", len(emp.Owns))
	}

	// assignment: relates with 1..
	assign := schema.Relations[1]
	if assign.Relates[0].Card != "1.." {
		t.Errorf("expected card=1.., got %q", assign.Relates[0].Card)
	}
}

func TestParseSchema_StripFunctions(t *testing.T) {
	schemaWithFuncs := testSchema + `
fun count_things() -> integer:
    match
        $x isa person;
    return count;

fun list_names() -> { string }:
    match
        $x isa person, has name $n;
    return { $n };
`
	schema, err := ParseSchema(schemaWithFuncs)
	if err != nil {
		t.Fatalf("ParseSchema with functions failed: %v", err)
	}

	// Should still parse entities and relations, ignoring functions
	if len(schema.Entities) != 4 {
		t.Errorf("expected 4 entities, got %d", len(schema.Entities))
	}
}

func TestParseSchema_FunctionExtraction(t *testing.T) {
	schemaWithFuncs := testSchema + `
fun count_things() -> integer:
    match
        $x isa person;
    return count;

fun get_age($person_name: string) -> integer:
    match
        $p isa person, has name $person_name, has age $a;
    return $a;

fun list_names() -> { string }:
    match
        $x isa person, has name $n;
    return { $n };
`
	schema, err := ParseSchema(schemaWithFuncs)
	if err != nil {
		t.Fatalf("ParseSchema failed: %v", err)
	}

	if len(schema.Functions) != 3 {
		t.Fatalf("expected 3 functions, got %d", len(schema.Functions))
	}

	// count_things: no params, returns integer
	f0 := schema.Functions[0]
	if f0.Name != "count_things" {
		t.Errorf("expected count_things, got %s", f0.Name)
	}
	if len(f0.Parameters) != 0 {
		t.Errorf("expected 0 params, got %d", len(f0.Parameters))
	}
	if f0.ReturnType != "integer" {
		t.Errorf("expected return type integer, got %q", f0.ReturnType)
	}

	// get_age: 1 param ($person_name: string), returns integer
	f1 := schema.Functions[1]
	if f1.Name != "get_age" {
		t.Errorf("expected get_age, got %s", f1.Name)
	}
	if len(f1.Parameters) != 1 {
		t.Fatalf("expected 1 param, got %d", len(f1.Parameters))
	}
	if f1.Parameters[0].Name != "person_name" {
		t.Errorf("expected param name person_name, got %q", f1.Parameters[0].Name)
	}
	if f1.Parameters[0].TypeName != "string" {
		t.Errorf("expected param type string, got %q", f1.Parameters[0].TypeName)
	}

	// list_names: returns { string }
	f2 := schema.Functions[2]
	if f2.Name != "list_names" {
		t.Errorf("expected list_names, got %s", f2.Name)
	}
	if f2.ReturnType != "{ string }" {
		t.Errorf("expected return type '{ string }', got %q", f2.ReturnType)
	}
}

func TestParseSchema_StructExtraction(t *testing.T) {
	t.Run("legacy_comma_value_name_type", func(t *testing.T) {
		schemaWithStructs := `define

attribute name, value string;

struct person-name,
    value first-name string,
    value last-name string,
    value middle-name string?;

entity person,
    owns name @key;
`
		schema, err := ParseSchema(schemaWithStructs)
		if err != nil {
			t.Fatalf("ParseSchema failed: %v", err)
		}

		if len(schema.Structs) != 1 {
			t.Fatalf("expected 1 struct, got %d", len(schema.Structs))
		}

		s := schema.Structs[0]
		if s.Name != "person-name" {
			t.Errorf("expected person-name, got %q", s.Name)
		}
		if len(s.Fields) != 3 {
			t.Fatalf("expected 3 fields, got %d", len(s.Fields))
		}
		if s.Fields[0].Name != "first-name" || s.Fields[0].ValueType != "string" {
			t.Errorf("field 0: got %s:%s", s.Fields[0].Name, s.Fields[0].ValueType)
		}
		if s.Fields[2].Name != "middle-name" || !s.Fields[2].Optional {
			t.Errorf("field 2: expected optional middle-name, got %s optional=%v", s.Fields[2].Name, s.Fields[2].Optional)
		}
	})

	t.Run("official_colon_name_value_type", func(t *testing.T) {
		schemaWithStructs := `define

attribute name, value string;

struct person-name:
    first-name value string,
    last-name value string,
    middle-name value string?;

entity person,
    owns name @key;
`
		schema, err := ParseSchema(schemaWithStructs)
		if err != nil {
			t.Fatalf("ParseSchema failed: %v", err)
		}

		if len(schema.Structs) != 1 {
			t.Fatalf("expected 1 struct, got %d", len(schema.Structs))
		}

		s := schema.Structs[0]
		if s.Name != "person-name" {
			t.Errorf("expected person-name, got %q", s.Name)
		}
		if len(s.Fields) != 3 {
			t.Fatalf("expected 3 fields, got %d", len(s.Fields))
		}
		if s.Fields[0].Name != "first-name" || s.Fields[0].ValueType != "string" {
			t.Errorf("field 0: got %s:%s", s.Fields[0].Name, s.Fields[0].ValueType)
		}
		if s.Fields[2].Name != "middle-name" || !s.Fields[2].Optional {
			t.Errorf("field 2: expected optional middle-name, got %s optional=%v", s.Fields[2].Name, s.Fields[2].Optional)
		}
	})
}

func TestExtractAnnotations(t *testing.T) {
	input := `define

# @description A person entity
# @version 1.0
entity person,
    owns name @key;

# @deprecated Use task instead
entity old-task,
    owns name @key;
`
	annots := ExtractAnnotations(input)

	if len(annots) != 2 {
		t.Fatalf("expected 2 annotated types, got %d", len(annots))
	}

	personAnnots, ok := annots["person"]
	if !ok {
		t.Fatal("expected annotations for person")
	}
	if personAnnots["description"] != "A person entity" {
		t.Errorf("expected description, got %q", personAnnots["description"])
	}
	if personAnnots["version"] != "1.0" {
		t.Errorf("expected version 1.0, got %q", personAnnots["version"])
	}

	taskAnnots, ok := annots["old-task"]
	if !ok {
		t.Fatal("expected annotations for old-task")
	}
	if taskAnnots["deprecated"] != "Use task instead" {
		t.Errorf("expected deprecated note, got %q", taskAnnots["deprecated"])
	}
}

func TestParseSchema_StructWithSemicolonInComment(t *testing.T) {
	input := `define

attribute name, value string;

struct metadata,
    value key string,
    value val string;

entity person,
    owns name @key;
`
	schema, err := ParseSchema(input)
	if err != nil {
		t.Fatalf("ParseSchema failed: %v", err)
	}

	if len(schema.Entities) != 1 {
		t.Errorf("expected 1 entity, got %d", len(schema.Entities))
	}
	if len(schema.Structs) != 1 {
		t.Fatalf("expected 1 struct, got %d", len(schema.Structs))
	}
	s := schema.Structs[0]
	if s.Name != "metadata" {
		t.Errorf("expected metadata, got %q", s.Name)
	}
	if len(s.Fields) != 2 {
		t.Errorf("expected 2 fields, got %d", len(s.Fields))
	}
}

func TestStripFunctions_KeywordInString(t *testing.T) {
	// "fun " inside a string literal should NOT trigger function stripping
	input := `define

attribute name, value string @regex("fun stuff");

entity person,
    owns name @key;
`
	schema, err := ParseSchema(input)
	if err != nil {
		t.Fatalf("ParseSchema failed: %v", err)
	}

	if len(schema.Entities) != 1 {
		t.Errorf("expected 1 entity, got %d", len(schema.Entities))
	}
	if len(schema.Attributes) != 1 {
		t.Errorf("expected 1 attribute, got %d", len(schema.Attributes))
	}
}

func TestStripFunctions_KeywordInComment(t *testing.T) {
	// "fun" in a comment should NOT trigger function stripping
	input := `define

attribute name, value string;

# fun is not a keyword here
entity person,
    owns name @key;
`
	schema, err := ParseSchema(input)
	if err != nil {
		t.Fatalf("ParseSchema failed: %v", err)
	}

	if len(schema.Entities) != 1 {
		t.Errorf("expected 1 entity, got %d", len(schema.Entities))
	}
}

func TestStripFunctions_RealFunctionAfterDefine(t *testing.T) {
	// Actual `fun` keyword should still be stripped
	input := `define

attribute name, value string;

entity person,
    owns name @key;

fun count_things() -> integer:
    match $x isa person;
    return count;
`
	schema, err := ParseSchema(input)
	if err != nil {
		t.Fatalf("ParseSchema failed: %v", err)
	}

	if len(schema.Entities) != 1 {
		t.Errorf("expected 1 entity, got %d", len(schema.Entities))
	}
	// Function should be extracted by extractFunctions
	if len(schema.Functions) != 1 {
		t.Errorf("expected 1 function, got %d", len(schema.Functions))
	}
}

func TestAccumulateInheritance(t *testing.T) {
	schema, err := ParseSchema(testSchema)
	if err != nil {
		t.Fatalf("ParseSchema failed: %v", err)
	}

	schema.AccumulateInheritance()

	// task inherits from artifact: should have artifact's owns + its own
	var task *EntitySpec
	for i := range schema.Entities {
		if schema.Entities[i].Name == "task" {
			task = &schema.Entities[i]
			break
		}
	}
	if task == nil {
		t.Fatal("task entity not found")
	}

	// artifact has: name @key, status @card(0..1)
	// task has: priority @card(0..1)
	// After inheritance: name, status, priority
	if len(task.Owns) != 3 {
		t.Fatalf("expected 3 owns after inheritance, got %d", len(task.Owns))
	}

	// Verify inherited fields are present
	ownsNames := make(map[string]bool)
	for _, o := range task.Owns {
		ownsNames[o.Attribute] = true
	}
	for _, want := range []string{"name", "status", "priority"} {
		if !ownsNames[want] {
			t.Errorf("expected task to own %q after inheritance", want)
		}
	}
}

func TestParseSchemaFile_Real(t *testing.T) {
	// Parse the real schema.tql if it exists
	schema, err := ParseSchemaFile("../schema.tql")
	if err != nil {
		t.Skipf("skipping real schema test: %v", err)
	}

	if len(schema.Attributes) == 0 {
		t.Error("expected attributes from real schema")
	}
	if len(schema.Entities) == 0 {
		t.Error("expected entities from real schema")
	}
	if len(schema.Relations) == 0 {
		t.Error("expected relations from real schema")
	}

	// Verify specific known entities
	entityNames := make(map[string]bool)
	for _, e := range schema.Entities {
		entityNames[e.Name] = true
	}
	for _, want := range []string{"project", "artifact", "persona", "user_story", "task"} {
		if !entityNames[want] {
			t.Errorf("expected entity %q in real schema", want)
		}
	}
}

func TestRender_BasicOutput(t *testing.T) {
	schema, err := ParseSchema(testSchema)
	if err != nil {
		t.Fatalf("ParseSchema failed: %v", err)
	}
	schema.AccumulateInheritance()

	var buf strings.Builder
	cfg := DefaultConfig()
	cfg.PackageName = "test"
	cfg.SkipAbstract = true

	err = Render(&buf, schema, cfg)
	if err != nil {
		t.Fatalf("Render failed: %v", err)
	}

	output := buf.String()

	// Check package
	if !strings.Contains(output, "package test") {
		t.Error("expected package test in output")
	}

	// Check entity struct
	if !strings.Contains(output, "type Person struct") {
		t.Error("expected Person struct")
	}
	if !strings.Contains(output, "gotype.BaseEntity") {
		t.Error("expected BaseEntity embed")
	}
	if !strings.Contains(output, "`typedb:\"name,key\"`") {
		t.Error("expected name key tag")
	}
	if !strings.Contains(output, "`typedb:\"email,unique\"`") {
		t.Error("expected email unique tag")
	}
	if !strings.Contains(output, "*int64") {
		t.Error("expected optional int64 pointer for age")
	}

	// Check relation struct
	if !strings.Contains(output, "type Employment struct") {
		t.Error("expected Employment struct")
	}
	if !strings.Contains(output, "gotype.BaseRelation") {
		t.Error("expected BaseRelation embed")
	}
	if !strings.Contains(output, "`typedb:\"role:employee\"`") {
		t.Error("expected role:employee tag")
	}

	// Abstract should be skipped
	if strings.Contains(output, "type Artifact struct") {
		t.Error("expected abstract Artifact to be skipped")
	}

	// Task should have inherited fields
	if !strings.Contains(output, "type Task struct") {
		t.Error("expected Task struct")
	}

	// Check DO NOT EDIT header
	if !strings.Contains(output, "DO NOT EDIT") {
		t.Error("expected DO NOT EDIT header")
	}
}
