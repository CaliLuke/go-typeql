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
type ConnPool struct {
	config      PoolConfig
	connFactory func() (Conn, error) // factory function to create new connections

	mu          sync.Mutex
	conns       []pooledConn // available connections
	numOpen     int          // total open connections (available + in-use)
	waitQueue   []chan Conn  // waiting goroutines
	closed      bool

	stopCleaner chan struct{} // signal to stop the idle connection cleaner
	cleanerDone chan struct{} // signal that cleaner has stopped
}

// pooledConn tracks a connection and its idle time.
type pooledConn struct {
	conn     Conn
	idleSince time.Time
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
		waitQueue:   make([]chan Conn, 0),
		stopCleaner: make(chan struct{}),
		cleanerDone: make(chan struct{}),
	}

	// Pre-warm the pool with MinSize connections
	if config.MinSize > 0 {
		for i := 0; i < config.MinSize; i++ {
			conn, err := factory()
			if err != nil {
				// Close any connections created so far
				pool.Close()
				return nil, fmt.Errorf("failed to create initial connection %d/%d: %w", i+1, config.MinSize, err)
			}
			pool.conns = append(pool.conns, pooledConn{conn: conn, idleSince: time.Now()})
			pool.numOpen++
		}
	}

	// Start the idle connection cleaner if idle timeout is configured
	if config.IdleTimeout > 0 {
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

	p.mu.Lock()

	if p.closed {
		p.mu.Unlock()
		return nil, ErrPoolClosed
	}

	// Try to get an available connection
	for len(p.conns) > 0 {
		pc := p.conns[len(p.conns)-1]
		p.conns = p.conns[:len(p.conns)-1]

		// Validate connection before returning
		if pc.conn.IsOpen() {
			p.mu.Unlock()
			return pc.conn, nil
		}

		// Connection is dead, close it and try next one
		pc.conn.Close()
		p.numOpen--
	}

	// No available connections - try to create a new one
	if p.config.MaxSize == 0 || p.numOpen < p.config.MaxSize {
		p.numOpen++
		p.mu.Unlock()

		conn, err := p.connFactory()
		if err != nil {
			p.mu.Lock()
			p.numOpen--
			p.mu.Unlock()
			return nil, fmt.Errorf("failed to create connection: %w", err)
		}
		return conn, nil
	}

	// Pool is at max capacity - must wait for a connection
	waiter := make(chan Conn, 1)
	p.waitQueue = append(p.waitQueue, waiter)
	p.mu.Unlock()

	// Determine wait timeout
	waitCtx := ctx
	if p.config.WaitTimeout > 0 {
		var cancel context.CancelFunc
		waitCtx, cancel = context.WithTimeout(ctx, p.config.WaitTimeout)
		defer cancel()
	}

	select {
	case conn := <-waiter:
		return conn, nil
	case <-waitCtx.Done():
		// Remove ourselves from wait queue
		p.mu.Lock()
		for i, w := range p.waitQueue {
			if w == waiter {
				p.waitQueue = append(p.waitQueue[:i], p.waitQueue[i+1:]...)
				break
			}
		}
		p.mu.Unlock()

		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, ErrPoolTimeout
	}
}

// Put returns a connection to the pool.
// If the connection is no longer open, it is discarded instead of being returned to the pool.
func (p *ConnPool) Put(conn Conn) {
	if conn == nil {
		return
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	if p.closed {
		conn.Close()
		return
	}

	// If connection is dead, discard it
	if !conn.IsOpen() {
		conn.Close()
		p.numOpen--
		return
	}

	// Try to satisfy a waiting goroutine first
	if len(p.waitQueue) > 0 {
		waiter := p.waitQueue[0]
		p.waitQueue = p.waitQueue[1:]
		select {
		case waiter <- conn:
			// Successfully handed off to waiter
			return
		default:
			// Waiter timed out or cancelled, fall through to return to pool
		}
	}

	// Return connection to pool
	p.conns = append(p.conns, pooledConn{conn: conn, idleSince: time.Now()})
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
	if p.config.IdleTimeout > 0 {
		close(p.stopCleaner)
	}

	// Close all available connections
	for _, pc := range p.conns {
		pc.conn.Close()
	}
	p.conns = nil

	// Notify waiting goroutines
	for _, waiter := range p.waitQueue {
		close(waiter)
	}
	p.waitQueue = nil

	p.mu.Unlock()

	// Wait for cleaner to stop
	if p.config.IdleTimeout > 0 {
		<-p.cleanerDone
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

			for _, pc := range p.conns {
				// Keep connections that are still within idle timeout or needed for MinSize
				if now.Sub(pc.idleSince) < p.config.IdleTimeout || len(keepConns) < p.config.MinSize {
					keepConns = append(keepConns, pc)
				} else {
					// Close idle connection
					pc.conn.Close()
					p.numOpen--
				}
			}

			p.conns = keepConns
			p.mu.Unlock()

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
	conn, err := pca.pool.Get(context.Background())
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

// Schema retrieves the schema using a connection from the pool.
func (pca *poolConnAdapter) Schema(dbName string) (string, error) {
	conn, err := pca.pool.Get(context.Background())
	if err != nil {
		return "", fmt.Errorf("get connection from pool: %w", err)
	}
	defer pca.pool.Put(conn)

	return conn.Schema(dbName)
}

// DatabaseCreate creates a database using a connection from the pool.
func (pca *poolConnAdapter) DatabaseCreate(name string) error {
	conn, err := pca.pool.Get(context.Background())
	if err != nil {
		return fmt.Errorf("get connection from pool: %w", err)
	}
	defer pca.pool.Put(conn)
	return conn.DatabaseCreate(name)
}

// DatabaseDelete deletes a database using a connection from the pool.
func (pca *poolConnAdapter) DatabaseDelete(name string) error {
	conn, err := pca.pool.Get(context.Background())
	if err != nil {
		return fmt.Errorf("get connection from pool: %w", err)
	}
	defer pca.pool.Put(conn)
	return conn.DatabaseDelete(name)
}

// DatabaseContains checks database existence using a connection from the pool.
func (pca *poolConnAdapter) DatabaseContains(name string) (bool, error) {
	conn, err := pca.pool.Get(context.Background())
	if err != nil {
		return false, fmt.Errorf("get connection from pool: %w", err)
	}
	defer pca.pool.Put(conn)
	return conn.DatabaseContains(name)
}

// DatabaseAll lists all databases using a connection from the pool.
func (pca *poolConnAdapter) DatabaseAll() ([]string, error) {
	conn, err := pca.pool.Get(context.Background())
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
	pt.tx.Close()
	pt.once.Do(func() { pt.pool.Put(pt.conn) })
}

func (pt *pooledTx) IsOpen() bool {
	return pt.tx.IsOpen()
}
