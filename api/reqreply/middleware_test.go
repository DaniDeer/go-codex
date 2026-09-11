package reqreply_test

import (
	"strings"
	"testing"

	"github.com/DaniDeer/go-codex/api/reqreply"
	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/middleware"
	"github.com/DaniDeer/go-codex/route"
)

var mwTestReqCodec = codex.Struct[computeReq](
	codex.RequiredField("x", codex.Int(),
		func(r computeReq) int { return r.X },
		func(r *computeReq, v int) { r.X = v }),
	codex.RequiredField("y", codex.Int(),
		func(r computeReq) int { return r.Y },
		func(r *computeReq, v int) { r.Y = v }),
)

var mwTestRespCodec = codex.Struct[computeResp](
	codex.RequiredField("sum", codex.Int(),
		func(r computeResp) int { return r.Sum },
		func(r *computeResp, v int) { r.Sum = v }),
)

var bearerCodecForMWTest = codex.String()
var bearerAuthTestMw = middleware.SecurityScheme("bearerAuth", route.BearerScheme("JWT"), []string{"read"}, &bearerCodecForMWTest)

func newMWTestRoute() reqreply.Route[computeReq, computeResp] {
	return reqreply.NewRoute[computeReq, computeResp](
		"compute/mw-test",
		mwTestReqCodec, mwTestRespCodec,
		reqreply.RouteMeta{OperationID: "mwTest"},
	)
}

func TestRoute_Use_Chainable(t *testing.T) {
	base := newMWTestRoute()
	r1 := base.Use(bearerAuthTestMw)
	r2 := base.Use(bearerAuthTestMw).Use(bearerAuthTestMw)

	b := reqreply.NewServer(reqreply.Info{Title: "t", Version: "1.0.0"})
	h1, err := r1.HandleMW(&bearerAuthTestMw, func() (map[string][]string, error) { return nil, nil }).Register(b)
	if err != nil {
		t.Fatalf("Register r1: %v", err)
	}
	if len(h1.Security) == 0 {
		t.Fatalf("r1: want Security declared via .Use(), got empty")
	}

	b2 := reqreply.NewServer(reqreply.Info{Title: "t2", Version: "1.0.0"})
	h2, err := r2.HandleMW(&bearerAuthTestMw, func() (map[string][]string, error) { return nil, nil }).Register(b2)
	if err != nil {
		t.Fatalf("Register r2 (.Use(mw1, mw2) equivalent): %v", err)
	}
	if len(h2.Security) == 0 {
		t.Fatalf("r2: want Security declared, got empty")
	}
}

func TestRoute_Use_DoesNotMutateOriginal(t *testing.T) {
	base := newMWTestRoute()
	_ = base.Use(bearerAuthTestMw)

	b := reqreply.NewServer(reqreply.Info{Title: "t", Version: "1.0.0"})
	h, err := base.Register(b)
	if err != nil {
		t.Fatalf("Register base: %v", err)
	}
	if len(h.Security) != 0 {
		t.Fatalf("base route was mutated by .Use() call on a derived value — want Security empty, got %v", h.Security)
	}
}

func TestHandleMW_Paired_DerivesSatisfiesFromSecurity(t *testing.T) {
	r := newMWTestRoute().Use(bearerAuthTestMw).
		HandleMW(&bearerAuthTestMw, func() (map[string][]string, error) { return nil, nil })
	b := reqreply.NewServer(reqreply.Info{Title: "t", Version: "1.0.0"})
	h, err := r.Register(b)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	if len(h.Implementations) != 1 {
		t.Fatalf("want 1 Implementation, got %d", len(h.Implementations))
	}
	if len(h.Implementations[0].Satisfies) != 1 || h.Implementations[0].Satisfies[0] != "bearerAuth" {
		t.Fatalf("want Satisfies=[bearerAuth], got %v", h.Implementations[0].Satisfies)
	}
}

func TestHandleMW_Unpaired_GeneralPurpose_EmptySatisfies(t *testing.T) {
	r := newMWTestRoute().HandleMW(nil, func() {})
	b := reqreply.NewServer(reqreply.Info{Title: "t", Version: "1.0.0"})
	h, err := r.Register(b)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	if len(h.Implementations) != 1 {
		t.Fatalf("want 1 Implementation, got %d", len(h.Implementations))
	}
	if len(h.Implementations[0].Satisfies) != 0 {
		t.Fatalf("want empty Satisfies for unpaired HandleMW, got %v", h.Implementations[0].Satisfies)
	}
}

func TestClientMW_Paired_DerivesSatisfiesFromSecurity(t *testing.T) {
	r := newMWTestRoute().Use(bearerAuthTestMw).
		HandleMW(&bearerAuthTestMw, func() (map[string][]string, error) { return nil, nil }).
		ClientMW(&bearerAuthTestMw, func() {})
	b := reqreply.NewServer(reqreply.Info{Title: "t", Version: "1.0.0"})
	h, err := r.Register(b)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	if len(h.ClientImplementations) != 1 {
		t.Fatalf("want 1 ClientImplementation, got %d", len(h.ClientImplementations))
	}
	if len(h.ClientImplementations[0].Satisfies) != 1 || h.ClientImplementations[0].Satisfies[0] != "bearerAuth" {
		t.Fatalf("want Satisfies=[bearerAuth], got %v", h.ClientImplementations[0].Satisfies)
	}
}

func TestClientMW_MultipleCallsForSameScheme_DistinctNames(t *testing.T) {
	r := newMWTestRoute().Use(bearerAuthTestMw).
		HandleMW(&bearerAuthTestMw, func() (map[string][]string, error) { return nil, nil }).
		ClientMW(&bearerAuthTestMw, func() { /* #0 */ }).
		ClientMW(&bearerAuthTestMw, func() { /* #1 */ })
	b := reqreply.NewServer(reqreply.Info{Title: "t", Version: "1.0.0"})
	h, err := r.Register(b)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	if len(h.ClientImplementations) != 2 {
		t.Fatalf("want 2 ClientImplementations, got %d", len(h.ClientImplementations))
	}
	if h.ClientImplementations[0].Name == h.ClientImplementations[1].Name {
		t.Fatalf("want distinct Names for two ClientMW calls attached to the same scheme, got %q twice", h.ClientImplementations[0].Name)
	}
}

func TestRoute_Register_PopulatesImplementations(t *testing.T) {
	r := newMWTestRoute().Use(bearerAuthTestMw).
		HandleMW(&bearerAuthTestMw, func() (map[string][]string, error) { return nil, nil }).
		ClientMW(&bearerAuthTestMw, func() {})
	b := reqreply.NewServer(reqreply.Info{Title: "t", Version: "1.0.0"})
	h, err := r.Register(b)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	if len(h.Implementations) != 1 || len(h.ClientImplementations) != 1 {
		t.Fatalf("want 1 Implementation and 1 ClientImplementation, got %d/%d", len(h.Implementations), len(h.ClientImplementations))
	}
}

func TestRoute_ClientHandle_PopulatesImplementations(t *testing.T) {
	r := newMWTestRoute().Use(bearerAuthTestMw).
		ClientMW(&bearerAuthTestMw, func() {})
	h := r.ClientHandle()
	if len(h.ClientImplementations) != 1 {
		t.Fatalf("want 1 ClientImplementation from ClientHandle(), got %d", len(h.ClientImplementations))
	}
	if len(h.Security) == 0 {
		t.Fatalf("want Security populated from .Use() via ClientHandle(), got empty")
	}
}

func TestRoute_Register_UnknownMiddlewareImplementationError(t *testing.T) {
	// HandleMW paired against "bearerAuth", but the route never .Use()'d
	// it — must fail loudly, not silently no-op.
	r := newMWTestRoute().
		HandleMW(&bearerAuthTestMw, func() (map[string][]string, error) { return nil, nil })
	b := reqreply.NewServer(reqreply.Info{Title: "t", Version: "1.0.0"})
	_, err := r.Register(b)
	var unknownErr reqreply.UnknownMiddlewareImplementationError
	if !isUnknownMiddlewareImplementationError(err, &unknownErr) {
		t.Fatalf("want reqreply.UnknownMiddlewareImplementationError, got %v", err)
	}
	if unknownErr.Scheme != "bearerAuth" {
		t.Fatalf("want Scheme=bearerAuth, got %q", unknownErr.Scheme)
	}
}

func isUnknownMiddlewareImplementationError(err error, target *reqreply.UnknownMiddlewareImplementationError) bool {
	e, ok := err.(reqreply.UnknownMiddlewareImplementationError)
	if ok {
		*target = e
	}
	return ok
}

// ── Phase 1b: header-param-as-middleware ────────────────────────────────────

var apiKeyHeaderCodec = codex.String()
var apiKeyHeaderMw = middleware.Middleware{
	Name: "declare-user-property-param:X-API-Key",
	RequestHeaderParams: []middleware.HeaderParamSpec{
		{Name: "X-API-Key", Description: "API key", Required: true, Codec: &apiKeyHeaderCodec},
	},
}

var traceHeaderMw = middleware.Middleware{
	Name: "declare-response-user-property-param:X-Trace-Id",
	ResponseHeaderParams: []middleware.ResponseHeaderParamSpec{
		{Name: "X-Trace-Id", Description: "Trace correlation id", Required: false},
	},
}

func TestRoute_Register_RendersRequestHeaderParamsIntoAsyncAPI(t *testing.T) {
	b := newBuilder()
	r := newMWTestRoute().Use(apiKeyHeaderMw)
	if _, err := r.Register(b); err != nil {
		t.Fatalf("Register: %v", err)
	}
	out := mustSpec(t, b)
	if !strings.Contains(out, "headers:") {
		t.Errorf("want \"headers:\" schema in spec:\n%s", out)
	}
	if !strings.Contains(out, "X-API-Key:") {
		t.Errorf("want \"X-API-Key:\" property in spec:\n%s", out)
	}
	if !strings.Contains(out, "API key") {
		t.Errorf("want header description 'API key' in spec:\n%s", out)
	}
}

func TestRoute_Register_RendersResponseHeaderParamsIntoAsyncAPI(t *testing.T) {
	b := newBuilder()
	r := newMWTestRoute().Use(traceHeaderMw)
	if _, err := r.Register(b); err != nil {
		t.Fatalf("Register: %v", err)
	}
	out := mustSpec(t, b)
	if !strings.Contains(out, "X-Trace-Id:") {
		t.Errorf("want \"X-Trace-Id:\" property in spec:\n%s", out)
	}
}

func TestRoute_Register_PopulatesRequestResponseHeaderParams(t *testing.T) {
	b := newBuilder()
	r := newMWTestRoute().Use(apiKeyHeaderMw, traceHeaderMw)
	h, err := r.Register(b)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	if len(h.RequestHeaderParams) != 1 || h.RequestHeaderParams[0].Name != "X-API-Key" {
		t.Fatalf("want 1 RequestHeaderParams entry named X-API-Key, got %+v", h.RequestHeaderParams)
	}
	if len(h.ResponseHeaderParams) != 1 || h.ResponseHeaderParams[0].Name != "X-Trace-Id" {
		t.Fatalf("want 1 ResponseHeaderParams entry named X-Trace-Id, got %+v", h.ResponseHeaderParams)
	}
}

func TestRoute_Register_DedupsHeaderParamsByName(t *testing.T) {
	// Two middlewares contributing the SAME request header param name —
	// must fold into ONE property in the spec, not two, and ONE entry in
	// RouteHandle.RequestHeaderParams.
	dup := middleware.Middleware{
		Name: "declare-user-property-param:X-API-Key-dup",
		RequestHeaderParams: []middleware.HeaderParamSpec{
			{Name: "X-API-Key", Required: true},
		},
	}
	b := newBuilder()
	r := newMWTestRoute().Use(apiKeyHeaderMw, dup)
	h, err := r.Register(b)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	if len(h.RequestHeaderParams) != 1 {
		t.Fatalf("want exactly 1 deduped RequestHeaderParams entry, got %d: %+v", len(h.RequestHeaderParams), h.RequestHeaderParams)
	}
	out := mustSpec(t, b)
	if strings.Count(out, "X-API-Key:") != 1 {
		t.Errorf("want \"X-API-Key:\" to appear exactly once in spec (deduped), got %d occurrences:\n%s", strings.Count(out, "X-API-Key:"), out)
	}
}

func TestRoute_ClientHandle_PopulatesHeaderParams(t *testing.T) {
	r := newMWTestRoute().Use(apiKeyHeaderMw, traceHeaderMw)
	h := r.ClientHandle()
	if len(h.RequestHeaderParams) != 1 || h.RequestHeaderParams[0].Name != "X-API-Key" {
		t.Fatalf("want 1 RequestHeaderParams entry from ClientHandle(), got %+v", h.RequestHeaderParams)
	}
	if len(h.ResponseHeaderParams) != 1 || h.ResponseHeaderParams[0].Name != "X-Trace-Id" {
		t.Fatalf("want 1 ResponseHeaderParams entry from ClientHandle(), got %+v", h.ResponseHeaderParams)
	}
}

func TestRoute_Register_NoHeaderParams_OmitsHeadersFromSpec(t *testing.T) {
	b := newBuilder()
	r := newMWTestRoute()
	if _, err := r.Register(b); err != nil {
		t.Fatalf("Register: %v", err)
	}
	out := mustSpec(t, b)
	if strings.Contains(out, "headers:") {
		t.Errorf("want no \"headers:\" schema when no header params are declared, got:\n%s", out)
	}
}
