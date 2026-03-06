---
name: go-1-25-review
description: Review and refactor Go code to leverage Go 1.25 features, new standard-library APIs, and new tooling guidance. Use when auditing a Go codebase after upgrading to Go 1.25, during code review, when modernizing concurrency/tests/http/logging code, or when looking for concrete places to replace older patterns with Go 1.25 capabilities.
---

# Go 1.25 Review

Use this skill to find actionable Go 1.25 adoption opportunities during review or refactor work. Prioritize changes that improve correctness, clarity, safety, or testability. Do not force mechanical churn for features that add no material value.

## Review Workflow

1. Confirm the package actually targets Go 1.25 or that the user explicitly wants forward-looking recommendations.
2. Scan for high-value adoption candidates first:
   - goroutine launch patterns that can use `sync.WaitGroup.Go`
   - flaky concurrency tests that can use `testing/synctest`
   - unsafe string-built network addresses that should use `net.JoinHostPort`
   - browser-facing unsafe HTTP mutation endpoints that may benefit from `net/http.CrossOriginProtection`
   - verbose `slog` attr-slice grouping that can use `slog.GroupAttrs`
   - reflection-heavy type assertions that can use `reflect.TypeAssert`
3. Check for correctness hazards newly exposed by Go 1.25:
   - use of values before checking `err`, especially nilable results from constructors/open calls
   - fragile `unsafe.Pointer` patterns that may break because more slice backing arrays now stack-allocate
4. Recommend toolchain and operational follow-ups where relevant:
   - `go vet` analyzers `waitgroup` and `hostport`
   - container-aware `GOMAXPROCS` behavior
   - `runtime/trace.FlightRecorder` for rare production incidents
5. Keep recommendations ranked:
   - correctness and security first
   - testability and observability second
   - readability and ergonomics third
   - speculative micro-optimizations last

## Refactor Rules

- Prefer refactors that remove custom code or common footguns.
- Preserve existing architecture and repo invariants.
- Avoid adding `GOEXPERIMENT` dependencies unless the user explicitly wants experimental adoption.
- Treat performance-only suggestions as opt-in unless there is evidence they matter here.
- When recommending a change, state:
  - the old pattern
  - the Go 1.25 replacement
  - the concrete benefit
  - any migration caveat

## High-Value Features

Read [go-1-25-features.md](./references/go-1-25-features.md) when you need the detailed mapping from old patterns to Go 1.25 APIs, including search heuristics and review prompts.

Use this shortlist first:

- `sync.WaitGroup.Go`: Replace manual `Add(1)` + `go func(){ defer Done() ... }()` when the wrapper adds no real behavior.
- `testing/synctest`: Rewrite timing-sensitive concurrent tests that currently depend on sleeps, polling, or race-prone coordination.
- `net/http.CrossOriginProtection`: Consider for browser-exposed state-changing endpoints that currently lack CSRF protection and do not rely on legacy token schemes.
- `runtime/trace.FlightRecorder`: Add for debugging rare production stalls, deadlocks, or latency spikes where continuous tracing is too expensive.
- `slog.GroupAttrs`: Use when building slog groups from a precomputed `[]slog.Attr`.
- `reflect.TypeAssert`: Use in hot or allocation-sensitive reflection paths that currently do `v.Interface().(T)`.
- `mime/multipart.FileContentDisposition`: Replace handwritten multipart file content-disposition formatting.
- `io/fs.ReadLinkFS` support across `os`, `archive/tar`, and `testing/fstest`: Simplify symlink-aware filesystem logic and tests.
- `crypto.SignMessage` and `crypto.MessageSigner`: Use when supporting signers that hash internally.

## Review Prompts

Ask these while reviewing:

- Is this manual concurrency plumbing now expressible with `WaitGroup.Go`?
- Is this concurrent test fighting the scheduler instead of using `testing/synctest`?
- Is this host/port string construction unsafe for IPv6?
- Is this browser-facing HTTP surface missing a simpler CSRF defense?
- Is this logging code assembling attr groups in a clumsy way?
- Is this reflection code paying allocation cost to get back to a concrete type?
- Is this production debugging path a candidate for `FlightRecorder` instead of ad hoc logging?
- Is this code incorrect under Go 1.25 because it touches a nilable result before checking `err`?

## Non-Adoption Notes

Some Go 1.25 changes are important for review but not usually reasons to refactor:

- No language change requires source rewrites.
- Container-aware `GOMAXPROCS` is mostly an operational/runtime behavior change.
- New GC (`GOEXPERIMENT=greenteagc`) and JSON v2 (`GOEXPERIMENT=jsonv2`) are experiments; suggest trials, not broad adoption, unless explicitly requested.
- DWARF5, linker alignment, VMA names, and many crypto/runtime speedups are mostly automatic wins.

## Validation

When you make Go 1.25-driven code changes, run the normal repo gates plus targeted checks:

```bash
go test ./...
golangci-lint run ./...
go vet ./...
```

If the review specifically touched concurrency or networking patterns, pay special attention to vet output from the `waitgroup` and `hostport` analyzers.
