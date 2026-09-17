//go:build cgo && typedb && integration

package driver

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"sort"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

// BenchmarkLiveCancellationRetention bounds timeout-heavy work to two callers
// and a 1.5-second server transaction timeout. It records caller release and
// the later native-handle drain separately, not just the prompt ctx return.
func BenchmarkLiveCancellationRetention(b *testing.B) {
	for _, policy := range []struct {
		limit     int
		timeoutMs int64
	}{{1, 1500}, {2, 1500}, {2, 200}} {
		b.Run(fmt.Sprintf("native-limit=%d/tx-timeout=%dms", policy.limit, policy.timeoutMs), func(b *testing.B) {
			benchmarkCancellationRetentionCase(b, policy.limit, policy.timeoutMs)
		})
	}
}

func benchmarkCancellationRetentionCase(b *testing.B, limit int, timeoutMs int64) {
	conn, err := OpenWithOptions(testAddr(), "admin", "password", DriverOptions{MaxNativeTransactions: limit})
	if err != nil {
		b.Fatal(err)
	}
	defer conn.Close()
	name := fmt.Sprintf("cancel_bench_%d", time.Now().UnixNano())
	if err := conn.Databases().Create(name); err != nil {
		b.Fatal(err)
	}
	defer conn.Databases().Delete(name)
	schema, err := conn.Transaction(name, Schema)
	if err != nil {
		b.Fatal(err)
	}
	if _, err := schema.Query("define attribute name, value string; entity person, owns name @key;"); err != nil {
		schema.Close()
		b.Fatal(err)
	}
	if err := schema.Commit(); err != nil {
		b.Fatal(err)
	}
	write, err := conn.Transaction(name, Write)
	if err != nil {
		b.Fatal(err)
	}
	var inserts strings.Builder
	inserts.WriteString("insert\n")
	for i := range 100 {
		fmt.Fprintf(&inserts, "$p%d isa person, has name \"person-%d\";\n", i, i)
	}
	if _, err := write.Query(inserts.String()); err != nil {
		write.Close()
		b.Fatal(err)
	}
	if err := write.Commit(); err != nil {
		b.Fatal(err)
	}
	ctx, drainCancel := context.WithTimeout(context.Background(), 30*time.Second)
	if err := WaitForPendingCloses(ctx); err != nil {
		drainCancel()
		b.Fatal(err)
	}
	drainCancel()
	baselineQueries := activeTxQuery.Load()
	const query = `match $a isa person; $b isa person; $c isa person; reduce $count = count;`
	opts := NewTransactionOptions().SetTimeout(timeoutMs)
	defer opts.Close()
	type observation struct {
		latency   time.Duration
		admission bool
		cancelled bool
		err       error
	}
	observations := make([]observation, b.N)
	peakNative, peakBackground := 0, int64(0)
	var mu sync.Mutex
	jobs := make(chan int)
	var workers sync.WaitGroup
	b.SetBytes(int64(len(query)))
	b.ReportAllocs()
	b.ResetTimer()
	for range 2 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for i := range jobs {
				start := time.Now()
				deadline, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
				tx, err := conn.TransactionWithContextAndOptions(deadline, name, Read, opts)
				if err != nil {
					observations[i] = observation{latency: time.Since(start), admission: errors.Is(err, context.DeadlineExceeded), err: err}
					cancel()
					continue
				}
				_, err = tx.QueryWithContext(deadline, query)
				ob := observation{latency: time.Since(start), cancelled: errors.Is(err, context.DeadlineExceeded), err: err}
				tx.Close()
				cancel()
				observations[i] = ob
				mu.Lock()
				peakNative = max(peakNative, conn.CleanupStats().NativeInUse)
				peakBackground = max(peakBackground, activeTxQuery.Load()-baselineQueries)
				mu.Unlock()
			}
		}()
	}
	for i := range b.N {
		jobs <- i
	}
	close(jobs)
	workers.Wait()
	callerDone := time.Now()
	callerNative := conn.CleanupStats().NativeInUse
	callerBackground := activeTxQuery.Load() - baselineQueries
	var callerMem runtime.MemStats
	runtime.ReadMemStats(&callerMem)
	callerRSS := processPeakRSS()
	deadline := time.Now().Add(15 * time.Second)
	for {
		native := conn.CleanupStats().NativeInUse
		background := activeTxQuery.Load() - baselineQueries
		peakNative = max(peakNative, native)
		peakBackground = max(peakBackground, background)
		if native == 0 && background <= 0 {
			break
		}
		if time.Now().After(deadline) {
			b.Fatal("abandoned native calls did not finish by timeout")
		}
		time.Sleep(10 * time.Millisecond)
	}
	drainCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	err = WaitForPendingCloses(drainCtx)
	cancel()
	b.StopTimer()
	if err != nil {
		b.Fatal(err)
	}
	stats := conn.CleanupStats()
	if stats.NativeInUse != 0 || stats.Pending != 0 {
		b.Fatalf("native handles retained after drain: %+v", stats)
	}
	var drainedMem runtime.MemStats
	runtime.ReadMemStats(&drainedMem)
	drainedRSS := processPeakRSS()
	latencies := make([]time.Duration, 0, b.N)
	queryCancels, admissionCancels := 0, 0
	for _, ob := range observations {
		latencies = append(latencies, ob.latency)
		if ob.cancelled {
			queryCancels++
		} else if ob.admission {
			admissionCancels++
		} else {
			b.Fatalf("unexpected query completion/error: %v", ob.err)
		}
	}
	if queryCancels == 0 {
		b.Fatal("no in-flight query cancellation exercised")
	}
	sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })
	// Nearest-rank p95 must select the slowest of four observations, not the
	// third-fastest (the (N-1)*p/100 shortcut understates small samples).
	p95Index := (len(latencies)*95+99)/100 - 1
	b.ReportMetric(float64(latencies[p95Index])/float64(time.Millisecond), "caller-p95-ms")
	b.ReportMetric(float64(time.Since(callerDone))/float64(time.Millisecond), "post-caller-drain-ms")
	b.ReportMetric(float64(queryCancels), "query-cancels")
	b.ReportMetric(float64(admissionCancels), "admission-cancels")
	b.ReportMetric(float64(peakNative), "peak-native")
	b.ReportMetric(float64(peakBackground), "background-queries")
	b.ReportMetric(float64(callerNative), "native-at-caller-return")
	b.ReportMetric(float64(callerBackground), "background-at-caller-return")
	b.ReportMetric(float64(limit), "native-limit")
	b.ReportMetric(float64(timeoutMs), "tx-timeout-ms")
	b.ReportMetric(2, "callers")
	b.ReportMetric(float64(callerMem.HeapInuse), "go-heap-at-caller-return-bytes")
	b.ReportMetric(float64(drainedMem.HeapInuse), "go-heap-after-drain-bytes")
	b.ReportMetric(callerRSS, "process-peak-rss-at-caller-return-bytes")
	b.ReportMetric(drainedRSS, "process-peak-rss-after-drain-bytes")
}

func processPeakRSS() float64 {
	var usage syscall.Rusage
	if syscall.Getrusage(syscall.RUSAGE_SELF, &usage) == nil {
		rss := float64(usage.Maxrss)
		if runtime.GOOS == "linux" {
			rss *= 1024
		}
		return rss
	}
	return 0
}
