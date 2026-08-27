package nethttp

import (
	"fmt"
	"net/http"
	"time"

	"github.com/DaniDeer/go-codex/middleware"
	"github.com/DaniDeer/go-codex/stats"
)

// ObservabilityMiddleware builds a general-purpose [middleware.Middleware]
// that wraps the ENTIRE call: records [stats.Observer.RecordRequest]
// (method/path/status/duration), drives [stats.TraceObserver.StartSpan]/
// [stats.TraceObserver.EndSpan] if obs implements it, and — after the wrapped
// handler returns — drains [stats.DiagnosticsFromContext] and forwards each
// to [stats.Observer.RecordValidationError]. This is the ONLY place in
// adapters/nethttp that calls into stats.Observer for request-lifecycle
// events (Options.Observer was removed — see docs/roadmap/declarative-middleware.md).
//
// Also injects obs into ctx via [stats.WithObserver] — so a security
// Middleware's Fn (or any downstream code) can resolve the SAME observer via
// [stats.ObserverFromContext] to call [stats.SecurityObserver.RecordSecurityRejection]
// itself (Fn-driven rejection recording is the Fn author's own
// responsibility now — see "L4"/"L12" in docs/roadmap/declarative-middleware.md).
// The credential-FORMAT check inside Handler/SSEHandler ALSO resolves obs
// this same way.
//
// Attach via [Handler]/[Register]'s variadic mws parameter (or
// [rest.WithMiddleware] at declaration time, though observability is not
// spec-relevant so the call-time attachment point is the common case):
//
//	nethttp.Register(mux, handle, fn, nethttp.Options{},
//	    nethttp.ObservabilityMiddleware(obs))
func ObservabilityMiddleware(obs stats.Observer) middleware.Middleware {
	return middleware.Middleware{
		Name: "observability",
		Fn: func(next http.Handler) http.Handler {
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
		},
	}
}
