package gotype

import (
	"reflect"
	"strings"
	"testing"
	"time"
)

// Test models

type TestPerson struct {
	BaseEntity
	Name  string `typedb:"name,key"`
	Email string `typedb:"email,unique"`
	Age   *int   `typedb:"age"`
}

type TestCompany struct {
	BaseEntity
	Name    string `typedb:"name,key"`
	Founded *int   `typedb:"founded"`
}

type TestEmployment struct {
	BaseRelation
	Employee *TestPerson  `typedb:"role:employee"`
	Employer *TestCompany `typedb:"role:employer"`
	Salary   *float64     `typedb:"salary"`
}

type TestPersonWithTags struct {
	BaseEntity
	Name     string   `typedb:"name,key"`
	Nickname []string `typedb:"nickname,card=0.."`
}

func TestExtractModelInfo_Entity(t *testing.T) {
	info, err := ExtractModelInfo(reflect.TypeOf(TestPerson{}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if info.Kind != ModelKindEntity {
		t.Errorf("Kind: got %v, want ModelKindEntity", info.Kind)
	}
	if info.TypeName != "test-person" {
		t.Errorf("TypeName: got %q, want %q", info.TypeName, "test-person")
	}
	if len(info.Fields) != 3 {
		t.Fatalf("Fields: got %d, want 3", len(info.Fields))
	}
	if len(info.KeyFields) != 1 {
		t.Fatalf("KeyFields: got %d, want 1", len(info.KeyFields))
	}
	if info.KeyFields[0].Tag.Name != "name" {
		t.Errorf("KeyField name: got %q, want %q", info.KeyFields[0].Tag.Name, "name")
	}

	// Check Name field
	nameField := info.Fields[0]
	if nameField.FieldName != "Name" {
		t.Errorf("Fields[0].FieldName: got %q, want %q", nameField.FieldName, "Name")
	}
	if nameField.Tag.Key != true {
		t.Errorf("Fields[0].Key: got %v, want true", nameField.Tag.Key)
	}
	if nameField.ValueType != "string" {
		t.Errorf("Fields[0].ValueType: got %q, want %q", nameField.ValueType, "string")
	}
	if nameField.IsPointer {
		t.Error("Fields[0].IsPointer should be false")
	}

	// Check Age field (pointer = optional)
	ageField := info.Fields[2]
	if ageField.FieldName != "Age" {
		t.Errorf("Fields[2].FieldName: got %q, want %q", ageField.FieldName, "Age")
	}
	if !ageField.IsPointer {
		t.Error("Fields[2].IsPointer should be true")
	}
	if ageField.ValueType != "integer" {
		t.Errorf("Fields[2].ValueType: got %q, want %q", ageField.ValueType, "integer")
	}
}

func TestExtractModelInfo_Relation(t *testing.T) {
	info, err := ExtractModelInfo(reflect.TypeOf(TestEmployment{}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if info.Kind != ModelKindRelation {
		t.Errorf("Kind: got %v, want ModelKindRelation", info.Kind)
	}
	if len(info.Roles) != 2 {
		t.Fatalf("Roles: got %d, want 2", len(info.Roles))
	}
	if info.Roles[0].RoleName != "employee" {
		t.Errorf("Roles[0].RoleName: got %q, want %q", info.Roles[0].RoleName, "employee")
	}
	if info.Roles[1].RoleName != "employer" {
		t.Errorf("Roles[1].RoleName: got %q, want %q", info.Roles[1].RoleName, "employer")
	}
	if len(info.Fields) != 1 {
		t.Fatalf("Fields: got %d, want 1 (salary)", len(info.Fields))
	}
	if info.Fields[0].Tag.Name != "salary" {
		t.Errorf("Fields[0].Name: got %q, want %q", info.Fields[0].Tag.Name, "salary")
	}
}

func TestExtractModelInfo_SliceField(t *testing.T) {
	info, err := ExtractModelInfo(reflect.TypeOf(TestPersonWithTags{}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	nicknameField := info.Fields[1]
	if !nicknameField.IsSlice {
		t.Error("Nickname field should be IsSlice")
	}
	if nicknameField.Tag.CardMin == nil || *nicknameField.Tag.CardMin != 0 {
		t.Error("Nickname CardMin should be 0")
	}
}

func TestExtractModelInfo_NotStruct(t *testing.T) {
	_, err := ExtractModelInfo(reflect.TypeOf(42))
	if err == nil {
		t.Fatal("expected error for non-struct")
	}
}

type BadModel struct {
	Name string `typedb:"name"`
}

func TestExtractModelInfo_NoBase(t *testing.T) {
	_, err := ExtractModelInfo(reflect.TypeOf(BadModel{}))
	if err == nil {
		t.Fatal("expected error for struct without BaseEntity/BaseRelation")
	}
}

func TestGoTypeToTypeDB(t *testing.T) {
	tests := []struct {
		goType reflect.Type
		want   string
	}{
		{reflect.TypeOf(""), "string"},
		{reflect.TypeOf(true), "boolean"},
		{reflect.TypeOf(0), "integer"},
		{reflect.TypeOf(int64(0)), "integer"},
		{reflect.TypeOf(0.0), "double"},
		{reflect.TypeOf(float32(0)), "double"},
		{reflect.TypeOf(time.Time{}), "datetime"},
	}

	for _, tt := range tests {
		t.Run(tt.goType.String(), func(t *testing.T) {
			got := goTypeToTypeDB(tt.goType)
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestToDict(t *testing.T) {
	ClearRegistry()
	_ = Register[testPersonModel]()

	p := &testPersonModel{
		Name:  "Alice",
		Email: "alice@example.com",
		Age:   intPtr(30),
	}
	p.SetIID("0xABC")

	d, err := ToDict(p)
	if err != nil {
		t.Fatalf("ToDict: %v", err)
	}

	if d["_iid"] != "0xABC" {
		t.Errorf("expected _iid '0xABC', got %v", d["_iid"])
	}
	if d["name"] != "Alice" {
		t.Errorf("expected name 'Alice', got %v", d["name"])
	}
	if d["email"] != "alice@example.com" {
		t.Errorf("expected email 'alice@example.com', got %v", d["email"])
	}
	if d["age"] != 30 {
		t.Errorf("expected age 30, got %v", d["age"])
	}
}

func TestToDict_NilOptionalOmitted(t *testing.T) {
	ClearRegistry()
	_ = Register[testPersonModel]()

	p := &testPersonModel{
		Name:  "Bob",
		Email: "bob@example.com",
	}

	d, err := ToDict(p)
	if err != nil {
		t.Fatalf("ToDict: %v", err)
	}

	if _, ok := d["age"]; ok {
		t.Error("expected nil age to be omitted from dict")
	}
}

func TestFromDict(t *testing.T) {
	ClearRegistry()
	_ = Register[testPersonModel]()

	data := map[string]any{
		"_iid":  "0xDEF",
		"name":  "Carol",
		"email": "carol@example.com",
		"age":   float64(25),
	}

	p, err := FromDict[testPersonModel](data)
	if err != nil {
		t.Fatalf("FromDict: %v", err)
	}
	if p.Name != "Carol" {
		t.Errorf("expected Carol, got %q", p.Name)
	}
	if p.Email != "carol@example.com" {
		t.Errorf("expected carol@example.com, got %q", p.Email)
	}
}

func TestToInsertQuery(t *testing.T) {
	ClearRegistry()
	_ = Register[testPersonModel]()

	p := &testPersonModel{
		Name:  "Alice",
		Email: "alice@example.com",
		Age:   intPtr(30),
	}

	q, err := ToInsertQuery(p)
	if err != nil {
		t.Fatalf("ToInsertQuery: %v", err)
	}
	if !strings.Contains(q, "insert") {
		t.Errorf("expected insert keyword, got %q", q)
	}
	if !strings.Contains(q, `has name "Alice"`) {
		t.Errorf("expected has name clause, got %q", q)
	}
	if !strings.Contains(q, "has age 30") {
		t.Errorf("expected has age clause, got %q", q)
	}
}

func TestToMatchQuery(t *testing.T) {
	ClearRegistry()
	_ = Register[testPersonModel]()

	p := &testPersonModel{
		Name:  "Alice",
		Email: "alice@example.com",
	}

	q, err := ToMatchQuery(p)
	if err != nil {
		t.Fatalf("ToMatchQuery: %v", err)
	}
	if !strings.Contains(q, "match") {
		t.Errorf("expected match keyword, got %q", q)
	}
	if !strings.Contains(q, `has name "Alice"`) {
		t.Errorf("expected has name clause, got %q", q)
	}
	// Match by key should not include non-key fields
	if strings.Contains(q, "email") {
		t.Errorf("expected no email in match by key, got %q", q)
	}
}

func TestToKebabCase(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"Person", "person"},
		{"UserAccount", "user-account"},
		{"TestPerson", "test-person"},
		{"HTTPServer", "h-t-t-p-server"}, // consecutive uppercase are split individually
		{"ABC", "a-b-c"},
		{"simple", "simple"},
		{"", ""},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := toKebabCase(tt.input)
			if got != tt.want {
				t.Errorf("toKebabCase(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

// testPersonModel is used for ToDict/FromDict tests.
type testPersonModel struct {
	BaseEntity
	Name  string `typedb:"name,key"`
	Email string `typedb:"email"`
	Age   *int   `typedb:"age"`
}

