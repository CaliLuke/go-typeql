package tqlgen

import (
	"fmt"
	"io"
	"sort"
	"strings"
	"text/template"
)

// DTOData holds all schema-derived data for DTO code generation.
type DTOData struct {
	PackageName string
	NeedsTime   bool
	IDFieldName string

	// Base structs (from BaseStructConfig)
	BaseStructs []baseStructDTOCtx

	// Entity DTOs
	Entities []entityDTOCtx

	// Relation DTOs
	Relations           []relationDTOCtx
	SkipRelationOut     bool
	RelationCreateEmbed string

	// Composite entity DTOs
	Composites []compositeDTOCtx

	// Union interface lists (concrete types only)
	ConcreteEntities  []string // Go type names
	ConcreteRelations []string

	// Configurable interface names
	EntityOutName      string
	EntityCreateName   string
	EntityPatchName    string
	RelationOutName    string
	RelationCreateName string
}

type baseStructDTOCtx struct {
	BaseName    string // e.g. "BaseArtifact"
	OutFields   []dtoFieldCtx
	ExtraFields []dtoFieldCtx
	// PatchFields and PatchExtraFields carry single-pointer (or slice) types
	// for the Patch variant; re-pointering OutFields in the template used to
	// produce **string fields (issue #79).
	PatchFields      []dtoFieldCtx
	PatchExtraFields []dtoFieldCtx
}

type entityDTOCtx struct {
	GoName       string // e.g. "Person"
	TypeName     string // e.g. "person"
	Abstract     bool
	EmbedOut     string // base struct to embed in Out, or ""
	EmbedCreate  string
	EmbedPatch   string
	OutFields    []dtoFieldCtx
	CreateFields []dtoFieldCtx
	PatchFields  []dtoFieldCtx
}

type relationDTOCtx struct {
	GoName       string
	TypeName     string
	Abstract     bool
	Roles        []roleFieldCtx
	OutFields    []dtoFieldCtx
	CreateFields []dtoFieldCtx
}

type dtoFieldCtx struct {
	GoName  string // e.g. "Name"
	GoType  string // e.g. "string" or "*string"
	JSONTag string // e.g. `json:"name"`
}

type compositeDTOCtx struct {
	GoName   string // e.g. "ArtifactDTO"
	TypeName string // e.g. "artifact"
	Fields   []dtoFieldCtx
}

type roleFieldCtx struct {
	GoName     string // e.g. "EmployeeIID" (Out) or "EmployeeID" (Create)
	GoType     string // e.g. "*string"
	JSONTag    string
	OutName    string // e.g. "EmployeeIID"
	OutJSON    string
	CreateName string // e.g. "EmployeeID"
	CreateJSON string
}

// BuildDTOData populates DTOData from a parsed schema.
// The schema should have AccumulateInheritance() called before this.
// PackageName is required to render the result: RenderDTO returns an error
// when it is empty.
func BuildDTOData(schema *ParsedSchema, cfg DTOConfig) *DTOData {
	if cfg.IDFieldName == "" {
		cfg.IDFieldName = "ID"
	}

	excludeEntities := toSet(cfg.ExcludeEntities)
	excludeRelations := toSet(cfg.ExcludeRelations)

	// Index attributes
	attrTypes := make(map[string]string, len(schema.Attributes))
	for _, a := range schema.Attributes {
		attrTypes[a.Name] = a.ValueType
	}

	// Index entities
	entityIndex := make(map[string]EntitySpec, len(schema.Entities))
	for _, e := range schema.Entities {
		entityIndex[e.Name] = e
	}

	// Build base struct lookup: source_entity → BaseStructConfig
	baseStructMap := make(map[string]BaseStructConfig)
	inheritedAttrSets := make(map[string]map[string]bool) // source_entity → set of inherited attrs
	for _, bs := range cfg.BaseStructs {
		baseStructMap[bs.SourceEntity] = bs
		s := make(map[string]bool, len(bs.InheritedAttrs))
		for _, a := range bs.InheritedAttrs {
			s[a] = true
		}
		inheritedAttrSets[bs.SourceEntity] = s
	}

	// Override lookup
	overrides := buildOverrideIndex(cfg.EntityFieldOverrides)

	data := &DTOData{
		PackageName:         cfg.PackageName,
		IDFieldName:         cfg.IDFieldName,
		SkipRelationOut:     cfg.SkipRelationOut,
		RelationCreateEmbed: cfg.RelationCreateEmbed,
		EntityOutName:       defaultStr(cfg.EntityOutName, "EntityOut"),
		EntityCreateName:    defaultStr(cfg.EntityCreateName, "EntityCreate"),
		EntityPatchName:     defaultStr(cfg.EntityPatchName, "EntityPatch"),
		RelationOutName:     defaultStr(cfg.RelationOutName, "RelationOut"),
		RelationCreateName:  defaultStr(cfg.RelationCreateName, "RelationCreate"),
	}

	// Check if we need time import
	data.NeedsTime = needsTimeDTOImport(schema, attrTypes, excludeEntities, excludeRelations)

	buildDTOBaseStructs(data, cfg, attrTypes, entityIndex)
	buildDTOEntities(data, cfg, attrTypes, entityIndex, baseStructMap, inheritedAttrSets, overrides, excludeEntities, schema)
	buildDTORelations(data, cfg, attrTypes, schema, excludeRelations)
	buildDTOComposites(data, cfg, attrTypes, entityIndex)

	return data
}

func buildDTOBaseStructs(data *DTOData, cfg DTOConfig, attrTypes map[string]string, entityIndex map[string]EntitySpec) {
	for _, bs := range cfg.BaseStructs {
		entity, ok := entityIndex[bs.SourceEntity]
		if !ok {
			continue
		}
		var outFields, patchFields []dtoFieldCtx
		for _, attrName := range bs.InheritedAttrs {
			o := findOwns(entity.Owns, attrName)
			goType := typeDBToGo(attrTypes[attrName])
			pointer := !cfg.StrictOut || !isRequiredOwns(o)
			outFields = append(outFields, dtoFieldFor(o, goType, pointer, cfg.UseAcronyms))
			patchFields = append(patchFields, dtoFieldFor(o, goType, true, cfg.UseAcronyms))
		}
		var extraFields, patchExtraFields []dtoFieldCtx
		for name, goType := range bs.ExtraFields {
			field := dtoFieldCtx{
				GoName:  goTypeName(name, RenderConfig{UseAcronyms: cfg.UseAcronyms}),
				GoType:  goType,
				JSONTag: fmt.Sprintf("`json:%q`", name),
			}
			extraFields = append(extraFields, field)
			field.GoType = pointerize(goType)
			patchExtraFields = append(patchExtraFields, field)
		}
		sortFields := func(fields []dtoFieldCtx) {
			sort.Slice(fields, func(i, j int) bool { return fields[i].GoName < fields[j].GoName })
		}
		sortFields(extraFields)
		sortFields(patchExtraFields)
		data.BaseStructs = append(data.BaseStructs, baseStructDTOCtx{
			BaseName:         bs.BaseName,
			OutFields:        outFields,
			ExtraFields:      extraFields,
			PatchFields:      patchFields,
			PatchExtraFields: patchExtraFields,
		})
	}
}

func buildDTOEntities(data *DTOData, cfg DTOConfig, attrTypes map[string]string, entityIndex map[string]EntitySpec, baseStructMap map[string]BaseStructConfig, inheritedAttrSets map[string]map[string]bool, overrides map[string][]EntityFieldOverride, excludeEntities map[string]bool, schema *ParsedSchema) {
	allEntities := make([]string, 0, len(schema.Entities))
	for _, e := range schema.Entities {
		allEntities = append(allEntities, e.Name)
	}
	sort.Strings(allEntities)

	for _, name := range allEntities {
		if excludeEntities[name] {
			continue
		}
		e := entityIndex[name]
		if cfg.SkipAbstract && e.Abstract {
			continue
		}
		goName := goTypeName(name, RenderConfig{UseAcronyms: cfg.UseAcronyms})

		embedOut, embedCreate, embedPatch := "", "", ""
		var skipAttrs map[string]bool
		if bs := findBaseStruct(name, entityIndex, baseStructMap); bs != nil && name != bs.SourceEntity {
			embedOut = bs.BaseName + "Out"
			embedCreate = bs.BaseName + "Create"
			embedPatch = bs.BaseName + "Patch"
			skipAttrs = inheritedAttrSets[bs.SourceEntity]
		}

		outFields, createFields, patchFields := entityDTOFields(e, attrTypes, skipAttrs, name, overrides, cfg)
		data.Entities = append(data.Entities, entityDTOCtx{
			GoName:       goName,
			TypeName:     name,
			Abstract:     e.Abstract,
			EmbedOut:     embedOut,
			EmbedCreate:  embedCreate,
			EmbedPatch:   embedPatch,
			OutFields:    outFields,
			CreateFields: createFields,
			PatchFields:  patchFields,
		})
		if !e.Abstract {
			data.ConcreteEntities = append(data.ConcreteEntities, goName)
		}
	}
}

func entityDTOFields(e EntitySpec, attrTypes map[string]string, skipAttrs map[string]bool, entityName string, overrides map[string][]EntityFieldOverride, cfg DTOConfig) (out, create, patch []dtoFieldCtx) {
	for _, o := range sortedOwns(e.Owns) {
		if skipAttrs[o.Attribute] {
			continue
		}
		goType := typeDBToGo(attrTypes[o.Attribute])
		required := isRequiredOwns(o)
		outReq, createReq := required, required
		for _, ov := range overrides[entityName+":"+o.Attribute] {
			if ov.Required == nil {
				continue
			}
			switch ov.Variant {
			case "out":
				outReq = *ov.Required
			case "create":
				createReq = *ov.Required
			}
		}
		out = append(out, dtoFieldFor(o, goType, !cfg.StrictOut || !outReq, cfg.UseAcronyms))
		create = append(create, dtoFieldFor(o, goType, !createReq, cfg.UseAcronyms))
		patch = append(patch, dtoFieldFor(o, goType, true, cfg.UseAcronyms))
	}
	return
}

func buildDTORelations(data *DTOData, cfg DTOConfig, attrTypes map[string]string, schema *ParsedSchema, excludeRelations map[string]bool) {
	allRelations := make([]string, 0, len(schema.Relations))
	for _, r := range schema.Relations {
		allRelations = append(allRelations, r.Name)
	}
	sort.Strings(allRelations)

	for _, name := range allRelations {
		if excludeRelations[name] {
			continue
		}
		r := findRelation(schema, name)
		if cfg.SkipAbstract && r.Abstract {
			continue
		}
		goName := goTypeName(name, RenderConfig{UseAcronyms: cfg.UseAcronyms})

		var roles []roleFieldCtx
		for _, rel := range r.Relates {
			roleGoName := goTypeName(rel.Role, RenderConfig{UseAcronyms: cfg.UseAcronyms})
			roles = append(roles, roleFieldCtx{
				OutName:    roleGoName + cfg.IDFieldName,
				OutJSON:    fmt.Sprintf("`json:%q`", rel.Role+"_"+strings.ToLower(cfg.IDFieldName)),
				CreateName: roleGoName + "ID",
				CreateJSON: fmt.Sprintf("`json:%q`", rel.Role+"_id"),
			})
		}

		var outFields, createFields []dtoFieldCtx
		for _, o := range sortedOwns(r.Owns) {
			goType := typeDBToGo(attrTypes[o.Attribute])
			required := isRequiredOwns(o)
			outFields = append(outFields, dtoFieldFor(o, goType, !cfg.StrictOut || !required, cfg.UseAcronyms))
			createFields = append(createFields, dtoFieldFor(o, goType, !required, cfg.UseAcronyms))
		}

		data.Relations = append(data.Relations, relationDTOCtx{
			GoName:       goName,
			TypeName:     name,
			Abstract:     r.Abstract,
			Roles:        roles,
			OutFields:    outFields,
			CreateFields: createFields,
		})
		if !r.Abstract {
			data.ConcreteRelations = append(data.ConcreteRelations, goName)
		}
	}
}

func buildDTOComposites(data *DTOData, cfg DTOConfig, attrTypes map[string]string, entityIndex map[string]EntitySpec) {
	for _, comp := range cfg.CompositeEntities {
		seen := make(map[string]bool)
		var fields []dtoFieldCtx
		for _, eName := range comp.Entities {
			e, ok := entityIndex[eName]
			if !ok {
				continue
			}
			for _, o := range e.Owns {
				if seen[o.Attribute] {
					continue
				}
				seen[o.Attribute] = true
				fields = append(fields, dtoFieldFor(o, typeDBToGo(attrTypes[o.Attribute]), true, cfg.UseAcronyms))
			}
		}
		sort.Slice(fields, func(i, j int) bool { return fields[i].GoName < fields[j].GoName })
		data.Composites = append(data.Composites, compositeDTOCtx{
			GoName:   comp.Name,
			TypeName: comp.TypeName,
			Fields:   fields,
		})
	}
}

// RenderDTO writes a gofmt-formatted DTO Go file from DTOData. It returns an
// error when data.PackageName is empty (the generated file would not be
// valid Go) or when the rendered output fails to format.
func RenderDTO(w io.Writer, data *DTOData) error {
	if data.PackageName == "" {
		return fmt.Errorf("tqlgen: DTOConfig.PackageName is required")
	}
	return writeFormattedGo(w, dtoTemplate, data)
}

// --- helpers ---

func toSet(ss []string) map[string]bool {
	m := make(map[string]bool, len(ss))
	for _, s := range ss {
		m[s] = true
	}
	return m
}

func makeDTOField(attrName, goType string, pointer bool, useAcronyms bool) dtoFieldCtx {
	goName := goTypeName(attrName, RenderConfig{UseAcronyms: useAcronyms})
	if pointer {
		goType = "*" + goType
	}
	return dtoFieldCtx{
		GoName:  goName,
		GoType:  goType,
		JSONTag: fmt.Sprintf("`json:%q`", attrName),
	}
}

// dtoFieldFor builds a DTO field for one ownership. List ownerships and
// cardinalities allowing more than one value become slices (never pointered,
// since nil already expresses absence); scalar fields become pointers when
// pointer is true.
func dtoFieldFor(o OwnsSpec, goType string, pointer bool, useAcronyms bool) dtoFieldCtx {
	if o.IsList || cardAllowsMany(o.Card) {
		return makeDTOField(o.Attribute, "[]"+goType, false, useAcronyms)
	}
	return makeDTOField(o.Attribute, goType, pointer, useAcronyms)
}

// pointerize prepends "*" to a Go type unless it is already a pointer or a
// slice (whose zero value already expresses absence).
func pointerize(goType string) string {
	if strings.HasPrefix(goType, "*") || strings.HasPrefix(goType, "[]") {
		return goType
	}
	return "*" + goType
}

// findOwns returns the ownership spec for attr, or a zero OwnsSpec carrying
// only the attribute name when the owner has no such clause.
func findOwns(owns []OwnsSpec, attr string) OwnsSpec {
	for _, o := range owns {
		if o.Attribute == attr {
			return o
		}
	}
	return OwnsSpec{Attribute: attr}
}

// isRequiredOwns reports whether an ownership implies a required field:
// @key, @unique, or a minimum cardinality of at least 1.
func isRequiredOwns(o OwnsSpec) bool {
	if o.Key || o.Unique {
		return true
	}
	return o.Card != "" && parseCardMin(o.Card) >= 1
}

// parseCardMin extracts the minimum from a cardinality string like "1", "0..1", "1..".
func parseCardMin(card string) int {
	parts := strings.SplitN(card, "..", 2)
	if len(parts) == 0 {
		return 0
	}
	var min int
	_, _ = fmt.Sscanf(parts[0], "%d", &min)
	return min
}

// sortedOwns returns the owns clauses sorted by attribute name.
func sortedOwns(owns []OwnsSpec) []OwnsSpec {
	out := make([]OwnsSpec, len(owns))
	copy(out, owns)
	sort.Slice(out, func(i, j int) bool { return out[i].Attribute < out[j].Attribute })
	return out
}

func findRelation(schema *ParsedSchema, name string) RelationSpec {
	for _, r := range schema.Relations {
		if r.Name == name {
			return r
		}
	}
	return RelationSpec{}
}

func findBaseStruct(entityName string, entities map[string]EntitySpec, baseMap map[string]BaseStructConfig) *BaseStructConfig {
	current := entityName
	for current != "" {
		if bs, ok := baseMap[current]; ok {
			return &bs
		}
		e, ok := entities[current]
		if !ok {
			break
		}
		current = e.Parent
	}
	return nil
}

func defaultStr(val, def string) string {
	if val != "" {
		return val
	}
	return def
}

type overrideKey = string // "entity:field"

func buildOverrideIndex(overrides []EntityFieldOverride) map[overrideKey][]EntityFieldOverride {
	m := make(map[overrideKey][]EntityFieldOverride)
	for _, o := range overrides {
		key := o.Entity + ":" + o.Field
		m[key] = append(m[key], o)
	}
	return m
}

func needsTimeDTOImport(schema *ParsedSchema, attrTypes map[string]string, excludeEntities, excludeRelations map[string]bool) bool {
	for _, e := range schema.Entities {
		if excludeEntities[e.Name] {
			continue
		}
		for _, o := range e.Owns {
			if isTimeValueType(attrTypes[o.Attribute]) {
				return true
			}
		}
	}
	for _, r := range schema.Relations {
		if excludeRelations[r.Name] {
			continue
		}
		for _, o := range r.Owns {
			if isTimeValueType(attrTypes[o.Attribute]) {
				return true
			}
		}
	}
	return false
}

// --- Template ---

var dtoFuncMap = template.FuncMap{
	"lower": strings.ToLower,
}

var dtoTemplate = template.Must(template.New("dto").Funcs(dtoFuncMap).Parse(`// Code generated by tqlgen; DO NOT EDIT.

package {{.PackageName}}
{{if .NeedsTime}}
import "time"
{{end}}
{{- range .BaseStructs}}
// --- {{.BaseName}} base structs ---

type {{.BaseName}}Out struct {
{{- range .OutFields}}
	{{.GoName}} {{.GoType}} {{.JSONTag}}
{{- end}}
{{- range .ExtraFields}}
	{{.GoName}} {{.GoType}} {{.JSONTag}}
{{- end}}
}

type {{.BaseName}}Create struct {
{{- range .OutFields}}
	{{.GoName}} {{.GoType}} {{.JSONTag}}
{{- end}}
{{- range .ExtraFields}}
	{{.GoName}} {{.GoType}} {{.JSONTag}}
{{- end}}
}

type {{.BaseName}}Patch struct {
{{- range .PatchFields}}
	{{.GoName}} {{.GoType}} {{.JSONTag}}
{{- end}}
{{- range .PatchExtraFields}}
	{{.GoName}} {{.GoType}} {{.JSONTag}}
{{- end}}
}
{{end}}
// --- Entity DTOs ---
{{range .Entities}}{{if not .Abstract}}
// {{.GoName}}Out is the response DTO for {{.TypeName}}.
type {{.GoName}}Out struct {
{{- if .EmbedOut}}
	{{.EmbedOut}}
{{- end}}
	{{$.IDFieldName}} string ` + "`" + `json:"{{$.IDFieldName | lower}}"` + "`" + `
	Type string ` + "`" + `json:"type"` + "`" + `
{{- range .OutFields}}
	{{.GoName}} {{.GoType}} {{.JSONTag}}
{{- end}}
}

// {{.GoName}}Create is the create request DTO for {{.TypeName}}.
type {{.GoName}}Create struct {
{{- if .EmbedCreate}}
	{{.EmbedCreate}}
{{- end}}
	Type string ` + "`" + `json:"type"` + "`" + `
{{- range .CreateFields}}
	{{.GoName}} {{.GoType}} {{.JSONTag}}
{{- end}}
}

// {{.GoName}}Patch is the partial update DTO for {{.TypeName}}.
type {{.GoName}}Patch struct {
{{- if .EmbedPatch}}
	{{.EmbedPatch}}
{{- end}}
{{- range .PatchFields}}
	{{.GoName}} {{.GoType}} {{.JSONTag}}
{{- end}}
}

func ({{.GoName}}Out) TypeName() string    { return "{{.TypeName}}" }
func ({{.GoName}}Create) TypeName() string { return "{{.TypeName}}" }
func ({{.GoName}}Patch) TypeName() string  { return "{{.TypeName}}" }
{{end}}{{end}}
{{- if not .SkipRelationOut}}
// --- Relation DTOs ---
{{range .Relations}}{{if not .Abstract}}
// {{.GoName}}Out is the response DTO for {{.TypeName}}.
type {{.GoName}}Out struct {
	{{$.IDFieldName}} string ` + "`" + `json:"{{$.IDFieldName | lower}}"` + "`" + `
	Type string ` + "`" + `json:"type"` + "`" + `
{{- range .Roles}}
	{{.OutName}} *string {{.OutJSON}}
{{- end}}
{{- range .OutFields}}
	{{.GoName}} {{.GoType}} {{.JSONTag}}
{{- end}}
}

func ({{.GoName}}Out) TypeName() string { return "{{.TypeName}}" }
{{end}}{{end}}
{{- end}}

// --- Relation Create DTOs ---
{{range .Relations}}{{if not .Abstract}}
// {{.GoName}}Create is the create request DTO for {{.TypeName}}.
type {{.GoName}}Create struct {
{{- if $.RelationCreateEmbed}}
	{{$.RelationCreateEmbed}}
{{- end}}
	Type string ` + "`" + `json:"type"` + "`" + `
{{- range .Roles}}
	{{.CreateName}} string {{.CreateJSON}}
{{- end}}
{{- range .CreateFields}}
	{{.GoName}} {{.GoType}} {{.JSONTag}}
{{- end}}
}

func ({{.GoName}}Create) TypeName() string { return "{{.TypeName}}" }
{{end}}{{end}}
{{- if .Composites}}
// --- Composite DTOs ---
{{range .Composites}}
// {{.GoName}}Out is a composite response DTO merging multiple entity types.
type {{.GoName}}Out struct {
	{{$.IDFieldName}} string ` + "`" + `json:"{{$.IDFieldName | lower}}"` + "`" + `
	Type string ` + "`" + `json:"type"` + "`" + `
{{- range .Fields}}
	{{.GoName}} {{.GoType}} {{.JSONTag}}
{{- end}}
}

func ({{.GoName}}Out) TypeName() string { return "{{.TypeName}}" }
{{end}}
{{- end}}
// --- Interfaces ---

// {{.EntityOutName}} is implemented by all entity Out DTOs.
type {{.EntityOutName}} interface {
	TypeName() string
}

// {{.EntityCreateName}} is implemented by all entity Create DTOs.
type {{.EntityCreateName}} interface {
	TypeName() string
}

// {{.EntityPatchName}} is implemented by all entity Patch DTOs.
type {{.EntityPatchName}} interface {
	TypeName() string
}

// {{.RelationOutName}} is implemented by all relation Out DTOs.
type {{.RelationOutName}} interface {
	TypeName() string
}

// {{.RelationCreateName}} is implemented by all relation Create DTOs.
type {{.RelationCreateName}} interface {
	TypeName() string
}
`))
