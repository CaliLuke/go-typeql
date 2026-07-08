package gotype

import (
	"fmt"
	"reflect"
	"regexp"
	"strings"
	"time"
)

// Filter represents a query filter expression that generates TypeQL patterns.
// Filters compose via And, Or, and Not to build complex match clauses.
//
// All filter types in this package also implement Validate() error, which
// reports construction problems (invalid attribute names, malformed IIDs,
// non-scalar comparison values). Query execution validates filters before
// building query text, so misuse surfaces as an error from Execute/Count/...
// instead of injected or malformed TypeQL reaching the server.
type Filter interface {
	// ToPatterns generates TypeQL pattern strings for this filter.
	// varName is the entity/relation variable name (e.g., "e").
	ToPatterns(varName string) []string
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

// ToPatterns generates TypeQL patterns for a comparison filter.
// It panics on a non-scalar value when called directly; query execution
// paths validate first (see Validate) and return an error instead.
func (f *ComparisonFilter) ToPatterns(varName string) []string {
	if !isScalarFilterValue(f.Value) {
		panic(fmt.Sprintf("gotype: comparison filter %q requires a scalar value, got %T", f.Attr, f.Value))
	}
	attrVar := sanitizeVar(varName + "__" + f.Attr)
	hasPattern := fmt.Sprintf("$%s has %s $%s", varName, f.Attr, attrVar)

	if f.Op == "==" {
		constraint := fmt.Sprintf("$%s == %s", attrVar, FormatValue(f.Value))
		patterns := []string{hasPattern + ";", constraint + ";"}
		if f.Negated {
			return wrapNot(patterns)
		}
		return patterns
	}

	constraint := fmt.Sprintf("$%s %s %s", attrVar, f.Op, FormatValue(f.Value))
	patterns := []string{hasPattern + ";", constraint + ";"}
	if f.Negated {
		return wrapNot(patterns)
	}
	return patterns
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

// ToPatterns generates TypeQL patterns for a string filter.
func (f *StringFilter) ToPatterns(varName string) []string {
	attrVar := sanitizeVar(varName + "__" + f.Attr)
	hasPattern := fmt.Sprintf("$%s has %s $%s;", varName, f.Attr, attrVar)
	constraint := fmt.Sprintf("$%s %s %s;", attrVar, f.Op, FormatValue(f.Pattern))

	patterns := []string{hasPattern, constraint}
	if f.Negated {
		return wrapNot(patterns)
	}
	return patterns
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

// ToPatterns generates TypeQL patterns for a set membership filter.
func (f *InFilter) ToPatterns(varName string) []string {
	if len(f.Values) == 0 {
		// Empty set: nothing matches. Use a contradiction pattern.
		if f.Negated {
			// NOT IN empty set → always true, no extra patterns needed.
			return nil
		}
		// IN empty set → never true.
		return []string{matchNothingPattern(varName)}
	}

	attrVar := sanitizeVar(varName + "__" + f.Attr)
	hasPattern := fmt.Sprintf("$%s has %s $%s;", varName, f.Attr, attrVar)

	var branches []string
	for _, val := range f.Values {
		branches = append(branches, fmt.Sprintf("{ $%s == %s; }", attrVar, FormatValue(val)))
	}
	orPattern := strings.Join(branches, " or ") + ";"
	patterns := []string{hasPattern, orPattern}

	if f.Negated {
		return wrapNot(patterns)
	}
	return patterns
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

// ToPatterns generates TypeQL patterns for a range filter.
func (f *RangeFilter) ToPatterns(varName string) []string {
	attrVar := sanitizeVar(varName + "__" + f.Attr)
	hasPattern := fmt.Sprintf("$%s has %s $%s;", varName, f.Attr, attrVar)
	minConstraint := fmt.Sprintf("$%s >= %s;", attrVar, FormatValue(f.Min))
	maxConstraint := fmt.Sprintf("$%s <= %s;", attrVar, FormatValue(f.Max))

	patterns := []string{hasPattern, minConstraint, maxConstraint}
	if f.Negated {
		return wrapNot(patterns)
	}
	return patterns
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

// ToPatterns generates TypeQL patterns for a regex filter.
func (f *RegexFilter) ToPatterns(varName string) []string {
	attrVar := sanitizeVar(varName + "__" + f.Attr)
	hasPattern := fmt.Sprintf("$%s has %s $%s;", varName, f.Attr, attrVar)
	constraint := fmt.Sprintf("$%s like %s;", attrVar, FormatValue(f.Pattern))

	patterns := []string{hasPattern, constraint}
	if f.Negated {
		return wrapNot(patterns)
	}
	return patterns
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

// ToPatterns generates TypeQL patterns for an existence filter.
func (f *ExistsFilter) ToPatterns(varName string) []string {
	pattern := fmt.Sprintf("$%s has %s $%s__;", varName, f.Attr, sanitizeVar(varName+"__"+f.Attr))
	if f.Negated {
		return wrapNot([]string{pattern})
	}
	return []string{pattern}
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

// ToPatterns generates TypeQL patterns for an IID filter.
func (f *IIDFilter) ToPatterns(varName string) []string {
	return []string{fmt.Sprintf("$%s iid %s;", varName, f.IID)}
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

// ToPatterns generates TypeQL patterns for matching multiple IIDs.
func (f *IIDInFilter) ToPatterns(varName string) []string {
	if len(f.IIDs) == 0 {
		// Empty set: nothing matches.
		return []string{matchNothingPattern(varName)}
	}
	if len(f.IIDs) == 1 {
		return []string{fmt.Sprintf("$%s iid %s;", varName, f.IIDs[0])}
	}
	var branches []string
	for _, iid := range f.IIDs {
		branches = append(branches, fmt.Sprintf("{ $%s iid %s; }", varName, iid))
	}
	return []string{strings.Join(branches, " or ") + ";"}
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

// ToPatterns generates TypeQL patterns by concatenating all child filter patterns.
func (f *AndFilter) ToPatterns(varName string) []string {
	return f.toPatternsScoped(varName, &varScope{})
}

func (f *AndFilter) toPatternsScoped(varName string, scope *varScope) []string {
	var patterns []string
	for _, child := range f.Filters {
		patterns = append(patterns, filterPatterns(child, varName, scope)...)
	}
	return patterns
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

// ToPatterns generates TypeQL or-branch patterns with scoped variables.
// Branch suffixes (_o1, _o2, ...) are allocated from a fresh per-call scope,
// so the output is deterministic. Query builders instead thread one shared
// scope through all filters of a query via filterPatterns, so sibling or/not
// blocks never reuse a suffix within the same query.
func (f *OrFilter) ToPatterns(varName string) []string {
	return f.toPatternsScoped(varName, &varScope{})
}

func (f *OrFilter) toPatternsScoped(varName string, scope *varScope) []string {
	var alternatives []string
	for _, child := range f.Filters {
		// Each Or branch gets a unique scope to avoid locally-scoped
		// variable collisions (TypeDB 3.x constraint).
		scopedVarName := fmt.Sprintf("%s_o%d", varName, scope.next())
		patterns := filterPatterns(child, varName, scope)
		var scoped []string
		for _, p := range patterns {
			scoped = append(scoped, scopeLocalVars(p, varName, scopedVarName))
		}
		alternatives = append(alternatives, "{ "+strings.Join(scoped, " ")+" }")
	}
	return []string{strings.Join(alternatives, " or ") + ";"}
}

// Or combines filters with logical OR.
func Or(filters ...Filter) Filter {
	return &OrFilter{Filters: filters}
}

// varScope allocates unique suffixes for locally-scoped variables in or {}
// and not {} blocks. TypeDB 3.x scopes variables locally to or/not branches,
// but a name reused across two different blocks in the same conjunction
// becomes a shared variable of the enclosing scope — so suffixes must be
// unique across ALL or/not blocks of one query. Query builders create one
// varScope per built query and thread it through filterPatterns; suffix
// numbering therefore restarts at 1 for every query, making the generated
// text deterministic.
type varScope struct {
	n int
}

// next returns the next unique suffix number within this scope.
func (s *varScope) next() int {
	s.n++
	return s.n
}

// scopedPatternFilter is implemented by this package's composite filters so
// that a single varScope can be threaded through an entire filter tree.
//
// Known limitation: a user-defined composite Filter that internally wraps an
// OrFilter breaks the scope chain — filterPatterns falls back to its public
// ToPatterns, so the inner Or numbers its branches from a fresh scope and
// could collide with a sibling or/not block of the same query when both bind
// the same attribute or variable name.
type scopedPatternFilter interface {
	toPatternsScoped(varName string, scope *varScope) []string
}

// filterPatterns generates the TypeQL patterns for f, threading scope through
// filters that support per-query scoping (see scopedPatternFilter). External
// Filter implementations fall back to f.ToPatterns(varName).
func filterPatterns(f Filter, varName string, scope *varScope) []string {
	if sf, ok := f.(scopedPatternFilter); ok {
		return sf.toPatternsScoped(varName, scope)
	}
	return f.ToPatterns(varName)
}

// NotFilter negates a filter expression.
type NotFilter struct {
	Inner Filter
}

// Validate recursively validates the inner filter.
func (f *NotFilter) Validate() error {
	return validateFilters(f.Inner)
}

// ToPatterns generates TypeQL patterns wrapped in a not {} block.
// The scope suffix (_n1, _n2, ...) is allocated from a fresh per-call scope,
// so the output is deterministic; query builders thread one shared scope
// through all filters of a query via filterPatterns (see varScope).
func (f *NotFilter) ToPatterns(varName string) []string {
	return f.toPatternsScoped(varName, &varScope{})
}

func (f *NotFilter) toPatternsScoped(varName string, scope *varScope) []string {
	// Generate patterns with a scoped variable name to avoid collisions
	// with locally-scoped variables in sibling or {} branches.
	scopedVarName := fmt.Sprintf("%s_n%d", varName, scope.next())
	inner := filterPatterns(f.Inner, varName, scope)
	// Rename locally-introduced variables (e.g., $e__name → $e_n1__name)
	// while keeping the entity variable ($e) unchanged.
	var scoped []string
	for _, p := range inner {
		scoped = append(scoped, scopeLocalVars(p, varName, scopedVarName))
	}
	return wrapNot(scoped)
}

// scopeLocalVars renames every variable a child pattern introduces so that
// sibling or {} / not {} branches never share locally-scoped variables
// (TypeDB 3.x constraint). The entity variable ($varName) is kept unchanged;
// attribute variables keep their suffix ($varName__X → $scopedName__X); any
// other variable — role players from RolePlayer, computed variables from
// Computed, nested scopes — is prefixed ($author → $scopedName__author).
// Variables inside quoted string literals are left untouched.
func scopeLocalVars(pattern, varName, scopedName string) string {
	var b strings.Builder
	b.Grow(len(pattern) + 16)
	inString := false
	for i := 0; i < len(pattern); i++ {
		c := pattern[i]
		if inString {
			b.WriteByte(c)
			if c == '\\' && i+1 < len(pattern) {
				i++
				b.WriteByte(pattern[i])
			} else if c == '"' {
				inString = false
			}
			continue
		}
		switch c {
		case '"':
			inString = true
			b.WriteByte(c)
		case '$':
			j := i + 1
			for j < len(pattern) && isVarNameChar(pattern[j]) {
				j++
			}
			b.WriteString(scopedVarToken(pattern[i+1:j], varName, scopedName))
			i = j - 1
		default:
			b.WriteByte(c)
		}
	}
	return b.String()
}

// isVarNameChar reports whether c can appear in a generated TypeQL variable
// name (generated variables are sanitized to letters, digits, underscores).
func isVarNameChar(c byte) bool {
	return c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')
}

// scopedVarToken renames a single variable name per the scopeLocalVars rules.
func scopedVarToken(name, varName, scopedName string) string {
	if name == "" || name == varName {
		return "$" + name
	}
	if rest, ok := strings.CutPrefix(name, varName+"__"); ok {
		return "$" + scopedName + "__" + rest
	}
	return "$" + scopedName + "__" + name
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

// ToPatterns generates TypeQL patterns linking a role player and applying inner filters.
func (f *RolePlayerFilter) ToPatterns(varName string) []string {
	return f.toPatternsScoped(varName, &varScope{})
}

func (f *RolePlayerFilter) toPatternsScoped(varName string, scope *varScope) []string {
	roleVar := sanitizeVar(f.RoleName)
	// Link the role player variable to the relation
	linkPattern := fmt.Sprintf("$%s links (%s: $%s);", varName, f.RoleName, roleVar)

	// Generate inner filter patterns using the role player variable
	innerPatterns := filterPatterns(f.Inner, roleVar, scope)

	patterns := []string{linkPattern}
	patterns = append(patterns, innerPatterns...)
	return patterns
}

// RolePlayer creates a filter that matches relations where the given role player
// satisfies the inner filter.
func RolePlayer(roleName string, inner Filter) Filter {
	return &RolePlayerFilter{RoleName: roleName, Inner: inner}
}

// --- Computed expression filters ---

// ComputedFilter uses a let-assignment to compute a value and compare it.
// Generates: let $computed = <expr>; $computed <op> <value>;
type ComputedFilter struct {
	// VarName is the name for the computed variable (without $).
	VarName string
	// Expr is the TypeQL expression to compute (e.g., "$e__price * $e__quantity").
	Expr string
	// Op is the comparison operator (==, !=, >, <, >=, <=).
	Op string
	// Value is the comparison target.
	Value any
}

// Validate reports construction errors: an invalid computed variable name,
// an unsupported operator, or a non-scalar comparison value. Expr is a raw
// TypeQL expression and is intentionally not validated.
func (f *ComputedFilter) Validate() error {
	if !identifierPattern.MatchString(f.VarName) {
		return fmt.Errorf("gotype: invalid computed variable name %q: must match [a-zA-Z][a-zA-Z0-9_-]*", f.VarName)
	}
	if err := validateComparisonOp(f.Op); err != nil {
		return err
	}
	if !isScalarFilterValue(f.Value) {
		return fmt.Errorf("gotype: computed filter %q requires a scalar value, got %T", f.VarName, f.Value)
	}
	return nil
}

// ToPatterns generates TypeQL let-assignment and comparison patterns.
func (f *ComputedFilter) ToPatterns(varName string) []string {
	computedVar := sanitizeVar(f.VarName)
	return []string{
		fmt.Sprintf("let $%s = %s;", computedVar, f.Expr),
		fmt.Sprintf("$%s %s %s;", computedVar, f.Op, FormatValue(f.Value)),
	}
}

// Computed creates a filter that assigns a computed expression to a variable
// and compares it using the given operator.
func Computed(varName, expr, op string, value any) Filter {
	return &ComputedFilter{VarName: varName, Expr: expr, Op: op, Value: value}
}

// ArithmeticExpr builds a TypeQL arithmetic expression string from two attribute
// references and an operator. Useful with Computed filter.
func ArithmeticExpr(varName, leftAttr, op, rightAttr string) string {
	left := sanitizeVar(varName + "__" + leftAttr)
	right := sanitizeVar(varName + "__" + rightAttr)
	return fmt.Sprintf("$%s %s $%s", left, op, right)
}

// BuiltinFuncExpr builds a TypeQL function call expression string.
// Useful with Computed filter.
func BuiltinFuncExpr(funcName string, args ...string) string {
	return fmt.Sprintf("%s(%s)", funcName, strings.Join(args, ", "))
}

// --- Helpers ---

// sanitizeVar replaces hyphens with underscores for TypeQL variable names.
func sanitizeVar(name string) string {
	return strings.ReplaceAll(name, "-", "_")
}

// wrapNot wraps patterns in a TypeQL not {} block.
func wrapNot(patterns []string) []string {
	return []string{"not { " + strings.Join(patterns, " ") + " };"}
}

// matchNothingPattern returns a self-contradiction that matches no instance
// ($x is always $x, so the negation never holds). Unlike a fabricated IID
// literal, it is structurally valid TypeQL on every server version and
// introduces no new variables (issue #85).
func matchNothingPattern(varName string) string {
	return fmt.Sprintf("not { $%s is $%s; };", varName, varName)
}
