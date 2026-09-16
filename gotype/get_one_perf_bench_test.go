//go:build cgo && typedb && integration

package gotype

import (
	"context"
	"errors"
	"fmt"
	"testing"
)

// BenchmarkLiveGetOneAlternatives compares the exact answer-row count contract
// with a distinct count-first lookup and a bounded, different-contract query.
// The latter two are experimental workloads, not replacements for GetOne.
func BenchmarkLiveGetOneAlternatives(b *testing.B) {
	f := liveBenchSetup(b)
	ctx := context.Background()
	for i := range 27 {
		age := int64(400)
		if i < 2 {
			age = 300
		}
		p := &liveBenchPerson{Name: fmt.Sprintf("get-one-extra-%d", i), Email: fmt.Sprintf("get-one-extra-%d@example.test", i), Age: age}
		if err := f.personMgr.Insert(ctx, p); err != nil {
			b.Fatal(err)
		}
	}
	for _, cardinality := range []struct {
		name string
		age  int64
		rows int
	}{{"zero", 999, 0}, {"one", 30, 1}, {"two", 300, 2}, {"many", 400, 25}} {
		for _, approach := range []string{"current", "count-first", "bounded-two"} {
			b.Run(cardinality.name+"/"+approach, func(b *testing.B) {
				filter := map[string]any{"age": cardinality.age}
				b.ReportAllocs()
				b.ResetTimer()
				for range b.N {
					switch approach {
					case "current":
						model, err := f.personMgr.GetOne(ctx, filter)
						if cardinality.rows == 1 && (err != nil || model == nil || model.Age != cardinality.age) {
							b.Fatalf("unique model: %+v, %v", model, err)
						}
						if cardinality.rows == 0 {
							var missing *NotFoundError
							if !errors.As(err, &missing) {
								b.Fatalf("missing: %v", err)
							}
						}
						if cardinality.rows > 1 {
							var duplicate *NotUniqueError
							if !errors.As(err, &duplicate) || duplicate.Count != cardinality.rows {
								b.Fatalf("duplicate: %v", err)
							}
						}
					case "count-first":
						count, err := f.personMgr.Query().Filter(Eq("age", cardinality.age)).Count(ctx)
						if err != nil || count != int64(cardinality.rows) {
							b.Fatalf("count = %d, %v", count, err)
						}
						if count == 1 {
							model, err := f.personMgr.GetOne(ctx, filter)
							if err != nil || model == nil || model.Age != cardinality.age {
								b.Fatalf("unique model: %+v, %v", model, err)
							}
						}
					case "bounded-two":
						model, err := benchmarkBoundedGetOne(ctx, f.personMgr, Eq("age", cardinality.age))
						switch cardinality.rows {
						case 0:
							var missing *NotFoundError
							if model != nil || !errors.As(err, &missing) {
								b.Fatalf("bounded missing: %+v, %v", model, err)
							}
						case 1:
							if err != nil || model == nil || model.Age != cardinality.age {
								b.Fatalf("bounded unique: %+v, %v", model, err)
							}
						default:
							var duplicate *NotUniqueError
							if model != nil || !errors.As(err, &duplicate) || duplicate.Count != 2 {
								b.Fatalf("bounded duplicate: %+v, %v", model, err)
							}
						}
					}
				}
				b.StopTimer()
				b.ReportMetric(float64(cardinality.rows), "matching-rows")
				queries := 1.0
				if approach == "count-first" && cardinality.rows == 1 {
					queries = 2
				}
				b.ReportMetric(queries, "queries/op")
				b.ReportMetric(1, "callers")
			})
		}
	}
}
