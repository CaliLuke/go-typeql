//go:build cgo && typedb

package driver

import (
	"sync"
	"testing"
	"time"
	"unsafe"
)

func TestCleanupStatsConcurrentAccounting(t *testing.T) {
	tracker := newTransactionCleanupTracker()
	other := newTransactionCleanupTracker()
	const jobs = 200
	var wg sync.WaitGroup
	for i := range jobs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			job := transactionCloseJob{ptr: unsafe.Pointer(new(byte))}
			tracker.begin(&job)
			if i%2 == 0 {
				tracker.enqueued()
				job.enqueued = time.Now().Add(-time.Millisecond)
				tracker.dequeued(job)
				tracker.finish(job, true, nil, 2*time.Millisecond, false)
			} else {
				tracker.finish(job, false, nil, 0, true)
			}
		}()
	}
	wg.Wait()
	stats := (&Driver{cleanup: tracker}).CleanupStats()
	if stats.Pending != 0 || stats.Queued != 0 || stats.NativeCompletions != jobs/2 || stats.NativeFailures != 0 || stats.QueueFullFallbacks != jobs/2 {
		t.Fatalf("inconsistent cleanup counts: %+v", stats)
	}
	if stats.QueueWaitTotal < jobs/2*time.Millisecond || stats.NativeCloseTotal != jobs*time.Millisecond {
		t.Fatalf("queue and native durations not tracked separately: %+v", stats)
	}
	if got := (&Driver{cleanup: other}).CleanupStats(); got != (TransactionCleanupStats{}) {
		t.Fatalf("driver statistics leaked across owners: %+v", got)
	}
	job := transactionCloseJob{ptr: unsafe.Pointer(new(byte))}
	tracker.begin(&job)
	tracker.mu.Lock()
	tracker.started[job.cleanupID] = time.Now().Add(-time.Second)
	tracker.mu.Unlock()
	if age := tracker.snapshot().OldestPendingAge; age < time.Second {
		t.Fatalf("oldest pending age = %s", age)
	}
	tracker.finish(job, true, errCleanupTest{}, time.Millisecond, false)
	if stats := tracker.snapshot(); stats.Pending != 0 || stats.NativeFailures != 1 || stats.OldestPendingAge != 0 {
		t.Fatalf("failed completion not accounted for: %+v", stats)
	}
}

type errCleanupTest struct{}

func (errCleanupTest) Error() string { return "close failed" }

func BenchmarkCleanupStatsDisabledLogging(b *testing.B) {
	b.Setenv("TYPEDB_GO_DEBUG", "0")
	tracker := newTransactionCleanupTracker()
	b.ReportAllocs()
	for range b.N {
		job := transactionCloseJob{ptr: unsafe.Pointer(new(byte))}
		tracker.begin(&job)
		tracker.enqueued()
		job.enqueued = time.Now()
		tracker.dequeued(job)
		tracker.finish(job, true, nil, time.Microsecond, false)
	}
}
