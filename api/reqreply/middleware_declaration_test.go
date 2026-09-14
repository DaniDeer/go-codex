package reqreply_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/DaniDeer/go-codex/api/reqreply"
	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/middleware"
)

type mdAuthIn struct{ Token string }
type mdAuthOut struct{ OK bool }

var mdAuthInCodec = codex.Struct[mdAuthIn]()
var mdAuthOutCodec = codex.Struct[mdAuthOut]()

func newAuthDeclaration(name string) middleware.Declaration[mdAuthIn, mdAuthOut] {
	return middleware.NewDeclaration(name, mdAuthInCodec, mdAuthOutCodec)
}

// TestRoute_Use_BundledWithReceive_AgnosticAttachment confirms a
// Middleware with WithReceive set, attached via plain .Use(), dispatches
// without needing Transform.
func TestRoute_Use_BundledWithReceive_AgnosticAttachment(t *testing.T) {
	mw := reqreply.NewMiddleware(newAuthDeclaration("bundled-auth")).
		WithReceive(func(ctx context.Context, in mdAuthIn) (mdAuthOut, error) {
			return mdAuthOut{OK: in.Token == "secret"}, nil
		})
	r := newMWTestRoute().Use(mw)
	b := newBuilder()
	h, err := r.Register(b)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	if len(h.MiddlewareHandlers) != 1 {
		t.Fatalf("want 1 MiddlewareHandler from bundled .Use(), got %d", len(h.MiddlewareHandlers))
	}
	if !h.MiddlewareHandlers[0].Agnostic {
		t.Fatalf("want Agnostic=true for a bundled .Use()-attached handler")
	}
}

// TestRoute_Use_SameMiddlewareValue_ReusedAcrossDifferentReqTypes mirrors
// D-0003's own "test that actually proves reuse" (Round 13) — the LITERAL
// SAME bundled Middleware value, attached via plain .Use() to TWO+ routes
// with DIFFERENT Req/Resp types, dispatches correctly and independently on
// EACH.
func TestRoute_Use_SameMiddlewareValue_ReusedAcrossDifferentReqTypes(t *testing.T) {
	mw := reqreply.NewMiddleware(newAuthDeclaration("shared-auth")).
		WithReceive(func(ctx context.Context, in mdAuthIn) (mdAuthOut, error) {
			return mdAuthOut{OK: true}, nil
		})

	type otherReq struct{ Name string }
	type otherResp struct{ Greeting string }
	otherReqCodec := codex.Struct[otherReq](
		codex.RequiredField("name", codex.String(),
			func(r otherReq) string { return r.Name },
			func(r *otherReq, v string) { r.Name = v }),
	)
	otherRespCodec := codex.Struct[otherResp](
		codex.RequiredField("greeting", codex.String(),
			func(r otherResp) string { return r.Greeting },
			func(r *otherResp, v string) { r.Greeting = v }),
	)
	otherRoute := reqreply.NewRoute[otherReq, otherResp]("other/greet", otherReqCodec, otherRespCodec).Use(mw)

	r1 := newMWTestRoute().Use(mw)
	b1 := newBuilder()
	h1, err := r1.Register(b1)
	if err != nil {
		t.Fatalf("Register r1 (computeReq/computeResp): %v", err)
	}
	b2 := newBuilder()
	h2, err := otherRoute.Register(b2)
	if err != nil {
		t.Fatalf("Register r2 (otherReq/otherResp): %v", err)
	}
	if len(h1.MiddlewareHandlers) != 1 || len(h2.MiddlewareHandlers) != 1 {
		t.Fatalf("want 1 MiddlewareHandler on EACH route, got %d and %d", len(h1.MiddlewareHandlers), len(h2.MiddlewareHandlers))
	}
}

// TestRoute_Register_DuplicateMiddlewareNameError covers D6(b): two
// Middleware values sharing a Declaration.Name on one route.
func TestRoute_Register_DuplicateMiddlewareNameError(t *testing.T) {
	mw1 := reqreply.Transform(newMWTestRoute(), reqreply.NewMiddleware(newAuthDeclaration("dup-name")),
		func(ctx context.Context, req *computeReq, in mdAuthIn) (mdAuthOut, error) { return mdAuthOut{}, nil })
	r := reqreply.Transform(mw1, reqreply.NewMiddleware(newAuthDeclaration("dup-name")),
		func(ctx context.Context, req *computeReq, in mdAuthIn) (mdAuthOut, error) { return mdAuthOut{}, nil })

	b := newBuilder()
	_, err := r.Register(b)
	var dupErr reqreply.DuplicateMiddlewareNameError
	if !errors.As(err, &dupErr) {
		t.Fatalf("want DuplicateMiddlewareNameError, got %T: %v", err, err)
	}
}

// TestRoute_Register_AmbiguousMiddlewareAttachmentError covers D7: one
// value both bundled (WithReceive) AND passed to Transform.
func TestRoute_Register_AmbiguousMiddlewareAttachmentError(t *testing.T) {
	mw := reqreply.NewMiddleware(newAuthDeclaration("ambiguous")).
		WithReceive(func(ctx context.Context, in mdAuthIn) (mdAuthOut, error) { return mdAuthOut{}, nil })

	r := reqreply.Transform(newMWTestRoute(), mw,
		func(ctx context.Context, req *computeReq, in mdAuthIn) (mdAuthOut, error) { return mdAuthOut{}, nil })

	b := newBuilder()
	_, err := r.Register(b)
	var ambErr reqreply.AmbiguousMiddlewareAttachmentError
	if !errors.As(err, &ambErr) {
		t.Fatalf("want AmbiguousMiddlewareAttachmentError, got %T: %v", err, err)
	}
}

// TestRoute_Register_ConflictingParamContributionError covers decision
// #5: two Middleware values declare the SAME property name with
// DIFFERENT attributes.
func TestRoute_Register_ConflictingParamContributionError(t *testing.T) {
	mw1 := reqreply.NewMiddleware(newAuthDeclaration("mw1")).
		WithRequestProperty(reqreply.NewPropertyParam("tenantID", codex.String(),
			func(v mdAuthIn) string { return v.Token },
			func(v *mdAuthIn, s string) { v.Token = s }))
	mw2 := reqreply.NewMiddleware(newAuthDeclaration("mw2")).
		WithRequestProperty(reqreply.NewOptionalPropertyParam("tenantID", codex.String(),
			func(v mdAuthIn) string { return v.Token },
			func(v *mdAuthIn, s string) { v.Token = s }))

	r := reqreply.Transform(newMWTestRoute(), mw1,
		func(ctx context.Context, req *computeReq, in mdAuthIn) (mdAuthOut, error) { return mdAuthOut{}, nil })
	r = reqreply.Transform(r, mw2,
		func(ctx context.Context, req *computeReq, in mdAuthIn) (mdAuthOut, error) { return mdAuthOut{}, nil })

	b := newBuilder()
	_, err := r.Register(b)
	var conflictErr reqreply.ConflictingParamContributionError
	if !errors.As(err, &conflictErr) {
		t.Fatalf("want ConflictingParamContributionError, got %T: %v", err, err)
	}
}

// TestRoute_Register_RequiredVsOptionalPropertyMismatchError (Round 11)
// is the SAME conflict via NewPropertyParam(required) vs.
// NewOptionalPropertyParam(optional) explicitly.
func TestRoute_Register_RequiredVsOptionalPropertyMismatchError(t *testing.T) {
	required := reqreply.NewPropertyParam("tenantID", codex.String(),
		func(v mdAuthIn) string { return v.Token },
		func(v *mdAuthIn, s string) { v.Token = s })
	optional := reqreply.NewOptionalPropertyParam("tenantID", codex.String(),
		func(v mdAuthIn) string { return v.Token },
		func(v *mdAuthIn, s string) { v.Token = s })

	mw1 := reqreply.NewMiddleware(newAuthDeclaration("req-mw")).WithRequestProperty(required)
	mw2 := reqreply.NewMiddleware(newAuthDeclaration("opt-mw")).WithRequestProperty(optional)

	r := reqreply.Transform(newMWTestRoute(), mw1,
		func(ctx context.Context, req *computeReq, in mdAuthIn) (mdAuthOut, error) { return mdAuthOut{}, nil })
	r = reqreply.Transform(r, mw2,
		func(ctx context.Context, req *computeReq, in mdAuthIn) (mdAuthOut, error) { return mdAuthOut{}, nil })

	b := newBuilder()
	_, err := r.Register(b)
	var conflictErr reqreply.ConflictingParamContributionError
	if !errors.As(err, &conflictErr) {
		t.Fatalf("want ConflictingParamContributionError, got %T: %v", err, err)
	}
}

// TestRoute_Register_ConflictingPropertyCodecSchemaError (Round 15)
// covers the Schema-based comparison — matching Required but DIFFERENT
// codec schemas.
func TestRoute_Register_ConflictingPropertyCodecSchemaError(t *testing.T) {
	mw1 := reqreply.NewMiddleware(newAuthDeclaration("mw1")).
		WithRequestProperty(reqreply.NewPropertyParam("tenantID", codex.String(),
			func(v mdAuthIn) string { return v.Token },
			func(v *mdAuthIn, s string) { v.Token = s }))
	mw2 := reqreply.NewMiddleware(newAuthDeclaration("mw2")).
		WithRequestProperty(reqreply.NewPropertyParam("tenantID", codex.Int(),
			func(v mdAuthIn) int { return 0 },
			func(v *mdAuthIn, i int) {}))

	r := reqreply.Transform(newMWTestRoute(), mw1,
		func(ctx context.Context, req *computeReq, in mdAuthIn) (mdAuthOut, error) { return mdAuthOut{}, nil })
	r = reqreply.Transform(r, mw2,
		func(ctx context.Context, req *computeReq, in mdAuthIn) (mdAuthOut, error) { return mdAuthOut{}, nil })

	b := newBuilder()
	_, err := r.Register(b)
	var conflictErr reqreply.ConflictingParamContributionError
	if !errors.As(err, &conflictErr) {
		t.Fatalf("want ConflictingParamContributionError (codec schema mismatch), got %T: %v", err, err)
	}
}

// TestRoute_Register_AgreeingParamContributions_DedupeWithoutError (D6(a))
// two Middleware values declaring the SAME name with the SAME attributes
// dedupe into one spec entry, no error.
func TestRoute_Register_AgreeingParamContributions_DedupeWithoutError(t *testing.T) {
	p := func() reqreply.MergedPropertyParam[mdAuthIn] {
		return reqreply.NewPropertyParam("tenantID", codex.String(),
			func(v mdAuthIn) string { return v.Token },
			func(v *mdAuthIn, s string) { v.Token = s })
	}
	mw1 := reqreply.NewMiddleware(newAuthDeclaration("mw1")).WithRequestProperty(p())
	mw2 := reqreply.NewMiddleware(newAuthDeclaration("mw2")).WithRequestProperty(p())

	r := reqreply.Transform(newMWTestRoute(), mw1,
		func(ctx context.Context, req *computeReq, in mdAuthIn) (mdAuthOut, error) { return mdAuthOut{}, nil })
	r = reqreply.Transform(r, mw2,
		func(ctx context.Context, req *computeReq, in mdAuthIn) (mdAuthOut, error) { return mdAuthOut{}, nil })

	b := newBuilder()
	if _, err := r.Register(b); err != nil {
		t.Fatalf("Register: %v", err)
	}
	out := mustSpec(t, b)
	if strings.Count(out, "tenantID:") != 1 {
		t.Errorf("want \"tenantID:\" to appear exactly once in spec (deduped), got %d occurrences:\n%s", strings.Count(out, "tenantID:"), out)
	}
}

// TestRoute_Register_TwoPhase1bOnlyContributions_MismatchNowErrors is the
// BREAKING-CHANGE regression test (Round 18): two Phase-1b-ONLY
// declarations (zero new-axis involvement) with differing Required/codec
// for the same property name must now FAIL.
func TestRoute_Register_TwoPhase1bOnlyContributions_MismatchNowErrors(t *testing.T) {
	strCodec := codex.String()
	mw1 := middleware.Middleware{
		Name: "flat-mw-1",
		RequestHeaderParams: []middleware.HeaderParamSpec{
			{Name: "X-Region", Required: true, Codec: &strCodec},
		},
	}
	mw2 := middleware.Middleware{
		Name: "flat-mw-2",
		RequestHeaderParams: []middleware.HeaderParamSpec{
			{Name: "X-Region", Required: false},
		},
	}
	r := newMWTestRoute().Use(mw1, mw2)
	b := newBuilder()
	_, err := r.Register(b)
	var conflictErr reqreply.ConflictingParamContributionError
	if !errors.As(err, &conflictErr) {
		t.Fatalf("want ConflictingParamContributionError for two mismatched Phase-1b-only declarations, got %T: %v", err, err)
	}
}

// TestRoute_Register_TopicAndPropertySameName_NoConflict (Round 15): a
// topic var named "tenantID" AND a property ALSO named "tenantID" on the
// SAME route — NO error, confirming INDEPENDENT namespaces.
func TestRoute_Register_TopicAndPropertySameName_NoConflict(t *testing.T) {
	type tvReq struct {
		TenantID string
		X, Y     int
	}
	tvReqCodec := codex.Struct[tvReq](
		codex.RequiredField("x", codex.Int(), func(r tvReq) int { return r.X }, func(r *tvReq, v int) { r.X = v }),
		codex.RequiredField("y", codex.Int(), func(r tvReq) int { return r.Y }, func(r *tvReq, v int) { r.Y = v }),
	)
	route := reqreply.NewRoute[tvReq, computeResp](
		"compute/{tenantID}/add", tvReqCodec, mwTestRespCodec,
		reqreply.NewTopicParam("tenantID", codex.String(),
			func(r tvReq) string { return r.TenantID },
			func(r *tvReq, v string) { r.TenantID = v }),
	)

	type tvIn struct{ TenantID string }
	type tvOut struct{}
	tvMw := reqreply.NewMiddleware(middleware.NewDeclaration("tv-mw", codex.Struct[tvIn](), codex.Struct[tvOut]())).
		WithRequestProperty(reqreply.NewPropertyParam("tenantID", codex.String(),
			func(v tvIn) string { return v.TenantID },
			func(v *tvIn, s string) { v.TenantID = s }))

	r := reqreply.Transform(route, tvMw, func(ctx context.Context, req *tvReq, in tvIn) (tvOut, error) { return tvOut{}, nil })
	b := newBuilder()
	if _, err := r.Register(b); err != nil {
		t.Fatalf("want no conflict between topic-var and property namespaces, got: %v", err)
	}
}

// TestRoute_Register_OptionalProperty_NotInSchemaRequiredList (Round 16).
func TestRoute_Register_OptionalProperty_NotInSchemaRequiredList(t *testing.T) {
	mw := reqreply.NewMiddleware(newAuthDeclaration("opt-schema")).
		WithRequestProperty(reqreply.NewOptionalPropertyParam("X-Optional", codex.String(),
			func(v mdAuthIn) string { return v.Token },
			func(v *mdAuthIn, s string) { v.Token = s }))
	r := reqreply.Transform(newMWTestRoute(), mw,
		func(ctx context.Context, req *computeReq, in mdAuthIn) (mdAuthOut, error) { return mdAuthOut{}, nil })
	b := newBuilder()
	if _, err := r.Register(b); err != nil {
		t.Fatalf("Register: %v", err)
	}
	out := mustSpec(t, b)
	if !strings.Contains(out, "X-Optional:") {
		t.Fatalf("want X-Optional to appear in the rendered schema:\n%s", out)
	}
	if containsRequiredEntry(out, "X-Optional") {
		t.Errorf("want X-Optional NOT in the rendered schema's required list:\n%s", out)
	}
}

// TestRoute_Register_RequiredProperty_InSchemaRequiredList (Round 16).
func TestRoute_Register_RequiredProperty_InSchemaRequiredList(t *testing.T) {
	mw := reqreply.NewMiddleware(newAuthDeclaration("req-schema")).
		WithRequestProperty(reqreply.NewPropertyParam("X-Required", codex.String(),
			func(v mdAuthIn) string { return v.Token },
			func(v *mdAuthIn, s string) { v.Token = s }))
	r := reqreply.Transform(newMWTestRoute(), mw,
		func(ctx context.Context, req *computeReq, in mdAuthIn) (mdAuthOut, error) { return mdAuthOut{}, nil })
	b := newBuilder()
	if _, err := r.Register(b); err != nil {
		t.Fatalf("Register: %v", err)
	}
	out := mustSpec(t, b)
	if !containsRequiredEntry(out, "X-Required") {
		t.Fatalf("want X-Required IN the rendered schema's required list:\n%s", out)
	}
}

// containsRequiredEntry is a crude textual check that name appears as a
// "- name" list entry somewhere under a "required:" YAML block —
// sufficient for this test's purpose given the existing test suite's own
// reliance on rendered-spec substring checks (see mustSpec's other
// callers in route_test.go/middleware_test.go).
func containsRequiredEntry(spec, name string) bool {
	idx := strings.Index(spec, "required:")
	for idx != -1 {
		end := strings.Index(spec[idx:], "\n\n")
		block := spec[idx:]
		if end != -1 {
			block = spec[idx : idx+end]
		}
		if strings.Contains(block, "- "+name) {
			return true
		}
		next := strings.Index(spec[idx+9:], "required:")
		if next == -1 {
			break
		}
		idx = idx + 9 + next
	}
	return false
}
