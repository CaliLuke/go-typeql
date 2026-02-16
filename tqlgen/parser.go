package tqlgen

import (
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/alecthomas/participle/v2"
	"github.com/alecthomas/participle/v2/lexer"
)

// --- Participle grammar structs ---
// These define the TypeQL schema grammar using struct tags.
// The grammar handles attribute, entity, relation, struct, and function definitions.

// AttrDef parses: attribute name [,] value type [@constraint(...)];
type AttrDef struct {
	Name      string        `parser:"'attribute' @Ident ','?"`
	ValueType string        `parser:"'value' @Ident"`
	Annots    []Annotation  `parser:"@@*"`
	Semi      string        `parser:"';'"`
}

// EntityDef parses: entity name [sub parent] [@abstract] [, clause...];
type EntityDef struct {
	Name     string         `parser:"'entity' @Ident"`
	Parent   *SubClause     `parser:"@@?"`
	Abstract bool           `parser:"@'@abstract'?"`
	Comma    string         `parser:"','?"`
	Clauses  []EntityClause `parser:"( @@ ( ',' @@ )* )? ';'"`
}

// SubClause parses: sub parent-name
type SubClause struct {
	Parent string `parser:"'sub' @Ident"`
}

// EntityClause is one of: owns or plays.
type EntityClause struct {
	Owns  *OwnsDef  `parser:"  @@"`
	Plays *PlaysDef `parser:"| @@"`
}

// OwnsDef parses: owns attr-name [@key] [@unique] [@card(...)]
type OwnsDef struct {
	Attribute string       `parser:"'owns' @Ident"`
	Annots    []Annotation `parser:"@@*"`
}

// PlaysDef parses: plays relation:role
type PlaysDef struct {
	Relation string `parser:"'plays' @Ident"`
	Role     string `parser:"':' @Ident"`
}

// RelationDef parses: relation name [sub parent] [@abstract] [, clause...];
type RelationDef struct {
	Name     string           `parser:"'relation' @Ident"`
	Parent   *SubClause       `parser:"@@?"`
	Abstract bool             `parser:"@'@abstract'?"`
	Comma    string           `parser:"','?"`
	Clauses  []RelationClause `parser:"( @@ ( ',' @@ )* )? ';'"`
}

// RelationClause is one of: relates, owns, or plays.
type RelationClause struct {
	Relates *RelatesDef `parser:"  @@"`
	Owns    *OwnsDef    `parser:"| @@"`
	Plays   *PlaysDef   `parser:"| @@"`
}

// RelatesDef parses: relates role-name [as parent-role] [@card(...)]
type RelatesDef struct {
	Role     string       `parser:"'relates' @Ident"`
	AsParent *AsClause    `parser:"@@?"`
	Annots   []Annotation `parser:"@@*"`
}

// AsClause parses: as parent-role
type AsClause struct {
	Parent string `parser:"'as' @Ident"`
}

// Annotation parses: @key, @unique, @abstract, @card(...), @regex(...), @values(...), @range(...)
type Annotation struct {
	Key    bool        `parser:"  @'@key'"`
	Unique bool        `parser:"| @'@unique'"`
	Card   *CardAnnot  `parser:"| @@"`
	Regex  *RegexAnnot `parser:"| @@"`
	Values *ValuesAnnot `parser:"| @@"`
	Range  *RangeAnnot `parser:"| @@"`
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

// RangeAnnot parses: @range(expr)
type RangeAnnot struct {
	Expr string `parser:"'@range' '(' @CardExpr ')'"`
}

// StructDefP parses: struct name (: | ,) field [, field]* [,] ;
// Supports both official TypeQL syntax (`:` separator, `name value type`) and
// legacy syntax (`,` separator, `value name type`).
type StructDefP struct {
	Name   string         `parser:"'struct' @Ident (':' | ',')"`
	Fields []StructFieldP `parser:"@@ (',' @@)* ','? ';'"`
}

// StructFieldP parses a struct field in either official or legacy order:
//   - Official: field-name value type[?]
//   - Legacy:   value field-name type[?]
type StructFieldP struct {
	FieldName string `parser:"( @Ident 'value' | 'value' @Ident )"`
	ValueType string `parser:"@Ident"`
	Optional  bool   `parser:"@'?'?"`
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
	Define      string     `parser:"'define'"`
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

// FunDef parses: fun name <body tokens until next fun or EOF>
// The body is captured as a flat list of tokens for signature extraction.
type FunDef struct {
	Name string       `parser:"FunKW @Ident"`
	Body []FunBodyTok `parser:"@@*"`
}

// FunBodyTok matches every token type EXCEPT FunKW. When the parser hits the
// next `fun`, it exits the current FunDef and starts a new one.
type FunBodyTok struct {
	Tok string `parser:"@(Ident | Keyword | Punct | String | CardExpr | AnnotKW | Var | Arrow | Operator)"`
}

var simpleLexer = lexer.MustSimple([]lexer.SimpleRule{
	{Name: "Comment", Pattern: `#[^\n]*`},
	{Name: "Whitespace", Pattern: `[\s]+`},
	{Name: "FunKW", Pattern: `\bfun\b`},
	{Name: "Keyword", Pattern: `\b(define|attribute|entity|relation|sub|value|owns|plays|relates|as|struct|match|return|isa|has|not|or|in|is|count|sum|max|min|mean|median|std|group)\b`},
	{Name: "AnnotKW", Pattern: `@(key|unique|abstract|card|regex|values|range)`},
	{Name: "String", Pattern: `"(?:[^"\\]|\\.)*"`},
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
			if toks[i] == ":" {
				break
			}
			retParts = append(retParts, toks[i])
		}
		spec.ReturnType = strings.Join(retParts, " ")
	}

	return spec
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

	return schema
}

func convertStruct(s *StructDefP) StructSpec {
	spec := StructSpec{Name: s.Name}
	for _, f := range s.Fields {
		spec.Fields = append(spec.Fields, StructFieldSpec{
			Name:      f.FieldName,
			ValueType: f.ValueType,
			Optional:  f.Optional,
		})
	}
	return spec
}

func convertAttr(a *AttrDef) AttributeSpec {
	spec := AttributeSpec{
		Name:      a.Name,
		ValueType: a.ValueType,
	}
	for _, ann := range a.Annots {
		if ann.Regex != nil {
			spec.Regex = unquote(ann.Regex.Pattern)
		}
		if ann.Values != nil {
			for _, v := range ann.Values.Values {
				spec.Values = append(spec.Values, unquote(v))
			}
		}
		if ann.Range != nil {
			spec.RangeOp = ann.Range.Expr
		}
	}
	return spec
}

func convertEntity(e *EntityDef) EntitySpec {
	spec := EntitySpec{
		Name:     e.Name,
		Abstract: e.Abstract,
	}
	if e.Parent != nil {
		spec.Parent = e.Parent.Parent
	}
	for _, c := range e.Clauses {
		if c.Owns != nil {
			spec.Owns = append(spec.Owns, convertOwns(c.Owns))
		}
		if c.Plays != nil {
			spec.Plays = append(spec.Plays, PlaysSpec{
				Relation: c.Plays.Relation,
				Role:     c.Plays.Role,
			})
		}
	}
	return spec
}

func convertRelation(r *RelationDef) RelationSpec {
	spec := RelationSpec{
		Name:     r.Name,
		Abstract: r.Abstract,
	}
	if r.Parent != nil {
		spec.Parent = r.Parent.Parent
	}
	for _, c := range r.Clauses {
		if c.Relates != nil {
			rs := RelatesSpec{Role: c.Relates.Role}
			if c.Relates.AsParent != nil {
				rs.AsParent = c.Relates.AsParent.Parent
			}
			for _, ann := range c.Relates.Annots {
				if ann.Card != nil {
					rs.Card = ann.Card.Expr
				}
			}
			spec.Relates = append(spec.Relates, rs)
		}
		if c.Owns != nil {
			spec.Owns = append(spec.Owns, convertOwns(c.Owns))
		}
		if c.Plays != nil {
			spec.Plays = append(spec.Plays, PlaysSpec{
				Relation: c.Plays.Relation,
				Role:     c.Plays.Role,
			})
		}
	}
	return spec
}

func convertOwns(o *OwnsDef) OwnsSpec {
	spec := OwnsSpec{Attribute: o.Attribute}
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

// unquote removes surrounding quotes from a string literal.
func unquote(s string) string {
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		return s[1 : len(s)-1]
	}
	return s
}



// --- Annotation extraction ---

// ExtractAnnotations parses comment annotations of the form "# @key value"
// from schema text. Returns a map of type name -> annotation map.
func ExtractAnnotations(input string) map[string]map[string]string {
	result := make(map[string]map[string]string)

	lines := strings.Split(input, "\n")
	var pendingAnnots []struct{ key, val string }

	// Match: # @key or # @key(value) or # @key value
	annotRe := regexp.MustCompile(`^#\s*@(\w+)(?:\(([^)]*)\)|\s+(.+))?$`)
	typeRe := regexp.MustCompile(`^(entity|relation|attribute|struct)\s+([\w-]+)`)

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)

		// Check for annotation comment
		if m := annotRe.FindStringSubmatch(trimmed); m != nil {
			val := m[2] // from (value)
			if val == "" {
				val = m[3] // from space-separated
			}
			pendingAnnots = append(pendingAnnots, struct{ key, val string }{m[1], strings.TrimSpace(val)})
			continue
		}

		// Check if this line defines a type
		if len(pendingAnnots) > 0 {
			if m := typeRe.FindStringSubmatch(trimmed); m != nil {
				annots := make(map[string]string)
				for _, a := range pendingAnnots {
					annots[a.key] = a.val
				}
				result[m[2]] = annots
			}
			pendingAnnots = nil
		} else if trimmed != "" && !strings.HasPrefix(trimmed, "#") {
			pendingAnnots = nil
		}
	}

	return result
}
