# CRUD Operations

`import "github.com/CaliLuke/go-typeql/gotype"` -- [pkg.go.dev](https://pkg.go.dev/github.com/CaliLuke/go-typeql/gotype)

The `Manager[T]` generic type provides Insert, Get, Update, Delete, Put (upsert), and batch operations for registered TypeDB models.

## Setup

```go
db := gotype.NewDatabase(conn, "my_db")
persons := gotype.NewManager[Person](db)
```

`NewManager` panics if type `T` has not been registered via `Register[T]()`.

## Insert

Inserts a new entity or relation. After a successful insert, the instance's IID is populated automatically (if it has key fields).

```go
alice := &Person{Name: "Alice", Email: "alice@example.com"}
err := persons.Insert(ctx, alice)
// alice.GetIID() is now set (e.g., "0x826e80018000000000000001")
```

`InsertMany` inserts multiple instances in a single write transaction. IIDs are populated via a follow-up read transaction after the batch commit.

## Get

Returns instances matching attribute filters. Filter keys are TypeDB attribute names. Pass `nil` to retrieve all instances (equivalent to `All()`).

```go
results, err := persons.Get(ctx, map[string]any{"name": "Alice"})
```

Other retrieval methods:

- `All(ctx)` -- shorthand for `Get(ctx, nil)`
- `GetByIID(ctx, iid)` -- fetch by TypeDB internal ID, returns nil if not found
- `GetByIIDPolymorphic(ctx, iid)` -- also returns the actual TypeDB type label
- `GetByIIDPolymorphicAny(ctx, iid)` -- hydrates as the concrete subtype (returns `any`)
- `GetWithRoles(ctx, filters)` -- for relations, populates role player entities

```go
// Get relations with role players populated
jobs := gotype.NewManager[Employment](db)
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

`DeleteMany` deletes multiple instances in a single write transaction, also supporting strict mode.

## Put (Upsert)

Inserts if the entity doesn't exist, updates if it does (matched by key attributes). After a successful put, the instance's IID is populated.

```go
persons.Put(ctx, &Person{Name: "Alice", Email: "alice@newdomain.com"})
```

`PutMany` upserts multiple instances in a single transaction.

## Query Builder

`persons.Query()` returns a chainable query builder. See [Queries](queries.md) for the full guide.

## Transaction Context

For explicit transaction control across multiple managers, use `TransactionContext`:

```go
tc, err := db.Begin(gotype.WriteTransaction)
defer tc.Close()

persons := gotype.NewManagerWithTx[Person](tc)
companies := gotype.NewManagerWithTx[Company](tc)

persons.Insert(ctx, &Person{Name: "Alice"})
companies.Insert(ctx, &Company{Name: "Acme"})

err = tc.Commit() // Both inserts in one transaction
```

Transaction types: `ReadTransaction` (0), `WriteTransaction` (1), `SchemaTransaction` (2).

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
- **KeyAttributeError** -- required key attribute is missing
- **HydrationError** -- failed to populate struct from query results (supports `Unwrap`)
- **NotFoundError** -- no matching entity/relation found
- **NotUniqueError** -- multiple matches when one expected
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
persons := gotype.NewManager[Person](db)
alice := &Person{Name: "Alice", Email: "alice@example.com"}
persons.Insert(ctx, alice)

companies := gotype.NewManager[Company](db)
acme := &Company{Name: "Acme Corp"}
companies.Insert(ctx, acme)

// Create relation
jobs := gotype.NewManager[Employment](db)
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
