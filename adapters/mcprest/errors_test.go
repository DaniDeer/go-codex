package mcprest_test

import (
	"errors"
	"testing"

	mcprest "github.com/DaniDeer/go-codex/adapters/mcprest"
	nethttp "github.com/DaniDeer/go-codex/adapters/nethttp"
	apimcp "github.com/DaniDeer/go-codex/api/mcp"
	"github.com/DaniDeer/go-codex/api/rest"
	"github.com/DaniDeer/go-codex/codex"
)

// dummy is a fields-free tool In/Out type — these tests only exercise
// ErrorPattern matching, not real tool input/output.
type dummy struct{}

var dummyCodec = codex.Struct[dummy]()

// newTestToolHandle builds an apimcp.ToolHandle carrying opts (usually
// mcprest.DefaultErrorPatterns(), optionally with a caller's own
// ErrorPattern declared first) — no Builder needed since ClientHandle
// skips duplicate-name/spec registration.
func newTestToolHandle(t *testing.T, opts ...apimcp.ToolOpt) *apimcp.ToolHandle[dummy, dummy] {
	t.Helper()
	h, err := apimcp.NewTool[dummy, dummy]("test_tool", dummyCodec, dummyCodec, opts...).ClientHandle()
	if err != nil {
		t.Fatalf("ClientHandle: %v", err)
	}
	return h
}

func TestDefaultErrorPatterns_UnexpectedStatusError_MapsStatusAndBody(t *testing.T) {
	h := newTestToolHandle(t, mcprest.DefaultErrorPatterns()...)

	err := nethttp.UnexpectedStatusError{Method: "GET", Path: "/x", StatusCode: 404, Body: []byte("not found")}
	resp, matched, matchErr := h.ErrorResponseFor(err)
	if matchErr != nil {
		t.Fatalf("unexpected map error: %v", matchErr)
	}
	if !matched {
		t.Fatal("want a match for UnexpectedStatusError")
	}
	payload, ok := resp.Value.(mcprest.RESTClientErrorPayload)
	if !ok {
		t.Fatalf("resp.Value = %T, want mcprest.RESTClientErrorPayload", resp.Value)
	}
	if payload.Kind != "unexpected_status" || payload.StatusCode != 404 || payload.Body != "not found" {
		t.Errorf("payload = %+v, want Kind=unexpected_status StatusCode=404 Body=%q", payload, "not found")
	}
}

func TestDefaultErrorPatterns_RequestError_MapsMessage(t *testing.T) {
	h := newTestToolHandle(t, mcprest.DefaultErrorPatterns()...)

	inner := errors.New("dial tcp: connection refused")
	err := nethttp.RequestError{Method: "GET", Path: "/x", Err: inner}
	resp, matched, matchErr := h.ErrorResponseFor(err)
	if matchErr != nil {
		t.Fatalf("unexpected map error: %v", matchErr)
	}
	if !matched {
		t.Fatal("want a match for RequestError")
	}
	payload := resp.Value.(mcprest.RESTClientErrorPayload)
	if payload.Kind != "request" || payload.Message == "" {
		t.Errorf("payload = %+v, want Kind=request with non-empty Message", payload)
	}
}

func TestDefaultErrorPatterns_SecurityCredentialError_Maps(t *testing.T) {
	h := newTestToolHandle(t, mcprest.DefaultErrorPatterns()...)

	err := rest.SecurityCredentialError{Scheme: "bearerAuth", Err: errors.New("empty token")}
	resp, matched, matchErr := h.ErrorResponseFor(err)
	if matchErr != nil {
		t.Fatalf("unexpected map error: %v", matchErr)
	}
	if !matched {
		t.Fatal("want a match for SecurityCredentialError")
	}
	payload := resp.Value.(mcprest.RESTClientErrorPayload)
	if payload.Kind != "security_credential" || payload.Message == "" {
		t.Errorf("payload = %+v, want Kind=security_credential with non-empty Message", payload)
	}
}

func TestDefaultErrorPatterns_PathParamError_Maps(t *testing.T) {
	h := newTestToolHandle(t, mcprest.DefaultErrorPatterns()...)

	err := rest.PathParamError{Name: "id", Value: "", Err: errors.New("expected non-empty string")}
	resp, matched, matchErr := h.ErrorResponseFor(err)
	if matchErr != nil {
		t.Fatalf("unexpected map error: %v", matchErr)
	}
	if !matched {
		t.Fatal("want a match for PathParamError")
	}
	payload := resp.Value.(mcprest.RESTClientErrorPayload)
	if payload.Kind != "path_param" || payload.Message == "" {
		t.Errorf("payload = %+v, want Kind=path_param with non-empty Message", payload)
	}
}

func TestDefaultErrorPatterns_MissingPathVarError_Maps(t *testing.T) {
	h := newTestToolHandle(t, mcprest.DefaultErrorPatterns()...)

	err := rest.MissingPathVarError{Name: "id"}
	resp, matched, matchErr := h.ErrorResponseFor(err)
	if matchErr != nil {
		t.Fatalf("unexpected map error: %v", matchErr)
	}
	if !matched {
		t.Fatal("want a match for MissingPathVarError")
	}
	payload := resp.Value.(mcprest.RESTClientErrorPayload)
	if payload.Kind != "missing_path_var" || payload.Message == "" {
		t.Errorf("payload = %+v, want Kind=missing_path_var with non-empty Message", payload)
	}
}

func TestDefaultErrorPatterns_QueryParamError_Maps(t *testing.T) {
	h := newTestToolHandle(t, mcprest.DefaultErrorPatterns()...)

	err := rest.QueryParamError{Name: "filter", Value: "bogus", Err: errors.New("invalid enum value")}
	resp, matched, matchErr := h.ErrorResponseFor(err)
	if matchErr != nil {
		t.Fatalf("unexpected map error: %v", matchErr)
	}
	if !matched {
		t.Fatal("want a match for QueryParamError")
	}
	payload := resp.Value.(mcprest.RESTClientErrorPayload)
	if payload.Kind != "query_param" || payload.Message == "" {
		t.Errorf("payload = %+v, want Kind=query_param with non-empty Message", payload)
	}
}

func TestDefaultErrorPatterns_CookieParamError_Maps(t *testing.T) {
	h := newTestToolHandle(t, mcprest.DefaultErrorPatterns()...)

	err := rest.CookieParamError{Name: "session_token", Value: "", Err: errors.New("expected non-empty string")}
	resp, matched, matchErr := h.ErrorResponseFor(err)
	if matchErr != nil {
		t.Fatalf("unexpected map error: %v", matchErr)
	}
	if !matched {
		t.Fatal("want a match for CookieParamError")
	}
	payload := resp.Value.(mcprest.RESTClientErrorPayload)
	if payload.Kind != "cookie_param" || payload.Message == "" {
		t.Errorf("payload = %+v, want Kind=cookie_param with non-empty Message", payload)
	}
}

func TestDefaultErrorPatterns_HeaderParamError_Maps(t *testing.T) {
	h := newTestToolHandle(t, mcprest.DefaultErrorPatterns()...)

	err := rest.HeaderParamError{Name: "X-Request-Id", Value: "not-a-uuid", Err: errors.New("invalid UUID")}
	resp, matched, matchErr := h.ErrorResponseFor(err)
	if matchErr != nil {
		t.Fatalf("unexpected map error: %v", matchErr)
	}
	if !matched {
		t.Fatal("want a match for HeaderParamError")
	}
	payload := resp.Value.(mcprest.RESTClientErrorPayload)
	if payload.Kind != "header_param" || payload.Message == "" {
		t.Errorf("payload = %+v, want Kind=header_param with non-empty Message", payload)
	}
}

func TestDefaultErrorPatterns_ToolRequestMapError_Maps(t *testing.T) {
	h := newTestToolHandle(t, mcprest.DefaultErrorPatterns()...)

	err := mcprest.ToolRequestMapError{Method: "POST", Path: "/greet", Err: errors.New("bad input")}
	resp, matched, matchErr := h.ErrorResponseFor(err)
	if matchErr != nil {
		t.Fatalf("unexpected map error: %v", matchErr)
	}
	if !matched {
		t.Fatal("want a match for ToolRequestMapError")
	}
	payload := resp.Value.(mcprest.RESTClientErrorPayload)
	if payload.Kind != "request_map" || payload.Message == "" {
		t.Errorf("payload = %+v, want Kind=request_map with non-empty Message", payload)
	}
}

func TestDefaultErrorPatterns_ToolResponseMapError_Maps(t *testing.T) {
	h := newTestToolHandle(t, mcprest.DefaultErrorPatterns()...)

	err := mcprest.ToolResponseMapError{Method: "POST", Path: "/greet", Err: errors.New("bad output")}
	resp, matched, matchErr := h.ErrorResponseFor(err)
	if matchErr != nil {
		t.Fatalf("unexpected map error: %v", matchErr)
	}
	if !matched {
		t.Fatal("want a match for ToolResponseMapError")
	}
	payload := resp.Value.(mcprest.RESTClientErrorPayload)
	if payload.Kind != "response_map" || payload.Message == "" {
		t.Errorf("payload = %+v, want Kind=response_map with non-empty Message", payload)
	}
}

func TestDefaultErrorPatterns_CustomPatternDeclaredFirst_Wins(t *testing.T) {
	type customPayload struct{ Note string }
	customCodec := codex.Struct[customPayload](
		codex.RequiredField("note", codex.String(),
			func(p customPayload) string { return p.Note },
			func(p *customPayload, v string) { p.Note = v }),
	)

	// Declare the custom pattern for UnexpectedStatusError BEFORE
	// DefaultErrorPatterns()'s own rule for the same type — first
	// declared rule wins.
	customOpt := apimcp.ErrorPattern[nethttp.UnexpectedStatusError, customPayload](customCodec,
		func(e nethttp.UnexpectedStatusError) (customPayload, error) {
			return customPayload{Note: "custom handling"}, nil
		},
	)
	opts := append([]apimcp.ToolOpt{customOpt}, mcprest.DefaultErrorPatterns()...)
	h := newTestToolHandle(t, opts...)

	err := nethttp.UnexpectedStatusError{Method: "GET", Path: "/x", StatusCode: 500}
	resp, matched, matchErr := h.ErrorResponseFor(err)
	if matchErr != nil {
		t.Fatalf("unexpected map error: %v", matchErr)
	}
	if !matched {
		t.Fatal("want a match")
	}
	payload, ok := resp.Value.(customPayload)
	if !ok {
		t.Fatalf("resp.Value = %T, want customPayload (custom rule should win)", resp.Value)
	}
	if payload.Note != "custom handling" {
		t.Errorf("payload.Note = %q, want %q", payload.Note, "custom handling")
	}
}
