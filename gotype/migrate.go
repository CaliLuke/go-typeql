// Package gotype provides automated schema migration capabilities.
package gotype

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/CaliLuke/go-typeql/tqlgen"
)

// internalMigrationTypes lists the schema type names owned by go-typeql's own
// migration trackers (see migrationSchemaSQL and seqMigrationSchemaSQL). They
// are excluded from DiffSchema's removal and staleness scans so that a diff
// never reports them as drift and a force sync never destroys its own
// migration history (issue #58).
var internalMigrationTypes = map[string]bool{
	"migration-record":       true,
	"migration-hash":         true,
	"migration-summary":      true,
	"migration-applied-at":   true,
	seqMigrationEntity:       true,
	seqMigrationNameAttr:     true,
	seqMigrationTimeAttr:     true,
	seqMigrationChecksumAttr: true,
}

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
	// AddPlays are new role-player declarations (entity/relation plays
	// relation:role) to be added to existing types.
	AddPlays []PlaysChange
	// ModifyOwns are ownership annotation changes that can be applied in
	// place with a redefine statement (currently: cardinality changes where
	// both sides declare an explicit @card).
	ModifyOwns []OwnsModify
	// RemoveOwns identifies attribute ownerships present in the DB but not in the code.
	RemoveOwns []OwnsChange
	// RemoveTypes identifies entity and relation types present in the DB but not in the code.
	RemoveTypes []string
	// RemoveAttributes identifies attribute types present in the DB but not in the code.
	RemoveAttributes []string
	// Unsupported lists detected changes that the diff engine cannot apply
	// automatically (value-type changes, @key/@unique toggles, supertype or
	// abstractness changes, role cardinality changes). They are reported by
	// Summary and BreakingChanges instead of being silently ignored, and must
	// be applied manually.
	Unsupported []BreakingChange
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

// PlaysChange describes a plays clause (TypeName plays Relation:Role) to be
// added to an existing type.
type PlaysChange struct {
	TypeName string
	Relation string
	Role     string
}

// OwnsModify describes an in-place annotation change on an existing
// ownership, applied with a redefine statement.
type OwnsModify struct {
	TypeName  string
	Attribute string
	OldAnnots string
	NewAnnots string
}

// Summary returns a human-readable description of the changes in the diff.
func (d *SchemaDiff) Summary() string {
	if d.IsEmpty() && len(d.Unsupported) == 0 {
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
	if n := len(d.AddPlays); n > 0 {
		parts = append(parts, fmt.Sprintf("add %d plays clause(s)", n))
	}
	if n := len(d.ModifyOwns); n > 0 {
		parts = append(parts, fmt.Sprintf("modify %d owns clause(s)", n))
	}
	if len(d.RemoveTypes) > 0 {
		parts = append(parts, fmt.Sprintf("WARNING: %d type(s) in DB not in code: %s",
			len(d.RemoveTypes), strings.Join(d.RemoveTypes, ", ")))
	}
	if len(d.RemoveAttributes) > 0 {
		parts = append(parts, fmt.Sprintf("WARNING: %d attribute(s) in DB not in code: %s",
			len(d.RemoveAttributes), strings.Join(d.RemoveAttributes, ", ")))
	}
	if len(d.RemoveOwns) > 0 {
		parts = append(parts, fmt.Sprintf("WARNING: %d owns clause(s) in DB not in code", len(d.RemoveOwns)))
	}
	if len(d.Unsupported) > 0 {
		details := make([]string, len(d.Unsupported))
		for i, u := range d.Unsupported {
			details[i] = u.Detail
		}
		parts = append(parts, fmt.Sprintf("WARNING: %d unsupported change(s): %s",
			len(d.Unsupported), strings.Join(details, "; ")))
	}
	return strings.Join(parts, "; ")
}

// IsEmpty returns true if the diff contains no applicable schema changes.
// Unsupported changes are not counted — they cannot be applied automatically —
// but they still surface through Summary, BreakingChanges, and
// HasBreakingChanges.
func (d *SchemaDiff) IsEmpty() bool {
	return len(d.AddAttributes) == 0 &&
		len(d.AddEntities) == 0 &&
		len(d.AddRelations) == 0 &&
		len(d.AddOwns) == 0 &&
		len(d.AddRelates) == 0 &&
		len(d.AddPlays) == 0 &&
		len(d.ModifyOwns) == 0 &&
		len(d.RemoveOwns) == 0 &&
		len(d.RemoveTypes) == 0 &&
		len(d.RemoveAttributes) == 0
}

// GenerateMigration produces the additive TypeQL statements (define and
// redefine) required to reconcile the database schema with the Go models.
// Statements are returned individually for review; execution should go
// through Plan, which batches them into atomic queries.
func (d *SchemaDiff) GenerateMigration() []string {
	var stmts []string
	for _, op := range d.Operations() {
		stmts = append(stmts, op.ToTypeQL())
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
//
// Beyond presence/absence, DiffSchema compares attribute value types, owns
// annotations (@key, @unique, @card), plays clauses, supertypes, and
// abstractness. Changes it cannot apply automatically are reported in
// Unsupported rather than silently dropped (issue #56). go-typeql's internal
// migration-tracking types are never reported as removals (issue #58).
func DiffSchema(desired *tqlgen.ParsedSchema, current *tqlgen.ParsedSchema) *SchemaDiff {
	diff := &SchemaDiff{}

	currentAttrs := make(map[string]string, len(current.Attributes))
	for _, a := range current.Attributes {
		currentAttrs[a.Name] = a.ValueType
	}
	currentEntities := make(map[string]*tqlgen.EntitySpec)
	for i := range current.Entities {
		currentEntities[current.Entities[i].Name] = &current.Entities[i]
	}
	currentRelations := make(map[string]*tqlgen.RelationSpec)
	for i := range current.Relations {
		currentRelations[current.Relations[i].Name] = &current.Relations[i]
	}

	desiredAttrs := make(map[string]bool, len(desired.Attributes))
	for _, a := range desired.Attributes {
		desiredAttrs[a.Name] = true
		curType, exists := currentAttrs[a.Name]
		if !exists {
			diff.AddAttributes = append(diff.AddAttributes, AttrChange{Name: a.Name, ValueType: a.ValueType})
			continue
		}
		if curType != a.ValueType {
			diff.Unsupported = append(diff.Unsupported, BreakingChange{
				Type:   "type_change",
				Entity: a.Name,
				Detail: fmt.Sprintf("unsupported change: attribute %q value type %s -> %s cannot be changed in place (migrate the data manually)", a.Name, curType, a.ValueType),
			})
		}
	}

	desiredEntities := diffEntities(diff, desired.Entities, currentEntities)
	desiredRelations := diffRelations(diff, desired.Relations, currentRelations)

	// Reverse scans: schema elements present in the DB but absent from the
	// code. Iterate the parsed slices (not the lookup maps) and sort the
	// results so plans are deterministic.
	for i := range current.Entities {
		name := current.Entities[i].Name
		if !desiredEntities[name] && !internalMigrationTypes[name] {
			diff.RemoveTypes = append(diff.RemoveTypes, name)
		}
	}
	for i := range current.Relations {
		name := current.Relations[i].Name
		if !desiredRelations[name] && !internalMigrationTypes[name] {
			diff.RemoveTypes = append(diff.RemoveTypes, name)
		}
	}
	for _, a := range current.Attributes {
		if !desiredAttrs[a.Name] && !internalMigrationTypes[a.Name] {
			diff.RemoveAttributes = append(diff.RemoveAttributes, a.Name)
		}
	}

	sort.Strings(diff.RemoveTypes)
	sort.Strings(diff.RemoveAttributes)
	sort.Slice(diff.RemoveOwns, func(i, j int) bool {
		if diff.RemoveOwns[i].TypeName != diff.RemoveOwns[j].TypeName {
			return diff.RemoveOwns[i].TypeName < diff.RemoveOwns[j].TypeName
		}
		return diff.RemoveOwns[i].Attribute < diff.RemoveOwns[j].Attribute
	})

	return diff
}

func diffEntities(diff *SchemaDiff, desired []tqlgen.EntitySpec, currentEntities map[string]*tqlgen.EntitySpec) map[string]bool {
	seen := make(map[string]bool, len(desired))
	for _, e := range desired {
		seen[e.Name] = true
		cur, exists := currentEntities[e.Name]
		if !exists {
			diff.AddEntities = append(diff.AddEntities, TypeChange{TypeQL: buildEntityDefine(e)})
			continue
		}
		diffTypeHeader(diff, "entity", e.Name, e.Parent, cur.Parent, e.Abstract, cur.Abstract)
		diffOwns(diff, e.Name, e.Owns, cur.Owns)
		diffPlays(diff, e.Name, e.Plays, cur.Plays)
	}
	return seen
}

func diffRelations(diff *SchemaDiff, desired []tqlgen.RelationSpec, currentRelations map[string]*tqlgen.RelationSpec) map[string]bool {
	seen := make(map[string]bool, len(desired))
	for _, r := range desired {
		seen[r.Name] = true
		cur, exists := currentRelations[r.Name]
		if !exists {
			diff.AddRelations = append(diff.AddRelations, TypeChange{TypeQL: buildRelationDefine(r)})
			continue
		}
		diffTypeHeader(diff, "relation", r.Name, r.Parent, cur.Parent, r.Abstract, cur.Abstract)
		curRelates := make(map[string]tqlgen.RelatesSpec, len(cur.Relates))
		for _, rel := range cur.Relates {
			curRelates[rel.Role] = rel
		}
		for _, rel := range r.Relates {
			curRel, ok := curRelates[rel.Role]
			if !ok {
				diff.AddRelates = append(diff.AddRelates, RelatesChange{
					TypeName: r.Name,
					Role:     rel.Role,
					Card:     rel.Card,
				})
				continue
			}
			if rel.Card != "" && curRel.Card != "" && rel.Card != curRel.Card {
				diff.Unsupported = append(diff.Unsupported, BreakingChange{
					Type:   "cardinality_change",
					Entity: r.Name,
					Detail: fmt.Sprintf("unsupported change: role %s:%s cardinality %s -> %s must be applied manually (redefine)", r.Name, rel.Role, curRel.Card, rel.Card),
				})
			}
		}
		diffOwns(diff, r.Name, r.Owns, cur.Owns)
		diffPlays(diff, r.Name, r.Plays, cur.Plays)
	}
	return seen
}

// diffTypeHeader compares the header-level properties of a type present on
// both sides. Supertype and abstractness changes cannot be applied
// automatically and are reported as unsupported.
func diffTypeHeader(diff *SchemaDiff, kind, name, desiredParent, curParent string, desiredAbstract, curAbstract bool) {
	if desiredParent != curParent {
		diff.Unsupported = append(diff.Unsupported, BreakingChange{
			Type:   "type_change",
			Entity: name,
			Detail: fmt.Sprintf("unsupported change: %s %q supertype %s -> %s cannot be changed automatically", kind, name, orNone(curParent), orNone(desiredParent)),
		})
	}
	if desiredAbstract != curAbstract {
		diff.Unsupported = append(diff.Unsupported, BreakingChange{
			Type:   "type_change",
			Entity: name,
			Detail: fmt.Sprintf("unsupported change: %s %q abstractness %t -> %t cannot be changed automatically", kind, name, curAbstract, desiredAbstract),
		})
	}
}

func orNone(s string) string {
	if s == "" {
		return "(none)"
	}
	return s
}

// diffOwns compares the ownerships of a type present on both sides: new
// ownerships become AddOwns, ownerships present only in the DB become
// RemoveOwns (issue #57), and annotation differences on common ownerships
// become ModifyOwns or Unsupported entries (issue #56).
func diffOwns(diff *SchemaDiff, typeName string, desired, current []tqlgen.OwnsSpec) {
	cur := make(map[string]tqlgen.OwnsSpec, len(current))
	for _, o := range current {
		cur[o.Attribute] = o
	}
	des := make(map[string]bool, len(desired))
	for _, o := range desired {
		des[o.Attribute] = true
		c, exists := cur[o.Attribute]
		if !exists {
			diff.AddOwns = append(diff.AddOwns, OwnsChange{
				TypeName:  typeName,
				Attribute: o.Attribute,
				Annots:    buildOwnsAnnots(o),
			})
			continue
		}
		diffOwnsAnnotations(diff, typeName, o, c)
	}
	for _, o := range current {
		if !des[o.Attribute] {
			diff.RemoveOwns = append(diff.RemoveOwns, OwnsChange{
				TypeName:  typeName,
				Attribute: o.Attribute,
			})
		}
	}
}

// diffOwnsAnnotations compares annotations on an ownership present on both
// sides. A cardinality change where both sides declare an explicit @card is
// applied in place via redefine (ModifyOwns); everything else that differs is
// reported as unsupported. Cardinality declared only in the DB is left alone —
// the Go model does not manage it.
func diffOwnsAnnotations(diff *SchemaDiff, typeName string, desired, current tqlgen.OwnsSpec) {
	if desired.Key != current.Key {
		diff.Unsupported = append(diff.Unsupported, BreakingChange{
			Type:   "annotation_change",
			Entity: typeName,
			Detail: fmt.Sprintf("unsupported change: @key on %s.%s cannot be toggled automatically", typeName, desired.Attribute),
		})
		return // @key implies uniqueness and cardinality; avoid stacked noise
	}
	if desired.Unique != current.Unique {
		diff.Unsupported = append(diff.Unsupported, BreakingChange{
			Type:   "annotation_change",
			Entity: typeName,
			Detail: fmt.Sprintf("unsupported change: @unique on %s.%s cannot be toggled automatically", typeName, desired.Attribute),
		})
	}
	switch {
	case desired.Card != "" && current.Card != "" && desired.Card != current.Card:
		diff.ModifyOwns = append(diff.ModifyOwns, OwnsModify{
			TypeName:  typeName,
			Attribute: desired.Attribute,
			OldAnnots: "@card(" + current.Card + ")",
			NewAnnots: "@card(" + desired.Card + ")",
		})
	case desired.Card != "" && current.Card == "" && !desired.Key:
		diff.Unsupported = append(diff.Unsupported, BreakingChange{
			Type:   "cardinality_change",
			Entity: typeName,
			Detail: fmt.Sprintf("unsupported change: adding @card(%s) to existing ownership %s.%s must be applied manually (redefine)", desired.Card, typeName, desired.Attribute),
		})
	}
}

// diffPlays compares the plays clauses of a type present on both sides and
// records missing role-player declarations (issue #34).
func diffPlays(diff *SchemaDiff, typeName string, desired, current []tqlgen.PlaysSpec) {
	cur := make(map[string]bool, len(current))
	for _, p := range current {
		cur[p.Relation+":"+p.Role] = true
	}
	for _, p := range desired {
		if !cur[p.Relation+":"+p.Role] {
			diff.AddPlays = append(diff.AddPlays, PlaysChange{
				TypeName: typeName,
				Relation: p.Relation,
				Role:     p.Role,
			})
		}
	}
}

// DiffSchemaFromRegistry compares the currently registered Go models against
// the provided database schema.
func DiffSchemaFromRegistry(currentDB *tqlgen.ParsedSchema) *SchemaDiff {
	desired := registryToParseSchema()
	return DiffSchema(desired, currentDB)
}

// --- Migration plans ---

// MigrationPlan is a reviewable preview of the schema changes that Migrate,
// MigrateWithState, or SyncSchema would execute. Build one with SchemaDiff.Plan
// or PlanSchema; nothing is executed until the plan's queries are run.
type MigrationPlan struct {
	// Diff is the schema diff the plan was built from.
	Diff *SchemaDiff
	// Statements are the individual TypeQL statements in application order,
	// suitable for review and logging.
	Statements []string
	// Queries are the batched queries that are executed atomically in a
	// single schema transaction: at most one define block, one redefine
	// statement per ownership modification, and (when destructive changes
	// are included) one undefine block.
	Queries []string
}

// IsEmpty returns true when the plan contains nothing to execute.
func (p *MigrationPlan) IsEmpty() bool { return len(p.Queries) == 0 }

// Summary returns the human-readable summary of the underlying diff.
func (p *MigrationPlan) Summary() string { return p.Diff.Summary() }

// Plan converts the diff into an executable migration plan. By default only
// additive changes (define/redefine) are included; pass WithDestructive() to
// also include the undefine block for removals.
func (d *SchemaDiff) Plan(opts ...MigrateOption) *MigrationPlan {
	cfg := migrateConfig{}
	for _, o := range opts {
		o(&cfg)
	}

	plan := &MigrationPlan{Diff: d}
	var defineBody, redefines, undefineBody []string
	for _, op := range d.Operations() {
		s := op.ToTypeQL()
		plan.Statements = append(plan.Statements, s)
		if body, ok := strings.CutPrefix(s, "define "); ok {
			defineBody = append(defineBody, body)
		} else {
			// redefine statements execute as standalone queries.
			redefines = append(redefines, s)
		}
	}
	if cfg.destructive {
		for _, op := range d.DestructiveOperations() {
			s := op.ToTypeQL()
			plan.Statements = append(plan.Statements, s)
			undefineBody = append(undefineBody, strings.TrimPrefix(s, "undefine "))
		}
	}

	if len(defineBody) > 0 {
		plan.Queries = append(plan.Queries, "define\n"+strings.Join(defineBody, "\n"))
	}
	plan.Queries = append(plan.Queries, redefines...)
	if len(undefineBody) > 0 {
		plan.Queries = append(plan.Queries, "undefine\n"+strings.Join(undefineBody, "\n"))
	}
	return plan
}

// executePlan runs the plan's queries — plus any extra data-write queries —
// in a single schema transaction and commits them atomically (issue #55).
// TypeDB 3.x schema transactions accept data writes, which lets migration
// state records commit together with the schema changes they describe.
func executePlan(ctx context.Context, db *Database, plan *MigrationPlan, extra ...string) error {
	queries := make([]string, 0, len(plan.Queries)+len(extra))
	queries = append(queries, plan.Queries...)
	queries = append(queries, extra...)
	if len(queries) == 0 {
		return nil
	}

	tx, err := db.TransactionContext(ctx, SchemaTransaction)
	if err != nil {
		return fmt.Errorf("open schema transaction: %w", err)
	}
	defer tx.Close()

	for _, q := range queries {
		if _, err := tx.QueryWithContext(ctx, q); err != nil {
			return fmt.Errorf("execute %q: %w", q, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	return nil
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
// comparing it with registered Go models, and applying any necessary additive
// changes atomically in a single schema transaction.
func MigrateFromSchema(ctx context.Context, db *Database, currentSchemaStr string) (*SchemaDiff, error) {
	current, err := IntrospectSchemaFromString(currentSchemaStr)
	if err != nil {
		return nil, fmt.Errorf("migrate: parse current schema: %w", err)
	}

	diff := DiffSchemaFromRegistry(current)
	plan := diff.Plan()
	if plan.IsEmpty() {
		return diff, nil
	}

	if err := executePlan(ctx, db, plan); err != nil {
		return diff, fmt.Errorf("migrate: %w", err)
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

// SyncSchemaOption configures SyncSchema and PlanSchema behavior.
type SyncSchemaOption func(*syncSchemaConfig)

type syncSchemaConfig struct {
	force        bool
	skipIfExists bool
}

// WithForce enables destructive changes (removing types, attributes, and
// ownerships). Preview what would be destroyed first with PlanSchema.
//
// TypeDB refuses to undefine a type that still has instances: delete the
// data first, or the sync fails with a schema validation error. go-typeql's
// internal migration-tracking types are never removed.
func WithForce() SyncSchemaOption {
	return func(c *syncSchemaConfig) { c.force = true }
}

// WithSkipIfExists makes SyncSchema a no-op when the database already
// contains user-defined schema types (go-typeql's internal migration-tracking
// types are ignored). Use it to bootstrap a schema exactly once: an empty
// database gets the full schema, an already-provisioned database is left
// untouched and the computed diff is returned for inspection.
func WithSkipIfExists() SyncSchemaOption {
	return func(c *syncSchemaConfig) { c.skipIfExists = true }
}

// schemaHasUserTypes reports whether the parsed schema declares any type
// beyond go-typeql's internal migration-tracking types.
func schemaHasUserTypes(s *tqlgen.ParsedSchema) bool {
	for _, a := range s.Attributes {
		if !internalMigrationTypes[a.Name] {
			return true
		}
	}
	for i := range s.Entities {
		if !internalMigrationTypes[s.Entities[i].Name] {
			return true
		}
	}
	for i := range s.Relations {
		if !internalMigrationTypes[s.Relations[i].Name] {
			return true
		}
	}
	return false
}

// planSchema fetches and parses the current DB schema and builds the plan the
// given configuration would execute.
func planSchema(ctx context.Context, db *Database, cfg syncSchemaConfig) (*MigrationPlan, *tqlgen.ParsedSchema, error) {
	schemaStr, err := db.Schema(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("fetch schema: %w", err)
	}
	current, err := IntrospectSchemaFromString(schemaStr)
	if err != nil {
		return nil, nil, fmt.Errorf("parse current schema: %w", err)
	}
	diff := DiffSchemaFromRegistry(current)
	if cfg.force {
		return diff.Plan(WithDestructive()), current, nil
	}
	return diff.Plan(), current, nil
}

// PlanSchema computes the migration plan that SyncSchema would execute with
// the same options, without executing anything. Use it to review the exact
// statement list — especially the undefine block produced by WithForce() —
// before applying it (issue #62).
func PlanSchema(ctx context.Context, db *Database, opts ...SyncSchemaOption) (*MigrationPlan, error) {
	cfg := syncSchemaConfig{}
	for _, o := range opts {
		o(&cfg)
	}
	plan, _, err := planSchema(ctx, db, cfg)
	if err != nil {
		return nil, fmt.Errorf("plan schema: %w", err)
	}
	return plan, nil
}

// SyncSchema performs a one-shot schema synchronization: introspect current DB
// schema, diff against registered Go models, and apply the changes atomically
// in a single schema transaction.
// Use WithForce() to also apply destructive changes (removals) — preview them
// first with PlanSchema.
// Use WithSkipIfExists() to only apply when the database has no user-defined
// schema yet.
func SyncSchema(ctx context.Context, db *Database, opts ...SyncSchemaOption) (*SchemaDiff, error) {
	cfg := syncSchemaConfig{}
	for _, o := range opts {
		o(&cfg)
	}

	plan, current, err := planSchema(ctx, db, cfg)
	if err != nil {
		return nil, fmt.Errorf("sync schema: %w", err)
	}

	if cfg.skipIfExists && schemaHasUserTypes(current) {
		return plan.Diff, nil
	}
	if plan.IsEmpty() {
		return plan.Diff, nil
	}

	if err := executePlan(ctx, db, plan); err != nil {
		return plan.Diff, fmt.Errorf("sync schema: %w", err)
	}
	return plan.Diff, nil
}

// --- Helpers ---

// registryToParseSchema converts the registered types into a tqlgen.ParsedSchema
// for comparison with the database schema.
func registryToParseSchema() *tqlgen.ParsedSchema {
	types := RegisteredTypes()
	schema := &tqlgen.ParsedSchema{}

	// Role-player declarations, mirrored from relation role metadata the same
	// way GenerateSchema's buildPlaysMap does (issue #34).
	playsByType := make(map[string][]tqlgen.PlaysSpec)
	for _, info := range types {
		if info.Kind != ModelKindRelation {
			continue
		}
		for _, role := range info.Roles {
			playsByType[role.PlayerTypeName] = append(playsByType[role.PlayerTypeName], tqlgen.PlaysSpec{
				Relation: info.TypeName,
				Role:     role.RoleName,
			})
		}
	}

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
				Doc:      info.Doc,
				Meta:     modelMetaToTQL(info.Meta),
				Plays:    playsByType[info.TypeName],
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
				Doc:      info.Doc,
				Meta:     modelMetaToTQL(info.Meta),
				Plays:    playsByType[info.TypeName],
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

func modelMetaToTQL(meta []Meta) []tqlgen.MetaSpec {
	if len(meta) == 0 {
		return nil
	}
	out := make([]tqlgen.MetaSpec, 0, len(meta))
	for _, item := range meta {
		out = append(out, tqlgen.MetaSpec{Key: item.Key, Value: item.Value})
	}
	return out
}

func fieldToOwns(f FieldInfo) tqlgen.OwnsSpec {
	o := tqlgen.OwnsSpec{
		Attribute: f.Tag.Name,
		Key:       f.Tag.Key,
		Unique:    f.Tag.Unique,
		Doc:       f.Doc,
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
	if o.Doc != "" {
		parts = append(parts, docAnnotation(o.Doc))
	}
	return strings.Join(parts, " ")
}

// buildEntityDefine renders the body of a define statement for a new entity
// type. Annotations attach to the type name and `sub <parent>` is emitted as
// a separate constraint: TypeDB 3.x treats annotations placed after `sub` as
// annotations on the sub clause itself and rejects them (issue #32).
func buildEntityDefine(e tqlgen.EntitySpec) string {
	var lines []string
	header := "entity " + e.Name
	if e.Abstract {
		header += " @abstract"
	}
	if e.Doc != "" {
		header += " " + docAnnotation(e.Doc)
	}
	for _, meta := range e.Meta {
		header += " " + metaAnnotation(meta.Key, meta.Value)
	}
	lines = append(lines, header)

	if e.Parent != "" {
		lines = append(lines, "    sub "+e.Parent)
	}
	for _, p := range e.Plays {
		lines = append(lines, "    plays "+p.Relation+":"+p.Role)
	}
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

// buildRelationDefine renders the body of a define statement for a new
// relation type. Annotations attach to the type name and `sub <parent>` is
// emitted as a separate constraint: TypeDB 3.x treats annotations placed
// after `sub` as annotations on the sub clause itself and rejects them
// (issue #32).
func buildRelationDefine(r tqlgen.RelationSpec) string {
	var lines []string
	header := "relation " + r.Name
	if r.Abstract {
		header += " @abstract"
	}
	if r.Doc != "" {
		header += " " + docAnnotation(r.Doc)
	}
	for _, meta := range r.Meta {
		header += " " + metaAnnotation(meta.Key, meta.Value)
	}
	lines = append(lines, header)

	if r.Parent != "" {
		lines = append(lines, "    sub "+r.Parent)
	}
	for _, rel := range r.Relates {
		line := "    relates " + rel.Role
		if rel.Card != "" {
			line += " @card(" + rel.Card + ")"
		}
		lines = append(lines, line)
	}
	for _, p := range r.Plays {
		lines = append(lines, "    plays "+p.Relation+":"+p.Role)
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
