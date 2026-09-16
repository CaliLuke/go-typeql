# GetOne uniqueness investigation (#129)

`Manager.GetOne` delegates to `Get`: it transfers and hydrates every answer
row, then returns `NotFoundError` at zero or `NotUniqueError{Count: len(rows)}`
above one. `Count` is an **exact answer-row count**, not the number of
distinct instance IIDs. A mock regression demonstrates that two identical
answer rows for one IID still produce `Count=2`, inside the bound transaction.
Changing to `Query.Count` changes that contract because `Count` deduplicates
instances. A two-row cap also changes the count for three or more matches.
The repository has no non-test `GetOne` callers; it is a public API and the
error message includes the count, so external callers can depend on it.

Repeated-row run 17 uses a synthetic transaction returning 25 copies of one
IID, with a distinct-count response of 1 and a two-row limited response.
Across five samples of 100 iterations, current GetOne took 3,604 ns/op,
2,317 B/op, 42 allocs/op; count-first followed by GetOne took 4,173 ns/op,
3,532 B/op, 68 allocs/op but still ended in `NotUniqueError{Count:25}`;
an experimental bounded-uniqueness helper took 1,225 ns/op, 1,139 B/op,
31 allocs/op and returned `NotUniqueError{Count:2}`. This isolates Go-side query construction and hydration, not TypeDB planning, network transfer,
or the cost of server-side limiting. It illustrates the semantic difference
and possible Go-side savings; the live cases below measure
real query costs on distinct matches.

Run 16 of `benchmarks.sqlite` records five independent processes, 20
iterations per series, TypeDB 3.13.0, Go 1.27.1, macOS/arm64 Apple M4 Pro.
Each process has 283 people: the standard fixture's 256 distinct ages
(30–285) plus 2 with age 300 and 25 with age 400. Cases match zero, one,
two, or 25 people.
`current` calls GetOne. `count-first` runs a distinct count, then GetOne only
for a single match (two queries); `bounded-two` hydrates at most two results
and maps them to not-found, success, or a capped `NotUniqueError{Count:2}`.
It deliberately cannot report an exact count above two. These last two are
experimental comparisons, not public replacement APIs.

| Matches | Current ns/op, B/op, allocs/op | Count-first | Bounded-two |
| ---: | ---: | ---: | ---: |
| 0 | 965k, 1,140, 33 | 1,126k, 2,402, 44 | 1,051k, 1,721, 46 |
| 1 | 1,160k, 1,829, 46 | 2,242k, 3,987, 88 | 1,165k, 2,391, 58 |
| 2 | 1,221k, 2,074, 57 | 1,106k, 2,414, 45 | 1,209k, 2,637, 70 |
| 25 | 1,920k, 6,677, 241 | 1,112k, 2,401, 44 | 1,197k, 2,636, 70 |

All variants issue one query except count-first with one match (two). The
count-first benchmark uses separate read transactions for its two calls;
a production count-then-fetch would need one transaction for a coherent
snapshot. It still cannot preserve GetOne's exact answer-row count if a
filter creates repeated rows. Bounded-two saves work on large non-unique
results but lacks the existing exact-count contract; it needs an explicitly
different public API/error semantics and a duplicate-row policy. The bounded
prototype uses `Query.Filter` with a bound attribute variable whereas GetOne
uses a direct literal match, so the live timing comparison also includes
query-shape and planning differences; it does not isolate the cost of `limit 2`.
There is no
justified transparent replacement or general default threshold, so GetOne
remains unchanged. Callers that only need any result can use `Query.First`,
and callers that want distinct counts can use `Query.Count` explicitly.
The bounded form uses the existing Query filter/limit syntax covered by the
official `typeql-check` syntax tests.

The host was shared with other containers and had a 6.25/5.53/6.32 load
average after this run. Timing is directional, especially sub-millisecond
differences. Go B/op excludes Rust and server memory; no server planning or
network transfer counters were captured.
