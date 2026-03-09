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
		var b strings.Builder
		b.WriteString("match\n")
		for i, p := range cl.Patterns {
			if i > 0 {
				b.WriteString(";\n")
			}
			s, err := c.compilePattern(p)
			if err != nil {
				return "", err
			}
			b.WriteString(s)
		}
		b.WriteByte(';')
		return b.String(), nil

	case MatchLetClause:
		return c.compileMatchLet(cl)

	case InsertClause:
		stmts := make([]string, 0, len(cl.Statements))
		for _, s := range cl.Statements {
			compiled, err := c.compileStatement(s)
			if err != nil {
				return "", err
			}
			stmts = append(stmts, compiled)
		}
		return "insert\n" + strings.Join(stmts, ";\n") + ";", nil

	case DeleteClause:
		stmts := make([]string, 0, len(cl.Statements))
		for _, s := range cl.Statements {
			compiled, err := c.compileStatement(s)
			if err != nil {
				return "", err
			}
			stmts = append(stmts, compiled)
		}
		return "delete\n" + strings.Join(stmts, ";\n") + ";", nil

	case UpdateClause:
		stmts := make([]string, 0, len(cl.Statements))
		for _, s := range cl.Statements {
			compiled, err := c.compileStatement(s)
			if err != nil {
				return "", err
			}
			stmts = append(stmts, compiled)
		}
		return "update\n" + strings.Join(stmts, ";\n") + ";", nil

	case FetchClause:
		var b strings.Builder
		b.WriteString("fetch {\n  ")
		for i, item := range cl.Items {
			if i > 0 {
				b.WriteString(",\n  ")
			}
			compiled, err := c.compileFetchItem(item)
			if err != nil {
				return "", err
			}
			b.WriteString(compiled)
		}
		b.WriteString("\n};")
		return b.String(), nil

	case ReduceClause:
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

	case PutClause:
		stmts := make([]string, 0, len(cl.Statements))
		for _, s := range cl.Statements {
			compiled, err := c.compileStatement(s)
			if err != nil {
				return "", err
			}
			stmts = append(stmts, compiled)
		}
		return "put\n" + strings.Join(stmts, ";\n") + ";", nil

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

func (c *Compiler) compileMatchLet(clause MatchLetClause) (string, error) {
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

	case RelationPattern:
		return c.compileRelationPattern(p)

	case SubTypePattern:
		return p.Variable + " sub " + p.ParentType, nil

	case HasPattern:
		return p.ThingVar + " has " + p.AttrType + " " + p.AttrVar, nil

	case ValueComparisonPattern:
		valStr, err := c.compileValueOrString(p.Value)
		if err != nil {
			return "", err
		}
		return p.Var + " " + p.Operator + " " + valStr, nil

	case NotPattern:
		subPatterns := make([]string, 0, len(p.Patterns))
		for _, sp := range p.Patterns {
			s, err := c.compilePattern(sp)
			if err != nil {
				return "", err
			}
			subPatterns = append(subPatterns, s)
		}
		inner := strings.Join(subPatterns, "; ")
		return "not { " + inner + "; }", nil

	case OrPattern:
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
			blockContent := strings.Join(subPatterns, "; ")
			blocks = append(blocks, "{ "+blockContent+"; }")
		}
		return strings.Join(blocks, " or "), nil

	case IidPattern:
		return p.Variable + " iid " + p.IID, nil

	case AttributePattern:
		parts := []string{p.Variable + " isa " + p.TypeName}
		if p.Value != nil {
			valStr, err := c.compileValue(p.Value)
			if err != nil {
				return "", err
			}
			parts = append(parts, p.Variable+" "+valStr)
		}
		return strings.Join(parts, "; "), nil

	case RawPattern:
		return p.Content, nil

	default:
		return "", fmt.Errorf("unknown pattern type: %T", pattern)
	}
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
		valStr, err := c.compileValueOrString(cn.Value)
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
		leftStr, err := c.compileValueOrString(val.Left)
		if err != nil {
			return "", err
		}
		rightStr, err := c.compileValueOrString(val.Right)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("(%s %s %s)", leftStr, val.Operator, rightStr), nil

	case FunctionCallValue:
		args := make([]string, 0, len(val.Args))
		for _, arg := range val.Args {
			s, err := c.compileValueOrString(arg)
			if err != nil {
				return "", err
			}
			args = append(args, s)
		}
		return fmt.Sprintf("%s(%s)", val.Function, strings.Join(args, ", ")), nil

	case LiteralValue:
		return FormatLiteral(val.Val, val.ValueType), nil

	default:
		return "", fmt.Errorf("unknown value type: %T", v)
	}
}

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
		return `"` + fi.Key + `": ` + fi.Var + "." + fi.AttrName, nil

	case FetchVariable:
		return `"` + fi.Key + `": ` + fi.Var, nil

	case FetchAttributeList:
		return `"` + fi.Key + `": [` + fi.Var + "." + fi.AttrName + "]", nil

	case FetchFunction:
		return `"` + fi.Key + `": ` + fi.FuncName + "(" + fi.Var + ")", nil

	case FetchWildcard:
		return `"` + fi.Key + `": ` + fi.Var + ".*", nil

	case FetchNestedWildcard:
		return `"` + fi.Key + `": { ` + fi.Var + ".* }", nil

	default:
		return "", fmt.Errorf("unknown fetch item type: %T", item)
	}
}

// --- Reduce ---

func (c *Compiler) compileReduceAssignment(a ReduceAssignment) (string, error) {
	exprStr, err := c.compileValueOrString(a.Expression)
	if err != nil {
		return "", err
	}
	return a.Variable + " = " + exprStr, nil
}

// FormatLiteral formats a Go value as a TypeQL literal string.
func FormatLiteral(val any, valueType string) string {
	switch valueType {
	case "string":
		s, _ := val.(string)
		return `"` + EscapeString(s) + `"`
	case "boolean":
		b, _ := val.(bool)
		if b {
			return "true"
		}
		return "false"
	case "long":
		return formatInteger(val)
	case "double":
		return formatFloat(val)
	case "datetime":
		if t, ok := val.(time.Time); ok {
			return t.Format("2006-01-02T15:04:05")
		}
		return fmt.Sprint(val)
	case "datetime-tz":
		if t, ok := val.(time.Time); ok {
			return t.Format(time.RFC3339)
		}
		return fmt.Sprint(val)
	case "date":
		if t, ok := val.(time.Time); ok {
			return t.Format("2006-01-02")
		}
		return fmt.Sprint(val)
	default:
		return `"` + EscapeString(fmt.Sprint(val)) + `"`
	}
}

// EscapeString escapes special characters in a string for use in TypeQL string literals.
// It handles backslashes, quotes, newlines, carriage returns, and tabs.
func EscapeString(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	s = strings.ReplaceAll(s, "\n", `\n`)
	s = strings.ReplaceAll(s, "\r", `\r`)
	s = strings.ReplaceAll(s, "\t", `\t`)
	return s
}

// FormatGoValue converts a Go value into its TypeQL literal string representation.
// It uses reflection to determine the type and handles basic types, pointers, and time.Time.
// This is the canonical formatting function for Go values; other packages should use this
// instead of implementing their own formatting logic.
func FormatGoValue(value any) string {
	if value == nil {
		return "null"
	}

	v := reflect.ValueOf(value)

	// Dereference pointers
	for v.Kind() == reflect.Ptr {
		if v.IsNil() {
			return "null"
		}
		v = v.Elem()
		value = v.Interface()
	}

	switch val := value.(type) {
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
		// Date-only format (midnight UTC)
		if val.Hour() == 0 && val.Minute() == 0 && val.Second() == 0 && val.Nanosecond() == 0 {
			return val.Format("2006-01-02")
		}
		// DateTime without timezone (UTC)
		if val.Location() == time.UTC {
			return val.Format("2006-01-02T15:04:05")
		}
		// DateTime with timezone
		return val.Format(time.RFC3339)
	default:
		// Fallback: convert to string and escape
		s := fmt.Sprint(val)
		return `"` + EscapeString(s) + `"`
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
