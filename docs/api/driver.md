# Rust FFI Driver

`import "github.com/CaliLuke/go-typeql/driver"` (requires build tags: `cgo,typedb`) -- [pkg.go.dev](https://pkg.go.dev/github.com/CaliLuke/go-typeql/driver)

The `driver` package provides Go bindings to the official TypeDB `typedb-driver` 3.x Rust crate via CGo. All files are gated with `//go:build cgo && typedb` so they don't affect builds that don't need the driver.

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

The Rust crate lives in `driver/rust/` and compiles to a static library (`libtypedb_ffi.a`) that CGo links against.

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
```

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

**JSON at the boundary**: Query results cross the FFI boundary as JSON strings. The Rust layer serializes each result row to JSON; the Go layer deserializes them to `[]map[string]any`. This avoids complex C struct marshalling while keeping the API clean.

**Thread-local error pattern**: The Rust FFI uses `typedb_check_error()` / `typedb_get_last_error()` for error reporting. The Go side checks after each FFI call.

**Build tags**: All driver source files use `//go:build cgo && typedb`. Integration tests additionally use the `integration` tag. This means:

- `go test ./gotype/...` works without Rust or CGo
- `go build -tags "cgo,typedb" ./driver/...` compiles the driver
- `go test -tags "cgo,typedb,integration" ./driver/...` runs integration tests

## Error Handling

Driver-specific errors:

- **ErrNotConnected** -- driver or transaction handle is nil
- **ErrNilPointer** -- FFI returned a nil pointer without setting an error
- **DriverError** -- error message from the Rust driver
