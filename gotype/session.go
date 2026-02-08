// Package gotype provides a high-level, struct-tag based ORM layer for TypeDB.
// It maps Go structs to TypeDB entities and relations, providing generic CRUD operations
// and automatic query generation.
package gotype

import (
	"context"
	"fmt"
	"log"
	"runtime"
)

// TransactionType represents the intended mode of operation for a TypeDB transaction.
type TransactionType int

const (
	// ReadTransaction is for data retrieval only.
	ReadTransaction TransactionType = 0
	// WriteTransaction allows for data modification.
	WriteTransaction TransactionType = 1
	// SchemaTransaction is for modifying the database schema.
	SchemaTransaction TransactionType = 2
)

// Tx is the interface for a TypeDB transaction, allowing for query execution and lifecycle management.
type Tx interface {
	// Query executes a TypeQL query and returns the results.
	Query(query string) ([]map[string]any, error)
	// QueryWithContext executes a TypeQL query with context cancellation support.
	QueryWithContext(ctx context.Context, query string) ([]map[string]any, error)
	// Commit persists changes made in the transaction.
	Commit() error
	// Rollback discards changes made in the transaction.
	Rollback() error
	// Close releases resources associated with the transaction.
	Close()
	// IsOpen returns true if the transaction is active.
	IsOpen() bool
}

// Conn is the interface for a TypeDB connection.
type Conn interface {
	// Transaction opens a new transaction on the specified database.
	Transaction(dbName string, txType int) (Tx, error)
	// Schema returns the current TypeQL schema definition for the named database.
	Schema(dbName string) (string, error)
	// DatabaseCreate creates a new database with the given name.
	DatabaseCreate(name string) error
	// DatabaseDelete permanently removes the named database.
	DatabaseDelete(name string) error
	// DatabaseContains returns true if the named database exists.
	DatabaseContains(name string) (bool, error)
	// DatabaseAll returns the names of all databases on the server.
	DatabaseAll() ([]string, error)
	// Close terminates the connection.
	Close()
	// IsOpen returns true if the connection is active.
	IsOpen() bool
}

// EnsureDatabase checks whether a database exists and creates it if not.
// Returns true if the database was newly created, false if it already existed.
func EnsureDatabase(ctx context.Context, conn Conn, name string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, fmt.Errorf("ensure database: context cancelled: %w", err)
	}
	exists, err := conn.DatabaseContains(name)
	if err != nil {
		return false, fmt.Errorf("ensure database: check existence: %w", err)
	}
	if exists {
		return false, nil
	}
	if err := conn.DatabaseCreate(name); err != nil {
		return false, fmt.Errorf("ensure database: create: %w", err)
	}
	return true, nil
}

// Database represents a high-level handle to a specific TypeDB database,
// providing convenient methods for transaction management and query execution.
type Database struct {
	conn    Conn
	dbName  string
	ownConn bool
}

// NewDatabase creates a new Database handle bound to a specific database name.
func NewDatabase(conn Conn, dbName string) *Database {
	return &Database{conn: conn, dbName: dbName}
}

// Close closes the underlying connection if it is owned by this Database handle.
func (db *Database) Close() {
	if db.ownConn && db.conn != nil {
		db.conn.Close()
	}
}

// Name returns the name of the database.
func (db *Database) Name() string {
	return db.dbName
}

// GetConn returns the underlying Conn implementation.
func (db *Database) GetConn() Conn {
	return db.conn
}

// Schema returns the current TypeQL schema definition for this database.
func (db *Database) Schema(ctx context.Context) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", fmt.Errorf("schema: context cancelled: %w", err)
	}
	return db.conn.Schema(db.dbName)
}

// Transaction opens a new transaction of the specified type.
func (db *Database) Transaction(txType TransactionType) (Tx, error) {
	return db.conn.Transaction(db.dbName, int(txType))
}

// ExecuteWrite executes a query in a new write transaction and commits it.
func (db *Database) ExecuteWrite(ctx context.Context, query string) ([]map[string]any, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("write: context cancelled: %w", err)
	}
	tx, err := db.Transaction(WriteTransaction)
	if err != nil {
		return nil, fmt.Errorf("open write transaction: %w", err)
	}
	defer tx.Close()

	results, err := tx.QueryWithContext(ctx, query)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit: %w", err)
	}
	return results, nil
}

// ExecuteRead executes a query in a new read transaction.
func (db *Database) ExecuteRead(ctx context.Context, query string) ([]map[string]any, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("read: context cancelled: %w", err)
	}
	tx, err := db.Transaction(ReadTransaction)
	if err != nil {
		return nil, fmt.Errorf("open read transaction: %w", err)
	}
	defer tx.Close()
	return tx.QueryWithContext(ctx, query)
}

// TransactionContext provides a scoped transaction that can be explicitly managed
// and shared across multiple Manager operations.
type TransactionContext struct {
	db     *Database
	tx     Tx
	txType TransactionType
	closed bool
}

// Begin starts a new TransactionContext.
// The caller must call Close() when done. A finalizer will log a warning
// if the transaction is garbage-collected without being closed.
func (db *Database) Begin(txType TransactionType) (*TransactionContext, error) {
	tx, err := db.Transaction(txType)
	if err != nil {
		return nil, fmt.Errorf("begin transaction: %w", err)
	}
	tc := &TransactionContext{db: db, tx: tx, txType: txType}
	runtime.SetFinalizer(tc, func(tc *TransactionContext) {
		if !tc.closed {
			log.Printf("WARNING: TransactionContext on %q was garbage-collected without being closed (possible transaction leak)", db.dbName)
		}
	})
	return tc, nil
}

// Commit persists changes in the scoped transaction.
func (tc *TransactionContext) Commit() error {
	tc.closed = true
	return tc.tx.Commit()
}

// Rollback discards changes in the scoped transaction.
func (tc *TransactionContext) Rollback() error {
	tc.closed = true
	return tc.tx.Rollback()
}

// Close releases resources associated with the scoped transaction.
func (tc *TransactionContext) Close() {
	tc.closed = true
	tc.tx.Close()
}

// Tx returns the underlying Tx for direct query execution.
func (tc *TransactionContext) Tx() Tx {
	return tc.tx
}

// ExecuteSchema executes a schema modification query in a schema transaction.
func (db *Database) ExecuteSchema(ctx context.Context, query string) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("schema: context cancelled: %w", err)
	}
	tx, err := db.Transaction(SchemaTransaction)
	if err != nil {
		return fmt.Errorf("open schema transaction: %w", err)
	}
	defer tx.Close()

	_, err = tx.QueryWithContext(ctx, query)
	if err != nil {
		return err
	}
	return tx.Commit()
}
