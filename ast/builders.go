// Package ast provides builder helpers for ergonomic AST construction.
package ast

import (
	"strings"
	"time"
)

// Match creates a MatchClause with the given patterns.
func Match(patterns ...Pattern) MatchClause {
	return MatchClause{Patterns: patterns}
}

// Insert creates an InsertClause with the given statements.
func Insert(statements ...Statement) InsertClause {
	return InsertClause{Statements: statements}
}

// Put creates a PutClause with the given statements.
func Put(statements ...Statement) PutClause {
	return PutClause{Statements: statements}
}

// Delete creates a DeleteClause with the given statements.
func Delete(statements ...Statement) DeleteClause {
	return DeleteClause{Statements: statements}
}

// Update creates an UpdateClause with the given statements.
func Update(statements ...Statement) UpdateClause {
	return UpdateClause{Statements: statements}
}

// Fetch creates a FetchClause with the given items.
func Fetch(items ...FetchItem) FetchClause {
	anyItems := make([]any, len(items))
	for i, item := range items {
		anyItems[i] = item
	}
	return FetchClause{Items: anyItems}
}

// Select creates a SelectClause for variable projection.
func Select(variables ...string) SelectClause {
	return SelectClause{Variables: variables}
}

// Sort creates a SortClause for the given variable and direction.
func Sort(variable, direction string) SortClause {
	return SortClause{Variable: variable, Direction: direction}
}

// Offset creates an OffsetClause with the given count.
func Offset(count int) OffsetClause {
	return OffsetClause{Count: count}
}

// Limit creates a LimitClause with the given count.
func Limit(count int) LimitClause {
	return LimitClause{Count: count}
}

// Entity creates an EntityPattern with the given variable, type, and constraints.
func Entity(varName, typeName string, constraints ...Constraint) EntityPattern {
	return EntityPattern{
		Variable:    varName,
		TypeName:    typeName,
		Constraints: constraints,
	}
}

// Relation creates a RelationPattern with the given variable, type, role players, and constraints.
func Relation(varName, typeName string, rolePlayers []RolePlayer, constraints ...Constraint) RelationPattern {
	return RelationPattern{
		Variable:    varName,
		TypeName:    typeName,
		RolePlayers: rolePlayers,
		Constraints: constraints,
	}
}

// Role creates a RolePlayer with the given role name and player variable.
func Role(roleName, playerVar string) RolePlayer {
	return RolePlayer{Role: roleName, PlayerVar: playerVar}
}

// Has creates a HasConstraint for the given attribute name and value.
// The value can be any Go value that will be formatted via FormatGoValue.
func Has(attrName string, value any) HasConstraint {
	return HasConstraint{AttrName: attrName, Value: value}
}

// Isa creates an IsaConstraint for the given type name.
func Isa(typeName string) IsaConstraint {
	return IsaConstraint{TypeName: typeName, Strict: false}
}

// IsaExact creates a strict IsaConstraint (isa!) for the given type name.
func IsaExact(typeName string) IsaConstraint {
	return IsaConstraint{TypeName: typeName, Strict: true}
}

// Iid creates an IidConstraint for the given IID value.
func Iid(iid string) IidConstraint {
	return IidConstraint{IID: iid}
}

// Lit creates a LiteralValue with the given value and type.
func Lit(value any, valueType string) LiteralValue {
	return LiteralValue{Val: value, ValueType: valueType}
}

// Str creates a string LiteralValue.
func Str(s string) LiteralValue {
	return LiteralValue{Val: s, ValueType: "string"}
}

// Long creates an integer LiteralValue.
func Long(n int64) LiteralValue {
	return LiteralValue{Val: n, ValueType: "long"}
}

// Double creates a double LiteralValue.
func Double(f float64) LiteralValue {
	return LiteralValue{Val: f, ValueType: "double"}
}

// Bool creates a boolean LiteralValue.
func Bool(b bool) LiteralValue {
	return LiteralValue{Val: b, ValueType: "boolean"}
}

// FuncCall creates a FunctionCallValue with the given function name and arguments.
func FuncCall(funcName string, args ...any) FunctionCallValue {
	return FunctionCallValue{Function: funcName, Args: args}
}

// HasStmt creates a HasStatement for the given subject variable, attribute name, and value.
// The value must be a Value type (use Str(), Long(), etc. to create literal values).
func HasStmt(subjectVar, attrName string, value Value) HasStatement {
	return HasStatement{SubjectVar: subjectVar, AttrName: attrName, Value: value}
}

// IsaStmt creates an IsaStatement for the given variable and type name.
func IsaStmt(variable, typeName string) IsaStatement {
	return IsaStatement{Variable: variable, TypeName: typeName}
}

// RelationStmt creates a RelationStatement with the given relation type and role players.
func RelationStmt(typeName string, rolePlayers ...RolePlayer) RelationStatement {
	return RelationStatement{
		TypeName:    typeName,
		RolePlayers: rolePlayers,
	}
}

// FetchAttr creates a FetchAttribute for fetching an attribute value.
// The attrPath should be like "$var.attrname".
func FetchAttr(key, varName, attrName string) FetchAttribute {
	return FetchAttribute{Key: key, Var: varName, AttrName: attrName}
}

// FetchAttrPath is a convenience for creating FetchAttribute from a dotted path like "$p.name".
func FetchAttrPath(key, attrPath string) FetchAttribute {
	parts := strings.Split(attrPath, ".")
	if len(parts) != 2 {
		// Fallback to raw format
		return FetchAttribute{Key: key, Var: attrPath, AttrName: ""}
	}
	return FetchAttribute{Key: key, Var: parts[0], AttrName: parts[1]}
}

// FetchVar creates a FetchVariable for fetching a variable directly.
func FetchVar(key, varName string) FetchVariable {
	return FetchVariable{Key: key, Var: varName}
}

// FetchFunc creates a FetchFunction for fetching the result of a function.
func FetchFunc(key, funcName, varName string) FetchFunction {
	return FetchFunction{Key: key, FuncName: funcName, Var: varName}
}

// DeleteHas creates a DeleteHasStatement for deleting an attribute from its owner.
// Compiles to: $attrVar of $ownerVar
func DeleteHas(attrVar, ownerVar string) DeleteHasStatement {
	return DeleteHasStatement{AttrVar: attrVar, OwnerVar: ownerVar}
}

// Cmp creates a ValueComparisonPattern for comparing a variable to a value.
func Cmp(variable, operator string, value any) ValueComparisonPattern {
	return ValueComparisonPattern{Var: variable, Operator: operator, Value: value}
}

// Or creates an OrPattern from multiple pattern alternatives.
// Each alternative is a slice of patterns that must all match.
func Or(alternatives ...[]Pattern) OrPattern {
	return OrPattern{Alternatives: alternatives}
}

// ValueFromGo converts a Go value to an AST Value node.
// Handles common types: string, int, int64, float64, bool, time.Time.
// Falls back to string representation for unknown types.
func ValueFromGo(val any) Value {
	if val == nil {
		return Str("") // or handle nil differently
	}

	switch v := val.(type) {
	case string:
		return Str(v)
	case int:
		return Long(int64(v))
	case int64:
		return Long(v)
	case float32:
		return Double(float64(v))
	case float64:
		return Double(v)
	case bool:
		return Bool(v)
	case time.Time:
		// Format as datetime literal
		return Lit(v.Format(time.RFC3339), "datetime")
	default:
		// Fallback: format as string using the existing formatter
		return Lit(FormatGoValue(val), "string")
	}
}
