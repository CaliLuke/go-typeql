// Package gotype provides a central registry for TypeDB model metadata.
package gotype

import (
	"fmt"
	"reflect"
	"strings"
	"sync"
)

var (
	globalRegistry = &Registry{
		byName: make(map[string]*ModelInfo),
		byType: make(map[reflect.Type]*ModelInfo),
	}
)

// Registry maintains a mapping between Go struct types and TypeDB model metadata.
// It is used to look up schema information during query generation and hydration.
type Registry struct {
	mu     sync.RWMutex
	byName map[string]*ModelInfo
	byType map[reflect.Type]*ModelInfo
}

// Register adds a Go struct type to the global registry as a TypeDB model.
// The type T must embed either BaseEntity or BaseRelation.
func Register[T any]() error {
	var zero T
	t := reflect.TypeOf(zero)
	if t.Kind() == reflect.Ptr {
		t = t.Elem()
	}

	info, err := ExtractModelInfo(t)
	if err != nil {
		return fmt.Errorf("registering %s: %w", t.Name(), err)
	}

	// Check for type: override in first field's tag
	for field := range t.Fields() {
		tagStr := field.Tag.Get("typedb")
		if tagStr == "" {
			continue
		}
		tag, err := ParseTag(tagStr)
		if err != nil {
			continue
		}
		if tag.TypeName != "" {
			info.TypeName = tag.TypeName
			break
		}
	}

	// Validate type name against reserved words
	kindStr := "entity"
	if info.Kind == ModelKindRelation {
		kindStr = "relation"
	}
	if IsReservedWord(info.TypeName) {
		return &ReservedWordError{Word: info.TypeName, Context: kindStr}
	}

	// Validate attribute names against reserved words
	for _, fi := range info.Fields {
		if fi.Tag.Name != "" && IsReservedWord(fi.Tag.Name) {
			return &ReservedWordError{Word: fi.Tag.Name, Context: "attribute"}
		}
	}

	// Validate role names for relations
	if info.Kind == ModelKindRelation {
		for _, fi := range info.Fields {
			if fi.Tag.RoleName != "" && IsReservedWord(fi.Tag.RoleName) {
				return &ReservedWordError{Word: fi.Tag.RoleName, Context: "role"}
			}
		}
	}

	globalRegistry.mu.Lock()
	defer globalRegistry.mu.Unlock()

	if existing, ok := globalRegistry.byName[info.TypeName]; ok {
		if existing.GoType != t {
			return fmt.Errorf("type name %q already registered to %s", info.TypeName, existing.GoType.Name())
		}
	}

	globalRegistry.byName[info.TypeName] = info
	globalRegistry.byType[t] = info
	return nil
}

// MustRegister is a helper that calls Register and panics if an error occurs.
// It is intended for use during application initialization.
func MustRegister[T any]() {
	if err := Register[T](); err != nil {
		panic(err)
	}
}

// Lookup retrieves ModelInfo for a given TypeDB type name.
func Lookup(typeName string) (*ModelInfo, bool) {
	globalRegistry.mu.RLock()
	defer globalRegistry.mu.RUnlock()
	info, ok := globalRegistry.byName[typeName]
	return info, ok
}

// LookupType retrieves ModelInfo for a given Go reflect.Type.
func LookupType(t reflect.Type) (*ModelInfo, bool) {
	if t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	globalRegistry.mu.RLock()
	defer globalRegistry.mu.RUnlock()
	info, ok := globalRegistry.byType[t]
	return info, ok
}

// LookupByGoName retrieves ModelInfo based on the name of the Go struct.
func LookupByGoName(name string) (*ModelInfo, bool) {
	globalRegistry.mu.RLock()
	defer globalRegistry.mu.RUnlock()
	lower := strings.ToLower(name)
	for _, info := range globalRegistry.byType {
		if strings.ToLower(info.GoType.Name()) == lower {
			return info, true
		}
	}
	return nil, false
}

// RegisteredTypes returns a slice containing ModelInfo for all registered types.
func RegisteredTypes() []*ModelInfo {
	globalRegistry.mu.RLock()
	defer globalRegistry.mu.RUnlock()
	result := make([]*ModelInfo, 0, len(globalRegistry.byType))
	for _, info := range globalRegistry.byType {
		result = append(result, info)
	}
	return result
}

// SubtypesOf returns a slice of registered types that are direct subtypes
// of the specified parent type.
func SubtypesOf(typeName string) []*ModelInfo {
	globalRegistry.mu.RLock()
	defer globalRegistry.mu.RUnlock()
	var result []*ModelInfo
	for _, info := range globalRegistry.byName {
		if info.Supertype == typeName {
			result = append(result, info)
		}
	}
	return result
}

// ResolveType maps a TypeDB type label to its registered ModelInfo.
func ResolveType(typeLabel string) (*ModelInfo, bool) {
	return Lookup(typeLabel)
}

// ClearRegistry resets the global registry, removing all registered models.
// This is primarily used for testing purposes.
func ClearRegistry() {
	globalRegistry.mu.Lock()
	defer globalRegistry.mu.Unlock()
	globalRegistry.byName = make(map[string]*ModelInfo)
	globalRegistry.byType = make(map[reflect.Type]*ModelInfo)
}
