# go-typeql Code Review

Triage of improvement opportunities across `ast/`, `gotype/`, `tqlgen/`, `driver/`.
Findings are grouped by severity with file:line refs and one-line recommendations.
Section 1 ("Top issues") is the highest-signal list; later sections are broader but lower priority.

## 1. Top issues

No remaining open top-priority issues.

## 2. Correctness — secondary

## 3. Debuggability

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

No remaining explicit ordering; the open items are mostly debuggability and lower-priority cleanup.
