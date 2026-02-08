//go:build cgo && typedb && integration

package gotype

import (
	"context"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/CaliLuke/go-typeql/driver"
)

// poolTestPerson is a simple entity for pool integration testing
type poolTestPerson struct {
	BaseEntity
	Email string `typedb:"email,key"`
	Name  string `typedb:"name"`
	Age   int    `typedb:"age"`
}

// driverAdapter adapts driver.Driver to the Conn interface for pool testing
type driverAdapter struct {
	drv *driver.Driver
}

func (a *driverAdapter) Transaction(dbName string, txType int) (Tx, error) {
	tx, err := a.drv.Transaction(dbName, driver.TransactionType(txType))
	if err != nil {
		return nil, err
	}
	return tx, nil
}

func (a *driverAdapter) Schema(dbName string) (string, error) {
	return a.drv.Databases().Schema(dbName)
}

func (a *driverAdapter) DatabaseCreate(name string) error {
	return a.drv.Databases().Create(name)
}

func (a *driverAdapter) DatabaseDelete(name string) error {
	return a.drv.Databases().Delete(name)
}

func (a *driverAdapter) DatabaseContains(name string) (bool, error) {
	return a.drv.Databases().Contains(name)
}

func (a *driverAdapter) DatabaseAll() ([]string, error) {
	return a.drv.Databases().All()
}

func (a *driverAdapter) Close() {
	a.drv.Close()
}

func (a *driverAdapter) IsOpen() bool {
	return a.drv.IsOpen()
}

func TestIntegration_ConnectionPool_ConcurrentQueries(t *testing.T) {
	ClearRegistry()
	Register[poolTestPerson]()

	dbAddr := os.Getenv("TEST_DB_ADDRESS")
	if dbAddr == "" {
		dbAddr = "localhost:1729"
	}

	dbName := "test_pool_concurrent"

	// Open a driver and create test database
	d, err := driver.Open(dbAddr, "admin", "password")
	if err != nil {
		t.Fatalf("Failed to open driver: %v", err)
	}
	defer d.Close()

	conn := &driverAdapter{drv: d}

	// Clean up first
	if exists, _ := conn.DatabaseContains(dbName); exists {
		_ = conn.DatabaseDelete(dbName)
	}

	// Create database
	if err := conn.DatabaseCreate(dbName); err != nil {
		t.Fatalf("Failed to create database: %v", err)
	}
	defer func() { _ = conn.DatabaseDelete(dbName) }()

	// Define schema
	schema := GenerateSchema()
	tempDB := NewDatabase(conn, dbName)
	defer tempDB.Close()

	if err := tempDB.ExecuteSchema(context.Background(), schema); err != nil {
		t.Fatalf("Failed to create schema: %v", err)
	}

	// Create database with connection pool
	config := PoolConfig{
		MinSize:     2,
		MaxSize:     5,
		IdleTimeout: 30 * time.Second,
		WaitTimeout: 5 * time.Second,
	}

	connFactory := func() (Conn, error) {
		drv, err := driver.Open(dbAddr, "admin", "password")
		if err != nil {
			return nil, err
		}
		return &driverAdapter{drv: drv}, nil
	}

	db, err := NewDatabaseWithPool(config, dbName, connFactory)
	if err != nil {
		t.Fatalf("Failed to create pooled database: %v", err)
	}
	defer db.Close()

	mgr := NewManager[poolTestPerson](db)

	// Insert some initial data
	ctx := context.Background()
	for i := 0; i < 10; i++ {
		person := &poolTestPerson{
			Email: fmt.Sprintf("person%d@pool.test", i),
			Name:  fmt.Sprintf("Person %d", i),
			Age:   20 + i,
		}
		if err := mgr.Insert(ctx, person); err != nil {
			t.Fatalf("Failed to insert person %d: %v", i, err)
		}
	}

	// Concurrent queries
	var wg sync.WaitGroup
	var successCount atomic.Int32
	var errorCount atomic.Int32

	numWorkers := 20
	queriesPerWorker := 5

	for i := 0; i < numWorkers; i++ {
		wg.Add(1)
		workerID := i

		go func() {
			defer wg.Done()

			for j := 0; j < queriesPerWorker; j++ {
				// Query all persons
				persons, err := mgr.All(ctx)
				if err != nil {
					t.Logf("Worker %d query %d failed: %v", workerID, j, err)
					errorCount.Add(1)
					continue
				}

				if len(persons) != 10 {
					t.Errorf("Worker %d query %d: expected 10 persons, got %d", workerID, j, len(persons))
					errorCount.Add(1)
					continue
				}

				successCount.Add(1)

				// Small delay between queries
				time.Sleep(10 * time.Millisecond)
			}
		}()
	}

	wg.Wait()

	totalQueries := int32(numWorkers * queriesPerWorker)
	success := successCount.Load()
	errors := errorCount.Load()

	t.Logf("Completed %d/%d queries successfully (%d errors)", success, totalQueries, errors)

	if success < totalQueries*9/10 {
		t.Errorf("Too many query failures: %d/%d succeeded", success, totalQueries)
	}

	// Check that pool stats are reasonable
	if poolConn, ok := db.GetConn().(*poolConnAdapter); ok {
		stats := poolConn.pool.Stats()
		t.Logf("Pool stats: Available=%d InUse=%d Total=%d", stats.Available, stats.InUse, stats.Total)

		if stats.Total > config.MaxSize {
			t.Errorf("Pool exceeded MaxSize: Total=%d MaxSize=%d", stats.Total, config.MaxSize)
		}

		if stats.InUse != 0 {
			t.Errorf("Expected all connections returned after completion, got InUse=%d", stats.InUse)
		}
	}
}

func TestIntegration_ConnectionPool_Transactions(t *testing.T) {
	ClearRegistry()
	Register[poolTestPerson]()

	dbAddr := os.Getenv("TEST_DB_ADDRESS")
	if dbAddr == "" {
		dbAddr = "localhost:1729"
	}

	dbName := "test_pool_transactions"

	// Setup
	d, err := driver.Open(dbAddr, "admin", "password")
	if err != nil {
		t.Fatalf("Failed to open driver: %v", err)
	}
	defer d.Close()

	conn := &driverAdapter{drv: d}

	if exists, _ := conn.DatabaseContains(dbName); exists {
		_ = conn.DatabaseDelete(dbName)
	}

	if err := conn.DatabaseCreate(dbName); err != nil {
		t.Fatalf("Failed to create database: %v", err)
	}
	defer func() { _ = conn.DatabaseDelete(dbName) }()

	schema := GenerateSchema()
	tempDB := NewDatabase(conn, dbName)
	if err := tempDB.ExecuteSchema(context.Background(), schema); err != nil {
		t.Fatalf("Failed to create schema: %v", err)
	}
	tempDB.Close()

	// Create pooled database
	config := PoolConfig{
		MinSize:     1,
		MaxSize:     3,
		IdleTimeout: 30 * time.Second,
	}

	connFactory := func() (Conn, error) {
		drv, err := driver.Open(dbAddr, "admin", "password")
		if err != nil {
			return nil, err
		}
		return &driverAdapter{drv: drv}, nil
	}

	db, err := NewDatabaseWithPool(config, dbName, connFactory)
	if err != nil {
		t.Fatalf("Failed to create pooled database: %v", err)
	}
	defer db.Close()

	mgr := NewManager[poolTestPerson](db)

	ctx := context.Background()

	// Test: Multiple concurrent transactions
	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()

			person := &poolTestPerson{
				Email: fmt.Sprintf("txtest%d@pool.test", id),
				Name:  fmt.Sprintf("TxTest %d", id),
				Age:   30 + id,
			}

			if err := mgr.Insert(ctx, person); err != nil {
				t.Errorf("Concurrent insert %d failed: %v", id, err)
			}
		}(i)
	}

	wg.Wait()

	// Verify all persons were inserted
	persons, err := mgr.All(ctx)
	if err != nil {
		t.Fatalf("All failed: %v", err)
	}

	if len(persons) != 5 {
		t.Errorf("Expected 5 persons after concurrent inserts, got %d", len(persons))
	}
}

func TestIntegration_ConnectionPool_ConnectionReuse(t *testing.T) {
	dbAddr := os.Getenv("TEST_DB_ADDRESS")
	if dbAddr == "" {
		dbAddr = "localhost:1729"
	}

	dbName := "test_pool_reuse"

	// Setup
	d, err := driver.Open(dbAddr, "admin", "password")
	if err != nil {
		t.Fatalf("Failed to open driver: %v", err)
	}
	defer d.Close()

	conn := &driverAdapter{drv: d}

	if exists, _ := conn.DatabaseContains(dbName); exists {
		_ = conn.DatabaseDelete(dbName)
	}

	if err := conn.DatabaseCreate(dbName); err != nil {
		t.Fatalf("Failed to create database: %v", err)
	}
	defer func() { _ = conn.DatabaseDelete(dbName) }()

	// Create pooled database with small max size to force reuse
	config := PoolConfig{
		MinSize:     1,
		MaxSize:     2,
		IdleTimeout: 30 * time.Second,
	}

	var connCount atomic.Int32
	connFactory := func() (Conn, error) {
		connCount.Add(1)
		drv, err := driver.Open(dbAddr, "admin", "password")
		if err != nil {
			return nil, err
		}
		return &driverAdapter{drv: drv}, nil
	}

	db, err := NewDatabaseWithPool(config, dbName, connFactory)
	if err != nil {
		t.Fatalf("Failed to create pooled database: %v", err)
	}
	defer db.Close()

	ctx := context.Background()

	// Execute multiple queries - should reuse connections
	for i := 0; i < 10; i++ {
		_, err := db.ExecuteRead(ctx, "match attribute $a; fetch { \"label\": label($a) };")
		if err != nil {
			t.Errorf("Query %d failed: %v", i, err)
		}
	}

	// Should have created at most MaxSize connections
	created := connCount.Load()
	if created > int32(config.MaxSize) {
		t.Errorf("Expected at most %d connections created, got %d", config.MaxSize, created)
	}

	t.Logf("Created %d connections for 10 queries (pool size: min=%d max=%d)", created, config.MinSize, config.MaxSize)
}
