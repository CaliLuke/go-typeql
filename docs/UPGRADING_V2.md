# Upgrading to v2

Version 2 keeps the current API without compatibility shims. Existing applications can continue to use a v1 release.

## Imports and installation

1. Install the v2 module:

   ```sh
   go get github.com/CaliLuke/go-typeql/v2@v2.0.0
   ```

2. Add `/v2` to each package import:

   ```go
   import (
       "github.com/CaliLuke/go-typeql/v2/driver"
       "github.com/CaliLuke/go-typeql/v2/given"
       "github.com/CaliLuke/go-typeql/v2/gotype"
   )
   ```

3. Regenerate model code with the v2 generator:

   ```sh
   go run github.com/CaliLuke/go-typeql/v2/tqlgen/cmd/tqlgen -schema schema.tql -out models_gen.go -pkg models
   ```

4. Run `go mod tidy` and the application tests.

The module requires Go 1.27 or later. The driver targets TypeDB 3.13.0. Darwin builds require macOS 13 or later.

`go get` downloads source only. For the driver, install the v2 release archive or run `make build-rust` in the source checkout.
The [installation guide](../README.md#prebuilt-ffi-library) lists the archive names and build tags.

## Native schema renames

The old `gotype.RenameAttribute` struct is removed. Its implementation created a new attribute type without moving existing values.

Use `RenameAttributeType` with `RenameMigration` for a native rename that preserves existing data:

```go
migration, err := gotype.RenameMigration("rename-email", gotype.RenameAttributeType("old-email", "email"))
if err != nil {
    return err
}
_, err = gotype.RunSequentialMigrations(ctx, db, []gotype.SequentialMigration{migration})
return err
```

Native constructors also support entity, relation, and role renames. The [schema guide](api/schema.md#native-rename-operations) describes execution and rollback.

## Custom transactions

Custom implementations and mocks of `gotype.Tx` must implement these methods:

```go
QueryWithRows(query string, rows given.Rows) ([]map[string]any, error)
QueryWithContextAndRows(ctx context.Context, query string, rows given.Rows) ([]map[string]any, error)
```

The `given.Rows` interface exposes `MarshalGivenRows() ([]byte, error)`. Both `*driver.GivenRows` and `*given.TypedRows` satisfy this interface.
The driver methods that accept input rows now use this shared interface. Update method expressions and custom interfaces that require the old `*driver.GivenRows` signatures.

A nil interface or typed nil pointer means that no input rows were supplied. An empty row set remains an explicit input with zero rows.
Empty strings, `false`, and numeric zero remain typed values during encoding.

## Streaming callbacks

`Manager.ForEach`, `Manager.ForEachWithRoles`, and `Query.ForEach` deliver a separate model for each answer row. The callback can retain that model.
Return `gotype.ErrStopIteration` to stop successfully. Other callback errors retain their identity through `errors.Is`.

A driver callback can call `IsOpen`. A query, commit, rollback, or `CloseChecked` on the same active transaction returns `driver.ErrTransactionBusy`.
`Close` and `CloseAsync` defer native cleanup until the stream releases its handle. Cancellation can wait for an active callback to return.
Use a separate transaction for database work inside a callback. The [iteration guide](api/iteration.md) explains transaction ownership and custom-driver behavior.

## Optional read and write APIs

- `GetProjected` and `ExecuteProjected` return selected fields as read-only `ProjectedResult` values. They do not create partial mutable models.
- `UpdateNoCount` and `DeleteNoCount` skip the count query and return only an error. They do not apply sorting or pagination.
- Compatible bulk writes use typed input batches automatically, including through the bundled pool. Other model shapes retain their normal write behavior.
- `Driver.CleanupStats` exposes cleanup and native transaction capacity. Context-aware opens can wait for capacity or return on cancellation.

See the [API guides](api/README.md) for examples and result contracts.
