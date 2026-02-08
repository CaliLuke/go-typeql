// Package gotype provides reflection-based TypeDB data mapping.
package gotype

// Relation is the marker interface for TypeDB relation types.
// Structs that represent TypeDB relations must satisfy this interface,
// typically by embedding the BaseRelation type.
type Relation interface {
	relation()
	// TypeDBTypeName returns the TypeDB label for this relation type.
	TypeDBTypeName() string
	// GetIID returns the internal TypeDB instance ID (IID) for the relation instance.
	GetIID() string
	// SetIID assigns the internal TypeDB instance ID (IID) to the relation instance.
	SetIID(iid string)
}

// BaseRelation is an embeddable base type for all Go structs mapping to TypeDB relations.
// It provides the internal state and methods required to satisfy the Relation interface.
//
// Example usage:
//
//	type Employment struct {
//	    gotype.BaseRelation
//	    Employee *Person  `typedb:"role:employee"`
//	    Employer *Company `typedb:"role:employer"`
//	}
type BaseRelation struct {
	iid string
}

func (BaseRelation) relation() {}

// TypeDBTypeName returns the TypeDB type name for the relation.
func (BaseRelation) TypeDBTypeName() string { return "" }

// GetIID returns the TypeDB internal instance ID.
func (r *BaseRelation) GetIID() string { return r.iid }

// SetIID sets the TypeDB internal instance ID.
func (r *BaseRelation) SetIID(iid string) { r.iid = iid }

// RoleInfo contains metadata about a role player in a relation model,
// defining how a struct field maps to a TypeDB role.
type RoleInfo struct {
	// RoleName is the TypeDB name of the role (e.g., "employee").
	RoleName string

	// FieldName is the name of the Go struct field representing the player.
	FieldName string

	// FieldIndex is the 0-based index of the field in the Go struct.
	FieldIndex int

	// PlayerTypeName is the TypeDB type label of the expected role player.
	PlayerTypeName string
}
