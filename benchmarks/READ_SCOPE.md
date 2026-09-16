# Transaction-scoped reads (#122)

TypeDB 3.13.0, Go 1.27.1, Apple M4 Pro, macOS/arm64, 2026-09-16. Five
independent integration benchmark processes, each with a fresh fixture of
256 people and five companies. One goroutine alternates person and company
IID reads. Each case uses five timed operations (`-benchtime=5x -count=1`).
The complete-scope cases include transaction open and caller-visible close;
the shared scope serializes reads on the same transaction. The per-read case
omits the shared scope's initial open and final close.

```sh
TEST_DB_ADDRESS=localhost:1730 TYPEDB_GO_COMPOSE_PORT_MAP=1 \
  go test -tags 'cgo,typedb,integration' ./gotype -run '^$' \
  -bench '^BenchmarkLiveReadScope(PerRead)?$' -benchtime=5x -count=1 -benchmem
```

Run five separate processes rather than `-count=5` in one process, so each
gets a fresh database fixture. Arithmetic means of the five samples:

| Reads per complete scope | Fresh tx per read | Shared read tx |
| ---: | ---: | ---: |
| 2 | 1.880 ms; 10,294 B; 164 allocs | 1.573 ms; 9,176 B; 149 allocs |
| 5 | 5.458 ms; 24,188 B; 414 allocs | 3.148 ms; 18,995 B; 344 allocs |
| 10 | 10.745 ms; 46,348 B; 813 allocs | 5.544 ms; 34,559 B; 653 allocs |

The deliberately incomplete per-read comparison measured 1.079 ms,
5,864 B and 84 allocations for fresh, versus 0.508 ms, 4,602 B and 67
allocations for shared. Complete-scope timings include caller-visible close
but not asynchronous native cleanup drain. Go allocation figures exclude
Rust and server memory. The host was shared and each series has only five
short timed operations per process, so timing differences are directional;
scope lifetime and snapshot consistency remain the primary design choices.
