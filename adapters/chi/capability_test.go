package chi

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	gochi "github.com/go-chi/chi/v5"

	"github.com/DaniDeer/go-codex/api/rest"
	"github.com/DaniDeer/go-codex/route"
)

// This file closes docs/design/d-0006-protocol-native-capabilities.md's
// Phase 3 verification for chi — mirrors adapters/nethttp's identical
// capability_test.go exactly.

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

	r := gochi.NewRouter()
	if err := serve(r, b); err != nil {
		t.Fatalf("Serve: %v (want nil — Tier 2 coverage must always pass for chi)", err)
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

	r := gochi.NewRouter()
	err = serve(r, b)
	var cce *rest.CapabilityCoverageError
	if !errors.As(err, &cce) {
		t.Fatalf("want *rest.CapabilityCoverageError (chi cannot supply QoS), got %v", err)
	}
	if len(cce.Missing) != 1 || cce.Missing[0] != "QoS" {
		t.Errorf("want Missing=[QoS], got %v", cce.Missing)
	}
}

// TestServeSSE_CapabilityCoverage_RequireQoS_RejectedAtServeTime mirrors
// TestServe_CapabilityCoverage_RequireQoS_RejectedAtServeTime for SSE
// routes — closes a confirmed gap where [rest.SSERouteHandle] carried no
// Requirements field at all, so a declared [rest.CapabilityRequirement]
// was silently accumulated into the OpenAPI spec but NEVER enforced at
// ServeSSE/Attach time.
func TestServeSSE_CapabilityCoverage_RequireQoS_RejectedAtServeTime(t *testing.T) {
	b := rest.NewServer(testInfo)
	err := rest.NewSSERoute[createReq, sseEvent]("/events-with-qos",
		createReqCodec, sseEventCodec, rest.RouteMeta{OperationID: "streamEventsWithQoS"},
		rest.RequireQoS(rest.AtLeastOnce),
	).WithHandler(func(ctx context.Context, req createReq, send func(sseEvent) error) error {
		return send(sseEvent{Message: "hi"})
	}).Register(b)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}

	r := gochi.NewRouter()
	err = serveSSE(r, b)
	var cce *rest.CapabilityCoverageError
	if !errors.As(err, &cce) {
		t.Fatalf("want *rest.CapabilityCoverageError (chi cannot supply QoS), got %v", err)
	}
	if len(cce.Missing) != 1 || cce.Missing[0] != "QoS" {
		t.Errorf("want Missing=[QoS], got %v", cce.Missing)
	}
}

func TestServe_CapabilityCoverage_APIKeyCookieScheme_StillPasses(t *testing.T) {
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

	r := gochi.NewRouter()
	if err := serve(r, b); err != nil {
		t.Fatalf("Serve: %v (want nil — Cookie kind implied by SecurityScheme.In must still pass)", err)
	}
}

// TestHttpCarrier_ExtractQuery_FirstValueWins confirms httpCarrier.ExtractQuery
// (unlike ExtractQueryMulti) collapses a repeated query key to its first
// value — the documented first-value-wins contract.
func TestHttpCarrier_ExtractQuery_FirstValueWins(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/?dryRun=true&dryRun=false&tenant=acme", nil)
	c := httpCarrier{r}
	got := c.ExtractQuery()
	want := map[string]string{"dryRun": "true", "tenant": "acme"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ExtractQuery() = %v, want %v", got, want)
	}
}

// TestHttpCarrier_ExtractQueryMulti_PreservesAllValues confirms
// ExtractQueryMulti keeps every value for a repeated query key, unlike
// ExtractQuery's first-value-wins collapse.
func TestHttpCarrier_ExtractQueryMulti_PreservesAllValues(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/?tags=a&tags=b&tags=c", nil)
	c := httpCarrier{r}
	got := c.ExtractQueryMulti()
	want := map[string][]string{"tags": {"a", "b", "c"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ExtractQueryMulti() = %v, want %v", got, want)
	}
}

// TestHttpCarrier_ExtractHeaders_FirstValueWins confirms
// httpCarrier.ExtractHeaders collapses a multi-value header to its first
// value, mirroring ExtractQuery's contract on the header axis.
func TestHttpCarrier_ExtractHeaders_FirstValueWins(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.Header.Add("X-Trace-Id", "trace-1")
	r.Header.Add("X-Trace-Id", "trace-2")
	r.Header.Set("X-Tenant", "acme")
	c := httpCarrier{r}
	got := c.ExtractHeaders()
	if got["X-Trace-Id"] != "trace-1" {
		t.Errorf("ExtractHeaders()[X-Trace-Id] = %q, want %q", got["X-Trace-Id"], "trace-1")
	}
	if got["X-Tenant"] != "acme" {
		t.Errorf("ExtractHeaders()[X-Tenant] = %q, want %q", got["X-Tenant"], "acme")
	}
}

// TestHttpCarrier_ExtractCookies_FirstValueWins confirms
// httpCarrier.ExtractCookies collapses a repeated cookie name to its
// first value, mirroring ExtractQuery/ExtractHeaders' contract.
func TestHttpCarrier_ExtractCookies_FirstValueWins(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.AddCookie(&http.Cookie{Name: "session", Value: "first"})
	r.Header.Add("Cookie", "session=second")
	r.AddCookie(&http.Cookie{Name: "tenant", Value: "acme"})
	c := httpCarrier{r}
	got := c.ExtractCookies()
	if got["session"] != "first" {
		t.Errorf("ExtractCookies()[session] = %q, want %q", got["session"], "first")
	}
	if got["tenant"] != "acme" {
		t.Errorf("ExtractCookies()[tenant] = %q, want %q", got["tenant"], "acme")
	}
}
