package gotype

// Regression tests for the pool lock/wake protocol redesign
// (issues #19, #20, #44, #96, #97). All of these fail against the previous
// protocol; short timeouts make a regression fail fast instead of hanging
// the suite.

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"
)

// Issue #19: NewConnPool must return (with an error) when the pre-warm
// factory fails and IdleTimeout > 0. The old code called pool.Close(), which
// blocked forever on a cleaner goroutine that had never been started.
func TestConnPool_PrewarmFailureWithIdleTimeoutDoesNotHang(t *testing.T) {
	created := newPoolMockConn(1)
	calls := 0
	factory := func() (Conn, error) {
		calls++
		if calls == 1 {
			return created, nil
		}
		return nil, errors.New("server down")
	}

	config := PoolConfig{MinSize: 2, MaxSize: 5, IdleTimeout: time.Minute}
	done := make(chan error, 1)
	go func() {
		_, err := NewConnPool(config, factory)
		done <- err
	}()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("expected error from pre-warm factory failure, got nil")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("NewConnPool deadlocked after pre-warm failure with IdleTimeout > 0")
	}

	if !created.closed.Load() {
		t.Error("connection created before the pre-warm failure was not closed")
	}
}

// Issue #20: returning a dead connection frees a capacity slot; a queued
// waiter must be woken so it can dial a fresh connection instead of timing
// out while capacity sits free.
func TestConnPool_DeadConnPutWakesWaiter(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var connID atomic.Int32
		factory := func() (Conn, error) {
			return newPoolMockConn(int(connID.Add(1))), nil
		}

		config := PoolConfig{MaxSize: 1, WaitTimeout: 2 * time.Second}
		pool, err := NewConnPool(config, factory)
		if err != nil {
			t.Fatalf("NewConnPool failed: %v", err)
		}
		defer pool.Close()

		ctx := context.Background()
		conn1, err := pool.Get(ctx)
		if err != nil {
			t.Fatalf("initial Get failed: %v", err)
		}

		var wg sync.WaitGroup
		var conn2 Conn
		var getErr error
		wg.Go(func() {
			conn2, getErr = pool.Get(ctx)
		})

		// Wait until the waiter is queued.
		synctest.Wait()
		if stats := pool.Stats(); stats.Waiting != 1 {
			t.Fatalf("expected 1 waiting goroutine, got %d", stats.Waiting)
		}

		// Kill the checked-out connection and return it: the freed slot must
		// wake the waiter, which then dials a fresh connection.
		conn1.(*poolMockConn).Close()
		pool.Put(conn1)
		wg.Wait()

		if getErr != nil {
			t.Fatalf("waiter Get failed after dead-conn Put freed capacity: %v", getErr)
		}
		if conn2 == nil {
			t.Fatal("waiter did not receive a connection")
		}
		if conn2.(*poolMockConn).id == conn1.(*poolMockConn).id {
			t.Error("waiter received the dead connection instead of a fresh one")
		}
		pool.Put(conn2)
	})
}

// Issue #20 (second path): a factory error after a slot was reserved must
// also wake a queued waiter.
func TestConnPool_FactoryErrorWakesWaiter(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var calls atomic.Int32
		gate := make(chan struct{})
		factory := func() (Conn, error) {
			if calls.Add(1) == 1 {
				<-gate
				return nil, errors.New("dial failed")
			}
			return newPoolMockConn(2), nil
		}

		config := PoolConfig{MaxSize: 1, WaitTimeout: 2 * time.Second}
		pool, err := NewConnPool(config, factory)
		if err != nil {
			t.Fatalf("NewConnPool failed: %v", err)
		}
		defer pool.Close()

		ctx := context.Background()

		var wg sync.WaitGroup
		var err1, err2 error
		var conn2 Conn

		wg.Go(func() {
			_, err1 = pool.Get(ctx) // reserves the only slot, blocked in factory
		})
		synctest.Wait()

		wg.Go(func() {
			conn2, err2 = pool.Get(ctx) // pool at capacity: queues as waiter
		})
		synctest.Wait()
		if stats := pool.Stats(); stats.Waiting != 1 {
			t.Fatalf("expected 1 waiting goroutine, got %d", stats.Waiting)
		}

		close(gate) // first dial fails; its freed slot must wake the waiter
		wg.Wait()

		if err1 == nil {
			t.Fatal("expected first Get to fail with factory error")
		}
		if err2 != nil {
			t.Fatalf("waiter Get failed after factory error freed capacity: %v", err2)
		}
		if conn2 == nil {
			t.Fatal("waiter did not receive a connection")
		}
		pool.Put(conn2)
	})
}

// Issue #44: Put must not hold the pool mutex across the connection health
// check, so a slow IsOpen cannot stall unrelated pool operations.
func TestConnPool_PutDoesNotHoldMutexDuringIsOpen(t *testing.T) {
	pool := &ConnPool{numOpen: 1}
	bc := newBlockingIsOpenConn(1)

	putDone := make(chan struct{})
	go func() {
		pool.Put(bc)
		close(putDone)
	}()

	<-bc.started

	statsDone := make(chan PoolStats, 1)
	go func() { statsDone <- pool.Stats() }()

	select {
	case <-statsDone:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("Stats blocked while Put was in IsOpen")
	}

	close(bc.release)
	<-putDone

	if stats := pool.Stats(); stats.Available != 1 {
		t.Errorf("expected connection returned to pool, got %+v", stats)
	}
}

// Issue #96: Get must honor context cancellation while the factory dial is
// in flight, and a late-arriving connection must be handed back to the pool
// rather than leaked.
func TestConnPool_GetHonorsContextDuringDial(t *testing.T) {
	started := make(chan struct{})
	gate := make(chan struct{})
	late := newPoolMockConn(1)
	factory := func() (Conn, error) {
		close(started)
		<-gate
		return late, nil
	}

	pool, err := NewConnPool(PoolConfig{MaxSize: 2}, factory)
	if err != nil {
		t.Fatalf("NewConnPool failed: %v", err)
	}
	defer pool.Close()

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		_, err := pool.Get(ctx)
		errCh <- err
	}()

	<-started
	cancel()

	select {
	case err := <-errCh:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("expected context.Canceled from Get during dial, got %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Get did not honor context cancellation during connection creation")
	}

	// Let the factory finish: the late connection must land back in the pool
	// with its reserved slot intact.
	close(gate)
	deadline := time.Now().Add(2 * time.Second)
	for {
		stats := pool.Stats()
		if stats.Available == 1 && stats.Total == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("late-dialed connection not returned to pool: %+v", stats)
		}
		time.Sleep(5 * time.Millisecond)
	}
	if late.closed.Load() {
		t.Error("late-dialed connection was closed instead of pooled")
	}
}

// Issue #97: when Close races with Get's out-of-lock validation of a popped
// idle connection, the discarded connection must still be deducted from
// numOpen.
func TestConnPool_CloseDuringValidateAccounting(t *testing.T) {
	bc := newBlockingIsOpenConn(1)
	pool := &ConnPool{
		conns:   []pooledConn{{conn: bc, idleSince: time.Now()}},
		numOpen: 1,
	}

	errCh := make(chan error, 1)
	go func() {
		_, err := pool.Get(context.Background())
		errCh <- err
	}()

	<-bc.started // Get popped the conn and is validating outside the lock
	pool.Close()
	close(bc.release)

	select {
	case err := <-errCh:
		if err != ErrPoolClosed {
			t.Fatalf("expected ErrPoolClosed, got %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Get did not return after Close raced with validation")
	}

	if !bc.closed.Load() {
		t.Error("validated connection not closed after Close won the race")
	}
	if stats := pool.Stats(); stats.Total != 0 {
		t.Errorf("numOpen leaked on close-race during validation: %+v", stats)
	}
}

// Issue #97: when Close races with Get's factory dial, the freshly created
// connection must be closed and deducted from numOpen.
func TestConnPool_CloseDuringDialAccounting(t *testing.T) {
	started := make(chan struct{})
	gate := make(chan struct{})
	fresh := newPoolMockConn(1)
	factory := func() (Conn, error) {
		close(started)
		<-gate
		return fresh, nil
	}

	pool, err := NewConnPool(PoolConfig{MaxSize: 1}, factory)
	if err != nil {
		t.Fatalf("NewConnPool failed: %v", err)
	}

	errCh := make(chan error, 1)
	go func() {
		_, err := pool.Get(context.Background())
		errCh <- err
	}()

	<-started // Get reserved a slot and is dialing
	pool.Close()
	close(gate)

	select {
	case err := <-errCh:
		if err != ErrPoolClosed {
			t.Fatalf("expected ErrPoolClosed, got %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Get did not return after Close raced with the dial")
	}

	if !fresh.closed.Load() {
		t.Error("freshly dialed connection not closed after Close won the race")
	}
	if stats := pool.Stats(); stats.Total != 0 {
		t.Errorf("numOpen leaked on close-race during dial: %+v", stats)
	}
}

// Shakes out races between Get, Put, waiters, and Close under -race, and
// verifies that accounting always settles at zero with every connection
// closed (issues #97 and the waiter protocol generally).
func TestConnPool_CloseRaceStress(t *testing.T) {
	ctx := context.Background()
	for i := 0; i < 200; i++ {
		var mu sync.Mutex
		var created []*poolMockConn
		var id atomic.Int32
		factory := func() (Conn, error) {
			mc := newPoolMockConn(int(id.Add(1)))
			mu.Lock()
			created = append(created, mc)
			mu.Unlock()
			return mc, nil
		}

		pool, err := NewConnPool(PoolConfig{MaxSize: 2, WaitTimeout: 50 * time.Millisecond}, factory)
		if err != nil {
			t.Fatalf("NewConnPool failed: %v", err)
		}

		conn1, err := pool.Get(ctx)
		if err != nil {
			t.Fatalf("initial Get failed: %v", err)
		}

		var wg sync.WaitGroup
		wg.Go(func() {
			if c, err := pool.Get(ctx); err == nil {
				pool.Put(c)
			}
		})
		wg.Go(func() { pool.Put(conn1) })
		wg.Go(func() { pool.Close() })
		wg.Wait()

		if stats := pool.Stats(); stats.Total != 0 || stats.Available != 0 || stats.Waiting != 0 {
			t.Fatalf("iteration %d: pool accounting did not settle at zero: %+v", i, stats)
		}
		mu.Lock()
		for _, mc := range created {
			if !mc.closed.Load() {
				t.Fatalf("iteration %d: connection %d leaked unclosed", i, mc.id)
			}
		}
		mu.Unlock()
	}
}
