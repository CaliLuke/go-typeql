//go:build integration && cgo && typedb

package gotype_test

import (
	"context"
	"testing"

	"github.com/CaliLuke/go-typeql/gotype"
)

func setupProfileDB(t *testing.T) *gotype.Database {
	return setupTestDBWith(t, func() {
		_ = gotype.Register[Profile]()
	})
}

func TestIntegration_Optional_AllNil(t *testing.T) {
	db := setupProfileDB(t)
	ctx := context.Background()
	mgr := gotype.NewManager[Profile](db)

	assertInsert(t, ctx, mgr, &Profile{Username: "ghost"})

	r := assertGetOne(t, ctx, mgr, map[string]any{"username": "ghost"})
	if r.Bio != nil {
		t.Errorf("expected nil Bio, got %v", *r.Bio)
	}
	if r.Score != nil {
		t.Errorf("expected nil Score, got %v", *r.Score)
	}
	if r.Active != nil {
		t.Errorf("expected nil Active, got %v", *r.Active)
	}
	if r.Level != nil {
		t.Errorf("expected nil Level, got %v", *r.Level)
	}
}

func TestIntegration_Optional_AllSet(t *testing.T) {
	db := setupProfileDB(t)
	ctx := context.Background()
	mgr := gotype.NewManager[Profile](db)

	p := &Profile{
		Username: "full",
		Bio:      new("hello world"),
		Score:    new(99.5),
		Active:   new(true),
		Level:    new(10),
	}
	assertInsert(t, ctx, mgr, p)

	r := assertGetOne(t, ctx, mgr, map[string]any{"username": "full"})
	if r.Bio == nil || *r.Bio != "hello world" {
		t.Errorf("expected bio 'hello world', got %v", r.Bio)
	}
	if r.Score == nil || *r.Score != 99.5 {
		t.Errorf("expected score 99.5, got %v", r.Score)
	}
	if r.Active == nil || *r.Active != true {
		t.Errorf("expected active true, got %v", r.Active)
	}
	if r.Level == nil || *r.Level != 10 {
		t.Errorf("expected level 10, got %v", r.Level)
	}
}

func TestIntegration_Optional_UpdateNilToValue(t *testing.T) {
	db := setupProfileDB(t)
	ctx := context.Background()
	mgr := gotype.NewManager[Profile](db)

	assertInsert(t, ctx, mgr, &Profile{Username: "updater"})

	fetched := assertGetOne(t, ctx, mgr, map[string]any{"username": "updater"})
	fetched.Bio = new("now set")
	assertUpdate(t, ctx, mgr, fetched)

	result := assertGetOne(t, ctx, mgr, map[string]any{"username": "updater"})
	if result.Bio == nil || *result.Bio != "now set" {
		t.Errorf("expected bio 'now set', got %v", result.Bio)
	}
}

func TestIntegration_Optional_UpdateExistingValue(t *testing.T) {
	db := setupProfileDB(t)
	ctx := context.Background()
	mgr := gotype.NewManager[Profile](db)

	assertInsert(t, ctx, mgr, &Profile{Username: "changer", Bio: new("old bio")})

	fetched := assertGetOne(t, ctx, mgr, map[string]any{"username": "changer"})
	fetched.Bio = new("new bio")
	assertUpdate(t, ctx, mgr, fetched)

	result := assertGetOne(t, ctx, mgr, map[string]any{"username": "changer"})
	if result.Bio == nil || *result.Bio != "new bio" {
		t.Errorf("expected 'new bio', got %v", result.Bio)
	}
}

func TestIntegration_Optional_FilterHasAttr(t *testing.T) {
	db := setupProfileDB(t)
	ctx := context.Background()
	mgr := gotype.NewManager[Profile](db)

	assertInsert(t, ctx, mgr, &Profile{Username: "withbio", Bio: new("hi")})
	assertInsert(t, ctx, mgr, &Profile{Username: "nobio"})

	// Filter: has bio.
	results, err := mgr.Query().Filter(gotype.HasAttr("bio")).Execute(ctx)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 with bio, got %d", len(results))
	}
	if results[0].Username != "withbio" {
		t.Errorf("expected 'withbio', got %q", results[0].Username)
	}

	// Filter: not has bio.
	results2, err := mgr.Query().Filter(gotype.NotHasAttr("bio")).Execute(ctx)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(results2) != 1 {
		t.Fatalf("expected 1 without bio, got %d", len(results2))
	}
	if results2[0].Username != "nobio" {
		t.Errorf("expected 'nobio', got %q", results2[0].Username)
	}
}

func TestIntegration_Optional_MixedNilAndSet(t *testing.T) {
	db := setupProfileDB(t)
	ctx := context.Background()
	mgr := gotype.NewManager[Profile](db)

	assertInsert(t, ctx, mgr, &Profile{
		Username: "mixed",
		Bio:      new("has bio"),
		Score:    nil,
		Active:   new(false),
		Level:    nil,
	})

	r := assertGetOne(t, ctx, mgr, map[string]any{"username": "mixed"})
	if r.Bio == nil || *r.Bio != "has bio" {
		t.Errorf("expected bio set, got %v", r.Bio)
	}
	if r.Score != nil {
		t.Errorf("expected nil score, got %v", *r.Score)
	}
	if r.Active == nil || *r.Active != false {
		t.Errorf("expected active false, got %v", r.Active)
	}
	if r.Level != nil {
		t.Errorf("expected nil level, got %v", *r.Level)
	}
}
