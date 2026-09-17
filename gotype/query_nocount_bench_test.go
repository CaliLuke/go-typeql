//go:build cgo && typedb && integration

package gotype

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
)

// BenchmarkLiveQueryNoCount compares identical bulk mutation queries with and
// without a separate distinct-count query. Each mutation is rolled back so
// all iterations see the same 256-person fixture.
func BenchmarkLiveQueryNoCount(b *testing.B) {
	f := liveBenchSetup(b)
	registerProjectionLiveModels()
	ctx := context.Background()
	for _, op := range []string{"update", "delete"} {
		for _, scope := range []string{"broad", "selective"} {
			for _, counted := range []bool{true, false} {
				label := "no-count"
				if counted {
					label = "counted"
				}
				b.Run(fmt.Sprintf("%s/%s/%s", op, scope, label), func(b *testing.B) {
					var queries atomic.Int64
					db := NewDatabase(&liveBenchCountConn{Conn: f.db.GetConn(), queries: &queries}, f.dbName)
					b.ReportAllocs()
					b.StopTimer()
					for range b.N {
						transaction, err := db.Begin(WriteTransaction)
						if err != nil {
							b.Fatal(err)
						}
						mgr := MustNewManagerWithTx[liveBenchPerson](transaction)
						q := mgr.Query()
						want := int64(liveBenchPersonCount)
						if scope == "selective" {
							q.Filter(Eq("name", "person-00"))
							want = 1
						}
						b.StartTimer()
						switch {
						case op == "update" && counted:
							var count int64
							count, err = q.Update(ctx, map[string]any{"age": 777})
							if count != want {
								b.Fatalf("updated %d, want %d", count, want)
							}
						case op == "update":
							err = q.UpdateNoCount(ctx, map[string]any{"age": 777})
						case counted:
							var count int64
							count, err = q.Delete(ctx)
							if count != want {
								b.Fatalf("deleted %d, want %d", count, want)
							}
						default:
							err = q.DeleteNoCount(ctx)
						}
						b.StopTimer()
						if err != nil {
							b.Fatal(err)
						}
						if err := transaction.Rollback(); err != nil {
							b.Fatal(err)
						}
						transaction.Close()
					}
					b.ReportMetric(float64(queries.Load())/float64(b.N), "queries/op")
				})
			}
		}
	}
}
