//go:build integration && cgo && typedb

package gotype_test

import (
	"context"
	"testing"

	"github.com/CaliLuke/go-typeql/gotype"
)

// ---------------------------------------------------------------------------
// IID population tests (ported from Python test_iid_feature.py)
// ---------------------------------------------------------------------------

func TestIntegration_IID_PopulatedAfterGet(t *testing.T) {
	// Ported from test_get_populates_iid.
	// get() should populate IID on returned entities.
	db := setupTestDBDefault(t)
	ctx := context.Background()

	mgr := gotype.NewManager[Person](db)

	assertInsert(t, ctx, mgr, &Person{Name: "Alice", Email: "alice@iid.com", Age: intPtr(30)})

	fetched := assertGetOne(t, ctx, mgr, map[string]any{"name": "Alice"})
	if fetched.GetIID() == "" {
		t.Error("expected IID to be populated after Get()")
	}
}

func TestIntegration_IID_PopulatedAfterFilter(t *testing.T) {
	// Ported from test_filter_execute_populates_iid.
	// Filter().Execute() should populate IID on returned entities.
	db := setupTestDBDefault(t)
	ctx := context.Background()

	mgr := gotype.NewManager[Person](db)

	assertInsert(t, ctx, mgr, &Person{Name: "Bob", Email: "bob@iid.com", Age: intPtr(25)})

	results, err := mgr.Query().Filter(gotype.Eq("name", "Bob")).Execute(ctx)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].GetIID() == "" {
		t.Error("expected IID to be populated after Filter().Execute()")
	}
}

func TestIntegration_IID_StableAcrossQueries(t *testing.T) {
	// Ported from test_iid_is_stable_across_queries.
	// The same entity should return the same IID across multiple queries.
	db := setupTestDBDefault(t)
	ctx := context.Background()

	mgr := gotype.NewManager[Person](db)

	assertInsert(t, ctx, mgr, &Person{Name: "Charlie", Email: "charlie@iid.com", Age: intPtr(35)})

	first := assertGetOne(t, ctx, mgr, map[string]any{"name": "Charlie"})
	second := assertGetOne(t, ctx, mgr, map[string]any{"name": "Charlie"})

	if first.GetIID() == "" {
		t.Fatal("first fetch: IID is empty")
	}
	if first.GetIID() != second.GetIID() {
		t.Errorf("IID not stable: first=%q, second=%q", first.GetIID(), second.GetIID())
	}
}

func TestIntegration_IID_UniquePerEntity(t *testing.T) {
	// Each entity should get a unique IID.
	db := setupTestDBDefault(t)
	ctx := context.Background()

	mgr := gotype.NewManager[Person](db)

	assertInsert(t, ctx, mgr, &Person{Name: "UniqueA", Email: "a@iid.com", Age: intPtr(20)})
	assertInsert(t, ctx, mgr, &Person{Name: "UniqueB", Email: "b@iid.com", Age: intPtr(21)})
	assertInsert(t, ctx, mgr, &Person{Name: "UniqueC", Email: "c@iid.com", Age: intPtr(22)})

	a := assertGetOne(t, ctx, mgr, map[string]any{"name": "UniqueA"})
	b := assertGetOne(t, ctx, mgr, map[string]any{"name": "UniqueB"})
	c := assertGetOne(t, ctx, mgr, map[string]any{"name": "UniqueC"})

	iids := map[string]bool{a.GetIID(): true, b.GetIID(): true, c.GetIID(): true}
	if len(iids) != 3 {
		t.Errorf("expected 3 unique IIDs, got %d (a=%q, b=%q, c=%q)",
			len(iids), a.GetIID(), b.GetIID(), c.GetIID())
	}
}

func TestIntegration_IID_PopulatedAfterAll(t *testing.T) {
	// All() should populate IID on all returned entities.
	db := setupTestDBDefault(t)
	ctx := context.Background()

	mgr := gotype.NewManager[Person](db)

	assertInsert(t, ctx, mgr, &Person{Name: "AllA", Email: "alla@iid.com"})
	assertInsert(t, ctx, mgr, &Person{Name: "AllB", Email: "allb@iid.com"})

	all := assertCount(t, ctx, mgr, 2)
	for _, p := range all {
		if p.GetIID() == "" {
			t.Errorf("expected IID populated for %q, got empty", p.Name)
		}
	}
}

// ---------------------------------------------------------------------------
// Relation IID tests (ported from TestRelationIidPopulation)
// ---------------------------------------------------------------------------

func TestIntegration_IID_RelationPopulated(t *testing.T) {
	// Ported from test_relation_get_populates_iid.
	// Relations returned from All() should have IIDs populated.
	db := setupTestDBDefault(t)
	ctx := context.Background()

	personMgr := gotype.NewManager[Person](db)
	companyMgr := gotype.NewManager[Company](db)
	empMgr := gotype.NewManager[Employment](db)

	p := insertAndGet(t, ctx, personMgr, &Person{Name: "Eve", Email: "eve@iid.com", Age: intPtr(28)}, "name", "Eve")
	c := insertAndGet(t, ctx, companyMgr, &Company{Name: "IIDCorp", Industry: "Tech"}, "name", "IIDCorp")

	assertInsert(t, ctx, empMgr, &Employment{Employee: p, Employer: c, StartDate: "2024-01-01"})

	rels := assertCount(t, ctx, empMgr, 1)
	if rels[0].GetIID() == "" {
		t.Error("expected relation IID to be populated")
	}
}

func TestIntegration_IID_RelationUniquePerRelation(t *testing.T) {
	// Ported from test_all_returns_unique_role_player_iids_issue_78.
	// Multiple relations should have unique IIDs.
	db := setupRelationDB(t)
	ctx := context.Background()

	personMgr := gotype.NewManager[Person](db)
	companyMgr := gotype.NewManager[Company](db)
	empMgr := gotype.NewManager[Employment](db)

	p1 := insertAndGet(t, ctx, personMgr, &Person{Name: "PersonA", Email: "pa@iid.com"}, "name", "PersonA")
	p2 := insertAndGet(t, ctx, personMgr, &Person{Name: "PersonB", Email: "pb@iid.com"}, "name", "PersonB")
	c1 := insertAndGet(t, ctx, companyMgr, &Company{Name: "CompanyX", Industry: "X"}, "name", "CompanyX")
	c2 := insertAndGet(t, ctx, companyMgr, &Company{Name: "CompanyY", Industry: "Y"}, "name", "CompanyY")

	assertInsert(t, ctx, empMgr, &Employment{Employee: p1, Employer: c1, StartDate: "2024-01-01"})
	assertInsert(t, ctx, empMgr, &Employment{Employee: p2, Employer: c2, StartDate: "2024-02-01"})

	all := assertCount(t, ctx, empMgr, 2)

	if all[0].GetIID() == "" || all[1].GetIID() == "" {
		t.Fatal("expected both relation IIDs populated")
	}
	if all[0].GetIID() == all[1].GetIID() {
		t.Errorf("expected unique relation IIDs, both got %q", all[0].GetIID())
	}

	// Also verify entity IIDs are unique.
	entityIIDs := map[string]bool{
		p1.GetIID(): true, p2.GetIID(): true,
		c1.GetIID(): true, c2.GetIID(): true,
	}
	if len(entityIIDs) != 4 {
		t.Errorf("expected 4 unique entity IIDs, got %d", len(entityIIDs))
	}
}

func TestIntegration_IID_ByIIDFilterConsistent(t *testing.T) {
	// Verify ByIID filter returns the same entity that was fetched by key.
	db := setupTestDBDefault(t)
	ctx := context.Background()

	mgr := gotype.NewManager[Person](db)

	assertInsert(t, ctx, mgr, &Person{Name: "IIDLookup", Email: "lookup@iid.com", Age: intPtr(40)})

	// Get by key filter.
	byKey := assertGetOne(t, ctx, mgr, map[string]any{"name": "IIDLookup"})
	iid := byKey.GetIID()
	if iid == "" {
		t.Skip("IID not available")
	}

	// Get by IID filter.
	results, err := mgr.Query().Filter(gotype.ByIID(iid)).Execute(ctx)
	if err != nil {
		t.Fatalf("ByIID query: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result from ByIID, got %d", len(results))
	}

	// Should be the same entity.
	if results[0].Name != "IIDLookup" {
		t.Errorf("expected name IIDLookup, got %q", results[0].Name)
	}
	if results[0].GetIID() != iid {
		t.Errorf("IID mismatch: expected %q, got %q", iid, results[0].GetIID())
	}
}
