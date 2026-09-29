package nethttp

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/DaniDeer/go-codex/api/rest"
	"github.com/DaniDeer/go-codex/route"
)

// This file closes docs/roadmap/capability-requirement-composition.md's
// Phase 3/6 verification: nethttp's httpCarrier satisfies
// all 3 Tier 2 interfaces, so a declared HeaderParam/CookieParam/
// QueryParam requirement is ALWAYS covered — proving the mechanism
// coexists with zero behavior change. A declared Tier 3a requirement
// (RequireQoS/RequireHWM) is ALWAYS rejected — nethttp has no matching
// Capability value to supply, exactly the intended "adapter doesn't
// support this capability" outcome.

func TestServe_CapabilityCoverage_HeaderCookieQuery_AlwaysPasses(t *testing.T) {
	b := rest.NewServer(testInfo)
	err := rest.NewRoute[createReq, userResp]("POST", "/users-with-params",
		createReqCodec, userRespCodec, rest.RouteMeta{OperationID: "createUserWithParams"},
		rest.HeaderParam{Name: "X-Trace-Id"},
		rest.CookieParam{Name: "session"},
		rest.QueryParam{Name: "dryRun"},
	).WithHandler(func(ctx context.Context, req createReq) (userResp, error) {
		return userResp{ID: "1", Name: req.Name}, nil
	}).Register(b)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}

	mux := http.NewServeMux()
	if err := serve(mux, b); err != nil {
		t.Fatalf("Serve: %v (want nil — Tier 2 coverage must always pass for nethttp)", err)
	}
}

func TestServe_CapabilityCoverage_RequireQoS_RejectedAtServeTime(t *testing.T) {
	b := rest.NewServer(testInfo)
	err := rest.NewRoute[createReq, userResp]("POST", "/users-with-qos",
		createReqCodec, userRespCodec, rest.RouteMeta{OperationID: "createUserWithQoS"},
		rest.RequireQoS(rest.AtLeastOnce),
	).WithHandler(func(ctx context.Context, req createReq) (userResp, error) {
		return userResp{ID: "1", Name: req.Name}, nil
	}).Register(b)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}

	mux := http.NewServeMux()
	err = serve(mux, b)
	var cce *rest.CapabilityCoverageError
	if !errors.As(err, &cce) {
		t.Fatalf("want *rest.CapabilityCoverageError (nethttp cannot supply QoS), got %v", err)
	}
	if len(cce.Missing) != 1 || cce.Missing[0] != "QoS" {
		t.Errorf("want Missing=[QoS], got %v", cce.Missing)
	}
}

func TestServe_CapabilityCoverage_APIKeyCookieScheme_StillPasses(t *testing.T) {
	// A route declaring a Cookie-based API key scheme (no separate
	// CookieParam) must ALSO pass — RequiredParamKinds scans
	// SecuritySchemes' In field too, and nethttp supports Cookie either
	// way.
	mw := rest.FromSecurityScheme("apiKeyCookie",
		rest.SecurityScheme{SecurityScheme: route.APIKeyScheme("X-Session", "cookie")}, nil)
	b := rest.NewServer(testInfo)
	err := rest.NewRoute[createReq, userResp]("POST", "/users-with-apikey-cookie",
		createReqCodec, userRespCodec, rest.RouteMeta{OperationID: "createUserWithAPIKeyCookie"},
	).Use(mw).WithHandler(func(ctx context.Context, req createReq) (userResp, error) {
		return userResp{ID: "1", Name: req.Name}, nil
	}).HandleMW(&mw, func(ctx context.Context, r *http.Request, req *createReq) (map[string][]string, error) {
		if _, err := r.Cookie("X-Session"); err != nil {
			return nil, err
		}
		return nil, nil
	}).Register(b)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}

	mux := http.NewServeMux()
	if err := serve(mux, b); err != nil {
		t.Fatalf("Serve: %v (want nil — Cookie kind implied by SecurityScheme.In must still pass)", err)
	}
}
