# Typed iteration

Use `ForEach` to process a large read without keeping every hydrated model in a result slice:

```go
err := persons.ForEach(ctx, nil, func(person *Person) error {
    fmt.Println(person.Name)
    return nil
})
```

The manager accepts the same attribute-filter map as `Get`. For a fluent query, use `Query.ForEach`:

```go
err := persons.Query().Filter(gotype.Gt("age", 18)).OrderAsc("name").
    Offset(10).Limit(20).ForEach(ctx, func(person *Person) error {
        return send(person)
    })
```

Each callback receives a newly allocated model. You may retain it after the callback. A relation read that needs populated role players uses `ForEachWithRoles`, which has the same result shape as `GetWithRoles`.

Return `gotype.ErrStopIteration` to end the read successfully. A wrapped stop error also works. Any other callback error stops the read and is returned with its identity preserved, so `errors.Is` works. A cancelled context stops delivery and returns a cancellation error. A failure after earlier stream chunks can leave some callbacks already completed; iteration does not roll back their external effects.

An unbound manager opens and closes its read transaction. A manager bound to a transaction leaves that transaction open for its caller. The callback must not start a second query on the same bound transaction before it returns.

With the bundled driver, the ORM hydrates and delivers each row as it arrives. It does not retain a slice of all models or raw rows. A custom transaction without `QueryEachWithContext` remains supported: it first materializes all raw result rows through `QueryWithContext`, then hydrates and delivers models one at a time. In that fallback, early stop saves hydration work but cannot prevent raw-row materialization.

For a result slice, use `All`, `Get`, or `Query.Execute`. For a read-only subset of fields, use [selected-field reads](projections.md).
