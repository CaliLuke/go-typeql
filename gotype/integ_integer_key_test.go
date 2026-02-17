//go:build integration && cgo && typedb

package gotype_test

import (
	"context"
	"testing"

	"github.com/CaliLuke/go-typeql/gotype"
)

// ---------------------------------------------------------------------------
// Integer key regression tests (ported from Python test_integer_key_bug.py)
// ---------------------------------------------------------------------------

// Sensor is an entity with an integer key.
type Sensor struct {
	gotype.BaseEntity
	SensorID int     `typedb:"sensor-id,key"`
	Location string  `typedb:"location"`
	Reading  *float64 `typedb:"reading,card=0..1"`
}

func setupIntKeyDB(t *testing.T) *gotype.Database {
	return setupTestDBWith(t, func() {
		_ = gotype.Register[Sensor]()
		_ = gotype.Register[Person]()
	})
}

func TestIntegration_IntegerKey_InsertAndQuery(t *testing.T) {
	// Ported from test_integer_key_insert_and_query.
	// Insert and query entities with integer keys.
	db := setupIntKeyDB(t)
	ctx := context.Background()

	mgr := gotype.NewManager[Sensor](db)

	assertInsert(t, ctx, mgr, &Sensor{SensorID: 100, Location: "Lab A"})
	assertInsert(t, ctx, mgr, &Sensor{SensorID: 200, Location: "Lab B"})

	result := assertGetOne(t, ctx, mgr, map[string]any{"sensor-id": 100})
	if result.SensorID != 100 {
		t.Errorf("expected sensor-id 100, got %d", result.SensorID)
	}
	if result.Location != "Lab A" {
		t.Errorf("expected location Lab A, got %q", result.Location)
	}
}

func TestIntegration_IntegerKey_VsStringKey(t *testing.T) {
	// Ported from test_integer_key_vs_string_key_comparison.
	// Integer key entities and string key entities should coexist.
	db := setupIntKeyDB(t)
	ctx := context.Background()

	sensorMgr := gotype.NewManager[Sensor](db)
	personMgr := gotype.NewManager[Person](db)

	assertInsert(t, ctx, sensorMgr, &Sensor{SensorID: 1, Location: "Office"})
	assertInsert(t, ctx, personMgr, &Person{Name: "Alice", Email: "alice@intkey.com"})

	assertCount(t, ctx, sensorMgr, 1)
	assertCount(t, ctx, personMgr, 1)
}

func TestIntegration_IntegerKey_DifferentValues(t *testing.T) {
	// Ported from test_integer_key_with_different_values.
	// Query with different integer key values.
	db := setupIntKeyDB(t)
	ctx := context.Background()

	mgr := gotype.NewManager[Sensor](db)

	for _, id := range []int{10, 20, 30, 40, 50} {
		assertInsert(t, ctx, mgr, &Sensor{SensorID: id, Location: "Zone"})
	}

	// Query specific key.
	result := assertGetOne(t, ctx, mgr, map[string]any{"sensor-id": 30})
	if result.SensorID != 30 {
		t.Errorf("expected 30, got %d", result.SensorID)
	}

	// Non-existent key returns 0 results.
	results, err := mgr.Get(ctx, map[string]any{"sensor-id": 999})
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("expected 0 for non-existent key, got %d", len(results))
	}
}

func TestIntegration_IntegerKey_FilterChainable(t *testing.T) {
	// Ported from test_integer_key_filter_chainable_query.
	// Integer keys work with chainable query API.
	db := setupIntKeyDB(t)
	ctx := context.Background()

	mgr := gotype.NewManager[Sensor](db)

	assertInsert(t, ctx, mgr, &Sensor{SensorID: 1, Location: "A", Reading: new(10.5)})
	assertInsert(t, ctx, mgr, &Sensor{SensorID: 2, Location: "B", Reading: new(20.0)})
	assertInsert(t, ctx, mgr, &Sensor{SensorID: 3, Location: "C", Reading: new(30.0)})

	// Filter by integer key using Eq.
	results, err := mgr.Query().Filter(gotype.Eq("sensor-id", 2)).Execute(ctx)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(results) != 1 || results[0].SensorID != 2 {
		t.Errorf("expected sensor 2, got %v", results)
	}

	// Filter by Gt on integer key.
	results, err = mgr.Query().Filter(gotype.Gt("sensor-id", 1)).Execute(ctx)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(results) != 2 {
		t.Errorf("expected 2 sensors with id > 1, got %d", len(results))
	}
}

func TestIntegration_IntegerKey_AllAndCount(t *testing.T) {
	// Ported from test_integer_key_all_and_count.
	// All() and Count() work with integer key entities.
	db := setupIntKeyDB(t)
	ctx := context.Background()

	mgr := gotype.NewManager[Sensor](db)

	for i := 1; i <= 5; i++ {
		assertInsert(t, ctx, mgr, &Sensor{SensorID: i * 100, Location: "Zone"})
	}

	all := assertCount(t, ctx, mgr, 5)
	if len(all) != 5 {
		t.Errorf("All returned %d, expected 5", len(all))
	}

	count, err := mgr.Query().Count(ctx)
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 5 {
		t.Errorf("Count returned %d, expected 5", count)
	}
}
