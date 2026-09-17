// Package given defines typed TypeQL input rows without a driver or CGo dependency.
package given

import (
	json "encoding/json/v2"
	"fmt"
)

// Rows supplies typed input rows for a TypeQL given stage.
type Rows interface {
	MarshalGivenRows() ([]byte, error)
}

// Value is a typed value in a TypeQL given row.
type Value struct {
	Type  string `json:"type"`
	Value any    `json:"value,omitzero"`
}

// TypedRows contains values ordered by the declared TypeQL variable names.
type TypedRows struct {
	Variables []string  `json:"variables"`
	Rows      [][]Value `json:"rows"`
}

// NewRows declares the variables shared by every row.
func NewRows(variables ...string) *TypedRows {
	return &TypedRows{Variables: append([]string(nil), variables...)}
}

// Add appends one row of values in variable order.
func (r *TypedRows) Add(values ...Value) error {
	if r == nil {
		return fmt.Errorf("given rows is nil")
	}
	if len(values) != len(r.Variables) {
		return fmt.Errorf("given row has %d values, expected %d", len(values), len(r.Variables))
	}
	r.Rows = append(r.Rows, append([]Value(nil), values...))
	return nil
}

// MarshalGivenRows validates and encodes the rows for the driver.
func (r *TypedRows) MarshalGivenRows() ([]byte, error) {
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
	}
	return json.Marshal(r)
}
