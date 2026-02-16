# Schema Generation and Migration

`import "github.com/CaliLuke/go-typeql/gotype"`

go-typeql can generate TypeQL `define` statements from registered Go structs, migrate an existing database schema, track migration state in the database, represent migrations as discrete operations with rollback support, and run sequential file-based migrations for projects that manage schema via raw TypeQL.

## Schema Generation

### GenerateSchema

```go
func GenerateSchema() string
```

Generates a complete TypeQL `define` statement for all registered types. Returns an empty string if no types are registered.

```go
gotype.Register[Person]()
gotype.Register[Company]()

schema := gotype.GenerateSchema()
// define
//   attribute name, value string;
//   attribute email, value string;
//   attribute age, value long;
//   entity person,
//     owns name @key,
//     owns email @unique,
//     owns age;
//   entity company,
//     owns name @key;
```

### GenerateSchemaFor

```go
func GenerateSchemaFor(info *ModelInfo) string
```

Generates a `define` statement for a single type, including its required attribute declarations.

## Schema Migration

Migration compares registered Go structs against an existing database schema, computes the diff, and applies changes.

### Migrate

```go
func Migrate(ctx context.Context, db *Database) (*SchemaDiff, error)
```

End-to-end migration -- fetches the current schema automatically, compares with registered Go models, and applies additive changes.

### MigrateFromSchema

```go
func MigrateFromSchema(ctx context.Context, db *Database, currentSchemaStr string) (*SchemaDiff, error)
```

Migrate from an explicitly provided schema string.

### MigrateFromEmpty

```go
func MigrateFromEmpty(ctx context.Context, db *Database) error
```

Convenience for initial database setup (applies the full schema).

### MigrateWithState

```go
func MigrateWithState(ctx context.Context, db *Database) (*SchemaDiff, error)
```

Migration with in-database state tracking. Fetches the schema automatically and skips migrations that have already been applied.

### MigrateWithStateFromSchema

```go
func MigrateWithStateFromSchema(ctx context.Context, db *Database, currentSchemaStr string) (*SchemaDiff, error)
```

Migration with state tracking from an explicitly provided schema string. This is the recommended migration function for production use. It:

1. Ensures the migration tracking schema exists (idempotent)
2. Computes the diff between registered Go types and the current DB schema
3. Hashes the migration statements (SHA-256)
4. Checks if this exact migration was already applied
5. Applies the migration if needed
6. Records the migration as applied

### Manual Diff Inspection

For more control, use the diff functions directly:

```go
current, err := gotype.IntrospectSchemaFromString(schemaStr)
diff := gotype.DiffSchemaFromRegistry(current)

if !diff.IsEmpty() {
    fmt.Println(diff.Summary())
    for _, stmt := range diff.GenerateMigration() {
        fmt.Println(stmt)
    }
}
```

### DiffSchema

```go
func DiffSchema(desired *tqlgen.ParsedSchema, current *tqlgen.ParsedSchema) *SchemaDiff
```

Lower-level diff between two `ParsedSchema` values. See [Generator](generator.md) for `ParsedSchema` details.

### DiffSchemaFromRegistry

```go
func DiffSchemaFromRegistry(currentDB *tqlgen.ParsedSchema) *SchemaDiff
```

Compares the currently registered Go models against the provided database schema.

## SchemaDiff Type

```go
type SchemaDiff struct {
    AddAttributes []AttrChange    // New attribute types
    AddEntities   []TypeChange    // New entity types
    AddRelations  []TypeChange    // New relation types
    AddOwns       []OwnsChange    // New owns clauses on existing types
    AddRelates    []RelatesChange // New relates clauses on existing relations
    RemoveOwns    []OwnsChange    // Owns in DB not in Go (informational)
    RemoveTypes   []string        // Types in DB not in Go (informational)
}
```

### Supporting Change Types

```go
type AttrChange struct {
    Name      string
    ValueType string
}

type TypeChange struct {
    TypeQL string // The full 'define' statement for the type
}

type OwnsChange struct {
    TypeName  string
    Attribute string
    Annots    string // TypeQL annotations like @key or @card
}

type RelatesChange struct {
    TypeName string
    Role     string
    Card     string
}
```

### SchemaDiff Methods

| Method                           | Return Type        | Description                                       |
| -------------------------------- | ------------------ | ------------------------------------------------- |
| `IsEmpty()`                      | `bool`             | True if no differences exist                      |
| `Summary()`                      | `string`           | Human-readable summary of changes                 |
| `GenerateMigration()`            | `[]string`         | TypeQL statements (additive only)                 |
| `GenerateMigrationWithOpts(...)` | `[]string`         | TypeQL statements with options (e.g. destructive) |
| `Operations()`                   | `[]Operation`      | All changes as discrete Operation objects         |
| `DestructiveOperations()`        | `[]Operation`      | Only destructive (removal) operations             |
| `BreakingChanges()`              | `[]BreakingChange` | Detect breaking schema changes                    |
| `HasBreakingChanges()`           | `bool`             | Quick check for breaking changes                  |

### MigrateOption

```go
type MigrateOption func(*migrateConfig)

func WithDestructive() MigrateOption
```

## Migration Operations

Each schema change is represented as a discrete `Operation`:

```go
type Operation interface {
    ToTypeQL() string       // TypeQL statement to apply
    IsReversible() bool     // Can this be rolled back?
    IsDestructive() bool    // Does this remove data?
    RollbackTypeQL() string // TypeQL to undo (if reversible)
}
```

### Additive Operations (non-destructive, reversible)

| Type           | Fields                           | Description                               |
| -------------- | -------------------------------- | ----------------------------------------- |
| `AddAttribute` | `Name, ValueType`                | New attribute type definition             |
| `AddEntity`    | `Name, Parent, Abstract, TypeQL` | New entity type definition                |
| `AddRelation`  | `Name, Parent, Abstract, TypeQL` | New relation type definition              |
| `AddOwnership` | `Owner, Attribute, Annots`       | New `owns` clause on existing type        |
| `AddRole`      | `Relation, Role, Card`           | New `relates` clause on existing relation |

### Destructive Operations (irreversible)

| Type              | Fields             | Description                               |
| ----------------- | ------------------ | ----------------------------------------- |
| `RemoveAttribute` | `Name`             | Remove an attribute type                  |
| `RemoveEntity`    | `Name`             | Remove an entity type                     |
| `RemoveRelation`  | `Name`             | Remove a relation type                    |
| `RemoveOwnership` | `Owner, Attribute` | Remove an `owns` clause from a type       |
| `RemoveRole`      | `Relation, Role`   | Remove a `relates` clause from a relation |

All operation types implement the `Operation` interface with `ToTypeQL()`, `IsReversible()`, `IsDestructive()`, and `RollbackTypeQL()` methods.

### Using Operations

```go
diff := gotype.DiffSchemaFromRegistry(current)

// Inspect all operations
for _, op := range diff.Operations() {
    fmt.Printf("%s (destructive=%v, reversible=%v)\n",
        op.ToTypeQL(), op.IsDestructive(), op.IsReversible())
}

// Check for breaking changes before migrating
if diff.HasBreakingChanges() {
    for _, bc := range diff.BreakingChanges() {
        fmt.Printf("BREAKING: %s on %s -- %s\n", bc.Type, bc.Entity, bc.Detail)
    }
}
```

### Destructive Migrations

By default, `GenerateMigration()` only emits additive statements. Use `GenerateMigrationWithOpts` with `WithDestructive()` to include removals:

```go
// Additive only (safe)
stmts := diff.GenerateMigration()

// Include destructive operations (drops types/owns)
stmts := diff.GenerateMigrationWithOpts(gotype.WithDestructive())
```

### Breaking Changes

```go
type BreakingChange struct {
    Type   string // "removal", "type_change", "cardinality_change"
    Entity string // Affected type name
    Detail string // Human-readable description
}
```

## Migration State Tracking

Migration state is stored in TypeDB as `migration-record` entities, following the same pattern used by Django, Rails, Prisma, and the Python type-bridge.

### MigrationState

```go
type MigrationState struct{ /* unexported */ }

func NewMigrationState(db *Database) *MigrationState
func (ms *MigrationState) EnsureSchema(ctx context.Context) error
func (ms *MigrationState) IsApplied(ctx context.Context, hash string) (bool, error)
func (ms *MigrationState) Applied(ctx context.Context) ([]MigrationRecord, error)
func (ms *MigrationState) Record(ctx context.Context, hash, summary string) error
```

### MigrationRecord

```go
type MigrationRecord struct {
    Hash      string    // SHA-256 of migration statements
    Summary   string    // Human-readable summary
    AppliedAt time.Time // When applied
}
```

### HashStatements

```go
func HashStatements(stmts []string) string
```

Compute a deterministic SHA-256 hash for a set of migration statements.

## Introspection

### IntrospectSchemaFromString

```go
func IntrospectSchemaFromString(schemaStr string) (*tqlgen.ParsedSchema, error)
```

Parses a TypeQL schema string (as returned by `Conn.Schema()`) into a `tqlgen.ParsedSchema`. Returns an empty `ParsedSchema` for empty input (no error). See [Generator](generator.md) for `ParsedSchema` details.

## Sequential Migrations

For projects that manage schema via `.tql` files (or programmatic steps) rather than Go struct tags, use the sequential migration system. Modeled after goose/golang-migrate.

### SequentialMigration

```go
type SequentialMigration struct {
    Name string
    Up   func(ctx context.Context, db *Database) error
    Down func(ctx context.Context, db *Database) error // nil if not reversible
}
```

### TQLMigration

```go
func TQLMigration(name string, up []string, down []string) SequentialMigration
```

Creates a migration from raw TypeQL statements. Each statement is automatically routed to `ExecuteSchema` (for `define`/`undefine`/`redefine`) or `ExecuteWrite` (for everything else).

```go
migrations := []gotype.SequentialMigration{
    gotype.TQLMigration("001_create_person", []string{
        "define attribute name, value string;",
        "define entity person, owns name @key;",
    }, []string{
        "undefine entity person;",
        "undefine attribute name;",
    }),
    gotype.TQLMigration("002_seed_data", []string{
        `insert $p isa person, has name "Alice";`,
    }, nil),
}
```

### RunSequentialMigrations

```go
func RunSequentialMigrations(ctx context.Context, db *Database, migrations []SequentialMigration, opts ...SeqMigrationOption) ([]string, error)
```

Validates, sorts by name, and applies pending migrations. Returns names of applied migrations.

```go
applied, err := gotype.RunSequentialMigrations(ctx, db, migrations)
```

### Options

| Option                | Description                                         |
| --------------------- | --------------------------------------------------- |
| `WithSeqDryRun()`     | Validate and return pending names without executing |
| `WithSeqTarget(name)` | Stop after applying the named migration             |
| `WithSeqLogger(fn)`   | Callback for progress messages                      |

### ValidateSequentialMigrations

```go
func ValidateSequentialMigrations(migrations []SequentialMigration) []SeqValidationIssue
```

Pure validation (no DB). Checks for empty names, duplicates, nil `Up` functions, and unsorted order. Returns issues with severity `"error"` or `"warning"`.

### SeqMigrationStatus

```go
func SeqMigrationStatus(ctx context.Context, db *Database, migrations []SequentialMigration) ([]SeqMigrationInfo, error)
```

Returns applied/pending status for each migration.

### RollbackSequentialMigration

```go
func RollbackSequentialMigration(ctx context.Context, db *Database, migrations []SequentialMigration, steps int) ([]string, error)
```

Rolls back the last N applied migrations in reverse name order. Each migration must have a non-nil `Down` function.

### SeqMigrationError

```go
type SeqMigrationError struct {
    Name  string
    Cause error
}
```

Returned when a migration's `Up` or `Down` function fails. Supports `errors.Unwrap`.

### Sequential vs Struct-Diff Migration

| Feature         | Struct-diff (`Migrate`)        | Sequential (`RunSequentialMigrations`) |
| --------------- | ------------------------------ | -------------------------------------- |
| Schema source   | Go struct tags                 | Raw TypeQL statements                  |
| Data migrations | No                             | Yes                                    |
| Rollback        | Via `Operation.RollbackTypeQL` | Via `Down` function                    |
| Ordering        | Automatic from diff            | Explicit by name                       |
| State tracking  | Hash-based                     | Name-based                             |

## Migration Workflow Guide

### Which function to use

| Scenario                   | Function                     | Description                                                            |
| -------------------------- | ---------------------------- | ---------------------------------------------------------------------- |
| New database, first deploy | `MigrateFromEmpty`           | Applies the full schema. Fast, no diff needed.                         |
| Development iteration      | `Migrate`                    | Diffs against live DB, applies additive changes. No state tracking.    |
| Production deploys         | `MigrateWithState`           | Diffs + state tracking. Skips already-applied migrations. Recommended. |
| Custom schema source       | `MigrateWithStateFromSchema` | Same as above but you provide the schema string.                       |
| Dry run / inspection       | `DiffSchemaFromRegistry`     | Returns the diff without applying anything.                            |
| File-based migrations      | `RunSequentialMigrations`    | Ordered, named migrations from TypeQL statements. Supports rollback.   |

### Development workflow

During development, use `Migrate` for quick iteration:

```go
gotype.Register[Person]()
gotype.Register[Company]()

db := gotype.NewDatabase(conn, "dev_db")

// Add fields to your structs, then:
diff, err := gotype.Migrate(ctx, db)
fmt.Println(diff.Summary())
// "add 1 attribute(s): phone; add 1 owns: person owns phone"
```

### Production workflow

Use `MigrateWithState` to avoid re-applying migrations:

```go
diff, err := gotype.MigrateWithState(ctx, db)
if err != nil {
    log.Fatal(err)
}
if diff.IsEmpty() {
    log.Println("Schema up to date")
}
```

### Safe migration with breaking change detection

Inspect the diff before applying to catch removals or type changes:

```go
current, _ := gotype.IntrospectSchemaFromString(schemaStr)
diff := gotype.DiffSchemaFromRegistry(current)

if diff.HasBreakingChanges() {
    for _, bc := range diff.BreakingChanges() {
        log.Printf("BREAKING: %s on %s -- %s", bc.Type, bc.Entity, bc.Detail)
    }
    log.Fatal("Aborting migration due to breaking changes")
}

// Safe to apply
for _, op := range diff.Operations() {
    log.Printf("Applying: %s", op.ToTypeQL())
}
```

### Handling destructive changes

By default, migrations are additive only (new types, new attributes, new owns). Removals are reported in the diff but not applied. To include destructive operations:

```go
stmts := diff.GenerateMigrationWithOpts(gotype.WithDestructive())
```

This is intentionally opt-in. Destructive operations (`RemoveAttribute`, `RemoveEntity`, etc.) are irreversible and will drop data.
