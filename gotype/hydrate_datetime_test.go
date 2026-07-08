package gotype

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// Test entity with datetime fields
type entityWithDatetime struct {
	BaseEntity
	Name      string    `typedb:"name,key"`
	CreatedAt time.Time `typedb:"created-at"`
}

func TestHydrate_DatetimeField(t *testing.T) {
	ClearRegistry()
	if err := Register[entityWithDatetime](); err != nil {
		t.Fatal(err)
	}

	// Simulate data from driver with datetime as string (how Rust FFI returns it)
	data := map[string]any{
		"_iid":       "0xABC123",
		"name":       "Test Entity",
		"created-at": "2024-01-15T10:30:00", // ISO 8601 format
	}

	var entity entityWithDatetime
	if err := Hydrate(&entity, data); err != nil {
		t.Fatalf("Hydrate failed: %v", err)
	}

	if entity.Name != "Test Entity" {
		t.Errorf("Name: got %q, want %q", entity.Name, "Test Entity")
	}

	expectedTime, _ := time.Parse("2006-01-02T15:04:05", "2024-01-15T10:30:00")
	if !entity.CreatedAt.Equal(expectedTime) {
		t.Errorf("CreatedAt: got %v, want %v", entity.CreatedAt, expectedTime)
	}
}

func TestHydrate_DatetimeRFC3339(t *testing.T) {
	ClearRegistry()
	if err := Register[entityWithDatetime](); err != nil {
		t.Fatal(err)
	}

	// Test with RFC3339 format (with timezone)
	data := map[string]any{
		"_iid":       "0xABC123",
		"name":       "Test Entity",
		"created-at": "2024-01-15T10:30:00Z",
	}

	var entity entityWithDatetime
	if err := Hydrate(&entity, data); err != nil {
		t.Fatalf("Hydrate failed: %v", err)
	}

	expectedTime, _ := time.Parse(time.RFC3339, "2024-01-15T10:30:00Z")
	if !entity.CreatedAt.Equal(expectedTime) {
		t.Errorf("CreatedAt: got %v, want %v", entity.CreatedAt, expectedTime)
	}
}

func TestHydrate_DateField(t *testing.T) {
	type entityWithDate struct {
		BaseEntity
		Name      string    `typedb:"name,key"`
		BirthDate time.Time `typedb:"birth-date"`
	}

	ClearRegistry()
	if err := Register[entityWithDate](); err != nil {
		t.Fatal(err)
	}

	// Date format (no time component)
	data := map[string]any{
		"_iid":       "0xABC123",
		"name":       "Test Entity",
		"birth-date": "2024-01-15",
	}

	var entity entityWithDate
	if err := Hydrate(&entity, data); err != nil {
		t.Fatalf("Hydrate failed: %v", err)
	}

	expectedTime, _ := time.Parse("2006-01-02", "2024-01-15")
	if !entity.BirthDate.Equal(expectedTime) {
		t.Errorf("BirthDate: got %v, want %v", entity.BirthDate, expectedTime)
	}
}

func TestCoerceTimeFast_CachesSuccessfulLayout(t *testing.T) {
	fi := &FieldInfo{timeLayoutHint: new(atomic.Uint32)}

	got, ok := coerceTimeFast("2024-01-15T10:30:00Z", fi)
	if !ok {
		t.Fatal("expected RFC3339 datetime to parse")
	}
	if hint := fi.timeLayoutHint.Load(); hint != 1 {
		t.Fatalf("expected RFC3339 layout hint 1, got %d", hint)
	}
	want, _ := time.Parse(time.RFC3339, "2024-01-15T10:30:00Z")
	if !got.Equal(want) {
		t.Fatalf("parsed time mismatch: got %v want %v", got, want)
	}

	fi.timeLayoutHint.Store(2)
	got, ok = coerceTimeFast("2024-01-16T10:30:00Z", fi)
	if !ok {
		t.Fatal("expected RFC3339 datetime to parse after stale cache hint")
	}
	if hint := fi.timeLayoutHint.Load(); hint != 1 {
		t.Fatalf("expected cache hint to refresh back to 1, got %d", hint)
	}
	want, _ = time.Parse(time.RFC3339, "2024-01-16T10:30:00Z")
	if !got.Equal(want) {
		t.Fatalf("parsed time mismatch after cache refresh: got %v want %v", got, want)
	}
}

// TestCoerceTimeFast_NilHint verifies hand-built FieldInfo values without an
// allocated cache still parse (the cache is simply skipped).
func TestCoerceTimeFast_NilHint(t *testing.T) {
	got, ok := coerceTimeFast("2024-01-15T10:30:00Z", &FieldInfo{})
	if !ok {
		t.Fatal("expected datetime to parse with nil layout-hint cache")
	}
	want, _ := time.Parse(time.RFC3339, "2024-01-15T10:30:00Z")
	if !got.Equal(want) {
		t.Fatalf("parsed time mismatch: got %v want %v", got, want)
	}
}

// TestHydrate_TimeLayoutHintSharedAcrossCopies verifies the layout cache is
// shared between the registry-owned FieldInfo and value copies such as
// FieldByAttrName results (issue #43: per-copy hints silently never
// propagated).
func TestHydrate_TimeLayoutHintSharedAcrossCopies(t *testing.T) {
	ClearRegistry()
	if err := Register[entityWithDatetime](); err != nil {
		t.Fatal(err)
	}
	info, ok := LookupType(typeOf[entityWithDatetime]())
	if !ok {
		t.Fatal("entityWithDatetime not registered")
	}

	var e entityWithDatetime
	if err := Hydrate(&e, map[string]any{"name": "x", "created-at": "2024-01-15T10:30:00Z"}); err != nil {
		t.Fatalf("Hydrate failed: %v", err)
	}

	copyFI, ok := info.FieldByAttrName("created-at")
	if !ok {
		t.Fatal("created-at field not found")
	}
	if copyFI.timeLayoutHint == nil || copyFI.timeLayoutHint.Load() != 1 {
		t.Fatalf("expected FieldInfo copy to share the populated layout hint, got %v", copyFI.timeLayoutHint)
	}
}

// TestHydrate_TimeLayoutHint_ConcurrentAccess is the regression test for
// issue #43: concurrent hydration (which stores the layout hint) and
// FieldInfo value copies (plain reads of the same struct) on one registered
// model must be race-free. Run with -race (check.sh does) to enforce it.
func TestHydrate_TimeLayoutHint_ConcurrentAccess(t *testing.T) {
	ClearRegistry()
	if err := Register[entityWithDatetime](); err != nil {
		t.Fatal(err)
	}
	info, ok := LookupType(typeOf[entityWithDatetime]())
	if !ok {
		t.Fatal("entityWithDatetime not registered")
	}

	var wg sync.WaitGroup
	for range 4 {
		wg.Add(2)
		go func() {
			defer wg.Done()
			for range 100 {
				var e entityWithDatetime
				if err := Hydrate(&e, map[string]any{
					"name":       "x",
					"created-at": "2024-01-15T10:30:00Z",
				}); err != nil {
					t.Errorf("Hydrate failed: %v", err)
					return
				}
			}
		}()
		go func() {
			defer wg.Done()
			for range 100 {
				// Value-copies of FieldInfo, as query building does.
				if _, ok := info.FieldByAttrName("created-at"); !ok {
					t.Error("created-at field not found")
					return
				}
				for _, fi := range info.Fields {
					_ = fi
				}
			}
		}()
	}
	wg.Wait()
}

// TestHydrate_DatetimeNaiveParsesAsUTC pins the read half of the datetime
// contract (issue #53): zone-less datetime strings are UTC wall-clock time,
// and fractional seconds are preserved.
func TestHydrate_DatetimeNaiveParsesAsUTC(t *testing.T) {
	ClearRegistry()
	if err := Register[entityWithDatetime](); err != nil {
		t.Fatal(err)
	}

	var e entityWithDatetime
	if err := Hydrate(&e, map[string]any{
		"name":       "x",
		"created-at": "2024-06-01T13:04:05.123456789",
	}); err != nil {
		t.Fatalf("Hydrate failed: %v", err)
	}

	want := time.Date(2024, 6, 1, 13, 4, 5, 123456789, time.UTC)
	if !e.CreatedAt.Equal(want) {
		t.Errorf("CreatedAt: got %v, want %v", e.CreatedAt, want)
	}
	// The instant written by the compiler for a non-UTC value must hydrate
	// back to the same instant: 15:04:05+02:00 is stored as 13:04:05 UTC.
	orig := time.Date(2024, 6, 1, 15, 4, 5, 123456789, time.FixedZone("UTC+2", 2*60*60))
	if !e.CreatedAt.Equal(orig) {
		t.Errorf("round-trip instant shifted: got %v, want instant %v", e.CreatedAt, orig)
	}
}
