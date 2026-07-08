package gotype

import (
	"math"
	"strings"
	"testing"
)

// Models exercising integer-width and slice-shape edge cases
// (issues #52 and #93).

type entityNarrowInts struct {
	BaseEntity
	Name  string `typedb:"name,key"`
	Small int8   `typedb:"small"`
	Wide  int64  `typedb:"wide"`
	Count uint16 `typedb:"tally"`
	OptI8 *int8  `typedb:"opt-i8,card=0..1"`
}

type entitySliceShapes struct {
	BaseEntity
	Name     string    `typedb:"name,key"`
	PtrElems []*string `typedb:"ptr-elems,card=0.."`
	PtrSlice *[]string `typedb:"ptr-slice,card=0.."`
	Uints    []uint    `typedb:"uints,card=0.."`
	U64s     []uint64  `typedb:"u64s,card=0.."`
	Narrow   []int8    `typedb:"narrow,card=0.."`
}

func registerNarrowInts(t *testing.T) {
	t.Helper()
	ClearRegistry()
	if err := Register[entityNarrowInts](); err != nil {
		t.Fatal(err)
	}
}

func registerSliceShapes(t *testing.T) {
	t.Helper()
	ClearRegistry()
	if err := Register[entitySliceShapes](); err != nil {
		t.Fatal(err)
	}
}

func hydrateNarrow(t *testing.T, data map[string]any) (*entityNarrowInts, error) {
	t.Helper()
	e := &entityNarrowInts{}
	base := map[string]any{"name": "n"}
	for k, v := range data {
		base[k] = v
	}
	return e, Hydrate(e, base)
}

// assertHydrateError asserts hydration fails and the error mentions want.
func assertHydrateError(t *testing.T, err error, want string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected hydration error containing %q, got nil", want)
	}
	if !strings.Contains(err.Error(), want) {
		t.Fatalf("expected error containing %q, got: %v", want, err)
	}
}

// TestHydrate_IntOverflowErrors is the regression test for issue #52:
// out-of-range values must error instead of silently wrapping (300 into an
// int8 used to hydrate as 44).
func TestHydrate_IntOverflowErrors(t *testing.T) {
	registerNarrowInts(t)

	_, err := hydrateNarrow(t, map[string]any{"small": int64(300)})
	assertHydrateError(t, err, "overflows")

	_, err = hydrateNarrow(t, map[string]any{"small": float64(300)})
	assertHydrateError(t, err, "overflows")

	// Pointer target with the same narrow width.
	_, err = hydrateNarrow(t, map[string]any{"opt-i8": int64(300)})
	assertHydrateError(t, err, "overflows")

	// In-range values still hydrate.
	e, err := hydrateNarrow(t, map[string]any{"small": int64(-128)})
	if err != nil {
		t.Fatalf("in-range value failed: %v", err)
	}
	if e.Small != -128 {
		t.Errorf("Small: got %d, want -128", e.Small)
	}
}

// TestHydrate_NonIntegralFloatErrors is the regression test for issue #52:
// 3.9 into an integer field must error, not truncate to 3. Integral floats
// (how JSON transports TypeDB integers) must still hydrate.
func TestHydrate_NonIntegralFloatErrors(t *testing.T) {
	registerNarrowInts(t)

	_, err := hydrateNarrow(t, map[string]any{"small": 3.9})
	assertHydrateError(t, err, "non-integral")

	_, err = hydrateNarrow(t, map[string]any{"wide": 3.9})
	assertHydrateError(t, err, "non-integral")

	e, err := hydrateNarrow(t, map[string]any{"small": 3.0, "wide": float64(1 << 40)})
	if err != nil {
		t.Fatalf("integral float failed: %v", err)
	}
	if e.Small != 3 {
		t.Errorf("Small: got %d, want 3", e.Small)
	}
	if e.Wide != 1<<40 {
		t.Errorf("Wide: got %d, want %d", e.Wide, int64(1)<<40)
	}
}

// TestHydrate_Uint64WrapErrors is the regression test for issue #52: a
// uint64 above MaxInt64 must not wrap to a negative int64.
func TestHydrate_Uint64WrapErrors(t *testing.T) {
	registerNarrowInts(t)

	_, err := hydrateNarrow(t, map[string]any{"wide": uint64(math.MaxUint64)})
	assertHydrateError(t, err, "overflows")
}

// TestHydrate_NegativeIntoUnsignedErrors verifies negative values error on
// unsigned targets instead of panicking or wrapping (issues #52/#93).
func TestHydrate_NegativeIntoUnsignedErrors(t *testing.T) {
	registerNarrowInts(t)

	_, err := hydrateNarrow(t, map[string]any{"tally": int64(-1)})
	assertHydrateError(t, err, "overflows")

	_, err = hydrateNarrow(t, map[string]any{"tally": int64(70000)})
	assertHydrateError(t, err, "overflows")

	e, err := hydrateNarrow(t, map[string]any{"tally": float64(65535)})
	if err != nil {
		t.Fatalf("in-range unsigned value failed: %v", err)
	}
	if e.Count != 65535 {
		t.Errorf("Count: got %d, want 65535", e.Count)
	}
}

// TestHydrate_SliceOfPointerElems is the regression test for issue #93:
// []*T fields used to panic ("value of type string is not assignable to
// type *string").
func TestHydrate_SliceOfPointerElems(t *testing.T) {
	registerSliceShapes(t)

	e := &entitySliceShapes{}
	err := Hydrate(e, map[string]any{
		"name":      "s",
		"ptr-elems": []any{"a", "b"},
	})
	if err != nil {
		t.Fatalf("Hydrate failed: %v", err)
	}
	if len(e.PtrElems) != 2 || e.PtrElems[0] == nil || *e.PtrElems[0] != "a" || e.PtrElems[1] == nil || *e.PtrElems[1] != "b" {
		t.Errorf("PtrElems: got %v", e.PtrElems)
	}
}

// TestHydrate_PointerToSliceField is the regression test for issue #93:
// *[]T fields used to panic ("MakeSlice of non-slice type").
func TestHydrate_PointerToSliceField(t *testing.T) {
	registerSliceShapes(t)

	e := &entitySliceShapes{}
	err := Hydrate(e, map[string]any{
		"name":      "s",
		"ptr-slice": []any{"x", "y"},
	})
	if err != nil {
		t.Fatalf("Hydrate failed: %v", err)
	}
	if e.PtrSlice == nil || len(*e.PtrSlice) != 2 || (*e.PtrSlice)[0] != "x" || (*e.PtrSlice)[1] != "y" {
		t.Errorf("PtrSlice: got %v", e.PtrSlice)
	}

	// Single (non-slice) value wraps into a one-element slice.
	e2 := &entitySliceShapes{}
	if err := Hydrate(e2, map[string]any{"name": "s", "ptr-slice": "solo"}); err != nil {
		t.Fatalf("Hydrate single value failed: %v", err)
	}
	if e2.PtrSlice == nil || len(*e2.PtrSlice) != 1 || (*e2.PtrSlice)[0] != "solo" {
		t.Errorf("PtrSlice single: got %v", e2.PtrSlice)
	}
}

// TestHydrate_UnsignedSliceElems is the regression test for issue #93:
// []uint and []uint64 used to panic because the slow path only produced
// signed values.
func TestHydrate_UnsignedSliceElems(t *testing.T) {
	registerSliceShapes(t)

	e := &entitySliceShapes{}
	err := Hydrate(e, map[string]any{
		"name":  "s",
		"uints": []any{int64(1), float64(2)},
		"u64s":  []any{int64(3), int64(4)},
	})
	if err != nil {
		t.Fatalf("Hydrate failed: %v", err)
	}
	if len(e.Uints) != 2 || e.Uints[0] != 1 || e.Uints[1] != 2 {
		t.Errorf("Uints: got %v", e.Uints)
	}
	if len(e.U64s) != 2 || e.U64s[0] != 3 || e.U64s[1] != 4 {
		t.Errorf("U64s: got %v", e.U64s)
	}
}

// TestHydrate_SliceElemOverflowErrors verifies slice elements get the same
// range checks as scalars (issue #52).
func TestHydrate_SliceElemOverflowErrors(t *testing.T) {
	registerSliceShapes(t)

	e := &entitySliceShapes{}
	err := Hydrate(e, map[string]any{
		"name":   "s",
		"narrow": []any{int64(1), int64(300)},
	})
	assertHydrateError(t, err, "overflows")

	e = &entitySliceShapes{}
	err = Hydrate(e, map[string]any{
		"name":  "s",
		"uints": []any{int64(-1)},
	})
	assertHydrateError(t, err, "overflows")
}
