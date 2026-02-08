// Package gotype provides reflection-based mapping between Go types and TypeDB models.
package gotype

import (
	"fmt"
	"reflect"
	"strings"
	"time"
)

// ModelKind specifies whether a registered TypeDB model is an entity or a relation.
type ModelKind int

const (
	// ModelKindEntity represents a TypeDB entity type.
	ModelKindEntity ModelKind = iota
	// ModelKindRelation represents a TypeDB relation type.
	ModelKindRelation
)

// FieldInfo contains metadata about a single field in a model struct,
// mapping it to a TypeDB attribute.
type FieldInfo struct {
	// Tag is the parsed 'typedb' struct tag.
	Tag FieldTag
	// FieldName is the name of the field in the Go struct.
	FieldName string
	// FieldIndex is the 0-based index of the field in the Go struct.
	FieldIndex int
	// FieldType is the reflection type of the field.
	FieldType reflect.Type
	// IsPointer is true if the field is a pointer, used for optional attributes.
	IsPointer bool
	// IsSlice is true if the field is a slice, used for multi-valued attributes.
	IsSlice bool
	// ElemType is the base element type for slices and pointers.
	ElemType reflect.Type
	// ValueType is the TypeDB value type (e.g., "string", "long", "boolean").
	ValueType string
}

// ModelInfo contains comprehensive metadata about a registered TypeDB model,
// including its mapping to a Go struct and its TypeDB schema properties.
type ModelInfo struct {
	// GoType is the reflection type of the Go struct representing the model.
	GoType reflect.Type
	// Kind indicates whether this model is an entity or a relation.
	Kind ModelKind
	// TypeName is the name of the type in the TypeDB schema.
	TypeName string
	// IsAbstract is true if the TypeDB type is defined as abstract.
	IsAbstract bool
	// Supertype is the name of the parent type in the TypeDB schema.
	Supertype string
	// Fields is a list of metadata for each attribute field in the model.
	Fields []FieldInfo
	// Roles is a list of metadata for each role player field (only for relations).
	Roles []RoleInfo
	// KeyFields is a subset of Fields containing attributes marked as keys.
	KeyFields []FieldInfo
}

// FieldByName retrieves FieldInfo by the Go struct field name.
func (m *ModelInfo) FieldByName(name string) (FieldInfo, bool) {
	for _, f := range m.Fields {
		if f.FieldName == name {
			return f, true
		}
	}
	return FieldInfo{}, false
}

// FieldByAttrName retrieves FieldInfo by the TypeDB attribute name.
func (m *ModelInfo) FieldByAttrName(attrName string) (FieldInfo, bool) {
	for _, f := range m.Fields {
		if f.Tag.Name == attrName {
			return f, true
		}
	}
	return FieldInfo{}, false
}

// ExtractModelInfo analyzes a Go struct type and extracts its TypeDB model metadata.
// The struct must embed BaseEntity or BaseRelation to be a valid model.
func ExtractModelInfo(t reflect.Type) (*ModelInfo, error) {
	if t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct {
		return nil, fmt.Errorf("expected struct, got %s", t.Kind())
	}

	info := &ModelInfo{
		GoType: t,
	}

	// Determine kind and type name
	kind, err := detectModelKind(t)
	if err != nil {
		return nil, err
	}
	info.Kind = kind

	// Default type name: kebab-case struct name (e.g. UserAccount → user-account)
	info.TypeName = toKebabCase(t.Name())

	// Scan fields
	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)

		// Skip unexported fields
		if !field.IsExported() {
			continue
		}

		// Skip the embedded base types
		if field.Anonymous {
			continue
		}

		tagStr := field.Tag.Get("typedb")
		if tagStr == "" || tagStr == "-" {
			continue
		}

		tag, err := ParseTag(tagStr)
		if err != nil {
			return nil, fmt.Errorf("field %s: %w", field.Name, err)
		}

		if tag.Skip {
			continue
		}

		// Handle abstract flag
		if tag.Abstract {
			info.IsAbstract = true
		}

		if tag.IsRole() {
			// Role player field
			role := RoleInfo{
				RoleName:   tag.RoleName,
				FieldName:  field.Name,
				FieldIndex: i,
			}

			// Determine player type name
			ft := field.Type
			if ft.Kind() == reflect.Ptr {
				ft = ft.Elem()
			}
			if ft.Kind() == reflect.Slice {
				ft = ft.Elem()
				if ft.Kind() == reflect.Ptr {
					ft = ft.Elem()
				}
			}
			role.PlayerTypeName = toKebabCase(ft.Name())

			info.Roles = append(info.Roles, role)
		} else {
			// Attribute field
			fi := buildFieldInfo(field, i, tag)
			info.Fields = append(info.Fields, fi)

			if tag.Key {
				info.KeyFields = append(info.KeyFields, fi)
			}
		}
	}

	return info, nil
}

func detectModelKind(t reflect.Type) (ModelKind, error) {
	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)
		if !field.Anonymous {
			continue
		}
		switch field.Type {
		case reflect.TypeOf(BaseEntity{}):
			return ModelKindEntity, nil
		case reflect.TypeOf(BaseRelation{}):
			return ModelKindRelation, nil
		}
	}
	return 0, fmt.Errorf("type %s must embed BaseEntity or BaseRelation", t.Name())
}

func buildFieldInfo(field reflect.StructField, index int, tag FieldTag) FieldInfo {
	fi := FieldInfo{
		Tag:        tag,
		FieldName:  field.Name,
		FieldIndex: index,
		FieldType:  field.Type,
	}

	ft := field.Type
	if ft.Kind() == reflect.Ptr {
		fi.IsPointer = true
		fi.ElemType = ft.Elem()
		ft = ft.Elem()
	}
	if ft.Kind() == reflect.Slice {
		fi.IsSlice = true
		fi.ElemType = ft.Elem()
		ft = ft.Elem()
		if ft.Kind() == reflect.Ptr {
			ft = ft.Elem()
		}
	}

	fi.ValueType = goTypeToTypeDB(ft)
	return fi
}

// ToDict converts a registered model instance to a map[string]any using
// TypeDB attribute names as keys. Includes "_iid" if set.
func ToDict[T any](instance *T) (map[string]any, error) {
	var zero T
	t := reflect.TypeOf(zero)
	if t.Kind() == reflect.Ptr {
		t = t.Elem()
	}

	info, ok := LookupType(t)
	if !ok {
		return nil, fmt.Errorf("gotype: type %s is not registered", t.Name())
	}

	v := reflect.ValueOf(instance)
	if v.Kind() == reflect.Ptr {
		v = v.Elem()
	}

	result := make(map[string]any)

	// Include IID if present
	iid := getIIDFromValue(v)
	if iid != "" {
		result["_iid"] = iid
	}

	for _, fi := range info.Fields {
		field := v.Field(fi.FieldIndex)

		if fi.IsPointer {
			if field.IsNil() {
				// Omit nil optional fields
				continue
			}
			result[fi.Tag.Name] = field.Elem().Interface()
		} else {
			result[fi.Tag.Name] = field.Interface()
		}
	}

	return result, nil
}

// FromDict creates a new model instance from a map[string]any.
// Keys are TypeDB attribute names. This is the inverse of ToDict.
func FromDict[T any](data map[string]any) (*T, error) {
	return HydrateNew[T](data)
}

// ToInsertQuery generates a TypeQL insert query string for the given instance.
func ToInsertQuery[T any](instance *T) (string, error) {
	var zero T
	t := reflect.TypeOf(zero)
	if t.Kind() == reflect.Ptr {
		t = t.Elem()
	}

	info, ok := LookupType(t)
	if !ok {
		return "", fmt.Errorf("gotype: type %s is not registered", t.Name())
	}

	s := strategyFor(info.Kind)
	return s.BuildInsertQuery(info, instance, "e"), nil
}

// ToMatchQuery generates a TypeQL match clause for the given instance (by key fields).
func ToMatchQuery[T any](instance *T) (string, error) {
	var zero T
	t := reflect.TypeOf(zero)
	if t.Kind() == reflect.Ptr {
		t = t.Elem()
	}

	info, ok := LookupType(t)
	if !ok {
		return "", fmt.Errorf("gotype: type %s is not registered", t.Name())
	}

	s := strategyFor(info.Kind)
	return s.BuildMatchByKey(info, instance, "e"), nil
}

// toKebabCase converts a PascalCase Go struct name to kebab-case.
// e.g. "UserAccount" → "user-account", "HTTPServer" → "httpserver"
func toKebabCase(name string) string {
	if name == "" {
		return ""
	}
	var b strings.Builder
	for i, r := range name {
		if r >= 'A' && r <= 'Z' {
			if i > 0 {
				b.WriteByte('-')
			}
			b.WriteByte(byte(r - 'A' + 'a'))
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// goTypeToTypeDB maps Go types to TypeDB value type strings.
func goTypeToTypeDB(t reflect.Type) string {
	switch t.Kind() {
	case reflect.String:
		return "string"
	case reflect.Bool:
		return "boolean"
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return "integer"
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return "integer"
	case reflect.Float32, reflect.Float64:
		return "double"
	default:
		if t == reflect.TypeOf(time.Time{}) {
			return "datetime"
		}
		return "string"
	}
}
