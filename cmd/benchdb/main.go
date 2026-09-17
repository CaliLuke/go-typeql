package main

import (
	"bufio"
	"context"
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

var benchmarkLineRE = regexp.MustCompile(`^(Benchmark\S+?)(?:-\d+)?\s+(\d+)\s+(.+)$`)

type benchmarkResult struct {
	Package     string
	Name        string
	Samples     int64
	Iterations  int64
	NsPerOp     float64
	BPerOp      float64
	AllocsPerOp float64
	Metrics     map[string]float64
}

type runRecord struct {
	ID         int64
	StartedAt  time.Time
	FinishedAt time.Time
	Commit     string
	Dirty      bool
	GoVersion  string
	GoOS       string
	GoArch     string
	CPU        string
	Hostname   string
	Command    string
	Output     string
}

type groupSpec struct {
	pattern   string
	tags      string
	benchTime string
	packages  []string
	live      bool
	required  []string
}

func benchmarkGroup(name string) (groupSpec, error) {
	switch name {
	case "unit":
		return groupSpec{pattern: ".", packages: []string{"./ast/...", "./gotype/...", "./tqlgen/..."}}, nil
	case "decode":
		return groupSpec{pattern: "^BenchmarkDecodeMsgpack(EachKeys)?$", tags: "cgo,typedb", benchTime: "10x", packages: []string{"./driver/..."}, required: []string{"BenchmarkDecodeMsgpack", "BenchmarkDecodeMsgpackEachKeys"}}, nil
	case "bulk":
		return groupSpec{pattern: "^BenchmarkLive(InsertMany|PutMany|DeleteMany|UpdateMany)$", tags: "cgo,typedb,integration", benchTime: "5x", packages: []string{"./gotype/..."}, live: true, required: []string{"BenchmarkLiveInsertMany", "BenchmarkLivePutMany", "BenchmarkLiveDeleteMany", "BenchmarkLiveUpdateMany"}}, nil
	case "projections":
		return groupSpec{pattern: "^Benchmark(ProjectionConstruction|LiveFetchCache)$", tags: "cgo,typedb,integration", benchTime: "100x", packages: []string{"./gotype/..."}, live: true, required: []string{"BenchmarkProjectionConstruction", "BenchmarkLiveFetchCache"}}, nil
	case "typed-reads":
		return groupSpec{pattern: "^BenchmarkLiveRead_(GetByIID|Get|All|GetWithRoles|GetByIIDBreakdown)$", tags: "cgo,typedb,integration", benchTime: "20x", packages: []string{"./gotype/..."}, live: true, required: []string{"BenchmarkLiveRead_GetByIID", "BenchmarkLiveRead_Get", "BenchmarkLiveRead_All", "BenchmarkLiveRead_GetWithRoles", "BenchmarkLiveRead_GetByIIDBreakdown"}}, nil
	case "lifecycle":
		return groupSpec{pattern: "^BenchmarkNativeClosePolicies$", tags: "cgo,typedb,integration", benchTime: "100x", packages: []string{"./driver/..."}, live: true, required: []string{"BenchmarkNativeClosePolicies"}}, nil
	case "result-reads":
		return groupSpec{pattern: "^BenchmarkLiveResult(Materialization|Streaming)$", tags: "cgo,typedb,integration", benchTime: "10x", packages: []string{"./driver/..."}, live: true, required: []string{"BenchmarkLiveResultMaterialization", "BenchmarkLiveResultStreaming"}}, nil
	case "pool":
		return groupSpec{pattern: "^BenchmarkLivePoolRead$", tags: "cgo,typedb,integration", benchTime: "100x", packages: []string{"./gotype/..."}, live: true, required: []string{
			"BenchmarkLivePoolRead/shared/callers=1", "BenchmarkLivePoolRead/pool-1/callers=1", "BenchmarkLivePoolRead/pool-4/callers=1",
			"BenchmarkLivePoolRead/shared/callers=4", "BenchmarkLivePoolRead/pool-1/callers=4", "BenchmarkLivePoolRead/pool-4/callers=4",
			"BenchmarkLivePoolRead/shared/callers=10", "BenchmarkLivePoolRead/pool-1/callers=10", "BenchmarkLivePoolRead/pool-4/callers=10",
		}}, nil
	case "get-one":
		return groupSpec{pattern: "^BenchmarkLiveGetOneAlternatives$", tags: "cgo,typedb,integration", benchTime: "20x", packages: []string{"./gotype/..."}, live: true, required: []string{
			"BenchmarkLiveGetOneAlternatives/zero/current", "BenchmarkLiveGetOneAlternatives/zero/count-first", "BenchmarkLiveGetOneAlternatives/zero/bounded-two",
			"BenchmarkLiveGetOneAlternatives/one/current", "BenchmarkLiveGetOneAlternatives/one/count-first", "BenchmarkLiveGetOneAlternatives/one/bounded-two",
			"BenchmarkLiveGetOneAlternatives/two/current", "BenchmarkLiveGetOneAlternatives/two/count-first", "BenchmarkLiveGetOneAlternatives/two/bounded-two",
			"BenchmarkLiveGetOneAlternatives/many/current", "BenchmarkLiveGetOneAlternatives/many/count-first", "BenchmarkLiveGetOneAlternatives/many/bounded-two",
		}}, nil
	case "get-one-duplicates":
		return groupSpec{pattern: "^BenchmarkGetOneRepeatedRows$", benchTime: "100x", packages: []string{"./gotype/..."}, required: []string{
			"BenchmarkGetOneRepeatedRows/current", "BenchmarkGetOneRepeatedRows/count-first", "BenchmarkGetOneRepeatedRows/bounded-two",
		}}, nil
	case "prefetch":
		return groupSpec{pattern: "^BenchmarkLiveStreamTuning$", tags: "cgo,typedb,integration", benchTime: "10x", packages: []string{"./driver/..."}, live: true, required: streamTuningSeries()}, nil
	case "stream-latency":
		return groupSpec{pattern: "^BenchmarkLiveStreamLatency$", tags: "cgo,typedb,integration", benchTime: "2x", packages: []string{"./driver/..."}, live: true, required: streamLatencySeries()}, nil
	case "cancellation":
		return groupSpec{pattern: "^BenchmarkLiveCancellationRetention$", tags: "cgo,typedb,integration", benchTime: "4x", packages: []string{"./driver/..."}, live: true, required: []string{
			"BenchmarkLiveCancellationRetention/native-limit=1/tx-timeout=1500ms",
			"BenchmarkLiveCancellationRetention/native-limit=2/tx-timeout=1500ms",
			"BenchmarkLiveCancellationRetention/native-limit=2/tx-timeout=200ms",
		}}, nil
	case "membership-build":
		return groupSpec{pattern: "^Benchmark(MembershipConstruction|IIDMembershipConstruction)$", benchTime: "100x", packages: []string{"./gotype/..."}, required: membershipBuildSeries()}, nil
	case "membership-live":
		return groupSpec{pattern: "^BenchmarkLiveMembershipStrategies$", tags: "cgo,typedb,integration", benchTime: "10x", packages: []string{"./gotype/..."}, live: true, required: membershipLiveSeries()}, nil
	default:
		return groupSpec{}, fmt.Errorf("unknown benchmark group %q", name)
	}
}

func membershipBuildSeries() []string {
	series := make([]string, 0, 15)
	for _, size := range []int{1, 8, 25, 64, 256} {
		for _, strategy := range []string{"expanded", "typed-rows"} {
			series = append(series, fmt.Sprintf("BenchmarkMembershipConstruction/values=%d/%s", size, strategy))
		}
		series = append(series, fmt.Sprintf("BenchmarkIIDMembershipConstruction/values=%d", size))
	}
	return series
}

func membershipLiveSeries() []string {
	series := make([]string, 0, 10)
	for _, size := range []int{1, 8, 25, 64, 256} {
		for _, strategy := range []string{"expanded", "typed-rows"} {
			series = append(series, fmt.Sprintf("BenchmarkLiveMembershipStrategies/values=%d/%s", size, strategy))
		}
	}
	return series
}

func streamTuningSeries() []string {
	series := make([]string, 0, 39)
	for _, shape := range []string{"narrow-64", "wide-64", "nested-256", "large-512"} {
		for _, chunk := range []int{32, 128, 256} {
			for _, prefetch := range []string{"default", "prefetch-1", "prefetch-256"} {
				series = append(series, fmt.Sprintf("BenchmarkLiveStreamTuning/%s/chunk=%d/%s", shape, chunk, prefetch))
			}
			if shape == "large-512" {
				series = append(series, fmt.Sprintf("BenchmarkLiveStreamTuning/%s/chunk=%d/stop-first", shape, chunk))
			}
		}
	}
	return series
}

func streamLatencySeries() []string {
	series := make([]string, 0, 24)
	for _, shape := range []string{"narrow-64", "large-512"} {
		for _, delay := range []int{0, 2} {
			for _, chunk := range []int{32, 256} {
				for _, prefetch := range []string{"default", "prefetch-1", "prefetch-256"} {
					series = append(series, fmt.Sprintf("BenchmarkLiveStreamLatency/%s/delay=%dms/chunk=%d/%s", shape, delay, chunk, prefetch))
				}
			}
		}
	}
	return series
}

func main() {
	if err := run(context.Background(), os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "benchdb: %v\n", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("benchdb", flag.ContinueOnError)
	dbPath := fs.String("db", "", "record reviewed results in this sqlite database (omit for exploratory stdout only)")
	count := fs.Int("count", 5, "benchmark sample count")
	group := fs.String("group", "unit", "benchmark group: unit, decode, bulk, projections, typed-reads, lifecycle, result-reads, pool, get-one, get-one-duplicates, prefetch, stream-latency, cancellation, membership-build, membership-live")
	bench := fs.String("bench", "", "override the group's benchmark regex")
	benchTime := fs.String("benchtime", "", "override the group's benchmark duration or fixed iterations")
	reset := fs.Bool("reset", false, "clear existing benchmark history before saving the new run")
	runPattern := fs.String("run", "^$", "test regex passed to go test -run")
	timeout := fs.Duration("timeout", 10*time.Minute, "go test timeout")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *reset && *dbPath == "" {
		return errors.New("-reset requires an explicit -db path")
	}
	if *count <= 0 {
		return errors.New("-count must be positive")
	}

	spec, err := benchmarkGroup(*group)
	if err != nil {
		return err
	}
	if *bench != "" {
		spec.pattern = *bench
	}
	if *benchTime != "" {
		spec.benchTime = *benchTime
	}
	goCount := *count
	if spec.live {
		goCount = 1
	}
	benchArgs := []string{
		"test",
		"-run", *runPattern,
		"-bench", spec.pattern,
		"-benchmem",
		"-count", strconv.Itoa(goCount),
		"-timeout", timeout.String(),
	}
	if spec.tags != "" {
		benchArgs = append(benchArgs, "-tags", spec.tags)
	}
	if spec.benchTime != "" {
		benchArgs = append(benchArgs, "-benchtime", spec.benchTime)
	}
	benchArgs = append(benchArgs, spec.packages...)

	startedAt := time.Now().UTC()
	output, results, cpuName, err := executeBenchmarkSamples(ctx, benchArgs, *count, spec.live)
	if err != nil {
		return err
	}
	finishedAt := time.Now().UTC()
	if len(results) == 0 {
		return errors.New("no benchmark results parsed from go test output")
	}
	if *bench == "" {
		if err := validateGroupSeries(spec, results); err != nil {
			return err
		}
	}

	if *dbPath == "" {
		fmt.Print(output)
		fmt.Printf("Exploratory %s benchmark: %d series; no database changed.\n", *group, len(results))
		return nil
	}
	return saveResults(ctx, *dbPath, *reset, benchArgs, *count, spec.live, startedAt, finishedAt, output, results, cpuName)
}

func validateGroupSeries(spec groupSpec, results []benchmarkResult) error {
	present := make(map[string]bool, len(results))
	for _, result := range results {
		name, _, _ := strings.Cut(result.Name, "/")
		present[name] = true
		present[result.Name] = true
	}
	for _, name := range spec.required {
		if !present[name] {
			return fmt.Errorf("benchmark group incomplete: missing %s", name)
		}
	}
	return nil
}

func saveResults(ctx context.Context, dbPath string, reset bool, benchArgs []string, count int, live bool, startedAt, finishedAt time.Time, output string, results []benchmarkResult, cpuName string) error {
	db, err := openDB(dbPath)
	if err != nil {
		return err
	}
	defer func() {
		_ = db.Close()
	}()
	if reset {
		if err := resetHistory(ctx, db); err != nil {
			return err
		}
	}

	commit, dirty := gitState(ctx)
	hostname, _ := os.Hostname()

	record := runRecord{
		StartedAt:  startedAt,
		FinishedAt: finishedAt,
		Commit:     commit,
		Dirty:      dirty,
		GoVersion:  runtime.Version(),
		GoOS:       runtime.GOOS,
		GoArch:     runtime.GOARCH,
		CPU:        cpuName,
		Hostname:   hostname,
		Command:    fmt.Sprintf("go %s%s", strings.Join(benchArgs, " "), sampleSuffix(count, live)),
		Output:     output,
	}
	runID, err := insertRun(ctx, db, record, results)
	if err != nil {
		return err
	}

	fmt.Printf("Saved benchmark run %d to %s\n", runID, dbPath)
	fmt.Printf("Command: %s\n", record.Command)
	fmt.Printf("Commit: %s", commitOrUnknown(commit))
	if dirty {
		fmt.Print(" (dirty)")
	}
	fmt.Printf("\nBenchmarks:\n")
	for _, result := range results {
		prev, ok, err := previousBenchmark(ctx, db, runID, result.Package, result.Name)
		if err != nil {
			return err
		}
		fmt.Printf("  %-40s %10.2f ns/op %8.2f B/op %8.2f allocs/op (%dx avg)",
			result.Package+":"+result.Name,
			result.NsPerOp,
			result.BPerOp,
			result.AllocsPerOp,
			result.Samples,
		)
		if ok {
			fmt.Printf("  vs prev %s", deltaSummary(prev, result))
		}
		fmt.Println()
	}

	return nil
}

func executeBenchmarks(ctx context.Context, args []string) (string, []benchmarkResult, string, error) {
	cmd := exec.CommandContext(ctx, "go", args...)
	out, err := cmd.CombinedOutput()
	output := string(out)
	if err != nil {
		return output, nil, "", fmt.Errorf("go test benchmarks failed: %w\n%s", err, output)
	}

	results, cpuName, parseErr := parseBenchmarkOutput(output)
	if parseErr != nil {
		return output, nil, cpuName, parseErr
	}
	return output, results, cpuName, nil
}

func sampleSuffix(count int, live bool) string {
	if live {
		return fmt.Sprintf(" (repeated in %d fresh processes)", count)
	}
	return ""
}

func executeBenchmarkSamples(ctx context.Context, args []string, count int, live bool) (string, []benchmarkResult, string, error) {
	if !live {
		return executeBenchmarks(ctx, args)
	}
	var output strings.Builder
	var samples []benchmarkResult
	var baseline []benchmarkResult
	cpu := ""
	for i := range count {
		raw, results, currentCPU, err := executeBenchmarks(ctx, args)
		fmt.Fprintf(&output, "# independent sample %d/%d\n%s", i+1, count, raw)
		if err != nil {
			return output.String(), nil, cpu, fmt.Errorf("live sample %d/%d: %w", i+1, count, err)
		}
		if i > 0 {
			if err := compareSampleSeries(baseline, results); err != nil {
				return output.String(), nil, cpu, fmt.Errorf("live sample %d/%d: %w", i+1, count, err)
			}
		} else {
			baseline = results
		}
		samples = append(samples, results...)
		if cpu == "" {
			cpu = currentCPU
		} else if cpu != currentCPU {
			return output.String(), nil, cpu, fmt.Errorf("live sample %d/%d: CPU changed from %q to %q", i+1, count, cpu, currentCPU)
		}
	}
	return output.String(), aggregateBenchmarks(samples), cpu, nil
}

func compareSampleSeries(want, got []benchmarkResult) error {
	if len(want) != len(got) {
		return fmt.Errorf("benchmark series count changed from %d to %d", len(want), len(got))
	}
	byKey := make(map[string]benchmarkResult, len(want))
	for _, result := range want {
		key := result.Package + "\x00" + result.Name
		if _, exists := byKey[key]; exists {
			return fmt.Errorf("duplicate benchmark series %q", key)
		}
		byKey[key] = result
	}
	for _, result := range got {
		key := result.Package + "\x00" + result.Name
		previous, ok := byKey[key]
		if !ok {
			return fmt.Errorf("unexpected benchmark series %q", key)
		}
		delete(byKey, key)
		if len(previous.Metrics) != len(result.Metrics) {
			return fmt.Errorf("metric set changed for %q", key)
		}
		for metric := range previous.Metrics {
			if _, ok := result.Metrics[metric]; !ok {
				return fmt.Errorf("missing metric %q for %q", metric, key)
			}
		}
	}
	if len(byKey) != 0 {
		return errors.New("benchmark series missing from sample")
	}
	return nil
}

func parseBenchmarkOutput(output string) ([]benchmarkResult, string, error) {
	scanner := bufio.NewScanner(strings.NewReader(output))
	currentPkg := ""
	cpuName := ""
	results := make([]benchmarkResult, 0, 16)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		switch {
		case strings.HasPrefix(line, "pkg: "):
			currentPkg = strings.TrimPrefix(line, "pkg: ")
		case strings.HasPrefix(line, "cpu: "):
			cpuName = strings.TrimPrefix(line, "cpu: ")
		case strings.HasPrefix(line, "Benchmark"):
			match := benchmarkLineRE.FindStringSubmatch(line)
			if match == nil {
				continue
			}
			iterations, err := strconv.ParseInt(match[2], 10, 64)
			if err != nil {
				return nil, cpuName, fmt.Errorf("parse iterations for %q: %w", line, err)
			}
			fields := strings.Fields(match[3])
			if len(fields)%2 != 0 {
				return nil, cpuName, fmt.Errorf("unpaired benchmark metric in %q", line)
			}
			metrics := make(map[string]float64, len(fields)/2)
			for i := 0; i < len(fields); i += 2 {
				value, err := strconv.ParseFloat(fields[i], 64)
				if err != nil {
					return nil, cpuName, fmt.Errorf("parse benchmark metric in %q: %w", line, err)
				}
				metrics[fields[i+1]] = value
			}
			nsPerOp, ok := metrics["ns/op"]
			if !ok {
				continue
			}
			results = append(results, benchmarkResult{
				Package:     currentPkg,
				Name:        match[1],
				Samples:     1,
				Iterations:  iterations,
				NsPerOp:     nsPerOp,
				BPerOp:      metrics["B/op"],
				AllocsPerOp: metrics["allocs/op"],
				Metrics:     metrics,
			})
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, cpuName, err
	}
	return aggregateBenchmarks(results), cpuName, nil
}

func aggregateBenchmarks(results []benchmarkResult) []benchmarkResult {
	type aggregate struct {
		benchmarkResult
	}

	aggregates := make(map[string]*aggregate, len(results))
	order := make([]string, 0, len(results))
	for _, result := range results {
		key := result.Package + "\x00" + result.Name
		if agg, ok := aggregates[key]; ok {
			agg.Samples++
			agg.Iterations += result.Iterations
			agg.NsPerOp += result.NsPerOp
			agg.BPerOp += result.BPerOp
			agg.AllocsPerOp += result.AllocsPerOp
			for name, value := range result.Metrics {
				agg.Metrics[name] += value
			}
			continue
		}
		copy := result
		aggregates[key] = &aggregate{benchmarkResult: copy}
		order = append(order, key)
	}

	merged := make([]benchmarkResult, 0, len(order))
	for _, key := range order {
		agg := aggregates[key]
		samples := float64(agg.Samples)
		agg.Iterations /= agg.Samples
		agg.NsPerOp /= samples
		agg.BPerOp /= samples
		agg.AllocsPerOp /= samples
		for name, value := range agg.Metrics {
			agg.Metrics[name] = value / samples
		}
		merged = append(merged, agg.benchmarkResult)
	}
	return merged
}

func openDB(path string) (*sql.DB, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	if err := initSchema(db); err != nil {
		_ = db.Close()
		return nil, err
	}
	return db, nil
}

func initSchema(db *sql.DB) error {
	schema := `
CREATE TABLE IF NOT EXISTS benchmark_runs (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	started_at TEXT NOT NULL,
	finished_at TEXT NOT NULL,
	git_commit TEXT,
	git_dirty INTEGER NOT NULL,
	go_version TEXT NOT NULL,
	go_os TEXT NOT NULL,
	go_arch TEXT NOT NULL,
	cpu_name TEXT,
	hostname TEXT,
	command TEXT NOT NULL,
	raw_output TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS benchmark_results (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	run_id INTEGER NOT NULL REFERENCES benchmark_runs(id) ON DELETE CASCADE,
	package_name TEXT NOT NULL,
	benchmark_name TEXT NOT NULL,
	sample_count INTEGER NOT NULL,
	iterations INTEGER NOT NULL,
	ns_per_op REAL NOT NULL,
	bytes_per_op REAL NOT NULL,
	allocs_per_op REAL NOT NULL
);

CREATE INDEX IF NOT EXISTS benchmark_results_lookup_idx
ON benchmark_results (package_name, benchmark_name, run_id);

CREATE TABLE IF NOT EXISTS benchmark_metrics (
	result_id INTEGER NOT NULL REFERENCES benchmark_results(id) ON DELETE CASCADE,
	metric_name TEXT NOT NULL,
	metric_value REAL NOT NULL,
	PRIMARY KEY (result_id, metric_name)
);
`
	if _, err := db.Exec(schema); err != nil {
		return err
	}
	_, err := db.Exec(`ALTER TABLE benchmark_results ADD COLUMN sample_count INTEGER NOT NULL DEFAULT 1`)
	if err != nil && !strings.Contains(err.Error(), "duplicate column name") {
		return err
	}
	return nil
}

func resetHistory(ctx context.Context, db *sql.DB) error {
	_, err := db.ExecContext(ctx, `
DELETE FROM benchmark_metrics;
DELETE FROM benchmark_results;
DELETE FROM benchmark_runs;
DELETE FROM sqlite_sequence WHERE name IN ('benchmark_results', 'benchmark_runs');
`)
	return err
}

func insertRun(ctx context.Context, db *sql.DB, record runRecord, results []benchmarkResult) (int64, error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer func() {
		_ = tx.Rollback()
	}()

	res, err := tx.ExecContext(ctx, `
INSERT INTO benchmark_runs (
	started_at, finished_at, git_commit, git_dirty, go_version, go_os, go_arch,
	cpu_name, hostname, command, raw_output
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		record.StartedAt.Format(time.RFC3339Nano),
		record.FinishedAt.Format(time.RFC3339Nano),
		record.Commit,
		boolToInt(record.Dirty),
		record.GoVersion,
		record.GoOS,
		record.GoArch,
		record.CPU,
		record.Hostname,
		record.Command,
		record.Output,
	)
	if err != nil {
		return 0, err
	}
	runID, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}

	stmt, err := tx.PrepareContext(ctx, `
INSERT INTO benchmark_results (
	run_id, package_name, benchmark_name, sample_count, iterations, ns_per_op, bytes_per_op, allocs_per_op
) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return 0, err
	}
	defer func() {
		_ = stmt.Close()
	}()
	metricsStmt, err := tx.PrepareContext(ctx, `INSERT INTO benchmark_metrics (result_id, metric_name, metric_value) VALUES (?, ?, ?)`)
	if err != nil {
		return 0, err
	}
	defer func() { _ = metricsStmt.Close() }()

	for _, result := range results {
		inserted, err := stmt.ExecContext(
			ctx,
			runID,
			result.Package,
			result.Name,
			result.Samples,
			result.Iterations,
			result.NsPerOp,
			result.BPerOp,
			result.AllocsPerOp,
		)
		if err != nil {
			return 0, err
		}
		resultID, err := inserted.LastInsertId()
		if err != nil {
			return 0, err
		}
		names := make([]string, 0, len(result.Metrics))
		for name := range result.Metrics {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			if _, err := metricsStmt.ExecContext(ctx, resultID, name, result.Metrics[name]); err != nil {
				return 0, err
			}
		}
	}

	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return runID, nil
}

func previousBenchmark(ctx context.Context, db *sql.DB, runID int64, pkg string, name string) (benchmarkResult, bool, error) {
	row := db.QueryRowContext(ctx, `
SELECT package_name, benchmark_name, sample_count, iterations, ns_per_op, bytes_per_op, allocs_per_op
FROM benchmark_results
WHERE package_name = ? AND benchmark_name = ? AND run_id < ?
ORDER BY run_id DESC
LIMIT 1`,
		pkg, name, runID,
	)

	var result benchmarkResult
	if err := row.Scan(
		&result.Package,
		&result.Name,
		&result.Samples,
		&result.Iterations,
		&result.NsPerOp,
		&result.BPerOp,
		&result.AllocsPerOp,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return benchmarkResult{}, false, nil
		}
		return benchmarkResult{}, false, err
	}
	return result, true, nil
}

func deltaSummary(prev benchmarkResult, current benchmarkResult) string {
	return fmt.Sprintf(
		"ns/op %s, B/op %s, allocs/op %s",
		formatDelta(prev.NsPerOp, current.NsPerOp, false),
		formatDelta(prev.BPerOp, current.BPerOp, false),
		formatDelta(prev.AllocsPerOp, current.AllocsPerOp, false),
	)
}

func formatDelta(prev float64, current float64, higherIsBetter bool) string {
	if prev == 0 {
		return "n/a"
	}
	change := ((current - prev) / prev) * 100
	label := "slower"
	if change < 0 {
		label = "faster"
	}
	if higherIsBetter {
		label = "lower"
		if change > 0 {
			label = "higher"
		}
	}
	if prev == current {
		return "flat"
	}
	return fmt.Sprintf("%+.2f%% %s", change, label)
}

func gitState(ctx context.Context) (string, bool) {
	commit := strings.TrimSpace(runCommand(ctx, "git", "rev-parse", "HEAD"))
	status := strings.TrimSpace(runCommand(ctx, "git", "status", "--porcelain"))
	return commit, status != ""
}

func runCommand(ctx context.Context, name string, args ...string) string {
	cmd := exec.CommandContext(ctx, name, args...)
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return string(out)
}

func boolToInt(v bool) int {
	if v {
		return 1
	}
	return 0
}

func commitOrUnknown(commit string) string {
	if commit == "" {
		return "unknown"
	}
	return commit
}
