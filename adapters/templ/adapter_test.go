package templ_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	atempl "github.com/a-h/templ"

	adapttempl "github.com/DaniDeer/go-codex/adapters/templ"
	"github.com/DaniDeer/go-codex/api/rest"
	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/validate"

	nethttp "github.com/DaniDeer/go-codex/adapters/nethttp"
	"github.com/DaniDeer/go-codex/format"
)

// ── Test domain types ─────────────────────────────────────────────────────────

type pageReq struct{ Query string }
type pageProps struct {
	Title string
	Items []string
}

var pageReqCodec = codex.Struct[pageReq](
	codex.Field[pageReq, string]{
		Name:  "query",
		Codec: codex.String(),
		Get:   func(r pageReq) string { return r.Query },
		Set:   func(r *pageReq, v string) { r.Query = v },
	},
)

var pagePropsCodec = codex.Struct[pageProps](
	codex.Field[pageProps, string]{
		Name:     "title",
		Codec:    codex.String().Refine(validate.NonEmptyString),
		Required: true,
		Get:      func(p pageProps) string { return p.Title },
		Set:      func(p *pageProps, v string) { p.Title = v },
	},
	codex.Field[pageProps, []string]{
		Name:     "items",
		Codec:    codex.SliceOf(codex.String()),
		Required: true,
		Get:      func(p pageProps) []string { return p.Items },
		Set:      func(p *pageProps, vs []string) { p.Items = vs },
	},
)

func pageComponent(p pageProps) atempl.Component {
	return atempl.ComponentFunc(func(_ context.Context, w io.Writer) error {
		_, err := io.WriteString(w, "<h1>"+p.Title+"</h1>")
		return err
	})
}

// ── Helpers ───────────────────────────────────────────────────────────────────

func buildRoute(t *testing.T) *rest.RouteHandle[pageReq, pageProps] {
	t.Helper()
	b := rest.NewBuilder(rest.Info{Title: "Test", Version: "1.0.0"})
	route, err := rest.AddRoute[pageReq, pageProps](b, "GET", "/page",
		pageReqCodec, pagePropsCodec,
		rest.RouteConfig{OperationID: "page"},
		adapttempl.Format(pagePropsCodec, pageComponent),
		format.JSON(pagePropsCodec),
	)
	if err != nil {
		t.Fatalf("AddRoute: %v", err)
	}
	return route
}

func get(t *testing.T, srv *httptest.Server, path, accept string) (int, string) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, srv.URL+path, nil) //nolint:noctx
	if accept != "" {
		req.Header.Set("Accept", accept)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	return resp.StatusCode, string(body)
}

// ── Tests ─────────────────────────────────────────────────────────────────────

func TestFormat_HTMLResponse(t *testing.T) {
	route := buildRoute(t)
	mux := http.NewServeMux()
	nethttp.Register(mux, route, func(_ context.Context, _ pageReq) (pageProps, error) {
		return pageProps{Title: "Hello", Items: []string{"a", "b"}}, nil
	}, nethttp.Options{})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	status, body := get(t, srv, "/page", "text/html")
	if status != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", status, body)
	}
	if !strings.Contains(body, "<h1>Hello</h1>") {
		t.Errorf("expected HTML component in body, got: %s", body)
	}
}

func TestFormat_ContentTypeHeader(t *testing.T) {
	route := buildRoute(t)
	mux := http.NewServeMux()
	nethttp.Register(mux, route, func(_ context.Context, _ pageReq) (pageProps, error) {
		return pageProps{Title: "T", Items: []string{}}, nil
	}, nethttp.Options{})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/page", nil) //nolint:noctx
	req.Header.Set("Accept", "text/html")
	resp, _ := http.DefaultClient.Do(req)
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()

	ct := resp.Header.Get("Content-Type")
	if !strings.HasPrefix(ct, "text/html") {
		t.Errorf("expected text/html Content-Type, got: %s", ct)
	}
}

func TestFormat_JSONFallback(t *testing.T) {
	// Same route, same handler — JSON format served when Accept is application/json.
	route := buildRoute(t)
	mux := http.NewServeMux()
	nethttp.Register(mux, route, func(_ context.Context, _ pageReq) (pageProps, error) {
		return pageProps{Title: "JSON", Items: []string{"x"}}, nil
	}, nethttp.Options{})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	status, body := get(t, srv, "/page", "application/json")
	if status != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", status, body)
	}
	if !strings.Contains(body, `"title"`) {
		t.Errorf("expected JSON in body, got: %s", body)
	}
}

func TestFormat_InvalidPropsReturn500(t *testing.T) {
	// Handler returns props that fail Refine constraints → 500 before render.
	route := buildRoute(t)
	mux := http.NewServeMux()
	nethttp.Register(mux, route, func(_ context.Context, _ pageReq) (pageProps, error) {
		return pageProps{Title: "", Items: []string{}}, nil // empty title fails NonEmptyString
	}, nethttp.Options{})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	status, _ := get(t, srv, "/page", "text/html")
	if status != http.StatusInternalServerError {
		t.Errorf("expected 500 for invalid props, got %d", status)
	}
}

func TestFormat_HandlerErrorReturn500(t *testing.T) {
	route := buildRoute(t)
	mux := http.NewServeMux()
	nethttp.Register(mux, route, func(_ context.Context, _ pageReq) (pageProps, error) {
		return pageProps{}, errors.New("service unavailable")
	}, nethttp.Options{})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	status, _ := get(t, srv, "/page", "text/html")
	if status != http.StatusInternalServerError {
		t.Errorf("expected 500 for handler error, got %d", status)
	}
}

func TestFormat_DecodeNotSupportedError(t *testing.T) {
	f := adapttempl.Format(pagePropsCodec, pageComponent)
	_, err := f.Unmarshal([]byte("some html"))
	if err == nil {
		t.Fatal("expected error from Unmarshal, got nil")
	}
	var notSupported adapttempl.DecodeNotSupportedError
	if !errors.As(err, &notSupported) {
		t.Errorf("expected DecodeNotSupportedError, got: %T %v", err, err)
	}
}

func TestFormat_ValidateUsesCodec(t *testing.T) {
	f := adapttempl.Format(pagePropsCodec, pageComponent)
	if err := f.Validate(pageProps{Title: "ok", Items: []string{}}); err != nil {
		t.Errorf("expected no error for valid props, got: %v", err)
	}
	if err := f.Validate(pageProps{Title: "", Items: []string{}}); err == nil {
		t.Error("expected constraint error for empty title, got nil")
	}
}

func TestFormat_SameHandlerServesHTMLAndJSON(t *testing.T) {
	// Demonstrates handler reuse: one handler, two formats.
	route := buildRoute(t)
	mux := http.NewServeMux()
	nethttp.Register(mux, route, func(_ context.Context, _ pageReq) (pageProps, error) {
		return pageProps{Title: "Shared", Items: []string{"item1"}}, nil
	}, nethttp.Options{})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	htmlStatus, htmlBody := get(t, srv, "/page", "text/html")
	jsonStatus, jsonBody := get(t, srv, "/page", "application/json")

	if htmlStatus != 200 || !strings.Contains(htmlBody, "<h1>") {
		t.Errorf("HTML: status=%d body=%s", htmlStatus, htmlBody)
	}
	if jsonStatus != 200 || !strings.Contains(jsonBody, `"title"`) {
		t.Errorf("JSON: status=%d body=%s", jsonStatus, jsonBody)
	}
}
