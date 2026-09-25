// Package otelperf connects perftrace to OpenTelemetry. It exports spans and
// metrics over OTLP/HTTP to the repository's own logal collector (see
// perf/logal.yaml and docs/PERFORMANCE_TRACING.md).
//
// Use it for performance work only. It refuses to start inside a benchmark
// run, because tracing changes the numbers that benchmarks record.
package otelperf

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"sync/atomic"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/metric"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.40.0"
	"go.opentelemetry.io/otel/trace"

	"github.com/CaliLuke/go-typeql/v3/internal/perftrace"
)

// EnvEnable is the environment variable that turns tracing on ("1").
const EnvEnable = "TYPEDB_GO_PERFTRACE"

// DefaultEndpoint is the OTLP/HTTP endpoint of the repository's logal
// instance (perf/logal.yaml). OTEL_EXPORTER_OTLP_ENDPOINT overrides it.
const DefaultEndpoint = "http://127.0.0.1:44318"

const scopeName = "github.com/CaliLuke/go-typeql/v3/internal/perftrace"

// ErrBenchmark is returned when Install is called in a benchmark run.
var ErrBenchmark = errors.New("otelperf: tracing is disabled for benchmark runs")

// Shutdown flushes and stops the exporters.
type Shutdown func(context.Context) error

// InstallFromEnv installs the tracer when TYPEDB_GO_PERFTRACE=1. It returns
// a no-op Shutdown when tracing is off or when the process is a benchmark
// run (a -test.bench pattern is set).
func InstallFromEnv(service string) (Shutdown, error) {
	noop := func(context.Context) error { return nil }
	if os.Getenv(EnvEnable) != "1" {
		return noop, nil
	}
	if benchmarkRun() {
		fmt.Fprintf(os.Stderr, "otelperf: %s=1 ignored: benchmark run\n", EnvEnable)
		return noop, nil
	}
	return Install(context.Background(), service)
}

// Install starts the OTLP exporters and installs the perftrace tracer.
func Install(ctx context.Context, service string) (Shutdown, error) {
	if benchmarkRun() {
		return nil, ErrBenchmark
	}
	endpoint := DefaultEndpoint
	if os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT") != "" {
		endpoint = "" // the exporters read the standard variables themselves
	}

	res, err := resource.Merge(resource.Default(), resource.NewSchemaless(
		semconv.ServiceName(service),
		attribute.String("typedb_go.run_id", fmt.Sprintf("%d-%d", os.Getpid(), time.Now().Unix())),
	))
	if err != nil {
		return nil, fmt.Errorf("otelperf: resource: %w", err)
	}

	var traceOpts []otlptracehttp.Option
	var metricOpts []otlpmetrichttp.Option
	if endpoint != "" {
		traceOpts = append(traceOpts, otlptracehttp.WithEndpointURL(endpoint+"/v1/traces"))
		metricOpts = append(metricOpts, otlpmetrichttp.WithEndpointURL(endpoint+"/v1/metrics"))
	}
	traceExp, err := otlptracehttp.New(ctx, traceOpts...)
	if err != nil {
		return nil, fmt.Errorf("otelperf: trace exporter: %w", err)
	}
	metricExp, err := otlpmetrichttp.New(ctx, metricOpts...)
	if err != nil {
		_ = traceExp.Shutdown(ctx)
		return nil, fmt.Errorf("otelperf: metric exporter: %w", err)
	}

	tp := sdktrace.NewTracerProvider(sdktrace.WithResource(res), sdktrace.WithBatcher(traceExp))
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithResource(res),
		sdkmetric.WithReader(sdkmetric.NewPeriodicReader(metricExp, sdkmetric.WithInterval(2*time.Second))))

	t, err := newTracer(tp.Tracer(scopeName), mp.Meter(scopeName))
	if err != nil {
		_ = tp.Shutdown(ctx)
		_ = mp.Shutdown(ctx)
		return nil, err
	}
	uninstall := perftrace.Install(t)
	current.Store(t)

	return func(ctx context.Context) error {
		uninstall()
		current.CompareAndSwap(t, nil)
		return errors.Join(tp.Shutdown(ctx), mp.Shutdown(ctx))
	}, nil
}

// StartTest starts a root span for one test. Until end is called, spans
// that start without a parent in their context become its children, so one
// test is one trace. Tests that call it must not run in parallel.
// It is a no-op when the tracer is not installed.
func StartTest(name string) (end func(failed bool)) {
	t := current.Load()
	if t == nil {
		return func(bool) {}
	}
	ctx, span := t.tracer.Start(context.Background(), "test "+name,
		trace.WithAttributes(attribute.String("test.name", name)))
	t.ambient.Store(&ctx)
	return func(failed bool) {
		t.ambient.CompareAndSwap(&ctx, nil)
		if failed {
			span.SetStatus(codes.Error, "test failed")
		}
		span.End()
	}
}

var current atomic.Pointer[tracer]

type tracer struct {
	tracer   trace.Tracer
	duration metric.Float64Histogram
	ambient  atomic.Pointer[context.Context]
}

func newTracer(tr trace.Tracer, m metric.Meter) (*tracer, error) {
	duration, err := m.Float64Histogram("typedb_go.span.duration",
		metric.WithUnit("ms"),
		metric.WithDescription("Duration of each perftrace span, by span name"))
	if err != nil {
		return nil, fmt.Errorf("otelperf: histogram: %w", err)
	}
	for _, g := range perftrace.Gauges() {
		read := g.Read
		_, err := m.Int64ObservableGauge(g.Name, metric.WithDescription(g.Description),
			metric.WithInt64Callback(func(_ context.Context, o metric.Int64Observer) error {
				o.Observe(read())
				return nil
			}))
		if err != nil {
			return nil, fmt.Errorf("otelperf: gauge %s: %w", g.Name, err)
		}
	}
	return &tracer{tracer: tr, duration: duration}, nil
}

func (t *tracer) Start(ctx context.Context, name string) (context.Context, perftrace.SpanRecorder) {
	if !trace.SpanContextFromContext(ctx).IsValid() {
		if amb := t.ambient.Load(); amb != nil {
			ctx = trace.ContextWithSpan(ctx, trace.SpanFromContext(*amb))
		}
	}
	ctx, span := t.tracer.Start(ctx, name)
	return ctx, &recorder{t: t, span: span, name: name, start: time.Now()}
}

type recorder struct {
	t       *tracer
	span    trace.Span
	name    string
	queryOp string
	start   time.Time
}

func (r *recorder) SetAttrs(attrs []perftrace.Attr) {
	kvs := make([]attribute.KeyValue, 0, len(attrs))
	for _, a := range attrs {
		switch a.Kind {
		case perftrace.KindString:
			kvs = append(kvs, attribute.String(a.Key, a.Str))
			if a.Key == "typedb.query.op" {
				r.queryOp = a.Str
			}
		case perftrace.KindInt:
			kvs = append(kvs, attribute.Int64(a.Key, a.Int))
		case perftrace.KindBool:
			kvs = append(kvs, attribute.Bool(a.Key, a.Int != 0))
		}
	}
	r.span.SetAttributes(kvs...)
}

func (r *recorder) End(err error) {
	if err != nil {
		r.span.RecordError(err)
		r.span.SetStatus(codes.Error, err.Error())
	}
	r.span.End()
	attrs := []attribute.KeyValue{attribute.String("span.name", r.name), attribute.Bool("error", err != nil)}
	if r.queryOp != "" {
		attrs = append(attrs, attribute.String("typedb.query.op", r.queryOp))
	}
	elapsed := float64(time.Since(r.start).Microseconds()) / 1000
	r.t.duration.Record(context.Background(), elapsed, metric.WithAttributes(attrs...))
}

// benchmarkRun reports whether this test binary runs benchmarks. It parses
// the command line first when TestMain has not done so yet; testing.M.Run
// does not parse it again.
func benchmarkRun() bool {
	f := flag.Lookup("test.bench")
	if f == nil {
		return false
	}
	if !flag.Parsed() {
		flag.Parse()
	}
	return f.Value.String() != ""
}
