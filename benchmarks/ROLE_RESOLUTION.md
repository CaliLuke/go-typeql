# Role-player lookup investigation (#134)

Five repetitions of 48 benchmark cases (`go test ./tqlgen -run '^$'
-bench '^BenchmarkRolePlayerResolution$' -benchtime=1000x -count=5 -benchmem`),
on Go 1.27.1, macOS/arm64, Apple M4 Pro, 2026-09-16. These are fresh results
on the upstream streaming branch; the earlier isolated branch's SQLite run 29
is not part of this branch's database history. No database, CGo, TypeDB server, schema parser, template execution,
or Go source formatter is in the timed section. Existing renderer construction
is outside timing for both variants. Each operation looks up all roles for one
relation. The indexed alternative evaluates the production threshold and builds
a fresh per-render map before the lookups, even for cases that would not pass
the threshold; the baseline scans the original declarations. Both use the
same relation-inheritance walk. The fixture has 16/64/256 entities (one
unrelated `plays` declaration each), 1/4/16/32 queried roles, and relation
inheritance depth 1/4. Matches are on the last entity and the root relation,
so this is a late-match stress case, not a representative schema sample.

| Entities / roles / depth | Scan ns/op | Index ns/op | Scan allocations | Index bytes / allocations |
| --- | ---: | ---: | ---: | ---: |
| 16 / 1 / 1 | 115 | 1,802 | 0 | 3,224 / 7 |
| 64 / 16 / 1 | 2,310 | 5,421 | 0 | 13,016 / 11 |
| 64 / 16 / 4 | 10,664 | 6,862 | 0 | 13,016 / 11 |
| 64 / 32 / 4 | 24,846 | 10,125 | 0 | 13,016 / 11 |
| 256 / 16 / 1 | 7,625 | 19,496 | 0 | 53,912 / 15 |
| 256 / 16 / 4 | 32,339 | 20,734 | 0 | 53,912 / 15 |
| 256 / 32 / 4 | 67,920 | 23,616 | 0 | 53,912 / 15 |

Values are arithmetic means of five same-process repetitions. The index is enabled only for
at least 64 entities and a relation declaring at least 16 roles at inheritance
depth four or more, matching the measured wins. Skipped abstract relations
do not trigger it. Other schemas continue using the allocation-free scan;
the benchmark baseline omits the production threshold check, whereas the
index variant includes it. An early matching player could still make the
indexed path slower despite that gate. This threshold is conservative, not a
general claim about end-to-end generation speed. Code generation normally
also parses, executes templates, and formats Go, so these microsecond gains
may be immaterial in the complete workflow. The tradeoff includes temporary
map allocation per render. Regression tests compare indexed and scanned
relation contexts and warnings for first-match, relation players, renamed and
inherited roles, and unresolved roles.
