# Grouped DeleteMany (#118)

TypeDB 3.13.0, Go 1.27.1, Apple M4 Pro, 2026-09-16. Five independent live
processes with fresh fixtures; each timed operation deletes 16 newly seeded
entities. Seed insertion is excluded from timing. Each case uses
`-benchtime=5x -count=1`; run the following five times:

```sh
TEST_DB_ADDRESS=localhost:1730 TYPEDB_GO_COMPOSE_PORT_MAP=1 \
  go test -tags 'cgo,typedb,integration' ./gotype -run '^$' \
  -bench '^BenchmarkLiveDeleteMany$' -benchtime=5x -count=1 -benchmem
```

Arithmetic means of five samples:

| Mode | Path | Queries/op | ms/op | Go B/op | Go allocs/op |
| --- | --- | ---: | ---: | ---: | ---: |
| Non-strict | Per-instance | 16 | 9.92 | 29,622 | 564 |
| Non-strict | Grouped | 1 | 3.47 | 13,761 | 146 |
| Strict | Per-instance checks/writes | 32 | 27.27 | 85,963 | 1,446 |
| Strict | Grouped checks/writes | 2 | 5.11 | 31,750 | 337 |

The 32-IID bound means larger inputs use multiple queries per phase. Strict
checks run before the write transaction; concurrent changes between checking
and deleting retain the existing non-atomic strict behavior. Timing is
directional on a shared host. Go allocation excludes Rust and server memory.
