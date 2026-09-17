//go:build cgo && typedb && integration

package gotype

import (
	"context"
	"runtime"
	"testing"
	"time"
)

// BenchmarkLiveTypedIteration compares full typed reads with callback reads.
// The first-model and peak-retained metrics use the same 256-row fixture.
func BenchmarkLiveTypedIteration(b *testing.B) {
	f := liveBenchSetup(b)
	registerProjectionLiveModels()
	ctx := context.Background()
	for _, mode := range []string{"all", "for-each"} {
		b.Run(mode, func(b *testing.B) {
			b.ReportAllocs()
			var firstTotal time.Duration
			b.ResetTimer()
			for b.Loop() {
				start := time.Now()
				if mode == "all" {
					models, err := f.personMgr.All(ctx)
					if err != nil || len(models) != liveBenchPersonCount {
						b.Fatalf("All: rows=%d err=%v", len(models), err)
					}
					firstTotal += time.Since(start)
					runtime.KeepAlive(models)
					continue
				}
				seen := 0
				err := f.personMgr.ForEach(ctx, nil, func(*liveBenchPerson) error {
					seen++
					if seen == 1 {
						firstTotal += time.Since(start)
					}
					return nil
				})
				if err != nil || seen != liveBenchPersonCount {
					b.Fatalf("ForEach: rows=%d err=%v", seen, err)
				}
			}
			b.StopTimer()
			b.ReportMetric(float64(firstTotal.Nanoseconds())/float64(b.N), "first-model-ns")
			baseline, peak := sampleTypedIterationRetainedHeap(b, f, mode)
			b.ReportMetric(float64(baseline), "baseline-live-heap-B")
			b.ReportMetric(float64(peak), "peak-live-heap-B")
		})
	}
}

func sampleTypedIterationRetainedHeap(b *testing.B, f *liveBenchFixture, mode string) (uint64, uint64) {
	b.Helper()
	runtime.GC()
	var stats runtime.MemStats
	runtime.ReadMemStats(&stats)
	baseline := stats.HeapAlloc
	peak := baseline
	observe := func() {
		runtime.GC()
		runtime.ReadMemStats(&stats)
		if stats.HeapAlloc > peak {
			peak = stats.HeapAlloc
		}
	}
	ctx := context.Background()
	if mode == "all" {
		models, err := f.personMgr.All(ctx)
		if err != nil || len(models) != liveBenchPersonCount {
			b.Fatalf("All heap sample: rows=%d err=%v", len(models), err)
		}
		observe()
		runtime.KeepAlive(models)
	} else {
		seen := 0
		err := f.personMgr.ForEach(ctx, nil, func(*liveBenchPerson) error {
			seen++
			if seen%32 == 0 {
				observe()
			}
			return nil
		})
		if err != nil || seen != liveBenchPersonCount {
			b.Fatalf("ForEach heap sample: rows=%d err=%v", seen, err)
		}
	}
	return baseline, peak
}
