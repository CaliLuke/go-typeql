package gotype

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/CaliLuke/go-typeql/v2/ast"
	"github.com/CaliLuke/go-typeql/v2/internal/naming"
)

// Filter compilation and the per-query variable allocator (issue #138).
//
// Every variable of a filter query comes from one varAlloc per query build.
// The allocator keys variables by meaning (varKey), so distinct meanings get
// distinct names by construction, and callers never write variable names.
// formal/lean/Allocator.lean and formal/lean/Scopes.lean model this code;
// the rules R1–R8 below refer to the specification in issue #138.

// varFamily is the kind of a generated variable. Each family is its own key
// space (R8), so, for example, a Computed result and a reduce output never
// share a key even when their labels are equal.
type varFamily int

const (
	famRoot varFamily = iota
	famAttr
	famPlayer
	famComputed
	famReduce
	famOld
)

// varKey identifies a variable by meaning (R1). label is the attribute label
// (famAttr), the role label (famPlayer), a counter id (famComputed), or an
// index (famReduce, famOld). scope is the id of the scope from the query
// counter; 0 is the query scope.
type varKey struct {
	family varFamily
	owner  string
	label  string
	scope  int
}

// varAlloc is the variable table of one query build.
type varAlloc struct {
	names map[varKey]string
	used  map[string]bool
	next  int // query counter for scope ids and Computed ids (R2, R6)
	// trace, when set, records every variable occurrence in the form of the
	// Lean model (Scopes.lean, emit). The differential test uses it.
	trace *[]traceOcc
}

// traceOcc is one variable occurrence: a binding, a use in the same scope,
// or a use of the owner.
type traceOcc struct {
	N     string `json:"n"`
	Fam   string `json:"fam"`
	Scope int    `json:"scope"`
	Kind  string `json:"kind"`
}

var famNames = [...]string{famRoot: "root", famAttr: "attr", famPlayer: "player",
	famComputed: "computed", famReduce: "reduce", famOld: "old"}

func (a *varAlloc) record(n string, fam varFamily, scope int, kind string) {
	if a.trace != nil {
		*a.trace = append(*a.trace, traceOcc{N: n, Fam: famNames[fam], Scope: scope, Kind: kind})
	}
}

func newVarAlloc() *varAlloc {
	return &varAlloc{names: make(map[varKey]string), used: make(map[string]bool), next: 1}
}

// reserve marks names that a builder writes itself (for example the fixed
// variables of a cached fetch projection), so no key is allocated them.
func (a *varAlloc) reserve(names ...string) {
	for _, n := range names {
		a.used[n] = true
	}
}

// fresh returns the next id of the query counter.
func (a *varAlloc) fresh() int {
	id := a.next
	a.next++
	return id
}

// name returns the variable name of k and reports whether this call
// allocated it. The same key always gets the same name (R3, R4); a new key
// gets its readable candidate, or the candidate with the first free suffix
// _2, _3, ... (R8).
func (a *varAlloc) name(k varKey) (string, bool) {
	if n, ok := a.names[k]; ok {
		return n, false
	}
	c := candidate(k)
	n := c
	for i := 2; a.used[n]; i++ {
		n = c + "_" + strconv.Itoa(i)
	}
	a.names[k] = n
	a.used[n] = true
	return n, true
}

// candidate is the readable name of a key (R8). Every candidate is a valid
// TypeQL variable name when the labels are valid (formal: designCand,
// query_names_valid).
func candidate(k varKey) string {
	switch k.family {
	case famRoot:
		return "e"
	case famAttr:
		owner := k.owner
		if k.scope != 0 {
			owner += "_s" + strconv.Itoa(k.scope)
		}
		return owner + "__" + naming.VarLabel(k.label)
	case famPlayer:
		return naming.RootSpelling(k.label)
	case famComputed, famReduce:
		return "result" + k.label
	case famOld:
		return "old" + k.label
	}
	panic(fmt.Sprintf("gotype: unknown variable family %d", k.family))
}

// compileCtx is the context of one filter compilation: the allocator, the
// owner (the query root, or the player of the enclosing RolePlayer filter),
// and the current scope.
type compileCtx struct {
	alloc    *varAlloc
	owner    string
	ownerFam varFamily
	scope    int
}

// child opens a child scope (an Or branch or a Not body). Only the owner
// crosses the boundary; every other key belongs to the new scope (R2).
func (c *compileCtx) child() *compileCtx {
	return &compileCtx{alloc: c.alloc, owner: c.owner, ownerFam: c.ownerFam, scope: c.alloc.fresh()}
}

// attr returns the variable of attribute label of the owner in the current
// scope, and the binding pattern when this is its first use in the scope
// (R3, R5).
func (c *compileCtx) attr(label string) (string, []ast.Pattern) {
	n, isNew := c.alloc.name(varKey{family: famAttr, owner: c.owner, label: label, scope: c.scope})
	c.alloc.record(c.owner, c.ownerFam, c.scope, "ownerUse")
	defer c.alloc.record(n, famAttr, c.scope, "use")
	if !isNew {
		return n, nil
	}
	c.alloc.record(n, famAttr, c.scope, "bind")
	return n, []ast.Pattern{ast.HasPattern{ThingVar: "$" + c.owner, AttrType: label, AttrVar: "$" + n}}
}

// player returns the player variable of role on the owner in the current
// scope, and the links pattern when this is its first use in the scope (R4).
func (c *compileCtx) player(role string) (string, []ast.Pattern) {
	n, isNew := c.alloc.name(varKey{family: famPlayer, owner: c.owner, label: role, scope: c.scope})
	c.alloc.record(c.owner, c.ownerFam, c.scope, "ownerUse")
	if !isNew {
		return n, nil
	}
	c.alloc.record(n, famPlayer, c.scope, "bind")
	return n, []ast.Pattern{ast.RawPattern{Content: fmt.Sprintf("$%s links (%s: $%s)", c.owner, role, n)}}
}

// compileFilters compiles filters in the context c.
func compileFilters(c *compileCtx, filters []Filter) ([]ast.Pattern, error) {
	var out []ast.Pattern
	for _, f := range filters {
		if f == nil {
			return nil, fmt.Errorf("gotype: filter must not be nil")
		}
		ps, err := f.compile(c)
		if err != nil {
			return nil, err
		}
		out = append(out, ps...)
	}
	return out, nil
}

var patternCompiler ast.Compiler

// renderPatterns renders top-level patterns, one per line, each ending in ";".
func renderPatterns(b *strings.Builder, ps []ast.Pattern) error {
	for _, p := range ps {
		s, err := patternCompiler.Compile(p)
		if err != nil {
			return err
		}
		b.WriteByte('\n')
		b.WriteString(s)
		b.WriteByte(';')
	}
	return nil
}

// matchBuilder builds the match clause of one query: the root, the filter
// patterns, and the variables that the query builder adds in the query scope
// (sort attributes, reduce outputs, old values).
type matchBuilder struct {
	typeName string
	ctx      *compileCtx
	patterns []ast.Pattern
}

// newMatchBuilder allocates the root and compiles filters. reserved names
// are names that the builder writes itself.
func newMatchBuilder(typeName string, filters []Filter, reserved ...string) (*matchBuilder, error) {
	alloc := newVarAlloc()
	alloc.reserve(reserved...)
	return newMatchBuilderWith(alloc, typeName, filters)
}

// newMatchBuilderWith is newMatchBuilder with a given allocator.
func newMatchBuilderWith(alloc *varAlloc, typeName string, filters []Filter) (*matchBuilder, error) {
	root, _ := alloc.name(varKey{family: famRoot})
	alloc.record(root, famRoot, 0, "bind")
	m := &matchBuilder{typeName: typeName, ctx: &compileCtx{alloc: alloc, owner: root, ownerFam: famRoot}}
	ps, err := compileFilters(m.ctx, filters)
	if err != nil {
		return nil, err
	}
	m.patterns = ps
	return m, nil
}

// root returns the root variable name (without "$").
func (m *matchBuilder) root() string { return m.ctx.owner }

// attr returns the query-scope variable of attribute label of the root and
// adds its binding if no filter bound it (R3, R5).
func (m *matchBuilder) attr(label string) string {
	n, bind := m.ctx.attr(label)
	m.patterns = append(m.patterns, bind...)
	return n
}

// output returns the variable of the output with the given index in family
// famReduce or famOld.
func (m *matchBuilder) output(fam varFamily, index int) string {
	n, _ := m.ctx.alloc.name(varKey{family: fam, label: strconv.Itoa(index)})
	m.ctx.alloc.record(n, fam, 0, "bind")
	m.ctx.alloc.record(n, fam, 0, "use")
	return n
}

// match renders the match clause: "match", the root pattern, and every
// pattern, one per line.
func (m *matchBuilder) match() (string, error) {
	var b strings.Builder
	b.WriteString("match\n$")
	b.WriteString(m.root())
	b.WriteString(" isa ")
	b.WriteString(m.typeName)
	b.WriteByte(';')
	if err := renderPatterns(&b, m.patterns); err != nil {
		return "", err
	}
	return b.String(), nil
}
