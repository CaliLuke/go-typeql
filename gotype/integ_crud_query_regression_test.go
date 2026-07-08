//go:build integration && cgo && typedb

package gotype_test

// Integration regression tests for the CRUD & query execution review cluster:
//   - issue #26: Update must round-trip multi-valued (slice) attributes
//   - issue #31: relation insert with slice role players must link all players
//   - issue #47: Count/Delete must count distinct entities, not answer rows

import (
	"context"
	"slices"
	"testing"

	"github.com/CaliLuke/go-typeql/gotype"
)

// TaggedDoc has a multi-valued (slice) attribute.
type TaggedDoc struct {
	gotype.BaseEntity
	DocID string   `typedb:"doc-id,key"`
	Tags  []string `typedb:"doc-tag,card=0.."`
}

// Squad is a relation with a slice role field (several players of one role).
type Squad struct {
	gotype.BaseRelation
	Members   []*Person `typedb:"role:squad-member"`
	SquadName string    `typedb:"squad-name"`
}

// TestIntegration_UpdateMultiValuedAttribute is the regression test for
// issue #26: Update on a model with a []string attribute must replace the
// old values with the new elements instead of writing a stringified Go
// slice (`"[a b]"`).
func TestIntegration_UpdateMultiValuedAttribute(t *testing.T) {
	db := setupTestDBWith(t, func() {
		_ = gotype.Register[TaggedDoc]()
	})
	ctx := context.Background()
	mgr := gotype.MustNewManager[TaggedDoc](db)

	doc := &TaggedDoc{DocID: "doc-1", Tags: []string{"red", "green"}}
	assertInsert(t, ctx, mgr, doc)

	fetched := assertGetOne(t, ctx, mgr, map[string]any{"doc-id": "doc-1"})
	assertSameStringSet(t, "after insert", []string{"red", "green"}, fetched.Tags)

	// Replace the whole tag set via Update.
	fetched.Tags = []string{"blue", "yellow", "purple"}
	assertUpdate(t, ctx, mgr, fetched)

	updated := assertGetOne(t, ctx, mgr, map[string]any{"doc-id": "doc-1"})
	assertSameStringSet(t, "after update", []string{"blue", "yellow", "purple"}, updated.Tags)

	// Clearing the slice deletes all values without inserting anything.
	updated.Tags = nil
	assertUpdate(t, ctx, mgr, updated)

	cleared := assertGetOne(t, ctx, mgr, map[string]any{"doc-id": "doc-1"})
	if len(cleared.Tags) != 0 {
		t.Fatalf("expected no tags after clearing update, got %v", cleared.Tags)
	}
}

// TestIntegration_RelationInsertSliceRolePlayers is the regression test for
// issue #31: inserting a relation whose role field is a slice must link
// every element as a player instead of silently dropping them all and
// emitting invalid `links ()`.
func TestIntegration_RelationInsertSliceRolePlayers(t *testing.T) {
	db := setupTestDBWith(t, func() {
		_ = gotype.Register[Person]()
		_ = gotype.Register[Squad]()
	})
	ctx := context.Background()

	// The generated schema declares the role with default cardinality; widen
	// it so one squad can link several members.
	if err := db.ExecuteSchema(ctx, "define relation squad relates squad-member @card(0..);"); err != nil {
		t.Fatalf("widening squad-member role cardinality: %v", err)
	}

	personMgr := gotype.MustNewManager[Person](db)
	alice := &Person{Name: "Alice", Email: "alice@example.com"}
	bob := &Person{Name: "Bob", Email: "bob@example.com"}
	assertInsert(t, ctx, personMgr, alice)
	assertInsert(t, ctx, personMgr, bob)

	squadMgr := gotype.MustNewManager[Squad](db)
	squad := &Squad{Members: []*Person{alice, bob}, SquadName: "alpha"}
	if err := squadMgr.Insert(ctx, squad); err != nil {
		t.Fatalf("inserting squad with slice role players: %v", err)
	}

	// Verify via a raw query that BOTH players are linked to the relation.
	rows, err := db.ExecuteRead(ctx, "match\n$r isa squad, links (squad-member: $p);\n$p has name $n;\nfetch { \"name\": $n };")
	if err != nil {
		t.Fatalf("reading squad members: %v", err)
	}
	var names []string
	for _, row := range rows {
		if name, ok := row["name"].(string); ok {
			names = append(names, name)
		}
	}
	assertSameStringSet(t, "squad members", []string{"Alice", "Bob"}, names)

	// A relation with no role players must be rejected instead of emitting
	// invalid `links ()`.
	if err := squadMgr.Insert(ctx, &Squad{SquadName: "empty"}); err == nil {
		t.Fatal("expected error inserting relation without role players")
	}
}

// TestIntegration_CountDistinctMultiValued is the regression test for
// issue #47: an entity matched via several values of a multi-valued
// attribute must be counted (and deleted) once, not once per matching value.
func TestIntegration_CountDistinctMultiValued(t *testing.T) {
	db := setupTestDBWith(t, func() {
		_ = gotype.Register[TaggedDoc]()
	})
	ctx := context.Background()
	mgr := gotype.MustNewManager[TaggedDoc](db)

	assertInsert(t, ctx, mgr, &TaggedDoc{DocID: "doc-1", Tags: []string{"x-red", "x-blue", "x-green"}})
	assertInsert(t, ctx, mgr, &TaggedDoc{DocID: "doc-2", Tags: []string{"x-yellow"}})
	assertInsert(t, ctx, mgr, &TaggedDoc{DocID: "doc-3", Tags: []string{"plain"}})

	// doc-1 matches the filter through THREE tag values, doc-2 through one:
	// the distinct-entity count must be 2 either way.
	count, err := mgr.Query().Filter(gotype.Contains("doc-tag", "x-")).Count(ctx)
	if err != nil {
		t.Fatalf("Count: %v", err)
	}
	if count != 2 {
		t.Fatalf("expected distinct count 2, got %d", count)
	}

	// Delete must report the same distinct number and remove exactly those.
	deleted, err := mgr.Query().Filter(gotype.Contains("doc-tag", "x-")).Delete(ctx)
	if err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if deleted != 2 {
		t.Fatalf("expected 2 deleted, got %d", deleted)
	}

	remaining := assertCount(t, ctx, mgr, 1)
	if remaining[0].DocID != "doc-3" {
		t.Fatalf("expected doc-3 to survive, got %q", remaining[0].DocID)
	}
}

// assertSameStringSet fails the test unless got contains exactly the
// expected strings, ignoring order.
func assertSameStringSet(t *testing.T, label string, expected, got []string) {
	t.Helper()
	e := slices.Clone(expected)
	g := slices.Clone(got)
	slices.Sort(e)
	slices.Sort(g)
	if !slices.Equal(e, g) {
		t.Fatalf("%s: expected %v, got %v", label, expected, got)
	}
}
