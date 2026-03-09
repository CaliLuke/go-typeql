# go-typeql

[![Go Version](https://img.shields.io/github/v/tag/CaliLuke/go-typeql?label=version)](https://pkg.go.dev/github.com/CaliLuke/go-typeql)
[![Go Reference](https://pkg.go.dev/badge/github.com/CaliLuke/go-typeql.svg)](https://pkg.go.dev/github.com/CaliLuke/go-typeql)

A Go ORM for [TypeDB](https://typedb.com/) 3.x. Define your graph schema as Go structs, and get type-safe CRUD, queries, migrations, and code generation.

## Why

TypeDB has no official Go driver. This project wraps the Rust 3.x driver via CGo and layers a full ORM on top — so you can work with TypeDB entities and relations as regular Go structs instead of writing raw TypeQL.

## TypeDB in 30 seconds

TypeDB is a strongly-typed database that organizes data into three primitives:

- **Entities** — independent objects (Person, Company, Document)
- **Relations** — typed connections between entities with named roles (Employment connects a Person as `employee` to a Company as `employer`)
- **Attributes** — typed values owned by entities or relations (name, email, age)

TypeDB uses its own query language, TypeQL. go-typeql generates TypeQL for you from Go structs, so you rarely need to write it by hand.

## What it looks like

```go
// Define models with struct tags
type Person struct {
    gotype.BaseEntity
    Name  string `typedb:"name,key"`
    Email string `typedb:"email,unique"`
    Age   *int   `typedb:"age"`
}

type Company struct {
    gotype.BaseEntity
    Name string `typedb:"name,key"`
}

type Employment struct {
    gotype.BaseRelation
    Employee *Person  `typedb:"role:employee"`
    Employer *Company `typedb:"role:employer"`
}

// Register, connect, go
gotype.Register[Person]()
gotype.Register[Company]()
gotype.Register[Employment]()

db := gotype.NewDatabase(conn, "my_db")
persons := gotype.NewManager[Person](db)

persons.Insert(ctx, &Person{Name: "Alice", Email: "alice@example.com"})

results, _ := persons.Query().Filter(gotype.Eq("name", "Alice")).Execute(ctx)
```

## Features

- **Struct-tag models** — entities and relations map to Go structs with `typedb:"..."` tags
- **Generic CRUD** — `Manager[T]` for Insert, Get, Update, Delete, Put (upsert), plus batch variants
- **Query builder** — chainable filters, sorting, pagination, aggregations (sum, count, min, max, mean, median, std, variance, group by)
- **Schema migration** — diff Go structs against a live database, apply changes, track migration state
- **Code generator** — `tqlgen` generates Go structs, DTOs, and a typed registry from TypeQL schema files
- **Rust FFI driver** — wraps `typedb-driver` 3.x via CGo; the ORM packages compile and test without it

## Packages

| Package   | What it does                                | Needs CGo |
| --------- | ------------------------------------------- | :-------: |
| `ast/`    | TypeQL AST nodes and compiler               |    No     |
| `gotype/` | ORM core: models, CRUD, queries, migrations |    No     |
| `tqlgen/` | Code generator: TypeQL schema to Go structs |    No     |
| `driver/` | Rust FFI bindings to `typedb-driver` 3.x    |    Yes    |

## Getting started

### Install

```bash
go get github.com/CaliLuke/go-typeql@v1.6.1
```

The `ast/`, `gotype/`, and `tqlgen/` packages work without CGo or a running database. The `driver/` package requires the Rust FFI static library. `go get` only downloads the source tree; it does not build or provision `libtypedb_go_ffi.a` for you. Before building or testing code that imports `driver/`, you must either build the Rust library from source in the module tree or install a prebuilt archive.

### Prebuilt FFI library

Each [release](https://github.com/CaliLuke/go-typeql/releases) includes prebuilt static libraries for:

- `linux-amd64`
- `linux-arm64`
- `darwin-amd64`
- `darwin-arm64`

```bash
platform="$(go env GOOS)-$(go env GOARCH)"

# Download for your platform
gh release download <tag> -p "libtypedb_go_ffi-${platform}.a" -R CaliLuke/go-typeql

# Option A: place in standard lib path, build with typedb_prebuilt tag
libdir=/usr/local/lib
if [ "$(go env GOOS)-$(go env GOARCH)" = "darwin-arm64" ]; then libdir=/opt/homebrew/lib; fi
cp "libtypedb_go_ffi-${platform}.a" "${libdir}/libtypedb_go_ffi.a"
go test -tags "cgo,typedb,typedb_prebuilt" ./...

# Option B: place in source tree (no extra build tag needed)
mkdir -p driver/rust/target/release
cp "libtypedb_go_ffi-${platform}.a" driver/rust/target/release/libtypedb_go_ffi.a
go test -tags "cgo,typedb" ./...
```

On Apple Silicon with Homebrew, `typedb_prebuilt` also searches `/opt/homebrew/lib`. If you do not want to install into a linker search path, use Option B instead.

If your platform does not have a published archive yet, build from source with `make build-rust` in the checked-out module before `go build` or `go test -tags "cgo,typedb" ...`. This matters even when the module comes from the Go proxy or module cache, because the Rust archive is not generated automatically during `go get`.

For a complete runnable example covering connect, schema, and CRUD, see the [Getting Started walkthrough](docs/GETTING_STARTED.md).

## Running tests

```bash
# Unit tests (397 tests, no database needed)
go test ./ast/... ./gotype/... ./tqlgen/...

# Integration tests (needs TypeDB on port 1729)
podman compose up -d
go test -tags "cgo,typedb,integration" ./driver/... ./gotype/...
```

## Debugging hangs

See the dedicated guide: [Debugging Hangs](docs/DEBUGGING_HANGS.md).

## Docs

- [Development Guide](docs/DEVELOPMENT.md) — building, project structure, Rust FFI, contributing
- [Testing Guide](docs/TESTING.md) — test strategy, mocks, integration test infrastructure
- [Debugging Hangs](docs/DEBUGGING_HANGS.md) — debug flags, log keys, startup-hang diagnostics
- [API Reference](docs/api/README.md) — models, CRUD, queries, filters, schema, migration, code generator

## Requirements

- Go 1.26+
- Rust toolchain (only for the driver)
- TypeDB 3.x (only for integration tests)

## License

MIT
