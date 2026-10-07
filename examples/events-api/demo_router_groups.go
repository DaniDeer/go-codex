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
