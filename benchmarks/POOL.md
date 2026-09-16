# Connection pool investigation (#128)

The same 25-person fixture was created afresh for each case and each of five
processes. TypeDB 3.13.0 under Colima; Go 1.27.1, Apple M4 Pro, macOS/arm64.
Run 15 in `benchmarks.sqlite` holds all raw five-sample output and mean custom
metrics. Each of 100 operations acquires a connection (except the shared
driver), opens a read transaction, fetches 25 names in one query, and waits
for `CloseChecked`. The shared and pooled read drivers are all freshly opened
after fixture setup. The final drain confirms native handles reach zero.

| Callers | Shared driver | Pool 1 | Pool 4 | Pool 1/4 peak acquirers |
| ---: | ---: | ---: | ---: | ---: |
| 1 | 1,631 µs/op | 1,632 µs/op | 1,645 µs/op | 1 / 1 |
| 4 | 502 µs/op | 1,627 µs/op | 493 µs/op | 4 / 1.6 |
| 10 | 269 µs/op | 1,648 µs/op | 503 µs/op | 10 / 8.2 |

The `ns/op` above is elapsed benchmark time divided by 100 completed reads,
so inverse ns/op reflects aggregate throughput, **not** an individual
request's latency. At ten callers, mean p95/p99 latency was 3.30/3.51 ms
shared, 17.29/17.94 ms with pool 1, and 6.24/7.62 ms with pool 4. Mean
p50 latency was 2.53/16.29/4.78 ms in the same order. Mean
connection acquisition at ten callers was below 1 µs shared, 14.12 ms with
pool 1, and 2.85 ms with pool 4; transaction open was 0.85/0.54/0.65 ms,
query 1.03/0.67/0.80 ms, and checked close 0.73/0.43/0.55 ms respectively.
Pooled close also includes returning the connection to its pool. The per-stage
times overlap across workers and must not be added to ns/op.
No timeout occurred (10-second pool wait bound); sampled peak native handles were
10/1/4 at ten callers. Peak concurrent acquisition attempts (0/10/8.2 mean
at ten callers) are an upper bound on blocked waiters, not measured queue
depth; acquisition latency reveals saturation. Go allocations ranged from
~10.1–10.8 KB and 118–126 allocs/op, excluding native and server memory.
Handle count is not native RSS. A pooled driver adds
a connection and cleanup worker per pool slot; the shared driver admits up
to 16 unfinished native transactions by default.

For independent short reads, a shared driver already runs concurrent
transactions; a pool of one serializes them and is a deliberate capacity
limit, not a throughput optimization. A pool of four approaches shared-driver
throughput at four callers but saturates at ten. Choose pool size for an
intentional concurrency cap or connection isolation, and measure it under
the application's transaction hold time. Keep the existing default: these
measurements do not establish that changing it helps general workloads.
Checked close removes asynchronous cleanup backlog from this comparison;
`lifecycle` run 14 separately measures async backlog and drain. The host was
shared with other containers and had background load (load average after
run 15: 5.65/5.43/6.76); timing ratios are directional. No controlled
network-latency or mixed read/write workload was tested.
