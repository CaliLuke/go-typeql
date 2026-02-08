package gotype

import (
	"strings"
	"testing"
)

func TestAddAttribute_ToTypeQL(t *testing.T) {
	op := AddAttribute{Name: "email", ValueType: "string"}
	got := op.ToTypeQL()
	if got != "define attribute email, value string;" {
		t.Errorf("got %q", got)
	}
	if !op.IsReversible() {
		t.Error("AddAttribute should be reversible")
	}
	if op.IsDestructive() {
		t.Error("AddAttribute should not be destructive")
	}
	rollback := op.RollbackTypeQL()
	if !strings.Contains(rollback, "undefine") {
		t.Errorf("expected undefine in rollback, got %q", rollback)
	}
}

func TestAddEntity_ToTypeQL(t *testing.T) {
	op := AddEntity{Name: "person", Parent: "entity"}
	got := op.ToTypeQL()
	if !strings.Contains(got, "define entity person") {
		t.Errorf("got %q", got)
	}
	if !strings.Contains(got, "sub entity") {
		t.Errorf("expected sub clause, got %q", got)
	}
}

func TestAddOwnership_ToTypeQL(t *testing.T) {
	op := AddOwnership{Owner: "person", Attribute: "email", Annots: "@unique"}
	got := op.ToTypeQL()
	if !strings.Contains(got, "person owns email @unique") {
		t.Errorf("got %q", got)
	}
}

func TestAddRole_ToTypeQL(t *testing.T) {
	op := AddRole{Relation: "employment", Role: "employee", Card: "1..1"}
	got := op.ToTypeQL()
	if !strings.Contains(got, "employment relates employee @card(1..1)") {
		t.Errorf("got %q", got)
	}
}

func TestRemoveAttribute_IsDestructive(t *testing.T) {
	op := RemoveAttribute{Name: "old-attr"}
	if !op.IsDestructive() {
		t.Error("RemoveAttribute should be destructive")
	}
	if op.IsReversible() {
		t.Error("RemoveAttribute should not be reversible")
	}
	if !strings.Contains(op.ToTypeQL(), "undefine") {
		t.Errorf("expected undefine, got %q", op.ToTypeQL())
	}
}

func TestRemoveOwnership_ToTypeQL(t *testing.T) {
	op := RemoveOwnership{Owner: "person", Attribute: "old-attr"}
	got := op.ToTypeQL()
	if got != "undefine person owns old-attr;" {
		t.Errorf("got %q", got)
	}
}

func TestRemoveRole_ToTypeQL(t *testing.T) {
	op := RemoveRole{Relation: "employment", Role: "old-role"}
	got := op.ToTypeQL()
	if got != "undefine employment relates old-role;" {
		t.Errorf("got %q", got)
	}
}

func TestSchemaDiff_BreakingChanges(t *testing.T) {
	diff := &SchemaDiff{
		RemoveTypes: []string{"obsolete-entity"},
		RemoveOwns: []OwnsChange{
			{TypeName: "person", Attribute: "old-field"},
		},
	}

	changes := diff.BreakingChanges()
	if len(changes) != 2 {
		t.Fatalf("expected 2 breaking changes, got %d", len(changes))
	}
	if changes[0].Type != "removal" {
		t.Errorf("expected removal type, got %q", changes[0].Type)
	}
	if changes[0].Entity != "obsolete-entity" {
		t.Errorf("expected obsolete-entity, got %q", changes[0].Entity)
	}
}

func TestSchemaDiff_HasBreakingChanges(t *testing.T) {
	empty := &SchemaDiff{}
	if empty.HasBreakingChanges() {
		t.Error("empty diff should not have breaking changes")
	}

	withRemovals := &SchemaDiff{RemoveTypes: []string{"foo"}}
	if !withRemovals.HasBreakingChanges() {
		t.Error("diff with removals should have breaking changes")
	}
}

func TestSchemaDiff_Operations(t *testing.T) {
	diff := &SchemaDiff{
		AddAttributes: []AttrChange{
			{Name: "email", ValueType: "string"},
		},
		AddEntities: []TypeChange{
			{TypeQL: "entity person, owns name @key;"},
		},
		AddOwns: []OwnsChange{
			{TypeName: "person", Attribute: "age"},
		},
	}

	ops := diff.Operations()
	if len(ops) != 3 {
		t.Fatalf("expected 3 operations, got %d", len(ops))
	}

	// First should be attribute (attributes before types)
	if _, ok := ops[0].(AddAttribute); !ok {
		t.Errorf("first op should be AddAttribute, got %T", ops[0])
	}
	if _, ok := ops[1].(AddEntity); !ok {
		t.Errorf("second op should be AddEntity, got %T", ops[1])
	}
	if _, ok := ops[2].(AddOwnership); !ok {
		t.Errorf("third op should be AddOwnership, got %T", ops[2])
	}
}

func TestSchemaDiff_DestructiveOperations(t *testing.T) {
	diff := &SchemaDiff{
		RemoveOwns:  []OwnsChange{{TypeName: "person", Attribute: "old"}},
		RemoveTypes: []string{"obsolete"},
	}

	ops := diff.DestructiveOperations()
	if len(ops) != 2 {
		t.Fatalf("expected 2 destructive ops, got %d", len(ops))
	}

	for _, op := range ops {
		if !op.IsDestructive() {
			t.Errorf("expected destructive op, got %T", op)
		}
	}
}

func TestGenerateMigrationWithOpts_Additive(t *testing.T) {
	diff := &SchemaDiff{
		AddAttributes: []AttrChange{{Name: "email", ValueType: "string"}},
		RemoveTypes:   []string{"obsolete"},
	}

	stmts := diff.GenerateMigrationWithOpts()
	// Should only have additive statement
	if len(stmts) != 1 {
		t.Fatalf("expected 1 statement without destructive, got %d", len(stmts))
	}
	if !strings.Contains(stmts[0], "define") {
		t.Errorf("expected define, got %q", stmts[0])
	}
}

func TestGenerateMigrationWithOpts_Destructive(t *testing.T) {
	diff := &SchemaDiff{
		AddAttributes: []AttrChange{{Name: "email", ValueType: "string"}},
		RemoveTypes:   []string{"obsolete"},
	}

	stmts := diff.GenerateMigrationWithOpts(WithDestructive())
	// Should have additive + destructive
	if len(stmts) != 2 {
		t.Fatalf("expected 2 statements with destructive, got %d", len(stmts))
	}

	hasUndefine := false
	for _, s := range stmts {
		if strings.Contains(s, "undefine") {
			hasUndefine = true
		}
	}
	if !hasUndefine {
		t.Error("expected undefine statement in destructive mode")
	}
}
