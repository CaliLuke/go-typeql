//go:build cgo && typedb

package driver

import (
	"encoding/json"
	"fmt"
)

// GivenRows contains typed input rows for a TypeQL query with a given stage.
//
// Variables must match the variable names declared by the query's given stage,
// without the leading "$". Each row must have exactly one value per variable.
type GivenRows struct {
	Variables []string       `json:"variables"`
	Rows      [][]GivenValue `json:"rows"`
}

// GivenValue is a typed value or opaque concept handle for a given input row.
type GivenValue struct {
	Type  GivenValueType `json:"type"`
	Value any            `json:"value,omitempty"`
}

// GivenValueType identifies the TypeDB value type for a given row entry.
type GivenValueType string

const (
	GivenEmpty      GivenValueType = "empty"
	GivenConcept    GivenValueType = "concept"
	GivenBoolean    GivenValueType = "boolean"
	GivenInteger    GivenValueType = "integer"
	GivenDouble     GivenValueType = "double"
	GivenString     GivenValueType = "string"
	GivenDecimal    GivenValueType = "decimal"
	GivenDate       GivenValueType = "date"
	GivenDatetime   GivenValueType = "datetime"
	GivenDatetimeTZ GivenValueType = "datetime-tz"
	GivenDuration   GivenValueType = "duration"
)

// Concept is an opaque TypeDB concept returned by a row query.
//
// Handle is only meaningful to this process and can be passed back with
// ConceptGiven. Kind, Type, and IID are exposed for diagnostics and filtering.
type Concept struct {
	Handle string
	Kind   string
	Type   string
	IID    string
}

// NewGivenRows creates a GivenRows value with the given variable names.
func NewGivenRows(variables ...string) *GivenRows {
	return &GivenRows{Variables: append([]string(nil), variables...)}
}

// Add appends a row. It returns an error if the row width does not match the
// declared variable count.
func (r *GivenRows) Add(values ...GivenValue) error {
	if r == nil {
		return fmt.Errorf("given rows is nil")
	}
	if len(values) != len(r.Variables) {
		return fmt.Errorf("given row has %d values, expected %d", len(values), len(r.Variables))
	}
	r.Rows = append(r.Rows, append([]GivenValue(nil), values...))
	return nil
}

// MustAdd appends a row and panics if the row is invalid.
func (r *GivenRows) MustAdd(values ...GivenValue) *GivenRows {
	if err := r.Add(values...); err != nil {
		panic(err)
	}
	return r
}

func (r *GivenRows) json() ([]byte, error) {
	if r == nil {
		return nil, fmt.Errorf("given rows is nil")
	}
	if len(r.Variables) == 0 {
		return nil, fmt.Errorf("given rows must declare at least one variable")
	}
	for i, variable := range r.Variables {
		if variable == "" {
			return nil, fmt.Errorf("given variable %d is empty", i)
		}
	}
	for i, row := range r.Rows {
		if len(row) != len(r.Variables) {
			return nil, fmt.Errorf("given row %d has %d values, expected %d", i, len(row), len(r.Variables))
		}
		for j, value := range row {
			if err := validateGivenValue(value); err != nil {
				return nil, fmt.Errorf("given row %d value %d: %w", i, j, err)
			}
		}
	}
	return json.Marshal(r)
}

func validateGivenValue(value GivenValue) error {
	if value.Type == GivenConcept {
		handle, ok := value.Value.(string)
		if !ok || handle == "" {
			return fmt.Errorf("concept given value requires a non-empty opaque handle")
		}
	}
	return nil
}

// EmptyGiven creates an empty given row entry.
func EmptyGiven() GivenValue {
	return GivenValue{Type: GivenEmpty}
}

// ConceptGiven creates a concept given row entry from an opaque concept handle.
func ConceptGiven(v Concept) GivenValue {
	return GivenValue{Type: GivenConcept, Value: v.Handle}
}

// BoolGiven creates a boolean given row entry.
func BoolGiven(v bool) GivenValue {
	return GivenValue{Type: GivenBoolean, Value: v}
}

// IntGiven creates an integer given row entry.
func IntGiven(v int64) GivenValue {
	return GivenValue{Type: GivenInteger, Value: v}
}

// DoubleGiven creates a double given row entry.
func DoubleGiven(v float64) GivenValue {
	return GivenValue{Type: GivenDouble, Value: v}
}

// StringGiven creates a string given row entry.
func StringGiven(v string) GivenValue {
	return GivenValue{Type: GivenString, Value: v}
}

// DecimalGiven creates a decimal given row entry from its TypeDB decimal string
// representation.
func DecimalGiven(v string) GivenValue {
	return GivenValue{Type: GivenDecimal, Value: v}
}

// DateGiven creates a date given row entry from an ISO-8601 date string.
func DateGiven(v string) GivenValue {
	return GivenValue{Type: GivenDate, Value: v}
}

// DatetimeGiven creates a datetime given row entry from an ISO-8601 local
// datetime string.
func DatetimeGiven(v string) GivenValue {
	return GivenValue{Type: GivenDatetime, Value: v}
}

// DatetimeTZGiven creates a datetime-tz given row entry from an ISO-8601
// timestamp with timezone.
func DatetimeTZGiven(v string) GivenValue {
	return GivenValue{Type: GivenDatetimeTZ, Value: v}
}

// DurationGiven creates a duration given row entry from a TypeDB duration string.
func DurationGiven(v string) GivenValue {
	return GivenValue{Type: GivenDuration, Value: v}
}

// AsConcept extracts an opaque TypeDB concept from a query result value.
func AsConcept(value any) (Concept, bool) {
	fields, ok := value.(map[string]any)
	if !ok {
		return Concept{}, false
	}
	handle, ok := fields["_concept_handle"].(string)
	if !ok || handle == "" {
		return Concept{}, false
	}
	return Concept{
		Handle: handle,
		Kind:   stringField(fields, "_kind"),
		Type:   stringField(fields, "_type"),
		IID:    stringField(fields, "_iid"),
	}, true
}

func stringField(fields map[string]any, key string) string {
	value, _ := fields[key].(string)
	return value
}
