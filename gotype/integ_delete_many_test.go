//go:build integration && cgo && typedb

package gotype_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/CaliLuke/go-typeql/v3/gotype"
)

// ---------------------------------------------------------------------------
// DeleteMany integration tests
// ---------------------------------------------------------------------------

func TestIntegration_DeleteMany_Entities(t *testing.T) {
	db := setupTestDBDefault(t)
	ctx := context.Background()
	mgr := gotype.MustNewManager[Person](db)

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
	mgr := gotype.MustNewManager[Person](db)

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
	mgr := gotype.MustNewManager[Person](db)

	persons := seedPersons(t, ctx, mgr)

	if err := mgr.DeleteMany(ctx, persons); err != nil {
		t.Fatalf("DeleteMany all failed: %v", err)
	}
	assertCount(t, ctx, mgr, 0)
}

func TestIntegration_Delete_Strict_NotFound(t *testing.T) {
	db := setupTestDBDefault(t)
	ctx := context.Background()
	mgr := gotype.MustNewManager[Person](db)

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
	mgr := gotype.MustNewManager[Person](db)

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
	mgr := gotype.MustNewManager[Person](db)

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

func TestIntegration_DeleteMany_StrictUppercaseIID(t *testing.T) {
	db := setupTestDBDefault(t)
	ctx := context.Background()
	mgr := gotype.MustNewManager[Person](db)
	p := insertAndGet(t, ctx, mgr, &Person{Name: "Case Check", Email: "case@test.com"}, "name", "Case Check")
	upperIID := "0x" + strings.ToUpper(strings.TrimPrefix(p.GetIID(), "0x"))
	if upperIID == p.GetIID() {
		t.Skip("generated IID has no hex letters to case-fold")
	}
	variant := *p
	variant.SetIID(upperIID)
	if err := mgr.DeleteMany(ctx, []*Person{&variant, p}, gotype.WithStrict()); err != nil {
		t.Fatalf("strict delete with equivalent IID spellings: %v", err)
	}
	assertCount(t, ctx, mgr, 0)
}

func TestIntegration_DeleteMany_GroupedStrictDuplicates(t *testing.T) {
	db := setupTestDBWith(t, func() { gotype.MustRegister[Company]() })
	ctx := context.Background()
	mgr := gotype.MustNewManager[Company](db)
	rows := make([]*Company, 33)
	for i := range rows {
		rows[i] = &Company{Name: fmt.Sprintf("delete-co-%d", i), Industry: "Tools"}
	}
	if err := mgr.InsertMany(ctx, rows); err != nil {
		t.Fatal(err)
	}
	if err := mgr.DeleteMany(ctx, append(rows, rows[0]), gotype.WithStrict()); err != nil {
		t.Fatalf("grouped strict delete: %v", err)
	}
	assertCount(t, ctx, mgr, 0)
}

func TestIntegration_DeleteMany_GroupedRelations(t *testing.T) {
	db := setupRelationDB(t)
	ctx := context.Background()
	personMgr := gotype.MustNewManager[Person](db)
	companyMgr := gotype.MustNewManager[Company](db)
	relationMgr := gotype.MustNewManager[Employment](db)
	p := insertAndGet(t, ctx, personMgr, &Person{Name: "Employee", Email: "employee@test.com"}, "name", "Employee")
	c := insertAndGet(t, ctx, companyMgr, &Company{Name: "Employer", Industry: "Tools"}, "name", "Employer")
	for _, date := range []string{"2024-01-01", "2025-01-01"} {
		if err := relationMgr.Insert(ctx, &Employment{Employee: p, Employer: c, StartDate: date}); err != nil {
			t.Fatal(err)
		}
	}
	relations, err := relationMgr.Get(ctx, nil)
	if err != nil || len(relations) != 2 {
		t.Fatalf("Get relations: %v count=%d", err, len(relations))
	}
	if err := relationMgr.DeleteMany(ctx, relations, gotype.WithStrict()); err != nil {
		t.Fatalf("grouped relation delete: %v", err)
	}
	assertCount(t, ctx, relationMgr, 0)
}
