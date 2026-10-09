package stats

import "context"

// Disposition is a handler's PER-MESSAGE RUNTIME outcome signal — a
// DISTINCT concept from a protocol-native Capability's declare-time
// CONFIGURATION (see docs/design/d-0006-protocol-native-capabilities.md's §8).
// Handler signatures stay COMPLETELY UNCHANGED; a handler that wants a
// non-default outcome calls [SetDisposition] inside its own body.
type Disposition int

const (
	// DispositionDefault means no explicit signal was given — the
	// resolving adapter falls back to its own default (see
	// [ResolveDisposition]: nil handler error -> [DispositionAck],
	// non-nil error -> [DispositionNackRequeue]).
	DispositionDefault Disposition = iota
	// DispositionAck acknowledges the message — it will not be
	// redelivered.
	DispositionAck
	// DispositionNackRequeue negatively acknowledges the message and
	// requests redelivery.
	DispositionNackRequeue
	// DispositionNackDiscard negatively acknowledges the message and
	// requests it be discarded (no redelivery).
	DispositionNackDiscard
)

// dispositionBoxKey is the unexported type for the shared Disposition box
// stored in context by [EnsureDispositionBox].
type dispositionBoxKey struct{}

// dispositionBox is the shared, mutable container [SetDisposition]/
// [DispositionFromContext] read/write through — pre-allocated ONCE per
// message dispatch by the owning adapter, mirroring
// [EnsureContextFields]'s/nethttp.WithResponseHeaders's exact
// pre-allocation pattern.
type dispositionBox struct {
	d Disposition
}

// EnsureDispositionBox pre-allocates the shared Disposition box on ctx if
// not already present, and returns the resulting context. Called ONCE by
// an adapter's own dispatch loop, BEFORE the handler runs — an adapter
// with no acknowledgement concept (e.g. MQTT 5) simply never calls this,
// and [SetDisposition] safely no-ops when called against a ctx that was
// never prepared (zero leakage of the concept into adapters that don't
// need it). Idempotent — safe to call more than once on the same ctx
// chain.
func EnsureDispositionBox(ctx context.Context) context.Context {
	if _, ok := ctx.Value(dispositionBoxKey{}).(*dispositionBox); ok {
		return ctx
	}
	return context.WithValue(ctx, dispositionBoxKey{}, &dispositionBox{d: DispositionDefault})
}

// SetDisposition records d into the box already present on ctx — a no-op
// when ctx was never prepared via [EnsureDispositionBox] (mirrors
// [stats.ObserverFromContext]'s no-op-when-absent safety: never panics or
// errors). Call from inside a handler body to signal a non-default
// outcome, e.g. `stats.SetDisposition(ctx,
// stats.DispositionNackRequeue)`.
func SetDisposition(ctx context.Context, d Disposition) {
	box, ok := ctx.Value(dispositionBoxKey{}).(*dispositionBox)
	if !ok {
		return
	}
	box.d = d
}

// DispositionFromContext returns the disposition currently recorded on
// ctx, or [DispositionDefault] if ctx was never prepared or no explicit
// signal was ever set.
func DispositionFromContext(ctx context.Context) Disposition {
	box, ok := ctx.Value(dispositionBoxKey{}).(*dispositionBox)
	if !ok {
		return DispositionDefault
	}
	return box.d
}

// ResolveDisposition returns the FINAL disposition an adapter should act
// on: the explicitly-signaled value from [SetDisposition] when one was
// given, otherwise the default fallback derived from handlerErr — nil
// error resolves to [DispositionAck], non-nil error resolves to
// [DispositionNackRequeue].
func ResolveDisposition(ctx context.Context, handlerErr error) Disposition {
	if d := DispositionFromContext(ctx); d != DispositionDefault {
		return d
	}
	if handlerErr != nil {
		return DispositionNackRequeue
	}
	return DispositionAck
}
