package main

import (
	"context"
	"fmt"

	"github.com/DaniDeer/go-codex/api/events"
	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/middleware"
)

// ── fixtures for demoEventsRouterGroups — self-contained, no dependency
// on the routes/handlers/mqtt5broker/zeromqbroker/client sub-packages the
// rest of this project uses, since Router's own value is in TOPIC/
// MIDDLEWARE ASSEMBLY, independent of any live transport. ────────────────

type routerDemoSensorReading struct {
	SensorID string
	Value    float64
}

var routerDemoSensorReadingCodec = codex.Struct[routerDemoSensorReading](
	codex.RequiredField("sensor_id", codex.String(),
		func(r routerDemoSensorReading) string { return r.SensorID },
		func(r *routerDemoSensorReading, v string) { r.SensorID = v },
	),
	codex.RequiredField("value", codex.Float64(),
		func(r routerDemoSensorReading) float64 { return r.Value },
		func(r *routerDemoSensorReading, v float64) { r.Value = v },
	),
)

func routerDemoOnReading(_ context.Context, r routerDemoSensorReading) error {
	fmt.Printf("  received reading: sensor=%s value=%.1f\n", r.SensorID, r.Value)
	return nil
}

// demoEventsRouterGroups shows docs/roadmap/declarative-router-groups.md's
// Phase B (api/events) end to end:
//   - channels declared completely independently (exactly as every OTHER
//     demo in this project declares them) — Router never changes the
//     codec-declaration step itself;
//   - a nested Router assembles a topic prefix ("sensors") + a sub-group
//     ("readings") with group-wide Security middleware attached via ONE
//     `.Use(...)` call, instead of repeating it per-channel;
//   - `Routes()` prints the FINAL, holistic, pre-registration view of how
//     the whole API assembles — every topic, role, and the middleware
//     that will dispatch for it — before a single byte is registered;
//   - events' topics never get a forced leading "/" (unlike REST's
//     paths) — MQTT/ZeroMQ-style topics don't use one.
func demoEventsRouterGroups() {
	fmt.Println("=== Router groups: declare independently, assemble with ONE Router ===")

	// Each channel declares only its OWN, relative topic — "readings"
	// (the group's own prefix, contributed once below) is NOT repeated
	// here; this is exactly the "declared independently, prefix
	// assembled later by the Router" workflow the whole feature exists
	// for.
	temperatureSub := events.NewChannel[routerDemoSensorReading]("temperature", routerDemoSensorReadingCodec,
		events.ChannelMeta{Description: "Temperature readings"},
	).WithSubscribe(events.Subscribe{Summary: "Receive temperature readings"}).
		WithHandler(routerDemoOnReading)

	humiditySub := events.NewChannel[routerDemoSensorReading]("humidity", routerDemoSensorReadingCodec,
		events.ChannelMeta{Description: "Humidity readings"},
	).WithSubscribe(events.Subscribe{Summary: "Receive humidity readings"}).
		WithHandler(routerDemoOnReading)

	alertPub := events.NewChannel[routerDemoSensorReading]("alerts", routerDemoSensorReadingCodec,
		events.ChannelMeta{Description: "Out-of-range alerts"},
	).WithPublish(events.Publish{Summary: "Publish out-of-range alerts"})

	// A reusable-class, spec-only middleware, attached ONCE at the Router
	// level — every leaf grouped under it (temperature/humidity/alerts)
	// gets it, with ZERO repetition across 3 separate .Use() calls. (A
	// Security-carrying middleware would ALSO need a satisfying
	// [Subscriber.SubscribeMW] implementation attached per-leaf to pass
	// [events.CheckCoverage] — Router-contributed middleware only ever
	// reaches each leaf's spec-level mws/middlewareHandlers, never its
	// impls list; see [api/events.Router]'s doc comment.)
	auditMiddleware := middleware.Middleware{Name: "audit-log"}

	readingsRouter := events.NewRouter("readings").
		Use(auditMiddleware).
		Route(temperatureSub).
		Route(humiditySub).
		Route(alertPub)

	sensorsRouter := events.NewRouter("sensors").Mount(readingsRouter)

	fmt.Println("-- Routes() — holistic, pre-registration view --")
	for _, e := range sensorsRouter.Routes() {
		fmt.Printf("  %-10s %-28s middleware=%v\n", e.Role, e.Path, e.MiddlewareNames)
	}

	client := events.NewClient(events.WithInfo(events.Info{Title: "Router Groups Demo", Version: "1.0.0"}))
	if err := sensorsRouter.Register(client); err != nil {
		fmt.Printf("  register error: %v\n", err)
		return
	}
	fmt.Println("-- Registered successfully; every leaf above is now live on client --")
	fmt.Println()
}

// demoSensorsStaticPrefixGroup demonstrates a NEW-GROUND finding from
// this project's own router-review round: a Router prefix must be
// STATIC (docs/design/d-0008-declarative-router-groups.md — no `{var}`
// placeholders in the Router's OWN prefix string), but that constraint
// is ONLY about the Router's own prefix — NOT about every leaf grouped
// under it. A leaf is free to declare its OWN relative topic containing
// a "{var}" segment (e.g. "{sensorID}/measurements") — Router composes
// prefix+leaf with a plain string join, with ZERO awareness of (or
// interference with) any "{var}" placeholder inside the leaf's OWN
// topic. This has NEVER been exercised anywhere in this codebase before.
//
// NOTE: routes.MeasurementChannel/WildcardChannel/ReadingsWithErrorsChannel/
// etc. (this project's REAL "sensors/{sensorID}/..." family) each embed
// the "sensors/" segment directly in their OWN declared topic (NOT
// relative to any Router) — grouping those AS-IS under a NEW
// events.NewRouter("sensors") would DOUBLE the prefix
// ("sensors/sensors/..."). Proving THIS demo's point without that bug,
// or without the invasive topic-string rename those real channels would
// need (explicitly out of scope — large, pre-existing blast radius
// across mqttbroker/demo_error_pattern.go/demo_wildcard_subscription.go),
// requires FRESH, LOCALLY-DECLARED fixture leaves with a genuinely
// RELATIVE, "{sensorID}"-containing topic — mirroring
// demoEventsRouterGroups' own established synthetic-fixture precedent
// above, extended here with a VARIABLE (not just static) leaf topic.
func demoSensorsStaticPrefixGroup() {
	fmt.Println("=== Router: static \"sensors\" prefix Group over a variable-suffix {sensorID} leaf ===")

	measurementSub := events.NewChannel[routerDemoSensorReading]("{sensorID}/measurements", routerDemoSensorReadingCodec,
		events.TopicParam{Name: "sensorID"},
		events.ChannelMeta{Description: "Per-sensor measurements (variable-suffix leaf topic)."},
	).WithSubscribe(events.Subscribe{Summary: "Receive a per-sensor measurement"}).
		WithHandler(routerDemoOnReading)

	alertsSub := events.NewChannel[routerDemoSensorReading]("{sensorID}/alerts", routerDemoSensorReadingCodec,
		events.TopicParam{Name: "sensorID"},
		events.ChannelMeta{Description: "Per-sensor alerts (variable-suffix leaf topic)."},
	).WithSubscribe(events.Subscribe{Summary: "Receive a per-sensor alert"}).
		WithHandler(routerDemoOnReading)

	auditMiddleware := middleware.Middleware{Name: "audit-log"}

	sensorsRouter := events.NewRouter("sensors").
		Use(auditMiddleware).
		Tags("variable-suffix-demo").
		Route(measurementSub).
		Route(alertsSub)

	fmt.Println("-- Routes() — holistic, pre-registration view (each leaf's OWN {sensorID} segment composes untouched) --")
	for _, e := range sensorsRouter.Routes() {
		fmt.Printf("  %-10s %-32s middleware=%v tags=%v\n", e.Role, e.Path, e.MiddlewareNames, e.Tags)
	}

	client := events.NewClient(events.WithInfo(events.Info{Title: "Sensors Static-Prefix Group Demo", Version: "1.0.0"}))
	if err := sensorsRouter.Register(client); err != nil {
		fmt.Printf("  register error: %v\n", err)
		return
	}
	fmt.Println("-- Registered successfully — a STATIC Router prefix composes cleanly over a variable-suffix leaf --")
	fmt.Println()
}
