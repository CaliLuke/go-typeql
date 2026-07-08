//go:build integration && cgo && typedb

package gotype_test

import (
	"context"
	"testing"

	"github.com/CaliLuke/go-typeql/gotype"
)

// ---------------------------------------------------------------------------
// Issue 85 regression test: empty In/IIDIn must match nothing on a live
// server, not fail with a query error. The "matches nothing" pattern is a
// self-contradiction (`not { $e is $e; };`) rather than a fabricated IID
// literal, so this exercises the server actually accepting and evaluating it.
// ---------------------------------------------------------------------------

func TestIntegration_EmptyInMatchesNothing(t *testing.T) {
	db := setupTestDBDefault(t)
	ctx := context.Background()

	mgr := gotype.MustNewManager[Person](db)
	persons := seedPersons(t, ctx, mgr)

	t.Run("In with empty set returns zero rows", func(t *testing.T) {
		results, err := mgr.Query().Filter(gotype.In("name", []any{})).All(ctx)
		if err != nil {
			t.Fatalf("empty In query errored: %v", err)
		}
		if len(results) != 0 {
			t.Errorf("expected 0 results for empty In, got %d", len(results))
		}
	})

	t.Run("Count with empty In returns zero", func(t *testing.T) {
		count, err := mgr.Query().Filter(gotype.In("name", []any{})).Count(ctx)
		if err != nil {
			t.Fatalf("empty In count errored: %v", err)
		}
		if count != 0 {
			t.Errorf("expected count 0 for empty In, got %d", count)
		}
	})

	t.Run("IIDIn with no IIDs returns zero rows", func(t *testing.T) {
		results, err := mgr.Query().Filter(gotype.IIDIn()).All(ctx)
		if err != nil {
			t.Fatalf("empty IIDIn query errored: %v", err)
		}
		if len(results) != 0 {
			t.Errorf("expected 0 results for empty IIDIn, got %d", len(results))
		}
	})

	t.Run("NotIn with empty set matches everything", func(t *testing.T) {
		results, err := mgr.Query().Filter(gotype.NotIn("name", []any{})).All(ctx)
		if err != nil {
			t.Fatalf("empty NotIn query errored: %v", err)
		}
		if len(results) != len(persons) {
			t.Errorf("expected %d results for empty NotIn, got %d", len(persons), len(results))
		}
	})

	t.Run("empty In combined with other filters returns zero rows", func(t *testing.T) {
		results, err := mgr.Query().
			Filter(gotype.Gt("age", 0), gotype.In("name", []any{})).
			All(ctx)
		if err != nil {
			t.Fatalf("combined empty In query errored: %v", err)
		}
		if len(results) != 0 {
			t.Errorf("expected 0 results, got %d", len(results))
		}
	})
}
