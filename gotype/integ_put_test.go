//go:build integration && cgo && typedb

package gotype_test

import (
	"context"
	"testing"

	"github.com/CaliLuke/go-typeql/gotype"
)

// ---------------------------------------------------------------------------
// Put (upsert) integration tests
// ---------------------------------------------------------------------------

func TestIntegration_Put_Entity_New(t *testing.T) {
	db := setupTestDBDefault(t)
	ctx := context.Background()
	mgr := gotype.NewManager[Person](db)

	p := &Person{Name: "PutPerson", Email: "put@test.com", Age: new(40)}
	if err := mgr.Put(ctx, p); err != nil {
		t.Fatalf("Put new entity failed: %v", err)
	}

	if p.GetIID() == "" {
		t.Error("expected IID to be set after Put")
	}

	// Verify it's in the DB
	fetched := assertGetOne(t, ctx, mgr, map[string]any{"name": "PutPerson"})
	if fetched.Email != "put@test.com" {
		t.Errorf("expected email put@test.com, got %q", fetched.Email)
	}
}

func TestIntegration_Put_Entity_Idempotent(t *testing.T) {
	db := setupTestDBDefault(t)
	ctx := context.Background()
	mgr := gotype.NewManager[Person](db)

	// Put the same entity twice — should not create a duplicate
	p1 := &Person{Name: "Idem", Email: "idem@test.com"}
	if err := mgr.Put(ctx, p1); err != nil {
		t.Fatalf("Put 1 failed: %v", err)
	}

	p2 := &Person{Name: "Idem", Email: "idem@test.com"}
	if err := mgr.Put(ctx, p2); err != nil {
		t.Fatalf("Put 2 failed: %v", err)
	}

	assertCount(t, ctx, mgr, 1)
}

func TestIntegration_Put_Relation(t *testing.T) {
	db := setupRelationDB(t)
	ctx := context.Background()

	personMgr := gotype.NewManager[Person](db)
	companyMgr := gotype.NewManager[Company](db)
	empMgr := gotype.NewManager[Employment](db)

	p := insertAndGet(t, ctx, personMgr, &Person{Name: "PutEmp", Email: "putemp@test.com"}, "name", "PutEmp")
	c := insertAndGet(t, ctx, companyMgr, &Company{Name: "PutCorp", Industry: "Tech"}, "name", "PutCorp")

	emp := &Employment{Employee: p, Employer: c, StartDate: "2024-06-01"}
	if err := empMgr.Put(ctx, emp); err != nil {
		t.Fatalf("Put relation failed: %v", err)
	}

	assertCount(t, ctx, empMgr, 1)
}

func TestIntegration_PutMany(t *testing.T) {
	db := setupTestDBDefault(t)
	ctx := context.Background()
	mgr := gotype.NewManager[Person](db)

	persons := []*Person{
		{Name: "PM1", Email: "pm1@test.com"},
		{Name: "PM2", Email: "pm2@test.com"},
		{Name: "PM3", Email: "pm3@test.com"},
	}
	if err := mgr.PutMany(ctx, persons); err != nil {
		t.Fatalf("PutMany failed: %v", err)
	}

	assertCount(t, ctx, mgr, 3)

	// Verify IIDs were populated
	for i, p := range persons {
		if p.GetIID() == "" {
			t.Errorf("persons[%d]: expected IID to be set after PutMany", i)
		}
	}
}

func TestIntegration_PutMany_Empty(t *testing.T) {
	db := setupTestDBDefault(t)
	ctx := context.Background()
	mgr := gotype.NewManager[Person](db)

	if err := mgr.PutMany(ctx, nil); err != nil {
		t.Fatalf("PutMany empty should succeed: %v", err)
	}
	assertCount(t, ctx, mgr, 0)
}
