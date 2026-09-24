# Query Builder

`import "github.com/CaliLuke/go-typeql/v3/gotype"` -- [pkg.go.dev](https://pkg.go.dev/github.com/CaliLuke/go-typeql/v3/gotype)

`Query[T]` provides a chainable builder for constructing TypeQL match-fetch queries with filters, sorting, pagination, aggregations, grouping, and bulk updates.

## Creating Queries

Start from a `Manager[T]`:

```go
persons := gotype.MustNewManager[Person](db)
q := persons.Query()
```

## Filters

All filters implement the `Filter` interface. The interface is sealed: only `gotype` implements it. The query builders compile filters through one variable allocator for each query, so you never write or read TypeQL variable names, and two distinct meanings never share a variable.

**Variable sharing**: filters on the same attribute in the same scope share one variable. `Gt("age", 1)` and `Lt("age", 5)` therefore apply to the same value. A filter inside `Or` or `Not` binds its own variable (see "Boolean Composition").

**Decimal comparison gotcha**: Filter combinators are context-free — they don't know
the attribute's value type, so `Eq("price", 0.1)` emits the double literal `0.1`,
which is not exactly equal to the stored `0.1dec` (a double holds the nearest binary
fraction). Ordering comparisons (`Gt`, `Lt`, ...) against `decimal` attributes work
fine, but exact equality on non-representable fractions can silently miss. For exact
decimal equality use `Manager.Get` / `GetOne` with a filters map — those paths know
the field metadata and emit `dec` literals (`has price 0.1dec`).

### Comparison Filters

```go
gotype.Eq("name", "Alice")    // ==
gotype.Neq("status", "inactive") // !=
gotype.Gt("age", 18)          // >
gotype.Gte("score", 90)       // >=
gotype.Lt("price", 100.0)     // <
gotype.Lte("priority", 3)     // <=
```

### String Filters

```go
gotype.Contains("email", "@example.com") // contains (literal substring)
gotype.Like("name", "Ali.*")             // like (regex)
gotype.Regex("email", "^[^@]+@.+")       // like (regex)
gotype.Startswith("name", "Al")           // literal prefix (metacharacters are escaped)
```

`Startswith` treats the prefix as a literal string — regex metacharacters are
quoted. Use `Like` or `Regex` when you want pattern semantics.

### Set Membership

```go
gotype.In("status", []any{"active", "pending"})    // or block with equality per value
gotype.NotIn("status", []any{"banned", "deleted"})  // wrapped in not block
```

An empty `In` (or `IIDIn` with no IIDs) matches nothing; an empty `NotIn` matches
everything.

### Range

```go
gotype.Range("age", 18, 65) // >= and <= combined
```

### Existence

```go
gotype.HasAttr("phone")    // attribute exists
gotype.NotHasAttr("phone") // attribute does not exist
```

### IID and Role Player

```go
gotype.ByIID("0x123")  // match by TypeDB internal ID

// Match multiple IIDs in a single OR query
gotype.IIDIn("0x123", "0x456", "0x789")

// For relations: filter by role player attributes
gotype.RolePlayer("employee", gotype.Eq("name", "Alice"))
```

### Input Validation

Filters and identifiers are validated before any query text is built: attribute
names must match `[a-zA-Z][a-zA-Z0-9_-]*`, IIDs must be `0x`-prefixed hex, and
filter values must be scalars. Misuse returns a descriptive error from
`Execute`/`Get`/`Count`/etc. instead of reaching the server (or panicking).
Every filter type also exposes `Validate() error` for early checking.

### Computed Expressions

`Computed` compares a typed expression with a value:

```go
// Filter where price * quantity > 100
gotype.Computed(gotype.Mul(gotype.Attr("price"), gotype.Attr("quantity")), ">", 100.0)

// Filter where abs(balance) > 1000
gotype.Computed(gotype.Abs(gotype.Attr("balance")), ">", 1000.0)
```

- `Attr(label)` is an attribute of the queried instance, or of the role player inside a `RolePlayer` filter. The compiler binds the attribute. You do not need a separate filter on it.
- `Literal(v)` is a scalar value.
- The operators are `Add`, `Sub`, `Mul`, `Div`, `Mod`, and `Pow`.
- The built-in functions are `Abs`, `Ceil`, `Floor`, `Round`, `Log10`, `Length`, `Max`, and `Min`. They emit the fully qualified TypeQL names, for example `std::math::abs` and `std::string::len`. These names need TypeDB server 3.13.6 or later. TypeDB 3.13.0 rejects them with a `[TQL03]` parse error. `Log10` exists only under its qualified name.

The compiler gives each `Computed` filter its own result variable.

### Boolean Composition

```go
gotype.And(filter1, filter2)  // logical AND (nested ANDs are flattened)
gotype.Or(filter1, filter2)   // TypeQL disjunction { ... } or { ... }
gotype.Not(filter)            // not { ... } block
```

Multiple calls to `Filter()` on the same query are ANDed together.

Each branch of `Or` and the body of `Not` is a child scope. Only the queried instance (or the role player of an enclosing `RolePlayer` filter) crosses into a child scope. Every other variable belongs to the scope that uses it. So `And(Gt("age", 1), Not(Eq("age", 5)))` means "some age value is more than 1, and no age value is 5". Sibling branches never share a variable.

The same filter tree always gives the same TypeQL text. The generated variable names are an internal detail and can change between versions.

## Sorting, Pagination

```go
q.OrderAsc("name")   // sort ascending
q.OrderDesc("age")   // sort descending
q.Limit(25)
q.Offset(50)
```

Sort attributes automatically get `has` patterns added to the match clause.

## Terminal Operations

```go
results, err := q.Execute(ctx)           // run query, return all matches
results, err := q.All(ctx)               // alias for Execute
first, err := q.First(ctx)              // first match (nil if none); doesn't mutate the builder
count, err := q.Count(ctx)              // count of distinct matching entities
exists, err := q.Exists(ctx)            // true if any match exists
deleted, err := q.Delete(ctx)           // delete all matches, return distinct-entity count
err := q.DeleteNoCount(ctx)              // delete all matches without a count query
```

`Count` and `Delete` count distinct entities, not answer rows — an entity matched
through several values of a multi-valued attribute counts once. Queries built from a
transaction-bound Manager (`MustNewManagerWithTx`) run inside that transaction: reads
see uncommitted writes, and write operations never auto-commit the bound transaction.

### Functional Update (UpdateWith)

Fetches all matches, applies a function to each, then writes all changes back in a single transaction:

```go
updated, err := persons.Query().
    Filter(gotype.Gt("age", 60)).
    UpdateWith(ctx, func(p *Person) {
        newAge := *p.Age + 1
        p.Age = &newAge
    })
```

### Bulk Attribute Update

Updates specific attributes on all matching instances using per-attribute delete-old/insert-new:

```go
count, err := persons.Query().
    Filter(gotype.Eq("status", "pending")).
    Update(ctx, map[string]any{"status": "active"})

err = persons.Query().
    Filter(gotype.Eq("status", "pending")).
    UpdateNoCount(ctx, map[string]any{"status": "active"})
```

`UpdateNoCount` and `DeleteNoCount` use the same filter and mutation query as
their count-returning counterparts. They skip the separate distinct-count
query and return only success or failure. No matches are a successful no-op.
An empty update map also does nothing. An unbound manager commits the mutation
in one write transaction. A transaction-bound manager leaves commit or rollback
to its caller. The count-free methods ignore `OrderAsc`, `OrderDesc`, `Offset`,
and `Limit`, just as `Update` and `Delete` do.

## Aggregations

Aggregation methods return `*AggregateQuery[T]` with its own `Execute` returning `float64`:

```go
avgAge, _ := persons.Query().
    Filter(gotype.Gt("age", 0)).
    Avg("age").
    Execute(ctx)
```

Available: `Sum`, `Avg`, `Min`, `Max`, `Median`, `Std`, `Variance`.

**TypeDB gotcha**: TypeDB uses `mean` (not `avg`) for average aggregation. The `Avg` method handles this mapping for you. TypeQL has no variance reducer, so `Variance` squares the result of `std`. TypeDB's `std` is the sample standard deviation, so `Variance` gives the sample variance.

`AggregateSpec.Fn` accepts `sum`, `mean`, `avg`, `min`, `max`, `median`, `std`, `variance`, and `count`. Other names return an error, because the name goes into the query text.

### Multi-Aggregation

Compute multiple aggregations in a single query:

```go
results, _ := persons.Query().
    Aggregate(ctx,
        gotype.AggregateSpec{Fn: "sum", Attr: "age"},
        gotype.AggregateSpec{Fn: "mean", Attr: "age"},
    )
// results["sum_age"] = 450.0, results["mean_age"] = 30.0
```

## GroupBy

Group results by an attribute and compute aggregations per group:

```go
results, _ := persons.Query().
    GroupBy("status").
    Aggregate(ctx,
        gotype.AggregateSpec{Fn: "count", Attr: "name"},
    )
// results["active"]["count_name"] = 5.0
// results["inactive"]["count_name"] = 2.0
```

## Function Queries

Use `FunctionQuery` to call a TypeDB schema function (defined with `fun`). It can also call a fully qualified built-in function, such as `std::math::log10`.

```go
fq := gotype.NewFunctionQuery(db, "get_user_score").
    Arg("Alice").
    Arg(42)

// Build the TypeQL string
query := fq.Build()
// match
// let $result = get_user_score("Alice", 42);
// select $result;

// Or execute directly
results, err := fq.Execute(ctx)
```

Use `ArgRaw` for a pre-formatted TypeQL expression of constants. The query has no match patterns, so a variable reference such as `$x` is not bound, and the server rejects the query.

```go
fq := gotype.NewFunctionQuery(db, "compute_total").
    ArgRaw("2 + 3").
    Arg(1.5)
```

`FunctionQuery` supports functions that return a single value. For a function that returns a stream or a tuple, write the query and run it with `Database.ExecuteRead`.

## Complete Example

```go
persons := gotype.MustNewManager[Person](db)
ctx := context.Background()

// Complex query with filters, sorting, pagination
results, err := persons.Query().
    Filter(gotype.And(
        gotype.Gte("age", 18),
        gotype.Or(
            gotype.Contains("email", "@acme.com"),
            gotype.Contains("email", "@example.com"),
        ),
    )).
    OrderAsc("name").
    Limit(25).
    Offset(0).
    Execute(ctx)

// Functional update on filtered set
updated, err := persons.Query().
    Filter(gotype.Eq("status", "trial")).
    UpdateWith(ctx, func(p *Person) {
        s := "active"
        p.Status = &s
    })

// Aggregation with grouping
grouped, err := persons.Query().
    GroupBy("department").
    Aggregate(ctx,
        gotype.AggregateSpec{Fn: "mean", Attr: "age"},
        gotype.AggregateSpec{Fn: "count", Attr: "name"},
    )
```
