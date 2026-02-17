# Development Guide

## Prerequisites

- **Go 1.26+**
- **Rust toolchain** (for the FFI driver) — install via [rustup](https://rustup.rs/)
- **TypeDB 3.x server** (for integration tests) — run via Podman/Docker or install directly
- **Podman or Docker** (optional, for running TypeDB in tests)

## Project Structure

```text
go-typeql/
├── ast/                TypeQL AST nodes and compiler
├── gotype/         ORM core (models, CRUD, queries, filters, migration)
├── driver/             Rust FFI driver (CGo)
│   └── rust/           Rust crate wrapping typedb-driver
├── tqlgen/             Code generator (TypeQL schema → Go structs)
│   └── cmd/tqlgen/     CLI entrypoint
├── docs/               Documentation
│   └── api/            API reference
├── docker-compose.yml  TypeDB for integration tests
├── Makefile            Build and test targets
└── go.mod
```

### gotype/ Package Layout

```text
gotype/
├── session.go          Conn/Tx interfaces, Database, TransactionContext
├── model.go            ModelInfo, FieldInfo, ToDict, FromDict, ToInsertQuery, ToMatchQuery
├── entity.go           BaseEntity
├── relation.go         BaseRelation
├── tags.go             Struct tag parsing
├── registry.go         Type registration, Lookup, SubtypesOf, ResolveType
├── reserved.go         TypeQL reserved word validation
├── errors.go           Error types (NotRegistered, KeyAttribute, Hydration, etc.)
├── schema.go           Schema generation (GenerateSchema, GenerateSchemaFor)
├── format.go           Go value → TypeQL string conversion
├── hydrate.go          TypeDB result → Go struct population
├── strategy.go         Query building strategies (entity/relation)
├── crud.go             Manager[T] CRUD operations (Insert, Get, Update, Delete, Put, etc.)
├── filter.go           Filter types (Eq, Gt, In, Range, Regex, RolePlayer, etc.)
├── query.go            Query[T] builder, AggregateQuery, GroupByQuery
├── migrate.go          Schema diff and migration (Migrate, DiffSchema, etc.)
├── migrate_ops.go      Migration operations (Operation interface, breaking changes)
├── migrate_state.go    Migration state tracking (MigrationState, MigrateWithState)
└── *_test.go           Unit tests
```

## Building the Rust FFI Library

The `driver/` package requires a compiled Rust static library. The Rust crate in `driver/rust/` wraps the official `typedb-driver` crate and exposes a C FFI interface.

```bash
# Build the static library (driver/rust/target/release/libtypedb_ffi.a)
make build-rust

# This runs: cd driver/rust && cargo build --release
```

After building, you can compile Go code with driver support:

```bash
go build -tags "cgo,typedb" ./...
```

## Make Targets

| Target                  | Description                                            |
| ----------------------- | ------------------------------------------------------ |
| `make build-rust`       | Build the Rust FFI static library                      |
| `make test-unit`        | Run unit tests (no DB required)                        |
| `make test-integration` | Run integration tests (requires TypeDB + Rust library) |
| `make test`             | Alias for `test-unit`                                  |
| `make lint`             | Run `go vet` on all packages                           |
| `make clean-rust`       | Clean Rust build artifacts                             |
| `make clean`            | Clean Rust artifacts + Go build cache                  |

## Container Setup

A `docker-compose.yml` is provided for running TypeDB during integration tests. Works with both Podman and Docker:

```bash
# Start TypeDB (port 1729)
podman compose up -d
# or: docker compose up -d

# Run integration tests
make test-integration

# Stop TypeDB
podman compose down
```

## Build Tags

The project uses build tags to isolate CGo-dependent code:

| Tag           | Usage                         |
| ------------- | ----------------------------- |
| `cgo`         | Required for CGo compilation  |
| `typedb`      | Gates all driver source files |
| `integration` | Gates integration test files  |

The `ast/`, `gotype/`, and `tqlgen/` packages compile and test without any build tags. Only the `driver/` package requires `cgo && typedb`.

```bash
# Unit tests (default, no tags needed) — 354 tests
go test ./ast/... ./gotype/... ./tqlgen/...

# Driver + integration tests
go test -tags "cgo,typedb,integration" ./driver/... ./gotype/...
```

## Package Dependencies

```text
ast/           — zero external dependencies (stdlib only)
gotype/    — depends on ast/ and tqlgen/ (no CGo)
tqlgen/        — depends on github.com/alecthomas/participle/v2
driver/        — CGo + Rust FFI (gated by build tags)
```

The `gotype` package defines `Conn` and `Tx` interfaces that decouple it from the `driver` package. This means `gotype` compiles and tests without CGo.

## Feature Parity with Python type-bridge

Go has full feature parity with the Python type-bridge for core ORM functionality:

- CRUD: Insert, Get, Update, Delete, Put (upsert), batch variants (InsertMany, DeleteMany, UpdateMany, PutMany)
- Query builder: filters, sort, offset/limit, aggregations (sum, count, min, max, mean, median, std, variance), group by
- Filters: Eq, Gt, Lt, Gte, Lte, Neq, Contains, Like, Regex, Startswith, In, NotIn, Range, HasAttr, NotHasAttr, RolePlayer, And, Or, Not
- Schema: generation, diffing, migration with state tracking, breaking change detection
- Polymorphism: SubtypesOf, ResolveType, GetByIIDPolymorphic
- Transactions: explicit TransactionContext, NewManagerWithTx
- Serialization: ToDict, FromDict, ToInsertQuery, ToMatchQuery
- Code generation: TypeQL schema to Go structs (tqlgen)
- Reserved word validation: 111 TypeQL keywords

### Not Yet Ported

These Python features are not yet implemented in Go:

| Feature                   | Description                                                   |
| ------------------------- | ------------------------------------------------------------- |
| Multi-value attributes    | Slice fields for `@card(0..)` attributes with CRUD support    |
| Multi-role players        | Multiple entities playing the same role in a relation         |
| Date/DateTimeTZ/Duration  | Go supports 5 of 9 TypeDB value types (missing date variants) |
| Decimal type              | TypeDB decimal mapped to Go type                              |
| Relations-as-role-players | Relations playing roles in other relations                    |
| Constraint enforcement    | Runtime validation of @key, @unique, @regex, @range, @values  |

## Architecture

The four packages form a layered architecture:

```text
  driver/          gotype/            ast/          tqlgen/
  (Rust FFI)   (ORM: CRUD, queries)  (TypeQL AST)  (code gen)
      |               |    |              |             |
      |     implements|    | uses         | uses        |
      +---- Conn/Tx --+    +--- builds ---+             |
                       |                                |
                       +------ introspects via ---------+
```

- `ast/` is a standalone TypeQL AST with zero dependencies. `gotype/` builds AST nodes and compiles them to TypeQL strings.
- `gotype/` defines `Conn` and `Tx` interfaces. The `driver/` package satisfies them, but `gotype/` never imports `driver/` — the user wires them together.
- `tqlgen/` parses TypeQL schema files. `gotype/` reuses its parser for schema introspection and migration diffing.
- `driver/` is fully gated behind `//go:build cgo && typedb`. Everything else compiles and tests without CGo.

## Documentation

### Godoc

All exported types and functions have doc comments that render on [pkg.go.dev](https://pkg.go.dev/github.com/CaliLuke/go-typeql). To preview locally:

```bash
make docs
# Opens pkgsite on http://localhost:8080
```

Or in the terminal:

```bash
go doc github.com/CaliLuke/go-typeql/gotype.Manager
go doc -all github.com/CaliLuke/go-typeql/ast
```

### Manual docs

The `docs/` directory contains hand-written guides:

- `docs/DEVELOPMENT.md` — this file (building, architecture, contributing)
- `docs/TESTING.md` — test strategy, mocks, integration infrastructure
- `docs/api/*.md` — API reference with examples (generated from godoc + hand-written sections)

## Performance Notes

Key optimizations applied to the codebase:

- **AST-based query building** — all strategies use AST compilation instead of string concatenation
- **Reduced round-trips** — Insert+FetchIID combined into single query (50% reduction); multi-aggregate uses single `reduce` query
- **No thread pinning** — FFI uses out-parameter error handling instead of thread-local storage, so goroutines are free to migrate
- **Connection pooling** — `ConnPool` with configurable min/max size, idle timeout, wait queue, health checks, and context support
- **Hydration** — benchmarked at 0.3ms/1000 rows with reflection; no optimization needed
