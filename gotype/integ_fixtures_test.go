//go:build integration && cgo && typedb

package gotype_test

import (
	"context"
	"testing"

	"github.com/CaliLuke/go-typeql/v3/gotype"
)

// ---------------------------------------------------------------------------
// Assertion helpers — reduce boilerplate in integration tests.
// ---------------------------------------------------------------------------

// assertCount asserts that Manager[T].All returns exactly `expected` results.
func assertCount[T any](t *testing.T, ctx context.Context, mgr *gotype.Manager[T], expected int) []*T {
	t.Helper()
	results, err := mgr.All(ctx)
	if err != nil {
		t.Fatalf("assertCount: All() failed: %v", err)
	}
	if len(results) != expected {
		t.Fatalf("assertCount: expected %d results, got %d", expected, len(results))
	}
	return results
}

// assertGetOne fetches via mgr.Get with the given filter map and asserts
// exactly one result is returned. Returns that result.
func assertGetOne[T any](t *testing.T, ctx context.Context, mgr *gotype.Manager[T], filter map[string]any) *T {
	t.Helper()
	results, err := mgr.Get(ctx, filter)
	if err != nil {
		t.Fatalf("assertGetOne: Get(%v) failed: %v", filter, err)
	}
	if len(results) != 1 {
		t.Fatalf("assertGetOne: expected 1 result for filter %v, got %d", filter, len(results))
	}
	return results[0]
}

// assertInsert inserts an instance and fails the test on error.
func assertInsert[T any](t *testing.T, ctx context.Context, mgr *gotype.Manager[T], instance *T) {
	t.Helper()
	if err := mgr.Insert(ctx, instance); err != nil {
		t.Fatalf("assertInsert: Insert failed: %v", err)
	}
}

// assertInsertMany inserts a slice of instances and fails the test on error.
func assertInsertMany[T any](t *testing.T, ctx context.Context, mgr *gotype.Manager[T], instances []*T) {
	t.Helper()
	if err := mgr.InsertMany(ctx, instances); err != nil {
		t.Fatalf("assertInsertMany: InsertMany failed: %v", err)
	}
}

// assertUpdate updates an instance and fails the test on error.
func assertUpdate[T any](t *testing.T, ctx context.Context, mgr *gotype.Manager[T], instance *T) {
	t.Helper()
	if err := mgr.Update(ctx, instance); err != nil {
		t.Fatalf("assertUpdate: Update failed: %v", err)
	}
}

// assertDelete deletes an instance and fails the test on error.
func assertDelete[T any](t *testing.T, ctx context.Context, mgr *gotype.Manager[T], instance *T) {
	t.Helper()
	if err := mgr.Delete(ctx, instance); err != nil {
		t.Fatalf("assertDelete: Delete failed: %v", err)
	}
}

// insertAndGet inserts an instance, then fetches it back by a key/value filter.
// Returns the fetched (hydrated) instance with IID populated.
func insertAndGet[T any](t *testing.T, ctx context.Context, mgr *gotype.Manager[T], instance *T, key string, value any) *T {
	t.Helper()
	assertInsert(t, ctx, mgr, instance)
	return assertGetOne(t, ctx, mgr, map[string]any{key: value})
}
