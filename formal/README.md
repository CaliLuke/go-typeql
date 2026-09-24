# Formal models

These models check the correctness of go-typeql code that is hard to test well.

| File | Models | Checked with |
| --- | --- | --- |
| `tla/ConnPool.tla` | `gotype/pool.go`: numOpen accounting, capacity bound, idle stack, waiter hand-off, lost wakeups | TLC |
| `tla/TxHandle.tla` | `driver/transaction.go` and `driver/driver.go`: ownership of one native transaction handle | TLC |
| `lean/Naming.lean` | `tqlgen` Go names, `gotype.toKebabCase`, the old `gotype.sanitizeVar`, and a proof that `naming.VarLabel` is injective | Lean 4 |
| `lean/Allocator.lean` | The variable allocator that issue #138 proposes: distinct names for distinct keys, the suffix search, valid TypeQL variable spellings, and the readable candidates (`rootSpelling`, attribute names) | Lean 4 |
| `lean/Scopes.lean` | The filter compiler and query builders of issue #138 over any filter tree: uses are bound in their scope, owners are bound in an enclosing scope, a variable lives in one scope, child scopes are fresh, `or` branches share no variable, and each `Computed` result, reduce output, and old value has its own variable | Lean 4 |

## Run the TLA+ models

Get `tla2tools.jar` from <https://github.com/tlaplus/tlaplus/releases>. Then:

```bash
cd formal/tla
java -cp tla2tools.jar pcal.trans -nocfg ConnPool.tla   # only after you edit the PlusCal
java -cp tla2tools.jar tlc2.TLC -deadlock -workers auto ConnPool.tla                          # safety
java -cp tla2tools.jar tlc2.TLC -deadlock -workers auto -config ConnPoolLive.cfg ConnPool.tla # liveness
java -cp tla2tools.jar tlc2.TLC -deadlock -workers auto TxHandle.tla
```

Use `-deadlock` because the background processes (cleaner, worker, env) wait forever when the clients are done.

## Run the Lean proofs

Install Lean with [elan](https://github.com/leanprover/elan). Then:

```bash
cd formal/lean
lake build
```

`lake build` checks all three files. If the build completes with no error, all proofs are correct. The files use Lean core and `Std` only (no Mathlib).

## Limits

- The models are bounded. `ConnPool` uses 3 clients, `MaxSize = 1`, and 1 or 2 rounds. `TxHandle` uses 2 user operations. There can be bugs that need more steps.
- The models are written by hand from the Go code. If the Go code changes, update the models.
- `Allocator.lean` and `Scopes.lean` model the design of issue #138, not Go code. When the compiler is implemented, the Go code must follow the model.
- `Scopes.lean` models filters as a binary tree (`And` and `Or` with two children), and it emits a `has` pattern for each attribute use. It does not model error reporting, filter values, or the TypeQL text. Generated names can depend on the order of allocation (an example in `Allocator.lean` shows this).
- A mutation test showed that each TLA+ model can find a bug. When one guard is removed from the model, TLC reports an error.
