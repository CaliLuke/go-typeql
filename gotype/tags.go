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
	// Skip indicates the field should be ignored by the ORM.
	Skip bool
}

// IsRole returns true if the tag identifies the field as a role player in a relation.
func (ft FieldTag) IsRole() bool {
	return ft.RoleName != ""
}

// ParseTag parses the content of a `typedb` struct tag into a FieldTag structure.
// It supports options like key, unique, cardinality (card=M..N), roles (role:name),
// and type name overrides (type:name).
func ParseTag(tag string) (FieldTag, error) {
	if tag == "" || tag == "-" {
		return FieldTag{Skip: tag == "-"}, nil
	}

	parts := strings.Split(tag, ",")
	ft := FieldTag{}

	for i, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}

		if i == 0 && !strings.Contains(part, "=") && !strings.Contains(part, ":") &&
			part != "key" && part != "unique" && part != "abstract" && part != "-" {
			ft.Name = part
			continue
		}

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
			ft.RoleName = strings.TrimPrefix(part, "role:")
		case strings.HasPrefix(part, "type:"):
			ft.TypeName = strings.TrimPrefix(part, "type:")
		case strings.HasPrefix(part, "card="):
			cardStr := strings.TrimPrefix(part, "card=")
			min, max, err := parseCardinality(cardStr)
			if err != nil {
				return FieldTag{}, fmt.Errorf("invalid cardinality %q: %w", cardStr, err)
			}
			ft.CardMin = min
			ft.CardMax = max
		default:
			if i == 0 {
				ft.Name = part
			} else {
				return FieldTag{}, fmt.Errorf("unknown tag option: %q", part)
			}
		}
	}

	return ft, nil
}

// parseCardinality parses cardinality strings like "0..1", "1..5", "2..", "0+".
func parseCardinality(s string) (min *int, max *int, err error) {
	// Handle shorthand: "0+" means 0..unbounded
	if strings.HasSuffix(s, "+") {
		v, err := strconv.Atoi(strings.TrimSuffix(s, "+"))
		if err != nil {
			return nil, nil, fmt.Errorf("invalid min value: %w", err)
		}
		return intPtr(v), nil, nil
	}

	// Handle range: "M..N" or "M.."
	parts := strings.Split(s, "..")
	if len(parts) != 2 {
		return nil, nil, fmt.Errorf("expected format M..N or M.., got %q", s)
	}

	v, err := strconv.Atoi(parts[0])
	if err != nil {
		return nil, nil, fmt.Errorf("invalid min value: %w", err)
	}
	minVal := intPtr(v)

	if parts[1] == "" {
		return minVal, nil, nil // unbounded max
	}

	v, err = strconv.Atoi(parts[1])
	if err != nil {
		return nil, nil, fmt.Errorf("invalid max value: %w", err)
	}
	return minVal, intPtr(v), nil
}

func intPtr(v int) *int {
	return &v
}
