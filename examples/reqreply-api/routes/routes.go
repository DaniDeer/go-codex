// Package routes declares every reqreply.Route used by this example — pure
// spec values, no handler/transport attached yet (that happens in
// mqtt5server/zeromqserver/zeromqrouterserver via route.WithHandler(fn).
// Register(server), mirroring examples/rest-api/routes' identical
// separation of concerns).
package routes

import (
	"github.com/DaniDeer/go-codex/api/reqreply"
	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/route"
	"github.com/DaniDeer/go-codex/validate"
)

// ComputeReq/ComputeResp are the shared request/response types for every
// compute route declared below.
type ComputeReq struct {
	X int
	Y int
}

type ComputeResp struct {
	Sum int
}

var ComputeReqCodec = codex.Struct[ComputeReq](
	codex.RequiredField("x", codex.Int(),
		func(r ComputeReq) int { return r.X },
		func(r *ComputeReq, v int) { r.X = v },
	),
	codex.RequiredField("y", codex.Int(),
		func(r ComputeReq) int { return r.Y },
		func(r *ComputeReq, v int) { r.Y = v },
	),
)

var ComputeRespCodec = codex.Struct[ComputeResp](
	codex.RequiredField("sum", codex.Int(),
		func(r ComputeResp) int { return r.Sum },
		func(r *ComputeResp, v int) { r.Sum = v },
	),
)

// ComputeRoute is the baseline, unsecured request-reply contract used by
// Demo 1 (basic call+serve), Demo 2 (dual-mode Call), and Demo 6 (spec
// printing).
var ComputeRoute = reqreply.NewRoute[ComputeReq, ComputeResp](
	"compute/add",
	ComputeReqCodec, ComputeRespCodec,
	reqreply.RouteMeta{OperationID: "computeAdd", Summary: "Add two integers."},
)

// DoubleRoute and TripleRoute exist ALONGSIDE ComputeRoute purely to give
// Demo 4 (concurrent multi-route dispatch) 3+ distinct routes registered
// against ONE Server, each independently answering calls concurrently.
var DoubleRoute = reqreply.NewRoute[ComputeReq, ComputeResp](
	"compute/double",
	ComputeReqCodec, ComputeRespCodec,
	reqreply.RouteMeta{OperationID: "computeDouble", Summary: "Double the sum of two integers."},
)

var TripleRoute = reqreply.NewRoute[ComputeReq, ComputeResp](
	"compute/triple",
	ComputeReqCodec, ComputeRespCodec,
	reqreply.RouteMeta{OperationID: "computeTriple", Summary: "Triple the sum of two integers."},
)

// BearerAuth is declared ONCE and referenced by SecuredComputeRoute below —
// the SAME declaration is consumed identically by server (Serve) and client
// (Call), mirroring rest.WithSecurityScheme.
var BearerAuth = reqreply.SecurityScheme{SecurityScheme: route.BearerScheme("JWT")}.
	WithCodec(codex.String().Refine(validate.NonEmptyString))

// SecuredComputeRoute declares its OWN Security (not relying on
// Server.AddGlobalSecurity) — used by Demo 3 (route-level security
// credential error) and Demo 2 (global-security dual-mode contrast).
var SecuredComputeRoute = reqreply.NewRoute[ComputeReq, ComputeResp](
	"compute/secured-add",
	ComputeReqCodec, ComputeRespCodec,
	reqreply.RouteMeta{
		OperationID: "securedComputeAdd",
		Summary:     "Add two integers — requires a bearer token.",
		Security:    []route.SecurityRequirement{route.Require("bearerAuth")},
	},
	reqreply.WithSecurityScheme("bearerAuth", BearerAuth),
)

// GlobalOnlyComputeRoute declares NO route-level RouteMeta.Security (left
// nil — "inherit global security") and only ever becomes secured via
// Server.AddGlobalSecurity, letting Demo 2 show the confirmed dual-mode
// Client.Call difference: a raw Route never sees the Server's global
// security (identical to REST's own accepted limitation), while an
// already-registered *RouteHandle does. It STILL declares WithSecurityScheme
// (the ONLY source of RouteHandle.SecuritySchemes, per that option's own
// doc comment) so the bearerAuth credential format is actually enforced
// once GlobalSecurity kicks in — RouteMeta.Security and WithSecurityScheme
// are deliberately independent declarations.
var GlobalOnlyComputeRoute = reqreply.NewRoute[ComputeReq, ComputeResp](
	"compute/global-secured-add",
	ComputeReqCodec, ComputeRespCodec,
	reqreply.RouteMeta{OperationID: "globalSecuredComputeAdd", Summary: "Add two integers — secured only via Server.AddGlobalSecurity."},
	reqreply.WithSecurityScheme("bearerAuth", BearerAuth),
)

// RouterComputeRoute is dispatched over a ZMQ ROUTER/DEALER socket pair in
// Demo 7. MissingComputeRoute is registered on the SAME server but
// DELIBERATELY has no corresponding socket wired in
// zeromqrouterserver.Build, to surface the typed MissingSocketError.
var RouterComputeRoute = reqreply.NewRoute[ComputeReq, ComputeResp](
	"compute/router-add",
	ComputeReqCodec, ComputeRespCodec,
	reqreply.RouteMeta{OperationID: "routerComputeAdd", Summary: "Add two integers over a ZMQ ROUTER/DEALER socket pair."},
)

// MissingSocketRoute is registered on the SAME router server as
// RouterComputeRoute but Demo 7 deliberately never wires a matching socket
// for it — demonstrating AttachRouterServer's upfront MissingSocketError.
var MissingSocketRoute = reqreply.NewRoute[ComputeReq, ComputeResp](
	"compute/router-add-missing",
	ComputeReqCodec, ComputeRespCodec,
	reqreply.RouteMeta{OperationID: "routerComputeAddMissing", Summary: "Deliberately has no matching socket — demonstrates MissingSocketError."},
)
