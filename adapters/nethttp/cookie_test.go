package nethttp_test

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	nethttp "github.com/DaniDeer/go-codex/adapters/nethttp"
	"github.com/DaniDeer/go-codex/api/rest"
	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/validate"
)

// parseCookie parses the Set-Cookie header from a recorder into an http.Cookie.
func parseCookies(rec *httptest.ResponseRecorder) []*http.Cookie {
	resp := rec.Result()
	defer resp.Body.Close() //nolint:errcheck
	return resp.Cookies()
}

func findCookie(cookies []*http.Cookie, name string) *http.Cookie {
	for _, c := range cookies {
		if c.Name == name {
			return c
		}
	}
	return nil
}

func TestSetCookie_defaults(t *testing.T) {
	rec := httptest.NewRecorder()
	if err := nethttp.SetCookie(rec, "session", "abc", nethttp.CookieOptions{}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	cookies := parseCookies(rec)
	c := findCookie(cookies, "session")
	if c == nil {
		t.Fatal("cookie 'session' not found in response")
	}
	if c.Value != "abc" {
		t.Errorf("want value 'abc', got %q", c.Value)
	}
	if c.Path != "/" {
		t.Errorf("want Path '/', got %q", c.Path)
	}
	if !c.Secure {
		t.Error("want Secure=true by default")
	}
	if !c.HttpOnly {
		t.Error("want HttpOnly=true by default")
	}
	if c.SameSite != http.SameSiteStrictMode {
		t.Errorf("want SameSite=Strict, got %v", c.SameSite)
	}
}

func TestSetCookie_Insecure(t *testing.T) {
	rec := httptest.NewRecorder()
	_ = nethttp.SetCookie(rec, "dev", "val", nethttp.CookieOptions{Insecure: true})

	cookies := parseCookies(rec)
	c := findCookie(cookies, "dev")
	if c == nil {
		t.Fatal("cookie not found")
	}
	if c.Secure {
		t.Error("want Secure=false when Insecure=true")
	}
	if !c.HttpOnly {
		t.Error("want HttpOnly still true")
	}
}

func TestSetCookie_AllowJS(t *testing.T) {
	rec := httptest.NewRecorder()
	_ = nethttp.SetCookie(rec, "csrf", "token", nethttp.CookieOptions{AllowJS: true})

	cookies := parseCookies(rec)
	c := findCookie(cookies, "csrf")
	if c == nil {
		t.Fatal("cookie not found")
	}
	if c.HttpOnly {
		t.Error("want HttpOnly=false when AllowJS=true")
	}
	if !c.Secure {
		t.Error("want Secure still true")
	}
}

func TestSetCookie_SameSiteLax(t *testing.T) {
	rec := httptest.NewRecorder()
	_ = nethttp.SetCookie(rec, "pref", "dark", nethttp.CookieOptions{SameSite: http.SameSiteLaxMode})

	cookies := parseCookies(rec)
	c := findCookie(cookies, "pref")
	if c == nil {
		t.Fatal("cookie not found")
	}
	if c.SameSite != http.SameSiteLaxMode {
		t.Errorf("want SameSite=Lax, got %v", c.SameSite)
	}
}

func TestSetCookie_MaxAge(t *testing.T) {
	rec := httptest.NewRecorder()
	_ = nethttp.SetCookie(rec, "session", "val", nethttp.CookieOptions{MaxAge: 3600})

	cookies := parseCookies(rec)
	c := findCookie(cookies, "session")
	if c == nil {
		t.Fatal("cookie not found")
	}
	if c.MaxAge != 3600 {
		t.Errorf("want MaxAge=3600, got %d", c.MaxAge)
	}
}

func TestSetCookie_Path(t *testing.T) {
	rec := httptest.NewRecorder()
	_ = nethttp.SetCookie(rec, "admin", "val", nethttp.CookieOptions{Path: "/admin"})

	cookies := parseCookies(rec)
	c := findCookie(cookies, "admin")
	if c == nil {
		t.Fatal("cookie not found")
	}
	if c.Path != "/admin" {
		t.Errorf("want Path '/admin', got %q", c.Path)
	}
}

func TestSetCookie_Domain(t *testing.T) {
	rec := httptest.NewRecorder()
	_ = nethttp.SetCookie(rec, "global", "val", nethttp.CookieOptions{Domain: "example.com"})

	cookies := parseCookies(rec)
	c := findCookie(cookies, "global")
	if c == nil {
		t.Fatal("cookie not found")
	}
	if c.Domain != "example.com" {
		t.Errorf("want Domain 'example.com', got %q", c.Domain)
	}
}

func TestSetCookie_Codec_valid(t *testing.T) {
	minLen := codex.String().Refine(validate.MinLen(8))
	rec := httptest.NewRecorder()
	err := nethttp.SetCookie(rec, "token", "longenoughtoken", nethttp.CookieOptions{}.WithCodec(minLen))
	if err != nil {
		t.Fatalf("want nil, got %v", err)
	}
	cookies := parseCookies(rec)
	if findCookie(cookies, "token") == nil {
		t.Fatal("cookie not set on valid value")
	}
}

func TestSetCookie_Codec_invalid(t *testing.T) {
	minLen := codex.String().Refine(validate.MinLen(8))
	rec := httptest.NewRecorder()
	err := nethttp.SetCookie(rec, "token", "short", nethttp.CookieOptions{}.WithCodec(minLen))

	var cookieErr rest.CookieParamError
	if !errors.As(err, &cookieErr) {
		t.Fatalf("want CookieParamError, got %T: %v", err, err)
	}
	if cookieErr.Name != "token" {
		t.Errorf("want Name='token', got %q", cookieErr.Name)
	}
	if cookieErr.Value != "short" {
		t.Errorf("want Value='short', got %q", cookieErr.Value)
	}
	// Cookie must NOT be written on validation failure.
	cookies := parseCookies(rec)
	if findCookie(cookies, "token") != nil {
		t.Error("cookie must not be written when codec validation fails")
	}
}

func TestCookieOptions_WithCodec_setsCodec(t *testing.T) {
	c := codex.String().Refine(validate.MinLen(4))
	opts := nethttp.CookieOptions{}.WithCodec(c)
	if opts.Codec == nil {
		t.Fatal("want Codec set, got nil")
	}
}

func TestCookieOptions_WithCodec_returnsDistinctCopy(t *testing.T) {
	c := codex.String().Refine(validate.MinLen(4))
	base := nethttp.CookieOptions{MaxAge: 3600}
	updated := base.WithCodec(c)
	if base.Codec != nil {
		t.Error("WithCodec must not mutate the original")
	}
	if updated.Codec == nil {
		t.Error("WithCodec must set Codec on the returned copy")
	}
	if updated.MaxAge != 3600 {
		t.Error("WithCodec must preserve other fields")
	}
}

func TestSetCookie_Codec_nil_noValidation(t *testing.T) {
	rec := httptest.NewRecorder()
	// Nil Codec: no validation, any value accepted.
	err := nethttp.SetCookie(rec, "session", "", nethttp.CookieOptions{Codec: nil})
	if err != nil {
		t.Fatalf("want nil when Codec is nil, got %v", err)
	}
}

func TestSetCookie_DeleteCookie(t *testing.T) {
	rec := httptest.NewRecorder()
	_ = nethttp.SetCookie(rec, "session", "", nethttp.CookieOptions{MaxAge: -1})

	cookies := parseCookies(rec)
	c := findCookie(cookies, "session")
	if c == nil {
		t.Fatal("cookie not found")
	}
	if c.MaxAge != -1 {
		t.Errorf("want MaxAge=-1 for delete, got %d", c.MaxAge)
	}
}
