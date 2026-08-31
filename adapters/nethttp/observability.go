package nethttp

import (
	"fmt"
	"net/http"
	"time"

	"github.com/DaniDeer/go-codex/stats"
)

// Observability builds a general-purpose `func(http.Handler) http.Handler`
// closure that wraps the ENTIRE call: records [stats.Observer.RecordRequest]
// (method/path/status/duration), drives [stats.TraceObserver.StartSpan]/
// [stats.TraceObserver.EndSpan] if obs implements it, and — after the
// wrapped handler returns — drains [stats.DiagnosticsFromContext] and
// forwards each to [stats.Observer.RecordValidationError]. This is the
// ONLY place in adapters/nethttp that calls into stats.Observer for
// request-lifecycle events.
//
// Returns the BARE closure, not a middleware.ServerImplementation —
// [rest.Route.HandleMW] builds that internally from whatever fn it
// receives (see docs/design/middleware-workflow-simplification.md's
// "Decision: HandleMW/ClientMW unification"). Observability never
// contributes to a route's spec, so pass nil as HandleMW's mw:
//
//	route = route.HandleMW(nil, nethttp.Observability(obs))
//
// Also injects obs into ctx via [stats.WithObserver] — so a security
// implementation's Fn (or any downstream code) can resolve the SAME
// observer via [stats.ObserverFromContext] to call
// [stats.SecurityObserver.RecordSecurityRejection] itself. The
// credential-FORMAT check inside Serve/ServeSSE ALSO resolves obs this
// same way.
func Observability(obs stats.Observer) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			ctx := stats.WithDiagnostics(r.Context())
			ctx = stats.WithObserver(ctx, obs)
			if to, ok := obs.(stats.TraceObserver); ok {
				ctx = to.StartSpan(ctx, "http.request", r.URL.Path)
			}
			sw := &statusResponseWriter{ResponseWriter: w, code: http.StatusOK}
			next.ServeHTTP(sw, r.WithContext(ctx))
			for _, d := range stats.DiagnosticsFromContext(ctx) {
				obs.RecordValidationError(d.Location, d.ConstraintName, d.Field)
			}
			obs.RecordRequest(r.Method, r.URL.Path, sw.code, time.Since(start))
			if to, ok := obs.(stats.TraceObserver); ok {
				var spanErr error   // http.Handler has no direct Go error — derive from
				if sw.code >= 400 { // status, matching TraceObserver's "err is the
					spanErr = fmt.Errorf("http %d", sw.code) // eventual operation error, nil on success" convention
				}
				to.EndSpan(ctx, spanErr)
			}
		})
	}
}
