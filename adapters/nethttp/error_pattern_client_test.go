package nethttp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/DaniDeer/go-codex/api/rest"
)

// This file tests Topic 6's client-side ergonomics (see docs/roadmap/
// d-0005-error-handling.md): ErrorPatternAs, HandleErrorPattern/
// Case, and ErrorPatternOpt.Match — all 3 collapse the errors.As +
// type-switch dance into a single conditional.

func TestErrorPatternAs_MatchedPattern_ExtractsTypedPayload(t *testing.T) {
	handle := newClientErrorPatternRoute()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		_, _ = w.Write([]byte(`{"code":"conflict"}`))
	}))
	defer srv.Close()

	_, err := CallWithHandle(context.Background(), srv.Client(), srv.URL,
		handle, createReq{Name: "Alice"}, CallOptions{})

	payload, ok := rest.ErrorPatternAs[clientErrPayload](err)
	if !ok {
		t.Fatalf("want ErrorPatternAs to match, got ok=false for err=%v", err)
	}
	if payload.Code != "conflict" {
		t.Errorf("code = %q, want conflict", payload.Code)
	}
}

func TestErrorPatternAs_WrongType_ReturnsFalse(t *testing.T) {
	handle := newClientErrorPatternRoute()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		_, _ = w.Write([]byte(`{"code":"conflict"}`))
	}))
	defer srv.Close()

	_, err := CallWithHandle(context.Background(), srv.Client(), srv.URL,
		handle, createReq{Name: "Alice"}, CallOptions{})

	type unrelatedType struct{ X int }
	_, ok := rest.ErrorPatternAs[unrelatedType](err)
	if ok {
		t.Error("want ErrorPatternAs to return false for a type mismatch")
	}
}

func TestErrorPatternAs_NoMatch_ReturnsFalse(t *testing.T) {
	_, ok := rest.ErrorPatternAs[clientErrPayload](nil)
	if ok {
		t.Error("want ErrorPatternAs to return false for a nil/unrelated error")
	}
}

func TestHandleErrorPattern_DispatchesFirstMatchingCase(t *testing.T) {
	handle := newClientErrorPatternRoute()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		_, _ = w.Write([]byte(`{"code":"conflict"}`))
	}))
	defer srv.Close()

	_, err := CallWithHandle(context.Background(), srv.Client(), srv.URL,
		handle, createReq{Name: "Alice"}, CallOptions{})

	var gotCode string
	handled := rest.HandleErrorPattern(err,
		rest.Case(func(p clientErrPayload) { gotCode = p.Code }),
	)
	if !handled {
		t.Fatal("want HandleErrorPattern to report handled=true")
	}
	if gotCode != "conflict" {
		t.Errorf("gotCode = %q, want conflict", gotCode)
	}
}

func TestHandleErrorPattern_NoCaseMatches_ReturnsFalse(t *testing.T) {
	handle := newClientErrorPatternRoute()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		_, _ = w.Write([]byte(`{"code":"conflict"}`))
	}))
	defer srv.Close()

	_, err := CallWithHandle(context.Background(), srv.Client(), srv.URL,
		handle, createReq{Name: "Alice"}, CallOptions{})

	type unrelatedType struct{ X int }
	handled := rest.HandleErrorPattern(err,
		rest.Case(func(p unrelatedType) { t.Fatal("must not be called") }),
	)
	if handled {
		t.Error("want handled=false when no Case matches")
	}
}

func TestErrorPatternOpt_Match_SameValueDeclaresAndMatches(t *testing.T) {
	pattern := rest.ErrorPattern[clientErrPayload, clientErrPayload](http.StatusConflict, clientErrPayloadCodec)
	b := rest.NewServer(testInfo)
	handle, err := rest.NewRoute[createReq, userResp]("POST", "/errors/client-call-match",
		createReqCodec, userRespCodec,
		pattern,
	).RegisterHandle(b)
	if err != nil {
		t.Fatalf("RegisterHandle: %v", err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		_, _ = w.Write([]byte(`{"code":"conflict"}`))
	}))
	defer srv.Close()

	_, callErr := CallWithHandle(context.Background(), srv.Client(), srv.URL,
		handle, createReq{Name: "Alice"}, CallOptions{})

	payload, ok := pattern.Match(callErr)
	if !ok {
		t.Fatalf("want pattern.Match to succeed, got ok=false for err=%v", callErr)
	}
	if payload.Code != "conflict" {
		t.Errorf("code = %q, want conflict", payload.Code)
	}
}

func TestErrorPatternOpt_Match_UnrelatedError_ReturnsFalse(t *testing.T) {
	pattern := rest.ErrorPattern[clientErrPayload, clientErrPayload](http.StatusConflict, clientErrPayloadCodec)
	_, ok := pattern.Match(nil)
	if ok {
		t.Error("want Match to return false for a nil/unrelated error")
	}
}
