# Native retention after caller cancellation (#132)

The bounded workload seeds 100 people and attempts four 100³ Cartesian-count
queries with two callers. Each caller has a 50 ms context deadline. A per-
driver native admission limit of one or two and a server transaction timeout
of 1,500 or 200 ms bound continued work; the fixture and process are fresh
for every sample. This is deliberately smaller than an unbounded cancellation
storm: the previous 300³ variant exhausted the compose container. All cases
wait until in-flight calls finish and pending native closes drain, then assert
zero retained handles. Run 21 in `benchmarks.sqlite` records five repetitions
of four attempts per case on TypeDB 3.13.0, Go 1.27.1, Apple M4 Pro under
Colima. Run 20 is a preliminary measurement before the caller-return and
after-drain resource counters were added.

| Admission / server timeout | Caller p95 | Query cancels / admission cancels | Background queries / peak native | Post-caller drain | Go B/op, allocs/op |
| --- | ---: | ---: | ---: | ---: | ---: |
| 1 / 1,500 ms | 51.53 ms | 1 / 3 | 1 / 1 | 1,002 ms | 1,933 B, 16.8 |
| 2 / 1,500 ms | 51.27 ms | 2 / 2 | 2 / 2 | 998 ms | 1,839 B, 21.4 |
| 2 / 200 ms | 51.19 ms | 2 / 2 | 2 / 2 | 138 ms | 1,214 B, 16.4 |

Values are five-sample means; caller p95 is nearest-rank over four attempts
in each sample (the slowest of four). `ns/op` in the database includes the final
native drain and is not caller latency. At caller return, all one/two native
handles and background calls remained active; at drain, both counts and pending
closes were zero. The five-sample mean Go heap in use at caller return vs
after drain was 1.60/1.60 MiB (limit 1), 1.61/1.62 MiB (limit 2, 1,500 ms),
and 1.62/1.62 MiB (limit 2, 200 ms). Process peak RSS at those phases was
20.00/20.00, 20.66/20.67, and 20.91/20.92 MB, respectively. The RSS metric
is a cumulative high-water mark, not current RSS or native allocation by
query; the Go heap likewise cannot measure Rust allocations.

A separate opt-in Rust debug trace
observed queries finishing ~1,095–1,149 ms after entry with the 1,500 ms
transaction timeout, despite its caller leaving at ~50 ms. Reproduce the
200 ms trace (or change the benchmark selector for 1,500 ms) with
`TYPEDB_GO_DEBUG_RUST=1 TEST_DB_ADDRESS=localhost:1730
TYPEDB_GO_COMPOSE_PORT_MAP=1 go test -tags 'cgo,typedb,integration'
./driver -run '^$' -bench 'BenchmarkLiveCancellationRetention/native-limit=2/tx-timeout=200ms'
-benchtime=2x -count=1`. With a 200 ms
timeout, Rust reported an interrupted-read error at ~225–233 ms. A separate
coarse server sample series during a 1,500 ms debug run (`date` followed by
`docker stats --no-stream` four times) recorded 17:17:09 38.6%/1.37 GiB,
17:17:10 205.0%/1.83 GiB, 17:17:12 7.1%/1.60 GiB, and 17:17:14
6.7%/1.60 GiB (CPU/memory). A 200 ms run with 40 attempts recorded 17:17:23
35.1%/1.38 GiB, 17:17:24 66.2%/1.35 GiB, 17:17:26 6.4%/1.35 GiB, and
17:17:28 7.7%/1.35 GiB. These snapshots are coarse, include fixture setup
and other work in the shared server, and are not a query-attributed CPU
profile or a leak test. Go B/op excludes native/server allocations. Admission
one prevents a second native
query from starting while the first is abandoned; it cannot interrupt the
first query. The supported transaction timeout does shorten the native
retention in this workload, at the cost of also limiting valid long queries.

No native interruption redesign is warranted from this bounded local test.
The driver still returns promptly on context cancellation and abandons that
transaction; it remains unusable, while the in-flight Rust call retains the
handle until completion and then enqueues its close. Existing lifecycle and
integration regressions cover post-cancel `Commit`/`Close` behavior,
single-owner cleanup and eventual native drain. This benchmark adds a
repeated-timeout resource bound and verifies final native/pending counts are
zero. It does not constitute a leak proof under arbitrary server failures or
longer storms. Timing is directional, with only two callers and one local
network path tested.
