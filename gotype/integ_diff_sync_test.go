//go:build integration && cgo && typedb

package gotype_test

import (
	"context"
	"strings"
	"testing"

	"github.com/CaliLuke/go-typeql/gotype"
)

// ---------------------------------------------------------------------------
// Diff-based migration redesign tests (issues #32, #34, #55, #57, #58, #61,
// #62, #100): destructive sync, rollback, plays generation, bootstrap skip,
// and plan preview against a live server.
// ---------------------------------------------------------------------------

// TestIntegration_SyncSchema_ForceDropPreservesHistory verifies that a
// destructive sync actually drops removed types (issue #32) while never
// touching go-typeql's own migration-tracking types or the recorded history
// (issue #58), and that recorded timestamps survive the round trip (#61).
func TestIntegration_SyncSchema_ForceDropPreservesHistory(t *testing.T) {
	db := setupTestDBWith(t, func() {
		_ = gotype.Register[Person]()
	})
	ctx := context.Background()

	// Grow the schema through the state-tracked path so a migration record
	// exists.
	gotype.ClearRegistry()
	_ = gotype.Register[Person]()
	_ = gotype.Register[NewEntity]()
	diff, err := gotype.MigrateWithState(ctx, db)
	if err != nil {
		t.Fatalf("MigrateWithState: %v", err)
	}
	if diff.IsEmpty() {
		t.Fatal("expected non-empty diff when adding NewEntity")
	}

	// Shrink the model set and force-sync: new-entity and its attributes must
	// be destroyed, the migration history must survive.
	gotype.ClearRegistry()
	_ = gotype.Register[Person]()
	diff, err = gotype.SyncSchema(ctx, db, gotype.WithForce())
	if err != nil {
		t.Fatalf("SyncSchema(WithForce): %v", err)
	}
	if len(diff.RemoveTypes) == 0 {
		t.Fatalf("expected new-entity in RemoveTypes, got: %s", diff.Summary())
	}
	for _, name := range diff.RemoveTypes {
		if strings.HasPrefix(name, "migration-") || strings.HasPrefix(name, "seq-migration-") {
			t.Errorf("force sync targeted its own tracking type %q", name)
		}
	}

	schema := getSchemaString(t, dbAddress(), db.Name())
	if strings.Contains(schema, "new-entity") {
		t.Error("expected new-entity to be dropped from the schema")
	}
	if !strings.Contains(schema, "migration-record") {
		t.Error("expected migration-record tracking type to survive the force sync")
	}
	if !strings.Contains(schema, "entity person") {
		t.Error("expected person to survive the force sync")
	}

	// The recorded history must still be readable, with a usable timestamp.
	records, err := gotype.NewMigrationState(db).Applied(ctx)
	if err != nil {
		t.Fatalf("Applied: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("expected 1 migration record to survive, got %d", len(records))
	}
	if records[0].Hash == "" {
		t.Error("expected a recorded hash")
	}
	if records[0].AppliedAt.IsZero() {
		t.Error("expected AppliedAt to round-trip through the database (issue #61)")
	}
}

// TestIntegration_Migration_OperationRollback applies every additive
// operation type and then executes their RollbackTypeQL statements — the
// Down path — against a live server, proving the TypeDB 3.x undefine forms
// are accepted (issue #32).
func TestIntegration_Migration_OperationRollback(t *testing.T) {
	db := setupTestDBWith(t, func() {
		_ = gotype.Register[Person]()
	})
	ctx := context.Background()

	ops := []gotype.Operation{
		gotype.AddAttribute{Name: "temp-attr", ValueType: "string"},
		gotype.AddEntity{Name: "temp-entity"},
		gotype.AddOwnership{Owner: "temp-entity", Attribute: "temp-attr"},
		gotype.AddRelation{Name: "temp-relation"},
		gotype.AddRole{Relation: "temp-relation", Role: "temp-role"},
		gotype.AddRolePlayer{Entity: "temp-entity", Relation: "temp-relation", Role: "temp-role"},
	}

	// Apply forward statements in one schema transaction (a relation and its
	// role must commit together).
	up, err := db.BeginContext(ctx, gotype.SchemaTransaction)
	if err != nil {
		t.Fatalf("begin schema tx: %v", err)
	}
	for _, op := range ops {
		if _, err := up.Tx().QueryWithContext(ctx, op.ToTypeQL()); err != nil {
			up.Close()
			t.Fatalf("apply %q: %v", op.ToTypeQL(), err)
		}
	}
	if err := up.Commit(); err != nil {
		t.Fatalf("commit forward migration: %v", err)
	}

	schema := getSchemaString(t, dbAddress(), db.Name())
	for _, want := range []string{"temp-entity", "temp-relation", "temp-attr"} {
		if !strings.Contains(schema, want) {
			t.Fatalf("expected %q in schema after forward migration:\n%s", want, schema)
		}
	}

	// Roll everything back in reverse order, again in one transaction.
	down, err := db.BeginContext(ctx, gotype.SchemaTransaction)
	if err != nil {
		t.Fatalf("begin rollback tx: %v", err)
	}
	for i := len(ops) - 1; i >= 0; i-- {
		if !ops[i].IsReversible() {
			t.Fatalf("operation %T should be reversible", ops[i])
		}
		if _, err := down.Tx().QueryWithContext(ctx, ops[i].RollbackTypeQL()); err != nil {
			down.Close()
			t.Fatalf("rollback %q: %v", ops[i].RollbackTypeQL(), err)
		}
	}
	if err := down.Commit(); err != nil {
		t.Fatalf("commit rollback: %v", err)
	}

	schema = getSchemaString(t, dbAddress(), db.Name())
	for _, gone := range []string{"temp-entity", "temp-relation", "temp-attr"} {
		if strings.Contains(schema, gone) {
			t.Errorf("expected %q to be rolled back, schema:\n%s", gone, schema)
		}
	}
	if !strings.Contains(schema, "entity person") {
		t.Error("rollback must not touch unrelated types")
	}
}

// TestIntegration_Migration_AddRelationGeneratesPlays reproduces issue #34:
// after migrating a new relation into an existing database, the player types
// must have gained their plays clauses, so inserting through the relation
// works.
func TestIntegration_Migration_AddRelationGeneratesPlays(t *testing.T) {
	db := setupTestDBWith(t, func() {
		_ = gotype.Register[Person]()
		_ = gotype.Register[NewEntity]()
	})
	ctx := context.Background()

	gotype.ClearRegistry()
	_ = gotype.Register[Person]()
	_ = gotype.Register[NewEntity]()
	_ = gotype.Register[NewRelation]()

	diff, err := gotype.Migrate(ctx, db)
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if len(diff.AddRelations) == 0 {
		t.Fatal("expected new relation in diff")
	}
	if len(diff.AddPlays) == 0 {
		t.Fatalf("expected plays clauses for existing player types, got: %s", diff.Summary())
	}

	// The issue #34 repro: this insert failed with a type-inference error
	// when the migration did not generate plays clauses.
	_, err = db.ExecuteWrite(ctx, `insert
$p isa person, has name "Plays", has email "plays@test.com";
$e isa new-entity, has code "PL1", has summary "s";
$r isa new-relation, links (source: $p, target: $e), has weight 5;`)
	if err != nil {
		t.Fatalf("insert through migrated relation: %v", err)
	}

	// Idempotence: a second migrate must find nothing to do.
	diff, err = gotype.Migrate(ctx, db)
	if err != nil {
		t.Fatalf("second migrate: %v", err)
	}
	if !diff.IsEmpty() {
		t.Errorf("expected empty diff on re-run, got: %s", diff.Summary())
	}
}

// TestIntegration_SyncSchema_SkipIfExists verifies the bootstrap semantics of
// WithSkipIfExists (issue #100): a provisioned database is left untouched, an
// unqualified sync then applies the pending changes.
func TestIntegration_SyncSchema_SkipIfExists(t *testing.T) {
	db := setupTestDBWith(t, func() {
		_ = gotype.Register[Person]()
	})
	ctx := context.Background()

	gotype.ClearRegistry()
	_ = gotype.Register[Person]()
	_ = gotype.Register[Company]()

	diff, err := gotype.SyncSchema(ctx, db, gotype.WithSkipIfExists())
	if err != nil {
		t.Fatalf("SyncSchema(WithSkipIfExists): %v", err)
	}
	if diff.IsEmpty() {
		t.Error("the skipped diff should still report the pending company type")
	}
	schema := getSchemaString(t, dbAddress(), db.Name())
	if strings.Contains(schema, "entity company") {
		t.Fatal("WithSkipIfExists must not modify a provisioned database")
	}

	// Without the option the same sync applies the pending changes.
	if _, err := gotype.SyncSchema(ctx, db); err != nil {
		t.Fatalf("SyncSchema: %v", err)
	}
	schema = getSchemaString(t, dbAddress(), db.Name())
	if !strings.Contains(schema, "entity company") {
		t.Error("expected company to be defined by the unqualified sync")
	}
}

// TestIntegration_PlanSchema_Preview verifies that PlanSchema exposes the
// exact statement list without executing anything (issue #62).
func TestIntegration_PlanSchema_Preview(t *testing.T) {
	db := setupTestDBWith(t, func() {
		_ = gotype.Register[Person]()
		_ = gotype.Register[NewEntity]()
	})
	ctx := context.Background()

	gotype.ClearRegistry()
	_ = gotype.Register[Person]()

	plan, err := gotype.PlanSchema(ctx, db, gotype.WithForce())
	if err != nil {
		t.Fatalf("PlanSchema: %v", err)
	}
	if plan.IsEmpty() {
		t.Fatalf("expected a destructive plan, got: %s", plan.Summary())
	}
	foundUndefine := false
	for _, q := range plan.Queries {
		if strings.HasPrefix(q, "undefine") {
			foundUndefine = true
			if !strings.Contains(q, "new-entity") {
				t.Errorf("expected new-entity in the undefine block:\n%s", q)
			}
		}
	}
	if !foundUndefine {
		t.Errorf("expected an undefine block in the plan, got: %v", plan.Queries)
	}

	// Nothing was executed.
	schema := getSchemaString(t, dbAddress(), db.Name())
	if !strings.Contains(schema, "new-entity") {
		t.Error("PlanSchema must not modify the database")
	}

	// Applying the previewed plan through SyncSchema drops the type.
	if _, err := gotype.SyncSchema(ctx, db, gotype.WithForce()); err != nil {
		t.Fatalf("SyncSchema(WithForce): %v", err)
	}
	schema = getSchemaString(t, dbAddress(), db.Name())
	if strings.Contains(schema, "new-entity") {
		t.Error("expected new-entity to be dropped after applying the plan")
	}
}
