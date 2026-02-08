//go:build integration && cgo && typedb

package gotype_test

import (
	"context"
	"testing"

	"github.com/CaliLuke/go-typeql/gotype"
)

// ---------------------------------------------------------------------------
// Attribute type CRUD cycle tests (ported from Python test_integer.py,
// test_double.py, test_boolean.py, test_string.py)
// ---------------------------------------------------------------------------

func TestIntegration_AttrCRUD_Integer(t *testing.T) {
	// Ported from test_integer_insert/fetch/update/delete.
	// Full CRUD cycle for integer attributes.
	db := setupTestDBDefault(t)
	ctx := context.Background()

	mgr := gotype.NewManager[Person](db)

	// Insert with integer age.
	assertInsert(t, ctx, mgr, &Person{Name: "IntTest", Email: "int@test.com", Age: intPtr(42)})

	// Fetch and verify.
	fetched := assertGetOne(t, ctx, mgr, map[string]any{"name": "IntTest"})
	if fetched.Age == nil || *fetched.Age != 42 {
		t.Errorf("expected age 42, got %v", fetched.Age)
	}

	// Update integer.
	fetched.Age = intPtr(99)
	assertUpdate(t, ctx, mgr, fetched)

	updated := assertGetOne(t, ctx, mgr, map[string]any{"name": "IntTest"})
	if updated.Age == nil || *updated.Age != 99 {
		t.Errorf("expected updated age 99, got %v", updated.Age)
	}

	// Delete.
	assertDelete(t, ctx, mgr, updated)
	assertCount(t, ctx, mgr, 0)
}

func TestIntegration_AttrCRUD_Double(t *testing.T) {
	// Ported from test_double_insert/fetch/update/delete.
	// Full CRUD cycle for float64 (double) attributes.
	db := setupTestDBWith(t, func() {
		_ = gotype.Register[Profile]()
	})
	ctx := context.Background()

	mgr := gotype.NewManager[Profile](db)

	// Insert with double score.
	assertInsert(t, ctx, mgr, &Profile{Username: "dbltest", Score: float64Ptr(3.14)})

	// Fetch and verify.
	fetched := assertGetOne(t, ctx, mgr, map[string]any{"username": "dbltest"})
	if fetched.Score == nil || *fetched.Score != 3.14 {
		t.Errorf("expected score 3.14, got %v", fetched.Score)
	}

	// Update double.
	fetched.Score = float64Ptr(2.718)
	assertUpdate(t, ctx, mgr, fetched)

	updated := assertGetOne(t, ctx, mgr, map[string]any{"username": "dbltest"})
	if updated.Score == nil || *updated.Score != 2.718 {
		t.Errorf("expected updated score 2.718, got %v", updated.Score)
	}

	// Delete.
	assertDelete(t, ctx, mgr, updated)
	assertCount(t, ctx, mgr, 0)
}

func TestIntegration_AttrCRUD_Boolean(t *testing.T) {
	// Ported from test_boolean_insert/fetch/update/delete.
	// Full CRUD cycle for boolean attributes.
	db := setupTestDBWith(t, func() {
		_ = gotype.Register[Profile]()
	})
	ctx := context.Background()

	mgr := gotype.NewManager[Profile](db)

	// Insert with boolean active=true.
	assertInsert(t, ctx, mgr, &Profile{Username: "booltest", Active: boolPtr(true)})

	// Fetch and verify.
	fetched := assertGetOne(t, ctx, mgr, map[string]any{"username": "booltest"})
	if fetched.Active == nil || *fetched.Active != true {
		t.Errorf("expected active true, got %v", fetched.Active)
	}

	// Update to false.
	fetched.Active = boolPtr(false)
	assertUpdate(t, ctx, mgr, fetched)

	updated := assertGetOne(t, ctx, mgr, map[string]any{"username": "booltest"})
	if updated.Active == nil || *updated.Active != false {
		t.Errorf("expected updated active false, got %v", updated.Active)
	}

	// Delete.
	assertDelete(t, ctx, mgr, updated)
	assertCount(t, ctx, mgr, 0)
}

func TestIntegration_AttrCRUD_String(t *testing.T) {
	// Ported from test_string_insert/fetch/update/delete.
	// Full CRUD cycle for string attributes.
	db := setupTestDBDefault(t)
	ctx := context.Background()

	mgr := gotype.NewManager[Person](db)

	// Insert.
	assertInsert(t, ctx, mgr, &Person{Name: "StrTest", Email: "str@test.com"})

	// Fetch and verify.
	fetched := assertGetOne(t, ctx, mgr, map[string]any{"name": "StrTest"})
	if fetched.Email != "str@test.com" {
		t.Errorf("expected email str@test.com, got %q", fetched.Email)
	}

	// Update string.
	fetched.Email = "newstr@test.com"
	assertUpdate(t, ctx, mgr, fetched)

	updated := assertGetOne(t, ctx, mgr, map[string]any{"name": "StrTest"})
	if updated.Email != "newstr@test.com" {
		t.Errorf("expected updated email newstr@test.com, got %q", updated.Email)
	}

	// Delete.
	assertDelete(t, ctx, mgr, updated)
	assertCount(t, ctx, mgr, 0)
}

func TestIntegration_AttrCRUD_ZeroValues(t *testing.T) {
	// Ported from type roundtrip zero values.
	// Zero values for each type should round-trip correctly.
	db := setupTestDBWith(t, func() {
		_ = gotype.Register[TypeTest]()
	})
	ctx := context.Background()

	mgr := gotype.NewManager[TypeTest](db)

	assertInsert(t, ctx, mgr, &TypeTest{
		TagName:  "zeros",
		IntVal:   0,
		FloatVal: 0.0,
		BoolVal:  false,
		StrVal:   "",
	})

	fetched := assertGetOne(t, ctx, mgr, map[string]any{"tag-name": "zeros"})
	if fetched.IntVal != 0 {
		t.Errorf("expected int 0, got %d", fetched.IntVal)
	}
	if fetched.FloatVal != 0.0 {
		t.Errorf("expected float 0.0, got %f", fetched.FloatVal)
	}
	if fetched.BoolVal != false {
		t.Errorf("expected bool false, got %v", fetched.BoolVal)
	}
	if fetched.StrVal != "" {
		t.Errorf("expected empty string, got %q", fetched.StrVal)
	}
}
