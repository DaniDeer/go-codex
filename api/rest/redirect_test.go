package rest_test

import (
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	"github.com/DaniDeer/go-codex/api/rest"
)

func TestRedirect_Success(t *testing.T) {
	target := rest.NewRoute[createReq, userResp]("GET", "/users/{id}",
		createReqCodec, userCodec, rest.PathParam{Name: "id"})
	from := rest.NewRoute[createReq, userResp]("GET", "/orders/{id}",
		createReqCodec, userCodec, rest.PathParam{Name: "id"})

	err := rest.Redirect(http.StatusSeeOther, from, target, map[string]string{"id": "f47ac10b"})
	if err == nil {
		t.Fatal("Redirect: want non-nil error (RedirectError), got nil")
	}
	var redirErr rest.RedirectError
	if !errors.As(err, &redirErr) {
		t.Fatalf("Redirect: want RedirectError, got %T: %v", err, err)
	}
	if redirErr.Status != http.StatusSeeOther {
		t.Errorf("Status: want %d, got %d", http.StatusSeeOther, redirErr.Status)
	}
	if redirErr.Location != "/users/f47ac10b" {
		t.Errorf("Location: want /users/f47ac10b, got %q", redirErr.Location)
	}
}

func TestRedirect_MissingVar_ReturnsRedirectTargetVarError(t *testing.T) {
	target := rest.NewRoute[createReq, userResp]("GET", "/users/{id}",
		createReqCodec, userCodec, rest.PathParam{Name: "id"})
	from := rest.NewRoute[createReq, userResp]("GET", "/orders/{id}",
		createReqCodec, userCodec, rest.PathParam{Name: "id"})

	err := rest.Redirect(http.StatusSeeOther, from, target, map[string]string{})
	var varErr rest.RedirectTargetVarError
	if !errors.As(err, &varErr) {
		t.Fatalf("Redirect: want RedirectTargetVarError, got %T: %v", err, err)
	}
	if varErr.Unwrap() == nil {
		t.Error("RedirectTargetVarError.Unwrap(): want non-nil inner error")
	}
}

func TestRedirect_307_MethodMismatch_ReturnsRedirectMethodMismatchError(t *testing.T) {
	target := rest.NewRoute[createReq, userResp]("POST", "/users/{id}",
		createReqCodec, userCodec, rest.PathParam{Name: "id"})
	from := rest.NewRoute[createReq, userResp]("GET", "/orders/{id}",
		createReqCodec, userCodec, rest.PathParam{Name: "id"})

	err := rest.Redirect(http.StatusTemporaryRedirect, from, target, map[string]string{"id": "x"})
	var mmErr rest.RedirectMethodMismatchError
	if !errors.As(err, &mmErr) {
		t.Fatalf("Redirect: want RedirectMethodMismatchError, got %T: %v", err, err)
	}
	if mmErr.Originating != "GET" || mmErr.Target != "POST" {
		t.Errorf("RedirectMethodMismatchError: want Originating=GET Target=POST, got %+v", mmErr)
	}
}

func TestRedirect_307_MethodMatch_Succeeds(t *testing.T) {
	target := rest.NewRoute[createReq, userResp]("POST", "/users/{id}",
		createReqCodec, userCodec, rest.PathParam{Name: "id"})
	from := rest.NewRoute[createReq, userResp]("POST", "/orders/{id}",
		createReqCodec, userCodec, rest.PathParam{Name: "id"})

	err := rest.Redirect(http.StatusTemporaryRedirect, from, target, map[string]string{"id": "x"})
	var redirErr rest.RedirectError
	if !errors.As(err, &redirErr) {
		t.Fatalf("Redirect: want RedirectError (method matches), got %T: %v", err, err)
	}
}

func TestRedirect_InvalidStatus_ReturnsRedirectStatusError(t *testing.T) {
	target := rest.NewRoute[createReq, userResp]("GET", "/users/{id}",
		createReqCodec, userCodec, rest.PathParam{Name: "id"})
	from := rest.NewRoute[createReq, userResp]("GET", "/orders/{id}",
		createReqCodec, userCodec, rest.PathParam{Name: "id"})

	err := rest.Redirect(http.StatusOK, from, target, map[string]string{"id": "x"})
	var statusErr rest.RedirectStatusError
	if !errors.As(err, &statusErr) {
		t.Fatalf("Redirect: want RedirectStatusError, got %T: %v", err, err)
	}
}

func TestRedirectToSSE_Success(t *testing.T) {
	target := rest.NewSSERoute[createReq, userResp]("/stream/{id}",
		createReqCodec, userCodec, rest.PathParam{Name: "id"})
	from := rest.NewRoute[createReq, userResp]("GET", "/orders/{id}",
		createReqCodec, userCodec, rest.PathParam{Name: "id"})

	err := rest.RedirectToSSE(http.StatusSeeOther, from, target, map[string]string{"id": "abc"})
	var redirErr rest.RedirectError
	if !errors.As(err, &redirErr) {
		t.Fatalf("RedirectToSSE: want RedirectError, got %T: %v", err, err)
	}
	if redirErr.Location != "/stream/abc" {
		t.Errorf("Location: want /stream/abc, got %q", redirErr.Location)
	}
}

func TestResponseMeta_Headers_RendersInSpec(t *testing.T) {
	b := rest.NewServer(testInfo)
	_, err := rest.NewRoute[createReq, userResp]("POST", "/orders", createReqCodec, userCodec,
		rest.ResponseMeta{
			Status:      "303",
			Description: "See other",
			Headers: map[string]rest.ResponseMetaHeader{
				"Location": {Description: "The redirect target", Required: true},
			},
		},
	).RegisterHandle(b)
	if err != nil {
		t.Fatalf("RegisterHandle: %v", err)
	}
	doc, err := b.OpenAPISpec()
	if err != nil {
		t.Fatalf("OpenAPISpec: %v", err)
	}
	raw, err := doc.MarshalJSON()
	if err != nil {
		t.Fatalf("MarshalJSON: %v", err)
	}
	spec := string(raw)
	if !strings.Contains(spec, `"303"`) {
		t.Errorf("spec missing 303 response entry: %s", spec)
	}
	if !strings.Contains(spec, `"Location"`) {
		t.Errorf("spec missing Location header entry: %s", spec)
	}
	if !strings.Contains(spec, `"The redirect target"`) {
		t.Errorf("spec missing Location header description: %s", spec)
	}
	// Confirms the default string schema was applied (round 3 finding G4) —
	// the caller left Headers["Location"].Schema unset.
	if !strings.Contains(spec, `"type":"string"`) && !strings.Contains(spec, `"type": "string"`) {
		t.Errorf("spec missing default string schema for Location header: %s", spec)
	}
}

// --- LogValue shape tests ---

func TestRedirectError_LogValue(t *testing.T) {
	err := rest.RedirectError{Status: 303, Location: "/x"}
	requireLogValueGroup(t, err.LogValue(), "status", "location")
}

func TestRedirectTargetVarError_LogValue(t *testing.T) {
	inner := errors.New("boom")
	err := rest.RedirectTargetVarError{Location: "/x/{id}", Err: inner}
	requireLogValueGroup(t, err.LogValue(), "location", "err")
	if err.Unwrap() != inner {
		t.Error("Unwrap: want inner error")
	}
}

func TestRedirectMethodMismatchError_LogValue(t *testing.T) {
	err := rest.RedirectMethodMismatchError{Status: 307, Originating: "GET", Target: "POST"}
	requireLogValueGroup(t, err.LogValue(), "status", "originating_method", "target_method")
}

func TestRedirectStatusError_LogValue(t *testing.T) {
	err := rest.RedirectStatusError{Status: 200}
	requireLogValueGroup(t, err.LogValue(), "status")
}

func TestUnrecognizedRedirectError_LogValue(t *testing.T) {
	err := rest.UnrecognizedRedirectError{Location: "/x", Status: 303}
	requireLogValueGroup(t, err.LogValue(), "status", "location")
}

func TestRedirectToStreamUnsupportedError_LogValue(t *testing.T) {
	err := rest.RedirectToStreamUnsupportedError{Location: "/x"}
	requireLogValueGroup(t, err.LogValue(), "location")
}

func TestRedirectTargetNotStreamableError_LogValue(t *testing.T) {
	err := rest.RedirectTargetNotStreamableError{Location: "/x"}
	requireLogValueGroup(t, err.LogValue(), "location")
}

func TestRedirectChainTooDeepError_LogValue(t *testing.T) {
	err := rest.RedirectChainTooDeepError{Location: "/x", Depth: 11}
	requireLogValueGroup(t, err.LogValue(), "location", "depth")
}

// requireLogValueGroup fails t if lv isn't a slog.KindGroup carrying every
// key in wantKeys — the project's established LogValue test-quality bar
// (see .github/skills/plan-a-new-codex-feature's "Test quality rule").
func requireLogValueGroup(t *testing.T, lv slog.Value, wantKeys ...string) {
	t.Helper()
	if lv.Kind() != slog.KindGroup {
		t.Fatalf("LogValue: want KindGroup, got %v", lv.Kind())
	}
	have := make(map[string]bool)
	for _, a := range lv.Group() {
		have[a.Key] = true
	}
	for _, k := range wantKeys {
		if !have[k] {
			t.Errorf("LogValue: missing key %q", k)
		}
	}
}
