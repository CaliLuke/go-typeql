// Package tqlgen provides utilities for transforming TypeDB names into Go-idiomatic names.
package tqlgen

import (
	"strings"
	"unicode"
)

// splitName splits a string on hyphens and underscores.
func splitName(name string) []string {
	return strings.FieldsFunc(name, func(r rune) bool {
		return r == '-' || r == '_'
	})
}

// ToPascalCase transforms a kebab-case or snake_case string into PascalCase.
func ToPascalCase(name string) string {
	// Split on underscores and hyphens
	parts := splitName(name)
	var b strings.Builder
	for _, part := range parts {
		if part == "" {
			continue
		}
		// Capitalize first letter, lowercase rest
		runes := []rune(part)
		b.WriteRune(unicode.ToUpper(runes[0]))
		for _, r := range runes[1:] {
			b.WriteRune(unicode.ToLower(r))
		}
	}
	return b.String()
}

// ToSnakeCase transforms a kebab-case string into snake_case.
func ToSnakeCase(name string) string {
	return strings.ReplaceAll(name, "-", "_")
}

// CommonAcronyms defines a set of common abbreviations that should be fully
// uppercased when generating Go names.
var CommonAcronyms = map[string]string{
	"id":   "ID",
	"url":  "URL",
	"uuid": "UUID",
	"api":  "API",
	"http": "HTTP",
	"iid":  "IID",
	"nf":   "NF",
}

// ToPascalCaseAcronyms transforms a string into PascalCase while preserving
// the casing of common Go acronyms.
func ToPascalCaseAcronyms(name string) string {
	parts := splitName(name)
	var b strings.Builder
	for _, part := range parts {
		if part == "" {
			continue
		}
		lower := strings.ToLower(part)
		if acronym, ok := CommonAcronyms[lower]; ok {
			b.WriteString(acronym)
			continue
		}
		runes := []rune(lower)
		b.WriteRune(unicode.ToUpper(runes[0]))
		for _, r := range runes[1:] {
			b.WriteRune(r)
		}
	}
	return b.String()
}
