# Async Transaction Close Benchmark Results

These results support the `v1.10.1` async transaction close change. They were run on
darwin/arm64 with an Apple M4 Pro against TypeDB on `localhost:1730`.

Benchmark command:

```bash
TEST_DB_ADDRESS=localhost:1730 go test -tags "cgo,typedb,integration" ./gotype \
  -run '^$' \
  -bench '^(BenchmarkLiveRead_GetByIID|BenchmarkLiveRead_Get|BenchmarkLiveRead_All|BenchmarkLiveRead_GetWithRoles|BenchmarkLiveRead_CloseOnly|BenchmarkLiveRead_CloseCheckedOnly|BenchmarkLiveRead_GetByIIDBreakdown|BenchmarkLiveClose_ChannelEnqueue|BenchmarkLiveClose_GoroutinePerClose)$' \
  -benchtime=20x -count=1 -benchmem
```

## Key Revision Comparison

| Revision | Meaning | GetByIID ns/op | Get ns/op | All ns/op | GetWithRoles ns/op | caller close in IID breakdown | close % |
| --- | --- | ---: | ---: | ---: | ---: | ---: | ---: |
| `7c21106` | `v1.8.1` | 896,648 | 891,021 | 1,591,888 | 1,017,254 | 3,644 | 0.33% |
| `c5299f6` | commit before sync close | 967,858 | 1,001,104 | 1,810,781 | 1,265,481 | 4,121 | 0.51% |
| `16b15c1` | `txn.close().resolve()` introduced | 1,651,579 | 1,387,890 | 2,355,615 | 1,560,685 | 328,860 | 22.86% |
| `490c6c1` | `v1.9.0` | 1,470,600 | 1,397,956 | 2,150,652 | 1,522,598 | 342,919 | 22.93% |
| current patch | async close | 1,032,356 | 1,212,121 | 2,040,394 | 1,231,108 | 3,361 | 0.29% |

`16b15c1` is the first revision where close becomes a material part of caller-visible
`GetByIID` latency. The caller-visible close portion jumps from roughly 3-4 us to
roughly 329 us in the targeted breakdown.

## Close Cost Comparison

| Benchmark | ns/op | B/op | allocs/op |
| --- | ---: | ---: | ---: |
| `v1.9.0` open + synchronous `Close()` | 1,030,073 | 1,136 | 16 |
| current patch open + caller-fast `Close()` | 498,104 | 1,207 | 16 |
| current patch open + `CloseChecked()` | 981,771 | 1,152 | 16 |
| current patch `GetByIID` caller-visible close | 3,361 | n/a | n/a |
| Go channel enqueue baseline | 22.90 | 0 | 0 |
| Go goroutine-per-close baseline | 472.9 | 472 | 1 |

The bounded worker path restores caller-visible close timing to the same order of
magnitude as the pre-`16b15c1` drop path while preserving checked close through
`CloseChecked()`.

## Current Patch Raw Output

```text
goos: darwin
goarch: arm64
pkg: github.com/CaliLuke/go-typeql/gotype
cpu: Apple M4 Pro
BenchmarkLiveRead_GetByIID-14                    20   1032356 ns/op    5008 B/op    86 allocs/op
BenchmarkLiveRead_Get-14                         20   1212121 ns/op    5007 B/op    88 allocs/op
BenchmarkLiveRead_All-14                         20   2040394 ns/op   20816 B/op   427 allocs/op
BenchmarkLiveRead_GetWithRoles-14                20   1231108 ns/op    8548 B/op   142 allocs/op
BenchmarkLiveRead_CloseOnly-14                   20    498104 ns/op    1207 B/op    16 allocs/op
BenchmarkLiveRead_CloseCheckedOnly-14            20    981771 ns/op    1152 B/op    16 allocs/op
BenchmarkLiveRead_GetByIIDBreakdown-14           20   1163862 ns/op    3361 close-ns/op    0.2888 close-pct    544448 open-ns/op    615744 query-ns/op    3589 B/op    55 allocs/op
BenchmarkLiveClose_ChannelEnqueue-14             20        22.90 ns/op     0 B/op     0 allocs/op
BenchmarkLiveClose_GoroutinePerClose-14          20       472.9 ns/op    472 B/op     1 allocs/op
PASS
ok      github.com/CaliLuke/go-typeql/gotype  1.162s
```
