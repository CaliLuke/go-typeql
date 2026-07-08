//go:build integration && cgo && typedb

package gotype_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/CaliLuke/go-typeql/driver"
	"github.com/CaliLuke/go-typeql/gotype"
)

// setupSeqMigrateDB creates a fresh empty database for sequential migration tests.
func setupSeqMigrateDB(t *testing.T) *gotype.Database {
	t.Helper()
	addr := dbAddress()

	drv, err := driver.Open(addr, "admin", "password")
	if err != nil {
		t.Skipf("TypeDB not available at %s: %v", addr, err)
	}

	sanitized := strings.NewReplacer("/", "_", " ", "_", ".", "_").Replace(t.Name())
	if len(sanitized) > 50 {
		sanitized = sanitized[:50]
	}
	dbName := fmt.Sprintf("go_seq_%s", strings.ToLower(sanitized))

	conn := &driverAdapter{drv: drv}

	exists, err := conn.DatabaseContains(dbName)
	if err != nil {
		drv.Close()
		t.Fatalf("checking database existence: %v", err)
	}
	if exists {
		if err := conn.DatabaseDelete(dbName); err != nil {
			drv.Close()
			t.Fatalf("deleting old test database: %v", err)
		}
	}

	if err := conn.DatabaseCreate(dbName); err != nil {
		drv.Close()
		t.Fatalf("creating test database: %v", err)
	}

	db := gotype.NewDatabase(conn, dbName)

	t.Cleanup(func() {
		_ = conn.DatabaseDelete(dbName)
		drv.Close()
	})

	return db
}

func TestSeqMigrate_Fresh(t *testing.T) {
	db := setupSeqMigrateDB(t)
	ctx := context.Background()

	migrations := []gotype.SequentialMigration{
		gotype.TQLMigration("001_create_person", []string{
			"define attribute name, value string; attribute email, value string;",
			"define entity person, owns name @key, owns email;",
		}, []string{
			"undefine entity person;",
			"undefine attribute name; attribute email;",
		}),
	}

	applied, err := gotype.RunSequentialMigrations(ctx, db, migrations)
	if err != nil {
		t.Fatalf("RunSequentialMigrations failed: %v", err)
	}
	if len(applied) != 1 || applied[0] != "001_create_person" {
		t.Errorf("expected [001_create_person], got %v", applied)
	}
}

func TestSeqMigrate_Idempotent(t *testing.T) {
	db := setupSeqMigrateDB(t)
	ctx := context.Background()

	migrations := []gotype.SequentialMigration{
		gotype.TQLMigration("001_create_person", []string{
			"define attribute name, value string;",
			"define entity person, owns name @key;",
		}, nil),
	}

	// Run twice
	applied1, err := gotype.RunSequentialMigrations(ctx, db, migrations)
	if err != nil {
		t.Fatalf("first run failed: %v", err)
	}
	applied2, err := gotype.RunSequentialMigrations(ctx, db, migrations)
	if err != nil {
		t.Fatalf("second run failed: %v", err)
	}

	if len(applied1) != 1 {
		t.Errorf("first run: expected 1 applied, got %d", len(applied1))
	}
	if len(applied2) != 0 {
		t.Errorf("second run: expected 0 applied, got %d", len(applied2))
	}
}

func TestSeqMigrate_Incremental(t *testing.T) {
	db := setupSeqMigrateDB(t)
	ctx := context.Background()

	batch1 := []gotype.SequentialMigration{
		gotype.TQLMigration("001_create_person", []string{
			"define attribute name, value string;",
			"define entity person, owns name @key;",
		}, nil),
	}

	applied1, err := gotype.RunSequentialMigrations(ctx, db, batch1)
	if err != nil {
		t.Fatalf("batch1 failed: %v", err)
	}
	if len(applied1) != 1 {
		t.Errorf("batch1: expected 1, got %d", len(applied1))
	}

	// Add a second migration
	batch2 := append(batch1, gotype.TQLMigration("002_add_email", []string{
		"define attribute email, value string;",
		"define person owns email;",
	}, nil))

	applied2, err := gotype.RunSequentialMigrations(ctx, db, batch2)
	if err != nil {
		t.Fatalf("batch2 failed: %v", err)
	}
	if len(applied2) != 1 || applied2[0] != "002_add_email" {
		t.Errorf("batch2: expected [002_add_email], got %v", applied2)
	}
}

func TestSeqMigrate_Status(t *testing.T) {
	db := setupSeqMigrateDB(t)
	ctx := context.Background()

	migrations := []gotype.SequentialMigration{
		gotype.TQLMigration("001_create_person", []string{
			"define attribute name, value string;",
			"define entity person, owns name @key;",
		}, nil),
		gotype.TQLMigration("002_add_email", []string{
			"define attribute email, value string;",
			"define person owns email;",
		}, nil),
	}

	// Apply only the first
	_, err := gotype.RunSequentialMigrations(ctx, db, migrations[:1])
	if err != nil {
		t.Fatalf("run failed: %v", err)
	}

	infos, err := gotype.SeqMigrationStatus(ctx, db, migrations)
	if err != nil {
		t.Fatalf("status failed: %v", err)
	}
	if len(infos) != 2 {
		t.Fatalf("expected 2 infos, got %d", len(infos))
	}
	if !infos[0].Applied {
		t.Error("001 should be applied")
	}
	if infos[1].Applied {
		t.Error("002 should not be applied")
	}
}

func TestSeqMigrate_DryRun(t *testing.T) {
	db := setupSeqMigrateDB(t)
	ctx := context.Background()

	migrations := []gotype.SequentialMigration{
		gotype.TQLMigration("001_create_person", []string{
			"define attribute name, value string;",
			"define entity person, owns name @key;",
		}, nil),
	}

	pending, err := gotype.RunSequentialMigrations(ctx, db, migrations, gotype.WithSeqDryRun())
	if err != nil {
		t.Fatalf("dry run failed: %v", err)
	}
	if len(pending) != 1 {
		t.Fatalf("expected 1 pending, got %d", len(pending))
	}

	// Verify nothing was actually applied
	infos, err := gotype.SeqMigrationStatus(ctx, db, migrations)
	if err != nil {
		t.Fatalf("status failed: %v", err)
	}
	if infos[0].Applied {
		t.Error("migration should not be applied after dry run")
	}
}

func TestSeqMigrate_Target(t *testing.T) {
	db := setupSeqMigrateDB(t)
	ctx := context.Background()

	migrations := []gotype.SequentialMigration{
		gotype.TQLMigration("001_create_person", []string{
			"define attribute name, value string;",
			"define entity person, owns name @key;",
		}, nil),
		gotype.TQLMigration("002_add_email", []string{
			"define attribute email, value string;",
			"define person owns email;",
		}, nil),
		gotype.TQLMigration("003_add_age", []string{
			"define attribute age, value long;",
			"define person owns age;",
		}, nil),
	}

	applied, err := gotype.RunSequentialMigrations(ctx, db, migrations, gotype.WithSeqTarget("002_add_email"))
	if err != nil {
		t.Fatalf("run failed: %v", err)
	}
	if len(applied) != 2 {
		t.Fatalf("expected 2 applied, got %d: %v", len(applied), applied)
	}
}

func TestSeqMigrate_FailureStops(t *testing.T) {
	db := setupSeqMigrateDB(t)
	ctx := context.Background()

	migrations := []gotype.SequentialMigration{
		gotype.TQLMigration("001_create_person", []string{
			"define attribute name, value string;",
			"define entity person, owns name @key;",
		}, nil),
		gotype.TQLMigration("002_bad_migration", []string{
			"define this is not valid typeql syntax!!!;",
		}, nil),
		gotype.TQLMigration("003_should_not_run", []string{
			"define attribute age, value long;",
		}, nil),
	}

	applied, err := gotype.RunSequentialMigrations(ctx, db, migrations)
	if err == nil {
		t.Fatal("expected error from bad migration")
	}
	// Only the first should have been applied
	if len(applied) != 1 {
		t.Errorf("expected 1 applied before failure, got %d: %v", len(applied), applied)
	}
}

func TestSeqMigrate_DataMigration(t *testing.T) {
	db := setupSeqMigrateDB(t)
	ctx := context.Background()

	migrations := []gotype.SequentialMigration{
		gotype.TQLMigration("001_schema", []string{
			"define attribute name, value string;",
			"define entity person, owns name @key;",
		}, nil),
		gotype.TQLMigration("002_seed_data", []string{
			`insert $p isa person, has name "Alice";`,
			`insert $p isa person, has name "Bob";`,
		}, nil),
	}

	applied, err := gotype.RunSequentialMigrations(ctx, db, migrations)
	if err != nil {
		t.Fatalf("run failed: %v", err)
	}
	if len(applied) != 2 {
		t.Fatalf("expected 2 applied, got %d", len(applied))
	}

	// Verify data was inserted
	results, err := db.ExecuteRead(ctx, `match $p isa person; fetch { "name": $p.name };`)
	if err != nil {
		t.Fatalf("read failed: %v", err)
	}
	if len(results) != 2 {
		t.Errorf("expected 2 persons, got %d", len(results))
	}
}

func TestSeqMigrate_MixedOps(t *testing.T) {
	db := setupSeqMigrateDB(t)
	ctx := context.Background()

	migrations := []gotype.SequentialMigration{
		gotype.TQLMigration("001_schema_and_data", []string{
			"define attribute name, value string;",
			"define entity person, owns name @key;",
			`insert $p isa person, has name "Alice";`,
		}, nil),
	}

	applied, err := gotype.RunSequentialMigrations(ctx, db, migrations)
	if err != nil {
		t.Fatalf("run failed: %v", err)
	}
	if len(applied) != 1 {
		t.Errorf("expected 1 applied, got %d", len(applied))
	}
}

// TestSeqMigrate_AtomicFailureLeavesNoPartialData verifies issue #60: a
// statement-based migration that fails partway must roll back entirely —
// no partial data commits and no migration record is written — and a
// corrected version must then apply cleanly without duplicating rows.
func TestSeqMigrate_AtomicFailureLeavesNoPartialData(t *testing.T) {
	db := setupSeqMigrateDB(t)
	ctx := context.Background()

	setup := []gotype.SequentialMigration{
		gotype.TQLMigration("001_schema", []string{
			"define attribute name, value string;",
			"define entity person, owns name @key;",
		}, nil),
	}
	if _, err := gotype.RunSequentialMigrations(ctx, db, setup); err != nil {
		t.Fatalf("setup failed: %v", err)
	}

	// Second statement fails: the first insert must not persist.
	bad := append(setup[:1:1], gotype.TQLMigration("002_seed", []string{
		`insert $p isa person, has name "Alice";`,
		"insert this is not valid typeql!!!;",
	}, nil))
	if _, err := gotype.RunSequentialMigrations(ctx, db, bad); err == nil {
		t.Fatal("expected failure from invalid statement")
	}

	results, err := db.ExecuteRead(ctx, `match $p isa person; fetch { "name": $p.name };`)
	if err != nil {
		t.Fatalf("read failed: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("failed migration must not commit partial data, found %d persons", len(results))
	}

	infos, err := gotype.SeqMigrationStatus(ctx, db, bad)
	if err != nil {
		t.Fatalf("status failed: %v", err)
	}
	for _, info := range infos {
		if info.Name == "002_seed" && info.Applied {
			t.Error("failed migration must not be recorded as applied")
		}
	}

	// Corrected migration applies without duplicating anything.
	fixed := append(setup[:1:1], gotype.TQLMigration("002_seed", []string{
		`insert $p isa person, has name "Alice";`,
		`insert $p isa person, has name "Bob";`,
	}, nil))
	applied, err := gotype.RunSequentialMigrations(ctx, db, fixed)
	if err != nil {
		t.Fatalf("fixed run failed: %v", err)
	}
	if len(applied) != 1 || applied[0] != "002_seed" {
		t.Errorf("expected [002_seed], got %v", applied)
	}
	results, err = db.ExecuteRead(ctx, `match $p isa person; fetch { "name": $p.name };`)
	if err != nil {
		t.Fatalf("read failed: %v", err)
	}
	if len(results) != 2 {
		t.Errorf("expected exactly 2 persons after fixed run, got %d", len(results))
	}
}

// TestSeqMigrate_TargetNotFound verifies issue #59: an unknown target is an
// error and applies nothing, instead of silently applying every migration.
func TestSeqMigrate_TargetNotFound(t *testing.T) {
	db := setupSeqMigrateDB(t)
	ctx := context.Background()

	migrations := []gotype.SequentialMigration{
		gotype.TQLMigration("001_create_person", []string{
			"define attribute name, value string;",
			"define entity person, owns name @key;",
		}, nil),
	}

	if _, err := gotype.RunSequentialMigrations(ctx, db, migrations, gotype.WithSeqTarget("001_create_persno")); err == nil {
		t.Fatal("expected error for typo'd target")
	}

	infos, err := gotype.SeqMigrationStatus(ctx, db, migrations)
	if err != nil {
		t.Fatalf("status failed: %v", err)
	}
	if infos[0].Applied {
		t.Error("nothing should be applied when the target does not exist")
	}
}

// TestSeqMigrate_RollbackAtomic verifies that rolling back a statement-based
// migration removes its record in the same transaction as the Down statements.
func TestSeqMigrate_RollbackAtomic(t *testing.T) {
	db := setupSeqMigrateDB(t)
	ctx := context.Background()

	migrations := []gotype.SequentialMigration{
		gotype.TQLMigration("001_schema", []string{
			"define attribute name, value string;",
			"define entity person, owns name @key;",
		}, nil),
		gotype.TQLMigration("002_seed", []string{
			`insert $p isa person, has name "Alice";`,
		}, []string{
			`match $p isa person, has name "Alice"; delete $p;`,
		}),
	}

	if _, err := gotype.RunSequentialMigrations(ctx, db, migrations); err != nil {
		t.Fatalf("run failed: %v", err)
	}

	rolledBack, err := gotype.RollbackSequentialMigration(ctx, db, migrations, 1)
	if err != nil {
		t.Fatalf("rollback failed: %v", err)
	}
	if len(rolledBack) != 1 || rolledBack[0] != "002_seed" {
		t.Fatalf("expected [002_seed], got %v", rolledBack)
	}

	results, err := db.ExecuteRead(ctx, `match $p isa person; fetch { "name": $p.name };`)
	if err != nil {
		t.Fatalf("read failed: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("expected 0 persons after rollback, got %d", len(results))
	}
	infos, err := gotype.SeqMigrationStatus(ctx, db, migrations)
	if err != nil {
		t.Fatalf("status failed: %v", err)
	}
	for _, info := range infos {
		if info.Name == "002_seed" && info.Applied {
			t.Error("rolled-back migration should not show as applied")
		}
	}
}

func TestStampSequentialMigrations_Integration(t *testing.T) {
	db := setupSeqMigrateDB(t)
	ctx := context.Background()

	// Apply schema directly (simulating bulk setup)
	if err := db.ExecuteSchema(ctx, "define attribute name, value string;"); err != nil {
		t.Fatalf("schema failed: %v", err)
	}
	if err := db.ExecuteSchema(ctx, "define entity person, owns name @key;"); err != nil {
		t.Fatalf("schema failed: %v", err)
	}
	if err := db.ExecuteSchema(ctx, "define attribute email, value string;"); err != nil {
		t.Fatalf("schema failed: %v", err)
	}
	if err := db.ExecuteSchema(ctx, "define person owns email;"); err != nil {
		t.Fatalf("schema failed: %v", err)
	}

	migrations := []gotype.SequentialMigration{
		gotype.TQLMigration("001_create_person", []string{
			"define attribute name, value string;",
			"define entity person, owns name @key;",
		}, nil),
		gotype.TQLMigration("002_add_email", []string{
			"define attribute email, value string;",
			"define person owns email;",
		}, nil),
		gotype.TQLMigration("003_add_age", []string{
			"define attribute age, value long;",
			"define person owns age;",
		}, nil),
	}

	// Stamp all 3 as applied
	stamped, err := gotype.StampSequentialMigrations(ctx, db, migrations)
	if err != nil {
		t.Fatalf("stamp failed: %v", err)
	}
	if len(stamped) != 3 {
		t.Fatalf("expected 3 stamped, got %d: %v", len(stamped), stamped)
	}

	// Verify status shows all as applied
	infos, err := gotype.SeqMigrationStatus(ctx, db, migrations)
	if err != nil {
		t.Fatalf("status failed: %v", err)
	}
	for _, info := range infos {
		if !info.Applied {
			t.Errorf("%s should be applied", info.Name)
		}
	}

	// RunSequentialMigrations should apply 0 (all stamped)
	applied, err := gotype.RunSequentialMigrations(ctx, db, migrations)
	if err != nil {
		t.Fatalf("run failed: %v", err)
	}
	if len(applied) != 0 {
		t.Errorf("expected 0 applied after stamp, got %d: %v", len(applied), applied)
	}
}
