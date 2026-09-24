//go:build integration && cgo && typedb

package gotype_test

import (
	"context"
	"testing"

	"github.com/CaliLuke/go-typeql/v3/gotype"
)

// RwItem uses names that are TypeQL words but not reserved keywords. TypeDB
// accepts them, so Register must accept them too.
type RwItem struct {
	gotype.BaseEntity
	Label string  `typedb:"label,key"`
	Count *int    `typedb:"count"`
	Value *string `typedb:"Match"`
}

func TestIntegration_NonReservedTypeQLWords(t *testing.T) {
	db := setupTestDBWith(t, func() {
		if err := gotype.Register[RwItem](); err != nil {
			t.Fatalf("Register: %v", err)
		}
	})
	ctx := context.Background()
	mgr := gotype.MustNewManager[RwItem](db)
	one, two, v := 1, 2, "x"
	assertInsert(t, ctx, mgr, &RwItem{Label: "a", Count: &one, Value: &v})
	assertInsert(t, ctx, mgr, &RwItem{Label: "b", Count: &two})

	rows, err := mgr.Query().Filter(gotype.Gt("count", 1)).OrderAsc("label").All(ctx)
	if err != nil || len(rows) != 1 || rows[0].Label != "b" {
		t.Fatalf("query = %v, %v; want b", rows, err)
	}
	a, err := mgr.GetOne(ctx, map[string]any{"label": "a"})
	if err != nil || a.Value == nil || *a.Value != "x" {
		t.Fatalf("GetOne = %+v, %v", a, err)
	}
	three := 3
	a.Count = &three
	if err := mgr.Update(ctx, a); err != nil {
		t.Fatalf("Update: %v", err)
	}
	if err := mgr.Delete(ctx, rows[0]); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	n, err := mgr.Query().Filter(gotype.Eq("count", 3)).Count(ctx)
	if err != nil || n != 1 {
		t.Fatalf("count = %d, %v; want 1", n, err)
	}
}
