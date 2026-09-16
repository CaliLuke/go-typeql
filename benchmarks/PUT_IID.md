# Put IID query (#116)

TypeDB 3.13.0, Go 1.27.1, Apple M4 Pro, 2026-09-16. Five independent live
benchmark processes with fresh fixtures; ten keyed-entity inserts per case
in each process. Run five times with:

```sh
TEST_DB_ADDRESS=localhost:1730 TYPEDB_GO_COMPOSE_PORT_MAP=1 \
  go test -tags 'cgo,typedb,integration' ./gotype -run '^$' \
  -bench '^BenchmarkLivePut$' -benchtime=10x -count=1 -benchmem
```

Arithmetic means of five process samples:

| Path | Queries/op | ns/op | Go B/op | Go allocs/op |
| --- | ---: | ---: | ---: | ---: |
| Separate put and key-match IID fetch | 2 | 2,593,289 | 7,966 | 144 |
| Put with inline IID fetch | 1 | 2,244,627 | 5,447 | 95 |

The legacy benchmark helper omits public `Put` key validation, so allocations
are not a fully matched comparison. The query-count reduction is exact for
this keyed insertion workload. Latency remains directional under shared-host
load; Rust and server allocations are not included. Keyless relations do not
fetch an IID through either path.
