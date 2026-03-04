package ast

import (
	"fmt"
	"strings"
)

// IdentifierMatcher determines whether an identifier should be treated as an IID.
type IdentifierMatcher interface {
	IsIID(identifier string) bool
}

// PrefixIdentifierMatcher considers values with the configured prefix as IIDs.
type PrefixIdentifierMatcher struct {
	Prefix string
}

// IsIID reports whether identifier matches the configured IID prefix.
func (m PrefixIdentifierMatcher) IsIID(identifier string) bool {
	if m.Prefix == "" {
		return false
	}
	return strings.HasPrefix(identifier, m.Prefix)
}

// DefaultIdentifierMatcher treats values prefixed with "0x" as TypeDB IIDs.
var DefaultIdentifierMatcher IdentifierMatcher = PrefixIdentifierMatcher{Prefix: "0x"}

// DeleteArtifactOptions configures DeleteArtifact matching behavior.
type DeleteArtifactOptions struct {
	VarName string
	IDAttr  string
	Matcher IdentifierMatcher
}

// PaginatedSearchOptions configures PaginatedSearch output and pagination behavior.
type PaginatedSearchOptions struct {
	VarName        string
	Limit          int
	Offset         int
	Sort           string // "name" for asc, "-name" for desc
	FetchIIDKey    string
	FetchEntityKey string
}

type fluentState struct {
	compiler         Compiler
	mainVar          string
	matchPatterns    []Pattern
	matchLet         []LetAssignment
	deleteStatements []Statement
	insertStatements []Statement
	fetchItems       []FetchItem
	selectVars       []string
	sortClause       *SortClause
	offsetClause     *OffsetClause
	limitClause      *LimitClause
}

func (s fluentState) clone() fluentState {
	cp := fluentState{
		compiler:         s.compiler,
		mainVar:          s.mainVar,
		matchPatterns:    append([]Pattern(nil), s.matchPatterns...),
		matchLet:         append([]LetAssignment(nil), s.matchLet...),
		deleteStatements: append([]Statement(nil), s.deleteStatements...),
		insertStatements: append([]Statement(nil), s.insertStatements...),
		fetchItems:       append([]FetchItem(nil), s.fetchItems...),
		selectVars:       append([]string(nil), s.selectVars...),
	}
	if s.sortClause != nil {
		sortCopy := *s.sortClause
		cp.sortClause = &sortCopy
	}
	if s.offsetClause != nil {
		offsetCopy := *s.offsetClause
		cp.offsetClause = &offsetCopy
	}
	if s.limitClause != nil {
		limitCopy := *s.limitClause
		cp.limitClause = &limitCopy
	}
	return cp
}

func (s fluentState) nodes() []QueryNode {
	nodes := make([]QueryNode, 0, 8)
	if len(s.matchLet) > 0 {
		nodes = append(nodes, MatchLetClause{Assignments: s.matchLet})
	} else if len(s.matchPatterns) > 0 {
		nodes = append(nodes, Match(s.matchPatterns...))
	}
	if len(s.deleteStatements) > 0 {
		nodes = append(nodes, Delete(s.deleteStatements...))
	}
	if len(s.insertStatements) > 0 {
		nodes = append(nodes, Insert(s.insertStatements...))
	}
	if len(s.fetchItems) > 0 {
		nodes = append(nodes, Fetch(s.fetchItems...))
	}
	if len(s.selectVars) > 0 {
		nodes = append(nodes, Select(s.selectVars...))
	}
	if s.sortClause != nil {
		nodes = append(nodes, *s.sortClause)
	}
	if s.offsetClause != nil {
		nodes = append(nodes, *s.offsetClause)
	}
	if s.limitClause != nil {
		nodes = append(nodes, *s.limitClause)
	}
	return nodes
}

func (s fluentState) build() (string, error) {
	return s.compiler.CompileBatch(s.nodes(), "")
}

// MatchStage is the pre-output stage for match queries.
// It supports matching/mutation operations and can transition to MatchResultStage.
type MatchStage interface {
	Has(attrName string, value any) MatchStage
	Iid(iid string) MatchStage
	MatchByIdentifier(identifier, attrName string, matcher IdentifierMatcher) MatchStage
	Set(attrName string, value any) MatchStage
	DeleteThing() MatchStage
	Fetch(varName string, attrNames ...string) MatchResultStage
	Select(vars ...string) MatchResultStage
	Build() (string, error)
	Nodes() []QueryNode
}

// MatchResultStage is the output stage for match queries.
// It supports fetch/select output shaping and pagination/sorting.
type MatchResultStage interface {
	Fetch(varName string, attrNames ...string) MatchResultStage
	Select(vars ...string) MatchResultStage
	Sort(variable, direction string) MatchResultStage
	Limit(count int) MatchResultStage
	Offset(count int) MatchResultStage
	Build() (string, error)
	Nodes() []QueryNode
}

// FunctionStage is the pre-output stage for function-based match-let queries.
type FunctionStage interface {
	Select(vars ...string) FunctionResultStage
}

// FunctionResultStage is the output stage for function queries.
type FunctionResultStage interface {
	Select(vars ...string) FunctionResultStage
	Sort(variable, direction string) FunctionResultStage
	Limit(count int) FunctionResultStage
	Offset(count int) FunctionResultStage
	Build() (string, error)
	Nodes() []QueryNode
}

// MatchBuilder is the concrete immutable builder for entity-first match queries.
type MatchBuilder struct {
	state fluentState
}

// MatchOutputBuilder is the concrete immutable output-stage builder for match queries.
type MatchOutputBuilder struct {
	state fluentState
}

// FunctionBuilder is the concrete immutable pre-output builder for function queries.
type FunctionBuilder struct {
	state fluentState
}

// FunctionOutputBuilder is the concrete immutable output-stage builder for function queries.
type FunctionOutputBuilder struct {
	state fluentState
}

// FluentMatch starts a fluent query with a primary matched variable/type.
func FluentMatch(varName, typeName string) MatchStage {
	v := ensureVar(varName)
	return MatchBuilder{state: fluentState{
		mainVar:       v,
		matchPatterns: []Pattern{Entity(v, typeName)},
	}}
}

// MatchFunction starts a fluent function query compiled as match-let.
func MatchFunction(funcName string, args ...any) FunctionStage {
	compiledArgs := make([]string, 0, len(args))
	for _, arg := range args {
		switch v := arg.(type) {
		case string:
			if strings.HasPrefix(v, "$") {
				compiledArgs = append(compiledArgs, v)
			} else {
				compiledArgs = append(compiledArgs, FormatGoValue(v))
			}
		default:
			compiledArgs = append(compiledArgs, FormatGoValue(v))
		}
	}

	call := fmt.Sprintf("%s(%s)", funcName, strings.Join(compiledArgs, ", "))
	return FunctionBuilder{state: fluentState{
		mainVar: "$result",
		matchLet: []LetAssignment{{
			Variables:  []string{"$result"},
			Expression: call,
		}},
	}}
}

// Has adds a has constraint to the primary matched variable.
func (b MatchBuilder) Has(attrName string, value any) MatchStage {
	next := b.state.clone()
	next.addPatternConstraint(Has(attrName, ValueFromGo(value)))
	return MatchBuilder{state: next}
}

// Iid adds an iid constraint to the primary matched variable.
func (b MatchBuilder) Iid(iid string) MatchStage {
	next := b.state.clone()
	next.addPatternConstraint(Iid(iid))
	return MatchBuilder{state: next}
}

// MatchByIdentifier matches by IID (as determined by matcher) or falls back to attribute matching.
func (b MatchBuilder) MatchByIdentifier(identifier, attrName string, matcher IdentifierMatcher) MatchStage {
	if matcher == nil {
		matcher = DefaultIdentifierMatcher
	}
	if matcher.IsIID(identifier) {
		return b.Iid(identifier)
	}
	return b.Has(attrName, identifier)
}

// Set emits a standard Match-Delete-Insert sequence for updating one attribute.
func (b MatchBuilder) Set(attrName string, value any) MatchStage {
	next := b.state.clone()
	oldVar := "$old_" + sanitizeIdentifier(attrName)
	next.matchPatterns = append(next.matchPatterns, HasPattern{ThingVar: next.mainVar, AttrType: attrName, AttrVar: oldVar})
	next.deleteStatements = append(next.deleteStatements, DeleteHas(oldVar, next.mainVar))
	next.insertStatements = append(next.insertStatements, HasStmt(next.mainVar, attrName, ValueFromGo(value)))
	return MatchBuilder{state: next}
}

// DeleteThing deletes the primary matched variable.
func (b MatchBuilder) DeleteThing() MatchStage {
	next := b.state.clone()
	next.deleteStatements = append(next.deleteStatements, DeleteThingStatement{Variable: next.mainVar})
	return MatchBuilder{state: next}
}

// Fetch fetches one or more attributes from a variable.
func (b MatchBuilder) Fetch(varName string, attrNames ...string) MatchResultStage {
	next := b.state.clone()
	v := ensureVar(varName)
	for _, attr := range attrNames {
		next.fetchItems = append(next.fetchItems, FetchAttr(attr, v, attr))
	}
	return MatchOutputBuilder{state: next}
}

// Select adds a select clause with projected variables.
func (b MatchBuilder) Select(vars ...string) MatchResultStage {
	next := b.state.clone()
	for _, v := range vars {
		next.selectVars = append(next.selectVars, ensureVar(v))
	}
	return MatchOutputBuilder{state: next}
}

// Nodes returns the compiled AST node sequence before string compilation.
func (b MatchBuilder) Nodes() []QueryNode {
	return b.state.nodes()
}

// Build compiles the fluent query into TypeQL.
func (b MatchBuilder) Build() (string, error) {
	return b.state.build()
}

// Fetch adds fetch attributes in output stage.
func (b MatchOutputBuilder) Fetch(varName string, attrNames ...string) MatchResultStage {
	next := b.state.clone()
	v := ensureVar(varName)
	for _, attr := range attrNames {
		next.fetchItems = append(next.fetchItems, FetchAttr(attr, v, attr))
	}
	return MatchOutputBuilder{state: next}
}

// Select adds a select clause with projected variables in output stage.
func (b MatchOutputBuilder) Select(vars ...string) MatchResultStage {
	next := b.state.clone()
	for _, v := range vars {
		next.selectVars = append(next.selectVars, ensureVar(v))
	}
	return MatchOutputBuilder{state: next}
}

// Sort configures a sort clause in output stage.
func (b MatchOutputBuilder) Sort(variable, direction string) MatchResultStage {
	next := b.state.clone()
	s := Sort(ensureVar(variable), direction)
	next.sortClause = &s
	return MatchOutputBuilder{state: next}
}

// Limit configures a limit clause in output stage.
func (b MatchOutputBuilder) Limit(count int) MatchResultStage {
	next := b.state.clone()
	l := Limit(count)
	next.limitClause = &l
	return MatchOutputBuilder{state: next}
}

// Offset configures an offset clause in output stage.
func (b MatchOutputBuilder) Offset(count int) MatchResultStage {
	next := b.state.clone()
	o := Offset(count)
	next.offsetClause = &o
	return MatchOutputBuilder{state: next}
}

// Nodes returns the compiled AST node sequence before string compilation.
func (b MatchOutputBuilder) Nodes() []QueryNode {
	return b.state.nodes()
}

// Build compiles the fluent query into TypeQL.
func (b MatchOutputBuilder) Build() (string, error) {
	return b.state.build()
}

// Select transitions function query to output stage.
func (b FunctionBuilder) Select(vars ...string) FunctionResultStage {
	next := b.state.clone()
	for _, v := range vars {
		next.selectVars = append(next.selectVars, ensureVar(v))
	}
	return FunctionOutputBuilder{state: next}
}

// Select adds additional selected variables in output stage.
func (b FunctionOutputBuilder) Select(vars ...string) FunctionResultStage {
	next := b.state.clone()
	for _, v := range vars {
		next.selectVars = append(next.selectVars, ensureVar(v))
	}
	return FunctionOutputBuilder{state: next}
}

// Sort configures a sort clause.
func (b FunctionOutputBuilder) Sort(variable, direction string) FunctionResultStage {
	next := b.state.clone()
	s := Sort(ensureVar(variable), direction)
	next.sortClause = &s
	return FunctionOutputBuilder{state: next}
}

// Limit configures a limit clause.
func (b FunctionOutputBuilder) Limit(count int) FunctionResultStage {
	next := b.state.clone()
	l := Limit(count)
	next.limitClause = &l
	return FunctionOutputBuilder{state: next}
}

// Offset configures an offset clause.
func (b FunctionOutputBuilder) Offset(count int) FunctionResultStage {
	next := b.state.clone()
	o := Offset(count)
	next.offsetClause = &o
	return FunctionOutputBuilder{state: next}
}

// Nodes returns the compiled AST node sequence before string compilation.
func (b FunctionOutputBuilder) Nodes() []QueryNode {
	return b.state.nodes()
}

// Build compiles the fluent query into TypeQL.
func (b FunctionOutputBuilder) Build() (string, error) {
	return b.state.build()
}

// UpdateAttribute builds the standard Match-Delete-Insert sequence for one attribute update.
func UpdateAttribute(varName, typeName, attrName string, value any) (string, error) {
	return FluentMatch(varName, typeName).
		Set(attrName, value).
		Build()
}

// DeleteArtifact builds a delete query that matches by IID or fallback attribute.
func DeleteArtifact(identifier, typeName string) (string, error) {
	return DeleteArtifactWithOptions(identifier, typeName, DeleteArtifactOptions{})
}

// DeleteArtifactWithOptions builds a delete query with custom identifier matching options.
func DeleteArtifactWithOptions(identifier, typeName string, opts DeleteArtifactOptions) (string, error) {
	varName := opts.VarName
	if varName == "" {
		varName = "n"
	}
	idAttr := opts.IDAttr
	if idAttr == "" {
		idAttr = "display_id"
	}
	matcher := opts.Matcher
	if matcher == nil {
		matcher = DefaultIdentifierMatcher
	}

	return FluentMatch(varName, typeName).
		MatchByIdentifier(identifier, idAttr, matcher).
		DeleteThing().
		Build()
}

// PaginatedSearch builds a standard typed search with sorting and pagination/fetch options.
func PaginatedSearch(types []string, opts PaginatedSearchOptions) (string, error) {
	varName := opts.VarName
	if varName == "" {
		varName = "n"
	}
	v := ensureVar(varName)

	state := fluentState{mainVar: v}
	if len(types) == 1 {
		state.matchPatterns = []Pattern{Entity(v, types[0])}
	}
	if len(types) > 1 {
		alternatives := make([][]Pattern, 0, len(types))
		for _, typeName := range types {
			alternatives = append(alternatives, []Pattern{Entity(v, typeName)})
		}
		state.matchPatterns = []Pattern{Or(alternatives...)}
	}

	if opts.Sort != "" {
		direction := "asc"
		sortAttr := opts.Sort
		if strings.HasPrefix(sortAttr, "-") {
			direction = "desc"
			sortAttr = strings.TrimPrefix(sortAttr, "-")
		}
		s := Sort(v+"."+sortAttr, direction)
		state.sortClause = &s
	}
	if opts.Offset > 0 {
		o := Offset(opts.Offset)
		state.offsetClause = &o
	}
	if opts.Limit > 0 {
		l := Limit(opts.Limit)
		state.limitClause = &l
	}

	iidKey := opts.FetchIIDKey
	if iidKey == "" {
		iidKey = "_iid"
	}
	entityKey := opts.FetchEntityKey
	if entityKey == "" {
		entityKey = "entity"
	}
	state.fetchItems = []FetchItem{
		FetchFunc(iidKey, "iid", v),
		FetchWildcard{Key: entityKey, Var: v},
	}

	return MatchBuilder{state: state}.Build()
}

func (s *fluentState) addPatternConstraint(constraint Constraint) {
	if len(s.matchPatterns) == 0 {
		return
	}
	first, ok := s.matchPatterns[0].(EntityPattern)
	if !ok {
		return
	}
	first.Constraints = append(first.Constraints, constraint)
	s.matchPatterns[0] = first
}

func ensureVar(v string) string {
	if strings.HasPrefix(v, "$") {
		return v
	}
	return "$" + v
}

func sanitizeIdentifier(s string) string {
	if s == "" {
		return "attr"
	}
	var b strings.Builder
	for _, ch := range s {
		if (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || (ch >= '0' && ch <= '9') {
			b.WriteRune(ch)
		} else {
			b.WriteByte('_')
		}
	}
	return b.String()
}
