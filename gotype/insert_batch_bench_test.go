//go:build cgo && typedb && integration

package gotype

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"

	"github.com/CaliLuke/go-typeql/v3/given"
)

var liveInsertBatchSequence atomic.Uint64

type insertBatchCountConn struct {
	Conn
	queryCount *atomic.Int64
	batch      bool
}

func (c *insertBatchCountConn) Transaction(name string, mode int) (Tx, error) {
	tx, err := c.Conn.Transaction(name, mode)
	if err != nil {
		return nil, err
	}
	counted := &insertLegacyCountTx{Tx: tx, count: c.queryCount}
	if c.batch {
		return &insertBatchCountTx{insertLegacyCountTx: counted}, nil
	}
	return counted, nil
}

type insertLegacyCountTx struct {
	Tx
	count *atomic.Int64
}

func (t *insertLegacyCountTx) QueryWithContext(ctx context.Context, query string) ([]map[string]any, error) {
	t.count.Add(1)
	return t.Tx.QueryWithContext(ctx, query)
}

type insertBatchCountTx struct{ *insertLegacyCountTx }

func (t *insertBatchCountTx) QueryWithGivenRows(ctx context.Context, query string, rows *given.TypedRows) ([]map[string]any, error) {
	t.count.Add(1)
	return t.Tx.(batchInsertTx).QueryWithGivenRows(ctx, query, rows)
}

// BenchmarkLiveInsertMany compares committed batch sizes and query counts.
// Run opt-in against compose with -benchtime=5x -count=5. Each insertion gets
// unique keys; the shared fixture grows across measurements.
func BenchmarkLiveInsertMany(b *testing.B) {
	f := liveBenchSetup(b)
	ctx := context.Background()
	for _, size := range []int{4, 16, 64} {
		for _, batched := range []bool{false, true} {
			label := "legacy"
			if batched {
				label = "batched"
			}
			b.Run(fmt.Sprintf("rows=%d/%s", size, label), func(b *testing.B) {
				var queryCount atomic.Int64
				conn := &insertBatchCountConn{Conn: f.db.GetConn(), queryCount: &queryCount, batch: batched}
				mgr := mustLiveBenchManager[liveBenchPerson](NewDatabase(conn, f.dbName))
				b.ReportAllocs()
				for range b.N {
					sequence := liveInsertBatchSequence.Add(1)
					people := make([]*liveBenchPerson, size)
					for i := range people {
						name := fmt.Sprintf("batch-%d-%d", sequence, i)
						people[i] = &liveBenchPerson{Name: name, Email: name + "@example.test", Age: 30}
					}
					if err := mgr.InsertMany(ctx, people); err != nil {
						b.Fatal(err)
					}
				}
				b.ReportMetric(float64(queryCount.Load())/float64(b.N), "queries/op")
			})
		}
	}
}
