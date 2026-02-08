//go:build integration && cgo && typedb

package gotype_test

import (
	"context"
	"testing"

	"github.com/CaliLuke/go-typeql/gotype"
)

// ---------------------------------------------------------------------------
// UpdateMany integration tests
// ---------------------------------------------------------------------------

func TestIntegration_UpdateMany(t *testing.T) {
	db := setupTestDBDefault(t)
	ctx := context.Background()
	mgr := gotype.NewManager[Person](db)

	persons := seedPersons(t, ctx, mgr)

	// Update emails for the first two persons
	persons[0].Email = "updated-alice@test.com"
	persons[1].Email = "updated-bob@test.com"

	if err := mgr.UpdateMany(ctx, persons[:2]); err != nil {
		t.Fatalf("UpdateMany failed: %v", err)
	}

	// Verify updates persisted
	alice := assertGetOne(t, ctx, mgr, map[string]any{"name": "Alice"})
	if alice.Email != "updated-alice@test.com" {
		t.Errorf("expected updated email for Alice, got %q", alice.Email)
	}

	bob := assertGetOne(t, ctx, mgr, map[string]any{"name": "Bob"})
	if bob.Email != "updated-bob@test.com" {
		t.Errorf("expected updated email for Bob, got %q", bob.Email)
	}

	// Verify others unchanged
	charlie := assertGetOne(t, ctx, mgr, map[string]any{"name": "Charlie"})
	if charlie.Email != "charlie@example.com" {
		t.Errorf("Charlie's email should be unchanged, got %q", charlie.Email)
	}
}

func TestIntegration_UpdateMany_Empty(t *testing.T) {
	db := setupTestDBDefault(t)
	ctx := context.Background()
	mgr := gotype.NewManager[Person](db)

	if err := mgr.UpdateMany(ctx, nil); err != nil {
		t.Fatalf("UpdateMany empty should succeed: %v", err)
	}
}

// ---------------------------------------------------------------------------
// UpdateWith integration tests (functional update via query)
// ---------------------------------------------------------------------------

func TestIntegration_Query_UpdateWith(t *testing.T) {
	db := setupTestDBDefault(t)
	ctx := context.Background()
	mgr := gotype.NewManager[Person](db)

	seedPersons(t, ctx, mgr)

	// Update all persons with age > 27 to have a new email pattern
	results, err := mgr.Query().
		Filter(gotype.Gt("age", 27)).
		UpdateWith(ctx, func(p *Person) {
			p.Email = p.Name + "-senior@test.com"
		})
	if err != nil {
		t.Fatalf("UpdateWith failed: %v", err)
	}

	// Alice (30), Charlie (35), Diana (28) match age > 27
	if len(results) != 3 {
		t.Fatalf("expected 3 updated results, got %d", len(results))
	}

	// Verify persisted
	alice := assertGetOne(t, ctx, mgr, map[string]any{"name": "Alice"})
	if alice.Email != "Alice-senior@test.com" {
		t.Errorf("expected Alice-senior@test.com, got %q", alice.Email)
	}

	// Bob (25) should be unchanged
	bob := assertGetOne(t, ctx, mgr, map[string]any{"name": "Bob"})
	if bob.Email != "bob@example.com" {
		t.Errorf("Bob should be unchanged, got %q", bob.Email)
	}
}

func TestIntegration_Query_UpdateWith_NoResults(t *testing.T) {
	db := setupTestDBDefault(t)
	ctx := context.Background()
	mgr := gotype.NewManager[Person](db)

	seedPersons(t, ctx, mgr)

	results, err := mgr.Query().
		Filter(gotype.Eq("name", "Ghost")).
		UpdateWith(ctx, func(p *Person) {
			p.Email = "ghost@test.com"
		})
	if err != nil {
		t.Fatalf("UpdateWith no results should not error: %v", err)
	}
	if results != nil {
		t.Errorf("expected nil results, got %d", len(results))
	}
}

// ---------------------------------------------------------------------------
// Query.Update (bulk map) integration tests
// ---------------------------------------------------------------------------

func TestIntegration_Query_Update_BulkMap(t *testing.T) {
	db := setupTestDBDefault(t)
	ctx := context.Background()
	mgr := gotype.NewManager[Person](db)

	seedPersons(t, ctx, mgr)

	// Bulk-update email for everyone named Alice
	_, err := mgr.Query().
		Filter(gotype.Eq("name", "Alice")).
		Update(ctx, map[string]any{
			"email": "alice-bulk@test.com",
		})
	if err != nil {
		t.Fatalf("Query.Update bulk failed: %v", err)
	}

	alice := assertGetOne(t, ctx, mgr, map[string]any{"name": "Alice"})
	if alice.Email != "alice-bulk@test.com" {
		t.Errorf("expected alice-bulk@test.com, got %q", alice.Email)
	}

	// Others unchanged
	bob := assertGetOne(t, ctx, mgr, map[string]any{"name": "Bob"})
	if bob.Email != "bob@example.com" {
		t.Errorf("Bob should be unchanged, got %q", bob.Email)
	}
}

func TestIntegration_Query_Update_EmptyMap(t *testing.T) {
	db := setupTestDBDefault(t)
	ctx := context.Background()
	mgr := gotype.NewManager[Person](db)

	seedPersons(t, ctx, mgr)

	count, err := mgr.Query().Update(ctx, map[string]any{})
	if err != nil {
		t.Fatalf("empty update should succeed: %v", err)
	}
	if count != 0 {
		t.Errorf("expected count 0, got %d", count)
	}
}
