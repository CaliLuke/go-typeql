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
	"strconv"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

var benchmarkLineRE = regexp.MustCompile(`^(Benchmark\S+?)(?:-\d+)?\s+(\d+)\s+([\d.]+)\s+ns/op(?:\s+([\d.]+)\s+B/op\s+([\d.]+)\s+allocs/op)?$`)

type benchmarkResult struct {
	Package     string
	Name        string
	Samples     int64
	Iterations  int64
	NsPerOp     float64
	BPerOp      float64
	AllocsPerOp float64
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

func main() {
	if err := run(context.Background(), os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "benchdb: %v\n", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("benchdb", flag.ContinueOnError)
	dbPath := fs.String("db", "benchmarks/benchmarks.sqlite", "sqlite database path")
	count := fs.Int("count", 5, "benchmark sample count")
	bench := fs.String("bench", ".", "benchmark regex passed to go test -bench")
	reset := fs.Bool("reset", false, "clear existing benchmark history before saving the new run")
	runPattern := fs.String("run", "^$", "test regex passed to go test -run")
	timeout := fs.Duration("timeout", 10*time.Minute, "go test timeout")
	if err := fs.Parse(args); err != nil {
		return err
	}

	benchArgs := []string{
		"test",
		"-run", *runPattern,
		"-bench", *bench,
		"-benchmem",
		"-count", strconv.Itoa(*count),
		"-timeout", timeout.String(),
		"./ast/...",
		"./gotype/...",
		"./tqlgen/...",
	}

	startedAt := time.Now().UTC()
	output, results, cpuName, err := executeBenchmarks(ctx, benchArgs)
	if err != nil {
		return err
	}
	finishedAt := time.Now().UTC()
	if len(results) == 0 {
		return errors.New("no benchmark results parsed from go test output")
	}

	db, err := openDB(*dbPath)
	if err != nil {
		return err
	}
	defer func() {
		_ = db.Close()
	}()
	if *reset {
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
		Command:    "go " + strings.Join(benchArgs, " "),
		Output:     output,
	}
	runID, err := insertRun(ctx, db, record, results)
	if err != nil {
		return err
	}

	fmt.Printf("Saved benchmark run %d to %s\n", runID, *dbPath)
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
			nsPerOp, err := strconv.ParseFloat(match[3], 64)
			if err != nil {
				return nil, cpuName, fmt.Errorf("parse ns/op for %q: %w", line, err)
			}
			bPerOp := 0.0
			if match[4] != "" {
				bPerOp, err = strconv.ParseFloat(match[4], 64)
				if err != nil {
					return nil, cpuName, fmt.Errorf("parse B/op for %q: %w", line, err)
				}
			}
			allocsPerOp := 0.0
			if match[5] != "" {
				allocsPerOp, err = strconv.ParseFloat(match[5], 64)
				if err != nil {
					return nil, cpuName, fmt.Errorf("parse allocs/op for %q: %w", line, err)
				}
			}
			results = append(results, benchmarkResult{
				Package:     currentPkg,
				Name:        match[1],
				Samples:     1,
				Iterations:  iterations,
				NsPerOp:     nsPerOp,
				BPerOp:      bPerOp,
				AllocsPerOp: allocsPerOp,
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

	for _, result := range results {
		if _, err := stmt.ExecContext(
			ctx,
			runID,
			result.Package,
			result.Name,
			result.Samples,
			result.Iterations,
			result.NsPerOp,
			result.BPerOp,
			result.AllocsPerOp,
		); err != nil {
			return 0, err
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
