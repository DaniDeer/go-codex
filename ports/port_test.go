package ports_test

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/DaniDeer/go-codex/api/events"
	apimcp "github.com/DaniDeer/go-codex/api/mcp"
	"github.com/DaniDeer/go-codex/api/reqreply"
	"github.com/DaniDeer/go-codex/api/rest"
	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/format"
	"github.com/DaniDeer/go-codex/ports"
	"github.com/DaniDeer/go-codex/route"
	"github.com/DaniDeer/go-codex/stats"
	gstream "github.com/DaniDeer/go-codex/stream"
	"github.com/DaniDeer/go-codex/validate"
)

// ── shared helpers ────────────────────────────────────────────────────────────

var intCodec = codex.Int()
var strCodec = codex.String()

func intPort(name string, buf int) *ports.SourcePort[int] {
	p, err := ports.NewSourcePort[int](name, intCodec, ports.PortOptions{Buffer: buf})
	if err != nil {
		panic(err) // never happens for a Patterns-less port
	}
	return p
}

func intSinkPort(name string, buf int) *ports.SinkPort[int] {
	p, err := ports.NewSinkPort[int](name, intCodec, ports.PortOptions{Buffer: buf})
	if err != nil {
		panic(err) // never happens for a Patterns-less port
	}
	return p
}

func feedChan(vals ...int) <-chan int {
	ch := make(chan int, len(vals))
	for _, v := range vals {
		ch <- v
	}
	close(ch)
	return ch
}

func collectStream[T any](ctx context.Context, s gstream.Stream[T]) ([]T, []error) {
	return gstream.Collect(ctx, s)
}

// ── T01: SourcePort single adapter ───────────────────────────────────────────

func TestSourcePort_SingleAdapter(t *testing.T) {
	ctx := context.Background()
	p := intPort("test", 4)
	p.Bind(ctx, ports.ChanSourceAdapter(feedChan(1, 2, 3)))
	vals, errs := collectStream(ctx, p.Stream(ctx))
	if len(vals) != 3 {
		t.Errorf("want 3 values, got %d", len(vals))
	}
	if len(errs) != 0 {
		t.Errorf("want 0 errors, got %d: %v", len(errs), errs)
	}
}

// ── T02: SourcePort fan-in (two adapters) ────────────────────────────────────

func TestSourcePort_FanIn(t *testing.T) {
	ctx := context.Background()
	p := intPort("test", 8)
	p.Bind(ctx, ports.ChanSourceAdapter(feedChan(1, 2)))
	p.Bind(ctx, ports.ChanSourceAdapter(feedChan(3, 4)))
	vals, errs := collectStream(ctx, p.Stream(ctx))
	if len(vals) != 4 {
		t.Errorf("want 4 values, got %d", len(vals))
	}
	if len(errs) != 0 {
		t.Errorf("want 0 errors, got %d", len(errs))
	}
}

// ── T03: SourcePort with no adapters closes immediately ──────────────────────

func TestSourcePort_NoAdapters(t *testing.T) {
	ctx := context.Background()
	p := intPort("test", 4)
	// No Bind calls — stream should close immediately
	vals, errs := collectStream(ctx, p.Stream(ctx))
	if len(vals) != 0 {
		t.Errorf("want 0 values, got %d", len(vals))
	}
	if len(errs) != 0 {
		t.Errorf("want 0 errors, got %d", len(errs))
	}
}

// ── T04: SourcePort ctx cancel stops adapter ─────────────────────────────────

func TestSourcePort_CtxCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	p := intPort("test", 8)

	// Infinite source channel (never closes)
	infinite := make(chan int, 4)
	infinite <- 1
	infinite <- 2
	p.Bind(ctx, ports.ChanSourceAdapter(infinite))
	s := p.Stream(ctx)
	cancel()
	// After cancel, stream should terminate
	vals, _ := collectStream(context.Background(), s)
	if len(vals) > 2 {
		t.Errorf("want at most 2 values after cancel, got %d", len(vals))
	}
}

// ── T05: SourcePort error from adapter reaches Stream.Errors ─────────────────

func TestSourcePort_AdapterErrorReachesStreamErrors(t *testing.T) {
	ctx := context.Background()

	// Custom adapter that emits one error
	errAdapter := &errSourceAdapter{err: errors.New("sensor offline")}
	p := intPort("test", 4)
	p.Bind(ctx, errAdapter)
	_, errs := collectStream(ctx, p.Stream(ctx))
	if len(errs) == 0 {
		t.Fatal("want error in Stream.Errors, got none")
	}
}

type errSourceAdapter struct{ err error }

func (a *errSourceAdapter) AdapterName() string { return "errSourceAdapter" }
func (a *errSourceAdapter) Activate(_ context.Context, _ chan<- int, errs chan<- error) {
	errs <- a.err
}

// ── T06: SinkPort single adapter ─────────────────────────────────────────────

func TestSinkPort_SingleAdapter(t *testing.T) {
	ctx := context.Background()
	p := intSinkPort("test", 4)
	out := make(chan int, 8)
	p.Bind(ctx, ports.ChanSinkAdapter(out))

	src := gstream.From(ctx, feedChan(10, 20, 30))
	p.Feed(ctx, src) // blocks until adapter goroutine finishes

	var got []int
	for len(out) > 0 {
		got = append(got, <-out)
	}
	if len(got) != 3 {
		t.Errorf("want 3 values in sink, got %d: %v", len(got), got)
	}
}

// ── T07: SinkPort fan-out (two adapters) ─────────────────────────────────────

func TestSinkPort_FanOut(t *testing.T) {
	ctx := context.Background()
	p := intSinkPort("test", 8)
	out1 := make(chan int, 8)
	out2 := make(chan int, 8)
	p.Bind(ctx, ports.ChanSinkAdapter(out1))
	p.Bind(ctx, ports.ChanSinkAdapter(out2))

	src := gstream.From(ctx, feedChan(1, 2, 3))
	p.Feed(ctx, src) // blocks until both adapter goroutines finish

	var got1, got2 []int
	for len(out1) > 0 {
		got1 = append(got1, <-out1)
	}
	for len(out2) > 0 {
		got2 = append(got2, <-out2)
	}
	if len(got1) != 3 || len(got2) != 3 {
		t.Errorf("want 3 in each sink, got %d and %d", len(got1), len(got2))
	}
}

// ── T08: IOPort happy path ────────────────────────────────────────────────────

func TestIOPort_HappyPath(t *testing.T) {
	ctx := context.Background()
	p, err := ports.NewIOPort[int, string]("double-str", intCodec, strCodec, ports.PortOptions{Buffer: 4})
	if err != nil {
		t.Fatalf("construct port: %v", err)
	}
	if err := p.Bind(ctx, ports.FuncIOAdapter(func(_ context.Context, v int) (string, error) {
		return fmt.Sprintf("%d", v*2), nil
	})); err != nil {
		t.Fatalf("Bind: %v", err)
	}

	src := gstream.From(ctx, feedChan(1, 2, 3))
	out := p.Connect(ctx, src)
	vals, errs := collectStream(ctx, out)
	if len(vals) != 3 {
		t.Errorf("want 3 values, got %d", len(vals))
	}
	if len(errs) != 0 {
		t.Errorf("want 0 errors, got %d: %v", len(errs), errs)
	}
	if vals[0] != "2" || vals[1] != "4" || vals[2] != "6" {
		t.Errorf("want [2 4 6], got %v", vals)
	}
}

// ── T09: IOPort no adapter → PortNoAdapterError ──────────────────────────────

func TestIOPort_NoAdapterError(t *testing.T) {
	ctx := context.Background()
	p, err := ports.NewIOPort[int, string]("test", intCodec, strCodec, ports.PortOptions{Buffer: 4})
	if err != nil {
		t.Fatalf("construct port: %v", err)
	}
	// No Bind

	src := gstream.From(ctx, feedChan(1))
	out := p.Connect(ctx, src)
	_, errs := collectStream(ctx, out)
	if len(errs) == 0 {
		t.Fatal("want PortNoAdapterError in Stream.Errors")
	}
	var nae ports.PortNoAdapterError
	if !errors.As(errs[0], &nae) {
		t.Errorf("want PortNoAdapterError, got %T: %v", errs[0], errs[0])
	}
	if nae.Port != "test" {
		t.Errorf("Port: want %q, got %q", "test", nae.Port)
	}
}

// ── T10: IOPort double bind → PortBindError ───────────────────────────────────

func TestIOPort_DoubleBind(t *testing.T) {
	ctx := context.Background()
	p, err := ports.NewIOPort[int, string]("test", intCodec, strCodec, ports.PortOptions{})
	if err != nil {
		t.Fatalf("construct port: %v", err)
	}
	fn := ports.FuncIOAdapter(func(_ context.Context, v int) (string, error) { return "", nil })

	if err := p.Bind(ctx, fn); err != nil {
		t.Fatalf("first Bind: unexpected error: %v", err)
	}
	err = p.Bind(ctx, fn)
	if err == nil {
		t.Fatal("second Bind: want PortBindError, got nil")
	}
	var pbe ports.PortBindError
	if !errors.As(err, &pbe) {
		t.Errorf("want PortBindError, got %T: %v", err, err)
	}
}

// ── T11: IOPort adapter error routes to Stream.Errors ────────────────────────

func TestIOPort_AdapterErrorInStreamErrors(t *testing.T) {
	ctx := context.Background()
	p, err := ports.NewIOPort[int, string]("test", intCodec, strCodec, ports.PortOptions{Buffer: 4})
	if err != nil {
		t.Fatalf("construct port: %v", err)
	}
	p.Bind(ctx, ports.FuncIOAdapter(func(_ context.Context, _ int) (string, error) { //nolint:errcheck
		return "", errors.New("enrichment failure")
	}))

	src := gstream.From(ctx, feedChan(1, 2))
	out := p.Connect(ctx, src)
	vals, errs := collectStream(ctx, out)
	if len(vals) != 0 {
		t.Errorf("want 0 values, got %d", len(vals))
	}
	if len(errs) != 2 {
		t.Errorf("want 2 errors, got %d: %v", len(errs), errs)
	}
}

// ── T12: ChanSourceAdapter pushes items ──────────────────────────────────────

func TestChanSourceAdapter(t *testing.T) {
	ctx := context.Background()
	p := intPort("test", 4)
	p.Bind(ctx, ports.ChanSourceAdapter(feedChan(7, 8, 9)))
	vals, _ := collectStream(ctx, p.Stream(ctx))
	if len(vals) != 3 {
		t.Errorf("want 3, got %d", len(vals))
	}
}

// ── T13: ChanSinkAdapter captures items ──────────────────────────────────────

func TestChanSinkAdapter(t *testing.T) {
	ctx := context.Background()
	p := intSinkPort("test", 4)
	out := make(chan int, 8)
	p.Bind(ctx, ports.ChanSinkAdapter(out))
	src := gstream.From(ctx, feedChan(1, 2, 3))
	p.Feed(ctx, src)
	var got []int
	for len(out) > 0 {
		got = append(got, <-out)
	}
	if len(got) != 3 {
		t.Errorf("want 3, got %d", len(got))
	}
}

// ── T14: FuncIOAdapter transforms correctly ───────────────────────────────────

func TestFuncIOAdapter(t *testing.T) {
	ctx := context.Background()
	adapter := ports.FuncIOAdapter(func(_ context.Context, v int) (int, error) {
		return v * 10, nil
	})
	src := gstream.From(ctx, feedChan(1, 2, 3))
	out := adapter.Transform(ctx, src)
	vals, errs := collectStream(ctx, out)
	if len(vals) != 3 || vals[0] != 10 || vals[1] != 20 || vals[2] != 30 {
		t.Errorf("want [10 20 30], got %v", vals)
	}
	if len(errs) != 0 {
		t.Errorf("want 0 errors, got %v", errs)
	}
}

// ── T15: PortBindError LogValue ───────────────────────────────────────────────

func TestPortBindError_LogValue(t *testing.T) {
	inner := errors.New("connect refused")
	e := ports.PortBindError{Port: "sensor-readings", Adapter: "mqtt5.SubscribeAdapter", Err: inner}

	v := e.LogValue()
	if v.Kind() != slog.KindGroup {
		t.Fatalf("want KindGroup, got %v", v.Kind())
	}
	attrs := v.Group()
	keys := map[string]bool{}
	for _, a := range attrs {
		keys[a.Key] = true
	}
	for _, want := range []string{"port", "adapter", "err"} {
		if !keys[want] {
			t.Errorf("missing attribute %q", want)
		}
	}
	if errors.Unwrap(e) != inner {
		t.Error("Unwrap must return inner error")
	}
}

// ── T16: PortNoAdapterError LogValue ─────────────────────────────────────────

func TestPortNoAdapterError_LogValue(t *testing.T) {
	e := ports.PortNoAdapterError{Port: "calibration"}
	v := e.LogValue()
	if v.Kind() != slog.KindGroup {
		t.Fatalf("want KindGroup, got %v", v.Kind())
	}
	attrs := v.Group()
	if len(attrs) == 0 || attrs[0].Key != "port" {
		t.Errorf("want 'port' attribute, got %v", attrs)
	}
}

// ── T17: IOParam WithCodec ────────────────────────────────────────────────────

func TestIOParam_WithCodec(t *testing.T) {
	original := ports.IOParam{Name: "sensorID", Required: true}
	if original.Codec != nil {
		t.Fatal("original should have nil Codec")
	}
	c := codex.String()
	updated := original.WithCodec(c)
	if updated.Codec == nil {
		t.Error("updated should have non-nil Codec")
	}
	if original.Codec != nil {
		t.Error("WithCodec must not mutate the original")
	}
	if updated.Name != "sensorID" {
		t.Error("WithCodec must preserve other fields")
	}
}

// ── ValidateParams / WithParams / ParamsFromContext ──────────────────────────

func TestValidateParams_MissingRequired(t *testing.T) {
	params := []ports.IOParam{{Name: "sensorID", Required: true}}
	err := ports.ValidateParams(params, map[string]string{})
	if err == nil {
		t.Fatal("want error for missing required param")
	}
	var ve codex.ValidationErrors
	if !errors.As(err, &ve) {
		t.Fatalf("want codex.ValidationErrors, got %T", err)
	}
	if len(ve) != 1 || ve[0].Field != "sensorID" {
		t.Errorf("want single error for field sensorID, got %v", ve)
	}
	if !errors.Is(ve[0].Err, ports.ErrParamMissing) {
		t.Errorf("want ErrParamMissing, got %v", ve[0].Err)
	}
}

func TestValidateParams_CodecFailure(t *testing.T) {
	uuidCodec := codex.String()
	uuidCodec.Decode = func(v any) (string, error) {
		s, _ := v.(string)
		if len(s) != 36 {
			return "", fmt.Errorf("not a uuid: %q", s)
		}
		return s, nil
	}
	sensorIDParam := ports.IOParam{Name: "sensorID"}.WithCodec(uuidCodec)
	params := []ports.IOParam{sensorIDParam}

	if err := ports.ValidateParams(params, map[string]string{"sensorID": "not-a-uuid"}); err == nil {
		t.Fatal("want codec validation error")
	} else {
		var ve codex.ValidationErrors
		if !errors.As(err, &ve) || len(ve) != 1 || ve[0].Field != "sensorID" {
			t.Errorf("want single ValidationError for sensorID, got %v", err)
		}
	}

	valid := "123456789012345678901234567890123456" // 36 chars
	if err := ports.ValidateParams(params, map[string]string{"sensorID": valid}); err != nil {
		t.Errorf("want nil for valid value, got %v", err)
	}
}

func TestValidateParams_OptionalMissingIsOK(t *testing.T) {
	params := []ports.IOParam{{Name: "sensorID", Required: false}}
	if err := ports.ValidateParams(params, map[string]string{}); err != nil {
		t.Errorf("want nil for missing optional param, got %v", err)
	}
}

func TestValidateParams_AllSatisfied(t *testing.T) {
	params := []ports.IOParam{{Name: "sensorID", Required: true}}
	if err := ports.ValidateParams(params, map[string]string{"sensorID": "abc"}); err != nil {
		t.Errorf("want nil, got %v", err)
	}
}

func TestWithParams_ParamsFromContext(t *testing.T) {
	params := []ports.IOParam{{Name: "sensorID", Required: true}}
	ctx := ports.WithParams(context.Background(), params)
	got := ports.ParamsFromContext(ctx)
	if len(got) != 1 || got[0].Name != "sensorID" {
		t.Errorf("want stored params, got %v", got)
	}
}

func TestParamsFromContext_NoneStored(t *testing.T) {
	got := ports.ParamsFromContext(context.Background())
	if got != nil {
		t.Errorf("want nil when nothing stored, got %v", got)
	}
}

// ── Observer integration (RecordRequest("port.bind", ...)) ───────────────────

type bindSpyObserver struct {
	requests []spyBindRequest
}

type spyBindRequest struct {
	method     string
	path       string
	statusCode int
}

func (s *bindSpyObserver) RecordValidationError(_, _, _ string)              {}
func (s *bindSpyObserver) RecordSubscribe(_ string, _ bool, _ time.Duration) {}
func (s *bindSpyObserver) RecordPublish(_ string, _ bool, _ time.Duration)   {}
func (s *bindSpyObserver) RecordRequest(method, path string, statusCode int, _ time.Duration) {
	s.requests = append(s.requests, spyBindRequest{method: method, path: path, statusCode: statusCode})
}

func TestSourcePort_Bind_RecordsObserverEvent(t *testing.T) {
	spy := &bindSpyObserver{}
	ctx := stats.WithObserver(context.Background(), spy)
	p := intPort("obs-source", 4)
	p.Bind(ctx, ports.ChanSourceAdapter(feedChan(1, 2)))
	_, _ = collectStream(ctx, p.Stream(ctx))

	if len(spy.requests) != 1 {
		t.Fatalf("want 1 RecordRequest call, got %d: %v", len(spy.requests), spy.requests)
	}
	if spy.requests[0].method != "port.bind" {
		t.Errorf("want method 'port.bind', got %q", spy.requests[0].method)
	}
	if spy.requests[0].statusCode != 200 {
		t.Errorf("want statusCode 200, got %d", spy.requests[0].statusCode)
	}
}

func TestSinkPort_Bind_RecordsObserverEvent(t *testing.T) {
	spy := &bindSpyObserver{}
	ctx := stats.WithObserver(context.Background(), spy)
	p := intSinkPort("obs-sink", 4)
	out := make(chan int, 4)
	p.Bind(ctx, ports.ChanSinkAdapter(out))
	errCh := make(chan error)
	close(errCh)
	p.Feed(ctx, gstream.Stream[int]{Values: feedChan(1, 2), Errors: errCh})

	if len(spy.requests) != 1 || spy.requests[0].method != "port.bind" {
		t.Fatalf("want 1 'port.bind' RecordRequest call, got %v", spy.requests)
	}
}

func TestIOPort_Bind_RecordsObserverEvent(t *testing.T) {
	spy := &bindSpyObserver{}
	ctx := stats.WithObserver(context.Background(), spy)
	p, err := ports.NewIOPort[int, string]("obs-io", intCodec, strCodec, ports.PortOptions{})
	if err != nil {
		t.Fatalf("construct port: %v", err)
	}
	if err := p.Bind(ctx, ports.FuncIOAdapter(func(_ context.Context, v int) (string, error) {
		return fmt.Sprint(v), nil
	})); err != nil {
		t.Fatalf("Bind: %v", err)
	}

	if len(spy.requests) != 1 || spy.requests[0].method != "port.bind" || spy.requests[0].statusCode != 200 {
		t.Fatalf("want 1 successful 'port.bind' RecordRequest call, got %v", spy.requests)
	}
}

func TestIOPort_Bind_RecordsObserverEvent_OnError(t *testing.T) {
	spy := &bindSpyObserver{}
	ctx := stats.WithObserver(context.Background(), spy)
	p, err := ports.NewIOPort[int, string]("obs-io-err", intCodec, strCodec, ports.PortOptions{})
	if err != nil {
		t.Fatalf("construct port: %v", err)
	}
	fn := ports.FuncIOAdapter(func(_ context.Context, v int) (string, error) { return "", nil })
	_ = p.Bind(ctx, fn)
	_ = p.Bind(ctx, fn) // second Bind fails — adapter already bound

	if len(spy.requests) != 2 {
		t.Fatalf("want 2 RecordRequest calls, got %d", len(spy.requests))
	}
	if spy.requests[1].statusCode != 500 {
		t.Errorf("want statusCode 500 for failed bind, got %d", spy.requests[1].statusCode)
	}
}

func TestToolPort_Bind_RecordsObserverEvent(t *testing.T) {
	spy := &bindSpyObserver{}
	ctx := stats.WithObserver(context.Background(), spy)
	p, err := ports.NewToolPort[int, string]("obs-tool", intCodec, strCodec, ports.PortOptions{})
	if err != nil {
		t.Fatalf("construct port: %v", err)
	}
	p.SetPipeline(func(_ context.Context, v int) gstream.Stream[string] {
		return gstream.Stream[string]{}
	})
	adapter := &mockToolAdapter{}
	if err := p.Bind(ctx, adapter); err != nil {
		t.Fatalf("Bind: %v", err)
	}

	if len(spy.requests) != 1 || spy.requests[0].method != "port.bind" || spy.requests[0].statusCode != 200 {
		t.Fatalf("want 1 successful 'port.bind' RecordRequest call, got %v", spy.requests)
	}
}

func TestToolPort_Bind_RecordsObserverEvent_NoPipeline(t *testing.T) {
	spy := &bindSpyObserver{}
	ctx := stats.WithObserver(context.Background(), spy)
	p, err := ports.NewToolPort[int, string]("obs-tool-nopipeline", intCodec, strCodec, ports.PortOptions{})
	if err != nil {
		t.Fatalf("construct port: %v", err)
	}
	adapter := &mockToolAdapter{}
	if err := p.Bind(ctx, adapter); err == nil {
		t.Fatal("want PortNoPipelineError")
	}

	if len(spy.requests) != 1 || spy.requests[0].statusCode != 500 {
		t.Fatalf("want 1 failed 'port.bind' RecordRequest call, got %v", spy.requests)
	}
}

// ── ToolPort ──────────────────────────────────────────────────────────────────

// mockToolAdapter is a test ToolAdapter that stores the bound fn.
type mockToolAdapter struct {
	name string
	fn   func(context.Context, int) gstream.Stream[string]
	err  error
}

func (a *mockToolAdapter) AdapterName() string { return a.name }
func (a *mockToolAdapter) Bind(_ context.Context, fn func(context.Context, int) gstream.Stream[string]) error {
	if a.err != nil {
		return a.err
	}
	a.fn = fn
	return nil
}

func TestToolPort_HappyPath(t *testing.T) {
	ctx := context.Background()
	p, err := ports.NewToolPort[int, string]("test", intCodec, strCodec, ports.PortOptions{})
	if err != nil {
		t.Fatalf("construct port: %v", err)
	}
	p.SetPipeline(func(_ context.Context, v int) gstream.Stream[string] {
		return gstream.Single(context.Background(), fmt.Sprintf("%d", v))
	})

	adapter := &mockToolAdapter{name: "mock.ToolAdapter"}
	if err := p.Bind(ctx, adapter); err != nil {
		t.Fatalf("Bind: unexpected error: %v", err)
	}
	if adapter.fn == nil {
		t.Fatal("Bind should have set fn on adapter")
	}

	out := adapter.fn(ctx, 42)
	vals, _ := gstream.Collect(ctx, out)
	if len(vals) != 1 || vals[0] != "42" {
		t.Errorf("want [42], got %v", vals)
	}
}

func TestToolPort_NoPipelineError(t *testing.T) {
	ctx := context.Background()
	p, err := ports.NewToolPort[int, string]("test", intCodec, strCodec, ports.PortOptions{})
	if err != nil {
		t.Fatalf("construct port: %v", err)
	}
	// No SetPipeline call

	adapter := &mockToolAdapter{name: "mock.ToolAdapter"}
	err = p.Bind(ctx, adapter)
	if err == nil {
		t.Fatal("want error when pipeline not set, got nil")
	}
	var pbe ports.PortBindError
	if !errors.As(err, &pbe) {
		t.Errorf("want PortBindError, got %T: %v", err, err)
	}
	var npe ports.PortNoPipelineError
	if !errors.As(err, &npe) {
		t.Errorf("want PortNoPipelineError wrapped in PortBindError, got %T", err)
	}
}

func TestToolPort_MultipleBind(t *testing.T) {
	ctx := context.Background()
	p, err := ports.NewToolPort[int, string]("test", intCodec, strCodec, ports.PortOptions{})
	if err != nil {
		t.Fatalf("construct port: %v", err)
	}
	p.SetPipeline(func(_ context.Context, v int) gstream.Stream[string] {
		return gstream.Single(context.Background(), fmt.Sprintf("%d", v))
	})

	a1 := &mockToolAdapter{name: "adapter1"}
	a2 := &mockToolAdapter{name: "adapter2"}
	if err := p.Bind(ctx, a1); err != nil {
		t.Fatalf("first Bind: %v", err)
	}
	if err := p.Bind(ctx, a2); err != nil {
		t.Fatalf("second Bind: %v", err)
	}
	if a1.fn == nil || a2.fn == nil {
		t.Error("both adapters should have fn set")
	}
}

func TestToolPort_AdapterError(t *testing.T) {
	ctx := context.Background()
	p, err := ports.NewToolPort[int, string]("test", intCodec, strCodec, ports.PortOptions{})
	if err != nil {
		t.Fatalf("construct port: %v", err)
	}
	p.SetPipeline(func(_ context.Context, _ int) gstream.Stream[string] {
		return gstream.Single(context.Background(), "")
	})

	adapter := &mockToolAdapter{name: "failing", err: errors.New("route conflict")}
	err = p.Bind(ctx, adapter)
	if err == nil {
		t.Fatal("want error from failing adapter, got nil")
	}
	var pbe ports.PortBindError
	if !errors.As(err, &pbe) {
		t.Errorf("want PortBindError, got %T", err)
	}
}

func TestPortNoPipelineError_LogValue(t *testing.T) {
	e := ports.PortNoPipelineError{Port: "oee-tool"}
	v := e.LogValue()
	if v.Kind() != slog.KindGroup {
		t.Fatalf("want KindGroup, got %v", v.Kind())
	}
	attrs := v.Group()
	if len(attrs) == 0 || attrs[0].Key != "port" {
		t.Errorf("want 'port' attribute, got %v", attrs)
	}
}

// ── MissingPatternError / PatternRegisterError ───────────────────────────────

func TestMissingPatternError_LogValue(t *testing.T) {
	e := ports.MissingPatternError{Port: "sensor-readings", Kind: "rest"}
	v := e.LogValue()
	if v.Kind() != slog.KindGroup {
		t.Fatalf("want KindGroup, got %v", v.Kind())
	}
	attrs := v.Group()
	if len(attrs) != 2 || attrs[0].Key != "port" || attrs[1].Key != "kind" {
		t.Errorf("want port+kind attributes, got %v", attrs)
	}
	if e.Error() == "" {
		t.Error("want non-empty Error() message")
	}
}

func TestPatternRegisterError_LogValue(t *testing.T) {
	inner := errors.New("unknown param name")
	e := ports.PatternRegisterError{Port: "sensor-readings", Kind: "event", Err: inner}
	v := e.LogValue()
	if v.Kind() != slog.KindGroup {
		t.Fatalf("want KindGroup, got %v", v.Kind())
	}
	attrs := v.Group()
	if len(attrs) != 3 {
		t.Errorf("want 3 attributes (port, kind, err), got %v", attrs)
	}
	if !errors.Is(e, inner) {
		t.Error("want errors.Is to reach the wrapped error via Unwrap")
	}
}

// ── Pattern → handle construction (Phase 4) ──────────────────────────────────

func TestRESTPattern_BuildsClientHandle(t *testing.T) {
	p, err := ports.NewIOPort[int, string]("call", intCodec, strCodec, ports.PortOptions{
		Patterns: []ports.Pattern{
			ports.RESTPattern{Method: "POST", Path: "/double", Opts: []rest.RouteOpt{
				rest.RouteMeta{OperationID: "double"},
			}},
		},
	})
	if err != nil {
		t.Fatalf("construct port: %v", err)
	}
	handle, ok := ports.RESTHandle[int, string](p)
	if !ok {
		t.Fatal("want RESTHandle to be present")
	}
	if handle.Descriptor.Path != "/double" || handle.Descriptor.Method != "POST" {
		t.Errorf("want method=POST path=/double, got %+v", handle.Descriptor)
	}
}

func TestEventPattern_BuildsClientHandle(t *testing.T) {
	p, err := ports.NewSourcePort[int]("readings", intCodec, ports.PortOptions{
		Patterns: []ports.Pattern{
			ports.EventPattern{Topic: "sensors/{sensorID}/data", Opts: []events.ChannelOpt{
				events.Subscribe{Summary: "sensor reading"},
			}},
		},
	})
	if err != nil {
		t.Fatalf("construct port: %v", err)
	}
	handle, ok := ports.EventHandle[int](p)
	if !ok {
		t.Fatal("want EventHandle to be present")
	}
	if handle.Topic != "sensors/{sensorID}/data" {
		t.Errorf("want topic %q, got %q", "sensors/{sensorID}/data", handle.Topic)
	}
}

func TestReqReplyPattern_BuildsClientHandle(t *testing.T) {
	p, err := ports.NewIOPort[int, string]("compute", intCodec, strCodec, ports.PortOptions{
		Patterns: []ports.Pattern{
			ports.ReqReplyPattern{Topic: "compute/add"},
		},
	})
	if err != nil {
		t.Fatalf("construct port: %v", err)
	}
	handle, ok := ports.ReqReplyHandle[int, string](p)
	if !ok {
		t.Fatal("want ReqReplyHandle to be present")
	}
	if handle.Topic != "compute/add" {
		t.Errorf("want topic %q, got %q", "compute/add", handle.Topic)
	}
}

func TestMCPPattern_BuildsClientHandle(t *testing.T) {
	p, err := ports.NewToolPort[int, string]("compute-tool", intCodec, strCodec, ports.PortOptions{
		Patterns: []ports.Pattern{
			ports.MCPPattern{Name: "compute", Opts: []apimcp.ToolOpt{
				apimcp.ToolMeta{Description: "computes a thing"},
			}},
		},
	})
	if err != nil {
		t.Fatalf("construct port: %v", err)
	}
	handle, ok := ports.MCPHandle[int, string](p)
	if !ok {
		t.Fatal("want MCPHandle to be present")
	}
	if handle.Name != "compute" {
		t.Errorf("want name %q, got %q", "compute", handle.Name)
	}
}

func TestHandleAccessor_MissingPattern_ReturnsFalse(t *testing.T) {
	p, err := ports.NewToolPort[int, string]("no-patterns", intCodec, strCodec, ports.PortOptions{})
	if err != nil {
		t.Fatalf("construct port: %v", err)
	}
	if _, ok := ports.RESTHandle[int, string](p); ok {
		t.Error("want RESTHandle to be absent")
	}
	if _, ok := ports.ReqReplyHandle[int, string](p); ok {
		t.Error("want ReqReplyHandle to be absent")
	}
	if _, ok := ports.MCPHandle[int, string](p); ok {
		t.Error("want MCPHandle to be absent")
	}
}

func TestHandleAccessor_NonPatternHolder_ReturnsFalse(t *testing.T) {
	if _, ok := ports.RESTHandle[int, string]("not a port"); ok {
		t.Error("want false for a value that does not implement patternHolder")
	}
}

func TestPort_MultiplePatterns_BothHandlesAvailable(t *testing.T) {
	p, err := ports.NewToolPort[int, string]("multi-transport", intCodec, strCodec, ports.PortOptions{
		Patterns: []ports.Pattern{
			ports.RESTPattern{Method: "POST", Path: "/compute"},
			ports.ReqReplyPattern{Topic: "compute/add"},
		},
	})
	if err != nil {
		t.Fatalf("construct port: %v", err)
	}
	if _, ok := ports.RESTHandle[int, string](p); !ok {
		t.Error("want RESTHandle to be present")
	}
	if _, ok := ports.ReqReplyHandle[int, string](p); !ok {
		t.Error("want ReqReplyHandle to be present")
	}
}

func TestMCPPattern_InvalidTool_ReturnsPatternRegisterError(t *testing.T) {
	_, err := ports.NewToolPort[int, string]("bad-mcp-tool", intCodec, strCodec, ports.PortOptions{
		Patterns: []ports.Pattern{
			ports.MCPPattern{Name: ""}, // empty tool name is rejected by apimcp.Tool.ClientHandle
		},
	})
	if err == nil {
		t.Fatal("want PatternRegisterError, got nil")
	}
	var pre ports.PatternRegisterError
	if !errors.As(err, &pre) {
		t.Errorf("want PatternRegisterError, got %T: %v", err, err)
	}
	if pre.Kind != "mcp" {
		t.Errorf("want Kind=mcp, got %q", pre.Kind)
	}
}

// ── RegisterREST / RegisterEvent / RegisterReqReply / RegisterMCP ────────────

func TestRegisterREST_AddsRouteToBuilder(t *testing.T) {
	p, err := ports.NewIOPort[int, string]("call", intCodec, strCodec, ports.PortOptions{
		Patterns: []ports.Pattern{
			ports.RESTPattern{Method: "POST", Path: "/double", Opts: []rest.RouteOpt{
				rest.RouteMeta{OperationID: "double"},
			}},
		},
	})
	if err != nil {
		t.Fatalf("construct port: %v", err)
	}
	b := rest.NewBuilder(rest.Info{Title: "Test", Version: "1.0.0"})
	if err := ports.RegisterREST[int, string](b, p); err != nil {
		t.Fatalf("RegisterREST: %v", err)
	}
	spec, err := b.OpenAPISpec()
	if err != nil {
		t.Fatalf("OpenAPISpec: %v", err)
	}
	out, err := spec.MarshalYAML()
	if err != nil {
		t.Fatalf("MarshalYAML: %v", err)
	}
	if !strings.Contains(string(out), "/double") {
		t.Errorf("want spec to contain /double, got:\n%s", out)
	}
}

func TestRegisterREST_MissingPattern(t *testing.T) {
	p, err := ports.NewIOPort[int, string]("no-rest", intCodec, strCodec, ports.PortOptions{})
	if err != nil {
		t.Fatalf("construct port: %v", err)
	}
	b := rest.NewBuilder(rest.Info{Title: "Test", Version: "1.0.0"})
	err = ports.RegisterREST[int, string](b, p)
	var mpe ports.MissingPatternError
	if !errors.As(err, &mpe) {
		t.Fatalf("want MissingPatternError, got %v", err)
	}
	if mpe.Port != "no-rest" || mpe.Kind != "rest" {
		t.Errorf("want Port=no-rest Kind=rest, got %+v", mpe)
	}
}

func TestRegisterEvent_AddsChannelToBuilder(t *testing.T) {
	p, err := ports.NewSourcePort[int]("readings", intCodec, ports.PortOptions{
		Patterns: []ports.Pattern{
			ports.EventPattern{Topic: "sensors/data", Opts: []events.ChannelOpt{
				events.Subscribe{Summary: "sensor reading"},
			}},
		},
	})
	if err != nil {
		t.Fatalf("construct port: %v", err)
	}
	b := events.NewBuilder(events.Info{Title: "Test", Version: "1.0.0"})
	if err := ports.RegisterEvent[int](b, p); err != nil {
		t.Fatalf("RegisterEvent: %v", err)
	}
	spec, err := b.AsyncAPISpec()
	if err != nil {
		t.Fatalf("AsyncAPISpec: %v", err)
	}
	out, err := spec.MarshalYAML()
	if err != nil {
		t.Fatalf("MarshalYAML: %v", err)
	}
	if !strings.Contains(string(out), "sensors/data") {
		t.Errorf("want spec to contain sensors/data, got:\n%s", out)
	}
}

func TestRegisterReqReply_AddsRouteToBuilder(t *testing.T) {
	p, err := ports.NewToolPort[int, string]("compute", intCodec, strCodec, ports.PortOptions{
		Patterns: []ports.Pattern{
			ports.ReqReplyPattern{Topic: "compute/add"},
		},
	})
	if err != nil {
		t.Fatalf("construct port: %v", err)
	}
	b := reqreply.NewBuilder(reqreply.Info{Title: "Test", Version: "1.0.0"})
	if err := ports.RegisterReqReply[int, string](b, p); err != nil {
		t.Fatalf("RegisterReqReply: %v", err)
	}
}

func TestRegisterMCP_AddsToolToBuilder(t *testing.T) {
	p, err := ports.NewToolPort[int, string]("compute-tool", intCodec, strCodec, ports.PortOptions{
		Patterns: []ports.Pattern{
			ports.MCPPattern{Name: "compute", Opts: []apimcp.ToolOpt{
				apimcp.ToolMeta{Description: "computes a thing"},
			}},
		},
	})
	if err != nil {
		t.Fatalf("construct port: %v", err)
	}
	b := apimcp.NewBuilder(apimcp.Info{Name: "Test", Version: "1.0.0"})
	if err := ports.RegisterMCP[int, string](b, p); err != nil {
		t.Fatalf("RegisterMCP: %v", err)
	}
	spec, err := b.MCPSpec()
	if err != nil {
		t.Fatalf("MCPSpec: %v", err)
	}
	if len(spec.Tools) != 1 || spec.Tools[0].Name != "compute" {
		t.Errorf("want 1 tool named compute, got %+v", spec.Tools)
	}
}

// ── Phase 5: Builder-backed Pattern construction (single construction path) ──

func TestEventPattern_WithBuilder_PopulatesSecuritySchemes(t *testing.T) {
	scheme := events.SecurityScheme{SecurityScheme: route.BearerScheme("JWT")}

	b := events.NewBuilder(events.Info{Title: "Test", Version: "1.0.0"})
	b.AddSecurityScheme("bearerAuth", scheme)
	b.AddGlobalSecurity(route.SecurityRequirement{"bearerAuth": {}})

	p, err := ports.NewSourcePort[int]("secured-readings", intCodec, ports.PortOptions{
		Patterns: []ports.Pattern{
			ports.EventPattern{Topic: "sensors/data"},
		},
		EventBuilder: b,
	})
	if err != nil {
		t.Fatalf("construct port: %v", err)
	}
	handle, ok := ports.EventHandle[int](p)
	if !ok {
		t.Fatal("want EventHandle to be present")
	}
	if len(handle.SecuritySchemes) != 1 {
		t.Errorf("want 1 security scheme propagated from EventBuilder, got %d", len(handle.SecuritySchemes))
	}
	if len(handle.GlobalSecurity) != 1 {
		t.Errorf("want GlobalSecurity propagated from EventBuilder, got %v", handle.GlobalSecurity)
	}
}

func TestEventPattern_NilBuilder_NoSecuritySchemes(t *testing.T) {
	// Regression: without an EventBuilder, the port still constructs successfully
	// (via a private, single-use builder) but carries no security schemes —
	// documents the "supply your own Builder to get security" contract.
	p, err := ports.NewSourcePort[int]("unsecured-readings", intCodec, ports.PortOptions{
		Patterns: []ports.Pattern{ports.EventPattern{Topic: "sensors/data"}},
	})
	if err != nil {
		t.Fatalf("construct port: %v", err)
	}
	handle, ok := ports.EventHandle[int](p)
	if !ok {
		t.Fatal("want EventHandle to be present")
	}
	if len(handle.SecuritySchemes) != 0 || handle.GlobalSecurity != nil {
		t.Errorf("want no security schemes without an EventBuilder, got schemes=%v global=%v",
			handle.SecuritySchemes, handle.GlobalSecurity)
	}
}

func TestRESTPattern_NilBuilder_StillGoesThroughRegister(t *testing.T) {
	// Proves ports always calls Register (never the weaker ClientHandle): an
	// unknown PathParam name (not a {var} placeholder in Path) is only caught
	// by Register, via rest.InvalidPathParamError.
	_, err := ports.NewIOPort[int, string]("bad-path-param", intCodec, strCodec, ports.PortOptions{
		Patterns: []ports.Pattern{
			ports.RESTPattern{
				Method: "GET",
				Path:   "/things",
				Opts:   []rest.RouteOpt{rest.PathParam{Name: "id"}}, // "id" has no {id} placeholder in "/things"
			},
		},
	})
	if err == nil {
		t.Fatal("want PatternRegisterError wrapping InvalidPathParamError, got nil")
	}
	var pre ports.PatternRegisterError
	if !errors.As(err, &pre) {
		t.Fatalf("want PatternRegisterError, got %T: %v", err, err)
	}
	var ipe rest.InvalidPathParamError
	if !errors.As(err, &ipe) {
		t.Errorf("want InvalidPathParamError wrapped inside, got %v", err)
	}
}

func TestRegisterReqReply_SameBuilderAlreadyUsed_ReturnsDuplicateRouteError(t *testing.T) {
	b := reqreply.NewBuilder(reqreply.Info{Title: "Test", Version: "1.0.0"})
	p, err := ports.NewToolPort[int, string]("compute-dup", intCodec, strCodec, ports.PortOptions{
		Patterns:        []ports.Pattern{ports.ReqReplyPattern{Topic: "compute/dup"}},
		ReqReplyBuilder: b,
	})
	if err != nil {
		t.Fatalf("construct port: %v", err)
	}
	// The port already registered "compute/dup" with b at construction time —
	// registering it again with the SAME builder must fail.
	err = ports.RegisterReqReply[int, string](b, p)
	var dre reqreply.DuplicateRouteError
	if !errors.As(err, &dre) {
		t.Fatalf("want DuplicateRouteError, got %v", err)
	}
}

func TestRegisterMCP_SameBuilderAlreadyUsed_ReturnsError(t *testing.T) {
	b := apimcp.NewBuilder(apimcp.Info{Name: "Test", Version: "1.0.0"})
	p, err := ports.NewToolPort[int, string]("compute-dup-tool", intCodec, strCodec, ports.PortOptions{
		Patterns:   []ports.Pattern{ports.MCPPattern{Name: "compute-dup-tool"}},
		MCPBuilder: b,
	})
	if err != nil {
		t.Fatalf("construct port: %v", err)
	}
	// The port already registered "compute-dup-tool" with b at construction time.
	if err := ports.RegisterMCP[int, string](b, p); err == nil {
		t.Fatal("want error registering the same tool name twice with the same builder, got nil")
	}
}

func TestRESTPattern_WithBuilder_UsesSharedBuilderForSpec(t *testing.T) {
	// A RESTPattern built with a shared RESTBuilder accumulates directly into
	// that builder's spec — no separate RegisterREST replay needed.
	b := rest.NewBuilder(rest.Info{Title: "Test", Version: "1.0.0"})
	_, err := ports.NewIOPort[int, string]("shared-builder-route", intCodec, strCodec, ports.PortOptions{
		Patterns: []ports.Pattern{
			ports.RESTPattern{Method: "GET", Path: "/shared", Opts: []rest.RouteOpt{
				rest.RouteMeta{OperationID: "sharedRoute"},
			}},
		},
		RESTBuilder: b,
	})
	if err != nil {
		t.Fatalf("construct port: %v", err)
	}
	spec, err := b.OpenAPISpec()
	if err != nil {
		t.Fatalf("OpenAPISpec: %v", err)
	}
	out, err := spec.MarshalYAML()
	if err != nil {
		t.Fatalf("MarshalYAML: %v", err)
	}
	if !strings.Contains(string(out), "/shared") {
		t.Errorf("want spec to contain /shared (registered at construction time), got:\n%s", out)
	}
}

func TestRESTPattern_WithBuilder_PathConstraintFailure_ReturnsPatternRegisterError(t *testing.T) {
	noDigits := codex.Constraint[string]{
		Name:    "no-digits-in-path",
		Check:   func(v string) bool { return !strings.ContainsAny(v, "0123456789") },
		Message: func(v string) string { return "path must not contain digits: " + v },
	}
	b := rest.NewBuilder(rest.Info{Title: "Test", Version: "1.0.0"}, rest.WithPathConstraints(noDigits))

	_, err := ports.NewIOPort[int, string]("bad-path-shape", intCodec, strCodec, ports.PortOptions{
		Patterns: []ports.Pattern{
			ports.RESTPattern{Method: "GET", Path: "/v2/things"}, // contains "2" -> violates noDigits
		},
		RESTBuilder: b,
	})
	if err == nil {
		t.Fatal("want PatternRegisterError from path constraint failure, got nil")
	}
	var pre ports.PatternRegisterError
	if !errors.As(err, &pre) {
		t.Fatalf("want PatternRegisterError, got %T: %v", err, err)
	}
	var ipe rest.InvalidPathError
	if !errors.As(err, &ipe) {
		t.Errorf("want InvalidPathError wrapped inside, got %v", err)
	}
}

// ── Phase 6: FilePattern / SQLPattern ─────────────────────────────────────────

type cfgItem struct{ V int }

var cfgCodec = codex.Struct[cfgItem](
	codex.RequiredField("v", codex.Int(), func(x cfgItem) int { return x.V }, func(x *cfgItem, v int) { x.V = v }),
)

func TestFilePattern_SinkPort_BuildsFileHandle(t *testing.T) {
	p, err := ports.NewSinkPort[cfgItem]("file-sink", cfgCodec, ports.PortOptions{
		Patterns: []ports.Pattern{
			ports.FilePattern{
				Path: "data/{id}/item.json",
				Opts: []ports.FileOpt{ports.FilePathParam{Name: "id"}},
			},
		},
	})
	if err != nil {
		t.Fatalf("construct port: %v", err)
	}
	f, ok := ports.FileHandle[cfgItem](p)
	if !ok {
		t.Fatal("want FileHandle to be present")
	}
	if f.Template != "data/{id}/item.json" {
		t.Errorf("want template preserved, got %q", f.Template)
	}
	path, err := f.BuildPath(map[string]string{"id": "abc"})
	if err != nil {
		t.Fatalf("BuildPath: %v", err)
	}
	if path != "data/abc/item.json" {
		t.Errorf("want data/abc/item.json, got %q", path)
	}
}

func TestFilePattern_IOPort_UsesRespCodec(t *testing.T) {
	p, err := ports.NewIOPort[int, cfgItem]("file-io", intCodec, cfgCodec, ports.PortOptions{
		Patterns: []ports.Pattern{
			ports.FilePattern{Path: "item.json"},
		},
	})
	if err != nil {
		t.Fatalf("construct port: %v", err)
	}
	// The handle is a ports.File of the RESPONSE type…
	if _, ok := ports.FileHandle[cfgItem](p); !ok {
		t.Error("want FileHandle[cfgItem] (response type) to be present")
	}
	// …not of the request type.
	if _, ok := ports.FileHandle[int](p); ok {
		t.Error("want FileHandle[int] (request type) to be absent")
	}
}

func TestFilePattern_FormatKinds(t *testing.T) {
	kinds := []struct {
		name string
		kind ports.FileFormatKind
		file string
	}{
		{"json", ports.FileFormatJSON, "item.json"},
		{"yaml", ports.FileFormatYAML, "item.yaml"},
		{"toml", ports.FileFormatTOML, "item.toml"},
	}
	for _, k := range kinds {
		t.Run(k.name, func(t *testing.T) {
			dir := t.TempDir()
			p, err := ports.NewSinkPort[cfgItem]("fmt-"+k.name, cfgCodec, ports.PortOptions{
				Patterns: []ports.Pattern{
					ports.FilePattern{Path: dir + "/" + k.file, Format: k.kind},
				},
			})
			if err != nil {
				t.Fatalf("construct port: %v", err)
			}
			f, ok := ports.FileHandle[cfgItem](p)
			if !ok {
				t.Fatal("want FileHandle to be present")
			}
			if err := f.Write(nil, cfgItem{V: 7}, ports.FileOptions{}); err != nil {
				t.Fatalf("write: %v", err)
			}
			got, err := f.Read(nil, ports.FileOptions{})
			if err != nil {
				t.Fatalf("read: %v", err)
			}
			if got.V != 7 {
				t.Errorf("round-trip: want V=7, got %d", got.V)
			}
		})
	}
}

func TestFileHandle_MissingPattern_ReturnsFalse(t *testing.T) {
	p := intSinkPort("no-file-pattern", 0)
	if _, ok := ports.FileHandle[int](p); ok {
		t.Error("want FileHandle to be absent")
	}
	if _, ok := ports.FileHandle[int](struct{}{}); ok {
		t.Error("want FileHandle to be absent for non-port value")
	}
}

func TestSQLPattern_MetaAccessor(t *testing.T) {
	p, err := ports.NewSinkPort[int]("sql-sink", intCodec, ports.PortOptions{
		Patterns: []ports.Pattern{
			ports.SQLPattern{Table: "readings", Op: "insert_reading"},
		},
	})
	if err != nil {
		t.Fatalf("construct port: %v", err)
	}
	m, ok := ports.SQLMeta(p)
	if !ok {
		t.Fatal("want SQLMeta to be present")
	}
	if m.Table != "readings" || m.Op != "insert_reading" {
		t.Errorf("want {readings insert_reading}, got %+v", m)
	}
	if _, ok := ports.SQLMeta(intSinkPort("plain", 0)); ok {
		t.Error("want SQLMeta to be absent on a port without SQLPattern")
	}
}

func TestWithSQLMeta_FromContext(t *testing.T) {
	ctx := ports.WithSQLMeta(context.Background(), ports.SQLPattern{Table: "t", Op: "o"})
	m, ok := ports.SQLMetaFromContext(ctx)
	if !ok || m.Table != "t" || m.Op != "o" {
		t.Errorf("want {t o}, got %+v ok=%v", m, ok)
	}
	if _, ok := ports.SQLMetaFromContext(context.Background()); ok {
		t.Error("want no SQLMeta in a fresh context")
	}
}

func TestSQLMeta_PropagatedOnBindAndConnect(t *testing.T) {
	ctx := context.Background()

	// IOPort: adapter Transform ctx (via Connect) must carry the metadata.
	iop, err := ports.NewIOPort[int, int]("sql-io", intCodec, intCodec, ports.PortOptions{
		Patterns: []ports.Pattern{ports.SQLPattern{Table: "calib", Op: "get"}},
	})
	if err != nil {
		t.Fatalf("construct port: %v", err)
	}
	var got ports.SQLPattern
	var gotOK bool
	if err := iop.Bind(ctx, ports.FuncIOAdapter(func(c context.Context, v int) (int, error) {
		got, gotOK = ports.SQLMetaFromContext(c)
		return v, nil
	})); err != nil {
		t.Fatalf("bind: %v", err)
	}
	vals, errs := collectStream(ctx, iop.Connect(ctx, gstream.From(ctx, feedChan(1))))
	if len(errs) != 0 || len(vals) != 1 {
		t.Fatalf("want 1 value 0 errors, got %d/%d", len(vals), len(errs))
	}
	if !gotOK || got.Table != "calib" || got.Op != "get" {
		t.Errorf("want {calib get} in adapter ctx, got %+v ok=%v", got, gotOK)
	}

	// SinkPort: adapter Activate ctx (via Bind) must carry the metadata.
	sp, err := ports.NewSinkPort[int]("sql-sink2", intCodec, ports.PortOptions{
		Patterns: []ports.Pattern{ports.SQLPattern{Table: "rd", Op: "ins"}},
	})
	if err != nil {
		t.Fatalf("construct port: %v", err)
	}
	metaCh := make(chan ports.SQLPattern, 1)
	sp.Bind(ctx, sinkCtxProbe{metaCh: metaCh})
	sp.Feed(ctx, gstream.From(ctx, feedChan(1)))
	select {
	case m := <-metaCh:
		if m.Table != "rd" || m.Op != "ins" {
			t.Errorf("want {rd ins}, got %+v", m)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for sink adapter ctx probe")
	}
}

type sinkCtxProbe struct{ metaCh chan ports.SQLPattern }

func (s sinkCtxProbe) AdapterName() string { return "test.sinkCtxProbe" }

func (s sinkCtxProbe) Activate(ctx context.Context, src gstream.Stream[int]) {
	m, _ := ports.SQLMetaFromContext(ctx)
	s.metaCh <- m
	for range src.Values {
	}
	for range src.Errors {
	}
}

// ── SinkPort Push lifecycle (G2) ──────────────────────────────────────────────

func TestSinkPortPush_DeliversToAdapters(t *testing.T) {
	ctx := context.Background()
	p := intSinkPort("push-port", 4)
	out := make(chan int, 8)
	p.Bind(ctx, ports.ChanSinkAdapter(out))

	p.Start(ctx)
	for i := 1; i <= 3; i++ {
		if err := p.Push(ctx, i); err != nil {
			t.Fatalf("push %d: %v", i, err)
		}
	}
	if err := p.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	close(out)
	var got []int
	for v := range out {
		got = append(got, v)
	}
	if len(got) != 3 || got[0] != 1 || got[1] != 2 || got[2] != 3 {
		t.Errorf("want [1 2 3] in order, got %v", got)
	}
}

func TestSinkPortPush_BeforeStart_Error(t *testing.T) {
	p := intSinkPort("not-started", 0)
	err := p.Push(context.Background(), 1)
	var nse ports.PortNotStartedError
	if !errors.As(err, &nse) {
		t.Fatalf("want PortNotStartedError, got %v", err)
	}
	if nse.Port != "not-started" || nse.Op != "push" {
		t.Errorf("want {not-started push}, got %+v", nse)
	}
	v := nse.LogValue()
	if v.Kind() != slog.KindGroup {
		t.Fatalf("want KindGroup, got %v", v.Kind())
	}
	keys := map[string]bool{}
	for _, a := range v.Group() {
		keys[a.Key] = true
	}
	if !keys["port"] || !keys["op"] {
		t.Errorf("want port+op keys, got %v", keys)
	}
}

func TestSinkPortPush_AfterClose_Error(t *testing.T) {
	ctx := context.Background()
	p := intSinkPort("closed-port", 4)
	out := make(chan int, 4)
	p.Bind(ctx, ports.ChanSinkAdapter(out))
	p.Start(ctx)
	if err := p.Push(ctx, 1); err != nil {
		t.Fatalf("push: %v", err)
	}
	if err := p.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	// Close waited for the drain: the item must already be in out.
	if len(out) != 1 {
		t.Errorf("want 1 item drained by Close, got %d", len(out))
	}
	var nse ports.PortNotStartedError
	if err := p.Push(ctx, 2); !errors.As(err, &nse) {
		t.Errorf("want PortNotStartedError after Close, got %v", err)
	}
	if err := p.Close(); err != nil {
		t.Errorf("double Close must be a no-op, got %v", err)
	}
}

func TestSinkPort_FeedAndPush_MutuallyExclusive(t *testing.T) {
	ctx := context.Background()
	p := intSinkPort("feed-driven", 4)
	out := make(chan int, 4)
	p.Bind(ctx, ports.ChanSinkAdapter(out))

	src := make(chan int, 1)
	src <- 7
	close(src)
	p.Feed(ctx, gstream.From(ctx, src))

	var nse ports.PortNotStartedError
	if err := p.Push(ctx, 1); !errors.As(err, &nse) {
		t.Errorf("want PortNotStartedError on a Feed-driven port, got %v", err)
	}
	// Start on a Feed-driven port is a no-op; Push still rejected.
	p.Start(ctx)
	if err := p.Push(ctx, 1); !errors.As(err, &nse) {
		t.Errorf("want PortNotStartedError after no-op Start, got %v", err)
	}
}

func TestSinkPortPush_CtxCancelUnblocks(t *testing.T) {
	ctx := context.Background()
	// No adapter bound and zero buffer: the drain goroutine broadcast loop has
	// nowhere to put items... adapters absent means broadcast is instant. To
	// block Push we use a full port-owned channel: buffer 0 and a slow start.
	p := intSinkPort("blocked-port", 0)
	blocker := make(chan int) // unbuffered, never read
	p.Bind(ctx, sinkBlockAdapter{ch: blocker})
	p.Start(ctx)
	// First Push is accepted into the drain pipeline and blocks the drain on
	// the adapter; subsequent Push blocks on the port-owned channel.
	pushCtx, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
	defer cancel()
	var err error
	for i := 0; i < 4; i++ { // fill any internal slack, then block
		if err = p.Push(pushCtx, i); err != nil {
			break
		}
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("want DeadlineExceeded from blocked Push, got %v", err)
	}
}

type sinkBlockAdapter struct{ ch chan int }

func (s sinkBlockAdapter) AdapterName() string { return "test.sinkBlockAdapter" }

func (s sinkBlockAdapter) Activate(ctx context.Context, src gstream.Stream[int]) {
	for v := range src.Values {
		select {
		case s.ch <- v: // never proceeds — blocks the drain
		case <-ctx.Done():
			return
		}
	}
}

func TestSinkPortPush_ConcurrentSafe(t *testing.T) {
	ctx := context.Background()
	p := intSinkPort("concurrent-port", 8)
	out := make(chan int, 200)
	p.Bind(ctx, ports.ChanSinkAdapter(out))
	p.Start(ctx)

	var wg sync.WaitGroup
	for g := 0; g < 10; g++ {
		wg.Add(1)
		go func(base int) {
			defer wg.Done()
			for i := 0; i < 10; i++ {
				if err := p.Push(ctx, base*100+i); err != nil {
					t.Errorf("push: %v", err)
				}
			}
		}(g)
	}
	wg.Wait()
	if err := p.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	close(out)
	n := 0
	for range out {
		n++
	}
	if n != 100 {
		t.Errorf("want 100 items, got %d", n)
	}
}

func TestSinkPortPush_FanOut(t *testing.T) {
	ctx := context.Background()
	p := intSinkPort("fanout-port", 4)
	out1 := make(chan int, 8)
	out2 := make(chan int, 8)
	p.Bind(ctx, ports.ChanSinkAdapter(out1))
	p.Bind(ctx, ports.ChanSinkAdapter(out2))
	p.Start(ctx)
	for i := 1; i <= 3; i++ {
		if err := p.Push(ctx, i); err != nil {
			t.Fatalf("push: %v", err)
		}
	}
	if err := p.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if len(out1) != 3 || len(out2) != 3 {
		t.Errorf("want 3 items in BOTH adapters, got %d/%d", len(out1), len(out2))
	}
}

// ── LatestPort (G1) ───────────────────────────────────────────────────────────

// funcLatestAdapter captures the latest func for direct test inspection.
type funcLatestAdapter[T any] struct {
	got   chan func() (T, bool)
	block bool
}

func (a *funcLatestAdapter[T]) AdapterName() string { return "test.funcLatestAdapter" }

func (a *funcLatestAdapter[T]) Serve(ctx context.Context, latest func() (T, bool)) error {
	a.got <- latest
	if a.block {
		<-ctx.Done() // zeromq-style blocking Serve
	}
	return nil
}

func TestLatestPort_ServesLatestValue(t *testing.T) {
	ctx := context.Background()
	p, err := ports.NewLatestPort[int]("latest", intCodec, ports.PortOptions{})
	if err != nil {
		t.Fatalf("construct: %v", err)
	}
	ad := &funcLatestAdapter[int]{got: make(chan func() (int, bool), 1)}
	if err := p.Bind(ctx, ad); err != nil {
		t.Fatalf("bind: %v", err)
	}
	latest := <-ad.got

	src := make(chan int, 2)
	src <- 1
	src <- 2
	close(src)
	p.Feed(ctx, gstream.From(ctx, src))

	if v, ok := p.Latest(); !ok || v != 2 {
		t.Errorf("port.Latest: want (2,true), got (%d,%v)", v, ok)
	}
	if v, ok := latest(); !ok || v != 2 {
		t.Errorf("adapter latest: want (2,true), got (%d,%v)", v, ok)
	}
}

func TestLatestPort_EmptyBeforeFirstValue(t *testing.T) {
	p, err := ports.NewLatestPort[int]("empty", intCodec, ports.PortOptions{})
	if err != nil {
		t.Fatalf("construct: %v", err)
	}
	if v, ok := p.Latest(); ok || v != 0 {
		t.Errorf("want (0,false) before first value, got (%d,%v)", v, ok)
	}
}

func TestLatestPort_SurvivesStreamTermination(t *testing.T) {
	ctx := context.Background()
	p, err := ports.NewLatestPort[int]("survive", intCodec, ports.PortOptions{})
	if err != nil {
		t.Fatalf("construct: %v", err)
	}
	src := make(chan int, 1)
	src <- 42
	close(src)
	p.Feed(ctx, gstream.From(ctx, src)) // returns when src terminates
	// The cache outlives the stream.
	if v, ok := p.Latest(); !ok || v != 42 {
		t.Errorf("want (42,true) after src end, got (%d,%v)", v, ok)
	}
}

func TestLatestPort_RESTPattern_InSpec(t *testing.T) {
	b := rest.NewBuilder(rest.Info{Title: "t", Version: "1"})
	p, err := ports.NewLatestPort[int]("latest-rest", intCodec, ports.PortOptions{
		Patterns: []ports.Pattern{
			ports.RESTPattern{Method: "GET", Path: "/latest", Opts: []rest.RouteOpt{
				rest.RouteMeta{OperationID: "getLatest"},
			}},
		},
		RESTBuilder: b,
	})
	if err != nil {
		t.Fatalf("construct: %v", err)
	}
	handle, ok := ports.RESTHandle[struct{}, int](p)
	if !ok {
		t.Fatal("want RESTHandle[struct{}, int] present")
	}
	if handle.Descriptor.Method != "GET" || handle.Descriptor.Path != "/latest" {
		t.Errorf("want GET /latest, got %+v", handle.Descriptor)
	}
	doc, err := b.OpenAPISpec()
	if err != nil {
		t.Fatalf("spec: %v", err)
	}
	raw, err := doc.MarshalJSON()
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(raw), "/latest") || !strings.Contains(string(raw), "getLatest") {
		t.Error("want /latest + getLatest in OpenAPI spec")
	}
}

func TestLatestPort_FanOut(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	p, err := ports.NewLatestPort[int]("fanout-latest", intCodec, ports.PortOptions{})
	if err != nil {
		t.Fatalf("construct: %v", err)
	}
	a1 := &funcLatestAdapter[int]{got: make(chan func() (int, bool), 1)}
	a2 := &funcLatestAdapter[int]{got: make(chan func() (int, bool), 1), block: true} // blocking Serve shape
	if err := p.Bind(ctx, a1); err != nil {
		t.Fatalf("bind a1: %v", err)
	}
	if err := p.Bind(ctx, a2); err != nil {
		t.Fatalf("bind a2: %v", err)
	}
	l1 := <-a1.got
	l2 := <-a2.got

	src := make(chan int, 1)
	src <- 9
	close(src)
	p.Feed(ctx, gstream.From(ctx, src))

	if v, ok := l1(); !ok || v != 9 {
		t.Errorf("adapter1: want (9,true), got (%d,%v)", v, ok)
	}
	if v, ok := l2(); !ok || v != 9 {
		t.Errorf("adapter2 (blocking Serve): want (9,true), got (%d,%v)", v, ok)
	}
}

// ── Phase C: RESTPattern on SourcePort (ingest) and SinkPort (SSE) ───────────

func TestRESTPattern_SourcePort_BuildsIngestHandle(t *testing.T) {
	p, err := ports.NewSourcePort[int]("ingest", intCodec, ports.PortOptions{
		Patterns: []ports.Pattern{
			ports.RESTPattern{Method: "POST", Path: "/readings", Opts: []rest.RouteOpt{
				rest.RouteMeta{OperationID: "ingestReading"},
			}},
		},
	})
	if err != nil {
		t.Fatalf("construct: %v", err)
	}
	handle, ok := ports.RESTHandle[int, struct{}](p)
	if !ok {
		t.Fatal("want ingest RESTHandle[int, struct{}] present")
	}
	if handle.Descriptor.Method != "POST" || handle.Descriptor.Path != "/readings" {
		t.Errorf("want POST /readings, got %+v", handle.Descriptor)
	}
}

func TestRESTPattern_SinkPort_BuildsSSEHandle(t *testing.T) {
	p, err := ports.NewSinkPort[int]("sse", intCodec, ports.PortOptions{
		Patterns: []ports.Pattern{
			ports.RESTPattern{Path: "/events", Opts: []rest.RouteOpt{
				rest.RouteMeta{OperationID: "streamEvents"},
			}}, // Method empty — SSE is always GET
		},
	})
	if err != nil {
		t.Fatalf("construct: %v", err)
	}
	handle, ok := ports.SSEHandle[int](p)
	if !ok {
		t.Fatal("want SSEHandle[int] present")
	}
	if handle.Descriptor.Method != "GET" || handle.Descriptor.Path != "/events" {
		t.Errorf("want GET /events, got %+v", handle.Descriptor)
	}
}

func TestRESTPattern_SinkPort_MethodValidation(t *testing.T) {
	_, err := ports.NewSinkPort[int]("sse-bad", intCodec, ports.PortOptions{
		Patterns: []ports.Pattern{
			ports.RESTPattern{Method: "POST", Path: "/events"},
		},
	})
	var pre ports.PatternRegisterError
	if !errors.As(err, &pre) {
		t.Fatalf("want PatternRegisterError for POST SSE, got %v", err)
	}
	if pre.Kind != "rest" {
		t.Errorf("want kind rest, got %q", pre.Kind)
	}
	// Explicit GET is accepted.
	if _, err := ports.NewSinkPort[int]("sse-get", intCodec, ports.PortOptions{
		Patterns: []ports.Pattern{ports.RESTPattern{Method: "GET", Path: "/events"}},
	}); err != nil {
		t.Errorf("explicit GET must be accepted, got %v", err)
	}
}

func TestRESTPattern_InSharedSpec_IngestAndSSE(t *testing.T) {
	b := rest.NewBuilder(rest.Info{Title: "t", Version: "1"})
	_, err := ports.NewSourcePort[int]("ingest-spec", intCodec, ports.PortOptions{
		Patterns: []ports.Pattern{
			ports.RESTPattern{Method: "POST", Path: "/in", Opts: []rest.RouteOpt{rest.RouteMeta{OperationID: "opIngest"}}},
		},
		RESTBuilder: b,
	})
	if err != nil {
		t.Fatalf("source: %v", err)
	}
	_, err = ports.NewSinkPort[int]("sse-spec", intCodec, ports.PortOptions{
		Patterns: []ports.Pattern{
			ports.RESTPattern{Path: "/out", Opts: []rest.RouteOpt{rest.RouteMeta{OperationID: "opSSE"}}},
		},
		RESTBuilder: b,
	})
	if err != nil {
		t.Fatalf("sink: %v", err)
	}
	doc, err := b.OpenAPISpec()
	if err != nil {
		t.Fatalf("spec: %v", err)
	}
	raw, _ := doc.MarshalJSON()
	for _, want := range []string{"/in", "opIngest", "/out", "opSSE", "text/event-stream"} {
		if !strings.Contains(string(raw), want) {
			t.Errorf("want %q in shared OpenAPI spec", want)
		}
	}
}

func TestSSEHandle_MissingPattern_ReturnsFalse(t *testing.T) {
	if _, ok := ports.SSEHandle[int](intSinkPort("no-pattern", 0)); ok {
		t.Error("want SSEHandle absent on a pattern-less port")
	}
	if _, ok := ports.SSEHandle[int](struct{}{}); ok {
		t.Error("want SSEHandle absent for non-port value")
	}
	// A SourcePort's RESTPattern builds an ingest handle, not an SSE handle.
	src, err := ports.NewSourcePort[int]("ingest-not-sse", intCodec, ports.PortOptions{
		Patterns: []ports.Pattern{ports.RESTPattern{Method: "POST", Path: "/in"}},
	})
	if err != nil {
		t.Fatalf("construct: %v", err)
	}
	if _, ok := ports.SSEHandle[int](src); ok {
		t.Error("want SSEHandle absent on a SourcePort (ingest shape)")
	}
}

func TestRegisterSSE_ReplaysSpec(t *testing.T) {
	p, err := ports.NewSinkPort[int]("sse-replay", intCodec, ports.PortOptions{
		Patterns: []ports.Pattern{
			ports.RESTPattern{Path: "/replay", Opts: []rest.RouteOpt{rest.RouteMeta{OperationID: "opReplay"}}},
		},
	})
	if err != nil {
		t.Fatalf("construct: %v", err)
	}
	b := rest.NewBuilder(rest.Info{Title: "t", Version: "1"})
	if err := ports.RegisterSSE[int](b, p); err != nil {
		t.Fatalf("RegisterSSE: %v", err)
	}
	doc, err := b.OpenAPISpec()
	if err != nil {
		t.Fatalf("spec: %v", err)
	}
	raw, _ := doc.MarshalJSON()
	if !strings.Contains(string(raw), "/replay") {
		t.Error("want replayed SSE route in spec")
	}

	var mpe ports.MissingPatternError
	if err := ports.RegisterSSE[int](b, intSinkPort("plain", 0)); !errors.As(err, &mpe) {
		t.Errorf("want MissingPatternError, got %v", err)
	}
}

// ── SocketPattern + DuplexPort ────────────────────────────────────────────────

func TestSocketPattern_PortAcceptance(t *testing.T) {
	pat := ports.SocketPattern{Path: "/live/{room}"}

	// SourcePort: accepted — Socket[T, struct{}].
	src, err := ports.NewSourcePort[int]("ws-src", intCodec, ports.PortOptions{
		Patterns: []ports.Pattern{pat},
	})
	if err != nil {
		t.Fatalf("SourcePort: %v", err)
	}
	if _, ok := ports.SocketHandle[int, struct{}](src); !ok {
		t.Error("SourcePort: want socket handle")
	}

	// SinkPort: accepted — Socket[struct{}, T].
	sink, err := ports.NewSinkPort[int]("ws-sink", intCodec, ports.PortOptions{
		Patterns: []ports.Pattern{pat},
	})
	if err != nil {
		t.Fatalf("SinkPort: %v", err)
	}
	if _, ok := ports.SocketHandle[struct{}, int](sink); !ok {
		t.Error("SinkPort: want socket handle")
	}

	// DuplexPort: accepted — Socket[In, Out].
	dup, err := ports.NewDuplexPort[int, string]("ws-dup", intCodec, strCodec, ports.PortOptions{
		Patterns: []ports.Pattern{pat},
	})
	if err != nil {
		t.Fatalf("DuplexPort: %v", err)
	}
	h, ok := ports.SocketHandle[int, string](dup)
	if !ok {
		t.Fatal("DuplexPort: want socket handle")
	}
	if h.Path != "/live/{room}" || h.Route == nil {
		t.Errorf("handle incomplete: %+v", h)
	}

	// IOPort / LatestPort / ToolPort: rejected.
	var pre ports.PatternRegisterError
	if _, err := ports.NewIOPort[int, string]("ws-io", intCodec, strCodec,
		ports.PortOptions{Patterns: []ports.Pattern{pat}}); !errors.As(err, &pre) || pre.Kind != "socket" {
		t.Errorf("IOPort: want PatternRegisterError{socket}, got %v", err)
	}
	if _, err := ports.NewLatestPort[int]("ws-latest", intCodec,
		ports.PortOptions{Patterns: []ports.Pattern{pat}}); !errors.As(err, &pre) || pre.Kind != "socket" {
		t.Errorf("LatestPort: want PatternRegisterError{socket}, got %v", err)
	}
	if _, err := ports.NewToolPort[int, string]("ws-tool", intCodec, strCodec,
		ports.PortOptions{Patterns: []ports.Pattern{pat}}); !errors.As(err, &pre) || pre.Kind != "socket" {
		t.Errorf("ToolPort: want PatternRegisterError{socket}, got %v", err)
	}

	// DuplexPort with a non-socket pattern: rejected.
	if _, err := ports.NewDuplexPort[int, string]("ws-dup2", intCodec, strCodec,
		ports.PortOptions{Patterns: []ports.Pattern{ports.SQLPattern{Table: "t"}}}); !errors.As(err, &pre) {
		t.Errorf("DuplexPort+SQLPattern: want PatternRegisterError, got %v", err)
	}
}

type fakeDuplexAdapter struct {
	sent []ports.Framed[string] // outbound frames the adapter delivered
	mu   sync.Mutex
	done chan struct{}
}

func (f *fakeDuplexAdapter) AdapterName() string { return "test.DuplexAdapter" }

func (f *fakeDuplexAdapter) Activate(ctx context.Context, dst chan<- ports.Framed[int], errs chan<- error, src gstream.Stream[ports.Framed[string]]) error {
	defer close(f.done)
	// Emit two inbound frames from two sessions.
	dst <- ports.Framed[int]{Session: "s1", Payload: 1}
	dst <- ports.Framed[int]{Session: "s2", Payload: 2}
	// Drain outbound.
	valCh, errCh := src.Values, src.Errors
	for valCh != nil || errCh != nil {
		select {
		case <-ctx.Done():
			return nil
		case v, ok := <-valCh:
			if !ok {
				valCh = nil
				continue
			}
			f.mu.Lock()
			f.sent = append(f.sent, v)
			f.mu.Unlock()
		case _, ok := <-errCh:
			if !ok {
				errCh = nil
			}
		}
	}
	return nil
}

func TestDuplexPort_Lifecycle(t *testing.T) {
	ctx := context.Background()
	port, err := ports.NewDuplexPort[int, string]("dup", intCodec, strCodec, ports.PortOptions{Buffer: 4})
	if err != nil {
		t.Fatalf("construct: %v", err)
	}

	fake := &fakeDuplexAdapter{done: make(chan struct{})}
	if err := port.Bind(ctx, fake); err != nil {
		t.Fatalf("bind: %v", err)
	}

	// Second bind rejected.
	var pbe ports.PortBindError
	if err := port.Bind(ctx, &fakeDuplexAdapter{done: make(chan struct{})}); !errors.As(err, &pbe) {
		t.Errorf("second Bind: want PortBindError, got %v", err)
	}

	// Feed two outbound frames (one targeted, one broadcast) and close.
	outVals := make(chan ports.Framed[string], 2)
	outVals <- ports.Framed[string]{Session: "s1", Payload: "reply"}
	outVals <- ports.Framed[string]{Payload: "broadcast"}
	close(outVals)
	outErrs := make(chan error)
	close(outErrs)
	port.Feed(ctx, gstream.Stream[ports.Framed[string]]{Values: outVals, Errors: outErrs})

	<-fake.done // adapter drained outbound and returned

	inbound := port.Inbound(ctx)
	vals, errs := gstream.Collect(ctx, inbound)
	if len(errs) != 0 || len(vals) != 2 {
		t.Fatalf("inbound: want 2 frames, got %v / %v", vals, errs)
	}
	if vals[0].Session != "s1" || vals[1].Session != "s2" {
		t.Errorf("session tags wrong: %v", vals)
	}

	fake.mu.Lock()
	defer fake.mu.Unlock()
	if len(fake.sent) != 2 || fake.sent[0].Session != "s1" || fake.sent[1].Session != "" {
		t.Errorf("outbound delivery wrong: %+v", fake.sent)
	}
}

// ── RegisterSocket (AsyncAPI ws spec) ─────────────────────────────────────────

func TestRegisterSocket_DuplexBothOperations(t *testing.T) {
	port, err := ports.NewDuplexPort[int, string]("spec-dup", intCodec, strCodec,
		ports.PortOptions{Patterns: []ports.Pattern{ports.SocketPattern{Path: "/live/{room}"}}})
	if err != nil {
		t.Fatalf("port: %v", err)
	}
	b := events.NewBuilder(events.Info{Title: "t", Version: "1"})
	b.AddServer("prod", events.Server{URL: "example.com", Protocol: "ws"})
	if err := ports.RegisterSocket[int, string](b, port); err != nil {
		t.Fatalf("RegisterSocket: %v", err)
	}
	doc, err := b.AsyncAPISpec()
	if err != nil {
		t.Fatalf("spec: %v", err)
	}
	raw, _ := doc.MarshalJSON()
	s := string(raw)
	for _, want := range []string{"/live/{room}", "room", "Inbound socket frames", "Outbound socket frames", `"ws"`} {
		if !strings.Contains(s, want) {
			t.Errorf("spec missing %q", want)
		}
	}
}

func TestRegisterSocket_OneDirectional(t *testing.T) {
	src, _ := ports.NewSourcePort[int]("spec-src", intCodec,
		ports.PortOptions{Patterns: []ports.Pattern{ports.SocketPattern{Path: "/in"}}})
	b := events.NewBuilder(events.Info{Title: "t", Version: "1"})
	if err := ports.RegisterSocket[int, struct{}](b, src); err != nil {
		t.Fatalf("RegisterSocket source: %v", err)
	}
	doc, err := b.AsyncAPISpec()
	if err != nil {
		t.Fatalf("spec: %v", err)
	}
	raw, _ := doc.MarshalJSON()
	s := string(raw)
	if !strings.Contains(s, "Inbound socket frames") {
		t.Error("source port: want inbound op")
	}
	if strings.Contains(s, "Outbound socket frames") {
		t.Error("source port: must NOT emit outbound op (struct{} side)")
	}
}

func TestRegisterSocket_MissingPattern(t *testing.T) {
	port, _ := ports.NewDuplexPort[int, string]("spec-none", intCodec, strCodec, ports.PortOptions{})
	b := events.NewBuilder(events.Info{})
	var mpe ports.MissingPatternError
	if err := ports.RegisterSocket[int, string](b, port); !errors.As(err, &mpe) || mpe.Kind != "socket" {
		t.Errorf("want MissingPatternError{socket}, got %v", err)
	}
}

// ── CustomFormat escape hatch ──────────────────────────────────────────────────

// CF1: FilePattern + CustomFormat(Gob) on an IOPort round-trips via the
// response codec.
func TestFilePattern_CustomFormat_Gob(t *testing.T) {
	dir := t.TempDir()
	p, err := ports.NewIOPort[int, cfgItem]("file-gob", intCodec, cfgCodec, ports.PortOptions{
		Patterns: []ports.Pattern{
			ports.FilePattern{Path: dir + "/item.gob", CustomFormat: format.Gob(cfgCodec)},
		},
	})
	if err != nil {
		t.Fatalf("construct port: %v", err)
	}
	f, ok := ports.FileHandle[cfgItem](p)
	if !ok {
		t.Fatal("want FileHandle to be present")
	}
	if err := f.Write(nil, cfgItem{V: 42}, ports.FileOptions{}); err != nil {
		t.Fatalf("write: %v", err)
	}
	got, err := f.Read(nil, ports.FileOptions{})
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if got.V != 42 {
		t.Errorf("want V=42, got %d", got.V)
	}
}

// CF2: FilePattern + CustomFormat(Binary) on a SinkPort passes raw bytes
// through unchanged and enforces the PNG constraint.
func TestFilePattern_CustomFormat_BinaryPNG(t *testing.T) {
	dir := t.TempDir()
	pngCodec := codex.Bytes().Refine(validate.PNG)
	p, err := ports.NewSinkPort[[]byte]("file-png", pngCodec, ports.PortOptions{
		Patterns: []ports.Pattern{
			ports.FilePattern{Path: dir + "/img.png",
				CustomFormat: format.Binary(pngCodec).WithContentType("image/png")},
		},
	})
	if err != nil {
		t.Fatalf("construct port: %v", err)
	}
	f, ok := ports.FileHandle[[]byte](p)
	if !ok {
		t.Fatal("want FileHandle to be present")
	}
	pngSig := []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A, 0x00}
	if err := f.Write(nil, pngSig, ports.FileOptions{}); err != nil {
		t.Fatalf("write: %v", err)
	}
	got, err := f.Read(nil, ports.FileOptions{})
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(got) != string(pngSig) {
		t.Errorf("bytes not preserved: %v", got)
	}
	// Non-PNG bytes rejected by the constraint on write.
	if err := f.Write(nil, []byte("not a png"), ports.FileOptions{}); err == nil {
		t.Error("want PNG constraint to reject non-PNG bytes")
	}
}

// CF3: CachePattern + CustomFormat(Gob) round-trips through the cache handle.
func TestCachePattern_CustomFormat_Gob(t *testing.T) {
	p, err := ports.NewIOPort[int, cfgItem]("cache-gob", intCodec, cfgCodec, ports.PortOptions{
		Patterns: []ports.Pattern{
			ports.CachePattern{Key: "item:{id}", CustomFormat: format.Gob(cfgCodec)},
		},
	})
	if err != nil {
		t.Fatalf("construct port: %v", err)
	}
	c, ok := ports.CacheHandle[cfgItem](p)
	if !ok {
		t.Fatal("want CacheHandle to be present")
	}
	data, err := c.Format.Marshal(cfgItem{V: 9})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	got, err := c.Format.Unmarshal(data)
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.V != 9 {
		t.Errorf("want V=9, got %d", got.V)
	}
}

// ── CachePattern key codecs (CacheKeyParam) ────────────────────────────────────

// CK1: CacheKeyParam.WithCodec returns an updated value; value semantics.
func TestCacheKeyParam_WithCodec(t *testing.T) {
	base := ports.CacheKeyParam{Name: "id"}
	withCodec := base.WithCodec(codex.String())
	if base.Codec != nil {
		t.Error("want original CacheKeyParam unmodified")
	}
	if withCodec.Codec == nil {
		t.Error("want returned CacheKeyParam to have Codec set")
	}
	if withCodec.Name != "id" {
		t.Errorf("want Name preserved, got %q", withCodec.Name)
	}
}

// CK2: no CacheKeyParam declared — BuildKey is byte-identical to pre-feature
// behavior (regression guard).
func TestCache_BuildKey_NoParams_Unchanged(t *testing.T) {
	c := ports.NewCache("user:{id}", format.JSON(cfgCodec))
	got, err := c.BuildKey(map[string]string{"id": "anything-goes"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "user:anything-goes" {
		t.Errorf("want %q, got %q", "user:anything-goes", got)
	}
}

// CK3: a declared codec accepts a valid value and substitutes it.
func TestCache_BuildKey_ValidatesDeclaredCodec_HappyPath(t *testing.T) {
	c := ports.NewCache("user:{id}", format.JSON(cfgCodec),
		ports.CacheKeyParam{Name: "id"}.WithCodec(codex.String().Refine(validate.UUID)))
	got, err := c.BuildKey(map[string]string{"id": "f47ac10b-58cc-4372-a567-0e02b2c3d479"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := "user:f47ac10b-58cc-4372-a567-0e02b2c3d479"
	if got != want {
		t.Errorf("want %q, got %q", want, got)
	}
}

// CK4: a declared codec rejects an invalid value with CacheKeyParamError.
func TestCache_BuildKey_CodecRejectsValue(t *testing.T) {
	c := ports.NewCache("user:{id}", format.JSON(cfgCodec),
		ports.CacheKeyParam{Name: "id"}.WithCodec(codex.String().Refine(validate.UUID)))
	_, err := c.BuildKey(map[string]string{"id": "not-a-uuid"})
	if err == nil {
		t.Fatal("want error for invalid UUID")
	}
	var paramErr ports.CacheKeyParamError
	if !errors.As(err, &paramErr) {
		t.Fatalf("want CacheKeyParamError, got %T: %v", err, err)
	}
	if paramErr.Key != "user:{id}" || paramErr.Var != "id" || paramErr.Value != "not-a-uuid" {
		t.Errorf("unexpected fields: %+v", paramErr)
	}
	if paramErr.Err == nil {
		t.Error("want wrapped cause")
	}
	v := paramErr.LogValue()
	if v.Kind() != slog.KindGroup {
		t.Fatalf("want slog.KindGroup, got %v", v.Kind())
	}
	attrs := v.Group()
	keys := make(map[string]bool, len(attrs))
	for _, a := range attrs {
		keys[a.Key] = true
	}
	for _, want := range []string{"key", "var", "value", "cause"} {
		if !keys[want] {
			t.Errorf("want LogValue key %q present, got %v", want, keys)
		}
	}
}

// CK5: a declared CacheKeyParam for a var absent from vars still returns
// CacheKeyError (not CacheKeyParamError) — can't codec-validate an absent value.
func TestCache_BuildKey_MissingVar_StillCacheKeyError(t *testing.T) {
	c := ports.NewCache("user:{id}", format.JSON(cfgCodec),
		ports.CacheKeyParam{Name: "id"}.WithCodec(codex.String().Refine(validate.UUID)))
	_, err := c.BuildKey(map[string]string{})
	var keyErr ports.CacheKeyError
	if !errors.As(err, &keyErr) {
		t.Fatalf("want CacheKeyError, got %T: %v", err, err)
	}
	var paramErr ports.CacheKeyParamError
	if errors.As(err, &paramErr) {
		t.Error("want CacheKeyError, not CacheKeyParamError, for a missing var")
	}
}

// CK6: ValidateKeyVars mirrors BuildKey's validation without building the key.
func TestCache_ValidateKeyVars_HappyAndError(t *testing.T) {
	c := ports.NewCache("user:{id}", format.JSON(cfgCodec),
		ports.CacheKeyParam{Name: "id"}.WithCodec(codex.String().Refine(validate.UUID)))

	if err := c.ValidateKeyVars(map[string]string{"id": "f47ac10b-58cc-4372-a567-0e02b2c3d479"}); err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	err := c.ValidateKeyVars(map[string]string{"id": "not-a-uuid"})
	var paramErr ports.CacheKeyParamError
	if !errors.As(err, &paramErr) {
		t.Fatalf("want CacheKeyParamError, got %T: %v", err, err)
	}

	err = c.ValidateKeyVars(map[string]string{})
	var keyErr ports.CacheKeyError
	if !errors.As(err, &keyErr) {
		t.Fatalf("want CacheKeyError for missing var, got %T: %v", err, err)
	}
}

// CK7: KeySchemas omits params without a codec; empty map when none declared.
func TestCache_KeySchemas_OmitsParamsWithoutCodec(t *testing.T) {
	c := ports.NewCache("thing:{id}:{tag}", format.JSON(cfgCodec),
		ports.CacheKeyParam{Name: "id"}.WithCodec(codex.String().Refine(validate.UUID)),
		ports.CacheKeyParam{Name: "tag"}, // no codec
	)
	schemas := c.KeySchemas()
	if _, ok := schemas["id"]; !ok {
		t.Error("want schema for \"id\" (has codec)")
	}
	if _, ok := schemas["tag"]; ok {
		t.Error("want no schema for \"tag\" (no codec)")
	}

	noParams := ports.NewCache("static", format.JSON(cfgCodec))
	if len(noParams.KeySchemas()) != 0 {
		t.Error("want empty map when no params declared")
	}
}

// CK8: CachePattern.Opts wired through an IOPort — end-to-end validation.
func TestCachePattern_Opts_WiredThroughIOPort(t *testing.T) {
	p, err := ports.NewIOPort[int, cfgItem]("cache-keyparam-io", intCodec, cfgCodec, ports.PortOptions{
		Patterns: []ports.Pattern{
			ports.CachePattern{
				Key: "item:{id}",
				Opts: []ports.CacheOpt{
					ports.CacheKeyParam{Name: "id"}.WithCodec(codex.String().Refine(validate.UUID)),
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("construct port: %v", err)
	}
	c, ok := ports.CacheHandle[cfgItem](p)
	if !ok {
		t.Fatal("want CacheHandle to be present")
	}
	if _, err := c.BuildKey(map[string]string{"id": "not-a-uuid"}); err == nil {
		t.Error("want invalid UUID rejected end-to-end")
	}
	if _, err := c.BuildKey(map[string]string{"id": "f47ac10b-58cc-4372-a567-0e02b2c3d479"}); err != nil {
		t.Errorf("want valid UUID accepted end-to-end, got %v", err)
	}
}

// CK9: CachePattern.Opts wired through a SinkPort.
func TestCachePattern_Opts_WiredThroughSinkPort(t *testing.T) {
	p, err := ports.NewSinkPort[cfgItem]("cache-keyparam-sink", cfgCodec, ports.PortOptions{
		Patterns: []ports.Pattern{
			ports.CachePattern{
				Key: "item:{id}",
				Opts: []ports.CacheOpt{
					ports.CacheKeyParam{Name: "id"}.WithCodec(codex.String().Refine(validate.UUID)),
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("construct port: %v", err)
	}
	c, ok := ports.CacheHandle[cfgItem](p)
	if !ok {
		t.Fatal("want CacheHandle to be present")
	}
	if _, err := c.BuildKey(map[string]string{"id": "not-a-uuid"}); err == nil {
		t.Error("want invalid UUID rejected end-to-end")
	}
}

// CK10: CachePattern.Opts wired through a LatestPort — a var-free key with
// Opts declared is a no-op, not an error.
func TestCachePattern_Opts_WiredThroughLatestPort(t *testing.T) {
	p, err := ports.NewLatestPort[cfgItem]("cache-keyparam-latest", cfgCodec, ports.PortOptions{
		Patterns: []ports.Pattern{
			ports.CachePattern{
				Key: "current", // var-free
				Opts: []ports.CacheOpt{
					ports.CacheKeyParam{Name: "id"}.WithCodec(codex.String().Refine(validate.UUID)),
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("construct port: %v", err)
	}
	c, ok := ports.CacheHandle[cfgItem](p)
	if !ok {
		t.Fatal("want CacheHandle to be present")
	}
	got, err := c.BuildKey(nil)
	if err != nil {
		t.Fatalf("want var-free key with unused CacheKeyParam to be a no-op, got %v", err)
	}
	if got != "current" {
		t.Errorf("want %q, got %q", "current", got)
	}
}

// CK11: NewCache builds a standalone Cache[T] usable with a cache adapter
// with no port/pipeline involved — same codec validation either way.
func TestNewCache_Standalone(t *testing.T) {
	c := ports.NewCache("user:{id}", format.JSON(cfgCodec),
		ports.CacheKeyParam{Name: "id"}.WithCodec(codex.String().Refine(validate.UUID)))
	c.TTL = 15 * time.Minute

	if c.TTL != 15*time.Minute {
		t.Errorf("want TTL settable via field assignment, got %v", c.TTL)
	}
	if _, err := c.BuildKey(map[string]string{"id": "not-a-uuid"}); err == nil {
		t.Error("want standalone Cache to enforce the same key codec validation")
	}
	data, err := c.Format.Marshal(cfgItem{V: 1})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	got, err := c.Format.Unmarshal(data)
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.V != 1 {
		t.Errorf("want V=1, got %d", got.V)
	}
}

// CF4: SocketPattern + CustomFormat(Gob) applies to both directions on a
// DuplexPort (same type both sides in this test — asymmetric is Phase 2).
func TestSocketPattern_CustomFormat_Gob(t *testing.T) {
	p, err := ports.NewDuplexPort[cfgItem, cfgItem]("socket-gob", cfgCodec, cfgCodec, ports.PortOptions{
		Patterns: []ports.Pattern{
			ports.SocketPattern{Path: "/live", CustomFormat: format.Gob(cfgCodec)},
		},
	})
	if err != nil {
		t.Fatalf("construct port: %v", err)
	}
	h, ok := ports.SocketHandle[cfgItem, cfgItem](p)
	if !ok {
		t.Fatal("want SocketHandle to be present")
	}
	data, err := h.InFormat.Marshal(cfgItem{V: 3})
	if err != nil {
		t.Fatalf("marshal in: %v", err)
	}
	if _, err := h.InFormat.Unmarshal(data); err != nil {
		t.Fatalf("unmarshal in: %v", err)
	}
	data, err = h.OutFormat.Marshal(cfgItem{V: 5})
	if err != nil {
		t.Fatalf("marshal out: %v", err)
	}
	if _, err := h.OutFormat.Unmarshal(data); err != nil {
		t.Fatalf("unmarshal out: %v", err)
	}
}

// CF4b: SocketPattern + CustomFormat on a one-directional port (SourcePort)
// must NOT fail when trying to build the unused struct{} side.
func TestSocketPattern_CustomFormat_OneDirectional(t *testing.T) {
	p, err := ports.NewSourcePort[cfgItem]("socket-gob-src", cfgCodec, ports.PortOptions{
		Patterns: []ports.Pattern{
			ports.SocketPattern{Path: "/in", CustomFormat: format.Gob(cfgCodec)},
		},
	})
	if err != nil {
		t.Fatalf("construct port: %v", err)
	}
	h, ok := ports.SocketHandle[cfgItem, struct{}](p)
	if !ok {
		t.Fatal("want SocketHandle to be present")
	}
	data, err := h.InFormat.Marshal(cfgItem{V: 11})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	got, err := h.InFormat.Unmarshal(data)
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.V != 11 {
		t.Errorf("want V=11, got %d", got.V)
	}
}

// CF5: type mismatch returns PatternRegisterError with errors.As + LogValue.
func TestCustomFormat_TypeMismatch(t *testing.T) {
	_, err := ports.NewIOPort[int, cfgItem]("cf-mismatch", intCodec, cfgCodec, ports.PortOptions{
		Patterns: []ports.Pattern{
			ports.FilePattern{Path: "item.bin", CustomFormat: format.Gob(intCodec)}, // wrong type: Format[int] not Format[cfgItem]
		},
	})
	var pre ports.PatternRegisterError
	if !errors.As(err, &pre) || pre.Kind != "file" {
		t.Fatalf("want PatternRegisterError{file}, got %v", err)
	}
	v := pre.LogValue()
	if v.Kind() != slog.KindGroup {
		t.Fatalf("want KindGroup, got %v", v.Kind())
	}
	keys := map[string]bool{}
	for _, a := range v.Group() {
		keys[a.Key] = true
	}
	for _, want := range []string{"port", "kind", "err"} {
		if !keys[want] {
			t.Errorf("missing LogValue key %q", want)
		}
	}
}

// CF6: CustomFormat nil, Format set — unchanged existing behavior (regression).
func TestCustomFormat_Nil_RegressionGuard(t *testing.T) {
	dir := t.TempDir()
	p, err := ports.NewSinkPort[cfgItem]("cf-nil", cfgCodec, ports.PortOptions{
		Patterns: []ports.Pattern{
			ports.FilePattern{Path: dir + "/item.yaml", Format: ports.FileFormatYAML},
		},
	})
	if err != nil {
		t.Fatalf("construct port: %v", err)
	}
	f, ok := ports.FileHandle[cfgItem](p)
	if !ok {
		t.Fatal("want FileHandle to be present")
	}
	if err := f.Write(nil, cfgItem{V: 1}, ports.FileOptions{}); err != nil {
		t.Fatalf("write: %v", err)
	}
	got, err := f.Read(nil, ports.FileOptions{})
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if got.V != 1 {
		t.Errorf("want V=1, got %d", got.V)
	}
}

// CF7: CustomFormat non-nil AND Format also set — CustomFormat wins.
func TestCustomFormat_PrecedenceOverFormat(t *testing.T) {
	dir := t.TempDir()
	p, err := ports.NewSinkPort[cfgItem]("cf-precedence", cfgCodec, ports.PortOptions{
		Patterns: []ports.Pattern{
			ports.FilePattern{
				Path:         dir + "/item.gob",
				Format:       ports.FileFormatYAML, // would produce YAML text if honored
				CustomFormat: format.Gob(cfgCodec), // must win — binary Gob instead
			},
		},
	})
	if err != nil {
		t.Fatalf("construct port: %v", err)
	}
	f, ok := ports.FileHandle[cfgItem](p)
	if !ok {
		t.Fatal("want FileHandle to be present")
	}
	if err := f.Write(nil, cfgItem{V: 77}, ports.FileOptions{}); err != nil {
		t.Fatalf("write: %v", err)
	}
	raw, err := os.ReadFile(dir + "/item.gob")
	if err != nil {
		t.Fatalf("read raw file: %v", err)
	}
	// Gob output is not valid YAML/UTF-8 text starting with "v:" — a cheap
	// but effective way to confirm CustomFormat (not Format) was used.
	if strings.HasPrefix(string(raw), "v:") {
		t.Error("want binary Gob output, got what looks like YAML — CustomFormat did not win")
	}
	got, err := f.Read(nil, ports.FileOptions{})
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if got.V != 77 {
		t.Errorf("want V=77, got %d", got.V)
	}
}
