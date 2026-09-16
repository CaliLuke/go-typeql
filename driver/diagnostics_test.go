//go:build cgo && typedb

package driver

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestQueryMetadataConcurrentReuse(t *testing.T) {
	meta := &queryMetadata{query: "match $e isa person; fetch { \"_iid\": iid($e) };"}
	wantOp, wantFP := queryOperation(meta.query), queryFingerprint(meta.query)
	var wg sync.WaitGroup
	for range 16 {
		wg.Go(func() {
			for range 100 {
				op, fp := meta.values()
				if op != wantOp || fp != wantFP {
					t.Errorf("metadata=%q/%q, want %q/%q", op, fp, wantOp, wantFP)
				}
			}
		})
	}
	wg.Wait()
}

func TestDiagnosticsLazyAndCancellation(t *testing.T) {
	var output bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&output, nil)))
	t.Cleanup(func() { slog.SetDefault(old) })
	t.Setenv("TYPEDB_GO_DEBUG", "false")
	t.Setenv("TYPEDB_GO_DEBUG_SLOW_MS", "2000")
	called := false
	logFFIDurationLazy("tx.query", time.Now(), func() []any {
		called = true
		return []any{"diagnostic", "unexpected"}
	})
	logFFIDebugLazy("tx.query_with_context.start", func() []any {
		called = true
		return nil
	})
	if called || output.Len() != 0 {
		t.Fatal("disabled logging evaluated fields")
	}
	logInFlightLazy("tx.query_inflight", 1, 1, 2, func() []any {
		called = true
		return []any{"query_op", "match"}
	})
	if called || output.Len() != 0 {
		t.Fatal("below-threshold counter warning evaluated fields")
	}
	logInFlightLazy("tx.query_inflight", 3, 3, 2, func() []any {
		called = true
		return []any{"query_op", "match"}
	})
	if !called || !strings.Contains(output.String(), "tx.query_inflight.high") || !strings.Contains(output.String(), "query_op=match") {
		t.Fatalf("missing high-water warning: %q", output.String())
	}
	output.Reset()

	t.Setenv("TYPEDB_GO_DEBUG", "true")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	tx := &Transaction{id: 12, dbName: "diagnostic"}
	if _, err := tx.QueryWithContextAndOptions(ctx, "match $e isa person;", nil, nil); err != context.Canceled {
		t.Fatalf("cancelled query: %v", err)
	}
	logged := output.String()
	for _, token := range []string{"tx.query_with_context.cancelled", "query_op=match", "query_fingerprint=", "context canceled"} {
		if !strings.Contains(logged, token) {
			t.Fatalf("missing %q in log %q", token, logged)
		}
	}
	output.Reset()
	logTransactionClose(transactionCloseJob{id: 13, dbName: "diagnostic", start: time.Now().Add(-3 * time.Second)}, nil)
	if !strings.Contains(output.String(), "tx.close.slow") {
		t.Fatalf("missing async close slow warning: %q", output.String())
	}
}

// BenchmarkQueryDiagnostics isolates disabled, enabled and slow-warning paths.
// Run with -benchtime=2000x -count=5.
func BenchmarkQueryDiagnostics(b *testing.B) {
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(io.Discard, nil)))
	b.Cleanup(func() { slog.SetDefault(old) })
	for _, tc := range []struct {
		name  string
		debug string
		slow  bool
	}{
		{"disabled", "false", false},
		{"enabled", "true", false},
		{"slow-warning", "false", true},
	} {
		b.Run(tc.name, func(b *testing.B) {
			b.Setenv("TYPEDB_GO_DEBUG", tc.debug)
			b.Setenv("TYPEDB_GO_DEBUG_SLOW_MS", "2000")
			tx := &Transaction{id: 1, dbName: "bench"}
			query := "match $e isa person; fetch { \"_iid\": iid($e) };"
			b.ReportAllocs()
			for range b.N {
				meta := &queryMetadata{query: query}
				start := time.Now()
				if tc.slow {
					start = start.Add(-3 * time.Second)
				}
				tx.logQueryDuration(start, meta, 1, 24, nil, func() []any { return []any{"with_options", false, "with_rows", false} })
			}
		})
	}
}
