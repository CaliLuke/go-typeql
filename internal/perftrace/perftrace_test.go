package perftrace

import (
	"context"
	"errors"
	"testing"
)

type fakeTracer struct {
	started []string
	ended   []error
	attrs   []Attr
}

func (f *fakeTracer) Start(ctx context.Context, name string) (context.Context, SpanRecorder) {
	f.started = append(f.started, name)
	return ctx, fakeRecorder{f}
}

type fakeRecorder struct{ f *fakeTracer }

func (r fakeRecorder) SetAttrs(attrs []Attr) { r.f.attrs = append(r.f.attrs, attrs...) }
func (r fakeRecorder) End(err error)         { r.f.ended = append(r.f.ended, err) }

func TestDisabledSpanIsNoop(t *testing.T) {
	ctx := context.Background()
	got, span := Start(ctx, "op")
	if got != ctx || span != nil || span.Recording() || Enabled() {
		t.Fatalf("disabled Start = (%v, %v), want the same ctx and nil", got, span)
	}
	span.SetAttrs(String("k", "v"))
	span.End(errors.New("ignored"))
}

func TestDisabledStartDoesNotAllocate(t *testing.T) {
	ctx := context.Background()
	allocs := testing.AllocsPerRun(1000, func() {
		_, span := Start(ctx, "op")
		if span.Recording() {
			span.SetAttrs(String("k", "v"), Int("n", 1))
		}
		span.End(nil)
	})
	if allocs != 0 {
		t.Fatalf("disabled path allocates %v times per call, want 0", allocs)
	}
}

func TestInstalledTracerRecords(t *testing.T) {
	f := &fakeTracer{}
	uninstall := Install(f)
	var nilCtx context.Context
	_, span := Start(nilCtx, "op")
	span.SetAttrs(Bool("b", true), Int("n", 7))
	failure := errors.New("boom")
	span.End(failure)
	uninstall()

	if len(f.started) != 1 || f.started[0] != "op" {
		t.Fatalf("started = %v, want [op]", f.started)
	}
	if len(f.ended) != 1 || !errors.Is(f.ended[0], failure) {
		t.Fatalf("ended = %v, want [boom]", f.ended)
	}
	if len(f.attrs) != 2 || f.attrs[0].Kind != KindBool || f.attrs[0].Int != 1 || f.attrs[1].Int != 7 {
		t.Fatalf("attrs = %+v", f.attrs)
	}
	if Enabled() {
		t.Fatal("tracer still installed after uninstall")
	}
}

func TestUninstallKeepsNewerTracer(t *testing.T) {
	first := Install(&fakeTracer{})
	second := Install(&fakeTracer{})
	first()
	if !Enabled() {
		t.Fatal("an old uninstall removed the newer tracer")
	}
	second()
}
