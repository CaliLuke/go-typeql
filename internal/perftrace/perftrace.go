// Package perftrace is the tracing hook for performance work in this
// repository. The library code calls Start at interesting points (a gotype
// operation, a transaction open, an FFI call, a decode). When no tracer is
// installed, Start costs one atomic load and returns a nil *Span, and every
// *Span method is a no-op on nil.
//
// Only in-repository test code installs a tracer (see the otelperf package),
// so library users never see this package or its dependencies. Do not
// install a tracer for benchmarks: benchmark numbers must not include the
// cost of tracing.
package perftrace

import (
	"context"
	"sync"
	"sync/atomic"
)

// Kind is the value type of an Attr.
type Kind uint8

// The Attr value kinds.
const (
	KindString Kind = iota
	KindInt
	KindBool
)

// Attr is one span attribute. It has no interface value, so building an
// Attr does not allocate.
type Attr struct {
	Key  string
	Kind Kind
	Str  string
	Int  int64
}

// String returns a string attribute.
func String(key, value string) Attr { return Attr{Key: key, Kind: KindString, Str: value} }

// Int returns an integer attribute.
func Int(key string, value int) Attr { return Attr{Key: key, Kind: KindInt, Int: int64(value)} }

// Bool returns a boolean attribute.
func Bool(key string, value bool) Attr {
	var n int64
	if value {
		n = 1
	}
	return Attr{Key: key, Kind: KindBool, Int: n}
}

// Tracer is the backend that records spans.
type Tracer interface {
	// Start starts a span as a child of the span in ctx, if there is one.
	Start(ctx context.Context, name string) (context.Context, SpanRecorder)
}

// SpanRecorder is one recording span of a Tracer.
type SpanRecorder interface {
	SetAttrs(attrs []Attr)
	End(err error)
}

// Span is a span handle. A nil *Span is valid and records nothing.
type Span struct {
	rec SpanRecorder
}

type tracerBox struct{ t Tracer }

var active atomic.Pointer[tracerBox]

// Install makes t the active tracer and returns a function that removes it.
func Install(t Tracer) (uninstall func()) {
	box := &tracerBox{t: t}
	active.Store(box)
	return func() { active.CompareAndSwap(box, nil) }
}

// Enabled reports whether a tracer is installed.
func Enabled() bool { return active.Load() != nil }

// Start starts a span. With no tracer installed it returns ctx and nil.
// A nil ctx is treated as context.Background.
func Start(ctx context.Context, name string) (context.Context, *Span) {
	box := active.Load()
	if box == nil {
		return ctx, nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, rec := box.t.Start(ctx, name)
	return ctx, &Span{rec: rec}
}

// Recording reports whether s records anything. Build attributes only when
// it is true, so the disabled path does not allocate.
func (s *Span) Recording() bool { return s != nil }

// SetAttrs adds attributes to s.
func (s *Span) SetAttrs(attrs ...Attr) {
	if s == nil {
		return
	}
	s.rec.SetAttrs(attrs)
}

// End ends s. A non-nil err marks the span as failed.
func (s *Span) End(err error) {
	if s == nil {
		return
	}
	s.rec.End(err)
}

// Gauge is a named int64 value that the tracer samples as a metric.
type Gauge struct {
	Name        string
	Description string
	Read        func() int64
}

var (
	gaugesMu sync.Mutex
	gauges   []Gauge
)

// RegisterGauge adds a gauge. Packages call it from init; the tracer reads
// the list when it is installed. Registration costs nothing at run time.
func RegisterGauge(g Gauge) {
	gaugesMu.Lock()
	defer gaugesMu.Unlock()
	gauges = append(gauges, g)
}

// Gauges returns a copy of the registered gauges.
func Gauges() []Gauge {
	gaugesMu.Lock()
	defer gaugesMu.Unlock()
	return append([]Gauge(nil), gauges...)
}
