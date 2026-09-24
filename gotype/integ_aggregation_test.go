//go:build integration && cgo && typedb

package gotype_test

import (
	"context"
	"math"
	"testing"

	"github.com/CaliLuke/go-typeql/v3/gotype"
)

func setupAggDB(t *testing.T) *gotype.Manager[Person] {
	db := setupTestDBWith(t, func() {
		_ = gotype.Register[Person]()
		_ = gotype.Register[Company]()
		_ = gotype.Register[Employment]()
	})
	ctx := context.Background()
	mgr := gotype.MustNewManager[Person](db)
	seedPersons(t, ctx, mgr)
	return mgr
}

func TestIntegration_Aggregate_Sum(t *testing.T) {
	mgr := setupAggDB(t)
	ctx := context.Background()

	sum, err := mgr.Query().Sum("age").Execute(ctx)
	if err != nil {
		t.Fatalf("sum: %v", err)
	}
	// 30 + 25 + 35 + 28 = 118 (Eve has no age, excluded from reduce)
	if sum != 118 {
		t.Errorf("expected sum 118, got %f", sum)
	}
}

func TestIntegration_Aggregate_Avg(t *testing.T) {
	mgr := setupAggDB(t)
	ctx := context.Background()

	avg, err := mgr.Query().Avg("age").Execute(ctx)
	if err != nil {
		t.Fatalf("avg: %v", err)
	}
	// mean of 30,25,35,28 = 29.5
	if math.Abs(avg-29.5) > 0.1 {
		t.Errorf("expected avg ~29.5, got %f", avg)
	}
}

func TestIntegration_Aggregate_Min(t *testing.T) {
	mgr := setupAggDB(t)
	ctx := context.Background()

	min, err := mgr.Query().Min("age").Execute(ctx)
	if err != nil {
		t.Fatalf("min: %v", err)
	}
	if min != 25 {
		t.Errorf("expected min 25, got %f", min)
	}
}

func TestIntegration_Aggregate_Max(t *testing.T) {
	mgr := setupAggDB(t)
	ctx := context.Background()

	max, err := mgr.Query().Max("age").Execute(ctx)
	if err != nil {
		t.Fatalf("max: %v", err)
	}
	if max != 35 {
		t.Errorf("expected max 35, got %f", max)
	}
}

func TestIntegration_Aggregate_SumWithFilter(t *testing.T) {
	mgr := setupAggDB(t)
	ctx := context.Background()

	// Sum where age > 25: 30 + 35 + 28 = 93
	sum, err := mgr.Query().Filter(gotype.Gt("age", 25)).Sum("age").Execute(ctx)
	if err != nil {
		t.Fatalf("sum: %v", err)
	}
	if sum != 93 {
		t.Errorf("expected sum 93 (age>25), got %f", sum)
	}
}

func TestIntegration_Aggregate_EmptySet(t *testing.T) {
	mgr := setupAggDB(t)
	ctx := context.Background()

	// Sum where age > 1000: nobody matches → 0
	sum, err := mgr.Query().Filter(gotype.Gt("age", 1000)).Sum("age").Execute(ctx)
	if err != nil {
		t.Fatalf("sum: %v", err)
	}
	if sum != 0 {
		t.Errorf("expected 0 for empty set, got %f", sum)
	}
}

func TestIntegration_Aggregate_StdAndVariance(t *testing.T) {
	mgr := setupAggDB(t)
	ctx := context.Background()

	// Ages 30, 25, 35, 28: squared deviations from 29.5 sum to 53, so the
	// sample variance is 53/3 (TypeDB's std is the sample deviation).
	std, err := mgr.Query().Std("age").Execute(ctx)
	if err != nil {
		t.Fatalf("std: %v", err)
	}
	if want := math.Sqrt(53.0 / 3); math.Abs(std-want) > 1e-9 {
		t.Errorf("std = %f, want %f", std, want)
	}
	variance, err := mgr.Query().Variance("age").Execute(ctx)
	if err != nil {
		t.Fatalf("variance: %v", err)
	}
	if want := 53.0 / 3; math.Abs(variance-want) > 1e-9 {
		t.Errorf("variance = %f, want %f", variance, want)
	}
}

func TestIntegration_GroupBy_Aggregate(t *testing.T) {
	mgr := setupAggDB(t)
	ctx := context.Background()

	groups, err := mgr.Query().GroupBy("name").Aggregate(ctx,
		gotype.AggregateSpec{Attr: "age", Fn: "sum"},
		gotype.AggregateSpec{Attr: "age", Fn: "avg"},
	)
	if err != nil {
		t.Fatalf("groupby: %v", err)
	}
	// Eve has no age, so she forms no group.
	want := map[string]float64{"Alice": 30, "Bob": 25, "Charlie": 35, "Diana": 28}
	if len(groups) != len(want) {
		t.Fatalf("got %d groups %v, want %d", len(groups), groups, len(want))
	}
	for name, age := range want {
		if got := groups[name]; got["sum_age"] != age || got["avg_age"] != age {
			t.Errorf("group %s = %v, want sum_age and avg_age %v", name, got, age)
		}
	}
}
