// Package ast defines the Abstract Syntax Tree (AST) for TypeQL queries.
package ast

import (
	"fmt"
	"reflect"
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
// The operation parameter (e.g., "match", "insert") can be provided to wrap the compiled nodes.
func (c *Compiler) CompileBatch(nodes []QueryNode, operation string) (string, error) {
	parts := make([]string, 0, len(nodes))
	for _, node := range nodes {
		s, err := c.Compile(node)
		if err != nil {
			return "", err
		}
		parts = append(parts, s)
	}
	query := strings.Join(parts, "\n")
	if operation != "" {
		return operation + "\n" + query, nil
	}
	return query, nil
}

// --- Clauses ---

func (c *Compiler) compileClause(clause Clause) (string, error) {
	switch cl := clause.(type) {
	case MatchClause:
		patterns := make([]string, 0, len(cl.Patterns))
		for _, p := range cl.Patterns {
			s, err := c.compilePattern(p)
			if err != nil {
				return "", err
			}
			patterns = append(patterns, s)
		}
		return "match\n" + strings.Join(patterns, ";\n") + ";", nil

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
		items := make([]string, 0, len(cl.Items))
		for _, item := range cl.Items {
			compiled, err := c.compileFetchItem(item)
			if err != nil {
				return "", err
			}
			items = append(items, compiled)
		}
		return "fetch {\n  " + strings.Join(items, ",\n  ") + "\n};", nil

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
	assignments := make([]string, 0, len(clause.Assignments))
	for _, a := range clause.Assignments {
		compiled, err := c.compileLetAssignment(a)
		if err != nil {
			return "", err
		}
		assignments = append(assignments, compiled)
	}
	return "match\n" + strings.Join(assignments, ";\n") + ";", nil
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
		parts := []string{fmt.Sprintf("%s %s %s", p.Variable, op, p.TypeName)}
		for _, constraint := range p.Constraints {
			s, err := c.compileConstraint(constraint)
			if err != nil {
				return "", err
			}
			parts = append(parts, s)
		}
		return strings.Join(parts, ", "), nil

	case RelationPattern:
		var parts []string
		if len(p.RolePlayers) > 0 {
			// Separate links roles from regular roles
			// In match patterns, "links" is a keyword and requires special syntax:
			//   links ($var) instead of (links: $var)
			var linksClauses []string
			var regularRoles []string
			for _, rp := range p.RolePlayers {
				if rp.Role == "links" {
					linksClauses = append(linksClauses, fmt.Sprintf("links (%s)", rp.PlayerVar))
				} else {
					regularRoles = append(regularRoles, fmt.Sprintf("%s: %s", rp.Role, rp.PlayerVar))
				}
			}

			// Build the base pattern
			if p.TypeName != "" {
				// Typed relation
				if len(regularRoles) > 0 {
					// $r isa type (regular: $x, regular: $y)
					rolesStr := "(" + strings.Join(regularRoles, ", ") + ")"
					parts = []string{fmt.Sprintf("%s isa %s %s", p.Variable, p.TypeName, rolesStr)}
				} else {
					// $r isa type (no regular roles, only links)
					parts = []string{fmt.Sprintf("%s isa %s", p.Variable, p.TypeName)}
				}
			} else {
				// Typeless relation
				if len(regularRoles) > 0 {
					// $r (regular: $x, regular: $y)
					rolesStr := "(" + strings.Join(regularRoles, ", ") + ")"
					parts = []string{fmt.Sprintf("%s %s", p.Variable, rolesStr)}
				} else {
					// $r (no regular roles, only links)
					parts = []string{p.Variable}
				}
			}

			// Add links clauses as separate parts
			parts = append(parts, linksClauses...)
		} else {
			if p.TypeName != "" {
				parts = []string{fmt.Sprintf("%s isa %s", p.Variable, p.TypeName)}
			} else {
				parts = []string{p.Variable}
			}
		}
		for _, constraint := range p.Constraints {
			s, err := c.compileConstraint(constraint)
			if err != nil {
				return "", err
			}
			parts = append(parts, s)
		}
		return strings.Join(parts, ", "), nil

	case SubTypePattern:
		return fmt.Sprintf("%s sub %s", p.Variable, p.ParentType), nil

	case HasPattern:
		return fmt.Sprintf("%s has %s %s", p.ThingVar, p.AttrType, p.AttrVar), nil

	case ValueComparisonPattern:
		valStr, err := c.compileValueOrString(p.Value)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("%s %s %s", p.Var, p.Operator, valStr), nil

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
		return fmt.Sprintf("not { %s; }", inner), nil

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
			blocks = append(blocks, fmt.Sprintf("{ %s; }", blockContent))
		}
		return strings.Join(blocks, " or "), nil

	case IidPattern:
		return fmt.Sprintf("%s iid %s", p.Variable, p.IID), nil

	case AttributePattern:
		parts := []string{fmt.Sprintf("%s isa %s", p.Variable, p.TypeName)}
		if p.Value != nil {
			valStr, err := c.compileValue(p.Value)
			if err != nil {
				return "", err
			}
			parts = append(parts, fmt.Sprintf("%s %s", p.Variable, valStr))
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
		return fmt.Sprintf("%s has %s %s", s.SubjectVar, s.AttrName, valStr), nil

	case IsaStatement:
		return fmt.Sprintf("%s isa %s", s.Variable, s.TypeName), nil

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
		return fmt.Sprintf("%s of %s", s.AttrVar, s.OwnerVar), nil

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
		return fmt.Sprintf("has %s %s", cn.AttrName, valStr), nil

	case IsaConstraint:
		op := "isa"
		if cn.Strict {
			op = "isa!"
		}
		return fmt.Sprintf("%s %s", op, cn.TypeName), nil

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

// --- Fetch Items ---

func (c *Compiler) compileFetchItem(item any) (string, error) {
	switch fi := item.(type) {
	case string:
		return fi, nil

	case FetchAttribute:
		return fmt.Sprintf(`"%s": %s.%s`, fi.Key, fi.Var, fi.AttrName), nil

	case FetchVariable:
		return fmt.Sprintf(`"%s": %s`, fi.Key, fi.Var), nil

	case FetchAttributeList:
		return fmt.Sprintf(`"%s": [%s.%s]`, fi.Key, fi.Var, fi.AttrName), nil

	case FetchFunction:
		return fmt.Sprintf(`"%s": %s(%s)`, fi.Key, fi.FuncName, fi.Var), nil

	case FetchWildcard:
		return fmt.Sprintf(`"%s": %s.*`, fi.Key, fi.Var), nil

	case FetchNestedWildcard:
		return fmt.Sprintf(`"%s": { %s.* }`, fi.Key, fi.Var), nil

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
	return fmt.Sprintf("%s = %s", a.Variable, exprStr), nil
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
		return fmt.Sprintf("%d", val)
	case "double":
		return fmt.Sprintf("%v", val)
	case "datetime":
		if t, ok := val.(time.Time); ok {
			return t.Format("2006-01-02T15:04:05")
		}
		return fmt.Sprintf("%v", val)
	case "datetime-tz":
		if t, ok := val.(time.Time); ok {
			return t.Format(time.RFC3339)
		}
		return fmt.Sprintf("%v", val)
	case "date":
		if t, ok := val.(time.Time); ok {
			return t.Format("2006-01-02")
		}
		return fmt.Sprintf("%v", val)
	default:
		return fmt.Sprintf(`"%v"`, val)
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
		return fmt.Sprintf("%d", val)
	case uint, uint8, uint16, uint32, uint64:
		return fmt.Sprintf("%d", val)
	case float32, float64:
		return fmt.Sprintf("%v", val)
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
		s := fmt.Sprintf("%v", val)
		return `"` + EscapeString(s) + `"`
	}
}
