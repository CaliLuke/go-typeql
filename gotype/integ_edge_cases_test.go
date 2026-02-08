//go:build integration && cgo && typedb

package gotype_test

import (
	"context"
	"testing"

	"github.com/CaliLuke/go-typeql/gotype"
)

// ---------------------------------------------------------------------------
// Update edge cases (ported from Python test_crud_edge_cases.py,
// test_update_advanced.py)
// ---------------------------------------------------------------------------

func TestIntegration_UpdatePreservesOtherAttributes(t *testing.T) {
	// Ported from test_update_preserves_other_attributes.
	// Updating one attribute should not affect other attributes.
	db := setupTestDBDefault(t)
	ctx := context.Background()

	mgr := gotype.NewManager[Person](db)

	assertInsert(t, ctx, mgr, &Person{Name: "Diana", Email: "diana@test.com", Age: intPtr(30)})
	fetched := assertGetOne(t, ctx, mgr, map[string]any{"name": "Diana"})

	// Update only age.
	fetched.Age = intPtr(31)
	assertUpdate(t, ctx, mgr, fetched)

	// Verify email is preserved.
	updated := assertGetOne(t, ctx, mgr, map[string]any{"name": "Diana"})
	if updated.Age == nil || *updated.Age != 31 {
		t.Errorf("expected age 31, got %v", updated.Age)
	}
	if updated.Email != "diana@test.com" {
		t.Errorf("expected email diana@test.com preserved, got %q", updated.Email)
	}
}

func TestIntegration_UpdateMultipleEntitiesSeparately(t *testing.T) {
	// Ported from test_update_multiple_entities_separately.
	// Updating one entity should not affect another.
	db := setupTestDBDefault(t)
	ctx := context.Background()

	mgr := gotype.NewManager[Person](db)

	assertInsert(t, ctx, mgr, &Person{Name: "Grace", Email: "grace@test.com", Age: intPtr(100)})
	assertInsert(t, ctx, mgr, &Person{Name: "Henry2", Email: "henry2@test.com", Age: intPtr(200)})

	// Update Grace only.
	grace := assertGetOne(t, ctx, mgr, map[string]any{"name": "Grace"})
	grace.Age = intPtr(150)
	assertUpdate(t, ctx, mgr, grace)

	// Verify Grace updated.
	updatedGrace := assertGetOne(t, ctx, mgr, map[string]any{"name": "Grace"})
	if updatedGrace.Age == nil || *updatedGrace.Age != 150 {
		t.Errorf("expected Grace age 150, got %v", updatedGrace.Age)
	}

	// Verify Henry2 unchanged.
	updatedHenry := assertGetOne(t, ctx, mgr, map[string]any{"name": "Henry2"})
	if updatedHenry.Age == nil || *updatedHenry.Age != 200 {
		t.Errorf("expected Henry2 age 200 (unchanged), got %v", updatedHenry.Age)
	}
}

func TestIntegration_UpdateOptional_MultipleAttrs(t *testing.T) {
	// Ported from test_update_multiple_optional_attrs.
	// Update multiple optional fields at once.
	db := setupTestDBWith(t, func() {
		_ = gotype.Register[Profile]()
	})
	ctx := context.Background()

	mgr := gotype.NewManager[Profile](db)

	assertInsert(t, ctx, mgr, &Profile{Username: "dave"})
	fetched := assertGetOne(t, ctx, mgr, map[string]any{"username": "dave"})

	// Set both bio and score.
	fetched.Bio = stringPtr("A software developer")
	fetched.Score = float64Ptr(95.5)
	assertUpdate(t, ctx, mgr, fetched)

	updated := assertGetOne(t, ctx, mgr, map[string]any{"username": "dave"})
	if updated.Bio == nil || *updated.Bio != "A software developer" {
		t.Errorf("expected bio set, got %v", updated.Bio)
	}
	if updated.Score == nil || *updated.Score != 95.5 {
		t.Errorf("expected score 95.5, got %v", updated.Score)
	}
}

func TestIntegration_UpdateOptional_ValueToNil(t *testing.T) {
	// Ported from test_update_optional_from_value_to_none.
	// Setting an optional attribute from a value back to nil should
	// remove it from the database.
	db := setupTestDBWith(t, func() {
		_ = gotype.Register[Profile]()
	})
	ctx := context.Background()

	mgr := gotype.NewManager[Profile](db)

	// Insert with bio set.
	assertInsert(t, ctx, mgr, &Profile{Username: "bob", Bio: stringPtr("old bio")})
	fetched := assertGetOne(t, ctx, mgr, map[string]any{"username": "bob"})

	if fetched.Bio == nil || *fetched.Bio != "old bio" {
		t.Fatalf("precondition: expected bio 'old bio', got %v", fetched.Bio)
	}

	// Set bio to nil.
	fetched.Bio = nil
	assertUpdate(t, ctx, mgr, fetched)

	// Verify bio is removed.
	updated := assertGetOne(t, ctx, mgr, map[string]any{"username": "bob"})
	if updated.Bio != nil {
		t.Errorf("expected bio nil after setting to nil, got %q", *updated.Bio)
	}
}

func TestIntegration_OptionalAttrLifecycle(t *testing.T) {
	// Ported from test_optional_attribute_lifecycle.
	// Full lifecycle: nil → value → nil.
	db := setupTestDBWith(t, func() {
		_ = gotype.Register[Profile]()
	})
	ctx := context.Background()

	mgr := gotype.NewManager[Profile](db)

	// 1. Insert without optional attr.
	assertInsert(t, ctx, mgr, &Profile{Username: "alice"})
	fetched := assertGetOne(t, ctx, mgr, map[string]any{"username": "alice"})
	if fetched.Bio != nil {
		t.Fatalf("step 1: expected nil bio, got %v", *fetched.Bio)
	}

	// 2. Add optional attr.
	fetched.Bio = stringPtr("Ali's bio")
	assertUpdate(t, ctx, mgr, fetched)

	fetched2 := assertGetOne(t, ctx, mgr, map[string]any{"username": "alice"})
	if fetched2.Bio == nil || *fetched2.Bio != "Ali's bio" {
		t.Fatalf("step 2: expected bio 'Ali's bio', got %v", fetched2.Bio)
	}

	// 3. Remove optional attr.
	fetched2.Bio = nil
	assertUpdate(t, ctx, mgr, fetched2)

	fetched3 := assertGetOne(t, ctx, mgr, map[string]any{"username": "alice"})
	if fetched3.Bio != nil {
		t.Errorf("step 3: expected nil bio after removal, got %q", *fetched3.Bio)
	}
}

// ---------------------------------------------------------------------------
// Delete edge cases (ported from Python test_crud_edge_cases.py)
// ---------------------------------------------------------------------------

func TestIntegration_DeleteEntityCascadesRelation(t *testing.T) {
	// Ported from test_delete_entity_with_relation_cascades.
	// TypeDB 3.x does NOT auto-cascade relation deletion when an entity
	// is deleted. We must delete the relation first, then the entity.
	db := setupTestDBDefault(t)
	ctx := context.Background()

	personMgr := gotype.NewManager[Person](db)
	companyMgr := gotype.NewManager[Company](db)
	empMgr := gotype.NewManager[Employment](db)

	bob := insertAndGet(t, ctx, personMgr, &Person{Name: "Bob", Email: "bob@test.com"}, "name", "Bob")
	techcorp := insertAndGet(t, ctx, companyMgr, &Company{Name: "TechCorp", Industry: "Tech"}, "name", "TechCorp")

	assertInsert(t, ctx, empMgr, &Employment{Employee: bob, Employer: techcorp, StartDate: "2024-01-01"})
	assertCount(t, ctx, empMgr, 1)

	// Delete the relation first, then the person.
	rels := assertCount(t, ctx, empMgr, 1)
	assertDelete(t, ctx, empMgr, rels[0])
	assertCount(t, ctx, empMgr, 0)

	assertDelete(t, ctx, personMgr, bob)

	// Person gone.
	persons, err := personMgr.Get(ctx, map[string]any{"name": "Bob"})
	if err != nil {
		t.Fatalf("get person: %v", err)
	}
	if len(persons) != 0 {
		t.Errorf("expected 0 persons after delete, got %d", len(persons))
	}

	// Company still exists.
	assertGetOne(t, ctx, companyMgr, map[string]any{"name": "TechCorp"})
}

func TestIntegration_DeleteRelationThenEntity(t *testing.T) {
	// Ported from test_delete_relation_then_entity_succeeds.
	// Explicitly deleting relation first, then entity should succeed.
	db := setupTestDBDefault(t)
	ctx := context.Background()

	personMgr := gotype.NewManager[Person](db)
	companyMgr := gotype.NewManager[Company](db)
	empMgr := gotype.NewManager[Employment](db)

	carol := insertAndGet(t, ctx, personMgr, &Person{Name: "Carol", Email: "carol@test.com"}, "name", "Carol")
	devcorp := insertAndGet(t, ctx, companyMgr, &Company{Name: "DevCorp", Industry: "Dev"}, "name", "DevCorp")

	assertInsert(t, ctx, empMgr, &Employment{Employee: carol, Employer: devcorp, StartDate: "2024-01-01"})

	// Delete relation first.
	rels := assertCount(t, ctx, empMgr, 1)
	assertDelete(t, ctx, empMgr, rels[0])
	assertCount(t, ctx, empMgr, 0)

	// Now delete person succeeds.
	assertDelete(t, ctx, personMgr, carol)
	assertCount(t, ctx, personMgr, 0)
}

// ---------------------------------------------------------------------------
// Relation update edge cases (ported from Python test_update.py relations)
// ---------------------------------------------------------------------------

func TestIntegration_UpdateRelationPreservesRolePlayers(t *testing.T) {
	// Ported from test_updating_relation_preserves_role_players.
	// Updating a relation's attribute should not change its role players.
	db := setupRelationDB(t)
	ctx := context.Background()

	personMgr := gotype.NewManager[Person](db)
	companyMgr := gotype.NewManager[Company](db)
	empMgr := gotype.NewManager[Employment](db)

	p := insertAndGet(t, ctx, personMgr, &Person{Name: "Preservee", Email: "pres@test.com"}, "name", "Preservee")
	c := insertAndGet(t, ctx, companyMgr, &Company{Name: "PresCorp", Industry: "Tech"}, "name", "PresCorp")

	assertInsert(t, ctx, empMgr, &Employment{Employee: p, Employer: c, StartDate: "2023-01-01"})

	// Update the relation's attribute.
	rels := assertCount(t, ctx, empMgr, 1)
	rels[0].StartDate = "2025-06-01"
	assertUpdate(t, ctx, empMgr, rels[0])

	// Verify attribute changed.
	updated := assertCount(t, ctx, empMgr, 1)
	if updated[0].StartDate != "2025-06-01" {
		t.Errorf("expected start-date 2025-06-01, got %q", updated[0].StartDate)
	}

	// Verify role players still exist.
	assertGetOne(t, ctx, personMgr, map[string]any{"name": "Preservee"})
	assertGetOne(t, ctx, companyMgr, map[string]any{"name": "PresCorp"})
}
