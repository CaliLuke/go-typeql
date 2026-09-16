//go:build cgo && typedb && integration

package gotype

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
)

var liveUpdateSequence atomic.Uint64

// BenchmarkLiveUpdateMany compares heterogeneous scalar updates across batch
// sizes. Seeding is outside timing. Run with -benchtime=5x -count=5.
func BenchmarkLiveUpdateMany(b *testing.B) {
	f := liveBenchSetup(b)
	ctx := context.Background()
	for _, size := range []int{4, 16, 64} {
		for _, batched := range []bool{false, true} {
			label := "per-instance"
			if batched {
				label = "batched"
			}
			b.Run(fmt.Sprintf("rows=%d/%s", size, label), func(b *testing.B) {
				b.StopTimer()
				seq := liveUpdateSequence.Add(1)
				people := make([]*liveBenchPerson, size)
				for i := range people {
					name := fmt.Sprintf("update-%d-%d", seq, i)
					people[i] = &liveBenchPerson{Name: name, Email: name + "@example.test", Age: 30}
				}
				if err := f.personMgr.InsertMany(ctx, people); err != nil {
					b.Fatal(err)
				}
				var queryCount atomic.Int64
				conn := &insertBatchCountConn{Conn: f.db.GetConn(), queryCount: &queryCount, batch: batched}
				mgr := mustLiveBenchManager[liveBenchPerson](NewDatabase(conn, f.dbName))
				b.ReportAllocs()
				b.StartTimer()
				for iteration := range b.N {
					for i, p := range people {
						p.Age = int64(100 + iteration*size + i)
					}
					if err := mgr.UpdateMany(ctx, people); err != nil {
						b.Fatal(err)
					}
				}
				b.ReportMetric(float64(queryCount.Load())/float64(b.N), "queries/op")
			})
		}
	}
}
