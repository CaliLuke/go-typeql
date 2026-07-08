// Package gotype provides utilities for formatting Go values into TypeQL syntax.
package gotype

import (
	"fmt"
	"math"
	"reflect"
	"regexp"
	"strconv"
	"strings"

	"github.com/CaliLuke/go-typeql/ast"
)

// FormatValue converts a Go value into its TypeQL literal string representation.
// It handles basic types, pointers, and time.Time, ensuring correct escaping
// for use in TypeQL queries.
//
// This function delegates to ast.FormatGoValue for the actual formatting logic.
func FormatValue(value any) string {
	return ast.FormatGoValue(value)
}

// formatFieldValue formats a Go value as a TypeQL literal using the field's
// TypeDB value type as context. For fields declared with the value:decimal
// tag option it emits a decimal literal with the `dec` suffix (e.g. 12.5dec,
// 3.0dec) so the server stores an exact decimal value instead of a double.
// All other fields format exactly like FormatValue.
//
// Equality gotcha: a plain double literal is not exactly equal to the decimal
// value with the same digits (0.1 as a double is the nearest binary fraction,
// not 0.1dec), so equality comparisons between double literals and decimal
// attributes can silently miss. Write paths with field context therefore emit
// `dec` literals; context-free paths (the Filter combinators, which only know
// attribute names) still emit double literals, which is fine for ordering
// comparisons but not for exact equality on non-representable fractions.
func formatFieldValue(fi *FieldInfo, val any) (string, error) {
	if fi != nil && fi.ValueType == "decimal" {
		return formatDecimalLiteral(val)
	}
	return FormatValue(val), nil
}

// formatDecimalLiteral formats a float or decimal-numeral string as a TypeQL
// decimal literal (`<digits>.<digits>dec`). A fractional part is always
// present (3 → "3.0dec") for compatibility with servers that reject bare
// integer decimal literals.
func formatDecimalLiteral(val any) (string, error) {
	switch v := val.(type) {
	case float64:
		return decimalFromFloat(v)
	case float32:
		return decimalFromFloat(float64(v))
	case string:
		return decimalFromString(v)
	}

	// Pointers and named float/string types take the reflection slow path.
	rv := reflect.ValueOf(val)
	for rv.Kind() == reflect.Pointer {
		if rv.IsNil() {
			return "", fmt.Errorf("cannot format nil %T as a TypeQL decimal literal", val)
		}
		rv = rv.Elem()
	}
	switch rv.Kind() {
	case reflect.Float32, reflect.Float64:
		return decimalFromFloat(rv.Float())
	case reflect.String:
		return decimalFromString(rv.String())
	default:
		return "", fmt.Errorf("cannot format %T as a TypeQL decimal literal", val)
	}
}

func decimalFromFloat(f float64) (string, error) {
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return "", fmt.Errorf("cannot format %v as a TypeQL decimal literal", f)
	}
	s := strconv.FormatFloat(f, 'f', -1, 64)
	if !strings.Contains(s, ".") {
		s += ".0"
	}
	return s + "dec", nil
}

// decimalNumeralRe matches decimal numerals: an optional sign, digits, and an
// optional fraction. Exponents, hex, whitespace, and empty parts are rejected.
var decimalNumeralRe = regexp.MustCompile(`^[+-]?[0-9]+(\.[0-9]+)?$`)

func decimalFromString(s string) (string, error) {
	if !decimalNumeralRe.MatchString(s) {
		return "", fmt.Errorf("invalid decimal numeral %q: expected optional sign, digits, and an optional fraction (e.g. \"12.5\")", s)
	}
	if !strings.Contains(s, ".") {
		s += ".0"
	}
	return s + "dec", nil
}
