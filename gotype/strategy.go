// Package gotype defines query generation strategies for different TypeDB model kinds.
package gotype

import (
	"fmt"
	"reflect"
	"strings"

	"github.com/CaliLuke/go-typeql/ast"
)

// ModelStrategy specifies the interface for building TypeQL queries based on
// the kind of model (entity or relation).
type ModelStrategy interface {
	// BuildInsertQuery generates a TypeQL insert statement for an instance.
	BuildInsertQuery(info *ModelInfo, instance any, varName string) string
	// BuildPutQuery generates a TypeQL put (upsert) statement for an instance.
	BuildPutQuery(info *ModelInfo, instance any, varName string) string
	// BuildMatchByKey generates a match clause based on the model's key attributes.
	BuildMatchByKey(info *ModelInfo, instance any, varName string) string
	// BuildMatchByIID generates a match clause based on the internal instance ID.
	BuildMatchByIID(iid string, varName string) string
	// BuildMatchAll generates a match clause for all instances of the type.
	BuildMatchAll(info *ModelInfo, varName string) string
	// BuildFetchAll generates a fetch clause for all attributes of the type.
	BuildFetchAll(info *ModelInfo, varName string) string
	// BuildMatchAllStrict generates a strict match clause using isa!.
	BuildMatchAllStrict(info *ModelInfo, varName string) string
	// BuildFetchAllWithType generates a fetch clause that includes the type label.
	BuildFetchAllWithType(info *ModelInfo, varName string) string
	// BuildFetchWithRoles generates a fetch clause including role player data for relations.
	BuildFetchWithRoles(info *ModelInfo, varName string) (matchAdditions string, fetchClause string)
}

// strategyFor returns the appropriate strategy for the given model kind.
func strategyFor(kind ModelKind) ModelStrategy {
	if kind == ModelKindRelation {
		return &relationStrategy{}
	}
	return &entityStrategy{}
}

// --- Entity Strategy ---

type entityStrategy struct{}

func (s *entityStrategy) BuildInsertQuery(info *ModelInfo, instance any, varName string) string {
	query := s.buildInsertOrPut(info, instance, varName, "insert")

	// Append fetch clause to retrieve IID in the same query
	fetch := ast.Fetch(ast.FetchFunc("_iid", "iid", "$"+varName))
	compiler := &ast.Compiler{}
	fetchStr, _ := compiler.Compile(fetch)

	return query + "\n" + fetchStr
}

func (s *entityStrategy) BuildPutQuery(info *ModelInfo, instance any, varName string) string {
	return s.buildInsertOrPut(info, instance, varName, "put")
}

func (s *entityStrategy) buildInsertOrPut(info *ModelInfo, instance any, varName string, keyword string) string {
	v := reflectValue(instance)

	// Build AST statements
	statements := []ast.Statement{
		ast.IsaStmt("$"+varName, info.TypeName),
	}

	for _, fi := range info.Fields {
		vals := extractFieldValues(v, fi)
		for _, val := range vals {
			statements = append(statements,
				ast.HasStmt("$"+varName, fi.Tag.Name, ast.ValueFromGo(val)))
		}
	}

	// Build clause based on keyword
	var clause ast.Clause
	if keyword == "insert" {
		clause = ast.Insert(statements...)
	} else {
		clause = ast.Put(statements...)
	}

	// Compile to TypeQL
	compiler := &ast.Compiler{}
	result, _ := compiler.Compile(clause)
	return result
}

func (s *entityStrategy) BuildMatchByKey(info *ModelInfo, instance any, varName string) string {
	v := reflectValue(instance)

	// Build has constraints for key fields
	var constraints []ast.Constraint
	for _, fi := range info.KeyFields {
		val := extractSingleFieldValue(v, fi)
		if val != nil {
			constraints = append(constraints, ast.Has(fi.Tag.Name, ast.ValueFromGo(val)))
		}
	}

	// Build match clause
	match := ast.Match(
		ast.Entity("$"+varName, info.TypeName, constraints...),
	)

	compiler := &ast.Compiler{}
	result, _ := compiler.Compile(match)
	return result
}

func (s *entityStrategy) BuildMatchByIID(iid string, varName string) string {
	match := ast.Match(
		ast.IidPattern{Variable: "$" + varName, IID: iid},
	)
	compiler := &ast.Compiler{}
	result, _ := compiler.Compile(match)
	return result
}

func (s *entityStrategy) BuildMatchAll(info *ModelInfo, varName string) string {
	match := ast.Match(
		ast.Entity("$"+varName, info.TypeName),
	)
	compiler := &ast.Compiler{}
	result, _ := compiler.Compile(match)
	return result
}

func (s *entityStrategy) BuildFetchAll(info *ModelInfo, varName string) string {
	var items []ast.FetchItem
	items = append(items, ast.FetchFunc("_iid", "iid", "$"+varName))

	for _, fi := range info.Fields {
		if fi.IsSlice {
			items = append(items, ast.FetchAttributeList{
				Key:      fi.Tag.Name,
				Var:      "$" + varName,
				AttrName: fi.Tag.Name,
			})
		} else {
			items = append(items, ast.FetchAttr(fi.Tag.Name, "$"+varName, fi.Tag.Name))
		}
	}

	fetch := ast.Fetch(items...)
	compiler := &ast.Compiler{}
	result, _ := compiler.Compile(fetch)
	return result
}

func (s *entityStrategy) BuildMatchAllStrict(info *ModelInfo, varName string) string {
	match := ast.Match(
		ast.RawPattern{Content: fmt.Sprintf("$%s isa! $t", varName)},
		ast.SubTypePattern{Variable: "$t", ParentType: info.TypeName},
	)
	compiler := &ast.Compiler{}
	result, _ := compiler.Compile(match)
	return result
}

func (s *entityStrategy) BuildFetchAllWithType(info *ModelInfo, varName string) string {
	var items []ast.FetchItem
	items = append(items, ast.FetchFunc("_iid", "iid", "$"+varName))
	items = append(items, ast.FetchFunc("_type", "label", "$t"))

	for _, fi := range info.Fields {
		if fi.IsSlice {
			items = append(items, ast.FetchAttributeList{
				Key:      fi.Tag.Name,
				Var:      "$" + varName,
				AttrName: fi.Tag.Name,
			})
		} else {
			items = append(items, ast.FetchAttr(fi.Tag.Name, "$"+varName, fi.Tag.Name))
		}
	}

	fetch := ast.Fetch(items...)
	compiler := &ast.Compiler{}
	result, _ := compiler.Compile(fetch)
	return result
}

func (s *entityStrategy) BuildFetchWithRoles(info *ModelInfo, varName string) (string, string) {
	return "", s.BuildFetchAll(info, varName)
}

// --- Relation Strategy ---

type relationStrategy struct{}

func (s *relationStrategy) BuildInsertQuery(info *ModelInfo, instance any, varName string) string {
	query := s.buildInsertOrPut(info, instance, varName, "insert")

	// Append fetch clause to retrieve IID in the same query
	fetch := ast.Fetch(ast.FetchFunc("_iid", "iid", "$"+varName))
	compiler := &ast.Compiler{}
	fetchStr, _ := compiler.Compile(fetch)

	return query + "\n" + fetchStr
}

func (s *relationStrategy) BuildPutQuery(info *ModelInfo, instance any, varName string) string {
	return s.buildInsertOrPut(info, instance, varName, "put")
}

func (s *relationStrategy) buildInsertOrPut(info *ModelInfo, instance any, varName string, keyword string) string {
	v := reflectValue(instance)

	var matchPatterns []ast.Pattern
	var roleParts []string

	for _, role := range info.Roles {
		field := v.Field(role.FieldIndex)
		if field.Kind() == reflect.Ptr && field.IsNil() {
			continue
		}
		playerVal := field
		if playerVal.Kind() == reflect.Ptr {
			playerVal = playerVal.Elem()
		}

		roleVar := role.RoleName

		// Look up player model info for key matching
		playerInfo, ok := LookupType(playerVal.Type())
		if !ok {
			continue
		}

		// Prefer IID, fall back to key attributes
		playerIID := getIIDFromValue(playerVal)

		if playerIID != "" {
			matchPatterns = append(matchPatterns, ast.Entity("$"+roleVar, playerInfo.TypeName, ast.Iid(playerIID)))
		} else {
			var constraints []ast.Constraint
			for _, kf := range playerInfo.KeyFields {
				kVal := extractSingleFieldValue(playerVal, kf)
				if kVal != nil {
					constraints = append(constraints, ast.Has(kf.Tag.Name, ast.ValueFromGo(kVal)))
				}
			}
			matchPatterns = append(matchPatterns, ast.Entity("$"+roleVar, playerInfo.TypeName, constraints...))
		}

		roleParts = append(roleParts, fmt.Sprintf("%s: $%s", role.RoleName, roleVar))
	}

	// Build insert/put statement parts
	var insertParts []string
	insertParts = append(insertParts, fmt.Sprintf("$%s isa %s, links (%s)",
		varName, info.TypeName, strings.Join(roleParts, ", ")))

	for _, fi := range info.Fields {
		vals := extractFieldValues(v, fi)
		for _, val := range vals {
			insertParts = append(insertParts, fmt.Sprintf("has %s %s", fi.Tag.Name, FormatValue(val)))
		}
	}

	// Compile query
	compiler := &ast.Compiler{}
	query := ""
	if len(matchPatterns) > 0 {
		match := ast.Match(matchPatterns...)
		matchStr, _ := compiler.Compile(match)
		query += matchStr + "\n"
	}
	query += keyword + "\n" + strings.Join(insertParts, ",\n") + ";"
	return query
}

func (s *relationStrategy) BuildMatchByKey(info *ModelInfo, instance any, varName string) string {
	v := reflectValue(instance)
	iid := getIIDFromValue(v)
	var match ast.MatchClause
	if iid != "" {
		match = ast.Match(
			ast.IidPattern{Variable: "$" + varName, IID: iid},
		)
	} else {
		match = ast.Match(
			ast.Entity("$"+varName, info.TypeName),
		)
	}
	compiler := &ast.Compiler{}
	result, _ := compiler.Compile(match)
	return result
}

func (s *relationStrategy) BuildMatchByIID(iid string, varName string) string {
	match := ast.Match(
		ast.IidPattern{Variable: "$" + varName, IID: iid},
	)
	compiler := &ast.Compiler{}
	result, _ := compiler.Compile(match)
	return result
}

func (s *relationStrategy) BuildMatchAll(info *ModelInfo, varName string) string {
	match := ast.Match(
		ast.Entity("$"+varName, info.TypeName),
	)
	compiler := &ast.Compiler{}
	result, _ := compiler.Compile(match)
	return result
}

func (s *relationStrategy) BuildFetchAll(info *ModelInfo, varName string) string {
	var items []ast.FetchItem
	items = append(items, ast.FetchFunc("_iid", "iid", "$"+varName))

	for _, fi := range info.Fields {
		if fi.IsSlice {
			items = append(items, ast.FetchAttributeList{
				Key:      fi.Tag.Name,
				Var:      "$" + varName,
				AttrName: fi.Tag.Name,
			})
		} else {
			items = append(items, ast.FetchAttr(fi.Tag.Name, "$"+varName, fi.Tag.Name))
		}
	}

	fetch := ast.Fetch(items...)
	compiler := &ast.Compiler{}
	result, _ := compiler.Compile(fetch)
	return result
}

func (s *relationStrategy) BuildMatchAllStrict(info *ModelInfo, varName string) string {
	match := ast.Match(
		ast.RawPattern{Content: fmt.Sprintf("$%s isa! $t", varName)},
		ast.SubTypePattern{Variable: "$t", ParentType: info.TypeName},
	)
	compiler := &ast.Compiler{}
	result, _ := compiler.Compile(match)
	return result
}

func (s *relationStrategy) BuildFetchAllWithType(info *ModelInfo, varName string) string {
	var items []ast.FetchItem
	items = append(items, ast.FetchFunc("_iid", "iid", "$"+varName))
	items = append(items, ast.FetchFunc("_type", "label", "$t"))

	for _, fi := range info.Fields {
		if fi.IsSlice {
			items = append(items, ast.FetchAttributeList{
				Key:      fi.Tag.Name,
				Var:      "$" + varName,
				AttrName: fi.Tag.Name,
			})
		} else {
			items = append(items, ast.FetchAttr(fi.Tag.Name, "$"+varName, fi.Tag.Name))
		}
	}

	fetch := ast.Fetch(items...)
	compiler := &ast.Compiler{}
	result, _ := compiler.Compile(fetch)
	return result
}

func (s *relationStrategy) BuildFetchWithRoles(info *ModelInfo, varName string) (string, string) {
	// Build match patterns for role players using AST
	var matchPatterns []ast.Pattern
	var items []string
	items = append(items, fmt.Sprintf(`"_iid": iid($%s)`, varName))

	// Own attributes
	for _, fi := range info.Fields {
		if fi.IsSlice {
			items = append(items, fmt.Sprintf(`"%s": [$%s.%s]`, fi.Tag.Name, varName, fi.Tag.Name))
		} else {
			items = append(items, fmt.Sprintf(`"%s": $%s.%s`, fi.Tag.Name, varName, fi.Tag.Name))
		}
	}

	// Role players
	for _, role := range info.Roles {
		roleVar := role.RoleName
		// Add links pattern to match (using RawPattern for "links" syntax)
		matchPatterns = append(matchPatterns, ast.RawPattern{
			Content: fmt.Sprintf("$%s links (%s: $%s)", varName, role.RoleName, roleVar),
		})

		// Look up player model info to get its attributes
		playerInfo, ok := Lookup(role.PlayerTypeName)
		if !ok {
			// Can't resolve player type — just include IID
			items = append(items, fmt.Sprintf(`"%s": { "_iid": iid($%s) }`, role.RoleName, roleVar))
			continue
		}

		// Build a sub-fetch for the role player
		var subItems []string
		subItems = append(subItems, fmt.Sprintf(`"_iid": iid($%s)`, roleVar))
		for _, pf := range playerInfo.Fields {
			if pf.IsSlice {
				subItems = append(subItems, fmt.Sprintf(`"%s": [$%s.%s]`, pf.Tag.Name, roleVar, pf.Tag.Name))
			} else {
				subItems = append(subItems, fmt.Sprintf(`"%s": $%s.%s`, pf.Tag.Name, roleVar, pf.Tag.Name))
			}
		}
		items = append(items, fmt.Sprintf(`"%s": { %s }`, role.RoleName, strings.Join(subItems, ", ")))
	}

	matchAdditions := ""
	if len(matchPatterns) > 0 {
		// Extract pattern contents (all are RawPattern with "links" syntax)
		var parts []string
		for _, p := range matchPatterns {
			if rp, ok := p.(ast.RawPattern); ok {
				parts = append(parts, rp.Content)
			}
		}
		matchAdditions = strings.Join(parts, ";\n") + ";"
	}

	fetchClause := "fetch {\n" + strings.Join(items, ",\n") + "\n};"
	return matchAdditions, fetchClause
}

// --- Helpers ---

func reflectValue(instance any) reflect.Value {
	v := reflect.ValueOf(instance)
	for v.Kind() == reflect.Ptr {
		v = v.Elem()
	}
	return v
}

func extractFieldValues(v reflect.Value, fi FieldInfo) []any {
	field := v.Field(fi.FieldIndex)
	if fi.IsPointer && field.IsNil() {
		return nil
	}

	if fi.IsSlice {
		if field.Len() == 0 {
			return nil
		}
		vals := make([]any, field.Len())
		for i := 0; i < field.Len(); i++ {
			vals[i] = field.Index(i).Interface()
		}
		return vals
	}

	val := field.Interface()
	if fi.IsPointer {
		val = field.Elem().Interface()
	}
	return []any{val}
}

func extractSingleFieldValue(v reflect.Value, fi FieldInfo) any {
	field := v.Field(fi.FieldIndex)
	if fi.IsPointer && field.IsNil() {
		return nil
	}
	val := field.Interface()
	if fi.IsPointer {
		val = field.Elem().Interface()
	}
	return val
}

func getIIDFromValue(v reflect.Value) string {
	if v.Kind() == reflect.Ptr {
		v = v.Elem()
	}
	for _, fv := range v.Fields() {
		if !fv.CanAddr() {
			continue
		}
		if e, ok := fv.Addr().Interface().(*BaseEntity); ok {
			return e.GetIID()
		}
		if r, ok := fv.Addr().Interface().(*BaseRelation); ok {
			return r.GetIID()
		}
	}
	return ""
}
