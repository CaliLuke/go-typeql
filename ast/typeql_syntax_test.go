package ast

import (
	"testing"
	"time"

	"github.com/CaliLuke/go-typeql/internal/typeqlcheck"
)

// assertTypeQL validates a generated query with the official typeql-check CLI
// (soft dependency — see internal/typeqlcheck). knownIssue marks output that
// is currently invalid because of an open bug: the case is skipped while the
// bug exists and fails loudly once the output becomes valid, so the marker
// must be removed when the issue is fixed.
func assertTypeQL(t *testing.T, label, query, knownIssue string) {
	t.Helper()
	if knownIssue == "" {
		typeqlcheck.AssertValid(t, label, query)
		return
	}
	if !typeqlcheck.Available() {
		return
	}
	if err := typeqlcheck.Validate(query); err != nil {
		t.Skipf("%s: known invalid output (%s): %v", label, knownIssue, err)
	}
	t.Errorf("%s: output is now valid TypeQL — %s appears fixed, remove the knownIssue marker", label, knownIssue)
}

// TestTypeQLSyntax_Compiler pipes representative compiler output through
// typeql-check so syntax regressions fail here instead of at a live server.
func TestTypeQLSyntax_Compiler(t *testing.T) {
	c := &Compiler{}

	cases := []struct {
		name       string
		nodes      []QueryNode
		knownIssue string
	}{
		{
			name: "match entity with constraints",
			nodes: []QueryNode{Match(
				Entity("$p", "person",
					Has("name", Str("Alice")),
					Has("age", Long(30)),
				),
			)},
		},
		{
			name: "match with escaped string value",
			nodes: []QueryNode{Match(
				Entity("$p", "person", Has("bio", Str("line1\nhe said \"hi\" \\ done"))),
			)},
		},
		{
			name: "match relation with role players",
			nodes: []QueryNode{Match(
				Entity("$p", "person"),
				Entity("$c", "company"),
				Relation("$e", "employment", []RolePlayer{
					Role("employee", "$p"),
					Role("employer", "$c"),
				}),
			)},
		},
		{
			name: "match iid and strict isa",
			nodes: []QueryNode{Match(
				Entity("$p", "person", Iid("0x1e00000000000000000000")),
			)},
		},
		{
			name: "match value comparison",
			nodes: []QueryNode{
				Match(
					Entity("$p", "person", Has("age", "$a")),
					Cmp("$a", ">", Long(18)),
				),
			},
		},
		{
			name: "match select sort offset limit",
			nodes: []QueryNode{
				Match(Entity("$p", "person", Has("name", "$n"))),
				Select("$p", "$n"),
				Sort("$n", "asc"),
				Offset(10),
				Limit(5),
			},
		},
		{
			name: "match fetch attributes",
			nodes: []QueryNode{
				Match(Entity("$p", "person")),
				Fetch(
					FetchAttr("name", "$p", "name"),
					FetchVar("entity", "$p"),
				),
			},
		},
		{
			name: "match reduce count",
			nodes: []QueryNode{
				Match(Entity("$p", "person")),
				ReduceClause{Assignments: []ReduceAssignment{
					{Variable: "$c", Expression: FuncCall("count", "$p")},
				}},
			},
		},
		{
			// Issue #25: bare Go values in Has/Cmp are emitted as literals.
			name: "match with bare Go values",
			nodes: []QueryNode{Match(
				Entity("$p", "person",
					Has("name", "Alice"),
					Has("age", 42),
					Has("active", true),
				),
			)},
		},
		{
			// Issue #25: injection-shaped values compile to escaped literals.
			name: "match with injection-shaped string value",
			nodes: []QueryNode{Match(
				Entity("$p", "person", Has("name", `x"; delete $p; match $q isa person, has name "y`)),
			)},
		},
		{
			// Issue #25: bare Go value comparison.
			name: "match value comparison with bare Go value",
			nodes: []QueryNode{
				Match(
					Entity("$p", "person", Has("age", "$a")),
					Cmp("$a", ">", 18),
				),
			},
		},
		{
			// Issue #63: AggregateExpr compiles in reduce assignments.
			name: "match reduce aggregate expr",
			nodes: []QueryNode{
				Match(Entity("$p", "person")),
				ReduceClause{Assignments: []ReduceAssignment{
					{Variable: "$c", Expression: AggregateExpr{FuncName: "count", Var: "$p"}},
				}},
			},
		},
		{
			// Issue #64: narrow integer widths and time values via ValueFromGo.
			name: "insert narrow int widths",
			nodes: []QueryNode{Insert(
				IsaStmt("$e", "event"),
				HasStmt("$e", "age", ValueFromGo(int32(5))),
				HasStmt("$e", "code", ValueFromGo(uint16(7))),
				HasStmt("$e", "created", ValueFromGo(time.Date(2024, 1, 15, 10, 30, 0, 0, time.UTC))),
			)},
		},
		{
			// Issues #53/#66: datetime literals are naive UTC (never date or
			// datetime-tz literals), keep sub-second precision, and midnight
			// values stay datetime literals.
			name: "insert datetime literal shapes",
			nodes: []QueryNode{Insert(
				IsaStmt("$e", "event"),
				HasStmt("$e", "created", ValueFromGo(time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC))),
				HasStmt("$e", "updated", ValueFromGo(time.Date(2024, 1, 15, 12, 30, 0, 123456789, time.FixedZone("UTC+2", 2*60*60)))),
			)},
		},
		{
			// Issue #66: explicit datetime-tz literals carry the offset.
			name: "insert datetime-tz literal",
			nodes: []QueryNode{Insert(
				IsaStmt("$e", "event"),
				HasStmt("$e", "seen-at", Lit(time.Date(2024, 1, 15, 12, 30, 0, 500000000, time.FixedZone("UTC+2", 2*60*60)), "datetime-tz")),
				HasStmt("$e", "logged-at", Lit(time.Date(2024, 1, 15, 10, 30, 0, 0, time.UTC), "datetime-tz")),
			)},
		},
		{
			// Issue #81: FetchAttrPath with a bare variable fetches the variable.
			name: "fetch attr path variants",
			nodes: []QueryNode{
				Match(Entity("$p", "person")),
				Fetch(
					FetchAttrPath("name", "$p.name"),
					FetchAttrPath("entity", "$p"),
				),
			},
		},
		{
			// Issue #82: fetch keys are escaped into valid string literals.
			name: "fetch with quote in key",
			nodes: []QueryNode{
				Match(Entity("$p", "person")),
				Fetch(FetchAttr(`weird"key`, "$p", "name")),
			},
		},
		{
			name: "insert entity with attributes",
			nodes: []QueryNode{Insert(
				IsaStmt("$p", "person"),
				HasStmt("$p", "name", Str("Bob")),
				HasStmt("$p", "age", Long(41)),
			)},
		},
		{
			name: "match delete has",
			nodes: []QueryNode{
				Match(
					Entity("$p", "person", Has("name", Str("Bob"))),
					Entity("$a", "age"),
				),
				Delete(DeleteHas("$a", "$p")),
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			query, err := c.CompileBatch(tc.nodes, "\n")
			if err != nil {
				t.Fatalf("compile error: %v", err)
			}
			assertTypeQL(t, tc.name, query, tc.knownIssue)
		})
	}
}

// TestTypeQLSyntax_Fluent validates the fluent layer's full-query output.
func TestTypeQLSyntax_Fluent(t *testing.T) {
	t.Run("match fetch (terminal)", func(t *testing.T) {
		q, err := FluentMatch("p", "person").
			Has("name", "Alice").
			Fetch("p", "name", "email").
			Build()
		if err != nil {
			t.Fatalf("build error: %v", err)
		}
		assertTypeQL(t, "fluent match+fetch", q, "")
	})

	t.Run("update attribute template", func(t *testing.T) {
		q, err := UpdateAttribute("n", "user-story", "status", "done")
		if err != nil {
			t.Fatalf("build error: %v", err)
		}
		assertTypeQL(t, "UpdateAttribute", q, "")
	})

	t.Run("fetch before sort/limit", func(t *testing.T) {
		q, err := FluentMatch("p", "person").
			Has("name", "Alice").
			Fetch("p", "name").
			Sort("name", "asc").
			Limit(10).
			Build()
		if err != nil {
			t.Fatalf("build error: %v", err)
		}
		assertTypeQL(t, "fluent fetch+sort+limit", q, "")
	})

	t.Run("sibling-branched builders", func(t *testing.T) {
		base := FluentMatch("p", "person").Has("a", 1).Has("b", 2).Has("c", 3)
		branchA := base.Has("y", 20)
		branchB := base.Has("z", 30)

		qA, err := branchA.Build()
		if err != nil {
			t.Fatalf("branch A build error: %v", err)
		}
		assertTypeQL(t, "fluent sibling branch A", qA, "")

		qB, err := branchB.Build()
		if err != nil {
			t.Fatalf("branch B build error: %v", err)
		}
		assertTypeQL(t, "fluent sibling branch B", qB, "")
	})

	t.Run("paginated search with sort", func(t *testing.T) {
		q, err := PaginatedSearch([]string{"person"}, PaginatedSearchOptions{
			VarName: "n",
			Sort:    "-name",
			Limit:   10,
			Offset:  5,
		})
		if err != nil {
			t.Fatalf("build error: %v", err)
		}
		assertTypeQL(t, "PaginatedSearch", q, "")
	})
}
