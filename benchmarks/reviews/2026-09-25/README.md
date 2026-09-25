Performance review evidence, September 25, 2026: close workers.

This review used the new tracing ([Performance Tracing](../../../docs/PERFORMANCE_TRACING.md)).
It found one bottleneck: a driver ran all asynchronous transaction closes on one goroutine.
The fix runs closes on 8 workers. These files do not update `benchmarks/benchmarks.sqlite`.

| File | Scope |
| --- | --- |
| [suite-span-summary.txt](suite-span-summary.txt) | Time per span name for the full traced integration suite |
| [close-workers-traced.txt](close-workers-traced.txt) | Four traced pairs, 1 against 8 workers, with span averages |
| [close-workers-ab.txt](close-workers-ab.txt) | Five untraced pairs of the final code, and three single-caller pairs |

Environment:

- Apple M5, 10 CPU cores, macOS arm64, Go 1.27.1, the Rust release library.
- TypeDB 3.13.6 under Colima, on host port 1732 (`TEST_DB_ADDRESS=localhost:1732`).
- The host had a high load from other work. The load average was 54 when the measurements started.
  Timings changed by up to ten times between runs of the same setting.
  For this reason, all comparisons are interleaved pairs, and the review reports medians.

## Finding 1: the ORM cost is small, and round trips cost the most

In one traced `Manager.Insert`, the child spans took 6.13 ms of 6.18 ms.
The children are the transaction open, the query, and the commit.
The ORM work (query construction and hydration) took about 0.05 ms.
In the full suite, decode took 0.01 ms per query on average.
Transaction open (5.8 ms), query (5.3 ms), and commit (6.8 ms) took the most time.
These are server round trips. A client change cannot make them shorter.

## Finding 2: the admission waits in the test suite are a test artifact

In the full suite, `typedb.tx.admission` took 1.28 ms on average.
782 of 5,920 admissions in the ORM tests waited 1 ms or more.
781 of these waits are in the `bench_async_close_*` databases, which fill the close backlog on purpose.
The other wait is in a test with `MaxNativeTransactions: 1`.
The sequential suite gives no evidence of an admission problem.

## Finding 3: one close worker limited concurrent throughput

`TestPerfWorkload_ConcurrentMixed` runs ten callers on one driver: 80% `GetByIID` and 20% `Insert`.
With one close worker, `typedb.tx.admission` took 66–76% of each `GetByIID` in four traced runs.

The cause is in the driver:

1. `Close` queues the native close and returns immediately.
2. The queued close keeps its native-handle slot until the native close completes.
3. One goroutine ran all native closes. Each close waits for one server round trip.
4. The driver therefore completed at most one close per round trip.
   When callers closed transactions faster, the queue held the slots, and new transaction opens waited.

In the traced runs, 800 closes at 2.78 ms each took 2.2 s. The complete loop took 2.23 s.

The fix runs the queue on a pool of workers: 8 by default, set with `DriverOptions.CloseWorkers`.
The admission limit caps the pool, because each queued close holds a slot.
The fix keeps the existing guarantee: a slot stays taken until its native close completes.

Traced pairs (experiment knob, 1 against 8 workers), average admission wait for each transaction open:

| Pair | 1 worker | 8 workers | Admission share of `GetByIID`, 1 → 8 |
| ---: | ---: | ---: | --- |
| 1 | 7.57 ms | 0.21 ms | 66% → 5% |
| 2 | 12.06 ms | 1.41 ms | 76% → 15% |
| 3 | 46.00 ms | 5.06 ms | 69% → 6% |
| 4 | 85.37 ms | 2.79 ms | 70% → 8% |

In pair 3, the server was slow for the 8-worker run (open and close took about 45 ms), so its throughput was lower.

Final code, five untraced interleaved pairs, ten callers, 1,000 operations:

| Metric | `CloseWorkers: 1` (median) | default, 8 (median) |
| --- | ---: | ---: |
| Throughput | 519 ops/s | 1,379 ops/s |
| p50 latency | 12.0 ms | 5.0 ms |
| p99 latency | 98.7 ms | 57.8 ms |
| Close drain after the loop | 12–45 ms | 1 ms |

The default had a higher throughput in all five pairs. The ratio per pair was 1.13–2.66.
With one caller, the three pairs show no consistent difference.
One caller rarely has more than one close in the queue, so this result is expected.

## Correctness

- `formal/tla/TxHandle.tla` now models two close workers that drain one queue.
  TLC found no error: 511,432 distinct states, with `EventuallyClean` and `CloserFinishes` satisfied.
- A mutant takes the job in a separate step after the queue check.
  TLC reports that two workers take one job (`Head` of an empty sequence).
  The Go code takes a job with one channel receive (`for job := range w.jobs`).
- Unit tests pass with `-race`. The full driver and ORM integration suites pass with `-race`.

## Limits

- One machine, one server under Colima, and a high background load.
- The workload uses short reads and inserts on one small type. Other workloads can show a different gain.
- The default of 8 comes from this measurement only. It is not tuned for a remote server with a longer round trip.
  A longer round trip makes the single-worker limit lower, so more workers can help more there.
