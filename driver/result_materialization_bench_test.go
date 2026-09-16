//go:build cgo && typedb && integration

package driver

import (
	"context"
	"fmt"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"
)

// BenchmarkLiveResultMaterialization measures the Query path after it has
// returned a fully materialized Go result slice. It is not a streaming or
// time-to-first-row benchmark.
func BenchmarkLiveResultMaterialization(b *testing.B) {
	for _, size := range []int{64, 256} {
		for _, width := range []int{16, 512} {
			b.Run(fmt.Sprintf("rows=%d/name-bytes=%d", size, width), func(b *testing.B) {
				conn, err := OpenWithTLS(testAddr(), "admin", "password", false, "")
				if err != nil {
					b.Fatal(err)
				}
				b.Cleanup(conn.Close)
				name := fmt.Sprintf("materialization_%d", time.Now().UnixNano())
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
				for i := range size {
					fmt.Fprintf(&inserts, "$p%d isa person, has name \"%04d%s\";\n", i, i, strings.Repeat("x", width-4))
				}
				if _, err := write.Query(inserts.String()); err != nil {
					write.Close()
					b.Fatal(err)
				}
				if err := write.Commit(); err != nil {
					b.Fatal(err)
				}
				const query = `match $p isa person, has name $n; fetch { "name": $n };`
				b.SetBytes(int64(size * width))
				b.ReportAllocs()
				b.ResetTimer()
				for range b.N {
					tx, err := conn.Transaction(name, Read)
					if err != nil {
						b.Fatal(err)
					}
					rows, err := tx.Query(query)
					tx.Close()
					if err != nil || len(rows) != size {
						b.Fatalf("query: %v; got %d rows, want %d", err, len(rows), size)
					}
				}
				b.StopTimer()
				b.ReportMetric(1, "queries/op")
				b.ReportMetric(float64(size), "rows/op")
				b.ReportMetric(float64(len(query)), "query-bytes")
				var mem runtime.MemStats
				runtime.ReadMemStats(&mem)
				b.ReportMetric(float64(mem.HeapInuse), "go-heap-inuse-bytes")
				var usage syscall.Rusage
				if syscall.Getrusage(syscall.RUSAGE_SELF, &usage) == nil {
					rss := float64(usage.Maxrss)
					if runtime.GOOS == "linux" {
						rss *= 1024
					}
					b.ReportMetric(rss, "process-peak-rss-bytes")
				}
			})
		}
	}
}

// BenchmarkLiveResultStreaming measures the chunked QueryEachWithContext path
// and its time to the first callback for 512 rows of 512-byte names. The
// current 256-row chunk size makes this a two-chunk stream.
func BenchmarkLiveResultStreaming(b *testing.B) {
	const size, width = 512, 512
	conn, err := OpenWithTLS(testAddr(), "admin", "password", false, "")
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(conn.Close)
	name := fmt.Sprintf("streaming_%d", time.Now().UnixNano())
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
	for i := range size {
		fmt.Fprintf(&inserts, "$p%d isa person, has name \"%04d%s\";\n", i, i, strings.Repeat("x", width-4))
	}
	if _, err := write.Query(inserts.String()); err != nil {
		write.Close()
		b.Fatal(err)
	}
	if err := write.Commit(); err != nil {
		b.Fatal(err)
	}
	const query = `match $p isa person, has name $n; fetch { "name": $n };`
	var firstTotal time.Duration
	b.SetBytes(size * width)
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		tx, err := conn.Transaction(name, Read)
		if err != nil {
			b.Fatal(err)
		}
		started := time.Now()
		seen := 0
		err = tx.QueryEachWithContext(context.Background(), query, func(_ int, _ map[string]any) error {
			if seen == 0 {
				firstTotal += time.Since(started)
			}
			seen++
			return nil
		})
		tx.Close()
		if err != nil || seen != size {
			b.Fatalf("stream query: %v; got %d rows, want %d", err, seen, size)
		}
	}
	b.StopTimer()
	b.ReportMetric(float64(firstTotal.Nanoseconds())/float64(b.N), "first-row-ns/op")
	b.ReportMetric(1, "queries/op")
	b.ReportMetric(size, "rows/op")
	b.ReportMetric(float64(len(query)), "query-bytes")
}
