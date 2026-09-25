package gotype

import (
	"context"

	"github.com/CaliLuke/go-typeql/v3/internal/perftrace"
)

func init() {
	perftrace.RegisterGauge(perftrace.Gauge{
		Name:        "typedb_go.gotype.tx_contexts_active",
		Description: "TransactionContext values that are not done",
		Read:        ActiveTransactionContexts,
	})
}

// startOp starts the span of one public gotype operation. The span name is a
// constant, so the disabled path does not allocate.
func (m *Manager[T]) startOp(ctx context.Context, name string) (context.Context, *perftrace.Span) {
	ctx, span := perftrace.Start(ctx, name)
	if span.Recording() {
		span.SetAttrs(
			perftrace.String("gotype.type", m.info.TypeName),
			perftrace.Bool("gotype.bound_tx", m.tx != nil),
		)
	}
	return ctx, span
}
