//go:build cgo && typedb

package driver

import (
	"context"

	"github.com/CaliLuke/go-typeql/given"
)

// QueryWithGivenRows executes a query using the pure-Go typed-row contract.
// It keeps ORM callers independent of the driver while preserving cancellation.
func (t *Transaction) QueryWithGivenRows(ctx context.Context, query string, rows *given.TypedRows) ([]map[string]any, error) {
	if rows == nil {
		return t.QueryWithContext(ctx, query)
	}
	return t.QueryWithContextAndOptions(ctx, query, nil, rows)
}
