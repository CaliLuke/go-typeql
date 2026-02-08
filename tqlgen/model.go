// Package tqlgen provides tools for parsing TypeQL schemas and generating Go code from them.
package tqlgen

// ParsedSchema holds all components extracted from a TypeQL schema file,
// including attribute, entity, and relation definitions, as well as functions and structs.
type ParsedSchema struct {
	// Attributes is a list of all attribute definitions in the schema.
	Attributes []AttributeSpec
	// Entities is a list of all entity definitions in the schema.
	Entities []EntitySpec
	// Relations is a list of all relation definitions in the schema.
	Relations []RelationSpec
	// Functions is a list of all function signatures in the schema.
	Functions []FunctionSpec
	// Structs is a list of all struct definitions in the schema.
	Structs []StructSpec
}

// FunctionSpec describes the signature of a TypeQL function definition.
type FunctionSpec struct {
	// Name is the name of the function.
	Name string
	// Parameters is a list of parameters accepted by the function.
	Parameters []ParameterSpec
	// ReturnType is the TypeQL return type of the function.
	ReturnType string
}

// ParameterSpec describes a single parameter of a TypeQL function.
type ParameterSpec struct {
	// Name is the name of the parameter (without the '$' prefix).
	Name string
	// TypeName is the TypeQL type name expected for this parameter.
	TypeName string
}

// StructSpec describes a TypeQL struct definition, which is a collection of named fields.
type StructSpec struct {
	// Name is the name of the struct type.
	Name string
	// Fields is a list of fields defined within the struct.
	Fields []StructFieldSpec
}

// StructFieldSpec describes a single field within a TypeQL struct.
type StructFieldSpec struct {
	// Name is the name of the field.
	Name string
	// ValueType is the TypeQL type of the field's value.
	ValueType string
	// Optional indicates whether the field is marked as optional in the schema.
	Optional bool
}

// AttributeSpec describes a TypeQL attribute definition.
type AttributeSpec struct {
	// Name is the name of the attribute type.
	Name string
	// ValueType is the base value type of the attribute (string, integer, double, boolean, datetime).
	ValueType string

	// Regex is an optional regular expression constraint for the attribute values.
	Regex string
	// Values is an optional list of allowed values (enumeration constraint).
	Values []string
	// RangeOp is an optional range constraint (e.g., "1..5").
	RangeOp string
}

// EntitySpec describes a TypeQL entity definition.
type EntitySpec struct {
	// Name is the name of the entity type.
	Name string
	// Parent is the name of the parent entity type if this is a subtype.
	Parent string
	// Abstract indicates whether the entity type is defined as abstract.
	Abstract bool

	// Owns is a list of attributes owned by this entity type.
	Owns []OwnsSpec
	// Plays is a list of relation roles this entity type can play.
	Plays []PlaysSpec
}

// RelationSpec describes a TypeQL relation definition.
type RelationSpec struct {
	// Name is the name of the relation type.
	Name string
	// Parent is the name of the parent relation type if this is a subtype.
	Parent string
	// Abstract indicates whether the relation type is defined as abstract.
	Abstract bool

	// Relates is a list of roles involved in this relation.
	Relates []RelatesSpec
	// Owns is a list of attributes owned by this relation type.
	Owns []OwnsSpec
	// Plays is a list of relation roles this relation type can play.
	Plays []PlaysSpec
}

// OwnsSpec describes an "owns attribute" clause in an entity or relation definition.
type OwnsSpec struct {
	// Attribute is the name of the attribute type being owned.
	Attribute string
	// Key indicates whether this attribute is a key for the owner.
	Key bool
	// Unique indicates whether the attribute value must be unique across all instances.
	Unique bool
	// Card specifies the cardinality of the ownership (e.g., "0..1", "1..5").
	Card string
}

// PlaysSpec describes a "plays relation:role" clause.
type PlaysSpec struct {
	// Relation is the name of the relation type.
	Relation string
	// Role is the name of the role played by the owner within that relation.
	Role string
}

// RelatesSpec describes a "relates role" clause in a relation definition.
type RelatesSpec struct {
	// Role is the name of the role in the relation.
	Role string
	// AsParent specifies an overridden role from a parent relation type.
	AsParent string
	// Card specifies the cardinality of players allowed for this role.
	Card string
}

// AccumulateInheritance propagates owns/plays from parent entities/relations
// to their children, so each child has the complete set of fields.
func (s *ParsedSchema) AccumulateInheritance() {
	// Build lookup maps
	entityMap := make(map[string]*EntitySpec)
	for i := range s.Entities {
		entityMap[s.Entities[i].Name] = &s.Entities[i]
	}
	relationMap := make(map[string]*RelationSpec)
	for i := range s.Relations {
		relationMap[s.Relations[i].Name] = &s.Relations[i]
	}

	// Propagate entity inheritance
	for i := range s.Entities {
		e := &s.Entities[i]
		if e.Parent == "" {
			continue
		}
		parent, ok := entityMap[e.Parent]
		if !ok {
			continue
		}
		// Recursively accumulate parent first
		accumulateEntity(parent, entityMap)
		e.Owns = mergeOwns(parent.Owns, e.Owns)
	}

	// Propagate relation inheritance
	for i := range s.Relations {
		r := &s.Relations[i]
		if r.Parent == "" {
			continue
		}
		parent, ok := relationMap[r.Parent]
		if !ok {
			continue
		}
		accumulateRelation(parent, relationMap)
		r.Owns = mergeOwns(parent.Owns, r.Owns)
		r.Relates = mergeRelates(parent.Relates, r.Relates)
	}
}

func accumulateEntity(e *EntitySpec, m map[string]*EntitySpec) {
	if e.Parent == "" {
		return
	}
	parent, ok := m[e.Parent]
	if !ok {
		return
	}
	accumulateEntity(parent, m)
	e.Owns = mergeOwns(parent.Owns, e.Owns)
}

func accumulateRelation(r *RelationSpec, m map[string]*RelationSpec) {
	if r.Parent == "" {
		return
	}
	parent, ok := m[r.Parent]
	if !ok {
		return
	}
	accumulateRelation(parent, m)
	r.Owns = mergeOwns(parent.Owns, r.Owns)
	r.Relates = mergeRelates(parent.Relates, r.Relates)
}

// mergeOwns combines parent and child owns, with child overriding parent.
func mergeOwns(parent, child []OwnsSpec) []OwnsSpec {
	seen := make(map[string]bool)
	for _, o := range child {
		seen[o.Attribute] = true
	}
	var merged []OwnsSpec
	for _, o := range parent {
		if !seen[o.Attribute] {
			merged = append(merged, o)
		}
	}
	merged = append(merged, child...)
	return merged
}

// mergeRelates combines parent and child relates, with child overriding parent.
func mergeRelates(parent, child []RelatesSpec) []RelatesSpec {
	seen := make(map[string]bool)
	for _, r := range child {
		seen[r.Role] = true
	}
	var merged []RelatesSpec
	for _, r := range parent {
		if !seen[r.Role] {
			merged = append(merged, r)
		}
	}
	merged = append(merged, child...)
	return merged
}
