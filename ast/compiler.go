// Package ast defines the Abstract Syntax Tree (AST) for TypeQL queries.
package ast

import (
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"time"
)

// Compiler compiles AST nodes into TypeQL query strings.
// It traverses the AST and generates the corresponding TypeQL syntax.
type Compiler struct{}

// Compile compiles a single AST node into its TypeQL string representation.
// It returns an error if the node type is unknown or if compilation fails.
func (c *Compiler) Compile(node QueryNode) (string, error) {
	switch n := node.(type) {
	case Clause:
		return c.compileClause(n)
	case Pattern:
		return c.compilePattern(n)
	case Statement:
		return c.compileStatement(n)
	case Constraint:
		return c.compileConstraint(n)
	case Value:
		return c.compileValue(n)
	default:
		return "", fmt.Errorf("unknown node type: %T", node)
	}
}

// CompileBatch compiles a list of AST nodes into a single query string.
// The separator controls how compiled nodes are joined; empty means newline.
func (c *Compiler) CompileBatch(nodes []QueryNode, separator string) (string, error) {
	if separator == "" {
		separator = "\n"
	}
	var b strings.Builder
	for i, node := range nodes {
		s, err := c.Compile(node)
		if err != nil {
			return "", err
		}
		if i > 0 {
			b.WriteString(separator)
		}
		b.WriteString(s)
	}
	return b.String(), nil
}

// --- Clauses ---

func (c *Compiler) compileClause(clause Clause) (string, error) {
	switch cl := clause.(type) {
	case MatchClause:
		return c.compileMatchClause(cl)
	case MatchLetClause:
		return c.compileMatchLet(cl)
	case InsertClause:
		return c.compileStmtBlock("insert", cl.Statements)
	case DeleteClause:
		return c.compileStmtBlock("delete", cl.Statements)
	case UpdateClause:
		return c.compileStmtBlock("update", cl.Statements)
	case PutClause:
		return c.compileStmtBlock("put", cl.Statements)
	case FetchClause:
		return c.compileFetchClause(cl)
	case ReduceClause:
		return c.compileReduceClause(cl)
	case SelectClause:
		return "select " + strings.Join(cl.Variables, ", ") + ";", nil
	case SortClause:
		return fmt.Sprintf("sort %s %s;", cl.Variable, cl.Direction), nil
	case OffsetClause:
		return fmt.Sprintf("offset %d;", cl.Count), nil
	case LimitClause:
		return fmt.Sprintf("limit %d;", cl.Count), nil
	default:
		return "", fmt.Errorf("unknown clause type: %T", clause)
	}
}

func (c *Compiler) compileMatchClause(cl MatchClause) (string, error) {
	if len(cl.Patterns) == 0 {
		return "", fmt.Errorf("match clause has no patterns")
	}
	return joinCompiled("match\n", ";\n", ";", cl.Patterns, c.compilePattern)
}

func (c *Compiler) compileStmtBlock(keyword string, statements []Statement) (string, error) {
	if len(statements) == 0 {
		return "", fmt.Errorf("%s clause has no statements", keyword)
	}
	stmts := make([]string, 0, len(statements))
	for _, s := range statements {
		compiled, err := c.compileStatement(s)
		if err != nil {
			return "", err
		}
		stmts = append(stmts, compiled)
	}
	return keyword + "\n" + strings.Join(stmts, ";\n") + ";", nil
}

func (c *Compiler) compileFetchClause(cl FetchClause) (string, error) {
	return joinCompiled("fetch {\n  ", ",\n  ", "\n};", cl.Items, c.compileFetchItem)
}

// joinCompiled compiles each item and concatenates prefix + items (separated) + suffix.
// Used by clause compilers that share this shape (match, fetch).
func joinCompiled[T any](prefix, separator, suffix string, items []T, compile func(T) (string, error)) (string, error) {
	var b strings.Builder
	b.WriteString(prefix)
	for i, item := range items {
		if i > 0 {
			b.WriteString(separator)
		}
		s, err := compile(item)
		if err != nil {
			return "", err
		}
		b.WriteString(s)
	}
	b.WriteString(suffix)
	return b.String(), nil
}

func (c *Compiler) compileReduceClause(cl ReduceClause) (string, error) {
	assignments := make([]string, 0, len(cl.Assignments))
	for _, a := range cl.Assignments {
		compiled, err := c.compileReduceAssignment(a)
		if err != nil {
			return "", err
		}
		assignments = append(assignments, compiled)
	}
	reduceStr := "reduce " + strings.Join(assignments, ", ")
	if cl.GroupBy != "" {
		reduceStr += " groupby " + cl.GroupBy
	}
	return reduceStr + ";", nil
}

func (c *Compiler) compileMatchLet(clause MatchLetClause) (string, error) {
	if len(clause.Patterns) == 0 && len(clause.Assignments) == 0 {
		return "", fmt.Errorf("match clause has no patterns or let assignments")
	}
	lines := make([]string, 0, len(clause.Patterns)+len(clause.Assignments))
	for _, pattern := range clause.Patterns {
		compiled, err := c.compilePattern(pattern)
		if err != nil {
			return "", err
		}
		lines = append(lines, compiled)
	}
	assignments := make([]string, 0, len(clause.Assignments))
	for _, a := range clause.Assignments {
		compiled, err := c.compileLetAssignment(a)
		if err != nil {
			return "", err
		}
		assignments = append(assignments, compiled)
	}
	lines = append(lines, assignments...)
	return "match\n" + strings.Join(lines, ";\n") + ";", nil
}

func (c *Compiler) compileLetAssignment(a LetAssignment) (string, error) {
	varsStr := strings.Join(a.Variables, ", ")
	op := "="
	if a.IsStream {
		op = "in"
	}
	exprStr, err := c.compileValueOrString(a.Expression)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("let %s %s %s", varsStr, op, exprStr), nil
}

// --- Patterns ---

func (c *Compiler) compilePattern(pattern Pattern) (string, error) {
	switch p := pattern.(type) {
	case EntityPattern:
		return c.compileEntityPattern(p)
	case RelationPattern:
		return c.compileRelationPattern(p)
	case SubTypePattern:
		return p.Variable + " sub " + p.ParentType, nil
	case HasPattern:
		return p.ThingVar + " has " + p.AttrType + " " + p.AttrVar, nil
	case ValueComparisonPattern:
		valStr, err := c.compileValueOrVar(p.Value)
		if err != nil {
			return "", err
		}
		return p.Var + " " + p.Operator + " " + valStr, nil
	case NotPattern:
		return c.compileNotPattern(p)
	case OrPattern:
		return c.compileOrPattern(p)
	case IidPattern:
		return p.Variable + " iid " + p.IID, nil
	case AttributePattern:
		return c.compileAttributePattern(p)
	case RawPattern:
		return p.Content, nil
	default:
		return "", fmt.Errorf("unknown pattern type: %T", pattern)
	}
}

func (c *Compiler) compileEntityPattern(p EntityPattern) (string, error) {
	op := "isa"
	if p.IsStrict {
		op = "isa!"
	}
	var b strings.Builder
	b.Grow(len(p.Variable) + len(op) + len(p.TypeName) + len(p.Constraints)*16)
	b.WriteString(p.Variable)
	b.WriteByte(' ')
	b.WriteString(op)
	b.WriteByte(' ')
	b.WriteString(p.TypeName)
	for _, constraint := range p.Constraints {
		s, err := c.compileConstraint(constraint)
		if err != nil {
			return "", err
		}
		b.WriteString(", ")
		b.WriteString(s)
	}
	return b.String(), nil
}

func (c *Compiler) compileNotPattern(p NotPattern) (string, error) {
	subPatterns := make([]string, 0, len(p.Patterns))
	for _, sp := range p.Patterns {
		s, err := c.compilePattern(sp)
		if err != nil {
			return "", err
		}
		subPatterns = append(subPatterns, s)
	}
	return "not { " + strings.Join(subPatterns, "; ") + "; }", nil
}

func (c *Compiler) compileOrPattern(p OrPattern) (string, error) {
	blocks := make([]string, 0, len(p.Alternatives))
	for _, alt := range p.Alternatives {
		subPatterns := make([]string, 0, len(alt))
		for _, sp := range alt {
			s, err := c.compilePattern(sp)
			if err != nil {
				return "", err
			}
			subPatterns = append(subPatterns, s)
		}
		blocks = append(blocks, "{ "+strings.Join(subPatterns, "; ")+"; }")
	}
	return strings.Join(blocks, " or "), nil
}

func (c *Compiler) compileAttributePattern(p AttributePattern) (string, error) {
	parts := []string{p.Variable + " isa " + p.TypeName}
	if p.Value != nil {
		valStr, err := c.compileValue(p.Value)
		if err != nil {
			return "", err
		}
		parts = append(parts, p.Variable+" "+valStr)
	}
	return strings.Join(parts, "; "), nil
}

// --- Statements ---

func (c *Compiler) compileStatement(stmt Statement) (string, error) {
	switch s := stmt.(type) {
	case HasStatement:
		valStr, err := c.compileValue(s.Value)
		if err != nil {
			return "", err
		}
		return s.SubjectVar + " has " + s.AttrName + " " + valStr, nil

	case IsaStatement:
		return s.Variable + " isa " + s.TypeName, nil

	case RelationStatement:
		roleParts := make([]string, 0, len(s.RolePlayers))
		for _, rp := range s.RolePlayers {
			roleParts = append(roleParts, fmt.Sprintf("%s: %s", rp.Role, rp.PlayerVar))
		}
		rolesStr := "(" + strings.Join(roleParts, ", ") + ")"

		var base string
		if s.IncludeVariable {
			base = fmt.Sprintf("%s isa %s, links %s", s.Variable, s.TypeName, rolesStr)
		} else {
			base = fmt.Sprintf("%s isa %s", rolesStr, s.TypeName)
		}

		if len(s.Attributes) > 0 {
			attrParts := make([]string, 0, len(s.Attributes))
			for _, attr := range s.Attributes {
				valStr, err := c.compileValue(attr.Value)
				if err != nil {
					return "", err
				}
				attrParts = append(attrParts, fmt.Sprintf("has %s %s", attr.AttrName, valStr))
			}
			return base + ", " + strings.Join(attrParts, ", "), nil
		}
		return base, nil

	case DeleteThingStatement:
		return s.Variable, nil

	case DeleteHasStatement:
		return s.AttrVar + " of " + s.OwnerVar, nil

	case RawStatement:
		return s.Content, nil

	default:
		return "", fmt.Errorf("unknown statement type: %T", stmt)
	}
}

// --- Constraints ---

func (c *Compiler) compileConstraint(constraint Constraint) (string, error) {
	switch cn := constraint.(type) {
	case IidConstraint:
		return "iid " + cn.IID, nil

	case HasConstraint:
		valStr, err := c.compileValueOrVar(cn.Value)
		if err != nil {
			return "", err
		}
		return "has " + cn.AttrName + " " + valStr, nil

	case IsaConstraint:
		op := "isa"
		if cn.Strict {
			op = "isa!"
		}
		return op + " " + cn.TypeName, nil

	default:
		return "", fmt.Errorf("unknown constraint type: %T", constraint)
	}
}

// --- Values ---

func (c *Compiler) compileValue(v Value) (string, error) {
	switch val := v.(type) {
	case ArithmeticValue:
		leftStr, err := c.compileValueOrVar(val.Left)
		if err != nil {
			return "", err
		}
		rightStr, err := c.compileValueOrVar(val.Right)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("(%s %s %s)", leftStr, val.Operator, rightStr), nil

	case FunctionCallValue:
		args := make([]string, 0, len(val.Args))
		for _, arg := range val.Args {
			s, err := c.compileValueOrVar(arg)
			if err != nil {
				return "", err
			}
			args = append(args, s)
		}
		return fmt.Sprintf("%s(%s)", val.Function, strings.Join(args, ", ")), nil

	case LiteralValue:
		return formatLiteralChecked(val.Val, val.ValueType)

	case errorValue:
		return "", val.err

	default:
		return "", fmt.Errorf("unknown value type: %T", v)
	}
}

// compileValueOrVar compiles a value position. Value nodes compile directly,
// strings starting with "$" pass through as variable references, and any
// other Go value (including bare strings) is converted via ValueFromGo so it
// is emitted as a properly quoted/escaped TypeQL literal.
func (c *Compiler) compileValueOrVar(v any) (string, error) {
	switch val := v.(type) {
	case Value:
		return c.compileValue(val)
	case string:
		if strings.HasPrefix(val, "$") {
			return val, nil
		}
		return c.compileValue(ValueFromGo(val))
	default:
		return c.compileValue(ValueFromGo(v))
	}
}

// compileValueOrString compiles an expression position (let/reduce
// expressions): Value nodes compile directly, strings pass through verbatim
// as raw TypeQL expressions (e.g. "count($p)" or "$x").
func (c *Compiler) compileValueOrString(v any) (string, error) {
	switch val := v.(type) {
	case Value:
		return c.compileValue(val)
	case string:
		return val, nil
	default:
		return "", fmt.Errorf("expected Value or string, got %T", v)
	}
}

func (c *Compiler) compileRelationPattern(p RelationPattern) (string, error) {
	op := "isa"
	if p.IsStrict {
		op = "isa!"
	}

	regularRoles := make([]RolePlayer, 0, len(p.RolePlayers))
	linkRoles := make([]RolePlayer, 0, len(p.RolePlayers))
	for _, rp := range p.RolePlayers {
		if rp.Role == "links" {
			linkRoles = append(linkRoles, rp)
		} else {
			regularRoles = append(regularRoles, rp)
		}
	}

	var b strings.Builder
	switch {
	case p.TypeName != "" && len(regularRoles) > 0:
		if p.Variable != "" {
			b.WriteString(p.Variable)
			b.WriteByte(' ')
			b.WriteString(op)
			b.WriteByte(' ')
			b.WriteString(p.TypeName)
			b.WriteByte(' ')
			appendRelationRoles(&b, regularRoles)
		} else {
			appendRelationRoles(&b, regularRoles)
			b.WriteByte(' ')
			b.WriteString(op)
			b.WriteByte(' ')
			b.WriteString(p.TypeName)
		}
	case p.TypeName != "":
		if p.Variable != "" {
			b.WriteString(p.Variable)
			b.WriteByte(' ')
		}
		b.WriteString(op)
		b.WriteByte(' ')
		b.WriteString(p.TypeName)
	case len(regularRoles) > 0:
		if p.Variable != "" {
			b.WriteString(p.Variable)
			b.WriteByte(' ')
		}
		appendRelationRoles(&b, regularRoles)
	default:
		b.WriteString(p.Variable)
	}

	for _, rp := range linkRoles {
		if b.Len() > 0 {
			b.WriteString(", ")
		}
		b.WriteString("links (")
		b.WriteString(rp.PlayerVar)
		b.WriteByte(')')
	}

	for _, constraint := range p.Constraints {
		s, err := c.compileConstraint(constraint)
		if err != nil {
			return "", err
		}
		if b.Len() > 0 {
			b.WriteString(", ")
		}
		b.WriteString(s)
	}

	return b.String(), nil
}

func appendRelationRoles(b *strings.Builder, roles []RolePlayer) {
	b.WriteByte('(')
	for i, rp := range roles {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(rp.Role)
		b.WriteString(": ")
		b.WriteString(rp.PlayerVar)
	}
	b.WriteByte(')')
}

// --- Fetch Items ---

func (c *Compiler) compileFetchItem(item any) (string, error) {
	switch fi := item.(type) {
	case string:
		return fi, nil

	case FetchAttribute:
		if fi.AttrName == "" {
			// No attribute: fetch the variable directly instead of emitting
			// an unparseable trailing dot.
			return `"` + EscapeString(fi.Key) + `": ` + fi.Var, nil
		}
		return `"` + EscapeString(fi.Key) + `": ` + fi.Var + "." + fi.AttrName, nil

	case FetchVariable:
		return `"` + EscapeString(fi.Key) + `": ` + fi.Var, nil

	case FetchAttributeList:
		return `"` + EscapeString(fi.Key) + `": [` + fi.Var + "." + fi.AttrName + "]", nil

	case FetchFunction:
		return `"` + EscapeString(fi.Key) + `": ` + fi.FuncName + "(" + fi.Var + ")", nil

	case FetchWildcard:
		// Attribute wildcards are only valid inside an object per the TypeQL
		// grammar, so the braced form is the only parseable emission.
		return `"` + EscapeString(fi.Key) + `": { ` + fi.Var + ".* }", nil

	case FetchNestedWildcard:
		return `"` + EscapeString(fi.Key) + `": { ` + fi.Var + ".* }", nil

	default:
		return "", fmt.Errorf("unknown fetch item type: %T", item)
	}
}

// --- Reduce ---

func (c *Compiler) compileReduceAssignment(a ReduceAssignment) (string, error) {
	if agg, ok := a.Expression.(AggregateExpr); ok {
		compiled, err := compileAggregateExpr(agg)
		if err != nil {
			return "", err
		}
		return a.Variable + " = " + compiled, nil
	}
	exprStr, err := c.compileValueOrString(a.Expression)
	if err != nil {
		return "", err
	}
	return a.Variable + " = " + exprStr, nil
}

// compileAggregateExpr compiles an AggregateExpr into TypeQL, e.g. count($p).
func compileAggregateExpr(agg AggregateExpr) (string, error) {
	if agg.FuncName == "" {
		return "", fmt.Errorf("aggregate expression has no function name")
	}
	if agg.AttrName != "" {
		// TypeQL 3.x reduce aggregates variables only; there is no attribute
		// projection like sum($p.salary). Refusing here beats emitting
		// syntax the server will reject.
		return "", fmt.Errorf(
			"aggregate expression %s: TypeQL reduce cannot aggregate attribute %q directly; bind it to a variable in the match clause (e.g. $p has %s $v) and aggregate that variable",
			agg.FuncName, agg.AttrName, agg.AttrName)
	}
	if agg.Var == "" {
		return "", fmt.Errorf("aggregate expression %s has no variable", agg.FuncName)
	}
	return agg.FuncName + "(" + agg.Var + ")", nil
}

// FormatLiteral formats a Go value as a TypeQL literal string.
// When val does not match valueType (e.g. an int passed as a "string"
// literal), it falls back to FormatGoValue instead of silently emitting a
// zero value. The compiler itself reports such mismatches as errors.
//
// A time.Time with valueType "datetime" is stored as UTC: the instant is
// converted to UTC and emitted as a naive (zone-less) datetime literal with
// up to nanosecond precision, and hydration parses it back as UTC, so the
// instant survives round trips. Use "datetime-tz" to keep the value's own
// offset, or "date" for a date-only literal.
func FormatLiteral(val any, valueType string) string {
	s, err := formatLiteralChecked(val, valueType)
	if err != nil {
		return FormatGoValue(val)
	}
	return s
}

// formatLiteralChecked formats a Go value as a TypeQL literal, returning an
// error when the value's dynamic type does not match the declared value type
// instead of silently emitting a zero value.
func formatLiteralChecked(val any, valueType string) (string, error) {
	switch valueType {
	case "string":
		s, ok := val.(string)
		if !ok {
			return "", fmt.Errorf("string literal: expected string value, got %T (%v)", val, val)
		}
		return `"` + EscapeString(s) + `"`, nil
	case "boolean":
		b, ok := val.(bool)
		if !ok {
			return "", fmt.Errorf("boolean literal: expected bool value, got %T (%v)", val, val)
		}
		if b {
			return "true", nil
		}
		return "false", nil
	case "long":
		switch val.(type) {
		case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64:
			return formatInteger(val), nil
		}
		return "", fmt.Errorf("long literal: expected integer value, got %T (%v)", val, val)
	case "double":
		switch val.(type) {
		case float32, float64:
			return formatFloat(val), nil
		case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64:
			return formatInteger(val), nil
		}
		return "", fmt.Errorf("double literal: expected numeric value, got %T (%v)", val, val)
	case "datetime":
		if t, ok := val.(time.Time); ok {
			// Plain datetime attributes have no timezone: the instant is
			// stored as UTC wall-clock time. Convert before dropping the
			// zone so non-UTC values keep the same instant on round trip
			// (issue #53). Fractional seconds are preserved (up to
			// nanoseconds) and omitted when zero.
			return t.UTC().Format(datetimeLayout), nil
		}
		if s, ok := val.(string); ok {
			// Pre-formatted datetime string.
			return s, nil
		}
		return "", fmt.Errorf("datetime literal: expected time.Time or string value, got %T (%v)", val, val)
	case "datetime-tz":
		if t, ok := val.(time.Time); ok {
			return t.Format(datetimeTZLayout), nil
		}
		if s, ok := val.(string); ok {
			return s, nil
		}
		return "", fmt.Errorf("datetime-tz literal: expected time.Time or string value, got %T (%v)", val, val)
	case "date":
		if t, ok := val.(time.Time); ok {
			return t.Format("2006-01-02"), nil
		}
		if s, ok := val.(string); ok {
			return s, nil
		}
		return "", fmt.Errorf("date literal: expected time.Time or string value, got %T (%v)", val, val)
	default:
		return `"` + EscapeString(fmt.Sprint(val)) + `"`, nil
	}
}

// typeqlStringEscaper rewrites TypeQL string-literal special characters in a
// single pass (and returns the input unchanged, allocation-free, when clean).
var typeqlStringEscaper = strings.NewReplacer(
	`\`, `\\`,
	`"`, `\"`,
	"\n", `\n`,
	"\r", `\r`,
	"\t", `\t`,
)

// EscapeString escapes special characters in a string for use in TypeQL string literals.
// It handles backslashes, quotes, newlines, carriage returns, and tabs.
func EscapeString(s string) string {
	return typeqlStringEscaper.Replace(s)
}

// FormatGoValue converts a Go value into its TypeQL literal string representation.
// It handles basic types, pointers, and time.Time; common concrete types take a
// fast path, and reflection is used only for pointers and named/unknown types.
// This is the canonical formatting function for Go values; other packages should use this
// instead of implementing their own formatting logic.
func FormatGoValue(value any) string {
	switch val := value.(type) {
	case nil:
		return "null"
	case string:
		return `"` + EscapeString(val) + `"`
	case bool:
		if val {
			return "true"
		}
		return "false"
	case int, int8, int16, int32, int64:
		return formatInteger(val)
	case uint, uint8, uint16, uint32, uint64:
		return formatInteger(val)
	case float32, float64:
		return formatFloat(val)
	case time.Time:
		return formatTimeValue(val)
	default:
		return formatGoValueReflect(value)
	}
}

// Canonical wire formats for time.Time values (issue #66). One format per
// TypeQL value type, used consistently by FormatLiteral, FormatGoValue, and
// ValueFromGo:
//
//   - datetime: naive UTC wall-clock time ("2006-01-02T15:04:05[.fff...]").
//     The instant is converted to UTC before the zone is dropped, and read
//     back as UTC on hydration, so round trips preserve the instant.
//   - datetime-tz: RFC 3339 with the value's own offset.
//
// Fractional seconds print up to nanosecond precision and are omitted when
// zero.
const (
	datetimeLayout   = "2006-01-02T15:04:05.999999999"
	datetimeTZLayout = "2006-01-02T15:04:05.999999999Z07:00"
)

// formatTimeValue formats a time.Time as a TypeQL datetime literal (naive,
// UTC). A time.Time always formats as a datetime literal regardless of its
// clock value — callers that want a date or datetime-tz literal must say so
// explicitly via Lit(t, "date") or Lit(t, "datetime-tz") (issue #66; the old
// midnight-means-date heuristic silently changed the literal kind).
func formatTimeValue(val time.Time) string {
	return val.UTC().Format(datetimeLayout)
}

// formatGoValueReflect is the reflection-based slow path of FormatGoValue,
// handling pointers and named/unknown types.
func formatGoValueReflect(value any) string {
	v := reflect.ValueOf(value)

	// Dereference pointers
	for v.Kind() == reflect.Pointer {
		if v.IsNil() {
			return "null"
		}
		v = v.Elem()
	}

	if t, ok := v.Interface().(time.Time); ok {
		return formatTimeValue(t)
	}

	switch v.Kind() {
	case reflect.String:
		return `"` + EscapeString(v.String()) + `"`
	case reflect.Bool:
		if v.Bool() {
			return "true"
		}
		return "false"
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return strconv.FormatInt(v.Int(), 10)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return strconv.FormatUint(v.Uint(), 10)
	case reflect.Float32:
		return strconv.FormatFloat(v.Float(), 'f', -1, 32)
	case reflect.Float64:
		return strconv.FormatFloat(v.Float(), 'f', -1, 64)
	default:
		// Fallback: convert to string and escape
		return `"` + EscapeString(fmt.Sprint(v.Interface())) + `"`
	}
}

func formatInteger(val any) string {
	switch v := val.(type) {
	case int:
		return strconv.Itoa(v)
	case int8:
		return strconv.FormatInt(int64(v), 10)
	case int16:
		return strconv.FormatInt(int64(v), 10)
	case int32:
		return strconv.FormatInt(int64(v), 10)
	case int64:
		return strconv.FormatInt(v, 10)
	case uint:
		return strconv.FormatUint(uint64(v), 10)
	case uint8:
		return strconv.FormatUint(uint64(v), 10)
	case uint16:
		return strconv.FormatUint(uint64(v), 10)
	case uint32:
		return strconv.FormatUint(uint64(v), 10)
	case uint64:
		return strconv.FormatUint(v, 10)
	default:
		return fmt.Sprint(val)
	}
}

func formatFloat(val any) string {
	switch v := val.(type) {
	case float32:
		return strconv.FormatFloat(float64(v), 'f', -1, 32)
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	default:
		return fmt.Sprint(val)
	}
}
