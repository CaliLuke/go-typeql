# Changelog

## Unreleased

### Breaking changes

- Sealed the `gotype.Filter` interface. Filters now compile through one variable allocator for each query (issue #138). Removed `Filter.ToPatterns`. Custom filter types are no longer supported. Compose the built-in filters instead.
- Replaced the string form of `Computed(varName, expr, op, value)` with typed expressions: `Computed(expr, op, value)`, with `Attr`, `Literal`, `Add`, `Sub`, `Mul`, `Div`, `Mod`, `Pow`, `Abs`, `Ceil`, `Floor`, `Round`, `Length`, `Max`, and `Min`. For example, write `Computed(Mul(Attr("price"), Attr("quantity")), ">", 100)`. Removed `ArithmeticExpr` and `BuiltinFuncExpr`.
- Changed generated variable names. Variable names are now an internal detail. In `or` and `not` blocks, attribute variables use `_s<i>` (for example `$e_s1__name`), not `_o<i>` or `_n<i>`. Count and aggregate queries use `$result0` for their output, not `$count` or `$result`.

### Fixes

- Fixed generated models that registered under the wrong TypeDB name. `tqlgen` now adds a `type:` tag when the Go name does not convert back to the schema label, for example `user_account`, `tag-2fa`, or `api-url`.
- Fixed role players with a `type:` tag. Relations now use the player's tag, not the kebab-case Go name, for hydration, projections, schema generation, and migrations.
- Fixed filters that used one TypeQL variable for two different meanings. TypeQL treats a shared variable as an equality, so these queries failed, matched nothing, or matched the wrong data. Examples: filters on `first-name` and `first_name`, a role named `e` (the relation had to play itself), and a `Computed` result that took the name of an attribute or of an aggregate output. Every variable now comes from the allocator, keyed by meaning.
- Fixed `not` and `or` scopes. A filter inside `Not` or `Or` now binds its own variables, and so do `NotIn` and `NotHasAttr`. For example, `And(Gt("age", 1), Not(Eq("age", 5)))` means "no age value is 5". Before, a variable of an outer filter could reach into a `not` body, and a `Computed` variable of an outer scope was renamed inside a branch.
- Fixed `Computed` filters that failed with "variable must be bound" unless another filter bound the same attribute. The compiler now binds each attribute of an expression.
- Fixed `RolePlayer` filters on role labels with `_`. They emitted a variable that started with `_`, which is not valid TypeQL.
- Fixed duplicate `has` bindings. A query now binds each attribute once in each scope. TypeDB evaluates each identical binding, so the duplicates made queries slower.
- Fixed `GroupBy(...).Aggregate`. It emitted `group`, which is not valid TypeQL (the keyword is `groupby`), and it read the group value from the wrong result key.
- Fixed `Variance`. It emitted a `variance` reducer, which TypeQL does not have. It now squares `std`, which gives the sample variance.
- Fixed aggregate function names that went into the query without validation. `Aggregate` and `GroupBy` now reject unknown names, and `GroupBy` now maps `avg` to `mean`.
- Fixed pooled transactions that returned their connection to the pool after a failed `Commit`, `Rollback`, or `CloseChecked` left them open. The pool could then close the connection, and the open transaction with it.

## v2.0.0 - 2026-09-16

### Breaking changes

- Changed the Go module path to `github.com/CaliLuke/go-typeql/v2`. Updated generated models to import the v2 package.
- Removed the create-only `RenameAttribute` operation. Use `RenameAttributeType` with `RenameMigration` for native renames.
- Added `QueryWithRows` and `QueryWithContextAndRows` to `gotype.Tx`. Custom transactions and mocks must implement both methods.
- Changed driver input-row parameters to the shared `given.Rows` interface.
- Required Go 1.27 or later and targeted TypeDB 3.13.0. Darwin builds require macOS 13 or later.

### Features

- Added selected-field reads with `GetProjected` and `ExecuteProjected`. Results distinguish omitted fields from missing values.
- Added typed callbacks with `ForEach`, `ForEachWithRoles`, and `ErrStopIteration`.
- Added `UpdateNoCount` and `DeleteNoCount` for mutations that do not need an affected-instance count.
- Added native entity, relation, attribute, and role renames with atomic tracked migrations and reverse-order rollback.
- Added per-driver cleanup metrics and bounded admission for native transaction handles.

### Performance

- Batched compatible inserts, upserts, and updates through typed input rows. Grouped deletes and strict existence checks.
- Fetched `Put` IIDs in the upsert query and stopped `Exists` after the first distinct match.
- Cached fetch projections and parser setup. Reused MessagePack field keys and avoided disabled diagnostic work.
- Indexed role-player lookups for large, deeply inherited schemas.
- Measured 2.12× average speed against `v1.15.0-alpha.3` across 12 database operations, with 53% less operation time.
- Measured 4.1× average bulk-write speed and about 60% fewer Go allocations across the full benchmark mix.
- These equal-weight geometric averages use five samples per version on one server. Background load makes small timing differences uncertain.

### Fixes

- Preserved empty strings and other zero values in driver and ORM input rows.
- Treated typed nil input rows as absent input.
- Preserved optional bulk-write support through pooled transactions.
- Allowed transaction inspection from streaming callbacks and rejected unsafe reentry with `ErrTransactionBusy`.
- Deferred close requests until streaming releases the native handle. Added regression tests for cancellation and callback cleanup.

See [Upgrading to v2](docs/UPGRADING_V2.md) and the [performance report](benchmarks/reviews/2026-09-16/release-comparison/README.md).

## v1.15.0-alpha.3 - 2026-08-27

- Exposed TypeQL `given` input rows through `gotype.Tx`, including context cancellation.
- Added the pure-Go `given.Rows` contract for driver-independent transaction interfaces.
- Updated the TypeDB server to `3.12.3`.

## v1.15.0-alpha.2 - 2026-08-12

- Removed the deprecated create-only `RenameAttribute` operation. Use `RenameAttributeType` with `RenameMigration`.

## v1.15.0-alpha.1 - 2026-08-12

- Added native entity, relation, attribute, and role rename operations.
- Added atomic tracked rename migrations with reverse-order rollback.
- Deprecated the create-only `RenameAttribute` operation without changing its output.
- Updated the TypeDB server to `3.12.2` and the Rust driver to `3.12.3`.
- Updated the build to Go `1.27rc2`, Rust `1.97.1`, and macOS `13` or later.
- Reduced allocations for result decoding, row encoding, and query-result streaming.
