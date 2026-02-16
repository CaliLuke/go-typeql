package tqlgen

import (
	"fmt"
	"io"
	"sort"
	"strings"
	"text/template"
)

// RegistryConfig specifies settings for generating a schema registry.
type RegistryConfig struct {
	// PackageName is the Go package name for the generated code.
	PackageName string
	// UseAcronyms applies Go acronym naming conventions (e.g., "ID" not "Id").
	UseAcronyms bool
	// SkipAbstract excludes abstract types from entity/relation constants and EntityAttributes.
	SkipAbstract bool
	// Enums generates string constants from @values constraints.
	Enums bool
	// TypePrefix is the prefix for entity type constants (default "Type").
	TypePrefix string
	// RelPrefix is the prefix for relation type constants (default "Rel").
	RelPrefix string
}

// RegistryData holds all schema-derived data for registry code generation.
type RegistryData struct {
	PackageName       string
	EntityConstants   []TypeConstCtx
	RelationConstants []TypeConstCtx
	Enums             []EnumCtx
	EntityParents     []KVCtx
	EntityAttributes  []KVSliceCtx
	AttrValueTypes    []KVCtx
	AttrEnumValues    []KVSliceCtx
	RelationSchema    []RelSchemaCtx
	RelationAttrs     []KVSliceCtx
	AllEntityTypes    []string
	AllRelationTypes  []string
}

// TypeConstCtx holds a Go constant name and its string value.
type TypeConstCtx struct {
	Name  string // e.g. "TypePersona"
	Value string // e.g. "persona"
}

// EnumCtx holds enum constants derived from @values constraints.
type EnumCtx = enumCtx

// EnumValueCtx holds a single enum constant.
type EnumValueCtx = enumValueCtx

// KVCtx is a simple key-value pair.
type KVCtx struct {
	Key, Value string
}

// KVSliceCtx is a key with multiple string values.
type KVSliceCtx struct {
	Key    string
	Values []string
}

// RelSchemaCtx describes a relation's role schema.
type RelSchemaCtx struct {
	Name       string
	Role0Name  string
	Role0Types []string
	Role1Name  string
	Role1Types []string
}

// BuildRegistryData populates a RegistryData from a parsed schema.
// The schema should have AccumulateInheritance() called before this.
func BuildRegistryData(schema *ParsedSchema, cfg RegistryConfig) *RegistryData {
	if cfg.PackageName == "" {
		cfg.PackageName = "graph"
	}
	if cfg.TypePrefix == "" {
		cfg.TypePrefix = "Type"
	}
	if cfg.RelPrefix == "" {
		cfg.RelPrefix = "Rel"
	}

	data := &RegistryData{PackageName: cfg.PackageName}

	// Index attributes
	attrIndex := make(map[string]AttributeSpec, len(schema.Attributes))
	for _, a := range schema.Attributes {
		attrIndex[a.Name] = a
	}

	// Index entities
	entityIndex := make(map[string]EntitySpec, len(schema.Entities))
	for _, e := range schema.Entities {
		entityIndex[e.Name] = e
	}

	// --- Entities ---
	allEntities := make([]string, 0, len(schema.Entities))
	for _, e := range schema.Entities {
		allEntities = append(allEntities, e.Name)
	}
	sort.Strings(allEntities)
	data.AllEntityTypes = allEntities

	for _, name := range allEntities {
		e := entityIndex[name]
		if cfg.SkipAbstract && e.Abstract {
			continue
		}
		data.EntityConstants = append(data.EntityConstants, TypeConstCtx{
			Name:  toRegistryConst(cfg.TypePrefix, name, cfg.UseAcronyms),
			Value: name,
		})
	}

	// EntityParents
	for _, name := range allEntities {
		e := entityIndex[name]
		if e.Parent != "" {
			data.EntityParents = append(data.EntityParents, KVCtx{name, e.Parent})
		}
	}

	// EntityAttributes (skip abstract)
	for _, name := range allEntities {
		e := entityIndex[name]
		if cfg.SkipAbstract && e.Abstract {
			continue
		}
		attrs := make([]string, 0, len(e.Owns))
		for _, o := range e.Owns {
			attrs = append(attrs, o.Attribute)
		}
		sort.Strings(attrs)
		data.EntityAttributes = append(data.EntityAttributes, KVSliceCtx{name, attrs})
	}

	// --- Attributes ---
	allAttrNames := make([]string, 0, len(schema.Attributes))
	for _, a := range schema.Attributes {
		allAttrNames = append(allAttrNames, a.Name)
	}
	sort.Strings(allAttrNames)

	for _, name := range allAttrNames {
		a := attrIndex[name]
		data.AttrValueTypes = append(data.AttrValueTypes, KVCtx{name, a.ValueType})
		if len(a.Values) > 0 {
			data.AttrEnumValues = append(data.AttrEnumValues, KVSliceCtx{name, a.Values})
		}
	}

	// --- Enums ---
	if cfg.Enums {
		for _, a := range schema.Attributes {
			if len(a.Values) > 0 {
				data.Enums = append(data.Enums, buildEnumCtx(a, RenderConfig{UseAcronyms: cfg.UseAcronyms}))
			}
		}
	}

	// --- Relations ---
	relIndex := make(map[string]RelationSpec, len(schema.Relations))
	allRelations := make([]string, 0, len(schema.Relations))
	for _, r := range schema.Relations {
		relIndex[r.Name] = r
		allRelations = append(allRelations, r.Name)
	}
	sort.Strings(allRelations)
	data.AllRelationTypes = allRelations

	for _, name := range allRelations {
		data.RelationConstants = append(data.RelationConstants, TypeConstCtx{
			Name:  toRegistryConst(cfg.RelPrefix, name, cfg.UseAcronyms),
			Value: name,
		})
	}

	// Role → player types from entity plays clauses
	rolePlayers := make(map[string][]string)
	for _, e := range schema.Entities {
		for _, p := range e.Plays {
			key := p.Relation + ":" + p.Role
			rolePlayers[key] = append(rolePlayers[key], e.Name)
		}
	}

	// RelationSchema
	for _, name := range allRelations {
		r := relIndex[name]
		if len(r.Relates) < 2 {
			continue
		}
		role0 := r.Relates[0]
		role1 := r.Relates[1]

		players0 := rolePlayers[name+":"+role0.Role]
		players1 := rolePlayers[name+":"+role1.Role]
		sort.Strings(players0)
		sort.Strings(players1)

		players0 = filterMostSpecific(players0, entityIndex)
		players1 = filterMostSpecific(players1, entityIndex)

		data.RelationSchema = append(data.RelationSchema, RelSchemaCtx{
			Name:       name,
			Role0Name:  role0.Role,
			Role0Types: players0,
			Role1Name:  role1.Role,
			Role1Types: players1,
		})
	}

	// RelationAttributes
	for _, name := range allRelations {
		r := relIndex[name]
		if len(r.Owns) == 0 {
			continue
		}
		attrs := make([]string, 0, len(r.Owns))
		for _, o := range r.Owns {
			attrs = append(attrs, o.Attribute)
		}
		sort.Strings(attrs)
		data.RelationAttrs = append(data.RelationAttrs, KVSliceCtx{name, attrs})
	}

	return data
}

// filterMostSpecific deduplicates player types.
func filterMostSpecific(types []string, entities map[string]EntitySpec) []string {
	if len(types) <= 1 {
		return types
	}
	seen := make(map[string]bool, len(types))
	var result []string
	for _, t := range types {
		if !seen[t] {
			seen[t] = true
			result = append(result, t)
		}
	}
	return result
}

// toRegistryConst converts a TypeDB name to a Go constant with a prefix.
// e.g. toRegistryConst("Type", "user_story", true) → "TypeUserStory"
func toRegistryConst(prefix, name string, useAcronyms bool) string {
	var b strings.Builder
	b.WriteString(prefix)
	parts := strings.Split(name, "_")
	for _, p := range parts {
		if useAcronyms {
			lower := strings.ToLower(p)
			if acronym, ok := CommonAcronyms[lower]; ok {
				b.WriteString(acronym)
				continue
			}
		}
		if len(p) > 0 {
			b.WriteString(strings.ToUpper(p[:1]))
			b.WriteString(p[1:])
		}
	}
	return b.String()
}

// RenderRegistry writes a complete schema registry Go file from RegistryData.
func RenderRegistry(w io.Writer, data *RegistryData) error {
	return registryTemplate.Execute(w, data)
}

func goStrSlice(vals []string) string {
	quoted := make([]string, len(vals))
	for i, v := range vals {
		quoted[i] = fmt.Sprintf("%q", v)
	}
	return "{" + strings.Join(quoted, ", ") + "}"
}

var registryFuncMap = template.FuncMap{
	"goStrSlice": goStrSlice,
	"title":      ToPascalCaseAcronyms,
}

var registryTemplate = template.Must(template.New("registry").Funcs(registryFuncMap).Parse(`// Code generated by tqlgen; DO NOT EDIT.

package {{.PackageName}}

// --- Type constants ---

{{range .EntityConstants -}}
const {{.Name}} = "{{.Value}}"
{{end}}
{{range .RelationConstants -}}
const {{.Name}} = "{{.Value}}"
{{end}}
{{- if .Enums}}
// --- Enum constants (from @values constraints) ---
{{range .Enums}}
const (
{{- range .Values}}
	{{.GoName}} = "{{.Value}}"
{{- end}}
)
{{end}}
{{- end}}
// --- Entity Parents ---

// EntityParents maps subtypes to their parent type.
var EntityParents = map[string]string{
{{- range .EntityParents}}
	"{{.Key}}": "{{.Value}}",
{{- end}}
}

// --- Entity Attributes (with inheritance) ---

// EntityAttributes maps entity type → sorted owned attributes.
var EntityAttributes = map[string][]string{
{{- range .EntityAttributes}}
	"{{.Key}}": {{goStrSlice .Values}},
{{- end}}
}

// --- Attribute Value Types ---

// AttributeValueTypes maps attribute name → TypeDB value type.
var AttributeValueTypes = map[string]string{
{{- range .AttrValueTypes}}
	"{{.Key}}": "{{.Value}}",
{{- end}}
}

// --- Attribute Enum Values ---

// AttributeEnumValues maps attribute name → allowed enum values.
var AttributeEnumValues = map[string][]string{
{{- range .AttrEnumValues}}
	"{{.Key}}": {{goStrSlice .Values}},
{{- end}}
}

// --- Relation Schema ---

// RoleInfo describes a role in a relation: its name and which entity types can fill it.
type RoleInfo struct {
	RoleName    string
	PlayerTypes []string
}

// RelationSchema maps relation type → [2]RoleInfo (index 0 = source, index 1 = target).
var RelationSchema = map[string][2]RoleInfo{
{{- range .RelationSchema}}
	"{{.Name}}": {{"{"}}{"{{.Role0Name}}", []string{{goStrSlice .Role0Types}}}, {"{{.Role1Name}}", []string{{goStrSlice .Role1Types}}}{{"}"}},
{{- end}}
}

// --- Relation Attributes ---

// RelationAttributes lists owned attributes per relation type.
var RelationAttributes = map[string][]string{
{{- range .RelationAttrs}}
	"{{.Key}}": {{goStrSlice .Values}},
{{- end}}
}

// --- Sorted type lists ---

// AllEntityTypes is a sorted list of all entity types.
var AllEntityTypes = []string{{goStrSlice .AllEntityTypes}}

// AllRelationTypes is a sorted list of all relation types.
var AllRelationTypes = []string{{goStrSlice .AllRelationTypes}}
`))
