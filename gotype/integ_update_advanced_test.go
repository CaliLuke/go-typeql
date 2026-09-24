//go:build integration && cgo && typedb

package gotype_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/CaliLuke/go-typeql/v3/driver"
	"github.com/CaliLuke/go-typeql/v3/given"
	"github.com/CaliLuke/go-typeql/v3/gotype"
)

func TestIntegration_UpdateGivenIIDMatch(t *testing.T) {
	db := setupTestDBWith(t, func() { gotype.MustRegister[Company]() })
	ctx := context.Background()
	mgr := gotype.MustNewManager[Company](db)
	company := &Company{Name: "GivenCompany", Industry: "Before"}
	if err := mgr.Insert(ctx, company); err != nil {
		t.Fatal(err)
	}
	tx, err := db.GetConn().Transaction(db.Name(), int(driver.Write))
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Close()
	rows := given.NewRows("id", "v")
	if err := rows.Add(given.Value{Type: "string", Value: company.GetIID()}, given.Value{Type: "string", Value: "After"}); err != nil {
		t.Fatal(err)
	}
	query := `given $id: string, $v: string; match $e isa company; iid($e) == $id; try { $e has industry $old; }; delete try { $old of $e; }; insert $e has industry == $v;`
	if _, err := tx.(interface {
		QueryWithGivenRows(context.Context, string, *given.TypedRows) ([]map[string]any, error)
	}).QueryWithGivenRows(ctx, query, rows); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	fetched := assertGetOne(t, ctx, mgr, map[string]any{"name": "GivenCompany"})
	if fetched.Industry != "After" {
		t.Fatalf("industry = %q, want After", fetched.Industry)
	}
}

func TestIntegration_UpdateMany_BatchedHeterogeneous(t *testing.T) {
	db := setupTestDBWith(t, func() { gotype.MustRegister[Company]() })
	ctx := context.Background()
	mgr := gotype.MustNewManager[Company](db)
	rows := make([]*Company, 40)
	for i := range rows {
		rows[i] = &Company{Name: fmt.Sprintf("UpdateCo-%d", i), Industry: "Before"}
	}
	if err := mgr.InsertMany(ctx, rows); err != nil {
		t.Fatal(err)
	}
	for i, row := range rows {
		row.Industry = fmt.Sprintf("After-%d", i)
	}
	// IIDs are hexadecimal, and callers may retain a different case than the
	// canonical spelling returned by TypeDB.
	rows[1].SetIID("0x" + strings.ToUpper(strings.TrimPrefix(rows[1].GetIID(), "0x")))
	if err := mgr.UpdateMany(ctx, rows); err != nil {
		t.Fatal(err)
	}
	for i, row := range rows {
		fetched := assertGetOne(t, ctx, mgr, map[string]any{"name": row.Name})
		if fetched.Industry != fmt.Sprintf("After-%d", i) {
			t.Fatalf("row %d industry = %s", i, fetched.Industry)
		}
	}
}

func TestIntegration_UpdateWith_BatchedHeterogeneous(t *testing.T) {
	db := setupTestDBWith(t, func() { gotype.MustRegister[Company]() })
	ctx := context.Background()
	mgr := gotype.MustNewManager[Company](db)
	for i := 0; i < 3; i++ {
		if err := mgr.Insert(ctx, &Company{Name: fmt.Sprintf("CallbackCo-%d", i), Industry: "Before"}); err != nil {
			t.Fatal(err)
		}
	}
	var callbackOrder []string
	updated, err := mgr.Query().UpdateWith(ctx, func(c *Company) {
		callbackOrder = append(callbackOrder, c.Name)
		c.Industry = "After-" + c.Name
	})
	if err != nil || len(updated) != 3 || len(callbackOrder) != 3 {
		t.Fatalf("UpdateWith: count=%d callbacks=%d err=%v", len(updated), len(callbackOrder), err)
	}
	for _, row := range updated {
		fetched := assertGetOne(t, ctx, mgr, map[string]any{"name": row.Name})
		if fetched.Industry != "After-"+row.Name {
			t.Fatalf("row %s industry=%s", row.Name, fetched.Industry)
		}
	}
}

// ---------------------------------------------------------------------------
// UpdateMany integration tests
// ---------------------------------------------------------------------------

func TestIntegration_UpdateMany(t *testing.T) {
	db := setupTestDBDefault(t)
	ctx := context.Background()
	mgr := gotype.MustNewManager[Person](db)

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
	mgr := gotype.MustNewManager[Person](db)

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
	mgr := gotype.MustNewManager[Person](db)

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
	mgr := gotype.MustNewManager[Person](db)

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
	mgr := gotype.MustNewManager[Person](db)

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
	mgr := gotype.MustNewManager[Person](db)

	seedPersons(t, ctx, mgr)

	count, err := mgr.Query().Update(ctx, map[string]any{})
	if err != nil {
		t.Fatalf("empty update should succeed: %v", err)
	}
	if count != 0 {
		t.Errorf("expected count 0, got %d", count)
	}
}
