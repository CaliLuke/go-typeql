package tqlgen

// DTOConfig configures DTO (Data Transfer Object) code generation.
// DTOs generate Out/Create/Patch struct variants for HTTP API layers.
type DTOConfig struct {
	// PackageName is the Go package name for the generated file (required).
	PackageName string
	// UseAcronyms applies Go acronym naming conventions (e.g., "ID" not "Id").
	UseAcronyms bool
	// SkipAbstract excludes abstract types from DTO generation.
	SkipAbstract bool
	// IDFieldName is the name of the ID field in Out structs (default "ID").
	IDFieldName string
	// StrictOut makes required fields non-pointer in Out structs.
	// When false (default), all attribute fields in Out are pointers for safety.
	StrictOut bool
	// ExcludeEntities lists entity names to skip during generation.
	ExcludeEntities []string
	// ExcludeRelations lists relation names to skip during generation.
	ExcludeRelations []string
	// SkipRelationOut skips generating Out structs for relations.
	SkipRelationOut bool
	// BaseStructs configures shared base structs for entity hierarchies.
	BaseStructs []BaseStructConfig
	// EntityFieldOverrides provides per-entity, per-variant field overrides.
	EntityFieldOverrides []EntityFieldOverride
	// CompositeEntities configures merged flat structs from multiple entity types.
	CompositeEntities []CompositeEntityConfig
	// EntityOutName overrides the interface name for entity Out DTOs (default "EntityOut").
	EntityOutName string
	// EntityCreateName overrides the interface name for entity Create DTOs (default "EntityCreate").
	EntityCreateName string
	// EntityPatchName overrides the interface name for entity Patch DTOs (default "EntityPatch").
	EntityPatchName string
	// RelationOutName overrides the interface name for relation Out DTOs (default "RelationOut").
	RelationOutName string
	// RelationCreateName overrides the interface name for relation Create DTOs (default "RelationCreate").
	RelationCreateName string
	// RelationCreateEmbed is a struct name to embed in all relation Create DTOs.
	RelationCreateEmbed string
}

// BaseStructConfig configures a shared embedded base struct for an entity hierarchy.
// When an entity inherits from SourceEntity, its DTOs embed the base struct
// instead of repeating the inherited fields.
type BaseStructConfig struct {
	// SourceEntity is the schema entity name that triggers this base struct.
	SourceEntity string
	// BaseName is the Go struct name prefix (e.g., "BaseArtifact").
	BaseName string
	// InheritedAttrs lists attribute names defined in the base struct.
	// These are skipped when rendering child entity DTOs.
	InheritedAttrs []string
	// ExtraFields adds additional fields as name → Go type annotation.
	ExtraFields map[string]string
}

// CompositeEntityConfig merges multiple entity subtypes into a single flat DTO
// with a Type discriminator. Useful for polymorphic API endpoints.
type CompositeEntityConfig struct {
	// Name is the Go struct name for the composite DTO (e.g., "ArtifactDTO").
	Name string
	// Entities lists the TypeDB entity names to merge.
	Entities []string
	// TypeName is the TypeDB type name for TypeName() method (e.g., "artifact").
	TypeName string
}

// EntityFieldOverride provides per-entity, per-variant field overrides.
type EntityFieldOverride struct {
	// Entity is the TypeDB entity name.
	Entity string
	// Field is the TypeDB attribute name.
	Field string
	// Variant is the DTO variant: "out", "create", or "patch".
	Variant string
	// Required overrides whether the field is required (non-pointer).
	// nil means keep the schema default.
	Required *bool
}
