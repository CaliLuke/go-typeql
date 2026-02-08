# Models

`import "github.com/CaliLuke/go-typeql/gotype"` -- [pkg.go.dev](https://pkg.go.dev/github.com/CaliLuke/go-typeql/gotype)

Models are Go structs that map to TypeDB entities and relations. They use struct tags to define attribute names, annotations, and role players.

## Defining Entities

Embed `gotype.BaseEntity` and tag each exported field with `typedb:"..."`:

```go
type Person struct {
    gotype.BaseEntity
    Name  string `typedb:"name,key"`
    Email string `typedb:"email,unique"`
    Age   *int   `typedb:"age"`
}
```

- `BaseEntity` provides the `Entity` marker interface plus `GetIID()` / `SetIID()` methods.
- The TypeDB type name is derived from the Go struct name in kebab-case (`UserAccount` becomes `user-account`).
- Pointer fields are optional attributes. Non-pointer fields are required.

## Defining Relations

Embed `gotype.BaseRelation`. Role player fields use `role:name` tags; attribute fields use the same syntax as entities:

```go
type Employment struct {
    gotype.BaseRelation
    Employee  *Person  `typedb:"role:employee"`
    Employer  *Company `typedb:"role:employer"`
    StartDate *string  `typedb:"start-date"`
}
```

- `BaseRelation` provides the `Relation` marker interface plus `GetIID()` / `SetIID()`.
- Role player fields must be pointers to registered entity types.

## Struct Tag Reference

Tags follow the format `typedb:"name[,option1][,option2]..."`:

| Tag            | Example                     | Description                           |
| -------------- | --------------------------- | ------------------------------------- |
| attribute name | `typedb:"name"`             | Maps field to a TypeDB attribute      |
| `key`          | `typedb:"name,key"`         | `@key` annotation (unique identifier) |
| `unique`       | `typedb:"email,unique"`     | `@unique` annotation                  |
| `card=M..N`    | `typedb:"items,card=0..5"`  | Cardinality constraint                |
| `role:name`    | `typedb:"role:employee"`    | Role player in a relation             |
| `abstract`     | `typedb:"abstract"`         | Marks the type as abstract            |
| `type:name`    | `typedb:"type:custom_name"` | Overrides the TypeDB type name        |
| `-`            | `typedb:"-"`                | Skip this field                       |

Cardinality formats: `0..1`, `1..5`, `2..` (unbounded max), `0+` (shorthand for `0..`).

## Go Type to TypeDB Value Type Mapping

| Go Type                   | TypeDB Value Type |
| ------------------------- | ----------------- |
| `string`                  | `string`          |
| `bool`                    | `boolean`         |
| `int`, `int8`..`int64`    | `long`            |
| `uint`, `uint8`..`uint64` | `long`            |
| `float32`, `float64`      | `double`          |
| `time.Time`               | `datetime`        |

## Registration

All model types must be registered before use. Registration extracts metadata via reflection and stores it in a global registry. Reserved TypeQL keywords (111 words like `define`, `match`, `entity`, etc.) are rejected during registration.

```go
// Register returns an error if the type is invalid
err := gotype.Register[Person]()

// MustRegister panics on error (convenient for init())
gotype.MustRegister[Person]()
```

The registry is global and shared. In tests, call `ClearRegistry()` and re-register per test since other tests may clear it.

Lookup functions let you find registered types by TypeDB name, Go type, or Go struct name. `SubtypesOf` and `ResolveType` support polymorphic type hierarchies.

## Hydration

Hydration populates struct fields from `map[string]any` data returned by TypeDB queries:

```go
// Populate an existing struct
err := gotype.Hydrate(&person, data)

// Create and populate in one step
person, err := gotype.HydrateNew[Person](data)

// Polymorphic: uses the "_type" field in data to pick the concrete type
instance, err := gotype.HydrateAny(data)
```

Hydration handles nested role player structs recursively, with a depth limit of 10 (`MaxHydrationDepth`) to prevent infinite loops when the database graph contains cycles.

## Serialization

Convert between struct instances and `map[string]any`:

```go
alice := &Person{Name: "Alice", Email: "alice@example.com"}

// Struct -> map
dict, err := gotype.ToDict(alice)
// {"name": "Alice", "email": "alice@example.com"}

// Map -> struct
person, err := gotype.FromDict[Person](dict)
```

Generate TypeQL strings directly from struct instances:

```go
alice := &Person{Name: "Alice", Email: "alice@example.com"}

insertQL, err := gotype.ToInsertQuery(alice)
// insert $e isa person, has name "Alice", has email "alice@example.com";

matchQL, err := gotype.ToMatchQuery(alice)
// match $e isa person, has name "Alice";
```

`ToMatchQuery` uses key fields only. Both require the type to be registered.

## Polymorphism

For type hierarchies (using `sub`), the registry supports polymorphic operations:

```go
// Get all registered subtypes of "artifact"
subtypes := gotype.SubtypesOf("artifact")

// Resolve a type label returned by TypeDB
info, ok := gotype.ResolveType("task")
```

See `GetByIIDPolymorphic` and `GetByIIDPolymorphicAny` in the [CRUD docs](crud.md) for fetching instances polymorphically.

## Key Internals

- **ModelInfo** holds all extracted metadata for a registered type: Go type, kind (entity/relation), TypeDB name, fields, roles, key fields. You can look up fields by Go name or TypeDB attribute name.
- **ModelStrategy** is the internal strategy pattern (`entityStrategy` / `relationStrategy`) that builds TypeQL strings for different type kinds. You don't interact with it directly.
- **Reserved words**: 111 TypeQL keywords are checked case-insensitively during registration. Using one as a type or attribute name produces a `ReservedWordError`.
