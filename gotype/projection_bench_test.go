//go:build cgo && typedb && integration

package gotype

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/CaliLuke/go-typeql/driver"
)

type projectionBenchNarrow struct {
	BaseEntity
	Name string `typedb:"name,key"`
}

type projectionBenchWide struct {
	BaseEntity
	Name string `typedb:"name,key"`
	A    string `typedb:"attr-a"`
	B    string `typedb:"attr-b"`
	C    string `typedb:"attr-c"`
	D    string `typedb:"attr-d"`
	E    string `typedb:"attr-e"`
	F    string `typedb:"attr-f"`
	G    string `typedb:"attr-g"`
	H    string `typedb:"attr-h"`
}

// BenchmarkLiveProjectionReads compares full-model reads with explicit
// identity-and-name projections on narrow and wide 64-row fixtures.
func BenchmarkLiveProjectionReads(b *testing.B) {
	ClearRegistry()
	MustRegister[projectionBenchNarrow]()
	MustRegister[projectionBenchWide]()
	drv, err := driver.OpenWithTLS(liveBenchAddress(), "admin", "password", false, "")
	if err != nil {
		b.Fatal(err)
	}
	defer drv.Close()
	name := fmt.Sprintf("projection_bench_%d", time.Now().UnixNano())
	if err := drv.Databases().Create(name); err != nil {
		b.Fatal(err)
	}
	defer drv.Databases().Delete(name)
	db := NewDatabase(&liveBenchDriverAdapter{drv: drv}, name)
	ctx := context.Background()
	if err := db.ExecuteSchema(ctx, GenerateSchema()); err != nil {
		b.Fatal(err)
	}
	narrow := MustNewManager[projectionBenchNarrow](db)
	wide := MustNewManager[projectionBenchWide](db)
	payload := strings.Repeat("x", 256)
	for i := range 64 {
		key := fmt.Sprintf("item-%02d", i)
		if err := narrow.Insert(ctx, &projectionBenchNarrow{Name: key}); err != nil {
			b.Fatal(err)
		}
		if err := wide.Insert(ctx, &projectionBenchWide{Name: key, A: payload, B: payload, C: payload, D: payload, E: payload, F: payload, G: payload, H: payload}); err != nil {
			b.Fatal(err)
		}
	}
	benchmarkProjectionShape(b, "narrow", narrow)
	benchmarkProjectionShape(b, "wide", wide)
}

func benchmarkProjectionShape[T any](b *testing.B, shape string, mgr *Manager[T]) {
	for _, mode := range []string{"full", "projected"} {
		b.Run(shape+"/"+mode, func(b *testing.B) {
			b.StopTimer()
			match, err := mgr.buildFilteredMatch("e", nil)
			if err != nil {
				b.Fatal(err)
			}
			var query string
			if mode == "full" {
				fetch, err := mgr.strategy.BuildFetchAll(mgr.info, "e")
				if err != nil {
					b.Fatal(err)
				}
				query = match + "\n" + fetch
			} else {
				plan, err := planProjection(mgr.info, Projection{Fields: []string{"name"}})
				if err != nil {
					b.Fatal(err)
				}
				additions, fetch := plan.clauses()
				query = match + "\n" + additions + "\n" + fetch
			}
			raw, err := mgr.readQuery(context.Background(), query)
			if err != nil {
				b.Fatal(err)
			}
			encoded, err := json.Marshal(raw)
			if err != nil {
				b.Fatal(err)
			}
			if len(raw) != 64 {
				b.Fatalf("fixture returned %d rows, want 64", len(raw))
			}
			b.ReportAllocs()
			b.ResetTimer()
			b.StartTimer()
			for range b.N {
				if mode == "full" {
					models, err := mgr.Get(context.Background(), nil)
					if err != nil || len(models) != 64 {
						b.Fatalf("full read: rows=%d err=%v", len(models), err)
					}
				} else {
					results, err := mgr.GetProjected(context.Background(), nil, Projection{Fields: []string{"name"}})
					if err != nil || len(results) != 64 {
						b.Fatalf("projected read: rows=%d err=%v", len(results), err)
					}
				}
			}
			b.StopTimer()
			b.ReportMetric(float64(len(encoded)), "result-json-bytes")
			b.ReportMetric(float64(len(query)), "query-bytes")
			b.ReportMetric(64, "rows/op")
		})
	}
}
