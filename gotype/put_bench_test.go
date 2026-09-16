//go:build cgo && typedb && integration

package gotype

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
)

var livePutSequence atomic.Uint64

type putCountConn struct {
	Conn
	queryCount *atomic.Int64
}

func (c *putCountConn) Transaction(name string, mode int) (Tx, error) {
	tx, err := c.Conn.Transaction(name, mode)
	if err != nil {
		return nil, err
	}
	return &putCountTx{Tx: tx, count: c.queryCount}, nil
}

type putCountTx struct {
	Tx
	count *atomic.Int64
}

func (t *putCountTx) QueryWithContext(ctx context.Context, query string) ([]map[string]any, error) {
	t.count.Add(1)
	return t.Tx.QueryWithContext(ctx, query)
}

// BenchmarkLivePut compares the prior two-query keyed upsert with a combined
// put/fetch query for fresh keyed entities (the insertion branch). Run against
// compose with -benchtime=10x -count=1 in five independent processes. The legacy helper omits the public
// method's key validation, so allocation counts are not a perfect comparison.
func BenchmarkLivePut(b *testing.B) {
	f := liveBenchSetup(b)
	ctx := context.Background()
	for _, legacy := range []bool{true, false} {
		label := "combined"
		if legacy {
			label = "two-queries"
		}
		b.Run(label, func(b *testing.B) {
			var queryCount atomic.Int64
			conn := &putCountConn{Conn: f.db.GetConn(), queryCount: &queryCount}
			mgr := mustLiveBenchManager[liveBenchPerson](NewDatabase(conn, f.dbName))
			b.ReportAllocs()
			for range b.N {
				id := livePutSequence.Add(1)
				person := &liveBenchPerson{Name: fmt.Sprintf("put-%d", id), Email: fmt.Sprintf("put-%d@example.test", id)}
				if legacy {
					if err := putWithSeparateIIDFetch(ctx, mgr, person); err != nil {
						b.Fatal(err)
					}
				} else if err := mgr.Put(ctx, person); err != nil {
					b.Fatal(err)
				}
				if person.GetIID() == "" {
					b.Fatal("put did not return IID")
				}
			}
			b.ReportMetric(float64(queryCount.Load())/float64(b.N), "queries/op")
		})
	}
}

func putWithSeparateIIDFetch(ctx context.Context, mgr *Manager[liveBenchPerson], person *liveBenchPerson) error {
	putQuery, err := mgr.strategy.BuildPutQuery(mgr.info, person, "e")
	if err != nil {
		return err
	}
	return mgr.withWriteTx(ctx, "put", mgr.writeTx, func(tx Tx) error {
		if _, err := tx.QueryWithContext(ctx, putQuery); err != nil {
			return err
		}
		matchQuery, err := mgr.strategy.BuildMatchByKey(mgr.info, person, "e")
		if err != nil {
			return err
		}
		results, err := tx.QueryWithContext(ctx, matchQuery+"\n"+`fetch { "_iid": iid($e) };`)
		if err != nil {
			return err
		}
		if len(results) == 1 {
			setIIDOnInfo(person, mgr.info, extractIID(results[0]))
		}
		return nil
	})
}
