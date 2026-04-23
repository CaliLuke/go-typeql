package gotype

import (
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
	fi := &FieldInfo{}

	got, ok := coerceTimeFast("2024-01-15T10:30:00Z", fi)
	if !ok {
		t.Fatal("expected RFC3339 datetime to parse")
	}
	if fi.timeLayoutHint != 1 {
		t.Fatalf("expected RFC3339 layout hint 1, got %d", fi.timeLayoutHint)
	}
	want, _ := time.Parse(time.RFC3339, "2024-01-15T10:30:00Z")
	if !got.Equal(want) {
		t.Fatalf("parsed time mismatch: got %v want %v", got, want)
	}

	fi.timeLayoutHint = 2
	got, ok = coerceTimeFast("2024-01-16T10:30:00Z", fi)
	if !ok {
		t.Fatal("expected RFC3339 datetime to parse after stale cache hint")
	}
	if fi.timeLayoutHint != 1 {
		t.Fatalf("expected cache hint to refresh back to 1, got %d", fi.timeLayoutHint)
	}
	want, _ = time.Parse(time.RFC3339, "2024-01-16T10:30:00Z")
	if !got.Equal(want) {
		t.Fatalf("parsed time mismatch after cache refresh: got %v want %v", got, want)
	}
}
