package rest

import (
	"strings"
	"testing"

	"github.com/DaniDeer/go-codex/codex"
)

type fakeCapabilityWithName struct{}

func (fakeCapabilityWithName) CapabilityName() string { return "Fake" }

type fakeCapabilityWithoutName struct{}

func TestCapabilityNameOf(t *testing.T) {
	if got := CapabilityNameOf(fakeCapabilityWithName{}); got != "Fake" {
		t.Errorf("want %q, got %q", "Fake", got)
	}
	if got := CapabilityNameOf(fakeCapabilityWithoutName{}); got != "rest.fakeCapabilityWithoutName" {
		t.Errorf("want %%T-derived name, got %q", got)
	}
}

func TestCheckCapabilityCoverage_passes(t *testing.T) {
	declared := []CapabilityRequirement{{Name: "QoS"}, {Name: "HWM"}}
	supplied := []any{fakeCapabilityWithName{}}
	if err := CheckCapabilityCoverage("route", declared, supplied); err == nil {
		t.Fatal("want CapabilityCoverageError, got nil")
	}
	declared2 := []CapabilityRequirement{{Name: "Fake"}}
	if err := CheckCapabilityCoverage("route", declared2, supplied); err != nil {
		t.Fatalf("want nil error when names match, got %v", err)
	}
}

func TestVerifyCapabilityCoverage_emptyDeclared_skipsEntirely(t *testing.T) {
	type fakeAdapterCapability interface{ isFakeAdapterCapability() }
	if err := VerifyCapabilityCoverage[fakeAdapterCapability]("t", nil, nil); err != nil {
		t.Fatalf("want nil, got %v", err)
	}
}

func TestVerifyCapabilityCoverage_delegatesToCheckCapabilityCoverage(t *testing.T) {
	declared := []CapabilityRequirement{{Name: "Fake"}}
	supplied := []fakeCapabilityWithName{{}}
	if err := VerifyCapabilityCoverage("t", declared, supplied); err != nil {
		t.Fatalf("want nil (name matches), got %v", err)
	}
	missing := []CapabilityRequirement{{Name: "QoS"}}
	err := VerifyCapabilityCoverage("t", missing, supplied)
	var cce *CapabilityCoverageError
	if e, ok := err.(*CapabilityCoverageError); ok {
		cce = e
	} else {
		t.Fatalf("want *CapabilityCoverageError, got %T (%v)", err, err)
	}
	if len(cce.Missing) != 1 || cce.Missing[0] != "QoS" {
		t.Errorf("want Missing=[QoS], got %v", cce.Missing)
	}
}

func TestCapabilityRequirement_appliesRouteAndRendersSpec(t *testing.T) {
	rt := NewRoute[capTestReq, capTestResp]("POST", "/cap/spec", capTestReqCodec, capTestRespCodec,
		CapabilityRequirement{Name: "QoS", Description: "MQTT-style quality-of-service level"})
	handle := rt.ClientHandle()
	if len(handle.Requirements) != 1 || handle.Requirements[0].Name != "QoS" {
		t.Fatalf("want Requirements=[{QoS ...}], got %+v", handle.Requirements)
	}
	if len(handle.Descriptor.Capabilities) != 1 || handle.Descriptor.Capabilities[0].Name != "QoS" {
		t.Fatalf("want Descriptor.Capabilities to carry the spec, got %+v", handle.Descriptor.Capabilities)
	}
}

// TestCapabilityRequirement_appliesSSERouteAndRendersSpec closes a
// confirmed gap: [SSERouteHandle] previously had no Requirements field at
// all, so a [CapabilityRequirement] declared via [NewSSERoute] was
// silently accumulated into the OpenAPI spec's x-codex-capabilities
// vendor extension (Descriptor.Capabilities, via buildDescriptor) but
// NEVER exposed on the handle for an adapter's capability-coverage check
// to consult — mirrors TestCapabilityRequirement_appliesRouteAndRendersSpec
// for SSE routes, verifying BOTH RegisterHandle and ClientHandle.
func TestCapabilityRequirement_appliesSSERouteAndRendersSpec(t *testing.T) {
	sr := NewSSERoute[capTestReq, capTestResp]("/cap/spec-sse", capTestReqCodec, capTestRespCodec,
		CapabilityRequirement{Name: "QoS", Description: "MQTT-style quality-of-service level"})

	clientHandle := sr.ClientHandle()
	if len(clientHandle.Requirements) != 1 || clientHandle.Requirements[0].Name != "QoS" {
		t.Fatalf("ClientHandle: want Requirements=[{QoS ...}], got %+v", clientHandle.Requirements)
	}

	b := NewServer(Info{Title: "t", Version: "1"})
	handle, err := sr.RegisterHandle(b)
	if err != nil {
		t.Fatalf("RegisterHandle: %v", err)
	}
	if len(handle.Requirements) != 1 || handle.Requirements[0].Name != "QoS" {
		t.Fatalf("RegisterHandle: want Requirements=[{QoS ...}], got %+v", handle.Requirements)
	}
	if len(handle.Descriptor.Capabilities) != 1 || handle.Descriptor.Capabilities[0].Name != "QoS" {
		t.Fatalf("want Descriptor.Capabilities to carry the spec, got %+v", handle.Descriptor.Capabilities)
	}
}

func TestBuildCapabilityRequirements_empty(t *testing.T) {
	if got := buildCapabilityRequirements(nil); got != nil {
		t.Errorf("want nil for empty input, got %+v", got)
	}
}

func TestBuildCapabilityRequirements_convertsShape(t *testing.T) {
	in := []CapabilityRequirement{{Name: "QoS", Description: "d"}}
	out := buildCapabilityRequirements(in)
	if len(out) != 1 || out[0].Name != "QoS" || out[0].Description != "d" {
		t.Fatalf("want [{QoS d}], got %+v", out)
	}
}

// ── Tier 2: HeaderParamNames/CookieParamNames/QueryParamNames ──────────

type paramMergeReq struct {
	MergeField string
}

var paramMergeReqCodec = codex.Struct[paramMergeReq](
	codex.RequiredField("mergeField", codex.String(),
		func(r paramMergeReq) string { return r.MergeField },
		func(r *paramMergeReq, v string) { r.MergeField = v },
	),
)

func TestHeaderParamNames_ReturnsPlainAndMergeFieldDeclarations(t *testing.T) {
	rt := NewRoute[paramMergeReq, capTestResp]("GET", "/cap/headers", paramMergeReqCodec, capTestRespCodec,
		HeaderParam{Name: "X-Plain"},
		NewRequiredHeaderParam("X-Merge", codex.String(), func(r paramMergeReq) string { return r.MergeField }, func(r *paramMergeReq, v string) { r.MergeField = v }),
	)
	handle := rt.ClientHandle()
	names := handle.HeaderParamNames()
	if !containsAll(names, "X-Plain", "X-Merge") {
		t.Fatalf("want BOTH plain and merge-field header names, got %v", names)
	}
}

func TestCookieParamNames_ReturnsPlainAndMergeFieldDeclarations(t *testing.T) {
	rt := NewRoute[paramMergeReq, capTestResp]("GET", "/cap/cookies", paramMergeReqCodec, capTestRespCodec,
		CookieParam{Name: "plain-cookie"},
		NewRequiredCookieParam("merge-cookie", codex.String(), func(r paramMergeReq) string { return r.MergeField }, func(r *paramMergeReq, v string) { r.MergeField = v }),
	)
	handle := rt.ClientHandle()
	names := handle.CookieParamNames()
	if !containsAll(names, "plain-cookie", "merge-cookie") {
		t.Fatalf("want BOTH plain and merge-field cookie names, got %v", names)
	}
}

func TestQueryParamNames_ReturnsPlainAndMergeFieldDeclarations(t *testing.T) {
	rt := NewRoute[paramMergeReq, capTestResp]("GET", "/cap/query", paramMergeReqCodec, capTestRespCodec,
		QueryParam{Name: "plain"},
		NewRequiredQueryParam("merge", codex.String(), func(r paramMergeReq) string { return r.MergeField }, func(r *paramMergeReq, v string) { r.MergeField = v }),
	)
	handle := rt.ClientHandle()
	names := handle.QueryParamNames()
	if !containsAll(names, "plain", "merge") {
		t.Fatalf("want BOTH plain and merge-field query names, got %v", names)
	}
}

func TestHeaderParamNames_IncludesMiddlewareDeclaredHeader(t *testing.T) {
	// WithRequestHeaderSpec declares a presence-only header on a
	// codec-backed Middleware[struct{},struct{}] value —
	// applyParamDeclarations merges it DIRECTLY into rb.headerParams
	// (confirmed via source trace), so a route declaring a header ONLY
	// via .Use(mw) must still show up in HeaderParamNames(). Uses
	// RegisterHandle (server-side), NOT ClientHandle — a genuine,
	// confirmed ASYMMETRY found while writing this test: ClientHandle
	// deliberately stays infallible and does NOT run
	// applyParamDeclarations's middleware-merge step at all (only
	// applyMiddlewareSecurityForClient, Security-only) — so a
	// middleware-declared header is invisible via ClientHandle().
	// HeaderParamNames() is documented for adapter Serve/AttachServer
	// consumption, which always uses the Register/RegisterHandle path,
	// so this is the CORRECT handle source for this test, not a
	// workaround.
	mw := NewMiddleware(Declaration[struct{}, struct{}]{Name: "declare-header-param:X-Via-Middleware"}).
		WithRequestHeaderSpec(HeaderParam{Name: "X-Via-Middleware"})
	rt := NewRoute[capTestReq, capTestResp]("GET", "/cap/mw-header", capTestReqCodec, capTestRespCodec).Use(mw)
	server := NewServer(Info{Title: "Test", Version: "1.0.0"})
	handle, err := rt.RegisterHandle(server)
	if err != nil {
		t.Fatalf("RegisterHandle: %v", err)
	}
	names := handle.HeaderParamNames()
	if !containsAll(names, "X-Via-Middleware") {
		t.Fatalf("want middleware-declared header included, got %v", names)
	}
}

func TestRequiredParamKinds_ScansPlainParamsAndSecuritySchemeIn(t *testing.T) {
	rt := NewRoute[capTestReq, capTestResp]("GET", "/cap/scopes", capTestReqCodec, capTestRespCodec,
		QueryParam{Name: "q"},
	).Use(SecurityMiddleware[struct{}, struct{}]("apiKeyCookie", APIKeyScheme("X-Session", "cookie"), nil))
	handle := rt.ClientHandle()
	kinds := RequiredParamKinds(handle.HeaderParamNames(), handle.CookieParamNames(), handle.QueryParamNames(), handle.SecuritySchemes)
	if !kinds["Query"] {
		t.Error("want Query kind required (plain QueryParam declared)")
	}
	if !kinds["Cookie"] {
		t.Error("want Cookie kind required (APIKeyScheme with In=cookie, no separate CookieParam declared)")
	}
	if kinds["Header"] {
		t.Error("want Header NOT required — nothing declared it")
	}
}

func TestUnsupportedParamKindError(t *testing.T) {
	err := UnsupportedParamKindError{Kind: "Cookie", Adapter: "zeromqrest"}
	if !strings.Contains(err.Error(), "Cookie") || !strings.Contains(err.Error(), "zeromqrest") {
		t.Errorf("want error mentioning Kind and Adapter, got %q", err.Error())
	}
	v := err.LogValue()
	keys := map[string]bool{}
	for _, a := range v.Group() {
		keys[a.Key] = true
	}
	for _, want := range []string{"kind", "adapter"} {
		if !keys[want] {
			t.Errorf("want key %q in LogValue group, got keys %v", want, keys)
		}
	}
}

func containsAll(haystack []string, wants ...string) bool {
	set := make(map[string]bool, len(haystack))
	for _, h := range haystack {
		set[h] = true
	}
	for _, w := range wants {
		if !set[w] {
			return false
		}
	}
	return true
}
