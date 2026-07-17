package tqlgen

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/alecthomas/participle/v2"
	"github.com/alecthomas/participle/v2/lexer"
)

// --- Participle grammar structs ---
// These define the TypeQL schema grammar using struct tags.
// The grammar handles attribute, entity, relation, struct, and function definitions.

// AttrDef parses: attribute name [sub parent] [annotations] [,] [value type] [@constraint(...)];
// The value clause is optional: abstract attribute supertypes omit it, and
// subtyped attributes inherit it from their parent chain (resolved in
// convertAST).
type AttrDef struct {
	Name      string       `parser:"'attribute' @Ident"`
	Parent    *SubClause   `parser:"@@?"`
	PreAnnots []Annotation `parser:"@@*"`
	Comma     string       `parser:"','?"`
	ValueType string       `parser:"('value' @Ident)?"`
	Annots    []Annotation `parser:"@@*"`
	Semi      string       `parser:"';'"`
}

// EntityDef parses: entity name [sub parent] [annotations] [, clause...];
type EntityDef struct {
	Name    string         `parser:"'entity' @Ident"`
	Parent  *SubClause     `parser:"@@?"`
	Annots  []Annotation   `parser:"@@*"`
	Comma   string         `parser:"','?"`
	Clauses []EntityClause `parser:"( @@ ( ',' @@ )* )? ';'"`
}

// SubClause parses: sub parent-name
type SubClause struct {
	Parent string       `parser:"'sub' @Ident"`
	Annots []Annotation `parser:"@@*"`
}

// EntityClause is one of: owns, plays, or sub. The comma-constraint sub form
// (`entity x @abstract, sub y`) is how generators emit annotated subtypes,
// since annotations after an inline `sub` bind to the sub clause (ANN9).
type EntityClause struct {
	Owns  *OwnsDef   `parser:"  @@"`
	Plays *PlaysDef  `parser:"| @@"`
	Sub   *SubClause `parser:"| @@"`
}

// OwnsDef parses: owns attr-name[[]] [@key] [@unique] [@card(...)]
// The optional '[]' suffix marks a TypeQL 3.x ordered-list ownership.
type OwnsDef struct {
	Attribute string       `parser:"'owns' @Ident"`
	IsList    bool         `parser:"( @'[' ']' )?"`
	Annots    []Annotation `parser:"@@*"`
}

// PlaysDef parses: plays relation:role
type PlaysDef struct {
	Relation string       `parser:"'plays' @Ident"`
	Role     string       `parser:"':' @Ident"`
	Annots   []Annotation `parser:"@@*"`
}

// RelationDef parses: relation name [sub parent] [annotations] [, clause...];
type RelationDef struct {
	Name    string           `parser:"'relation' @Ident"`
	Parent  *SubClause       `parser:"@@?"`
	Annots  []Annotation     `parser:"@@*"`
	Comma   string           `parser:"','?"`
	Clauses []RelationClause `parser:"( @@ ( ',' @@ )* )? ';'"`
}

// RelationClause is one of: relates, owns, plays, or sub (comma-constraint
// form; see EntityClause).
type RelationClause struct {
	Relates *RelatesDef `parser:"  @@"`
	Owns    *OwnsDef    `parser:"| @@"`
	Plays   *PlaysDef   `parser:"| @@"`
	Sub     *SubClause  `parser:"| @@"`
}

// RelatesDef parses: relates role-name[[]] [as parent-role] [@card(...)]
// The optional '[]' suffix marks a TypeQL 3.x ordered-list role.
type RelatesDef struct {
	Role     string       `parser:"'relates' @Ident"`
	IsList   bool         `parser:"( @'[' ']' )?"`
	AsParent *AsClause    `parser:"@@?"`
	Annots   []Annotation `parser:"@@*"`
}

// AsClause parses: as parent-role
type AsClause struct {
	Parent string `parser:"'as' @Ident"`
}

// Annotation parses TypeQL schema annotations. Some annotations are carried
// into generated Go metadata, while doc/meta/capability annotations are
// accepted for parser compatibility and otherwise ignored.
type Annotation struct {
	Key         bool         `parser:"  @'@key'"`
	Unique      bool         `parser:"| @'@unique'"`
	Abstract    bool         `parser:"| @'@abstract'"`
	Cascade     bool         `parser:"| @'@cascade'"`
	Independent bool         `parser:"| @'@independent'"`
	Distinct    bool         `parser:"| @'@distinct'"`
	Card        *CardAnnot   `parser:"| @@"`
	Regex       *RegexAnnot  `parser:"| @@"`
	Values      *ValuesAnnot `parser:"| @@"`
	Range       *RangeAnnot  `parser:"| @@"`
	Subkey      *SubkeyAnnot `parser:"| @@"`
	Doc         *DocAnnot    `parser:"| @@"`
	Meta        *MetaAnnot   `parser:"| @@"`
}

// CardAnnot parses: @card(expr)
type CardAnnot struct {
	Expr string `parser:"'@card' '(' @CardExpr ')'"`
}

// RegexAnnot parses: @regex("pattern")
type RegexAnnot struct {
	Pattern string `parser:"'@regex' '(' @String ')'"`
}

// ValuesAnnot parses: @values("a", "b", ...)
type ValuesAnnot struct {
	Values []string `parser:"'@values' '(' @String ( ',' @String )* ')'"`
}

// RangeAnnot parses: @range(operand), where the operand is any TypeQL range
// expression — integer (1..5), decimal (0.5..9.5), date/datetime, or string
// ("a".."z") bounds, including half-open forms. The operand is captured as a
// flat token list up to the closing parenthesis and reassembled verbatim by
// Expr.
type RangeAnnot struct {
	Toks []RangeTok `parser:"'@range' '(' @@+ ')'"`
}

// RangeTok matches a single token of a @range operand: anything except the
// closing parenthesis that ends the annotation.
type RangeTok struct {
	Tok string `parser:"(?! ')' ) @(Ident | Punct | String | CardExpr | Var | Operator)"`
}

// Expr returns the range operand reassembled from its tokens. TypeQL range
// operands contain no significant whitespace, so plain concatenation
// reconstructs the source text (e.g. "0.5..9.5", `"a".."z"`).
func (r *RangeAnnot) Expr() string {
	var b strings.Builder
	for _, t := range r.Toks {
		b.WriteString(t.Tok)
	}
	return b.String()
}

// SubkeyAnnot parses: @subkey(identifier)
type SubkeyAnnot struct {
	Key string `parser:"'@subkey' '(' @Ident ')'"`
}

// DocAnnot parses: @doc("docstring")
type DocAnnot struct {
	Text string `parser:"'@doc' '(' @String ')'"`
}

// MetaAnnot parses: @meta("key", "value")
type MetaAnnot struct {
	Key   string `parser:"'@meta' '(' @String ','"`
	Value string `parser:"@String ')'"`
}

// StructDefP parses: struct name [annotations] (: | ,) field [, field]* [,] ;
// Supports both official TypeQL syntax (`:` separator, `name value type`) and
// legacy syntax (`,` separator, `value name type`).
type StructDefP struct {
	Name   string         `parser:"'struct' @Ident"`
	Annots []Annotation   `parser:"@@* (':' | ',')"`
	Fields []StructFieldP `parser:"@@ (',' @@)* ','? ';'"`
}

// StructFieldP parses a struct field in either official or legacy order:
//   - Official: field-name value type[?]
//   - Legacy:   value field-name type[?]
type StructFieldP struct {
	FieldName string       `parser:"( @Ident 'value' | 'value' @Ident )"`
	ValueType string       `parser:"@Ident"`
	Optional  bool         `parser:"@'?'?"`
	Annots    []Annotation `parser:"@@*"`
}

// --- Parser construction and entry point ---

// ParseSchema parses a TypeQL schema string into a ParsedSchema structure.
// It handles attribute, entity, relation, function, and struct definitions.
// Function blocks are parsed by the grammar natively — no pre-processing needed.
func ParseSchema(input string) (*ParsedSchema, error) {
	parser, err := participle.Build[TQLFileSimple](
		participle.Lexer(simpleLexer),
		participle.Elide("Comment", "Whitespace"),
		participle.UseLookahead(3),
	)
	if err != nil {
		return nil, fmt.Errorf("build parser: %w", err)
	}

	ast, err := parser.ParseString("schema.tql", input)
	if err != nil {
		return nil, fmt.Errorf("parse schema: %w", err)
	}

	return convertAST(ast), nil
}

// ParseSchemaFile reads a TypeQL schema from the specified file path and parses it.
func ParseSchemaFile(path string) (*ParsedSchema, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read schema: %w", err)
	}
	return ParseSchema(string(data))
}

// --- Top-level grammar ---

// TQLFileSimple is the top-level grammar for a TypeQL define block.
type TQLFileSimple struct {
	Define      string      `parser:"'define'"`
	Definitions []SimpleDef `parser:"@@*"`
}

// SimpleDef represents a single top-level definition within a TypeQL define block.
type SimpleDef struct {
	Attribute *AttrDef     `parser:"  @@"`
	Entity    *EntityDef   `parser:"| @@"`
	Relation  *RelationDef `parser:"| @@"`
	Struct    *StructDefP  `parser:"| @@"`
	Fun       *FunDef      `parser:"| @@"`
}

// FunDef parses: fun name <body tokens until the next top-level definition or EOF>
// The body is captured as a flat list of tokens for signature extraction.
type FunDef struct {
	Name string       `parser:"'fun' @Ident"`
	Body []FunBodyTok `parser:"@@*"`
}

// FunBodyTok matches any token except the keywords that open a new top-level
// definition (or another fun), so the parser exits the function body at the
// next definition instead of swallowing the rest of the file. Limitation: a
// function body using one of these words as a bare label (e.g. `$t sub entity`)
// ends the body early at that word.
type FunBodyTok struct {
	Tok string `parser:"(?! 'entity' | 'relation' | 'attribute' | 'struct' | 'fun' ) @(Ident | Punct | String | CardExpr | AnnotKW | Var | Arrow | Operator)"`
}

var simpleLexer = lexer.MustSimple([]lexer.SimpleRule{
	{Name: "Comment", Pattern: `#[^\n]*`},
	{Name: "Whitespace", Pattern: `[\s]+`},
	{Name: "AnnotKW", Pattern: `@(key|unique|abstract|cascade|independent|distinct|card|regex|values|range|subkey|doc|meta)`},
	{Name: "String", Pattern: `"(?:[^"\\]|\\.)*"|'(?:[^'\\]|\\.)*'`},
	{Name: "Var", Pattern: `\$[a-zA-Z_][a-zA-Z0-9_-]*`},
	{Name: "Arrow", Pattern: `->`},
	{Name: "CardExpr", Pattern: `[0-9]+(?:\.\.[0-9]*)?`},
	{Name: "Ident", Pattern: `[a-zA-Z_][a-zA-Z0-9_-]*`},
	{Name: "Operator", Pattern: `==|!=|>=|<=|\.\.|[+\-*/^%.<>=!]`},
	{Name: "Punct", Pattern: `[;,:?()\[\]{}]`},
})

// convertFunDef extracts a FunctionSpec from a parsed FunDef by walking the
// flat token list to find parameters (between first '(' and matching ')')
// and return type (tokens between '->' and the first ':' after it).
func convertFunDef(f *FunDef) FunctionSpec {
	spec := FunctionSpec{Name: f.Name}
	toks := make([]string, len(f.Body))
	for i, bt := range f.Body {
		toks[i] = bt.Tok
	}

	// Find params: tokens between first '(' and matching ')'
	parenStart := -1
	parenEnd := -1
	depth := 0
	for i, t := range toks {
		if t == "(" {
			if parenStart == -1 {
				parenStart = i
			}
			depth++
		} else if t == ")" {
			depth--
			if depth == 0 {
				parenEnd = i
				break
			}
		}
	}

	if parenStart >= 0 && parenEnd > parenStart+1 {
		// Extract params: split by "," — each param is $name : type
		paramToks := toks[parenStart+1 : parenEnd]
		var current []string
		for _, t := range paramToks {
			if t == "," {
				spec.Parameters = append(spec.Parameters, parseParam(current))
				current = nil
			} else {
				current = append(current, t)
			}
		}
		if len(current) > 0 {
			spec.Parameters = append(spec.Parameters, parseParam(current))
		}
	}

	// Find return type: tokens between '->' and first ':' after it
	arrowIdx := -1
	for i := parenEnd + 1; i < len(toks); i++ {
		if toks[i] == "->" {
			arrowIdx = i
			break
		}
	}
	if arrowIdx >= 0 {
		var retParts []string
		for i := arrowIdx + 1; i < len(toks); i++ {
			if toks[i] == ":" || strings.HasPrefix(toks[i], "@") {
				break
			}
			retParts = append(retParts, toks[i])
		}
		spec.ReturnType = strings.Join(retParts, " ")
	}
	applyFunctionDocMeta(toks, &spec.Doc, &spec.Meta)

	return spec
}

func applyFunctionDocMeta(toks []string, doc *string, meta *[]MetaSpec) {
	for i := 0; i < len(toks); i++ {
		switch toks[i] {
		case "@doc":
			if i+3 < len(toks) && toks[i+1] == "(" && toks[i+3] == ")" {
				*doc = unquote(toks[i+2])
				i += 3
			}
		case "@meta":
			if i+5 < len(toks) && toks[i+1] == "(" && toks[i+3] == "," && toks[i+5] == ")" {
				*meta = append(*meta, MetaSpec{Key: unquote(toks[i+2]), Value: unquote(toks[i+4])})
				i += 5
			}
		}
	}
}

// parseParam extracts a ParameterSpec from tokens like [$name, :, type].
func parseParam(toks []string) ParameterSpec {
	var ps ParameterSpec
	for _, t := range toks {
		if t == ":" {
			continue
		}
		if strings.HasPrefix(t, "$") {
			ps.Name = strings.TrimPrefix(t, "$")
		} else if t != "" {
			ps.TypeName = t
		}
	}
	return ps
}

// convertAST converts the participle AST to our domain model.
func convertAST(file *TQLFileSimple) *ParsedSchema {
	schema := &ParsedSchema{}

	for _, def := range file.Definitions {
		switch {
		case def.Attribute != nil:
			schema.Attributes = append(schema.Attributes, convertAttr(def.Attribute))
		case def.Entity != nil:
			schema.Entities = append(schema.Entities, convertEntity(def.Entity))
		case def.Relation != nil:
			schema.Relations = append(schema.Relations, convertRelation(def.Relation))
		case def.Struct != nil:
			schema.Structs = append(schema.Structs, convertStruct(def.Struct))
		case def.Fun != nil:
			schema.Functions = append(schema.Functions, convertFunDef(def.Fun))
		}
	}

	resolveAttrValueTypes(schema)
	return schema
}

// resolveAttrValueTypes fills the value type of subtyped attributes that omit
// an explicit `value` clause by walking the parent chain (with a cycle guard).
// Abstract roots with no value type anywhere in the chain keep an empty
// ValueType.
func resolveAttrValueTypes(schema *ParsedSchema) {
	byName := make(map[string]*AttributeSpec, len(schema.Attributes))
	for i := range schema.Attributes {
		byName[schema.Attributes[i].Name] = &schema.Attributes[i]
	}
	for i := range schema.Attributes {
		a := &schema.Attributes[i]
		seen := map[string]bool{a.Name: true}
		parent := a.Parent
		for a.ValueType == "" && parent != "" && !seen[parent] {
			seen[parent] = true
			p, ok := byName[parent]
			if !ok {
				break
			}
			a.ValueType = p.ValueType
			parent = p.Parent
		}
	}
}

func convertStruct(s *StructDefP) StructSpec {
	spec := StructSpec{Name: s.Name}
	applyDocMeta(s.Annots, &spec.Doc, &spec.Meta)
	for _, f := range s.Fields {
		field := StructFieldSpec{
			Name:      f.FieldName,
			ValueType: f.ValueType,
			Optional:  f.Optional,
		}
		applyDocMeta(f.Annots, &field.Doc, &field.Meta)
		spec.Fields = append(spec.Fields, StructFieldSpec{
			Name:      field.Name,
			ValueType: field.ValueType,
			Optional:  field.Optional,
			Doc:       field.Doc,
			Meta:      field.Meta,
		})
	}
	return spec
}

func convertAttr(a *AttrDef) AttributeSpec {
	spec := AttributeSpec{
		Name:      a.Name,
		ValueType: a.ValueType,
	}
	annots := append(append([]Annotation{}, a.PreAnnots...), a.Annots...)
	if a.Parent != nil {
		spec.Parent = a.Parent.Parent
		annots = append(annots, a.Parent.Annots...)
	}
	spec.Abstract = hasAbstract(annots)
	applyDocMeta(annots, &spec.Doc, &spec.Meta)
	for _, ann := range annots {
		if ann.Regex != nil {
			spec.Regex = unquote(ann.Regex.Pattern)
		}
		if ann.Values != nil {
			for _, v := range ann.Values.Values {
				spec.Values = append(spec.Values, unquote(v))
			}
		}
		if ann.Range != nil {
			spec.RangeOp = ann.Range.Expr()
		}
	}
	return spec
}

func convertEntity(e *EntityDef) EntitySpec {
	spec := EntitySpec{
		Name:     e.Name,
		Abstract: hasAbstract(e.Annots),
	}
	applyDocMeta(e.Annots, &spec.Doc, &spec.Meta)
	if e.Parent != nil {
		spec.Parent = e.Parent.Parent
	}
	for _, c := range e.Clauses {
		if c.Owns != nil {
			spec.Owns = append(spec.Owns, convertOwns(c.Owns))
		}
		if c.Plays != nil {
			plays := PlaysSpec{
				Relation: c.Plays.Relation,
				Role:     c.Plays.Role,
			}
			applyDocMeta(c.Plays.Annots, &plays.Doc, &plays.Meta)
			spec.Plays = append(spec.Plays, plays)
		}
		if c.Sub != nil && spec.Parent == "" {
			spec.Parent = c.Sub.Parent
		}
	}
	return spec
}

func convertRelation(r *RelationDef) RelationSpec {
	spec := RelationSpec{
		Name:     r.Name,
		Abstract: hasAbstract(r.Annots),
	}
	applyDocMeta(r.Annots, &spec.Doc, &spec.Meta)
	if r.Parent != nil {
		spec.Parent = r.Parent.Parent
	}
	for _, c := range r.Clauses {
		if c.Relates != nil {
			rs := RelatesSpec{Role: c.Relates.Role, IsList: c.Relates.IsList}
			if c.Relates.AsParent != nil {
				rs.AsParent = c.Relates.AsParent.Parent
			}
			for _, ann := range c.Relates.Annots {
				if ann.Card != nil {
					rs.Card = ann.Card.Expr
				}
			}
			applyDocMeta(c.Relates.Annots, &rs.Doc, &rs.Meta)
			spec.Relates = append(spec.Relates, rs)
		}
		if c.Owns != nil {
			spec.Owns = append(spec.Owns, convertOwns(c.Owns))
		}
		if c.Plays != nil {
			plays := PlaysSpec{
				Relation: c.Plays.Relation,
				Role:     c.Plays.Role,
			}
			applyDocMeta(c.Plays.Annots, &plays.Doc, &plays.Meta)
			spec.Plays = append(spec.Plays, plays)
		}
		if c.Sub != nil && spec.Parent == "" {
			spec.Parent = c.Sub.Parent
		}
	}
	return spec
}

func applyDocMeta(annots []Annotation, doc *string, meta *[]MetaSpec) {
	for _, ann := range annots {
		if ann.Doc != nil {
			*doc = unquote(ann.Doc.Text)
		}
		if ann.Meta != nil {
			*meta = append(*meta, MetaSpec{
				Key:   unquote(ann.Meta.Key),
				Value: unquote(ann.Meta.Value),
			})
		}
	}
}

func hasAbstract(annots []Annotation) bool {
	for _, ann := range annots {
		if ann.Abstract {
			return true
		}
	}
	return false
}

func convertOwns(o *OwnsDef) OwnsSpec {
	spec := OwnsSpec{Attribute: o.Attribute, IsList: o.IsList}
	applyDocMeta(o.Annots, &spec.Doc, &spec.Meta)
	for _, ann := range o.Annots {
		if ann.Key {
			spec.Key = true
		}
		if ann.Unique {
			spec.Unique = true
		}
		if ann.Card != nil {
			spec.Card = ann.Card.Expr
		}
	}
	return spec
}

// unquote removes surrounding quotes from a TypeQL string literal and decodes
// the subset of JSON-style escapes used in schema annotations, plus TypeQL's
// \u{...} unicode form for supplementary planes. Surrogate pairs are not
// combined here; callers should use \u{...} for code points beyond the BMP.
func unquote(s string) string {
	if len(s) < 2 {
		return s
	}

	quote := s[0]
	if (quote != '"' && quote != '\'') || s[len(s)-1] != quote {
		return s
	}

	inner := s[1 : len(s)-1]
	var b strings.Builder
	b.Grow(len(inner))

	for i := 0; i < len(inner); i++ {
		ch := inner[i]
		if ch != '\\' {
			b.WriteByte(ch)
			continue
		}
		if i+1 >= len(inner) {
			b.WriteByte('\\')
			break
		}

		next := inner[i+1]
		switch next {
		case '\\', '"', '\'', '/':
			b.WriteByte(next)
			i++
		case 'b':
			b.WriteByte('\b')
			i++
		case 'f':
			b.WriteByte('\f')
			i++
		case 'n':
			b.WriteByte('\n')
			i++
		case 'r':
			b.WriteByte('\r')
			i++
		case 't':
			b.WriteByte('\t')
			i++
		case 'u':
			r, consumed, ok := decodeUnicodeEscape(inner[i+1:])
			if !ok {
				b.WriteByte('\\')
				continue
			}
			b.WriteRune(r)
			i += consumed
		default:
			// Preserve unknown escapes verbatim rather than guessing.
			b.WriteByte('\\')
			b.WriteByte(next)
			i++
		}
	}

	return b.String()
}

func decodeUnicodeEscape(s string) (rune, int, bool) {
	if len(s) == 0 || s[0] != 'u' {
		return 0, 0, false
	}

	if len(s) >= 3 && s[1] == '{' {
		end := strings.IndexByte(s[2:], '}')
		if end < 0 {
			return 0, 0, false
		}
		hexDigits := s[2 : 2+end]
		if len(hexDigits) == 0 || len(hexDigits) > 6 {
			return 0, 0, false
		}
		v, err := strconv.ParseUint(hexDigits, 16, 32)
		if err != nil || v > 0x10FFFF {
			return 0, 0, false
		}
		return rune(v), 2 + end + 1, true
	}

	if len(s) < 5 {
		return 0, 0, false
	}
	v, err := strconv.ParseUint(s[1:5], 16, 16)
	if err != nil {
		return 0, 0, false
	}
	return rune(v), 5, true
}
