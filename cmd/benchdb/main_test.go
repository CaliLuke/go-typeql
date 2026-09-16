package main

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestParseBenchmarkOutputWithCustomMetrics(t *testing.T) {
	output := `pkg: github.com/CaliLuke/go-typeql/driver
cpu: Apple M4 Pro
BenchmarkNativeClosePolicies/async-bounded-16-14 100 595323 ns/op 0.08 MB/s 6.012 drain-ms 13.77 p50-ms 15.06 p99-ms 16 peak-native 15 peak-pending 694.1 queue-wait-ms 1295 B/op 22 allocs/op
BenchmarkNativeClosePolicies/async-bounded-16-14 100 594157 ns/op 0.08 MB/s 5.763 drain-ms 13.73 p50-ms 15.16 p99-ms 16 peak-native 15 peak-pending 689.7 queue-wait-ms 1311 B/op 22 allocs/op
`
	results, cpu, err := parseBenchmarkOutput(output)
	if err != nil {
		t.Fatal(err)
	}
	if cpu != "Apple M4 Pro" || len(results) != 1 {
		t.Fatalf("cpu=%q results=%+v", cpu, results)
	}
	r := results[0]
	if r.Name != "BenchmarkNativeClosePolicies/async-bounded-16" || r.Samples != 2 || r.NsPerOp != 594740 || r.Metrics["peak-native"] != 16 || r.Metrics["p99-ms"] != 15.11 || r.BPerOp != 1303 || r.AllocsPerOp != 22 {
		t.Fatalf("parsed custom metrics: %+v", r)
	}
}

func TestInsertRunPreservesMetricSeries(t *testing.T) {
	db, err := openDB(filepath.Join(t.TempDir(), "bench.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	result := benchmarkResult{Package: "driver", Name: "BenchmarkNativeClosePolicies/checked", Samples: 5, Iterations: 100, NsPerOp: 220_000, BPerOp: 1200, AllocsPerOp: 20, Metrics: map[string]float64{"ns/op": 220_000, "B/op": 1200, "allocs/op": 20, "p95-ms": 2.7, "peak-pending": 9}}
	id, err := insertRun(ctx, db, runRecord{StartedAt: time.Now(), FinishedAt: time.Now(), GoVersion: "test", GoOS: "darwin", GoArch: "arm64", Command: "go test"}, []benchmarkResult{result})
	if err != nil {
		t.Fatal(err)
	}
	var count int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM benchmark_metrics WHERE result_id IN (SELECT id FROM benchmark_results WHERE run_id = ?)`, id).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != len(result.Metrics) {
		t.Fatalf("stored %d metrics, want %d", count, len(result.Metrics))
	}
}

func TestBenchmarkGroupsAreExplicit(t *testing.T) {
	for _, name := range []string{"unit", "decode", "bulk", "projections", "typed-reads", "result-reads", "lifecycle"} {
		spec, err := benchmarkGroup(name)
		if err != nil || spec.pattern == "" || len(spec.packages) == 0 {
			t.Fatalf("group %s: %+v, %v", name, spec, err)
		}
		if name != "unit" && spec.tags == "" {
			t.Fatalf("live/driver group %s lacks explicit tags", name)
		}
	}
	if _, err := benchmarkGroup("unknown"); err == nil {
		t.Fatal("unknown group accepted")
	}
	if err := run(context.Background(), []string{"-reset"}); err == nil {
		t.Fatal("reset without explicit DB accepted")
	}
}

func TestCompareSampleSeriesRejectsPartialResults(t *testing.T) {
	base := []benchmarkResult{{Package: "driver", Name: "BenchmarkOne", Metrics: map[string]float64{"ns/op": 1, "p95-ms": 2}}}
	for _, tc := range []struct {
		name string
		got  []benchmarkResult
		fail bool
	}{
		{"same metrics", []benchmarkResult{{Package: "driver", Name: "BenchmarkOne", Metrics: map[string]float64{"p95-ms": 3, "ns/op": 4}}}, false},
		{"missing series", nil, true},
		{"extra series", append(append([]benchmarkResult{}, base...), benchmarkResult{Package: "driver", Name: "BenchmarkTwo"}), true},
		{"missing metric", []benchmarkResult{{Package: "driver", Name: "BenchmarkOne", Metrics: map[string]float64{"ns/op": 4}}}, true},
		{"different series", []benchmarkResult{{Package: "driver", Name: "BenchmarkTwo", Metrics: map[string]float64{"ns/op": 4, "p95-ms": 2}}}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := compareSampleSeries(base, tc.got)
			if (err != nil) != tc.fail {
				t.Fatalf("compareSampleSeries error = %v; fail = %v", err, tc.fail)
			}
		})
	}
}

func TestValidateGroupSeriesRejectsMissingBenchmark(t *testing.T) {
	spec, err := benchmarkGroup("bulk")
	if err != nil {
		t.Fatal(err)
	}
	results := make([]benchmarkResult, 0, len(spec.required))
	for _, name := range spec.required {
		results = append(results, benchmarkResult{Name: name + "/rows=16"})
	}
	if err := validateGroupSeries(spec, results); err != nil {
		t.Fatal(err)
	}
	if err := validateGroupSeries(spec, results[:len(results)-1]); err == nil {
		t.Fatal("missing benchmark was accepted")
	}
}
