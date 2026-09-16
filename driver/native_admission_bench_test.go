//go:build cgo && typedb && integration

package driver

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"testing"
	"time"
)

// BenchmarkNativeClosePolicies measures completed reads (including close), not
// the time for Close to enqueue. Run with -benchtime=100x -count=5 against a
// disposable TypeDB 3.13 server. Go B/op excludes Rust and server memory.
func BenchmarkNativeClosePolicies(b *testing.B) {
	const callers = 10
	const query = `match attribute $a; fetch { "label": label($a) };`
	for _, policy := range []struct {
		name    string
		limit   int
		checked bool
	}{
		{"async-unbounded", -1, false},
		{"async-bounded-16", 16, false},
		{"checked", -1, true},
	} {
		b.Run(policy.name, func(b *testing.B) {
			conn, err := OpenWithOptions(closeBacklogTestAddr(), "admin", "password", DriverOptions{MaxNativeTransactions: policy.limit})
			if err != nil {
				b.Fatal(err)
			}
			defer conn.Close()
			name := fmt.Sprintf("native_policy_%d", time.Now().UnixNano())
			if err := conn.Databases().Create(name); err != nil {
				b.Fatal(err)
			}
			defer conn.Databases().Delete(name)
			schema, err := conn.Transaction(name, Schema)
			if err != nil {
				b.Fatal(err)
			}
			if _, err := schema.Query("define attribute name, value string;"); err != nil {
				schema.Close()
				b.Fatal(err)
			}
			if err := schema.Commit(); err != nil {
				b.Fatal(err)
			}

			var mu sync.Mutex
			latencies := make([]time.Duration, 0, b.N)
			var firstErr error
			peakPending := 0
			peakNative := 0
			record := func(start time.Time, err error) {
				mu.Lock()
				defer mu.Unlock()
				if err != nil && firstErr == nil {
					firstErr = err
				}
				latencies = append(latencies, time.Since(start))
			}
			jobs := make(chan struct{})
			var workers, callbacks sync.WaitGroup
			b.SetBytes(int64(len(query)))
			b.ReportAllocs()
			b.ResetTimer()
			for range callers {
				workers.Add(1)
				go func() {
					defer workers.Done()
					for range jobs {
						start := time.Now()
						tx, err := conn.Transaction(name, Read)
						if err != nil {
							record(start, err)
							continue
						}
						if _, err := tx.Query(query); err != nil {
							tx.Close()
							record(start, err)
							continue
						}
						if policy.checked {
							record(start, tx.CloseChecked())
						} else {
							callbacks.Add(1)
							tx.CloseAsync(func(err error) {
								record(start, err)
								callbacks.Done()
							})
						}
						stats := conn.CleanupStats()
						mu.Lock()
						peakPending = max(peakPending, stats.Pending)
						peakNative = max(peakNative, stats.NativeInUse)
						mu.Unlock()
					}
				}()
			}
			for range b.N {
				jobs <- struct{}{}
			}
			close(jobs)
			workers.Wait()
			drainStart := time.Now()
			callbacks.Wait()
			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			err = WaitForPendingCloses(ctx)
			cancel()
			if err != nil {
				b.Fatal(err)
			}
			drain := time.Since(drainStart)
			b.StopTimer()
			if firstErr != nil {
				b.Fatal(firstErr)
			}
			if len(latencies) != b.N {
				b.Fatalf("completed %d of %d reads", len(latencies), b.N)
			}
			sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })
			percentile := func(p int) float64 { return float64(latencies[(len(latencies)-1)*p/100]) / float64(time.Millisecond) }
			stats := conn.CleanupStats()
			b.ReportMetric(percentile(50), "p50-ms")
			b.ReportMetric(percentile(95), "p95-ms")
			b.ReportMetric(percentile(99), "p99-ms")
			b.ReportMetric(float64(drain)/float64(time.Millisecond), "drain-ms")
			b.ReportMetric(float64(peakPending), "peak-pending")
			b.ReportMetric(float64(peakNative), "peak-native")
			b.ReportMetric(float64(stats.QueueWaitTotal)/float64(time.Millisecond), "queue-wait-ms")
			b.ReportMetric(float64(stats.NativeCloseTotal)/float64(time.Millisecond), "native-close-ms")
		})
	}
}
