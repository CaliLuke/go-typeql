package tqlgen

import (
	"testing"

	"github.com/CaliLuke/go-typeql/internal/typeqlcheck"
)

// TestTypeQLSyntax_ParserConformance cross-checks the tqlgen parser against
// the official typeql-check CLI: every schema the official grammar accepts
// should also be parseable by tqlgen (or be a documented gap). Cases with a
// knownIssue are skipped while the gap exists and fail loudly once tqlgen
// accepts them, so the marker must be removed when the issue is fixed.
func TestTypeQLSyntax_ParserConformance(t *testing.T) {
	if !typeqlcheck.Available() {
		t.Skip("typeql-check binary not installed (run: make install-typeql-check)")
	}

	cases := []struct {
		name       string
		schema     string
		knownIssue string
	}{
		{
			name: "flat entities and relation",
			schema: `define
attribute name, value string;
attribute start-date, value datetime;
entity person, owns name @key, plays employment:employee;
entity company, owns name @key, plays employment:employer;
relation employment, relates employee, relates employer, owns start-date;
`,
		},
		{
			name: "keyword-prefixed hyphenated labels",
			schema: `define
attribute is-active, value boolean;
attribute max-age, value integer;
entity person, owns is-active, owns max-age;
`,
		},
		{
			name: "abstract attribute with subtyping",
			schema: `define
attribute id @abstract;
attribute email sub id, value string;
entity person, owns email;
`,
		},
		{
			name: "definitions after a function",
			schema: `define
attribute name, value string;
fun person_count() -> integer:
match
$p isa person;
return count($p);
entity person, owns name;
`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := typeqlcheck.Validate(tc.schema); err != nil {
				t.Fatalf("test schema is not valid TypeQL — fix the fixture: %v", err)
			}

			parsed, err := ParseSchema(tc.schema)
			if tc.knownIssue != "" {
				if err != nil {
					t.Skipf("known tqlgen parser gap (%s): %v", tc.knownIssue, err)
				}
				t.Errorf("tqlgen now accepts this schema — %s appears fixed, remove the knownIssue marker", tc.knownIssue)
				return
			}
			if err != nil {
				t.Fatalf("tqlgen rejected a schema typeql-check accepts: %v", err)
			}
			// The parser must retain every definition, not just parse without
			// error (a fun block once silently swallowed trailing definitions).
			if len(parsed.Entities) == 0 {
				t.Errorf("no entities parsed from schema:\n%s", tc.schema)
			}
		})
	}
}

// TestAttributeSubtypingResolution verifies that subtyped attributes without
// an explicit value clause inherit it from their parent chain.
func TestAttributeSubtypingResolution(t *testing.T) {
	parsed, err := ParseSchema(`define
attribute id @abstract, value string;
attribute email sub id;
attribute work-email sub email;
entity person, owns work-email;
`)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	got := map[string]AttributeSpec{}
	for _, a := range parsed.Attributes {
		got[a.Name] = a
	}
	if !got["id"].Abstract {
		t.Error("expected id to be abstract")
	}
	if got["email"].Parent != "id" {
		t.Errorf("expected email parent id, got %q", got["email"].Parent)
	}
	for _, name := range []string{"email", "work-email"} {
		if got[name].ValueType != "string" {
			t.Errorf("expected %s to inherit value type string, got %q", name, got[name].ValueType)
		}
	}
}
