//go:build integration && cgo && typedb

package gotype_test

// Live checks of the filter compiler (issue #138).

import (
	"context"
	"testing"

	"github.com/CaliLuke/go-typeql/v3/gotype"
)

type IcItem struct {
	gotype.BaseEntity
	IcName  string   `typedb:"ic-name,key"`
	IcScore []int    `typedb:"ic-score,card=0.."`
	IcTag   *string  `typedb:"ic-tag"`
	IcRate  *float64 `typedb:"ic-rate"`
}

func setupCompileDB(t *testing.T) (context.Context, *gotype.Manager[IcItem]) {
	t.Helper()
	db := setupTestDBWith(t, func() { _ = gotype.Register[IcItem]() })
	ctx := context.Background()
	mgr := gotype.MustNewManager[IcItem](db)
	tag := "abc"
	rate := -2.5
	for _, s := range []*IcItem{
		{IcName: "a", IcScore: []int{3, 5}, IcTag: &tag, IcRate: &rate},
		{IcName: "b", IcScore: []int{3, 8}},
		{IcName: "c", IcScore: []int{12}},
	} {
		assertInsert(t, ctx, mgr, s)
	}
	return ctx, mgr
}

func names(rows []*IcItem) map[string]bool {
	out := map[string]bool{}
	for _, r := range rows {
		out[r.IcName] = true
	}
	return out
}

// Fault 5: a Computed filter binds its attribute, with no companion filter.
func TestIntegration_Compile_ComputedAlone(t *testing.T) {
	ctx, mgr := setupCompileDB(t)
	rows, err := mgr.Query().Filter(gotype.Computed(gotype.Mul(gotype.Attr("ic-score"), gotype.Literal(2)), ">", 20)).All(ctx)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if got := names(rows); len(got) != 1 || !got["c"] {
		t.Fatalf("got %v, want only c (12 * 2 > 20)", got)
	}
}

// R2 on a multi-valued attribute: the not body binds its own variable, so it
// means "no score value is 5", not "the score that is more than 1 is not 5".
func TestIntegration_Compile_NotBodyOwnVariable(t *testing.T) {
	ctx, mgr := setupCompileDB(t)
	rows, err := mgr.Query().Filter(gotype.Gt("ic-score", 1), gotype.Not(gotype.Eq("ic-score", 5))).All(ctx)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if got := names(rows); len(got) != 2 || got["a"] {
		t.Fatalf("got %v, want b and c (a has the score 5)", got)
	}
	// NotIn compiles as a not scope in the same way.
	rows, err = mgr.Query().Filter(gotype.Gt("ic-score", 1), gotype.NotIn("ic-score", []any{5, 12})).All(ctx)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if got := names(rows); len(got) != 1 || !got["b"] {
		t.Fatalf("NotIn: got %v, want only b", got)
	}
}

// Each built-in function of the expression API against the server: the
// parser accepts any function name, so only the server can check them.
func TestIntegration_Compile_BuiltinFunctions(t *testing.T) {
	ctx, mgr := setupCompileDB(t)
	for name, f := range map[string]gotype.Filter{
		"abs":    gotype.Computed(gotype.Abs(gotype.Attr("ic-rate")), ">", 2),
		"ceil":   gotype.Computed(gotype.Ceil(gotype.Attr("ic-rate")), "==", -2),
		"floor":  gotype.Computed(gotype.Floor(gotype.Attr("ic-rate")), "==", -3),
		"round":  gotype.Computed(gotype.Round(gotype.Attr("ic-rate")), "<", 0),
		"length": gotype.Computed(gotype.Length(gotype.Attr("ic-tag")), "==", 3),
		"max":    gotype.Computed(gotype.Max(gotype.Attr("ic-score"), gotype.Literal(4)), "==", 5),
		"min":    gotype.Computed(gotype.Min(gotype.Attr("ic-score"), gotype.Literal(4)), "==", 3),
	} {
		rows, err := mgr.Query().Filter(f).All(ctx)
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if got := names(rows); !got["a"] {
			t.Errorf("%s: got %v, want a", name, got)
		}
	}
}

// Count, aggregates, and group-by read their outputs by allocated name.
func TestIntegration_Compile_OutputsByAllocatedName(t *testing.T) {
	ctx, mgr := setupCompileDB(t)
	n, err := mgr.Query().Filter(gotype.Computed(gotype.Attr("ic-score"), ">", 4)).Count(ctx)
	if err != nil || n != 3 {
		t.Fatalf("count = %d, %v; want 3", n, err)
	}
	agg, err := mgr.Query().Filter(gotype.Computed(gotype.Add(gotype.Attr("ic-score"), gotype.Literal(1)), ">", 0)).
		Aggregate(ctx, gotype.AggregateSpec{Attr: "ic-score", Fn: "sum"}, gotype.AggregateSpec{Attr: "ic-score", Fn: "max"})
	if err != nil {
		t.Fatalf("aggregate: %v", err)
	}
	if agg["sum_ic-score"] != 31 || agg["max_ic-score"] != 12 {
		t.Fatalf("aggregate = %v, want sum 31 and max 12", agg)
	}
}
