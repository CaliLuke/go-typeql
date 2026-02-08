//go:build integration && cgo && typedb

package gotype_test

import (
	"context"
	"math"
	"testing"

	"github.com/CaliLuke/go-typeql/gotype"
)

func setupAggDB(t *testing.T) *gotype.Manager[Person] {
	db := setupTestDBWith(t, func() {
		_ = gotype.Register[Person]()
		_ = gotype.Register[Company]()
		_ = gotype.Register[Employment]()
	})
	ctx := context.Background()
	mgr := gotype.NewManager[Person](db)
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
