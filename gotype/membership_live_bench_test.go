//go:build cgo && typedb && integration

package gotype

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"

	"github.com/CaliLuke/go-typeql/given"
)

// BenchmarkLiveMembershipStrategies compares expanded In query text with
// typed string input rows. It does not use opaque concept handles or IIDs.
func BenchmarkLiveMembershipStrategies(b *testing.B) {
	f := liveBenchSetup(b)
	for _, size := range []int{1, 8, 25, 64, 256} {
		values, rows := membershipInputs(size)
		expanded := membershipExpandedQuery(values)
		payload, err := rows.MarshalGivenRows()
		if err != nil {
			b.Fatal(err)
		}
		for _, strategy := range []string{"expanded", "typed-rows"} {
			b.Run(fmt.Sprintf("values=%d/%s", size, strategy), func(b *testing.B) {
				b.ReportAllocs()
				cpuBefore := benchmarkServerCPU(b)
				b.ResetTimer()
				for range b.N {
					tx, err := f.db.GetConn().Transaction(f.dbName, int(ReadTransaction))
					if err != nil {
						b.Fatal(err)
					}
					var results []map[string]any
					if strategy == "expanded" {
						results, err = tx.QueryWithContext(context.Background(), expanded)
					} else {
						results, err = tx.(batchInsertTx).QueryWithGivenRows(context.Background(), membershipGivenQuery, rows)
					}
					closeErr := tx.(interface{ CloseChecked() error }).CloseChecked()
					if err != nil || closeErr != nil || len(results) != min(size, 25) {
						b.Fatalf("membership %s/%d: query=%v close=%v rows=%d", strategy, size, err, closeErr, len(results))
					}
				}
				b.StopTimer()
				cpuAfter := benchmarkServerCPU(b)
				if cpuAfter >= cpuBefore && os.Getenv("TYPEDB_BENCH_CONTAINER") != "" {
					b.ReportMetric(float64(cpuAfter-cpuBefore)/float64(b.N)/1000, "server-cpu-ms/op")
				}
				queryBytes := len(expanded)
				inputBytes := 0
				if strategy == "typed-rows" {
					queryBytes = len(membershipGivenQuery)
					inputBytes = len(payload)
				}
				b.ReportMetric(float64(queryBytes), "query-bytes")
				b.ReportMetric(float64(inputBytes), "input-bytes")
				b.ReportMetric(float64(size), "values")
				b.ReportMetric(float64(min(size, 25)), "matching-rows")
				b.ReportMetric(1, "queries/op")
				b.ReportMetric(1, "callers")
			})
		}
	}
}

// benchmarkServerCPU reads the dedicated server container's cgroup CPU time
// only when requested. It includes all activity in that container, not solely
// the query planner, and must not be enabled against a shared server.
func benchmarkServerCPU(b *testing.B) uint64 {
	b.Helper()
	container := os.Getenv("TYPEDB_BENCH_CONTAINER")
	if container == "" {
		return 0
	}
	output, err := exec.Command("docker", "exec", container, "cat", "/sys/fs/cgroup/cpu.stat").Output()
	if err != nil {
		b.Fatalf("read server CPU from %s: %v", container, err)
	}
	for _, line := range strings.Split(string(output), "\n") {
		if value, ok := strings.CutPrefix(line, "usage_usec "); ok {
			usec, err := strconv.ParseUint(value, 10, 64)
			if err != nil {
				b.Fatalf("parse server CPU from %s: %v", container, err)
			}
			return usec
		}
	}
	b.Fatalf("server CPU usage_usec unavailable in %s", container)
	return 0
}

func TestIntegration_MembershipTypedRowsDuplicateSemantics(t *testing.T) {
	f := liveBenchSetup(t)
	values := []any{"person-00", "person-00"}
	rows := membershipInputsForNames("person-00", "person-00")
	for _, tc := range []struct {
		name  string
		query string
		rows  bool
		want  int
	}{
		{"expanded", membershipExpandedQuery(values), false, 1},
		{"typed", membershipGivenQuery, true, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tx, err := f.db.GetConn().Transaction(f.dbName, int(ReadTransaction))
			if err != nil {
				t.Fatal(err)
			}
			var result []map[string]any
			if tc.rows {
				result, err = tx.(batchInsertTx).QueryWithGivenRows(context.Background(), tc.query, rows)
			} else {
				result, err = tx.QueryWithContext(context.Background(), tc.query)
			}
			closeErr := tx.(interface{ CloseChecked() error }).CloseChecked()
			if err != nil || closeErr != nil {
				t.Fatalf("query=%v close=%v", err, closeErr)
			}
			if len(result) != tc.want {
				t.Fatalf("duplicate input returned %d rows, want %d", len(result), tc.want)
			}
		})
	}
}

func membershipInputsForNames(names ...string) *given.TypedRows {
	rows := given.NewRows("n")
	for _, name := range names {
		if err := rows.Add(given.Value{Type: "string", Value: name}); err != nil {
			panic(err)
		}
	}
	return rows
}
