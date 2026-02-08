//go:build integration && cgo && typedb

package gotype_test

import (
	"context"
	"strings"
	"testing"

	"github.com/CaliLuke/go-typeql/driver"
	"github.com/CaliLuke/go-typeql/gotype"
)

// MigratedPerson is Person with an extra attribute for migration tests.
type MigratedPerson struct {
	gotype.BaseEntity
	Name     string  `typedb:"name,key"`
	Email    string  `typedb:"email,unique"`
	Age      *int    `typedb:"age,card=0..1"`
	Nickname *string `typedb:"nickname,card=0..1"`
}

// NewEntity is a brand new entity type for migration tests.
type NewEntity struct {
	gotype.BaseEntity
	Code string `typedb:"code,key"`
	Summary string `typedb:"summary"`
}

// NewRelation is a brand new relation type for migration tests.
type NewRelation struct {
	gotype.BaseRelation
	Source *Person    `typedb:"role:source"`
	Target *NewEntity `typedb:"role:target"`
	Weight int        `typedb:"weight"`
}

func TestIntegration_Migration_AddAttribute(t *testing.T) {
	// Start with Person schema, then migrate to MigratedPerson (adds nickname).
	db := setupTestDBWith(t, func() {
		_ = gotype.Register[Person]()
		_ = gotype.Register[Company]()
		_ = gotype.Register[Employment]()
	})
	ctx := context.Background()

	// Re-register with the new model.
	gotype.ClearRegistry()
	_ = gotype.Register[MigratedPerson]()
	_ = gotype.Register[Company]()
	_ = gotype.Register[Employment]()

	diff, err := gotype.Migrate(ctx, db)
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if diff.IsEmpty() {
		t.Error("expected non-empty diff (new nickname attribute)")
	}

	// Verify the new attribute is usable.
	_, err = db.ExecuteWrite(ctx,
		`insert $e isa migrated-person, has name "Test", has email "t@t.com", has nickname "Testy";`)
	if err != nil {
		t.Fatalf("insert with new attr: %v", err)
	}
}

func TestIntegration_Migration_AddEntityType(t *testing.T) {
	db := setupTestDBWith(t, func() {
		_ = gotype.Register[Person]()
	})
	ctx := context.Background()

	gotype.ClearRegistry()
	_ = gotype.Register[Person]()
	_ = gotype.Register[NewEntity]()

	diff, err := gotype.Migrate(ctx, db)
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if len(diff.AddEntities) == 0 {
		t.Error("expected new entity in diff")
	}

	// Verify new entity is usable.
	mgr := gotype.NewManager[NewEntity](db)
	if err := mgr.Insert(ctx, &NewEntity{Code: "X1", Summary: "test"}); err != nil {
		t.Fatalf("insert new entity: %v", err)
	}
}

func TestIntegration_Migration_AddRelationType(t *testing.T) {
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
		t.Error("expected new relation in diff")
	}
}

func TestIntegration_Migration_AddOwns(t *testing.T) {
	// Start with Person, then add an owns clause by re-registering
	// a version with an extra field.
	db := setupTestDBWith(t, func() {
		_ = gotype.Register[Person]()
	})
	ctx := context.Background()

	gotype.ClearRegistry()
	_ = gotype.Register[MigratedPerson]()

	diff, err := gotype.Migrate(ctx, db)
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	// Should have added nickname owns + nickname attribute.
	hasOwns := false
	for _, o := range diff.AddOwns {
		if o.Attribute == "nickname" {
			hasOwns = true
		}
	}
	hasAttr := false
	for _, a := range diff.AddAttributes {
		if a.Name == "nickname" {
			hasAttr = true
		}
	}
	if !hasOwns && !hasAttr {
		t.Error("expected nickname to appear in diff (owns or attributes)")
	}
}

func TestIntegration_Migration_NoOp(t *testing.T) {
	db := setupTestDBWith(t, func() {
		_ = gotype.Register[Person]()
		_ = gotype.Register[Company]()
		_ = gotype.Register[Employment]()
	})
	ctx := context.Background()

	// Re-register the exact same types.
	gotype.ClearRegistry()
	_ = gotype.Register[Person]()
	_ = gotype.Register[Company]()
	_ = gotype.Register[Employment]()

	diff, err := gotype.Migrate(ctx, db)
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if !diff.IsEmpty() {
		t.Errorf("expected empty diff for no-op migration, got: %s", diff.Summary())
	}
}

func TestIntegration_MigrateFromEmpty(t *testing.T) {
	// Create a fresh DB with no schema, then apply via MigrateFromEmpty.
	addr := dbAddress()

	drv, err := driver.Open(addr, "admin", "password")
	if err != nil {
		t.Skipf("TypeDB not available: %v", err)
	}
	dbName := "go_integ_migrate_empty"
	dbm := drv.Databases()

	exists, _ := dbm.Contains(dbName)
	if exists {
		dbm.Delete(dbName)
	}
	if err := dbm.Create(dbName); err != nil {
		drv.Close()
		t.Fatalf("create db: %v", err)
	}

	gotype.ClearRegistry()
	_ = gotype.Register[Person]()

	conn := &driverAdapter{drv: drv}
	db := gotype.NewDatabase(conn, dbName)

	ctx := context.Background()
	if err := gotype.MigrateFromEmpty(ctx, db); err != nil {
		t.Fatalf("MigrateFromEmpty: %v", err)
	}

	// Verify schema was applied.
	mgr := gotype.NewManager[Person](db)
	if err := mgr.Insert(ctx, &Person{Name: "FromEmpty", Email: "empty@test.com"}); err != nil {
		t.Fatalf("insert after MigrateFromEmpty: %v", err)
	}

	t.Cleanup(func() {
		_ = dbm.Delete(dbName)
		drv.Close()
	})
}

func TestIntegration_Migration_DiffSummary(t *testing.T) {
	// Construct a diff manually and verify Summary() output.
	diff := &gotype.SchemaDiff{
		AddAttributes: []gotype.AttrChange{
			{Name: "nickname", ValueType: "string"},
		},
		AddEntities: []gotype.TypeChange{
			{TypeQL: "entity newentity, owns code @key;"},
		},
	}
	summary := diff.Summary()
	if !strings.Contains(summary, "nickname") {
		t.Errorf("summary should mention 'nickname', got: %s", summary)
	}
	if !strings.Contains(summary, "1 entity") {
		t.Errorf("summary should mention entity, got: %s", summary)
	}
}
