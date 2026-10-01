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
