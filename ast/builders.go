// Package ast provides builder helpers for ergonomic AST construction.
package ast

import (
	"fmt"
	"math"
	"reflect"
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
// The value can be a Value node (Str, Long, ...), a string beginning with
// "$" (treated as a variable reference), or any other Go value, which is
// converted via ValueFromGo and emitted as a quoted/escaped TypeQL literal.
// To match a literal string that begins with "$", wrap it in Str.
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
// The path is split at the first dot. A path without a dot (e.g. "$p") is
// treated as a bare variable and compiles to a plain variable fetch.
func FetchAttrPath(key, attrPath string) FetchAttribute {
	varName, attrName, found := strings.Cut(attrPath, ".")
	if !found {
		return FetchAttribute{Key: key, Var: attrPath}
	}
	return FetchAttribute{Key: key, Var: varName, AttrName: attrName}
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
// The value can be a Value node, a string beginning with "$" (treated as a
// variable reference), or any other Go value, which is converted via
// ValueFromGo and emitted as a quoted/escaped TypeQL literal.
func Cmp(variable, operator string, value any) ValueComparisonPattern {
	return ValueComparisonPattern{Var: variable, Operator: operator, Value: value}
}

// Or creates an OrPattern from multiple pattern alternatives.
// Each alternative is a slice of patterns that must all match.
func Or(alternatives ...[]Pattern) OrPattern {
	return OrPattern{Alternatives: alternatives}
}

// errorValue is a Value carrying a conversion error. Compiling it surfaces
// the error, so invalid conversions (e.g. nil) fail at compile time instead
// of silently producing a wrong literal.
type errorValue struct{ err error }

func (errorValue) queryNode() {}
func (errorValue) value()     {}

// errValuef creates an errorValue with a formatted message.
func errValuef(format string, args ...any) Value {
	return errorValue{err: fmt.Errorf(format, args...)}
}

// ValueFromGo converts a Go value to an AST Value node.
// It handles strings, booleans, all integer and unsigned integer widths,
// floats, time.Time, and (via reflection) pointers and named types of those
// kinds; other types fall back to their string representation. A nil value
// (or nil pointer) yields a Value whose compilation fails with a descriptive
// error rather than silently becoming an empty string.
func ValueFromGo(val any) Value {
	switch v := val.(type) {
	case nil:
		return errValuef("cannot convert nil to a TypeQL value")
	case string:
		return Str(v)
	case bool:
		return Bool(v)
	case int:
		return Long(int64(v))
	case int8:
		return Long(int64(v))
	case int16:
		return Long(int64(v))
	case int32:
		return Long(int64(v))
	case int64:
		return Long(v)
	case uint:
		return longFromUint64(uint64(v))
	case uint8:
		return Long(int64(v))
	case uint16:
		return Long(int64(v))
	case uint32:
		return Long(int64(v))
	case uint64:
		return longFromUint64(v)
	case float32:
		return Double(float64(v))
	case float64:
		return Double(v)
	case time.Time:
		// Format as datetime literal
		return Lit(v.Format(time.RFC3339), "datetime")
	default:
		return valueFromGoReflect(val)
	}
}

// valueFromGoReflect is the reflection-based slow path of ValueFromGo,
// handling pointers and named/unknown types.
func valueFromGoReflect(val any) Value {
	v := reflect.ValueOf(val)
	for v.Kind() == reflect.Pointer {
		if v.IsNil() {
			return errValuef("cannot convert nil %T to a TypeQL value", val)
		}
		v = v.Elem()
	}

	if t, ok := v.Interface().(time.Time); ok {
		return Lit(t.Format(time.RFC3339), "datetime")
	}

	switch v.Kind() {
	case reflect.String:
		return Str(v.String())
	case reflect.Bool:
		return Bool(v.Bool())
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return Long(v.Int())
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return longFromUint64(v.Uint())
	case reflect.Float32, reflect.Float64:
		return Double(v.Float())
	default:
		// Fallback: single string formatting; quoting/escaping happens once
		// when the literal is compiled.
		return Str(fmt.Sprint(v.Interface()))
	}
}

// longFromUint64 converts an unsigned value to a Long, guarding against
// overflow of TypeQL's signed 64-bit integer range.
func longFromUint64(v uint64) Value {
	if v > math.MaxInt64 {
		return errValuef("uint64 value %d overflows TypeQL's signed 64-bit integer range", v)
	}
	return Long(int64(v))
}
