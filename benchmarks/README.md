# Benchmarks

This directory stores benchmark history for the repository.

- `benchmarks.sqlite` is the canonical benchmark database and is committed with the repo.
- Each `make bench` run appends a new benchmark run with git metadata, machine metadata, and the raw `go test` output.
- `make bench` explicitly records the unit suite. Exploratory `benchdb` runs
  print results without opening the tracked SQLite file unless `-db` is given.

Run benchmarks with:

```bash
make bench
```

The benchmark recorder prints the current numbers and compares them to the previous recorded run for each benchmark.

The isolated role-player resolution benchmark does not need TypeDB; run
`go test ./tqlgen -run '^$' -bench '^BenchmarkRolePlayerResolution$' -benchtime=1000x -count=5 -benchmem`.
See [the role-resolution report](ROLE_RESOLUTION.md) for workload and results.

## Reproducible groups

Run `go run ./cmd/benchdb -group=decode` for the in-memory decoder. The live
groups require a disposable TypeDB **3.13.0** server, the built Rust FFI
library, and the repo compose address mapping:

```bash
docker compose up -d
make build-rust
TEST_DB_ADDRESS=localhost:1730 TYPEDB_GO_COMPOSE_PORT_MAP=1 \
  go run ./cmd/benchdb -group=bulk
```

Replace `bulk` with `projections`, `typed-reads`, `result-reads`, `lifecycle`, `pool`, `get-one`, `prefetch`, `stream-latency`, `cancellation`, `membership-build`, `membership-live`, `projection-reads`, `projection-transfer`, or `typed-iteration`. Live groups
are opt-in; the runner executes five separate test processes by default, so
each sample starts with a fresh database fixture. `-benchtime=20x` and
`-count=5` can override a group's defaults. Do not compare samples from
different server versions, fixture sizes, machine loads, or concurrency.

| Group | Timed workload and data size | Concurrency |
| --- | --- | ---: |
| `unit` | Compiler/ORM microbenchmarks, no server | 1 |
| `decode` | 1,000 flat, nested, wide, key-only, or string-value MessagePack rows, including callback decoding | 1 |
| `bulk` | Insert/update 4/16/64 rows, put/delete 16 rows; query counts reported per operation | 1 |
| `projections` | Fresh vs cached projection, one unique-key model read per operation | 1 |
| `typed-reads` | One model by IID/key, all fixture models, or one relation with roles | 1 |
| `result-reads` | Materialized 64/256-row results with 16/512-byte names; two-chunk 512-row/512-byte stream with first-row timing | 1 |
| `lifecycle` | 100 completed empty-result reads, 49-byte query, including close/drain | 10 |
| `pool` | 100 reads of 25 named people with checked native close; shared driver vs pools of 1/4 | 1/4/10 |
| `get-one` | 0/1/2/25 matches in a 283-person fixture; current vs count-first vs bounded-two | 1 |
| `get-one-duplicates` | Synthetic 25 identical answer rows for one IID; ORM hydration only | 1 |
| `prefetch` | Chunked stream: FFI row limits 32/128/256 × server prefetch default/1/256; 64/256/512 narrow, wide, nested and early-stop results | 1 |
| `stream-latency` | Narrow 64 and wide 512 streamed through a local socket proxy with 0/2 ms per read; chunk 32/256 × prefetch default/1/256 | 1 |
| `cancellation` | Two callers, four 100³ cross-product queries, 50 ms caller deadline, 200/1500 ms transaction timeout | 2 |
| `membership-build` | In, typed-row JSON, and IIDIn construction at 1/8/25/64/256 values | 1 |
| `membership-live` | Expanded In vs typed string input on 25 people, 1/8/25/64/256 supplied values | 1 |
| `projection-reads` | Full vs name-only reads on 64 narrow and 64 eight-attribute wide entities | 1 |
| `projection-transfer` | Encoded FFI response bytes for the same 64-row projection query shapes | 1 |
| `typed-iteration` | Full `All` vs typed `ForEach` on 256 entities, with first-model and retained-heap metrics | 1 |

See [typed iteration measurements](TYPED_ITERATION.md) for the five-sample result and heap measurement limits.

For `membership-live`, set `TYPEDB_BENCH_CONTAINER=typedb_test` only when
that container is dedicated to this benchmark. The runner then records
container CPU time per operation from cgroup counters; without it, the
server-CPU metric is omitted. This is total server-container CPU, not an
isolated query-planner profile.

The ORM live fixture starts with 256 people, five companies, and 256 employments;
bulk inserts grow it within each independent process. The driver result-read
fixture is separate and inserts exactly the row count named by its case. The bulk comparison
thus uses identical starting fixtures per sample, but later sub-benchmarks
see the earlier writes in a fixed order. `ns/op`, `B/op`, `allocs/op`, and
additional metrics such as `queries/op`, p50/p95/p99, queue wait, and peak
pending are preserved in the raw output and (when explicitly recorded) in
`benchmark_metrics`. Go B/op does **not** include Rust allocations or server
memory; sample `docker stats` and native/Rust logging separately, and report
peak process memory only when actually measured. The materialized `result-reads` cases report the
process-wide peak RSS high-water mark (Go plus native code, excluding the
server) and current Go heap in use. Both are snapshots after each case, not
isolated per-case deltas; prior cases in the same process can raise the peak.
`result-reads` compares eager Query with chunked QueryEachWithContext; the
streaming case records time to the first callback. The `lifecycle` group's
completed-operation latency includes final cleanup; the `typed-reads`
breakdown's `close-ns/op` measures caller-visible `Close()` only.

Use `-db /path/to/disposable.sqlite` for a local comparison. Only after
reviewing fixture, server version, and five samples should a baseline be
recorded with an explicit `-db benchmarks/benchmarks.sqlite`; never use
`-reset` on that tracked file for exploration. Note CPU, OS, Go version,
server version, concurrent workloads and background load alongside a reviewed
baseline; timing ratios under shared host load are directional.

## Reviewed baseline: 2026-09-16

Runs 9–14 record five samples per group for `decode`, `bulk`, `projections`,
`typed-reads`, `result-reads`, and `lifecycle` (51 benchmark series and 260
retained metric values). Runs 1–8 are the older unit history. The six new
runs used Go 1.27.1 on an Apple M4 Pro (macOS/arm64); the live samples used
the TypeDB 3.13.0 container on host port 1730 and the built Rust driver.
Each live sample ran in a fresh test process with a disposable fixture; the
decoder's five samples ran within one process. The recorded git base is
`2d02ad4` with a dirty flag because this ticket's runner and benchmarks had
not yet been committed. The host also ran other containers, including a
separate TypeDB 3.12.2 instance, and the load average after the run was
4.09/5.39/7.37. No idle-host isolation or controlled load sweep was done.
Treat ns/op differences as directional, not strict thresholds; Go B/op
excludes Rust and server allocations. Materialized result-read peak RSS is
process-wide and includes fixture setup and earlier cases.
