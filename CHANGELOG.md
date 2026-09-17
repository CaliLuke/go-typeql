# Changelog

## Unreleased

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
