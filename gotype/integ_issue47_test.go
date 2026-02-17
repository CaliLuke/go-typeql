//go:build integration && cgo && typedb

package gotype_test

import (
	"context"
	"testing"

	"github.com/CaliLuke/go-typeql/gotype"
)

// ---------------------------------------------------------------------------
// Issue 47 regression test (ported from Python test_issue_47.py)
// ---------------------------------------------------------------------------

func TestIntegration_UpdateMultipleNoneOptionals(t *testing.T) {
	// Ported from test_update_entity_with_multiple_none_optional_attributes.
	// Updating an entity where multiple optional fields are nil should
	// correctly remove all those attributes (not silently skip them).
	db := setupTestDBWith(t, func() {
		_ = gotype.Register[Profile]()
	})
	ctx := context.Background()

	mgr := gotype.NewManager[Profile](db)

	// Insert with all optional fields set.
	assertInsert(t, ctx, mgr, &Profile{
		Username: "issue47",
		Bio:      new("A bio"),
		Score:    new(99.9),
		Active:   new(true),
		Level:    new(5),
	})

	fetched := assertGetOne(t, ctx, mgr, map[string]any{"username": "issue47"})

	// Verify all set.
	if fetched.Bio == nil || fetched.Score == nil || fetched.Active == nil || fetched.Level == nil {
		t.Fatal("precondition: expected all optional fields to be set")
	}

	// Set ALL optional fields to nil.
	fetched.Bio = nil
	fetched.Score = nil
	fetched.Active = nil
	fetched.Level = nil
	assertUpdate(t, ctx, mgr, fetched)

	// Verify all removed.
	updated := assertGetOne(t, ctx, mgr, map[string]any{"username": "issue47"})
	if updated.Bio != nil {
		t.Errorf("expected bio nil, got %q", *updated.Bio)
	}
	if updated.Score != nil {
		t.Errorf("expected score nil, got %v", *updated.Score)
	}
	if updated.Active != nil {
		t.Errorf("expected active nil, got %v", *updated.Active)
	}
	if updated.Level != nil {
		t.Errorf("expected level nil, got %v", *updated.Level)
	}
}
