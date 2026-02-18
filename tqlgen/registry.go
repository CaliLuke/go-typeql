package tqlgen

import (
	"crypto/sha256"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"text/template"
)

// RegistryConfig specifies settings for generating a schema registry.
type RegistryConfig struct {
	// PackageName is the Go package name for the generated code (required).
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
	// SchemaText is the raw schema source. If non-empty, a SHA256-based SchemaHash is computed
	// and annotations are extracted from comments.
	SchemaText string
	// SchemaVersion is a user-provided version string emitted as a constant.
	SchemaVersion string
	// TypedConstants generates typed string constants (type EntityType string, etc.)
	// for compile-time safety instead of plain string constants.
	TypedConstants bool
	// JSONSchema generates JSON schema fragment maps for each entity/relation type.
	JSONSchema bool
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
	// Metadata fields
	EntityKeys       []KVSliceCtx
	EntityAbstract   []string
	RelationAbstract []string
	RelationParents  []KVCtx
	SchemaHash       string
	SchemaVersion    string
	AttributeTypes   []string

	// Annotations from schema comments (# @key value)
	EntityAnnotations    []KVMapCtx
	AttributeAnnotations []KVMapCtx
	RelationAnnotations  []KVMapCtx

	// Typed constants (StrEnum-style)
	TypedConstants         bool
	AttributeTypeConstants []TypeConstCtx

	// JSON schema fragments
	JSONSchema       bool
	EntityJSONSchema []JSONSchemaCtx
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

// RoleCtx describes a single role in a relation: its name and which entity types can fill it.
type RoleCtx struct {
	RoleName    string
	PlayerTypes []string
	Card        string // e.g. "1", "0..", "1.."
}

// RelSchemaCtx describes a relation's role schema with N roles.
type RelSchemaCtx struct {
	Name  string
	Roles []RoleCtx
}

// KVMapCtx is a key with a map of string key-value pairs (for annotations).
type KVMapCtx struct {
	Key    string
	Values []KVCtx
}

// JSONSchemaCtx holds a JSON schema fragment for a single type.
type JSONSchemaCtx struct {
	TypeName   string
	Properties []JSONSchemaPropCtx
	Required   []string
}

// JSONSchemaPropCtx describes a single property in a JSON schema fragment.
type JSONSchemaPropCtx struct {
	Name     string
	JSONType string
}

// BuildRegistryData populates a RegistryData from a parsed schema.
// The schema should have AccumulateInheritance() called before this.
func BuildRegistryData(schema *ParsedSchema, cfg RegistryConfig) *RegistryData {
	if cfg.PackageName == "" {
		return &RegistryData{} // PackageName required
	}
	if cfg.TypePrefix == "" {
		cfg.TypePrefix = "Type"
	}
	if cfg.RelPrefix == "" {
		cfg.RelPrefix = "Rel"
	}

	data := &RegistryData{
		PackageName:    cfg.PackageName,
		TypedConstants: cfg.TypedConstants,
		JSONSchema:     cfg.JSONSchema,
	}

	// Schema version
	if cfg.SchemaVersion != "" {
		data.SchemaVersion = cfg.SchemaVersion
	}

	// Schema hash
	if cfg.SchemaText != "" {
		h := sha256.Sum256([]byte(cfg.SchemaText))
		data.SchemaHash = fmt.Sprintf("sha256:%x", h[:8])
	}

	// Annotations from schema comments
	if cfg.SchemaText != "" {
		annotations := ExtractAnnotations(cfg.SchemaText)
		data.EntityAnnotations, data.AttributeAnnotations, data.RelationAnnotations =
			buildAnnotationCtx(annotations, schema)
	}

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

	// --- Attributes ---
	allAttrNames := make([]string, 0, len(schema.Attributes))
	for _, a := range schema.Attributes {
		allAttrNames = append(allAttrNames, a.Name)
	}
	sort.Strings(allAttrNames)
	data.AttributeTypes = allAttrNames

	if cfg.TypedConstants {
		for _, name := range allAttrNames {
			data.AttributeTypeConstants = append(data.AttributeTypeConstants, TypeConstCtx{
				Name:  toRegistryConst("Attr", name, cfg.UseAcronyms),
				Value: name,
			})
		}
	}

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

	// EntityAbstract
	for _, name := range allEntities {
		e := entityIndex[name]
		if e.Abstract {
			data.EntityAbstract = append(data.EntityAbstract, name)
		}
	}

	// EntityKeys
	for _, name := range allEntities {
		e := entityIndex[name]
		if cfg.SkipAbstract && e.Abstract {
			continue
		}
		var keys []string
		for _, o := range e.Owns {
			if o.Key {
				keys = append(keys, o.Attribute)
			}
		}
		if len(keys) > 0 {
			sort.Strings(keys)
			data.EntityKeys = append(data.EntityKeys, KVSliceCtx{name, keys})
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
		r := relIndex[name]
		if cfg.SkipAbstract && r.Abstract {
			continue
		}
		data.RelationConstants = append(data.RelationConstants, TypeConstCtx{
			Name:  toRegistryConst(cfg.RelPrefix, name, cfg.UseAcronyms),
			Value: name,
		})
	}

	// RelationAbstract
	for _, name := range allRelations {
		r := relIndex[name]
		if r.Abstract {
			data.RelationAbstract = append(data.RelationAbstract, name)
		}
	}

	// RelationParents
	for _, name := range allRelations {
		r := relIndex[name]
		if r.Parent != "" {
			data.RelationParents = append(data.RelationParents, KVCtx{name, r.Parent})
		}
	}

	// Role → player types from entity plays clauses
	rolePlayers := make(map[string][]string)
	for _, e := range schema.Entities {
		for _, p := range e.Plays {
			key := p.Relation + ":" + p.Role
			rolePlayers[key] = append(rolePlayers[key], e.Name)
		}
	}

	// RelationSchema — supports N roles
	for _, name := range allRelations {
		r := relIndex[name]
		if len(r.Relates) == 0 {
			continue
		}
		var roles []RoleCtx
		for _, rel := range r.Relates {
			players := rolePlayers[name+":"+rel.Role]
			sort.Strings(players)
			players = filterMostSpecific(players, entityIndex)
			roles = append(roles, RoleCtx{
				RoleName:    rel.Role,
				PlayerTypes: players,
				Card:        rel.Card,
			})
		}
		data.RelationSchema = append(data.RelationSchema, RelSchemaCtx{
			Name:  name,
			Roles: roles,
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

	// JSON schema fragments
	if cfg.JSONSchema {
		for _, name := range allEntities {
			e := entityIndex[name]
			if cfg.SkipAbstract && e.Abstract {
				continue
			}
			var props []JSONSchemaPropCtx
			var required []string
			for _, o := range e.Owns {
				attr, ok := attrIndex[o.Attribute]
				if !ok {
					continue
				}
				jt := typeDBToJSONSchemaType(attr.ValueType)
				props = append(props, JSONSchemaPropCtx{Name: o.Attribute, JSONType: jt})
				if o.Key || o.Unique {
					required = append(required, o.Attribute)
				}
			}
			sort.Slice(props, func(i, j int) bool { return props[i].Name < props[j].Name })
			sort.Strings(required)
			data.EntityJSONSchema = append(data.EntityJSONSchema, JSONSchemaCtx{
				TypeName:   name,
				Properties: props,
				Required:   required,
			})
		}
	}

	return data
}

// filterMostSpecific removes ancestor types when a descendant is also present.
func filterMostSpecific(types []string, entities map[string]EntitySpec) []string {
	if len(types) <= 1 {
		return types
	}
	// Build set of all types
	typeSet := make(map[string]bool, len(types))
	for _, t := range types {
		typeSet[t] = true
	}
	// Collect all ancestors of each type
	ancestors := make(map[string]bool)
	for _, t := range types {
		e, ok := entities[t]
		if !ok {
			continue
		}
		parent := e.Parent
		for parent != "" {
			if typeSet[parent] {
				ancestors[parent] = true
			}
			pe, ok := entities[parent]
			if !ok {
				break
			}
			parent = pe.Parent
		}
	}
	// Filter out ancestors
	var result []string
	for _, t := range types {
		if !ancestors[t] {
			result = append(result, t)
		}
	}
	return result
}

// toRegistryConst converts a TypeDB name to a Go constant with a prefix.
// Splits on both hyphens and underscores.
// e.g. toRegistryConst("Type", "user-story", true) → "TypeUserStory"
func toRegistryConst(prefix, name string, useAcronyms bool) string {
	var b strings.Builder
	b.WriteString(prefix)
	parts := splitName(name)
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

// buildAnnotationCtx converts ExtractAnnotations output into typed context slices.
func buildAnnotationCtx(annotations map[string]map[string]string, schema *ParsedSchema) (entities, attributes, relations []KVMapCtx) {
	entitySet := make(map[string]bool)
	for _, e := range schema.Entities {
		entitySet[e.Name] = true
	}
	attrSet := make(map[string]bool)
	for _, a := range schema.Attributes {
		attrSet[a.Name] = true
	}
	relSet := make(map[string]bool)
	for _, r := range schema.Relations {
		relSet[r.Name] = true
	}

	// Sort annotation keys for deterministic output
	sortedNames := make([]string, 0, len(annotations))
	for name := range annotations {
		sortedNames = append(sortedNames, name)
	}
	sort.Strings(sortedNames)

	for _, name := range sortedNames {
		annots := annotations[name]
		var kvs []KVCtx
		sortedKeys := make([]string, 0, len(annots))
		for k := range annots {
			sortedKeys = append(sortedKeys, k)
		}
		sort.Strings(sortedKeys)
		for _, k := range sortedKeys {
			kvs = append(kvs, KVCtx{k, annots[k]})
		}
		entry := KVMapCtx{Key: name, Values: kvs}
		if entitySet[name] {
			entities = append(entities, entry)
		} else if attrSet[name] {
			attributes = append(attributes, entry)
		} else if relSet[name] {
			relations = append(relations, entry)
		}
	}
	return
}

// typeDBToJSONSchemaType maps TypeDB value types to JSON Schema types.
func typeDBToJSONSchemaType(vtype string) string {
	switch vtype {
	case "string":
		return "string"
	case "long", "integer":
		return "integer"
	case "double":
		return "number"
	case "boolean":
		return "boolean"
	case "datetime":
		return "string" // ISO 8601
	default:
		return "string"
	}
}

// RenderRegistry writes a complete schema registry Go file from RegistryData.
func RenderRegistry(w io.Writer, data *RegistryData) error {
	return registryTemplate.Execute(w, data)
}

// LeafConstantsConfig configures leaf constants package generation.
type LeafConstantsConfig struct {
	// PackageName for the generated file (e.g. "schema").
	PackageName string
	// UseAcronyms applies Go acronym naming conventions.
	UseAcronyms bool
	// SkipAbstract excludes abstract types.
	SkipAbstract bool
}

// RenderLeafConstants writes a standalone leaf package containing only
// type, relation, and enum constants. This package has zero internal
// dependencies, making it safe to import from any package.
func RenderLeafConstants(w io.Writer, schema *ParsedSchema, cfg LeafConstantsConfig) error {
	// Build constants from the same logic as BuildRegistryData
	regData := BuildRegistryData(schema, RegistryConfig{
		PackageName:  cfg.PackageName,
		UseAcronyms:  cfg.UseAcronyms,
		SkipAbstract: cfg.SkipAbstract,
		Enums:        true, // always include enums in leaf package
	})
	return leafTemplate.Execute(w, regData)
}

var leafTemplate = template.Must(template.New("leaf").Funcs(registryFuncMap).Parse(`// Code generated by tqlgen; DO NOT EDIT.

// Package {{.PackageName}} provides type, relation, and enum constants derived from schema.tql.
// This is a leaf package with zero internal dependencies, safe to import from
// any package without creating import cycles.
package {{.PackageName}}

// --- Entity type constants ---

const (
{{- range .EntityConstants}}
	{{.Name}} = "{{.Value}}"
{{- end}}
)

// --- Relation type constants ---

const (
{{- range .RelationConstants}}
	{{.Name}} = "{{.Value}}"
{{- end}}
)
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
{{- if .AttributeTypes}}

// --- Attribute type constants ---

// AllAttributeTypes is a sorted list of all attribute type names.
var AllAttributeTypes = []string{{goStrSlice .AttributeTypes}}
{{- end}}
`))

func goStrSlice(vals []string) string {
	quoted := make([]string, len(vals))
	for i, v := range vals {
		quoted[i] = fmt.Sprintf("%q", v)
	}
	return "{" + strings.Join(quoted, ", ") + "}"
}

func goKVMapSlice(kvs []KVCtx) string {
	parts := make([]string, len(kvs))
	for i, kv := range kvs {
		parts[i] = fmt.Sprintf("%q: %q", kv.Key, kv.Value)
	}
	return "{" + strings.Join(parts, ", ") + "}"
}

// cardMin parses a cardinality string and returns the minimum cardinality as an int.
// Examples: "1" → 1, "1.." → 1, "0..1" → 0, "0.." → 0, "" → 0.
func cardMin(card string) int {
	if card == "" {
		return 0
	}
	parts := strings.SplitN(card, "..", 2)
	n, err := strconv.Atoi(parts[0])
	if err != nil {
		return 0
	}
	return n
}

var registryFuncMap = template.FuncMap{
	"goStrSlice":   goStrSlice,
	"goKVMapSlice": goKVMapSlice,
	"title":        ToPascalCaseAcronyms,
	"cardMin":      cardMin,
}

var registryTemplate = template.Must(template.New("registry").Funcs(registryFuncMap).Parse(`// Code generated by tqlgen; DO NOT EDIT.

package {{.PackageName}}
{{- if or .SchemaVersion .SchemaHash}}

// --- Schema metadata ---
{{- if .SchemaVersion}}

// SchemaVersion is the user-provided schema version string.
const SchemaVersion = "{{.SchemaVersion}}"
{{- end}}
{{- if .SchemaHash}}

// SchemaHash is a fingerprint of the schema source used to generate this file.
const SchemaHash = "{{.SchemaHash}}"
{{- end}}
{{- end}}

// --- Type constants ---
{{if .TypedConstants}}
// EntityType is a typed string for entity type names.
type EntityType string

// RelationType is a typed string for relation type names.
type RelationType string

// AttributeType is a typed string for attribute type names.
type AttributeType string

const (
{{- range .EntityConstants}}
	{{.Name}} EntityType = "{{.Value}}"
{{- end}}
)

const (
{{- range .RelationConstants}}
	{{.Name}} RelationType = "{{.Value}}"
{{- end}}
)
{{- if .AttributeTypes}}

const (
{{- range .AttributeTypeConstants}}
	{{.Name}} AttributeType = "{{.Value}}"
{{- end}}
)
{{- end}}
{{- else}}
{{range .EntityConstants -}}
const {{.Name}} = "{{.Value}}"
{{end}}
{{range .RelationConstants -}}
const {{.Name}} = "{{.Value}}"
{{end}}
{{- end}}
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

// --- Entity Keys ---

// EntityKeys maps entity type → sorted key attributes.
var EntityKeys = map[string][]string{
{{- range .EntityKeys}}
	"{{.Key}}": {{goStrSlice .Values}},
{{- end}}
}

// --- Abstract types ---

// EntityAbstract tracks which entity types are abstract.
var EntityAbstract = map[string]bool{
{{- range .EntityAbstract}}
	"{{.}}": true,
{{- end}}
}

// RelationAbstract tracks which relation types are abstract.
var RelationAbstract = map[string]bool{
{{- range .RelationAbstract}}
	"{{.}}": true,
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
	MinCard     int // minimum cardinality (0 or 1+); from @card annotation
}

// RelationSchema maps relation type → slice of RoleInfo (one per role).
var RelationSchema = map[string][]RoleInfo{
{{- range .RelationSchema}}
	"{{.Name}}": {
	{{- range .Roles}}
		{"{{.RoleName}}", []string{{goStrSlice .PlayerTypes}}, {{cardMin .Card}}},
	{{- end}}
	},
{{- end}}
}

// --- Relation Parents ---

// RelationParents maps relation subtypes to their parent type.
var RelationParents = map[string]string{
{{- range .RelationParents}}
	"{{.Key}}": "{{.Value}}",
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

// AllAttributeTypes is a sorted list of all attribute type names.
var AllAttributeTypes = []string{{goStrSlice .AttributeTypes}}
{{- if or .EntityAnnotations .AttributeAnnotations .RelationAnnotations}}

// --- Annotations (from schema comments) ---
{{- if .EntityAnnotations}}

// EntityAnnotations maps entity names to their annotation key-value pairs.
var EntityAnnotations = map[string]map[string]string{
{{- range .EntityAnnotations}}
	"{{.Key}}": {{goKVMapSlice .Values}},
{{- end}}
}
{{- end}}
{{- if .AttributeAnnotations}}

// AttributeAnnotations maps attribute names to their annotation key-value pairs.
var AttributeAnnotations = map[string]map[string]string{
{{- range .AttributeAnnotations}}
	"{{.Key}}": {{goKVMapSlice .Values}},
{{- end}}
}
{{- end}}
{{- if .RelationAnnotations}}

// RelationAnnotations maps relation names to their annotation key-value pairs.
var RelationAnnotations = map[string]map[string]string{
{{- range .RelationAnnotations}}
	"{{.Key}}": {{goKVMapSlice .Values}},
{{- end}}
}
{{- end}}
{{- end}}
{{- if .JSONSchema}}

// --- JSON Schema fragments ---

// EntityTypeJSONSchema contains JSON schema property maps per entity type.
// Useful for OpenAPI spec generation and LLM tool-use schemas.
var EntityTypeJSONSchema = map[string]map[string]any{
{{- range .EntityJSONSchema}}
	"{{.TypeName}}": {
		"type": "object",
		"properties": map[string]any{
		{{- range .Properties}}
			"{{.Name}}": map[string]any{"type": "{{.JSONType}}"},
		{{- end}}
		},
		{{- if .Required}}
		"required": []string{{goStrSlice .Required}},
		{{- end}}
	},
{{- end}}
}
{{- end}}

// --- Convenience functions ---

// GetEntityKeys returns the key attributes for an entity type, or nil if not found.
func GetEntityKeys(entityType string) []string {
	return EntityKeys[entityType]
}

// IsAbstractEntity returns true if the entity type is abstract.
func IsAbstractEntity(entityType string) bool {
	return EntityAbstract[entityType]
}

// IsAbstractRelation returns true if the relation type is abstract.
func IsAbstractRelation(relationType string) bool {
	return RelationAbstract[relationType]
}

// GetRolePlayers returns the RoleInfo slice for a relation, or nil if not found.
func GetRolePlayers(relationType string) []RoleInfo {
	return RelationSchema[relationType]
}

// GetRoleInfo returns the RoleInfo for a specific role in a relation, or nil if not found.
func GetRoleInfo(relationType, roleName string) *RoleInfo {
	for i, ri := range RelationSchema[relationType] {
		if ri.RoleName == roleName {
			return &RelationSchema[relationType][i]
		}
	}
	return nil
}

// GetEntityAttributes returns the owned attributes for an entity type, or nil if not found.
func GetEntityAttributes(entityType string) []string {
	return EntityAttributes[entityType]
}

// GetRelationAttributes returns the owned attributes for a relation type, or nil if not found.
func GetRelationAttributes(relationType string) []string {
	return RelationAttributes[relationType]
}
`))
