package mcp_test

import (
	"encoding/json"
	"errors"
	"testing"

	apimcp "github.com/DaniDeer/go-codex/api/mcp"
	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/validate"
)

type calcConflictErr struct{ msg string }

func (e calcConflictErr) Error() string { return "conflict: " + e.msg }

type calcOtherErr struct{ msg string }

func (e calcOtherErr) Error() string { return "other: " + e.msg }

type calcErrPayload struct {
	Code    string
	Message string
}

func (e calcErrPayload) Error() string { return "error payload " + e.Code }

var calcErrPayloadCodec = codex.Struct[calcErrPayload](
	codex.RequiredField("code", codex.String().Refine(validate.NonEmptyString),
		func(e calcErrPayload) string { return e.Code },
		func(e *calcErrPayload, v string) { e.Code = v },
	),
	codex.RequiredField("message", codex.String(),
		func(e calcErrPayload) string { return e.Message },
		func(e *calcErrPayload, v string) { e.Message = v },
	),
)

func TestErrorPattern_MappedPayload_MatchAndEncode(t *testing.T) {
	tool := apimcp.NewTool[calcInput, calcOutput]("calc-ep1", calcInputCodec, calcOutputCodec,
		apimcp.ErrorPattern[calcConflictErr, calcErrPayload](calcErrPayloadCodec,
			func(e calcConflictErr) (calcErrPayload, error) {
				return calcErrPayload{Code: "conflict", Message: e.msg}, nil
			},
		),
	)
	handle, err := tool.Register(newBuilder())
	if err != nil {
		t.Fatalf("Register: %v", err)
	}

	resp, matched, mapErr := handle.ErrorResponseFor(calcConflictErr{msg: "duplicate"})
	if mapErr != nil {
		t.Fatalf("unexpected map error: %v", mapErr)
	}
	if !matched {
		t.Fatal("want match, got no match")
	}
	var got calcErrPayload
	if err := json.Unmarshal(resp.Body, &got); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if got.Code != "conflict" || got.Message != "duplicate" {
		t.Errorf("unexpected payload: %+v", got)
	}
}

func TestErrorPattern_DirectMode_TypeAssignable(t *testing.T) {
	tool := apimcp.NewTool[calcInput, calcOutput]("calc-ep2", calcInputCodec, calcOutputCodec,
		apimcp.ErrorPattern[calcErrPayload, calcErrPayload](calcErrPayloadCodec),
	)
	handle, err := tool.Register(newBuilder())
	if err != nil {
		t.Fatalf("Register: %v", err)
	}

	resp, matched, mapErr := handle.ErrorResponseFor(calcErrPayload{Code: "direct", Message: "boom"})
	if mapErr != nil {
		t.Fatalf("unexpected map error: %v", mapErr)
	}
	if !matched {
		t.Fatal("want match")
	}
	var got calcErrPayload
	if err := json.Unmarshal(resp.Body, &got); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if got.Code != "direct" {
		t.Errorf("got %+v", got)
	}
}

func TestErrorPattern_NoMatch_ReturnsFalse(t *testing.T) {
	tool := apimcp.NewTool[calcInput, calcOutput]("calc-ep3", calcInputCodec, calcOutputCodec,
		apimcp.ErrorPattern[calcConflictErr, calcErrPayload](calcErrPayloadCodec,
			func(e calcConflictErr) (calcErrPayload, error) {
				return calcErrPayload{Code: "conflict", Message: e.msg}, nil
			},
		),
	)
	handle, err := tool.Register(newBuilder())
	if err != nil {
		t.Fatalf("Register: %v", err)
	}

	resp, matched, mapErr := handle.ErrorResponseFor(calcOtherErr{msg: "unrelated"})
	if mapErr != nil {
		t.Fatalf("unexpected map error: %v", mapErr)
	}
	if matched {
		t.Fatalf("want no match, got %+v", resp)
	}
}

func TestErrorPattern_Precedence_FirstDeclaredWins(t *testing.T) {
	tool := apimcp.NewTool[calcInput, calcOutput]("calc-ep4", calcInputCodec, calcOutputCodec,
		apimcp.ErrorPattern[calcConflictErr, calcErrPayload](calcErrPayloadCodec,
			func(e calcConflictErr) (calcErrPayload, error) {
				return calcErrPayload{Code: "first", Message: e.msg}, nil
			},
		),
		apimcp.ErrorPattern[calcConflictErr, calcErrPayload](calcErrPayloadCodec,
			func(e calcConflictErr) (calcErrPayload, error) {
				return calcErrPayload{Code: "second", Message: e.msg}, nil
			},
		),
	)
	handle, err := tool.Register(newBuilder())
	if err != nil {
		t.Fatalf("Register: %v", err)
	}

	resp, matched, mapErr := handle.ErrorResponseFor(calcConflictErr{msg: "x"})
	if mapErr != nil {
		t.Fatalf("unexpected map error: %v", mapErr)
	}
	if !matched {
		t.Fatal("want match")
	}
	var got calcErrPayload
	if err := json.Unmarshal(resp.Body, &got); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if got.Code != "first" {
		t.Errorf("got code %q, want first-declared rule to win", got.Code)
	}
}

func TestErrorPattern_MapperError_ReturnsMatchedWithError(t *testing.T) {
	wantErr := errors.New("mapper failed")
	tool := apimcp.NewTool[calcInput, calcOutput]("calc-ep5", calcInputCodec, calcOutputCodec,
		apimcp.ErrorPattern[calcConflictErr, calcErrPayload](calcErrPayloadCodec,
			func(e calcConflictErr) (calcErrPayload, error) {
				return calcErrPayload{}, wantErr
			},
		),
	)
	handle, err := tool.Register(newBuilder())
	if err != nil {
		t.Fatalf("Register: %v", err)
	}

	_, matched, mapErr := handle.ErrorResponseFor(calcConflictErr{msg: "x"})
	if !matched {
		t.Fatal("want matched=true even on mapper error (terminal for this pattern)")
	}
	if !errors.Is(mapErr, wantErr) {
		t.Fatalf("got map error %v, want wrapping %v", mapErr, wantErr)
	}
}

func TestErrorPattern_ClientHandle_carriesRules(t *testing.T) {
	tool := apimcp.NewTool[calcInput, calcOutput]("calc-ep6", calcInputCodec, calcOutputCodec,
		apimcp.ErrorPattern[calcConflictErr, calcErrPayload](calcErrPayloadCodec,
			func(e calcConflictErr) (calcErrPayload, error) {
				return calcErrPayload{Code: "conflict", Message: e.msg}, nil
			},
		),
	)
	handle, err := tool.ClientHandle()
	if err != nil {
		t.Fatalf("ClientHandle: %v", err)
	}

	_, matched, mapErr := handle.ErrorResponseFor(calcConflictErr{msg: "x"})
	if mapErr != nil {
		t.Fatalf("unexpected map error: %v", mapErr)
	}
	if !matched {
		t.Fatal("want ClientHandle to carry error pattern rules like Register")
	}
}
