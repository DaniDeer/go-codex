package stats_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"regexp"
	"testing"
	"time"

	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/stats"
	"github.com/DaniDeer/go-codex/validate"
)

// capturingObserver records all RecordValidationError calls for assertion.
type capturingObserver struct {
	calls []validationCall
}

type validationCall struct {
	location   string
	constraint string
	field      string
}

func (o *capturingObserver) RecordValidationError(location, constraint, field string) {
	o.calls = append(o.calls, validationCall{location, constraint, field})
}

func assertCalls(t *testing.T, obs *capturingObserver, want []validationCall) {
	t.Helper()
	if len(obs.calls) != len(want) {
		t.Fatalf("RecordValidationError called %d times, want %d; got %v", len(obs.calls), len(want), obs.calls)
	}
	for i, got := range obs.calls {
		w := want[i]
		if got.location != w.location || got.constraint != w.constraint || got.field != w.field {
			t.Errorf("call[%d]: got {%q, %q, %q}, want {%q, %q, %q}",
				i, got.location, got.constraint, got.field,
				w.location, w.constraint, w.field)
		}
	}
}

func TestReportErrors_ValidationErrors(t *testing.T) {
	c := codex.Struct[struct{ Name string }](
		codex.RequiredField("name", codex.String().Refine(validate.NonEmptyString),
			func(v struct{ Name string }) string { return v.Name },
			func(v *struct{ Name string }, s string) { v.Name = s },
		),
	)
	_, err := c.Decode(map[string]any{"name": ""})
	if err == nil {
		t.Fatal("expected error")
	}
	obs := &capturingObserver{}
	stats.ReportErrors(obs, "body", err)
	assertCalls(t, obs, []validationCall{
		{"body", "non-empty", "name"},
	})
}

func TestReportErrors_KeyError(t *testing.T) {
	pattern := regexp.MustCompile(`^[a-z]+-\d+$`)
	keyCodec := codex.String().Refine(validate.Pattern(pattern))
	c := codex.Map[string, int](keyCodec, codex.Int())

	_, err := c.Encode(map[string]int{"INVALID_KEY": 1})
	if err == nil {
		t.Fatal("expected error")
	}

	obs := &capturingObserver{}
	stats.ReportErrors(obs, "payload", err)
	if len(obs.calls) == 0 {
		t.Fatal("RecordValidationError not called for KeyError")
	}
	got := obs.calls[0]
	if got.field != "INVALID_KEY" {
		t.Errorf("want field=INVALID_KEY, got %q", got.field)
	}
	if got.location != "payload" {
		t.Errorf("want location=payload, got %q", got.location)
	}
	if got.constraint == "" {
		t.Error("want non-empty constraint name")
	}
}

func TestReportErrors_KeyError_Decode(t *testing.T) {
	pattern := regexp.MustCompile(`^[a-z]+-\d+$`)
	keyCodec := codex.String().Refine(validate.Pattern(pattern))
	c := codex.Map[string, int](keyCodec, codex.Int())

	_, err := c.Decode(map[string]any{"BAD_KEY": 5})
	if err == nil {
		t.Fatal("expected error")
	}

	obs := &capturingObserver{}
	stats.ReportErrors(obs, "payload", err)
	if len(obs.calls) == 0 {
		t.Fatal("RecordValidationError not called for KeyError on Decode")
	}
	if obs.calls[0].field != "BAD_KEY" {
		t.Errorf("want field=BAD_KEY, got %q", obs.calls[0].field)
	}
}

func TestReportErrors_ElementError(t *testing.T) {
	c := codex.SliceOf(codex.Int().Refine(validate.PositiveInt))

	_, err := c.Decode([]any{1, -5, 3})
	if err == nil {
		t.Fatal("expected error")
	}

	obs := &capturingObserver{}
	stats.ReportErrors(obs, "items", err)
	if len(obs.calls) == 0 {
		t.Fatal("RecordValidationError not called for ElementError")
	}
	got := obs.calls[0]
	if got.field != "[1]" {
		t.Errorf("want field=[1], got %q", got.field)
	}
	if got.location != "items" {
		t.Errorf("want location=items, got %q", got.location)
	}
}

func TestReportErrors_Nil(t *testing.T) {
	obs := &capturingObserver{}
	stats.ReportErrors(obs, "loc", nil)
	if len(obs.calls) != 0 {
		t.Errorf("expected no calls for nil error, got %v", obs.calls)
	}
}

func TestReportErrors_UnknownError(t *testing.T) {
	obs := &capturingObserver{}
	stats.ReportErrors(obs, "loc", fmt.Errorf("some opaque error"))
	if len(obs.calls) != 0 {
		t.Errorf("expected no calls for opaque error, got %v", obs.calls)
	}
}

func TestReportErrors_NestedKeyError_ThroughUnwrap(t *testing.T) {
	// Simulate forge.InputError wrapping a KeyError (the forge-collection Scenario 5 path).
	pattern := regexp.MustCompile(`^[a-z]+-\d+$`)
	keyCodec := codex.String().Refine(validate.Pattern(pattern))
	mc := codex.Map[string, int](keyCodec, codex.Int())
	_, innerErr := mc.Encode(map[string]int{"BAD": 1})
	if innerErr == nil {
		t.Fatal("expected inner error")
	}
	// Wrap like forge.InputError would.
	wrapped := fmt.Errorf("outer wrapper: %w", innerErr)
	obs := &capturingObserver{}
	stats.ReportErrors(obs, "sensors", wrapped)
	if len(obs.calls) == 0 {
		t.Fatal("RecordValidationError not called for nested KeyError through Unwrap")
	}
	if obs.calls[0].field != "BAD" {
		t.Errorf("want field=BAD, got %q", obs.calls[0].field)
	}
}

func TestConstraintName_KeyError(t *testing.T) {
	pattern := regexp.MustCompile(`^[a-z]+-\d+$`)
	keyCodec := codex.String().Refine(validate.Pattern(pattern))
	mc := codex.Map[string, int](keyCodec, codex.Int())
	_, err := mc.Encode(map[string]int{"BAD": 1})
	if err == nil {
		t.Fatal("expected error")
	}
	var ke codex.KeyError
	if !isKeyError(err, &ke) {
		t.Fatalf("expected KeyError, got %T", err)
	}
	name := stats.ConstraintName(ke.Err)
	if name == "" {
		t.Error("ConstraintName should return non-empty for ConstraintError inside KeyError")
	}
}

// isKeyError uses errors.As to check for KeyError.
func isKeyError(err error, target *codex.KeyError) bool {
	if ke, ok := err.(codex.KeyError); ok {
		*target = ke
		return true
	}
	return false
}

// --- Example functions (shown on pkg.go.dev as runnable snippets) ---

func ExampleReportErrors() {
	// ValidationObserver: implement to receive per-field validation events.
	type simpleObserver struct{ count int }
	obs := &simpleObserver{}

	recordFn := func(location, constraintName, field string) {
		obs.count++
	}

	// Wrap in a ValidationObserver for ReportErrors.
	type wrapObs struct{ fn func(string, string, string) }
	w := &wrapObs{fn: recordFn}

	// Example: decode a struct with two failing fields.
	type Config struct {
		Port  int
		Level string
	}
	portCodec := codex.Int().Refine(validate.RangeInt(1, 65535))
	levelCodec := codex.String().Refine(validate.OneOf("debug", "info", "warn", "error"))
	configCodec := codex.Struct[Config](
		codex.RequiredField("port", portCodec,
			func(c Config) int { return c.Port },
			func(c *Config, v int) { c.Port = v },
		),
		codex.OptionalField("level", levelCodec,
			func(c Config) string { return c.Level },
			func(c *Config, v string) { c.Level = v },
		),
	)

	_, err := configCodec.Decode(map[string]any{"port": float64(99999), "level": "verbose"})
	_ = w // suppress unused warning
	stats.ReportErrors(stats.NoopObserver{}, "config", err)

	// err is a structured codex.ValidationErrors — all fields collected at once.
	fmt.Println(err != nil)
	// Output:
	// true
}

// ── LoggingObserver ───────────────────────────────────────────────────────────

func TestLoggingObserver_ImplementsAllInterfaces(t *testing.T) {
	obs := stats.NewLoggingObserver(slog.New(slog.NewTextHandler(io.Discard, nil)))
	var _ stats.Observer = obs
	var _ stats.PipelineObserver = obs
	var _ stats.SecurityObserver = obs
	var _ stats.FileObserver = obs
	var _ stats.SQLObserver = obs
}

func TestLoggingObserver_AllMethods_NoPanic(t *testing.T) {
	obs := stats.NewLoggingObserver(slog.New(slog.NewTextHandler(io.Discard, nil)))
	obs.RecordValidationError("body", "minLen(3)", "email")
	obs.RecordRequest("GET", "/users/{id}", 200, 5*time.Millisecond)
	obs.RecordSubscribe("sensors/+/data", true, 2*time.Millisecond)
	obs.RecordPublish("sensors/42/data", false, 1*time.Millisecond)
	obs.RecordApply("oeeCalc", "1.0.0", true, 3*time.Millisecond)
	obs.RecordSecurityRejection("/admin", "bearerAuth")
	obs.RecordFileRead("/etc/config.toml", true, 1*time.Millisecond)
	obs.RecordFileWrite("/etc/config.toml", false, 1*time.Millisecond)
	obs.RecordValidation("users", "get_user", 1*time.Millisecond, nil)
	obs.RecordMigration("up", "00001_create_users.sql", 1, 2*time.Millisecond, nil)
}

// ── NewFanout ─────────────────────────────────────────────────────────────────

// fanoutSpy records call counts for assertion.
type fanoutSpy struct {
	stats.NoopObserver
	valErrors, requests, subscribes, publishes int
}

func (s *fanoutSpy) RecordValidationError(_, _, _ string)              { s.valErrors++ }
func (s *fanoutSpy) RecordRequest(_, _ string, _ int, _ time.Duration) { s.requests++ }
func (s *fanoutSpy) RecordSubscribe(_ string, _ bool, _ time.Duration) { s.subscribes++ }
func (s *fanoutSpy) RecordPublish(_ string, _ bool, _ time.Duration)   { s.publishes++ }

type fanoutFileSpy struct {
	fanoutSpy
	reads, writes int
}

func (s *fanoutFileSpy) RecordFileRead(_ string, _ bool, _ time.Duration)  { s.reads++ }
func (s *fanoutFileSpy) RecordFileWrite(_ string, _ bool, _ time.Duration) { s.writes++ }

var _ stats.FileObserver = (*fanoutFileSpy)(nil)

type fanoutSecSpy struct {
	fanoutSpy
	rejections int
}

func (s *fanoutSecSpy) RecordSecurityRejection(_, _ string) { s.rejections++ }

var _ stats.SecurityObserver = (*fanoutSecSpy)(nil)

type fanoutPipeSpy struct {
	fanoutSpy
	applies int
}

func (s *fanoutPipeSpy) RecordApply(_, _ string, _ bool, _ time.Duration) { s.applies++ }

var _ stats.PipelineObserver = (*fanoutPipeSpy)(nil)

func TestNewFanout_DelegatesBaseToAll(t *testing.T) {
	a, b := &fanoutSpy{}, &fanoutSpy{}
	obs := stats.NewFanout(a, b)
	obs.RecordValidationError("body", "email", "f")
	obs.RecordRequest("GET", "/", 200, 0)
	obs.RecordSubscribe("t", true, 0)
	obs.RecordPublish("t", false, 0)
	for _, s := range []*fanoutSpy{a, b} {
		if s.valErrors != 1 || s.requests != 1 || s.subscribes != 1 || s.publishes != 1 {
			t.Errorf("base call not fanned out: %+v", s)
		}
	}
}

func TestNewFanout_FileObserver_OnlyToImplementors(t *testing.T) {
	plain := &fanoutSpy{}
	file := &fanoutFileSpy{}
	obs := stats.NewFanout(plain, file)
	fo, ok := obs.(stats.FileObserver)
	if !ok {
		t.Fatal("fanout must implement FileObserver")
	}
	fo.RecordFileRead("/p", true, 0)
	fo.RecordFileWrite("/p", false, 0)
	if file.reads != 1 || file.writes != 1 {
		t.Errorf("FileObserver not delegated: reads=%d writes=%d", file.reads, file.writes)
	}
}

func TestNewFanout_SecurityObserver_OnlyToImplementors(t *testing.T) {
	plain := &fanoutSpy{}
	sec := &fanoutSecSpy{}
	obs := stats.NewFanout(plain, sec)
	so, ok := obs.(stats.SecurityObserver)
	if !ok {
		t.Fatal("fanout must implement SecurityObserver")
	}
	so.RecordSecurityRejection("/admin", "bearer")
	if sec.rejections != 1 {
		t.Errorf("SecurityObserver not delegated: %d", sec.rejections)
	}
}

func TestNewFanout_PipelineObserver_OnlyToImplementors(t *testing.T) {
	plain := &fanoutSpy{}
	pipe := &fanoutPipeSpy{}
	obs := stats.NewFanout(plain, pipe)
	po, ok := obs.(stats.PipelineObserver)
	if !ok {
		t.Fatal("fanout must implement PipelineObserver")
	}
	po.RecordApply("fn", "1.0", true, 0)
	if pipe.applies != 1 {
		t.Errorf("PipelineObserver not delegated: %d", pipe.applies)
	}
}

func TestNewFanout_Empty_NoPanic(t *testing.T) {
	obs := stats.NewFanout()
	obs.RecordValidationError("x", "y", "z")
	if fo, ok := obs.(stats.FileObserver); ok {
		fo.RecordFileRead("/", true, 0)
	}
}

func TestNewFanout_WithLoggingObserver(t *testing.T) {
	metrics := &fanoutSpy{}
	logging := stats.NewLoggingObserver(slog.New(slog.NewTextHandler(io.Discard, nil)))
	obs := stats.NewFanout(metrics, logging)
	obs.RecordValidationError("body", "email", "email")
	obs.RecordRequest("POST", "/users", 201, 10*time.Millisecond)
	if metrics.valErrors != 1 || metrics.requests != 1 {
		t.Errorf("metrics not updated: valErrs=%d reqs=%d", metrics.valErrors, metrics.requests)
	}
}

// ── TraceObserver tests ──────────────────────────────────────────────────────

type traceSpy struct {
	stats.NoopObserver
	startOperation string
	startName      string
	endErr         error
	started        int
	ended          int
}

func (s *traceSpy) StartSpan(ctx context.Context, operation, name string) context.Context {
	s.startOperation = operation
	s.startName = name
	s.started++
	return context.WithValue(ctx, traceKey{}, "injected")
}

func (s *traceSpy) EndSpan(ctx context.Context, err error) {
	s.endErr = err
	s.ended++
	if v := ctx.Value(traceKey{}); v != nil {
		_ = v
	}
}

type traceKey struct{}

func TestTraceObserver_StartEnd(t *testing.T) {
	spy := &traceSpy{}
	obs := stats.NewFanout(spy)
	to, ok := obs.(stats.TraceObserver)
	if !ok {
		t.Fatal("fanout with TraceObserver must implement TraceObserver")
	}
	ctx := to.StartSpan(context.Background(), "http.request", "/users/{id}")
	if spy.startOperation != "http.request" {
		t.Errorf("want http.request, got %s", spy.startOperation)
	}
	if spy.startName != "/users/{id}" {
		t.Errorf("want /users/{id}, got %s", spy.startName)
	}
	if spy.started != 1 {
		t.Errorf("want 1 StartSpan call, got %d", spy.started)
	}
	if ctx.Value(traceKey{}) == nil {
		t.Error("want StartSpan to return modified ctx")
	}
	to.EndSpan(ctx, nil)
	if spy.ended != 1 {
		t.Errorf("want 1 EndSpan call, got %d", spy.ended)
	}
	if spy.endErr != nil {
		t.Errorf("want nil err, got %v", spy.endErr)
	}
}

func TestTraceObserver_EndSpanWithError(t *testing.T) {
	spy := &traceSpy{}
	obs := stats.NewFanout(spy)
	to := obs.(stats.TraceObserver)
	ctx := to.StartSpan(context.Background(), "file.read", "/tmp/data.json")
	errSentinel := errors.New("file not found")
	to.EndSpan(ctx, errSentinel)
	if spy.ended != 1 {
		t.Errorf("want 1 EndSpan call, got %d", spy.ended)
	}
	if !errors.Is(spy.endErr, errSentinel) {
		t.Errorf("want err sentinel, got %v", spy.endErr)
	}
}

func TestFanout_TraceObserver_OnlyToImplementors(t *testing.T) {
	spy := &traceSpy{}
	plain := &fanoutSpy{} // does NOT implement TraceObserver
	obs := stats.NewFanout(plain, spy)
	to, ok := obs.(stats.TraceObserver)
	if !ok {
		t.Fatal("fanout must implement TraceObserver when any inner does")
	}
	ctx := to.StartSpan(context.Background(), "forge.apply", "calc")
	if spy.started != 1 {
		t.Errorf("want 1 StartSpan on spy, got %d", spy.started)
	}
	to.EndSpan(ctx, errors.New("fail"))
	if spy.ended != 1 {
		t.Errorf("want 1 EndSpan on spy, got %d", spy.ended)
	}
}

func TestLoggingObserver_DoesNotImplementTraceObserver(t *testing.T) {
	logging := stats.NewLoggingObserver(slog.New(slog.NewTextHandler(io.Discard, nil)))
	var anyObs any = logging
	_, ok := anyObs.(stats.TraceObserver)
	if ok {
		t.Error("LoggingObserver should NOT implement TraceObserver")
	}
}

func TestNoopObserver_ImplementsTraceObserver(t *testing.T) {
	var noop stats.NoopObserver
	var anyObs any = noop
	to, ok := anyObs.(stats.TraceObserver)
	if !ok {
		t.Fatal("NoopObserver must implement TraceObserver")
	}
	ctx := to.StartSpan(context.Background(), "op", "name")
	if ctx == nil {
		t.Error("StartSpan must return non-nil ctx")
	}
	// Must not panic
	to.EndSpan(ctx, nil)
	to.EndSpan(ctx, errors.New("err"))
}

// ── SQLObserver fanout tests ──────────────────────────────────────────────────

type sqlFanoutSpy struct {
	stats.NoopObserver
	validations int
	migrations  int
}

func (s *sqlFanoutSpy) RecordValidation(_, _ string, _ time.Duration, _ error) { s.validations++ }
func (s *sqlFanoutSpy) RecordMigration(_, _ string, _ int64, _ time.Duration, _ error) {
	s.migrations++
}

func TestFanout_SQLObserver_OnlyToImplementors(t *testing.T) {
	spy := &sqlFanoutSpy{}
	plain := &fanoutSpy{} // does NOT implement SQLObserver
	obs := stats.NewFanout(plain, spy)

	so, ok := obs.(stats.SQLObserver)
	if !ok {
		t.Fatal("fanout must implement SQLObserver when any inner does")
	}

	so.RecordValidation("users", "get_user", time.Millisecond, nil)
	if spy.validations != 1 {
		t.Errorf("RecordValidation: want 1 call on spy, got %d", spy.validations)
	}

	so.RecordMigration("up", "00001_create_users.sql", 1, time.Millisecond, nil)
	if spy.migrations != 1 {
		t.Errorf("RecordMigration: want 1 call on spy, got %d", spy.migrations)
	}
}

func TestFanout_SQLObserver_SkipsNonImplementors(t *testing.T) {
	plain := &fanoutSpy{} // does NOT implement SQLObserver
	obs := stats.NewFanout(plain)

	so, ok := obs.(stats.SQLObserver)
	if !ok {
		t.Fatal("fanout must implement SQLObserver regardless of inner observers")
	}
	// Must not panic when no inner observer implements SQLObserver.
	so.RecordValidation("orders", "insert_order", time.Millisecond, nil)
	so.RecordMigration("down", "00002_add_status.sql", 2, time.Millisecond, nil)
}
