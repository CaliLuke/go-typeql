//go:build cgo && typedb && integration

package driver

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/CaliLuke/go-typeql/v3/given"
	"github.com/CaliLuke/go-typeql/v3/internal/typeqlcheck"
)

func TestGivenRowsZeroValuesAndTypedNil(t *testing.T) {
	conn, name := setupLifecycleDB(t, "")
	tx, err := conn.Transaction(name, Read)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Close()
	const plain = `match let $v = "hello"; fetch { "value": $v };`
	const scalar = `given $s: string, $b: boolean, $i: integer, $d: double; match $s == $s; fetch { "s": $s, "b": $b, "i": $i, "d": $d };`
	typeqlcheck.AssertValid(t, "plain query without given input", plain)
	typeqlcheck.AssertValid(t, "zero scalar given values", scalar)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	queries := []struct {
		name string
		run  func(string, given.Rows) ([]map[string]any, error)
	}{
		{"rows", tx.QueryWithRows},
		{"options", func(q string, rows given.Rows) ([]map[string]any, error) {
			return tx.QueryWithOptionsAndRows(q, nil, rows)
		}},
		{"context", func(q string, rows given.Rows) ([]map[string]any, error) {
			return tx.QueryWithContextAndRows(ctx, q, rows)
		}},
		{"context_options", func(q string, rows given.Rows) ([]map[string]any, error) {
			return tx.QueryWithContextAndOptions(ctx, q, nil, rows)
		}},
	}
	ormRows := given.NewRows("s", "b", "i", "d")
	if err := ormRows.Add(given.Value{Type: "string", Value: ""}, given.Value{Type: "boolean", Value: false},
		given.Value{Type: "integer", Value: int64(0)}, given.Value{Type: "double", Value: float64(0)}); err != nil {
		t.Fatal(err)
	}
	driverRows := NewGivenRows("s", "b", "i", "d").MustAdd(StringGiven(""), BoolGiven(false), IntGiven(0), DoubleGiven(0))
	for _, query := range queries {
		t.Run(query.name, func(t *testing.T) {
			for _, rows := range []given.Rows{nil, (*GivenRows)(nil), (*given.TypedRows)(nil)} {
				got, err := query.run(plain, rows)
				if err != nil || !reflect.DeepEqual(got, []map[string]any{{"value": "hello"}}) {
					t.Fatalf("nil %T: results=%v err=%v", rows, got, err)
				}
			}
			for _, rows := range []given.Rows{driverRows, ormRows} {
				got, err := query.run(scalar, rows)
				if err != nil || len(got) != 1 {
					t.Fatalf("zero values %T: results=%#v err=%v", rows, got, err)
				}
				row := got[0]
				// Fetch documents can use signed or unsigned numeric encodings.
				zero := func(v any) bool { return v == int64(0) || v == uint64(0) || v == float64(0) }
				if len(row) != 4 || row["s"] != "" || row["b"] != false || !zero(row["i"]) || !zero(row["d"]) {
					t.Fatalf("zero values %T did not round trip: %#v", rows, got)
				}
			}
			for _, rows := range []given.Rows{NewGivenRows("s", "b", "i", "d"), given.NewRows("s", "b", "i", "d")} {
				got, err := query.run(scalar, rows)
				if err != nil || len(got) != 0 {
					t.Fatalf("empty input rows %T: results=%v err=%v", rows, got, err)
				}
			}
		})
	}
}
