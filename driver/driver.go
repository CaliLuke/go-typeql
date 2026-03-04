//go:build cgo && typedb

// Package driver provides Go bindings to the TypeDB Rust driver via CGo FFI.
//
// This package wraps a thin C FFI layer (driver/rust/) that itself wraps the
// official typedb-driver Rust crate. Query results cross the FFI boundary as
// JSON strings, avoiding complex C struct marshalling.
package driver

// #include "typedb_ffi.h"
import "C"
import (
	"sync"
	"time"
	"unsafe"
)

// TransactionType specifies the intended mode of operation for a transaction.
type TransactionType int32

const (
	// Read transactions are for data retrieval only.
	Read TransactionType = 0
	// Write transactions allow for data modification (insert, delete, update).
	Write TransactionType = 1
	// Schema transactions are for modifying the database schema (define, undefine).
	Schema TransactionType = 2
)

var loggingInitOnce sync.Once

func ensureLoggingInitialized() {
	loggingInitOnce.Do(func() {
		C.typedb_init_logging()
	})
}

// Driver represents an active connection to a TypeDB server.
// It is used to open transactions and manage databases.
type Driver struct {
	ptr unsafe.Pointer
	mu  sync.Mutex
}

// Open creates a new connection to a TypeDB server at the specified address.
// It uses the provided username and password for authentication.
func Open(address, username, password string) (*Driver, error) {
	return OpenWithTLS(address, username, password, false, "")
}

// OpenWithTLS creates a new connection to a TypeDB server with optional TLS configuration.
// If tlsEnabled is true, it establishes an encrypted connection.
// tlsRootCA can optionally specify a path to a custom root certificate authority.
func OpenWithTLS(address, username, password string, tlsEnabled bool, tlsRootCA string) (*Driver, error) {
	ensureLoggingInitialized()
	start := time.Now()
	logFFIDebug("driver.open.start", "address", address, "tls_enabled", tlsEnabled, "has_tls_ca", tlsRootCA != "")

	cAddr := C.CString(address)
	defer C.free(unsafe.Pointer(cAddr))

	cUser := C.CString(username)
	defer C.free(unsafe.Pointer(cUser))

	cPass := C.CString(password)
	defer C.free(unsafe.Pointer(cPass))

	creds := C.typedb_credentials_new(cUser, cPass)
	if creds == nil {
		logFFIDuration("driver.open", start, "address", address, "result", "error", "error_type", "nil_credentials")
		return nil, ErrNilPointer
	}
	defer C.typedb_credentials_drop(creds)

	var cCA *C.char
	if tlsRootCA != "" {
		cCA = C.CString(tlsRootCA)
		defer C.free(unsafe.Pointer(cCA))
	}

	var optsErr *C.char
	opts := C.typedb_driver_options_new(C.bool(tlsEnabled), cCA, &optsErr)
	if opts == nil {
		if err := getError(optsErr); err != nil {
			logFFIDuration("driver.open", start, "address", address, "result", "error", "error", err.Error())
			return nil, err
		}
		logFFIDuration("driver.open", start, "address", address, "result", "error", "error_type", "nil_driver_options")
		return nil, ErrNilPointer
	}
	defer C.typedb_driver_options_drop(opts)

	var openErr *C.char
	ptr := C.typedb_driver_open(cAddr, creds, opts, &openErr)
	if ptr == nil {
		if err := getError(openErr); err != nil {
			logFFIDuration("driver.open", start, "address", address, "result", "error", "error", err.Error())
			return nil, err
		}
		logFFIDuration("driver.open", start, "address", address, "result", "error", "error_type", "nil_driver_ptr")
		return nil, ErrNilPointer
	}

	logFFIDuration("driver.open", start, "address", address, "result", "ok")
	return &Driver{ptr: ptr}, nil
}

// IsOpen checks if the driver connection is still open.
func (d *Driver) IsOpen() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.ptr == nil {
		return false
	}
	return bool(C.typedb_driver_is_open(d.ptr))
}

// Close closes the driver connection and frees resources.
func (d *Driver) Close() {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.ptr != nil {
		start := time.Now()
		C.typedb_driver_close(d.ptr)
		d.ptr = nil
		logFFIDuration("driver.close", start, "result", "ok")
	}
}

// Transaction opens a new transaction with default options.
func (d *Driver) Transaction(databaseName string, txnType TransactionType) (*Transaction, error) {
	return d.TransactionWithOptions(databaseName, txnType, nil)
}

// TransactionWithOptions opens a new transaction with the given options.
func (d *Driver) TransactionWithOptions(databaseName string, txnType TransactionType, opts *TransactionOptions) (*Transaction, error) {
	start := time.Now()
	txID := nextTxID()
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.ptr == nil {
		logFFIDuration("tx.open", start, "tx_id", txID, "db", databaseName, "tx_type", int(txnType), "result", "error", "error", ErrNotConnected.Error())
		return nil, ErrNotConnected
	}

	cName := C.CString(databaseName)
	defer C.free(unsafe.Pointer(cName))

	var cOpts unsafe.Pointer
	if opts != nil {
		cOpts = opts.ptr
	}

	var txErr *C.char
	ptr := C.typedb_transaction_open(d.ptr, cName, C.int(txnType), cOpts, &txErr)
	if ptr == nil {
		if err := getError(txErr); err != nil {
			logFFIDuration("tx.open", start, "tx_id", txID, "db", databaseName, "tx_type", int(txnType), "result", "error", "error", err.Error())
			return nil, err
		}
		logFFIDuration("tx.open", start, "tx_id", txID, "db", databaseName, "tx_type", int(txnType), "result", "error", "error_type", "nil_tx_ptr")
		return nil, ErrNilPointer
	}

	logFFIDuration("tx.open", start, "tx_id", txID, "db", databaseName, "tx_type", int(txnType), "result", "ok")
	return newTransaction(ptr, txID, databaseName, txnType), nil
}

// Databases returns a DatabaseManager for this connection.
func (d *Driver) Databases() *DatabaseManager {
	return &DatabaseManager{driver: d}
}
