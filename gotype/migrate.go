// Package gotype provides automated schema migration capabilities.
package gotype

import (
	"context"
	"fmt"
	"strings"

	"github.com/CaliLuke/go-typeql/tqlgen"
)

// SchemaDiff represents the calculated differences between the schema defined
// by Go structs and the current schema in the TypeDB database.
type SchemaDiff struct {
	// AddAttributes are new attribute types to be defined.
	AddAttributes []AttrChange
	// AddEntities are new entity types to be defined.
	AddEntities []TypeChange
	// AddRelations are new relation types to be defined.
	AddRelations []TypeChange
	// AddOwns are new attribute ownerships to be added to existing types.
	AddOwns []OwnsChange
	// AddRelates are new role relations to be added to existing relation types.
	AddRelates []RelatesChange
	// RemoveOwns identifies attribute ownerships present in the DB but not in the code.
	RemoveOwns []OwnsChange
	// RemoveTypes identifies types present in the DB but not in the code.
	RemoveTypes []string
}

// AttrChange describes an attribute type to be added to the schema.
type AttrChange struct {
	Name      string
	ValueType string
}

// TypeChange describes an entity or relation type to be added to the schema.
type TypeChange struct {
	TypeQL string // The full 'define' statement for the type.
}

// OwnsChange describes an attribute ownership to be added to a type.
type OwnsChange struct {
	TypeName  string
	Attribute string
	Annots    string // TypeQL annotations like @key or @card.
}

// RelatesChange describes a role to be added to a relation type.
type RelatesChange struct {
	TypeName string
	Role     string
	Card     string
}

// Summary returns a human-readable description of the changes in the diff.
func (d *SchemaDiff) Summary() string {
	if d.IsEmpty() && len(d.RemoveTypes) == 0 && len(d.RemoveOwns) == 0 {
		return "schema is up to date"
	}
	var parts []string
	if n := len(d.AddAttributes); n > 0 {
		names := make([]string, n)
		for i, a := range d.AddAttributes {
			names[i] = a.Name
		}
		parts = append(parts, fmt.Sprintf("add %d attribute(s): %s", n, strings.Join(names, ", ")))
	}
	if n := len(d.AddEntities); n > 0 {
		parts = append(parts, fmt.Sprintf("add %d entity type(s)", n))
	}
	if n := len(d.AddRelations); n > 0 {
		parts = append(parts, fmt.Sprintf("add %d relation type(s)", n))
	}
	if n := len(d.AddOwns); n > 0 {
		parts = append(parts, fmt.Sprintf("add %d owns clause(s)", n))
	}
	if n := len(d.AddRelates); n > 0 {
		parts = append(parts, fmt.Sprintf("add %d relates clause(s)", n))
	}
	if len(d.RemoveTypes) > 0 {
		parts = append(parts, fmt.Sprintf("WARNING: %d type(s) in DB not in code: %s",
			len(d.RemoveTypes), strings.Join(d.RemoveTypes, ", ")))
	}
	if len(d.RemoveOwns) > 0 {
		parts = append(parts, fmt.Sprintf("WARNING: %d owns clause(s) in DB not in code", len(d.RemoveOwns)))
	}
	return strings.Join(parts, "; ")
}

// IsEmpty returns true if no schema differences were detected.
func (d *SchemaDiff) IsEmpty() bool {
	return len(d.AddAttributes) == 0 &&
		len(d.AddEntities) == 0 &&
		len(d.AddRelations) == 0 &&
		len(d.AddOwns) == 0 &&
		len(d.AddRelates) == 0 &&
		len(d.RemoveOwns) == 0 &&
		len(d.RemoveTypes) == 0
}

// GenerateMigration produces a slice of TypeQL 'define' statements required
// to reconcile the database schema with the Go models.
func (d *SchemaDiff) GenerateMigration() []string {
	var stmts []string

	// Add new attributes
	for _, a := range d.AddAttributes {
		stmts = append(stmts, fmt.Sprintf("define attribute %s, value %s;", a.Name, a.ValueType))
	}

	// Add new entity/relation types
	for _, e := range d.AddEntities {
		stmts = append(stmts, "define "+e.TypeQL)
	}
	for _, r := range d.AddRelations {
		stmts = append(stmts, "define "+r.TypeQL)
	}

	// Add owns clauses to existing types
	for _, o := range d.AddOwns {
		annots := ""
		if o.Annots != "" {
			annots = " " + o.Annots
		}
		// TypeDB 3.x: alter type add owns
		stmts = append(stmts, fmt.Sprintf("define %s owns %s%s;", o.TypeName, o.Attribute, annots))
	}

	// Add relates clauses to existing relations
	for _, r := range d.AddRelates {
		card := ""
		if r.Card != "" {
			card = " @card(" + r.Card + ")"
		}
		stmts = append(stmts, fmt.Sprintf("define %s relates %s%s;", r.TypeName, r.Role, card))
	}

	return stmts
}

// IntrospectSchemaFromString parses a TypeQL schema string into a ParsedSchema structure.
func IntrospectSchemaFromString(schemaStr string) (*tqlgen.ParsedSchema, error) {
	if schemaStr == "" {
		return &tqlgen.ParsedSchema{}, nil
	}
	return tqlgen.ParseSchema(schemaStr)
}

// DiffSchema compares two parsed schemas and returns a SchemaDiff representing
// the changes needed to transform the current schema into the desired schema.
func DiffSchema(desired *tqlgen.ParsedSchema, current *tqlgen.ParsedSchema) *SchemaDiff {
	diff := &SchemaDiff{}

	// Build lookup maps for current schema
	currentAttrs := make(map[string]bool)
	for _, a := range current.Attributes {
		currentAttrs[a.Name] = true
	}

	currentEntities := make(map[string]*tqlgen.EntitySpec)
	for i := range current.Entities {
		currentEntities[current.Entities[i].Name] = &current.Entities[i]
	}

	currentRelations := make(map[string]*tqlgen.RelationSpec)
	for i := range current.Relations {
		currentRelations[current.Relations[i].Name] = &current.Relations[i]
	}

	// Check desired attributes
	for _, a := range desired.Attributes {
		if !currentAttrs[a.Name] {
			diff.AddAttributes = append(diff.AddAttributes, AttrChange{
				Name:      a.Name,
				ValueType: a.ValueType,
			})
		}
	}

	// Check desired entities
	desiredEntities := make(map[string]bool)
	for _, e := range desired.Entities {
		desiredEntities[e.Name] = true
		cur, exists := currentEntities[e.Name]
		if !exists {
			diff.AddEntities = append(diff.AddEntities, TypeChange{
				TypeQL: buildEntityDefine(e),
			})
			continue
		}
		// Entity exists — check for new owns
		curOwns := make(map[string]bool)
		for _, o := range cur.Owns {
			curOwns[o.Attribute] = true
		}
		for _, o := range e.Owns {
			if !curOwns[o.Attribute] {
				diff.AddOwns = append(diff.AddOwns, OwnsChange{
					TypeName:  e.Name,
					Attribute: o.Attribute,
					Annots:    buildOwnsAnnots(o),
				})
			}
		}
	}

	// Check desired relations
	desiredRelations := make(map[string]bool)
	for _, r := range desired.Relations {
		desiredRelations[r.Name] = true
		cur, exists := currentRelations[r.Name]
		if !exists {
			diff.AddRelations = append(diff.AddRelations, TypeChange{
				TypeQL: buildRelationDefine(r),
			})
			continue
		}
		// Relation exists — check for new relates/owns
		curRelates := make(map[string]bool)
		for _, rel := range cur.Relates {
			curRelates[rel.Role] = true
		}
		for _, rel := range r.Relates {
			if !curRelates[rel.Role] {
				diff.AddRelates = append(diff.AddRelates, RelatesChange{
					TypeName: r.Name,
					Role:     rel.Role,
					Card:     rel.Card,
				})
			}
		}
		curOwns := make(map[string]bool)
		for _, o := range cur.Owns {
			curOwns[o.Attribute] = true
		}
		for _, o := range r.Owns {
			if !curOwns[o.Attribute] {
				diff.AddOwns = append(diff.AddOwns, OwnsChange{
					TypeName:  r.Name,
					Attribute: o.Attribute,
					Annots:    buildOwnsAnnots(o),
				})
			}
		}
	}

	// Detect removals (informational only)
	for name := range currentEntities {
		if !desiredEntities[name] {
			diff.RemoveTypes = append(diff.RemoveTypes, name)
		}
	}
	for name := range currentRelations {
		if !desiredRelations[name] {
			diff.RemoveTypes = append(diff.RemoveTypes, name)
		}
	}

	return diff
}

// DiffSchemaFromRegistry compares the currently registered Go models against
// the provided database schema.
func DiffSchemaFromRegistry(currentDB *tqlgen.ParsedSchema) *SchemaDiff {
	desired := registryToParseSchema()
	return DiffSchema(desired, currentDB)
}

// Migrate performs a schema migration by fetching the current database schema,
// comparing it with registered Go models, and applying any necessary additive changes.
func Migrate(ctx context.Context, db *Database) (*SchemaDiff, error) {
	schemaStr, err := db.Schema(ctx)
	if err != nil {
		return nil, fmt.Errorf("migrate: fetch schema: %w", err)
	}
	return MigrateFromSchema(ctx, db, schemaStr)
}

// MigrateFromSchema performs a schema migration using the provided schema string,
// comparing it with registered Go models, and applying any necessary additive changes.
func MigrateFromSchema(ctx context.Context, db *Database, currentSchemaStr string) (*SchemaDiff, error) {
	current, err := IntrospectSchemaFromString(currentSchemaStr)
	if err != nil {
		return nil, fmt.Errorf("migrate: parse current schema: %w", err)
	}

	diff := DiffSchemaFromRegistry(current)
	if diff.IsEmpty() {
		return diff, nil
	}

	stmts := diff.GenerateMigration()
	for _, stmt := range stmts {
		if err := db.ExecuteSchema(ctx, stmt); err != nil {
			return diff, fmt.Errorf("migrate: execute %q: %w", stmt, err)
		}
	}

	return diff, nil
}

// MigrateFromEmpty applies the complete schema defined by registered Go models
// to an empty database.
func MigrateFromEmpty(ctx context.Context, db *Database) error {
	schema := GenerateSchema()
	if schema == "" {
		return nil
	}
	return db.ExecuteSchema(ctx, schema)
}

// SyncSchemaOption configures SyncSchema behavior.
type SyncSchemaOption func(*syncSchemaConfig)

type syncSchemaConfig struct {
	force       bool
	skipIfMatch bool
}

// WithForce enables destructive changes (removing types/attributes).
func WithForce() SyncSchemaOption {
	return func(c *syncSchemaConfig) { c.force = true }
}

// WithSkipIfExists skips the migration if the schema already matches.
func WithSkipIfExists() SyncSchemaOption {
	return func(c *syncSchemaConfig) { c.skipIfMatch = true }
}

// SyncSchema performs a one-shot schema synchronization: introspect current DB
// schema, diff against registered Go models, and apply changes.
// Use WithForce() to also apply destructive changes (removals).
// Use WithSkipIfExists() to skip if the schema already matches.
func SyncSchema(ctx context.Context, db *Database, opts ...SyncSchemaOption) (*SchemaDiff, error) {
	cfg := syncSchemaConfig{}
	for _, o := range opts {
		o(&cfg)
	}

	schemaStr, err := db.Schema(ctx)
	if err != nil {
		return nil, fmt.Errorf("sync schema: fetch schema: %w", err)
	}

	current, err := IntrospectSchemaFromString(schemaStr)
	if err != nil {
		return nil, fmt.Errorf("sync schema: parse current schema: %w", err)
	}

	diff := DiffSchemaFromRegistry(current)

	if cfg.skipIfMatch && diff.IsEmpty() && !diff.HasBreakingChanges() {
		return diff, nil
	}

	if diff.IsEmpty() && (!cfg.force || !diff.HasBreakingChanges()) {
		return diff, nil
	}

	var stmts []string
	if cfg.force {
		stmts = diff.GenerateMigrationWithOpts(WithDestructive())
	} else {
		stmts = diff.GenerateMigration()
	}

	for _, stmt := range stmts {
		if err := db.ExecuteSchema(ctx, stmt); err != nil {
			return diff, fmt.Errorf("sync schema: execute %q: %w", stmt, err)
		}
	}

	return diff, nil
}

// --- Helpers ---

// registryToParseSchema converts the registered types into a tqlgen.ParsedSchema
// for comparison with the database schema.
func registryToParseSchema() *tqlgen.ParsedSchema {
	types := RegisteredTypes()
	schema := &tqlgen.ParsedSchema{}

	attrsSeen := make(map[string]bool)
	for _, info := range types {
		for _, f := range info.Fields {
			if attrsSeen[f.Tag.Name] {
				continue
			}
			attrsSeen[f.Tag.Name] = true
			schema.Attributes = append(schema.Attributes, tqlgen.AttributeSpec{
				Name:      f.Tag.Name,
				ValueType: f.ValueType,
			})
		}

		if info.Kind == ModelKindEntity {
			e := tqlgen.EntitySpec{
				Name:     info.TypeName,
				Abstract: info.IsAbstract,
				Parent:   info.Supertype,
			}
			for _, f := range info.Fields {
				e.Owns = append(e.Owns, fieldToOwns(f))
			}
			schema.Entities = append(schema.Entities, e)
		} else {
			r := tqlgen.RelationSpec{
				Name:     info.TypeName,
				Abstract: info.IsAbstract,
				Parent:   info.Supertype,
			}
			for _, role := range info.Roles {
				r.Relates = append(r.Relates, tqlgen.RelatesSpec{
					Role: role.RoleName,
				})
			}
			for _, f := range info.Fields {
				r.Owns = append(r.Owns, fieldToOwns(f))
			}
			schema.Relations = append(schema.Relations, r)
		}
	}

	return schema
}

func fieldToOwns(f FieldInfo) tqlgen.OwnsSpec {
	o := tqlgen.OwnsSpec{
		Attribute: f.Tag.Name,
		Key:       f.Tag.Key,
		Unique:    f.Tag.Unique,
	}
	if f.Tag.CardMin != nil || f.Tag.CardMax != nil {
		o.Card = formatCardString(f.Tag.CardMin, f.Tag.CardMax)
	}
	return o
}

func formatCardString(min, max *int) string {
	if min == nil && max == nil {
		return ""
	}
	minStr := "0"
	if min != nil {
		minStr = fmt.Sprintf("%d", *min)
	}
	if max == nil {
		return minStr + ".."
	}
	return fmt.Sprintf("%s..%d", minStr, *max)
}

func buildOwnsAnnots(o tqlgen.OwnsSpec) string {
	var parts []string
	if o.Key {
		parts = append(parts, "@key")
	}
	if o.Unique {
		parts = append(parts, "@unique")
	}
	if o.Card != "" {
		parts = append(parts, "@card("+o.Card+")")
	}
	return strings.Join(parts, " ")
}

func buildEntityDefine(e tqlgen.EntitySpec) string {
	var lines []string
	header := "entity " + e.Name
	if e.Abstract {
		header += " @abstract"
	}
	if e.Parent != "" {
		header += " sub " + e.Parent
	}
	lines = append(lines, header)

	for _, o := range e.Owns {
		line := "    owns " + o.Attribute
		annots := buildOwnsAnnots(o)
		if annots != "" {
			line += " " + annots
		}
		lines = append(lines, line)
	}

	return strings.Join(lines, ",\n") + ";"
}

func buildRelationDefine(r tqlgen.RelationSpec) string {
	var lines []string
	header := "relation " + r.Name
	if r.Abstract {
		header += " @abstract"
	}
	if r.Parent != "" {
		header += " sub " + r.Parent
	}
	lines = append(lines, header)

	for _, rel := range r.Relates {
		line := "    relates " + rel.Role
		if rel.Card != "" {
			line += " @card(" + rel.Card + ")"
		}
		lines = append(lines, line)
	}
	for _, o := range r.Owns {
		line := "    owns " + o.Attribute
		annots := buildOwnsAnnots(o)
		if annots != "" {
			line += " " + annots
		}
		lines = append(lines, line)
	}

	return strings.Join(lines, ",\n") + ";"
}
