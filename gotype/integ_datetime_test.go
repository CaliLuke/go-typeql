//go:build integration && cgo && typedb

package gotype_test

import (
	"context"
	"testing"
	"time"

	"github.com/CaliLuke/go-typeql/gotype"
)

// DatetimeEvent exercises the plain (zone-less) datetime value type.
type DatetimeEvent struct {
	gotype.BaseEntity
	Name       string    `typedb:"name,key"`
	OccurredAt time.Time `typedb:"occurred-at"`
}

func setupDatetimeDB(t *testing.T) *gotype.Database {
	return setupTestDBWith(t, func() {
		_ = gotype.Register[DatetimeEvent]()
	})
}

// TestIntegration_DatetimeRoundTrip_NonUTC is the round-trip regression test
// for issues #53/#66: a non-UTC time.Time is written as a UTC naive datetime
// literal and must hydrate back to the same instant (as UTC), not shifted by
// the original zone offset. Sub-second precision must survive the trip.
func TestIntegration_DatetimeRoundTrip_NonUTC(t *testing.T) {
	db := setupDatetimeDB(t)
	ctx := context.Background()
	mgr := gotype.MustNewManager[DatetimeEvent](db)

	loc := time.FixedZone("UTC+2", 2*60*60)
	orig := time.Date(2024, 6, 1, 15, 4, 5, 123456789, loc) // 13:04:05.123456789 UTC

	assertInsert(t, ctx, mgr, &DatetimeEvent{Name: "dt-nonutc", OccurredAt: orig})

	r := assertGetOne(t, ctx, mgr, map[string]any{"name": "dt-nonutc"})
	if !r.OccurredAt.Equal(orig) {
		t.Errorf("datetime instant shifted on round trip: inserted %v (= %v), got back %v",
			orig, orig.UTC(), r.OccurredAt)
	}
	if got := r.OccurredAt.Nanosecond(); got != orig.Nanosecond() {
		t.Errorf("sub-second precision lost on round trip: got %dns, want %dns",
			got, orig.Nanosecond())
	}
}

// TestIntegration_DatetimeRoundTrip_Midnight guards the issue #66 regression:
// a midnight timestamp must be written as a datetime literal (not silently
// downgraded to a date literal) and round-trip intact.
func TestIntegration_DatetimeRoundTrip_Midnight(t *testing.T) {
	db := setupDatetimeDB(t)
	ctx := context.Background()
	mgr := gotype.MustNewManager[DatetimeEvent](db)

	orig := time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC)

	assertInsert(t, ctx, mgr, &DatetimeEvent{Name: "dt-midnight", OccurredAt: orig})

	r := assertGetOne(t, ctx, mgr, map[string]any{"name": "dt-midnight"})
	if !r.OccurredAt.Equal(orig) {
		t.Errorf("midnight datetime shifted on round trip: inserted %v, got back %v",
			orig, r.OccurredAt)
	}
}
