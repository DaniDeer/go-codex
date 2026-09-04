// Package ports-plain-go demonstrates that ports.SourcePort, ports.SinkPort,
// and ports.ToolPort can be used with ZERO forge/gstream composition — plain
// idiomatic Go is a first-class consumption style, not a fallback.
//
// The declaration mechanism is IDENTICAL to a forge-pipeline application like
// examples/sensor-service: declare the port's shape, plug in a
// ports.RESTPattern to get a typed handle, bind a concrete adapter. Only the
// code AFTER Bind differs:
//
//	// examples/sensor-service (forge-pipeline style):
//	sensors := domain.SensorReadings.Stream(ctx)
//	oee := stream.Apply(ctx, sensors, oeeCalcFn, stream.ApplyOptions{})
//	go domain.OEEResults.Feed(ctx, oee)
//
//	// examples/ports-plain-go (plain-Go style, this file):
//	stream.Drain(ctx, Readings.Stream(ctx), onReading, nil, stream.DrainOptions{})
//	Alerts.Start(ctx); Alerts.Push(ctx, alert); Alerts.Close()
//
// Three ports, three plain-Go escape hatches:
//
//   - SourcePort[TempReading] — consumed via Stream(ctx) + stream.Drain,
//     never gstream.Apply.
//   - ToolPort[ConvertIn, ConvertOut] — business logic registered via
//     SetFunc(func(ctx, In) (Out, error)) AND via SetPipeline, on two
//     endpoints sharing the SAME RESTPattern shape (path param + header
//     param) — proving path/header merge-field codecs behave identically
//     regardless of consumption style.
//   - SinkPort[Alert] — driven via Start/Push/Close from a plain goroutine,
//     never Feed(ctx, stream).
//
// # Path and header codecs work the same in both consumption styles
//
// ConvertIn's Unit field is merged from the URL path ("/convert/{unit}")
// and its TraceID field from an optional request header ("X-Trace-Id") —
// declared once via rest.NewPathParam/rest.NewOptionalHeaderParam on a
// shared RESTPattern builder (convertPattern). Both the SetFunc endpoint
// and the SetPipeline endpoint plug in that SAME pattern shape (only the
// path prefix differs, to run both on one mux); nethttp.PipelineAdapter
// merges path+header vars into ConvertIn automatically for either one — the
// merge-field codec layer does not know or care whether the handler
// behind it is a plain function or a forge-style pipeline.
//
// Two DIFFERENT codecs are in play for "unit", at two DIFFERENT layers:
// unitCodec validates the segment's runtime VALUE ("c" or "f", checked per
// request), while exactUnitPathConstraint validates the route TEMPLATE's
// SHAPE (prefix, exactly one placeholder, nothing after — checked once at
// Register time via rest.WithPathConstraints on each ToolPort's own
// rest.Server). Both reuse the same declare-once idiom this file follows
// throughout.
//
// # Running
//
// go run ./examples/ports-plain-go
package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"

	"github.com/DaniDeer/go-codex/adapters/nethttp"
	"github.com/DaniDeer/go-codex/api/rest"
	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/forge"
	"github.com/DaniDeer/go-codex/ports"
	"github.com/DaniDeer/go-codex/stream"
	"github.com/DaniDeer/go-codex/validate"
)

// ── Domain types — no adapter imports ──────────────────────────────────────

// TempReading is a temperature reading from a sensor.
type TempReading struct {
	SensorID string
	Celsius  float64
}

var tempReadingCodec = codex.Struct[TempReading](
	codex.RequiredField("sensorID", codex.String(),
		func(t TempReading) string { return t.SensorID },
		func(t *TempReading, v string) { t.SensorID = v }),
	codex.RequiredField("celsius", codex.Float64(),
		func(t TempReading) float64 { return t.Celsius },
		func(t *TempReading, v float64) { t.Celsius = v }),
)

// Alert is raised when a reading exceeds the threshold.
type Alert struct {
	SensorID string
	Message  string
}

var alertCodec = codex.Struct[Alert](
	codex.RequiredField("sensorID", codex.String(),
		func(a Alert) string { return a.SensorID },
		func(a *Alert, v string) { a.SensorID = v }),
	codex.RequiredField("message", codex.String(),
		func(a Alert) string { return a.Message },
		func(a *Alert, v string) { a.Message = v }),
)

// ConvertIn is the ToolPort's request type. Value and Unit travel in the
// JSON body; Unit is ALSO merged in from the URL path, and TraceID from an
// optional request header — both by the exact same
// rest.NewPathParam/rest.NewOptionalHeaderParam mechanism documented in
// docs/concepts/api-contracts.md ("one struct, one call"). DecodeMerged
// runs the merge AFTER body decode, so the path's Unit always wins over
// whatever the body sent — the body copy is redundant, not authoritative.
type ConvertIn struct {
	Value   float64 // body: {"value": 100}
	Unit    string  // path: /convert/{unit} — "c" (→ Fahrenheit) or "f" (→ Celsius)
	TraceID string  // header: X-Trace-Id (optional, echoed back in the response)
}

// valueCodec/unitCodec/traceIDCodec are declared ONCE, per field — the
// "one struct, one call" pattern extends one level deeper here: the SAME
// codec value backs both the field's validation constraint AND its
// wire-boundary constructor (body field, path param, header param) below,
// instead of repeating the Refine chain inline at each call site.
var (
	valueCodec   = codex.Float64()
	unitCodec    = codex.String().Refine(validate.OneOf("c", "f"))
	traceIDCodec = codex.String()
)

// convertInCodec declares all three fields, reusing valueCodec/unitCodec/
// traceIDCodec (never re-declared). "unit" stays REQUIRED — the request
// body includes it too (redundant with the path segment, see main()) —
// while "traceID" stays OPTIONAL, matching the header being optional.
// DecodeMerged always merges path/header vars AFTER body decode, so
// whatever "unit" the caller puts in the body is immediately overwritten by
// the authoritative path value — declaring it Required here just means the
// OpenAPI schema for ConvertIn documents it as part of the payload shape,
// consistent with the URL template it is also bound to.
var convertInCodec = codex.Struct[ConvertIn](
	codex.RequiredField("value", valueCodec,
		func(c ConvertIn) float64 { return c.Value },
		func(c *ConvertIn, v float64) { c.Value = v }),
	codex.RequiredField("unit", unitCodec,
		func(c ConvertIn) string { return c.Unit },
		func(c *ConvertIn, v string) { c.Unit = v }),
	codex.OptionalField("traceID", traceIDCodec,
		func(c ConvertIn) string { return c.TraceID },
		func(c *ConvertIn, v string) { c.TraceID = v }),
)

// exactUnitPathConstraint makes the path SCHEMA itself explicit — not just
// the "unit" segment's VALUE (that's unitCodec, applied per-request by
// NewPathParam below), but the route TEMPLATE's shape: prefix, followed by
// exactly one placeholder segment, with nothing after it. Applied via
// rest.WithPathConstraints, this runs once at Register time — but the
// constraint never sees the literal "{unit}" text: go-codex strips every
// {varName} placeholder down to the single character "x" first (api/
// internal.StripTemplateVars) so path-shape constraints are
// "template-transparent" — they validate structure (segment count, literal
// text), not variable names. So a template of "/convert/{unit}" arrives
// here as "/convert/x"; a malformed template (wrong prefix, an extra
// trailing segment, or a SECOND placeholder) produces "/convert/x/x" or
// similar and fails immediately instead of silently registering.
func exactUnitPathConstraint(prefix string) codex.Constraint[string] {
	want := prefix + "x" // "x" is StripTemplateVars' placeholder stand-in for {unit}
	return codex.Constraint[string]{
		Name:  "exact-unit-path",
		Check: func(shape string) bool { return shape == want },
		Message: func(shape string) string {
			return fmt.Sprintf("path must be exactly %q (prefix %q, nothing after {unit}); got shape %q",
				prefix+"{unit}", prefix, shape)
		},
	}
}

// convertPattern builds the SAME path-param + header-param RESTPattern
// shape for a given prefix — used by BOTH the SetFunc endpoint and the
// SetPipeline endpoint below, to prove the merge-field codec layer behaves
// identically regardless of which consumption style handles the request.
// The path itself is assembled from prefix, not passed in free-form, so
// exactUnitPathConstraint(prefix) (enforced via the port's RESTBuilder, see
// main()) always agrees with what's actually registered.
func convertPattern(prefix string) ports.RESTPattern {
	return ports.RESTPattern{
		Method: http.MethodPost,
		Path:   prefix + "{unit}",
		Opts: []rest.RouteOpt{
			// NewPathParam reuses unitCodec — BOTH validates "unit"'s VALUE
			// against it AND merges it into ConvertIn.Unit after body
			// decode. exactUnitPathConstraint (above) is the companion
			// check on the template SHAPE, not the value.
			rest.NewPathParam[ConvertIn]("unit", unitCodec,
				func(c ConvertIn) string { return c.Unit },
				func(c *ConvertIn, v string) { c.Unit = v }),
			// NewOptionalHeaderParam reuses traceIDCodec — same merge
			// mechanism, but the header may be absent — ConvertIn.TraceID
			// simply stays "".
			rest.NewOptionalHeaderParam[ConvertIn]("X-Trace-Id", traceIDCodec,
				func(c ConvertIn) string { return c.TraceID },
				func(c *ConvertIn, v string) { c.TraceID = v }),
		},
	}
}

type ConvertOut struct {
	Result  float64
	TraceID string
}

var convertOutCodec = codex.Struct[ConvertOut](
	codex.RequiredField("result", codex.Float64(),
		func(c ConvertOut) float64 { return c.Result },
		func(c *ConvertOut, v float64) { c.Result = v }),
	codex.RequiredField("traceID", codex.String(),
		func(c ConvertOut) string { return c.TraceID },
		func(c *ConvertOut, v string) { c.TraceID = v }),
)

// convertFn is the shared business logic used by BOTH ToolPort endpoints —
// only the registration call (SetFunc vs SetPipeline) differs.
func convertFn(in ConvertIn) ConvertOut {
	result := in.Value
	switch in.Unit {
	case "c":
		result = in.Value*9/5 + 32 // Celsius -> Fahrenheit
	case "f":
		result = (in.Value - 32) * 5 / 9 // Fahrenheit -> Celsius
	}
	return ConvertOut{Result: result, TraceID: in.TraceID}
}

// convertFunction wraps convertFn as a *forge.Function so it can be used
// with stream.Apply — the pipeline-composed counterpart to SetFunc's plain
// closure. Same computation, same codecs, different registration surface.
var convertFunction = forge.NewFunction("convert", "1.0.0", convertInCodec, convertOutCodec,
	func(in ConvertIn) (ConvertOut, error) { return convertFn(in), nil })

// ── Port declarations — same shape as any forge-pipeline application ──────

var (
	// Readings is fed test data via ports.ChanSourceAdapter — a real
	// application would bind mqtt5.SubscribeAdapter or nethttp.IngestAdapter
	// instead; the declaration and consumption code below never changes.
	Readings = codex.Must(ports.NewSourcePort[TempReading](
		"readings", tempReadingCodec, ports.PortOptions{Buffer: 8}))

	// Alerts is drained by main() into stdout via ports.ChanSinkAdapter.
	Alerts = codex.Must(ports.NewSinkPort[Alert](
		"alerts", alertCodec, ports.PortOptions{Buffer: 8}))
)

const threshold = 30.0

// doConvertRequest performs req and returns the response body, closing it
// via defer (the standard net/http idiom — unlike a bare call, a deferred
// Close's discarded error is the accepted pattern used throughout
// examples/adapters-nethttp and examples/adapters-chi).
func doConvertRequest(req *http.Request) ([]byte, error) {
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return io.ReadAll(resp.Body)
}

func main() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	mux := http.NewServeMux()

	// Each ToolPort gets its own rest.Server with exactUnitPathConstraint
	// bound to its own prefix — WithPathConstraints checks the literal
	// route TEMPLATE at Register time, so a typo'd path (wrong prefix, an
	// extra trailing segment, a differently-named placeholder) fails
	// immediately instead of silently registering.
	convertBuilder := rest.NewServer(rest.Info{Title: "ports-plain-go", Version: "1.0.0"},
		rest.WithPathConstraints(exactUnitPathConstraint("/convert/")))
	pipelineBuilder := rest.NewServer(rest.Info{Title: "ports-plain-go-pipeline", Version: "1.0.0"},
		rest.WithPathConstraints(exactUnitPathConstraint("/convert-pipeline/")))

	// ── ToolPort #1: SetFunc — path + header codecs, plain-Go style ────────
	//
	// NewRestToolPort combines port construction and Pattern-plugging into
	// one call — the SAME two steps (declare, plug in Pattern) as a
	// forge-pipeline ToolPort; only SetFunc (below) differs from SetPipeline.
	convertPort, convertHandle := codex.Must2(ports.NewRestToolPort[ConvertIn, ConvertOut](
		"convert", convertInCodec, convertOutCodec,
		convertPattern("/convert/"), ports.PortOptions{RESTBuilder: convertBuilder}))

	// Plain Go business logic — no gstream.Stream, no forge.Function. `in`
	// already has Unit/TraceID merged in by the time SetFunc's fn runs.
	convertPort.SetFunc(func(_ context.Context, in ConvertIn) (ConvertOut, error) {
		return convertFn(in), nil
	})

	if err := convertPort.Bind(ctx, nethttp.PipelineAdapter(mux, convertHandle,
		nethttp.PipelineAdapterOptions{})); err != nil {
		fmt.Fprintln(os.Stderr, "bind convert tool:", err)
		os.Exit(1)
	}

	// ── ToolPort #2: SetPipeline — SAME path+header Pattern shape ──────────
	//
	// Different prefix (mux needs distinct routes) but the identical
	// convertPattern() call — proving path/header merge fields aren't
	// special-cased for plain-Go endpoints.
	pipelinePort, pipelineHandle := codex.Must2(ports.NewRestToolPort[ConvertIn, ConvertOut](
		"convert-pipeline", convertInCodec, convertOutCodec,
		convertPattern("/convert-pipeline/"), ports.PortOptions{RESTBuilder: pipelineBuilder}))

	pipelinePort.SetPipeline(func(ctx context.Context, in ConvertIn) stream.Stream[ConvertOut] {
		return stream.Apply(ctx, stream.Single(ctx, in), convertFunction, stream.ApplyOptions{})
	})

	if err := pipelinePort.Bind(ctx, nethttp.PipelineAdapter(mux, pipelineHandle,
		nethttp.PipelineAdapterOptions{})); err != nil {
		fmt.Fprintln(os.Stderr, "bind convert-pipeline tool:", err)
		os.Exit(1)
	}

	srv := httptest.NewServer(mux)
	defer srv.Close()

	// Call both endpoints the same way — the body also carries "unit"
	// (satisfying convertInCodec's Required field), redundant with the path
	// segment, which is what actually wins after DecodeMerged — TraceID
	// comes from a header — to show the merge-field codecs work identically
	// for SetFunc and SetPipeline handlers alike.
	for _, path := range []string{"/convert/c", "/convert-pipeline/c"} {
		req, _ := http.NewRequest(http.MethodPost, srv.URL+path,
			strings.NewReader(`{"value":100,"unit":"c"}`))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Trace-Id", "demo-trace-1")
		respBody, err := doConvertRequest(req)
		if err != nil {
			fmt.Fprintln(os.Stderr, "call", path, ":", err)
			os.Exit(1)
		}
		fmt.Printf("POST %s {value:100,unit:c} -> %s\n", path, respBody)
	}

	// ── SourcePort: stream.Drain instead of gstream.Apply ──────────────────
	readingCh := make(chan TempReading, 4)
	Readings.Bind(ctx, ports.ChanSourceAdapter[TempReading](readingCh))

	// ── SinkPort: Start/Push/Close instead of Feed(ctx, stream) ────────────
	alertOut := make(chan Alert, 4)
	Alerts.Bind(ctx, ports.ChanSinkAdapter[Alert](alertOut))
	Alerts.Start(ctx)

	done := make(chan struct{})
	go func() {
		defer close(done)
		for a := range alertOut {
			fmt.Printf("ALERT [%s]: %s\n", a.SensorID, a.Message)
		}
	}()

	readingCh <- TempReading{SensorID: "s1", Celsius: 22.0}
	readingCh <- TempReading{SensorID: "s2", Celsius: 35.5}
	readingCh <- TempReading{SensorID: "s3", Celsius: 31.2}
	close(readingCh)

	stream.Drain(ctx, Readings.Stream(ctx),
		func(ctx context.Context, r TempReading) error {
			fmt.Printf("reading: %s = %.1fC\n", r.SensorID, r.Celsius)
			if r.Celsius > threshold {
				return Alerts.Push(ctx, Alert{
					SensorID: r.SensorID,
					Message:  fmt.Sprintf("%.1fC exceeds threshold %.1fC", r.Celsius, threshold),
				})
			}
			return nil
		},
		func(err error) { fmt.Fprintln(os.Stderr, "reading error:", err) },
		stream.DrainOptions{})

	if err := Alerts.Close(); err != nil {
		fmt.Fprintln(os.Stderr, "close alerts:", err)
	}
	// Close waited for the adapter to finish draining, so it's now safe to
	// close the channel ports.ChanSinkAdapter writes to.
	close(alertOut)
	<-done
}
