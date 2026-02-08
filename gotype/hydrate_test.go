package gotype

import (
	"testing"
)

func TestHydrate_Entity(t *testing.T) {
	ClearRegistry()
	MustRegister[TestPerson]()

	data := map[string]any{
		"_iid":  "0x12345",
		"name":  "Alice",
		"email": "alice@example.com",
		"age":   float64(30), // JSON numbers come as float64
	}

	person := &TestPerson{}
	err := Hydrate(person, data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if person.Name != "Alice" {
		t.Errorf("Name: got %q, want %q", person.Name, "Alice")
	}
	if person.Email != "alice@example.com" {
		t.Errorf("Email: got %q, want %q", person.Email, "alice@example.com")
	}
	if person.Age == nil {
		t.Fatal("Age should not be nil")
	}
	if *person.Age != 30 {
		t.Errorf("Age: got %d, want 30", *person.Age)
	}
	if person.GetIID() != "0x12345" {
		t.Errorf("IID: got %q, want %q", person.GetIID(), "0x12345")
	}
}

func TestHydrate_NilOptional(t *testing.T) {
	ClearRegistry()
	MustRegister[TestPerson]()

	data := map[string]any{
		"name":  "Bob",
		"email": "bob@example.com",
	}

	person := &TestPerson{}
	err := Hydrate(person, data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if person.Age != nil {
		t.Error("Age should be nil")
	}
}

func TestHydrateNew(t *testing.T) {
	ClearRegistry()
	MustRegister[TestPerson]()

	data := map[string]any{
		"name":  "Carol",
		"email": "carol@example.com",
	}

	person, err := HydrateNew[TestPerson](data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if person.Name != "Carol" {
		t.Errorf("Name: got %q, want %q", person.Name, "Carol")
	}
}

func TestHydrate_NonPointerTarget(t *testing.T) {
	ClearRegistry()
	MustRegister[TestPerson]()

	data := map[string]any{"name": "Test"}
	var person TestPerson
	err := Hydrate(person, data) // not a pointer
	if err == nil {
		t.Fatal("expected error for non-pointer target")
	}
}

func TestHydrate_UnregisteredType(t *testing.T) {
	ClearRegistry()

	data := map[string]any{"name": "Test"}
	person := &TestPerson{}
	err := Hydrate(person, data)
	if err == nil {
		t.Fatal("expected error for unregistered type")
	}
}

func TestHydrate_RelationWithRoles(t *testing.T) {
	ClearRegistry()
	MustRegister[TestPerson]()
	MustRegister[TestCompany]()
	MustRegister[TestEmployment]()

	data := map[string]any{
		"_iid":   "0xREL1",
		"salary": float64(75000),
		"employee": map[string]any{
			"_iid":  "0xPER1",
			"name":  "Alice",
			"email": "alice@example.com",
			"age":   float64(30),
		},
		"employer": map[string]any{
			"_iid":    "0xCOM1",
			"name":    "Acme",
			"founded": float64(2000),
		},
	}

	emp := &TestEmployment{}
	err := Hydrate(emp, data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if emp.GetIID() != "0xREL1" {
		t.Errorf("IID: got %q, want %q", emp.GetIID(), "0xREL1")
	}
	if emp.Salary == nil || *emp.Salary != 75000 {
		t.Errorf("Salary: got %v, want 75000", emp.Salary)
	}
	if emp.Employee == nil {
		t.Fatal("Employee should not be nil")
	}
	if emp.Employee.Name != "Alice" {
		t.Errorf("Employee.Name: got %q, want %q", emp.Employee.Name, "Alice")
	}
	if emp.Employee.GetIID() != "0xPER1" {
		t.Errorf("Employee.IID: got %q, want %q", emp.Employee.GetIID(), "0xPER1")
	}
	if emp.Employer == nil {
		t.Fatal("Employer should not be nil")
	}
	if emp.Employer.Name != "Acme" {
		t.Errorf("Employer.Name: got %q, want %q", emp.Employer.Name, "Acme")
	}
}

func TestHydrate_CycleDetection(t *testing.T) {
	ClearRegistry()
	MustRegister[TestPerson]()
	MustRegister[TestCompany]()
	MustRegister[TestEmployment]()

	// Simulate a cycle: employee data contains a nested role pointing back with same IID
	data := map[string]any{
		"_iid":   "0xREL1",
		"salary": float64(50000),
		"employee": map[string]any{
			"_iid":  "0xPER1",
			"name":  "Alice",
			"email": "alice@example.com",
		},
		"employer": map[string]any{
			"_iid":    "0xCOM1",
			"name":    "Acme",
			"founded": float64(2000),
		},
	}

	emp := &TestEmployment{}
	err := Hydrate(emp, data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Normal hydration should work fine
	if emp.Employee == nil || emp.Employee.Name != "Alice" {
		t.Error("expected employee to be hydrated")
	}
}

func TestHydrate_VisitedIIDStopsCycle(t *testing.T) {
	ClearRegistry()
	MustRegister[TestPerson]()
	MustRegister[TestCompany]()
	MustRegister[TestEmployment]()

	// Employee has same IID as the outer relation — simulates cycle
	data := map[string]any{
		"_iid":   "0xCYCLE",
		"salary": float64(50000),
		"employee": map[string]any{
			"_iid":  "0xCYCLE", // same IID as parent → cycle
			"name":  "Alice",
			"email": "alice@example.com",
		},
		"employer": map[string]any{
			"_iid":    "0xCOM1",
			"name":    "Acme",
			"founded": float64(2000),
		},
	}

	emp := &TestEmployment{}
	err := Hydrate(emp, data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// The employee pointer should be set (non-nil) but fields should be zero values
	// because the IID was already visited
	if emp.Employee == nil {
		t.Fatal("employee should not be nil even with cycle")
	}
	if emp.Employee.Name != "" {
		t.Errorf("expected empty name for cycled entity, got %q", emp.Employee.Name)
	}
}

func TestHydrate_SliceField(t *testing.T) {
	ClearRegistry()
	MustRegister[TestPersonWithTags]()

	data := map[string]any{
		"name":     "Dave",
		"nickname": []any{"D", "Davey"},
	}

	person := &TestPersonWithTags{}
	err := Hydrate(person, data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(person.Nickname) != 2 {
		t.Fatalf("Nickname: got %d items, want 2", len(person.Nickname))
	}
	if person.Nickname[0] != "D" || person.Nickname[1] != "Davey" {
		t.Errorf("Nickname: got %v, want [D Davey]", person.Nickname)
	}
}

func TestHydrateAny(t *testing.T) {
	ClearRegistry()
	MustRegister[TestPerson]()

	data := map[string]any{
		"_type": "test-person",
		"_iid":  "0x123",
		"name":  "Eve",
		"email": "eve@example.com",
	}

	result, err := HydrateAny(data)
	if err != nil {
		t.Fatalf("HydrateAny failed: %v", err)
	}

	person, ok := result.(*TestPerson)
	if !ok {
		t.Fatalf("expected *TestPerson, got %T", result)
	}
	if person.Name != "Eve" {
		t.Errorf("expected Eve, got %q", person.Name)
	}
	if person.Email != "eve@example.com" {
		t.Errorf("expected eve@example.com, got %q", person.Email)
	}
}

func TestHydrateAny_MissingType(t *testing.T) {
	data := map[string]any{
		"name": "Nobody",
	}

	_, err := HydrateAny(data)
	if err == nil {
		t.Fatal("expected error for missing _type")
	}
}

func TestHydrateAny_UnregisteredType(t *testing.T) {
	ClearRegistry()

	data := map[string]any{
		"_type": "nonexistent-type",
		"name":  "Ghost",
	}

	_, err := HydrateAny(data)
	if err == nil {
		t.Fatal("expected error for unregistered type")
	}
}
