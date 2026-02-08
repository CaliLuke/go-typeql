---
name: go-typeql
description: Use the go-typeql Go ORM for TypeDB 3.x. Covers defining entities, relations, and attributes via struct tags, CRUD operations with Manager[T], query builder with filters, schema generation, migration, and code generation with tqlgen. Use when working with TypeDB in Go projects.
---

# go-typeql: Go ORM for TypeDB 3.x

go-typeql is a struct-tag driven ORM for TypeDB that provides type-safe abstractions over TypeQL. It wraps the Rust TypeDB driver via CGo FFI, but the ORM packages (`gotype/`, `ast/`, `tqlgen/`) compile and test without CGo.

## Quick Start

```go
import "github.com/CaliLuke/go-typeql/gotype"

// 1. Define models with struct tags
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
    StartDate *string `typedb:"start-date"`
}

// 2. Register models
gotype.MustRegister[Person]()
gotype.MustRegister[Company]()
gotype.MustRegister[Employment]()

// 3. Connect and set up database
db := gotype.NewDatabase(conn, "mydb")
defer db.Close()

// 4. Apply schema
ctx := context.Background()
schema := gotype.GenerateSchema()
db.ExecuteSchema(ctx, schema)

// 5. CRUD operations
persons := gotype.NewManager[Person](db)
persons.Insert(ctx, &Person{Name: "Alice", Email: "alice@example.com"})

results, _ := persons.Query().Filter(gotype.Eq("name", "Alice")).Execute(ctx)
```

---

## Defining Models

### Entities

Embed `gotype.BaseEntity` and use `typedb` struct tags to map fields to TypeDB attributes.

```go
type Person struct {
    gotype.BaseEntity
    // Key attribute (required, unique identifier)
    Name string `typedb:"name,key"`

    // Unique attribute
    Email string `typedb:"email,unique"`

    // Optional attribute (pointer = nullable)
    Age *int `typedb:"age"`

    // Multi-valued attribute with cardinality
    Tags []string `typedb:"tag,card=0.."`

    // Cardinality with bounds
    Phones []string `typedb:"phone,card=1..3"`
}

// Abstract entity (use type: override for TypeDB name)
type Artifact struct {
    gotype.BaseEntity
    _ byte `typedb:"type:artifact,abstract"`
    Name string `typedb:"name,key"`
}

// Inherited entity (Go embedding = TypeDB subtyping)
type Document struct {
    Artifact
    Content string `typedb:"content"`
}
```

### Relations

Embed `gotype.BaseRelation` and use `role:` tags for role players.

```go
type Employment struct {
    gotype.BaseRelation
    Employee *Person  `typedb:"role:employee"`
    Employer *Company `typedb:"role:employer"`

    // Relations can own attributes
    StartDate *string `typedb:"start-date"`
    EndDate   *string `typedb:"end-date"`
}

// Relation with multi-player roles
type IsSimilarTo struct {
    gotype.BaseRelation
    Memory1 *Memory `typedb:"role:similar-memory"`
    Memory2 *Memory `typedb:"role:similar-memory"`
}
```

### Struct Tags Reference

| Tag                      | TypeQL                 | Meaning                            |
| ------------------------ | ---------------------- | ---------------------------------- |
| `typedb:"name"`          | `has name`             | Attribute named "name"             |
| `typedb:"name,key"`      | `owns name @key`       | Key attribute (unique identifier)  |
| `typedb:"email,unique"`  | `owns email @unique`   | Unique constraint                  |
| `typedb:"age,card=0..1"` | `owns age @card(0..1)` | Optional with explicit cardinality |
| `typedb:"tag,card=0.."`  | `owns tag @card(0..)`  | Zero or more                       |
| `typedb:"role:employee"` | `relates employee`     | Role player in a relation          |
| `typedb:"type:my-type"`  | N/A                    | Override the TypeDB type name      |
| `typedb:"abstract"`      | `@abstract`            | Abstract type                      |
| `typedb:"-"`             | N/A                    | Skip field                         |

### Naming Convention

Go struct names are automatically converted to kebab-case for TypeDB type names:

- `UserAccount` becomes `user-account`
- `MigratedPerson` becomes `migrated-person`

Any hand-written TypeQL must use the kebab-case form, not the Go name.

---

## Registry

All model types must be registered before use. The registry is global and thread-safe.

```go
// Register with error handling
err := gotype.Register[Person]()

// Register with panic on error (for init)
gotype.MustRegister[Person]()

// Lookup by TypeDB name
info, ok := gotype.Lookup("person")

// Lookup by Go type
info, ok := gotype.LookupType(reflect.TypeOf(Person{}))

// Get all registered types
allTypes := gotype.RegisteredTypes()

// Get subtypes of a parent type
subtypes := gotype.SubtypesOf("artifact")
```

**Important for tests**: The registry is global and shared across tests. Always call `gotype.ClearRegistry()` and re-register needed types at the start of each test.

```go
func TestSomething(t *testing.T) {
    gotype.ClearRegistry()
    gotype.MustRegister[Person]()
    // ... test code
}
```

---

## CRUD Operations

### Manager[T]

`Manager[T]` is the generic CRUD entry point. Create one per model type.

```go
db := gotype.NewDatabase(conn, "mydb")
persons := gotype.NewManager[Person](db)
```

### Insert

```go
ctx := context.Background()

// Insert single instance (IID is populated on success)
alice := &Person{Name: "Alice", Email: "alice@example.com"}
err := persons.Insert(ctx, alice)
// alice.GetIID() is now set

// Insert multiple in a single transaction
err := persons.InsertMany(ctx, []*Person{
    {Name: "Bob", Email: "bob@example.com"},
    {Name: "Carol", Email: "carol@example.com"},
})
```

### Get / All

```go
// Get all instances
all, err := persons.All(ctx)

// Get with attribute filters
results, err := persons.Get(ctx, map[string]any{"name": "Alice"})

// Get by internal ID
person, err := persons.GetByIID(ctx, "0x1e00000000000000000123")

// Get with role players populated (for relations)
emps := gotype.NewManager[Employment](db)
results, err := emps.GetWithRoles(ctx, nil)
```

### Update

```go
// Update a fetched instance (must have IID)
alice.Age = intPtr(31)
err := persons.Update(ctx, alice)

// Update multiple instances
err := persons.UpdateMany(ctx, []*Person{alice, bob})
```

### Delete

```go
// Delete by IID
err := persons.Delete(ctx, alice)

// Strict delete (errors if instance doesn't exist)
err := persons.Delete(ctx, alice, gotype.WithStrict())

// Delete multiple
err := persons.DeleteMany(ctx, []*Person{alice, bob})
```

### Put (Upsert)

```go
// Insert or update by key
err := persons.Put(ctx, &Person{Name: "Alice", Email: "newalice@example.com"})

// Put multiple
err := persons.PutMany(ctx, []*Person{...})
```

---

## Query Builder

The query builder provides a chainable API for constructing TypeDB queries.

```go
persons := gotype.NewManager[Person](db)
q := persons.Query()
```

### Filters

```go
// Equality
q.Filter(gotype.Eq("name", "Alice"))

// Comparison
q.Filter(gotype.Gt("age", 18))
q.Filter(gotype.Gte("age", 18))
q.Filter(gotype.Lt("age", 65))
q.Filter(gotype.Lte("age", 65))
q.Filter(gotype.Neq("status", "inactive"))

// String operations
q.Filter(gotype.Contains("name", "Ali"))
q.Filter(gotype.Like("name", "^A.*"))
q.Filter(gotype.Startswith("name", "Al"))
q.Filter(gotype.Regex("email", ".*@example\\.com"))

// Set membership
q.Filter(gotype.In("status", []any{"active", "pending"}))
q.Filter(gotype.NotIn("role", []any{"admin", "superuser"}))

// Range (inclusive)
q.Filter(gotype.Range("age", 18, 65))

// Existence
q.Filter(gotype.HasAttr("email"))
q.Filter(gotype.NotHasAttr("deleted-at"))

// By internal ID
q.Filter(gotype.ByIID("0x1e00000000000000000123"))
```

### Boolean Combinators

```go
// AND (multiple Filter calls are implicitly ANDed)
q.Filter(gotype.Gte("age", 18)).Filter(gotype.Lt("age", 65))

// Explicit AND
q.Filter(gotype.And(gotype.Eq("name", "Alice"), gotype.Gt("age", 25)))

// OR
q.Filter(gotype.Or(
    gotype.Eq("name", "Alice"),
    gotype.Eq("name", "Bob"),
))

// NOT
q.Filter(gotype.Not(gotype.Eq("status", "inactive")))
```

### Role Player Filters

Filter relations by properties of their role players:

```go
emps := gotype.NewManager[Employment](db)
q := emps.Query()
q.Filter(gotype.RolePlayer("employee", gotype.Eq("name", "Alice")))
```

### Sorting, Pagination

```go
q.OrderAsc("name")
q.OrderDesc("age")
q.Limit(10)
q.Offset(20)
```

### Executing Queries

```go
// Get all results
results, err := q.Execute(ctx)  // or q.All(ctx)

// Get first result
first, err := q.First(ctx)

// Count matches
count, err := q.Count(ctx)

// Check existence
exists, err := q.Exists(ctx)

// Delete matching instances
deleted, err := q.Delete(ctx)
```

### Bulk Updates

```go
// Update all matching instances with a function
updated, err := q.UpdateWith(ctx, func(p *Person) {
    age := *p.Age + 1
    p.Age = &age
})

// Bulk attribute update (set values on all matches)
count, err := q.Update(ctx, map[string]any{"status": "archived"})
```

### Aggregations

```go
q := persons.Query()

// Single aggregation
avg, err := q.Avg("age").Execute(ctx)
sum, err := q.Sum("score").Execute(ctx)
min, err := q.Min("age").Execute(ctx)
max, err := q.Max("age").Execute(ctx)
med, err := q.Median("age").Execute(ctx)
std, err := q.Std("score").Execute(ctx)
v, err := q.Variance("score").Execute(ctx)

// Multiple aggregations in one query
results, err := q.Aggregate(ctx,
    gotype.AggregateSpec{Attr: "age", Fn: "mean"},
    gotype.AggregateSpec{Attr: "score", Fn: "sum"},
)
// results["mean_age"], results["sum_score"]

// Group by
grouped, err := q.GroupBy("department").Aggregate(ctx,
    gotype.AggregateSpec{Attr: "salary", Fn: "mean"},
)
// grouped["Engineering"]["mean_salary"]
```

**Note**: TypeDB uses `mean` not `avg` for average aggregation. The `q.Avg()` method maps to `mean` internally.

---

## Transactions

### Automatic Transactions

By default, each Manager operation opens and commits its own transaction:

```go
persons.Insert(ctx, alice)  // opens write tx, inserts, commits
persons.All(ctx)            // opens read tx, fetches, closes
```

### Explicit Transaction Context

For multiple operations in a single transaction:

```go
tc, err := db.Begin(gotype.WriteTransaction)
if err != nil {
    return err
}
defer tc.Close()

personMgr := gotype.NewManagerWithTx[Person](tc)
companyMgr := gotype.NewManagerWithTx[Company](tc)

personMgr.Insert(ctx, &Person{Name: "Alice"})
companyMgr.Insert(ctx, &Company{Name: "Acme"})

err = tc.Commit() // or tc.Rollback()
```

### Raw Queries

```go
// Read query
results, err := db.ExecuteRead(ctx, `
    match $p isa person, has name $n;
    fetch { "name": $n };
`)

// Write query
results, err := db.ExecuteWrite(ctx, `
    insert $p isa person, has name "Alice";
`)

// Schema query
err := db.ExecuteSchema(ctx, `
    define
    attribute nickname, value string;
    entity person, owns nickname;
`)
```

---

## Database Management

```go
// Create a database
err := conn.DatabaseCreate("mydb")

// Check existence
exists, err := conn.DatabaseContains("mydb")

// Delete a database
err := conn.DatabaseDelete("mydb")

// List all databases
names, err := conn.DatabaseAll()

// Convenience: ensure database exists (create if not)
created, err := gotype.EnsureDatabase(ctx, conn, "mydb")
```

---

## Schema Generation

Generate TypeQL schema from registered Go models:

```go
gotype.MustRegister[Person]()
gotype.MustRegister[Company]()
gotype.MustRegister[Employment]()

schema := gotype.GenerateSchema()
// Returns a complete "define ..." TypeQL string

// Apply to database
err := db.ExecuteSchema(ctx, schema)

// Generate for a single model
info, _ := gotype.LookupType(reflect.TypeOf(Person{}))
schema := gotype.GenerateSchemaFor(info)
```

---

## Schema Migration

Diff Go models against a live database and apply changes:

```go
// Migrate diffs registered models against the live DB and applies changes.
// Returns the diff describing what was changed.
diff, err := gotype.Migrate(ctx, db)

// Inspect what changed
fmt.Println(diff.Summary())
// "add 2 attribute(s): nickname, phone; add 1 entity type(s)"

// For new databases with no existing schema:
err = gotype.MigrateFromEmpty(ctx, db)
```

The `SchemaDiff` struct contains:

- `AddAttributes` -- new attribute types
- `AddEntities` -- new entity types
- `AddRelations` -- new relation types
- `AddOwns` -- new attribute ownerships
- `AddRelates` -- new role declarations
- `RemoveOwns` -- ownerships in DB but not in code (warnings)
- `RemoveTypes` -- types in DB but not in code (warnings)

Only additive changes are applied automatically. Removals are flagged as warnings.

---

## Serialization

Convert between Go structs and maps/TypeQL:

```go
// Convert to map
data, err := gotype.ToDict[Person](alice)
// {"name": "Alice", "email": "alice@example.com", "age": 30}

// Create from map
person, err := gotype.FromDict[Person](data)

// Generate insert TypeQL
query, err := gotype.ToInsertQuery[Person](alice)
// "insert $e isa person, has name \"Alice\", has email \"alice@example.com\";"

// Generate match TypeQL
query, err := gotype.ToMatchQuery[Person](alice)
// "match $e isa person, has name \"Alice\";"
```

---

## Polymorphic Queries

Query abstract types to get all subtypes:

```go
type Animal struct {
    gotype.BaseEntity
    _ byte `typedb:"type:animal,abstract"`
    Name string `typedb:"name,key"`
}

type Dog struct {
    Animal
    Breed string `typedb:"breed"`
}

type Cat struct {
    Animal
    Indoor *bool `typedb:"indoor"`
}

gotype.MustRegister[Animal]()
gotype.MustRegister[Dog]()
gotype.MustRegister[Cat]()

animals := gotype.NewManager[Animal](db)

// Polymorphic get by IID - returns *Animal and the concrete type label
animal, typeLabel, err := animals.GetByIIDPolymorphic(ctx, someIID)
// typeLabel is "dog" or "cat"

// Get as concrete type (returns any)
instance, typeLabel, err := animals.GetByIIDPolymorphicAny(ctx, someIID)
if dog, ok := instance.(*Dog); ok {
    fmt.Println(dog.Breed)
}
```

---

## Code Generator (tqlgen)

Generate Go structs from existing TypeQL schema files:

```go
import "github.com/CaliLuke/go-typeql/tqlgen"

// Parse a TypeQL schema file
schema, err := tqlgen.ParseFile("schema.tql")

// Render Go code
code, err := tqlgen.Render(schema, "models")
// Writes Go structs with proper struct tags, BaseEntity/BaseRelation embedding
```

The parser handles `define` blocks including attributes, entities, relations, and struct types. Functions in the schema are stripped by a character-level scanner and extracted separately.

---

## Connection Interface

The `Conn` and `Tx` interfaces decouple the ORM from the driver:

```go
// Conn is the interface for a TypeDB connection.
type Conn interface {
    Transaction(dbName string, txType int) (Tx, error)
    Schema(dbName string) (string, error)
    DatabaseCreate(name string) error
    DatabaseDelete(name string) error
    DatabaseContains(name string) (bool, error)
    DatabaseAll() ([]string, error)
    Close()
    IsOpen() bool
}

// Tx is the interface for a TypeDB transaction.
type Tx interface {
    Query(query string) ([]map[string]any, error)
    QueryWithContext(ctx context.Context, query string) ([]map[string]any, error)
    Commit() error
    Rollback() error
    Close()
    IsOpen() bool
}
```

The `driver/` package provides the real implementation via Rust FFI. For unit tests, use mock implementations.

---

## Testing Patterns

### Mock Tx and Conn

Unit tests use `mockTx` with preset responses consumed in query order, and `mockConn` with a list of `mockTx` instances:

```go
func TestSomething(t *testing.T) {
    gotype.ClearRegistry()
    gotype.MustRegister[Person]()

    // mockTx has responses consumed in order per Query() call
    tx := &mockTx{
        responses: [][]map[string]any{
            // Response for first Query() call
            {{"name": map[string]any{"value": "Alice"}, "_iid": map[string]any{"value": "0x123"}}},
        },
    }
    conn := &mockConn{txs: []*mockTx{tx}}
    db := gotype.NewDatabase(conn, "testdb")

    mgr := gotype.NewManager[Person](db)
    // ... test CRUD operations
}
```

### Important Testing Notes

1. **Always clear the registry** at the start of each test with `gotype.ClearRegistry()` then re-register needed types. The registry is global and other tests may have cleared it.

2. **Mock responses are consumed in order** -- each `tx.Query()` call pops the next response from `responses`.

3. **`mockConn` provides one tx per `Transaction()` call** from its `txs` slice.

4. **No CGo needed for unit tests** -- `gotype/`, `ast/`, and `tqlgen/` packages are pure Go.

---

## Important Notes

1. **Kebab-case naming**: Go struct names are auto-converted to kebab-case (`UserAccount` becomes `user-account`). TypeQL queries must use the kebab-case form.

2. **Pointer fields are optional**: Use `*int`, `*string`, etc. for nullable TypeDB attributes. Non-pointer fields are required.

3. **IID required for Update/Delete**: Instances must have their IID set (from Insert, Get, or GetByIID) before calling Update or Delete.

4. **Role player matching**: When inserting relations, role player entities are identified by:
   - **IID (preferred)**: If the entity was fetched from DB and has IID set.
   - **Key attributes (fallback)**: Uses `key` tagged fields to match.

5. **Transaction types**:
   - `gotype.ReadTransaction` (0): For queries
   - `gotype.WriteTransaction` (1): For insert/update/delete
   - `gotype.SchemaTransaction` (2): For schema changes

6. **TypeDB uses `mean` not `avg`**: The ORM handles this mapping internally.

7. **Result unwrapping**: TypeDB wraps values as `{"value": X}`. The ORM unwraps these automatically via `unwrapResult`/`unwrapValue`.

8. **Reserved words**: TypeQL has 111 reserved keywords. The registry validates type names, attribute names, and role names against these and returns `ReservedWordError` if a conflict is found.
