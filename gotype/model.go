// Package gotype provides reflection-based mapping between Go types and TypeDB models.
package gotype

import (
	"fmt"
	"reflect"
	"sort"
	"strings"
	"sync/atomic"
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
	// Doc is the optional TypeDB @doc annotation for this ownership.
	Doc string
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
	// timeLayoutHint caches the last successful datetime parsing layout index
	// (1-based; 0 means unknown). It is a shared pointer, allocated once per
	// field at registration time, so FieldInfo value copies (KeyFields,
	// FieldByName/FieldByAttrName results, range-by-value loops) all observe
	// the same atomically updated cache instead of racing on a plain word
	// inside each copy (issue #43).
	timeLayoutHint *atomic.Uint32
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
	// Doc is the optional TypeDB @doc annotation for this type.
	Doc string
	// Meta is the ordered list of optional TypeDB @meta annotations for this type.
	Meta []Meta
	// IsAbstract is true if the TypeDB type is defined as abstract.
	IsAbstract bool
	// Supertype is the name of the parent type in the TypeDB schema.
	Supertype string
	// Fields is a list of metadata for each attribute field in the model.
	Fields []FieldInfo
	// Roles is a list of metadata for each role player field (only for relations).
	Roles []RoleInfo
	// KeyFields is a subset of Fields containing attributes marked as keys.
	KeyFields      []FieldInfo
	baseFieldIndex int
	// typeNameOverride records an explicit type: tag override so conflicting
	// overrides on different fields can be detected.
	typeNameOverride string
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
// Type-level tag options (abstract, type:name, sub:parent-name) may appear on
// any field, including the embedded base field and blank (`_`) fields.
// Fields with unsupported Go types or option-only tags without an attribute
// name cause an error so invalid schemas fail at registration time.
func ExtractModelInfo(t reflect.Type) (*ModelInfo, error) {
	if t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct {
		return nil, fmt.Errorf("expected struct, got %s", t.Kind())
	}

	info := &ModelInfo{
		GoType:         t,
		baseFieldIndex: -1,
	}

	// Determine kind and type name
	kind, baseFieldIndex, err := detectModelKind(t)
	if err != nil {
		return nil, err
	}
	info.Kind = kind
	info.baseFieldIndex = baseFieldIndex

	// Default type name: kebab-case struct name (e.g. UserAccount → user-account)
	info.TypeName = toKebabCase(t.Name())
	info.Doc = schemaDocForType(t)
	info.Meta = schemaMetaForType(t)

	fieldCount := t.NumField()
	info.Fields = make([]FieldInfo, 0, max(1, fieldCount/2+1))
	info.Roles = make([]RoleInfo, 0, max(1, fieldCount/2))
	info.KeyFields = make([]FieldInfo, 0, 1)

	// Scan fields
	for field := range t.Fields() {
		if err := scanModelField(info, field); err != nil {
			return nil, err
		}
	}

	return info, nil
}

// scanModelField processes a single struct field during model extraction,
// routing it to the role list, the attribute list, or type-level options.
func scanModelField(info *ModelInfo, field reflect.StructField) error {
	tagStr := field.Tag.Get("typedb")
	if tagStr == "" || tagStr == "-" {
		return nil
	}

	tag, err := ParseTag(tagStr)
	if err != nil {
		return fmt.Errorf("field %s: %w", field.Name, err)
	}
	if tag.Skip {
		return nil
	}

	// Embedded base types and unexported fields (e.g. `_ byte`) may carry
	// type-level options (abstract, type:, sub:) but never map to attributes.
	if field.Anonymous || !field.IsExported() {
		if tag.Name != "" || tag.hasFieldOptions() || tag.IsRole() {
			return fmt.Errorf("field %s: only type-level options (abstract, type:, sub:) are allowed on embedded or unexported fields, got %q", field.Name, tagStr)
		}
		return applyTypeLevelTag(info, field.Name, tag)
	}

	// Type-level options may ride on any field's tag.
	if err := applyTypeLevelTag(info, field.Name, tag); err != nil {
		return err
	}

	if tag.IsRole() {
		return addRoleField(info, field, tag)
	}

	if tag.Name == "" {
		if tag.hasTypeLevelOptions() && !tag.hasFieldOptions() {
			// Pure type-level tag (e.g. `typedb:"abstract"`); no attribute.
			return nil
		}
		return fmt.Errorf("field %s: typedb tag %q has options but no attribute name", field.Name, tagStr)
	}

	// Attribute field
	fi, err := buildFieldInfo(field, field.Index[0], tag)
	if err != nil {
		return err
	}
	info.Fields = append(info.Fields, fi)

	if tag.Key {
		info.KeyFields = append(info.KeyFields, fi)
	}
	return nil
}

// addRoleField appends a role player field to the model's role list.
func addRoleField(info *ModelInfo, field reflect.StructField, tag FieldTag) error {
	if tag.Name != "" {
		return fmt.Errorf("field %s: tag cannot declare both an attribute name %q and a role %q", field.Name, tag.Name, tag.RoleName)
	}
	role := RoleInfo{
		RoleName:   tag.RoleName,
		FieldName:  field.Name,
		FieldIndex: field.Index[0],
	}

	// Determine player type name
	ft := field.Type
	if ft.Kind() == reflect.Pointer {
		ft = ft.Elem()
	}
	if ft.Kind() == reflect.Slice {
		ft = ft.Elem()
		if ft.Kind() == reflect.Pointer {
			ft = ft.Elem()
		}
	}
	role.PlayerTypeName = toKebabCase(ft.Name())

	info.Roles = append(info.Roles, role)
	return nil
}

// applyTypeLevelTag applies type-level tag options (abstract, type:, sub:) to
// the model, rejecting conflicting values coming from different fields.
func applyTypeLevelTag(info *ModelInfo, fieldName string, tag FieldTag) error {
	if tag.Abstract {
		info.IsAbstract = true
	}
	if tag.TypeName != "" {
		if info.typeNameOverride != "" && info.typeNameOverride != tag.TypeName {
			return fmt.Errorf("field %s: conflicting type name overrides %q and %q", fieldName, info.typeNameOverride, tag.TypeName)
		}
		info.typeNameOverride = tag.TypeName
		info.TypeName = tag.TypeName
	}
	if tag.Sub != "" {
		if info.Supertype != "" && info.Supertype != tag.Sub {
			return fmt.Errorf("field %s: conflicting supertypes %q and %q", fieldName, info.Supertype, tag.Sub)
		}
		info.Supertype = tag.Sub
	}
	return nil
}

var (
	baseEntityType   = reflect.TypeOf(BaseEntity{})
	baseRelationType = reflect.TypeOf(BaseRelation{})
)

func detectModelKind(t reflect.Type) (ModelKind, int, error) {
	for field := range t.Fields() {
		if !field.Anonymous {
			continue
		}
		switch field.Type {
		case baseEntityType:
			return ModelKindEntity, field.Index[0], nil
		case baseRelationType:
			return ModelKindRelation, field.Index[0], nil
		}
	}
	return 0, -1, fmt.Errorf("type %s must embed BaseEntity or BaseRelation", t.Name())
}

// SchemaDocumented can be implemented by a model to emit a type-level TypeDB
// @doc annotation during schema generation.
type SchemaDocumented interface {
	SchemaDoc() string
}

// Meta describes a TypeDB @meta("key", "value") annotation.
type Meta struct {
	// Key is the metadata key.
	Key string
	// Value is the metadata value.
	Value string
}

// SchemaAnnotated can be implemented by a model to emit type-level TypeDB
// @meta annotations during schema generation.
type SchemaAnnotated interface {
	SchemaMeta() map[string]string
}

func schemaDocForType(t reflect.Type) string {
	if t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct {
		return ""
	}
	v := reflect.New(t).Interface()
	documented, ok := v.(SchemaDocumented)
	if !ok {
		return ""
	}
	return documented.SchemaDoc()
}

func schemaMetaForType(t reflect.Type) []Meta {
	if t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct {
		return nil
	}
	v := reflect.New(t).Interface()
	annotated, ok := v.(SchemaAnnotated)
	if !ok {
		return nil
	}

	values := annotated.SchemaMeta()
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	meta := make([]Meta, 0, len(keys))
	for _, key := range keys {
		meta = append(meta, Meta{Key: key, Value: values[key]})
	}
	return meta
}

func buildFieldInfo(field reflect.StructField, index int, tag FieldTag) (FieldInfo, error) {
	fi := FieldInfo{
		Tag:        tag,
		Doc:        field.Tag.Get("typedb_doc"),
		FieldName:  field.Name,
		FieldIndex: index,
		FieldType:  field.Type,
		// Shared across all value copies of this FieldInfo (issue #43).
		timeLayoutHint: new(atomic.Uint32),
	}

	ft := field.Type
	if ft.Kind() == reflect.Pointer {
		fi.IsPointer = true
		fi.ElemType = ft.Elem()
		ft = ft.Elem()
	}
	if ft.Kind() == reflect.Slice {
		fi.IsSlice = true
		fi.ElemType = ft.Elem()
		ft = ft.Elem()
		if ft.Kind() == reflect.Pointer {
			ft = ft.Elem()
		}
	}

	valueType, err := goTypeToTypeDB(ft)
	if err != nil {
		return FieldInfo{}, fmt.Errorf("field %s: %w", field.Name, err)
	}
	fi.ValueType = valueType
	return fi, nil
}

// ToDict converts a registered model instance to a map[string]any using
// TypeDB attribute names as keys. Includes "_iid" if set.
func ToDict[T any](instance *T) (map[string]any, error) {
	var zero T
	t := reflect.TypeOf(zero)
	if t.Kind() == reflect.Pointer {
		t = t.Elem()
	}

	info, ok := LookupType(t)
	if !ok {
		return nil, fmt.Errorf("gotype: type %s is not registered", t.Name())
	}

	v := reflect.ValueOf(instance)
	if v.Kind() == reflect.Pointer {
		v = v.Elem()
	}

	result := make(map[string]any)

	// Include IID if present
	iid := getIIDFromValueInfo(v, info)
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
	info, s, err := lookupStrategy[T]()
	if err != nil {
		return "", err
	}
	return s.BuildInsertQuery(info, instance, "e")
}

// ToMatchQuery generates a TypeQL match clause for the given instance (by key fields).
func ToMatchQuery[T any](instance *T) (string, error) {
	info, s, err := lookupStrategy[T]()
	if err != nil {
		return "", err
	}
	return s.BuildMatchByKey(info, instance, "e")
}

func lookupStrategy[T any]() (*ModelInfo, ModelStrategy, error) {
	var zero T
	t := reflect.TypeOf(zero)
	if t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	info, ok := LookupType(t)
	if !ok {
		return nil, nil, fmt.Errorf("gotype: type %s is not registered", t.Name())
	}
	return info, strategyFor(info.Kind), nil
}

// toKebabCase converts a PascalCase Go struct name to kebab-case, treating
// consecutive uppercase runs as initialisms.
// e.g. "UserAccount" → "user-account", "HTTPServer" → "http-server",
// "User2FA" → "user2-fa".
func toKebabCase(name string) string {
	if name == "" {
		return ""
	}
	runes := []rune(name)
	isUpper := func(r rune) bool { return r >= 'A' && r <= 'Z' }
	var b strings.Builder
	for i, r := range runes {
		if isUpper(r) {
			// Start a new word when the previous rune is not uppercase (end of a
			// lowercase/digit run), or when this uppercase rune starts a new word
			// after an initialism run (next rune is lowercase).
			startsWord := i > 0 && (!isUpper(runes[i-1]) ||
				(i+1 < len(runes) && runes[i+1] >= 'a' && runes[i+1] <= 'z'))
			if startsWord {
				b.WriteByte('-')
			}
			b.WriteByte(byte(r - 'A' + 'a'))
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// goTypeToTypeDB maps Go types to TypeDB value type strings. It returns an
// error for Go types that have no TypeDB attribute value type equivalent so
// registration fails early instead of emitting a broken schema.
func goTypeToTypeDB(t reflect.Type) (string, error) {
	switch t.Kind() {
	case reflect.String:
		return "string", nil
	case reflect.Bool:
		return "boolean", nil
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return "integer", nil
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return "integer", nil
	case reflect.Float32, reflect.Float64:
		return "double", nil
	default:
		if t == reflect.TypeOf(time.Time{}) {
			return "datetime", nil
		}
		return "", fmt.Errorf("unsupported type %s for a TypeDB attribute", t)
	}
}
