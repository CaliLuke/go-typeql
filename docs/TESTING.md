# Testing Guide

## Test Strategy

go-typeql uses a two-tier testing approach:

- **Unit tests** — test all packages without a database connection. Use mock implementations of `Conn` and `Tx` interfaces.
- **Integration tests** — test against a live TypeDB instance. Gated by `-tags integration` and require the Rust FFI library.

## Running Tests

```bash
# Unit tests (354 tests, no DB required)
make test-unit
# Or directly:
go test ./ast/... ./gotype/... ./tqlgen/...

# Integration tests (requires TypeDB + Rust library)
podman compose up -d
make test-integration
# Or directly:
go test -tags "cgo,typedb,integration" ./driver/... ./gotype/...

# Lint
make lint
```

## Test Organization

| Package   | Test File(s) | What's Tested                                                            |
| --------- | ------------ | ------------------------------------------------------------------------ |
| `ast/`    | `*_test.go`  | AST node compilation, literal formatting                                 |
| `gotype/` | `*_test.go`  | Tags, models, registry, schema gen, CRUD, queries, filters, migration    |
| `tqlgen/` | `*_test.go`  | Schema parsing, code generation, naming, functions, structs, annotations |
| `driver/` | `*_test.go`  | Connection, transactions, queries (integration only)                     |

## Mock Patterns

Unit tests use mock implementations of the `Conn` and `Tx` interfaces defined in `gotype/session.go`. The mocks use a **sequence-based** pattern where each transaction gets preset responses:

```go
// mockTx records queries and returns responses in sequence
type mockTx struct {
    queries   []string              // All queries executed (recorded)
    responses [][]map[string]any    // One response per query, in order
    committed bool
    err       error
}

func (m *mockTx) Query(query string) ([]map[string]any, error) {
    m.queries = append(m.queries, query)
    if m.err != nil {
        return nil, m.err
    }
    idx := len(m.queries) - 1
    if idx < len(m.responses) {
        return m.responses[idx], nil
    }
    return nil, nil
}

func (m *mockTx) Commit() error  { m.committed = true; return nil }
func (m *mockTx) Rollback() error { return nil }
func (m *mockTx) Close()          {}
func (m *mockTx) IsOpen() bool    { return true }

// mockConn returns transactions in sequence
type mockConn struct {
    txs []*mockTx // One tx per Transaction() call, in order
}

func (m *mockConn) Transaction(dbName string, txType int) (gotype.Tx, error) {
    idx := /* tracks call count */
    return m.txs[idx], nil
}

func (m *mockConn) Schema(dbName string) (string, error) { return "", nil }
func (m *mockConn) Close()                               {}
func (m *mockConn) IsOpen() bool                         { return true }
```

This pattern allows testing multi-query operations (like Insert which queries then fetches IID) by providing responses in the order they'll be consumed.

## Registry in Tests

The global type registry is shared across tests. Each test that registers types should clear the registry first:

```go
func TestSomething(t *testing.T) {
    gotype.ClearRegistry()
    gotype.Register[Person]()
    // ... test code
}
```

This prevents interference from other tests that may have registered (or cleared) different types. A helper pattern used in many tests:

```go
func registerTestTypes(t *testing.T) {
    t.Helper()
    gotype.ClearRegistry()
    gotype.Register[testPerson]()
    gotype.Register[testCompany]()
    gotype.Register[testEmployment]()
}
```

## Test Fixtures

Common test types used across the test suite:

```go
type testPerson struct {
    gotype.BaseEntity
    Name  string `typedb:"name,key"`
    Email string `typedb:"email,unique"`
    Age   *int   `typedb:"age"`
}

type testCompany struct {
    gotype.BaseEntity
    Name string `typedb:"name,key"`
}

type testEmployment struct {
    gotype.BaseRelation
    Employee *testPerson  `typedb:"role:employee"`
    Employer *testCompany `typedb:"role:employer"`
}
```

## Integration Test Infrastructure

Integration tests live in `gotype/integ_*_test.go` files, gated by `//go:build integration && cgo && typedb`. They use a shared `TestMain` that creates/deletes a test database.

### Helpers (`integ_helpers_test.go`)

Test helpers for unique data and common assertions:

```go
// uniqueSuffix returns a 6-char hex string for test isolation
func uniqueSuffix() string

// makeName returns "prefix-abc123" with a unique suffix
func makeName(prefix string) string

// assertEntityExists fetches by filter and fails if not found
func assertEntityExists[T Entity](t *testing.T, mgr *Manager[T], filter Filter) T

// assertEntityCount checks that the count matches expected
func assertEntityCount[T Entity](t *testing.T, mgr *Manager[T], expected int)
```

Use `uniqueSuffix()` / `makeName()` to prevent test collisions when tests run against a shared database.

### Per-Test Schema Setup

Each integration test registers its own types and syncs the schema. Use `t.Cleanup` to clear the registry:

```go
func TestIntegration_SomeFeature(t *testing.T) {
    gotype.ClearRegistry()
    gotype.Register[myEntity]()
    // Sync schema to DB...
    t.Cleanup(func() { gotype.ClearRegistry() })
}
```

### Python Test Parity

The Python type-bridge has 698 integration tests across 80 files. Go currently covers the core paths (~83 ported). The main unported areas are:

| Area                   | Python Tests | Status                                            |
| ---------------------- | ------------ | ------------------------------------------------- |
| Multi-value attributes | ~30          | Not yet supported (slice CRUD)                    |
| Date/Duration/Decimal  | ~20          | Go supports 5 of 9 TypeDB value types             |
| Multi-role players     | ~13          | Not yet supported                                 |
| Constraint enforcement | ~18          | @key/@unique/@card/@regex/@range enforcement      |
| Relations-as-roles     | ~15          | Relations playing roles in other relations        |
| Domain scenarios       | ~80          | Bookstore, STIX, Social, Drug Discovery, IAM      |
| Role player queries    | ~40          | Filter by role player attributes (partially done) |

## Writing New Tests

1. Clear and re-register types at the start of each test.
2. Use `mockConn`/`mockTx` to control query results with the sequence-based pattern.
3. Verify generated TypeQL by inspecting `mockTx.queries`.
4. For schema/migration tests, use `IntrospectSchemaFromString` with a TypeQL string.
5. Integration tests should use the `integration` build tag and clean up test databases.
6. Use `uniqueSuffix()` in integration tests to avoid data collisions.

## Example: Testing a Multi-Query Operation

```go
func TestManager_Insert(t *testing.T) {
    registerTestTypes(t)

    // Insert executes 2 queries: the insert, then a key match to fetch IID
    tx := &mockTx{
        responses: [][]map[string]any{
            nil, // insert returns nothing
            {{"_iid": map[string]any{"value": "0x123"}}}, // key match returns IID
        },
    }
    mock := &mockConn{txs: []*mockTx{tx}}
    db := gotype.NewDatabase(mock, "testdb")
    mgr := gotype.NewManager[testPerson](db)

    p := &testPerson{Name: "Alice"}
    err := mgr.Insert(context.Background(), p)
    if err != nil {
        t.Fatal(err)
    }
    // Verify IID was set
    if p.GetIID() != "0x123" {
        t.Errorf("expected IID 0x123, got %s", p.GetIID())
    }
}
```
