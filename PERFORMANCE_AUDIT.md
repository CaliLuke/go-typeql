# Performance Audit: go-typeql

This report tracks measured performance work on `go-typeql`, focusing on high-impact areas identified during code review, benchmarking, and follow-up profiling.

---

## Current Status

The benchmark harness is now institutionalized in the repo:

- `make bench`
- benchmark history stored in `benchmarks/benchmarks.sqlite`
- baseline run: `run 1`
- latest optimized run before the next pass in this document: `run 5`

Measured gains from `run 1` to `run 3`:

| Benchmark | `ns/op` | `B/op` | `allocs/op` |
| --- | ---: | ---: | ---: |
| `BenchmarkHydrate_10000Rows` | `-68.87%` | `-67.46%` | `-85.72%` |
| `BenchmarkHydrate_1000Rows` | `-67.15%` | `-65.75%` | `-85.72%` |
| `BenchmarkHydrate_100Rows` | `-67.07%` | `-65.93%` | `-85.67%` |
| `BenchmarkHydrate_SingleRow` | `-50.69%` | `-66.67%` | `-85.71%` |
| `BenchmarkCompiler_CompileBatch` | `-21.80%` | `+4.90%` | `-28.57%` |
| `BenchmarkCompiler_FormatGoValue` | `-14.95%` | `0.00%` | `-20.00%` |
| `BenchmarkExtractModelInfo_Entity` | `-8.20%` | `-10.46%` | `-4.35%` |
| `BenchmarkExtractModelInfo_Relation` | `+0.34%` | `+8.72%` | `-4.00%` |

What has already been addressed:

- Raw-row hydration replaced per-row `unwrapResult` copies on the main path.
- Embedded base-field metadata is cached in `ModelInfo` for IID access.
- `hydrateResults` now hydrates through pre-resolved `ModelInfo` rather than paying generic registry lookup on every row.
- Roleless hydration no longer allocates cycle-detection state.
- Common scalar field types now use typed setter fast paths instead of the generic `reflect.ValueOf(converted)` path.
- `FormatLiteral`, `FormatGoValue`, and parts of `CompileBatch` / fetch compilation now use cheaper formatting paths.
- Benchmark recording, comparison, and persistence are first-class parts of the repo workflow.

What remains open is no longer “easy big wins everywhere”; it is concentrated in hydration’s remaining generic reflection path and, secondarily, compiler recursive string construction.

---

## 1. Highest Remaining Headroom: Data Hydration (`gotype/hydrate.go`)

The hydration process is the primary bottleneck for read-heavy applications, particularly when fetching large result sets.

### Evidence
- **Hot profile concentration:** `BenchmarkHydrate_10000Rows` is still the largest absolute hotspot after the first optimization pass.
- **Allocation profile:** `HydrateNew` still accounts for ~50% of alloc space and `coerceValue` for ~30%.
- **Remaining reflection cost:** `setFieldValue` and `reflect.Value.Set` are still a major fraction of the CPU time in bulk hydration.
- **Generic coercion path:** common scalar fields still flow through generic `coerceValue` and `reflect.ValueOf(converted)` instead of typed fast paths.

### Suggestions
- **Pre-compiled setters:** Store setter functions in `ModelInfo` to avoid per-row generic branching.
- **Pre-compiled hydrators:** If the safer fast paths stop paying off, generate per-model hydrators at registration time.
- **Unsafe field access:** Highest-risk, highest-reward follow-up if another major hydration gain is required.
- **Typed slice fast paths:** multi-valued attributes still fall back to the generic slice path.

---

## 2. High-Impact Area: AST Compilation (`ast/compiler.go`)

Query generation frequency can match or exceed hydration frequency in many workloads.

### Evidence
- **Profile still shows recursive string churn:** `compilePattern` and `compileConstraint` remain the main CPU and alloc hotspots inside the compiler.
- **Partial cleanup only:** some `fmt.Sprintf` and `strings.Join` use remains in relation-pattern and constraint assembly.
- **Absolute cost is lower than hydration:** compiler work is still worth improving, but it is now the secondary target.

### Suggestions
- **Full recursive builder threading:** pass a builder through recursive compilation instead of returning intermediate strings.
- **Finish removing `fmt.Sprintf` in hot relation/constraint paths.**
- **Reduce temporary slices in relation pattern assembly.**

---

## 3. CRUD Logic & Result Processing (`gotype/crud.go`)

### Evidence
- **Main-path intermediate map allocation has already been removed.**
- **`unwrapResult` still exists for secondary paths and benchmarks, but it is not the bulk hydration bottleneck anymore.**

### Suggestions
- **Keep `unwrapResult` as a compatibility helper, but do not spend more time on it unless it re-enters a hot path.**
- **Only consider `sync.Pool` if a real application trace shows instances are extremely short-lived and GC-bound.**

---

## 4. Model Metadata & Registration (`gotype/model.go`)

### Evidence
- **IID field scanning was addressed by caching the embedded base-field index.**
- **Registration is now relatively cheap in absolute terms; it is not a meaningful end-to-end bottleneck compared with hydration.**

### Suggestions
- **Do not prioritize `toKebabCase` work unless startup profiling proves it matters.**
- **Only revisit registration if generated/precompiled hydrators are introduced and need richer metadata.**

---

## Summary Of Remaining Headroom

From the current `run 3` baseline, the realistic remaining upside appears to be:

1. **Hydration:** another **10-20%** is still plausible, but the easy wins are now mostly exhausted.
2. **Compiler:** another **10-20%** with a full recursive builder rewrite.
3. **Beyond that:** larger wins are still possible, but probably require the intrusive precompiled/unsafe hydrator approach rather than incremental cleanup.
