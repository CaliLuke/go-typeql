//go:build cgo && typedb && integration

package driver

import (
	"context"
	"fmt"
	"os"
	"sync"
	"testing"

	"github.com/CaliLuke/go-typeql/v3/internal/perftrace"
	"github.com/CaliLuke/go-typeql/v3/internal/perftrace/otelperf"
)

// TestMain turns on performance tracing when TYPEDB_GO_PERFTRACE=1 (see
// docs/PERFORMANCE_TRACING.md). Benchmark runs are never traced.
func TestMain(m *testing.M) {
	shutdownTracing, err := otelperf.InstallFromEnv("go-typeql-driver-tests")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	code := m.Run()
	if err := shutdownTracing(context.Background()); err != nil {
		fmt.Fprintln(os.Stderr, err)
	}
	os.Exit(code)
}

// closeSpanTracer records the typedb.tx.close spans. The close workers call
// it from other goroutines.
type closeSpanTracer struct {
	mu     sync.Mutex
	inline []bool
}

func (c *closeSpanTracer) Start(ctx context.Context, name string) (context.Context, perftrace.SpanRecorder) {
	return ctx, &closeSpanRecorder{c: c, name: name}
}

type closeSpanRecorder struct {
	c    *closeSpanTracer
	name string
}

func (r *closeSpanRecorder) SetAttrs(attrs []perftrace.Attr) {
	if r.name != "typedb.tx.close" {
		return
	}
	for _, a := range attrs {
		if a.Key == "typedb.tx.close.inline" {
			r.c.mu.Lock()
			r.c.inline = append(r.c.inline, a.Int != 0)
			r.c.mu.Unlock()
		}
	}
}

func (r *closeSpanRecorder) End(error) {}

func TestTracing_CloseSpans(t *testing.T) {
	conn, name := setupLifecycleDB(t, `define attribute name, value string;`)
	rec := &closeSpanTracer{}
	uninstall := perftrace.Install(rec)
	defer uninstall()

	checked, err := conn.Transaction(name, Read)
	if err != nil {
		t.Fatal(err)
	}
	if err := checked.CloseChecked(); err != nil {
		t.Fatalf("CloseChecked: %v", err)
	}
	queued, err := conn.Transaction(name, Read)
	if err != nil {
		t.Fatal(err)
	}
	queued.Close()
	if err := WaitForPendingCloses(context.Background()); err != nil {
		t.Fatal(err)
	}

	rec.mu.Lock()
	defer rec.mu.Unlock()
	if len(rec.inline) != 2 || !rec.inline[0] || rec.inline[1] {
		t.Fatalf("close spans inline = %v, want [true false] (CloseChecked, then Close)", rec.inline)
	}
}
