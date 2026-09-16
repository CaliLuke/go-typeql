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

type PutKeyedEmployment struct {
	gotype.BaseRelation
	Employee  *Person  `typedb:"role:employee"`
	Employer  *Company `typedb:"role:employer"`
	Reference string   `typedb:"reference,key"`
}

func TestIntegration_Put_Entity_New(t *testing.T) {
	db := setupTestDBDefault(t)
	ctx := context.Background()
	mgr := gotype.MustNewManager[Person](db)

	p := &Person{Name: "PutPerson", Email: "put@test.com", Age: new(40)}
	if err := mgr.Put(ctx, p); err != nil {
		t.Fatalf("Put new entity failed: %v", err)
	}

	if p.GetIID() == "" {
		t.Error("expected IID to be set after Put")
	}

	// Verify it's in the DB
	fetched := assertGetOne(t, ctx, mgr, map[string]any{"name": "PutPerson"})
	if fetched.GetIID() != p.GetIID() {
		t.Errorf("fetched IID %q differs from put IID %q", fetched.GetIID(), p.GetIID())
	}
	if fetched.Email != "put@test.com" {
		t.Errorf("expected email put@test.com, got %q", fetched.Email)
	}
}

func TestIntegration_Put_Entity_Idempotent(t *testing.T) {
	db := setupTestDBDefault(t)
	ctx := context.Background()
	mgr := gotype.MustNewManager[Person](db)

	// Put the same entity twice — should not create a duplicate
	p1 := &Person{Name: "Idem", Email: "idem@test.com"}
	if err := mgr.Put(ctx, p1); err != nil {
		t.Fatalf("Put 1 failed: %v", err)
	}

	p2 := &Person{Name: "Idem", Email: "idem@test.com"}
	if err := mgr.Put(ctx, p2); err != nil {
		t.Fatalf("Put 2 failed: %v", err)
	}
	if p1.GetIID() != p2.GetIID() {
		t.Errorf("upserted IID %q differs from existing IID %q", p2.GetIID(), p1.GetIID())
	}

	assertCount(t, ctx, mgr, 1)
}

func TestIntegration_Put_Relation(t *testing.T) {
	db := setupRelationDB(t)
	ctx := context.Background()

	personMgr := gotype.MustNewManager[Person](db)
	companyMgr := gotype.MustNewManager[Company](db)
	empMgr := gotype.MustNewManager[Employment](db)

	p := insertAndGet(t, ctx, personMgr, &Person{Name: "PutEmp", Email: "putemp@test.com"}, "name", "PutEmp")
	c := insertAndGet(t, ctx, companyMgr, &Company{Name: "PutCorp", Industry: "Tech"}, "name", "PutCorp")

	emp := &Employment{Employee: p, Employer: c, StartDate: "2024-06-01"}
	if err := empMgr.Put(ctx, emp); err != nil {
		t.Fatalf("Put relation failed: %v", err)
	}
	if emp.GetIID() != "" {
		t.Errorf("keyless relation unexpectedly assigned IID %q", emp.GetIID())
	}

	assertCount(t, ctx, empMgr, 1)
}

func TestIntegration_PutMany(t *testing.T) {
	db := setupTestDBDefault(t)
	ctx := context.Background()
	mgr := gotype.MustNewManager[Person](db)

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
		fetched := assertGetOne(t, ctx, mgr, map[string]any{"name": p.Name})
		if fetched.GetIID() != p.GetIID() {
			t.Errorf("persons[%d]: fetched IID %q differs from put IID %q", i, fetched.GetIID(), p.GetIID())
		}
	}
}

func TestIntegration_PutMany_DuplicateKey(t *testing.T) {
	db := setupTestDBDefault(t)
	ctx := context.Background()
	mgr := gotype.MustNewManager[Person](db)
	first := &Person{Name: "Repeated", Email: "repeat@test.com"}
	second := &Person{Name: "Repeated", Email: "repeat@test.com"}
	if err := mgr.PutMany(ctx, []*Person{first, second}); err != nil {
		t.Fatalf("PutMany duplicate key failed: %v", err)
	}
	if first.GetIID() == "" || first.GetIID() != second.GetIID() {
		t.Errorf("duplicate key should return one IID, got %q and %q", first.GetIID(), second.GetIID())
	}
	assertCount(t, ctx, mgr, 1)
}

func TestIntegration_Put_KeyedRelation(t *testing.T) {
	db := setupTestDBWith(t, func() {
		gotype.MustRegister[Person]()
		gotype.MustRegister[Company]()
		gotype.MustRegister[PutKeyedEmployment]()
	})
	ctx := context.Background()
	personMgr := gotype.MustNewManager[Person](db)
	companyMgr := gotype.MustNewManager[Company](db)
	relationMgr := gotype.MustNewManager[PutKeyedEmployment](db)
	p := insertAndGet(t, ctx, personMgr, &Person{Name: "KeyedEmployee", Email: "keyed@test.com"}, "name", "KeyedEmployee")
	c := insertAndGet(t, ctx, companyMgr, &Company{Name: "KeyedEmployer", Industry: "Tech"}, "name", "KeyedEmployer")
	first := &PutKeyedEmployment{Employee: p, Employer: c, Reference: "employment-1"}
	second := &PutKeyedEmployment{Employee: p, Employer: c, Reference: "employment-1"}
	if err := relationMgr.Put(ctx, first); err != nil {
		t.Fatalf("Put keyed relation failed: %v", err)
	}
	if err := relationMgr.Put(ctx, second); err != nil {
		t.Fatalf("Put existing keyed relation failed: %v", err)
	}
	if first.GetIID() == "" || first.GetIID() != second.GetIID() {
		t.Errorf("keyed relation should return same IID, got %q and %q", first.GetIID(), second.GetIID())
	}
	assertCount(t, ctx, relationMgr, 1)
}

func TestIntegration_PutMany_Empty(t *testing.T) {
	db := setupTestDBDefault(t)
	ctx := context.Background()
	mgr := gotype.MustNewManager[Person](db)

	if err := mgr.PutMany(ctx, nil); err != nil {
		t.Fatalf("PutMany empty should succeed: %v", err)
	}
	assertCount(t, ctx, mgr, 0)
}
