// Package tqlgen provides code generation from TypeQL schemas.
package tqlgen

import (
	"bytes"
	"fmt"
	"go/format"
	"io"
	"os"
	"strconv"
	"strings"
	"text/template"
	"unicode"
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
	// WarnWriter receives non-fatal generation warnings (unknown value types,
	// undefined attributes, roles without a resolvable player). When nil,
	// warnings are written to os.Stderr.
	WarnWriter io.Writer
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

// Render processes a ParsedSchema and writes the generated Go source code,
// gofmt-formatted, to the provided writer. It returns an error when two
// schema labels would fold to the same Go identifier (e.g. "user-name" and
// "user_name"), since the generated code would not compile, and when the
// rendered output is not valid Go (the raw output is still written so it can
// be inspected). Non-fatal issues (unknown value types, undefined attributes,
// roles with no resolvable player, skipped functions) are reported to
// cfg.WarnWriter (os.Stderr by default).
func Render(w io.Writer, schema *ParsedSchema, cfg RenderConfig) error {
	if cfg.PackageName == "" {
		cfg.PackageName = "models"
	}
	if cfg.ModulePath == "" {
		cfg.ModulePath = "github.com/CaliLuke/go-typeql/gotype"
	}

	r := newRenderer(schema, cfg)

	// Build template context
	data := &renderData{
		PackageName: cfg.PackageName,
		ModulePath:  cfg.ModulePath,
	}

	if cfg.Enums {
		for _, a := range schema.Attributes {
			if len(a.Values) > 0 {
				data.Enums = append(data.Enums, buildEnumCtx(a, cfg))
			}
		}
	}

	// TypeQL struct definitions map naturally onto plain Go structs (issue #78).
	for _, s := range schema.Structs {
		data.Structs = append(data.Structs, r.buildStructCtx(s))
	}

	for _, e := range schema.Entities {
		if cfg.SkipAbstract && e.Abstract {
			continue
		}
		data.Entities = append(data.Entities, r.buildEntityCtx(e))
	}

	for _, rel := range schema.Relations {
		if cfg.SkipAbstract && rel.Abstract {
			continue
		}
		data.Relations = append(data.Relations, r.buildRelationCtx(rel))
	}

	// TypeQL functions have no Go codegen; say so instead of dropping them
	// silently (issue #78).
	for _, fn := range schema.Functions {
		r.warnf("function %q has no Go codegen; skipped", fn.Name)
	}

	data.NeedsTime = r.needsTime
	data.NeedsGotype = len(data.Entities)+len(data.Relations) > 0

	if err := checkNameCollisions(data); err != nil {
		return err
	}

	return writeFormattedGo(w, renderTemplate, data)
}

// writeFormattedGo executes tmpl into a buffer, formats the result with
// gofmt (go/format), and writes it to w. When formatting fails — meaning the
// generator emitted syntactically invalid Go — the raw output is still
// written so it can be inspected, and an error is returned so the malformed
// generation is loud instead of landing silently in a file (issue #107).
func writeFormattedGo(w io.Writer, tmpl *template.Template, data any) error {
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return err
	}
	formatted, err := format.Source(buf.Bytes())
	if err != nil {
		if _, werr := w.Write(buf.Bytes()); werr != nil {
			return fmt.Errorf("generated code is not valid Go: %v (writing raw output also failed: %w)", err, werr)
		}
		return fmt.Errorf("generated code is not valid Go: %w", err)
	}
	_, err = w.Write(formatted)
	return err
}

// renderer carries per-render state shared by the context builders: the
// attribute value-type lookup, the relation lookup for inheritance walks,
// the warning sink, and whether any emitted field needs the time import.
type renderer struct {
	cfg       RenderConfig
	schema    *ParsedSchema
	attrSpecs map[string]AttributeSpec // attr name -> spec
	relations map[string]*RelationSpec // relation name -> spec
	structs   map[string]bool          // TypeQL struct names
	warnTo    io.Writer
	needsTime bool
}

func newRenderer(schema *ParsedSchema, cfg RenderConfig) *renderer {
	r := &renderer{
		cfg:       cfg,
		schema:    schema,
		attrSpecs: make(map[string]AttributeSpec, len(schema.Attributes)),
		relations: make(map[string]*RelationSpec, len(schema.Relations)),
		structs:   make(map[string]bool, len(schema.Structs)),
		warnTo:    cfg.WarnWriter,
	}
	if r.warnTo == nil {
		r.warnTo = os.Stderr
	}
	for _, a := range schema.Attributes {
		r.attrSpecs[a.Name] = a
	}
	for i := range schema.Relations {
		r.relations[schema.Relations[i].Name] = &schema.Relations[i]
	}
	for _, s := range schema.Structs {
		r.structs[s.Name] = true
	}
	return r
}

func (r *renderer) warnf(format string, args ...any) {
	// Warnings are best-effort diagnostics; a failed write must not abort
	// code generation.
	_, _ = fmt.Fprintf(r.warnTo, "tqlgen: warning: "+format+"\n", args...)
}

// --- Template context types ---

type renderData struct {
	PackageName string
	ModulePath  string
	NeedsTime   bool
	NeedsGotype bool
	Enums       []enumCtx
	Structs     []structCtx
	Entities    []entityCtx
	Relations   []relationCtx
}

type enumCtx struct {
	AttrName     string // TypeDB attribute name
	GoPrefix     string // PascalCase prefix
	Comment      string
	MetaComments []string
	Values       []enumValueCtx
}

type enumValueCtx struct {
	GoName string // e.g. "StatusProposed"
	Value  string // e.g. "proposed"
}

type metaCtx struct {
	Key   string
	Value string
}

type entityCtx struct {
	GoName             string
	TypeName           string // TypeDB name
	Abstract           bool
	Comment            string
	MetaComments       []string
	SchemaMeta         []metaCtx
	InheritanceComment string
	SchemaDoc          string
	Fields             []fieldCtx
}

type relationCtx struct {
	GoName             string
	TypeName           string
	Abstract           bool
	Comment            string
	MetaComments       []string
	SchemaMeta         []metaCtx
	InheritanceComment string
	SchemaDoc          string
	Roles              []roleCtx
	Fields             []fieldCtx
}

type fieldCtx struct {
	GoName       string
	GoType       string
	Tag          string
	AttrName     string // TypeDB attribute name (source label)
	Comment      string
	MetaComments []string
	Constraints  []string // @regex/@range constraints, emitted as comments
}

type structCtx struct {
	GoName       string
	TypeName     string // TypeDB struct name
	Comment      string
	MetaComments []string
	Fields       []fieldCtx
}

type roleCtx struct {
	GoName       string
	GoType       string
	Tag          string
	PlayerType   string // Go type of the role player
	RoleName     string // TypeDB role name (source label)
	Unresolved   bool   // no player found; field is emitted as a comment
	Comment      string
	MetaComments []string
}

// --- Context builders ---

func buildEnumCtx(a AttributeSpec, cfg RenderConfig) enumCtx {
	prefix := goTypeName(a.Name, cfg)
	ctx := enumCtx{
		AttrName:     a.Name,
		GoPrefix:     prefix,
		Comment:      docComment(a.Doc),
		MetaComments: metaComments(a.Meta),
	}
	for _, v := range a.Values {
		ctx.Values = append(ctx.Values, enumValueCtx{
			GoName: prefix + goEnumValueName(v, cfg),
			Value:  v,
		})
	}
	return ctx
}

// goEnumValueName converts an arbitrary @values string into a Go identifier
// fragment. Runes that are not letters or digits act as word separators, so
// "n/a" becomes "NA" and `with "quote"` becomes "WithQuote" (issue #72). A
// value containing no letters or digits at all falls back to "X" so the
// constant still gets a name; duplicates are caught by the collision check.
func goEnumValueName(v string, cfg RenderConfig) string {
	mapped := strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return r
		}
		return '-'
	}, v)
	name := goTypeName(mapped, cfg)
	if name == "" {
		return "X"
	}
	return name
}

// buildStructCtx maps a TypeQL struct definition onto a plain Go struct:
// each field keeps its declared value type and optional fields become
// pointers. Fields whose value type is another TypeQL struct reference the
// generated Go struct for it (issue #78).
func (r *renderer) buildStructCtx(s StructSpec) structCtx {
	cfg := r.cfg
	ctx := structCtx{
		GoName:       goTypeName(s.Name, cfg),
		TypeName:     s.Name,
		Comment:      docComment(s.Doc),
		MetaComments: metaComments(s.Meta),
	}
	for _, f := range s.Fields {
		goType := r.structFieldGoType(s, f)
		if f.Optional {
			goType = "*" + goType
		}
		ctx.Fields = append(ctx.Fields, fieldCtx{
			GoName:       goFieldName(f.Name, cfg),
			GoType:       goType,
			Tag:          structTagLiteral(fmt.Sprintf(`typedb:%s`, strconv.Quote(f.Name))),
			AttrName:     f.Name,
			Comment:      docComment(f.Doc),
			MetaComments: metaComments(f.Meta),
		})
	}
	return ctx
}

func (r *renderer) structFieldGoType(s StructSpec, f StructFieldSpec) string {
	goType, known := structGoFieldType(f, r.structs, r.cfg)
	if !known {
		r.warnf("struct %q field %q has unsupported value type %q; defaulting to string", s.Name, f.Name, f.ValueType)
	}
	if strings.Contains(goType, "time.") {
		r.needsTime = true
	}
	return goType
}

// structGoFieldType maps a TypeQL struct field's value type onto Go, shared
// by the models and DTO renderers. Fields whose value type is another TypeQL
// struct reference that struct's generated Go type; other value types go
// through typeDBToGoStrict. known is false when the value type is neither a
// struct nor a recognized TypeDB value type (the mapping then defaults to
// string).
func structGoFieldType(f StructFieldSpec, structNames map[string]bool, cfg RenderConfig) (goType string, known bool) {
	if structNames[f.ValueType] {
		return goTypeName(f.ValueType, cfg), true
	}
	return typeDBToGoStrict(f.ValueType)
}

func (r *renderer) buildEntityCtx(e EntitySpec) entityCtx {
	cfg := r.cfg
	ctx := entityCtx{
		GoName:       goTypeName(e.Name, cfg),
		TypeName:     e.Name,
		Abstract:     e.Abstract,
		MetaComments: metaComments(e.Meta),
		SchemaMeta:   schemaMeta(e.Meta),
		SchemaDoc:    e.Doc,
	}
	if e.Doc != "" {
		ctx.Comment = docComment(e.Doc)
	}
	if e.Parent != "" {
		ctx.InheritanceComment = fmt.Sprintf("%s inherits from %s.", ctx.GoName, e.Parent)
	}

	for _, o := range e.Owns {
		ctx.Fields = append(ctx.Fields, r.buildFieldCtx(o, fmt.Sprintf("entity %q", e.Name)))
	}

	return ctx
}

func (r *renderer) buildRelationCtx(rel RelationSpec) relationCtx {
	cfg := r.cfg
	ctx := relationCtx{
		GoName:       goTypeName(rel.Name, cfg),
		TypeName:     rel.Name,
		Abstract:     rel.Abstract,
		MetaComments: metaComments(rel.Meta),
		SchemaMeta:   schemaMeta(rel.Meta),
		SchemaDoc:    rel.Doc,
	}
	if rel.Doc != "" {
		ctx.Comment = docComment(rel.Doc)
	}
	if rel.Parent != "" {
		ctx.InheritanceComment = fmt.Sprintf("%s inherits from %s.", ctx.GoName, rel.Parent)
	}

	// Build role player type lookup from relation's relates + entity/relation plays
	for _, rs := range rel.Relates {
		role := roleCtx{
			GoName:       goFieldName(rs.Role, cfg),
			Tag:          fmt.Sprintf("`typedb:\"role:%s\"`", rs.Role),
			RoleName:     rs.Role,
			Comment:      docComment(rs.Doc),
			MetaComments: metaComments(rs.Meta),
		}

		// Find which entity or relation plays this role, walking the
		// relation's ancestor chain and role overrides (relates X as Y).
		playerType := r.findRolePlayer(rel, rs)
		if playerType != "" {
			role.PlayerType = goTypeName(playerType, cfg)
			// List roles (relates role[]) and roles whose cardinality allows
			// more than one player become slices, mirroring how multi-valued
			// ownerships are handled in buildFieldCtx.
			if rs.IsList || cardAllowsMany(rs.Card) {
				role.GoType = "[]*" + role.PlayerType
			} else {
				role.GoType = "*" + role.PlayerType
			}
		} else {
			// Never invent a Go type name: emit the field as a comment so the
			// generated file still compiles (issue #39).
			role.Unresolved = true
			r.warnf("relation %q: no entity or relation plays %s:%s; field %s omitted (add a 'plays %s:%s' clause to the schema)",
				rel.Name, rel.Name, rs.Role, role.GoName, rel.Name, rs.Role)
		}

		ctx.Roles = append(ctx.Roles, role)
	}

	for _, o := range rel.Owns {
		ctx.Fields = append(ctx.Fields, r.buildFieldCtx(o, fmt.Sprintf("relation %q", rel.Name)))
	}

	return ctx
}

func (r *renderer) buildFieldCtx(o OwnsSpec, owner string) fieldCtx {
	cfg := r.cfg
	f := fieldCtx{
		GoName:       goFieldName(o.Attribute, cfg),
		AttrName:     o.Attribute,
		Comment:      docComment(o.Doc),
		MetaComments: metaComments(o.Meta),
	}

	// Determine Go type from TypeDB value type
	spec, defined := r.attrSpecs[o.Attribute]
	goType, known := typeDBToGoStrict(spec.ValueType)
	switch {
	case !defined:
		r.warnf("attribute %q owned by %s is not defined in the schema; defaulting to string", o.Attribute, owner)
	case !known:
		r.warnf("attribute %q has unsupported value type %q; defaulting to string", o.Attribute, spec.ValueType)
	}
	if strings.Contains(goType, "time.") {
		r.needsTime = true
	}

	// Surface @regex/@range constraints as comments so schema intent survives
	// the round trip through generated code (issue #77).
	if spec.Regex != "" {
		f.Constraints = append(f.Constraints, fmt.Sprintf("@regex(%s)", strconv.Quote(spec.Regex)))
	}
	if spec.RangeOp != "" {
		f.Constraints = append(f.Constraints, fmt.Sprintf("@range(%s)", docComment(spec.RangeOp)))
	}

	// Build tag parts
	var tagParts []string
	tagParts = append(tagParts, o.Attribute)
	if o.Key {
		tagParts = append(tagParts, "key")
	}
	if o.Unique {
		tagParts = append(tagParts, "unique")
	}
	// Contract with gotype: value:decimal makes ParseTag register the
	// attribute as TypeDB value type decimal, format write literals as
	// <number>dec, and hydrate the driver's decimal strings. The tag option
	// sits after key/unique and before card.
	if spec.ValueType == "decimal" {
		tagParts = append(tagParts, "value:decimal")
		f.Constraints = append(f.Constraints,
			"decimal: precision is preserved on the wire; the Go field is float64")
	}
	if o.Card != "" {
		tagParts = append(tagParts, "card="+o.Card)
	}

	tag := fmt.Sprintf(`typedb:%s`, strconv.Quote(strings.Join(tagParts, ",")))
	if o.Doc != "" {
		tag += fmt.Sprintf(` typedb_doc:%s`, strconv.Quote(o.Doc))
	}
	f.Tag = structTagLiteral(tag)

	// Multi-valued attributes (list ownership or max cardinality > 1) become
	// slices; optional single-valued attributes become pointers.
	switch {
	case o.IsList || cardAllowsMany(o.Card):
		f.GoType = "[]" + goType
	case isOptional(o):
		f.GoType = "*" + goType
	default:
		f.GoType = goType
	}

	return f
}

// checkNameCollisions reports an error when two schema labels fold to the
// same Go identifier, which would make the generated file fail to compile
// (e.g. "user-name" and "user_name" both become UserName).
func checkNameCollisions(data *renderData) error {
	claim := func(scope map[string]string, name, source string) error {
		if prev, ok := scope[name]; ok {
			return fmt.Errorf("go name collision: %s and %s both map to %q; rename one of them in the schema", prev, source, name)
		}
		scope[name] = source
		return nil
	}

	// Package-level declarations: enum constants, entity and relation types.
	top := make(map[string]string)
	for _, e := range data.Enums {
		for _, v := range e.Values {
			if err := claim(top, v.GoName, fmt.Sprintf("enum constant for attribute %q value %q", e.AttrName, v.Value)); err != nil {
				return err
			}
		}
	}
	for _, s := range data.Structs {
		if err := claim(top, s.GoName, fmt.Sprintf("struct %q", s.TypeName)); err != nil {
			return err
		}
		fields := make(map[string]string)
		for _, f := range s.Fields {
			if err := claim(fields, f.GoName, fmt.Sprintf("field %q on struct %q", f.AttrName, s.TypeName)); err != nil {
				return err
			}
		}
	}
	for _, e := range data.Entities {
		if err := claim(top, e.GoName, fmt.Sprintf("entity %q", e.TypeName)); err != nil {
			return err
		}
		fields := make(map[string]string)
		for _, f := range e.Fields {
			if err := claim(fields, f.GoName, fmt.Sprintf("attribute %q on entity %q", f.AttrName, e.TypeName)); err != nil {
				return err
			}
		}
	}
	for _, rel := range data.Relations {
		if err := claim(top, rel.GoName, fmt.Sprintf("relation %q", rel.TypeName)); err != nil {
			return err
		}
		fields := make(map[string]string)
		for _, role := range rel.Roles {
			if role.Unresolved {
				continue // emitted as a comment, not a declaration
			}
			if err := claim(fields, role.GoName, fmt.Sprintf("role %q on relation %q", role.RoleName, rel.TypeName)); err != nil {
				return err
			}
		}
		for _, f := range rel.Fields {
			if err := claim(fields, f.GoName, fmt.Sprintf("attribute %q on relation %q", f.AttrName, rel.TypeName)); err != nil {
				return err
			}
		}
	}
	return nil
}

func docComment(doc string) string {
	return strings.Join(strings.Fields(doc), " ")
}

func metaComments(meta []MetaSpec) []string {
	if len(meta) == 0 {
		return nil
	}
	comments := make([]string, 0, len(meta))
	for _, item := range meta {
		comments = append(comments, "@meta "+docComment(item.Key)+"="+docComment(item.Value))
	}
	return comments
}

func schemaMeta(meta []MetaSpec) []metaCtx {
	if len(meta) == 0 {
		return nil
	}
	entries := make([]metaCtx, 0, len(meta))
	for _, item := range meta {
		entries = append(entries, metaCtx(item))
	}
	return entries
}

func structTagLiteral(tag string) string {
	if strings.Contains(tag, "`") {
		return strconv.Quote(tag)
	}
	return "`" + tag + "`"
}

// findRolePlayer resolves the type that plays a role of the given relation.
// It matches `plays` clauses on both entities and relations, and walks the
// relation's ancestor chain so that inherited roles (declared on a parent
// relation) and overridden roles (`relates child-role as parent-role`) still
// resolve: `company plays employment:employer` satisfies the inherited
// `employer` role on a `management sub employment` relation (issue #40).
// It returns "" when no player is found.
func (r *renderer) findRolePlayer(rel RelationSpec, role RelatesSpec) string {
	relName := rel.Name
	roleName := role.Role
	parentRole := role.AsParent
	visited := make(map[string]bool)
	for relName != "" && !visited[relName] {
		visited[relName] = true
		if p := findPlays(relName, roleName, r.schema); p != "" {
			return p
		}
		cur, ok := r.relations[relName]
		if !ok {
			break
		}
		// Ascend to the parent relation; the role keeps its name unless this
		// level renamed it with `relates X as Y`.
		relName = cur.Parent
		if parentRole != "" {
			roleName = parentRole
		}
		parentRole = ""
		if parent, ok := r.relations[relName]; ok {
			for _, rr := range parent.Relates {
				if rr.Role == roleName {
					parentRole = rr.AsParent
					break
				}
			}
		}
	}
	return ""
}

// findPlays returns the name of the first entity or relation declaring
// `plays relName:roleName`.
func findPlays(relName, roleName string, schema *ParsedSchema) string {
	for _, e := range schema.Entities {
		for _, p := range e.Plays {
			if p.Relation == relName && p.Role == roleName {
				return e.Name
			}
		}
	}
	for _, rel := range schema.Relations {
		for _, p := range rel.Plays {
			if p.Relation == relName && p.Role == roleName {
				return rel.Name
			}
		}
	}
	return ""
}

// cardAllowsMany reports whether a cardinality annotation permits more than
// one value, in which case the field is generated as a slice. Card strings
// come from @card(N), @card(N..) and @card(N..M).
func cardAllowsMany(card string) bool {
	if card == "" {
		return false
	}
	lo, hi, found := strings.Cut(card, "..")
	if !found {
		n, err := strconv.Atoi(lo)
		return err == nil && n > 1
	}
	if hi == "" {
		return true // unbounded upper limit
	}
	n, err := strconv.Atoi(hi)
	return err == nil && n > 1
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

// typeDBToGo maps a TypeDB value type to its Go equivalent, defaulting to
// string for unknown value types.
func typeDBToGo(vtype string) string {
	goType, _ := typeDBToGoStrict(vtype)
	return goType
}

// typeDBToGoStrict maps a TypeDB value type to its Go equivalent. The second
// return value is false when the value type is unknown (including the empty
// string produced by unresolved attribute references), in which case the
// mapping defaults to string.
func typeDBToGoStrict(vtype string) (string, bool) {
	switch vtype {
	case "string":
		return "string", true
	case "integer", "long":
		return "int64", true
	case "double":
		return "float64", true
	case "decimal":
		// Go has no native fixed-point decimal; the field is float64 and
		// buildFieldCtx adds value:decimal to the tag so gotype registers the
		// attribute as decimal and preserves precision on the wire.
		return "float64", true
	case "boolean":
		return "bool", true
	case "datetime", "datetime-tz", "date":
		return "time.Time", true
	case "duration":
		return "time.Duration", true
	default:
		return "string", false
	}
}

// isTimeValueType reports whether a TypeDB value type maps to a Go type from
// the time package (used to decide whether generated files import "time").
func isTimeValueType(vtype string) bool {
	goType, _ := typeDBToGoStrict(vtype)
	return strings.HasPrefix(goType, "time.")
}

// --- Go template ---

var renderTemplate = template.Must(template.New("models").Funcs(template.FuncMap{
	"quote": strconv.Quote,
}).Parse(`// Code generated by tqlgen. DO NOT EDIT.

package {{.PackageName}}
{{- if or .NeedsGotype .NeedsTime}}

import (
{{- if .NeedsGotype}}
	"github.com/CaliLuke/go-typeql/gotype"
{{- end}}
{{- if .NeedsTime}}
	"time"
{{- end}}
)
{{- end}}
{{- if .Enums}}

// --- Enum constants (from @values constraints) ---
{{range .Enums}}
{{- if .Comment}}
// {{.GoPrefix}} — {{.Comment}}
{{- end}}
{{- range .MetaComments}}
// {{.}}
{{- end}}
// {{.GoPrefix}} values for the "{{.AttrName}}" attribute.
const (
{{- range .Values}}
	{{.GoName}} = {{quote .Value}}
{{- end}}
)
{{end}}
{{- end}}
{{- if .Structs}}

// --- Value structs (from TypeQL struct definitions) ---
{{range .Structs}}
{{- if .Comment}}
// {{.GoName}} — {{.Comment}}
{{- end}}
{{- range .MetaComments}}
// {{.}}
{{- end}}
// {{.GoName}} is generated from TypeQL struct "{{.TypeName}}".
type {{.GoName}} struct {
{{- range .Fields}}
{{- if .Comment}}
	// {{.GoName}} — {{.Comment}}
{{- end}}
{{- range .MetaComments}}
	// {{.}}
{{- end}}
{{- range .Constraints}}
	// {{.}}
{{- end}}
	{{.GoName}} {{.GoType}} {{.Tag}}
{{- end}}
}
{{end}}
{{- end}}
{{range .Entities}}
{{- if .Comment}}
// {{.GoName}} — {{.Comment}}
{{- end}}
{{- range .MetaComments}}
// {{.}}
{{- end}}
{{- if .InheritanceComment}}
// {{.InheritanceComment}}
{{- end}}
type {{.GoName}} struct {
	gotype.BaseEntity
{{- range .Fields}}
{{- if .Comment}}
	// {{.GoName}} — {{.Comment}}
{{- end}}
{{- range .MetaComments}}
	// {{.}}
{{- end}}
{{- range .Constraints}}
	// {{.}}
{{- end}}
	{{.GoName}} {{.GoType}} {{.Tag}}
{{- end}}
}
{{- if .SchemaDoc}}

func ({{.GoName}}) SchemaDoc() string { return {{quote .SchemaDoc}} }
{{- end}}
{{- if .SchemaMeta}}

func ({{.GoName}}) SchemaMeta() map[string]string {
	return map[string]string{
{{- range .SchemaMeta}}
		{{quote .Key}}: {{quote .Value}},
{{- end}}
	}
}
{{- end}}
{{end}}
{{- range .Relations}}
{{- if .Comment}}
// {{.GoName}} — {{.Comment}}
{{- end}}
{{- range .MetaComments}}
// {{.}}
{{- end}}
{{- if .InheritanceComment}}
// {{.InheritanceComment}}
{{- end}}
type {{.GoName}} struct {
	gotype.BaseRelation
{{- range .Roles}}
{{- if .Comment}}
	// {{.GoName}} — {{.Comment}}
{{- end}}
{{- range .MetaComments}}
	// {{.}}
{{- end}}
{{- if .Unresolved}}
	// TODO: no entity or relation plays role "{{.RoleName}}"; field {{.GoName}} omitted.
{{- else}}
	{{.GoName}} {{.GoType}} {{.Tag}}
{{- end}}
{{- end}}
{{- range .Fields}}
{{- if .Comment}}
	// {{.GoName}} — {{.Comment}}
{{- end}}
{{- range .MetaComments}}
	// {{.}}
{{- end}}
{{- range .Constraints}}
	// {{.}}
{{- end}}
	{{.GoName}} {{.GoType}} {{.Tag}}
{{- end}}
}
{{- if .SchemaDoc}}

func ({{.GoName}}) SchemaDoc() string { return {{quote .SchemaDoc}} }
{{- end}}
{{- if .SchemaMeta}}

func ({{.GoName}}) SchemaMeta() map[string]string {
	return map[string]string{
{{- range .SchemaMeta}}
		{{quote .Key}}: {{quote .Value}},
{{- end}}
	}
}
{{- end}}
{{end}}`))
