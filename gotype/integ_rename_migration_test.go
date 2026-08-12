//go:build integration && cgo && typedb

package gotype_test

import (
	"context"
	"strings"
	"testing"

	"github.com/CaliLuke/go-typeql/gotype"
)

const renameTestSchema = `define
attribute old-email, value string;
entity base-person @abstract;
entity old-person @doc("Old person."), sub base-person, owns old-email @key;
entity company;
relation old-employment,
    relates old-worker @card(1..1),
    relates employer @card(1..1);
old-person plays old-employment:old-worker;
company plays old-employment:employer;`

type renameDiffPerson struct {
	gotype.BaseEntity `typedb:"type:person"`
	Email             string `typedb:"email,key"`
}

type oldRenameDiffPerson struct {
	gotype.BaseEntity `typedb:"type:old-person"`
	Email             string `typedb:"old-email,key"`
}

func TestRenameMigration_CombinedPreservesDataAndRollsBack(t *testing.T) {
	db := setupSeqMigrateDB(t)
	ctx := context.Background()
	if err := db.ExecuteSchema(ctx, renameTestSchema); err != nil {
		t.Fatalf("define rename test schema: %v", err)
	}

	rows, err := db.ExecuteWrite(ctx, `insert
$p isa old-person, has old-email "alice@example.com";
$c isa company;
$r isa old-employment, links (old-worker: $p, employer: $c);
select $p, $r;`)
	if err != nil {
		t.Fatalf("insert rename test data: %v", err)
	}
	personIID := integrationConceptIID(t, rows, "p")
	relationIID := integrationConceptIID(t, rows, "r")

	migration := mustRenameMigration(t, "001_native_renames",
		gotype.RenameAttributeType("old-email", "email"),
		gotype.RenameEntity("old-person", "person"),
		gotype.RenameRole("old-employment", "old-worker", "worker"),
		gotype.RenameRelation("old-employment", "employment"),
	)
	if _, err := gotype.RunSequentialMigrations(ctx, db, []gotype.SequentialMigration{migration}); err != nil {
		t.Fatalf("apply rename migration: %v", err)
	}

	rows, err = db.ExecuteRead(ctx, `match
$p isa! person, has email "alice@example.com";
$r isa! employment, links (worker: $p);
select $p, $r;`)
	if err != nil {
		t.Fatalf("read renamed data: %v", err)
	}
	if got := integrationConceptIID(t, rows, "p"); got != personIID {
		t.Errorf("person IID changed: got %q, want %q", got, personIID)
	}
	if got := integrationConceptIID(t, rows, "r"); got != relationIID {
		t.Errorf("relation IID changed: got %q, want %q", got, relationIID)
	}
	if _, err := db.ExecuteWrite(ctx, `match $c isa company;
insert
$p isa person, has email "bob@example.com";
$r isa employment, links (worker: $p, employer: $c);`); err != nil {
		t.Fatalf("renamed schema capabilities did not accept new data: %v", err)
	}
	assertIntegrationQueryFails(t, db, `match $p isa old-person; select $p;`)
	assertIntegrationQueryFails(t, db, `match $p isa person, has old-email $e; select $p;`)

	if _, err := gotype.RollbackSequentialMigration(ctx, db, []gotype.SequentialMigration{migration}, 1); err != nil {
		t.Fatalf("roll back rename migration: %v", err)
	}
	rows, err = db.ExecuteRead(ctx, `match
$p isa! old-person, has old-email "alice@example.com";
$r isa! old-employment, links (old-worker: $p);
select $p, $r;`)
	if err != nil {
		t.Fatalf("read rolled-back data: %v", err)
	}
	if got := integrationConceptIID(t, rows, "p"); got != personIID {
		t.Errorf("person IID changed after rollback: got %q, want %q", got, personIID)
	}
	if got := integrationConceptIID(t, rows, "r"); got != relationIID {
		t.Errorf("relation IID changed after rollback: got %q, want %q", got, relationIID)
	}
	if _, err := db.ExecuteWrite(ctx, `match $c isa company;
insert
$p isa old-person, has old-email "carol@example.com";
$r isa old-employment, links (old-worker: $p, employer: $c);`); err != nil {
		t.Fatalf("rolled-back schema capabilities did not accept new data: %v", err)
	}
}

func TestRenameMigration_ForwardFailureIsAtomic(t *testing.T) {
	db := setupSeqMigrateDB(t)
	ctx := context.Background()
	if err := db.ExecuteSchema(ctx, `define
attribute old-email, value string;
attribute email, value string;
entity old-person, owns old-email;`); err != nil {
		t.Fatalf("define collision schema: %v", err)
	}
	migration := mustRenameMigration(t, "001_forward_collision",
		gotype.RenameEntity("old-person", "person"),
		gotype.RenameAttributeType("old-email", "email"),
	)

	if _, err := gotype.RunSequentialMigrations(ctx, db, []gotype.SequentialMigration{migration}); err == nil {
		t.Fatal("expected target-label collision")
	}
	if _, err := db.ExecuteRead(ctx, `match entity $t; $t label old-person; select $t;`); err != nil {
		t.Fatalf("first rename committed despite later failure: %v", err)
	}
	assertIntegrationQueryFails(t, db, `match entity $t; $t label person; select $t;`)
	assertMigrationApplied(t, ctx, db, migration, false)
}

func TestRenameMigration_RollbackFailureIsAtomic(t *testing.T) {
	db := setupSeqMigrateDB(t)
	ctx := context.Background()
	if err := db.ExecuteSchema(ctx, `define
attribute old-email, value string;
entity old-person, owns old-email;`); err != nil {
		t.Fatalf("define rollback schema: %v", err)
	}
	migration := mustRenameMigration(t, "001_rollback_collision",
		gotype.RenameEntity("old-person", "person"),
		gotype.RenameAttributeType("old-email", "email"),
	)
	if _, err := gotype.RunSequentialMigrations(ctx, db, []gotype.SequentialMigration{migration}); err != nil {
		t.Fatalf("apply rename migration: %v", err)
	}
	if err := db.ExecuteSchema(ctx, `define entity old-person;`); err != nil {
		t.Fatalf("create rollback collision: %v", err)
	}

	if _, err := gotype.RollbackSequentialMigration(ctx, db, []gotype.SequentialMigration{migration}, 1); err == nil {
		t.Fatal("expected rollback target-label collision")
	}
	if _, err := db.ExecuteRead(ctx, `match attribute $t; $t label email; select $t;`); err != nil {
		t.Fatalf("first reverse rename committed despite later failure: %v", err)
	}
	assertIntegrationQueryFails(t, db, `match attribute $t; $t label old-email; select $t;`)
	if _, err := db.ExecuteRead(ctx, `match entity $t; $t label person; select $t;`); err != nil {
		t.Fatalf("renamed entity disappeared after failed rollback: %v", err)
	}
	assertMigrationApplied(t, ctx, db, migration, true)
}

func TestRenameMigration_ChainsSwapsAndScopedRoles(t *testing.T) {
	db := setupSeqMigrateDB(t)
	ctx := context.Background()
	if err := db.ExecuteSchema(ctx, `define
entity a;
entity b;
entity alpha;
entity beta;
relation employment, relates old-worker;
relation project, relates old-worker;`); err != nil {
		t.Fatalf("define boundary schema: %v", err)
	}

	chain := mustRenameMigration(t, "001_chain",
		gotype.RenameEntity("b", "c"),
		gotype.RenameEntity("a", "b"),
	)
	swap := mustRenameMigration(t, "002_swap",
		gotype.RenameEntity("alpha", "rename-temp-alpha"),
		gotype.RenameEntity("beta", "alpha"),
		gotype.RenameEntity("rename-temp-alpha", "beta"),
	)
	scopedRole := mustRenameMigration(t, "003_scoped_role",
		gotype.RenameRole("employment", "old-worker", "worker"),
	)
	migrations := []gotype.SequentialMigration{chain, swap, scopedRole}
	if _, err := gotype.RunSequentialMigrations(ctx, db, migrations); err != nil {
		t.Fatalf("apply boundary migrations: %v", err)
	}

	for _, query := range []string{
		`match $t label b; select $t;`,
		`match $t label c; select $t;`,
		`match $t label alpha; select $t;`,
		`match $t label beta; select $t;`,
		`match employment relates $role; $role label employment:worker; select $role;`,
		`match project relates $role; $role label project:old-worker; select $role;`,
	} {
		assertIntegrationQueryHasRows(t, db, query)
	}
	assertIntegrationQueryHasNoRows(t, db, `match employment relates $role; $role label employment:old-worker; select $role;`)

	if _, err := gotype.RollbackSequentialMigration(ctx, db, migrations, 3); err != nil {
		t.Fatalf("roll back boundary migrations: %v", err)
	}
	for _, query := range []string{
		`match $t label a; select $t;`,
		`match $t label b; select $t;`,
		`match $t label alpha; select $t;`,
		`match $t label beta; select $t;`,
		`match employment relates $role; $role label employment:old-worker; select $role;`,
		`match project relates $role; $role label project:old-worker; select $role;`,
	} {
		assertIntegrationQueryHasRows(t, db, query)
	}
}

func TestRenameMigration_DiffConvergesWithNewLabels(t *testing.T) {
	db := setupSeqMigrateDB(t)
	ctx := context.Background()
	if err := db.ExecuteSchema(ctx, `define
attribute old-email, value string;
entity old-person, owns old-email @key;`); err != nil {
		t.Fatalf("define diff schema: %v", err)
	}
	migration := mustRenameMigration(t, "001_diff_rename",
		gotype.RenameAttributeType("old-email", "email"),
		gotype.RenameEntity("old-person", "person"),
	)
	if _, err := gotype.RunSequentialMigrations(ctx, db, []gotype.SequentialMigration{migration}); err != nil {
		t.Fatalf("apply rename migration: %v", err)
	}

	gotype.ClearRegistry()
	t.Cleanup(gotype.ClearRegistry)
	gotype.MustRegister[renameDiffPerson]()
	plan, err := gotype.PlanSchema(ctx, db)
	if err != nil {
		t.Fatalf("plan new-label diff: %v", err)
	}
	if !plan.IsEmpty() {
		t.Fatalf("new-label diff did not converge: %s", plan.Summary())
	}

	gotype.ClearRegistry()
	gotype.MustRegister[oldRenameDiffPerson]()
	oldPlan, err := gotype.PlanSchema(ctx, db)
	if err != nil {
		t.Fatalf("plan old-label diff: %v", err)
	}
	if oldPlan.IsEmpty() {
		t.Fatal("old-label models did not report schema drift")
	}
}

func mustRenameMigration(t *testing.T, name string, ops ...gotype.RenameOperation) gotype.SequentialMigration {
	t.Helper()
	migration, err := gotype.RenameMigration(name, ops...)
	if err != nil {
		t.Fatalf("RenameMigration() failed: %v", err)
	}
	return migration
}

func integrationConceptIID(t *testing.T, rows []map[string]any, variable string) string {
	t.Helper()
	if len(rows) == 0 {
		t.Fatalf("query returned no rows for variable %q", variable)
	}
	concept, ok := rows[0][variable].(map[string]any)
	if !ok {
		t.Fatalf("variable %q is not a concept map: %#v", variable, rows[0][variable])
	}
	iid, ok := concept["_iid"].(string)
	if !ok || iid == "" {
		t.Fatalf("variable %q has no IID: %#v", variable, concept)
	}
	return iid
}

func assertIntegrationQueryFails(t *testing.T, db *gotype.Database, query string) {
	t.Helper()
	if _, err := db.ExecuteRead(context.Background(), query); err == nil {
		t.Fatalf("query unexpectedly succeeded: %s", strings.TrimSpace(query))
	}
}

func assertIntegrationQueryHasRows(t *testing.T, db *gotype.Database, query string) {
	t.Helper()
	rows, err := db.ExecuteRead(context.Background(), query)
	if err != nil {
		t.Fatalf("query failed for %q: %v", strings.TrimSpace(query), err)
	}
	if len(rows) == 0 {
		t.Fatalf("query returned no rows: %s", strings.TrimSpace(query))
	}
}

func assertIntegrationQueryHasNoRows(t *testing.T, db *gotype.Database, query string) {
	t.Helper()
	rows, err := db.ExecuteRead(context.Background(), query)
	if err == nil && len(rows) != 0 {
		t.Fatalf("query returned rows for an absent label: %s", strings.TrimSpace(query))
	}
}

func assertMigrationApplied(
	t *testing.T,
	ctx context.Context,
	db *gotype.Database,
	migration gotype.SequentialMigration,
	want bool,
) {
	t.Helper()
	status, err := gotype.SeqMigrationStatus(ctx, db, []gotype.SequentialMigration{migration})
	if err != nil {
		t.Fatalf("read migration status: %v", err)
	}
	if len(status) != 1 || status[0].Applied != want {
		t.Fatalf("migration applied status = %#v, want %t", status, want)
	}
}
