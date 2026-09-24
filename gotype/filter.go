package gotype

import (
	"fmt"
	"reflect"
	"regexp"
	"strconv"
	"time"

	"github.com/CaliLuke/go-typeql/v2/ast"
)

// Filter is a query filter. Filters compose via And, Or, and Not.
//
// The interface is sealed: only this package implements it. Query builders
// compile filters through one variable allocator per query (see compile.go),
// so variables never collide and callers never write variable names.
//
// All filter types in this package also implement Validate() error, which
// reports construction problems (invalid attribute or role names, malformed
// IIDs, non-scalar values, unknown operators). Query builders validate
// filters before they compile them, so misuse surfaces as an error from
// Execute/Count/... instead of malformed TypeQL reaching the server.
type Filter interface {
	compile(c *compileCtx) ([]ast.Pattern, error)
}

// --- Identifier and filter validation ---

var (
	// iidPattern matches TypeDB internal IDs: 0x followed by hex digits.
	iidPattern = regexp.MustCompile(`^0x[0-9a-fA-F]+$`)
	// identifierPattern matches TypeQL identifiers (attribute and role names).
	identifierPattern = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9_-]*$`)
)

// validateIID checks that iid looks like a TypeDB internal ID (0x + hex).
// IIDs are interpolated raw into query text, so anything else is rejected
// with a descriptive error instead of producing broken or injected TypeQL.
func validateIID(iid string) error {
	if !iidPattern.MatchString(iid) {
		return fmt.Errorf("gotype: invalid IID %q: must match 0x[0-9a-fA-F]+", iid)
	}
	return nil
}

// validateAttrName checks that attr is a plausible TypeQL identifier.
// Attribute names are interpolated raw into query text, so anything else is
// rejected with a descriptive error instead of producing injected TypeQL.
func validateAttrName(attr string) error {
	if !identifierPattern.MatchString(attr) {
		return fmt.Errorf("gotype: invalid attribute name %q: must match [a-zA-Z][a-zA-Z0-9_-]*", attr)
	}
	return nil
}

// validateComparisonOp checks that op is a supported TypeQL comparison operator.
func validateComparisonOp(op string) error {
	switch op {
	case "==", "!=", ">", ">=", "<", "<=":
		return nil
	}
	return fmt.Errorf("gotype: invalid comparison operator %q", op)
}

// filterValidator is implemented by every filter type in this package.
type filterValidator interface{ Validate() error }

// validateFilters returns the first construction error found in filters.
// Combinators validate their children recursively.
func validateFilters(filters ...Filter) error {
	for _, f := range filters {
		if f == nil {
			return fmt.Errorf("gotype: filter must not be nil")
		}
		if v, ok := f.(filterValidator); ok {
			if err := v.Validate(); err != nil {
				return err
			}
		}
	}
	return nil
}

// --- Comparison filters ---

// ComparisonFilter compares an attribute to a value using a TypeQL operator.
type ComparisonFilter struct {
	Attr    string
	Op      string
	Value   any
	Negated bool
}

// Validate reports construction errors: an invalid attribute name, an
// unsupported operator, or a non-scalar comparison value (use In for set
// membership). Query execution calls this before building query text.
func (f *ComparisonFilter) Validate() error {
	if err := validateAttrName(f.Attr); err != nil {
		return err
	}
	if err := validateComparisonOp(f.Op); err != nil {
		return err
	}
	if !isScalarFilterValue(f.Value) {
		return fmt.Errorf("gotype: comparison filter %q requires a scalar value, got %T (use In for set membership)", f.Attr, f.Value)
	}
	return nil
}

func (f *ComparisonFilter) compile(c *compileCtx) ([]ast.Pattern, error) {
	if f.Negated {
		return compileNot(c, &ComparisonFilter{Attr: f.Attr, Op: f.Op, Value: f.Value})
	}
	return attrConstraints(c, f.Attr, [2]string{f.Op, FormatValue(f.Value)}), nil
}

func isScalarFilterValue(value any) bool {
	if value == nil {
		return true
	}

	v := reflect.ValueOf(value)
	for v.Kind() == reflect.Pointer {
		if v.IsNil() {
			return true
		}
		v = v.Elem()
		value = v.Interface()
	}

	if _, ok := value.(time.Time); ok {
		return true
	}

	switch v.Kind() {
	case reflect.Bool, reflect.String,
		reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
		reflect.Float32, reflect.Float64:
		return true
	default:
		return false
	}
}

// --- Convenience constructors ---

// Eq creates an equality filter: attribute == value.
func Eq(attr string, value any) Filter {
	return &ComparisonFilter{Attr: attr, Op: "==", Value: value}
}

// Neq creates a not-equal filter: attribute != value.
func Neq(attr string, value any) Filter {
	return &ComparisonFilter{Attr: attr, Op: "!=", Value: value}
}

// Gt creates a greater-than filter: attribute > value.
func Gt(attr string, value any) Filter {
	return &ComparisonFilter{Attr: attr, Op: ">", Value: value}
}

// Gte creates a greater-or-equal filter: attribute >= value.
func Gte(attr string, value any) Filter {
	return &ComparisonFilter{Attr: attr, Op: ">=", Value: value}
}

// Lt creates a less-than filter: attribute < value.
func Lt(attr string, value any) Filter {
	return &ComparisonFilter{Attr: attr, Op: "<", Value: value}
}

// Lte creates a less-or-equal filter: attribute <= value.
func Lte(attr string, value any) Filter {
	return &ComparisonFilter{Attr: attr, Op: "<=", Value: value}
}

// --- String filters ---

// StringFilter applies string operations (contains, like) on an attribute.
type StringFilter struct {
	Attr    string
	Op      string // "contains" or "like"
	Pattern string
	Negated bool
}

// Validate reports construction errors: an invalid attribute name or an
// unsupported string operator.
func (f *StringFilter) Validate() error {
	if err := validateAttrName(f.Attr); err != nil {
		return err
	}
	switch f.Op {
	case "contains", "like":
		return nil
	}
	return fmt.Errorf("gotype: invalid string filter operator %q (want \"contains\" or \"like\")", f.Op)
}

func (f *StringFilter) compile(c *compileCtx) ([]ast.Pattern, error) {
	if f.Negated {
		return compileNot(c, &StringFilter{Attr: f.Attr, Op: f.Op, Pattern: f.Pattern})
	}
	return attrConstraints(c, f.Attr, [2]string{f.Op, FormatValue(f.Pattern)}), nil
}

// Contains creates a string contains filter. The pattern is a literal
// substring, not a regex.
func Contains(attr string, pattern string) Filter {
	return &StringFilter{Attr: attr, Op: "contains", Pattern: pattern}
}

// Like creates a string like filter (TypeQL regex matching). The pattern is
// a raw regular expression; regex metacharacters are NOT escaped. Use
// Startswith for a literal prefix match.
func Like(attr string, pattern string) Filter {
	return &StringFilter{Attr: attr, Op: "like", Pattern: pattern}
}

// --- Set membership filters ---

// InFilter checks whether an attribute value is in a set of values.
type InFilter struct {
	Attr    string
	Values  []any
	Negated bool
}

// Validate reports construction errors: an invalid attribute name or a
// non-scalar member value.
func (f *InFilter) Validate() error {
	if err := validateAttrName(f.Attr); err != nil {
		return err
	}
	for _, val := range f.Values {
		if !isScalarFilterValue(val) {
			return fmt.Errorf("gotype: in filter %q requires scalar values, got %T", f.Attr, val)
		}
	}
	return nil
}

func (f *InFilter) compile(c *compileCtx) ([]ast.Pattern, error) {
	if f.Negated {
		// NOT IN the empty set always holds; otherwise negate IN in a child scope.
		if len(f.Values) == 0 {
			return nil, nil
		}
		return compileNot(c, &InFilter{Attr: f.Attr, Values: f.Values})
	}
	if len(f.Values) == 0 {
		// IN the empty set never holds.
		return []ast.Pattern{matchNothing(c.owner)}, nil
	}
	v, ps := c.attr(f.Attr)
	alternatives := make([][]ast.Pattern, 0, len(f.Values))
	for _, val := range f.Values {
		alternatives = append(alternatives, []ast.Pattern{constraint(v, "==", FormatValue(val))})
	}
	return append(ps, ast.OrPattern{Alternatives: alternatives}), nil
}

// In creates a filter that checks if an attribute value is in a set.
func In(attr string, values []any) Filter {
	return &InFilter{Attr: attr, Values: values}
}

// NotIn creates a filter that checks if an attribute value is NOT in a set.
func NotIn(attr string, values []any) Filter {
	return &InFilter{Attr: attr, Values: values, Negated: true}
}

// --- Range filter ---

// RangeFilter checks whether an attribute value falls between min and max (inclusive).
type RangeFilter struct {
	Attr    string
	Min     any
	Max     any
	Negated bool
}

// Validate reports construction errors: an invalid attribute name or a
// non-scalar bound.
func (f *RangeFilter) Validate() error {
	if err := validateAttrName(f.Attr); err != nil {
		return err
	}
	if !isScalarFilterValue(f.Min) {
		return fmt.Errorf("gotype: range filter %q requires a scalar min, got %T", f.Attr, f.Min)
	}
	if !isScalarFilterValue(f.Max) {
		return fmt.Errorf("gotype: range filter %q requires a scalar max, got %T", f.Attr, f.Max)
	}
	return nil
}

func (f *RangeFilter) compile(c *compileCtx) ([]ast.Pattern, error) {
	if f.Negated {
		return compileNot(c, &RangeFilter{Attr: f.Attr, Min: f.Min, Max: f.Max})
	}
	return attrConstraints(c, f.Attr, [2]string{">=", FormatValue(f.Min)}, [2]string{"<=", FormatValue(f.Max)}), nil
}

// Range creates a filter that checks if an attribute value is between min and max (inclusive).
func Range(attr string, min, max any) Filter {
	return &RangeFilter{Attr: attr, Min: min, Max: max}
}

// --- Regex filter ---

// RegexFilter applies a regex match on a string attribute using TypeQL "like".
type RegexFilter struct {
	Attr    string
	Pattern string
	Negated bool
}

// Validate reports an invalid attribute name.
func (f *RegexFilter) Validate() error {
	return validateAttrName(f.Attr)
}

func (f *RegexFilter) compile(c *compileCtx) ([]ast.Pattern, error) {
	if f.Negated {
		return compileNot(c, &RegexFilter{Attr: f.Attr, Pattern: f.Pattern})
	}
	return attrConstraints(c, f.Attr, [2]string{"like", FormatValue(f.Pattern)}), nil
}

// Regex creates a filter that matches an attribute value against a regex pattern.
// The pattern is a raw regular expression; metacharacters are NOT escaped.
func Regex(attr string, pattern string) Filter {
	return &RegexFilter{Attr: attr, Pattern: pattern}
}

// --- Startswith filter ---

// Startswith creates a filter that checks if a string attribute starts with a
// literal prefix. The prefix is treated as data: regex metacharacters are
// escaped before it is compiled into the underlying TypeQL "like" pattern.
// Use Like or Regex for raw regex matching.
func Startswith(attr string, prefix string) Filter {
	return Like(attr, regexp.QuoteMeta(prefix)+".*")
}

// --- Existence filter ---

// ExistsFilter checks whether an attribute exists (has) or not.
type ExistsFilter struct {
	Attr    string
	Negated bool
}

// Validate reports an invalid attribute name.
func (f *ExistsFilter) Validate() error {
	return validateAttrName(f.Attr)
}

func (f *ExistsFilter) compile(c *compileCtx) ([]ast.Pattern, error) {
	if f.Negated {
		return compileNot(c, &ExistsFilter{Attr: f.Attr})
	}
	// The anonymous $_ keeps existence independent of every other filter on
	// the same attribute.
	return []ast.Pattern{ast.HasPattern{ThingVar: "$" + c.owner, AttrType: f.Attr, AttrVar: "$_"}}, nil
}

// HasAttr creates an attribute existence filter.
func HasAttr(attr string) Filter {
	return &ExistsFilter{Attr: attr}
}

// NotHasAttr creates a negated attribute existence filter.
func NotHasAttr(attr string) Filter {
	return &ExistsFilter{Attr: attr, Negated: true}
}

// --- IID filter ---

// IIDFilter matches by internal ID.
type IIDFilter struct {
	IID string
}

// Validate reports a malformed IID (anything not matching 0x[0-9a-fA-F]+).
func (f *IIDFilter) Validate() error {
	return validateIID(f.IID)
}

func (f *IIDFilter) compile(c *compileCtx) ([]ast.Pattern, error) {
	return []ast.Pattern{ast.IidPattern{Variable: "$" + c.owner, IID: f.IID}}, nil
}

// ByIID creates a filter matching a specific internal ID.
// The IID must match 0x[0-9a-fA-F]+; anything else is rejected when the
// query is executed (see Validate).
func ByIID(iid string) Filter {
	return &IIDFilter{IID: iid}
}

// IIDInFilter matches any of multiple internal IDs using an OR pattern.
type IIDInFilter struct {
	IIDs []string
}

// Validate reports the first malformed IID in the set.
func (f *IIDInFilter) Validate() error {
	for _, iid := range f.IIDs {
		if err := validateIID(iid); err != nil {
			return err
		}
	}
	return nil
}

func (f *IIDInFilter) compile(c *compileCtx) ([]ast.Pattern, error) {
	return []ast.Pattern{iidInPattern(c.owner, f.IIDs)}, nil
}

// iidInPattern matches varName against any of iids (nothing for no IIDs).
func iidInPattern(varName string, iids []string) ast.Pattern {
	switch len(iids) {
	case 0:
		return matchNothing(varName)
	case 1:
		return ast.IidPattern{Variable: "$" + varName, IID: iids[0]}
	}
	alternatives := make([][]ast.Pattern, 0, len(iids))
	for _, iid := range iids {
		alternatives = append(alternatives, []ast.Pattern{ast.IidPattern{Variable: "$" + varName, IID: iid}})
	}
	return ast.OrPattern{Alternatives: alternatives}
}

// iidInText renders iidInPattern as one pattern line ending in ";".
func iidInText(varName string, iids []string) string {
	s, err := patternCompiler.Compile(iidInPattern(varName, iids))
	if err != nil {
		panic(fmt.Sprintf("gotype: iid pattern: %v", err)) // the patterns above always compile
	}
	return s + ";"
}

// IIDIn creates a filter matching any of the specified internal IDs.
// Each IID must match 0x[0-9a-fA-F]+; anything else is rejected when the
// query is executed (see Validate). With no IIDs the filter matches nothing.
func IIDIn(iids ...string) Filter {
	return &IIDInFilter{IIDs: iids}
}

// --- Boolean combinators ---

// AndFilter combines multiple filters with AND (conjunction).
type AndFilter struct {
	Filters []Filter
}

// Validate recursively validates all child filters.
func (f *AndFilter) Validate() error {
	return validateFilters(f.Filters...)
}

func (f *AndFilter) compile(c *compileCtx) ([]ast.Pattern, error) {
	return compileFilters(c, f.Filters)
}

// And combines filters with logical AND.
func And(filters ...Filter) Filter {
	// Flatten nested ANDs
	var flat []Filter
	for _, f := range filters {
		if a, ok := f.(*AndFilter); ok {
			flat = append(flat, a.Filters...)
		} else {
			flat = append(flat, f)
		}
	}
	return &AndFilter{Filters: flat}
}

// OrFilter combines alternatives with OR (disjunction).
type OrFilter struct {
	Filters []Filter
}

// Validate recursively validates all child filters.
func (f *OrFilter) Validate() error {
	return validateFilters(f.Filters...)
}

// compile gives each branch its own child scope (R2): sibling branches never
// share a variable, and only the owner crosses into a branch.
func (f *OrFilter) compile(c *compileCtx) ([]ast.Pattern, error) {
	if len(f.Filters) == 0 {
		// An empty disjunction never holds.
		return []ast.Pattern{matchNothing(c.owner)}, nil
	}
	alternatives := make([][]ast.Pattern, 0, len(f.Filters))
	for _, child := range f.Filters {
		if child == nil {
			return nil, fmt.Errorf("gotype: filter must not be nil")
		}
		ps, err := child.compile(c.child())
		if err != nil {
			return nil, err
		}
		if len(ps) == 0 {
			// A branch without constraints always holds, so the disjunction does.
			return nil, nil
		}
		alternatives = append(alternatives, ps)
	}
	return []ast.Pattern{ast.OrPattern{Alternatives: alternatives}}, nil
}

// Or combines filters with logical OR.
func Or(filters ...Filter) Filter {
	return &OrFilter{Filters: filters}
}

// NotFilter negates a filter expression.
type NotFilter struct {
	Inner Filter
}

// Validate recursively validates the inner filter.
func (f *NotFilter) Validate() error {
	return validateFilters(f.Inner)
}

func (f *NotFilter) compile(c *compileCtx) ([]ast.Pattern, error) {
	return compileNot(c, f.Inner)
}

// compileNot compiles inner in a child scope (R2) and negates it.
func compileNot(c *compileCtx, inner Filter) ([]ast.Pattern, error) {
	if inner == nil {
		return nil, fmt.Errorf("gotype: filter must not be nil")
	}
	ps, err := inner.compile(c.child())
	if err != nil {
		return nil, err
	}
	if len(ps) == 0 {
		// The negation of a filter that always holds never holds.
		return []ast.Pattern{matchNothing(c.owner)}, nil
	}
	return []ast.Pattern{ast.NotPattern{Patterns: ps}}, nil
}

// Not negates a filter.
func Not(filter Filter) Filter {
	return &NotFilter{Inner: filter}
}

// --- Role player filter ---

// RolePlayerFilter matches relations where a given role player satisfies the inner filter.
type RolePlayerFilter struct {
	RoleName string
	Inner    Filter
}

// Validate reports an invalid role name and recursively validates the inner filter.
func (f *RolePlayerFilter) Validate() error {
	if !identifierPattern.MatchString(f.RoleName) {
		return fmt.Errorf("gotype: invalid role name %q: must match [a-zA-Z][a-zA-Z0-9_-]*", f.RoleName)
	}
	return validateFilters(f.Inner)
}

// compile links the player of the role (one player per role, owner, and
// scope, R4) and compiles the inner filter with the player as owner.
func (f *RolePlayerFilter) compile(c *compileCtx) ([]ast.Pattern, error) {
	p, ps := c.player(f.RoleName)
	if f.Inner == nil {
		return nil, fmt.Errorf("gotype: filter must not be nil")
	}
	inner, err := f.Inner.compile(&compileCtx{alloc: c.alloc, owner: p, ownerFam: famPlayer, scope: c.scope})
	if err != nil {
		return nil, err
	}
	return append(ps, inner...), nil
}

// RolePlayer creates a filter that matches relations where the given role player
// satisfies the inner filter.
func RolePlayer(roleName string, inner Filter) Filter {
	return &RolePlayerFilter{RoleName: roleName, Inner: inner}
}

// --- Computed expression filters ---

// Expr is a typed expression for Computed filters. Build it with Attr,
// Literal, the arithmetic operators, and the built-in functions. The
// interface is sealed: expressions name attributes, not variables, and the
// compiler allocates every variable.
type Expr interface {
	compileExpr(c *compileCtx) (any, []ast.Pattern, error)
}

type attrExpr struct{ label string }

type literalExpr struct{ value any }

type binaryExpr struct {
	op   string
	a, b Expr
}

type funcExpr struct {
	fn   string
	args []Expr
}

// Attr is the value of attribute label of the current owner: the queried
// instance, or the role player inside a RolePlayer filter. The compiler binds
// the attribute if no filter in the same scope binds it.
func Attr(label string) Expr { return attrExpr{label: label} }

// Literal is a scalar value (string, bool, number, or time.Time).
func Literal(v any) Expr { return literalExpr{value: v} }

// Add is a + b.
func Add(a, b Expr) Expr { return binaryExpr{op: "+", a: a, b: b} }

// Sub is a - b.
func Sub(a, b Expr) Expr { return binaryExpr{op: "-", a: a, b: b} }

// Mul is a * b.
func Mul(a, b Expr) Expr { return binaryExpr{op: "*", a: a, b: b} }

// Div is a / b.
func Div(a, b Expr) Expr { return binaryExpr{op: "/", a: a, b: b} }

// Mod is a % b.
func Mod(a, b Expr) Expr { return binaryExpr{op: "%", a: a, b: b} }

// Pow is a ^ b.
func Pow(a, b Expr) Expr { return binaryExpr{op: "^", a: a, b: b} }

// Abs is the TypeQL built-in abs(a).
func Abs(a Expr) Expr { return funcExpr{fn: "abs", args: []Expr{a}} }

// Ceil is the TypeQL built-in ceil(a).
func Ceil(a Expr) Expr { return funcExpr{fn: "ceil", args: []Expr{a}} }

// Floor is the TypeQL built-in floor(a).
func Floor(a Expr) Expr { return funcExpr{fn: "floor", args: []Expr{a}} }

// Round is the TypeQL built-in round(a).
func Round(a Expr) Expr { return funcExpr{fn: "round", args: []Expr{a}} }

// Length is the length of a string: the TypeQL built-in len(a).
func Length(a Expr) Expr { return funcExpr{fn: "len", args: []Expr{a}} }

// Max is the TypeQL built-in max(a, b).
func Max(a, b Expr) Expr { return funcExpr{fn: "max", args: []Expr{a, b}} }

// Min is the TypeQL built-in min(a, b).
func Min(a, b Expr) Expr { return funcExpr{fn: "min", args: []Expr{a, b}} }

func (e attrExpr) compileExpr(c *compileCtx) (any, []ast.Pattern, error) {
	if err := validateAttrName(e.label); err != nil {
		return nil, nil, err
	}
	v, ps := c.attr(e.label)
	return "$" + v, ps, nil
}

func (e literalExpr) compileExpr(*compileCtx) (any, []ast.Pattern, error) {
	if e.value == nil || !isScalarFilterValue(e.value) {
		return nil, nil, fmt.Errorf("gotype: Literal requires a non-nil scalar value, got %T", e.value)
	}
	return ast.ValueFromGo(e.value), nil, nil
}

func (e binaryExpr) compileExpr(c *compileCtx) (any, []ast.Pattern, error) {
	if e.a == nil || e.b == nil {
		return nil, nil, fmt.Errorf("gotype: operator %s requires two expressions", e.op)
	}
	a, pa, err := e.a.compileExpr(c)
	if err != nil {
		return nil, nil, err
	}
	b, pb, err := e.b.compileExpr(c)
	if err != nil {
		return nil, nil, err
	}
	return ast.ArithmeticValue{Left: a, Operator: e.op, Right: b}, append(pa, pb...), nil
}

func (e funcExpr) compileExpr(c *compileCtx) (any, []ast.Pattern, error) {
	args := make([]any, 0, len(e.args))
	var ps []ast.Pattern
	for _, arg := range e.args {
		if arg == nil {
			return nil, nil, fmt.Errorf("gotype: %s requires an expression argument", e.fn)
		}
		v, p, err := arg.compileExpr(c)
		if err != nil {
			return nil, nil, err
		}
		args = append(args, v)
		ps = append(ps, p...)
	}
	return ast.FunctionCallValue{Function: e.fn, Args: args}, ps, nil
}

// ComputedFilter computes an expression into its own variable and compares
// it: let $<result> = <expr>; $<result> <op> <value>. The compiler allocates
// the result variable (R6).
type ComputedFilter struct {
	// Expr is the expression to compute.
	Expr Expr
	// Op is the comparison operator (==, !=, >, <, >=, <=).
	Op string
	// Value is the comparison target.
	Value any
}

// Validate reports construction errors: a nil expression, an unsupported
// operator, or a non-scalar comparison value. Errors inside the expression
// are reported when the query is compiled.
func (f *ComputedFilter) Validate() error {
	if f.Expr == nil {
		return fmt.Errorf("gotype: computed filter requires an expression")
	}
	if err := validateComparisonOp(f.Op); err != nil {
		return err
	}
	if !isScalarFilterValue(f.Value) {
		return fmt.Errorf("gotype: computed filter requires a scalar value, got %T", f.Value)
	}
	return nil
}

func (f *ComputedFilter) compile(c *compileCtx) ([]ast.Pattern, error) {
	val, ps, err := f.Expr.compileExpr(c)
	if err != nil {
		return nil, err
	}
	var expr string
	switch v := val.(type) {
	case string:
		expr = v
	case ast.Value:
		if expr, err = patternCompiler.Compile(v); err != nil {
			return nil, err
		}
	}
	r, _ := c.alloc.name(varKey{family: famComputed, label: strconv.Itoa(c.alloc.fresh()), scope: c.scope})
	c.alloc.record(r, famComputed, c.scope, "bind")
	c.alloc.record(r, famComputed, c.scope, "use")
	return append(ps,
		ast.RawPattern{Content: "let $" + r + " = " + expr},
		constraint(r, f.Op, FormatValue(f.Value)),
	), nil
}

// Computed creates a filter that computes expr and compares the result with
// value using op:
//
//	gotype.Computed(gotype.Mul(gotype.Attr("price"), gotype.Attr("quantity")), ">", 100)
func Computed(expr Expr, op string, value any) Filter {
	return &ComputedFilter{Expr: expr, Op: op, Value: value}
}

// --- Helpers ---

// attrConstraints binds attribute attr of the owner (once per scope, R5) and
// constrains its variable with each (operator, literal) pair.
func attrConstraints(c *compileCtx, attr string, constraints ...[2]string) []ast.Pattern {
	v, ps := c.attr(attr)
	for _, oc := range constraints {
		ps = append(ps, constraint(v, oc[0], oc[1]))
	}
	return ps
}

// constraint is the pattern "$v <op> <literal>".
func constraint(v, op, literal string) ast.Pattern {
	return ast.RawPattern{Content: "$" + v + " " + op + " " + literal}
}

// matchNothing is a self-contradiction that matches no instance ($x is always
// $x, so the negation never holds). It introduces no variables (issue #85).
func matchNothing(varName string) ast.Pattern {
	return ast.RawPattern{Content: fmt.Sprintf("not { $%s is $%s; }", varName, varName)}
}
