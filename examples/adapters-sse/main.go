// Package adapters-sse demonstrates Server-Sent Events (SSE) using the
// go-codex adapters/nethttp and adapters/chi adapters — a FULL ROUNDTRIP:
// every route below is declared once, served, AND consumed back by a
// client in this same example.
//
// # What this example shows
//
//   - [rest.NewSSERoute] — declaring a typed SSE route with request and event codecs
//   - [nethttp.AttachMux] — wiring a whole builder's SSE routes onto net/http
//   - [chiadapter.AttachRouter] — wiring a whole builder's SSE routes onto chi
//   - Path parameter codec validation on {id} via [rest.PathParam.Codec]
//   - [rest.SSERouteHandle.BuildPath] for validated URL assembly
//   - Codec validation on each event before it is written to the client
//   - Stats observer counting validation errors from rejected events
//   - [rest.ResponseHeaderParam] on SSE routes — custom response header committed on first send
//   - OpenAPI 3.1 spec generation including SSE routes
//   - [nethttp.Consumer] + [nethttp.Consume] — the CLIENT-side counterpart
//     to Call/Caller: declare a route once, pass the SAME value to Consume
//     for consumption, no separate client-side declaration needed
//   - [nethttp.CallSSEAdapter] — the port-adapter counterpart of Consume,
//     taking a pre-built *SSERouteHandle directly (for [ports.SourcePort]
//     pipelines, or when a caller already holds a handle)
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"time"

	gochi "github.com/go-chi/chi/v5"

	chiadapter "github.com/DaniDeer/go-codex/adapters/chi"
	nethttp "github.com/DaniDeer/go-codex/adapters/nethttp"
	"github.com/DaniDeer/go-codex/api/rest"
	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/format"
	"github.com/DaniDeer/go-codex/ports"
	"github.com/DaniDeer/go-codex/stats"
	"github.com/DaniDeer/go-codex/validate"
)

// ── Domain types ──────────────────────────────────────────────────────────────

type counterEvent struct {
	Count int
}

type sensorReading struct {
	SensorID    string
	Temperature float64
	Unit        string
}

// sensorPathReq is /sse/sensor/{id}'s Req type — declared as a real
// struct (not struct{}) so ID can be registered via [rest.NewPathParam],
// a MERGE-CAPABLE path param. This is what lets the CLIENT side
// (nethttp.Consume/CallSSEAdapter) auto-derive "/sse/sensor/room-42"
// from a plain sensorPathReq{ID: "room-42"} value — a route whose only
// path param is a plain validate-only rest.PathParam (like this one used
// to be) has NO merge field for Consume/CallSSEAdapter to read, so it
// cannot be round-tripped through the declarative client mechanism at
// all (both require merge-field-driven path derivation; there is no
// vars-override escape hatch, by design).
type sensorPathReq struct {
	ID string
}

type readingMeta struct {
	SensorID string
}

type readingPayload struct {
	Temperature float64
	Unit        string
}

type readingEvent struct {
	Meta    readingMeta
	Payload readingPayload
}

// ── Codecs ────────────────────────────────────────────────────────────────────

var counterEventCodec = codex.Struct[counterEvent](
	codex.RequiredField("count", codex.Int(), func(e counterEvent) int { return e.Count }, func(e *counterEvent, v int) { e.Count = v }),
)

var temperatureConstraint = codex.Constraint[float64]{
	Name:    "valid-temperature",
	Check:   func(v float64) bool { return v >= -273.15 },
	Message: func(v float64) string { return fmt.Sprintf("temperature %v below absolute zero", v) },
}

var sensorReadingCodec = codex.Struct[sensorReading](
	codex.RequiredField("sensor_id", codex.String().Refine(validate.NonEmptyString), func(r sensorReading) string { return r.SensorID }, func(r *sensorReading, v string) { r.SensorID = v }),
	codex.RequiredField("temperature", codex.Float64().Refine(temperatureConstraint), func(r sensorReading) float64 { return r.Temperature }, func(r *sensorReading, v float64) { r.Temperature = v }),
	codex.RequiredField("unit", codex.String().Refine(validate.NonEmptyString), func(r sensorReading) string { return r.Unit }, func(r *sensorReading, v string) { r.Unit = v }),
)

var readingEventCodec = codex.Struct[readingEvent](
	codex.RequiredField("sensor_id", codex.String().Refine(validate.NonEmptyString), func(r readingEvent) string { return r.Meta.SensorID }, func(r *readingEvent, v string) { r.Meta.SensorID = v }),
	codex.RequiredField("temperature", codex.Float64().Refine(temperatureConstraint), func(r readingEvent) float64 { return r.Payload.Temperature }, func(r *readingEvent, v float64) { r.Payload.Temperature = v }),
	codex.RequiredField("unit", codex.String().Refine(validate.NonEmptyString), func(r readingEvent) string { return r.Payload.Unit }, func(r *readingEvent, v string) { r.Payload.Unit = v }),
)

// sensorIDCodec validates that a sensor ID is in the "<word>-<word>" format.
var sensorIDCodec = codex.String().Refine(
	validate.NonEmptyString,
	codex.Constraint[string]{
		Name:    "sensor-id",
		Check:   func(v string) bool { return len(strings.Split(v, "-")) >= 2 },
		Message: func(v string) string { return fmt.Sprintf("sensor ID %q must be in format '<word>-<word>'", v) },
	},
)

// ── Stats observer ────────────────────────────────────────────────────────────

type statsObserver struct {
	requests         int
	validationErrors int
}

func (o *statsObserver) RecordRequest(method, path string, statusCode int, d time.Duration) {
	o.requests++
}

func (o *statsObserver) RecordValidationError(location, constraint, fieldName string) {
	o.validationErrors++
}

func (o *statsObserver) Print() {
	fmt.Printf("  requests: %d  validation errors: %d\n", o.requests, o.validationErrors)
}

func (o *statsObserver) RecordPublish(_ string, _ bool, _ time.Duration)   {}
func (o *statsObserver) RecordSubscribe(_ string, _ bool, _ time.Duration) {}

var _ stats.Observer = (*statsObserver)(nil)

// ── Handlers ──────────────────────────────────────────────────────────────────

func handleCounter(_ context.Context, _ struct{}, send func(counterEvent) error) error {
	for i := 1; i <= 3; i++ {
		if err := send(counterEvent{Count: i}); err != nil {
			return fmt.Errorf("counter send: %w", err)
		}
	}
	return nil
}

// handleSensor reads {id} via req, populated by the merge-capable
// NewPathParam declared above.
func handleSensor(ctx context.Context, _ sensorPathReq, send func(sensorReading) error) error {
	// req is IGNORED here — even with sensorPathReq's Req-side merge
	// param (added so the CLIENT can auto-derive the connection URL),
	// SSERouteHandle.Decode's own doc comment documents that the SERVER
	// never merges path/query/header/cookie values into Req at all (a
	// zero-value Req is always passed to the handler — confirmed in
	// buildSSERouteHandler) — this is deliberate, unaffected by the
	// client-side merge-field accessors. SSE handlers read path values
	// from context manually, same as handleSensorManualEscape below.
	r, _ := chiadapter.RequestFromContext(ctx)
	sensorID := "unknown"
	if r != nil {
		if id := gochi.URLParam(r, "id"); id != "" {
			sensorID = id
		}
	}

	readings := []float64{20.0, 20.5, 21.0}
	for _, temp := range readings {
		select {
		case <-ctx.Done():
			return nil
		default:
		}
		if err := send(sensorReading{SensorID: sensorID, Temperature: temp, Unit: "°C"}); err != nil {
			return err
		}
	}
	return nil
}

// handleWithHeaders stages a custom response header before sending the first event.
// The adapter commits the header to the wire on the first send() call.
func handleWithHeaders(ctx context.Context, _ struct{}, send func(counterEvent) error) error {
	extra := make(http.Header)
	extra.Set("X-Trace-Id", "trace-abc-123")
	nethttp.WithResponseHeaders(ctx, extra)
	return send(counterEvent{Count: 1})
}

// handleInvalid sends one valid event, then an invalid one (empty SensorID),
// then another valid event — demonstrating per-event validation.
func handleInvalid(_ context.Context, _ struct{}, send func(sensorReading) error) error {
	_ = send(sensorReading{SensorID: "s1", Temperature: 21.0, Unit: "°C"})
	if err := send(sensorReading{SensorID: "", Temperature: 99.0, Unit: "°C"}); err != nil {
		fmt.Printf("  rejected invalid event (expected): %v\n", err)
	}
	_ = send(sensorReading{SensorID: "s1", Temperature: 22.0, Unit: "°C"})
	return nil
}

// handleSensorMergedConvenience sends events without setting SensorID.
// NewRequiredSSEEventParam merges {id} into Meta.SensorID automatically.
func handleSensorMergedConvenience(_ context.Context, _ sensorPathReq, send func(readingEvent) error) error {
	for _, temp := range []float64{21.0, 21.5} {
		if err := send(readingEvent{Payload: readingPayload{Temperature: temp, Unit: "C"}}); err != nil {
			return err
		}
	}
	return nil
}

// handleSensorManualEscape demonstrates the escape hatch:
// manually reading {id} and setting Meta.SensorID before send.
func handleSensorManualEscape(ctx context.Context, _ sensorPathReq, send func(readingEvent) error) error {
	r, _ := nethttp.RequestFromContext(ctx)
	sensorID := "unknown"
	if r != nil {
		sensorID = r.PathValue("id")
	}
	for _, temp := range []float64{21.0, 21.5} {
		if err := send(readingEvent{
			Meta:    readingMeta{SensorID: sensorID},
			Payload: readingPayload{Temperature: temp, Unit: "C"},
		}); err != nil {
			return err
		}
	}
	return nil
}

// ── Main ──────────────────────────────────────────────────────────────────────

// mustServe exits the program if Register/AttachMux/AttachRouter returns an
// error — e.g. a malformed middleware Fn shape, caught eagerly at wiring time.
func mustServe(err error, what string) {
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s failed: %v\n", what, err)
		os.Exit(1)
	}
}

// mustFreeAddr reserves an OS-assigned free TCP port on localhost, then
// releases it immediately so AttachMux/AttachRouter's own *http.Server can
// bind to it.
func mustFreeAddr() string {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		fmt.Fprintf(os.Stderr, "reserve free port failed: %v\n", err)
		os.Exit(1)
	}
	addr := l.Addr().String()
	_ = l.Close()
	return addr
}

// waitForReady polls addr until it accepts TCP connections — Serve wires
// routes synchronously before starting its listener goroutine, so a
// successful dial here guarantees the mux/router is fully wired.
func waitForReady(addr string) {
	for range 100 {
		conn, err := net.DialTimeout("tcp", addr, 50*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug}))
	slog.SetDefault(logger)

	metrics := &statsObserver{}
	obs := stats.NewFanout(metrics, stats.NewLoggingObserver(logger.With("component", "sse")))
	// Observability is the only nethttp/chi call site that
	// touches stats.Observer now — chi reuses nethttp's directly (same
	// general-purpose Fn shape both packages recognize).
	obsFn := nethttp.Observability(obs)
	opts := nethttp.Options{}
	chiOpts := chiadapter.Options{}

	// Two builders: bHTTP feeds nethttp.AttachMux/mux, bChi feeds
	// chiadapter.AttachRouter/chi router. AttachMux/AttachRouter walks EVERY
	// SSE route registered into the builder it's given, so a route destined
	// for chi must live in a builder never passed to nethttp.AttachMux (and
	// vice versa) — otherwise it would get wired onto both.
	bHTTP := rest.NewServer(rest.Info{Title: "SSE Demo API (net/http)", Version: "1.0.0"})
	bChi := rest.NewServer(rest.Info{Title: "SSE Demo API (chi)", Version: "1.0.0"})

	// Every route is captured as a plain rest.SSERoute VALUE (not just
	// registered inline) — this SAME value is reused below by
	// nethttp.Consume for the client side, demonstrating the full
	// roundtrip: one declared route, one server registration, one client
	// consumption, no separate client-side declaration needed.
	counterRoute := rest.NewSSERoute[struct{}, counterEvent]("/sse/counter",
		codex.Empty, counterEventCodec,
		rest.RouteMeta{OperationID: "streamCounter", Summary: "Stream a counter"},
	).WithHandler(handleCounter).HandleMW(nil, obsFn).WithOptions(opts)
	if err := counterRoute.Register(bHTTP); err != nil {
		log.Fatalf("NewSSERoute counter: %v", err)
	}

	sensorPathReqCodec := codex.Struct[sensorPathReq](
		codex.OptionalField("id", codex.String(), func(r sensorPathReq) string { return r.ID }, func(r *sensorPathReq, v string) { r.ID = v }),
	)
	sensorRoute := rest.NewSSERoute[sensorPathReq, sensorReading]("/sse/sensor/{id}",
		sensorPathReqCodec, sensorReadingCodec,
		rest.RouteMeta{
			OperationID: "streamSensor",
			Summary:     "Stream sensor readings",
		},
		rest.NewPathParam("id", sensorIDCodec,
			func(r sensorPathReq) string { return r.ID },
			func(r *sensorPathReq, v string) { r.ID = v },
		).WithDescription("Sensor ID (<word>-<word>)"),
	).WithHandler(handleSensor).HandleMW(nil, obsFn).WithOptions(chiOpts)
	sensorHandle, err := sensorRoute.RegisterHandle(bChi)
	if err != nil {
		log.Fatalf("NewSSERoute sensor: %v", err)
	}

	invalidRoute := rest.NewSSERoute[struct{}, sensorReading]("/sse/invalid",
		codex.Empty, sensorReadingCodec,
		rest.RouteMeta{OperationID: "streamInvalid", Summary: "Codec rejection demo"},
	).WithHandler(handleInvalid).HandleMW(nil, obsFn).WithOptions(opts)
	if err := invalidRoute.Register(bHTTP); err != nil {
		log.Fatalf("NewSSERoute invalid: %v", err)
	}

	traceCodec := codex.String().Refine(validate.NonEmptyString)
	withHeadersRoute := rest.NewSSERoute[struct{}, counterEvent]("/sse/with-headers",
		codex.Empty, counterEventCodec,
		rest.RouteMeta{OperationID: "streamWithHeaders", Summary: "Stream with custom response header"},
		rest.ResponseHeaderParam{Name: "X-Trace-Id", Description: "Distributed trace ID"}.WithCodec(traceCodec),
	).WithHandler(handleWithHeaders).HandleMW(nil, obsFn).WithOptions(opts)
	if err := withHeadersRoute.Register(bHTTP); err != nil {
		log.Fatalf("NewSSERoute with-headers: %v", err)
	}

	// Both routes below now declare a Req-side merge-capable NewPathParam
	// (sensorPathReq, same type sensorRoute uses) — this is what lets the
	// CLIENT side auto-derive "/sse/merge/room-42"/"/sse/manual/room-42"
	// from a plain sensorPathReq{ID: "room-42"} value below. This is
	// INDEPENDENT of, and additional to, mergedRoute's EXISTING
	// NewRequiredSSEEventParam (which is a SERVER-side concern: writing
	// the path value INTO the outgoing Event.Meta.SensorID automatically
	// — see handleSensorMergedConvenience, which never reads req at all).
	// A non-default wire format (YAML block style, which embeds raw
	// newlines) — exercises the per-line "data:" framing fix in
	// writeSSEData (adapters/nethttp/serve_sse.go): each line of the
	// multi-line YAML payload gets its OWN "data:" prefix, correctly
	// reassembled on the CLIENT side. rest.Formats declared INLINE here
	// applies identically to BOTH construction paths — RegisterHandle
	// (server, below) AND ClientHandle (client, used internally by
	// nethttp.Consume further down) — so a plain nethttp.Consume call
	// against this route picks up YAML automatically, no separate
	// client-side handle/WithFormats call needed.
	mergedRoute := rest.NewSSERoute[sensorPathReq, readingEvent]("/sse/merge/{id}",
		sensorPathReqCodec, readingEventCodec,
		rest.RouteMeta{OperationID: "streamMerged", Summary: "One-struct SSE merge convenience"},
		rest.NewPathParam("id", sensorIDCodec,
			func(r sensorPathReq) string { return r.ID },
			func(r *sensorPathReq, v string) { r.ID = v },
		).WithDescription("Sensor ID (<word>-<word>)"),
		rest.NewRequiredSSEEventParam("id", codex.String(), func(e readingEvent) string { return e.Meta.SensorID }, func(e *readingEvent, v string) { e.Meta.SensorID = v }),
		rest.Formats(format.YAML(readingEventCodec)),
	).WithHandler(handleSensorMergedConvenience).HandleMW(nil, obsFn).WithOptions(opts)
	if err := mergedRoute.Register(bHTTP); err != nil {
		log.Fatalf("NewSSERoute merge: %v", err)
	}

	manualRoute := rest.NewSSERoute[sensorPathReq, readingEvent]("/sse/manual/{id}",
		sensorPathReqCodec, readingEventCodec,
		rest.RouteMeta{OperationID: "streamManual", Summary: "SSE merge escape hatch"},
		rest.NewPathParam("id", sensorIDCodec,
			func(r sensorPathReq) string { return r.ID },
			func(r *sensorPathReq, v string) { r.ID = v },
		).WithDescription("Sensor ID (<word>-<word>)"),
		rest.Formats(format.YAML(readingEventCodec)),
	).WithHandler(handleSensorManualEscape).HandleMW(nil, obsFn).WithOptions(opts)
	if err := manualRoute.Register(bHTTP); err != nil {
		log.Fatalf("NewSSERoute manual: %v", err)
	}

	// ── BuildPath codec validation ─────────────────────────────────────────
	fmt.Println("=== BuildPath with sensorIDCodec ===")
	if path, err := sensorHandle.BuildPath(map[string]string{"id": "room-42"}); err != nil {
		log.Fatalf("BuildPath room-42: %v", err)
	} else {
		fmt.Printf("  valid:   %s\n", path)
	}
	if _, err := sensorHandle.BuildPath(map[string]string{"id": "invalid"}); err != nil {
		fmt.Printf("  invalid: BuildPath rejected (expected): %v\n\n", err)
	}

	// ── Wiring ────────────────────────────────────────────────────────────
	//
	// AttachMux/AttachRouter wire BOTH plain and SSE routes internally, so a
	// single AttachMux/AttachRouter+Serve(ctx) call per builder replaces the
	// old separate ServeSSE-only calls — bHTTP/bChi here happen to declare
	// only SSE routes, so the plain-route half of each Serve(ctx) call
	// simply wires nothing.
	mux := http.NewServeMux()
	httpAddr := mustFreeAddr()
	mustServe(nethttp.AttachMux(bHTTP, mux, httpAddr), "nethttp.AttachMux")
	httpCtx, httpCancel := context.WithCancel(context.Background())
	go func() { _ = bHTTP.Serve(httpCtx) }()
	defer httpCancel()
	waitForReady(httpAddr)

	r := gochi.NewRouter()
	chiAddr := mustFreeAddr()
	mustServe(chiadapter.AttachRouter(bChi, r, chiAddr), "chiadapter.AttachRouter")
	chiCtx, chiCancel := context.WithCancel(context.Background())
	go func() { _ = bChi.Serve(chiCtx) }()
	defer chiCancel()
	waitForReady(chiAddr)
	mux.Handle("/sse/sensor/", r)

	srv := httptest.NewServer(mux)
	defer srv.Close()

	// ── Client-side consumption via nethttp.Consumer/nethttp.Consume ───────
	//
	// This is the FULL ROUNDTRIP this example demonstrates: every route
	// above is declared ONCE (rest.NewSSERoute) and reused for BOTH roles
	// — .Register/.RegisterHandle wires the SERVER side (above), and the
	// SAME route value is passed directly to nethttp.Consume for the
	// CLIENT side below, no separate client-side declaration needed.
	consumer := nethttp.NewConsumer(srv.Client(), srv.URL)

	// consumeN runs a bounded Consume call: cancels its own context after
	// collecting `want` events (or a short timeout), for a deterministic,
	// finite demo run against a long-lived stream.
	consumeN := func(label string, want int, run func(ctx context.Context, cancel context.CancelFunc) error) {
		fmt.Printf("=== %s ===\n", label)
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if err := run(ctx, cancel); err != nil {
			fmt.Printf("  Consume error: %v\n", err)
		}
		fmt.Println()
	}

	// ── Counter stream ─────────────────────────────────────────────────────
	consumeN("Consume /sse/counter (3 events)", 3, func(ctx context.Context, cancel context.CancelFunc) error {
		n := 0
		return nethttp.Consume(ctx, consumer, counterRoute, struct{}{},
			func(_ context.Context, e counterEvent) error {
				fmt.Printf("  event: {count:%d}\n", e.Count)
				n++
				if n >= 3 {
					cancel()
				}
				return nil
			}, nethttp.ConsumeOptions{})
	})

	// ── Sensor stream with path param codec (served by chi) ────────────────
	consumeN("Consume /sse/sensor/room-42 (chi, {id} codec-validated)", 2, func(ctx context.Context, cancel context.CancelFunc) error {
		n := 0
		return nethttp.Consume(ctx, consumer, sensorRoute, sensorPathReq{ID: "room-42"},
			func(_ context.Context, e sensorReading) error {
				fmt.Printf("  event: {sensor:%s temp:%.1f%s}\n", e.SensorID, e.Temperature, e.Unit)
				n++
				if n >= 2 {
					cancel()
				}
				return nil
			}, nethttp.ConsumeOptions{})
	})

	// ── Invalid event demo ─────────────────────────────────────────────────
	consumeN("Consume /sse/invalid (middle event rejected by codec)", 2, func(ctx context.Context, cancel context.CancelFunc) error {
		n, parseErrs := 0, 0
		err := nethttp.Consume(ctx, consumer, invalidRoute, struct{}{},
			func(_ context.Context, e sensorReading) error {
				fmt.Printf("  event: {sensor:%s temp:%.1f%s}\n", e.SensorID, e.Temperature, e.Unit)
				n++
				if n >= 2 {
					cancel()
				}
				return nil
			}, nethttp.ConsumeOptions{
				OnError: func(err error) {
					var parseErr nethttp.SSEParseError
					if errors.As(err, &parseErr) {
						parseErrs++
					}
				},
			})
		fmt.Printf("  received %d valid events, %d rejected by codec\n", n, parseErrs)
		return err
	})

	// ── ResponseHeaderParam: custom header committed on first send ─────────
	// Consume derives Req-side path/query/header/cookie values, but a
	// RESPONSE header (like X-Trace-Id here) is read from the raw HTTP
	// response — outside Consume's per-event Event value entirely, same
	// asymmetry REST's own response-header merge has (response headers
	// merge into Resp for request/response Call, but SSE has no single
	// Resp to merge into). CallSSEAdapter's port-based form doesn't
	// expose the raw response either — reading a response header from an
	// SSE connection remains a case-by-case concern for now, so this demo
	// simply shows the events; the header itself was validated in the
	// server-side handler already (see handleWithHeaders).
	consumeN("Consume /sse/with-headers (X-Trace-Id committed server-side on first send)", 1, func(ctx context.Context, cancel context.CancelFunc) error {
		return nethttp.Consume(ctx, consumer, withHeadersRoute, struct{}{},
			func(_ context.Context, e counterEvent) error {
				fmt.Printf("  event: {count:%d}\n", e.Count)
				cancel()
				return nil
			}, nethttp.ConsumeOptions{})
	})

	// ── Side-by-side: one-struct merge vs manual escape hatch (YAML) ───────
	// Both routes declare rest.Formats(format.YAML(...)) INLINE above —
	// nethttp.Consume (the route-value convenience, not a pre-built
	// handle) picks up that declared format automatically, since its
	// internally-derived ClientHandle() now applies the SAME rb.respFormats
	// registerHandle applies server-side (a previously-fixed bug: an
	// earlier version required building a client handle by hand and
	// calling WithFormats(YAML) on it, then using CallSSEAdapter instead
	// of Consume — no longer necessary).
	fmt.Println("=== Side-by-side: one-struct merge vs manual escape hatch (YAML, via Consume) ===")
	consumeViaConsume := func(label string, sseRoute rest.SSERoute[sensorPathReq, readingEvent]) {
		ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
		defer cancel()
		n := 0
		err := nethttp.Consume(ctx, consumer, sseRoute, sensorPathReq{ID: "room-42"},
			func(_ context.Context, e readingEvent) error {
				fmt.Printf("  %s event: {sensor:%s temp:%.1f%s}\n", label, e.Meta.SensorID, e.Payload.Temperature, e.Payload.Unit)
				n++
				if n >= 2 {
					cancel() // both events collected — stop before a reconnect attempt
				}
				return nil
			}, nethttp.ConsumeOptions{})
		if err != nil {
			fmt.Printf("  %s Consume error: %v\n", label, err)
		}
	}
	consumeViaConsume("merge", mergedRoute)
	consumeViaConsume("manual", manualRoute)
	fmt.Println("  both routes deliver same payload; merge route removes manual path-value stitching")
	fmt.Println()

	// ── CallSSEAdapter: the ports.SourcePort counterpart of Consume ────────
	// Same reconnect loop Consume uses, exposed as a ports.SourceAdapter
	// for pipeline/port-based consumers instead of a direct callback.
	// Takes a pre-built *SSERouteHandle directly (here, a plain
	// ClientHandle() with the route's default JSON format — no override
	// needed for this route).
	fmt.Println("=== CallSSEAdapter /sse/counter (ports.SourcePort, 2 events) ===")
	counterHandle := counterRoute.ClientHandle()
	p, err := ports.NewSourcePort[counterEvent]("counterEvents", counterEventCodec, ports.PortOptions{Buffer: 4})
	if err != nil {
		log.Fatalf("NewSourcePort counterEvents: %v", err)
	}
	adapterCtx, adapterCancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
	defer adapterCancel()
	p.Bind(adapterCtx, nethttp.CallSSEAdapter(srv.Client(), srv.URL, counterHandle, struct{}{}, nethttp.ConsumeOptions{}))
	stream := p.Stream(adapterCtx)
	for i := 0; i < 2; i++ {
		select {
		case e := <-stream.Values:
			fmt.Printf("  event: {count:%d}\n", e.Count)
		case <-time.After(1 * time.Second):
		}
	}
	adapterCancel()
	fmt.Println()

	// ── Stats summary ──────────────────────────────────────────────────────
	fmt.Println("=== Observer summary ===")
	metrics.Print()
	fmt.Println()

	// ── OpenAPI spec ───────────────────────────────────────────────────────
	// bHTTP and bChi each generate their own spec — the SSE routes each
	// builder holds appear in that builder's document regardless of
	// which adapter ultimately serves them.
	printSpec := func(label string, b *rest.Server) {
		fmt.Printf("=== OpenAPI 3.1 spec: %s (SSE routes included) ===\n", label)
		doc, err := b.OpenAPISpec()
		if err != nil {
			fmt.Fprintf(os.Stderr, "OpenAPISpec error: %v\n", err)
			os.Exit(1)
		}
		yaml, err := doc.MarshalYAML()
		if err != nil {
			fmt.Fprintf(os.Stderr, "MarshalYAML error: %v\n", err)
			os.Exit(1)
		}
		fmt.Print(string(yaml))
		fmt.Println()
	}
	printSpec("net/http routes", bHTTP)
	printSpec("chi routes", bChi)
}
