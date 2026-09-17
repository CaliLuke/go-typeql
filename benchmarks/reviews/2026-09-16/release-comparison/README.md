# Release performance comparison — 2026-09-16

Current code is about **2.1× as fast** across the 12-operation database benchmark mix. The corresponding operation-time reduction is **53%**.

This compares `v1.15.0-alpha.3` (`5ec3482`) with `d69aa00` plus the current, uncommitted defect fixes. The release was published on 2026-08-27.

Bulk writes provide most of the improvement. The five bulk cases average **4.1×** speed, or **76% less time**.

The five read cases average **1.25×** speed, or **20% less time**, but their timing variation is substantial.

## Method and limits

- Both versions used Go 1.27.1, Apple M5, macOS 26.6.2, and `GOMAXPROCS=4`.
- Both used the same TypeDB 3.13.0 server through Colima. Each used its own release-mode Rust library and dependency lockfile.
- This holds the database server constant. It does not measure the server upgrade from 3.12.x to 3.13.0.
- The common fixture contained 256 people, five companies, and 256 employment relations. Reads ran before writes.
- Bulk calls handled 16 people. `Put16` used eight existing and eight new people. Setup inserts were outside the timed loops.
- Each revision ran in five fresh processes. Run order alternated: release/current, current/release, and so on.
- Database cases used ten iterations per sample. Local cases used 300 ms per sample. All benchmark processes passed.
- Values below are medians of five samples. `benchstat` was unavailable, so the analysis uses explicit median and ratio calculations.
- Each operation has equal weight in the geometric average. Batch operations count as one benchmark each.
- The average is not a forecast for a production workload. That requires the actual operation mix and concurrency.
- Background load was high: one-minute load averages ranged from 12.0 to 13.9. Benchmarks ran sequentially.
- Overall speed ratios for individual paired rounds ranged from 1.58× to 2.44×. Read-only ratios ranged from 0.84× to 1.48×.
- Those ranges describe observed variation, not confidence intervals. Treat small timing changes as uncertain.
- Allocation figures cover Go allocations. They do not measure Rust allocations, server memory, or peak retained memory.

## Database operations

| Operation | Release, ms/op | Current, ms/op | Speed ratio | Time saved |
|---|---:|---:|---:|---:|
| `ReadByIID` | 2.303 | 1.674 | 1.38× | 27.3% |
| `ReadByKey` | 1.973 | 1.508 | 1.31× | 23.5% |
| `GetOne` | 2.128 | 1.617 | 1.32× | 24.0% |
| `All256` | 11.836 | 9.449 | 1.25× | 20.2% |
| `WithRoles256` | 17.920 | 17.151 | 1.04× | 4.3% |
| `ExistsBroad256` | 2.571 | 1.611 | 1.60× | 37.3% |
| `PutOne` | 5.830 | 4.112 | 1.42× | 29.5% |
| `Insert16` | 19.326 | 4.758 | 4.06× | 75.4% |
| `Put16` | 52.183 | 6.933 | 7.53× | 86.7% |
| `Update16` | 24.515 | 13.800 | 1.78× | 43.7% |
| `Delete16` | 21.638 | 6.048 | 3.58× | 72.0% |
| `Delete16Strict` | 54.764 | 8.929 | 6.13× | 83.7% |

The 12-case geometric averages show **60% fewer allocated bytes** and **60% fewer allocations** per operation.

Bulk cases show 70% fewer allocated bytes and 74% fewer allocations. Read cases show 49% fewer bytes and 42% fewer allocations.

## Local CPU benchmarks

These results are separate from the database average. A faster parser does not imply an equal improvement in database operations.

| Benchmark | Release, ns/op | Current, ns/op | Speed ratio | Release allocations | Current allocations |
|---|---:|---:|---:|---:|---:|
| `Hydrate_100Rows` | 10,950.00 | 11,087.00 | 0.99× | 101 | 101 |
| `Hydrate_1000Rows` | 112,860.00 | 122,832.00 | 0.92× | 1,001 | 1,001 |
| `Hydrate_10000Rows` | 1,145,729.00 | 1,218,460.00 | 0.94× | 10,001 | 10,001 |
| `Hydrate_SingleRow` | 122.20 | 116.80 | 1.05× | 1 | 1 |
| `ExtractModelInfo_Entity` | 1,632.00 | 1,481.00 | 1.10× | 29 | 29 |
| `ExtractModelInfo_Relation` | 1,098.00 | 1,272.00 | 0.86× | 28 | 28 |
| `UnwrapResult` | 126.30 | 126.30 | 1.00× | 0 | 0 |
| `ReleaseFetchProjection` | 412.80 | 92.72 | 4.45× | 19 | 3 |
| `ReleaseParseSchema/blocks_1` | 263,263.00 | 18,920.00 | 13.91× | 5,009 | 296 |
| `ReleaseParseSchema/blocks_20` | 652,474.00 | 363,694.00 | 1.79× | 9,850 | 5,136 |
| `DecodeMsgpack/flat/reused-keys` | 222,633.00 | 189,914.00 | 1.17× | 7,751 | 4,754 |
| `DecodeMsgpack/nested/reused-keys` | 528,487.00 | 472,647.00 | 1.12× | 19,495 | 13,501 |
| `DecodeMsgpack/wide/reused-keys` | 1,223,894.00 | 992,012.00 | 1.23× | 33,752 | 6,784 |
| `DecodeMsgpack/keys-only/reused-keys` | 152,562.00 | 117,909.00 | 1.29× | 3,751 | 2,752 |
| `DecodeMsgpack/string-value/reused-keys` | 188,980.00 | 137,441.00 | 1.37× | 5,007 | 4,008 |
| `DecodeMsgpackEachKeys/flat/reused-keys` | 140,563.00 | 115,615.00 | 1.22× | 5,747 | 2,750 |
| `DecodeMsgpackEachKeys/nested/reused-keys` | 395,806.00 | 370,752.00 | 1.07× | 17,491 | 13,495 |
| `DecodeMsgpackEachKeys/wide/reused-keys` | 984,806.00 | 825,403.00 | 1.19× | 29,752 | 2,784 |
| `Compiler_CompileBatch` | 1,118.00 | 1,345.00 | 0.83× | 33 | 33 |
| `Compiler_FormatGoValue` | 253.80 | 262.20 | 0.97× | 4 | 4 |

The largest local gains are cached fetch construction and cached parser setup. MessagePack gains depend on row shape.

Some controls ran slower. Hydration at 1,000 and 10,000 rows took about 9% and 6% more time. Relation metadata extraction took 16% more time. AST batch compilation took 20% more time.

The hydration source and benchmark source are unchanged between revisions. These measurements do not establish a regression cause; they show the limits of small timing claims.

## Scope

The comparison covers existing public operations. It does not include optional selected-field reads, typed callbacks, count-free mutations, transaction-scoped reads, or deep role-resolution workloads.

Those features require different workloads or API choices. Their published measurements remain separate from this release average.

## Reproduction and files

- `raw/` contains all 50 process outputs and the commands, timing, and host load for each run.
- `summary.json` contains all 320 measurements, medians, and aggregate calculations.
- `metadata.json` identifies both revisions, the environment, and the current patch hash.
- `current.patch.gz` captures tracked workspace changes. `_workspace-tests/` contains the four new regression test files.
- `_harnesses/` contains the common benchmark sources and fixture used for both revisions.
- `run_compare.py` runs the binaries serially. `analyze.py` calculates the results.

To repeat the comparison, create detached worktrees named `release` and `current` under a temporary directory. Use the commits from `metadata.json`.

Decompress `current.patch.gz` and apply `current.patch` to `current`. Then copy `_workspace-tests/` into that worktree. Copy the common harnesses into both worktrees:

| Saved file | Destination |
|---|---|
| `live_comparison_test.go` | `gotype/release_comparison_test.go` |
| `live_bench_test.go` | `gotype/live_bench_test.go` |
| `local_comparison_test.go` | `gotype/release_local_test.go` |
| `parser_comparison_test.go` | `tqlgen/release_comparison_test.go` |
| `msgpack_bench_test.go` | `driver/msgpack_bench_test.go` |

Build each Rust library with `cargo build --release --manifest-path driver/rust/Cargo.toml` from its worktree. Build four test binaries per revision:

```sh
go test -c -tags cgo,typedb,integration -o ../release-gotype.test ./gotype
go test -c -tags cgo,typedb -o ../release-driver.test ./driver
go test -c -o ../release-tqlgen.test ./tqlgen
go test -c -o ../release-ast.test ./ast
```

Use the `current-` prefix for binaries built in the current worktree. Place both Python scripts beside the binaries and worktrees.

Run TypeDB 3.13.0 on port 1731, then execute:

```sh
python3 run_compare.py live
python3 run_compare.py gotype parser decode ast
python3 analyze.py
```

The runner sets the database address, compose port mapping, and `GOMAXPROCS`. The analysis uses this formula:

```text
speed ratio = median(release ns/op) / median(current ns/op)
average speed ratio = exp(mean(log(speed ratio)))
operation-time reduction = 1 - 1 / average speed ratio
```
