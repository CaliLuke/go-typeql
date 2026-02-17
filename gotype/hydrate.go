// Package gotype provides mechanisms for hydrating Go structs from TypeDB results.
package gotype

import (
	"fmt"
	"reflect"
	"time"
)

// MaxHydrationDepth is the maximum nesting depth for recursive role player hydration.
// This prevents infinite loops when the database graph contains cycles.
const MaxHydrationDepth = 10

// Hydrate populates the fields of a target struct pointer with data from a map
// of TypeDB attribute names to values. The struct type must be registered.
func Hydrate(target any, data map[string]any) error {
	return hydrateWithDepth(target, data, 0, make(map[string]bool))
}

// hydrateWithDepth performs hydration with cycle detection and depth limiting.
// visited tracks IIDs already hydrated in this call chain to detect cycles.
func hydrateWithDepth(target any, data map[string]any, depth int, visited map[string]bool) error {
	if depth > MaxHydrationDepth {
		return fmt.Errorf("hydration depth exceeded maximum of %d (possible cycle in graph)", MaxHydrationDepth)
	}

	v := reflect.ValueOf(target)
	if v.Kind() != reflect.Ptr || v.IsNil() {
		return fmt.Errorf("target must be a non-nil pointer to struct")
	}
	v = v.Elem()
	if v.Kind() != reflect.Struct {
		return fmt.Errorf("target must point to a struct, got %s", v.Kind())
	}

	info, ok := LookupType(v.Type())
	if !ok {
		return fmt.Errorf("type %s is not registered", v.Type().Name())
	}

	// Set IID if present and check for cycles
	if iid, ok := data["_iid"]; ok {
		if iidStr, ok := iid.(string); ok {
			if visited[iidStr] {
				return nil // cycle detected — stop recursion, leave fields at zero values
			}
			visited[iidStr] = true
			setIID(v, iidStr)
		}
	}

	// Set attribute fields
	for _, fi := range info.Fields {
		val, ok := data[fi.Tag.Name]
		if !ok {
			continue
		}
		if val == nil {
			continue
		}

		field := v.Field(fi.FieldIndex)
		if err := setFieldValue(field, fi, val); err != nil {
			return fmt.Errorf("field %s: %w", fi.FieldName, err)
		}
	}

	// Set role player fields (relations only)
	for _, role := range info.Roles {
		roleData, ok := data[role.RoleName]
		if !ok {
			continue
		}
		roleMap, ok := roleData.(map[string]any)
		if !ok {
			continue
		}

		playerInfo, ok := Lookup(role.PlayerTypeName)
		if !ok {
			continue
		}

		// Create a new instance of the player type and hydrate it
		playerPtr := reflect.New(playerInfo.GoType)
		if err := hydrateWithDepth(playerPtr.Interface(), roleMap, depth+1, visited); err != nil {
			return fmt.Errorf("role %s: %w", role.RoleName, err)
		}

		// Set the field (which is a pointer to the player type)
		field := v.Field(role.FieldIndex)
		if field.Kind() == reflect.Ptr && field.Type().Elem() == playerInfo.GoType {
			field.Set(playerPtr)
		}
	}

	return nil
}

// HydrateNew is a convenience function that creates a new instance of type T,
// hydrates it with the provided data, and returns a pointer to it.
func HydrateNew[T any](data map[string]any) (*T, error) {
	result := new(T)
	if err := Hydrate(result, data); err != nil {
		return nil, err
	}
	return result, nil
}

// HydrateAny creates and hydrates an instance of the concrete type identified
// by the "_type" field in data. This enables true polymorphic hydration where
// the returned value's concrete type matches the TypeDB type label.
// Returns the hydrated instance as any (actual type is a pointer to the concrete struct).
func HydrateAny(data map[string]any) (any, error) {
	typeLabel, ok := data["_type"].(string)
	if !ok {
		return nil, fmt.Errorf("hydrate_any: _type field missing or not a string")
	}

	modelInfo, ok := ResolveType(typeLabel)
	if !ok {
		return nil, fmt.Errorf("hydrate_any: type %q not registered", typeLabel)
	}

	instancePtr := reflect.New(modelInfo.GoType)
	if err := Hydrate(instancePtr.Interface(), data); err != nil {
		return nil, fmt.Errorf("hydrate_any type %s: %w", typeLabel, err)
	}

	return instancePtr.Interface(), nil
}

func setIID(v reflect.Value, iid string) {
	// Look for BaseEntity or BaseRelation embedded field
	for _, fv := range v.Fields() {
		if !fv.CanAddr() {
			continue
		}
		addr := fv.Addr().Interface()
		if e, ok := addr.(*BaseEntity); ok {
			e.SetIID(iid)
			return
		}
		if r, ok := addr.(*BaseRelation); ok {
			r.SetIID(iid)
			return
		}
	}
}

func setFieldValue(field reflect.Value, fi FieldInfo, val any) error {
	if fi.IsSlice {
		return setSliceField(field, fi, val)
	}

	converted, err := coerceValue(val, fi)
	if err != nil {
		return err
	}

	if fi.IsPointer {
		ptr := reflect.New(fi.ElemType)
		ptr.Elem().Set(reflect.ValueOf(converted))
		field.Set(ptr)
	} else {
		field.Set(reflect.ValueOf(converted))
	}
	return nil
}

func setSliceField(field reflect.Value, fi FieldInfo, val any) error {
	rv := reflect.ValueOf(val)
	if rv.Kind() != reflect.Slice {
		// Single value -> wrap in slice
		converted, err := coerceValue(val, fi)
		if err != nil {
			return err
		}
		slice := reflect.MakeSlice(fi.FieldType, 1, 1)
		slice.Index(0).Set(reflect.ValueOf(converted))
		field.Set(slice)
		return nil
	}

	slice := reflect.MakeSlice(fi.FieldType, rv.Len(), rv.Len())
	for i := 0; i < rv.Len(); i++ {
		converted, err := coerceValue(rv.Index(i).Interface(), fi)
		if err != nil {
			return fmt.Errorf("index %d: %w", i, err)
		}
		slice.Index(i).Set(reflect.ValueOf(converted))
	}
	field.Set(slice)
	return nil
}

func coerceValue(val any, fi FieldInfo) (any, error) {
	targetType := fi.ElemType
	if targetType == nil {
		targetType = fi.FieldType
	}
	if targetType.Kind() == reflect.Ptr {
		targetType = targetType.Elem()
	}

	switch fi.ValueType {
	case "string":
		s, ok := val.(string)
		if !ok {
			s = fmt.Sprintf("%v", val)
		}
		return s, nil

	case "long", "integer":
		return coerceToInt64(val, targetType)

	case "double":
		return coerceToFloat64(val, targetType)

	case "boolean":
		b, ok := val.(bool)
		if !ok {
			return nil, fmt.Errorf("expected bool, got %T", val)
		}
		return b, nil

	case "datetime", "datetime-tz", "date":
		return coerceToTime(val)

	default:
		return val, nil
	}
}

func coerceToInt64(val any, targetType reflect.Type) (any, error) {
	var i64 int64
	switch v := val.(type) {
	case float64:
		i64 = int64(v)
	case float32:
		i64 = int64(v)
	case int:
		i64 = int64(v)
	case int64:
		i64 = v
	case int32:
		i64 = int64(v)
	case uint64:
		i64 = int64(v)
	default:
		return nil, fmt.Errorf("cannot coerce %T to integer", val)
	}

	// Convert to the actual target type
	switch targetType.Kind() {
	case reflect.Int:
		return int(i64), nil
	case reflect.Int8:
		return int8(i64), nil
	case reflect.Int16:
		return int16(i64), nil
	case reflect.Int32:
		return int32(i64), nil
	case reflect.Int64:
		return i64, nil
	default:
		return int(i64), nil
	}
}

func coerceToFloat64(val any, targetType reflect.Type) (any, error) {
	var f64 float64
	switch v := val.(type) {
	case float64:
		f64 = v
	case float32:
		f64 = float64(v)
	case int:
		f64 = float64(v)
	case int64:
		f64 = float64(v)
	case uint64:
		f64 = float64(v)
	default:
		return nil, fmt.Errorf("cannot coerce %T to float", val)
	}

	if targetType.Kind() == reflect.Float32 {
		return float32(f64), nil
	}
	return f64, nil
}

func coerceToTime(val any) (any, error) {
	switch v := val.(type) {
	case time.Time:
		return v, nil
	case string:
		// Try common formats
		for _, layout := range []string{
			time.RFC3339,
			"2006-01-02T15:04:05",
			"2006-01-02",
		} {
			t, err := time.Parse(layout, v)
			if err == nil {
				return t, nil
			}
		}
		return nil, fmt.Errorf("cannot parse time string: %q", v)
	default:
		return nil, fmt.Errorf("cannot coerce %T to time.Time", val)
	}
}
