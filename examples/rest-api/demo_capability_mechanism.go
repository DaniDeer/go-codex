package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"

	"github.com/DaniDeer/go-codex/adapters/nethttp"
	"github.com/DaniDeer/go-codex/api/rest"
	"github.com/DaniDeer/go-codex/codex"
)

// demoCapabilityMechanism demonstrates Phase 3 of docs/roadmap/
// capability-requirement-composition.md: `api/rest` gains the SAME
// protocol-native Capability mechanism `api/events`/`api/reqreply`
// already ship, generalized across THREE layers:
//
//   - Tier 2 (Implicit): rest.HeaderParam/CookieParam/QueryParam become
//     GENUINELY runtime-checked capabilities once REST has more than one
//     transport family. nethttp/chi implement all 3 marker interfaces
//     trivially (HTTP structurally always supports headers/cookies/
//     query) — so a route declaring any of them serves with ZERO
//     behavior change.
//   - Tier 3a (Explicit, Sealed): rest.RequireQoS/RequireHWM declare a
//     standalone protocol-native requirement, independent of any
//     adapter. As of this writing, NO shipped REST adapter supplies a
//     concrete QoS/HWM value — HTTP has no such concept — so a route
//     declaring RequireQoS is CORRECTLY, EAGERLY rejected at Serve time
//     when attached to nethttp/chi, exactly the intended "this adapter
//     doesn't support this capability" outcome (mirrors MQTT5 never
//     supporting AMQP's exchange/queue capability). A future non-HTTP
//     REST-eligible transport (see docs/roadmap/zeromq-rest-adapter.md,
//     tracked as an independent future effort) would be the first to
//     satisfy it.
//   - The declared requirement renders into the OpenAPI spec's
//     "x-codex-capabilities" vendor extension (mirrors AsyncAPI's own
//     "x-capabilities" for events/reqreply) — see demoSpecEndpoint's
//     printed spec for this route's own entry.
func demoCapabilityMechanism() {
	fmt.Println("=== Demo: protocol-native Capability mechanism reaches api/rest (Phase 3) ===")

	// Tier 2: a route declaring Header/Query params serves with ZERO
	// behavior change — nethttp's httpCarrier satisfies all 3 Tier 2
	// interfaces trivially.
	tier2Route := rest.NewRoute[capabilityDemoReq, capabilityDemoResp]("GET", "/capability-demo/echo",
		capabilityDemoReqCodec, capabilityDemoRespCodec,
		rest.RouteMeta{OperationID: "capabilityDemoEcho"},
		rest.HeaderParam{Name: "X-Trace-Id"},
		rest.QueryParam{Name: "loud"},
	).WithHandler(func(_ context.Context, req capabilityDemoReq) (capabilityDemoResp, error) {
		return capabilityDemoResp{Echo: req.Message}, nil
	})
	handler, err := nethttp.ServeOne(tier2Route)
	if err != nil {
		fmt.Printf("  [error] Tier 2 ServeOne: %v\n", err)
		return
	}
	srv := httptest.NewServer(handler)
	defer srv.Close()
	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/capability-demo/echo?loud=true", strings.NewReader("")) //nolint:noctx
	req.Header.Set("X-Trace-Id", "trace-123")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		fmt.Printf("  [error] Tier 2 request: %v\n", err)
		return
	}
	defer resp.Body.Close()
	fmt.Printf("  ✓ Tier 2 (HeaderParam+QueryParam): Status=%s — nethttp's marker interfaces satisfy the declared requirement, zero behavior change\n", resp.Status)

	// Tier 3a: a route declaring RequireQoS is CORRECTLY, EAGERLY
	// rejected at Serve time — nethttp has no QoS Capability value to
	// supply.
	tier3aRoute := rest.NewRoute[capabilityDemoReq, capabilityDemoResp]("GET", "/capability-demo/qos",
		capabilityDemoReqCodec, capabilityDemoRespCodec,
		rest.RouteMeta{OperationID: "capabilityDemoQoS"},
		rest.RequireQoS(rest.AtLeastOnce),
	).WithHandler(func(_ context.Context, req capabilityDemoReq) (capabilityDemoResp, error) {
		return capabilityDemoResp{Echo: req.Message}, nil
	})
	_, err = nethttp.ServeOne(tier3aRoute)
	var cce *rest.CapabilityCoverageError
	if errors.As(err, &cce) {
		fmt.Printf("  ✓ Tier 3a (RequireQoS): correctly rejected — %v\n", cce)
	} else {
		fmt.Printf("  [error] expected a *rest.CapabilityCoverageError, got: %v\n", err)
	}

	// The mechanism is value-aware, not just name-matched — demonstrated
	// directly (no adapter can supply a REAL QoS value yet, so this uses
	// a hand-written fake, mirroring events'/reqreply's own
	// demoCapabilityMechanism style before real non-HTTP values existed).
	declared := []rest.CapabilityRequirement{{Name: "QoS", MinLevel: intPtr(int(rest.ExactlyOnce))}}
	insufficientlyLeveled := []any{fakeLeveledQoS{level: int(rest.AtMostOnce)}}
	if covErr := rest.CheckCapabilityCoverage("GET /capability-demo/qos-strict", declared, insufficientlyLeveled); covErr != nil {
		fmt.Printf("  ✓ CheckCapabilityCoverage correctly rejects an insufficient QoS level: %v\n", covErr)
	} else {
		fmt.Println("  [error] expected an Insufficient-level error, got nil")
	}
	fmt.Println()
}

type capabilityDemoReq struct{ Message string }
type capabilityDemoResp struct{ Echo string }

var capabilityDemoReqCodec = codex.Struct[capabilityDemoReq](
	codex.OptionalField("message", codex.String(),
		func(r capabilityDemoReq) string { return r.Message },
		func(r *capabilityDemoReq, v string) { r.Message = v },
	),
)

var capabilityDemoRespCodec = codex.Struct[capabilityDemoResp](
	codex.RequiredField("echo", codex.String(),
		func(r capabilityDemoResp) string { return r.Echo },
		func(r *capabilityDemoResp, v string) { r.Echo = v },
	),
)

// fakeLeveledQoS simulates a future non-HTTP REST adapter's own sealed
// QoS Capability value implementing rest.LeveledCapability — no real
// adapter supplies one yet (see docs/roadmap/zeromq-rest-adapter.md).
type fakeLeveledQoS struct{ level int }

func (f fakeLeveledQoS) CapabilityName() string { return "QoS" }
func (f fakeLeveledQoS) Level() int             { return f.level }

func intPtr(v int) *int { return &v }
