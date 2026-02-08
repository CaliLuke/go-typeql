// Package gotype provides utilities for generating TypeQL schema definitions from Go models.
package gotype

import (
	"fmt"
	"strings"
)

// GenerateSchema produces a complete TypeQL `define` query string for all
// models currently registered in the global registry.
func GenerateSchema() string {
	types := RegisteredTypes()
	if len(types) == 0 {
		return ""
	}

	var parts []string
	attrsSeen := make(map[string]bool)

	// Collect all attribute definitions first
	for _, info := range types {
		for _, f := range info.Fields {
			attrKey := f.Tag.Name + ":" + f.ValueType
			if attrsSeen[attrKey] {
				continue
			}
			attrsSeen[attrKey] = true
			parts = append(parts, fmt.Sprintf("attribute %s, value %s;", f.Tag.Name, f.ValueType))
		}
	}

	// Build a map of entity type → plays clauses from relation definitions.
	// TypeDB 3.x requires entities to declare which roles they play.
	playsMap := buildPlaysMap(types)

	// Generate entity/relation definitions
	for _, info := range types {
		def := generateTypeDef(info, playsMap)
		if def != "" {
			parts = append(parts, def)
		}
	}

	return "define\n" + strings.Join(parts, "\n")
}

// GenerateSchemaFor produces a TypeQL `define` query string specifically for
// the provided ModelInfo, including its required attribute declarations.
func GenerateSchemaFor(info *ModelInfo) string {
	var parts []string
	attrsSeen := make(map[string]bool)

	for _, f := range info.Fields {
		attrKey := f.Tag.Name + ":" + f.ValueType
		if attrsSeen[attrKey] {
			continue
		}
		attrsSeen[attrKey] = true
		parts = append(parts, fmt.Sprintf("attribute %s, value %s;", f.Tag.Name, f.ValueType))
	}

	def := generateTypeDef(info, nil)
	if def != "" {
		parts = append(parts, def)
	}

	return "define\n" + strings.Join(parts, "\n")
}

// buildPlaysMap scans relation types and builds a map of entityTypeName → []playsClause.
func buildPlaysMap(types []*ModelInfo) map[string][]string {
	plays := make(map[string][]string)
	for _, info := range types {
		if info.Kind != ModelKindRelation {
			continue
		}
		for _, role := range info.Roles {
			clause := fmt.Sprintf("    plays %s:%s", info.TypeName, role.RoleName)
			plays[role.PlayerTypeName] = append(plays[role.PlayerTypeName], clause)
		}
	}
	return plays
}

func generateTypeDef(info *ModelInfo, playsMap map[string][]string) string {
	var lines []string

	kindStr := "entity"
	if info.Kind == ModelKindRelation {
		kindStr = "relation"
	}

	header := fmt.Sprintf("%s %s", kindStr, info.TypeName)
	if info.IsAbstract {
		header += " @abstract"
	}
	if info.Supertype != "" {
		header += fmt.Sprintf(", sub %s", info.Supertype)
	}
	lines = append(lines, header)

	// Roles (relation only)
	for _, role := range info.Roles {
		lines = append(lines, fmt.Sprintf("    relates %s", role.RoleName))
	}

	// Plays clauses (entity only, TypeDB 3.x)
	if info.Kind == ModelKindEntity && playsMap != nil {
		lines = append(lines, playsMap[info.TypeName]...)
	}

	// Attribute ownerships
	for _, f := range info.Fields {
		ownership := fmt.Sprintf("    owns %s", f.Tag.Name)
		annotations := fieldAnnotations(f)
		if annotations != "" {
			ownership += " " + annotations
		}
		lines = append(lines, ownership)
	}

	return strings.Join(lines, ",\n") + ";"
}

func fieldAnnotations(f FieldInfo) string {
	var anns []string
	if f.Tag.Key {
		anns = append(anns, "@key")
	}
	if f.Tag.Unique {
		anns = append(anns, "@unique")
	}

	// Only add @card if not @key (since @key implies @card(1..1))
	if !f.Tag.Key && (f.Tag.CardMin != nil || f.Tag.CardMax != nil) {
		card := formatCardAnnotation(f.Tag.CardMin, f.Tag.CardMax)
		if card != "" {
			// Skip if @unique with default (1,1)
			isDefault := f.Tag.CardMin != nil && *f.Tag.CardMin == 1 &&
				f.Tag.CardMax != nil && *f.Tag.CardMax == 1
			if !f.Tag.Unique || !isDefault {
				anns = append(anns, card)
			}
		}
	}

	return strings.Join(anns, " ")
}

func formatCardAnnotation(min, max *int) string {
	if min == nil && max == nil {
		return ""
	}
	minStr := "0"
	if min != nil {
		minStr = fmt.Sprintf("%d", *min)
	}
	if max == nil {
		return fmt.Sprintf("@card(%s..)", minStr)
	}
	return fmt.Sprintf("@card(%s..%d)", minStr, *max)
}
