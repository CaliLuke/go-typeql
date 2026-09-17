# Large membership filters (#133)

Runs 23 (live execution) and 24 (construction) in the tracked SQLite history
contain five samples for every case. Go 1.27.1, Apple M4 Pro, TypeDB 3.13.0
under Colima for live reads. Each fresh process starts with the shared 256-person
fixture; membership values name `person-00` through `person-24`, and sets of
64/256 include names absent from that fixture. Both
strategies use one query and return the same 1/8/25/25/25 rows for distinct
inputs. The live benchmark constructs its query/rows before timing and includes
transaction open, full query, and checked native close. The separate unit
benchmark includes In/IIDIn query construction or typed-row construction plus
JSON serialization, excluding server execution.

| Values | Expanded In query bytes, live µs/op | Typed query + input bytes, live µs/op | Expanded/typed server CPU ms/op | Expanded/typed Go B/op |
| ---: | ---: | ---: | ---: | ---: |
| 1 | 111, 1,707 | 87 + 68, 1,710 | 1.22 / 1.24 | 1,402 / 1,805 |
| 8 | 335, 3,976 | 87 + 348, 1,918 | 3.50 / 1.55 | 4,034 / 5,195 |
| 25 | 879, 8,524 | 87 + 1,028, 2,367 | 8.35 / 2.11 | 10,434 / 13,651 |
| 64 | 2,127, 17,479 | 87 + 2,588, 2,617 | 17.27 / 2.30 | 10,430 / 18,310 |
| 256 | 8,427, 61,427 | 87 + 10,424, 3,374 | 62.05 / 2.95 | 10,449 / 41,213 |

Typed rows cut end-to-end read latency at larger sizes, but their Go
allocations rise to 378/op at 256 versus 118/op for expanded In. Building
the 256-value expanded query took ~31.2 µs, 67.8 KB, 1,041 allocs; building
and serializing typed rows from the same prebuilt names took ~41.5 µs,
39.6 KB, 524 allocs. Run 22 is an earlier construction sample recorded while
another benchmark was starting; run 24 is the uncontended comparison.
The server/driver phase dominates these microsecond construction costs in
this local read workload. The opt-in `TYPEDB_BENCH_CONTAINER=typedb_test`
setting reads `/sys/fs/cgroup/cpu.stat` before and after each timed case in
the dedicated TypeDB container. Its `server-cpu-ms/op` is total container CPU
time divided by completed operations, not isolated planner time: background
server activity can contribute, and no network time was isolated from FFI
or decoding. The Docker reads occur outside the timed Go benchmark. No robust
threshold follows for arbitrary result shape,
network latency, or allocation-sensitive use.

Semantics prevent transparent substitution. The live duplicate-input
regression returns one answer row for `In(name, [x,x])` but two for two
typed given rows. Empty In matches nothing; NotIn(empty) adds no restriction;
negation and invalid scalar inputs retain their existing validated filter
behavior. IIDIn builds a different `iid` token predicate: at 256 IIDs its
query was 8,759 bytes and construction ~19.5 µs/54 KB/779 allocs. The official
`typeql-check` rejected `given $iid: string; ... iid $iid;` because `iid`
requires an IID literal, not a typed string variable. Opaque concept handles
are process-local registry references with explicit release, not IID strings.
There is no justified typed IID replacement. The valid typed-string and
expanded membership forms are in the TypeQL syntax battery.

No automatic filter rewrite or default threshold is selected: pure-Go
transaction implementations need no optional typed-row interface, and the
existing In/NotIn/IIDIn contracts stay intact. An opt-in typed membership
API could be designed for deduplicated scalar strings after specifying
duplicate and negation semantics, but the measured latency gain alone does
not authorize changing existing filter behavior. The host was shared with
other containers. Timing is
directional and Go B/op excludes Rust and server memory.
