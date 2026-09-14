// Package observability holds this example's OWN [stats.Observer]
// implementation ([DemoObserver]) — kept as its own package (not folded
// into handlers/) because it is a cross-cutting concern shared across
// every route/adapter in this example, not domain business logic for
// one specific route.
//
// Layering note: Observer involvement belongs ONLY where code actually
// executes:
//   - routes/routes.go's Route declarations and routes/middleware.go's
//     Use()-attached middleware SPEC values are pure metadata — they
//     have NO Observer involvement and this package adds none to them.
//   - The general-purpose observer MIDDLEWARE itself is the shipped,
//     library-owned [reqreply.Observability] — used directly at the
//     assembly layer (zeromqserver/zeromqrouterserver/demo files) via
//     .HandleMW(nil, ...)/.ClientMW(nil, ...), the same call sites
//     VerifyBearer is wired at for security. This package does NOT
//     reimplement it.
//   - adapters/mqtt5 and adapters/zeromq's reqreply transports already
//     call stats.Observer on every dispatch path (RecordRequest,
//     SecurityObserver, TraceObserver) — unchanged by this package.
//
// Every call site reads/writes the SAME shared Observer via ctx (either
// main.go's one-time [stats.WithObserver] injection, or
// [reqreply.Observability]'s own per-attachment ctx injection) — one
// Observer value, one consistent story across the handler and adapter
// layers.
package observability

import (
	"log/slog"
	"sync"
	"time"

	"github.com/DaniDeer/go-codex/stats"
)

// DemoObserver implements [stats.Observer] (RecordRequest) and
// [stats.SecurityObserver] (RecordSecurityRejection) — mirrors
// examples/stats-observer's ConfigObserver pattern: embed
// [stats.NoopObserver] to satisfy every Observer sub-interface for free,
// override only the ones this demo cares about, and log every event via
// slog so console output visibly shows the adapter layer (RecordRequest
// from adapters/mqtt5+adapters/zeromq's reqreply transports) recording
// through the SAME Observer value every [reqreply.Observability]
// attachment (see demo_observer_middleware.go) was constructed with.
//
// A production observer would call prometheus.CounterVec.With(...).Inc()
// instead of/in addition to logging.
type DemoObserver struct {
	stats.NoopObserver

	logger *slog.Logger

	mu       sync.Mutex
	requests int
	rejected int
}

// NewDemoObserver returns a [DemoObserver] logging via logger.
func NewDemoObserver(logger *slog.Logger) *DemoObserver {
	return &DemoObserver{logger: logger}
}

// RecordRequest implements [stats.Observer]. Called by the adapter-layer
// reqreply transports (adapters/mqtt5, adapters/zeromq — method is
// "MQTT5-REQ"/"MQTT5-REP"/"ZMQ-REQ"/etc.) on every dispatch; reachable
// from any route/adapter this demo attaches [reqreply.Observability] to,
// since it injects the SAME Observer value into ctx.
func (o *DemoObserver) RecordRequest(method, path string, statusCode int, d time.Duration) {
	o.mu.Lock()
	o.requests++
	o.mu.Unlock()
	o.logger.Info("observer.request", "method", method, "path", path, "status", statusCode, "duration", d)
}

// RecordSecurityRejection implements [stats.SecurityObserver].
func (o *DemoObserver) RecordSecurityRejection(location, scheme string) {
	o.mu.Lock()
	o.rejected++
	o.mu.Unlock()
	o.logger.Warn("observer.security_rejection", "location", location, "scheme", scheme)
}

// Summary returns a snapshot of counts recorded so far — printed at the
// end of main.go's demo run to show the shared Observer really did
// accumulate events from every layer/adapter/demo.
func (o *DemoObserver) Summary() (requests, rejected int) {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.requests, o.rejected
}
