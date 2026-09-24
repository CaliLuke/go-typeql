package gotype

import (
	"fmt"
	"strings"
	"testing"

	"github.com/CaliLuke/go-typeql/v3/given"
)

func membershipInputs(size int) ([]any, *given.TypedRows) {
	values := make([]any, size)
	rows := given.NewRows("n")
	for i := range size {
		name := fmt.Sprintf("person-%02d", i)
		if i >= 25 {
			name = fmt.Sprintf("absent-%02d", i)
		}
		values[i] = name
		if err := rows.Add(given.Value{Type: "string", Value: name}); err != nil {
			panic(err) // one declared variable and one value are always supplied
		}
	}
	return values, rows
}

func membershipExpandedQuery(values []any) string {
	return "match\n$e isa live-bench-person;\n" + strings.Join(compilePatterns(In("name", values)), "\n") + "\nfetch { \"name\": $e__name };"
}

const membershipGivenQuery = `given $n: string;
match $e isa live-bench-person, has name == $n;
fetch { "name": $n };`

// BenchmarkMembershipConstruction compares emitted TypeQL with typed input
// row construction and JSON serialization, excluding server execution.
func BenchmarkMembershipConstruction(b *testing.B) {
	for _, size := range []int{1, 8, 25, 64, 256} {
		values, _ := membershipInputs(size)
		b.Run(fmt.Sprintf("values=%d/expanded", size), func(b *testing.B) {
			b.ReportAllocs()
			var query string
			for range b.N {
				query = membershipExpandedQuery(values)
			}
			b.ReportMetric(float64(len(query)), "query-bytes")
			b.ReportMetric(float64(size), "values")
		})
		b.Run(fmt.Sprintf("values=%d/typed-rows", size), func(b *testing.B) {
			b.ReportAllocs()
			var payload []byte
			for range b.N {
				rows := given.NewRows("n")
				for _, value := range values {
					if err := rows.Add(given.Value{Type: "string", Value: value}); err != nil {
						b.Fatal(err)
					}
				}
				var err error
				payload, err = rows.MarshalGivenRows()
				if err != nil {
					b.Fatal(err)
				}
			}
			b.ReportMetric(float64(len(membershipGivenQuery)), "query-bytes")
			b.ReportMetric(float64(len(payload)), "input-bytes")
			b.ReportMetric(float64(size), "values")
		})
	}
}

// BenchmarkIIDMembershipConstruction isolates IIDIn's query-text growth.
// Typed string values cannot stand in for the TypeQL iid token or an opaque
// concept handle, so this group intentionally has no typed-row comparison.
func BenchmarkIIDMembershipConstruction(b *testing.B) {
	for _, size := range []int{1, 8, 25, 64, 256} {
		iids := make([]string, size)
		for i := range iids {
			iids[i] = fmt.Sprintf("0x%016x", i+1)
		}
		b.Run(fmt.Sprintf("values=%d", size), func(b *testing.B) {
			b.ReportAllocs()
			var query string
			for range b.N {
				query = "match $e isa live-bench-person;\n" + strings.Join(compilePatterns(IIDIn(iids...)), "\n") + "\nfetch { \"iid\": iid($e) };"
			}
			b.ReportMetric(float64(len(query)), "query-bytes")
			b.ReportMetric(float64(size), "values")
		})
	}
}
