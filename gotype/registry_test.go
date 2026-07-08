package gotype

import (
	"reflect"
	"strings"
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

// Issue #29: role names are validated at registration time.

type reservedRoleRelation struct {
	BaseRelation
	Target *TestPerson `typedb:"role:delete"`
}

type invalidRoleRelation struct {
	BaseRelation
	Target *TestPerson `typedb:"role:has space"`
}

func TestRegister_RoleNameValidation(t *testing.T) {
	ClearRegistry()

	if err := Register[reservedRoleRelation](); err == nil {
		t.Fatal("expected error for reserved role name 'delete'")
	}
	if err := Register[invalidRoleRelation](); err == nil {
		t.Fatal("expected error for role name with whitespace")
	}
}

// Issue #33: RegisteredTypes, and everything derived from it, is deterministic.

type orderModelA struct {
	BaseEntity
	Name string `typedb:"order-a-name,key"`
}

type orderModelB struct {
	BaseEntity
	Name string `typedb:"order-b-name,key"`
}

type orderModelC struct {
	BaseEntity
	Name string `typedb:"order-c-name,key"`
}

type orderModelD struct {
	BaseRelation
	Left  *orderModelA `typedb:"role:order-left"`
	Right *orderModelB `typedb:"role:order-right"`
}

type orderModelE struct {
	BaseEntity
	Name string `typedb:"order-e-name,key"`
}

func TestRegisteredTypes_DeterministicOrder(t *testing.T) {
	ClearRegistry()
	MustRegister[orderModelE]()
	MustRegister[orderModelC]()
	MustRegister[orderModelA]()
	MustRegister[orderModelD]()
	MustRegister[orderModelB]()

	want := []string{"order-model-a", "order-model-b", "order-model-c", "order-model-d", "order-model-e"}
	for run := 0; run < 50; run++ {
		types := RegisteredTypes()
		if len(types) != len(want) {
			t.Fatalf("got %d types, want %d", len(types), len(want))
		}
		for i, info := range types {
			if info.TypeName != want[i] {
				t.Fatalf("run %d: types[%d] = %q, want %q", run, i, info.TypeName, want[i])
			}
		}
	}
}

func TestGenerateSchema_StableAcrossRuns(t *testing.T) {
	ClearRegistry()
	MustRegister[orderModelD]()
	MustRegister[orderModelB]()
	MustRegister[orderModelE]()
	MustRegister[orderModelA]()
	MustRegister[orderModelC]()

	first := GenerateSchema()
	for run := 0; run < 50; run++ {
		if got := GenerateSchema(); got != first {
			t.Fatalf("run %d: GenerateSchema output changed:\nfirst:\n%s\ngot:\n%s", run, first, got)
		}
	}
}

// Registered supertypes must precede their subtypes even when the child
// sorts first alphabetically, so per-statement migrations define parents first.
type aaaChildModel struct {
	BaseEntity `typedb:"sub:zzz-parent-model"`
	Name       string `typedb:"aaa-child-name,key"`
}

type zzzParentModel struct {
	BaseEntity `typedb:"abstract"`
	Name       string `typedb:"zzz-parent-name,key"`
}

func TestRegisteredTypes_SupertypesFirst(t *testing.T) {
	ClearRegistry()
	MustRegister[aaaChildModel]()
	MustRegister[zzzParentModel]()

	types := RegisteredTypes()
	if len(types) != 2 {
		t.Fatalf("got %d types, want 2", len(types))
	}
	if types[0].TypeName != "zzz-parent-model" || types[1].TypeName != "aaa-child-model" {
		t.Fatalf("expected parent before child, got [%s, %s]", types[0].TypeName, types[1].TypeName)
	}
}

// Issue #91: sub: makes SubtypesOf reachable.
func TestSubtypesOf_WithSubTag(t *testing.T) {
	ClearRegistry()
	MustRegister[zzzParentModel]()
	MustRegister[aaaChildModel]()

	subs := SubtypesOf("zzz-parent-model")
	if len(subs) != 1 || subs[0].TypeName != "aaa-child-model" {
		t.Fatalf("expected [aaa-child-model], got %#v", subs)
	}
}

type selfSupertypeModel struct {
	BaseEntity `typedb:"sub:self-supertype-model"`
	Name       string `typedb:"name,key"`
}

func TestRegister_SelfSupertypeErrors(t *testing.T) {
	ClearRegistry()
	if err := Register[selfSupertypeModel](); err == nil {
		t.Fatal("expected error for self-referential supertype")
	}
}

// Issue #54: conflicting value types for a shared attribute name fail at
// registration instead of emitting a self-contradictory define.

type conflictAgeIntModel struct {
	BaseEntity
	Name string `typedb:"conflict-name,key"`
	Age  int    `typedb:"conflict-age"`
}

type conflictAgeStringModel struct {
	BaseEntity
	Title string `typedb:"conflict-title,key"`
	Age   string `typedb:"conflict-age"`
}

func TestRegister_AttributeValueTypeConflict(t *testing.T) {
	ClearRegistry()
	MustRegister[conflictAgeIntModel]()

	err := Register[conflictAgeStringModel]()
	if err == nil {
		t.Fatal("expected error for conflicting value types of attribute conflict-age")
	}
	if !strings.Contains(err.Error(), "conflict-age") {
		t.Errorf("error should name the attribute, got: %v", err)
	}

	// Re-registering the same model stays idempotent.
	if err := Register[conflictAgeIntModel](); err != nil {
		t.Fatalf("re-register: %v", err)
	}
}

type duplicateAttrModel struct {
	BaseEntity
	A string `typedb:"dup-attr,key"`
	B int    `typedb:"dup-attr"`
}

func TestRegister_DuplicateAttributeInModelErrors(t *testing.T) {
	ClearRegistry()
	if err := Register[duplicateAttrModel](); err == nil {
		t.Fatal("expected error for duplicate attribute name within one model")
	}
}

// Issue #92: Go type names that collide case-insensitively must not silently
// overwrite each other in the byGoName index.

type ABTest struct {
	BaseEntity
	Name string `typedb:"ab-test-name,key"`
}

type AbTest struct {
	BaseEntity
	Name string `typedb:"abtest-name,key"`
}

func TestRegister_GoNameCollisionErrors(t *testing.T) {
	ClearRegistry()
	MustRegister[ABTest]()

	if err := Register[AbTest](); err == nil {
		t.Fatal("expected error for case-insensitive Go name collision")
	}
}
