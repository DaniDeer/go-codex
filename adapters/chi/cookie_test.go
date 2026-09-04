package chi

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/DaniDeer/go-codex/api/rest"
	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/validate"
)

func parseCookiesChi(rec *httptest.ResponseRecorder) []*http.Cookie {
	resp := rec.Result()
	defer resp.Body.Close() //nolint:errcheck
	return resp.Cookies()
}

func findCookieChi(cookies []*http.Cookie, name string) *http.Cookie {
	for _, c := range cookies {
		if c.Name == name {
			return c
		}
	}
	return nil
}

func TestChiSetCookie_defaults(t *testing.T) {
	rec := httptest.NewRecorder()
	if err := SetCookie(rec, "session", "abc", CookieOptions{}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	cookies := parseCookiesChi(rec)
	c := findCookieChi(cookies, "session")
	if c == nil {
		t.Fatal("cookie 'session' not found in response")
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
	if c.Path != "/" {
		t.Errorf("want Path '/', got %q", c.Path)
	}
}

func TestChiSetCookie_Codec_valid(t *testing.T) {
	minLen := codex.String().Refine(validate.MinLen(8))
	rec := httptest.NewRecorder()
	err := SetCookie(rec, "token", "longenoughtoken", CookieOptions{}.WithCodec(minLen))
	if err != nil {
		t.Fatalf("want nil, got %v", err)
	}
	if findCookieChi(parseCookiesChi(rec), "token") == nil {
		t.Fatal("cookie not set on valid value")
	}
}

func TestChiSetCookie_Codec_invalid(t *testing.T) {
	minLen := codex.String().Refine(validate.MinLen(8))
	rec := httptest.NewRecorder()
	err := SetCookie(rec, "token", "short", CookieOptions{}.WithCodec(minLen))

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
	if findCookieChi(parseCookiesChi(rec), "token") != nil {
		t.Error("cookie must not be written when codec validation fails")
	}
}

func TestChiCookieOptions_WithCodec_setsCodec(t *testing.T) {
	c := codex.String().Refine(validate.MinLen(4))
	opts := CookieOptions{}.WithCodec(c)
	if opts.Codec == nil {
		t.Fatal("want Codec set, got nil")
	}
}

func TestChiCookieOptions_WithCodec_returnsDistinctCopy(t *testing.T) {
	c := codex.String().Refine(validate.MinLen(4))
	base := CookieOptions{MaxAge: 3600}
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
