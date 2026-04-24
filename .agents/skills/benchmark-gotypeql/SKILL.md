---
name: benchmark-gotypeql
description: Benchmark and investigate performance in the go-typeql repository. Use when the user asks about performance, regressions, benchmark results, ns/op changes, allocs/op changes, tag-to-tag comparisons, or profiling. Covers stable benchmark execution, release comparison, read-path regression isolation, benchmark history handling, and pprof workflow for this repo.
---

# Benchmark go-typeql

Use this skill for performance questions in `go-typeql`. The goal is to produce a defensible answer, not a noisy one.

Follow this order. Stop and report if an earlier step already explains the issue.

## 1. Classify the request

- If the user wants a routine checkpoint, use the recorded suite: `make bench`.
- If the user wants tag-to-tag comparison, root-cause analysis, or a rollback decision, do **not** start by writing to `benchmarks/benchmarks.sqlite`.
- If the user says "run tests" or "test", that means full unit + integration per repo rules. Benchmarking does not replace tests.

## 2. Preserve the user worktree

For release or commit comparisons, benchmark in temporary worktrees instead of checking out tags in the current tree.

Preferred pattern:

```bash
git worktree add /tmp/go-typeql-v1.8.1 v1.8.1
git worktree add /tmp/go-typeql-v1.9.0 v1.9.0
```

Clean them up when finished:

```bash
git worktree remove /tmp/go-typeql-v1.8.1
git worktree remove /tmp/go-typeql-v1.9.0
```

## 3. Find the relevant benchmarks first

Search before running anything broad:

```bash
rg -n "Benchmark|bench" --glob '*_test.go'
```

Current built-in suite covers:

- `ast/compiler_bench_test.go`
- `gotype/hydrate_bench_test.go`
- `gotype/model_bench_test.go`

If the user mentions read paths like `All`, `Get`, `GetByIID`, or `GetWithRoles`, verify whether a benchmark already exists. If not, say so and add a targeted benchmark only if the task requires code changes to investigate.

## 4. Use stable benchmark settings

Do not trust `-benchtime=3x` for regression calls. Prefer one of:

```bash
go test ./gotype -run '^$' -bench 'Hydrate|Unwrap|ExtractModelInfo' -benchmem -count=5
go test ./gotype -run '^$' -bench 'Hydrate|Unwrap|ExtractModelInfo' -benchmem -benchtime=10x
```

For compiler benchmarks:

```bash
go test ./ast -run '^$' -bench . -benchmem -count=5
```

Stability rules:

- Run the same command on both revisions.
- Prefer `-count=5` over single-shot runs.
- Compare `ns/op`, `B/op`, and `allocs/op`.
- Call out when a result is noisy instead of overstating it.

If `benchstat` is installed, use it. Otherwise, compare repeated runs manually.

## 5. Know when to use the benchmark recorder

Use `make bench` when the user wants a repo performance checkpoint recorded in history.

```bash
make bench
```

Do **not** use `make bench` for every exploratory tag comparison. It appends to the committed SQLite history and creates noise in the repo.

Use the recorder after you have a meaningful result worth preserving.

## 6. Read-path regression workflow

If a user reports read regressions, inspect these areas first:

- `gotype/crud.go`
- `gotype/hydrate.go`
- `gotype/strategy.go`
- `gotype/query.go`
- `gotype/session.go`
- `gotype/pool.go`
- `driver/transaction.go`

Typical split:

- Query build cost: fetch/match clause construction
- Driver/query cost: transaction open, FFI query call, MessagePack decode
- Hydration cost: `hydrateResults`, `Hydrate`, reflection, coercion, role recursion

Diff the suspect area between revisions before guessing:

```bash
git diff v1.8.1..v1.9.0 -- gotype/crud.go gotype/hydrate.go gotype/strategy.go gotype/query.go gotype/session.go gotype/pool.go driver/transaction.go
```

## 7. Isolate the hot path

Once you know the slow benchmark, narrow it:

- If built-in hydration benches regress, focus on `gotype/hydrate.go`.
- If read-path integration benches regress but hydration does not, focus on transaction/query/open-close overhead and fetch generation.
- If edge-fetch stays flat while read paths regress, suspect per-row hydration or result decoding more than query text generation.

When needed, run a single benchmark:

```bash
go test ./gotype -run '^$' -bench '^BenchmarkHydrate_10000Rows$' -benchmem -count=5
```

## 8. Profile before declaring root cause

Use CPU and memory profiles on the smallest benchmark that reproduces the regression clearly.

Example:

```bash
go test ./gotype -run '^$' -bench '^BenchmarkHydrate_10000Rows$' -benchtime=20x -cpuprofile=/tmp/gotype.cpu.out -memprofile=/tmp/gotype.mem.out
go tool pprof -top /tmp/gotype.cpu.out
go tool pprof -top -alloc_space /tmp/gotype.mem.out
```

For compiler hot paths:

```bash
go test ./ast -run '^$' -bench '^BenchmarkCompiler_CompileBatch$' -benchtime=20x -cpuprofile=/tmp/ast.cpu.out
go tool pprof -top /tmp/ast.cpu.out
```

Report the top frames, not just "it seems slower."

## 9. Compare releases cleanly

In each worktree, run the exact same benchmark command and save the output to separate files if you want manual diffs.

Example:

```bash
go test ./gotype -run '^$' -bench 'Hydrate' -benchmem -count=5
```

If `benchstat` is available:

```bash
benchstat old.txt new.txt
```

If not, summarize median or representative repeated results and state the limitation.

## 10. What to conclude

A good answer includes:

- whether the regression reproduces with a stable run
- which benchmark(s) moved and by how much
- whether the change is in query build, driver/decode, or hydration
- the concrete diff or function most likely responsible
- confidence level and remaining uncertainty

Do not jump straight to "roll back" unless the regression is reproduced and materially significant.

## 11. Repo-specific reminders

- Unit benchmarks do not require TypeDB or CGo.
- Integration performance work does require the Rust library and TypeDB:

```bash
make build-rust
podman compose up -d
TEST_DB_ADDRESS=localhost:1730 go test -tags "cgo,typedb,integration" ./driver/... ./gotype/...
```

- `podman`, not `docker`.
- The compose file exposes TypeDB on host port `1730`.
- If you add a benchmark during investigation, keep it focused and remove throwaway instrumentation before finishing unless the user wants the benchmark kept.
