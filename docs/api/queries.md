# Query Builder

`import "github.com/CaliLuke/go-typeql/gotype"` -- [pkg.go.dev](https://pkg.go.dev/github.com/CaliLuke/go-typeql/gotype)

`Query[T]` provides a chainable builder for constructing TypeQL match-fetch queries with filters, sorting, pagination, aggregations, grouping, and bulk updates.

## Creating Queries

Start from a `Manager[T]`:

```go
persons := gotype.NewManager[Person](db)
q := persons.Query()
```

## Filters

All filters implement the `Filter` interface. They generate TypeQL pattern strings injected into the match clause.

**Variable scoping gotcha**: Variable names use the format `$e__attr_name` (double underscore separator) to avoid TypeQL implicit equality semantics. Hyphens in attribute names are replaced with underscores in variable names.

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
gotype.Contains("email", "@example.com") // contains
gotype.Like("name", "Ali.*")             // like (regex)
gotype.Regex("email", "^[^@]+@.+")       // like (regex)
gotype.Startswith("name", "Al")           // like (prefix)
```

### Set Membership

```go
gotype.In("status", []any{"active", "pending"})    // or block with equality per value
gotype.NotIn("status", []any{"banned", "deleted"})  // wrapped in not block
```

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

### Computed Expressions

Use `Computed` with `ArithmeticExpr` and `BuiltinFuncExpr` to filter on computed values:

```go
// Filter where price * quantity > 100
gotype.Computed("total",
    gotype.ArithmeticExpr("e", "price", "*", "quantity"),
    ">", 100.0)

// Filter where abs(balance) > 1000
gotype.Computed("abs_bal",
    gotype.BuiltinFuncExpr("abs", "$e__balance"),
    ">", 1000.0)
```

`ArithmeticExpr` supports operators: `+`, `-`, `*`, `/`, `%`, `^`.

`BuiltinFuncExpr` wraps TypeQL built-in functions: `abs`, `ceil`, `floor`, `round`, `length`, `max`, `min`.

### Boolean Composition

```go
gotype.And(filter1, filter2)  // logical AND (nested ANDs are flattened)
gotype.Or(filter1, filter2)   // TypeQL disjunction { ... } or { ... }
gotype.Not(filter)            // not { ... } block
```

Multiple calls to `Filter()` on the same query are ANDed together.

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
first, err := q.First(ctx)              // limit 1, return first (nil if none)
count, err := q.Count(ctx)              // count of matches
exists, err := q.Exists(ctx)            // true if any match exists
deleted, err := q.Delete(ctx)           // delete all matches, return count
```

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
```

## Aggregations

Aggregation methods return `*AggregateQuery[T]` with its own `Execute` returning `float64`:

```go
avgAge, _ := persons.Query().
    Filter(gotype.Gt("age", 0)).
    Avg("age").
    Execute(ctx)
```

Available: `Sum`, `Avg`, `Min`, `Max`, `Median`, `Std`, `Variance`.

**TypeDB gotcha**: TypeDB uses `mean` (not `avg`) for average aggregation. The `Avg` method handles this mapping for you.

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

Call TypeDB schema functions (defined with `fun`) using `FunctionQuery`:

```go
fq := gotype.NewFunctionQuery(db, "get_user_score").
    Arg("Alice").
    Arg(42)

// Build the TypeQL string
query := fq.Build()
// let $result = get_user_score("Alice", 42);
// return $result;

// Or execute directly
results, err := fq.Execute(ctx)
```

Use `ArgRaw` for pre-formatted expressions like variable references:

```go
fq := gotype.NewFunctionQuery(db, "compute_total").
    ArgRaw("$x").
    Arg(1.5)
```

## Complete Example

```go
persons := gotype.NewManager[Person](db)
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
