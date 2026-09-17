Performance review evidence for commit `5ec3482`.

The [review](../../../docs/PERFORMANCE_REVIEW.md) explains the findings and their limits.
The [summary](summary.json) contains all samples, medians, and timing ranges.
The raw logs retain diagnostic warnings because close backlogs are part of the result.
These files do not update `benchmarks/benchmarks.sqlite`.

| File | Scope |
| --- | --- |
| [local-bench.txt](local-bench.txt) | Compiler, hydration, fetch construction, and parser reuse |
| [driver-bench.txt](driver-bench.txt) | MessagePack decoding and disabled diagnostics |
| [live-bench.txt](live-bench.txt) | Reads, typed batch inserts, existence, projections, and concurrency |
| [lifecycle-bench.txt](lifecycle-bench.txt) | Inline `put` fetch and completed transaction lifecycles |
| [lifecycle-four-workers.txt](lifecycle-four-workers.txt) | Temporary four-worker close prototype |
| [hydrate-cpu-top.txt](hydrate-cpu-top.txt) | Hydration CPU profile |
| [hydrate-alloc-top.txt](hydrate-alloc-top.txt) | Hydration allocation profile |
| [decode-cpu-top.txt](decode-cpu-top.txt) | Callback decoder CPU profile |
| [decode-alloc-top.txt](decode-alloc-top.txt) | Callback decoder allocation profile |
| [unit-tests.txt](unit-tests.txt) | Passing `go test ./...` result |
| [integration-tests.txt](integration-tests.txt) | Passing driver and ORM integration results |

All summarized benchmarks use `-benchmem -benchtime=1s -count=5`.
Runs execute sequentially after the Rust release build finishes.
The hydration profile comes from an earlier exploratory run during the Rust build.
Its timing is excluded from the result table. Its allocation breakdown supports the final hydration benchmark.
The machine also runs unrelated workloads. No process isolation or statistical confidence interval is claimed.

Environment:

- Apple M5, 10 CPU cores, 24 GiB RAM, macOS arm64.
- Go 1.27.1 and the repository's Rust release library.
- TypeDB 3.12.3 under Colima, with host port 1731.
- `TEST_DB_ADDRESS=localhost:1731` and `TYPEDB_GO_COMPOSE_PORT_MAP=1` for live runs.
- Default query options, default debug behavior, and ten workers in concurrent benchmarks.

The existing benchmarks can be repeated with these commands:

```sh
go test ./ast ./gotype -run '^$' -bench . -benchmem -benchtime=1s -count=5
go test -tags 'cgo,typedb' ./driver -run '^$' -bench BenchmarkDecodeMsgpackRows -benchmem -benchtime=1s -count=5
TEST_DB_ADDRESS=localhost:1731 TYPEDB_GO_COMPOSE_PORT_MAP=1 go test -tags 'cgo,typedb,integration' ./gotype -run '^$' -bench 'BenchmarkLiveRead_(GetByIID|All|GetWithRoles|GetByIIDBreakdown)$' -benchmem -benchtime=1s -count=5
```

The temporary experiments use the existing live fixture, with 256 people and their employment relations.
Existence and projection experiments add 10,000 people through typed input rows.
Bulk experiments insert 32 people per operation, with unique keys and committed transactions.
Both insert variants retrieve IIDs and assign them by key.
The inline `put` experiment uses an existing keyed person and checks the returned IID.
Parser experiments use one attribute and one keyed entity.
The reused parser performs parsing and AST conversion in every measured iteration.

Transaction-reuse timings exclude the initial transaction open and final close for the reused scope.
Ordinary read timings include enqueueing asynchronous close, but they do not wait for native cleanup.
The focused lifecycle experiment includes final close drain in measured time.
It uses ten concurrent callers and the same raw IID query for both close variants.
The four-worker prototype only changes the number of consumers of the close-job queue.

Temporary sources, patches, and binary profiles remain in `/tmp/go-typeql-perf-review-20260916`.
The `experiment-sources` directory contains the benchmark source snapshots.
`live-experiments.patch` adds the live experiments to the reviewed commit.
`four-worker-prototype.patch` contains the isolated close-worker change.
These temporary files are outside the repository and can disappear during system cleanup.

The review does not establish production latency percentiles, peak process memory, native Rust allocation costs, or server execution plans.
It also does not measure remote-network latency, high-fanout graphs, cancellation storms, or large-schema generation.

Validation commands:

```sh
make build-rust
go test ./...
TEST_DB_ADDRESS=localhost:1731 TYPEDB_GO_COMPOSE_PORT_MAP=1 go test -tags 'cgo,typedb,integration' ./driver/... ./gotype/...
```

The official `typeql-check` CLI accepted all experimental query forms before their live runs.
