// Package gotype provides reflection-based TypeDB data mapping.
package gotype

// Entity is the marker interface for TypeDB entity types.
// Structs that represent TypeDB entities must satisfy this interface,
// typically by embedding the BaseEntity type.
type Entity interface {
	entity()
	// TypeDBTypeName returns the TypeDB label for this entity type.
	TypeDBTypeName() string
	// GetIID returns the internal TypeDB instance ID (IID) for the entity instance.
	GetIID() string
	// SetIID assigns the internal TypeDB instance ID (IID) to the entity instance.
	SetIID(iid string)
}

// BaseEntity is an embeddable base type for all Go structs mapping to TypeDB entities.
// It provides the internal state and methods required to satisfy the Entity interface.
//
// Example usage:
//
//	type Person struct {
//	    gotype.BaseEntity
//	    Name  string `typedb:"name,key"`
//	}
type BaseEntity struct {
	iid string
}

func (BaseEntity) entity() {}

// TypeDBTypeName returns the TypeDB type name for the entity.
func (BaseEntity) TypeDBTypeName() string { return "" }

// GetIID returns the TypeDB internal instance ID.
func (e *BaseEntity) GetIID() string { return e.iid }

// SetIID sets the TypeDB internal instance ID.
func (e *BaseEntity) SetIID(iid string) { e.iid = iid }
