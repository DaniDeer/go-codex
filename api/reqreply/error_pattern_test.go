package reqreply_test

import (
	"encoding/json"
	"errors"
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
