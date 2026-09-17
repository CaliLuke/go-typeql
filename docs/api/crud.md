# CRUD Operations

`import "github.com/CaliLuke/go-typeql/gotype"` -- [pkg.go.dev](https://pkg.go.dev/github.com/CaliLuke/go-typeql/gotype)

The `Manager[T]` generic type provides Insert, Get, Update, Delete, Put (upsert), and batch operations for registered TypeDB models.

## Setup

```go
db := gotype.NewDatabase(conn, "my_db")
persons := db.MustManager[Person]()
```

`MustManager` panics if type `T` has not been registered via `Register[T]()`. Use
`db.Manager[T]()` when registration failures should be returned as errors. The
package-level `NewManager` and `MustNewManager` functions remain available for
compatibility.

## Insert

Inserts a new entity or relation. After a successful insert, the instance's IID is populated automatically (if it has key fields).

```go
alice := &Person{Name: "Alice", Email: "alice@example.com"}
err := persons.Insert(ctx, alice)
// alice.GetIID() is now set (e.g., "0x826e80018000000000000001")
```

`InsertMany` inserts multiple instances in a single write transaction. With the
bundled driver and a scalar entity model that has one string key and no absent
optional values or slice fields, it uses typed input rows in groups of at most
32 (one query per group). It maps returned IIDs by key rather than result order
and assigns them only after successful completion. Relations, decimals, list
fields, absent optional values, and custom `Tx` implementations without typed
row support use the compatible one-query-per-instance path. Both paths keep
the transaction atomic. `PutMany` also uses one transaction but has its own
query behavior.

Insert and Put (and their Many variants) validate key attributes before writing: a
zero-value key (`""`, `0`, nil pointer) returns a `*KeyAttributeError` instead of
inserting an unidentifiable instance.

## Get

Returns instances matching attribute filters. Filter keys are TypeDB attribute names. Pass `nil` to retrieve all instances (equivalent to `All()`).

```go
results, err := persons.Get(ctx, map[string]any{"name": "Alice"})
```

Other retrieval methods:

- `All(ctx)` -- shorthand for `Get(ctx, nil)`
- `GetOne(ctx, filters)` -- exactly one match: returns `*NotFoundError` on zero matches,
  `*NotUniqueError` on more than one

`NotUniqueError.Count` is the exact number of fetched answer rows (including
repeated rows for the same instance), not a distinct-instance count. For a
bounded first result or a distinct count instead, use `Query.First` or
`Query.Count` explicitly; see [the performance investigation](../../benchmarks/GET_ONE.md).
- `GetByIID(ctx, iid)` -- fetch by TypeDB internal ID, returns nil if not found
- `GetByIIDPolymorphic(ctx, iid)` -- also returns the actual TypeDB type label
- `GetByIIDPolymorphicAny(ctx, iid)` -- hydrates as the concrete subtype (returns `any`)
- `GetWithRoles(ctx, filters)` -- for relations, populates role player entities

For selected attributes or role-player fields, use [selected-field reads](projections.md).
For large result sets, use [typed iteration](iteration.md) to handle one model at a time.

```go
// Get relations with role players populated
jobs := db.MustManager[Employment]()
results, err := jobs.GetWithRoles(ctx, nil)
// results[0].Employee is populated with the Person data
// results[0].Employer is populated with the Company data
```

## Update

Updates a previously fetched instance. The instance must have a valid IID from a prior Insert or Get. Update uses per-attribute delete-old/insert-new semantics in a single write transaction. Key fields are not updated.

`UpdateMany` updates multiple instances in a single write transaction. All instances must have valid IIDs.

## Delete

Deletes an instance by its IID. By default, deleting a non-existent instance is a no-op.

With `WithStrict()`, delete pre-checks existence and returns an error if the instance is not found:

```go
err := persons.Delete(ctx, alice, gotype.WithStrict())
```

`DeleteMany` deletes distinct IIDs in groups of up to 32 within one write
transaction. Strict mode checks existence in bounded groups before writing;
the first missing input is reported without deleting any rows. Repeated IIDs
are checked and deleted once.

## Put (Upsert)

Inserts if the entity doesn't exist, or returns the matching instance when the
specified attributes already agree. A changed non-key value on an existing
unique-key entity can produce a TypeDB key conflict; use `Update` for that
change. After a successful put, the instance's IID is populated.

```go
persons.Put(ctx, &Person{Name: "Alice", Email: "alice@newdomain.com"})
```

For keyed models, `Put` fetches the IID in the same query as the upsert, avoiding
a second key-match query. Keyless relations retain their existing behavior and
do not receive an IID from `Put`. `PutMany` upserts multiple instances in one
transaction; IIDs are assigned only after commit.
When all rows are scalar entities with one string key and the driver supports
typed input rows, `PutMany` batches up to 32 rows per query and matches returned
IIDs by key, regardless of result order. Duplicate keys, relations, and
unsupported optional, slice, or decimal values use the per-instance path.

`UpdateMany` and query `UpdateWith` batch compatible scalar entity updates
using typed rows in groups of eight, matching each row by its IID. Updates
with absent optional values, slices, decimals, relations, or repeated IIDs
retain the per-instance path so deletion and ordering semantics stay the same.
`UpdateWith` runs all callbacks in fetched order before persistence.

## Query Builder

`persons.Query()` returns a chainable query builder. See [Queries](queries.md) for the full guide.

## Transaction Context

For explicit transaction control across multiple managers, use `TransactionContext`:

```go
tc, err := db.Begin(gotype.WriteTransaction)
defer tc.Close()

persons := tc.MustManager[Person]()
companies := tc.MustManager[Company]()

persons.Insert(ctx, &Person{Name: "Alice"})
companies.Insert(ctx, &Company{Name: "Acme"})

err = tc.Commit() // Both inserts in one transaction
```

Transaction types: `ReadTransaction` (0), `WriteTransaction` (1), `SchemaTransaction` (2).

The `Tx` interface includes `QueryWithRows` and `QueryWithContextAndRows`.
These methods execute a raw query with typed input for its `given` stage.
`*driver.GivenRows` supplies the input:

```go
tx, err := db.Transaction(gotype.WriteTransaction)
if err != nil {
    return err
}
defer tx.Close()

rows := driver.NewGivenRows("name").
    MustAdd(driver.StringGiven("Alice")).
    MustAdd(driver.StringGiven("Bob"))

_, err = tx.QueryWithContextAndRows(ctx, `
given $name: string;
insert $p isa person, has name == $name;
`, rows)
if err != nil {
    return err
}
return tx.Commit()
```

For several related reads, share one short-lived read snapshot across managers:

```go
scope, err := db.BeginContext(ctx, gotype.ReadTransaction)
if err != nil {
    return err
}
defer scope.Close() // The caller that opens the scope owns cleanup.

persons := gotype.MustNewManagerWithTx[Person](scope)
companies := gotype.MustNewManagerWithTx[Company](scope)
alice, err := persons.GetByIID(ctx, personIID)
if err != nil {
    return err
}
acme, err := companies.GetByIID(ctx, companyIID)
if err != nil {
    return err
}
// Use alice and acme here, while the snapshot is still open.
```

`BeginContext` passes the caller's context to connections that support
context-aware transaction opening; a plain `Conn` implementation falls back to
its context-free `Transaction` method. Pass the context to reads; the bundled
driver returns promptly on cancellation. Custom `Tx` implementations must
honor `QueryWithContext` and, when they provide streamed reads,
`QueryEachWithContext`. Always close the scope, including on errors or
cancellation. Bound managers do not close it for
you. Keep the unit of work bounded: a read transaction holds a snapshot until
closed, and operations on the same native transaction must be serialized, not
fanned out concurrently. For independent parallel reads, use separate scopes.
The compiled [read-scope example](../../gotype/example_read_scope_test.go) and
its [live integration test](../../gotype/integ_query_test.go) exercise this API.

The [read-scope measurements](../../benchmarks/READ_SCOPE.md) compare fresh
and shared transactions on the current server and fixture. The complete-scope
benchmark includes transaction open and caller-visible close in both cases;
the per-read variant deliberately excludes the shared scope's boundaries.

These are Go allocation figures, not Rust/server memory. The driver's close
may finish native cleanup asynchronously, so this does not measure close-drain
time. The small sample and background host load make ratios directional; run
`BenchmarkLiveReadScope` and `BenchmarkLiveReadScopePerRead` with the integration
tags in five independent processes on your own workload before choosing scope
size. Per-read measurements that exclude the initial open and final close
overstate the benefit of reuse for short scopes.

When *all* matching models need the same values, use the existing uniform-update
API, `Query.Update`, instead of fetching each model and calling `Manager.Update`
separately. For example, filter by status and pass
`map[string]any{"status": "active"}` to `Query.Update`.
Use `UpdateWith` only when the new value depends on each fetched model.

## Database

`Database` wraps a `Conn` with a database name and provides convenience methods for executing queries:

```go
db := gotype.NewDatabase(conn, "my_db")

// Or with a connection pool for concurrent access
db, err := gotype.NewDatabaseWithPool(config, "my_db", connFactory)
```

Key methods: `ExecuteRead`, `ExecuteWrite`, `ExecuteSchema`, `Schema` (returns current TypeQL schema), `Begin` (opens a `TransactionContext`), `Transaction` (opens a raw `Tx`).

`EnsureDatabase` is a convenience that checks existence and creates if needed:

```go
created, err := gotype.EnsureDatabase(ctx, conn, "my_db")
```

## Connection Pool

For concurrent access, `ConnPool` manages a pool of `Conn` instances:

```go
pool, err := gotype.NewConnPool(gotype.DefaultPoolConfig(), connFactory)
conn, err := pool.Get(ctx)
defer pool.Put(conn)
```

`PoolConfig` controls min/max size, idle timeout, and wait timeout. Use `pool.Stats()` to inspect pool state.

## Conn and Tx Interfaces

The ORM is decoupled from the driver via interfaces. The `driver.Driver` satisfies `Conn` and `driver.Transaction` satisfies `Tx`, but you can provide your own implementations for testing or alternative backends. See [Testing Guide](../TESTING.md) for the mock pattern.

## Error Types

The `gotype` package defines structured error types for common failure modes:

- **NotRegisteredError** -- type not found in registry
- **KeyAttributeError** -- key attribute missing or zero-valued (returned by
  `Insert`/`InsertMany`/`Put`/`PutMany`)
- **HydrationError** -- failed to populate struct from query results (supports `Unwrap`)
- **NotFoundError** -- no matching entity/relation found (returned by `GetOne`)
- **NotUniqueError** -- multiple matches when one expected (returned by `GetOne`)
- **ReservedWordError** -- attribute/type name is a TypeQL reserved word
- **SchemaValidationError** -- schema definition is invalid
- **SchemaConflictError** -- conflicting schema definitions
- **MigrationError** -- migration execution failed (supports `Unwrap`)

## Complete Example

```go
gotype.Register[Person]()
gotype.Register[Company]()
gotype.Register[Employment]()

db := gotype.NewDatabase(conn, "my_db")
defer db.Close()

// Apply schema
gotype.MigrateFromEmpty(ctx, db)

// Insert
persons := gotype.MustNewManager[Person](db)
alice := &Person{Name: "Alice", Email: "alice@example.com"}
persons.Insert(ctx, alice)

companies := gotype.MustNewManager[Company](db)
acme := &Company{Name: "Acme Corp"}
companies.Insert(ctx, acme)

// Create relation
jobs := gotype.MustNewManager[Employment](db)
jobs.Insert(ctx, &Employment{
    Employee: alice,
    Employer: acme,
})

// Get with role players populated
rels, _ := jobs.GetWithRoles(ctx, nil)

// Upsert
persons.Put(ctx, &Person{Name: "Alice", Email: "alice@newdomain.com"})

// Batch delete with strict mode
persons.DeleteMany(ctx, []*Person{alice}, gotype.WithStrict())
```
