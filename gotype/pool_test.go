package gotype

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// poolTestEntity is a simple entity for pool testing
type poolTestEntity struct {
	BaseEntity
	Name string `typedb:"name,key"`
}

// poolMockConn is a mock connection for pool testing
type poolMockConn struct {
	id     int
	open   atomic.Bool
	closed atomic.Bool
}

func newPoolMockConn(id int) *poolMockConn {
	mc := &poolMockConn{id: id}
	mc.open.Store(true)
	return mc
}

func (mc *poolMockConn) Transaction(dbName string, txType int) (Tx, error) {
	if !mc.open.Load() {
		return nil, errors.New("connection closed")
	}
	// Return a valid mock transaction with no preset responses
	return &mockTx{responses: [][]map[string]any{}}, nil
}

func (mc *poolMockConn) Schema(dbName string) (string, error) {
	if !mc.open.Load() {
		return "", errors.New("connection closed")
	}
	return "define\nentity test;", nil
}

func (mc *poolMockConn) DatabaseCreate(name string) error           { return nil }
func (mc *poolMockConn) DatabaseDelete(name string) error           { return nil }
func (mc *poolMockConn) DatabaseContains(name string) (bool, error) { return true, nil }
func (mc *poolMockConn) DatabaseAll() ([]string, error)             { return []string{"mock"}, nil }

func (mc *poolMockConn) Close() {
	mc.open.Store(false)
	mc.closed.Store(true)
}

func (mc *poolMockConn) IsOpen() bool {
	return mc.open.Load()
}

func TestConnPool_GetPut(t *testing.T) {
	connID := 0
	factory := func() (Conn, error) {
		connID++
		return newPoolMockConn(connID), nil
	}

	config := PoolConfig{MinSize: 0, MaxSize: 5}
	pool, err := NewConnPool(config, factory)
	if err != nil {
		t.Fatalf("NewConnPool failed: %v", err)
	}
	defer pool.Close()

	// Get a connection
	ctx := context.Background()
	conn1, err := pool.Get(ctx)
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}

	stats := pool.Stats()
	if stats.InUse != 1 || stats.Total != 1 {
		t.Errorf("Stats after Get: got InUse=%d Total=%d, want InUse=1 Total=1", stats.InUse, stats.Total)
	}

	// Return connection to pool
	pool.Put(conn1)

	stats = pool.Stats()
	if stats.Available != 1 || stats.InUse != 0 {
		t.Errorf("Stats after Put: got Available=%d InUse=%d, want Available=1 InUse=0", stats.Available, stats.InUse)
	}

	// Get connection again - should reuse existing one
	conn2, err := pool.Get(ctx)
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}

	// Should be the same connection
	if conn2.(*poolMockConn).id != conn1.(*poolMockConn).id {
		t.Errorf("Expected reused connection, got different connection")
	}

	pool.Put(conn2)
}

func TestConnPool_MaxSize(t *testing.T) {
	connID := 0
	factory := func() (Conn, error) {
		connID++
		return newPoolMockConn(connID), nil
	}

	config := PoolConfig{MinSize: 0, MaxSize: 2, WaitTimeout: 100 * time.Millisecond}
	pool, err := NewConnPool(config, factory)
	if err != nil {
		t.Fatalf("NewConnPool failed: %v", err)
	}
	defer pool.Close()

	ctx := context.Background()

	// Get max connections
	conn1, _ := pool.Get(ctx)
	conn2, _ := pool.Get(ctx)

	stats := pool.Stats()
	if stats.Total != 2 {
		t.Errorf("Expected 2 total connections, got %d", stats.Total)
	}

	// Try to get one more - should timeout
	start := time.Now()
	_, err = pool.Get(ctx)
	elapsed := time.Since(start)

	if err != ErrPoolTimeout {
		t.Errorf("Expected ErrPoolTimeout, got %v", err)
	}

	if elapsed < 90*time.Millisecond {
		t.Errorf("Timeout happened too quickly: %v", elapsed)
	}

	// Return a connection and try again - should succeed
	pool.Put(conn1)

	conn3, err := pool.Get(ctx)
	if err != nil {
		t.Errorf("Get after Put failed: %v", err)
	}

	pool.Put(conn2)
	pool.Put(conn3)
}

func TestConnPool_MinSize(t *testing.T) {
	connID := 0
	factory := func() (Conn, error) {
		connID++
		return newPoolMockConn(connID), nil
	}

	config := PoolConfig{MinSize: 3, MaxSize: 10}
	pool, err := NewConnPool(config, factory)
	if err != nil {
		t.Fatalf("NewConnPool failed: %v", err)
	}
	defer pool.Close()

	stats := pool.Stats()
	if stats.Available != 3 {
		t.Errorf("Expected 3 pre-warmed connections, got %d", stats.Available)
	}

	if connID != 3 {
		t.Errorf("Expected 3 connections created, got %d", connID)
	}
}

func TestConnPool_InvalidConfig(t *testing.T) {
	factory := func() (Conn, error) {
		return newPoolMockConn(1), nil
	}

	// MinSize > MaxSize should fail
	config := PoolConfig{MinSize: 10, MaxSize: 5}
	_, err := NewConnPool(config, factory)
	if err == nil {
		t.Error("Expected error for MinSize > MaxSize, got nil")
	}
}

func TestConnPool_FactoryError(t *testing.T) {
	factoryErr := errors.New("connection failed")
	factory := func() (Conn, error) {
		return nil, factoryErr
	}

	config := PoolConfig{MinSize: 2, MaxSize: 5}
	_, err := NewConnPool(config, factory)
	if err == nil {
		t.Error("Expected error from factory failure during pre-warm, got nil")
	}
}

func TestConnPool_DeadConnectionDiscarded(t *testing.T) {
	connID := 0
	factory := func() (Conn, error) {
		connID++
		return newPoolMockConn(connID), nil
	}

	config := PoolConfig{MinSize: 0, MaxSize: 5}
	pool, err := NewConnPool(config, factory)
	if err != nil {
		t.Fatalf("NewConnPool failed: %v", err)
	}
	defer pool.Close()

	ctx := context.Background()

	// Get a connection and close it
	conn1, _ := pool.Get(ctx)
	mc := conn1.(*poolMockConn)
	mc.Close()

	// Return dead connection - should be discarded
	pool.Put(conn1)

	stats := pool.Stats()
	if stats.Available != 0 {
		t.Errorf("Expected dead connection to be discarded, got Available=%d", stats.Available)
	}

	// Get another connection - should create a new one
	conn2, err := pool.Get(ctx)
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}

	if conn2.(*poolMockConn).id == mc.id {
		t.Error("Expected new connection, got dead connection")
	}

	pool.Put(conn2)
}

func TestConnPool_ConcurrentAccess(t *testing.T) {
	var connID atomic.Int32
	factory := func() (Conn, error) {
		id := connID.Add(1)
		return newPoolMockConn(int(id)), nil
	}

	config := PoolConfig{MinSize: 2, MaxSize: 10}
	pool, err := NewConnPool(config, factory)
	if err != nil {
		t.Fatalf("NewConnPool failed: %v", err)
	}
	defer pool.Close()

	ctx := context.Background()
	var wg sync.WaitGroup

	// Spawn 20 goroutines that each get and put a connection
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			conn, err := pool.Get(ctx)
			if err != nil {
				t.Errorf("Get failed: %v", err)
				return
			}
			// Simulate some work
			time.Sleep(10 * time.Millisecond)
			pool.Put(conn)
		}()
	}

	wg.Wait()

	stats := pool.Stats()
	if stats.InUse != 0 {
		t.Errorf("Expected all connections returned, got InUse=%d", stats.InUse)
	}

	if stats.Total > 10 {
		t.Errorf("Expected max 10 total connections, got %d", stats.Total)
	}
}

func TestConnPool_ContextCancellation(t *testing.T) {
	factory := func() (Conn, error) {
		return newPoolMockConn(1), nil
	}

	config := PoolConfig{MinSize: 0, MaxSize: 1}
	pool, err := NewConnPool(config, factory)
	if err != nil {
		t.Fatalf("NewConnPool failed: %v", err)
	}
	defer pool.Close()

	// Get the only connection
	conn1, _ := pool.Get(context.Background())

	// Try to get another with cancelled context
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err = pool.Get(ctx)
	if err != context.Canceled {
		t.Errorf("Expected context.Canceled, got %v", err)
	}

	pool.Put(conn1)
}

func TestConnPool_Close(t *testing.T) {
	connID := 0
	var createdConns []*poolMockConn
	factory := func() (Conn, error) {
		connID++
		mc := newPoolMockConn(connID)
		createdConns = append(createdConns, mc)
		return mc, nil
	}

	config := PoolConfig{MinSize: 3, MaxSize: 5}
	pool, err := NewConnPool(config, factory)
	if err != nil {
		t.Fatalf("NewConnPool failed: %v", err)
	}

	// Close pool
	pool.Close()

	// All pre-warmed connections should be closed
	for _, mc := range createdConns {
		if !mc.closed.Load() {
			t.Error("Expected connection to be closed after pool.Close()")
		}
	}

	// Get should fail after close
	_, err = pool.Get(context.Background())
	if err != ErrPoolClosed {
		t.Errorf("Expected ErrPoolClosed after Close, got %v", err)
	}
}

func TestConnPool_IdleTimeout(t *testing.T) {
	connID := 0
	var createdConns []*poolMockConn
	factory := func() (Conn, error) {
		connID++
		mc := newPoolMockConn(connID)
		createdConns = append(createdConns, mc)
		return mc, nil
	}

	config := PoolConfig{
		MinSize:     2,
		MaxSize:     5,
		IdleTimeout: 100 * time.Millisecond,
	}
	pool, err := NewConnPool(config, factory)
	if err != nil {
		t.Fatalf("NewConnPool failed: %v", err)
	}
	defer pool.Close()

	ctx := context.Background()

	// Create 3 additional connections (total 5)
	conn1, _ := pool.Get(ctx)
	conn2, _ := pool.Get(ctx)
	conn3, _ := pool.Get(ctx)

	// Return them all
	pool.Put(conn1)
	pool.Put(conn2)
	pool.Put(conn3)

	// Wait for idle timeout + cleaner cycle
	time.Sleep(200 * time.Millisecond)

	stats := pool.Stats()
	// Should have cleaned down to MinSize (2)
	if stats.Available < 2 {
		t.Errorf("Expected at least MinSize (2) connections after cleanup, got %d", stats.Available)
	}
	if stats.Available > 3 {
		t.Errorf("Expected idle connections cleaned up, got %d available", stats.Available)
	}
}

func TestConnPool_WaitQueue(t *testing.T) {
	factory := func() (Conn, error) {
		return newPoolMockConn(1), nil
	}

	config := PoolConfig{MinSize: 0, MaxSize: 1, WaitTimeout: 500 * time.Millisecond}
	pool, err := NewConnPool(config, factory)
	if err != nil {
		t.Fatalf("NewConnPool failed: %v", err)
	}
	defer pool.Close()

	ctx := context.Background()

	// Get the only connection
	conn1, _ := pool.Get(ctx)

	var wg sync.WaitGroup
	var conn2 Conn
	var getErr error

	// Spawn goroutine to wait for connection
	wg.Add(1)
	go func() {
		defer wg.Done()
		conn2, getErr = pool.Get(ctx)
	}()

	// Give it time to enter wait queue
	time.Sleep(50 * time.Millisecond)

	stats := pool.Stats()
	if stats.Waiting != 1 {
		t.Errorf("Expected 1 waiting goroutine, got %d", stats.Waiting)
	}

	// Return connection - should satisfy waiter
	pool.Put(conn1)

	wg.Wait()

	if getErr != nil {
		t.Errorf("Waiter Get failed: %v", getErr)
	}

	if conn2 == nil {
		t.Error("Waiter did not receive connection")
	}

	pool.Put(conn2)
}

func TestNewDatabaseWithPool(t *testing.T) {
	ClearRegistry()
	if err := Register[poolTestEntity](); err != nil {
		t.Fatal(err)
	}

	connID := 0
	factory := func() (Conn, error) {
		connID++
		return newPoolMockConn(connID), nil
	}

	config := DefaultPoolConfig()
	config.MinSize = 2
	config.MaxSize = 5

	db, err := NewDatabaseWithPool(config, "testdb", factory)
	if err != nil {
		t.Fatalf("NewDatabaseWithPool failed: %v", err)
	}
	defer db.Close()

	// Should have pre-warmed connections
	if connID != 2 {
		t.Errorf("Expected 2 pre-warmed connections, got %d", connID)
	}

	// Should be able to open transaction
	tx, err := db.Transaction(WriteTransaction)
	if err != nil {
		t.Fatalf("Transaction failed: %v", err)
	}
	defer tx.Close()

	// Should be able to get schema
	ctx := context.Background()
	schema, err := db.Schema(ctx)
	if err != nil {
		t.Fatalf("Schema failed: %v", err)
	}

	if schema == "" {
		t.Error("Expected non-empty schema")
	}
}

func TestPooledTx_ReturnsConnectionOnClose(t *testing.T) {
	factory := func() (Conn, error) {
		return newPoolMockConn(1), nil
	}

	config := PoolConfig{MinSize: 1, MaxSize: 2}
	pool, err := NewConnPool(config, factory)
	if err != nil {
		t.Fatalf("NewConnPool failed: %v", err)
	}
	defer pool.Close()

	adapter := &poolConnAdapter{pool: pool, dbName: "testdb"}

	// Open transaction
	tx, err := adapter.Transaction("testdb", 0)
	if err != nil {
		t.Fatalf("Transaction failed: %v", err)
	}

	stats := pool.Stats()
	if stats.InUse != 1 {
		t.Errorf("Expected 1 in-use connection, got %d", stats.InUse)
	}

	// Close transaction - should return connection to pool
	tx.Close()

	stats = pool.Stats()
	if stats.InUse != 0 {
		t.Errorf("Expected connection returned after tx.Close(), got InUse=%d", stats.InUse)
	}
	if stats.Available != 1 {
		t.Errorf("Expected connection available after tx.Close(), got Available=%d", stats.Available)
	}
}

func TestPooledTx_ReturnsConnectionOnCommit(t *testing.T) {
	factory := func() (Conn, error) {
		return newPoolMockConn(1), nil
	}

	config := PoolConfig{MinSize: 1, MaxSize: 2}
	pool, err := NewConnPool(config, factory)
	if err != nil {
		t.Fatalf("NewConnPool failed: %v", err)
	}
	defer pool.Close()

	adapter := &poolConnAdapter{pool: pool, dbName: "testdb"}

	tx, err := adapter.Transaction("testdb", 1)
	if err != nil {
		t.Fatalf("Transaction failed: %v", err)
	}

	// Commit - should return connection
	if err := tx.Commit(); err != nil {
		t.Fatalf("Commit failed: %v", err)
	}

	stats := pool.Stats()
	if stats.Available != 1 {
		t.Errorf("Expected connection available after Commit, got %d", stats.Available)
	}
}
