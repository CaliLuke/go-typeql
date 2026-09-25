# Performance Tracing

This guide explains how to trace go-typeql with OpenTelemetry and inspect the
data with [logal](https://github.com/CaliLuke/logal). Use tracing for
performance work only. Benchmarks do not use tracing (see
[Benchmarks never trace](#benchmarks-never-trace)).

## What tracing shows

Benchmarks give one total per benchmark (`ns/op`, `B/op`, `allocs/op`). A
trace shows where the time of one operation goes. For example, a trace of
`Manager.Insert` shows the time of the transaction open, the FFI query, the
decode, and the commit.

When tracing is on, each integration test that uses the setup helpers is one
trace. The span tree of a test looks like this:

```text
test TestIntegration_InsertRelation
└── gotype.Manager.Insert
    ├── typedb.tx.open
    │   ├── typedb.tx.admission      (wait for a native-handle slot)
    │   └── typedb.ffi.tx_open
    ├── typedb.tx.query
    │   ├── typedb.ffi.query
    │   └── typedb.decode
    └── typedb.tx.commit
```

## Spans

| Span | Package | Measures |
| --- | --- | --- |
| `test <name>` | test setup | One test. It is the root of the trace. |
| `gotype.Manager.<Op>`, `gotype.Query.<Op>` | `gotype` | One public operation, from the call to the return. |
| `gotype.pool.get` | `gotype` | `ConnPool.Get`, including the wait for a free connection. |
| `typedb.tx.open` | `driver` | Transaction open. Children: `typedb.tx.admission` and `typedb.ffi.tx_open`. |
| `typedb.tx.query` | `driver` | Materialized query. Children: `typedb.ffi.query` and `typedb.decode`. |
| `typedb.tx.query_stream` | `driver` | Streamed query. Children: `typedb.ffi.stream_open`, `typedb.ffi.stream_next`, `typedb.decode_consume`. |
| `typedb.tx.commit`, `typedb.tx.rollback` | `driver` | Commit and rollback. |
| `typedb.tx.close` | `driver` | Native close on the close worker. `typedb.tx.close.delay_us` is the time from `Close` to the start of the native close. |

The `typedb.decode_consume` span includes the row callbacks, for example
hydration. Query spans have the attributes `db.query.text` (the first 4096
bytes), `typedb.query.op`, `typedb.query.fingerprint`, and
`typedb.result.rows`.

## Metrics

| Metric | Type | Value |
| --- | --- | --- |
| `typedb_go.span.duration` | histogram (ms) | Duration of each span, with the attributes `span.name`, `error`, and `typedb.query.op`. |
| `typedb_go.tx.open_inflight` | gauge | Driver transactions that are open. |
| `typedb_go.tx.query_inflight` | gauge | Driver queries that are in an FFI call. |
| `typedb_go.gotype.tx_contexts_active` | gauge | `TransactionContext` values that are not done. |

## Run a traced test

The repository has its own logal instance. Its config is
[`perf/logal.yaml`](../perf/logal.yaml). It uses ports that are different
from the logal defaults, so it does not collide with other projects:

| Port | Use |
| --- | --- |
| `127.0.0.1:44318` | OTLP/HTTP (the tests export here) |
| `127.0.0.1:44317` | OTLP/gRPC |
| `127.0.0.1:43133` | health and status |

The database is `.perf/otel.debug.sqlite`. Git ignores the `.perf/`
directory. The database is disposable: logal can delete or recreate it.

1. Install logal: `brew install CaliLuke/logal/logal`.
2. Start TypeDB: `docker compose up -d`.
3. Build the Rust library: `make build-rust`.
4. Run the traced tests:

   ```bash
   make perf-trace PERF_RUN='TestIntegration_Insert' PERF_PKGS=./gotype/...
   ```

`make perf-trace` starts the logal instance if it is not ready. Then it runs
the integration tests with `TYPEDB_GO_PERFTRACE=1`. `PERF_RUN` is a `-run`
pattern. `PERF_PKGS` is a package list. If TypeDB is not on
`localhost:1730`, set `TEST_DB_ADDRESS`.

Other targets:

| Target | Action |
| --- | --- |
| `make perf-logal-up` | Start the logal instance and wait until it is ready. |
| `make perf-logal-down` | Stop the logal instance. |
| `make perf-logal-clear` | Delete all stored telemetry. The instance continues to run. |

## Run the concurrent workload

The integration tests run one operation at a time, so they do not show
contention. `TestPerfWorkload_ConcurrentMixed` runs concurrent reads and
inserts on one driver. It runs only when `TYPEDB_GO_PERF_WORKLOAD=1` is set.

```bash
make perf-workload PERF_WORKERS=10 PERF_OPS=1000
```

The test logs the throughput, the p50, p95, and p99 latency, and the time to
drain the asynchronous closes. These variables change the load:

| Variable | Default | Meaning |
| --- | --- | --- |
| `PERF_WORKERS` | 10 | Concurrent callers |
| `PERF_OPS` | 1000 | Operations in total |
| `PERF_WRITE_PCT` | 20 | Percentage of operations that are inserts |
| `PERF_CLOSE_WORKERS` | 0 | `DriverOptions.CloseWorkers` (0 uses the driver default) |

The workload is not a benchmark, and `benchdb` does not run it. To compare
two settings, run them in alternation several times, because the load on the
host changes the numbers.

## Inspect the data

Set the database path once per shell:

```bash
export LOGAL_DB_PATH=$PWD/.perf/otel.debug.sqlite
```

Find the slow spans:

```bash
logal spans --name typedb.tx.commit --min-duration 5ms
```

Read one test as a tree:

```bash
logal trace <trace-id> --columns name,duration_ms,span_id,parent_span_id
```

Find where the time of a run goes:

```bash
logal sql --timeout 20s --query "
  select name, count(*) n,
         round(avg(end_time_unix_nano - start_time_unix_nano) / 1e6, 3) avg_ms,
         round(sum(end_time_unix_nano - start_time_unix_nano) / 1e9, 2) total_s
  from otel_spans where name not like 'test %'
  group by name order by total_s desc"
```

Read the metrics:

```bash
logal metrics list
logal metrics points --name typedb_go.span.duration
```

## Benchmarks never trace

Tracing adds work, so benchmark numbers must not include it. Three guards
keep tracing out of benchmark runs:

- `otelperf.InstallFromEnv` does nothing when a `-test.bench` pattern is set.
  It prints `otelperf: TYPEDB_GO_PERFTRACE=1 ignored: benchmark run`.
- `cmd/benchdb` removes `TYPEDB_GO_PERFTRACE` from the environment of the
  `go test` processes that it starts.
- The benchmark history stays in `benchmarks/benchmarks.sqlite`. logal does
  not store benchmark results.

## Cost when tracing is off

Library users never turn tracing on. The hook package
(`internal/perftrace`) is internal and has no dependencies. When no tracer is
installed, each span point costs one atomic load and no allocation.
`TestDisabledStartDoesNotAllocate` checks the allocation count. Only test code
imports the OpenTelemetry adapter (`internal/perftrace/otelperf`), so
programs that import go-typeql do not link the OpenTelemetry SDK.

## Add a span

1. Start the span with `perftrace.Start(ctx, "<package>.<name>")`. Use a
   constant name.
2. Add attributes only inside `if span.Recording() { ... }`. Then the path
   with tracing off does not allocate.
3. End the span with `span.End(err)`. A non-nil `err` marks the span as
   failed.
4. Add the span to the table in this guide.
