// Package gotype defines discrete migration operations.
package gotype

import "fmt"

// Operation defines the interface for a single, atomic schema migration step.
type Operation interface {
	// ToTypeQL returns the TypeQL statement required to perform the operation.
	ToTypeQL() string
	// IsReversible returns true if the operation can be undone without data loss.
	IsReversible() bool
	// RollbackTypeQL returns the TypeQL statement required to undo the operation.
	RollbackTypeQL() string
	// IsDestructive returns true if the operation results in the deletion of schema elements or data.
	IsDestructive() bool
}

// AddAttribute represents the creation of a new attribute type in the schema.
type AddAttribute struct {
	Name      string
	ValueType string
}

func (op AddAttribute) ToTypeQL() string {
	return fmt.Sprintf("define attribute %s, value %s;", op.Name, op.ValueType)
}
func (op AddAttribute) IsReversible() bool  { return true }
func (op AddAttribute) IsDestructive() bool { return false }
func (op AddAttribute) RollbackTypeQL() string {
	return fmt.Sprintf("undefine attribute %s;", op.Name)
}

// AddEntity represents the creation of a new entity type in the schema.
type AddEntity struct {
	Name     string
	Parent   string
	Abstract bool
	TypeQL   string // The full define statement for the entity.
}

func (op AddEntity) ToTypeQL() string {
	if op.TypeQL != "" {
		return "define " + op.TypeQL
	}
	s := fmt.Sprintf("define entity %s", op.Name)
	if op.Abstract {
		s += " @abstract"
	}
	if op.Parent != "" {
		s += " sub " + op.Parent
	}
	return s + ";"
}
func (op AddEntity) IsReversible() bool    { return true }
func (op AddEntity) IsDestructive() bool   { return false }
func (op AddEntity) RollbackTypeQL() string { return fmt.Sprintf("undefine entity %s;", op.Name) }

// AddRelation represents the creation of a new relation type in the schema.
type AddRelation struct {
	Name     string
	Parent   string
	Abstract bool
	TypeQL   string // The full define statement for the relation.
}

func (op AddRelation) ToTypeQL() string {
	if op.TypeQL != "" {
		return "define " + op.TypeQL
	}
	s := fmt.Sprintf("define relation %s", op.Name)
	if op.Abstract {
		s += " @abstract"
	}
	if op.Parent != "" {
		s += " sub " + op.Parent
	}
	return s + ";"
}
func (op AddRelation) IsReversible() bool    { return true }
func (op AddRelation) IsDestructive() bool   { return false }
func (op AddRelation) RollbackTypeQL() string { return fmt.Sprintf("undefine relation %s;", op.Name) }

// AddOwnership represents the assignment of an attribute ownership to a type.
type AddOwnership struct {
	Owner     string
	Attribute string
	Annots    string
}

func (op AddOwnership) ToTypeQL() string {
	annots := ""
	if op.Annots != "" {
		annots = " " + op.Annots
	}
	return fmt.Sprintf("define %s owns %s%s;", op.Owner, op.Attribute, annots)
}
func (op AddOwnership) IsReversible() bool  { return true }
func (op AddOwnership) IsDestructive() bool { return false }
func (op AddOwnership) RollbackTypeQL() string {
	return fmt.Sprintf("undefine %s owns %s;", op.Owner, op.Attribute)
}

// AddRole represents the addition of a role to a relation type.
type AddRole struct {
	Relation string
	Role     string
	Card     string
}

func (op AddRole) ToTypeQL() string {
	card := ""
	if op.Card != "" {
		card = " @card(" + op.Card + ")"
	}
	return fmt.Sprintf("define %s relates %s%s;", op.Relation, op.Role, card)
}
func (op AddRole) IsReversible() bool    { return true }
func (op AddRole) IsDestructive() bool   { return false }
func (op AddRole) RollbackTypeQL() string { return fmt.Sprintf("undefine %s relates %s;", op.Relation, op.Role) }

// --- Destructive operations ---

// RemoveAttribute removes an attribute type.
type RemoveAttribute struct {
	Name string
}

func (op RemoveAttribute) ToTypeQL() string       { return fmt.Sprintf("undefine attribute %s;", op.Name) }
func (op RemoveAttribute) IsReversible() bool      { return false }
func (op RemoveAttribute) IsDestructive() bool     { return true }
func (op RemoveAttribute) RollbackTypeQL() string  { return "" }

// RemoveEntity removes an entity type.
type RemoveEntity struct {
	Name string
}

func (op RemoveEntity) ToTypeQL() string       { return fmt.Sprintf("undefine entity %s;", op.Name) }
func (op RemoveEntity) IsReversible() bool      { return false }
func (op RemoveEntity) IsDestructive() bool     { return true }
func (op RemoveEntity) RollbackTypeQL() string  { return "" }

// RemoveRelation removes a relation type.
type RemoveRelation struct {
	Name string
}

func (op RemoveRelation) ToTypeQL() string       { return fmt.Sprintf("undefine relation %s;", op.Name) }
func (op RemoveRelation) IsReversible() bool      { return false }
func (op RemoveRelation) IsDestructive() bool     { return true }
func (op RemoveRelation) RollbackTypeQL() string  { return "" }

// RemoveOwnership removes an owns clause from a type.
type RemoveOwnership struct {
	Owner     string
	Attribute string
}

func (op RemoveOwnership) ToTypeQL() string {
	return fmt.Sprintf("undefine %s owns %s;", op.Owner, op.Attribute)
}
func (op RemoveOwnership) IsReversible() bool    { return false }
func (op RemoveOwnership) IsDestructive() bool   { return true }
func (op RemoveOwnership) RollbackTypeQL() string { return "" }

// RemoveRole removes a relates clause from a relation type.
type RemoveRole struct {
	Relation string
	Role     string
}

func (op RemoveRole) ToTypeQL() string {
	return fmt.Sprintf("undefine %s relates %s;", op.Relation, op.Role)
}
func (op RemoveRole) IsReversible() bool    { return false }
func (op RemoveRole) IsDestructive() bool   { return true }
func (op RemoveRole) RollbackTypeQL() string { return "" }

// --- Role player operations ---

// AddRolePlayer represents adding a plays clause (entity plays relation:role).
type AddRolePlayer struct {
	Entity   string
	Relation string
	Role     string
}

func (op AddRolePlayer) ToTypeQL() string {
	return fmt.Sprintf("define %s plays %s:%s;", op.Entity, op.Relation, op.Role)
}
func (op AddRolePlayer) IsReversible() bool  { return true }
func (op AddRolePlayer) IsDestructive() bool { return false }
func (op AddRolePlayer) RollbackTypeQL() string {
	return fmt.Sprintf("undefine %s plays %s:%s;", op.Entity, op.Relation, op.Role)
}

// RemoveRolePlayer removes a plays clause from an entity type.
type RemoveRolePlayer struct {
	Entity   string
	Relation string
	Role     string
}

func (op RemoveRolePlayer) ToTypeQL() string {
	return fmt.Sprintf("undefine %s plays %s:%s;", op.Entity, op.Relation, op.Role)
}
func (op RemoveRolePlayer) IsReversible() bool    { return false }
func (op RemoveRolePlayer) IsDestructive() bool   { return true }
func (op RemoveRolePlayer) RollbackTypeQL() string { return "" }

// --- Modify ownership ---

// ModifyOwnership represents changing annotations on an existing owns clause.
type ModifyOwnership struct {
	Owner     string
	Attribute string
	OldAnnots string
	NewAnnots string
}

func (op ModifyOwnership) ToTypeQL() string {
	annots := ""
	if op.NewAnnots != "" {
		annots = " " + op.NewAnnots
	}
	return fmt.Sprintf("redefine %s owns %s%s;", op.Owner, op.Attribute, annots)
}
func (op ModifyOwnership) IsReversible() bool { return op.OldAnnots != "" }
func (op ModifyOwnership) IsDestructive() bool { return false }
func (op ModifyOwnership) RollbackTypeQL() string {
	if op.OldAnnots == "" {
		return ""
	}
	return fmt.Sprintf("redefine %s owns %s %s;", op.Owner, op.Attribute, op.OldAnnots)
}

// --- Rename attribute ---

// RenameAttribute represents renaming an attribute type.
// TypeDB has no native rename, so this generates a multi-step sequence:
// 1. Define new attribute
// 2. Reassign ownership from old to new
// Note: data migration must be handled separately.
type RenameAttribute struct {
	OldName   string
	NewName   string
	ValueType string
}

func (op RenameAttribute) ToTypeQL() string {
	return fmt.Sprintf("define attribute %s, value %s;", op.NewName, op.ValueType)
}
func (op RenameAttribute) IsReversible() bool  { return false }
func (op RenameAttribute) IsDestructive() bool { return false }
func (op RenameAttribute) RollbackTypeQL() string { return "" }

// --- Arbitrary TypeQL ---

// RunTypeQL executes arbitrary TypeQL as a migration step.
// Provide Up for the forward migration and optionally Down for rollback.
type RunTypeQL struct {
	Up   string
	Down string
}

func (op RunTypeQL) ToTypeQL() string         { return op.Up }
func (op RunTypeQL) IsReversible() bool        { return op.Down != "" }
func (op RunTypeQL) IsDestructive() bool       { return false }
func (op RunTypeQL) RollbackTypeQL() string    { return op.Down }

// BreakingChange describes a change that could cause data loss or schema errors.
type BreakingChange struct {
	Type   string // "removal", "type_change", "cardinality_change"
	Entity string // affected type name
	Detail string // human-readable description
}

// BreakingChanges analyzes the diff for changes that could cause data loss.
func (d *SchemaDiff) BreakingChanges() []BreakingChange {
	var changes []BreakingChange

	for _, name := range d.RemoveTypes {
		changes = append(changes, BreakingChange{
			Type:   "removal",
			Entity: name,
			Detail: fmt.Sprintf("type %q exists in DB but not in Go structs — removing would delete all instances", name),
		})
	}

	for _, o := range d.RemoveOwns {
		changes = append(changes, BreakingChange{
			Type:   "removal",
			Entity: o.TypeName,
			Detail: fmt.Sprintf("ownership %s.%s exists in DB but not in Go structs — removing would delete attribute data", o.TypeName, o.Attribute),
		})
	}

	return changes
}

// HasBreakingChanges returns true if the diff contains any breaking changes.
func (d *SchemaDiff) HasBreakingChanges() bool {
	return len(d.RemoveTypes) > 0 || len(d.RemoveOwns) > 0
}

// Operations converts the diff into a list of discrete, ordered operations.
func (d *SchemaDiff) Operations() []Operation {
	var ops []Operation

	// Attributes first (types depend on them)
	for _, a := range d.AddAttributes {
		ops = append(ops, AddAttribute(a))
	}

	// Entity and relation types
	for _, e := range d.AddEntities {
		ops = append(ops, AddEntity{TypeQL: e.TypeQL})
	}
	for _, r := range d.AddRelations {
		ops = append(ops, AddRelation{TypeQL: r.TypeQL})
	}

	// Ownership additions
	for _, o := range d.AddOwns {
		ops = append(ops, AddOwnership{Owner: o.TypeName, Attribute: o.Attribute, Annots: o.Annots})
	}

	// Role additions
	for _, r := range d.AddRelates {
		ops = append(ops, AddRole{Relation: r.TypeName, Role: r.Role, Card: r.Card})
	}

	return ops
}

// DestructiveOperations returns operations that remove schema elements.
// These are only generated when explicitly requested.
func (d *SchemaDiff) DestructiveOperations() []Operation {
	var ops []Operation

	// Remove ownerships first (before removing types)
	for _, o := range d.RemoveOwns {
		ops = append(ops, RemoveOwnership{Owner: o.TypeName, Attribute: o.Attribute})
	}

	// Remove types
	for _, name := range d.RemoveTypes {
		// We don't know if it's an entity or relation from just the name,
		// but undefine works on the type name regardless
		ops = append(ops, RemoveEntity{Name: name})
	}

	return ops
}

// MigrateOption configures migration behavior.
type MigrateOption func(*migrateConfig)

type migrateConfig struct {
	destructive bool
}

// WithDestructive enables destructive migration (removals).
func WithDestructive() MigrateOption {
	return func(c *migrateConfig) { c.destructive = true }
}

// GenerateMigrationWithOpts produces TypeQL statements to apply the diff.
// With WithDestructive(), also generates undefine statements for removals.
func (d *SchemaDiff) GenerateMigrationWithOpts(opts ...MigrateOption) []string {
	cfg := migrateConfig{}
	for _, o := range opts {
		o(&cfg)
	}

	var stmts []string

	// Additive operations
	for _, op := range d.Operations() {
		stmts = append(stmts, op.ToTypeQL())
	}

	// Destructive operations (only if opted in)
	if cfg.destructive {
		for _, op := range d.DestructiveOperations() {
			stmts = append(stmts, op.ToTypeQL())
		}
	}

	return stmts
}
