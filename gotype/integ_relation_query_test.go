//go:build integration && cgo && typedb

package gotype_test

import (
	"context"
	"testing"

	"github.com/CaliLuke/go-typeql/gotype"
)

// ---------------------------------------------------------------------------
// Relation query/filter tests (ported from Python test_filter.py,
// test_update.py, test_delete.py for relations)
// ---------------------------------------------------------------------------

// setupRelationQueryDB creates a DB with standard models and multiple employments.
func setupRelationQueryDB(t *testing.T) (
	*gotype.Database,
	*gotype.Manager[Person],
	*gotype.Manager[Company],
	*gotype.Manager[Employment],
) {
	db := setupRelationDB(t)
	ctx := context.Background()

	personMgr := gotype.NewManager[Person](db)
	companyMgr := gotype.NewManager[Company](db)
	empMgr := gotype.NewManager[Employment](db)

	// Create entities.
	p1 := insertAndGet(t, ctx, personMgr, &Person{Name: "Ann", Email: "ann@rq.com", Age: intPtr(25)}, "name", "Ann")
	p2 := insertAndGet(t, ctx, personMgr, &Person{Name: "Ben", Email: "ben@rq.com", Age: intPtr(35)}, "name", "Ben")
	p3 := insertAndGet(t, ctx, personMgr, &Person{Name: "Cat", Email: "cat@rq.com", Age: intPtr(30)}, "name", "Cat")
	c1 := insertAndGet(t, ctx, companyMgr, &Company{Name: "AlphaCo", Industry: "Tech"}, "name", "AlphaCo")
	c2 := insertAndGet(t, ctx, companyMgr, &Company{Name: "BetaCo", Industry: "Finance"}, "name", "BetaCo")

	// Create relations.
	assertInsert(t, ctx, empMgr, &Employment{Employee: p1, Employer: c1, StartDate: "2023-01-01"})
	assertInsert(t, ctx, empMgr, &Employment{Employee: p2, Employer: c1, StartDate: "2023-06-15"})
	assertInsert(t, ctx, empMgr, &Employment{Employee: p3, Employer: c2, StartDate: "2024-01-01"})

	return db, personMgr, companyMgr, empMgr
}

func TestIntegration_RelationFilter_ByAttribute(t *testing.T) {
	// Ported from test_filter_relations_by_attribute.
	// Filter relations by an owned attribute.
	_, _, _, empMgr := setupRelationQueryDB(t)
	ctx := context.Background()

	results, err := empMgr.Query().
		Filter(gotype.Eq("start-date", "2023-06-15")).
		Execute(ctx)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(results) != 1 {
		t.Errorf("expected 1 relation with start-date 2023-06-15, got %d", len(results))
	}
	if len(results) == 1 && results[0].StartDate != "2023-06-15" {
		t.Errorf("expected start-date 2023-06-15, got %q", results[0].StartDate)
	}
}

func TestIntegration_RelationFilter_EmptyResult(t *testing.T) {
	// Ported from test_filter_relations_empty_result.
	// Filter that matches no relations returns empty list.
	_, _, _, empMgr := setupRelationQueryDB(t)
	ctx := context.Background()

	results, err := empMgr.Query().
		Filter(gotype.Eq("start-date", "1999-01-01")).
		Execute(ctx)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("expected 0, got %d", len(results))
	}
}

func TestIntegration_RelationFilter_MultipleResults(t *testing.T) {
	// Ported from test_filter_relations_with_multiple_matching_results.
	// Filter that matches multiple relations.
	_, _, _, empMgr := setupRelationQueryDB(t)
	ctx := context.Background()

	// Both Ann and Ben work at AlphaCo with dates starting with "2023-"
	results, err := empMgr.Query().
		Filter(gotype.Like("start-date", "2023.*")).
		Execute(ctx)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(results) != 2 {
		t.Errorf("expected 2 relations matching 2023-*, got %d", len(results))
	}
}

func TestIntegration_RelationFilter_LimitOffset(t *testing.T) {
	// Ported from test_filter_with_limit_and_offset.
	// Limit and offset work on relation queries.
	_, _, _, empMgr := setupRelationQueryDB(t)
	ctx := context.Background()

	// Get all 3, then verify limit.
	results, err := empMgr.Query().
		OrderAsc("start-date").
		Limit(2).
		Execute(ctx)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(results) != 2 {
		t.Errorf("expected 2 with limit, got %d", len(results))
	}

	// Offset 1 should skip 1.
	results, err = empMgr.Query().
		OrderAsc("start-date").
		Offset(1).
		Execute(ctx)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(results) != 2 {
		t.Errorf("expected 2 with offset 1 from 3, got %d", len(results))
	}
}

func TestIntegration_RelationFilter_FirstAndCount(t *testing.T) {
	// Ported from test_filter_first_and_count.
	// First() and Count() work on relation queries.
	_, _, _, empMgr := setupRelationQueryDB(t)
	ctx := context.Background()

	count, err := empMgr.Query().Count(ctx)
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 3 {
		t.Errorf("expected count 3, got %d", count)
	}

	first, err := empMgr.Query().OrderAsc("start-date").First(ctx)
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	if first == nil {
		t.Fatal("expected non-nil first result")
	}
	if first.StartDate != "2023-01-01" {
		t.Errorf("expected earliest start-date 2023-01-01, got %q", first.StartDate)
	}
}

func TestIntegration_RelationQuery_Delete(t *testing.T) {
	// Ported from test_relation_query_delete.
	// Query().Filter().Delete() works on relations.
	_, _, _, empMgr := setupRelationQueryDB(t)
	ctx := context.Background()

	// Delete relations with start-date = "2023-01-01".
	_, err := empMgr.Query().
		Filter(gotype.Eq("start-date", "2023-01-01")).
		Delete(ctx)
	if err != nil {
		t.Fatalf("delete: %v", err)
	}

	assertCount(t, ctx, empMgr, 2)
}

func TestIntegration_Relation_UpdateOptionalAttr(t *testing.T) {
	// Ported from test_update_relation_with_optional_attribute.
	// Updating a relation's optional attribute.
	db := setupRelationDB(t)
	ctx := context.Background()

	personMgr := gotype.NewManager[Person](db)
	companyMgr := gotype.NewManager[Company](db)
	memMgr := gotype.NewManager[Membership](db)

	p := insertAndGet(t, ctx, personMgr, &Person{Name: "OptRel", Email: "optrel@test.com"}, "name", "OptRel")
	c := insertAndGet(t, ctx, companyMgr, &Company{Name: "OptCorp", Industry: "Test"}, "name", "OptCorp")

	// Insert with optional active = nil.
	assertInsert(t, ctx, memMgr, &Membership{Member: p, Group: c, Title: "Intern", Active: nil})

	rels := assertCount(t, ctx, memMgr, 1)
	if rels[0].Active != nil {
		t.Fatalf("precondition: expected active nil, got %v", *rels[0].Active)
	}

	// Set optional to a value.
	rels[0].Active = boolPtr(true)
	assertUpdate(t, ctx, memMgr, rels[0])

	updated := assertCount(t, ctx, memMgr, 1)
	if updated[0].Active == nil || *updated[0].Active != true {
		t.Errorf("expected active true after update, got %v", updated[0].Active)
	}

	// Set optional back to nil.
	updated[0].Active = nil
	assertUpdate(t, ctx, memMgr, updated[0])

	final := assertCount(t, ctx, memMgr, 1)
	if final[0].Active != nil {
		t.Errorf("expected active nil after removal, got %v", *final[0].Active)
	}
}

func TestIntegration_Relation_UpdateMultiple(t *testing.T) {
	// Ported from test_update_multiple_relations.
	// Updating one relation does not affect another.
	db := setupRelationDB(t)
	ctx := context.Background()

	personMgr := gotype.NewManager[Person](db)
	companyMgr := gotype.NewManager[Company](db)
	empMgr := gotype.NewManager[Employment](db)

	p1 := insertAndGet(t, ctx, personMgr, &Person{Name: "Multi1", Email: "m1@test.com"}, "name", "Multi1")
	p2 := insertAndGet(t, ctx, personMgr, &Person{Name: "Multi2", Email: "m2@test.com"}, "name", "Multi2")
	c := insertAndGet(t, ctx, companyMgr, &Company{Name: "MultiCorp", Industry: "Test"}, "name", "MultiCorp")

	assertInsert(t, ctx, empMgr, &Employment{Employee: p1, Employer: c, StartDate: "2023-01-01"})
	assertInsert(t, ctx, empMgr, &Employment{Employee: p2, Employer: c, StartDate: "2023-06-01"})

	rels := assertCount(t, ctx, empMgr, 2)

	// Update only one relation.
	var target *Employment
	for _, r := range rels {
		if r.StartDate == "2023-01-01" {
			target = r
			break
		}
	}
	if target == nil {
		t.Fatal("could not find relation with start-date 2023-01-01")
	}
	target.StartDate = "2025-01-01"
	assertUpdate(t, ctx, empMgr, target)

	// Verify one changed, one unchanged.
	allRels := assertCount(t, ctx, empMgr, 2)
	dates := map[string]bool{}
	for _, r := range allRels {
		dates[r.StartDate] = true
	}
	if !dates["2025-01-01"] {
		t.Error("expected updated date 2025-01-01")
	}
	if !dates["2023-06-01"] {
		t.Error("expected unchanged date 2023-06-01")
	}
}
