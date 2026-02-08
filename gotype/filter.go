package gotype

import (
	"fmt"
	"strings"
	"sync/atomic"
)

// Filter represents a query filter expression that generates TypeQL patterns.
// Filters compose via And, Or, and Not to build complex match clauses.
type Filter interface {
	// ToPatterns generates TypeQL pattern strings for this filter.
	// varName is the entity/relation variable name (e.g., "e").
	ToPatterns(varName string) []string
}

// --- Comparison filters ---

// ComparisonFilter compares an attribute to a value using a TypeQL operator.
type ComparisonFilter struct {
	Attr     string
	Op       string
	Value    any
	Negated  bool
}

// ToPatterns generates TypeQL patterns for a comparison filter.
func (f *ComparisonFilter) ToPatterns(varName string) []string {
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

// Contains creates a string contains filter.
func Contains(attr string, pattern string) Filter {
	return &StringFilter{Attr: attr, Op: "contains", Pattern: pattern}
}

// Like creates a string like filter (TypeQL pattern matching).
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

// ToPatterns generates TypeQL patterns for a set membership filter.
func (f *InFilter) ToPatterns(varName string) []string {
	if len(f.Values) == 0 {
		// Empty set: nothing matches. Use a contradiction pattern.
		if f.Negated {
			// NOT IN empty set → always true, no extra patterns needed.
			return nil
		}
		// IN empty set → never true. Match impossible IID.
		return []string{fmt.Sprintf("$%s iid 0xFFFFFFFFFFFFFFFF;", varName)}
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
func Regex(attr string, pattern string) Filter {
	return &RegexFilter{Attr: attr, Pattern: pattern}
}

// --- Startswith filter ---

// Startswith creates a filter that checks if a string attribute starts with a prefix.
// This is sugar over Like with a prefix pattern.
func Startswith(attr string, prefix string) Filter {
	return Like(attr, prefix+".*")
}

// --- Existence filter ---

// ExistsFilter checks whether an attribute exists (has) or not.
type ExistsFilter struct {
	Attr    string
	Negated bool
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

// ToPatterns generates TypeQL patterns for an IID filter.
func (f *IIDFilter) ToPatterns(varName string) []string {
	return []string{fmt.Sprintf("$%s iid %s;", varName, f.IID)}
}

// ByIID creates a filter matching a specific internal ID.
func ByIID(iid string) Filter {
	return &IIDFilter{IID: iid}
}

// --- Boolean combinators ---

// AndFilter combines multiple filters with AND (conjunction).
type AndFilter struct {
	Filters []Filter
}

// ToPatterns generates TypeQL patterns by concatenating all child filter patterns.
func (f *AndFilter) ToPatterns(varName string) []string {
	var patterns []string
	for _, child := range f.Filters {
		patterns = append(patterns, child.ToPatterns(varName)...)
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

// ToPatterns generates TypeQL or-branch patterns with scoped variables.
func (f *OrFilter) ToPatterns(varName string) []string {
	var alternatives []string
	for _, child := range f.Filters {
		// Each Or branch gets a unique scope to avoid locally-scoped
		// variable collisions (TypeDB 3.x constraint).
		n := varScopeCounter.Add(1)
		scopedVarName := fmt.Sprintf("%s_o%d", varName, n)
		patterns := child.ToPatterns(varName)
		var scoped []string
		for _, p := range patterns {
			scoped = append(scoped, renameAttrVars(p, varName, scopedVarName))
		}
		alternatives = append(alternatives, "{ "+strings.Join(scoped, " ")+" }")
	}
	return []string{strings.Join(alternatives, " or ") + ";"}
}

// Or combines filters with logical OR.
func Or(filters ...Filter) Filter {
	return &OrFilter{Filters: filters}
}

// varScopeCounter generates unique suffixes for locally-scoped variables
// to avoid collisions between or {} and not {} blocks (TypeDB 3.x constraint).
var varScopeCounter atomic.Int64

// NotFilter negates a filter expression.
type NotFilter struct {
	Inner Filter
}

// ToPatterns generates TypeQL patterns wrapped in a not {} block.
func (f *NotFilter) ToPatterns(varName string) []string {
	// Generate patterns with a scoped variable name to avoid collisions
	// with locally-scoped variables in sibling or {} branches.
	n := varScopeCounter.Add(1)
	scopedVarName := fmt.Sprintf("%s_n%d", varName, n)
	inner := f.Inner.ToPatterns(varName)
	// Rename attribute variables (e.g., $e__name → $e_n1__name) while
	// keeping entity variable ($e) unchanged.
	var scoped []string
	for _, p := range inner {
		scoped = append(scoped, renameAttrVars(p, varName, scopedVarName))
	}
	return wrapNot(scoped)
}

// renameAttrVars replaces attribute variable references ($varName__X) with
// scoped versions ($scopedName__X) without changing the entity variable ($varName).
func renameAttrVars(pattern, varName, scopedName string) string {
	// Replace $varName__ with $scopedName__ (attribute variables use double underscore)
	return strings.ReplaceAll(pattern, "$"+varName+"__", "$"+scopedName+"__")
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

// ToPatterns generates TypeQL patterns linking a role player and applying inner filters.
func (f *RolePlayerFilter) ToPatterns(varName string) []string {
	roleVar := sanitizeVar(f.RoleName)
	// Link the role player variable to the relation
	linkPattern := fmt.Sprintf("$%s links (%s: $%s);", varName, f.RoleName, roleVar)

	// Generate inner filter patterns using the role player variable
	innerPatterns := f.Inner.ToPatterns(roleVar)

	patterns := []string{linkPattern}
	patterns = append(patterns, innerPatterns...)
	return patterns
}

// RolePlayer creates a filter that matches relations where the given role player
// satisfies the inner filter.
func RolePlayer(roleName string, inner Filter) Filter {
	return &RolePlayerFilter{RoleName: roleName, Inner: inner}
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
