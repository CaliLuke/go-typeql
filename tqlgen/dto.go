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
func BuildDTOData(schema *ParsedSchema, cfg DTOConfig) *DTOData {
	if cfg.PackageName == "" {
		return &DTOData{}
	}
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

	// --- Base Structs ---
	for _, bs := range cfg.BaseStructs {
		entity, ok := entityIndex[bs.SourceEntity]
		if !ok {
			continue
		}
		var outFields []dtoFieldCtx
		for _, attrName := range bs.InheritedAttrs {
			vtype := attrTypes[attrName]
			goType := typeDBToGo(vtype)
			required := isRequiredAttr(attrName, entity)
			if cfg.StrictOut && required {
				outFields = append(outFields, makeDTOField(attrName, goType, false, cfg.UseAcronyms))
			} else {
				outFields = append(outFields, makeDTOField(attrName, goType, true, cfg.UseAcronyms))
			}
		}
		var extraFields []dtoFieldCtx
		for name, goType := range bs.ExtraFields {
			extraFields = append(extraFields, dtoFieldCtx{
				GoName:  goTypeName(name, RenderConfig{UseAcronyms: cfg.UseAcronyms}),
				GoType:  goType,
				JSONTag: fmt.Sprintf("`json:%q`", name),
			})
		}
		sort.Slice(extraFields, func(i, j int) bool { return extraFields[i].GoName < extraFields[j].GoName })

		data.BaseStructs = append(data.BaseStructs, baseStructDTOCtx{
			BaseName:    bs.BaseName,
			OutFields:   outFields,
			ExtraFields: extraFields,
		})
	}

	// --- Entities ---
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

		// Determine base struct embedding
		embedOut, embedCreate, embedPatch := "", "", ""
		var skipAttrs map[string]bool
		bs := findBaseStruct(name, entityIndex, baseStructMap)
		if bs != nil && name != bs.SourceEntity {
			embedOut = bs.BaseName + "Out"
			embedCreate = bs.BaseName + "Create"
			embedPatch = bs.BaseName + "Patch"
			skipAttrs = inheritedAttrSets[bs.SourceEntity]
		}

		var outFields, createFields, patchFields []dtoFieldCtx
		attrs := sortedOwnedAttrs(e)
		for _, attrName := range attrs {
			if skipAttrs[attrName] {
				continue
			}
			vtype := attrTypes[attrName]
			goType := typeDBToGo(vtype)
			required := isRequiredAttr(attrName, e)

			// Apply overrides
			outReq := required
			createReq := required
			for _, ov := range overrides[name+":"+attrName] {
				if ov.Required != nil {
					switch ov.Variant {
					case "out":
						outReq = *ov.Required
					case "create":
						createReq = *ov.Required
					}
				}
			}

			// Out field
			if cfg.StrictOut && outReq {
				outFields = append(outFields, makeDTOField(attrName, goType, false, cfg.UseAcronyms))
			} else {
				outFields = append(outFields, makeDTOField(attrName, goType, true, cfg.UseAcronyms))
			}

			// Create field
			if createReq {
				createFields = append(createFields, makeDTOField(attrName, goType, false, cfg.UseAcronyms))
			} else {
				createFields = append(createFields, makeDTOField(attrName, goType, true, cfg.UseAcronyms))
			}

			// Patch field — always pointer
			patchFields = append(patchFields, makeDTOField(attrName, goType, true, cfg.UseAcronyms))
		}

		ctx := entityDTOCtx{
			GoName:       goName,
			TypeName:     name,
			Abstract:     e.Abstract,
			EmbedOut:     embedOut,
			EmbedCreate:  embedCreate,
			EmbedPatch:   embedPatch,
			OutFields:    outFields,
			CreateFields: createFields,
			PatchFields:  patchFields,
		}
		data.Entities = append(data.Entities, ctx)

		if !e.Abstract {
			data.ConcreteEntities = append(data.ConcreteEntities, goName)
		}
	}

	// --- Relations ---
	allRelations := make([]string, 0, len(schema.Relations))
	for _, r := range schema.Relations {
		allRelations = append(allRelations, r.Name)
	}
	sort.Strings(allRelations)

	// Role player lookup
	rolePlayers := make(map[string][]string)
	for _, e := range schema.Entities {
		for _, p := range e.Plays {
			key := p.Relation + ":" + p.Role
			rolePlayers[key] = append(rolePlayers[key], e.Name)
		}
	}

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
		attrs := sortedRelationOwnedAttrs(r)
		for _, attrName := range attrs {
			vtype := attrTypes[attrName]
			goType := typeDBToGo(vtype)
			required := isRequiredRelAttr(attrName, r)

			if cfg.StrictOut && required {
				outFields = append(outFields, makeDTOField(attrName, goType, false, cfg.UseAcronyms))
			} else {
				outFields = append(outFields, makeDTOField(attrName, goType, true, cfg.UseAcronyms))
			}

			if required {
				createFields = append(createFields, makeDTOField(attrName, goType, false, cfg.UseAcronyms))
			} else {
				createFields = append(createFields, makeDTOField(attrName, goType, true, cfg.UseAcronyms))
			}
		}

		ctx := relationDTOCtx{
			GoName:       goName,
			TypeName:     name,
			Abstract:     r.Abstract,
			Roles:        roles,
			OutFields:    outFields,
			CreateFields: createFields,
		}
		data.Relations = append(data.Relations, ctx)

		if !r.Abstract {
			data.ConcreteRelations = append(data.ConcreteRelations, goName)
		}
	}

	// --- Composite entities ---
	for _, comp := range cfg.CompositeEntities {
		// Merge all fields from all listed entities, deduplicating by attribute name
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
				vtype := attrTypes[o.Attribute]
				goType := typeDBToGo(vtype)
				// All composite fields are pointer (union of different types)
				fields = append(fields, makeDTOField(o.Attribute, goType, true, cfg.UseAcronyms))
			}
		}
		sort.Slice(fields, func(i, j int) bool { return fields[i].GoName < fields[j].GoName })
		data.Composites = append(data.Composites, compositeDTOCtx{
			GoName:   comp.Name,
			TypeName: comp.TypeName,
			Fields:   fields,
		})
	}

	return data
}

// RenderDTO writes a DTO Go file from DTOData.
func RenderDTO(w io.Writer, data *DTOData) error {
	return dtoTemplate.Execute(w, data)
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

func isRequiredAttr(attrName string, e EntitySpec) bool {
	for _, o := range e.Owns {
		if o.Attribute == attrName {
			if o.Key || o.Unique {
				return true
			}
			if o.Card != "" {
				min := parseCardMin(o.Card)
				return min >= 1
			}
			return false
		}
	}
	return false
}

func isRequiredRelAttr(attrName string, r RelationSpec) bool {
	for _, o := range r.Owns {
		if o.Attribute == attrName {
			if o.Key || o.Unique {
				return true
			}
			if o.Card != "" {
				min := parseCardMin(o.Card)
				return min >= 1
			}
			return false
		}
	}
	return false
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

func sortedOwnedAttrs(e EntitySpec) []string {
	attrs := make([]string, 0, len(e.Owns))
	for _, o := range e.Owns {
		attrs = append(attrs, o.Attribute)
	}
	sort.Strings(attrs)
	return attrs
}

func sortedRelationOwnedAttrs(r RelationSpec) []string {
	attrs := make([]string, 0, len(r.Owns))
	for _, o := range r.Owns {
		attrs = append(attrs, o.Attribute)
	}
	sort.Strings(attrs)
	return attrs
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
			if attrTypes[o.Attribute] == "datetime" {
				return true
			}
		}
	}
	for _, r := range schema.Relations {
		if excludeRelations[r.Name] {
			continue
		}
		for _, o := range r.Owns {
			if attrTypes[o.Attribute] == "datetime" {
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
{{- range .OutFields}}
	{{.GoName}} *{{.GoType}} {{.JSONTag}}
{{- end}}
{{- range .ExtraFields}}
	{{.GoName}} *{{.GoType}} {{.JSONTag}}
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
