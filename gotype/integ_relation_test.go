//go:build integration && cgo && typedb

package gotype_test

import (
	"context"
	"testing"

	"github.com/CaliLuke/go-typeql/gotype"
)

func setupRelationDB(t *testing.T) *gotype.Database {
	return setupTestDBWith(t, func() {
		_ = gotype.Register[Person]()
		_ = gotype.Register[Company]()
		_ = gotype.Register[Employment]()
		_ = gotype.Register[Friendship]()
		_ = gotype.Register[Membership]()
	})
}

func TestIntegration_Relation_SelfReferencing(t *testing.T) {
	db := setupRelationDB(t)
	ctx := context.Background()

	personMgr := gotype.NewManager[Person](db)
	alice := insertAndGet(t, ctx, personMgr, &Person{Name: "Alice", Email: "alice@example.com", Age: intPtr(30)}, "name", "Alice")
	bob := insertAndGet(t, ctx, personMgr, &Person{Name: "Bob", Email: "bob@example.com", Age: intPtr(25)}, "name", "Bob")

	friendMgr := gotype.NewManager[Friendship](db)
	assertInsert(t, ctx, friendMgr, &Friendship{
		Friend1: alice,
		Friend2: bob,
		Since:   "2024-01-01",
	})

	all := assertCount(t, ctx, friendMgr, 1)
	if all[0].Since != "2024-01-01" {
		t.Errorf("expected since 2024-01-01, got %q", all[0].Since)
	}
}

func TestIntegration_Relation_MultipleAttributes(t *testing.T) {
	db := setupRelationDB(t)
	ctx := context.Background()

	personMgr := gotype.NewManager[Person](db)
	companyMgr := gotype.NewManager[Company](db)

	alice := insertAndGet(t, ctx, personMgr, &Person{Name: "Alice", Email: "alice@example.com"}, "name", "Alice")
	corp := insertAndGet(t, ctx, companyMgr, &Company{Name: "Corp", Industry: "Tech"}, "name", "Corp")

	memMgr := gotype.NewManager[Membership](db)
	assertInsert(t, ctx, memMgr, &Membership{
		Member: alice,
		Group:  corp,
		Title:  "Lead",
		Active: boolPtr(true),
	})

	all := assertCount(t, ctx, memMgr, 1)
	if all[0].Title != "Lead" {
		t.Errorf("expected title Lead, got %q", all[0].Title)
	}
	if all[0].Active == nil || *all[0].Active != true {
		t.Errorf("expected active true, got %v", all[0].Active)
	}
}

func TestIntegration_Relation_FetchByAttributeFilter(t *testing.T) {
	db := setupRelationDB(t)
	ctx := context.Background()

	personMgr := gotype.NewManager[Person](db)
	companyMgr := gotype.NewManager[Company](db)

	p1 := insertAndGet(t, ctx, personMgr, &Person{Name: "P1", Email: "p1@example.com"}, "name", "P1")
	p2 := insertAndGet(t, ctx, personMgr, &Person{Name: "P2", Email: "p2@example.com"}, "name", "P2")
	c := insertAndGet(t, ctx, companyMgr, &Company{Name: "Acme", Industry: "Mfg"}, "name", "Acme")

	empMgr := gotype.NewManager[Employment](db)
	assertInsert(t, ctx, empMgr, &Employment{Employee: p1, Employer: c, StartDate: "2023-01-01"})
	assertInsert(t, ctx, empMgr, &Employment{Employee: p2, Employer: c, StartDate: "2024-06-15"})

	results, err := empMgr.Get(ctx, map[string]any{"start-date": "2024-06-15"})
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if len(results) != 1 {
		t.Errorf("expected 1 relation by date filter, got %d", len(results))
	}
}

func TestIntegration_Relation_UpdateAttribute(t *testing.T) {
	db := setupRelationDB(t)
	ctx := context.Background()

	personMgr := gotype.NewManager[Person](db)
	companyMgr := gotype.NewManager[Company](db)

	p := insertAndGet(t, ctx, personMgr, &Person{Name: "Updater", Email: "updater@example.com"}, "name", "Updater")
	c := insertAndGet(t, ctx, companyMgr, &Company{Name: "UpdateCorp", Industry: "Tech"}, "name", "UpdateCorp")

	empMgr := gotype.NewManager[Employment](db)
	assertInsert(t, ctx, empMgr, &Employment{Employee: p, Employer: c, StartDate: "2023-01-01"})

	rels := assertCount(t, ctx, empMgr, 1)
	rels[0].StartDate = "2025-01-01"
	assertUpdate(t, ctx, empMgr, rels[0])

	rels2 := assertCount(t, ctx, empMgr, 1)
	if rels2[0].StartDate != "2025-01-01" {
		t.Errorf("expected start-date 2025-01-01, got %q", rels2[0].StartDate)
	}
}

func TestIntegration_Relation_DeleteKeepsPlayers(t *testing.T) {
	db := setupRelationDB(t)
	ctx := context.Background()

	personMgr := gotype.NewManager[Person](db)
	companyMgr := gotype.NewManager[Company](db)

	p := insertAndGet(t, ctx, personMgr, &Person{Name: "Keeper", Email: "keeper@example.com"}, "name", "Keeper")
	c := insertAndGet(t, ctx, companyMgr, &Company{Name: "KeepCorp", Industry: "Fin"}, "name", "KeepCorp")

	empMgr := gotype.NewManager[Employment](db)
	assertInsert(t, ctx, empMgr, &Employment{Employee: p, Employer: c, StartDate: "2023-01-01"})

	rels := assertCount(t, ctx, empMgr, 1)
	assertDelete(t, ctx, empMgr, rels[0])
	assertCount(t, ctx, empMgr, 0)

	// Role players still exist.
	pAfter, _ := personMgr.Get(ctx, map[string]any{"name": "Keeper"})
	if len(pAfter) != 1 {
		t.Errorf("expected person to persist after relation delete, got %d", len(pAfter))
	}
	cAfter, _ := companyMgr.Get(ctx, map[string]any{"name": "KeepCorp"})
	if len(cAfter) != 1 {
		t.Errorf("expected company to persist after relation delete, got %d", len(cAfter))
	}
}

func TestIntegration_Relation_InsertMany(t *testing.T) {
	db := setupRelationDB(t)
	ctx := context.Background()

	personMgr := gotype.NewManager[Person](db)
	companyMgr := gotype.NewManager[Company](db)

	assertInsertMany(t, ctx, personMgr, []*Person{
		{Name: "M1", Email: "m1@example.com"},
		{Name: "M2", Email: "m2@example.com"},
	})

	c := insertAndGet(t, ctx, companyMgr, &Company{Name: "BulkCorp", Industry: "Bulk"}, "name", "BulkCorp")
	p1 := assertGetOne(t, ctx, personMgr, map[string]any{"name": "M1"})
	p2 := assertGetOne(t, ctx, personMgr, map[string]any{"name": "M2"})

	empMgr := gotype.NewManager[Employment](db)
	assertInsertMany(t, ctx, empMgr, []*Employment{
		{Employee: p1, Employer: c, StartDate: "2024-01-01"},
		{Employee: p2, Employer: c, StartDate: "2024-02-01"},
	})

	assertCount(t, ctx, empMgr, 2)
}

func TestIntegration_Relation_GetAll(t *testing.T) {
	db := setupRelationDB(t)
	ctx := context.Background()

	personMgr := gotype.NewManager[Person](db)
	companyMgr := gotype.NewManager[Company](db)

	for i := 0; i < 3; i++ {
		p := &Person{Name: "AllRel" + string(rune('A'+i)), Email: "allrel" + string(rune('a'+i)) + "@example.com"}
		assertInsert(t, ctx, personMgr, p)
	}
	c := insertAndGet(t, ctx, companyMgr, &Company{Name: "AllRelCorp", Industry: "Test"}, "name", "AllRelCorp")

	empMgr := gotype.NewManager[Employment](db)
	for i := 0; i < 3; i++ {
		name := "AllRel" + string(rune('A'+i))
		ps := assertGetOne(t, ctx, personMgr, map[string]any{"name": name})
		assertInsert(t, ctx, empMgr, &Employment{Employee: ps, Employer: c, StartDate: "2024-01-0" + string(rune('1'+i))})
	}

	assertCount(t, ctx, empMgr, 3)
}
