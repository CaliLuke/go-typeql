//go:build cgo && typedb && integration

package driver

import (
	"context"
	"maps"
	"reflect"
	"testing"

	"github.com/CaliLuke/go-typeql/v3/internal/typeqlcheck"
)

func TestFetchPreservesNumericValues(t *testing.T) {
	conn, name := setupLifecycleDB(t, "")
	tx, err := conn.Transaction(name, Read)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Close()
	const query = `match
let $large = 9007199254740993;
let $negative = -9007199254740993;
let $maximum = 9223372036854775807;
let $minimum = -9223372036854775808;
let $double = 2.0;
let $decimal = -9.59dec;
fetch { "large": $large, "negative": $negative, "maximum": $maximum,
"minimum": $minimum, "double": $double, "decimal": $decimal,
"nested": { "large": $large } };`
	typeqlcheck.AssertValid(t, "fetch exact numeric values", query)
	want := []map[string]any{{
		"large": uint64(9007199254740993), "negative": int64(-9007199254740993),
		"maximum": uint64(9223372036854775807), "minimum": int64(-9223372036854775808),
		"double": float64(2), "decimal": "-9.59dec",
		"nested": map[string]any{"large": uint64(9007199254740993)},
	}}
	t.Run("materialized", func(t *testing.T) {
		got, err := tx.Query(query)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("fetch values = %#v, want %#v", got, want)
		}
	})
	t.Run("callback", func(t *testing.T) {
		var got []map[string]any
		err := tx.QueryEachWithContext(context.Background(), query, func(_ int, row map[string]any) error {
			got = append(got, maps.Clone(row))
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("fetch values = %#v, want %#v", got, want)
		}
	})
}
