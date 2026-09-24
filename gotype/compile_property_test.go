package gotype

import (
	"math/rand/v2"
	"regexp"
	"testing"

	"github.com/CaliLuke/go-typeql/v2/internal/typeqlcheck"
)

// Property-based test of the filter compiler (issue #138): for random filter
// trees, every variable is a valid TypeQL variable, each variable is bound
// once (R5), the text is deterministic, and typeql-check accepts the query.

var (
	propAttrs = []string{"name", "age", "first-name", "first_name", "a-b_c", "e"}
	propRoles = []string{"member", "e", "first_author", "first-author"}
)

type filterGen struct{ r *rand.Rand }

func (g filterGen) attr() string { return propAttrs[g.r.IntN(len(propAttrs))] }

func (g filterGen) expr(depth int) Expr {
	switch n := g.r.IntN(5); {
	case depth <= 0 || n == 0:
		return Attr(g.attr())
	case n == 1:
		return Literal(g.r.IntN(10))
	case n == 2:
		return Abs(g.expr(depth - 1))
	case n == 3:
		return Max(g.expr(depth-1), g.expr(depth-1))
	default:
		return []func(a, b Expr) Expr{Add, Sub, Mul}[g.r.IntN(3)](g.expr(depth-1), g.expr(depth-1))
	}
}

func (g filterGen) leaf() Filter {
	switch g.r.IntN(9) {
	case 0:
		return Eq(g.attr(), "x")
	case 1:
		return Gt(g.attr(), g.r.IntN(100))
	case 2:
		return In(g.attr(), []any{1, 2})
	case 3:
		return NotIn(g.attr(), []any{3})
	case 4:
		return Range(g.attr(), 1, 9)
	case 5:
		return Contains(g.attr(), "a")
	case 6:
		return HasAttr(g.attr())
	case 7:
		return NotHasAttr(g.attr())
	default:
		return Computed(g.expr(2), ">", g.r.IntN(50))
	}
}

func (g filterGen) filter(depth int) Filter {
	if depth <= 0 {
		return g.leaf()
	}
	switch g.r.IntN(6) {
	case 0:
		return And(g.filter(depth-1), g.filter(depth-1))
	case 1:
		return Or(g.filter(depth-1), g.filter(depth-1), g.filter(depth-1))
	case 2:
		return Not(g.filter(depth - 1))
	case 3:
		return RolePlayer(propRoles[g.r.IntN(len(propRoles))], g.filter(depth-1))
	default:
		return g.leaf()
	}
}

var (
	varTokenRe = regexp.MustCompile(`\$([A-Za-z0-9_-]*)`)
	hasBindRe  = regexp.MustCompile(`has [A-Za-z0-9_-]+ \$([A-Za-z0-9_-]+)`)
)

func TestCompileProperty_RandomTrees(t *testing.T) {
	registerMultiValueTypes(t)
	mgr := MustNewManager[testTeam](NewDatabase(&mockConn{}, "test_db"))
	g := filterGen{r: rand.New(rand.NewPCG(138, 1))}
	checkSyntax := typeqlcheck.Available()
	const cases = 300
	for i := range cases {
		f := g.filter(3)
		q, err := mgr.Query().Filter(f).OrderAsc(g.attr()).buildQuery()
		if err != nil {
			t.Fatalf("case %d: buildQuery: %v", i, err)
		}
		for _, m := range varTokenRe.FindAllStringSubmatch(q, -1) {
			if name := m[1]; name != "_" && !validVarRe.MatchString(name) {
				t.Fatalf("case %d: invalid variable $%s in:\n%s", i, name, q)
			}
		}
		// Each variable name belongs to one key and so to one scope; a name
		// bound twice would be a duplicate binding (R5).
		seen := map[string]bool{}
		for _, m := range hasBindRe.FindAllStringSubmatch(q, -1) {
			if name := m[1]; name != "_" {
				if seen[name] {
					t.Fatalf("case %d: $%s bound twice in:\n%s", i, name, q)
				}
				seen[name] = true
			}
		}
		if checkSyntax {
			if err := typeqlcheck.Validate(q); err != nil {
				t.Fatalf("case %d: typeql-check rejected the query: %v\n%s", i, err, q)
			}
		}
	}
}

// Determinism on random trees: two builds of one tree give the same text.
func TestCompileProperty_Deterministic(t *testing.T) {
	registerMultiValueTypes(t)
	mgr := MustNewManager[testTeam](NewDatabase(&mockConn{}, "test_db"))
	g := filterGen{r: rand.New(rand.NewPCG(138, 2))}
	for i := range 200 {
		f := g.filter(3)
		a, err := mgr.Query().Filter(f).buildQuery()
		if err != nil {
			t.Fatalf("case %d: %v", i, err)
		}
		if b, _ := mgr.Query().Filter(f).buildQuery(); a != b {
			t.Fatalf("case %d: text differs between builds:\n%s\n---\n%s", i, a, b)
		}
	}
}
