---
name: go-1-26-review
description: Review and refactor Go code to leverage Go 1.26 features, new language and standard-library APIs, modern `go fix` workflows, and newly deprecated or tightened behaviors. Use when auditing a Go codebase after upgrading to Go 1.26, during code review, when modernizing error handling/logging/testing/reflection/proxy code, or when looking for concrete places to replace older patterns with Go 1.26 capabilities.
---

# Go 1.26 Review

Use this skill to find actionable Go 1.26 adoption opportunities during review or refactor work. Prioritize changes that improve correctness, clarity, safety, maintainability, or test ergonomics. Do not create churn just to mention every release-note item.

## Review Workflow

1. Confirm the package actually targets Go 1.26 or that the user explicitly wants forward-looking recommendations.
2. Start with the highest-value adoption points:
   - run or consider `go fix` modernizers before doing manual cleanup
   - replace pointer-helper boilerplate with `new(expr)` where it materially improves clarity
   - replace `errors.As` target boilerplate with `errors.AsType`
   - replace custom fan-out slog handlers with `slog.NewMultiHandler`
   - replace unsafe `httputil.ReverseProxy.Director` usage with `ReverseProxy.Rewrite`
   - replace ad hoc benchmark loops with clean `b.Loop` usage where relevant
3. Check for deprecations, tightened validation, or behavioral changes that should influence refactors:
   - deprecated `ReverseProxy.Director`
   - deprecated unsafe RSA PKCS #1 v1.5 encryption padding
   - stricter URL parsing with malformed host colons
   - crypto APIs that no longer honor custom randomness parameters
4. Recommend operational or diagnostic follow-ups when relevant:
   - new default Green Tea GC behavior
   - goroutine leak profile experiment
   - new scheduler metrics in `runtime/metrics`
   - `go fix` as the first pass for modernizations
5. Keep recommendations ranked:
   - correctness and security first
   - maintainability and tooling second
   - testability and observability third
   - stylistic cleanups last

## Refactor Rules

- Prefer refactors that remove bespoke code, unsafe hooks, or error-prone boilerplate.
- Preserve existing architecture and repo invariants.
- Avoid introducing experiments unless the user explicitly wants experimental adoption.
- Treat automatic runtime/compiler wins as context, not as reasons to rewrite source.
- When recommending a change, state:
  - the old pattern
  - the Go 1.26 replacement
  - the concrete benefit
  - any caveat or rollout risk

## High-Value Features

Read [go-1-26-features.md](./references/go-1-26-features.md) when you need the detailed mapping from old patterns to Go 1.26 APIs, including search heuristics and review prompts.

Use this shortlist first:

- `go fix`: Run or inspect modernizers before hand-editing old idioms.
- `new(expr)`: Replace local helper closures or temp variables used only to take pointers to optional scalar values.
- `errors.AsType`: Replace `var target *T; if errors.As(err, &target)` boilerplate with typed extraction.
- `slog.NewMultiHandler`: Replace custom "fan out to multiple handlers" plumbing.
- `httputil.ReverseProxy.Rewrite`: Migrate away from deprecated and unsafe `Director`.
- `testing.T.ArtifactDir`: Use for tests that emit files or diagnostic artifacts.
- `testing/cryptotest.SetGlobalRandom`: Use in tests that need deterministic crypto randomness under the new crypto behavior.
- `os/signal.NotifyContext` cause propagation: Use `context.Cause` when signal-aware shutdown paths need to know which signal fired.
- goroutine leak profile experiment: Consider for CI or production debugging of blocked-goroutine leaks.
- `runtime/metrics` scheduler metrics: Prefer built-in scheduler gauges over custom approximations.

## Review Prompts

Ask these while reviewing:

- Can `go fix` or a modernizer do this migration more safely than a manual edit?
- Is this pointer-helper boilerplate now clearer as `new(expr)`?
- Is this `errors.As` pattern just typed extraction boilerplate?
- Is this reverse proxy still using `Director` when it should use `Rewrite`?
- Is this logging fan-out code reinventing `slog.NewMultiHandler`?
- Are these crypto tests depending on custom randomness parameters that Go 1.26 now ignores?
- Should this test write files through `ArtifactDir` instead of ad hoc temp-path handling?
- Is this shutdown path able to inspect the signal cause from `NotifyContext` cancellation?
- Is this URL parsing logic assuming malformed hosts will still pass?

## Non-Adoption Notes

Some Go 1.26 changes matter for review but are not usually direct refactor targets:

- the Green Tea GC is now default
- cgo call overhead is lower
- heap base randomization is automatic
- many linker/executable layout changes only matter to binary-analysis tooling
- `io.ReadAll` and JPEG improvements are mostly automatic wins
- several crypto and TLS changes are defaults or behavior changes rather than API migrations

Experimental features should stay opt-in unless explicitly requested:

- goroutine leak profile
- `simd/archsimd`
- `runtime/secret`

## Validation

When you make Go 1.26-driven code changes, run the normal repo gates plus targeted checks:

```bash
go test ./...
golangci-lint run ./...
go vet ./...
```

If the review touched proxying, crypto, or testing utilities, pay special attention to integration coverage and compatibility behavior rather than just compile success.
