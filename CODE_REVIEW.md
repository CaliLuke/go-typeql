# go-typeql Code Review

Triage of improvement opportunities across `ast/`, `gotype/`, `tqlgen/`, `driver/`.
Findings are grouped by severity with file:line refs and one-line recommendations.
Section 1 ("Top issues") is the highest-signal list; later sections are broader but lower priority.

## 1. Top issues

### 1.1 `decodeMsgpack` allocates per query

`driver/transaction.go:193–202`: `C.GoBytes` (full copy),
`bytes.NewReader`, `msgpack.NewDecoder` (encoder config), `Decode`. For
small result sets this is pure overhead. Options: (a) use
`msgpack.Unmarshal` directly against a `C.GoBytes` slice, skipping the
Reader; (b) keep a `sync.Pool` of decoders; (c) use
`unsafe.Slice((*byte)(unsafe.Pointer(buf)), int(outLen))` when the
decoder promises not to retain the bytes (it doesn't — msgpack-v5 can
alias strings via zero-copy), so stick with the copy but skip the Reader.

### 1.2 Hot-path query builders use `fmt.Sprintf` + `strings.Join` + `[]string` accumulation

`gotype/query.go:119–169, 222–260, 308–330, 417–476`,
`gotype/crud.go:680–691`, `gotype/filter.go` (~30 call sites). Every
call path that produces a query slices-up little `fmt.Sprintf` results,
appends them, and joins. For a single read this is fine; under load
(e.g. an HTTP handler firing one query per request) it's a lot of
garbage. A single `strings.Builder` threaded through the builder chain
would cut allocations by ~4–10× on measured paths.

Related: `query.go:131–165` (`buildQuery`) performs the
double-build dance — `match := ...buildMatchClause()`, then inside
the `if len(q.orderBy) > 0` branch `match +=` mutates it, then
`parts = []string{match}` discards the initially-appended version.
Refactor to build a single time.

### 1.3 `extractFieldValues` allocates a 1-slot slice per scalar field

`gotype/strategy.go:423–445`: the common case (scalar, non-slice) goes
through `return []any{val}`. Callers iterate the slice with one element.
Split the API into scalar and slice versions, or return `(val any, extras []any)`.
Hit on every insert and every update for every field — easy win.

## 2. Correctness — secondary

### 2.1 `Query.Update` + `Query.Delete` return fake counts

`query.go:114` returns `-1`, `query.go:259` returns `-1`. The doc comment
says "or -1 if the count is unknown" — but TypeDB can return the match
count via `reduce $c = count($e);` in the same tx. Either plumb that
through or change the signature to `error` (not `(int64, error)`) to
stop misleading callers.

### 2.2 `getIIDOf` / `setIIDOn` do a registry lookup per call

`crud.go:710–727`: both helpers do `LookupType(v.Type())` every call,
even though `Manager[T]` already has `m.info` cached. In a tight insert
loop this is one `sync.RWMutex` acquisition per row per field. Either
pass `info` in or add a fast-path.

### 2.3 `Database.Transaction()` has no ctx parameter

`session.go:119–121`: callers pass `ctx` to `ExecuteWrite`, which then
calls `Transaction(...)` — but transaction-open itself isn't
ctx-aware. The pool adapter confirms this:
`pool.go:388` passes `context.Background()` to `pool.Get`. If the pool
is saturated, the caller's deadline is ignored until a connection frees.
This is a breaking API change; consider `TransactionCtx(ctx, txType)`.

### 2.4 `ExecuteWrite` has no rollback on commit failure

`session.go:138–140`: on `tx.Commit()` error, the deferred `tx.Close()`
runs — but because the Rust driver consumes the transaction on commit
(`transaction.go:218` nils the ptr), `Close` is a no-op. There's nothing
to roll back (driver state is already half-gone), so the behavior is
correct but the ergonomics are confusing. Document that a failed commit
leaves the tx unusable and no rollback is possible.

### 2.5 Pool: `IsOpen()` under pool mutex

`pool.go:133`: the pool acquires `p.mu` and then calls
`pc.conn.IsOpen()`, which makes an FFI call. Every `Get` stalls
every other `Get`/`Put` for a network-bound check. Move validation
outside the lock (pop the conn under lock, validate unlocked, re-try
if dead).

### 2.6 `InsertMany` / `PutMany` never set IID under partial failure

`crud.go:533–570`, `crud.go:473–517`: if item N fails, items [0,N-1] were
already successfully queried but the commit hasn't run yet. Current code
bails — OK. But IIDs for successful items were already parsed from the
fetch result and set on the instance (`crud.go:558–562`). The caller
gets mutated instances with IIDs for a transaction that will never
commit. Either clear the IIDs on error or delay `setIIDOn` until after
commit.

## 3. Debuggability

### 3.1 Seven-way `logFFIDuration` call sites in `QueryWithOptions`

`driver/transaction.go:74–114`: every return path ends in a long
key/value list that must be kept in sync by hand. Drift is inevitable.
Wrap with:

```go
defer func(start time.Time) {
    logFFIDuration("tx.query", start, ...common..., "result", status, "error", errStr, "rows", rowCount, "bytes", byteCount)
}(time.Now())
```

…and assign `status`, `errStr`, `rowCount`, `byteCount` once per path.

### 3.2 `TransactionContext` leak warning is log-only

`session.go:175–178`: only printed to the Go `log` package. Easy to miss
under JSON-logger setups. Surface as a metric/counter (you already have
`incActiveTxOpen` in driver) and consider panicking in test builds
behind a `-tags=strict_tx` build tag.

### 3.3 Panic-on-unregistered in `NewManager`

`crud.go:33, 54`: constructing a manager for an unregistered type panics.
Fine for startup; bad during a request. Return `(*Manager[T], error)`
and let callers choose.

### 3.4 Error messages often lack the query string

When a TypeQL compile/execute fails, the returned error has "update
person: &lt;driver error&gt;" but no query text. During development this is
the #1 time-waster. Gate a `DEBUG`-level log (or optional
`err.(*DriverError).Query`) to include the offending query.

## 4. Speed of development

### 4.1 `ModelStrategy` interface is too wide and inconsistent

10 methods, with `BuildFetchWithRoles` returning two strings and every
other method returning one. Entity's `BuildFetchWithRoles` just delegates
to `BuildFetchAll` with `""`. Collapse: either have every method return
`(matchAdditions, fetchClause string, err error)` or split into
`InsertBuilder`, `MatchBuilder`, `FetchBuilder` interfaces that strategies
compose. Current shape forces every new operation to touch 2 types.

### 4.2 Duplicated fetch-item construction

`strategy.go:132–152`, `294–314`, `326–351`, `crud.go:769–801`, and
`buildFetchAllWithType` at `strategy.go:332–351` all implement the same
"emit fetch items for info.Fields" loop. Promote to one helper.

### 4.3 `Delete` vs `DeleteMany` / `Update` vs `UpdateMany` / `Put` vs `PutMany`

Each pair duplicates the tx + validation + commit scaffold with minor
variation. A `runBatch(tx, items, per func(Tx, *T, int) error)` helper
could halve the file size of `crud.go`.

### 4.4 Ad-hoc `queryOperation` / `queryFingerprint` called twice

`driver/transaction.go:75, 76, 123, 124`: computed twice per query-with-
context (once for early-cancelled log, once in the goroutine). Compute
once and pass through.

## 5. Performance — smaller

- `ast.Compiler{}` is allocated on every call to `BuildX` in `strategy.go`.
  The compiler is stateless; make it a package-level `var defaultCompiler = &Compiler{}`.
- `coerceTimeFast` (`hydrate.go:465`) loops three layouts on every time
  field. Cache the last-successful layout per ModelInfo field.
- `Registry.LookupByGoName` (`registry.go:142–151`) linear-scans and
  lowercases every entry per call. Build a secondary index at register time.
- `validateModelNames` (`registry.go:74–111`) runs `IsReservedWord` +
  `ValidateIdentifier` on every field of every register call; if
  `reserved.go` uses a slice scan, replace with `map[string]struct{}`.

## 6. Low-priority / nits

- `gotype/filter.go:32–41`: `EqualsFilter` builds `$x == val` even for
  non-scalar `FormatValue(x)`. If `val` is a struct, this silently
  produces an invalid query.
- `ast/compiler.go:31`: `"unknown node type: %T"` — include the node's
  position/context if available (currently AST has no position info;
  adding that would pay for itself at the tqlgen level).
- `tqlgen`: not reviewed in depth here; flag for a follow-up pass.
- `driver/rust/`: not reviewed.

## 7. What's good

For contrast — these are well-done and worth preserving when refactoring:

- The `Conn` / `Tx` interface split in `gotype/session.go` cleanly isolates
  CGo. This is the single largest factor making the codebase testable.
- Registry reflection is done once at registration and cached in
  `ModelInfo`; hot-path code reads the cache.
- `hydrate.go` fast-path scalar setters (`trySetScalarField`) avoid
  the reflect-based fallback for the common case.
- The `pooledTx.once` guard correctly prevents double-put on commit-then-close.
- `Hydrate` cycle detection via visited-set + `MaxHydrationDepth`.
- Strategy pattern keeps entity/relation divergence localized.

## 8. Suggested ordering for fixes

1. **2.6** (partial-success mutation) — small and correctness-sensitive.
2. **1.1, 1.3** (perf quick wins) — contained, benchmarkable.
3. **2.5** (pool concurrency) — bug potential grows with adoption.
4. **3.1, 4.1, 4.3** (refactors) — do after the above to avoid merge pain.
