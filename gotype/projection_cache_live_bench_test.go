//go:build cgo && typedb && integration

package gotype

import (
	"context"
	"sync/atomic"
	"testing"
)

type uncachedFetchStrategy struct{ ModelStrategy }

func (s uncachedFetchStrategy) BuildFetchAll(info *ModelInfo, varName string) (string, error) {
	return compileFetchAll(info, varName)
}

// BenchmarkLiveFetchCache measures the cache's effect on a complete unique-key
// ORM read. Run against compose with -benchtime=100x -count=5.
func BenchmarkLiveFetchCache(b *testing.B) {
	f := liveBenchSetup(b)
	ctx := context.Background()
	for _, cached := range []bool{false, true} {
		label := "fresh-projection"
		if cached {
			label = "cached-projection"
		}
		b.Run(label, func(b *testing.B) {
			var queries atomic.Int64
			mgr := mustLiveBenchManager[liveBenchPerson](NewDatabase(&liveBenchCountConn{Conn: f.db.GetConn(), queries: &queries}, f.dbName))
			if !cached {
				mgr.strategy = uncachedFetchStrategy{ModelStrategy: mgr.strategy}
			}
			b.ReportAllocs()
			for range b.N {
				row, err := mgr.GetOne(ctx, map[string]any{"name": f.person.Name})
				if err != nil || row == nil {
					b.Fatalf("GetOne: %v, %v", row, err)
				}
			}
			b.ReportMetric(float64(queries.Load())/float64(b.N), "queries/op")
		})
	}
}
