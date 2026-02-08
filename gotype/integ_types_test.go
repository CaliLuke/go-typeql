//go:build integration && cgo && typedb

package gotype_test

import (
	"context"
	"testing"

	"github.com/CaliLuke/go-typeql/gotype"
)

func setupTypeTestDB(t *testing.T) *gotype.Database {
	return setupTestDBWith(t, func() {
		_ = gotype.Register[TypeTest]()
	})
}

func TestIntegration_TypeRoundTrip_String(t *testing.T) {
	db := setupTypeTestDB(t)
	ctx := context.Background()
	mgr := gotype.NewManager[TypeTest](db)

	assertInsert(t, ctx, mgr, &TypeTest{TagName: "str-test", StrVal: "hello world", IntVal: 1, FloatVal: 1.0, BoolVal: true})

	r := assertGetOne(t, ctx, mgr, map[string]any{"tag-name": "str-test"})
	if r.StrVal != "hello world" {
		t.Errorf("expected %q, got %q", "hello world", r.StrVal)
	}
}

func TestIntegration_TypeRoundTrip_Int(t *testing.T) {
	db := setupTypeTestDB(t)
	ctx := context.Background()
	mgr := gotype.NewManager[TypeTest](db)

	assertInsert(t, ctx, mgr, &TypeTest{TagName: "int-test", IntVal: 42, FloatVal: 0.0, BoolVal: false, StrVal: "x"})

	r := assertGetOne(t, ctx, mgr, map[string]any{"tag-name": "int-test"})
	if r.IntVal != 42 {
		t.Errorf("expected 42, got %d", r.IntVal)
	}
}

func TestIntegration_TypeRoundTrip_Float(t *testing.T) {
	db := setupTypeTestDB(t)
	ctx := context.Background()
	mgr := gotype.NewManager[TypeTest](db)

	assertInsert(t, ctx, mgr, &TypeTest{TagName: "float-test", FloatVal: 3.14159, IntVal: 0, BoolVal: false, StrVal: "x"})

	r := assertGetOne(t, ctx, mgr, map[string]any{"tag-name": "float-test"})
	if r.FloatVal < 3.14 || r.FloatVal > 3.15 {
		t.Errorf("expected ~3.14159, got %f", r.FloatVal)
	}
}

func TestIntegration_TypeRoundTrip_Bool(t *testing.T) {
	db := setupTypeTestDB(t)
	ctx := context.Background()
	mgr := gotype.NewManager[TypeTest](db)

	assertInsert(t, ctx, mgr, &TypeTest{TagName: "bool-test", BoolVal: true, IntVal: 0, FloatVal: 0.0, StrVal: "x"})

	r := assertGetOne(t, ctx, mgr, map[string]any{"tag-name": "bool-test"})
	if r.BoolVal != true {
		t.Errorf("expected true, got %v", r.BoolVal)
	}
}

func TestIntegration_TypeRoundTrip_ZeroValues(t *testing.T) {
	db := setupTypeTestDB(t)
	ctx := context.Background()
	mgr := gotype.NewManager[TypeTest](db)

	assertInsert(t, ctx, mgr, &TypeTest{TagName: "zeros", IntVal: 0, FloatVal: 0.0, BoolVal: false, StrVal: ""})

	r := assertGetOne(t, ctx, mgr, map[string]any{"tag-name": "zeros"})
	if r.IntVal != 0 {
		t.Errorf("expected int 0, got %d", r.IntVal)
	}
	if r.FloatVal != 0.0 {
		t.Errorf("expected float 0.0, got %f", r.FloatVal)
	}
	if r.BoolVal != false {
		t.Errorf("expected bool false, got %v", r.BoolVal)
	}
	if r.StrVal != "" {
		t.Errorf("expected empty string, got %q", r.StrVal)
	}
}

func TestIntegration_TypeRoundTrip_LargeNumeric(t *testing.T) {
	db := setupTypeTestDB(t)
	ctx := context.Background()
	mgr := gotype.NewManager[TypeTest](db)

	assertInsert(t, ctx, mgr, &TypeTest{TagName: "large", IntVal: 999999999, FloatVal: 1.23e15, BoolVal: false, StrVal: "big"})

	r := assertGetOne(t, ctx, mgr, map[string]any{"tag-name": "large"})
	if r.IntVal != 999999999 {
		t.Errorf("expected 999999999, got %d", r.IntVal)
	}
	if r.FloatVal < 1.2e15 || r.FloatVal > 1.3e15 {
		t.Errorf("expected ~1.23e15, got %e", r.FloatVal)
	}
}
