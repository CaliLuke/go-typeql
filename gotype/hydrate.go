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
	v, info, err := hydrateTargetInfo(target)
	if err != nil {
		return err
	}
	var visited map[string]bool
	if len(info.Roles) > 0 {
		visited = make(map[string]bool)
	}
	return hydrateValueWithDepth(v, info, data, 0, visited)
}

func hydrateTargetInfo(target any) (reflect.Value, *ModelInfo, error) {
	v := reflect.ValueOf(target)
	if v.Kind() != reflect.Ptr || v.IsNil() {
		return reflect.Value{}, nil, fmt.Errorf("target must be a non-nil pointer to struct")
	}
	v = v.Elem()
	if v.Kind() != reflect.Struct {
		return reflect.Value{}, nil, fmt.Errorf("target must point to a struct, got %s", v.Kind())
	}

	info, ok := LookupType(v.Type())
	if !ok {
		return reflect.Value{}, nil, fmt.Errorf("type %s is not registered", v.Type().Name())
	}
	return v, info, nil
}

func hydrateValueWithDepth(v reflect.Value, info *ModelInfo, data map[string]any, depth int, visited map[string]bool) error {
	if depth > MaxHydrationDepth {
		return fmt.Errorf("hydration depth exceeded maximum of %d (possible cycle in graph)", MaxHydrationDepth)
	}

	// Set IID if present and check for cycles
	if iid, ok := lookupResultValue(data, "_iid"); ok {
		if iidStr, ok := iid.(string); ok {
			if visited != nil {
				if visited[iidStr] {
					return nil // cycle detected — stop recursion, leave fields at zero values
				}
				visited[iidStr] = true
			}
			setIIDWithInfo(v, info, iidStr)
		}
	}

	// Set attribute fields
	for _, fi := range info.Fields {
		val, ok := lookupResultValue(data, fi.Tag.Name)
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
		roleData, ok := lookupResultValue(data, role.RoleName)
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
		nextVisited := visited
		if nextVisited == nil {
			nextVisited = make(map[string]bool)
		}
		if err := hydrateValueWithDepth(playerPtr.Elem(), playerInfo, roleMap, depth+1, nextVisited); err != nil {
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
	var zero T
	t := reflect.TypeOf(zero)
	if t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	info, ok := LookupType(t)
	if !ok {
		return nil, fmt.Errorf("type %s is not registered", t.Name())
	}
	return hydrateNewWithInfo[T](info, data)
}

// HydrateAny creates and hydrates an instance of the concrete type identified
// by the "_type" field in data. This enables true polymorphic hydration where
// the returned value's concrete type matches the TypeDB type label.
// Returns the hydrated instance as any (actual type is a pointer to the concrete struct).
func HydrateAny(data map[string]any) (any, error) {
	typeVal, ok := lookupResultValue(data, "_type")
	if !ok {
		return nil, fmt.Errorf("hydrate_any: _type field missing or not a string")
	}
	typeLabel, ok := typeVal.(string)
	if !ok {
		return nil, fmt.Errorf("hydrate_any: _type field missing or not a string")
	}

	modelInfo, ok := ResolveType(typeLabel)
	if !ok {
		return nil, fmt.Errorf("hydrate_any: type %q not registered", typeLabel)
	}

	instancePtr := reflect.New(modelInfo.GoType)
	var visited map[string]bool
	if len(modelInfo.Roles) > 0 {
		visited = make(map[string]bool)
	}
	if err := hydrateValueWithDepth(instancePtr.Elem(), modelInfo, data, 0, visited); err != nil {
		return nil, fmt.Errorf("hydrate_any type %s: %w", typeLabel, err)
	}

	return instancePtr.Interface(), nil
}

func hydrateNewWithInfo[T any](info *ModelInfo, data map[string]any) (*T, error) {
	result := new(T)
	v := reflect.ValueOf(result).Elem()
	if v.Kind() != reflect.Struct {
		return nil, fmt.Errorf("target must point to a struct, got %s", v.Kind())
	}
	var visited map[string]bool
	if len(info.Roles) > 0 {
		visited = make(map[string]bool)
	}
	if err := hydrateValueWithDepth(v, info, data, 0, visited); err != nil {
		return nil, err
	}
	return result, nil
}

func setIIDWithInfo(v reflect.Value, info *ModelInfo, iid string) {
	if info != nil && info.baseFieldIndex >= 0 {
		setIIDOnBaseField(v.Field(info.baseFieldIndex), iid)
		return
	}
	for _, fv := range v.Fields() {
		if setIIDOnBaseField(fv, iid) {
			return
		}
	}
}

func setIIDOnBaseField(fv reflect.Value, iid string) bool {
	if !fv.CanAddr() {
		return false
	}
	addr := fv.Addr()
	if e, ok := reflect.TypeAssert[*BaseEntity](addr); ok {
		e.SetIID(iid)
		return true
	}
	if r, ok := reflect.TypeAssert[*BaseRelation](addr); ok {
		r.SetIID(iid)
		return true
	}
	return false
}

func setFieldValue(field reflect.Value, fi FieldInfo, val any) error {
	if fi.IsSlice {
		return setSliceField(field, fi, val)
	}

	if trySetScalarField(field, fi, val) {
		return nil
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

func trySetScalarField(field reflect.Value, fi FieldInfo, val any) bool {
	targetType := fi.FieldType
	if fi.IsPointer {
		targetType = fi.ElemType
	}
	if targetType == nil {
		return false
	}

	switch fi.ValueType {
	case "string":
		s, ok := coerceStringFast(val)
		if !ok {
			return false
		}
		if fi.IsPointer {
			ptr := reflect.New(targetType)
			ptr.Elem().SetString(s)
			field.Set(ptr)
		} else {
			field.SetString(s)
		}
		return true

	case "long", "integer":
		return setIntegerFast(field, fi, targetType, val)

	case "double":
		return setFloatFast(field, fi, targetType, val)

	case "boolean":
		b, ok := val.(bool)
		if !ok {
			return false
		}
		if fi.IsPointer {
			ptr := reflect.New(targetType)
			ptr.Elem().SetBool(b)
			field.Set(ptr)
		} else {
			field.SetBool(b)
		}
		return true

	case "datetime", "datetime-tz", "date":
		t, ok := coerceTimeFast(val)
		if !ok || targetType != reflect.TypeOf(time.Time{}) {
			return false
		}
		if fi.IsPointer {
			ptr := reflect.New(targetType)
			ptr.Elem().Set(reflect.ValueOf(t))
			field.Set(ptr)
		} else {
			field.Set(reflect.ValueOf(t))
		}
		return true
	}

	return false
}

func coerceStringFast(val any) (string, bool) {
	switch v := val.(type) {
	case string:
		return v, true
	case []byte:
		return string(v), true
	default:
		return "", false
	}
}

func setIntegerFast(field reflect.Value, fi FieldInfo, targetType reflect.Type, val any) bool {
	switch targetType.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		i64, ok := coerceInt64Fast(val)
		if !ok {
			return false
		}
		if fi.IsPointer {
			ptr := reflect.New(targetType)
			ptr.Elem().SetInt(i64)
			field.Set(ptr)
		} else {
			field.SetInt(i64)
		}
		return true

	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		u64, ok := coerceUint64Fast(val)
		if !ok {
			return false
		}
		if fi.IsPointer {
			ptr := reflect.New(targetType)
			ptr.Elem().SetUint(u64)
			field.Set(ptr)
		} else {
			field.SetUint(u64)
		}
		return true
	}

	return false
}

func setFloatFast(field reflect.Value, fi FieldInfo, targetType reflect.Type, val any) bool {
	f64, ok := coerceFloat64Fast(val)
	if !ok {
		return false
	}
	if fi.IsPointer {
		ptr := reflect.New(targetType)
		ptr.Elem().SetFloat(f64)
		field.Set(ptr)
	} else {
		field.SetFloat(f64)
	}
	return targetType.Kind() == reflect.Float32 || targetType.Kind() == reflect.Float64
}

func coerceInt64Fast(val any) (int64, bool) {
	switch v := val.(type) {
	case int:
		return int64(v), true
	case int8:
		return int64(v), true
	case int16:
		return int64(v), true
	case int32:
		return int64(v), true
	case int64:
		return v, true
	case uint:
		return int64(v), true
	case uint8:
		return int64(v), true
	case uint16:
		return int64(v), true
	case uint32:
		return int64(v), true
	case uint64:
		return int64(v), true
	case float32:
		return int64(v), true
	case float64:
		return int64(v), true
	default:
		return 0, false
	}
}

func coerceUint64Fast(val any) (uint64, bool) {
	switch v := val.(type) {
	case uint:
		return uint64(v), true
	case uint8:
		return uint64(v), true
	case uint16:
		return uint64(v), true
	case uint32:
		return uint64(v), true
	case uint64:
		return v, true
	case int:
		if v < 0 {
			return 0, false
		}
		return uint64(v), true
	case int8:
		if v < 0 {
			return 0, false
		}
		return uint64(v), true
	case int16:
		if v < 0 {
			return 0, false
		}
		return uint64(v), true
	case int32:
		if v < 0 {
			return 0, false
		}
		return uint64(v), true
	case int64:
		if v < 0 {
			return 0, false
		}
		return uint64(v), true
	case float32:
		if v < 0 {
			return 0, false
		}
		return uint64(v), true
	case float64:
		if v < 0 {
			return 0, false
		}
		return uint64(v), true
	default:
		return 0, false
	}
}

func coerceFloat64Fast(val any) (float64, bool) {
	switch v := val.(type) {
	case float32:
		return float64(v), true
	case float64:
		return v, true
	case int:
		return float64(v), true
	case int8:
		return float64(v), true
	case int16:
		return float64(v), true
	case int32:
		return float64(v), true
	case int64:
		return float64(v), true
	case uint:
		return float64(v), true
	case uint8:
		return float64(v), true
	case uint16:
		return float64(v), true
	case uint32:
		return float64(v), true
	case uint64:
		return float64(v), true
	default:
		return 0, false
	}
}

func coerceTimeFast(val any) (time.Time, bool) {
	switch v := val.(type) {
	case time.Time:
		return v, true
	case string:
		for _, layout := range []string{
			time.RFC3339,
			"2006-01-02T15:04:05",
			"2006-01-02",
		} {
			t, err := time.Parse(layout, v)
			if err == nil {
				return t, true
			}
		}
	}
	return time.Time{}, false
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
		switch v := val.(type) {
		case string:
			return v, nil
		case []byte:
			return string(v), nil
		}
		return fmt.Sprint(val), nil

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

func lookupResultValue(data map[string]any, key string) (any, bool) {
	val, ok := data[key]
	if !ok {
		return nil, false
	}
	return unwrapValue(val), true
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
