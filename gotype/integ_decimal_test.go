//go:build integration && cgo && typedb

package gotype_test

import (
	"context"
	"math"
	"testing"

	"github.com/CaliLuke/go-typeql/gotype"
)

// DecimalItem exercises TypeDB decimal attributes via the value:decimal tag
// option, with both the lossy-but-idiomatic float64 carrier and the exact
// string carrier.
type DecimalItem struct {
	gotype.BaseEntity
	Code   string  `typedb:"code,key"`
	Price  float64 `typedb:"price,value:decimal"`
	Amount string  `typedb:"exact-amount,value:decimal"`
}

func setupDecimalTestDB(t *testing.T) *gotype.Database {
	return setupTestDBWith(t, func() {
		_ = gotype.Register[DecimalItem]()
	})
}

func TestIntegration_DecimalRoundTrip(t *testing.T) {
	db := setupDecimalTestDB(t)
	ctx := context.Background()
	mgr := gotype.MustNewManager[DecimalItem](db)

	assertInsert(t, ctx, mgr, &DecimalItem{Code: "widget", Price: 99.99, Amount: "0.1"})

	r := assertGetOne(t, ctx, mgr, map[string]any{"code": "widget"})
	if math.Abs(r.Price-99.99) > 1e-9 {
		t.Errorf("Price = %v, want ~99.99", r.Price)
	}
	if r.Amount != "0.1" {
		t.Errorf("Amount = %q, want %q (exact string round trip)", r.Amount, "0.1")
	}
}

func TestIntegration_DecimalIntegralValue(t *testing.T) {
	// Integral values are written as 3.0dec (fraction always present); they
	// must round-trip through the server unchanged.
	db := setupDecimalTestDB(t)
	ctx := context.Background()
	mgr := gotype.MustNewManager[DecimalItem](db)

	assertInsert(t, ctx, mgr, &DecimalItem{Code: "whole", Price: 3.0, Amount: "7"})

	r := assertGetOne(t, ctx, mgr, map[string]any{"code": "whole"})
	if r.Price != 3.0 {
		t.Errorf("Price = %v, want 3", r.Price)
	}
}

func TestIntegration_DecimalUpdate(t *testing.T) {
	db := setupDecimalTestDB(t)
	ctx := context.Background()
	mgr := gotype.MustNewManager[DecimalItem](db)

	item := &DecimalItem{Code: "mutable", Price: 1.25, Amount: "1.25"}
	assertInsert(t, ctx, mgr, item)

	item.Price = 2.5
	item.Amount = "2.5"
	if err := mgr.Update(ctx, item); err != nil {
		t.Fatalf("Update failed: %v", err)
	}

	r := assertGetOne(t, ctx, mgr, map[string]any{"code": "mutable"})
	if math.Abs(r.Price-2.5) > 1e-9 {
		t.Errorf("Price after update = %v, want 2.5", r.Price)
	}
	if r.Amount != "2.5" {
		t.Errorf("Amount after update = %q, want %q", r.Amount, "2.5")
	}
}

func TestIntegration_DecimalFilterEquality(t *testing.T) {
	// A Get filter on a decimal attribute emits a dec literal, so exact
	// equality holds even for fractions a double cannot represent (0.1).
	db := setupDecimalTestDB(t)
	ctx := context.Background()
	mgr := gotype.MustNewManager[DecimalItem](db)

	assertInsert(t, ctx, mgr, &DecimalItem{Code: "tenth", Price: 0.1, Amount: "0.1"})

	results, err := mgr.Get(ctx, map[string]any{"price": 0.1})
	if err != nil {
		t.Fatalf("Get by decimal value failed: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("Get by decimal value returned %d results, want 1", len(results))
	}
	if results[0].Code != "tenth" {
		t.Errorf("Code = %q, want %q", results[0].Code, "tenth")
	}
}
