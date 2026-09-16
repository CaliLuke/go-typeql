//go:build cgo && typedb && integration

package gotype

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
)

var livePutBatchSequence atomic.Uint64

// BenchmarkLivePutMany uses eight existing and eight fresh scalar keyed
// entities per operation, with unchanged attributes on existing rows.
// Run against compose with -benchtime=5x -count=5.
func BenchmarkLivePutMany(b *testing.B) {
	f := liveBenchSetup(b)
	ctx := context.Background()
	for _, batched := range []bool{false, true} {
		label := "per-instance"
		if batched {
			label = "batched"
		}
		b.Run(label, func(b *testing.B) {
			var queryCount atomic.Int64
			conn := &insertBatchCountConn{Conn: f.db.GetConn(), queryCount: &queryCount, batch: batched}
			mgr := mustLiveBenchManager[liveBenchPerson](NewDatabase(conn, f.dbName))
			b.ReportAllocs()
			for range b.N {
				seq := livePutBatchSequence.Add(1)
				people := make([]*liveBenchPerson, 16)
				for i := range people {
					if i%2 == 0 {
						j := i / 2
						people[i] = &liveBenchPerson{Name: fmt.Sprintf("person-%02d", j), Email: fmt.Sprintf("person-%02d@example.test", j), Age: int64(30 + j)}
					} else {
						name := fmt.Sprintf("put-batch-%d-%d", seq, i)
						people[i] = &liveBenchPerson{Name: name, Email: name + "@example.test", Age: 30}
					}
				}
				if err := mgr.PutMany(ctx, people); err != nil {
					b.Fatal(err)
				}
				for i, p := range people {
					if p.GetIID() == "" {
						b.Fatalf("row %d missing IID", i)
					}
				}
			}
			b.ReportMetric(float64(queryCount.Load())/float64(b.N), "queries/op")
		})
	}
}
