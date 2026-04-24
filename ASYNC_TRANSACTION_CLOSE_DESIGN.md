# Async Transaction Close Design

## Motivation

`v1.9.0` changed `driver.Transaction.Close()` from a cheap Rust handle drop into a synchronous TypeDB driver close:

```rust
txn.close().resolve()
```

That preserved observability for close failures, but it put a blocking driver wait on every deferred read transaction close. In local live-read benchmarks, hydration did not regress and was generally faster in `v1.9.0`; the clearest slowdown was `GetByIID`, moving from roughly `1.32-1.66 ms/op` on `v1.8.1` to `1.89-2.07 ms/op` on `v1.9.0`.

Those facts are not enough to prove close is the whole regression. The `v1.8.1` to `v1.9.0` range contains many performance-relevant commits, including registry lookup indexing, shared compiler reuse, query builder allocation reductions, MessagePack decoder pooling, value parsing changes, and time layout caching. Before implementation, this design must prove which commit caused the `GetByIID` regression and what fraction of wall time is actually spent closing.

The current Go API cannot return close errors anyway:

```go
Close()
```

So the synchronous close may be buying either logging/diagnostics, server-side resource reclamation certainty, or both. The design must quantify the client latency cost and the server-side resource tradeoff before changing the close path again.

## Goals

- Recover the old caller-visible `Close()` latency for normal read cleanup, including pooled read paths.
- Keep the `v1.9.0` observability benefit: close failures should still be logged.
- Offer a synchronous checked close API for callers that deliberately want the error.
- Avoid forcing every `gotype.Tx` mock and adapter to implement a new required method.
- Preserve `Commit()` and `Rollback()` behavior as synchronous, error-returning operations.

## Non-Goals

- Do not make `Close()` return an error; that is a breaking API change across the core `Tx` interface and existing mocks.
- Do not change `Commit()` or `Rollback()` semantics.
- Do not introduce a transaction finalizer. A finalizer can log leaked handles later if leak observability becomes necessary.

## Premise Validation

No implementation should start until these facts are recorded in this document:

1. Run a commit-by-commit benchmark check across `v1.8.1..v1.9.0` for the targeted live-read benchmarks and identify the first commit that materially regresses `GetByIID`.
2. Profile `GetByIID` on `v1.8.1`, on `16b15c1`, and on `v1.9.0` with pprof or equivalent timing instrumentation; record close time as a fraction of wall time.
3. Record Rust drop semantics. In `typedb-driver` 3.10.0, `TransactionTransmitter.Drop` calls `submit_close()`, which marks the transmitter closed and sends transaction-stream shutdown without waiting for the close callback. So `v1.8.1` was not blank memory free; it was fire-and-forget close, while `16b15c1` changed `typedb_transaction_close` to wait on `txn.close().resolve()` and surface errors.
4. Run the same-connection overlap test: open transaction A, start caller-fast close, immediately open transaction B on the same connection, query, repeat under concurrency.
5. Measure peak pending close backlog under benchmark load. This is the client-side queue depth and the server-side transaction working set pressure created by any async design.

If close is a small fraction of `GetByIID`, or if another commit caused the regression, stop and solve that root cause instead of implementing async close.

### Validation Results - 2026-04-24

Benchmark harness:

- Temporary live TypeDB benchmark added as `gotype/live_bench_test.go`, using `TEST_DB_ADDRESS=localhost:1730`.
- Commit-by-commit run covered `v1.8.1` plus every commit in `v1.8.1..v1.9.0`.
- Command shape: `go test -tags "cgo,typedb,integration" ./gotype -run '^$' -bench '^(BenchmarkLiveRead_GetByIID|BenchmarkLiveRead_CloseOnly|BenchmarkLiveRead_GetByIIDBreakdown)$' -benchtime=10x -count=1 -benchmem`.
- Durable benchmark results are in `docs/benchmarks/async-transaction-close.md`. The original scratch logs were produced under `.tmp/livebench/results/` during the investigation and were not release artifacts.

First regressing commit:

| Commit | Meaning | GetByIID ns/op | close ns/op inside raw IID loop | close % |
|---|---:|---:|---:|---:|
| `7c21106` | `v1.8.1` | 880,842 | 3,183 | 0.38% |
| `c5299f6` | commit before close change | 911,104 | 3,204 | 0.35% |
| `16b15c1` | close changed to `txn.close().resolve()` | 1,095,292 | 296,333 | 26.06% |
| `490c6c1` | `v1.9.0` | 1,648,683 | 387,100 | 22.35% |

`16b15c1` is the first commit where close becomes a material fraction of caller wall time. The commit-by-commit run has noise in total `GetByIID` because live TypeDB open/query latency moves around, but the close timing changes discontinuously at `16b15c1`: roughly 3-4 us before the commit and roughly 270-474 us from `16b15c1` onward.

Key revision comparison with all target read paths (`-benchtime=20x`):

| Revision | GetByIID | Get | All | GetWithRoles | caller close in IID breakdown | close % |
|---|---:|---:|---:|---:|---:|---:|
| `v1.8.1` (`7c21106`) | 896,648 | 891,021 | 1,591,888 | 1,017,254 | 3,644 | 0.33% |
| pre-close-change (`c5299f6`) | 967,858 | 1,001,104 | 1,810,781 | 1,265,481 | 4,121 | 0.51% |
| close-change (`16b15c1`) | 1,651,579 | 1,387,890 | 2,355,615 | 1,560,685 | 328,860 | 22.86% |
| `v1.9.0` (`490c6c1`) | 1,470,600 | 1,397,956 | 2,150,652 | 1,522,598 | 342,919 | 22.93% |
| current Option B patch | 1,032,356 | 1,212,121 | 2,040,394 | 1,231,108 | 3,361 | 0.29% |

Close-only and overhead measurements:

| Measurement | Result |
|---|---:|
| `v1.9.0` open + synchronous `Close()` | 1,030,073 ns/op |
| current patch open + caller-fast `Close()` | 498,104 ns/op |
| current patch open + `CloseChecked()` | 981,771 ns/op |
| current patch `GetByIID` caller-visible close | 3,361 ns/op |
| Go channel enqueue baseline | 22.90 ns/op, 0 allocs/op |
| Go goroutine-per-close baseline | 472.9 ns/op, 472 B/op, 1 alloc/op |

Rust drop semantics:

- In `typedb-driver` 3.10.0, `TransactionTransmitter.Drop` calls `submit_close()`.
- `submit_close()` marks `is_open=false` and sends shutdown on the transaction stream without waiting for the close callback.
- The sync `close()` path registers an `on_close` callback, sends shutdown, then blocks on the callback. That is the wait introduced by `16b15c1`.
- Source checked locally in Cargo registry:
  - `typedb-driver-3.10.0/src/connection/network/transmitter/transaction.rs`
  - `typedb-driver-3.10.0/src/transaction.rs`

Same-connection overlap:

- `TestLiveRead_SameConnectionOverlapStress` repeatedly opened read transactions on the same connection, queried, caller-fast closed, and immediately opened the next transaction under concurrency.
- Result: passed with `TEST_DB_ADDRESS=localhost:1730 go test -tags "cgo,typedb,integration" ./gotype -run '^TestLiveRead_SameConnectionOverlapStress$' -count=1`.
- This supports immediate connection reuse after scheduling async close.

Pending close backlog:

- `TestTransactionCloseBacklogUnderReadLoad` ran 200 open/query/caller-fast-close iterations on one connection.
- Result: `peak_pending=1`, final `drain_elapsed=268.5 us`.
- The test logs an implied final drain rate of ~744k closes/s, but that rate is inflated by the tiny final backlog; the useful result is that the worker kept up with this benchmark load and did not build a queue.

Implementation decision:

- Close is a material part of the regression, and `16b15c1` is the first commit that makes it material.
- Current `sync` feature usage in `typedb-driver` exposes the blocking close path; a Rust-side detached async close would require changing the FFI crate's dependency/runtime shape before a safe prototype can be built.
- Option B is justified by the measured enqueue cost, the low pending backlog under load, and recovered caller-visible close timing.
- Because overlap passed, pooled `Close()` should return the connection immediately after scheduling async close; the completion callback is for close-error observation, not for pool return ordering.

## Proposed Design

Keep `gotype.Tx.Close()` as the existing non-error method, but do not commit to a Go-side async implementation until the close cost is measured. The implementation decision is:

1. Measure raw `typedb_transaction_close(... close().resolve())` cost, Go channel enqueue cost, Go goroutine-per-close prototype cost, and Rust-side detached-close prototype cost if feasible.
2. Choose Rust-side detached close if it can be implemented safely in the FFI crate and wins the targeted read-path benchmark.
3. Otherwise choose Go-side bounded worker close with drop-on-full backpressure.
4. Do not implement goroutine-per-close.

The public Go shape stays the same for both viable options:

```go
func (t *Transaction) Close() {
    // caller-fast cleanup
}

func (t *Transaction) CloseAsync(onDone func(error)) {
    // caller-fast cleanup, then optional completion callback when the implementation can observe completion
}

func (t *Transaction) CloseChecked() error {
    // synchronous checked close, returns the close error
}
```

`gotype.Tx` remains unchanged. Code that only knows about `Tx` keeps using `Close()`. Code that wants the close result can type-assert:

```go
if tx, ok := tx.(interface{ CloseChecked() error }); ok {
    return tx.CloseChecked()
}
tx.Close()
return nil
```

Use the anonymous interface form at assertion sites. Do not define this interface in `driver/`, because `gotype/` must not import `driver/`.

The pool path uses an anonymous optional completion interface:

```go
if tx, ok := pt.tx.(interface{ CloseAsync(func(error)) }); ok {
    tx.CloseAsync(func(error) { pt.pool.Put(pt.conn) })
    return
}
```

This is an API addition on concrete driver transactions, not a change to the required `gotype.Tx` interface. For Option B, the callback runs after checked close handling. For Option A, the callback can run immediately only if the empirical connection-reuse test proves immediate reuse is safe; otherwise Option A needs a Rust completion callback FFI before it can support conservative pooled return. The callback must not call back into the detached `*driver.Transaction`.

## Option A: Rust-Side Detached Close

Preferred if feasible. Add an FFI function that takes ownership of the transaction pointer and returns immediately:

```c
extern void typedb_transaction_close_detached(void* txn);
```

The Rust side converts the pointer back into `Box<Transaction>` and schedules checked close work on Rust async infrastructure instead of forcing Go to create goroutines or enqueue work:

```rust
let txn = unsafe { Box::from_raw(txn) };
runtime.spawn(async move {
    let _ = txn.close().await;
});
```

This is the best performance shape because Go `Close()` becomes detach+nil plus one cheap FFI pointer handoff. It avoids per-close Go goroutine allocation, Go scheduler churn, Go channel contention, and closure captures. `CloseChecked()` keeps using the existing synchronous checked FFI.

Feasibility work for this option:

- Confirm whether the FFI crate can use the TypeDB driver's async close path while the dependency is built with the current `sync` feature.
- If the current dependency shape cannot `.await` `Transaction::close()`, evaluate whether the FFI crate can host a Tokio runtime and call a non-sync driver API without duplicating driver state.
- Decide where detached close errors are logged. Rust-side error logging is acceptable; Go cannot return the error from `Close()` anyway.
- Measure this prototype against `GetByIID`, `All`, `Get`, and `GetWithRoles`.

## Option B: Go-Side Bounded Worker

Use this only if Option A is too invasive or does not benchmark better. The concrete driver transaction implementation is:

1. Under the transaction mutex, capture the detached close job: `t.ptr`, `tx_id`, `db`, `tx_type`, and close start time.
2. Set `t.ptr = nil` and mark the Go transaction closed immediately.
3. Release the transaction mutex before any FFI close call.
4. Try to enqueue the close job as a value type on a bounded background worker channel.
5. Return from `Close()` without waiting for Rust `close().resolve()`.
6. The close worker calls the checked Rust close on the detached pointer, logs success/failure using the existing `logFFIDuration` path, and then runs any captured completion callback.

The detached pointer is single-owner state. After detach, the worker must not read or lock `*Transaction`; it owns only the captured job data. `t.mu` is held only for detach+nil+state update, never for the close FFI call.

All state inspection paths (`Query`, `QueryWithContext`, `QueryWithOptions`, `IsOpen`, `Commit`, `Rollback`, `Close`, `CloseChecked`) must observe the nil pointer under `t.mu` after detach and behave as closed. There should be no externally visible "open but closing" state.

Backpressure policy is performance-first:

- If enqueue succeeds, the worker performs checked close and logs errors.
- If the worker queue is full, call a drop-only FFI fallback immediately and return.
- Do not allocate an emergency goroutine when the queue is full.
- The drop-only fallback gives up checked close observability for that transaction and relies on server-side transaction timeout/reaping, matching the old client-visible behavior under overload.

The worker should be connection-scoped, not one global worker, if the TypeDB driver serializes transaction stream work per connection. That model avoids N Go goroutines or global workers piling up on the same Rust-side connection serialization point. Start with one long-lived worker per connection and a fixed buffered channel sized from benchmarks. A tiny fixed worker count per connection is acceptable only if benchmarks show one worker cannot drain close work quickly enough. Per-close goroutines are explicitly rejected for this design.

For both options, `Close()`, `CloseAsync(...)`, and `CloseChecked()` all detach under `t.mu`. A second close call after any entry point detaches the pointer is a nil-return no-op; a second `CloseAsync` callback is not run because no new close job exists. `Commit()` consumes the transaction handle synchronously; a deferred `Close()` after successful or failed commit is also a no-op.

## FFI Shape

Keep the existing checked FFI function for `CloseChecked()` and the Go worker's normal close path:

```c
extern void typedb_transaction_close(void* txn, char** err_out);
```

Add this function for Option A if Rust-side detached close is feasible:

```c
extern void typedb_transaction_close_detached(void* txn);
```

Add this function for Option B's overload fallback:

```c
extern void typedb_transaction_drop(void* txn);
```

`typedb_transaction_drop` is not a pure optimization. It restores `v1.8.1` client-visible latency by abandoning the transaction locally; the server still has to notice and reap the transaction later. That tradeoff is acceptable only as the worker-queue-full behavior, where preserving caller latency is more important than checked close observability for every individual close.

## Pooling Considerations

`pooledTx.Close()` currently closes the underlying transaction and returns the connection to the pool immediately after:

```go
pt.tx.Close()
pt.pool.Put(pt.conn)
```

With async close, returning the connection before the Rust close finishes may allow the same connection to open a new transaction while the previous close is still resolving. We should avoid that in the pool path while still removing close latency from the caller.

Performance is the priority, so the pool path must also remove close latency from the caller. A design that only accelerates non-pooled `Database.ExecuteRead` does not solve the production case.

Initial pooled strategy:

- First run the connection-reuse stress test against the current TypeDB driver.
- If the driver tolerates opening transaction B while transaction A is closing, `pooledTx.Close()` returns the connection to the pool immediately after detaching/scheduling close.
- If the driver does not tolerate overlap, `pooledTx.Close()` uses the completion callback and returns the connection to the pool only after background close completes.
- `pooledTx.Commit()` and `pooledTx.Rollback()` remain synchronous and return the connection after the operation completes.
- If the wrapped transaction does not implement `CloseAsync`, `pooledTx.Close()` keeps the current conservative fallback: call `pt.tx.Close()` synchronously, then `pool.Put(conn)`.

The fast pooled path is the default if the empirical overlap test passes. A synchronous/conservative close mode can be added as a pool option for deployments that prefer strict connection reuse ordering over read latency, but it should not be the default performance path.

Close errors do not poison the pooled connection by default. The async close path logs the close error when that path can observe it and still returns the connection to the pool, matching today's `Close()` behavior where callers cannot act on close failures. If later evidence shows a close error leaves the driver connection unusable, change the completion policy to discard that connection instead of `pool.Put`.

The same overlap concern exists for non-pooled `Database` handles, because a caller can open a second transaction while the first close is still resolving in the background. Source inspection of `typedb-driver` 3.10.0 shows:

- The `sync` feature exposes blocking promise resolution over an internal `BackgroundRuntime`.
- Each transaction gets its own `TransactionTransmitter` and open transaction stream.
- Close registers an `on_close` callback, marks that transaction transmitter closed, and signals that transaction stream shutdown.

That makes concurrent open plus prior close on the same connection expected to work, but it is not a public contract we should rely on without a test. The first implementation must include a non-pooled integration stress test that repeatedly opens a transaction, runs a small read, calls async `Close()`, immediately opens the next transaction on the same connection, and drains pending closes at the end.

## Server-Side Resource Pressure

Caller-fast close can increase the number of transactions that are closed from Go's point of view but not yet fully reclaimed by TypeDB. That matters if the checked close in `v1.9.0` was added to make server-side transaction cleanup deterministic, not only to improve close-error logging.

The benchmark plan must record:

- Peak pending close jobs on the client under the targeted read benchmark.
- Close worker drain rate per connection.
- Whether TypeDB exposes enough metrics or logs to observe open transaction pressure during the benchmark.
- The implied worst case: `read_qps * close_latency_or_timeout` pending transactions if close work falls behind or drop-on-full relies on server timeout.

`typedb_transaction_drop` is acceptable only as an overload fallback after this tradeoff is quantified. It should not be treated as free server-side cleanup.

## Context Cancellation

`Close()` does not take a context today. Caller-fast close improves cancellation-heavy call paths because the caller stops waiting for FFI close, but the actual Rust close still runs to completion in the background and cannot honor the cancelled request context.

`CloseChecked()` is intentionally synchronous and also has no context. Callers that opt into checked close are choosing to wait for the driver close result even after their request context has been cancelled. If cancellable checked close becomes necessary, it should be a separate API such as `CloseCheckedContext(ctx)` rather than changing `Close()` semantics.

## Alternatives Considered

- Transaction reuse / request-scoped read transactions: avoid fixed open/close cost by opening one read transaction per request or explicit unit of work. This may be simpler and faster when real workloads perform several reads per request. Evaluate before async close if pprof shows fixed transaction overhead dominates.
- Rust-side detached close: preferred async implementation if feasible and benchmark-proven.
- Go-side connection-scoped bounded worker: fallback if Rust-side detached close is too invasive or slower.
- Go-side goroutine-per-close: rejected due to scheduler, allocation, and likely Rust-side connection serialization costs.
- Keep synchronous close: acceptable if pprof shows close is not a material part of the regression or if server-side resource pressure makes async close unsafe.

## Error Handling

`Close()` cannot return an error, so async close errors are logged only:

- event: `tx.close`
- result: `error`
- fields: `tx_id`, `db`, `tx_type`, elapsed duration, error string

`CloseChecked()` returns the error and also logs it.

If `Close()` is called after `Commit()` consumed the handle, it remains a no-op.

If `CloseChecked()` is called after `Commit()` or after another close detached the handle, return `nil`. That preserves idempotent close semantics and matches `Close()`.

Async closes may still be in flight at process exit. If the chosen implementation has Go-owned pending work, include a package-level drain hook backed by the bounded worker:

```go
func WaitForPendingCloses(ctx context.Context) error
```

Tests and long-running applications can call this before teardown/shutdown to wait for checked close logging and pool return completion. If Option A owns all pending work inside Rust, either expose an equivalent Rust-backed drain or document that detached Rust close work is not drainable from Go and rely on integration stress tests plus process lifetime.

The worker should track pending jobs with a `sync.WaitGroup` or equivalent. `WaitForPendingCloses` waits for jobs already accepted by the queue and returns `ctx.Err()` if the context expires first. It does not close the worker permanently; it is a drain point, not a shutdown switch.

## Expected Performance Impact

The implementation must include numbers before choosing Option A, Option B, or no async change:

- First regressing commit between `v1.8.1` and `v1.9.0`.
- Raw synchronous close cost: `typedb_transaction_close(... close().resolve())` with no query work.
- Go goroutine-per-close prototype cost, only as a rejected baseline.
- Go worker enqueue cost under the chosen channel size.
- Rust detached-close FFI handoff cost if Option A is feasible.
- Fraction of `GetByIID` wall time attributable to close before and after the patch.
- Peak pending close backlog and drain rate under the targeted benchmark.

For normal read paths:

```go
defer tx.Close()
```

caller-visible latency should return close to `v1.8.1`, because the caller no longer waits for `txn.close().resolve()`.

Total system work does not disappear; it moves off the read call's critical path, and any Go-side solution must avoid adding enough scheduler/allocation overhead to erase the win. Option A should have the lowest Go overhead. Option B's bounded worker caps goroutine pressure and provides a drain point; on queue saturation it drops locally instead of blocking the caller.

Success logging should remain cheap when disabled. `logFFIDuration` currently computes elapsed time and appends one field even when debug logging is off, then logs only slow calls or debug-enabled calls. The implementation should avoid extra formatting/allocation on the close hot path beyond the existing duration check, and should not add always-on success logging.

## Test Plan

Unit-level:

- `Close()` marks the transaction closed and nils the pointer immediately.
- `CloseChecked()` returns the checked close error.
- Repeated `Close()` / `CloseChecked()` is safe.
- `CloseChecked()` after `Commit()` or prior `Close()` returns `nil`.
- Concurrent `Close()` + `CloseChecked()` + `IsOpen()` + query attempts are race-free and observe closed state after detach.
- `Commit()` consumes the handle synchronously and deferred `Close()` is a no-op.
- `pooledTx.Close()` returns to the caller immediately; pool return timing matches the empirical reuse decision.
- `pooledTx` returns the connection exactly once across `Close()`, `Commit()`, and `Rollback()`.
- The `CloseAsync` completion callback runs exactly once. For Option B it runs after checked close logging and receives the checked close error.
- On pooled checked-close error, the connection is still returned to the pool and the error is logged.
- Option B only: worker queue full calls `typedb_transaction_drop` instead of blocking or spawning another goroutine.
- Option B only: close jobs are value structs sent through the channel, not per-close goroutine closures.
- Run race coverage for close ordering:

```bash
go test -race ./gotype/... ./driver/...
```

Integration/performance:

- Bisect or otherwise run commit-by-commit targeted benchmarks across `v1.8.1..v1.9.0` and record the first regressing commit.
- Capture pprof or explicit timing breakdown for `GetByIID` on `v1.8.1`, `16b15c1`, and `v1.9.0`; record close as a percentage of wall time.
- Add a close-only benchmark that measures transaction open plus immediate close, synchronous checked close, Go worker close, Go goroutine prototype close, and Rust detached close if feasible.
- Add a benchmark result table to this design doc before implementation is finalized.
- Re-run targeted live-read benchmarks for `All`, `Get`, `GetByIID`, and `GetWithRoles` against `v1.8.1`, current `main`, and the patch.
- Stress test one shared non-pooled connection with many goroutines opening, querying, and closing transactions; call `WaitForPendingCloses(ctx)` when the chosen option has a Go drain point and verify no FFI crashes, leaks, or race reports.
- Stress test pooled read close under concurrency and prove whether immediate connection reuse after async close is safe. This test decides the default pooled strategy.
- Add a transaction-reuse benchmark variant that performs several `GetByIID`-style reads inside one read transaction and compare it to one transaction per read.
- Run unit tests:

```bash
go test ./ast/... ./gotype/... ./tqlgen/...
```

- Run integration tests:

```bash
make build-rust
podman compose up -d
TEST_DB_ADDRESS=localhost:1730 go test -tags "cgo,typedb,integration" ./driver/... ./gotype/...
```

## Ship Criteria

- The doc identifies the first regressing commit in `v1.8.1..v1.9.0` and shows that close is a material fraction of `GetByIID` wall time.
- The doc contains a benchmark table showing close-only cost and targeted read-path results for baseline, current sync close, and the chosen async strategy.
- `GetByIID` and the other targeted live-read benchmarks recover most of the `v1.9.0` close regression without adding measurable scheduler/allocation overhead.
- The non-pooled same-connection stress test passes under integration test and race-test runs.
- The pooled close test decides and proves the default pool behavior; if immediate reuse is safe, pooled close is caller-fast and returns the connection immediately.
- Option B ships only with bounded worker, drop-on-full, and no goroutine-per-close path.
- The transaction-reuse alternative is either shown to be insufficient for the target regression or documented as the preferred fix.
