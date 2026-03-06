# Go 1.25 Features To Look For

Use this file as a review catalog, not as a requirement to churn code. Focus on places where Go 1.25 lets you delete footguns, reduce boilerplate, or improve diagnostics.

## 1. Concurrency and Tests

### `sync.WaitGroup.Go`

Prefer:

```go
var wg sync.WaitGroup
wg.Go(func() {
    work()
})
wg.Wait()
```

Over:

```go
var wg sync.WaitGroup
wg.Add(1)
go func() {
    defer wg.Done()
    work()
}()
wg.Wait()
```

Use when:

- the goroutine body does not need wrapper-only bookkeeping beyond `Done`
- the current code manually balances `Add`/`Done`
- you want to reduce `WaitGroup.Add` placement mistakes now caught by `go vet`

Keep the old form when:

- goroutine startup must happen conditionally after other setup
- the code needs a named goroutine wrapper for panic recovery, metrics, tracing, or semaphore release
- the wrapper is doing meaningful orchestration, not just `defer wg.Done()`

Search hints:

- `rg 'WaitGroup'`
- `rg '\.Add\(1\)'`
- `rg 'defer .*\.Done\(\)'`

Review note:

- `WaitGroup.Go` improves ergonomics, but it is not a reason to hide lifecycle logic that should stay explicit.

### `testing/synctest`

Use for deterministic concurrent tests that currently rely on wall-clock sleeps or scheduler luck.

Strong candidates:

- tests using `time.Sleep` to "let goroutines run"
- tests polling channels with arbitrary deadlines
- tests that need virtual time advancement while goroutines are blocked

Common migration target:

- replace sleep-heavy timing coordination with `synctest.Test` and `synctest.Wait`

Search hints:

- `rg 'time\\.Sleep\\(' --glob '*_test.go'`
- `rg 'Eventually|After\\(|NewTimer|NewTicker' --glob '*_test.go'`

### `testing.T.Attr` and `T.Output`

Use `T.Attr` for structured test metadata that should survive `go test -json`.
Use `T.Output` when writing streamed or multiline output that should land in the test log without fake file/line ownership.

Good fits:

- integration tests emitting environment metadata, fixture IDs, or scenario labels
- helper functions that need an `io.Writer` but should write to test output

### `AllocsPerRun` panic with parallel tests

Review any allocation tests that may run while parallel tests are active. Go 1.25 now makes that misuse fail loudly.

## 2. Networking and HTTP

### `net.JoinHostPort`

Go 1.25 `go vet` now flags common broken patterns.

Replace:

```go
fmt.Sprintf("%s:%d", host, port)
```

With:

```go
net.JoinHostPort(host, strconv.Itoa(port))
```

Or the string-port variant already available in your code.

Why:

- correct for IPv6
- now directly covered by the new `hostport` vet analyzer

Search hints:

- `rg 'Sprintf\\(\"%s:%d\"'`
- `rg '\"%s:%s\"'`
- `rg 'host.*port'`

### `net/http.CrossOriginProtection`

Review browser-facing mutation endpoints for this API when:

- requests come from browsers
- endpoints mutate state
- CSRF protections are ad hoc or absent
- token-based CSRF handling is not otherwise required

Avoid blind adoption when:

- the surface is non-browser or machine-to-machine only
- the existing auth layer already depends on bespoke CSRF behavior
- cross-origin exceptions are complex and would make the protection misleading

## 3. Observability and Runtime Diagnostics

### `runtime/trace.FlightRecorder`

Use when production failures are rare and hard to catch with always-on tracing.

Good fits:

- intermittent latency spikes
- deadlock-like stalls
- scheduler anomalies
- rare production-only contention issues

Pattern:

- keep a bounded in-memory recorder running
- dump recent trace data only on a significant event

This is especially useful when logs alone cannot explain low-level runtime behavior.

### `runtime.SetDefaultGOMAXPROCS`

Use only when code or environment currently overrides `GOMAXPROCS` and you want to re-enable Go 1.25's default container-aware behavior.

Review prompt:

- Are we setting `GOMAXPROCS` manually in a way that fights container CPU limits?

### Panic behavior and err-check ordering

Go 1.25 fixes a compiler bug that used to delay nil checks in some incorrect programs.

Audit for:

```go
v, err := someCall()
use(v)
if err != nil { ... }
```

If `v` can be nil when `err != nil`, the code is wrong and may now panic.

Search hints:

- `rg ':= .*\\n.*\\.\\w+\\(\\)\\n\\s*if err != nil' -U`
- practical manual review around constructors/openers: `os.Open`, `http.NewRequest`, decoders, parsers, DB calls

## 4. Logging and Reflection

### `log/slog.GroupAttrs`

Use when you already have a `[]slog.Attr` and need to wrap it as a group attr.

Candidate pattern:

```go
attrs := []slog.Attr{...}
logger.LogAttrs(ctx, level, msg, slog.Group("payload", attrs...))
```

Prefer:

```go
attrs := []slog.Attr{...}
logger.LogAttrs(ctx, level, msg, slog.GroupAttrs("payload", attrs...))
```

Benefit:

- clearer intent when the input is already attrs

### `slog.Record.Source`

Relevant only if you are building custom handlers or log post-processors and need source info from `slog.Record`.

### `reflect.TypeAssert`

Replace allocation-prone patterns like:

```go
u := v.Interface().(MyType)
```

With `reflect.TypeAssert` when:

- the code already operates on `reflect.Value`
- the path is hot or allocation-sensitive
- the direct assertion is awkward or expensive

Do not force this in ordinary code where reflection itself is the bigger smell.

## 5. Filesystems and Multipart

### `io/fs.ReadLinkFS`

Go 1.25 adds symlink-aware support in several filesystem helpers.

Review custom filesystem abstractions and tests for:

- handwritten symlink support that can now implement `ReadLinkFS`
- tar/copy/test helpers that can rely on standard library support instead of bespoke logic

Relevant packages:

- `io/fs`
- `os.DirFS`
- `os.Root.FS`
- `archive/tar.Writer.AddFS`
- `testing/fstest.MapFS`
- `testing/fstest.TestFS`

### `mime/multipart.FileContentDisposition`

Use instead of manually formatting multipart file `Content-Disposition` values.

Search hints:

- `rg 'Content-Disposition'`
- `rg 'form-data; name='`

## 6. Crypto and Security APIs

### `crypto.MessageSigner` and `crypto.SignMessage`

Use when code needs to work with signers that hash internally instead of requiring the caller to pre-hash and call `Signer.Sign`.

Look for:

- generic signer abstraction layers
- x509 certificate/csr/crl creation wrappers
- HSM/KMS adapters with one-shot signing semantics

### `crypto/ecdsa` low-level key encoding helpers

Review for code still using `crypto/elliptic` or manual `math/big` plumbing only to parse or serialize raw ECDSA keys and points.

Potential replacements:

- `ecdsa.ParseRawPrivateKey`
- `ecdsa.ParseUncompressedPublicKey`
- `PrivateKey.Bytes`
- `PublicKey.Bytes`

## 7. Go Analysis, AST, and Token APIs

These matter mainly if the codebase includes analyzers, generators, linters, or AST tooling.

### New tools and APIs

- `go/token.FileSet.AddExistingFiles`
- `go/ast.PreorderStack`
- `go/types.LookupSelection`
- `go/types.Var.Kind`

### Deprecations to notice

- `go/parser.ParseDir`
- `go/ast.FilterPackage`
- `go/ast.PackageExports`
- `go/ast.MergePackageFiles`
- `go/ast.MergeMode`

Refactor only if the repo actually owns AST tooling that benefits from the newer APIs.

## 8. Experiments and Mostly-Automatic Changes

Mention these as optional follow-ups, not routine refactors:

- `GOEXPERIMENT=greenteagc`
- `GOEXPERIMENT=jsonv2`
- DWARF5 output
- more stack allocation for slice backing stores
- crypto and hashing speedups
- container-aware `GOMAXPROCS`

### Unsafe caveat for faster slices

If a refactor or upgrade exposes new crashes or corruption around `unsafe.Pointer`, suspect assumptions about heap allocation. Go 1.25 can stack-allocate more slice backing stores. Fix the unsafe code; do not disable the optimization unless debugging.
