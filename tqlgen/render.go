// Package tqlgen provides code generation from TypeQL schemas.
package tqlgen

import (
	"fmt"
	"io"
	"strings"
	"text/template"
)

// RenderConfig specifies the settings for generating Go code from a TypeQL schema.
type RenderConfig struct {
	// PackageName is the name of the Go package for the generated code.
	PackageName string
	// ModulePath is the module import path for the 'gotype' package.
	ModulePath string
	// UseAcronyms, if true, applies Go acronym naming conventions (e.g., 'ID' instead of 'Id').
	UseAcronyms bool
	// SkipAbstract, if true, excludes abstract TypeDB types from the generated Go code.
	SkipAbstract bool
	// SchemaVersion is an optional string included in the generated file header.
	SchemaVersion string
	// Enums, if true, generates string constants from @values constraints on attributes.
	Enums bool
}

// DefaultConfig returns a standard RenderConfig with sensible defaults.
func DefaultConfig() RenderConfig {
	return RenderConfig{
		PackageName:  "models",
		ModulePath:   "github.com/CaliLuke/go-typeql/gotype",
		UseAcronyms:  true,
		SkipAbstract: true,
		Enums:        true,
	}
}

// Render processes a ParsedSchema and writes the generated Go source code to the provided writer.
func Render(w io.Writer, schema *ParsedSchema, cfg RenderConfig) error {
	if cfg.PackageName == "" {
		cfg.PackageName = "models"
	}
	if cfg.ModulePath == "" {
		cfg.ModulePath = "github.com/CaliLuke/go-typeql/gotype"
	}

	// Build attribute type lookup
	attrTypes := make(map[string]string) // attr name -> value type
	for _, a := range schema.Attributes {
		attrTypes[a.Name] = a.ValueType
	}

	// Build template context
	data := &renderData{
		PackageName: cfg.PackageName,
		ModulePath:  cfg.ModulePath,
		NeedsTime:   needsTimeImport(schema, attrTypes),
	}

	if cfg.Enums {
		for _, a := range schema.Attributes {
			if len(a.Values) > 0 {
				data.Enums = append(data.Enums, buildEnumCtx(a, cfg))
			}
		}
	}

	for _, e := range schema.Entities {
		if cfg.SkipAbstract && e.Abstract {
			continue
		}
		data.Entities = append(data.Entities, buildEntityCtx(e, attrTypes, cfg))
	}

	for _, r := range schema.Relations {
		if cfg.SkipAbstract && r.Abstract {
			continue
		}
		data.Relations = append(data.Relations, buildRelationCtx(r, schema, attrTypes, cfg))
	}

	return renderTemplate.Execute(w, data)
}

// --- Template context types ---

type renderData struct {
	PackageName string
	ModulePath  string
	NeedsTime   bool
	Enums       []enumCtx
	Entities    []entityCtx
	Relations   []relationCtx
}

type enumCtx struct {
	AttrName string // TypeDB attribute name
	GoPrefix string // PascalCase prefix
	Values   []enumValueCtx
}

type enumValueCtx struct {
	GoName string // e.g. "StatusProposed"
	Value  string // e.g. "proposed"
}

type entityCtx struct {
	GoName   string
	TypeName string // TypeDB name
	Abstract bool
	Comment  string
	Fields   []fieldCtx
}

type relationCtx struct {
	GoName   string
	TypeName string
	Abstract bool
	Comment  string
	Roles    []roleCtx
	Fields   []fieldCtx
}

type fieldCtx struct {
	GoName  string
	GoType  string
	Tag     string
	Comment string
}

type roleCtx struct {
	GoName     string
	GoType     string
	Tag        string
	PlayerType string // Go type of the role player
}

// --- Context builders ---

func buildEnumCtx(a AttributeSpec, cfg RenderConfig) enumCtx {
	prefix := goTypeName(a.Name, cfg)
	ctx := enumCtx{
		AttrName: a.Name,
		GoPrefix: prefix,
	}
	for _, v := range a.Values {
		ctx.Values = append(ctx.Values, enumValueCtx{
			GoName: prefix + goTypeName(v, cfg),
			Value:  v,
		})
	}
	return ctx
}

func buildEntityCtx(e EntitySpec, attrTypes map[string]string, cfg RenderConfig) entityCtx {
	ctx := entityCtx{
		GoName:   goTypeName(e.Name, cfg),
		TypeName: e.Name,
		Abstract: e.Abstract,
	}
	if e.Parent != "" {
		ctx.Comment = fmt.Sprintf("inherits from %s", e.Parent)
	}

	for _, o := range e.Owns {
		ctx.Fields = append(ctx.Fields, buildFieldCtx(o, attrTypes, cfg))
	}

	return ctx
}

func buildRelationCtx(r RelationSpec, schema *ParsedSchema, attrTypes map[string]string, cfg RenderConfig) relationCtx {
	ctx := relationCtx{
		GoName:   goTypeName(r.Name, cfg),
		TypeName: r.Name,
		Abstract: r.Abstract,
	}
	if r.Parent != "" {
		ctx.Comment = fmt.Sprintf("inherits from %s", r.Parent)
	}

	// Build role player type lookup from relation's relates + entity plays
	for _, rel := range r.Relates {
		role := roleCtx{
			GoName: goFieldName(rel.Role, cfg),
			Tag:    fmt.Sprintf("`typedb:\"role:%s\"`", rel.Role),
		}

		// Find which entity plays this role
		playerType := findRolePlayer(r.Name, rel.Role, schema)
		if playerType != "" {
			role.PlayerType = goTypeName(playerType, cfg)
			role.GoType = "*" + role.PlayerType
		} else {
			// Fallback: use role name as type hint
			role.PlayerType = goTypeName(rel.Role, cfg)
			role.GoType = "*" + role.PlayerType
		}

		ctx.Roles = append(ctx.Roles, role)
	}

	for _, o := range r.Owns {
		ctx.Fields = append(ctx.Fields, buildFieldCtx(o, attrTypes, cfg))
	}

	return ctx
}

func buildFieldCtx(o OwnsSpec, attrTypes map[string]string, cfg RenderConfig) fieldCtx {
	f := fieldCtx{
		GoName: goFieldName(o.Attribute, cfg),
	}

	// Determine Go type from TypeDB value type
	vtype := attrTypes[o.Attribute]
	goType := typeDBToGo(vtype)

	// Build tag parts
	var tagParts []string
	tagParts = append(tagParts, o.Attribute)
	if o.Key {
		tagParts = append(tagParts, "key")
	}
	if o.Unique {
		tagParts = append(tagParts, "unique")
	}
	if o.Card != "" {
		tagParts = append(tagParts, "card="+o.Card)
	}

	f.Tag = fmt.Sprintf("`typedb:\"%s\"`", strings.Join(tagParts, ","))

	// Make optional fields pointer types
	if isOptional(o) {
		f.GoType = "*" + goType
	} else {
		f.GoType = goType
	}

	return f
}

// findRolePlayer searches entities for one that plays the given relation:role.
func findRolePlayer(relName, roleName string, schema *ParsedSchema) string {
	for _, e := range schema.Entities {
		for _, p := range e.Plays {
			if p.Relation == relName && p.Role == roleName {
				return e.Name
			}
		}
	}
	return ""
}

// isOptional returns true if the owns clause indicates an optional field.
func isOptional(o OwnsSpec) bool {
	if o.Key {
		return false
	}
	// @card(0..1) or @card(0..) → optional
	if strings.HasPrefix(o.Card, "0") {
		return true
	}
	// No cardinality specified and not key → optional by default
	if o.Card == "" && !o.Key {
		return true
	}
	return false
}

func goTypeName(name string, cfg RenderConfig) string {
	if cfg.UseAcronyms {
		return ToPascalCaseAcronyms(name)
	}
	return ToPascalCase(name)
}

func goFieldName(name string, cfg RenderConfig) string {
	return goTypeName(name, cfg)
}

func typeDBToGo(vtype string) string {
	switch vtype {
	case "string":
		return "string"
	case "integer", "long":
		return "int64"
	case "double":
		return "float64"
	case "boolean":
		return "bool"
	case "datetime":
		return "time.Time"
	default:
		return "string"
	}
}

func needsTimeImport(schema *ParsedSchema, attrTypes map[string]string) bool {
	// Check if any owned attribute uses datetime
	check := func(owns []OwnsSpec) bool {
		for _, o := range owns {
			if attrTypes[o.Attribute] == "datetime" {
				return true
			}
		}
		return false
	}
	for _, e := range schema.Entities {
		if check(e.Owns) {
			return true
		}
	}
	for _, r := range schema.Relations {
		if check(r.Owns) {
			return true
		}
	}
	return false
}

// --- Go template ---

var renderTemplate = template.Must(template.New("models").Parse(`// Code generated by tqlgen. DO NOT EDIT.

package {{.PackageName}}

import (
	"github.com/CaliLuke/go-typeql/gotype"
{{- if .NeedsTime}}
	"time"
{{- end}}
)
{{- if .Enums}}

// --- Enum constants (from @values constraints) ---
{{range .Enums}}
// {{.GoPrefix}} values for the "{{.AttrName}}" attribute.
const (
{{- range .Values}}
	{{.GoName}} = "{{.Value}}"
{{- end}}
)
{{end}}
{{- end}}
{{range .Entities}}
{{- if .Comment}}
// {{.GoName}} — {{.Comment}}
{{- end}}
type {{.GoName}} struct {
	gotype.BaseEntity
{{- range .Fields}}
	{{.GoName}} {{.GoType}} {{.Tag}}
{{- end}}
}
{{end}}
{{- range .Relations}}
{{- if .Comment}}
// {{.GoName}} — {{.Comment}}
{{- end}}
type {{.GoName}} struct {
	gotype.BaseRelation
{{- range .Roles}}
	{{.GoName}} {{.GoType}} {{.Tag}}
{{- end}}
{{- range .Fields}}
	{{.GoName}} {{.GoType}} {{.Tag}}
{{- end}}
}
{{end}}`))
