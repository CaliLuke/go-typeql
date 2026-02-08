//go:build integration && cgo && typedb

package gotype_test

import (
	"context"
	"testing"

	"github.com/CaliLuke/go-typeql/gotype"
)

// ---------------------------------------------------------------------------
// String special character tests (ported from Python test_string.py)
// ---------------------------------------------------------------------------

// StringEntity has a string key and a string value for testing escaping.
type StringEntity struct {
	gotype.BaseEntity
	Tag   string `typedb:"tag,key"`
	Value string `typedb:"str-value"`
}

func setupStringDB(t *testing.T) *gotype.Database {
	return setupTestDBWith(t, func() {
		_ = gotype.Register[StringEntity]()
	})
}

func TestIntegration_String_InsertFetchUpdateDelete(t *testing.T) {
	// Ported from test_string_insert, test_string_fetch, test_string_update,
	// test_string_delete.
	// Full CRUD cycle for string attributes.
	db := setupStringDB(t)
	ctx := context.Background()

	mgr := gotype.NewManager[StringEntity](db)

	// Insert.
	assertInsert(t, ctx, mgr, &StringEntity{Tag: "s1", Value: "hello world"})

	// Fetch.
	fetched := assertGetOne(t, ctx, mgr, map[string]any{"tag": "s1"})
	if fetched.Value != "hello world" {
		t.Errorf("expected 'hello world', got %q", fetched.Value)
	}

	// Update.
	fetched.Value = "updated value"
	assertUpdate(t, ctx, mgr, fetched)

	updated := assertGetOne(t, ctx, mgr, map[string]any{"tag": "s1"})
	if updated.Value != "updated value" {
		t.Errorf("expected 'updated value', got %q", updated.Value)
	}

	// Delete.
	assertDelete(t, ctx, mgr, updated)
	assertCount(t, ctx, mgr, 0)
}

func TestIntegration_String_SpecialChars_Quotes(t *testing.T) {
	// Ported from test_string_special_characters_escaping.
	// Strings with double quotes are escaped properly.
	db := setupStringDB(t)
	ctx := context.Background()

	mgr := gotype.NewManager[StringEntity](db)

	assertInsert(t, ctx, mgr, &StringEntity{Tag: "q1", Value: `She said "hello"`})

	fetched := assertGetOne(t, ctx, mgr, map[string]any{"tag": "q1"})
	if fetched.Value != `She said "hello"` {
		t.Errorf("expected string with quotes, got %q", fetched.Value)
	}
}

func TestIntegration_String_SpecialChars_Backslash(t *testing.T) {
	// Ported from test_string_special_characters_escaping.
	// Strings with backslashes are escaped properly.
	db := setupStringDB(t)
	ctx := context.Background()

	mgr := gotype.NewManager[StringEntity](db)

	assertInsert(t, ctx, mgr, &StringEntity{Tag: "bs1", Value: `path\to\file`})

	fetched := assertGetOne(t, ctx, mgr, map[string]any{"tag": "bs1"})
	if fetched.Value != `path\to\file` {
		t.Errorf("expected string with backslashes, got %q", fetched.Value)
	}
}

func TestIntegration_String_SpecialChars_Mixed(t *testing.T) {
	// Ported from test_string_special_characters_escaping.
	// Strings with mixed special characters.
	db := setupStringDB(t)
	ctx := context.Background()

	mgr := gotype.NewManager[StringEntity](db)

	mixed := `It's a "test" with \ and \n`
	assertInsert(t, ctx, mgr, &StringEntity{Tag: "mix1", Value: mixed})

	fetched := assertGetOne(t, ctx, mgr, map[string]any{"tag": "mix1"})
	if fetched.Value != mixed {
		t.Errorf("expected mixed special chars, got %q", fetched.Value)
	}
}

func TestIntegration_String_EmptyString(t *testing.T) {
	// Ported from test_multivalue_empty_string_escaping.
	// Empty string values should round-trip correctly.
	db := setupStringDB(t)
	ctx := context.Background()

	mgr := gotype.NewManager[StringEntity](db)

	assertInsert(t, ctx, mgr, &StringEntity{Tag: "empty1", Value: ""})

	fetched := assertGetOne(t, ctx, mgr, map[string]any{"tag": "empty1"})
	if fetched.Value != "" {
		t.Errorf("expected empty string, got %q", fetched.Value)
	}
}

func TestIntegration_String_Update_SpecialChars(t *testing.T) {
	// Updating string from normal to special characters.
	db := setupStringDB(t)
	ctx := context.Background()

	mgr := gotype.NewManager[StringEntity](db)

	assertInsert(t, ctx, mgr, &StringEntity{Tag: "upd1", Value: "normal"})
	fetched := assertGetOne(t, ctx, mgr, map[string]any{"tag": "upd1"})

	fetched.Value = `now "with" special \chars`
	assertUpdate(t, ctx, mgr, fetched)

	updated := assertGetOne(t, ctx, mgr, map[string]any{"tag": "upd1"})
	if updated.Value != `now "with" special \chars` {
		t.Errorf("expected special chars after update, got %q", updated.Value)
	}
}
