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
		{"0 unbounded", intPtr(0), nil, "@card(0..)"},
		{"1 5", intPtr(1), intPtr(5), "@card(1..5)"},
		{"0 1", intPtr(0), intPtr(1), "@card(0..1)"},
		{"2 unbounded", intPtr(2), nil, "@card(2..)"},
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
