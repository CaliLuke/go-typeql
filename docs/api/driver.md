# Rust FFI Driver

`import "github.com/CaliLuke/go-typeql/driver"` (requires build tags: `cgo,typedb`) -- [pkg.go.dev](https://pkg.go.dev/github.com/CaliLuke/go-typeql/driver)

The `driver` package provides Go bindings to the official TypeDB `typedb-driver` 3.x Rust crate via CGo. All files are gated with `//go:build cgo && typedb` so they don't affect builds that don't need the driver.

The bundled Rust FFI crate currently depends on the final `typedb-driver` and `typeql` `3.12.0` Rust crates and is tested against the final `typedb/typedb:3.12.0` server image.

`go get` only downloads the module source. It does not build the Rust static library automatically. If you import `driver/`, you must either run `make build-rust` in the module tree that Go is compiling, or provide a prebuilt `libtypedb_go_ffi.a` and build with the `typedb_prebuilt` tag. Release archives are published for `linux-amd64`, `linux-arm64`, `darwin-amd64`, and `darwin-arm64`.

## Prerequisites

- **Rust toolchain** -- install via [rustup](https://rustup.rs/)
- **TypeDB 3.x server** -- for integration tests
- **CGo** -- enabled by default in Go

## Building

```bash
# Build the Rust FFI static library
make build-rust

# Build Go code with driver support
go build -tags "cgo,typedb" ./...
```

The Rust crate lives in `driver/rust/` and compiles to `driver/rust/target/release/libtypedb_go_ffi.a`, which the default CGo build links via `driver/ffi.go`.

## Connecting

```go
import "github.com/CaliLuke/go-typeql/driver"

// Basic connection
drv, err := driver.Open("localhost:1729", "admin", "password")
if err != nil {
    log.Fatal(err)
}
defer drv.Close()

// With TLS
drv, err := driver.OpenWithTLS("localhost:1729", "admin", "password", true, "/path/to/ca.crt")

// With TypeDB driver-level options
drv, err := driver.OpenWithOptions("localhost:1729", "admin", "password", driver.DriverOptions{
    RequestTimeoutMillis:   5000,
    PrimaryFailoverRetries: 1,
})

// Inspect the connected server version
version, err := drv.ServerVersion()
if err == nil {
    log.Printf("connected to %s %s", version.Distribution, version.Version)
}

// Multiple public addresses
drv, err = driver.OpenWithAddresses([]string{
    "typedb-1.example.com:1729",
    "typedb-2.example.com:1729",
}, "admin", "password", driver.DriverOptions{})

// Public-to-private address translation for clusters or mapped containers
drv, err = driver.OpenWithAddressTranslation(map[string]string{
    "localhost:1730": "127.0.0.1:1729",
}, "admin", "password", driver.DriverOptions{})

// The same mapping through DriverOptions on a single-address open
drv, err = driver.OpenWithOptions("localhost:1730", "admin", "password", driver.DriverOptions{
    AddressTranslation: map[string]string{"localhost:1730": "127.0.0.1:1729"},
})
```

Addresses are dialed exactly as given -- the driver never rewrites localhost
ports implicitly. When TypeDB advertises an address that differs from the one
clients dial (for example the repo compose setup, where the container
advertises `127.0.0.1:1729` while the host maps port `1730`), configure the
mapping explicitly with `OpenWithAddressTranslation` or
`DriverOptions.AddressTranslation`. For local test runs only, setting the
`TYPEDB_GO_COMPOSE_PORT_MAP=1` environment variable maps any dialed
`localhost`/`127.0.0.1` address with a non-`1729` port to `127.0.0.1:1729`.

### Connection Features

The Rust driver exposes a small set of connection-level controls through
`DriverOptions`:

| Option                   | Applies to                                              |
| ------------------------ | ------------------------------------------------------- |
| `RequestTimeoutMillis`   | Unary RPCs such as database create/list, schema fetch, and transaction open |
| `PrimaryFailoverRetries` | Finding or re-routing to a primary server in clustered deployments |
| `TLSEnabled`/`TLSRootCA` | TLS setup, equivalent to `OpenWithTLS`                  |
| `AddressTranslation`     | Public-to-private address mapping for single-address opens |

These options do not replace `QueryOptions`; query result prefetch and
instance-type inclusion are still configured per query.

`ServerVersion` is useful at process startup to make protocol mismatches
obvious before application code begins opening transactions:

```go
version, err := drv.ServerVersion()
if err != nil {
    log.Fatal(err)
}
log.Printf("connected to %s %s", version.Distribution, version.Version)
```

For clusters and containerized deployments, use:

- `OpenWithAddresses` when several public server addresses are directly reachable.
- `OpenWithAddressTranslation` when TypeDB advertises private addresses that differ
  from the addresses clients must dial.

## Transactions

```go
txn, err := drv.Transaction("my_db", driver.Write)
if err != nil {
    log.Fatal(err)
}
defer txn.Close()

results, err := txn.Query(`insert $p isa person, has name "Alice";`)
if err != nil {
    log.Fatal(err)
}

err = txn.Commit()
```

Transaction types: `Read` (0), `Write` (1), `Schema` (2).

### Context Cancellation and Abandonment

`QueryWithContext` (and `QueryWithContextAndOptions`, which also carries
`*QueryOptions`/`*GivenRows`) returns `ctx.Err()` as soon as the context is
cancelled, even while the FFI call is still running. The transaction is then
**abandoned**: ownership of the native handle transfers to the in-flight call,
which frees it in the background once the server returns. Subsequent
`Commit`/`Rollback`/`Query*` calls return `ErrTransactionAbandoned`
immediately, and `Close`/`CloseAsync` are instant no-ops — a deferred `Close`
after a cancelled query never blocks behind a slow server. Bound the
server-side work with `TransactionOptions.SetTimeout`; the abandoned call
keeps consuming server resources until it finishes or times out. Don't
`Close()` a `QueryOptions` passed to a cancelled call until pending closes
drain (`WaitForPendingCloses`).

Lifecycle guarantees: `CloseAsync(onDone)` invokes its callback exactly once
in every path (queued, dropped, or already closed). Transactions that are
garbage-collected while open are freed by a finalizer backstop that logs
`typedb_go.tx.finalizer.leak` via `slog` — but rely on explicit
`Commit`/`Rollback`/`Close`, not the finalizer. `Driver.Close()` closes any
still-open transactions before releasing the driver handle.

Concurrency: transaction opens, database-manager operations, and version
checks run in parallel (the driver uses a read-write lock; only `Close` is
exclusive). Individual transactions still serialize their own FFI calls.

### Given Rows

TypeDB 3.12 adds the `given` stage for passing input rows separately from the
query string. Use `QueryWithRows` or `QueryWithOptionsAndRows` to send typed
values without interpolating them into TypeQL:

```go
rows := driver.NewGivenRows("name", "age").
    MustAdd(driver.StringGiven("Alice"), driver.IntGiven(30)).
    MustAdd(driver.StringGiven("Bob"), driver.IntGiven(41))

_, err := txn.QueryWithRows(`
given $name: string, $age: integer;
insert $p isa person, has name == $name, has age == $age;
`, rows)
```

The Go API supports scalar given values: string, integer, double, boolean,
decimal, date, datetime, datetime-tz, duration, and empty entries. It also
supports opaque entity and relation concepts returned by row queries. Concept
handles are opt-in: request them with `QueryOptions.SetConceptHandles(true)`:

```go
opts := driver.NewQueryOptions().SetConceptHandles(true)
defer opts.Close()

conceptRows, err := txn.QueryWithOptions(`
match
  $p isa person, has name "Alice";
select $p;
`, opts)
person, ok := driver.AsConcept(conceptRows[0]["p"])
if !ok {
    log.Fatal("query did not return an opaque concept")
}
defer person.Release()

rows := driver.NewGivenRows("person", "age").
    MustAdd(driver.ConceptGiven(person), driver.IntGiven(30))

_, err = txn.QueryWithRows(`
given $person: person, $age: integer;
insert $person has age == $age;
`, rows)
```

Opaque concept handles are process-local values produced by this driver. They
are intended to be passed back to `ConceptGiven`, not persisted or constructed
manually. Each handle pins the concept in a process-global registry and stays
valid across transactions until released, so call `Concept.Release` (or
`driver.ReleaseAllConcepts`) when you are done with it; queries executed
without `SetConceptHandles(true)` never register handles.

### Transaction Lifecycle Helpers

The driver tracks transactions opened through each `Driver` instance. This is
useful when deleting or recreating databases in tests and long-running tools:

```go
open, err := drv.HasOpenTransactions("my_db")
if err != nil {
    log.Fatal(err)
}
if open {
    ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
    defer cancel()
    if err := drv.CloseDatabaseTransactions(ctx, "my_db"); err != nil {
        log.Fatal(err)
    }
}
```

These helpers are scoped to the current Go driver process. They report and close
transactions opened by this `Driver`; they do not enumerate unrelated server-side
transactions opened by other clients.

`Close()` is caller-fast for uncommitted transactions: it detaches the Go handle immediately and completes the checked TypeDB close on a bounded background worker. Close failures are logged because the `gotype.Tx` interface cannot return a close error.

Use `CloseChecked()` when you deliberately want to wait for the TypeDB close result:

```go
if err := txn.CloseChecked(); err != nil {
    log.Printf("close failed: %v", err)
}
```

Use `CloseAsync` when you need a completion callback without blocking the caller:

```go
txn.CloseAsync(func(err error) {
    if err != nil {
        log.Printf("close failed: %v", err)
    }
})
```

`Commit()` and `Rollback()` remain synchronous. A deferred `Close()` after `Commit()` is a no-op because `Commit()` consumes the transaction handle.

Long-running applications and integration tests can drain accepted background close work before shutdown:

```go
ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
defer cancel()
if err := driver.WaitForPendingCloses(ctx); err != nil {
    log.Printf("transaction close drain timed out: %v", err)
}
```

## Database Management

```go
dbs := drv.Databases()

exists, _ := dbs.Contains("my_db")
if !exists {
    dbs.Create("my_db")
}

// Get schema for migration
schema, _ := dbs.Schema("my_db")

// List all databases
names, _ := dbs.All()

// Delete a database
dbs.Delete("my_db")
```

## Interface Compatibility

The `driver.Driver` type satisfies the `gotype.Conn` interface and `driver.Transaction` satisfies `gotype.Tx`. This is the key decoupling that lets the ORM layer work without CGo:

- The `gotype` package compiles without CGo or the `typedb` build tag.
- Unit tests use mock implementations of `Conn`/`Tx` (see [Testing Guide](../TESTING.md)).
- Any compatible TypeDB client can be used as a backend.

The `Conn` interface includes database lifecycle methods (`DatabaseCreate`, `DatabaseDelete`, `DatabaseContains`, `DatabaseAll`) and schema introspection (`Schema`), all of which the driver satisfies.

## Architecture Notes

**MessagePack at the boundary**: Query results cross the FFI boundary as MessagePack byte buffers. The Rust layer serializes result rows and documents; the Go layer deserializes them to `[]map[string]any`.

**Out-parameter error pattern**: The Rust FFI returns errors through `char**` out-parameters. The Go side converts those into `DriverError` values.

**Build tags**: All driver source files use `//go:build cgo && typedb`. Integration tests additionally use the `integration` tag. This means:

- `go test ./gotype/...` works without Rust or CGo
- `go build -tags "cgo,typedb" ./driver/...` compiles the driver
- `go test -tags "cgo,typedb,integration" ./driver/...` runs integration tests

If you use the repo `docker-compose.yml` for integration tests, set `TEST_DB_ADDRESS=localhost:1730` and `TYPEDB_GO_COMPOSE_PORT_MAP=1` because the compose stack maps host port `1730` to the server's internal `1729` (the env var opts into the localhost address translation; `make test-integration` sets both by default).

**Rust driver logs**: `driver.Open` installs a `tracing` subscriber for the underlying Rust driver crate. Set `TYPEDB_GO_RUST_LOG` (or `RUST_LOG`) to an env-filter such as `typedb_driver=debug` to see driver logs on stderr; when unset, logging stays off.

## Error Handling

Driver-specific errors:

- **ErrNotConnected** -- driver or transaction handle is nil
- **ErrNilPointer** -- FFI returned a nil pointer without setting an error
- **ErrTransactionAbandoned** -- transaction was abandoned after a cancelled context call
- **DriverError** -- error message from the Rust driver
