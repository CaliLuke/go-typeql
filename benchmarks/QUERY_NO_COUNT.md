# Count-free bulk mutation measurements (#130)

Run 30 in `benchmarks.sqlite` records five fresh-process samples for each case. The tests used Go 1.27.1, Apple M4 Pro, and TypeDB 3.13.0 under Colima.

Each sample starts with 256 `live-bench-person` entities. A broad mutation selects all 256; a selective mutation selects `person-00`. Each timed iteration runs inside a caller-owned write transaction and is rolled back outside the timer, so the next iteration starts with the same data. Counted and count-free cases execute the same mutation TypeQL. The only query difference is the counted case's preceding distinct-count query. Each sample ran 10 iterations.

| Mutation | Scope | Count | Queries/op | Time/op | Go B/op | Go allocs/op |
| --- | --- | --- | ---: | ---: | ---: | ---: |
| Update | Broad | Yes | 2 | 8.05 ms | 204,118 | 2,645 |
| Update | Broad | No | 1 | 7.43 ms | 202,891 | 2,624 |
| Update | Selective | Yes | 2 | 1.09 ms | 4,644 | 94 |
| Update | Selective | No | 1 | 0.63 ms | 2,980 | 61 |
| Delete | Broad | Yes | 2 | 6.66 ms | 16,483 | 292 |
| Delete | Broad | No | 1 | 5.99 ms | 15,259 | 271 |
| Delete | Selective | Yes | 2 | 1.01 ms | 3,344 | 63 |
| Delete | Selective | No | 1 | 0.55 ms | 1,750 | 31 |

Skipping the count helps most when the mutation matches one row. Broad mutations remain dominated by the write. Timing ratios are directional because the host had background load. Go allocation figures exclude Rust and TypeDB server memory. Transaction open and rollback are outside the timed loop; query construction and execution are inside it.
