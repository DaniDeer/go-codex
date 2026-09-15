// Package observability holds this example's OWN [stats.Observer]
// implementation ([DemoObserver]) — kept as its own package (not folded
// into handlers/) because it is a cross-cutting concern shared across
// every channel/adapter in this example, not domain business logic for
// one specific channel. Mirrors examples/reqreply-api/observability's
// own precedent exactly, adapted for pub/sub's own lifecycle events.
//
// Layering note: Observer involvement belongs ONLY where code actually
// executes:
//   - routes/routes.go's channel declarations are pure metadata — they
//     have NO Observer involvement and this package adds none to them.
//   - The general-purpose observer MIDDLEWARE itself is the shipped,
//     library-owned [events.Observability] (core) / thinned
//     [adapters/mqtt5.Observability]/[adapters/mqtt.Observability] — used
//     directly at the assembly layer (mqtt5broker/zeromqbroker/mqttbroker/
//     demo files) via .SubscribeMW(nil, ...)/.PublishMW(nil, ...), the
//     same call sites the security implementations are wired at. This
//     package does NOT reimplement it.
//   - Every adapter's own subscribe/publish dispatch already calls
//     stats.Observer on every code path (RecordSubscribe/RecordPublish,
//     SecurityObserver, TraceObserver) — unchanged by this package.
//
// Every call site reads/writes the SAME shared Observer via ctx (either
// main.go's one-time [stats.WithObserver] injection, or
// [events.Observability]'s own per-attachment ctx injection) — one
// Observer value, one consistent story across the handler and adapter
// layers.
package observability

import (
	"log/slog"
	"sync"
	"time"

	"github.com/DaniDeer/go-codex/stats"
)

// DemoObserver implements [stats.Observer] (RecordSubscribe/RecordPublish)
// and [stats.SecurityObserver] (RecordSecurityRejection) — mirrors
// examples/stats-observer's ConfigObserver pattern: embed
// [stats.NoopObserver] to satisfy every Observer sub-interface for free,
// override only the ones this demo cares about, and log every event via
// slog so console output visibly shows the adapter layer recording
// through the SAME Observer value every [events.Observability] attachment
// (see demo_observability_middleware.go) was constructed with.
//
// A production observer would call prometheus.CounterVec.With(...).Inc()
// instead of/in addition to logging.
type DemoObserver struct {
	stats.NoopObserver

	logger *slog.Logger

	mu         sync.Mutex
	subscribed int
	published  int
	rejected   int
}

// NewDemoObserver returns a [DemoObserver] logging via logger.
func NewDemoObserver(logger *slog.Logger) *DemoObserver {
	return &DemoObserver{logger: logger}
}

// RecordSubscribe implements [stats.Observer]. Called by the adapter
// layer (adapters/mqtt5, adapters/mqtt, adapters/zeromq) on every
// subscribe dispatch — reachable from any channel this demo attaches
// [events.Observability] to, since it injects the SAME Observer value
// into ctx.
func (o *DemoObserver) RecordSubscribe(topic string, success bool, d time.Duration) {
	o.mu.Lock()
	o.subscribed++
	o.mu.Unlock()
	o.logger.Info("observer.subscribe", "topic", topic, "success", success, "duration", d)
}

// RecordPublish implements [stats.Observer]. Called by the adapter layer
// on every publish dispatch.
func (o *DemoObserver) RecordPublish(topic string, success bool, d time.Duration) {
	o.mu.Lock()
	o.published++
	o.mu.Unlock()
	o.logger.Info("observer.publish", "topic", topic, "success", success, "duration", d)
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
func (o *DemoObserver) Summary() (subscribed, published, rejected int) {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.subscribed, o.published, o.rejected
}
