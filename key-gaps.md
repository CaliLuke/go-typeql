# Key gaps in our Go version vs type-bridge

## Completed

1. ~~N-role relations~~ — RelSchemaCtx now uses `Roles []RoleCtx` slice
2. ~~minimal_role_players filters ancestors~~ — filterMostSpecific walks parent chain
3. ~~Naming splits on hyphens~~ — toRegistryConst uses splitName()
4. ~~Entity keys~~ — EntityKeys map in generated registry
5. ~~Abstract tracking~~ — EntityAbstract / RelationAbstract maps
6. ~~Relation parents~~ — RelationParents map
7. ~~Schema hash~~ — SHA256 from SchemaText config
8. ~~Convenience functions~~ — GetEntityKeys, IsAbstractEntity, GetRolePlayers, etc.
9. ~~DTO generator~~ — `tqlgen -dto` generates Out/Create/Patch structs (dto.go, dto_config.go)
10. ~~DTOConfig high-priority options~~ — BaseStructConfig, ExcludeEntities, IDFieldName, StrictOut,
    EntityFieldOverrides, SkipRelationOut all implemented
11. ~~SCHEMA_VERSION constant~~ — SchemaVersion string in RegistryConfig + template
12. ~~Annotations in registry~~ — EntityAnnotations, AttributeAnnotations, RelationAnnotations maps
    wired from ExtractAnnotations via SchemaText
13. ~~StrEnum-style types~~ — `TypedConstants` flag generates `type EntityType string` with typed consts
14. ~~GetRoleInfo convenience~~ — `GetRoleInfo(relationType, roleName)` for O(1)-like role lookup
15. ~~JSON schema fragments~~ — `JSONSchema` flag generates EntityTypeJSONSchema map
16. ~~Composite entities~~ — CompositeEntityConfig merges multiple entity types into flat DTO
17. ~~Entity union name~~ — configurable interface names (EntityOutName, etc.)
18. ~~Relation create base class~~ — RelationCreateEmbed for custom embed in relation Create DTOs

## All gaps closed

No remaining gaps between go-typeql and type-bridge generator features.

## Go 1.26 opportunities

Upgraded from Go 1.25.7 to Go 1.26.0. Key features we can leverage:

### High value

1. **`new()` with initial value** — Eliminates `intPtr()`/`strPtr()` helpers throughout tests and
   examples. Instead of `Age: intPtr(42)`, write `Age: new(42)`. Affects gotype/ tests heavily.
2. **`reflect.Type.Fields()` / `Value.Fields()` iterators** — Could simplify struct field iteration
   in `gotype/model.go` (model extraction) and `gotype/hydrate.go` (hydration). Replace manual
   `for i := 0; i < t.NumField(); i++` loops with range-based iteration.
3. **`errors.AsType[T]()`** — Generic type-safe error assertion. Replace
   `var target *SchemaValidationError; errors.As(err, &target)` with
   `if t, ok := errors.AsType[*SchemaValidationError](err); ok { ... }`.

### Free performance (no code changes)

1. **Green Tea GC** — 10-40% GC overhead reduction, enabled by default.
2. **~30% faster cgo** — Directly benefits our Rust FFI driver calls.
3. **`io.ReadAll()` ~2x faster** — Benefits schema file reading in tqlgen.
4. **Stack-allocated slices** — Compiler optimization for small slices.

### Minor

1. **`go fix` modernizers** — Could auto-modernize codebase patterns.

## Template system

type-bridge uses Jinja2 templates shipped internally in `generator/templates/`. Users never edit
templates — customization is entirely through the Python config API (DTOConfig, BaseClassConfig,
etc.). Our Go `text/template` strings embedded in code are equivalent. No need to externalize
templates — the config API is the customization surface.
