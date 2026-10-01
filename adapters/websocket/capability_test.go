package websocket

import (
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

// This file closes docs/roadmap/capability-requirement-composition.md's
// Phase 6a verification gap: wsCarrier's Extract* methods were never
// directly unit-tested (only exercised indirectly via existing
// upgrade/dispatch integration tests) — mirrors adapters/nethttp's and
// adapters/chi's identical httpCarrier direct tests.

// TestWsCarrier_ExtractQuery_FirstValueWins confirms wsCarrier.ExtractQuery
// (unlike ExtractQueryMulti) collapses a repeated query key to its first
// value — the documented first-value-wins contract.
func TestWsCarrier_ExtractQuery_FirstValueWins(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/?dryRun=true&dryRun=false&tenant=acme", nil)
	c := wsCarrier{r}
	got := c.ExtractQuery()
	want := map[string]string{"dryRun": "true", "tenant": "acme"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ExtractQuery() = %v, want %v", got, want)
	}
}

// TestWsCarrier_ExtractQueryMulti_PreservesAllValues confirms
// ExtractQueryMulti keeps every value for a repeated query key, unlike
// ExtractQuery's first-value-wins collapse.
func TestWsCarrier_ExtractQueryMulti_PreservesAllValues(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/?tags=a&tags=b&tags=c", nil)
	c := wsCarrier{r}
	got := c.ExtractQueryMulti()
	want := map[string][]string{"tags": {"a", "b", "c"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ExtractQueryMulti() = %v, want %v", got, want)
	}
}

// TestWsCarrier_ExtractHeaders_FirstValueWins confirms
// wsCarrier.ExtractHeaders collapses a multi-value header to its first
// value, mirroring ExtractQuery's contract on the header axis.
func TestWsCarrier_ExtractHeaders_FirstValueWins(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.Header.Add("X-Trace-Id", "trace-1")
	r.Header.Add("X-Trace-Id", "trace-2")
	r.Header.Set("X-Tenant", "acme")
	c := wsCarrier{r}
	got := c.ExtractHeaders()
	if got["X-Trace-Id"] != "trace-1" {
		t.Errorf("ExtractHeaders()[X-Trace-Id] = %q, want %q", got["X-Trace-Id"], "trace-1")
	}
	if got["X-Tenant"] != "acme" {
		t.Errorf("ExtractHeaders()[X-Tenant] = %q, want %q", got["X-Tenant"], "acme")
	}
}

// TestWsCarrier_ExtractCookies_FirstValueWins confirms
// wsCarrier.ExtractCookies collapses a repeated cookie name to its
// first value (Phase 6a Finding 2's cookie-support addition), mirroring
// ExtractQuery/ExtractHeaders' contract.
func TestWsCarrier_ExtractCookies_FirstValueWins(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.AddCookie(&http.Cookie{Name: "session", Value: "first"})
	r.Header.Add("Cookie", "session=second")
	r.AddCookie(&http.Cookie{Name: "tenant", Value: "acme"})
	c := wsCarrier{r}
	got := c.ExtractCookies()
	if got["session"] != "first" {
		t.Errorf("ExtractCookies()[session] = %q, want %q", got["session"], "first")
	}
	if got["tenant"] != "acme" {
		t.Errorf("ExtractCookies()[tenant] = %q, want %q", got["tenant"], "acme")
	}
}
