//go:build cgo && typedb && integration

package gotype

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
)

var liveDeleteSequence atomic.Uint64

// BenchmarkLiveDeleteMany compares 16-row deletions, separating strict
// existence checks from non-strict writes. Seed insertion is outside timing.
// Run against compose with -benchtime=5x -count=1 in five independent processes.
func BenchmarkLiveDeleteMany(b *testing.B) {
	f := liveBenchSetup(b)
	ctx := context.Background()
	for _, strict := range []bool{false, true} {
		for _, grouped := range []bool{false, true} {
			label := "per-instance"
			if grouped {
				label = "grouped"
			}
			b.Run(fmt.Sprintf("strict=%t/%s", strict, label), func(b *testing.B) {
				var queryCount atomic.Int64
				conn := &putCountConn{Conn: f.db.GetConn(), queryCount: &queryCount}
				mgr := mustLiveBenchManager[liveBenchPerson](NewDatabase(conn, f.dbName))
				b.ReportAllocs()
				for range b.N {
					b.StopTimer()
					seq := liveDeleteSequence.Add(1)
					people := make([]*liveBenchPerson, 16)
					for i := range people {
						name := fmt.Sprintf("delete-%d-%d", seq, i)
						people[i] = &liveBenchPerson{Name: name, Email: name + "@example.test", Age: 30}
						if err := f.personMgr.Insert(ctx, people[i]); err != nil {
							b.Fatal(err)
						}
					}
					b.StartTimer()
					if grouped {
						var err error
						if strict {
							err = mgr.DeleteMany(ctx, people, WithStrict())
						} else {
							err = mgr.DeleteMany(ctx, people)
						}
						if err != nil {
							b.Fatal(err)
						}
					} else if err := deleteManyPerInstance(ctx, mgr, people, strict); err != nil {
						b.Fatal(err)
					}
				}
				b.ReportMetric(float64(queryCount.Load())/float64(b.N), "queries/op")
			})
		}
	}
}

func deleteManyPerInstance(ctx context.Context, mgr *Manager[liveBenchPerson], people []*liveBenchPerson, strict bool) error {
	if strict {
		for _, p := range people {
			count, err := mgr.countByIID(ctx, p.GetIID())
			if err != nil {
				return err
			}
			if count == 0 {
				return fmt.Errorf("legacy strict check: missing IID %s", p.GetIID())
			}
		}
	}
	return mgr.withWriteTx(ctx, "delete_many", mgr.writeTx, func(tx Tx) error {
		for _, p := range people {
			query := fmt.Sprintf("match\n$e isa %s, iid %s;\ndelete $e;", mgr.info.TypeName, p.GetIID())
			if _, err := tx.QueryWithContext(ctx, query); err != nil {
				return err
			}
		}
		return nil
	})
}
