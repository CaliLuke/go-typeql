//go:build cgo && typedb

package driver

import (
	"context"

	"github.com/CaliLuke/go-typeql/v3/internal/perftrace"
)

// maxTracedQueryLen limits the query text copied into a span.
const maxTracedQueryLen = 4096

func init() {
	perftrace.RegisterGauge(perftrace.Gauge{
		Name:        "typedb_go.tx.open_inflight",
		Description: "Driver transactions that are open",
		Read:        activeTxOpen.Load,
	})
	perftrace.RegisterGauge(perftrace.Gauge{
		Name:        "typedb_go.tx.query_inflight",
		Description: "Driver queries that are in an FFI call",
		Read:        activeTxQuery.Load,
	})
}

// traceParent is the context that parents the spans of a query: the caller
// context when the query has one, else the context that opened the
// transaction.
func (t *Transaction) traceParent(meta *queryMetadata) context.Context {
	if meta.ctx != nil {
		return meta.ctx
	}
	return t.traceCtx
}

func (t *Transaction) startQuerySpan(meta *queryMetadata, name string) (context.Context, *perftrace.Span) {
	ctx, span := perftrace.Start(t.traceParent(meta), name)
	if span.Recording() {
		op, fp := meta.values()
		text := meta.query
		if len(text) > maxTracedQueryLen {
			text = text[:maxTracedQueryLen]
		}
		span.SetAttrs(
			perftrace.Int("typedb.tx.id", int(t.id)),
			perftrace.String("db.namespace", t.dbName),
			perftrace.Int("typedb.tx.type", int(t.txType)),
			perftrace.String("typedb.query.op", op),
			perftrace.String("typedb.query.fingerprint", fp),
			perftrace.Int("typedb.query.len", len(meta.query)),
			perftrace.String("db.query.text", text),
		)
	}
	return ctx, span
}

func (t *Transaction) startTxSpan(name string) *perftrace.Span {
	_, span := perftrace.Start(t.traceCtx, name)
	if span.Recording() {
		span.SetAttrs(
			perftrace.Int("typedb.tx.id", int(t.id)),
			perftrace.String("db.namespace", t.dbName),
			perftrace.Int("typedb.tx.type", int(t.txType)),
		)
	}
	return span
}

// traceContext keeps ctx for later transaction spans, only while tracing,
// so the disabled path does not retain caller contexts.
func traceContext(ctx context.Context) context.Context {
	if !perftrace.Enabled() {
		return nil
	}
	return ctx
}
