//go:build cgo && typedb && integration

package gotype_test

import (
	"context"
	"testing"

	"github.com/CaliLuke/go-typeql/v3/gotype"
)

func TestIntegration_QueryNoCountOptionalEmptyAndRepeatedMatches(t *testing.T) {
	db := setupTestDBWith(t, func() {
		_ = gotype.Register[Person]()
		_ = gotype.Register[TaggedDoc]()
	})
	ctx := context.Background()
	people := gotype.MustNewManager[Person](db)
	alice := &Person{Name: "Alice", Email: "alice@example.test"}
	bob := &Person{Name: "Bob", Email: "bob@example.test"}
	for _, person := range []*Person{alice, bob} {
		if err := people.Insert(ctx, person); err != nil {
			t.Fatal(err)
		}
	}
	if err := people.Query().Filter(gotype.Eq("name", "Alice")).UpdateNoCount(ctx, map[string]any{"age": 42}); err != nil {
		t.Fatal(err)
	}
	gotAlice := assertGetOne(t, ctx, people, map[string]any{"name": "Alice"})
	gotBob := assertGetOne(t, ctx, people, map[string]any{"name": "Bob"})
	if gotAlice.Age == nil || *gotAlice.Age != 42 || gotBob.Age != nil {
		t.Fatalf("optional age update changed wrong models: Alice=%+v Bob=%+v", gotAlice, gotBob)
	}
	if err := people.Query().Filter(gotype.Eq("name", "missing")).UpdateNoCount(ctx, map[string]any{"age": 99}); err != nil {
		t.Fatalf("empty-match update: %v", err)
	}
	if err := people.Query().Filter(gotype.Eq("name", "missing")).DeleteNoCount(ctx); err != nil {
		t.Fatalf("empty-match delete: %v", err)
	}
	if count, err := people.Query().Count(ctx); err != nil || count != 2 {
		t.Fatalf("empty matches changed the person set: count=%d err=%v", count, err)
	}

	docs := gotype.MustNewManager[TaggedDoc](db)
	for _, doc := range []*TaggedDoc{
		{DocID: "repeated", Tags: []string{"x-red", "x-blue"}},
		{DocID: "other", Tags: []string{"plain"}},
	} {
		if err := docs.Insert(ctx, doc); err != nil {
			t.Fatal(err)
		}
	}
	if err := docs.Query().Filter(gotype.Contains("doc-tag", "x-")).DeleteNoCount(ctx); err != nil {
		t.Fatal(err)
	}
	remaining := assertCount(t, ctx, docs, 1)
	if remaining[0].DocID != "other" {
		t.Fatalf("duplicate answers changed delete semantics: %+v", remaining)
	}
}

func TestIntegration_QueryNoCountKeepsBoundWriteTransaction(t *testing.T) {
	db := setupTestDBWith(t, func() { _ = gotype.Register[Person]() })
	ctx := context.Background()
	people := gotype.MustNewManager[Person](db)
	if err := people.Insert(ctx, &Person{Name: "Alice", Email: "alice@example.test"}); err != nil {
		t.Fatal(err)
	}
	scope, err := db.Begin(gotype.WriteTransaction)
	if err != nil {
		t.Fatal(err)
	}
	defer scope.Close()
	bound := gotype.MustNewManagerWithTx[Person](scope)
	if err := bound.Query().Filter(gotype.Eq("name", "Alice")).UpdateNoCount(ctx, map[string]any{"age": 42}); err != nil {
		t.Fatal(err)
	}
	if !scope.Tx().IsOpen() {
		t.Fatal("count-free update closed caller-owned write transaction")
	}
	if err := scope.Commit(); err != nil {
		t.Fatal(err)
	}
	got := assertGetOne(t, ctx, people, map[string]any{"name": "Alice"})
	if got.Age == nil || *got.Age != 42 {
		t.Fatalf("bound update did not persist: %+v", got)
	}
}
