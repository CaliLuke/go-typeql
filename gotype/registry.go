// Package gotype provides a central registry for TypeDB model metadata.
package gotype

import (
	"fmt"
	"reflect"
	"sort"
	"sync"
)

var (
	globalRegistry = &Registry{
		byName:   make(map[string]*ModelInfo),
		byType:   make(map[reflect.Type]*ModelInfo),
		byGoName: make(map[string]*ModelInfo),
	}
)

// Registry maintains a mapping between Go struct types and TypeDB model metadata.
// It is used to look up schema information during query generation and hydration.
type Registry struct {
	mu       sync.RWMutex
	byName   map[string]*ModelInfo
	byType   map[reflect.Type]*ModelInfo
	byGoName map[string]*ModelInfo
}

// Register adds a Go struct type to the global registry as a TypeDB model.
// The type T must embed either BaseEntity or BaseRelation.
func Register[T any]() error {
	var zero T
	t := reflect.TypeOf(zero)
	if t.Kind() == reflect.Pointer {
		t = t.Elem()
	}

	info, err := ExtractModelInfo(t)
	if err != nil {
		return fmt.Errorf("registering %s: %w", t.Name(), err)
	}

	if err := validateModelNames(info); err != nil {
		return err
	}

	globalRegistry.mu.Lock()
	defer globalRegistry.mu.Unlock()

	if existing, ok := globalRegistry.byName[info.TypeName]; ok {
		if existing.GoType != t {
			return fmt.Errorf("type name %q already registered to %s", info.TypeName, existing.GoType.Name())
		}
	}
	if existing, ok := globalRegistry.byGoName[lowerGoName(t.Name())]; ok {
		if existing.GoType != t {
			return fmt.Errorf("go type name %q conflicts (case-insensitively) with already registered %s", t.Name(), existing.GoType.Name())
		}
	}
	if err := validateAgainstRegistry(info, t); err != nil {
		return err
	}

	globalRegistry.byName[info.TypeName] = info
	globalRegistry.byType[t] = info
	globalRegistry.byGoName[lowerGoName(t.Name())] = info
	return nil
}

// validateAgainstRegistry checks the model against already-registered models:
// shared attribute names must agree on their value type, and a declared
// supertype (if registered) must be of the same kind. Callers must hold the
// registry lock.
func validateAgainstRegistry(info *ModelInfo, t reflect.Type) error {
	for _, fi := range info.Fields {
		for _, other := range globalRegistry.byName {
			if other.GoType == t {
				continue
			}
			if of, ok := other.FieldByAttrName(fi.Tag.Name); ok && of.ValueType != fi.ValueType {
				return fmt.Errorf("registering %s: attribute %q has value type %s but is already registered with value type %s by %s",
					t.Name(), fi.Tag.Name, fi.ValueType, of.ValueType, other.GoType.Name())
			}
		}
	}
	if info.Supertype != "" {
		if parent, ok := globalRegistry.byName[info.Supertype]; ok && parent.Kind != info.Kind {
			return fmt.Errorf("registering %s: supertype %q is a different kind (entity vs relation)", t.Name(), info.Supertype)
		}
	}
	return nil
}

func validateModelNames(info *ModelInfo) error {
	kindStr := "entity"
	if info.Kind == ModelKindRelation {
		kindStr = "relation"
	}
	if IsReservedWord(info.TypeName) {
		return &ReservedWordError{Word: info.TypeName, Context: kindStr}
	}
	if err := ValidateIdentifier(info.TypeName, kindStr); err != nil {
		return err
	}
	if info.Supertype != "" {
		if IsReservedWord(info.Supertype) {
			return &ReservedWordError{Word: info.Supertype, Context: "supertype"}
		}
		if err := ValidateIdentifier(info.Supertype, "supertype"); err != nil {
			return err
		}
		if info.Supertype == info.TypeName {
			return fmt.Errorf("%s %q cannot be its own supertype", kindStr, info.TypeName)
		}
	}
	attrTypes := make(map[string]string, len(info.Fields))
	for _, fi := range info.Fields {
		if IsReservedWord(fi.Tag.Name) {
			return &ReservedWordError{Word: fi.Tag.Name, Context: "attribute"}
		}
		if err := ValidateIdentifier(fi.Tag.Name, "attribute"); err != nil {
			return err
		}
		if prev, ok := attrTypes[fi.Tag.Name]; ok {
			return fmt.Errorf("%s %q declares attribute %q more than once (value types %s and %s)",
				kindStr, info.TypeName, fi.Tag.Name, prev, fi.ValueType)
		}
		attrTypes[fi.Tag.Name] = fi.ValueType
	}
	for _, role := range info.Roles {
		if IsReservedWord(role.RoleName) {
			return &ReservedWordError{Word: role.RoleName, Context: "role"}
		}
		if err := ValidateIdentifier(role.RoleName, "role"); err != nil {
			return err
		}
	}
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
	if t.Kind() == reflect.Pointer {
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
	info, ok := globalRegistry.byGoName[lowerGoName(name)]
	if ok {
		return info, true
	}
	return nil, false
}

// RegisteredTypes returns a slice containing ModelInfo for all registered
// types in a deterministic order: sorted by TypeName, with registered
// supertypes always preceding their subtypes. Deterministic ordering keeps
// generated schemas and migration plans stable across runs.
func RegisteredTypes() []*ModelInfo {
	globalRegistry.mu.RLock()
	defer globalRegistry.mu.RUnlock()
	result := make([]*ModelInfo, 0, len(globalRegistry.byType))
	for _, info := range globalRegistry.byType {
		result = append(result, info)
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].TypeName < result[j].TypeName
	})
	return supertypesFirst(result)
}

// supertypesFirst reorders the name-sorted slice so that every registered
// supertype precedes its subtypes, keeping the order stable otherwise.
func supertypesFirst(sorted []*ModelInfo) []*ModelInfo {
	byName := make(map[string]*ModelInfo, len(sorted))
	for _, info := range sorted {
		byName[info.TypeName] = info
	}
	out := make([]*ModelInfo, 0, len(sorted))
	visited := make(map[string]bool, len(sorted))
	var emit func(info *ModelInfo)
	emit = func(info *ModelInfo) {
		if visited[info.TypeName] {
			return
		}
		visited[info.TypeName] = true
		if parent, ok := byName[info.Supertype]; ok {
			emit(parent)
		}
		out = append(out, info)
	}
	for _, info := range sorted {
		emit(info)
	}
	return out
}

// SubtypesOf returns a slice of registered types that are direct subtypes
// of the specified parent type, sorted by TypeName.
func SubtypesOf(typeName string) []*ModelInfo {
	globalRegistry.mu.RLock()
	defer globalRegistry.mu.RUnlock()
	var result []*ModelInfo
	for _, info := range globalRegistry.byName {
		if info.Supertype == typeName {
			result = append(result, info)
		}
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].TypeName < result[j].TypeName
	})
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
	globalRegistry.byGoName = make(map[string]*ModelInfo)
}

func lowerGoName(name string) string {
	b := make([]byte, len(name))
	for i := range len(name) {
		c := name[i]
		if 'A' <= c && c <= 'Z' {
			c += 'a' - 'A'
		}
		b[i] = c
	}
	return string(b)
}
