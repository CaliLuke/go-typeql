//go:build integration && cgo && typedb

package gotype_test

import (
	"context"
	"testing"

	"github.com/CaliLuke/go-typeql/gotype"
)

// ---------------------------------------------------------------------------
// Advanced delete tests (ported from Python test_delete.py)
// ---------------------------------------------------------------------------

func TestIntegration_DeleteNonexistent_Idempotent(t *testing.T) {
	// Ported from test_delete_nonexistent_entity_with_key_is_idempotent.
	// Deleting a non-existent entity (by IID) should not error — the
	// match simply finds nothing, so delete is a no-op.
	db := setupTestDBDefault(t)
	ctx := context.Background()

	mgr := gotype.NewManager[Person](db)

	// Insert and get (for IID).
	p := insertAndGet(t, ctx, mgr, &Person{Name: "Ephemeral", Email: "eph@test.com"}, "name", "Ephemeral")

	// Delete once.
	assertDelete(t, ctx, mgr, p)
	assertCount(t, ctx, mgr, 0)

	// Delete again — should not error.
	err := mgr.Delete(ctx, p)
	if err != nil {
		t.Errorf("expected idempotent delete, got error: %v", err)
	}
}

func TestIntegration_FilterDelete_NoMatches(t *testing.T) {
	// Ported from test_filter_based_delete_still_works (no-match case).
	// Query-based delete that matches nothing should not error.
	db := setupTestDBDefault(t)
	ctx := context.Background()

	mgr := gotype.NewManager[Person](db)
	seedPersons(t, ctx, mgr)

	_, err := mgr.Query().Filter(gotype.Eq("name", "Ghost")).Delete(ctx)
	if err != nil {
		t.Errorf("expected no error on no-match delete, got: %v", err)
	}

	// All 5 remain.
	assertCount(t, ctx, mgr, 5)
}

func TestIntegration_DeleteRelation_Nonexistent_Idempotent(t *testing.T) {
	// Ported from test_delete_nonexistent_relation_is_idempotent.
	// Deleting a non-existent relation should not error.
	db := setupRelationDB(t)
	ctx := context.Background()

	personMgr := gotype.NewManager[Person](db)
	companyMgr := gotype.NewManager[Company](db)
	empMgr := gotype.NewManager[Employment](db)

	p := insertAndGet(t, ctx, personMgr, &Person{Name: "TempPerson", Email: "temp@test.com"}, "name", "TempPerson")
	c := insertAndGet(t, ctx, companyMgr, &Company{Name: "TempCorp", Industry: "Test"}, "name", "TempCorp")

	assertInsert(t, ctx, empMgr, &Employment{Employee: p, Employer: c, StartDate: "2024-01-01"})
	rels := assertCount(t, ctx, empMgr, 1)

	// Delete once.
	assertDelete(t, ctx, empMgr, rels[0])
	assertCount(t, ctx, empMgr, 0)

	// Delete again — should not error.
	err := empMgr.Delete(ctx, rels[0])
	if err != nil {
		t.Errorf("expected idempotent relation delete, got error: %v", err)
	}
}

func TestIntegration_DeleteRelation_PreservesEntities(t *testing.T) {
	// Ported from test_delete_relation_preserves_role_player_entities.
	// Deleting a relation should NOT delete its role player entities.
	db := setupRelationDB(t)
	ctx := context.Background()

	personMgr := gotype.NewManager[Person](db)
	companyMgr := gotype.NewManager[Company](db)
	empMgr := gotype.NewManager[Employment](db)

	p := insertAndGet(t, ctx, personMgr, &Person{Name: "Survivor", Email: "surv@test.com"}, "name", "Survivor")
	c := insertAndGet(t, ctx, companyMgr, &Company{Name: "SurvCorp", Industry: "Test"}, "name", "SurvCorp")

	assertInsert(t, ctx, empMgr, &Employment{Employee: p, Employer: c, StartDate: "2024-01-01"})
	rels := assertCount(t, ctx, empMgr, 1)

	assertDelete(t, ctx, empMgr, rels[0])

	// Person and company still exist.
	assertGetOne(t, ctx, personMgr, map[string]any{"name": "Survivor"})
	assertGetOne(t, ctx, companyMgr, map[string]any{"name": "SurvCorp"})
}
