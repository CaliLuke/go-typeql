//go:build integration && cgo && typedb

package gotype_test

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"testing"

	"github.com/CaliLuke/go-typeql/gotype"
)

// ---------------------------------------------------------------------------
// Data builders — generate unique test data to prevent collisions.
// ---------------------------------------------------------------------------

// uniqueSuffix returns a 6-character random hex string.
func uniqueSuffix() string {
	b := make([]byte, 3)
	if _, err := rand.Read(b); err != nil {
		panic("uniqueSuffix: " + err.Error())
	}
	return hex.EncodeToString(b)
}

// makeName returns a unique name like "Test-a1b2c3".
func makeName(prefix string) string {
	if prefix == "" {
		prefix = "Test"
	}
	return fmt.Sprintf("%s-%s", prefix, uniqueSuffix())
}

// makeEmail returns a unique email like "user-a1b2c3@test.com".
func makeEmail(name string) string {
	if name == "" {
		name = "user-" + uniqueSuffix()
	}
	return fmt.Sprintf("%s@test.com", name)
}

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

// assertQueryCount asserts that a fresh query's Count returns exactly `expected`.
func assertQueryCount[T any](t *testing.T, ctx context.Context, mgr *gotype.Manager[T], expected int64) {
	t.Helper()
	count, err := mgr.Query().Count(ctx)
	if err != nil {
		t.Fatalf("assertQueryCount: Count() failed: %v", err)
	}
	if count != expected {
		t.Fatalf("assertQueryCount: expected count %d, got %d", expected, count)
	}
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
