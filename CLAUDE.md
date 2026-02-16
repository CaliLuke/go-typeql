# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What This Is

Go ORM for TypeDB 3.x. Wraps the Rust driver via CGo FFI. Module: `github.com/CaliLuke/go-typeql`

## Commands

```bash
# Unit tests (298 tests, no DB or CGo needed)
go test ./ast/... ./gotype/... ./tqlgen/...

# Single test
go test -run TestManager_Insert ./gotype/...

# Integration tests (needs TypeDB on port 1729 + built Rust lib)
# ALWAYS run these after changes — not just unit tests.
# When the user says "test" or "run tests", run ALL tests (unit + integration).
podman compose up -d
go test -tags "cgo,typedb,integration" ./driver/... ./gotype/...

# Build Rust FFI library
make build-rust

# Linting (MUST pass before committing)
go vet ./...                    # Built-in static analysis
golangci-lint run ./...         # Comprehensive linter (50+ checks)
```

## Code Quality Standards

**Linting is mandatory** — all code must pass both `go vet` and `golangci-lint` before committing.

**Common issues to avoid:**

- Unused variables, functions, or struct fields
- Ineffectual assignments (variables assigned but immediately overwritten)
- Loops that can be simplified with `append(slice, items...)`
- Struct literals when type conversion is available
- Missing error checks

**When golangci-lint fails:**

1. Fix the issue — don't ignore linter warnings
2. Run tests to verify fix didn't break functionality
3. Commit fix separately if unrelated to current work

## Architecture

Four packages with deliberate CGo isolation:

- **`ast/`** — TypeQL AST nodes + compiler. Pure Go, zero dependencies. Type-switch dispatch.
- **`gotype/`** — ORM core. Models, CRUD, queries, filters, migration. **No CGo.** Decoupled from driver via `Conn`/`Tx` interfaces.
- **`tqlgen/`** — Code generator. TypeQL schema → Go structs. Participle-based parser handles `define` blocks (attributes, entities, relations, structs) directly via grammar. Functions are stripped by a character-level scanner (string/comment-aware) and extracted separately via regex.
- **`driver/`** — Rust FFI bindings. **All files gated with `//go:build cgo && typedb`**. Integration tests additionally gated with `integration`.

The key decoupling: `gotype/session.go` defines `Conn` and `Tx` interfaces that the driver satisfies. This means `gotype/` compiles, tests, and works without CGo — unit tests use `mockTx`/`mockConn`.

## Key Patterns

**Struct tags** drive everything: `typedb:"attr-name,key,unique,card=M..N,role:name"`

**Kebab-case naming**: Go struct names are converted to kebab-case for TypeDB type names via `toKebabCase()` (`UserAccount` → `user-account`, `MigratedPerson` → `migrated-person`). Any hand-written TypeQL in tests must use the kebab-case form, not the Go name.

**Registry** is global — `Register[T]()` extracts model metadata via reflect. Tests must `ClearRegistry()` + re-register per test since the registry is shared.

**Strategy pattern** in `gotype/strategy.go`: `entityStrategy` and `relationStrategy` implement `ModelStrategy` to build TypeQL strings for different type kinds.

**Filter interface**: `ToPatterns(varName string) []string`. Variable names use double-underscore separator (`$e__attr_name`) to avoid TypeQL implicit equality. Hyphens sanitized to underscores.

**Manager[T]** is the generic CRUD entry point. Insert fetches IID in the same write transaction via key match. Update uses per-attribute delete-old/insert-new.

**Mock pattern** for unit tests: `mockTx` has `responses [][]map[string]any` — preset responses consumed in query order. `mockConn` has `txs []*mockTx` — one tx per `Transaction()` call.

## TypeDB Quirks

- Uses `mean` not `avg` for average aggregation
- Result values wrapped as `{"value": X}` — unwrapped automatically by `unwrapResult`/`unwrapValue`
- `isa!` for exact type match (no subtypes), `isa` for polymorphic

## Documentation

Documentation lives in `docs/` with this structure:

```text
docs/
├── GETTING_STARTED.md      # Full walkthrough (connect → schema → CRUD)
├── DEVELOPMENT.md          # Building, architecture, contributing
├── TESTING.md              # Test strategy, mocks, integration infra
├── SKILL.md                # AI agent skill file for go-typeql
└── api/
    ├── README.md           # Index of guides + reference links
    ├── models.md           # Guide: defining models, tags, registry
    ├── crud.md             # Guide: CRUD patterns, transactions
    ├── queries.md          # Guide: filters, aggregations, gotchas
    ├── schema.md           # Guide: migration workflows
    ├── generator.md        # Guide: tqlgen usage
    ├── ast.md              # Guide: AST query building
    ├── driver.md           # Guide: FFI driver setup
    └── reference/          # Auto-generated from godoc (DO NOT hand-edit)
        ├── ast.md
        ├── gotype.md
        └── tqlgen.md
```

**Guides** (`docs/api/*.md`) explain workflows, patterns, and gotchas. They're hand-written and should not duplicate API signatures — that's what the reference docs are for.

**Reference** (`docs/api/reference/`) is generated from source code comments via `gomarkdoc`. Regenerate after changing godoc comments:

```bash
~/go/bin/gomarkdoc ./ast/ > docs/api/reference/ast.md
~/go/bin/gomarkdoc ./gotype/ > docs/api/reference/gotype.md
~/go/bin/gomarkdoc ./tqlgen/ > docs/api/reference/tqlgen.md
```

When adding exported symbols, always add a godoc comment. The reference docs and pkg.go.dev render directly from these comments.

## Git

Do NOT add `Co-Authored-By` lines to commit messages.

## Releasing a New Version

Use `/release-checks <version>` to run the full 13-step release checklist (tests, coverage, linting, docs, tagging, changelog, pkg.go.dev verification). See `.claude/skills/release-checks.md`.

## Container Runtime

Uses **podman** (not docker). TypeDB runs on port 1729. Integration test address configurable via `TEST_DB_ADDRESS` env var (default `localhost:1729`).
