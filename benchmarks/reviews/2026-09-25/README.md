Performance review evidence, September 25, 2026: close workers.

This review used the new tracing ([Performance Tracing](../../../docs/PERFORMANCE_TRACING.md)).
It found one bottleneck: a driver ran all asynchronous transaction closes on one goroutine.
The fix runs closes on 8 workers. These files do not update `benchmarks/benchmarks.sqlite`.

| File | Scope |
| --- | --- |
| [suite-span-summary.txt](suite-span-summary.txt) | Time per span name for the full traced integration suite |
| [close-workers-traced.txt](close-workers-traced.txt) | Four traced pairs, 1 against 8 workers, with span averages |
| [close-workers-ab.txt](close-workers-ab.txt) | Five untraced pairs of the final code, and three single-caller pairs |
| [slots-drop-matrix.txt](slots-drop-matrix.txt) | 32 callers: slot limits 16, 32, 64, and the read drop, four rounds at low load |
| [read-drop-ab.txt](read-drop-ab.txt) | Final code, checked read close against `DropReadClose`, 10 and 32 callers, at a rising load |
| [read-drop-traced.txt](read-drop-traced.txt) | The same comparison with span averages, at a high load |
| [query-shapes.txt](query-shapes.txt) | Generated read and insert queries against baseline queries, and the optional `given` check |
| [insertmany-optional.txt](insertmany-optional.txt) | `InsertMany` with nil optional fields, before and after the change |

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

## Finding 4: more native-handle slots are not always better

The workload ran with 32 callers, 16 more than the default slot limit.
At a load average of about 9, four interleaved rounds gave these medians:

| Slot limit | Throughput | p50 | p99 |
| ---: | ---: | ---: | ---: |
| 16 (default) | 3,525 ops/s | 7.6 ms | 22.5 ms |
| 32 | 4,195 ops/s | 6.2 ms | 18.8 ms |
| 64 | 1,935 ops/s | 13.4 ms | 50.7 ms |

32 slots gave 1.19 times the throughput. 64 slots gave 0.55 times.
The server becomes slower when it has more live transactions.
The default stays at 16. The review did not trace the cause of the 64-slot result.

## Finding 5: the generated queries are efficient

In one transaction, a trivial `match` on one IID took 0.42–0.54 ms.
The generated `GetByIID` fetch took 0.06–0.09 ms more.
So the round trip, not the query shape, sets the cost of a read.
A literal insert and a typed-row insert of the same shape differed by 0–18%.
This difference does not justify a change to `Insert`.

## Finding 6: `InsertMany` did not batch rows with a nil optional field

`InsertMany` sends 32 rows in one typed-row query.
But one nil pointer field in one row made the complete call fall back to one query for each row.
The traces showed this: five seed rows, one without `age`, gave five insert queries.

The batch query now declares a pointer field as an optional variable (`$v2: integer?`).
It inserts that attribute in a `try` block, and a nil value is an `empty` given value.
The official TypeQL checker accepts the query, and the live server inserts the rows correctly.
An entity with a nil field has no value for that attribute, as with `Insert`.

For 32 rows with a nil `age` in every second row, three runs of 15 calls gave these medians:

| Run | Before (one query per row) | After (one batch) |
| ---: | ---: | ---: |
| 1 | 66.1 ms | 9.0 ms |
| 2 | 64.7 ms | 10.5 ms |
| 3 | 200.5 ms | 10.9 ms |

`PutMany` and `UpdateMany` still fall back for nil optional fields.
For an update, a nil value can mean "delete the value", so these need a separate design.

## Finding 7: dropping read transactions depends on the server load

`DriverOptions.DropReadClose` drops a read transaction in `Close`.
The driver sends the close without a wait for the server, and the slot returns at once.

- At a load average of about 9, the drop gave 1.18 times the throughput with 16 slots (four rounds).
- At a load average of 12–74, the 11 recorded pairs gave no gain: the drop was slower in 5, faster in 3, and about equal in 3.
- The traces show the cost. With the drop, the server-side spans took longer:
  transaction open 24–31 ms → 26–46 ms, query 25–35 ms → 34–51 ms, commit up to 98 ms.
  The server has more live transactions, as in the 64-slot run.

The drop removes the bound that the slot limit puts on closes that the server has still to finish.
Thus, `DropReadClose` is off by default. Measure it on the target server before you use it.

## Correctness

- `formal/tla/TxHandle.tla` now models two close workers that drain one queue.
  TLC found no error: 511,432 distinct states, with `EventuallyClean` and `CloserFinishes` satisfied.
- A mutant takes the job in a separate step after the queue check.
  TLC reports that two workers take one job (`Head` of an empty sequence).
  The Go code takes a job with one channel receive (`for job := range w.jobs`).
- Unit tests pass with `-race`. The full driver and ORM integration suites pass with `-race`.
- The TLA+ model includes an inline drop as a possible result of every close job.
  Thus, `DropReadClose` uses a path that the model checks.
- `TestIntegration_InsertManyNilOptionalFields` inserts 40 people (two batches), with an age for every third one.
  It reads each person back and checks the name, the email, and the age or its absence.

## Limits

- One machine, one server under Colima, and a high background load.
- The workload uses short reads and inserts on one small type. Other workloads can show a different gain.
- The default of 8 comes from this measurement only. It is not tuned for a remote server with a longer round trip.
  A longer round trip makes the single-worker limit lower, so more workers can help more there.
