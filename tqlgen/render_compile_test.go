package tqlgen

import (
	"bytes"
	"go/format"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// renderSchema parses a TypeQL schema, accumulates inheritance, and renders
// Go code with the given config. It returns the generated source and any
// warnings emitted during rendering.
func renderSchema(t *testing.T, schemaSrc string, cfg RenderConfig) (source, warnings string) {
	t.Helper()
	schema, err := ParseSchema(schemaSrc)
	if err != nil {
		t.Fatalf("ParseSchema: %v", err)
	}
	if err := schema.AccumulateInheritance(); err != nil {
		t.Fatalf("AccumulateInheritance: %v", err)
	}
	var out, warn bytes.Buffer
	cfg.WarnWriter = &warn
	if err := Render(&out, schema, cfg); err != nil {
		t.Fatalf("Render: %v", err)
	}
	return out.String(), warn.String()
}

// containsCode reports whether want appears in out, ignoring differences in
// whitespace runs: gofmt-formatted output (issue #107) aligns struct fields,
// tags, and map entries with padding, so exact-substring checks on single
// spaces would be brittle.
func containsCode(out, want string) bool {
	norm := func(s string) string { return strings.Join(strings.Fields(s), " ") }
	return strings.Contains(norm(out), norm(want))
}

// compileGenerated writes the generated source into a temporary module that
// replaces github.com/CaliLuke/go-typeql with the local repository, then runs
// `go build` on it. It fails the test if the generated code does not compile.
func compileGenerated(t *testing.T, source string) {
	t.Helper()

	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot determine test file location")
	}
	repoRoot := filepath.Dir(filepath.Dir(thisFile))

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "models_gen.go"), []byte(source), 0o644); err != nil {
		t.Fatalf("write generated source: %v", err)
	}
	goMod := "module rendercompiletest\n\ngo 1.26.2\n\n" +
		"require github.com/CaliLuke/go-typeql v0.0.0\n\n" +
		"replace github.com/CaliLuke/go-typeql => " + repoRoot + "\n"
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(goMod), 0o644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	if sum, err := os.ReadFile(filepath.Join(repoRoot, "go.sum")); err == nil {
		if err := os.WriteFile(filepath.Join(dir, "go.sum"), sum, 0o644); err != nil {
			t.Fatalf("write go.sum: %v", err)
		}
	}

	env := append(os.Environ(), "GOWORK=off", "GOFLAGS=-mod=mod")
	for _, args := range [][]string{{"mod", "tidy"}, {"build", "./..."}} {
		cmd := exec.Command("go", args...)
		cmd.Dir = dir
		cmd.Env = env
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("go %s: %v\n%s\n--- generated source ---\n%s", strings.Join(args, " "), err, out, source)
		}
	}
}

// TestRenderCompile_NoRolePlayerOmitsField is a regression test for issue #39:
// when no entity or relation plays a role, the generator used to invent a Go
// type from the role name, producing code that referenced undefined types.
// The field must instead be omitted (as a TODO comment) with a warning.
func TestRenderCompile_NoRolePlayerOmitsField(t *testing.T) {
	src := `define
attribute name, value string;
entity person, owns name;
relation friendship, relates friend;
`
	out, warnings := renderSchema(t, src, DefaultConfig())

	if strings.Contains(out, "*Friend") {
		t.Errorf("generated code references invented type *Friend\n%s", out)
	}
	if !strings.Contains(out, `// TODO: no entity or relation plays role "friend"; field Friend omitted.`) {
		t.Errorf("missing TODO comment for playerless role\n%s", out)
	}
	if !strings.Contains(warnings, "no entity or relation plays friendship:friend") {
		t.Errorf("missing warning for playerless role, got: %q", warnings)
	}

	compileGenerated(t, out)
}

// TestRenderCompile_RelationInheritance is a regression test for issue #40:
// relation subtyping with `relates X as Y` used to inherit the overridden
// parent role as a duplicate field, and inherited roles never resolved their
// players because only the subtype's name was matched against plays clauses.
func TestRenderCompile_RelationInheritance(t *testing.T) {
	src := `define
attribute name, value string;
entity person, owns name, plays employment:employee;
entity company, owns name, plays employment:employer;
relation employment @abstract, relates employee, relates employer;
relation management sub employment, relates manager as employee;
`
	out, warnings := renderSchema(t, src, DefaultConfig())

	// The overridden parent role must not survive on the subtype.
	if strings.Contains(out, `typedb:"role:employee"`) {
		t.Errorf("Management struct still carries the overridden employee role\n%s", out)
	}
	// `manager as employee` resolves its player through the parent role.
	if !containsCode(out, "Manager *Person `typedb:\"role:manager\"`") {
		t.Errorf("missing Manager *Person role field\n%s", out)
	}
	// The inherited employer role resolves company's `plays employment:employer`.
	if !containsCode(out, "Employer *Company `typedb:\"role:employer\"`") {
		t.Errorf("missing Employer *Company role field\n%s", out)
	}
	if warnings != "" {
		t.Errorf("unexpected warnings: %q", warnings)
	}

	compileGenerated(t, out)
}

// TestRenderCompile_ModernValueTypes is a regression test for issue #74:
// TypeDB 3.x value types (date, datetime-tz, decimal, duration) used to fall
// through to string silently, as did owns clauses referencing attributes that
// were never defined.
func TestRenderCompile_ModernValueTypes(t *testing.T) {
	src := `define
attribute birth-date, value date;
attribute joined-at, value datetime-tz;
attribute balance, value decimal;
attribute tenure, value duration;
entity person, owns birth-date, owns joined-at, owns balance, owns tenure, owns nickname;
`
	out, warnings := renderSchema(t, src, DefaultConfig())

	for _, want := range []string{
		"BirthDate *time.Time",
		"JoinedAt *time.Time",
		"Balance *float64",
		"Tenure *time.Duration",
		"Nickname *string", // undefined attribute defaults to string, with a warning
		`"time"`,
	} {
		if !containsCode(out, want) {
			t.Errorf("missing %q in generated code\n%s", want, out)
		}
	}
	if !strings.Contains(warnings, `attribute "nickname" owned by entity "person" is not defined in the schema`) {
		t.Errorf("missing undefined-attribute warning, got: %q", warnings)
	}

	compileGenerated(t, out)
}

// TestRenderCompile_MultiValuedAttributes is a regression test for issue #75:
// ownerships with max cardinality > 1 (or unbounded) and list ownerships
// (owns attr[]) must generate slice fields, not scalar pointers.
func TestRenderCompile_MultiValuedAttributes(t *testing.T) {
	src := `define
attribute tag, value string;
attribute score, value integer;
attribute alias, value string;
attribute nick, value string;
entity person, owns tag @card(0..5), owns score @card(1..), owns alias[], owns nick @card(0..1);
`
	out, warnings := renderSchema(t, src, DefaultConfig())

	for _, want := range []string{
		"Tag []string `typedb:\"tag,card=0..5\"`",   // bounded multi-value keeps the card tag
		"Score []int64 `typedb:\"score,card=1..\"`", // unbounded upper limit
		"Alias []string `typedb:\"alias\"`",         // list ownership (owns alias[])
		"Nick *string `typedb:\"nick,card=0..1\"`",  // max 1 stays an optional scalar
	} {
		if !containsCode(out, want) {
			t.Errorf("missing %q in generated code\n%s", want, out)
		}
	}
	if warnings != "" {
		t.Errorf("unexpected warnings: %q", warnings)
	}

	compileGenerated(t, out)
}

// TestRenderCompile_ListRoles covers the relation half of the multi-valued
// treatment: list roles (relates role[]) and roles whose cardinality allows
// more than one player must generate slice fields ([]*Player), matching the
// slice role players the ORM supports, while plain roles stay scalar
// pointers.
func TestRenderCompile_ListRoles(t *testing.T) {
	src := `define
attribute name, value string;
entity person, owns name, plays team:member, plays team:leader, plays guild:participant;
relation team, relates member[], relates leader;
relation guild, relates participant @card(0..);
`
	out, warnings := renderSchema(t, src, DefaultConfig())

	for _, want := range []string{
		"Member []*Person `typedb:\"role:member\"`",           // list role (relates member[])
		"Leader *Person `typedb:\"role:leader\"`",             // plain role stays scalar
		"Participant []*Person `typedb:\"role:participant\"`", // many-cardinality role
	} {
		if !containsCode(out, want) {
			t.Errorf("missing %q in generated code\n%s", want, out)
		}
	}
	if warnings != "" {
		t.Errorf("unexpected warnings: %q", warnings)
	}

	compileGenerated(t, out)
}

// TestRenderCompile_DecimalValueTag covers the decimal contract with gotype:
// decimal attributes keep float64 Go fields but carry value:decimal in the
// generated tag (after key/unique, before card), so gotype registers the
// attribute as TypeDB decimal and preserves precision on the wire.
func TestRenderCompile_DecimalValueTag(t *testing.T) {
	src := `define
attribute price, value decimal;
attribute discount-rate, value decimal;
attribute cost, value decimal;
entity product, owns price @key, owns cost, owns discount-rate @card(0..);
`
	out, warnings := renderSchema(t, src, DefaultConfig())

	for _, want := range []string{
		"Price float64 `typedb:\"price,key,value:decimal\"`",                       // key decimal, tag order key < value:decimal
		"Cost *float64 `typedb:\"cost,value:decimal\"`",                            // optional decimal
		"DiscountRate []float64 `typedb:\"discount-rate,value:decimal,card=0..\"`", // value:decimal before card
		"// decimal: precision is preserved on the wire; the Go field is float64",
	} {
		if !containsCode(out, want) {
			t.Errorf("missing %q in generated code\n%s", want, out)
		}
	}
	if warnings != "" {
		t.Errorf("unexpected warnings: %q", warnings)
	}

	compileGenerated(t, out)
}

// TestRenderCompile_EnumValueSanitization is a regression test for issue #72:
// @values strings containing '/', '+', spaces, or quotes used to be spliced
// into constant names and values verbatim, producing invalid Go.
func TestRenderCompile_EnumValueSanitization(t *testing.T) {
	src := `define
attribute grade, value string @values("n/a", "a+", "b c", "with \"quote\"", "1st");
entity student, owns grade;
`
	out, warnings := renderSchema(t, src, DefaultConfig())

	for _, want := range []string{
		`GradeNA = "n/a"`,
		`GradeA = "a+"`,
		`GradeBC = "b c"`,
		`GradeWithQuote = "with \"quote\""`,
		`Grade1st = "1st"`,
	} {
		if !containsCode(out, want) {
			t.Errorf("missing %q in generated code\n%s", want, out)
		}
	}
	if warnings != "" {
		t.Errorf("unexpected warnings: %q", warnings)
	}

	compileGenerated(t, out)
}

// TestRenderCompile_StructDefinitions is a regression test for issue #78:
// TypeQL struct definitions were parsed and then silently dropped from the
// generated models. They now map onto plain Go structs, with optional fields
// as pointers and struct-typed fields referencing the generated struct.
func TestRenderCompile_StructDefinitions(t *testing.T) {
	src := `define
attribute name, value string;
entity person, owns name;
struct address:
    street value string,
    zip value integer?;
struct contact:
    home value address?;
`
	out, warnings := renderSchema(t, src, DefaultConfig())

	for _, want := range []string{
		`// Address is generated from TypeQL struct "address".`,
		"type Address struct {",
		"Street string `typedb:\"street\"`",
		"Zip *int64 `typedb:\"zip\"`",
		"type Contact struct {",
		"Home *Address `typedb:\"home\"`", // struct-typed field references the generated struct
	} {
		if !containsCode(out, want) {
			t.Errorf("missing %q in generated code\n%s", want, out)
		}
	}
	if warnings != "" {
		t.Errorf("unexpected warnings: %q", warnings)
	}

	compileGenerated(t, out)
}

// TestRender_FunctionSkippedWarning covers the function half of issue #78:
// TypeQL functions have no Go codegen, and dropping them must say so.
func TestRender_FunctionSkippedWarning(t *testing.T) {
	src := `define
attribute name, value string;
entity person, owns name;
fun person_count() -> integer:
match
$p isa person;
return count($p);
`
	_, warnings := renderSchema(t, src, DefaultConfig())
	if !strings.Contains(warnings, `function "person_count" has no Go codegen; skipped`) {
		t.Errorf("missing skipped-function warning, got: %q", warnings)
	}
}

// TestRenderCompile_RegexRangeConstraintComments is a regression test for
// issue #77: @regex and @range constraints were parsed and then discarded by
// every renderer. Models now carry them as comments on the generated fields.
func TestRenderCompile_RegexRangeConstraintComments(t *testing.T) {
	src := `define
attribute username, value string @regex("^[a-z]+$");
attribute age, value integer @range(0..150);
entity person, owns username, owns age;
`
	out, warnings := renderSchema(t, src, DefaultConfig())

	if !strings.Contains(out, `// @regex("^[a-z]+$")`) {
		t.Errorf("missing @regex constraint comment\n%s", out)
	}
	if !strings.Contains(out, "// @range(0..150)") {
		t.Errorf("missing @range constraint comment\n%s", out)
	}
	if warnings != "" {
		t.Errorf("unexpected warnings: %q", warnings)
	}

	compileGenerated(t, out)
}

// TestRender_OutputIsGofmtFormatted is a regression test for issue #107:
// generated models must come out of Render already gofmt-formatted.
func TestRender_OutputIsGofmtFormatted(t *testing.T) {
	src := `define
attribute name, value string;
attribute status, value string @values("open", "closed");
attribute created-at, value datetime;
entity person, owns name @key, owns status, owns created-at, plays employment:employee;
entity company, owns name @key, plays employment:employer;
relation employment, relates employee, relates employer;
struct address:
    street value string,
    zip value integer?;
`
	out, warnings := renderSchema(t, src, DefaultConfig())
	if warnings != "" {
		t.Errorf("unexpected warnings: %q", warnings)
	}

	formatted, err := format.Source([]byte(out))
	if err != nil {
		t.Fatalf("generated code is not valid Go: %v\n%s", err, out)
	}
	if string(formatted) != out {
		t.Errorf("Render output is not gofmt-formatted:\n--- got ---\n%s\n--- want ---\n%s", out, formatted)
	}
}

// TestRender_NameCollisionErrors is a regression test for issue #76: schema
// labels that fold to the same Go identifier used to emit duplicate
// declarations silently; Render must fail with both source labels named.
func TestRender_NameCollisionErrors(t *testing.T) {
	renderErr := func(t *testing.T, schemaSrc string) error {
		t.Helper()
		schema, err := ParseSchema(schemaSrc)
		if err != nil {
			t.Fatalf("ParseSchema: %v", err)
		}
		if err := schema.AccumulateInheritance(); err != nil {
			t.Fatalf("AccumulateInheritance: %v", err)
		}
		cfg := DefaultConfig()
		cfg.WarnWriter = &bytes.Buffer{}
		return Render(&bytes.Buffer{}, schema, cfg)
	}

	cases := []struct {
		name    string
		schema  string
		wantErr []string
	}{
		{
			name: "field collision within one entity",
			schema: `define
attribute user-name, value string;
attribute user_name, value integer;
entity account, owns user-name, owns user_name;
`,
			wantErr: []string{`"user-name"`, `"user_name"`, `"UserName"`},
		},
		{
			name: "type collision between entities",
			schema: `define
attribute name, value string;
entity user-account, owns name;
entity user_account, owns name;
`,
			wantErr: []string{`entity "user-account"`, `entity "user_account"`, `"UserAccount"`},
		},
		{
			name: "role and attribute collision within one relation",
			schema: `define
attribute owner, value string;
entity person, plays ownership:owner;
relation ownership, relates owner, owns owner;
`,
			wantErr: []string{`role "owner"`, `attribute "owner"`, `"Owner"`},
		},
		{
			name: "enum constant collision",
			schema: `define
attribute status, value string @values("a-b", "a_b");
entity ticket, owns status;
`,
			wantErr: []string{`"a-b"`, `"a_b"`, `"StatusAB"`},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := renderErr(t, tc.schema)
			if err == nil {
				t.Fatal("Render succeeded, want name-collision error")
			}
			for _, want := range tc.wantErr {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error %q missing %q", err, want)
				}
			}
		})
	}
}
