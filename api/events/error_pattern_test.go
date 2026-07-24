package events_test

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/DaniDeer/go-codex/api/events"
	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/validate"
)

type sensorValidationError struct{ msg string }

func (e sensorValidationError) Error() string { return "sensor validation: " + e.msg }

type sensorOtherError struct{ msg string }

func (e sensorOtherError) Error() string { return "sensor other: " + e.msg }

type sensorErrorPayload struct {
	Code    string
	Message string
}

func (e sensorErrorPayload) Error() string { return "sensor error payload " + e.Code }

var sensorErrorPayloadCodec = codex.Struct[sensorErrorPayload](
	codex.RequiredField("code", codex.String().Refine(validate.NonEmptyString),
		func(e sensorErrorPayload) string { return e.Code },
		func(e *sensorErrorPayload, v string) { e.Code = v },
	),
	codex.RequiredField("message", codex.String(),
		func(e sensorErrorPayload) string { return e.Message },
		func(e *sensorErrorPayload, v string) { e.Message = v },
	),
)

func TestErrorChannel_MappedPayload_MatchAndEncode(t *testing.T) {
	b := events.NewBuilder(testInfo)
	h, err := events.NewChannel[userEvent]("sensors/{id}/data", userEventCodec,
		events.ErrorChannel[sensorValidationError, sensorErrorPayload](
			"sensors/{id}/errors", sensorErrorPayloadCodec,
			func(e sensorValidationError) (sensorErrorPayload, error) {
				return sensorErrorPayload{Code: "validation", Message: e.msg}, nil
			},
		),
	).Register(b)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}

	resp, matched, mapErr := h.ErrorResponseFor(sensorValidationError{msg: "out of range"})
	if mapErr != nil {
		t.Fatalf("unexpected map error: %v", mapErr)
	}
	if !matched {
		t.Fatal("want match, got no match")
	}
	if resp.Topic != "sensors/{id}/errors" {
		t.Errorf("got topic %q, want sensors/{id}/errors", resp.Topic)
	}
	if resp.Action != events.ErrorRespond {
		t.Errorf("got action %q, want respond (default)", resp.Action)
	}
	var got sensorErrorPayload
	if err := json.Unmarshal(resp.Body, &got); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if got.Code != "validation" || got.Message != "out of range" {
		t.Errorf("unexpected payload: %+v", got)
	}
}

func TestErrorChannel_DirectMode_TypeAssignable(t *testing.T) {
	b := events.NewBuilder(testInfo)
	h, err := events.NewChannel[userEvent]("sensors/{id}/data", userEventCodec,
		events.ErrorChannel[sensorErrorPayload, sensorErrorPayload](
			"sensors/{id}/errors", sensorErrorPayloadCodec,
		),
	).Register(b)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}

	resp, matched, mapErr := h.ErrorResponseFor(sensorErrorPayload{Code: "direct", Message: "boom"})
	if mapErr != nil {
		t.Fatalf("unexpected map error: %v", mapErr)
	}
	if !matched {
		t.Fatal("want match")
	}
	var got sensorErrorPayload
	if err := json.Unmarshal(resp.Body, &got); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if got.Code != "direct" {
		t.Errorf("got %+v", got)
	}
}

func TestErrorChannel_NoMatch_ReturnsFalse(t *testing.T) {
	b := events.NewBuilder(testInfo)
	h, err := events.NewChannel[userEvent]("sensors/{id}/data", userEventCodec,
		events.ErrorChannel[sensorValidationError, sensorErrorPayload](
			"sensors/{id}/errors", sensorErrorPayloadCodec,
			func(e sensorValidationError) (sensorErrorPayload, error) {
				return sensorErrorPayload{Code: "validation", Message: e.msg}, nil
			},
		),
	).Register(b)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}

	resp, matched, mapErr := h.ErrorResponseFor(sensorOtherError{msg: "unrelated"})
	if mapErr != nil {
		t.Fatalf("unexpected map error: %v", mapErr)
	}
	if matched {
		t.Fatalf("want no match, got %+v", resp)
	}
}

func TestErrorChannel_Precedence_FirstDeclaredWins(t *testing.T) {
	b := events.NewBuilder(testInfo)
	h, err := events.NewChannel[userEvent]("sensors/{id}/data", userEventCodec,
		events.ErrorChannel[sensorValidationError, sensorErrorPayload](
			"sensors/{id}/errors-first", sensorErrorPayloadCodec,
			func(e sensorValidationError) (sensorErrorPayload, error) {
				return sensorErrorPayload{Code: "first", Message: e.msg}, nil
			},
		),
		events.ErrorChannel[sensorValidationError, sensorErrorPayload](
			"sensors/{id}/errors-second", sensorErrorPayloadCodec,
			func(e sensorValidationError) (sensorErrorPayload, error) {
				return sensorErrorPayload{Code: "second", Message: e.msg}, nil
			},
		),
	).Register(b)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}

	resp, matched, mapErr := h.ErrorResponseFor(sensorValidationError{msg: "x"})
	if mapErr != nil {
		t.Fatalf("unexpected map error: %v", mapErr)
	}
	if !matched {
		t.Fatal("want match")
	}
	if resp.Topic != "sensors/{id}/errors-first" {
		t.Errorf("got topic %q, want first-declared rule to win", resp.Topic)
	}
}

func TestErrorChannel_WithAction_Handle(t *testing.T) {
	b := events.NewBuilder(testInfo)
	h, err := events.NewChannel[userEvent]("sensors/{id}/data", userEventCodec,
		events.ErrorChannel[sensorValidationError, sensorErrorPayload](
			"sensors/{id}/errors", sensorErrorPayloadCodec,
			func(e sensorValidationError) (sensorErrorPayload, error) {
				return sensorErrorPayload{Code: "validation", Message: e.msg}, nil
			},
		).WithAction(events.ErrorHandle),
	).Register(b)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}

	resp, matched, mapErr := h.ErrorResponseFor(sensorValidationError{msg: "x"})
	if mapErr != nil {
		t.Fatalf("unexpected map error: %v", mapErr)
	}
	if !matched {
		t.Fatal("want match")
	}
	if resp.Action != events.ErrorHandle {
		t.Errorf("got action %q, want handle", resp.Action)
	}
}

func TestErrorChannel_WithAction_Log(t *testing.T) {
	b := events.NewBuilder(testInfo)
	h, err := events.NewChannel[userEvent]("sensors/{id}/data", userEventCodec,
		events.ErrorChannel[sensorValidationError, sensorErrorPayload](
			"sensors/{id}/errors", sensorErrorPayloadCodec,
			func(e sensorValidationError) (sensorErrorPayload, error) {
				return sensorErrorPayload{Code: "validation", Message: e.msg}, nil
			},
		).WithAction(events.ErrorLog),
	).Register(b)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}

	resp, matched, mapErr := h.ErrorResponseFor(sensorValidationError{msg: "x"})
	if mapErr != nil {
		t.Fatalf("unexpected map error: %v", mapErr)
	}
	if !matched {
		t.Fatal("want match")
	}
	if resp.Action != events.ErrorLog {
		t.Errorf("got action %q, want log", resp.Action)
	}
}

func TestErrorChannel_MapperError_ReturnsMatchedWithError(t *testing.T) {
	wantErr := errors.New("mapper failed")
	b := events.NewBuilder(testInfo)
	h, err := events.NewChannel[userEvent]("sensors/{id}/data", userEventCodec,
		events.ErrorChannel[sensorValidationError, sensorErrorPayload](
			"sensors/{id}/errors", sensorErrorPayloadCodec,
			func(e sensorValidationError) (sensorErrorPayload, error) {
				return sensorErrorPayload{}, wantErr
			},
		),
	).Register(b)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}

	_, matched, mapErr := h.ErrorResponseFor(sensorValidationError{msg: "x"})
	if !matched {
		t.Fatal("want matched=true even on mapper error (terminal for this pattern)")
	}
	if !errors.Is(mapErr, wantErr) {
		t.Fatalf("got map error %v, want wrapping %v", mapErr, wantErr)
	}
}

func TestErrorChannel_ClientHandle_carriesRules(t *testing.T) {
	h := events.NewChannel[userEvent]("sensors/{id}/data", userEventCodec,
		events.ErrorChannel[sensorValidationError, sensorErrorPayload](
			"sensors/{id}/errors", sensorErrorPayloadCodec,
			func(e sensorValidationError) (sensorErrorPayload, error) {
				return sensorErrorPayload{Code: "validation", Message: e.msg}, nil
			},
		),
	).ClientHandle()

	_, matched, mapErr := h.ErrorResponseFor(sensorValidationError{msg: "x"})
	if mapErr != nil {
		t.Fatalf("unexpected map error: %v", mapErr)
	}
	if !matched {
		t.Fatal("want ClientHandle to carry error channel rules like Register")
	}
}
