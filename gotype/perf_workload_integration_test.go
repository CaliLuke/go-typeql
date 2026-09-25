//go:build cgo && typedb && integration

package gotype_test

import (
	"context"
	"fmt"
	"os"
	"slices"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/CaliLuke/go-typeql/v3/driver"
	"github.com/CaliLuke/go-typeql/v3/gotype"
	"github.com/CaliLuke/go-typeql/v3/internal/perftrace"
)

// TestPerfWorkload_ConcurrentMixed is a workload for performance tracing, not
// a benchmark. It runs only with TYPEDB_GO_PERF_WORKLOAD=1 (see `make
// perf-workload` and docs/PERFORMANCE_TRACING.md). Workers share one driver
// and run a mix of reads and inserts through unbound managers, so each
// operation opens and closes its own transaction. The test logs latency
// percentiles and the time to drain the asynchronous closes.
//
// PERF_WORKERS (default 10), PERF_OPS (default 1000), and PERF_WRITE_PCT
// (default 20) change the load. PERF_CLOSE_WORKERS sets
// driver.DriverOptions.CloseWorkers and PERF_MAX_NATIVE sets
// driver.DriverOptions.MaxNativeTransactions (default 0 for both, the driver
// defaults). PERF_DROP_READ_CLOSE=1 sets driver.DriverOptions.DropReadClose.
func TestPerfWorkload_ConcurrentMixed(t *testing.T) {
	if os.Getenv("TYPEDB_GO_PERF_WORKLOAD") != "1" {
		t.Skip("set TYPEDB_GO_PERF_WORKLOAD=1 to run the performance workload")
	}
	workers := envInt(t, "PERF_WORKERS", 10)
	ops := envInt(t, "PERF_OPS", 1000)
	writePct := envInt(t, "PERF_WRITE_PCT", 20)
	closeWorkers := envInt(t, "PERF_CLOSE_WORKERS", 0)
	maxNative := envInt(t, "PERF_MAX_NATIVE", 0)
	dropReadClose := os.Getenv("PERF_DROP_READ_CLOSE") == "1"
	if workers == 0 || ops == 0 {
		t.Fatal("PERF_WORKERS and PERF_OPS must be positive")
	}

	// The workload uses its own driver so that PERF_CLOSE_WORKERS applies.
	base := setupTestDBDefault(t)
	drv, err := driver.OpenWithOptions(dbAddress(), "admin", "password", driver.DriverOptions{CloseWorkers: closeWorkers, MaxNativeTransactions: maxNative, DropReadClose: dropReadClose})
	if err != nil {
		t.Fatalf("open workload driver: %v", err)
	}
	t.Cleanup(drv.Close)
	db := gotype.NewDatabase(&driverAdapter{drv: drv}, base.Name())
	ctx := context.Background()
	mgr, err := gotype.NewManager[Person](db)
	if err != nil {
		t.Fatal(err)
	}
	seeded := seedPersons(t, ctx, mgr)
	iids := make([]string, len(seeded))
	for i, p := range seeded {
		iids[i] = p.GetIID()
	}

	var next atomic.Int64
	var failures atomic.Int64
	latencies := make([][]time.Duration, workers)
	start := time.Now()
	var wg sync.WaitGroup
	for w := range workers {
		wg.Go(func() {
			for {
				i := next.Add(1) - 1
				if i >= int64(ops) {
					return
				}
				opStart := time.Now()
				var err error
				if int(i%100) < writePct {
					err = mgr.Insert(ctx, &Person{Name: fmt.Sprintf("perf-%d", i), Email: fmt.Sprintf("perf-%d@example.com", i)})
				} else {
					_, err = mgr.GetByIID(ctx, iids[int(i)%len(iids)])
				}
				latencies[w] = append(latencies[w], time.Since(opStart))
				if err != nil {
					failures.Add(1)
					t.Errorf("op %d: %v", i, err)
				}
			}
		})
	}
	wg.Wait()
	loop := time.Since(start)

	_, drainSpan := perftrace.Start(ctx, "perf.drain_closes")
	drainStart := time.Now()
	if err := driver.WaitForPendingCloses(ctx); err != nil {
		t.Fatalf("drain closes: %v", err)
	}
	drain := time.Since(drainStart)
	drainSpan.End(nil)

	all := slices.Concat(latencies...)
	slices.Sort(all)
	pct := func(p float64) time.Duration { return all[int(p*float64(len(all)-1))] }
	t.Logf("workers=%d ops=%d write_pct=%d close_workers=%d max_native=%d drop_read_close=%v failures=%d", workers, ops, writePct, closeWorkers, maxNative, dropReadClose, failures.Load())
	t.Logf("loop=%v throughput=%.0f ops/s drain=%v", loop.Round(time.Millisecond), float64(ops)/loop.Seconds(), drain.Round(time.Millisecond))
	t.Logf("latency p50=%v p95=%v p99=%v max=%v", pct(0.50).Round(time.Microsecond), pct(0.95).Round(time.Microsecond), pct(0.99).Round(time.Microsecond), all[len(all)-1].Round(time.Microsecond))
}

func envInt(t *testing.T, name string, fallback int) int {
	t.Helper()
	v := os.Getenv(name)
	if v == "" {
		return fallback
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 0 {
		t.Fatalf("%s=%q: want a non-negative integer", name, v)
	}
	return n
}
