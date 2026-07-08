// Package gotype provides parsing and representation of 'typedb' struct tags.
package gotype

import (
	"fmt"
	"strconv"
	"strings"
)

// FieldTag contains the structured representation of a parsed `typedb` struct tag.
type FieldTag struct {
	// Name is the TypeDB attribute name.
	Name string
	// Key specifies if the attribute is a primary key (@key).
	Key bool
	// Unique specifies if the attribute value must be unique (@unique).
	Unique bool
	// CardMin is the minimum cardinality constraint.
	CardMin *int
	// CardMax is the maximum cardinality constraint.
	CardMax *int
	// RoleName is the name of the role for relation player fields.
	RoleName string
	// Abstract marks the model type as abstract.
	Abstract bool
	// TypeName provides an explicit override for the TypeDB type name.
	TypeName string
	// Sub names the TypeDB supertype of the model (sub:parent-name).
	Sub string
	// Skip indicates the field should be ignored by the ORM.
	Skip bool
}

// hasFieldOptions reports whether the tag carries attribute-level options
// that only make sense together with an attribute name.
func (ft FieldTag) hasFieldOptions() bool {
	return ft.Key || ft.Unique || ft.CardMin != nil || ft.CardMax != nil
}

// hasTypeLevelOptions reports whether the tag carries type-level options
// (abstract, type:, sub:) that apply to the model rather than a field.
func (ft FieldTag) hasTypeLevelOptions() bool {
	return ft.Abstract || ft.TypeName != "" || ft.Sub != ""
}

// IsRole returns true if the tag identifies the field as a role player in a relation.
func (ft FieldTag) IsRole() bool {
	return ft.RoleName != ""
}

// ParseTag parses the content of a `typedb` struct tag into a FieldTag structure.
// It supports options like key, unique, cardinality (card=M..N), roles (role:name),
// type name overrides (type:name), and supertype declarations (sub:parent-name).
func ParseTag(tag string) (FieldTag, error) {
	if tag == "" || tag == "-" {
		return FieldTag{Skip: tag == "-"}, nil
	}

	ft := FieldTag{}
	for i, raw := range strings.Split(tag, ",") {
		part := strings.TrimSpace(raw)
		if part == "" {
			continue
		}
		if i == 0 && isBareName(part) {
			ft.Name = part
			continue
		}
		if err := applyTagOption(&ft, part, i == 0); err != nil {
			return FieldTag{}, err
		}
	}
	return ft, nil
}

func isBareName(part string) bool {
	if strings.ContainsAny(part, "=:") {
		return false
	}
	switch part {
	case "key", "unique", "abstract", "-":
		return false
	}
	return true
}

func applyTagOption(ft *FieldTag, part string, isFirst bool) error {
	switch {
	case part == "key":
		ft.Key = true
	case part == "unique":
		ft.Unique = true
	case part == "abstract":
		ft.Abstract = true
	case part == "-":
		ft.Skip = true
	case strings.HasPrefix(part, "role:"):
		name := strings.TrimSpace(strings.TrimPrefix(part, "role:"))
		if name == "" {
			return fmt.Errorf("tag option %q: role name cannot be empty", part)
		}
		ft.RoleName = name
	case strings.HasPrefix(part, "type:"):
		name := strings.TrimSpace(strings.TrimPrefix(part, "type:"))
		if name == "" {
			return fmt.Errorf("tag option %q: type name cannot be empty", part)
		}
		ft.TypeName = name
	case strings.HasPrefix(part, "sub:"):
		name := strings.TrimSpace(strings.TrimPrefix(part, "sub:"))
		if name == "" {
			return fmt.Errorf("tag option %q: supertype name cannot be empty", part)
		}
		ft.Sub = name
	case strings.HasPrefix(part, "card="):
		cardStr := strings.TrimPrefix(part, "card=")
		min, max, err := parseCardinality(cardStr)
		if err != nil {
			return fmt.Errorf("invalid cardinality %q: %w", cardStr, err)
		}
		ft.CardMin = min
		ft.CardMax = max
	default:
		if isFirst {
			ft.Name = part
			return nil
		}
		return fmt.Errorf("unknown tag option: %q", part)
	}
	return nil
}

// parseCardinality parses cardinality strings like "0..1", "1..5", "2..", "0+".
// Negative bounds and inverted ranges (min > max) are rejected.
func parseCardinality(s string) (min *int, max *int, err error) {
	// Handle shorthand: "0+" means 0..unbounded
	if strings.HasSuffix(s, "+") {
		minV, err := strconv.Atoi(strings.TrimSuffix(s, "+"))
		if err != nil {
			return nil, nil, fmt.Errorf("invalid min value: %w", err)
		}
		if minV < 0 {
			return nil, nil, fmt.Errorf("min value cannot be negative, got %d", minV)
		}
		return new(minV), nil, nil
	}

	// Handle range: "M..N" or "M.."
	parts := strings.Split(s, "..")
	if len(parts) != 2 {
		return nil, nil, fmt.Errorf("expected format M..N or M.., got %q", s)
	}

	minV, err := strconv.Atoi(parts[0])
	if err != nil {
		return nil, nil, fmt.Errorf("invalid min value: %w", err)
	}
	if minV < 0 {
		return nil, nil, fmt.Errorf("min value cannot be negative, got %d", minV)
	}

	if parts[1] == "" {
		return new(minV), nil, nil // unbounded max
	}

	maxV, err := strconv.Atoi(parts[1])
	if err != nil {
		return nil, nil, fmt.Errorf("invalid max value: %w", err)
	}
	if maxV < 0 {
		return nil, nil, fmt.Errorf("max value cannot be negative, got %d", maxV)
	}
	if minV > maxV {
		return nil, nil, fmt.Errorf("min value %d exceeds max value %d", minV, maxV)
	}
	return new(minV), new(maxV), nil
}
