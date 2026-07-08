// Package tqlgen provides tools for parsing TypeQL schemas and generating Go code from them.
package tqlgen

import (
	"fmt"
	"strings"
)

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

// MetaSpec describes a TypeDB @meta("key", "value") annotation.
type MetaSpec struct {
	// Key is the metadata key.
	Key string
	// Value is the metadata value.
	Value string
}

// FunctionSpec describes the signature of a TypeQL function definition.
type FunctionSpec struct {
	// Name is the name of the function.
	Name string
	// Parameters is a list of parameters accepted by the function.
	Parameters []ParameterSpec
	// ReturnType is the TypeQL return type of the function.
	ReturnType string
	// Doc is the optional @doc annotation text.
	Doc string
	// Meta is the list of @meta annotations.
	Meta []MetaSpec
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
	// Doc is the optional @doc annotation text.
	Doc string
	// Meta is the list of @meta annotations.
	Meta []MetaSpec
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
	// Doc is the optional @doc annotation text.
	Doc string
	// Meta is the list of @meta annotations.
	Meta []MetaSpec
}

// AttributeSpec describes a TypeQL attribute definition.
type AttributeSpec struct {
	// Name is the name of the attribute type.
	Name string
	// ValueType is the base value type of the attribute (string, integer, double, boolean, datetime).
	ValueType string
	// Doc is the optional @doc annotation text.
	Doc string
	// Meta is the list of @meta annotations.
	Meta []MetaSpec

	// Regex is an optional regular expression constraint for the attribute values.
	Regex string
	// Values is an optional list of allowed values (enumeration constraint).
	Values []string
	// RangeOp is an optional range constraint (e.g., "1..5").
	RangeOp string

	// Parent is the parent attribute type name when the attribute is declared
	// with `sub` (attribute subtyping). Empty for root attributes.
	Parent string
	// Abstract indicates whether the attribute type is declared @abstract.
	Abstract bool
}

// EntitySpec describes a TypeQL entity definition.
type EntitySpec struct {
	// Name is the name of the entity type.
	Name string
	// Parent is the name of the parent entity type if this is a subtype.
	Parent string
	// Abstract indicates whether the entity type is defined as abstract.
	Abstract bool
	// Doc is the optional @doc annotation text.
	Doc string
	// Meta is the list of @meta annotations.
	Meta []MetaSpec

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
	// Doc is the optional @doc annotation text.
	Doc string
	// Meta is the list of @meta annotations.
	Meta []MetaSpec

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
	// IsList indicates a TypeQL 3.x ordered-list ownership (owns attr[]).
	IsList bool
	// Doc is the optional @doc annotation text.
	Doc string
	// Meta is the list of @meta annotations.
	Meta []MetaSpec
}

// PlaysSpec describes a "plays relation:role" clause.
type PlaysSpec struct {
	// Relation is the name of the relation type.
	Relation string
	// Role is the name of the role played by the owner within that relation.
	Role string
	// Doc is the optional @doc annotation text.
	Doc string
	// Meta is the list of @meta annotations.
	Meta []MetaSpec
}

// RelatesSpec describes a "relates role" clause in a relation definition.
type RelatesSpec struct {
	// Role is the name of the role in the relation.
	Role string
	// AsParent specifies an overridden role from a parent relation type.
	AsParent string
	// Card specifies the cardinality of players allowed for this role.
	Card string
	// IsList indicates a TypeQL 3.x ordered-list role (relates role[]).
	IsList bool
	// Doc is the optional @doc annotation text.
	Doc string
	// Meta is the list of @meta annotations.
	Meta []MetaSpec
}

// AccumulateInheritance propagates owns/plays from parent entities/relations
// to their children, so each child has the complete set of fields. It returns
// an error when the schema declares an inheritance cycle (e.g. `a sub b` and
// `b sub a`), which would otherwise recurse forever. Types whose parent is not
// defined in the schema are left as-is.
func (s *ParsedSchema) AccumulateInheritance() error {
	// Build lookup maps
	entityMap := make(map[string]*EntitySpec)
	for i := range s.Entities {
		entityMap[s.Entities[i].Name] = &s.Entities[i]
	}
	relationMap := make(map[string]*RelationSpec)
	for i := range s.Relations {
		relationMap[s.Relations[i].Name] = &s.Relations[i]
	}

	// Propagate entity inheritance. The state map memoizes finished types so
	// each type is merged exactly once, and flags in-progress types so cycles
	// surface as an error instead of infinite recursion.
	entityState := make(map[string]visitState)
	for i := range s.Entities {
		if err := accumulateEntity(&s.Entities[i], entityMap, entityState, nil); err != nil {
			return err
		}
	}

	// Propagate relation inheritance
	relationState := make(map[string]visitState)
	for i := range s.Relations {
		if err := accumulateRelation(&s.Relations[i], relationMap, relationState, nil); err != nil {
			return err
		}
	}
	return nil
}

// visitState tracks inheritance traversal progress for cycle detection.
type visitState int

const (
	visiting visitState = iota + 1
	visited
)

// cycleError formats an inheritance-cycle error like "inheritance cycle: a -> b -> a".
func cycleError(path []string, repeat string) error {
	return fmt.Errorf("inheritance cycle: %s -> %s", strings.Join(path, " -> "), repeat)
}

func accumulateEntity(e *EntitySpec, m map[string]*EntitySpec, state map[string]visitState, path []string) error {
	switch state[e.Name] {
	case visited:
		return nil
	case visiting:
		return cycleError(path, e.Name)
	}
	state[e.Name] = visiting
	if e.Parent != "" {
		if parent, ok := m[e.Parent]; ok {
			if err := accumulateEntity(parent, m, state, append(path, e.Name)); err != nil {
				return err
			}
			e.Owns = mergeOwns(parent.Owns, e.Owns)
		}
	}
	state[e.Name] = visited
	return nil
}

func accumulateRelation(r *RelationSpec, m map[string]*RelationSpec, state map[string]visitState, path []string) error {
	switch state[r.Name] {
	case visited:
		return nil
	case visiting:
		return cycleError(path, r.Name)
	}
	state[r.Name] = visiting
	if r.Parent != "" {
		if parent, ok := m[r.Parent]; ok {
			if err := accumulateRelation(parent, m, state, append(path, r.Name)); err != nil {
				return err
			}
			r.Owns = mergeOwns(parent.Owns, r.Owns)
			r.Relates = mergeRelates(parent.Relates, r.Relates)
		}
	}
	state[r.Name] = visited
	return nil
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
