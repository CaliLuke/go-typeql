//go:build integration && cgo && typedb

package gotype_test

import (
	"context"
	"testing"

	"github.com/CaliLuke/go-typeql/gotype"
)

// setupQueryDB creates a DB with Person model and seeds 5 persons.
func setupQueryDB(t *testing.T) (*gotype.Database, *gotype.Manager[Person]) {
	db := setupTestDBWith(t, func() {
		_ = gotype.Register[Person]()
		_ = gotype.Register[Company]()
		_ = gotype.Register[Employment]()
	})
	ctx := context.Background()
	mgr := gotype.NewManager[Person](db)
	seedPersons(t, ctx, mgr)
	return db, mgr
}

func TestIntegration_Filter_Eq(t *testing.T) {
	_, mgr := setupQueryDB(t)
	ctx := context.Background()

	results, err := mgr.Query().Filter(gotype.Eq("name", "Alice")).Execute(ctx)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(results) != 1 || results[0].Name != "Alice" {
		t.Errorf("expected Alice, got %v", results)
	}
}

func TestIntegration_Filter_Neq(t *testing.T) {
	_, mgr := setupQueryDB(t)
	ctx := context.Background()

	results, err := mgr.Query().Filter(gotype.Neq("name", "Alice")).Execute(ctx)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	for _, r := range results {
		if r.Name == "Alice" {
			t.Error("Neq filter should exclude Alice")
		}
	}
	// We have 5 persons; Alice excluded but Eve has name so she's included.
	if len(results) < 3 {
		t.Errorf("expected at least 3 results, got %d", len(results))
	}
}

func TestIntegration_Filter_Gt(t *testing.T) {
	_, mgr := setupQueryDB(t)
	ctx := context.Background()

	// age > 28 should include Alice(30), Charlie(35)
	results, err := mgr.Query().Filter(gotype.Gt("age", 28)).Execute(ctx)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(results) != 2 {
		t.Errorf("expected 2 (age > 28), got %d", len(results))
	}
}

func TestIntegration_Filter_Gte(t *testing.T) {
	_, mgr := setupQueryDB(t)
	ctx := context.Background()

	// age >= 30 should include Alice(30), Charlie(35)
	results, err := mgr.Query().Filter(gotype.Gte("age", 30)).Execute(ctx)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(results) != 2 {
		t.Errorf("expected 2 (age >= 30), got %d", len(results))
	}
}

func TestIntegration_Filter_Lt(t *testing.T) {
	_, mgr := setupQueryDB(t)
	ctx := context.Background()

	// age < 28 should include Bob(25)
	results, err := mgr.Query().Filter(gotype.Lt("age", 28)).Execute(ctx)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(results) != 1 {
		t.Errorf("expected 1 (age < 28), got %d", len(results))
	}
}

func TestIntegration_Filter_Lte(t *testing.T) {
	_, mgr := setupQueryDB(t)
	ctx := context.Background()

	// age <= 28 should include Bob(25), Diana(28)
	results, err := mgr.Query().Filter(gotype.Lte("age", 28)).Execute(ctx)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(results) != 2 {
		t.Errorf("expected 2 (age <= 28), got %d", len(results))
	}
}

func TestIntegration_Filter_Contains(t *testing.T) {
	_, mgr := setupQueryDB(t)
	ctx := context.Background()

	results, err := mgr.Query().Filter(gotype.Contains("email", "alice")).Execute(ctx)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(results) != 1 || results[0].Name != "Alice" {
		t.Errorf("expected Alice via contains, got %v", results)
	}
}

func TestIntegration_Filter_Like(t *testing.T) {
	_, mgr := setupQueryDB(t)
	ctx := context.Background()

	// Like uses TypeQL regex-style patterns.
	results, err := mgr.Query().Filter(gotype.Like("name", ".*li.*")).Execute(ctx)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	// Alice and Charlie both contain "li"
	if len(results) != 2 {
		t.Errorf("expected 2 (like .*li.*), got %d", len(results))
	}
}

func TestIntegration_Filter_HasAttr(t *testing.T) {
	_, mgr := setupQueryDB(t)
	ctx := context.Background()

	// Eve has no age; 4 persons have age.
	results, err := mgr.Query().Filter(gotype.HasAttr("age")).Execute(ctx)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(results) != 4 {
		t.Errorf("expected 4 with age, got %d", len(results))
	}
}

func TestIntegration_Filter_NotHasAttr(t *testing.T) {
	_, mgr := setupQueryDB(t)
	ctx := context.Background()

	results, err := mgr.Query().Filter(gotype.NotHasAttr("age")).Execute(ctx)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(results) != 1 {
		t.Errorf("expected 1 without age, got %d", len(results))
	}
	if len(results) == 1 && results[0].Name != "Eve" {
		t.Errorf("expected Eve, got %q", results[0].Name)
	}
}

func TestIntegration_Filter_ByIID(t *testing.T) {
	_, mgr := setupQueryDB(t)
	ctx := context.Background()

	// Get Alice's IID first.
	alices, err := mgr.Get(ctx, map[string]any{"name": "Alice"})
	if err != nil || len(alices) != 1 {
		t.Fatalf("get Alice: %v", err)
	}
	iid := alices[0].GetIID()
	if iid == "" {
		t.Skip("IID not available")
	}

	results, err := mgr.Query().Filter(gotype.ByIID(iid)).Execute(ctx)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(results) != 1 || results[0].Name != "Alice" {
		t.Errorf("expected Alice by IID, got %v", results)
	}
}

func TestIntegration_Filter_And(t *testing.T) {
	_, mgr := setupQueryDB(t)
	ctx := context.Background()

	// age >= 25 AND age <= 30 → Bob(25), Diana(28), Alice(30)
	f := gotype.And(
		gotype.Gte("age", 25),
		gotype.Lte("age", 30),
	)
	results, err := mgr.Query().Filter(f).Execute(ctx)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(results) != 3 {
		t.Errorf("expected 3 (25<=age<=30), got %d", len(results))
	}
}

func TestIntegration_Filter_Or(t *testing.T) {
	_, mgr := setupQueryDB(t)
	ctx := context.Background()

	// name == "Alice" OR name == "Bob"
	f := gotype.Or(
		gotype.Eq("name", "Alice"),
		gotype.Eq("name", "Bob"),
	)
	results, err := mgr.Query().Filter(f).Execute(ctx)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(results) != 2 {
		t.Errorf("expected 2 (Alice or Bob), got %d", len(results))
	}
}

func TestIntegration_Filter_Not(t *testing.T) {
	_, mgr := setupQueryDB(t)
	ctx := context.Background()

	// NOT name == "Alice" → everyone except Alice (who has name attr matched)
	f := gotype.Not(gotype.Eq("name", "Alice"))
	results, err := mgr.Query().Filter(f).Execute(ctx)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	for _, r := range results {
		if r.Name == "Alice" {
			t.Error("Not filter should exclude Alice")
		}
	}
}

func TestIntegration_Query_Execute(t *testing.T) {
	_, mgr := setupQueryDB(t)
	ctx := context.Background()

	results, err := mgr.Query().Filter(gotype.Gt("age", 26)).Execute(ctx)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	// Alice(30), Charlie(35), Diana(28)
	if len(results) != 3 {
		t.Errorf("expected 3, got %d", len(results))
	}
}

func TestIntegration_Query_First(t *testing.T) {
	_, mgr := setupQueryDB(t)
	ctx := context.Background()

	result, err := mgr.Query().Filter(gotype.Eq("name", "Charlie")).First(ctx)
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	if result == nil || result.Name != "Charlie" {
		t.Errorf("expected Charlie, got %v", result)
	}
}

func TestIntegration_Query_FirstEmpty(t *testing.T) {
	_, mgr := setupQueryDB(t)
	ctx := context.Background()

	result, err := mgr.Query().Filter(gotype.Eq("name", "Nobody")).First(ctx)
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	if result != nil {
		t.Errorf("expected nil for no match, got %v", result)
	}
}

func TestIntegration_Query_Count(t *testing.T) {
	_, mgr := setupQueryDB(t)
	ctx := context.Background()

	count, err := mgr.Query().Count(ctx)
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 5 {
		t.Errorf("expected 5, got %d", count)
	}
}

func TestIntegration_Query_OrderAsc(t *testing.T) {
	_, mgr := setupQueryDB(t)
	ctx := context.Background()

	results, err := mgr.Query().Filter(gotype.HasAttr("age")).OrderAsc("age").Execute(ctx)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(results) < 2 {
		t.Fatalf("need at least 2 results for sort check, got %d", len(results))
	}
	for i := 1; i < len(results); i++ {
		if results[i].Age == nil || results[i-1].Age == nil {
			continue
		}
		if *results[i].Age < *results[i-1].Age {
			t.Errorf("not sorted asc: %d < %d at index %d", *results[i].Age, *results[i-1].Age, i)
		}
	}
}

func TestIntegration_Query_OrderDesc(t *testing.T) {
	_, mgr := setupQueryDB(t)
	ctx := context.Background()

	results, err := mgr.Query().Filter(gotype.HasAttr("age")).OrderDesc("age").Execute(ctx)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(results) < 2 {
		t.Fatalf("need at least 2 results, got %d", len(results))
	}
	for i := 1; i < len(results); i++ {
		if results[i].Age == nil || results[i-1].Age == nil {
			continue
		}
		if *results[i].Age > *results[i-1].Age {
			t.Errorf("not sorted desc: %d > %d at index %d", *results[i].Age, *results[i-1].Age, i)
		}
	}
}

func TestIntegration_Query_Limit(t *testing.T) {
	_, mgr := setupQueryDB(t)
	ctx := context.Background()

	results, err := mgr.Query().Limit(2).Execute(ctx)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(results) != 2 {
		t.Errorf("expected 2 (limit), got %d", len(results))
	}
}

func TestIntegration_Query_Offset(t *testing.T) {
	_, mgr := setupQueryDB(t)
	ctx := context.Background()

	// Get all sorted by age asc, then offset 2 should skip the first 2.
	all, err := mgr.Query().Filter(gotype.HasAttr("age")).OrderAsc("age").Execute(ctx)
	if err != nil {
		t.Fatalf("query all: %v", err)
	}
	offsetResults, err := mgr.Query().Filter(gotype.HasAttr("age")).OrderAsc("age").Offset(2).Execute(ctx)
	if err != nil {
		t.Fatalf("query offset: %v", err)
	}
	if len(offsetResults) != len(all)-2 {
		t.Errorf("expected %d (offset 2 from %d), got %d", len(all)-2, len(all), len(offsetResults))
	}
}

func TestIntegration_Query_Delete(t *testing.T) {
	_, mgr := setupQueryDB(t)
	ctx := context.Background()

	// Delete persons with age < 28 → only Bob(25).
	_, err := mgr.Query().Filter(gotype.Lt("age", 28)).Delete(ctx)
	if err != nil {
		t.Fatalf("delete: %v", err)
	}

	remaining, err := mgr.All(ctx)
	if err != nil {
		t.Fatalf("All: %v", err)
	}
	for _, r := range remaining {
		if r.Name == "Bob" {
			t.Error("Bob should have been deleted (age 25 < 28)")
		}
	}
}

func TestIntegration_Filter_Complex(t *testing.T) {
	_, mgr := setupQueryDB(t)
	ctx := context.Background()

	// (age >= 28 AND age <= 35) AND NOT name == "Diana"
	f := gotype.And(
		gotype.Gte("age", 28),
		gotype.Lte("age", 35),
		gotype.Not(gotype.Eq("name", "Diana")),
	)
	results, err := mgr.Query().Filter(f).Execute(ctx)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	// 28<=age<=35: Diana(28), Alice(30), Charlie(35) minus Diana → Alice, Charlie
	if len(results) != 2 {
		t.Errorf("expected 2 (complex filter), got %d", len(results))
	}
	names := make(map[string]bool)
	for _, r := range results {
		names[r.Name] = true
	}
	if !names["Alice"] || !names["Charlie"] {
		t.Errorf("expected Alice and Charlie, got %v", names)
	}
}
