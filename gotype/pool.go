package gotype

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

// PoolConfig specifies connection pool behavior.
type PoolConfig struct {
	// MinSize is the minimum number of connections to maintain (0 = no minimum).
	MinSize int
	// MaxSize is the maximum number of connections allowed (0 = unlimited).
	MaxSize int
	// IdleTimeout is the duration after which idle connections are closed (0 = never expire).
	IdleTimeout time.Duration
	// WaitTimeout is the maximum time to wait for an available connection (0 = no timeout).
	WaitTimeout time.Duration
}

// DefaultPoolConfig returns a reasonable default pool configuration.
func DefaultPoolConfig() PoolConfig {
	return PoolConfig{
		MinSize:     2,
		MaxSize:     10,
		IdleTimeout: 5 * time.Minute,
		WaitTimeout: 10 * time.Second,
	}
}

// ConnPool manages a pool of database connections for concurrent access.
//
// Locking protocol: p.mu protects conns, numOpen, waitQueue, and closed.
// Blocking calls on connections (IsOpen, Close, the factory) are never made
// while holding p.mu. numOpen counts every live connection: idle in conns,
// checked out by callers, and slots reserved for in-flight factory dials.
// Whenever numOpen is decremented while waiters are queued, one waiter is
// woken with a retry signal so freed capacity is never lost.
type ConnPool struct {
	config      PoolConfig
	connFactory func() (Conn, error) // factory function to create new connections

	mu        sync.Mutex
	conns     []pooledConn  // available connections
	numOpen   int           // total open connections (available + in-use + dialing)
	waitQueue []*poolWaiter // waiting goroutines
	closed    bool

	cleanerStarted bool          // whether the idle-connection cleaner goroutine was started
	stopCleaner    chan struct{} // signal to stop the idle connection cleaner
	cleanerDone    chan struct{} // signal that cleaner has stopped
}

// pooledConn tracks a connection and its idle time.
type pooledConn struct {
	conn      Conn
	idleSince time.Time
}

// poolWaitResult is delivered to a queued waiter. Exactly one of three shapes
// is sent: a connection handoff (conn != nil), an error (err != nil), or a
// retry signal (both nil) meaning a capacity slot was freed and the waiter
// should loop and try to acquire again.
type poolWaitResult struct {
	conn Conn
	err  error
}

// poolWaiter represents a goroutine queued in Get waiting for capacity.
//
// The result channel is buffered (capacity 1) and has exactly one sender: the
// party that pops the waiter from waitQueue while holding p.mu owns it and
// sends at most one result before releasing the lock. Sends therefore never
// block. A waiter that times out removes itself from the queue under p.mu and
// then drains the channel non-blockingly; because pop+send happen inside one
// critical section, any result claimed before the removal is guaranteed to be
// in the buffer by then.
type poolWaiter struct {
	result chan poolWaitResult
}

var (
	// ErrPoolClosed is returned when attempting to get a connection from a closed pool.
	ErrPoolClosed = errors.New("connection pool is closed")
	// ErrPoolTimeout is returned when waiting for a connection times out.
	ErrPoolTimeout = errors.New("timeout waiting for available connection")
)

// NewConnPool creates a new connection pool with the given configuration and factory function.
// The factory function is called to create new connections when needed.
// If config.MinSize > 0, the pool will be pre-warmed with MinSize connections.
func NewConnPool(config PoolConfig, factory func() (Conn, error)) (*ConnPool, error) {
	if config.MaxSize > 0 && config.MinSize > config.MaxSize {
		return nil, fmt.Errorf("invalid pool config: MinSize (%d) > MaxSize (%d)", config.MinSize, config.MaxSize)
	}

	pool := &ConnPool{
		config:      config,
		connFactory: factory,
		conns:       make([]pooledConn, 0, config.MaxSize),
		waitQueue:   make([]*poolWaiter, 0),
		stopCleaner: make(chan struct{}),
		cleanerDone: make(chan struct{}),
	}

	// Pre-warm the pool with MinSize connections
	if config.MinSize > 0 {
		for i := 0; i < config.MinSize; i++ {
			conn, err := factory()
			if err != nil {
				// Close the connections created so far directly. The pool has
				// not escaped yet and the cleaner goroutine has not started,
				// so pool.Close() must not be used here: it would block
				// forever waiting for a cleaner that never ran (issue #19).
				for _, pc := range pool.conns {
					pc.conn.Close()
				}
				return nil, fmt.Errorf("failed to create initial connection %d/%d: %w", i+1, config.MinSize, err)
			}
			pool.conns = append(pool.conns, pooledConn{conn: conn, idleSince: time.Now()})
			pool.numOpen++
		}
	}

	// Start the idle connection cleaner if idle timeout is configured
	if config.IdleTimeout > 0 {
		pool.cleanerStarted = true
		go pool.cleanIdleConnections()
	}

	return pool, nil
}

// Get acquires a connection from the pool.
// If no connections are available and the pool is at max capacity, it waits for one to become available.
// Returns ErrPoolClosed if the pool is closed, or ErrPoolTimeout if WaitTimeout is exceeded.
func (p *ConnPool) Get(ctx context.Context) (Conn, error) {
	// Fast path: check context before acquiring lock
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	for {
		p.mu.Lock()

		if p.closed {
			p.mu.Unlock()
			return nil, ErrPoolClosed
		}

		// Try to get an available connection.
		if n := len(p.conns); n > 0 {
			pc := p.conns[n-1]
			p.conns = p.conns[:n-1]
			p.mu.Unlock()

			// Validate the connection outside the pool mutex so one slow FFI
			// health check does not block unrelated Get/Put operations.
			if pc.conn.IsOpen() {
				p.mu.Lock()
				if p.closed {
					// The pool closed while we validated; it no longer tracks
					// this connection, so account for it and discard it.
					p.numOpen--
					p.mu.Unlock()
					pc.conn.Close()
					return nil, ErrPoolClosed
				}
				p.mu.Unlock()
				return pc.conn, nil
			}

			p.mu.Lock()
			p.freeSlotLocked()
			p.mu.Unlock()
			pc.conn.Close()
			continue
		}

		// No available connections - try to create a new one.
		if p.config.MaxSize == 0 || p.numOpen < p.config.MaxSize {
			p.numOpen++ // reserve a capacity slot for the dial
			p.mu.Unlock()

			conn, err := p.dial(ctx)
			if err != nil {
				// dial released the reserved slot (or transferred ownership
				// to a background goroutine on context cancellation).
				return nil, err
			}

			p.mu.Lock()
			if p.closed {
				p.numOpen--
				p.mu.Unlock()
				conn.Close()
				return nil, ErrPoolClosed
			}
			p.mu.Unlock()
			return conn, nil
		}

		// Pool is at max capacity - must wait for capacity or a handoff.
		waiter := &poolWaiter{result: make(chan poolWaitResult, 1)}
		p.waitQueue = append(p.waitQueue, waiter)
		p.mu.Unlock()

		conn, retry, err := p.awaitConn(ctx, waiter)
		if err != nil {
			return nil, err
		}
		if retry {
			continue
		}
		return conn, nil
	}
}

// awaitConn blocks until the waiter receives a handoff, an error, a retry
// signal (retry == true: a capacity slot was freed, loop and re-acquire), or
// the wait times out.
func (p *ConnPool) awaitConn(ctx context.Context, waiter *poolWaiter) (conn Conn, retry bool, err error) {
	waitCtx := ctx
	if p.config.WaitTimeout > 0 {
		var cancel context.CancelFunc
		waitCtx, cancel = context.WithTimeout(ctx, p.config.WaitTimeout)
		defer cancel()
	}

	select {
	case result := <-waiter.result:
		if result.err != nil {
			return nil, false, result.err
		}
		if result.conn == nil {
			return nil, true, nil // retry signal: capacity was freed
		}
		p.mu.Lock()
		if p.closed {
			// Close raced with the handoff; the pool no longer tracks this
			// connection, so account for it and discard it.
			p.numOpen--
			p.mu.Unlock()
			result.conn.Close()
			return nil, false, ErrPoolClosed
		}
		p.mu.Unlock()
		return result.conn, false, nil

	case <-waitCtx.Done():
		p.removeWaiter(waiter)
		// A sender may have claimed this waiter before we removed it; the
		// result is then already buffered (pop+send share one critical
		// section), so a non-blocking drain is race-free.
		select {
		case result := <-waiter.result:
			switch {
			case result.conn != nil:
				p.Put(result.conn)
			case result.err == nil:
				// A retry signal was addressed to us; forward the freed slot
				// to the next waiter so it is not lost.
				p.mu.Lock()
				p.wakeWaiterLocked()
				p.mu.Unlock()
			}
		default:
		}

		if ctx.Err() != nil {
			return nil, false, ctx.Err()
		}
		return nil, false, ErrPoolTimeout
	}
}

// dial creates a new connection via the factory while honoring ctx. The
// caller must have reserved a capacity slot (numOpen++). On factory error the
// slot is released here. On context cancellation, ownership of the slot moves
// to a background goroutine that either returns the late connection to the
// pool or releases the slot if the factory failed.
func (p *ConnPool) dial(ctx context.Context) (Conn, error) {
	type dialResult struct {
		conn Conn
		err  error
	}
	ch := make(chan dialResult, 1)
	go func() {
		conn, err := p.connFactory()
		ch <- dialResult{conn: conn, err: err}
	}()

	select {
	case r := <-ch:
		if r.err != nil {
			p.mu.Lock()
			p.freeSlotLocked()
			p.mu.Unlock()
			return nil, fmt.Errorf("failed to create connection: %w", r.err)
		}
		return r.conn, nil
	case <-ctx.Done():
		go func() {
			r := <-ch
			if r.err != nil {
				p.mu.Lock()
				p.freeSlotLocked()
				p.mu.Unlock()
				return
			}
			p.Put(r.conn)
		}()
		return nil, ctx.Err()
	}
}

// freeSlotLocked releases a capacity slot and wakes one queued waiter so the
// freed capacity is not lost. Caller must hold p.mu.
func (p *ConnPool) freeSlotLocked() {
	p.numOpen--
	p.wakeWaiterLocked()
}

// wakeWaiterLocked pops one waiter (if any) and signals it to retry.
// Caller must hold p.mu; the buffered, single-sender channel makes the send
// non-blocking.
func (p *ConnPool) wakeWaiterLocked() {
	if w := p.popWaiterLocked(); w != nil {
		w.result <- poolWaitResult{}
	}
}

// popWaiterLocked removes and returns the next queued waiter, or nil.
// Caller must hold p.mu.
func (p *ConnPool) popWaiterLocked() *poolWaiter {
	if len(p.waitQueue) == 0 {
		return nil
	}
	w := p.waitQueue[0]
	p.waitQueue = p.waitQueue[1:]
	return w
}

// Put returns a connection to the pool.
// If the connection is no longer open, it is discarded instead of being returned to the pool.
func (p *ConnPool) Put(conn Conn) {
	if conn == nil {
		return
	}

	// Health-check outside the pool mutex so one slow FFI call does not
	// block unrelated Get/Put/Stats operations.
	healthy := conn.IsOpen()

	p.mu.Lock()

	if p.closed {
		p.numOpen--
		p.mu.Unlock()
		conn.Close()
		return
	}

	// If the connection is dead, discard it and wake a waiter: the freed
	// capacity slot lets it dial a fresh connection.
	if !healthy {
		p.freeSlotLocked()
		p.mu.Unlock()
		conn.Close()
		return
	}

	// Hand off to a waiting goroutine if there is one. The send must happen
	// in the same critical section as the pop (see poolWaiter).
	if w := p.popWaiterLocked(); w != nil {
		w.result <- poolWaitResult{conn: conn}
		p.mu.Unlock()
		return
	}

	// Return connection to pool
	p.conns = append(p.conns, pooledConn{conn: conn, idleSince: time.Now()})
	p.mu.Unlock()
}

// Close closes all connections in the pool and prevents new connections from being acquired.
func (p *ConnPool) Close() {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return
	}
	p.closed = true

	// Stop the idle connection cleaner
	if p.cleanerStarted {
		close(p.stopCleaner)
	}

	// Take ownership of the idle connections; they are closed after the
	// lock is released so slow FFI closes cannot stall other pool users.
	idle := p.conns
	p.conns = nil
	p.numOpen -= len(idle)

	// Notify waiting goroutines. Buffered single-sender channels make these
	// sends non-blocking (see poolWaiter).
	for _, waiter := range p.waitQueue {
		waiter.result <- poolWaitResult{err: ErrPoolClosed}
	}
	p.waitQueue = nil

	p.mu.Unlock()

	for _, pc := range idle {
		pc.conn.Close()
	}

	// Wait for cleaner to stop
	if p.cleanerStarted {
		<-p.cleanerDone
	}
}

func (p *ConnPool) removeWaiter(waiter *poolWaiter) {
	p.mu.Lock()
	defer p.mu.Unlock()

	for i, w := range p.waitQueue {
		if w == waiter {
			p.waitQueue = append(p.waitQueue[:i], p.waitQueue[i+1:]...)
			return
		}
	}
}

// Stats returns current pool statistics.
func (p *ConnPool) Stats() PoolStats {
	p.mu.Lock()
	defer p.mu.Unlock()

	return PoolStats{
		Available: len(p.conns),
		InUse:     p.numOpen - len(p.conns),
		Total:     p.numOpen,
		Waiting:   len(p.waitQueue),
	}
}

// PoolStats provides statistics about the connection pool.
type PoolStats struct {
	Available int // connections available in the pool
	InUse     int // connections currently in use
	Total     int // total open connections
	Waiting   int // goroutines waiting for a connection
}

// cleanIdleConnections runs in a background goroutine to close idle connections.
func (p *ConnPool) cleanIdleConnections() {
	defer close(p.cleanerDone)

	ticker := time.NewTicker(p.config.IdleTimeout / 2)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			p.mu.Lock()

			now := time.Now()
			keepConns := make([]pooledConn, 0, len(p.conns))
			var expired []Conn

			for _, pc := range p.conns {
				// Keep connections that are still within idle timeout or needed for MinSize
				if now.Sub(pc.idleSince) < p.config.IdleTimeout || len(keepConns) < p.config.MinSize {
					keepConns = append(keepConns, pc)
				} else {
					// Reap the idle connection; the actual Close happens
					// after the lock is released.
					expired = append(expired, pc.conn)
					p.freeSlotLocked()
				}
			}

			p.conns = keepConns
			p.mu.Unlock()

			for _, c := range expired {
				c.Close()
			}

		case <-p.stopCleaner:
			return
		}
	}
}

// NewDatabaseWithPool creates a Database that uses a connection pool for concurrent access.
// The pool is created with the given configuration and pre-warmed with MinSize connections.
// The Database takes ownership of the pool and will close it when Database.Close() is called.
func NewDatabaseWithPool(config PoolConfig, dbName string, factory func() (Conn, error)) (*Database, error) {
	pool, err := NewConnPool(config, factory)
	if err != nil {
		return nil, fmt.Errorf("failed to create connection pool: %w", err)
	}

	poolConn := &poolConnAdapter{pool: pool, dbName: dbName}

	return &Database{
		conn:    poolConn,
		dbName:  dbName,
		ownConn: true,
	}, nil
}

// poolConnAdapter adapts a ConnPool to the Conn interface.
// It acquires connections from the pool for each operation and returns them immediately.
type poolConnAdapter struct {
	pool   *ConnPool
	dbName string
}

// Transaction opens a transaction using a connection from the pool.
// The transaction holds the connection until Close/Commit/Rollback is called.
func (pca *poolConnAdapter) Transaction(dbName string, txType int) (Tx, error) {
	return pca.TransactionContext(context.Background(), dbName, txType)
}

// TransactionContext opens a transaction using a connection from the pool
// while honoring caller cancellation during pool acquisition.
func (pca *poolConnAdapter) TransactionContext(ctx context.Context, dbName string, txType int) (Tx, error) {
	conn, err := pca.pool.Get(ctx)
	if err != nil {
		return nil, fmt.Errorf("get connection from pool: %w", err)
	}

	tx, err := conn.Transaction(dbName, txType)
	if err != nil {
		pca.pool.Put(conn) // return connection to pool on error
		return nil, err
	}

	// Wrap the transaction to return connection to pool on close
	return &pooledTx{tx: tx, conn: conn, pool: pca.pool}, nil
}

// acquireCtx returns the context used for pool acquisition in the adapter's
// admin operations. The Conn interface has no context parameters for these,
// so the pool's WaitTimeout bounds the whole acquisition (queue wait and
// dial) instead of leaving it uncancellable; WaitTimeout == 0 preserves the
// documented "no timeout" behavior.
func (pca *poolConnAdapter) acquireCtx() (context.Context, context.CancelFunc) {
	if t := pca.pool.config.WaitTimeout; t > 0 {
		return context.WithTimeout(context.Background(), t)
	}
	return context.WithCancel(context.Background())
}

// Schema retrieves the schema using a connection from the pool.
func (pca *poolConnAdapter) Schema(dbName string) (string, error) {
	ctx, cancel := pca.acquireCtx()
	defer cancel()
	conn, err := pca.pool.Get(ctx)
	if err != nil {
		return "", fmt.Errorf("get connection from pool: %w", err)
	}
	defer pca.pool.Put(conn)

	return conn.Schema(dbName)
}

// DatabaseCreate creates a database using a connection from the pool.
func (pca *poolConnAdapter) DatabaseCreate(name string) error {
	ctx, cancel := pca.acquireCtx()
	defer cancel()
	conn, err := pca.pool.Get(ctx)
	if err != nil {
		return fmt.Errorf("get connection from pool: %w", err)
	}
	defer pca.pool.Put(conn)
	return conn.DatabaseCreate(name)
}

// DatabaseDelete deletes a database using a connection from the pool.
func (pca *poolConnAdapter) DatabaseDelete(name string) error {
	ctx, cancel := pca.acquireCtx()
	defer cancel()
	conn, err := pca.pool.Get(ctx)
	if err != nil {
		return fmt.Errorf("get connection from pool: %w", err)
	}
	defer pca.pool.Put(conn)
	return conn.DatabaseDelete(name)
}

// DatabaseContains checks database existence using a connection from the pool.
func (pca *poolConnAdapter) DatabaseContains(name string) (bool, error) {
	ctx, cancel := pca.acquireCtx()
	defer cancel()
	conn, err := pca.pool.Get(ctx)
	if err != nil {
		return false, fmt.Errorf("get connection from pool: %w", err)
	}
	defer pca.pool.Put(conn)
	return conn.DatabaseContains(name)
}

// DatabaseAll lists all databases using a connection from the pool.
func (pca *poolConnAdapter) DatabaseAll() ([]string, error) {
	ctx, cancel := pca.acquireCtx()
	defer cancel()
	conn, err := pca.pool.Get(ctx)
	if err != nil {
		return nil, fmt.Errorf("get connection from pool: %w", err)
	}
	defer pca.pool.Put(conn)
	return conn.DatabaseAll()
}

// Close closes the connection pool.
func (pca *poolConnAdapter) Close() {
	pca.pool.Close()
}

// IsOpen returns true if the pool is not closed.
func (pca *poolConnAdapter) IsOpen() bool {
	pca.pool.mu.Lock()
	defer pca.pool.mu.Unlock()
	return !pca.pool.closed
}

// pooledTx wraps a transaction and returns its connection to the pool when closed.
type pooledTx struct {
	tx   Tx
	conn Conn
	pool *ConnPool
	once sync.Once
}

func (pt *pooledTx) Query(query string) ([]map[string]any, error) {
	return pt.tx.Query(query)
}

func (pt *pooledTx) QueryWithContext(ctx context.Context, query string) ([]map[string]any, error) {
	return pt.tx.QueryWithContext(ctx, query)
}

func (pt *pooledTx) Commit() error {
	err := pt.tx.Commit()
	pt.once.Do(func() { pt.pool.Put(pt.conn) })
	return err
}

func (pt *pooledTx) Rollback() error {
	err := pt.tx.Rollback()
	pt.once.Do(func() { pt.pool.Put(pt.conn) })
	return err
}

func (pt *pooledTx) Close() {
	pt.CloseAsync(nil)
}

func (pt *pooledTx) CloseAsync(onDone func(error)) {
	if tx, ok := pt.tx.(interface{ CloseAsync(func(error)) }); ok {
		tx.CloseAsync(func(err error) {
			if onDone != nil {
				onDone(err)
			}
		})
		pt.once.Do(func() { pt.pool.Put(pt.conn) })
		return
	}

	pt.tx.Close()
	pt.once.Do(func() { pt.pool.Put(pt.conn) })
	if onDone != nil {
		onDone(nil)
	}
}

func (pt *pooledTx) CloseChecked() error {
	var err error
	if tx, ok := pt.tx.(interface{ CloseChecked() error }); ok {
		err = tx.CloseChecked()
	} else {
		pt.tx.Close()
	}
	pt.once.Do(func() { pt.pool.Put(pt.conn) })
	return err
}

func (pt *pooledTx) IsOpen() bool {
	return pt.tx.IsOpen()
}
