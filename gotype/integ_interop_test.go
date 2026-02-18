//go:build integration && cgo && typedb

package gotype_test

import (
	"context"
	"testing"

	"github.com/CaliLuke/go-typeql/gotype"
)

// ---------------------------------------------------------------------------
// Interop domain models — deep inheritance and mixed types
// ---------------------------------------------------------------------------

// Base abstract entity (registered but no instances created directly).
type Vehicle struct {
	gotype.BaseEntity
	Vin   string `typedb:"vin,key"`
	Make  string `typedb:"make"`
	Year  int    `typedb:"vehicle-year"`
}

type Car struct {
	gotype.BaseEntity
	Vin   string `typedb:"vin,key"`
	Make  string `typedb:"make"`
	Year  int    `typedb:"vehicle-year"`
	Doors int    `typedb:"doors"`
}

type Truck struct {
	gotype.BaseEntity
	Vin      string  `typedb:"vin,key"`
	Make     string  `typedb:"make"`
	Year     int     `typedb:"vehicle-year"`
	Payload  float64 `typedb:"payload-tons"`
}

type Driver struct {
	gotype.BaseEntity
	License string `typedb:"license,key"`
	DName   string `typedb:"driver-name"`
}

type Garage struct {
	gotype.BaseEntity
	GarageName string `typedb:"garage-name,key"`
	Capacity   int    `typedb:"capacity"`
}

type Drives struct {
	gotype.BaseRelation
	Operator   *Driver `typedb:"role:operator"`
	Operated   *Car    `typedb:"role:operated-car"`
}

type DrivesTruck struct {
	gotype.BaseRelation
	TruckOperator *Driver `typedb:"role:truck-operator"`
	OperatedTruck *Truck  `typedb:"role:operated-truck"`
}

type ParkedAt struct {
	gotype.BaseRelation
	ParkedCar    *Car    `typedb:"role:parked-car"`
	ParkingGarage *Garage `typedb:"role:parking-garage"`
}

// ---------------------------------------------------------------------------
// Setup
// ---------------------------------------------------------------------------

func setupInteropDB(t *testing.T) *gotype.Database {
	return setupTestDBWith(t, func() {
		_ = gotype.Register[Car]()
		_ = gotype.Register[Truck]()
		_ = gotype.Register[Driver]()
		_ = gotype.Register[Garage]()
		_ = gotype.Register[Drives]()
		_ = gotype.Register[DrivesTruck]()
		_ = gotype.Register[ParkedAt]()
	})
}

type interopFixture struct {
	db      *gotype.Database
	cars    []*Car
	trucks  []*Truck
	drivers []*Driver
	garages []*Garage
}

func seedInterop(t *testing.T) interopFixture {
	t.Helper()
	db := setupInteropDB(t)
	ctx := context.Background()

	carMgr := gotype.NewManager[Car](db)
	truckMgr := gotype.NewManager[Truck](db)
	driverMgr := gotype.NewManager[Driver](db)
	garageMgr := gotype.NewManager[Garage](db)
	drivesMgr := gotype.NewManager[Drives](db)
	drivesTruckMgr := gotype.NewManager[DrivesTruck](db)
	parkedMgr := gotype.NewManager[ParkedAt](db)

	cars := []*Car{
		{Vin: "CAR-001", Make: "Toyota", Year: 2020, Doors: 4},
		{Vin: "CAR-002", Make: "Honda", Year: 2022, Doors: 2},
		{Vin: "CAR-003", Make: "Tesla", Year: 2024, Doors: 4},
	}
	assertInsertMany(t, ctx, carMgr, cars)
	for i, c := range cars {
		cars[i] = assertGetOne(t, ctx, carMgr, map[string]any{"vin": c.Vin})
	}

	trucks := []*Truck{
		{Vin: "TRK-001", Make: "Ford", Year: 2021, Payload: 2.5},
		{Vin: "TRK-002", Make: "Chevy", Year: 2023, Payload: 3.0},
	}
	assertInsertMany(t, ctx, truckMgr, trucks)
	for i, tr := range trucks {
		trucks[i] = assertGetOne(t, ctx, truckMgr, map[string]any{"vin": tr.Vin})
	}

	drivers := []*Driver{
		{License: "DL-001", DName: "Alice"},
		{License: "DL-002", DName: "Bob"},
		{License: "DL-003", DName: "Carol"},
	}
	assertInsertMany(t, ctx, driverMgr, drivers)
	for i, d := range drivers {
		drivers[i] = assertGetOne(t, ctx, driverMgr, map[string]any{"license": d.License})
	}

	garages := []*Garage{
		{GarageName: "Downtown Garage", Capacity: 100},
		{GarageName: "Airport Parking", Capacity: 500},
	}
	assertInsertMany(t, ctx, garageMgr, garages)
	for i, g := range garages {
		garages[i] = assertGetOne(t, ctx, garageMgr, map[string]any{"garage-name": g.GarageName})
	}

	// Drives: Alice→CAR-001, Bob→CAR-002, Carol→CAR-003
	for i := range drivers {
		assertInsert(t, ctx, drivesMgr, &Drives{Operator: drivers[i], Operated: cars[i]})
	}

	// DrivesTruck: Alice→TRK-001, Bob→TRK-002
	assertInsert(t, ctx, drivesTruckMgr, &DrivesTruck{TruckOperator: drivers[0], OperatedTruck: trucks[0]})
	assertInsert(t, ctx, drivesTruckMgr, &DrivesTruck{TruckOperator: drivers[1], OperatedTruck: trucks[1]})

	// Parked: CAR-001→Downtown, CAR-002→Airport, CAR-003→Downtown
	parkedData := []struct{ c, g int }{
		{0, 0}, {1, 1}, {2, 0},
	}
	for _, p := range parkedData {
		assertInsert(t, ctx, parkedMgr, &ParkedAt{ParkedCar: cars[p.c], ParkingGarage: garages[p.g]})
	}

	return interopFixture{db: db, cars: cars, trucks: trucks, drivers: drivers, garages: garages}
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

func TestIntegration_Interop_AllEntitiesInserted(t *testing.T) {
	f := seedInterop(t)
	ctx := context.Background()

	assertCount(t, ctx, gotype.NewManager[Car](f.db), 3)
	assertCount(t, ctx, gotype.NewManager[Truck](f.db), 2)
	assertCount(t, ctx, gotype.NewManager[Driver](f.db), 3)
	assertCount(t, ctx, gotype.NewManager[Garage](f.db), 2)
}

func TestIntegration_Interop_AllRelationsInserted(t *testing.T) {
	f := seedInterop(t)
	ctx := context.Background()

	assertCount(t, ctx, gotype.NewManager[Drives](f.db), 3)
	assertCount(t, ctx, gotype.NewManager[DrivesTruck](f.db), 2)
	assertCount(t, ctx, gotype.NewManager[ParkedAt](f.db), 3)
}

func TestIntegration_Interop_MixedEntityTypesInSameDB(t *testing.T) {
	// Cars and Trucks coexist in same schema, both have vin/make/year.
	f := seedInterop(t)
	ctx := context.Background()

	carMgr := gotype.NewManager[Car](f.db)
	truckMgr := gotype.NewManager[Truck](f.db)

	// Both types share vin attribute but are distinct entity types.
	car := assertGetOne(t, ctx, carMgr, map[string]any{"vin": "CAR-001"})
	if car.Make != "Toyota" {
		t.Errorf("expected Toyota, got %q", car.Make)
	}

	truck := assertGetOne(t, ctx, truckMgr, map[string]any{"vin": "TRK-001"})
	if truck.Make != "Ford" {
		t.Errorf("expected Ford, got %q", truck.Make)
	}
}

func TestIntegration_Interop_DriverDrivesMultipleVehicleTypes(t *testing.T) {
	// Alice drives both a car (Drives) and a truck (DrivesTruck).
	f := seedInterop(t)
	ctx := context.Background()

	drivesMgr := gotype.NewManager[Drives](f.db)
	drivesTruckMgr := gotype.NewManager[DrivesTruck](f.db)

	drivesCount, err := drivesMgr.Query().Count(ctx)
	if err != nil {
		t.Fatalf("count drives: %v", err)
	}
	truckDrivesCount, err := drivesTruckMgr.Query().Count(ctx)
	if err != nil {
		t.Fatalf("count truck drives: %v", err)
	}
	if drivesCount != 3 {
		t.Errorf("expected 3 car drives, got %d", drivesCount)
	}
	if truckDrivesCount != 2 {
		t.Errorf("expected 2 truck drives, got %d", truckDrivesCount)
	}
}

func TestIntegration_Interop_FilterCrossingTypes(t *testing.T) {
	// Filter cars and trucks by the same attribute (year).
	f := seedInterop(t)
	ctx := context.Background()

	carMgr := gotype.NewManager[Car](f.db)
	truckMgr := gotype.NewManager[Truck](f.db)

	// Cars from 2022+
	cars, err := carMgr.Query().
		Filter(gotype.Gte("vehicle-year", 2022)).
		Execute(ctx)
	if err != nil {
		t.Fatalf("car query: %v", err)
	}
	if len(cars) != 2 { // Honda 2022, Tesla 2024
		t.Errorf("expected 2 recent cars, got %d", len(cars))
	}

	// Trucks from 2022+
	trucks, err := truckMgr.Query().
		Filter(gotype.Gte("vehicle-year", 2022)).
		Execute(ctx)
	if err != nil {
		t.Fatalf("truck query: %v", err)
	}
	if len(trucks) != 1 { // Chevy 2023
		t.Errorf("expected 1 recent truck, got %d", len(trucks))
	}
}

func TestIntegration_Interop_UpdateDifferentEntityTypes(t *testing.T) {
	f := seedInterop(t)
	ctx := context.Background()

	carMgr := gotype.NewManager[Car](f.db)
	truckMgr := gotype.NewManager[Truck](f.db)

	// Update car
	car := assertGetOne(t, ctx, carMgr, map[string]any{"vin": "CAR-001"})
	car.Doors = 5
	assertUpdate(t, ctx, carMgr, car)

	updated := assertGetOne(t, ctx, carMgr, map[string]any{"vin": "CAR-001"})
	if updated.Doors != 5 {
		t.Errorf("expected 5 doors, got %d", updated.Doors)
	}

	// Update truck
	truck := assertGetOne(t, ctx, truckMgr, map[string]any{"vin": "TRK-001"})
	truck.Payload = 3.5
	assertUpdate(t, ctx, truckMgr, truck)

	updatedTruck := assertGetOne(t, ctx, truckMgr, map[string]any{"vin": "TRK-001"})
	if updatedTruck.Payload != 3.5 {
		t.Errorf("expected payload 3.5, got %f", updatedTruck.Payload)
	}
}

func TestIntegration_Interop_DeleteEntityDoesNotAffectOtherTypes(t *testing.T) {
	f := seedInterop(t)
	ctx := context.Background()

	driverMgr := gotype.NewManager[Driver](f.db)

	// Delete a driver — should not affect cars or trucks.
	// First need to delete relations referencing this driver.
	drivesMgr := gotype.NewManager[Drives](f.db)
	drivesTruckMgr := gotype.NewManager[DrivesTruck](f.db)

	// Get and delete Carol's drives relation
	allDrives, err := drivesMgr.All(ctx)
	if err != nil {
		t.Fatalf("get drives: %v", err)
	}
	// Carol is driver[2], drives car[2]
	for _, d := range allDrives {
		assertDelete(t, ctx, drivesMgr, d)
	}

	// Now delete all truck drives too
	allTruckDrives, err := drivesTruckMgr.All(ctx)
	if err != nil {
		t.Fatalf("get truck drives: %v", err)
	}
	for _, d := range allTruckDrives {
		assertDelete(t, ctx, drivesTruckMgr, d)
	}

	// Now we can delete a driver
	carol := assertGetOne(t, ctx, driverMgr, map[string]any{"license": "DL-003"})
	assertDelete(t, ctx, driverMgr, carol)

	// Cars and trucks should be unaffected
	assertCount(t, ctx, gotype.NewManager[Car](f.db), 3)
	assertCount(t, ctx, gotype.NewManager[Truck](f.db), 2)
	assertCount(t, ctx, driverMgr, 2)
}

func TestIntegration_Interop_BatchInsertMixedTypes(t *testing.T) {
	// Insert multiple entities of different types in sequence.
	db := setupInteropDB(t)
	ctx := context.Background()

	carMgr := gotype.NewManager[Car](db)
	truckMgr := gotype.NewManager[Truck](db)

	cars := []*Car{
		{Vin: "BATCH-C1", Make: "BMW", Year: 2025, Doors: 4},
		{Vin: "BATCH-C2", Make: "Audi", Year: 2025, Doors: 4},
	}
	assertInsertMany(t, ctx, carMgr, cars)

	trucks := []*Truck{
		{Vin: "BATCH-T1", Make: "Volvo", Year: 2025, Payload: 5.0},
	}
	assertInsertMany(t, ctx, truckMgr, trucks)

	assertCount(t, ctx, carMgr, 2)
	assertCount(t, ctx, truckMgr, 1)
}

func TestIntegration_Interop_AggregateAcrossEntityType(t *testing.T) {
	f := seedInterop(t)
	ctx := context.Background()

	carMgr := gotype.NewManager[Car](f.db)
	truckMgr := gotype.NewManager[Truck](f.db)

	// Avg year of cars
	carAvg, err := carMgr.Query().Avg("vehicle-year").Execute(ctx)
	if err != nil {
		t.Fatalf("car avg: %v", err)
	}
	// (2020+2022+2024)/3 = 2022
	if carAvg < 2021.9 || carAvg > 2022.1 {
		t.Errorf("expected car avg year ~2022, got %f", carAvg)
	}

	// Avg payload of trucks
	truckAvg, err := truckMgr.Query().Avg("payload-tons").Execute(ctx)
	if err != nil {
		t.Fatalf("truck avg: %v", err)
	}
	// (2.5+3.0)/2 = 2.75
	if truckAvg < 2.74 || truckAvg > 2.76 {
		t.Errorf("expected truck avg payload ~2.75, got %f", truckAvg)
	}
}

func TestIntegration_Interop_GarageCapacityFilter(t *testing.T) {
	f := seedInterop(t)
	ctx := context.Background()
	mgr := gotype.NewManager[Garage](f.db)

	results, err := mgr.Query().
		Filter(gotype.Gte("capacity", 200)).
		Execute(ctx)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 large garage, got %d", len(results))
	}
	if results[0].GarageName != "Airport Parking" {
		t.Errorf("expected Airport Parking, got %q", results[0].GarageName)
	}
}
