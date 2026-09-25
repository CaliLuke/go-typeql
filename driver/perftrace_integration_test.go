//go:build cgo && typedb && integration

package driver

import (
	"context"
	"fmt"
	"os"
	"testing"

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
