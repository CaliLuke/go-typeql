//go:build cgo && typedb && integration

package gotype

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/CaliLuke/go-typeql/driver"
)

// BenchmarkLivePoolRead compares a shared driver with pools of one or four
// drivers. Each operation includes checked native cleanup before completion.
func BenchmarkLivePoolRead(b *testing.B) {
	address := os.Getenv("TEST_DB_ADDRESS")
	if address == "" {
		address = "localhost:1729"
	}
	for _, callers := range []int{1, 4, 10} {
		for _, mode := range []struct {
			name string
			size int
		}{{"shared", 0}, {"pool-1", 1}, {"pool-4", 4}} {
			b.Run(fmt.Sprintf("%s/callers=%d", mode.name, callers), func(b *testing.B) {
				benchmarkPoolReadCase(b, address, mode.size, callers)
			})
		}
	}
}

type poolReadTimings struct {
	latency, acquire, open, query, close time.Duration
}

func benchmarkPoolReadCase(b *testing.B, address string, poolSize, callers int) {
	admin, err := driver.Open(address, "admin", "password")
	if err != nil {
		b.Fatal(err)
	}
	defer admin.Close()
	name := fmt.Sprintf("pool_bench_%d", time.Now().UnixNano())
	if err := admin.Databases().Create(name); err != nil {
		b.Fatal(err)
	}
	defer admin.Databases().Delete(name)
	schema, err := admin.Transaction(name, driver.Schema)
	if err != nil {
		b.Fatal(err)
	}
	if _, err := schema.Query(`define attribute name, value string; entity person, owns name @key;`); err != nil {
		schema.Close()
		b.Fatal(err)
	}
	if err := schema.Commit(); err != nil {
		b.Fatal(err)
	}
	write, err := admin.Transaction(name, driver.Write)
	if err != nil {
		b.Fatal(err)
	}
	var inserts strings.Builder
	inserts.WriteString("insert\n")
	for i := range 25 {
		fmt.Fprintf(&inserts, "$p%d isa person, has name \"person-%d\";\n", i, i)
	}
	if _, err := write.Query(inserts.String()); err != nil {
		write.Close()
		b.Fatal(err)
	}
	if err := write.Commit(); err != nil {
		b.Fatal(err)
	}

	var pool *ConnPool
	var natives []*driver.Driver
	var shared Conn
	var mu sync.Mutex
	if poolSize > 0 {
		pool, err = NewConnPool(PoolConfig{MinSize: poolSize, MaxSize: poolSize, WaitTimeout: 10 * time.Second}, func() (Conn, error) {
			drv, err := driver.Open(address, "admin", "password")
			if err == nil {
				mu.Lock()
				natives = append(natives, drv)
				mu.Unlock()
			}
			return &driverAdapter{drv: drv}, err
		})
		if err != nil {
			b.Fatal(err)
		}
		defer pool.Close()
	} else {
		sharedDriver, openErr := driver.Open(address, "admin", "password")
		if openErr != nil {
			b.Fatal(openErr)
		}
		defer sharedDriver.Close()
		natives = []*driver.Driver{sharedDriver}
		shared = &driverAdapter{drv: sharedDriver}
	}

	const query = `match $p isa person, has name $n; fetch { "name": $n };`
	results := make([]poolReadTimings, b.N)
	var firstErr error
	peakNative := 0
	var acquiring, peakAcquirers atomic.Int64
	jobs := make(chan int)
	var workers sync.WaitGroup
	b.SetBytes(int64(len(query)))
	b.ReportAllocs()
	b.ResetTimer()
	for range callers {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for i := range jobs {
				start := time.Now()
				conn := shared
				if pool != nil {
					var err error
					active := acquiring.Add(1)
					for {
						peak := peakAcquirers.Load()
						if active <= peak || peakAcquirers.CompareAndSwap(peak, active) {
							break
						}
					}
					conn, err = pool.Get(context.Background())
					acquiring.Add(-1)
					if err != nil {
						mu.Lock()
						firstErr = err
						mu.Unlock()
						continue
					}
				}
				acquired := time.Now()
				tx, err := conn.Transaction(name, int(ReadTransaction))
				opened := time.Now()
				if err == nil {
					var rows []map[string]any
					rows, err = tx.Query(query)
					if err == nil && len(rows) != 25 {
						err = fmt.Errorf("got %d rows, want 25", len(rows))
					}
				}
				queried := time.Now()
				mu.Lock()
				active := 0
				for _, drv := range natives {
					active += drv.CleanupStats().NativeInUse
				}
				peakNative = max(peakNative, active)
				mu.Unlock()
				if tx != nil {
					if checked, ok := tx.(interface{ CloseChecked() error }); ok {
						if closeErr := checked.CloseChecked(); err == nil {
							err = closeErr
						}
					} else {
						tx.Close()
						if err == nil {
							err = fmt.Errorf("transaction does not support checked close")
						}
					}
				}
				if pool != nil {
					pool.Put(conn)
				}
				closed := time.Now()
				results[i] = poolReadTimings{closed.Sub(start), acquired.Sub(start), opened.Sub(acquired), queried.Sub(opened), closed.Sub(queried)}
				mu.Lock()
				if err != nil && firstErr == nil {
					firstErr = err
				}
				mu.Unlock()
			}
		}()
	}
	for i := range b.N {
		jobs <- i
	}
	close(jobs)
	workers.Wait()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	err = driver.WaitForPendingCloses(ctx)
	cancel()
	b.StopTimer()
	if err != nil || firstErr != nil {
		b.Fatalf("drain: %v; read: %v", err, firstErr)
	}
	for _, drv := range natives {
		if stats := drv.CleanupStats(); stats.NativeInUse != 0 || stats.Pending != 0 {
			b.Fatalf("native cleanup not drained: %+v", stats)
		}
	}
	sort.Slice(results, func(i, j int) bool { return results[i].latency < results[j].latency })
	metric := func(value time.Duration, name string) { b.ReportMetric(float64(value)/float64(b.N), name) }
	var total poolReadTimings
	for _, sample := range results {
		total.acquire += sample.acquire
		total.open += sample.open
		total.query += sample.query
		total.close += sample.close
	}
	metric(total.acquire, "acquire-ns/op")
	metric(total.open, "open-ns/op")
	metric(total.query, "query-ns/op")
	metric(total.close, "close-ns/op")
	for _, p := range []int{50, 95, 99} {
		b.ReportMetric(float64(results[(len(results)-1)*p/100].latency)/float64(time.Millisecond), fmt.Sprintf("p%d-ms", p))
	}
	b.ReportMetric(float64(callers), "callers")
	b.ReportMetric(float64(poolSize), "pool-size")
	b.ReportMetric(float64(peakAcquirers.Load()), "peak-acquirers")
	b.ReportMetric(float64(peakNative), "peak-native")
	b.ReportMetric(1, "queries/op")
	b.ReportMetric(0, "timeouts/op")
}
