// Package given defines the input-row contract for TypeQL given stages.
package given

// Rows supplies typed input rows for a TypeQL given stage.
type Rows interface {
	MarshalGivenRows() ([]byte, error)
}
