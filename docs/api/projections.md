# Selected-field reads

Use `GetProjected` when a read needs fewer attributes than the registered model defines. The method returns `ProjectedResult` values, not model pointers.

```go
people, err := persons.GetProjected(ctx, map[string]any{"name": "Alice"}, gotype.Projection{
    Fields: []string{"name", "email"},
})
if err != nil {
    return err
}
email := people[0].Fields["email"]
if email.Present {
    fmt.Println(email.Value)
}
```

`Fields` uses TypeDB attribute names. An empty field list requests only the IID and concrete type label. Unknown or repeated names return an error before a query starts.

For a relation, use `Roles` to select role players and their attributes:

```go
jobs, err := employments.GetProjected(ctx, nil, gotype.Projection{
    Fields: []string{"since"},
    Roles: map[string][]string{
        "employee": {"name"},
        "employer": {},
    },
})
```

The empty `employer` list requests its IID and type label, with no attributes. An unrequested role does not appear in `Roles`. Each selected role uses the relation's normal role-link match.

`ProjectedResult.IID` and `TypeName` identify the concrete TypeDB instance. The type can be a subtype of the registered manager type. Selected attributes must belong to the registered manager or role-player model.

`Fields` contains only requested attributes. A requested but missing scalar has `Present == false` and `Value == nil`. An omitted attribute has no map entry. A requested slice has `Present == true`, even if it is empty. A present value uses the driver's decoded type. For example, an integer can appear as `float64`.

The projection result cannot be passed to `Manager.Update`, which accepts a full model pointer. Use a normal full-model read before a full-model update. This prevents omitted attributes from becoming zero-value writes.

For filters, sorting, and pagination, call `Query.ExecuteProjected`:

```go
page, err := persons.Query().OrderAsc("name").Offset(10).Limit(20).
    ExecuteProjected(ctx, gotype.Projection{Fields: []string{"name"}})
```

The fluent read uses the same filter, sort, offset, and limit stages as `Query.Execute`. A manager bound to a transaction keeps that transaction open for its caller. An unbound manager opens and closes a read transaction.

If a selected role is absent, the inner link match excludes that relation. Each returned row contains all selected roles. If a role has multiple players, the query can return multiple rows for one relation IID. Each result contains one role-player combination. Sorting and pagination apply to answer rows, not distinct relation IIDs.

Default `Get`, `All`, `GetWithRoles`, and `Query.Execute` still return full models. They do not use the selected-field result contract.
