// Package gotype provides mechanisms for hydrating Go structs from TypeDB results.
package gotype

import (
	"fmt"
	"math"
	"reflect"
	"strconv"
	"sync/atomic"
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
	if v.Kind() != reflect.Pointer || v.IsNil() {
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
	for i := range info.Fields {
		fi := &info.Fields[i]
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
		if field.Kind() == reflect.Pointer && field.Type().Elem() == playerInfo.GoType {
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
	if t.Kind() == reflect.Pointer {
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

func setFieldValue(field reflect.Value, fi *FieldInfo, val any) error {
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

func trySetScalarField(field reflect.Value, fi *FieldInfo, val any) bool {
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
		t, ok := coerceTimeFast(val, fi)
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

// setIntegerFast sets an integer-typed field. It refuses values that would
// overflow a narrow target (e.g. 300 into an int8), handing them to the slow
// path so hydration fails with a descriptive error instead of reflect's
// silent truncation (issue #52).
func setIntegerFast(field reflect.Value, fi *FieldInfo, targetType reflect.Type, val any) bool {
	switch targetType.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		i64, ok := coerceInt64Fast(val)
		if !ok || reflect.Zero(targetType).OverflowInt(i64) {
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
		if !ok || reflect.Zero(targetType).OverflowUint(u64) {
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

func setFloatFast(field reflect.Value, fi *FieldInfo, targetType reflect.Type, val any) bool {
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

// coerceInt64Fast converts a raw result value to int64 without allocation.
// It refuses (returns false) on non-integral floats and values outside the
// int64 range instead of silently truncating or wrapping (issue #52); the
// slow path (coerceToInt64) then reports a descriptive error.
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
		if uint64(v) > math.MaxInt64 {
			return 0, false
		}
		return int64(v), true
	case uint8:
		return int64(v), true
	case uint16:
		return int64(v), true
	case uint32:
		return int64(v), true
	case uint64:
		if v > math.MaxInt64 {
			return 0, false
		}
		return int64(v), true
	case float32:
		return int64FromFloat(float64(v))
	case float64:
		return int64FromFloat(v)
	default:
		return 0, false
	}
}

// int64FromFloat converts a float to int64 only when it is integral and in
// range. JSON transports TypeDB integers as float64, so integral floats must
// convert, but 3.9 or out-of-range values must not silently become 3 or wrap
// (issue #52).
func int64FromFloat(v float64) (int64, bool) {
	i64, err := float64ToInt64Checked(v)
	return i64, err == nil
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
		return uint64FromFloat(float64(v))
	case float64:
		return uint64FromFloat(v)
	default:
		return 0, false
	}
}

// uint64FromFloat converts a float to uint64 only when it is integral,
// non-negative, and in range (issue #52).
func uint64FromFloat(v float64) (uint64, bool) {
	if v != math.Trunc(v) || v < 0 || v >= math.MaxUint64 {
		return 0, false
	}
	return uint64(v), true
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

var timeCoerceLayouts = [...]string{
	time.RFC3339,
	"2006-01-02T15:04:05",
	"2006-01-02",
}

// coerceTimeFast parses a datetime result value. Zone-less strings parse as
// UTC, matching the write side, which stores plain datetime values as UTC
// wall-clock time (issue #53); fractional seconds are accepted by all
// layouts. The layout cache is a pointer shared by every copy of the
// FieldInfo and is accessed atomically (issue #43).
func coerceTimeFast(val any, fi *FieldInfo) (time.Time, bool) {
	switch v := val.(type) {
	case time.Time:
		return v, true
	case string:
		var hint *atomic.Uint32
		if fi != nil {
			hint = fi.timeLayoutHint
		}
		if hint != nil {
			if idx := hint.Load(); idx > 0 && int(idx-1) < len(timeCoerceLayouts) {
				if t, err := time.Parse(timeCoerceLayouts[idx-1], v); err == nil {
					return t, true
				}
			}
		}
		for i, layout := range timeCoerceLayouts {
			t, err := time.Parse(layout, v)
			if err == nil {
				if hint != nil {
					hint.Store(uint32(i + 1))
				}
				return t, true
			}
		}
	}
	return time.Time{}, false
}

// setSliceField populates a slice-valued field from a single value or a slice
// of raw result values. It supports pointer-to-slice fields (*[]T), slices of
// pointers ([]*T), and named/unsigned element types, none of which may panic
// during hydration (issue #93).
func setSliceField(field reflect.Value, fi *FieldInfo, val any) error {
	sliceType := fi.FieldType
	if sliceType.Kind() == reflect.Pointer {
		sliceType = sliceType.Elem()
	}
	if sliceType.Kind() != reflect.Slice {
		return fmt.Errorf("cannot hydrate multi-valued attribute into %s", fi.FieldType)
	}
	elemType := sliceType.Elem()

	rv := reflect.ValueOf(val)
	var slice reflect.Value
	if rv.Kind() != reflect.Slice {
		// Single value -> wrap in slice
		elem, err := coerceSliceElem(val, fi, elemType)
		if err != nil {
			return err
		}
		slice = reflect.MakeSlice(sliceType, 1, 1)
		slice.Index(0).Set(elem)
	} else {
		slice = reflect.MakeSlice(sliceType, rv.Len(), rv.Len())
		for i := 0; i < rv.Len(); i++ {
			elem, err := coerceSliceElem(rv.Index(i).Interface(), fi, elemType)
			if err != nil {
				return fmt.Errorf("index %d: %w", i, err)
			}
			slice.Index(i).Set(elem)
		}
	}

	if fi.FieldType.Kind() == reflect.Pointer {
		ptr := reflect.New(sliceType)
		ptr.Elem().Set(slice)
		field.Set(ptr)
		return nil
	}
	field.Set(slice)
	return nil
}

// coerceSliceElem coerces a raw result value into a slice element of
// elemType, wrapping the value in a pointer when the element type is a
// pointer (e.g. []*string) and converting to assignment-compatible named
// types.
func coerceSliceElem(val any, fi *FieldInfo, elemType reflect.Type) (reflect.Value, error) {
	converted, err := coerceValue(val, fi)
	if err != nil {
		return reflect.Value{}, err
	}

	base := elemType
	if base.Kind() == reflect.Pointer {
		base = base.Elem()
	}
	cv := reflect.ValueOf(converted)
	if !cv.Type().AssignableTo(base) {
		if !cv.Type().ConvertibleTo(base) {
			return reflect.Value{}, fmt.Errorf("cannot assign %T to slice element type %s", converted, base)
		}
		cv = cv.Convert(base)
	}

	if elemType.Kind() == reflect.Pointer {
		ptr := reflect.New(base)
		ptr.Elem().Set(cv)
		return ptr, nil
	}
	return cv, nil
}

func coerceValue(val any, fi *FieldInfo) (any, error) {
	targetType := fi.ElemType
	if targetType == nil {
		targetType = fi.FieldType
	}
	if targetType.Kind() == reflect.Pointer {
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

	case "decimal":
		return coerceDecimal(val, targetType)

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

// coerceToInt64 converts a raw result value to the integer target type,
// returning an error instead of silently truncating non-integral floats,
// wrapping out-of-range values, or overflowing narrow targets (issue #52).
// Unsigned target kinds are supported so fields like []uint hydrate instead
// of panicking (issue #93). The returned value has exactly targetType's
// dynamic type (including named types).
func coerceToInt64(val any, targetType reflect.Type) (any, error) {
	i64, err := toInt64Checked(val)
	if err != nil {
		return nil, err
	}

	switch targetType.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		if reflect.Zero(targetType).OverflowInt(i64) {
			return nil, fmt.Errorf("value %d overflows %s", i64, targetType)
		}
		out := reflect.New(targetType).Elem()
		out.SetInt(i64)
		return out.Interface(), nil

	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		if i64 < 0 {
			return nil, fmt.Errorf("value %d overflows %s", i64, targetType)
		}
		u64 := uint64(i64)
		if reflect.Zero(targetType).OverflowUint(u64) {
			return nil, fmt.Errorf("value %d overflows %s", i64, targetType)
		}
		out := reflect.New(targetType).Elem()
		out.SetUint(u64)
		return out.Interface(), nil

	default:
		return nil, fmt.Errorf("cannot hydrate integer value into %s", targetType)
	}
}

// toInt64Checked converts a raw result value to int64, rejecting non-integral
// floats and values outside the int64 range (issue #52).
func toInt64Checked(val any) (int64, error) {
	switch v := val.(type) {
	case int:
		return int64(v), nil
	case int8:
		return int64(v), nil
	case int16:
		return int64(v), nil
	case int32:
		return int64(v), nil
	case int64:
		return v, nil
	case uint:
		if uint64(v) > math.MaxInt64 {
			return 0, fmt.Errorf("value %d overflows int64", v)
		}
		return int64(v), nil
	case uint8:
		return int64(v), nil
	case uint16:
		return int64(v), nil
	case uint32:
		return int64(v), nil
	case uint64:
		if v > math.MaxInt64 {
			return 0, fmt.Errorf("value %d overflows int64", v)
		}
		return int64(v), nil
	case float32:
		return float64ToInt64Checked(float64(v))
	case float64:
		return float64ToInt64Checked(v)
	default:
		return 0, fmt.Errorf("cannot coerce %T to integer", val)
	}
}

// float64ToInt64Checked converts a float64 to int64, rejecting non-integral
// and out-of-range values (issue #52).
func float64ToInt64Checked(v float64) (int64, error) {
	if v != math.Trunc(v) {
		return 0, fmt.Errorf("cannot coerce non-integral float %v to integer", v)
	}
	if v < math.MinInt64 || v >= math.MaxInt64 {
		return 0, fmt.Errorf("float value %v overflows int64", v)
	}
	return int64(v), nil
}

// coerceDecimal converts a raw decimal result value into the target Go type.
// The Rust FFI driver transports TypeDB decimal values as strings (e.g.
// "12.5"), so strings are the primary input; string targets receive the
// digits verbatim (exact), float targets parse via strconv.ParseFloat
// (lossy for non-representable fractions). Numeric inputs are accepted
// defensively. The returned value has exactly targetType's dynamic type
// (including named types).
func coerceDecimal(val any, targetType reflect.Type) (any, error) {
	switch targetType.Kind() {
	case reflect.String:
		var s string
		switch v := val.(type) {
		case string:
			s = v
		case []byte:
			s = string(v)
		case float64:
			s = strconv.FormatFloat(v, 'f', -1, 64)
		case float32:
			s = strconv.FormatFloat(float64(v), 'f', -1, 32)
		case int64:
			s = strconv.FormatInt(v, 10)
		default:
			return nil, fmt.Errorf("cannot coerce %T to decimal string", val)
		}
		out := reflect.New(targetType).Elem()
		out.SetString(s)
		return out.Interface(), nil

	case reflect.Float32, reflect.Float64:
		var f64 float64
		switch v := val.(type) {
		case string:
			parsed, err := strconv.ParseFloat(v, 64)
			if err != nil {
				return nil, fmt.Errorf("cannot parse decimal value %q: %w", v, err)
			}
			f64 = parsed
		case float64:
			f64 = v
		case float32:
			f64 = float64(v)
		case int64:
			f64 = float64(v)
		default:
			return nil, fmt.Errorf("cannot coerce %T to decimal float", val)
		}
		out := reflect.New(targetType).Elem()
		out.SetFloat(f64)
		return out.Interface(), nil

	default:
		return nil, fmt.Errorf("cannot hydrate decimal value into %s", targetType)
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
