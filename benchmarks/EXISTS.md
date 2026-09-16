# Bounded Exists (#115)

TypeDB 3.13.0, Go 1.27, Apple M4 Pro, 2026-09-16. Five separate live
integration benchmark processes, each with a fresh fixture; each operation
is a query and each case uses `-benchtime=5x -count=1`. Command:

```sh
TEST_DB_ADDRESS=localhost:1730 TYPEDB_GO_COMPOSE_PORT_MAP=1 \
  go test -tags 'cgo,typedb,integration' ./gotype -run '^$' \
  -bench '^BenchmarkLiveExists$' -benchtime=5x -count=1 -benchmem
```

Run the command five times, not `-count=5` in one process: the fixture grows
from 256 to 512 to 1,000 people during each run. The benchmark compares
`Exists` with the full distinct `Count` on an absent name, a unique name,
and a broad age filter. A second five-process sweep gave these ranges
(milliseconds per operation):

| People | Absent Exists / Count | Unique Exists / Count | Broad Exists / Count |
| ---: | --- | --- | --- |
| 256 | 0.83–0.91 / 0.80–0.95 | 0.86–1.04 / 0.96–1.08 | 1.00–1.12 / 1.26–1.47 |
| 512 | 0.77–0.94 / 0.77–0.94 | 0.86–1.15 / 1.14–1.15 | 1.02–1.20 / 1.47–1.69 |
| 1,000 | 0.82–0.98 / 0.88–0.94 | 1.06–1.21 / 1.15–1.23 | 1.12–1.26 / 2.30–2.38 |

The first five-process sweep showed the same broad-query direction. Typical Go
allocation was about 5.6–5.8 KB and 73–75 allocations per operation for
both paths; native and server allocations are not included. These are
directional under shared-host load, not an end-to-end latency guarantee.
