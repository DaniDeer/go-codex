package reqreply_test

import (
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/DaniDeer/go-codex/api/reqreply"
	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/validate"
)

type computeConflictErr struct{ msg string }

func (e computeConflictErr) Error() string { return "conflict: " + e.msg }

type computeOtherErr struct{ msg string }

func (e computeOtherErr) Error() string { return "other: " + e.msg }

type computeErrPayload struct {
	Code    string
	Message string
}

func (e computeErrPayload) Error() string { return "error payload " + e.Code }

var computeErrPayloadCodec = codex.Struct[computeErrPayload](
	codex.RequiredField("code", codex.String().Refine(validate.NonEmptyString),
		func(e computeErrPayload) string { return e.Code },
		func(e *computeErrPayload, v string) { e.Code = v },
	),
	codex.RequiredField("message", codex.String(),
		func(e computeErrPayload) string { return e.Message },
		func(e *computeErrPayload, v string) { e.Message = v },
	),
)

func TestErrorPattern_MappedPayload_MatchAndEncode(t *testing.T) {
	route := reqreply.NewRoute[computeReq, computeResp]("compute/add", reqCodec, respCodec,
		reqreply.ErrorPattern[computeConflictErr, computeErrPayload](computeErrPayloadCodec,
			func(e computeConflictErr) (computeErrPayload, error) {
				return computeErrPayload{Code: "conflict", Message: e.msg}, nil
			},
		),
	)
	b := newBuilder()
	handle, err := route.Register(b)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}

	resp, matched, mapErr := handle.ErrorResponseFor(computeConflictErr{msg: "duplicate"})
	if mapErr != nil {
		t.Fatalf("unexpected map error: %v", mapErr)
	}
	if !matched {
		t.Fatal("want match, got no match")
	}
	var got computeErrPayload
	if err := json.Unmarshal(resp.Body, &got); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if got.Code != "conflict" || got.Message != "duplicate" {
		t.Errorf("unexpected payload: %+v", got)
	}
}

func TestErrorPattern_DirectMode_TypeAssignable(t *testing.T) {
	route := reqreply.NewRoute[computeReq, computeResp]("compute/add-direct", reqCodec, respCodec,
		reqreply.ErrorPattern[computeErrPayload, computeErrPayload](computeErrPayloadCodec),
	)
	b := newBuilder()
	handle, err := route.Register(b)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}

	resp, matched, mapErr := handle.ErrorResponseFor(computeErrPayload{Code: "direct", Message: "boom"})
	if mapErr != nil {
		t.Fatalf("unexpected map error: %v", mapErr)
	}
	if !matched {
		t.Fatal("want match")
	}
	var got computeErrPayload
	if err := json.Unmarshal(resp.Body, &got); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if got.Code != "direct" {
		t.Errorf("got %+v", got)
	}
}

func TestErrorPattern_NoMatch_ReturnsFalse(t *testing.T) {
	route := reqreply.NewRoute[computeReq, computeResp]("compute/add-nomatch", reqCodec, respCodec,
		reqreply.ErrorPattern[computeConflictErr, computeErrPayload](computeErrPayloadCodec,
			func(e computeConflictErr) (computeErrPayload, error) {
				return computeErrPayload{Code: "conflict", Message: e.msg}, nil
			},
		),
	)
	b := newBuilder()
	handle, err := route.Register(b)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}

	resp, matched, mapErr := handle.ErrorResponseFor(computeOtherErr{msg: "unrelated"})
	if mapErr != nil {
		t.Fatalf("unexpected map error: %v", mapErr)
	}
	if matched {
		t.Fatalf("want no match, got %+v", resp)
	}
}

func TestErrorPattern_Precedence_FirstDeclaredWins(t *testing.T) {
	route := reqreply.NewRoute[computeReq, computeResp]("compute/add-precedence", reqCodec, respCodec,
		reqreply.ErrorPattern[computeConflictErr, computeErrPayload](computeErrPayloadCodec,
			func(e computeConflictErr) (computeErrPayload, error) {
				return computeErrPayload{Code: "first", Message: e.msg}, nil
			},
		).WithCode("first"),
		reqreply.ErrorPattern[computeConflictErr, computeErrPayload](computeErrPayloadCodec,
			func(e computeConflictErr) (computeErrPayload, error) {
				return computeErrPayload{Code: "second", Message: e.msg}, nil
			},
		).WithCode("second"),
	)
	b := newBuilder()
	handle, err := route.Register(b)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}

	resp, matched, mapErr := handle.ErrorResponseFor(computeConflictErr{msg: "x"})
	if mapErr != nil {
		t.Fatalf("unexpected map error: %v", mapErr)
	}
	if !matched {
		t.Fatal("want match")
	}
	var got computeErrPayload
	if err := json.Unmarshal(resp.Body, &got); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if got.Code != "first" {
		t.Errorf("got code %q, want first-declared rule to win", got.Code)
	}
}

func TestErrorPattern_MapperError_ReturnsMatchedWithError(t *testing.T) {
	wantErr := errors.New("mapper failed")
	route := reqreply.NewRoute[computeReq, computeResp]("compute/add-maperr", reqCodec, respCodec,
		reqreply.ErrorPattern[computeConflictErr, computeErrPayload](computeErrPayloadCodec,
			func(e computeConflictErr) (computeErrPayload, error) {
				return computeErrPayload{}, wantErr
			},
		),
	)
	b := newBuilder()
	handle, err := route.Register(b)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}

	_, matched, mapErr := handle.ErrorResponseFor(computeConflictErr{msg: "x"})
	if !matched {
		t.Fatal("want matched=true even on mapper error (terminal for this pattern)")
	}
	if !errors.Is(mapErr, wantErr) {
		t.Fatalf("got map error %v, want wrapping %v", mapErr, wantErr)
	}
}

func TestErrorPattern_ClientHandle_carriesRules(t *testing.T) {
	handle := reqreply.NewRoute[computeReq, computeResp]("compute/add-client", reqCodec, respCodec,
		reqreply.ErrorPattern[computeConflictErr, computeErrPayload](computeErrPayloadCodec,
			func(e computeConflictErr) (computeErrPayload, error) {
				return computeErrPayload{Code: "conflict", Message: e.msg}, nil
			},
		),
	).ClientHandle()

	_, matched, mapErr := handle.ErrorResponseFor(computeConflictErr{msg: "x"})
	if mapErr != nil {
		t.Fatalf("unexpected map error: %v", mapErr)
	}
	if !matched {
		t.Fatal("want ClientHandle to carry error pattern rules like Register")
	}
}

// TestErrorPattern_AutoGeneratesAsyncAPIErrorReply verifies ErrorPattern
// drives the SAME AsyncAPI reply-error channel/operation rendering that
// ErrorReplyMeta previously required declaring separately.
func TestErrorPattern_AutoGeneratesAsyncAPIErrorReply(t *testing.T) {
	route := reqreply.NewRoute[computeReq, computeResp]("compute/add-spec", reqCodec, respCodec,
		reqreply.RouteMeta{OperationID: "computeAddSpec"},
		reqreply.ErrorPattern[computeConflictErr, computeErrPayload](computeErrPayloadCodec).
			WithCode("conflict").
			WithSchemaName("ComputeErrPayload"),
	)
	b := newBuilder()
	_, err := route.Register(b)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	out := mustSpec(t, b)
	if !strings.Contains(out, "computeAddSpecReplyErrorConflict:") {
		t.Errorf("want auto-generated error reply channel key in spec:\n%s", out)
	}
	if !strings.Contains(out, "address: compute/add-spec/reply/error/conflict") {
		t.Errorf("want auto-generated error reply address in spec:\n%s", out)
	}
	if !strings.Contains(out, "ComputeErrPayload:") {
		t.Errorf("want error reply schema registered in components:\n%s", out)
	}
}

// TestErrorPattern_DefaultCode_DerivedFromTypeName verifies the default Code
// (when WithCode is not called) is derived from the error type's name.
func TestErrorPattern_DefaultCode_DerivedFromTypeName(t *testing.T) {
	route := reqreply.NewRoute[computeReq, computeResp]("compute/add-defaultcode", reqCodec, respCodec,
		reqreply.RouteMeta{OperationID: "computeAddDefaultCode"},
		reqreply.ErrorPattern[computeConflictErr, computeErrPayload](computeErrPayloadCodec),
	)
	b := newBuilder()
	_, err := route.Register(b)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	out := mustSpec(t, b)
	if !strings.Contains(out, "address: compute/add-defaultcode/reply/error/computeConflictErr") {
		t.Errorf("want default code derived from type name in spec:\n%s", out)
	}
}

// TestErrorPattern_DuplicateCode_Rejected confirms two ErrorPatterns
// sharing a Code (default, sanitized-type-name derived) are rejected at
// Register time — unlike REST's accepted same-status ambiguity, reqreply
// is a fresh mechanism with no back-compat constraint.
func TestErrorPattern_DuplicateCode_Rejected(t *testing.T) {
	route := reqreply.NewRoute[computeReq, computeResp]("compute/add-dupcode", reqCodec, respCodec,
		reqreply.ErrorPattern[computeConflictErr, computeErrPayload](computeErrPayloadCodec),
		reqreply.ErrorPattern[computeOtherErr, computeErrPayload](computeErrPayloadCodec).
			WithCode("computeConflictErr"), // collides with the FIRST pattern's default code
	)
	b := newBuilder()
	_, err := route.Register(b)
	if err == nil {
		t.Fatal("want error, got nil")
	}
	var dupErr reqreply.DuplicateErrorPatternCodeError
	if !errors.As(err, &dupErr) {
		t.Fatalf("want DuplicateErrorPatternCodeError, got %T: %v", err, err)
	}
	if dupErr.Code != "computeConflictErr" {
		t.Errorf("want Code %q, got %q", "computeConflictErr", dupErr.Code)
	}
}

// TestErrorPattern_ExplicitWithCode_Unique_NoConflict confirms distinct
// explicit codes on the same route do not conflict.
func TestErrorPattern_ExplicitWithCode_Unique_NoConflict(t *testing.T) {
	route := reqreply.NewRoute[computeReq, computeResp]("compute/add-uniquecode", reqCodec, respCodec,
		reqreply.ErrorPattern[computeConflictErr, computeErrPayload](computeErrPayloadCodec).WithCode("a"),
		reqreply.ErrorPattern[computeOtherErr, computeErrPayload](computeErrPayloadCodec).WithCode("b"),
	)
	b := newBuilder()
	if _, err := route.Register(b); err != nil {
		t.Fatalf("Register: %v", err)
	}
}

// TestDecodeErrorFor_HappyPath verifies the client-side counterpart of
// ErrorResponseFor: given the code+body a server would have transmitted
// for a matched pattern, DecodeErrorFor recovers the SAME typed Value.
func TestDecodeErrorFor_HappyPath(t *testing.T) {
	route := reqreply.NewRoute[computeReq, computeResp]("compute/add-decodeerr", reqCodec, respCodec,
		reqreply.ErrorPattern[computeConflictErr, computeErrPayload](computeErrPayloadCodec,
			func(e computeConflictErr) (computeErrPayload, error) {
				return computeErrPayload{Code: "conflict", Message: e.msg}, nil
			},
		).WithCode("conflict"),
	)
	b := newBuilder()
	handle, err := route.Register(b)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}

	serverResp, matched, mapErr := handle.ErrorResponseFor(computeConflictErr{msg: "duplicate"})
	if mapErr != nil || !matched {
		t.Fatalf("server match failed: matched=%v err=%v", matched, mapErr)
	}

	decoded, ok, applyErr := handle.DecodeErrorFor(serverResp.Code, serverResp.Body)
	if applyErr != nil {
		t.Fatalf("DecodeErrorFor applyErr: %v", applyErr)
	}
	if !ok {
		t.Fatal("want match")
	}
	payload, isPayload := decoded.Value.(computeErrPayload)
	if !isPayload {
		t.Fatalf("want decoded.Value to be computeErrPayload, got %T", decoded.Value)
	}
	if payload.Code != "conflict" || payload.Message != "duplicate" {
		t.Errorf("unexpected decoded payload: %+v", payload)
	}
	if decoded.Code != "conflict" {
		t.Errorf("want decoded.Code %q, got %q", "conflict", decoded.Code)
	}
}

// TestDecodeErrorFor_UnknownCode verifies a code not matching any
// declared pattern returns ok=false.
func TestDecodeErrorFor_UnknownCode(t *testing.T) {
	route := reqreply.NewRoute[computeReq, computeResp]("compute/add-decodeerr-unknown", reqCodec, respCodec,
		reqreply.ErrorPattern[computeConflictErr, computeErrPayload](computeErrPayloadCodec).WithCode("conflict"),
	)
	b := newBuilder()
	handle, err := route.Register(b)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}

	_, ok, applyErr := handle.DecodeErrorFor("unknown-code", []byte(`{}`))
	if applyErr != nil {
		t.Fatalf("unexpected applyErr: %v", applyErr)
	}
	if ok {
		t.Fatal("want no match for an unknown code")
	}
}

// TestDecodeErrorFor_EmptyCode verifies an empty code (the plain-text
// fallback path, which never transmits a code) returns ok=false — mirrors
// the "no declared pattern" case rather than accidentally matching.
func TestDecodeErrorFor_EmptyCode(t *testing.T) {
	route := reqreply.NewRoute[computeReq, computeResp]("compute/add-decodeerr-empty", reqCodec, respCodec,
		reqreply.ErrorPattern[computeConflictErr, computeErrPayload](computeErrPayloadCodec).WithCode("conflict"),
	)
	b := newBuilder()
	handle, err := route.Register(b)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}

	_, ok, applyErr := handle.DecodeErrorFor("", []byte(`plain text error`))
	if applyErr != nil {
		t.Fatalf("unexpected applyErr: %v", applyErr)
	}
	if ok {
		t.Fatal("want no match for an empty code")
	}
}

// TestDecodeErrorFor_DecodeFailure verifies a matching code with a
// malformed body returns ok=true with a non-nil applyErr — callers should
// treat this the same as ok=false (fall back to the untyped error).
func TestDecodeErrorFor_DecodeFailure(t *testing.T) {
	route := reqreply.NewRoute[computeReq, computeResp]("compute/add-decodeerr-fail", reqCodec, respCodec,
		reqreply.ErrorPattern[computeConflictErr, computeErrPayload](computeErrPayloadCodec).WithCode("conflict"),
	)
	b := newBuilder()
	handle, err := route.Register(b)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}

	_, ok, applyErr := handle.DecodeErrorFor("conflict", []byte(`{"code": ""}`)) // missing required "message" + empty "code"
	if !ok {
		t.Fatal("want ok=true (code matched) even though decode failed")
	}
	if applyErr == nil {
		t.Fatal("want a non-nil applyErr for malformed body")
	}
}

// TestDecodeErrorFor_NoPatternsDeclared locks DecodeErrorFor's zero-value
// behavior on a handle with no declared ErrorPattern.
func TestDecodeErrorFor_NoPatternsDeclared(t *testing.T) {
	route := reqreply.NewRoute[computeReq, computeResp]("compute/add-decodeerr-none", reqCodec, respCodec)
	b := newBuilder()
	handle, err := route.Register(b)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	_, ok, applyErr := handle.DecodeErrorFor("anything", []byte(`{}`))
	if applyErr != nil {
		t.Fatalf("unexpected applyErr: %v", applyErr)
	}
	if ok {
		t.Fatal("want no match when no patterns declared")
	}
}

// TestDuplicateErrorPatternCodeError_LogValue verifies the new error
// type's LogValue shape.
func TestDuplicateErrorPatternCodeError_LogValue(t *testing.T) {
	err := reqreply.DuplicateErrorPatternCodeError{
		Route: "compute/add", Code: "conflict",
		FirstType: "domain.ConflictError", SecondType: "domain.OtherConflictError",
	}
	if err.Error() == "" {
		t.Error("want non-empty Error() message")
	}
	v := err.LogValue()
	if v.Kind() != slog.KindGroup {
		t.Fatalf("want slog.KindGroup, got %v", v.Kind())
	}
	seen := map[string]bool{}
	for _, a := range v.Group() {
		seen[a.Key] = true
	}
	for _, key := range []string{"route", "code", "first_type", "second_type"} {
		if !seen[key] {
			t.Errorf("want LogValue group to include key %q, got %v", key, v.Group())
		}
	}
}
