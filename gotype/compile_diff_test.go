package gotype

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"testing"
)

// Differential test against the Lean model (issue #138). Random filter trees
// in the model's subset go through the Go compiler and through the model's
// `emit` (formal/lean/Scopes.lean, run by formal/lean/DiffMain.lean). Both
// outputs are normalized, then compared:
//
//   - duplicate bindings of one (variable, scope) pair are removed;
//   - scope ids are renamed by first appearance;
//   - names are renamed by first appearance within their family.
//
// Equal normal forms mean the same binding coverage and the same variable
// sharing: which occurrences share a variable, and the scope of each
// variable. The names themselves can differ. Separately, the test asserts
// that Go emits exactly one binding for each variable and scope (R5).
//
// The model binary is a soft dependency, like typeql-check. Build it with:
//
//	cd formal/lean && lake build diffmodel

type diffSpec struct {
	Filter  any      `json:"filter"`
	Sorts   []string `json:"sorts"`
	Reduces []string `json:"reduces"`
	Olds    int      `json:"olds"`

	goFilter Filter
}

// diffGen builds a random filter tree as a Go filter and as model JSON.
type diffGen struct{ r *rand.Rand }

var (
	diffAttrs = []string{"age", "name", "first-name", "first_name", "e"}
	diffRoles = []string{"member", "e", "first_author"}
)

func (g diffGen) label(pool []string) string { return pool[g.r.IntN(len(pool))] }

func (g diffGen) expr(depth int) (Expr, any) {
	switch n := g.r.IntN(3); {
	case depth <= 0 || n == 0:
		l := g.label(diffAttrs)
		return Attr(l), map[string]any{"attr": l}
	case n == 1:
		return Literal(1), "lit"
	default:
		a, ja := g.expr(depth - 1)
		b, jb := g.expr(depth - 1)
		return Add(a, b), map[string]any{"bin": []any{ja, jb}}
	}
}

func (g diffGen) filter(depth int) (Filter, any) {
	n := g.r.IntN(6)
	if depth <= 0 {
		n = g.r.IntN(2)
	}
	switch n {
	case 0:
		l := g.label(diffAttrs)
		return Eq(l, "x"), map[string]any{"cmp": l}
	case 1:
		e, je := g.expr(2)
		return Computed(e, ">", 1), map[string]any{"computed": je}
	case 2:
		role := g.label(diffRoles)
		f, jf := g.filter(depth - 1)
		return RolePlayer(role, f), map[string]any{"role": []any{role, jf}}
	case 3:
		a, ja := g.filter(depth - 1)
		b, jb := g.filter(depth - 1)
		return And(a, b), map[string]any{"and": []any{ja, jb}}
	case 4:
		a, ja := g.filter(depth - 1)
		b, jb := g.filter(depth - 1)
		return Or(a, b), map[string]any{"or": []any{ja, jb}}
	default:
		f, jf := g.filter(depth - 1)
		return Not(f), map[string]any{"not": jf}
	}
}

func (g diffGen) spec() diffSpec {
	f, jf := g.filter(3)
	s := diffSpec{Filter: jf, goFilter: f, Sorts: []string{}, Reduces: []string{}}
	for range g.r.IntN(3) {
		s.Sorts = append(s.Sorts, g.label(diffAttrs))
	}
	for range g.r.IntN(3) {
		s.Reduces = append(s.Reduces, g.label(diffAttrs))
	}
	s.Olds = g.r.IntN(3)
	return s
}

// goTrace compiles spec with the Go compiler, and calls the matchBuilder
// operations of the builders in the model's order: sort and reduce
// attributes, then reduce outputs, then old values.
func goTrace(t *testing.T, s diffSpec) []traceOcc {
	t.Helper()
	var tr []traceOcc
	alloc := newVarAlloc()
	alloc.trace = &tr
	m, err := newMatchBuilderWith(alloc, "t", []Filter{s.goFilter})
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	for _, l := range slices.Concat(s.Sorts, s.Reduces) {
		m.attr(l)
	}
	for i := range s.Reduces {
		m.output(famReduce, i)
	}
	for i := range s.Olds {
		m.output(famOld, i)
	}
	return tr
}

// normalize applies the normal form described at the top of this file.
func normalize(tr []traceOcc) map[string][]string {
	type bindKey struct {
		n     string
		scope int
	}
	bound := map[bindKey]bool{}
	scopes := map[int]int{}
	names := map[string]map[string]int{}
	out := map[string][]string{}
	for _, o := range tr {
		if o.Kind == "bind" {
			k := bindKey{o.N, o.Scope}
			if bound[k] {
				continue
			}
			bound[k] = true
		}
		if _, ok := scopes[o.Scope]; !ok {
			scopes[o.Scope] = len(scopes)
		}
		fam := names[o.Fam]
		if fam == nil {
			fam = map[string]int{}
			names[o.Fam] = fam
		}
		if _, ok := fam[o.N]; !ok {
			fam[o.N] = len(fam)
		}
		out[o.Fam] = append(out[o.Fam], fmt.Sprintf("%d@s%d:%s", fam[o.N], scopes[o.Scope], o.Kind))
	}
	return out
}

func diffModelPath(t *testing.T) string {
	t.Helper()
	_, file, _, _ := runtime.Caller(0)
	p := filepath.Join(filepath.Dir(file), "..", "formal", "lean", ".lake", "build", "bin", "diffmodel")
	if _, err := os.Stat(p); err != nil {
		t.Skipf("Lean model binary not built (%v); run: cd formal/lean && lake build diffmodel", err)
	}
	return p
}

func TestCompileDiff_LeanModel(t *testing.T) {
	bin := diffModelPath(t)
	g := diffGen{r: rand.New(rand.NewPCG(138, 3))}
	const cases = 500
	specs := make([]diffSpec, cases)
	for i := range specs {
		specs[i] = g.spec()
	}
	input, err := json.Marshal(specs)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(bin)
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("diffmodel: %v\n%s", err, stderr.String())
	}
	var model [][]traceOcc
	if err := json.Unmarshal(output, &model); err != nil {
		t.Fatalf("diffmodel output: %v", err)
	}
	if len(model) != cases {
		t.Fatalf("diffmodel returned %d results for %d specs", len(model), cases)
	}
	for i, s := range specs {
		gtr := goTrace(t, s)
		// R5: Go emits exactly one binding for each variable and scope.
		seen := map[string]bool{}
		for _, o := range gtr {
			if o.Kind == "bind" {
				k := o.N + "@" + strconv.Itoa(o.Scope)
				if seen[k] {
					t.Fatalf("case %d: Go binds %s twice in scope %d", i, o.N, o.Scope)
				}
				seen[k] = true
			}
		}
		gn, mn := normalize(gtr), normalize(model[i])
		for _, fam := range []string{"root", "attr", "player", "computed", "reduce", "old"} {
			if !slices.Equal(gn[fam], mn[fam]) {
				spec, _ := json.Marshal(s)
				t.Fatalf("case %d: family %s differs from the Lean model\nspec: %s\ngo:    %v\nmodel: %v",
					i, fam, spec, gn[fam], mn[fam])
			}
		}
	}
}
