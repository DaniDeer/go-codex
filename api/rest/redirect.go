package rest

import (
	"fmt"
	"log/slog"
	"net/http"
)

// redirectStatuses is the set of HTTP status codes [Redirect]/[RedirectToSSE]
// accept — the general redirect family, not just 303 (see
// docs/roadmap/rest-typed-redirects.md's Scope decision 1).
var redirectStatuses = map[int]bool{
	http.StatusMovedPermanently:  true, // 301
	http.StatusFound:             true, // 302
	http.StatusSeeOther:          true, // 303
	http.StatusTemporaryRedirect: true, // 307
	http.StatusPermanentRedirect: true, // 308
}

// RedirectStatusError is returned by [Redirect]/[RedirectToSSE] when status
// is not one of the 5 supported redirect codes (301, 302, 303, 307, 308).
type RedirectStatusError struct {
	Status int
}

func (e RedirectStatusError) Error() string {
	return fmt.Sprintf("api/rest: redirect status %d is not one of 301, 302, 303, 307, 308", e.Status)
}

// LogValue implements [slog.LogValuer] for structured logging.
func (e RedirectStatusError) LogValue() slog.Value {
	return slog.GroupValue(
		slog.Int("status", e.Status),
	)
}

// RedirectError signals that a handler wants the caller redirected to
// another registered route, instead of returning a normal response body.
// Returned as the error value from a handler — recognized by
// [adapters/nethttp]/[adapters/chi]'s dispatch via [errors.As], BEFORE
// [ErrorPattern]/[ErrorStatus] matching.
//
// Construct via [Redirect]/[RedirectToSSE] — never directly.
type RedirectError struct {
	// Status is one of 301, 302, 303, 307, 308.
	Status int
	// Location is the fully-resolved target path (via the target
	// route's own BuildPath), ready to write as the Location response
	// header.
	Location string
}

func (e RedirectError) Error() string {
	return fmt.Sprintf("api/rest: redirect %d to %q", e.Status, e.Location)
}

// LogValue implements [slog.LogValuer] for structured logging.
func (e RedirectError) LogValue() slog.Value {
	return slog.GroupValue(
		slog.Int("status", e.Status),
		slog.String("location", e.Location),
	)
}

// RedirectTargetVarError is returned by [Redirect]/[RedirectToSSE] when a
// var required by the target route's own path template is missing, or
// fails the target's own codec constraint.
type RedirectTargetVarError struct {
	Location string // the target route's (unresolved) path template
	Err      error
}

func (e RedirectTargetVarError) Error() string {
	return fmt.Sprintf("api/rest: redirect target %q: %v", e.Location, e.Err)
}

// Unwrap allows [errors.Is]/[errors.As] to reach the underlying error.
func (e RedirectTargetVarError) Unwrap() error { return e.Err }

// LogValue implements [slog.LogValuer] for structured logging.
func (e RedirectTargetVarError) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("location", e.Location),
		slog.Any("err", e.Err),
	)
}

// RedirectMethodMismatchError is returned by [Redirect] when status is 307
// or 308 (method+body MUST be preserved per RFC 9110) and target's Method
// differs from originating's — 301/302/303 have no such constraint (the
// client is allowed to switch to GET).
type RedirectMethodMismatchError struct {
	Status      int
	Originating string // the originating route's Method
	Target      string // the target route's Method
}

func (e RedirectMethodMismatchError) Error() string {
	return fmt.Sprintf("api/rest: redirect %d requires target Method %q to match originating Method %q (RFC 9110 method/body preservation)",
		e.Status, e.Originating, e.Target)
}

// LogValue implements [slog.LogValuer] for structured logging.
func (e RedirectMethodMismatchError) LogValue() slog.Value {
	return slog.GroupValue(
		slog.Int("status", e.Status),
		slog.String("originating_method", e.Originating),
		slog.String("target_method", e.Target),
	)
}

// UnrecognizedRedirectError is returned by [Client.Call]/[Client.Consume]
// when a received redirect's Location does not match any route
// registered on the [*Client] (via a prior [Client.Call]/[Client.Consume]
// or [Client.RegisterRoute]) — carries the raw Location+Status so a
// caller can fall back to a manual follow-up (e.g. their own [http.Get]).
type UnrecognizedRedirectError struct {
	Location string
	Status   int
}

func (e UnrecognizedRedirectError) Error() string {
	return fmt.Sprintf("api/rest: redirect %d to %q does not match any route registered on this Client", e.Status, e.Location)
}

// LogValue implements [slog.LogValuer] for structured logging.
func (e UnrecognizedRedirectError) LogValue() slog.Value {
	return slog.GroupValue(
		slog.Int("status", e.Status),
		slog.String("location", e.Location),
	)
}

// RedirectToStreamUnsupportedError is returned by [Client.Call] when an
// auto-followed redirect resolves to a route registered ONLY as an
// [SSERoute] (a streaming target) — Call's single-decode contract cannot
// service a stream; use [Client.Consume] against the target directly
// instead.
type RedirectToStreamUnsupportedError struct {
	Location string
}

func (e RedirectToStreamUnsupportedError) Error() string {
	return fmt.Sprintf("api/rest: redirect to %q resolves to a streaming (SSERoute) target; use Client.Consume, not Client.Call", e.Location)
}

// LogValue implements [slog.LogValuer] for structured logging.
func (e RedirectToStreamUnsupportedError) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("location", e.Location),
	)
}

// RedirectTargetNotStreamableError is [Client.Consume]'s mirror-image of
// [RedirectToStreamUnsupportedError]: returned when an auto-followed
// redirect resolves to a route registered ONLY as a plain [Route] (a
// non-streaming target) — Consume cannot decode a single value as an
// event stream.
type RedirectTargetNotStreamableError struct {
	Location string
}

func (e RedirectTargetNotStreamableError) Error() string {
	return fmt.Sprintf("api/rest: redirect to %q resolves to a non-streaming (Route) target; use Client.Call, not Client.Consume", e.Location)
}

// LogValue implements [slog.LogValuer] for structured logging.
func (e RedirectTargetNotStreamableError) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("location", e.Location),
	)
}

// RedirectChainTooDeepError is returned by [Client.Call]/[Client.Consume]
// when auto-following a redirect chain exceeds a depth cap — covers both
// unreasonably long chains and cycles (A redirects to B redirects to A)
// with one mechanism, no separate cycle detector. Depth is tracked via a
// context value threaded through repeated follow-up calls, invisible to
// the public Call/Consume signatures. The cap defaults to 10 (mirrors
// net/http's own precedent) and is overridable via
// [ClientCallOptions.MaxRedirects].
type RedirectChainTooDeepError struct {
	Location string
	Depth    int
}

func (e RedirectChainTooDeepError) Error() string {
	return fmt.Sprintf("api/rest: redirect chain exceeded depth %d at %q", e.Depth, e.Location)
}

// LogValue implements [slog.LogValuer] for structured logging.
func (e RedirectChainTooDeepError) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("location", e.Location),
		slog.Int("depth", e.Depth),
	)
}

// redirectSource is satisfied by ANY [Route[Req, Resp]]/[SSERoute[Req, Event]]
// value (for ANY Req/Resp/Event) — [RouteMethod] has no type parameters of
// its own, so this plain, unexported interface lets [Redirect]/
// [RedirectToSSE] accept the CALLING route's own value directly (e.g. the
// package-level `uploadRoute` var a handler closure already has in scope)
// without a second pair of generic type parameters. Mirrors [routable]'s
// own identical, already-established pattern in router.go.
type redirectSource interface {
	RouteMethod() string
}

// redirectTarget is satisfied by *RouteHandle[Req,Resp]/*SSERouteHandle[Req,Event]
// — unexported, purely to share buildRedirect's body without duplicating
// it per target shape.
type redirectTarget interface {
	BuildPath(vars map[string]string) (string, error)
	Method() string
	PathTemplate() string
}

type routeRedirectTarget[Req, Resp any] struct{ h *RouteHandle[Req, Resp] }

func (t routeRedirectTarget[Req, Resp]) BuildPath(vars map[string]string) (string, error) {
	return t.h.BuildPath(vars)
}
func (t routeRedirectTarget[Req, Resp]) Method() string       { return t.h.Descriptor.Method }
func (t routeRedirectTarget[Req, Resp]) PathTemplate() string { return t.h.Descriptor.Path }

type sseRedirectTarget[Req, Event any] struct{ h *SSERouteHandle[Req, Event] }

func (t sseRedirectTarget[Req, Event]) BuildPath(vars map[string]string) (string, error) {
	return t.h.BuildPath(vars)
}
func (t sseRedirectTarget[Req, Event]) Method() string       { return t.h.Descriptor.Method }
func (t sseRedirectTarget[Req, Event]) PathTemplate() string { return t.h.Descriptor.Path }

// buildRedirect is [Redirect]/[RedirectToSSE]'s shared body — target-agnostic
// over the 2 concrete BuildPath/Method-bearing handle shapes via
// [redirectTarget].
func buildRedirect(status int, from redirectSource, target redirectTarget, vars map[string]string) error {
	if !redirectStatuses[status] {
		return RedirectStatusError{Status: status}
	}
	if status == http.StatusTemporaryRedirect || status == http.StatusPermanentRedirect {
		if target.Method() != from.RouteMethod() {
			return RedirectMethodMismatchError{Status: status, Originating: from.RouteMethod(), Target: target.Method()}
		}
	}
	location, err := target.BuildPath(vars)
	if err != nil {
		return RedirectTargetVarError{Location: target.PathTemplate(), Err: err}
	}
	return RedirectError{Status: status, Location: location}
}

// Redirect resolves target's path template against vars (via
// target.ClientHandle().BuildPath) and returns a single error a handler
// returns as its own error value: the [RedirectError] itself on success,
// or a typed [RedirectTargetVarError]/[RedirectMethodMismatchError]/
// [RedirectStatusError] on failure.
//
// from is the route handling THIS request — its own package-level
// [Route]/[SSERoute] value, e.g. the SAME `uploadRoute` the handler
// closure was built from — used only to validate 307/308's
// method-preservation constraint (RFC 9110); ignored for 301/302/303.
//
//	func(ctx context.Context, req Req) (Resp, error) {
//	    var zero Resp
//	    return zero, rest.Redirect(http.StatusSeeOther, uploadRoute, targetRoute, vars)
//	}
func Redirect[TReq, TResp any](status int, from redirectSource, target Route[TReq, TResp], vars map[string]string) error {
	return buildRedirect(status, from, routeRedirectTarget[TReq, TResp]{target.ClientHandle()}, vars)
}

// RedirectToSSE mirrors [Redirect] for an [SSERoute] target.
func RedirectToSSE[TReq, TEvent any](status int, from redirectSource, target SSERoute[TReq, TEvent], vars map[string]string) error {
	return buildRedirect(status, from, sseRedirectTarget[TReq, TEvent]{target.ClientHandle()}, vars)
}
