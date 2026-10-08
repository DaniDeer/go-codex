package rest

import (
	"context"
	"fmt"
)

// CallWithTransport is the API-LAYER-OWNED "attach and drive" verb for
// calling exactly ONE route against an ALREADY-BUILT [ClientTransport] —
// mirrors [events.PublishHandle]/[reqreply.CallWithTransport]'s shape
// exactly (docs/design/d-0006-protocol-native-capabilities.md's Phase
// 5a). It replaces the former adapter-owned
// `nethttp.CallWithHandle[Req,Resp]`, which built a SEPARATE, hand-
// written encode/dispatch pipeline rather than delegating through this
// package's own [ClientTransport] interface — CallWithTransport closes
// that gap: [ClientCallOptions] was grown (Phase 5a) to carry every
// field the deleted escape hatch's own richer options type had, so this
// is now a lossless replacement, not merely a smaller convenience.
//
// Build transport via an adapter's own `New*Transport(opts) ClientTransport`
// factory (e.g. [nethttp.NewClientTransport]) — the SAME factory
// [Client.Attach] consumes — then call CallWithTransport directly,
// WITHOUT constructing a [*Client] at all, for a caller who has ONLY a
// *[RouteHandle] (e.g. [adapters/mcprest], bridging a REST route into a
// different protocol) or wants zero [*Client]/spec registration
// ceremony for a single route.
//
//	transport := nethttp.NewClientTransport(nethttp.ClientTransportOptions{HTTPClient: client, BaseURL: baseURL})
//	resp, err := rest.CallWithTransport(ctx, transport, getUserHandle, GetUserReq{ID: "f47ac10b"})
func CallWithTransport[Req, Resp any](
	ctx context.Context,
	transport ClientTransport,
	handle *RouteHandle[Req, Resp],
	req Req,
	opts ...ClientCallOptions,
) (Resp, error) {
	var zero Resp
	// Tier 3a — mirrors each server adapter's buildRouteHandler's
	// identical check (docs/design/d-0006-protocol-native-capabilities.md's
	// Phase 3): a [CapabilityRequirement] declared on a [Route] must be
	// verified client-side too, not just server-side — a [Route] used
	// ONLY via a client (e.g. calling a remote REST API never served by
	// this codebase) previously got ZERO enforcement at all, unlike
	// reqreply/mqtt5/zeromq's symmetric client-side check. No REST
	// adapter supplies any concrete Capability today, so this currently
	// only ever fires for a mistakenly-declared requirement — exactly
	// the intended "fail fast" outcome, not a behavior change for any
	// route that declares none.
	routeLabel := handle.Descriptor.Method + " " + handle.Descriptor.Path
	if err := VerifyCapabilityCoverage[CapabilityName](routeLabel, handle.Requirements, nil); err != nil {
		return zero, err
	}
	respAny, err := transport.Call(ctx, handle, req, opts...)
	if err != nil {
		return zero, err
	}
	resp, ok := respAny.(Resp)
	if !ok {
		return zero, TransportTypeMismatchError{Path: handle.Descriptor.Path, Want: fmt.Sprintf("%T", zero), Got: fmt.Sprintf("%T", respAny)}
	}
	return resp, nil
}
