package gotype

import (
	"errors"
	"testing"
)

func TestIsReservedWord(t *testing.T) {
	reserved := []string{
		"label", "entity", "relation", "attribute", "match", "fetch",
		"insert", "delete", "iid", "isa", "has", "sub", "owns",
		"true", "false", "string", "boolean", "integer", "double",
	}
	for _, w := range reserved {
		if !IsReservedWord(w) {
			t.Errorf("expected %q to be reserved", w)
		}
	}
}

func TestIsReservedWord_CaseInsensitive(t *testing.T) {
	if !IsReservedWord("Label") {
		t.Error("expected 'Label' (uppercase) to be reserved")
	}
	if !IsReservedWord("ENTITY") {
		t.Error("expected 'ENTITY' (all caps) to be reserved")
	}
}

func TestIsReservedWord_NotReserved(t *testing.T) {
	notReserved := []string{
		"person", "company", "name", "email", "age", "score",
		"employment", "friendship", "username",
	}
	for _, w := range notReserved {
		if IsReservedWord(w) {
			t.Errorf("expected %q to NOT be reserved", w)
		}
	}
}

// BadEntity uses "label" as attribute name — should be rejected.
type reservedAttrEntity struct {
	BaseEntity
	Label string `typedb:"label,key"`
}

func TestRegister_RejectsReservedAttributeName(t *testing.T) {
	ClearRegistry()
	err := Register[reservedAttrEntity]()
	if err == nil {
		t.Fatal("expected error for reserved attribute name 'label'")
	}
	var rwe *ReservedWordError
	if !errors.As(err, &rwe) {
		t.Fatalf("expected ReservedWordError, got %T: %v", err, err)
	}
	if rwe.Word != "label" {
		t.Errorf("expected word 'label', got %q", rwe.Word)
	}
	if rwe.Context != "attribute" {
		t.Errorf("expected context 'attribute', got %q", rwe.Context)
	}
}

// We can't easily test type name rejection for auto-derived names like "entity"
// since Go struct names are user-chosen. But we can test with a struct whose
// lowercase name happens to be a reserved word. In practice, this is rare,
// so we test the IsReservedWord function coverage above.

func TestReservedWordError_Message(t *testing.T) {
	err := &ReservedWordError{Word: "label", Context: "attribute"}
	msg := err.Error()
	assertContains(t, msg, "label")
	assertContains(t, msg, "reserved keyword")
	assertContains(t, msg, "attribute")
}
