//go:build integration && cgo && typedb

package gotype_test

import (
	"context"
	"testing"

	"github.com/CaliLuke/go-typeql/gotype"
)

// ---------------------------------------------------------------------------
// DeleteMany integration tests
// ---------------------------------------------------------------------------

func TestIntegration_DeleteMany_Entities(t *testing.T) {
	db := setupTestDBDefault(t)
	ctx := context.Background()
	mgr := gotype.NewManager[Person](db)

	persons := seedPersons(t, ctx, mgr)
	assertCount(t, ctx, mgr, 5)

	// Delete first two
	toDelete := persons[:2]
	if err := mgr.DeleteMany(ctx, toDelete); err != nil {
		t.Fatalf("DeleteMany failed: %v", err)
	}

	assertCount(t, ctx, mgr, 3)
}

func TestIntegration_DeleteMany_Empty(t *testing.T) {
	db := setupTestDBDefault(t)
	ctx := context.Background()
	mgr := gotype.NewManager[Person](db)

	seedPersons(t, ctx, mgr)

	// Empty delete should be a no-op
	if err := mgr.DeleteMany(ctx, nil); err != nil {
		t.Fatalf("DeleteMany empty should succeed: %v", err)
	}
	assertCount(t, ctx, mgr, 5)
}

func TestIntegration_DeleteMany_AllRecords(t *testing.T) {
	db := setupTestDBDefault(t)
	ctx := context.Background()
	mgr := gotype.NewManager[Person](db)

	persons := seedPersons(t, ctx, mgr)

	if err := mgr.DeleteMany(ctx, persons); err != nil {
		t.Fatalf("DeleteMany all failed: %v", err)
	}
	assertCount(t, ctx, mgr, 0)
}

func TestIntegration_Delete_Strict_NotFound(t *testing.T) {
	db := setupTestDBDefault(t)
	ctx := context.Background()
	mgr := gotype.NewManager[Person](db)

	// Insert and get (for IID), then delete it
	p := insertAndGet(t, ctx, mgr, &Person{Name: "Temp", Email: "temp@test.com"}, "name", "Temp")
	assertDelete(t, ctx, mgr, p)

	// Now strict delete should fail
	err := mgr.Delete(ctx, p, gotype.WithStrict())
	if err == nil {
		t.Fatal("expected strict delete error for already-deleted instance")
	}
}

func TestIntegration_Delete_Strict_Found(t *testing.T) {
	db := setupTestDBDefault(t)
	ctx := context.Background()
	mgr := gotype.NewManager[Person](db)

	p := insertAndGet(t, ctx, mgr, &Person{Name: "Exists", Email: "exists@test.com"}, "name", "Exists")

	// Strict delete should succeed
	err := mgr.Delete(ctx, p, gotype.WithStrict())
	if err != nil {
		t.Fatalf("strict delete of existing instance failed: %v", err)
	}
	assertCount(t, ctx, mgr, 0)
}

func TestIntegration_DeleteMany_Strict(t *testing.T) {
	db := setupTestDBDefault(t)
	ctx := context.Background()
	mgr := gotype.NewManager[Person](db)

	persons := seedPersons(t, ctx, mgr)

	// Delete one person first
	assertDelete(t, ctx, mgr, persons[0])

	// Strict DeleteMany including the already-deleted person should fail
	err := mgr.DeleteMany(ctx, persons, gotype.WithStrict())
	if err == nil {
		t.Fatal("expected strict DeleteMany error when one instance is missing")
	}

	// The remaining 4 should still be there (strict check happens before delete)
	assertCount(t, ctx, mgr, 4)
}
