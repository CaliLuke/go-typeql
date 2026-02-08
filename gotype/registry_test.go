package gotype

import (
	"reflect"
	"testing"
)

func TestRegister(t *testing.T) {
	ClearRegistry()

	err := Register[TestPerson]()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Lookup by name
	info, ok := Lookup("test-person")
	if !ok {
		t.Fatal("expected to find testperson")
	}
	if info.TypeName != "test-person" {
		t.Errorf("TypeName: got %q, want %q", info.TypeName, "test-person")
	}

	// Lookup by type
	info2, ok := LookupType(reflect.TypeOf(TestPerson{}))
	if !ok {
		t.Fatal("expected to find TestPerson by type")
	}
	if info2 != info {
		t.Error("expected same ModelInfo from both lookups")
	}
}

func TestRegister_Duplicate(t *testing.T) {
	ClearRegistry()

	err := Register[TestPerson]()
	if err != nil {
		t.Fatalf("first register: %v", err)
	}

	// Re-registering same type should succeed (idempotent)
	err = Register[TestPerson]()
	if err != nil {
		t.Fatalf("duplicate register: %v", err)
	}
}

func TestRegister_Relation(t *testing.T) {
	ClearRegistry()

	err := Register[TestEmployment]()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	info, ok := Lookup("test-employment")
	if !ok {
		t.Fatal("expected to find testemployment")
	}
	if info.Kind != ModelKindRelation {
		t.Error("expected ModelKindRelation")
	}
	if len(info.Roles) != 2 {
		t.Errorf("Roles: got %d, want 2", len(info.Roles))
	}
}

func TestLookupByGoName(t *testing.T) {
	ClearRegistry()

	MustRegister[TestPerson]()

	info, ok := LookupByGoName("TestPerson")
	if !ok {
		t.Fatal("expected to find TestPerson by Go name")
	}
	if info.TypeName != "test-person" {
		t.Errorf("TypeName: got %q, want %q", info.TypeName, "test-person")
	}

	// Case insensitive
	info2, ok := LookupByGoName("testperson")
	if !ok {
		t.Fatal("expected case-insensitive lookup to work")
	}
	if info2 != info {
		t.Error("expected same ModelInfo")
	}
}

func TestRegisteredTypes(t *testing.T) {
	ClearRegistry()

	MustRegister[TestPerson]()
	MustRegister[TestCompany]()

	types := RegisteredTypes()
	if len(types) != 2 {
		t.Errorf("got %d registered types, want 2", len(types))
	}
}

func TestFieldByName(t *testing.T) {
	ClearRegistry()
	MustRegister[TestPerson]()

	info, _ := Lookup("test-person")

	f, ok := info.FieldByName("Name")
	if !ok {
		t.Fatal("expected to find Name field")
	}
	if f.Tag.Name != "name" {
		t.Errorf("Tag.Name: got %q, want %q", f.Tag.Name, "name")
	}

	_, ok = info.FieldByName("NonExistent")
	if ok {
		t.Error("expected not to find NonExistent field")
	}
}

func TestSubtypesOf(t *testing.T) {
	ClearRegistry()
	MustRegister[TestPerson]()

	// No subtypes registered
	subs := SubtypesOf("test-person")
	if len(subs) != 0 {
		t.Errorf("expected 0 subtypes, got %d", len(subs))
	}
}

func TestResolveType(t *testing.T) {
	ClearRegistry()
	MustRegister[TestPerson]()

	info, ok := ResolveType("test-person")
	if !ok {
		t.Fatal("expected to resolve testperson")
	}
	if info.TypeName != "test-person" {
		t.Errorf("expected testperson, got %q", info.TypeName)
	}

	_, ok = ResolveType("nonexistent")
	if ok {
		t.Error("expected nonexistent to not resolve")
	}
}

func TestFieldByAttrName(t *testing.T) {
	ClearRegistry()
	MustRegister[TestPerson]()

	info, _ := Lookup("test-person")

	f, ok := info.FieldByAttrName("email")
	if !ok {
		t.Fatal("expected to find email attribute")
	}
	if f.FieldName != "Email" {
		t.Errorf("FieldName: got %q, want %q", f.FieldName, "Email")
	}
}
