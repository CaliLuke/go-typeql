# Go 1.26 Features To Look For

Use this file as a review catalog, not as a mandate to rewrite code. Focus on places where Go 1.26 removes boilerplate, replaces unsafe hooks, improves diagnostics, or changes behavior in ways that deserve explicit review.

## 1. Tooling and Modernization

### `go fix` modernizers

Go 1.26 turns `go fix` into the home of source modernizers built on the analysis framework.

Review guidance:

- Consider `go fix` before manual broad modernizations.
- Prefer it for mechanical, semantics-preserving migrations.
- If a refactor spans many files and mostly updates idioms, check whether `go fix` can do the first pass.

Good fits:

- language/library idiom migrations
- internal API migrations that can use `//go:fix inline`
- large cleanup passes where consistency matters

Search hints:

- broad legacy cleanup tasks rather than specific APIs

### `go mod init` default version behavior

This is mostly operational. Mention it if tooling or scaffolding scripts assume a freshly initialized module will use the current toolchain version in `go.mod`.

## 2. Language-Level Opportunities

### `new(expr)`

Go 1.26 allows `new` to take an expression as its operand.

Prefer:

```go
Age: new(yearsSince(born))
```

Over patterns like:

```go
age := yearsSince(born)
Age: &age
```

Or:

```go
func intPtr(v int) *int { return &v }
Age: intPtr(yearsSince(born))
```

Use when:

- the pointer exists only to represent an optional scalar or value in a composite literal
- the helper function exists only to take addresses of values
- the expression remains readable

Avoid when:

- the temp variable carries meaningful name or documentation value
- the pointed-to value is reused
- the expression is complex enough that naming it is clearer

Search hints:

- `rg 'Ptr\\('`
- `rg '&[a-zA-Z_][a-zA-Z0-9_]*'`
- `rg 'func .*Ptr\\('`

### Self-referential generic constraints

This is a capability expansion, not a routine refactor target. Use only if generic abstractions in the codebase are currently awkward because they cannot express "this type behaves like itself."

## 3. Errors, Logging, and Reflection

### `errors.AsType`

Prefer:

```go
target, ok := errors.AsType[*MyError](err)
```

Over:

```go
var target *MyError
ok := errors.As(err, &target)
```

Use when:

- the code is simply extracting a typed error
- reducing boilerplate improves readability
- the package already depends on ordinary `errors.As`

Search hints:

- `rg 'errors\\.As\\('`
- `rg 'var .*\\*.*\\n.*errors\\.As' -U`

### `fmt.Errorf("x")`

Go 1.26 reduces allocation differences for constant-string `fmt.Errorf`. This is useful context, but not a reason to replace `errors.New` or vice versa unless there is another readability reason.

### `slog.NewMultiHandler`

Use when the code manually fans records out to multiple handlers.

Replace patterns like:

- custom composite handlers
- wrappers that loop over handlers and call `Handle`
- duplicated `WithAttrs` or `WithGroup` fan-out plumbing

Benefit:

- less custom logging infrastructure
- correct enabled-state handling across handlers

Search hints:

- `rg 'Handle\\(ctx, r\\)'`
- `rg 'WithAttrs\\('`
- `rg 'WithGroup\\('`
- `rg '[]]slog\\.Handler|\\[\\]slog\\.Handler'`

### New reflect iterators

Go 1.26 adds iterator-style APIs:

- `Type.Fields`
- `Type.Methods`
- `Type.Ins`
- `Type.Outs`
- `Value.Fields`
- `Value.Methods`

Consider them when reflection-heavy code currently allocates intermediate slices or uses verbose index-based loops. Do not force churn in stable reflection code unless the new iterator form materially improves clarity or cost.

## 4. HTTP, Networking, and URL Handling

### `httputil.ReverseProxy.Rewrite`

This is one of the most important concrete review items in Go 1.26.

`ReverseProxy.Director` is deprecated because malicious clients can strip headers added by `Director` by designating them hop-by-hop.

Prefer:

- `ReverseProxy.Rewrite`

Over:

- `ReverseProxy.Director`

Review prompt:

- Are we adding security-sensitive or routing-sensitive outbound headers in `Director`?

Search hints:

- `rg 'ReverseProxy'`
- `rg 'Director:'`
- `rg '\\.Director ='`

### `net.Dialer` typed dialing methods

New methods:

- `DialIP`
- `DialTCP`
- `DialUDP`
- `DialUnix`

Use when:

- code currently drops context or casts after dialing
- typed dialing with context makes the call site clearer

This is usually a targeted cleanup, not a mandatory rewrite.

### `net/http.Client` cookie host behavior

Review code that relies on `Request.Host` differing from the connection address. Cookie behavior now follows `Request.Host` when available.

### `net/http/httptest.Server.Client`

Tests involving `example.com` now redirect to the test server. Mention this if test behavior changes or previous host-routing workarounds become unnecessary.

### `net/url.Parse` strict host-colon rejection

Go 1.26 rejects malformed URLs like `http://::1/` and `http://localhost:80:80/`.

Review prompt:

- Are tests or parsers depending on previously accepted malformed inputs?

Search hints:

- `rg 'url\\.Parse\\('`
- malformed host fixture review in tests and parsers

## 5. Testing Improvements

### `testing.T.ArtifactDir`

Use for tests that intentionally emit files.

Prefer:

- writing snapshots, traces, images, golden diffs, or debug bundles into `t.ArtifactDir()`

Over:

- ad hoc current-directory writes
- custom temp-dir logging patterns
- tests that make artifact paths hard to discover

Search hints:

- `rg 'os\\.WriteFile\\(' --glob '*_test.go'`
- `rg 't\\.TempDir\\(' --glob '*_test.go'`
- `rg 'CreateTemp|MkdirTemp' --glob '*_test.go'`

### `testing/cryptotest.SetGlobalRandom`

Go 1.26 crypto packages increasingly ignore explicit randomness parameters and always use secure randomness.

Use `testing/cryptotest.SetGlobalRandom` in tests that need deterministic outputs from:

- `crypto/rand`
- RSA/ECDSA/DSA/ECDH keygen or signing paths
- other crypto APIs that now ignore caller-provided randomness

Review prompt:

- Are tests still trying to inject deterministic randomness through arguments that no longer matter?

Search hints:

- `rg 'rand.New|math/rand|bytes.NewReader'`
- `rg 'GenerateKey|SignASN1|EncryptPKCS1v15|Prime'`

### `B.Loop`

Go 1.26 fixes the inlining concern around `B.Loop`.

If benchmark code stayed on `for i := 0; i < b.N; i++` only because of earlier `B.Loop` caveats, it can now move cleanly to `for b.Loop() { ... }`.

## 6. Runtime, Metrics, and Diagnostics

### Green Tea GC default

This is mainly operational context. Do not rewrite source because of it. Mention it when analyzing GC-sensitive regressions or behavior changes.

### Experimental goroutine leak profile

Consider for CI, tests, or production debugging when goroutines may block forever on unreachable synchronization primitives.

Good fits:

- fan-out workers that can return early and strand senders
- blocked channel send/receive leaks
- deadlock-like incidents not caught by ordinary goroutine dumps

This is experimental and should stay opt-in unless the user explicitly wants it.

### `runtime/metrics` scheduler metrics

New metrics include:

- `/sched/goroutines/...`
- `/sched/threads:threads`
- `/sched/goroutines-created:goroutines`

Use built-in metrics when the codebase currently approximates scheduler state with custom counters or periodic `runtime.NumGoroutine` polling.

Search hints:

- `rg 'NumGoroutine|runtime/metrics|sched'`

### `os/signal.NotifyContext` cancel cause

Go 1.26 now cancels with a cause that indicates the signal received.

Use when shutdown code benefits from:

- distinguishing SIGTERM from SIGINT
- logging the actual terminating signal
- branching on `context.Cause(ctx)`

## 7. Crypto and Security Review Points

### New crypto interfaces and packages

Potential targeted adoption points:

- `crypto/hpke` for HPKE support instead of external implementations
- `crypto.Encapsulator` / `crypto.Decapsulator` for abstract KEM integration
- `crypto/ecdh.KeyExchanger` for abstract ECDH private keys
- `rsa.EncryptOAEPWithOptions` when OAEP and MGF1 hashes must differ

Only recommend these if the codebase already works in those domains.

### Deprecated or tightened crypto behavior

Review for:

- deprecated PKCS #1 v1.5 encryption use
- code mutating RSA keys after `Precompute`
- direct use of deprecated `ecdsa.PublicKey` / `PrivateKey` `big.Int` fields
- assumptions that custom randomness injection into crypto APIs still works

### `crypto/subtle.WithDataIndependentTiming`

Mention when code previously worked around thread-locking behavior or needs to understand the new propagation behavior across goroutines and cgo.

## 8. Files, Buffers, and Miscellaneous APIs

### `bytes.Buffer.Peek`

Use when code needs to inspect upcoming bytes without advancing the buffer and currently copies or manually slices after converting state.

### `os.Process.WithHandle`

Relevant only for low-level process integrations needing pidfd/handle access.

### `io.ReadAll`

Go 1.26 improves it automatically. This is context for performance review, not a rewrite trigger.

## 9. Experiments and Mostly-Automatic Changes

Mention these as optional follow-ups, not routine refactors:

- goroutine leak profile
- `simd/archsimd`
- `runtime/secret`
- Green Tea GC rollout details
- heap base randomization
- faster cgo calls
- linker section layout changes

Only recommend experiments when the user explicitly wants adoption or investigation in that area.
