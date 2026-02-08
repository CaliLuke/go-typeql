//go:build integration && cgo && typedb

package gotype_test

import (
	"context"
	"testing"

	"github.com/CaliLuke/go-typeql/gotype"
)

// ---------------------------------------------------------------------------
// Chainable delete tests (ported from Python test_chainable.py)
// ---------------------------------------------------------------------------

func TestIntegration_ChainableDelete_ExpressionFilter(t *testing.T) {
	// Ported from test_chainable_delete_with_expression_filter.
	// Delete using expression-based filter via Query().Filter().Delete().
	db := setupTestDBDefault(t)
	ctx := context.Background()

	mgr := gotype.NewManager[Person](db)
	seedPersons(t, ctx, mgr)

	// Delete persons with age > 30 → Charlie(35)
	_, err := mgr.Query().Filter(gotype.Gt("age", 30)).Delete(ctx)
	if err != nil {
		t.Fatalf("delete: %v", err)
	}

	remaining, err := mgr.All(ctx)
	if err != nil {
		t.Fatalf("All: %v", err)
	}
	for _, r := range remaining {
		if r.Name == "Charlie" {
			t.Error("Charlie should have been deleted (age 35 > 30)")
		}
	}
	// Should have Alice(30), Bob(25), Diana(28), Eve(nil) remaining = 4
	if len(remaining) != 4 {
		t.Errorf("expected 4 remaining, got %d", len(remaining))
	}
}

func TestIntegration_ChainableDelete_MultipleFilters(t *testing.T) {
	// Ported from test_chainable_delete_with_multiple_filters.
	// Delete using multiple filter expressions (AND).
	db := setupTestDBDefault(t)
	ctx := context.Background()

	mgr := gotype.NewManager[Person](db)
	seedPersons(t, ctx, mgr)

	// Delete persons with age >= 28 AND age <= 30 → Diana(28), Alice(30)
	_, err := mgr.Query().
		Filter(gotype.Gte("age", 28), gotype.Lte("age", 30)).
		Delete(ctx)
	if err != nil {
		t.Fatalf("delete: %v", err)
	}

	remaining, err := mgr.All(ctx)
	if err != nil {
		t.Fatalf("All: %v", err)
	}
	for _, r := range remaining {
		if r.Name == "Alice" || r.Name == "Diana" {
			t.Errorf("%s should have been deleted", r.Name)
		}
	}
	// Bob(25), Charlie(35), Eve(nil) = 3
	if len(remaining) != 3 {
		t.Errorf("expected 3 remaining, got %d", len(remaining))
	}
}

func TestIntegration_ChainableDelete_NoMatches(t *testing.T) {
	// Ported from test_chainable_delete_returns_zero_for_no_matches.
	// Deleting with a filter that matches nothing should not error.
	db := setupTestDBDefault(t)
	ctx := context.Background()

	mgr := gotype.NewManager[Person](db)
	seedPersons(t, ctx, mgr)

	_, err := mgr.Query().Filter(gotype.Eq("name", "NonExistent")).Delete(ctx)
	if err != nil {
		t.Fatalf("delete non-matching: %v", err)
	}

	// All 5 still present.
	assertCount(t, ctx, mgr, 5)
}

func TestIntegration_ChainableDelete_RangeFilter(t *testing.T) {
	// Ported from test_chainable_delete_with_range_filter.
	// Delete using range filters (gte + lt).
	db := setupTestDBDefault(t)
	ctx := context.Background()

	mgr := gotype.NewManager[Person](db)
	seedPersons(t, ctx, mgr)

	// Delete persons with 25 <= age < 30 → Bob(25), Diana(28)
	_, err := mgr.Query().
		Filter(gotype.Gte("age", 25), gotype.Lt("age", 30)).
		Delete(ctx)
	if err != nil {
		t.Fatalf("delete: %v", err)
	}

	remaining, err := mgr.All(ctx)
	if err != nil {
		t.Fatalf("All: %v", err)
	}
	// Alice(30), Charlie(35), Eve(nil) = 3
	if len(remaining) != 3 {
		t.Errorf("expected 3 remaining, got %d", len(remaining))
	}
	for _, r := range remaining {
		if r.Name == "Bob" || r.Name == "Diana" {
			t.Errorf("%s should have been deleted (age in [25,30))", r.Name)
		}
	}
}
