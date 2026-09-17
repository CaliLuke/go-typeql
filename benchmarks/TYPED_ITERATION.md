# Typed iteration measurements (#121)

Run 29 in `benchmarks.sqlite` records five fresh-process samples per case. Run 28 is a preliminary sample with a superseded heap metric. The test used Go 1.27.1, Apple M4 Pro, and TypeDB 3.13.0 under Colima.

Each timed operation reads all 256 `live-bench-person` entities from one disposable database. `All` returns 256 model pointers. `ForEach` delivers 256 model pointers to a callback that does not retain them. Both use the same TypeQL fetch and transaction path. The measured first-model time for `All` is when the full slice returns; for `ForEach` it is when the first callback starts.

| Read | Time/op | First model | Go B/op | Go allocs/op | Peak live Go heap |
| --- | ---: | ---: | ---: | ---: | ---: |
| `All` | 7.64 ms | 7.64 ms | 51,174 | 2,088 | 720,344 B |
| `ForEach` | 7.60 ms | 7.49 ms | 48,800 | 2,088 | 692,854 B |

The heap measurement is a separate pass after each timed case. It calls `runtime.GC` before the read, then samples live Go heap after `All` returns or every 32 callback deliveries. The average baseline live heap was 685,642 B for `All` and 692,854 B for `ForEach`. `All` retained about 34.7 KB above its baseline. The `ForEach` samples did not rise above their baseline after collection. These small heap differences are sensitive to runtime and fixture noise; they do not measure Rust or server memory.

The callback path is not meaningfully faster on this 256-row fixture. The driver returns these rows in one chunk, so the first callback starts close to full-read completion. The useful difference is ownership: the ORM does not accumulate a `[]*T` during `ForEach`. A consumer that retains every callback model can still use comparable memory to `All`.
