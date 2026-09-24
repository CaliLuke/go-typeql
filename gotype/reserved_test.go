package gotype

import (
	"errors"
	"os"
	"regexp"
	"testing"
)

func TestIsReservedWord(t *testing.T) {
	reserved := []string{
		"entity", "relation", "attribute", "given", "match", "fetch",
		"insert", "delete", "iid", "isa", "has", "sub", "owns",
		"true", "false", "role", "end", "last",
	}
	for _, w := range reserved {
		if !IsReservedWord(w) {
			t.Errorf("expected %q to be reserved", w)
		}
	}
}

// The set is exactly the reserved rule of the grammar: every grammar keyword
// is in the set, and the set has no other word.
func TestTypeQLReservedWords_MatchesGrammarReservedRule(t *testing.T) {
	grammar, err := os.ReadFile("../typeql-reference/typeql.pest")
	if err != nil {
		t.Fatalf("read TypeQL grammar: %v", err)
	}

	reservedRule := regexp.MustCompile(`(?ms)^reserved = \{(.*?)\n\s*\}`).FindSubmatch(grammar)
	if len(reservedRule) != 2 {
		t.Fatal("reserved rule not found in TypeQL grammar")
	}
	tokens := regexp.MustCompile(`[A-Z][A-Z_]*`).FindAllString(string(reservedRule[1]), -1)
	if len(tokens) == 0 {
		t.Fatal("reserved rule contains no tokens")
	}

	grammarWords := map[string]bool{}
	for _, token := range tokens {
		definition := regexp.MustCompile(`(?m)^` + regexp.QuoteMeta(token) + `\s*=.*?"([^"]+)"`).FindSubmatch(grammar)
		if len(definition) != 2 {
			t.Fatalf("reserved token %s has no literal definition", token)
		}
		word := string(definition[1])
		grammarWords[word] = true
		if !TypeQLReservedWords[word] {
			t.Errorf("TypeQLReservedWords does not contain grammar keyword %q", word)
		}
	}
	for word := range TypeQLReservedWords {
		if !grammarWords[word] {
			t.Errorf("TypeQLReservedWords contains %q, which is not in the grammar reserved rule", word)
		}
	}
}

// The server rejects reserved keywords only in lowercase.
func TestIsReservedWord_CaseSensitive(t *testing.T) {
	for _, w := range []string{"Match", "ENTITY", "Label"} {
		if IsReservedWord(w) {
			t.Errorf("expected %q to NOT be reserved", w)
		}
	}
}

func TestIsReservedWord_NotReserved(t *testing.T) {
	notReserved := []string{
		"person", "company", "name", "email", "age", "score",
		"employment", "friendship", "username",
		// TypeQL words that the server accepts as names (TypeDB 3.13.6).
		"label", "count", "string", "value", "key", "abs", "len", "length", "let", "select", "std",
	}
	for _, w := range notReserved {
		if IsReservedWord(w) {
			t.Errorf("expected %q to NOT be reserved", w)
		}
	}
}

// reservedAttrEntity uses "match" as attribute name, which must be rejected.
type reservedAttrEntity struct {
	BaseEntity
	Match string `typedb:"match,key"`
}

func TestRegister_RejectsReservedAttributeName(t *testing.T) {
	ClearRegistry()
	err := Register[reservedAttrEntity]()
	if err == nil {
		t.Fatal("expected error for reserved attribute name 'match'")
	}
	rwe, ok := errors.AsType[*ReservedWordError](err)
	if !ok {
		t.Fatalf("expected ReservedWordError, got %T: %v", err, err)
	}
	if rwe.Word != "match" {
		t.Errorf("expected word 'match', got %q", rwe.Word)
	}
	if rwe.Context != "attribute" {
		t.Errorf("expected context 'attribute', got %q", rwe.Context)
	}
}

// We can't easily test type name rejection for auto-derived names like "entity"
// since Go struct names are user-chosen. But we can test with a struct whose
// lowercase name happens to be a reserved word. In practice, this is rare,
// so we test the IsReservedWord function coverage above.

// --- ValidateIdentifier ---

func TestValidateIdentifier_Valid(t *testing.T) {
	valid := []string{"person", "my-entity", "name_attr", "_private", "a1b2"}
	for _, name := range valid {
		if err := ValidateIdentifier(name, "test"); err != nil {
			t.Errorf("expected %q to be valid, got: %v", name, err)
		}
	}
}

func TestValidateIdentifier_Invalid(t *testing.T) {
	tests := []struct {
		name   string
		reason string
	}{
		{"", "empty"},
		{"123abc", "must start with"},
		{"my entity", "invalid character"},
		{"foo.bar", "invalid character"},
		{"@attr", "must start with"},
	}
	for _, tt := range tests {
		err := ValidateIdentifier(tt.name, "test")
		if err == nil {
			t.Errorf("expected %q to be invalid", tt.name)
			continue
		}
		if tt.name != "" {
			if _, ok := errors.AsType[*InvalidIdentifierError](err); !ok {
				t.Errorf("expected InvalidIdentifierError for %q, got %T", tt.name, err)
			}
		}
	}
}

func TestReservedWordError_Message(t *testing.T) {
	err := &ReservedWordError{Word: "match", Context: "attribute"}
	msg := err.Error()
	assertContains(t, msg, "match")
	assertContains(t, msg, "reserved keyword")
	assertContains(t, msg, "attribute")
}
