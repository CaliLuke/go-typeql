//go:build cgo && typedb && integration

package driver

import (
	"errors"
	"fmt"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"
)

type streamBenchShape struct {
	name  string
	rows  int
	width int
	query string
}

// BenchmarkLiveStreamTuning varies the FFI row limit independently from the
// server's prefetch option. These are private benchmark controls, not API
// defaults. Each shape has one fixture per fresh benchmark process.
func BenchmarkLiveStreamTuning(b *testing.B) {
	shapes := []streamBenchShape{
		{"narrow-64", 64, 16, `match $p isa person, has name $n; fetch { "name": $n };`},
		{"wide-64", 64, 512, `match $p isa person, has name $n; fetch { "name": $n };`},
		{"nested-256", 256, 16, `match $p isa person, has name $n; fetch { "person": { "name": $n } };`},
		{"large-512", 512, 512, `match $p isa person, has name $n; fetch { "name": $n };`},
	}
	for _, shape := range shapes {
		b.Run(shape.name, func(b *testing.B) {
			conn, dbName := setupStreamBenchFixture(b, shape)
			for _, chunk := range []int{32, 128, 256} {
				for _, prefetch := range []struct {
					name string
					size int64
				}{{"default", -1}, {"prefetch-1", 1}, {"prefetch-256", 256}} {
					b.Run(fmt.Sprintf("chunk=%d/%s", chunk, prefetch.name), func(b *testing.B) {
						benchmarkStreamCase(b, conn, dbName, shape, chunk, prefetch.size, false)
					})
				}
				if shape.name == "large-512" {
					b.Run(fmt.Sprintf("chunk=%d/stop-first", chunk), func(b *testing.B) {
						benchmarkStreamCase(b, conn, dbName, shape, chunk, -1, true)
					})
				}
			}
		})
	}
}

var errStreamBenchStop = errors.New("stop after first row")

func benchmarkStreamCase(b *testing.B, conn *Driver, dbName string, shape streamBenchShape, chunk int, prefetch int64, stopFirst bool) {
	var opts *QueryOptions
	if prefetch >= 0 {
		opts = NewQueryOptions().SetPrefetchSize(prefetch)
		b.Cleanup(opts.Close)
	}
	var firstTotal time.Duration
	peakChunkBytes, peakChunkRows := 0, 0
	var encodedBytes int64
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		tx, err := conn.Transaction(dbName, Read)
		if err != nil {
			b.Fatal(err)
		}
		start := time.Now()
		seen := 0
		err = tx.queryEachConfigured(&queryMetadata{query: shape.query}, opts, chunk, func(rows, bytes int) {
			peakChunkRows = max(peakChunkRows, rows)
			peakChunkBytes = max(peakChunkBytes, bytes)
			encodedBytes += int64(bytes)
		}, func(_ int, _ map[string]any) error {
			if seen == 0 {
				firstTotal += time.Since(start)
			}
			seen++
			if stopFirst {
				return errStreamBenchStop
			}
			return nil
		})
		closeErr := tx.CloseChecked()
		if stopFirst {
			if err == nil || !strings.Contains(err.Error(), errStreamBenchStop.Error()) || seen != 1 {
				b.Fatalf("stop after first: seen=%d err=%v", seen, err)
			}
		} else if err != nil || seen != shape.rows {
			b.Fatalf("stream: seen=%d want=%d err=%v", seen, shape.rows, err)
		}
		if closeErr != nil {
			b.Fatal(closeErr)
		}
	}
	b.StopTimer()
	// The benchmark throughput is based on encoded FFI bytes actually received,
	// including envelopes, not the nominal name payload or a row-count cap.
	b.SetBytes(encodedBytes / int64(b.N))
	b.ReportMetric(float64(firstTotal.Nanoseconds())/float64(b.N), "first-row-ns/op")
	b.ReportMetric(float64(encodedBytes)/float64(b.N), "ffi-bytes/op")
	b.ReportMetric(float64(peakChunkBytes), "peak-chunk-bytes")
	b.ReportMetric(float64(peakChunkRows), "peak-chunk-rows")
	b.ReportMetric(1, "queries/op")
	b.ReportMetric(float64(shape.rows), "fixture-rows")
	b.ReportMetric(float64(shape.width), "name-bytes/row")
	if stopFirst {
		b.ReportMetric(1, "consumed-rows/op")
	} else {
		b.ReportMetric(float64(shape.rows), "consumed-rows/op")
	}
	// Sample heap usage during a separate untimed replay; ReadMemStats inside
	// the timed callback would dominate the small-chunk cases.
	peakHeap := sampleStreamHeap(b, conn, dbName, shape, opts, chunk, stopFirst)
	b.ReportMetric(float64(peakHeap), "sampled-peak-go-heap-inuse-bytes")
	var usage syscall.Rusage
	if syscall.Getrusage(syscall.RUSAGE_SELF, &usage) == nil {
		rss := float64(usage.Maxrss)
		if runtime.GOOS == "linux" {
			rss *= 1024
		}
		b.ReportMetric(rss, "process-peak-rss-bytes")
	}
}

func sampleStreamHeap(b *testing.B, conn *Driver, dbName string, shape streamBenchShape, opts *QueryOptions, chunk int, stopFirst bool) uint64 {
	b.Helper()
	tx, err := conn.Transaction(dbName, Read)
	if err != nil {
		b.Fatal(err)
	}
	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)
	peak := mem.HeapInuse
	err = tx.queryEachConfigured(&queryMetadata{query: shape.query}, opts, chunk, nil, func(_ int, _ map[string]any) error {
		runtime.ReadMemStats(&mem)
		peak = max(peak, mem.HeapInuse)
		if stopFirst {
			return errStreamBenchStop
		}
		return nil
	})
	closeErr := tx.CloseChecked()
	if stopFirst {
		if err == nil || !strings.Contains(err.Error(), errStreamBenchStop.Error()) {
			b.Fatalf("untimed early stop: %v", err)
		}
	} else if err != nil {
		b.Fatal(err)
	}
	if closeErr != nil {
		b.Fatal(closeErr)
	}
	return peak
}

func setupStreamBenchFixture(b *testing.B, shape streamBenchShape) (*Driver, string) {
	b.Helper()
	conn, err := OpenWithTLS(testAddr(), "admin", "password", false, "")
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(conn.Close)
	name := fmt.Sprintf("stream_tuning_%d", time.Now().UnixNano())
	if err := conn.Databases().Create(name); err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = conn.Databases().Delete(name) })
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
	for i := range shape.rows {
		fmt.Fprintf(&inserts, "$p%d isa person, has name \"%04d%s\";\n", i, i, strings.Repeat("x", shape.width-4))
	}
	if _, err := write.Query(inserts.String()); err != nil {
		write.Close()
		b.Fatal(err)
	}
	if err := write.Commit(); err != nil {
		b.Fatal(err)
	}
	return conn, name
}
