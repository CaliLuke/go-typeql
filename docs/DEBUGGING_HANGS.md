# Debugging Hangs

Use this guide when a TypeDB operation appears blocked or very slow.

## Enable debug logs

```bash
export TYPEDB_GO_DEBUG=1
export TYPEDB_GO_DEBUG_RUST=1
```

Optional tuning:

```bash
export TYPEDB_GO_DEBUG_SLOW_MS=2000
export TYPEDB_GO_DEBUG_TX_OPEN_WARN=64
export TYPEDB_GO_DEBUG_TX_QUERY_WARN=32
```

## Key log fields

- `tx_id`, `db`, `tx_type`
- `query_op` (`match|insert|delete|update|define|undefine|fetch|reduce|other`)
- `query_fingerprint` (stable short hash of query text; avoids logging raw query payload)
- `elapsed_ms`
- `deadline_remaining_ms` (for `QueryWithContext`)
- in-flight gauges: `active`, `high_water`, `warn_threshold`

## Quick interpretation

- Stuck before `tx.open` completes:
  - investigate connection/auth/open path and environment readiness.
- `tx.open` completes, but `tx.query` does not:
  - query path is blocked/hanging; use `query_op` and `query_fingerprint` to group offenders.
- Repeated `tx.query_inflight.high` or `tx.open_inflight.high`:
  - likely contention, leaked transaction/query work, or upstream service pressure.
- `tx.finalizer.leak` appears:
  - transaction object was GC'd while still open; ensure explicit `Commit/Rollback/Close`.

## Startup-only hang smoke test

Use this when you suspect init/loader/FFI startup issues:

```bash
go test -tags "cgo,typedb" ./driver -run '^$' -count=1 -timeout 30s -v
```

Or run the automated diagnostic target:

```bash
make diagnose-startup-hang
```

This target applies a hard timeout and captures a `sample` trace for stuck `driver.test` processes.
