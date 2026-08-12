# Native Type Rename Migration Design

## Status

This design is implemented in `v1.15.0-alpha.1`.

The branch uses TypeDB `3.12.2`, `typedb-driver` `3.12.3`, and `typeql` `3.12.2`.
The framework does not yet use the native rename commands from TypeDB `3.12.2`.

The current `RenameAttribute` operation uses an old create-only workflow. This design keeps that operation unchanged for compatibility.

## Source Behavior

TypeDB `3.12.2` adds these schema commands:

```typeql
redefine entity old-person label person;
redefine relation old-employment label employment;
redefine attribute old-email label email;
redefine employment:old-employee label employee;
```

The TypeQL prefixes for entity, relation, and attribute are optional. The operations include them to make intent explicit.

The TypeDB release describes type and role renames as native schema changes. The TypeQL `3.12.2` grammar accepts all four forms.

Sources:

- [TypeDB 3.12.2 release](https://github.com/typedb/typedb/releases/tag/3.12.2)
- [TypeQL 3.12.2 release](https://github.com/typedb/typeql/releases/tag/3.12.2)
- [`typeql-reference/typeql.pest`](typeql-reference/typeql.pest)

## Goals

The feature will:

1. Expose native operations for entity, relation, attribute, and role renames.
2. Preserve existing instances, values, ownerships, role players, and schema capabilities.
3. Support rollback for each native rename operation.
4. Integrate rename operations with tracked sequential migrations.
5. Execute each tracked migration in one schema transaction.
6. Keep the existing `RenameAttribute` source and behavior compatible.
7. Add unit, syntax, integration, rollback, and atomicity tests.

## Non-Goals

The feature will not infer renames from ordinary schema diffs.

An added label and a removed label do not prove a rename. They can represent unrelated schema changes.

The feature will not rename functions or structs. TypeDB `3.12.2` does not include native commands for those definitions.

The feature will not add client-side data-copy queries. Native rename keeps the existing schema object and its data.

## Public API

### Rename Operations

Add one opaque operation type and four constructors to `gotype/migrate_ops.go`:

```go
type RenameOperation struct {
    // All fields are unexported.
}

func RenameEntity(oldName, newName string) RenameOperation
func RenameRelation(oldName, newName string) RenameOperation
func RenameAttributeType(oldName, newName string) RenameOperation
func RenameRole(relation, oldName, newName string) RenameOperation
```

`RenameAttributeType` has a distinct name because `RenameAttribute` is already public. The existing operation keeps its current create-only behavior.

`RenameOperation` will implement `Operation`. Its unexported fields make the operation set closed to package constructors.

```go
var _ Operation = RenameOperation{}
```

An interface with an unexported marker is not sufficient. External code can embed a package type and override its public methods.

| Operation | `ToTypeQL()` | `RollbackTypeQL()` |
| --- | --- | --- |
| `RenameEntity(...)` | `redefine entity old-person label person;` | `redefine entity person label old-person;` |
| `RenameRelation(...)` | `redefine relation old-employment label employment;` | `redefine relation employment label old-employment;` |
| `RenameAttributeType(...)` | `redefine attribute old-email label email;` | `redefine attribute email label old-email;` |
| `RenameRole(...)` | `redefine employment:old-employee label employee;` | `redefine employment:employee label old-employee;` |

All values from the four constructors will return these flags:

```go
IsReversible() == true
IsDestructive() == false
```

Rollback is conditional. It can fail after later schema drift or target-label reuse.

External code can construct the zero value as `RenameOperation{}`. The zero value is invalid but harmless:

```go
RenameOperation{}.ToTypeQL() == ""
RenameOperation{}.RollbackTypeQL() == ""
RenameOperation{}.IsReversible() == false
RenameOperation{}.IsDestructive() == false
```

`RenameMigration` will reject the zero value before it creates a migration.

### Compatibility for `RenameAttribute`

The existing `RenameAttribute` operation will not change in this release.

Changing its output from `define` to `redefine` can invalidate checksums for applied sequential migrations. It can also break callers that omit `OldName`.

Mark `RenameAttribute` as deprecated. Direct callers to `ToTypeQL()` will continue to receive the original create-only statement.

The compatibility test will continue to call this deprecated API. Run all lint gates after the deprecation comment is added.

If SA1019 flags that test, add a narrow `//nolint:staticcheck` directive with a compatibility-test explanation. Do not suppress SA1019 elsewhere.

The next major release can remove `RenameAttribute`. The migration guide will direct new code to `RenameAttributeType`.

### Rename-Based Sequential Migrations

Add this constructor to `gotype/seq_migrate.go`:

```go
func RenameMigration(name string, renames ...RenameOperation) (SequentialMigration, error)
```

The constructor returns an error for an empty name, no operations, or invalid labels.

The value-only parameter cannot contain nil operations. External code can construct only a zero value or a value from a package constructor.

The constructor will use parser-aligned validation for each type, relation, and role label. TypeDB will still reject missing sources and duplicate targets.

The constructor will create `Up` statements in the given order. It will create `Down` statements in reverse order.

The constructor will use `TQLMigration` internally. Existing state tracking, checksums, previews, and rollback will then apply.

The opaque value type prevents destructive or arbitrary operations from entering this constructor.

Example:

```go
migration, err := gotype.RenameMigration(
    "20260812_native_schema_renames",
    gotype.RenameAttributeType("email", "email-address"),
    gotype.RenameEntity("user", "person"),
    gotype.RenameRole("employment", "worker", "employee"),
)
if err != nil {
    return err
}

migrations := []gotype.SequentialMigration{migration}
```

`WithSeqDryRun()` will display the native statements. `RollbackSequentialMigration` will execute the reverse statements.

## Schema Diff Behavior

`DiffSchema` will not guess rename pairs. Its existing add-and-remove result will remain unchanged before an explicit rename migration runs.

CAUTION: Run the explicit rename before any diff migration uses models with the new labels.

If migration runs first, the additive plan can create the target label. The later native rename then fails because that label exists.

After the rename, do not run a diff migration with models that use the old labels.

The old models can create fresh empty types with the old labels. They can also report the renamed types as removals.

The supported workflow has two steps:

1. Run a `RenameMigration` that changes the labels.
2. Run the diff-based migration with models that use the new labels.

After the rename, schema introspection will return the new labels. The following diff must not add or remove the renamed objects.

This workflow keeps user intent separate from structural comparison. It prevents an incorrect rename from merging unrelated types or data.

## Transaction and Ordering Rules

`RunSequentialMigrations` will execute all rename statements and its state record in one schema transaction.

`RollbackSequentialMigration` will execute all reverse statements and the state-record removal in one schema transaction.

If a statement fails, TypeDB will roll back the complete transaction.

Direct calls to the generated `Up` or `Down` function are not atomic. `TQLMigration` uses one transaction for each direct statement.

Operations execute in the caller order. Rollback operations execute in the reverse order.

If one migration renames a role and its relation, rename the role first. Then rename the relation.

Example forward order:

```typeql
redefine old-employment:old-worker label worker;
redefine relation old-employment label employment;
```

The generated rollback order is:

```typeql
redefine relation employment label old-employment;
redefine old-employment:worker label old-worker;
```

This order keeps the scoped role label valid in both directions.

### Chains, Swaps, and Reused Labels

Rename chains are order-sensitive. For `a -> b -> c`, rename `b` to `c` before renaming `a` to `b`.

A direct swap cannot work because each target already exists. Use a unique temporary label in three operations.

```typeql
redefine entity alpha label rename-temp-alpha;
redefine entity beta label alpha;
redefine entity rename-temp-alpha label beta;
```

The caller owns temporary-label selection. TypeDB rejects a collision and rolls back the tracked migration.

## Validation and Errors

`RenameMigration` will validate all input before it creates the migration.

The error will identify the operation index, field name, and invalid label.

Add an internal `validateTypeQLLabel` helper. It will check the identifier shape and all reserved TypeQL `3.12.2` keywords.

Align `TypeQLReservedWords` with `typeql::is_reserved_keyword` and the vendored grammar. Correct missing words such as `role`, `end`, and `last`.

Add a superset invariant test from the `reserved` rule in `typeql-reference/typeql.pest`.

Every word in that grammar rule must exist in `TypeQLReservedWords`. Extra Go words remain valid because validation is intentionally stricter.

Execution errors from `applySeqStatements` currently omit the failing statement index. Add index and statement context to that error.

The error change applies to all statement-based sequential migrations. It does not alter transaction behavior.

Reject a rename when its old and new labels are equal. This no-op has no useful migration behavior.

## Syntax Gate Plan

The published `typeql-check` release remains at `3.12.0`. That binary rejects the valid `3.12.2` rename grammar.

Add separate syntax subtests for each forward and rollback statement. Separate subtests prevent one temporary skip from hiding other cases.

Use a checker-lag marker that references a repository issue. The test must fail when a newer checker accepts the statement.

When a newer checker package exists, update `TYPEQL_CHECK_VERSION` in the `Makefile`. Then remove the temporary markers after the syntax tests pass.

Also parse each statement with this repository's direct `typeql` `3.12.2` Rust dependency.

Add `cargo test --manifest-path driver/rust/Cargo.toml` to the full gates. `make build-rust` does not run Rust tests.

The live integration tests remain the final syntax and behavior gate.

## Test Plan

### Unit Tests

Add exact-output tests for each new operation:

- Forward TypeQL.
- Rollback TypeQL.
- Reversible flag.
- Non-destructive flag.
- Harmless zero-value behavior.

Keep the existing `RenameAttribute` output test unchanged. Add a deprecation comment near that test.

Add `RenameMigration` tests for:

- Forward statement order.
- Reverse rollback order.
- Statement storage for dry-run output.
- Checksum stability.
- Empty migration name.
- Empty operation list.
- Invalid labels with legal characters, including reserved words.
- Invalid source, target, relation, and role labels.
- Compile-time exclusion of destructive operations through the opaque value type.
- Inability of an external package to construct or wrap a custom rename operation.
- Rejection of equal old and new labels.

### Routing and Error Tests

Make sure that `inferTxType` routes each rename statement to a schema transaction.

Make sure that tracked rename migrations use one schema transaction and one state record.

Make sure that direct `Up` and `Down` calls use the documented non-atomic path.

Make sure that a multi-statement error identifies the statement index.

No rename operation will enter `SchemaDiff.Operations()`. This test protects the no-inference decision.

### Live Integration Tests

Run every scenario against `typedb/typedb:3.12.2`.

#### Entity Rename

1. Define `old-person` with a supertype and annotation.
2. Insert one instance and record its IID.
3. Rename `old-person` to `person`.
4. Fetch the same IID through `person`.
5. Make sure that the supertype and annotation remain.
6. Make sure that `old-person` is absent from the schema.
7. Roll back the operation and fetch the same IID through `old-person`.

#### Attribute Rename

1. Define `old-email` with a value type and constrained ownership.
2. Insert one value and record its IID.
3. Rename `old-email` to `email`.
4. Fetch the same IID and value through `email`.
5. Make sure that the value type and ownership annotations remain.
6. Make sure that `old-email` is absent from the schema.
7. Roll back and fetch the same IID and value through `old-email`.

#### Relation Rename

1. Define `old-employment` with roles, ownership, and a supertype.
2. Insert one relation and record its IID.
3. Rename `old-employment` to `employment`.
4. Match the same IID through `employment`.
5. Make sure that roles, ownership, supertype, and player capabilities remain.
6. Make sure that `old-employment` is absent from the schema.
7. Roll back and match the same IID through `old-employment`.

#### Role Rename

1. Define `employment:old-employee` with cardinality and player capabilities.
2. Insert one relation and record the player IID.
3. Rename `old-employee` to `employee`.
4. Match the same player IID through `employee`.
5. Make sure that role cardinality and player capabilities remain.
6. Make sure that only the selected relation scope changed.
7. Roll back and match the same player IID through `old-employee`.

#### Combined Migration

Apply attribute, entity, role, and relation renames in one `RenameMigration`.

Make sure that all IIDs and data remain available through the new labels. Then roll back and make sure that all old labels work.

#### Forward Atomic Failure

Create a target-label collision in the second rename.

Make sure that the first rename did not commit. Also make sure that no migration state record exists.

#### Rollback Atomic Failure

Apply a two-operation migration. Then create a collision for the second reverse rename.

Make sure that no reverse rename commits. Also make sure that the applied migration state record remains.

#### Diff Convergence

Run the rename migration, then compare the live schema with models that use the new labels.

Make sure that the diff does not add or remove renamed types, ownerships, roles, or player capabilities.

#### Ordering and Boundary Cases

Test a rename chain, a temporary-label swap, rejected same-name input, and repeated role names in different relation scopes.

Test the failure that occurs when a diff-based migration creates the new label before the explicit rename.

Test the drift that occurs when old-label models run after a successful rename.

### Full Repository Gates

Run these commands after implementation:

```bash
make build-rust
make test-rust
go test ./ast/... ./gotype/... ./tqlgen/...
docker compose up -d
TEST_DB_ADDRESS=localhost:1730 TYPEDB_GO_COMPOSE_PORT_MAP=1 \
  go test -tags "cgo,typedb,integration" ./driver/... ./gotype/...
./check.sh
```

Add a `test-rust` Make target for the Cargo test command. Keep `check.sh` focused on its documented Go unit scope.

Add `make test-rust` to the `/release-checks` workflow. The release workflow is the complete Rust test entry point.

## Documentation Changes

Update `docs/api/schema.md` with:

- The opaque native operation type and its four constructors.
- The `RenameMigration` constructor and its errors.
- A tracked rename and rollback example.
- The explicit two-step workflow for schema diffs.
- The warning about running the rename first.
- The warning about using old-label models after the rename.
- The reason that automatic inference is unsafe.
- The deprecation and unchanged behavior of `RenameAttribute`.
- The rollback limitation after later schema drift.

Add full Go doc comments for every exported type and function.

Update the release notes with the new API and the old-operation deprecation.

Regenerate `docs/api/reference/gotype.md` after the exported API and comments change.

Update `docs/SKILL.md` with the explicit rename workflow. Audit `.agents/skills/*` and update each affected repository skill.

## Implementation Order

1. Add the opaque rename type and four native constructors.
2. Add operation output, rollback, flag, and validation tests.
3. Deprecate `RenameAttribute` without changing its output, then run the lint gates.
4. Add `RenameMigration` and its unit tests.
5. Add statement-index context to sequential migration errors.
6. Add separate TypeQL syntax cases and the checker-lag marker.
7. Add Rust parser tests for the four command forms.
8. Add the live integration scenarios.
9. Add the `test-rust` target and update `/release-checks`.
10. Update the migration guide, agent docs, repository skills, and release notes.
11. Regenerate the Go API reference.
12. Run the full repository gates.

## Acceptance Criteria

The feature is complete when all these statements are true:

- The public API represents all four native rename commands.
- Every native rename operation is reversible and non-destructive.
- The existing `RenameAttribute` output and migration checksums remain stable.
- `RenameMigration` accepts only validated opaque rename operations.
- The zero `RenameOperation` is harmless and rejected by `RenameMigration`.
- The Go reserved-word list includes every word from the TypeQL grammar rule.
- Tracked rename migrations are atomic and support rollback.
- Existing IIDs and data remain available after each rename and rollback.
- Schema capabilities follow renamed relation, attribute, and role labels.
- A failed forward or rollback transaction leaves no partial schema state.
- A post-rename schema diff converges without add-or-remove churn.
- The unit, Rust, syntax, integration, and quality gates pass.

## Main Risks

The main risk is accidental rename inference. This design requires explicit operations in tracked migrations.

Another risk is incorrect model order. New-label models cannot run before the rename, and old-label models cannot run after it.

The last risk is checker lag. Rust parser tests and live server tests cover syntax until a matching checker package exists.
