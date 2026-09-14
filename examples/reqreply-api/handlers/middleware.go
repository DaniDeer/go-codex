// Package handlers (this file) holds the IMPLEMENTATION half of
// routes/middleware.go's codec-declared enrichment middleware — mirrors
// security.go/oauth.go's identical declare/implement split, just for a
// non-security concern.
package handlers

import (
	"context"

	"github.com/DaniDeer/go-codex/examples/reqreply-api/routes"
)

// ProcessTenant is routes.TenantPropertyMw's Transform fn — attached via
// reqreply.Transform(routes.PropertyAxisComputeRoute, routes.TenantPropertyMw,
// ProcessTenant) in BOTH mqtt5server.Build and zeromqserver.Build.
//
// Deliberately ADAPTER-AGNOSTIC: unlike VerifyBearer above (whose PAIRED
// security Fn shape is mqtt5-specific, reading *pahomqtt5.Publish
// directly), this fn's signature — func(ctx, req *routes.ComputeReq, in
// routes.TenantIn) (routes.TenantAck, error) — never references any
// adapter type at all. That's what makes registering the SAME
// declaration+implementation pair against two completely different
// transports (mqtt5, zeromq) possible with zero duplication: only the
// WIRING (in {adapter}server/server.go) differs per adapter, never the
// declaration or the implementation.
//
// req is available to read/enrich (unused here — this demo's enrichment
// is entirely in the Middleware's OWN In/Out vocabulary, not the route's
// own ComputeReq/ComputeResp), mirroring how a real enrichment concern
// (e.g. stamping a derived audit field onto the route's own request)
// would use it.
func ProcessTenant(_ context.Context, _ *routes.ComputeReq, in routes.TenantIn) (routes.TenantAck, error) {
	return routes.TenantAck{Ack: "processed-for-" + in.TenantID}, nil
}
