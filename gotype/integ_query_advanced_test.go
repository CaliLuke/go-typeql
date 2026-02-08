//go:build integration && cgo && typedb

package gotype_test

import (
	"context"
	"math"
	"testing"

	"github.com/CaliLuke/go-typeql/gotype"
)

// ---------------------------------------------------------------------------
// Advanced query tests (ported from Python test_expressions.py,
// test_order_by.py, test_lookup_filters.py, test_pagination.py,
// test_filters.py)
// ---------------------------------------------------------------------------

func TestIntegration_Query_OrderByWithFilter(t *testing.T) {
	// Ported from test_entity_order_by_with_filter.
	// Order by combined with expression filter.
	_, mgr := setupQueryDB(t)
	ctx := context.Background()

	// age >= 28 sorted asc → Diana(28), Alice(30), Charlie(35)
	results, err := mgr.Query().
		Filter(gotype.Gte("age", 28)).
		OrderAsc("age").
		Execute(ctx)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(results) != 3 {
		t.Fatalf("expected 3 results, got %d", len(results))
	}
	if *results[0].Age != 28 {
		t.Errorf("expected first age 28, got %d", *results[0].Age)
	}
	if *results[2].Age != 35 {
		t.Errorf("expected last age 35, got %d", *results[2].Age)
	}
}

func TestIntegration_Query_OrderByWithPagination(t *testing.T) {
	// Ported from test_entity_order_by_with_pagination.
	// Order by with limit and offset.
	_, mgr := setupQueryDB(t)
	ctx := context.Background()

	// All with age, sorted asc, offset 1, limit 2 → Diana(28), Alice(30)
	results, err := mgr.Query().
		Filter(gotype.HasAttr("age")).
		OrderAsc("age").
		Offset(1).
		Limit(2).
		Execute(ctx)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(results) != 2 {
		t.Errorf("expected 2 results (offset 1, limit 2), got %d", len(results))
	}
	// Should be Diana(28) and Alice(30), skipping Bob(25)
	if len(results) == 2 {
		if *results[0].Age != 28 {
			t.Errorf("expected first age 28 (Diana), got %d", *results[0].Age)
		}
		if *results[1].Age != 30 {
			t.Errorf("expected second age 30 (Alice), got %d", *results[1].Age)
		}
	}
}

func TestIntegration_Query_RangeSameField(t *testing.T) {
	// Ported from test_range_query_same_field.
	// Range query on the same field (25 <= age < 35).
	_, mgr := setupQueryDB(t)
	ctx := context.Background()

	results, err := mgr.Query().
		Filter(
			gotype.Gte("age", 25),
			gotype.Lt("age", 35),
		).
		OrderAsc("age").
		Execute(ctx)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	// Bob(25), Diana(28), Alice(30)
	if len(results) != 3 {
		t.Errorf("expected 3 (25 <= age < 35), got %d", len(results))
	}
}

func TestIntegration_Query_EmptyResults(t *testing.T) {
	// Ported from test_lookup_empty_results.
	// Queries matching nothing return empty lists.
	_, mgr := setupQueryDB(t)
	ctx := context.Background()

	results, err := mgr.Query().
		Filter(gotype.Eq("name", "NonExistentPerson")).
		Execute(ctx)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("expected 0 results, got %d", len(results))
	}

	count, err := mgr.Query().
		Filter(gotype.Eq("name", "NonExistentPerson")).
		Count(ctx)
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 0 {
		t.Errorf("expected count 0, got %d", count)
	}
}

func TestIntegration_Query_MultipleFilters(t *testing.T) {
	// Ported from test_query_with_multiple_filters.
	// Multiple filters passed to Filter() are combined with AND.
	_, mgr := setupQueryDB(t)
	ctx := context.Background()

	// Contains "li" in name AND age >= 30 → Alice(30, "Alice") and Charlie(35, "Charlie")
	results, err := mgr.Query().
		Filter(
			gotype.Like("name", ".*li.*"),
			gotype.Gte("age", 30),
		).
		Execute(ctx)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(results) != 2 {
		t.Errorf("expected 2 (name has 'li' AND age >= 30), got %d", len(results))
	}
}

func TestIntegration_Query_CountWithFilter(t *testing.T) {
	// Ported from test_query_count_method (with filter).
	// Count() works with expression filters.
	_, mgr := setupQueryDB(t)
	ctx := context.Background()

	count, err := mgr.Query().
		Filter(gotype.Gt("age", 25)).
		Count(ctx)
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	// Alice(30), Diana(28), Charlie(35) = 3
	if count != 3 {
		t.Errorf("expected count 3 (age > 25), got %d", count)
	}
}

func TestIntegration_Query_LimitAndOffset(t *testing.T) {
	// Ported from test_query_with_limit_and_offset (combined).
	// Limit and offset together paginate correctly.
	_, mgr := setupQueryDB(t)
	ctx := context.Background()

	// Sort by age asc with HasAttr → Bob(25), Diana(28), Alice(30), Charlie(35)
	page, err := mgr.Query().
		Filter(gotype.HasAttr("age")).
		OrderAsc("age").
		Offset(1).
		Limit(2).
		Execute(ctx)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(page) != 2 {
		t.Fatalf("expected 2 results for page, got %d", len(page))
	}
	// Skip Bob(25), get Diana(28) and Alice(30)
	if *page[0].Age != 28 {
		t.Errorf("page[0] age expected 28, got %d", *page[0].Age)
	}
	if *page[1].Age != 30 {
		t.Errorf("page[1] age expected 30, got %d", *page[1].Age)
	}
}

func TestIntegration_Query_ComplexPromotionCandidates(t *testing.T) {
	// Ported from test_complex_query_promotion_candidates.
	// Complex real-world query: find persons with age >= 28 sorted desc,
	// excluding specific names.
	_, mgr := setupQueryDB(t)
	ctx := context.Background()

	results, err := mgr.Query().
		Filter(
			gotype.Gte("age", 28),
			gotype.Not(gotype.Eq("name", "Diana")),
		).
		OrderDesc("age").
		Execute(ctx)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	// Charlie(35), Alice(30) — Diana excluded
	if len(results) != 2 {
		t.Fatalf("expected 2, got %d", len(results))
	}
	if results[0].Name != "Charlie" {
		t.Errorf("expected Charlie first (highest age), got %q", results[0].Name)
	}
	if results[1].Name != "Alice" {
		t.Errorf("expected Alice second, got %q", results[1].Name)
	}
}

func TestIntegration_Aggregate_Multiple(t *testing.T) {
	// Ported from test_multiple_aggregations.
	// Run multiple different aggregations on the same dataset.
	mgr := setupAggDB(t)
	ctx := context.Background()

	sum, err := mgr.Query().Sum("age").Execute(ctx)
	if err != nil {
		t.Fatalf("sum: %v", err)
	}
	avg, err := mgr.Query().Avg("age").Execute(ctx)
	if err != nil {
		t.Fatalf("avg: %v", err)
	}
	min, err := mgr.Query().Min("age").Execute(ctx)
	if err != nil {
		t.Fatalf("min: %v", err)
	}
	max, err := mgr.Query().Max("age").Execute(ctx)
	if err != nil {
		t.Fatalf("max: %v", err)
	}

	// Verify all aggregations are consistent.
	// sum=118, avg=29.5, min=25, max=35
	if sum != 118 {
		t.Errorf("sum: expected 118, got %f", sum)
	}
	if math.Abs(avg-29.5) > 0.1 {
		t.Errorf("avg: expected ~29.5, got %f", avg)
	}
	if min != 25 {
		t.Errorf("min: expected 25, got %f", min)
	}
	if max != 35 {
		t.Errorf("max: expected 35, got %f", max)
	}
}

func TestIntegration_Query_BackwardCompatDictFilters(t *testing.T) {
	// Ported from test_backward_compatibility_dict_filters.
	// Manager.Get with map[string]any filters (dict-style) still works.
	_, mgr := setupQueryDB(t)
	ctx := context.Background()

	results, err := mgr.Get(ctx, map[string]any{"name": "Alice"})
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if len(results) != 1 || results[0].Name != "Alice" {
		t.Errorf("expected Alice via dict filter, got %v", results)
	}
}

func TestIntegration_Query_FilterContainsEmail(t *testing.T) {
	// Ported from test_contains_filter.
	// Contains works on string attributes.
	_, mgr := setupQueryDB(t)
	ctx := context.Background()

	results, err := mgr.Query().
		Filter(gotype.Contains("email", "bob")).
		Execute(ctx)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(results) != 1 || results[0].Name != "Bob" {
		t.Errorf("expected Bob via email contains, got %v", results)
	}
}

func TestIntegration_Query_OrLogic(t *testing.T) {
	// Ported from test_or_logic.
	// OR filter combines two conditions.
	_, mgr := setupQueryDB(t)
	ctx := context.Background()

	results, err := mgr.Query().
		Filter(gotype.Or(
			gotype.Eq("name", "Alice"),
			gotype.Eq("name", "Charlie"),
		)).
		Execute(ctx)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(results) != 2 {
		t.Errorf("expected 2 (Alice or Charlie), got %d", len(results))
	}
}

func TestIntegration_Query_AndLogicExplicit(t *testing.T) {
	// Ported from test_and_logic_explicit.
	// Explicit AND filter.
	_, mgr := setupQueryDB(t)
	ctx := context.Background()

	results, err := mgr.Query().
		Filter(gotype.And(
			gotype.Gte("age", 25),
			gotype.Lte("age", 30),
		)).
		Execute(ctx)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	// Bob(25), Diana(28), Alice(30)
	if len(results) != 3 {
		t.Errorf("expected 3 (25 <= age <= 30), got %d", len(results))
	}
}

func TestIntegration_Query_AndLogicImplicit(t *testing.T) {
	// Ported from test_and_logic_implicit.
	// Multiple filters passed to Filter() are implicitly ANDed.
	_, mgr := setupQueryDB(t)
	ctx := context.Background()

	results, err := mgr.Query().
		Filter(gotype.Gte("age", 25)).
		Filter(gotype.Lte("age", 30)).
		Execute(ctx)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	// Same result as explicit AND.
	if len(results) != 3 {
		t.Errorf("expected 3 (implicit AND 25 <= age <= 30), got %d", len(results))
	}
}

func TestIntegration_Query_ComplexBoolean(t *testing.T) {
	// Ported from test_complex_boolean.
	// Complex nested boolean: (age > 30 OR name == "Bob") AND NOT name == "Eve"
	_, mgr := setupQueryDB(t)
	ctx := context.Background()

	results, err := mgr.Query().
		Filter(
			gotype.Or(
				gotype.Gt("age", 30),
				gotype.Eq("name", "Bob"),
			),
			gotype.Not(gotype.Eq("name", "Eve")),
		).
		Execute(ctx)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	// age > 30: Charlie(35)
	// name == "Bob": Bob(25)
	// NOT Eve → doesn't affect since Eve isn't in OR set
	// Result: Charlie, Bob
	if len(results) != 2 {
		t.Errorf("expected 2 (complex boolean), got %d", len(results))
	}
	names := map[string]bool{}
	for _, r := range results {
		names[r.Name] = true
	}
	if !names["Charlie"] || !names["Bob"] {
		t.Errorf("expected Charlie and Bob, got %v", names)
	}
}
