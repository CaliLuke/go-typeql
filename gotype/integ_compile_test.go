//go:build integration && cgo && typedb

package gotype_test

// Live checks of the filter compiler (issue #138).

import (
	"context"
	"fmt"
	"slices"
	"strings"
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
	for name, tc := range map[string]struct {
		f    gotype.Filter
		want string
	}{
		"abs":    {gotype.Computed(gotype.Abs(gotype.Attr("ic-rate")), ">", 2), "a"},
		"ceil":   {gotype.Computed(gotype.Ceil(gotype.Attr("ic-rate")), "==", -2), "a"},
		"floor":  {gotype.Computed(gotype.Floor(gotype.Attr("ic-rate")), "==", -3), "a"},
		"round":  {gotype.Computed(gotype.Round(gotype.Attr("ic-rate")), "<", 0), "a"},
		"length": {gotype.Computed(gotype.Length(gotype.Attr("ic-tag")), "==", 3), "a"},
		"max":    {gotype.Computed(gotype.Max(gotype.Attr("ic-score"), gotype.Literal(4)), "==", 5), "a"},
		"min":    {gotype.Computed(gotype.Min(gotype.Attr("ic-score"), gotype.Literal(4)), "==", 3), "a,b"},
		"log10":  {gotype.Computed(gotype.Log10(gotype.Attr("ic-score")), ">", 1), "c"},
	} {
		rows, err := mgr.Query().Filter(tc.f).All(ctx)
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		var got []string
		for n := range names(rows) {
			got = append(got, n)
		}
		slices.Sort(got)
		if strings.Join(got, ",") != tc.want {
			t.Errorf("%s: got %v, want %s", name, got, tc.want)
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

// FunctionQuery calls a schema function and a fully qualified built-in
// function (TypeQL 3.13.4).
func TestIntegration_FunctionQuery(t *testing.T) {
	db := setupTestDBWith(t, func() { _ = gotype.Register[IcItem]() })
	ctx := context.Background()
	if err := db.ExecuteSchema(ctx, `define
fun double_it($x: integer) -> integer: match let $y = $x * 2; return first $y;`); err != nil {
		t.Fatalf("schema: %v", err)
	}
	for name, tc := range map[string]struct {
		fq   *gotype.FunctionQuery
		want float64
	}{
		"schema function":     {gotype.NewFunctionQuery(db, "double_it").Arg(21), 42},
		"namespaced built-in": {gotype.NewFunctionQuery(db, "std::math::log10").Arg(100), 2},
	} {
		rows, err := tc.fq.Execute(ctx)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if len(rows) != 1 {
			t.Fatalf("%s: got %d rows", name, len(rows))
		}
		if got := fmt.Sprint(rows[0]["result"]); got != fmt.Sprint(tc.want) {
			t.Fatalf("%s: result = %v, want %v", name, rows[0]["result"], tc.want)
		}
	}
}
