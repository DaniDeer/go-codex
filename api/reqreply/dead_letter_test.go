package reqreply_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/DaniDeer/go-codex/api/reqreply"
	"github.com/DaniDeer/go-codex/format"
	"github.com/DaniDeer/go-codex/stats"
)

// TestRegister_FailedRegistration_DoesNotOrphanDeadLetterChannel is a
// regression test for a confirmed, previously-real bug: registering a
// DeadLetter channel used to happen BEFORE checkReqReplyParamConflicts/
// the RequestFormats/Formats type-assertion checks — so a route failing
// one of THOSE later checks still left an orphaned DeadLetter channel
// spec entry behind, despite Register() returning a non-nil error.
// Register() must be atomic: either it fully succeeds, or it has NO
// observable side effect on b at all.
func TestRegister_FailedRegistration_DoesNotOrphanDeadLetterChannel(t *testing.T) {
	b := reqreply.NewServer(reqreply.Info{Title: "t", Version: "1"})
	route := reqreply.NewRoute[computeReq, computeResp]("compute/add", reqCodec, respCodec,
		reqreply.DeadLetter("compute/dlq"),
		reqreply.RequestFormats(format.Binary(reqreplyPngCodec)), // type-mismatched -> FormatOptError
	)
	_, err := route.Register(b)
	var fe reqreply.FormatOptError
	if !errors.As(err, &fe) {
		t.Fatalf("want FormatOptError, got %v", err)
	}

	spec, err := b.AsyncAPISpec()
	if err != nil {
		t.Fatalf("AsyncAPISpec: %v", err)
	}
	y, err := spec.MarshalYAML()
	if err != nil {
		t.Fatalf("MarshalYAML: %v", err)
	}
	if strings.Contains(string(y), "compute/dlq") {
		t.Errorf("want NO orphaned compute/dlq channel after a failed Register(), got:\n%s", y)
	}
}

func TestDeadLetterFor_UndeclaredHandle_ReturnsFalse(t *testing.T) {
	b := reqreply.NewServer(reqreply.Info{Title: "Test", Version: "1.0.0"})
	h, err := reqreply.NewRoute[computeReq, computeResp]("compute/no-dlq", reqCodec, respCodec).Register(b)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}

	_, _, ok := h.DeadLetterFor(stats.NoopObserver{}, "compute/no-dlq", []byte("raw"), errors.New("boom"))
	if ok {
		t.Fatal("want ok=false when no DeadLetter declared")
	}
}

func TestDeadLetterFor_ReportsObserver_AndReturnsEncodedEnvelope(t *testing.T) {
	b := reqreply.NewServer(reqreply.Info{Title: "Test", Version: "1.0.0"})
	h, err := reqreply.NewRoute[computeReq, computeResp]("compute/dlq", reqCodec, respCodec,
		reqreply.DeadLetter("compute/dead-letter"),
	).Register(b)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}

	topic, body, ok := h.DeadLetterFor(stats.NoopObserver{}, "compute/dlq", []byte(`{"bad":true}`), errors.New("decode failed"))
	if !ok {
		t.Fatal("want ok=true when DeadLetter is declared")
	}
	if topic != "compute/dead-letter" {
		t.Errorf("want topic=compute/dead-letter, got %q", topic)
	}
	if !strings.Contains(string(body), "decode failed") {
		t.Errorf("want envelope body to contain the error message, got: %s", body)
	}
}

func TestDeadLetter_GlobalDefault_Inherited(t *testing.T) {
	b := reqreply.NewServer(reqreply.Info{Title: "Test", Version: "1.0.0"})
	b.AddGlobalDeadLetter("global/dead-letter")
	h, err := reqreply.NewRoute[computeReq, computeResp]("compute/inherits-global", reqCodec, respCodec).Register(b)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}

	topic, _, ok := h.DeadLetterFor(stats.NoopObserver{}, "compute/inherits-global", nil, errors.New("boom"))
	if !ok || topic != "global/dead-letter" {
		t.Fatalf("want inherited global default topic=global/dead-letter ok=true, got topic=%q ok=%v", topic, ok)
	}
}

func TestDeadLetter_OverriddenPerRoute(t *testing.T) {
	b := reqreply.NewServer(reqreply.Info{Title: "Test", Version: "1.0.0"})
	b.AddGlobalDeadLetter("global/dead-letter")
	h, err := reqreply.NewRoute[computeReq, computeResp]("compute/overrides-global", reqCodec, respCodec,
		reqreply.DeadLetter("compute/own-dead-letter"),
	).Register(b)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}

	topic, _, ok := h.DeadLetterFor(stats.NoopObserver{}, "compute/overrides-global", nil, errors.New("boom"))
	if !ok || topic != "compute/own-dead-letter" {
		t.Fatalf("want per-route override to win, got topic=%q ok=%v", topic, ok)
	}
}

func TestDeadLetter_OptedOut(t *testing.T) {
	b := reqreply.NewServer(reqreply.Info{Title: "Test", Version: "1.0.0"})
	b.AddGlobalDeadLetter("global/dead-letter")
	h, err := reqreply.NewRoute[computeReq, computeResp]("compute/opts-out", reqCodec, respCodec,
		reqreply.DeadLetter(""), // explicit opt-out
	).Register(b)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}

	_, _, ok := h.DeadLetterFor(stats.NoopObserver{}, "compute/opts-out", nil, errors.New("boom"))
	if ok {
		t.Fatal("want ok=false when explicitly opted out of the global default")
	}
}

// This section tests F1's fix (session review finding): DeadLetter now
// generates its own AsyncAPI channel entry, per the roadmap's Topic 4
// "DECIDED this round" section, previously unimplemented.

func TestDeadLetter_RegistersAsyncAPIChannel(t *testing.T) {
	b := reqreply.NewServer(reqreply.Info{Title: "Test", Version: "1.0.0"})
	_, err := reqreply.NewRoute[computeReq, computeResp]("compute/dlq-spec", reqCodec, respCodec,
		reqreply.DeadLetter("compute/dlq-spec/dead-letter").
			WithDescription("Undeliverable compute requests.").
			WithSchemaName("ComputeDeadLetter"),
	).Register(b)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}

	out := mustSpec(t, b)
	if !strings.Contains(out, "compute/dlq-spec/dead-letter:") {
		t.Errorf("want a dead-letter channel key in spec:\n%s", out)
	}
	if !strings.Contains(out, "address: compute/dlq-spec/dead-letter") {
		t.Errorf("want the dead-letter channel address in spec:\n%s", out)
	}
	if !strings.Contains(out, "ComputeDeadLetter:") {
		t.Errorf("want the dead-letter schema registered under its SchemaName in components:\n%s", out)
	}
	if !strings.Contains(out, "Undeliverable compute requests.") {
		t.Errorf("want the dead-letter description in spec:\n%s", out)
	}
}

func TestDeadLetter_GlobalDefault_RegistersOneSharedAsyncAPIChannel(t *testing.T) {
	b := reqreply.NewServer(reqreply.Info{Title: "Test", Version: "1.0.0"})
	b.AddGlobalDeadLetter("global/dlq-spec")
	_, err := reqreply.NewRoute[computeReq, computeResp]("compute/dlq-spec-a", reqCodec, respCodec).Register(b)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	_, err = reqreply.NewRoute[computeReq, computeResp]("compute/dlq-spec-b", reqCodec, respCodec).Register(b)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}

	out := mustSpec(t, b)
	if strings.Count(out, "global/dlq-spec:") != 1 {
		t.Errorf("want the shared global dead-letter channel registered EXACTLY once, got spec:\n%s", out)
	}
}

func TestDeadLetter_NoDeclaration_NoAsyncAPIChannel(t *testing.T) {
	b := reqreply.NewServer(reqreply.Info{Title: "Test", Version: "1.0.0"})
	_, err := reqreply.NewRoute[computeReq, computeResp]("compute/no-dlq-spec", reqCodec, respCodec).Register(b)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}

	out := mustSpec(t, b)
	if strings.Contains(out, "dead-letter") || strings.Contains(out, "DeadLetter") {
		t.Errorf("want NO dead-letter channel in spec when none is declared:\n%s", out)
	}
}
