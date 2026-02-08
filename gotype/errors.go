// Package gotype defines various error types for ORM operations and schema management.
package gotype

import "fmt"

// NotRegisteredError is returned when an operation is attempted on a Go type
// that has not been registered with the ORM.
type NotRegisteredError struct {
	TypeName string
}

// Error returns the error message for NotRegisteredError.
func (e *NotRegisteredError) Error() string {
	return fmt.Sprintf("type %q is not registered", e.TypeName)
}

// KeyAttributeError is returned when a mandatory key attribute is missing
// during an insert or update operation.
type KeyAttributeError struct {
	EntityType string
	FieldName  string
	Operation  string
}

// Error returns the error message for KeyAttributeError.
func (e *KeyAttributeError) Error() string {
	return fmt.Sprintf("key attribute %q on %s is required for %s",
		e.FieldName, e.EntityType, e.Operation)
}

// HydrationError is returned when an error occurs while populating a Go struct
// with data retrieved from TypeDB.
type HydrationError struct {
	TypeName string
	Field    string
	Cause    error
}

// Error returns the error message for HydrationError.
func (e *HydrationError) Error() string {
	return fmt.Sprintf("hydrating %s.%s: %v", e.TypeName, e.Field, e.Cause)
}

// Unwrap returns the underlying cause of the HydrationError.
func (e *HydrationError) Unwrap() error {
	return e.Cause
}

// ReservedWordError is returned when a TypeQL reserved keyword is used
// as a name for a type, attribute, or role.
type ReservedWordError struct {
	Word    string
	Context string // "attribute", "entity", "relation", "role"
}

// Error returns the error message for ReservedWordError.
func (e *ReservedWordError) Error() string {
	return fmt.Sprintf("gotype: %q is a TypeQL reserved keyword and cannot be used as %s name",
		e.Word, e.Context)
}

// SchemaValidationError is returned when the registered Go models do not
// align with the expected TypeDB schema patterns.
type SchemaValidationError struct {
	TypeName string
	Message  string
}

// Error returns the error message for SchemaValidationError.
func (e *SchemaValidationError) Error() string {
	return fmt.Sprintf("schema validation %s: %s", e.TypeName, e.Message)
}

// SchemaConflictError is returned when a proposed schema migration conflicts
// with the existing database schema in a non-recoverable way.
type SchemaConflictError struct {
	TypeName string
	Change   string
}

// Error returns the error message for SchemaConflictError.
func (e *SchemaConflictError) Error() string {
	return fmt.Sprintf("schema conflict %s: %s", e.TypeName, e.Change)
}

// MigrationError is returned when an error occurs during the execution
// of a schema migration.
type MigrationError struct {
	Operation string
	Cause     error
}

// Error returns the error message for MigrationError.
func (e *MigrationError) Error() string {
	return fmt.Sprintf("migration %s: %v", e.Operation, e.Cause)
}

// Unwrap returns the underlying cause of the MigrationError.
func (e *MigrationError) Unwrap() error {
	return e.Cause
}

// NotFoundError is returned when a query expected to return an instance
// finds no matching results.
type NotFoundError struct {
	TypeName string
}

// Error returns the error message for NotFoundError.
func (e *NotFoundError) Error() string {
	return fmt.Sprintf("%s: not found", e.TypeName)
}

// NotUniqueError is returned when a query expected to return a single
// unique instance finds multiple matches.
type NotUniqueError struct {
	TypeName string
	Count    int
}

// Error returns the error message for NotUniqueError.
func (e *NotUniqueError) Error() string {
	return fmt.Sprintf("%s: expected unique, got %d", e.TypeName, e.Count)
}
