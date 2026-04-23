package gotype

import (
	"reflect"
	"strings"
	"testing"
	"time"
)

// Test models for strategy tests
type testPerson struct {
	BaseEntity
	Name  string `typedb:"name,key"`
	Email string `typedb:"email,unique"`
	Age   *int   `typedb:"age"`
}

type testCompany struct {
	BaseEntity
	Name     string `typedb:"name,key"`
	Industry string `typedb:"industry"`
}

type testEmployment struct {
	BaseRelation
	Employee  *testPerson  `typedb:"role:employee"`
	Employer  *testCompany `typedb:"role:employer"`
	StartDate *time.Time   `typedb:"start-date"`
}

// registerTestTypes registers the test types fresh (clears first).
func registerTestTypes(t *testing.T) {
	t.Helper()
	ClearRegistry()
	MustRegister[testPerson]()
	MustRegister[testCompany]()
	MustRegister[testEmployment]()
}

func TestEntityStrategy_BuildInsertQuery(t *testing.T) {
	registerTestTypes(t)
	info, _ := LookupType(typeOf[testPerson]())
	s := &entityStrategy{}

	age := 30
	p := &testPerson{Name: "Alice", Email: "alice@example.com", Age: &age}
	query, err := s.BuildInsertQuery(info, p, "e")
	if err != nil {
		t.Fatalf("BuildInsertQuery: %v", err)
	}

	assertContains(t, query, "insert")
	assertContains(t, query, `$e isa test-person`)
	assertContains(t, query, `has name "Alice"`)
	assertContains(t, query, `has email "alice@example.com"`)
	assertContains(t, query, `has age 30`)
}

func TestEntityStrategy_BuildInsertQuery_NilOptional(t *testing.T) {
	registerTestTypes(t)
	info, _ := LookupType(typeOf[testPerson]())
	s := &entityStrategy{}

	p := &testPerson{Name: "Bob", Email: "bob@example.com"} // Age is nil
	query, err := s.BuildInsertQuery(info, p, "e")
	if err != nil {
		t.Fatalf("BuildInsertQuery: %v", err)
	}

	assertContains(t, query, `has name "Bob"`)
	assertContains(t, query, `has email "bob@example.com"`)
	assertNotContains(t, query, "has age")
}

func TestEntityStrategy_BuildMatchByKey(t *testing.T) {
	registerTestTypes(t)
	info, _ := LookupType(typeOf[testPerson]())
	s := &entityStrategy{}

	p := &testPerson{Name: "Alice", Email: "alice@example.com"}
	query, err := s.BuildMatchByKey(info, p, "e")
	if err != nil {
		t.Fatalf("BuildMatchByKey: %v", err)
	}

	assertContains(t, query, "match")
	assertContains(t, query, `$e isa test-person`)
	assertContains(t, query, `has name "Alice"`)
	assertNotContains(t, query, "email")
}

func TestEntityStrategy_BuildMatchAll(t *testing.T) {
	registerTestTypes(t)
	info, _ := LookupType(typeOf[testPerson]())
	s := &entityStrategy{}

	query, err := s.BuildMatchAll(info, "e")
	if err != nil {
		t.Fatalf("BuildMatchAll: %v", err)
	}
	assertEqual(t, "match\n$e isa test-person;", query)
}

func TestEntityStrategy_BuildMatchByIID(t *testing.T) {
	s := &entityStrategy{}
	query, err := s.BuildMatchByIID("0x826e80018000000000000001", "e")
	if err != nil {
		t.Fatalf("BuildMatchByIID: %v", err)
	}
	assertEqual(t, "match\n$e iid 0x826e80018000000000000001;", query)
}

func TestEntityStrategy_BuildFetchAll(t *testing.T) {
	registerTestTypes(t)
	info, _ := LookupType(typeOf[testPerson]())
	s := &entityStrategy{}

	query, err := s.BuildFetchAll(info, "e")
	if err != nil {
		t.Fatalf("BuildFetchAll: %v", err)
	}
	assertContains(t, query, "fetch {")
	assertContains(t, query, `"_iid": iid($e)`)
	assertContains(t, query, `"name": $e.name`)
	assertContains(t, query, `"email": $e.email`)
	assertContains(t, query, `"age": $e.age`)
}

func TestRelationStrategy_BuildInsertQuery_ByKey(t *testing.T) {
	registerTestTypes(t)
	info, _ := LookupType(typeOf[testEmployment]())
	s := &relationStrategy{}

	p := &testPerson{Name: "Alice", Email: "alice@example.com"}
	c := &testCompany{Name: "Acme", Industry: "Tech"}
	startDate := time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC)
	emp := &testEmployment{Employee: p, Employer: c, StartDate: &startDate}

	query, err := s.BuildInsertQuery(info, emp, "r")
	if err != nil {
		t.Fatalf("BuildInsertQuery: %v", err)
	}

	assertContains(t, query, "match")
	assertContains(t, query, `$employee isa test-person, has name "Alice"`)
	assertContains(t, query, `$employer isa test-company, has name "Acme"`)
	assertContains(t, query, "insert")
	assertContains(t, query, "employee: $employee")
	assertContains(t, query, "employer: $employer")
	assertContains(t, query, "isa test-employment")
	assertContains(t, query, "has start-date 2024-01-15")
}

func TestRelationStrategy_BuildInsertQuery_ByIID(t *testing.T) {
	registerTestTypes(t)
	info, _ := LookupType(typeOf[testEmployment]())
	s := &relationStrategy{}

	p := &testPerson{Name: "Alice", Email: "alice@example.com"}
	p.SetIID("0x1234")
	c := &testCompany{Name: "Acme", Industry: "Tech"}
	c.SetIID("0x5678")

	emp := &testEmployment{Employee: p, Employer: c}
	query, err := s.BuildInsertQuery(info, emp, "r")
	if err != nil {
		t.Fatalf("BuildInsertQuery: %v", err)
	}

	assertContains(t, query, "$employee isa test-person, iid 0x1234")
	assertContains(t, query, "$employer isa test-company, iid 0x5678")
}

func TestEntityStrategy_BuildPutQuery(t *testing.T) {
	registerTestTypes(t)
	info, _ := LookupType(typeOf[testPerson]())
	s := &entityStrategy{}

	age := 25
	p := &testPerson{Name: "Alice", Email: "alice@example.com", Age: &age}
	query, err := s.BuildPutQuery(info, p, "e")
	if err != nil {
		t.Fatalf("BuildPutQuery: %v", err)
	}

	assertContains(t, query, "put")
	assertNotContains(t, query, "insert")
	assertContains(t, query, `$e isa test-person`)
	assertContains(t, query, `has name "Alice"`)
	assertContains(t, query, `has email "alice@example.com"`)
	assertContains(t, query, `has age 25`)
}

func TestRelationStrategy_BuildPutQuery(t *testing.T) {
	registerTestTypes(t)
	info, _ := LookupType(typeOf[testEmployment]())
	s := &relationStrategy{}

	p := &testPerson{Name: "Alice", Email: "alice@example.com"}
	c := &testCompany{Name: "Acme", Industry: "Tech"}
	emp := &testEmployment{Employee: p, Employer: c}

	query, err := s.BuildPutQuery(info, emp, "r")
	if err != nil {
		t.Fatalf("BuildPutQuery: %v", err)
	}

	assertContains(t, query, "put")
	assertNotContains(t, query, "insert")
	assertContains(t, query, "match")
	assertContains(t, query, `$employee isa test-person, has name "Alice"`)
	assertContains(t, query, `$employer isa test-company, has name "Acme"`)
	assertContains(t, query, "isa test-employment")
}

// --- Test helpers ---

func typeOf[T any]() reflect.Type {
	var zero T
	return reflect.TypeOf(zero)
}

func assertContains(t *testing.T, s, substr string) {
	t.Helper()
	if !strings.Contains(s, substr) {
		t.Errorf("expected %q to contain %q", s, substr)
	}
}

func TestEntityStrategy_BuildMatchAllStrict(t *testing.T) {
	registerTestTypes(t)
	info, _ := LookupType(typeOf[testPerson]())
	s := &entityStrategy{}

	q, err := s.BuildMatchAllStrict(info, "e")
	if err != nil {
		t.Fatalf("BuildMatchAllStrict: %v", err)
	}
	assertContains(t, q, "isa! $t")
	assertContains(t, q, "$t sub test-person")
}

func TestEntityStrategy_BuildFetchAllWithType(t *testing.T) {
	registerTestTypes(t)
	info, _ := LookupType(typeOf[testPerson]())
	s := &entityStrategy{}

	q, err := s.BuildFetchAllWithType(info, "e")
	if err != nil {
		t.Fatalf("BuildFetchAllWithType: %v", err)
	}
	assertContains(t, q, `"_iid": iid($e)`)
	assertContains(t, q, `"_type": label($t)`)
	assertContains(t, q, `"name"`)
}

func TestEntityStrategy_BuildFetchWithRoles(t *testing.T) {
	registerTestTypes(t)
	info, _ := LookupType(typeOf[testPerson]())
	s := &entityStrategy{}

	matchAdd, fetch, err := s.BuildFetchWithRoles(info, "e")
	if err != nil {
		t.Fatalf("BuildFetchWithRoles: %v", err)
	}
	if matchAdd != "" {
		t.Errorf("expected empty match additions for entity, got %q", matchAdd)
	}
	assertContains(t, fetch, `"name": $e.name`)
}

func TestRelationStrategy_BuildFetchWithRoles(t *testing.T) {
	registerTestTypes(t)
	info, _ := LookupType(typeOf[testEmployment]())
	s := &relationStrategy{}

	matchAdd, fetch, err := s.BuildFetchWithRoles(info, "r")
	if err != nil {
		t.Fatalf("BuildFetchWithRoles: %v", err)
	}
	assertContains(t, matchAdd, "$r links (employee: $employee)")
	assertContains(t, matchAdd, "$r links (employer: $employer)")
	assertContains(t, fetch, `"_iid": iid($r)`)
	assertContains(t, fetch, `"start-date": $r.start-date`)
	assertContains(t, fetch, `"employee"`)
	assertContains(t, fetch, `"employer"`)
	assertContains(t, fetch, `"_iid": iid($employee)`)
	assertContains(t, fetch, `"name": $employee.name`)
}

func assertNotContains(t *testing.T, s, substr string) {
	t.Helper()
	if strings.Contains(s, substr) {
		t.Errorf("expected %q to NOT contain %q", s, substr)
	}
}

func assertEqual(t *testing.T, expected, actual string) {
	t.Helper()
	if expected != actual {
		t.Errorf("expected:\n%s\ngot:\n%s", expected, actual)
	}
}
