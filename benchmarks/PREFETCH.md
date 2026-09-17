# FFI stream chunk and server prefetch investigation (#131)

The current driver has a pull-based native stream. `QueryEachWithContext`
requests at most 256 rows per FFI chunk by default; the server's independent
prefetch setting has its own default. `Query` still materializes the full
result and is not the path measured here. This investigation uses an internal
benchmark-only configuration to sweep FFI row limits 32/128/256 against
server prefetch default/1/256 without changing the public API or defaults.

The `prefetch` benchmark group uses four fixture shapes: 64 narrow (16-byte)
names, 64 wide (512-byte) names, 256 nested documents with narrow names, and
512 wide names. Each case reports total ns/op, time to first callback, Go
B/op and allocs/op, actual encoded FFI buffer bytes and largest single chunk,
Go heap in use, and process peak RSS. A 512-row early-stop case returns after
one callback for each chunk size. FFI chunk size is a row-count cap, not a
byte cap; the per-chunk encoded size varies with row width. Process peak RSS
is a process-wide high-water mark (including fixture setup and earlier cases),
not isolated native memory. Go B/op excludes Rust and server memory.

Runs 18 and 19 in `benchmarks.sqlite` contain five fresh-process samples per
series on TypeDB 3.13.0, Go 1.27.1, macOS/arm64 Apple M4 Pro: 39 local cases
and 24 controlled socket-delay cases. Each completed operation opens a read
transaction, streams the result, and waits for checked native close. The
early-stop cases close after the first callback. Selected means (ms/op):

| 512 wide names | Chunk 32 | Chunk 256 |
| --- | ---: | ---: |
| Local, default prefetch | 9.70 | 9.15 |
| Local, prefetch 1 | 185.87 | 181.98 |
| Local, prefetch 256 | 4.55 | 4.39 |
| Stop after first row, default | 2.33 | 5.55 |
| Proxy 0 ms/read, default | 11.77 | 10.65 |
| Proxy 2 ms/read, default | 31.10 | 39.27 |

On the local 512-wide default path, chunk 32 reached the first callback at
1.35 ms with a largest encoded FFI buffer of 16,677 bytes; chunk 256 reached
it at 4.46 ms with a 133,381-byte buffer. Both completed paths transferred
roughly 267 KB/op across FFI. The early-stop path transferred one full chunk:
16,677 or 133,381 encoded bytes, not just one row. The untimed heap replay
sampled about 4.4 MB peak Go heap-in-use on completed large reads, but the
sample includes fixture setup and is not native-buffer memory. Go B/op for
the completed local default large cases was 278 KB/1,145 allocs (chunk 32)
versus 272 KB/1,047 allocs (chunk 256). The full 39-series run includes
64-row narrow/wide and 256-row nested cases, plus first-row, buffer, heap,
and RSS metrics for every series.

The delay comparison uses a benchmark-local TCP proxy. It delays each socket
read by 0 or 2 ms in both directions, not a controlled RTT, and can change
packet batching as well as latency. In particular, prefetch-1 with the
2-ms proxy was sometimes faster than with its 0-ms proxy; do not infer a WAN
prefetch default from these local measurements. The host also ran other
containers, including a separate TypeDB instance, with load average
4.79/4.99/5.47 after recording. Treat ns/op differences as directional.
Process RSS is a high-water mark across all cases in one process; the
sampled Go heap is an untimed replay and may miss a shorter-lived peak.

There is a genuine tradeoff: smaller chunks deliver the first row sooner and
transfer less on early stop, while the current 256-row limit uses fewer Go
allocations for complete reads. Server prefetch 1 was much slower on local
full reads, whereas explicit 256 helped in these fixtures; that does not
justify a global default change without representative latency and server
load. No public option or default changed. An explicit chunk option should
be considered only for callers that need first-row/early-stop control, with
byte-based backpressure and callback ownership documented separately.
