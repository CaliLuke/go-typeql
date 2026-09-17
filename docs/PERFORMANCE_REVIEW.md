Performance review, September 16, 2026

The system has recoverable performance costs. The largest opportunities concern database round trips, result volume, and transaction scope.
The recent streaming and hydration changes already remove much of the avoidable work in the local read path.

This review covers commit `5ec3482` (`v1.15.0-alpha.3`). It examines the ORM, query builder, AST compiler, driver, Rust FFI, pool, generator, and migrations.
Experiments run in a detached temporary worktree. Production code remains unchanged.

The measurements use Go 1.27.1, an Apple M5 with 10 CPU cores and 24 GiB RAM, and TypeDB 3.12.3 under Colima.
The test server uses port 1731 because another container occupies port 1730.
The host has substantial background load. Five repetitions establish directional evidence, but timing ranges remain wide.
Allocation counts are more stable. Go allocation metrics exclude Rust allocations, server memory, and peak resident memory.

The review uses a balanced workload because no production workload or latency target was supplied.
Synthetic fixtures establish opportunities. They do not predict a universal application speedup.

The table gives medians from five repetitions. Each live experiment includes the costs described in its finding.
These are experimental variants, not changes applied to the library.

| Experiment | Current path | Alternative | Evidence |
| --- | --- | --- | --- |
| Insert 32 entities and assign IIDs | 161.89 ms, 133,058 B | 16.41 ms, 43,544 B | About 9.9 times faster in this fixture. Query calls fall from 32 to one. |
| Existence check over 10,256 entities | 11.70 ms | 1.91 ms | About 6.1 times faster with a one-match limit. |
| Put an existing keyed entity | 6.72 ms, 140 allocations | 7.12 ms, 82 allocations | Inline IID fetch removes one query and 58 allocations. A latency gain is unproven. |
| Repeated IID read | 1.97 ms, 87 allocations | 0.87 ms, 69 allocations | About 2.3 times faster inside an existing read transaction. |
| Ten concurrent readers, including close completion | 11.41 ms/op with queued closes | 0.41 ms/op with checked closes | About 28 times higher completed-operation throughput in this stress fixture. |
| Fetch 10,256 entities through a callback | 1,619,794 B, 113,105 allocations | 472,208 B, 31,063 allocations | IID-only projection reduces Go allocation volume by 71%. |
| Parse a small schema repeatedly | 372.48 microseconds, 711,902 B | 28.01 microseconds, 26,791 B | Reusing the grammar reduces allocation volume by 96%. |
| Decode 1,000 flat rows | 464.46 microseconds, 511,684 B | 365.64 microseconds, 159,367 B | The existing callback path already reduces allocation volume by 69%. |

Insert timings range from 52–212 ms for the current path and 10–38 ms for the batch variant.
Existence timings range from 10.3–15.2 ms for a full count and 1.6–8.5 ms for the bounded query.
Projection timings overlap substantially: 396–1,441 ms for full rows and 245–519 ms for IID-only rows.
The projection finding therefore rests primarily on allocation reduction, not a precise latency claim.
The improvements overlap and cannot be added together.

The following findings cover the main recovery paths.

1. **Batch the bulk APIs at the query boundary.**

   `InsertMany`, `UpdateMany`, and `DeleteMany` send one query per instance.
   `PutMany` sends two queries per keyed instance.
   A shared transaction reduces commit overhead, but it leaves the per-row query calls intact.
   Typed `given` rows already provide the driver mechanism for a batch implementation.

   The experiment compares the existing `InsertMany` with one typed-input query for 32 scalar entities.
   Both paths commit the transaction, retrieve IIDs, and assign IIDs to the input objects.
   The batch path associates results by key, rather than assuming result order.

   A production implementation needs bounded batches and one transaction across those batches.
   Typed-row construction must stay in the pure Go layer so `gotype` remains independent of `driver`.
   It must preserve duplicate-key errors, cancellation, IID assignment, and rollback behavior.
   Optional attributes, slices, decimals, and relation players need explicit coverage.
   The scalar experiment does not establish performance for every model shape.

   Sources: [InsertMany](../gotype/crud.go#L603), [UpdateMany](../gotype/crud.go#L448), [DeleteMany](../gotype/crud.go#L400), [PutMany](../gotype/crud.go#L523).

2. **Stop existence checks after the first match.**

   `Query.Exists` calls `Count`, which performs a distinct count over all matching instances.
   The caller only needs a boolean.
   A bounded existence query can preserve the filter behavior while avoiding the full count.
   The experiment adds `limit 1` before the final reduction and uses 10,256 matching entities.
   The official TypeQL checker accepts this query, and the live server executes it.

   Empty results, duplicate matches, complex filters, and transaction-bound managers need regression coverage.
   Existing limit and offset behavior must remain explicit because `Count` currently ignores pagination.

   Source: [Exists and Count](../gotype/query.go#L61).

3. **Return the IID directly from `put`.**

   `Put` sends a `put` query, then sends a separate key-match query for the IID.
   `PutMany` repeats that sequence for every keyed instance.
   A fetch stage after `put` can remove the second query.
   The official TypeQL checker accepts the combined query.
   The live experiment returns the correct IID for an existing keyed entity.
   Its latency ranges overlap, so query-count and allocation reductions are the established benefits.

   Source: [Put](../gotype/crud.go#L477).

4. **Support narrow projections and a typed row callback.**

   Normal ORM reads fetch every attribute and the IID.
   `GetWithRoles` also fetches every attribute of each role player.
   The driver streams chunks, but `hydrateQueryRows` still retains every model in the final result slice.
   A projection API reduces server work, transfer volume, decoding, and allocations when callers need only selected fields.
   A typed callback or iterator can also avoid retaining the complete model slice.

   The projection experiment compares all three entity attributes plus IID against IID alone over 10,256 entities.
   Both variants use the same driver callback path and consume every row.
   Neither variant hydrates models, so the result isolates projection and decoding costs.
   The narrower result has different content. It only applies when the omitted attributes are unnecessary.

   Partial models need an explicit contract. A caller must not accidentally overwrite omitted fields through `Update`.
   Projection DTOs or a dedicated result type can make that distinction clear.

   Sources: [fetch construction](../gotype/strategy.go#L174), [role fetch](../gotype/strategy.go#L376), [result retention](../gotype/crud.go#L813).

5. **Use existing transaction scopes for related reads.**

   Unbound manager reads open a transaction for each operation.
   A manager bound to `TransactionContext` can reuse one transaction for a related sequence of reads.
   The experiment compares repeated IID reads through these two existing paths.

   This is an application-level optimization with a snapshot tradeoff.
   Reuse fits a bounded unit of work that needs one snapshot.
   It does not justify a global read transaction or concurrent calls on one native transaction handle.

   Sources: [readHydrated](../gotype/crud.go#L798), [transaction-bound manager](../gotype/crud.go#L76), [transaction mutex](../driver/transaction.go#L46).

6. **Bound unfinished transaction cleanup and measure pool settings together.**

   The pool lends a complete connection to each transaction.
   One shared driver can already open separate transactions concurrently.
   Consequently, a small pool can impose an additional concurrency limit.
   Extra connections also create more drivers and close workers.

   The concurrent run produced 903 slow-close warnings, with enqueue-to-completion durations up to 17.5 seconds.
   Each driver has one worker that processes queued closes serially.
   The queue holds 1,024 jobs. A full queue triggers a local-drop fallback.
   This is evidence of delayed cleanup under this test load, rather than a measured server-side close duration.
   Queue delay, server load, and warning-log overhead can all contribute.
   A second experiment includes the complete close drain in the measured time.
   With ten callers, asynchronous close takes a median 11.41 ms per completed operation.
   Concurrent `CloseChecked` calls take 0.41 ms per completed operation.
   These are throughput measurements, not single-request latency measurements.
   The asynchronous variant still needs a median 1.45 seconds to drain after its query loop finishes.

   A four-worker prototype also develops a backlog.
   Its asynchronous path takes a median 10.38 ms per completed operation, with a 2.92-second final drain.
   More close workers alone do not establish a sufficient fix.
   The next experiment needs a bound on transactions that remain open until cleanup completes.
   That bound must retain capacity until the native close finishes, rather than when Go detaches the handle.
   The current pool returns the connection immediately after it queues the close.
   The active-transaction counter also decreases before native cleanup completes.
   Those values therefore do not describe unfinished cleanup.
   Queue depth, oldest-job age, and native completion counts can expose this cost.
   Rust and server profiles remain necessary to separate native callback delays from server pressure.

   The experiment compares a shared driver against pools of one and four connections with ten benchmark workers.
   Its `ns/op` measures aggregate throughput, not per-request latency.
   Pool sizing also needs production p95/p99 latency, saturation, timeout, and resource measurements.

   The initial pool comparison contains large timing outliers and substantial close backlog.
   It does not support a general pool-size recommendation.

   Sources: [pool transaction acquisition](../gotype/pool.go#L536), [pool return](../gotype/pool.go#L697), [driver transaction open](../driver/driver.go#L529), [close worker](../driver/transaction.go#L158), [fallback](../driver/transaction.go#L838).

7. **Reduce repeated local construction after the database changes.**

   Every ordinary read rebuilds the model's fetch projection through the AST compiler.
   An immutable projection cached with model metadata can remove that repeated work.
   Cache invalidation must account for registry changes, variable names, role metadata, and subtype fields.

   Driver context wrappers and their inner query methods both compute query fingerprints and operation names.
   Disabled logging still constructs diagnostic fields.
   Rust logging callers also construct strings and vectors before the logger checks its enabled flag.
   Lazy diagnostic construction can reduce allocations while preserving slow-query warnings and counters.

   These are small local costs relative to the measured database calls.
   They become more relevant after batching or for applications with high request rates.

   Sources: [projection construction](../gotype/strategy.go#L174), [context query wrapper](../driver/transaction.go#L623), [diagnostics](../driver/debug.go#L71), [Rust diagnostics](../driver/rust/src/lib.rs#L1244).

8. **Reuse the schema parser for repeated generator or migration work.**

   `ParseSchema` rebuilds the Participle grammar on every call.
   The experiment compares this path with one parser reused across calls.
   This cost affects generator and migration workflows, rather than normal CRUD requests.
   A production change needs concurrent parsing checks before a shared parser replaces per-call construction.

   Generator role resolution also scans model lists repeatedly.
   A role-to-player index can improve large-schema generation, but this review does not measure that change.

   Sources: [parser construction](../tqlgen/parser.go#L202), [role lookup](../tqlgen/render.go#L649).

9. **Reduce decoder allocations after projections and batching.**

   The callback decoder still allocates 14,003 objects for 1,000 flat rows.
   Its allocation profile attributes about 60% of allocated bytes to MessagePack string creation.
   `DecodeInterfaceLoose` also allocates boxed values.
   Reusing field-name strings within a query can remove repeated key allocations.
   A direct-to-model decoder is a larger experiment that can avoid intermediate map and interface work.

   Returned string values must retain independent ownership because the driver frees each C buffer after decoding.
   Any implementation must preserve nested maps, lists, numeric conversions, and callback error behavior.
   The current profile establishes the cost, but this review does not measure a replacement decoder.

   Source: [callback decoder](../driver/transaction.go#L721).

Several additional opportunities need workload evidence or a contract decision.

- `GetOne` fetches and hydrates all matches before it reports a uniqueness error.
  A two-row limit avoids that work, but it changes the exact `NotUniqueError.Count` value.
  An alternative can count first and fetch only a unique match, at the cost of another query for successful lookups.
- Strict `DeleteMany` checks each IID separately before the write loop.
  A batched existence check can remove those calls while preserving strict behavior.
- `UpdateWith` fetches all models and writes each model separately.
  Existing `Query.Update` already provides a set-based option when every matching model receives the same values.
- `Query.Update` and `Query.Delete` execute a count query before the mutation.
  A count-free variant can avoid this work when callers do not need an affected-row count.
- The streaming driver uses fixed 256-row chunks and passes default query options.
  It exposes no prefetch or chunk-size control through the ORM streaming path.
  Chunk-size and prefetch sweeps need wide rows, network latency, and time-to-first-row measurements.
- Cancellation releases the Go caller, but the native query can continue until the synchronous call returns.
  Timeout-heavy workloads need native resource and close-backlog measurements before any concurrency changes.
- Large `In` and IID filters expand query text into alternatives.
  Typed inputs can avoid that growth, but the server-side benefit needs a dedicated benchmark.

The existing implementation already contains several valuable optimizations.

- Hydration uses registered field metadata and scalar conversion paths.
- Flat entity hydration allocates one model per row plus one result slice.
- The driver decodes directly from the C buffer and reuses the top-level row map on the callback path.
- Rust serializes fetch documents directly, without an intermediate JSON tree.
- Streaming bounds the FFI result buffer by row chunks.
- Asynchronous close removes checked close latency from the ordinary caller path.
- `GetWithRoles` fetches players in one query, rather than issuing one query per player.
- Multi-aggregate queries already combine several reductions in one request.

The flat hydration benchmark takes a median 1.51 ms for 10,000 rows, with 881,923 B and 10,001 allocations.
Its allocation profile attributes 90.4% of bytes to model objects and 9.2% to the result slice.
Map lookup accounts for 21.6% of sampled CPU time cumulatively.
Field assignment accounts for 19.2% cumulatively.
These profiles support retaining the current hydration design while reducing the number of rows and fields sent through it.

The local projection builder takes about 804 ns and allocates 963 B across 26 objects per call.
Repeated query metadata costs about 672 ns and 528 B in the isolated diagnostic benchmark.
The disabled query-duration logger still allocates 432 B across four objects per call.
These costs are measurable, but they remain much smaller than millisecond-scale database calls.

The current evidence favors fewer database calls, bounded unfinished cleanup, and smaller results.

The benchmark suite needs broader coverage to protect these gains.
The recorded suite has compiler, hydration, metadata, and unwrap benchmarks.
Driver decoding and live reads exist outside that default recording path.
Bulk writes, realistic nested results, partial projections, cancellation load, and latency percentiles need durable coverage.
The existing close-backlog integration test logs backlog and drain time but does not enforce a bound.
It can pass despite excessive cleanup delay.
Future measurements must separate query construction, transaction open, server execution, FFI encoding, Go decoding, and hydration.

The recommended implementation sequence is:

1. Add scalar batch inserts through typed input rows, with the current transaction and IID contracts intact.
2. Add unfinished-close metrics and a bounded transaction-lifecycle experiment before selecting a cleanup policy.
3. Add a bounded existence query. Then evaluate inline IID fetch for `put` with broader model coverage.
4. Add explicit projections and a typed callback API for large reads.
5. Cache fetch projections and parser construction. Make disabled diagnostic construction lazy.
6. Investigate decoder key reuse if production profiles still identify decoding as a substantial cost.

The acceptance criteria need workload measurements, rather than a promised aggregate speedup.
Bulk changes must reduce query count without changing atomicity or IID mapping.
Close changes must improve sustained throughput while bounding unfinished native transactions and p95/p99 latency.
Projection changes must reduce transferred data and memory without exposing unsafe partial updates.
Every TypeQL change needs the official syntax gate and live integration coverage.

The [raw evidence](../benchmarks/reviews/2026-09-16/README.md) contains 30 benchmark series with five samples each, summary statistics, and profile reports.
The committed benchmark-history database remains unchanged.
Experimental source snapshots and binary profiles remain under `/tmp/go-typeql-perf-review-20260916` for local follow-up.
The temporary worktree and its code changes are removed after the experiments.

The official TypeQL checker accepted the experimental typed-input, projection, existence, and combined `put` queries.
The live server executed all those variants successfully.
The unit suite passed through `go test ./...`.
The full driver and ORM integration suites also passed against TypeDB 3.12.3.
Driver integration took 18.0 seconds. ORM integration took 122.6 seconds.
The Rust release library was rebuilt before the live experiments.
