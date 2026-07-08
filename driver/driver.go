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
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
	"unsafe"
	"weak"
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

// ensureLoggingInitialized installs the Rust driver's tracing subscriber once
// per process. Log verbosity is controlled by the TYPEDB_GO_RUST_LOG
// environment variable (falling back to RUST_LOG); when neither is set,
// Rust-side logging stays off.
func ensureLoggingInitialized() {
	loggingInitOnce.Do(func() {
		C.typedb_init_logging()
	})
}

// Driver represents an active connection to a TypeDB server.
// It is used to open transactions and manage databases.
//
// The underlying Rust driver handle is internally thread-safe, so driver-level
// operations (opening transactions, database management, ServerVersion) run
// concurrently; only Close serializes against them while it frees the handle.
type Driver struct {
	ptr unsafe.Pointer
	// mu guards the ptr lifecycle: RLock for FFI calls on the handle, Lock
	// only while Close drops it.
	mu          sync.RWMutex
	closeWorker *transactionCloseWorker
	txMu        sync.Mutex
	// txs tracks locally opened transactions by id. Entries are weak so an
	// abandoned *Transaction can still be garbage-collected, letting its
	// finalizer free the native handle; Close and the per-database helpers
	// prune entries whose transaction has been collected.
	txs map[uint64]weak.Pointer[Transaction]
}

// DriverOptions configures connection-level TypeDB driver behavior.
//
// Zero-valued fields keep the underlying TypeDB driver's defaults. These
// options apply to driver-level operations such as connection setup, database
// management, and transaction opening; query execution still uses QueryOptions.
type DriverOptions struct {
	// TLSEnabled controls whether the driver connects with TLS.
	TLSEnabled bool
	// TLSRootCA optionally points to a custom root CA certificate when TLS is enabled.
	TLSRootCA string
	// RequestTimeoutMillis bounds unary RPCs such as database management and transaction open.
	// Zero keeps the TypeDB driver's default.
	RequestTimeoutMillis int64
	// PrimaryFailoverRetries controls retries when finding or re-routing to a primary server.
	// Zero keeps the TypeDB driver's default.
	PrimaryFailoverRetries int
	// AddressTranslation optionally maps public (dialed) addresses to the
	// private addresses the TypeDB server advertises, for setups such as
	// container port mappings where the two differ. When set on a call to
	// Open/OpenWithTLS/OpenWithOptions, the map must contain an entry for the
	// dialed address. Addresses are used exactly as given; the driver never
	// rewrites them implicitly.
	AddressTranslation map[string]string
}

// ServerVersion is the TypeDB server version reported by the connected server.
type ServerVersion struct {
	// Distribution is the server distribution name, such as "TypeDB CE".
	Distribution string `json:"distribution"`
	// Version is the server version string, such as "3.11.0-rc1".
	Version string `json:"version"`
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
	return OpenWithOptions(address, username, password, DriverOptions{
		TLSEnabled: tlsEnabled,
		TLSRootCA:  tlsRootCA,
	})
}

// OpenWithOptions creates a new connection to a TypeDB server with connection-level options.
//
// The address is dialed exactly as given. If the server advertises a private
// address that differs from the dialed one (clusters, container port mappings
// such as the repo compose setup on localhost:1730), configure the mapping
// explicitly via DriverOptions.AddressTranslation or
// OpenWithAddressTranslation. For local test setups, setting the
// TYPEDB_GO_COMPOSE_PORT_MAP environment variable to "1" maps any dialed
// localhost/127.0.0.1 address with a non-1729 port to the advertised
// 127.0.0.1:1729.
func OpenWithOptions(address, username, password string, opts DriverOptions) (*Driver, error) {
	if translation, err := resolveAddressTranslation(address, opts); err != nil {
		return nil, err
	} else if translation != nil {
		sub := opts
		sub.AddressTranslation = nil
		return OpenWithAddressTranslation(translation, username, password, sub)
	}

	ensureLoggingInitialized()
	start := time.Now()
	logFFIDebug("driver.open.start", "address", address, "tls_enabled", opts.TLSEnabled, "has_tls_ca", opts.TLSRootCA != "")

	creds, driverOpts, cleanup, err := openInputs(username, password, opts)
	if err != nil {
		logFFIDuration("driver.open", start, "address", address, "result", "error", "error", err.Error())
		return nil, err
	}
	defer cleanup()

	cAddr := C.CString(address)
	defer C.free(unsafe.Pointer(cAddr))

	var openErr *C.char
	ptr := C.typedb_driver_open(cAddr, creds, driverOpts, &openErr)
	if ptr == nil {
		if err := getError(openErr); err != nil {
			logFFIDuration("driver.open", start, "address", address, "result", "error", "error", err.Error())
			return nil, err
		}
		logFFIDuration("driver.open", start, "address", address, "result", "error", "error_type", "nil_driver_ptr")
		return nil, ErrNilPointer
	}

	logFFIDuration("driver.open", start, "address", address, "result", "ok")
	return newDriver(ptr), nil
}

// OpenWithAddresses creates a new connection using one or more public TypeDB addresses.
//
// Use this for clustered or routed TypeDB deployments where several public
// server addresses are available. For a single address, this behaves like
// OpenWithOptions.
func OpenWithAddresses(addresses []string, username, password string, opts DriverOptions) (*Driver, error) {
	return openWithAddressSet(addresses, nil, username, password, opts)
}

// OpenWithAddressTranslation creates a connection with public-to-private address translation.
//
// Keys are user-facing public addresses; values are private addresses advertised by TypeDB.
// This is useful for clusters, container port mappings, and network layouts
// where the address clients dial differs from the address the server reports.
func OpenWithAddressTranslation(addressTranslation map[string]string, username, password string, opts DriverOptions) (*Driver, error) {
	public := make([]string, 0, len(addressTranslation))
	for address := range addressTranslation {
		public = append(public, address)
	}
	sort.Strings(public)

	private := make([]string, len(public))
	for i, address := range public {
		private[i] = addressTranslation[address]
	}
	return openWithAddressSet(public, private, username, password, opts)
}

func openWithAddressSet(publicAddresses, privateAddresses []string, username, password string, opts DriverOptions) (*Driver, error) {
	ensureLoggingInitialized()
	start := time.Now()
	logFFIDebug("driver.open_addresses.start", "address_count", len(publicAddresses), "translated", privateAddresses != nil)

	if len(publicAddresses) == 0 {
		return nil, &DriverError{Message: "driver: at least one address is required"}
	}
	if privateAddresses != nil && len(privateAddresses) != len(publicAddresses) {
		return nil, &DriverError{Message: "driver: address translation must have the same number of public and private addresses"}
	}
	if privateAddresses == nil && len(publicAddresses) == 1 {
		return OpenWithOptions(publicAddresses[0], username, password, opts)
	}

	creds, driverOpts, cleanup, err := openInputs(username, password, opts)
	if err != nil {
		logFFIDuration("driver.open_addresses", start, "address_count", len(publicAddresses), "result", "error", "error", err.Error())
		return nil, err
	}
	defer cleanup()

	cPublic, cleanupPublic := cStringArray(publicAddresses)
	defer cleanupPublic()
	var publicPtr **C.char
	if len(cPublic) > 0 {
		publicPtr = &cPublic[0]
	}

	var privatePtr **C.char
	var cleanupPrivate func()
	if privateAddresses != nil {
		cPrivate, cleanup := cStringArray(privateAddresses)
		cleanupPrivate = cleanup
		if len(cPrivate) > 0 {
			privatePtr = &cPrivate[0]
		}
	} else {
		cleanupPrivate = func() {}
	}
	defer cleanupPrivate()

	var openErr *C.char
	ptr := C.typedb_driver_open_addresses(
		(**C.char)(unsafe.Pointer(publicPtr)),
		(**C.char)(unsafe.Pointer(privatePtr)),
		C.size_t(len(publicAddresses)),
		creds,
		driverOpts,
		&openErr,
	)
	if ptr == nil {
		if err := getError(openErr); err != nil {
			logFFIDuration("driver.open_addresses", start, "address_count", len(publicAddresses), "result", "error", "error", err.Error())
			return nil, err
		}
		logFFIDuration("driver.open_addresses", start, "address_count", len(publicAddresses), "result", "error", "error_type", "nil_driver_ptr")
		return nil, ErrNilPointer
	}

	logFFIDuration("driver.open_addresses", start, "address_count", len(publicAddresses), "result", "ok")
	return newDriver(ptr), nil
}

func newDriver(ptr unsafe.Pointer) *Driver {
	return &Driver{
		ptr:         ptr,
		closeWorker: newTransactionCloseWorker(),
		txs:         make(map[uint64]weak.Pointer[Transaction]),
	}
}

// resolveAddressTranslation decides whether a single-address open should use
// public-to-private address translation. It returns a non-nil translation map
// when DriverOptions.AddressTranslation is set, or when the
// TYPEDB_GO_COMPOSE_PORT_MAP test convenience applies. It returns an error
// when a configured translation map lacks the dialed address.
func resolveAddressTranslation(address string, opts DriverOptions) (map[string]string, error) {
	if len(opts.AddressTranslation) > 0 {
		if _, ok := opts.AddressTranslation[address]; !ok {
			return nil, &DriverError{Message: fmt.Sprintf("driver: AddressTranslation has no entry for dialed address %q", address)}
		}
		return opts.AddressTranslation, nil
	}
	if composePortMapEnabled() {
		if host, port, err := net.SplitHostPort(address); err == nil &&
			(host == "localhost" || host == "127.0.0.1") && port != "1729" {
			// Repo docker-compose convenience: the container advertises its
			// internal 127.0.0.1:1729 address while the host maps another port.
			return map[string]string{address: "127.0.0.1:1729"}, nil
		}
	}
	return nil, nil
}

// composePortMapEnabled reports whether the TYPEDB_GO_COMPOSE_PORT_MAP
// environment variable opts into the localhost compose port mapping. This is
// a test-setup convenience only; production code should use
// DriverOptions.AddressTranslation or OpenWithAddressTranslation.
func composePortMapEnabled() bool {
	value := strings.ToLower(strings.TrimSpace(os.Getenv("TYPEDB_GO_COMPOSE_PORT_MAP")))
	switch value {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

func openInputs(username, password string, opts DriverOptions) (unsafe.Pointer, unsafe.Pointer, func(), error) {
	cUser := C.CString(username)
	cPass := C.CString(password)

	var credsErr *C.char
	creds := C.typedb_credentials_new(cUser, cPass, &credsErr)
	if creds == nil {
		C.free(unsafe.Pointer(cUser))
		C.free(unsafe.Pointer(cPass))
		if err := getError(credsErr); err != nil {
			return nil, nil, nil, err
		}
		return nil, nil, nil, ErrNilPointer
	}

	var cCA *C.char
	if opts.TLSRootCA != "" {
		cCA = C.CString(opts.TLSRootCA)
	}

	var optsErr *C.char
	driverOpts := C.typedb_driver_options_new(C.bool(opts.TLSEnabled), cCA, &optsErr)
	if driverOpts == nil {
		C.typedb_credentials_drop(creds)
		C.free(unsafe.Pointer(cUser))
		C.free(unsafe.Pointer(cPass))
		if cCA != nil {
			C.free(unsafe.Pointer(cCA))
		}
		if err := getError(optsErr); err != nil {
			return nil, nil, nil, err
		}
		return nil, nil, nil, ErrNilPointer
	}

	if opts.RequestTimeoutMillis > 0 {
		C.typedb_driver_options_set_request_timeout(driverOpts, C.longlong(opts.RequestTimeoutMillis))
	}
	if opts.PrimaryFailoverRetries > 0 {
		C.typedb_driver_options_set_primary_failover_retries(driverOpts, C.size_t(opts.PrimaryFailoverRetries))
	}

	cleanup := func() {
		C.typedb_driver_options_drop(driverOpts)
		C.typedb_credentials_drop(creds)
		C.free(unsafe.Pointer(cUser))
		C.free(unsafe.Pointer(cPass))
		if cCA != nil {
			C.free(unsafe.Pointer(cCA))
		}
	}
	return unsafe.Pointer(creds), unsafe.Pointer(driverOpts), cleanup, nil
}

func cStringArray(values []string) ([]*C.char, func()) {
	cValues := make([]*C.char, len(values))
	for i, value := range values {
		cValues[i] = C.CString(value)
	}
	return cValues, func() {
		for _, value := range cValues {
			C.free(unsafe.Pointer(value))
		}
	}
}

// IsOpen checks if the driver connection is still open.
func (d *Driver) IsOpen() bool {
	d.mu.RLock()
	defer d.mu.RUnlock()
	if d.ptr == nil {
		return false
	}
	return bool(C.typedb_driver_is_open(d.ptr))
}

// ServerVersion returns the version reported by the connected TypeDB server.
//
// It is useful for startup diagnostics and for checking that the server is
// compatible with the linked TypeDB driver protocol.
func (d *Driver) ServerVersion() (ServerVersion, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	if d.ptr == nil {
		return ServerVersion{}, ErrNotConnected
	}

	var versionErr *C.char
	cJSON := C.typedb_driver_server_version(d.ptr, &versionErr)
	if cJSON == nil {
		if err := getError(versionErr); err != nil {
			return ServerVersion{}, err
		}
		return ServerVersion{}, nil
	}
	defer C.typedb_free_string(cJSON)

	var version ServerVersion
	if err := json.Unmarshal([]byte(C.GoString(cJSON)), &version); err != nil {
		return ServerVersion{}, &DriverError{Message: "failed to parse server version: " + err.Error()}
	}
	return version, nil
}

// Close closes the driver connection and frees resources.
//
// Transactions still open on this driver are closed first (their close jobs
// drain through the async close worker), so forgotten transactions do not
// leak native handles or server-side transactions. Close blocks behind
// in-flight FFI calls: a running query on an open, non-abandoned transaction
// must return before that transaction can be closed, and in-flight driver
// calls (transaction opens, database operations) must return before the
// driver handle is freed.
func (d *Driver) Close() {
	for _, tx := range d.openTransactions() {
		tx.Close()
	}

	d.mu.Lock()
	worker := d.closeWorker
	d.closeWorker = nil
	d.mu.Unlock()

	if worker != nil {
		worker.close()
	}

	d.mu.Lock()
	defer d.mu.Unlock()
	if d.ptr != nil {
		start := time.Now()
		C.typedb_driver_close(d.ptr)
		d.ptr = nil
		logFFIDuration("driver.close", start, "result", "ok")
	}
}

// HasOpenTransactions reports whether this driver has locally opened
// transactions for the named database that have not been committed, rolled
// back, or closed.
func (d *Driver) HasOpenTransactions(databaseName string) (bool, error) {
	d.mu.RLock()
	connected := d.ptr != nil
	d.mu.RUnlock()
	if !connected {
		return false, ErrNotConnected
	}

	return len(d.transactionsForDatabase(databaseName)) > 0, nil
}

// CloseDatabaseTransactions synchronously closes all transactions opened by
// this driver for the named database.
func (d *Driver) CloseDatabaseTransactions(ctx context.Context, databaseName string) error {
	if ctx == nil {
		ctx = context.Background()
	}
	d.mu.RLock()
	connected := d.ptr != nil
	d.mu.RUnlock()
	if !connected {
		return ErrNotConnected
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	transactions := d.transactionsForDatabase(databaseName)
	for _, tx := range transactions {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := tx.CloseChecked(); err != nil {
			return err
		}
	}
	return nil
}

func (d *Driver) registerTransaction(tx *Transaction) {
	d.txMu.Lock()
	d.txs[tx.id] = weak.Make(tx)
	d.txMu.Unlock()
}

func (d *Driver) unregisterTransaction(id uint64) {
	d.txMu.Lock()
	delete(d.txs, id)
	d.txMu.Unlock()
}

// liveTransactions returns strong references to the registered transactions
// accepted by keep, pruning entries whose transaction has been collected (the
// finalizer backstop handles those native handles).
func (d *Driver) liveTransactions(keep func(*Transaction) bool) []*Transaction {
	d.txMu.Lock()
	defer d.txMu.Unlock()

	transactions := make([]*Transaction, 0, len(d.txs))
	for id, ref := range d.txs {
		tx := ref.Value()
		if tx == nil {
			delete(d.txs, id)
			continue
		}
		if keep(tx) {
			transactions = append(transactions, tx)
		}
	}
	return transactions
}

func (d *Driver) transactionsForDatabase(databaseName string) []*Transaction {
	return d.liveTransactions(func(tx *Transaction) bool { return tx.dbName == databaseName })
}

func (d *Driver) openTransactions() []*Transaction {
	return d.liveTransactions(func(*Transaction) bool { return true })
}

// Transaction opens a new transaction with default options.
func (d *Driver) Transaction(databaseName string, txnType TransactionType) (*Transaction, error) {
	return d.TransactionWithOptions(databaseName, txnType, nil)
}

// TransactionWithOptions opens a new transaction with the given options.
func (d *Driver) TransactionWithOptions(databaseName string, txnType TransactionType, opts *TransactionOptions) (*Transaction, error) {
	start := time.Now()
	txID := nextTxID()
	d.mu.RLock()
	defer d.mu.RUnlock()

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

	tx := newTransaction(ptr, txID, databaseName, txnType, d, d.closeWorker)
	d.registerTransaction(tx)
	logFFIDuration("tx.open", start, "tx_id", txID, "db", databaseName, "tx_type", int(txnType), "result", "ok")
	return tx, nil
}

// Databases returns a DatabaseManager for this connection.
func (d *Driver) Databases() *DatabaseManager {
	return &DatabaseManager{driver: d}
}
