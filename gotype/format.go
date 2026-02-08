// Package gotype provides utilities for formatting Go values into TypeQL syntax.
package gotype

import (
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
