package middleware

import (
	"context"
	"fmt"
	"log/slog"
	"sync"

	"github.com/DaniDeer/go-codex/codex"
)

// ctxFieldBoxKey is the unexported type for the shared ContextField box
// stored in context by [EnsureContextFields].
type ctxFieldBoxKey struct{}

// contextFieldBox is the shared, mutable container every [ContextField]
// reads/writes through — pre-allocated ONCE per request/call by the owning
// adapter, mirroring the exact pre-allocation pattern already used by
// nethttp.WithResponseHeaders. Because it is a POINTER stored as a ctx
// value, every descendant context derived from the SAME ancestor (any Fn's
// ctx, a handler's internal ctx, a general-purpose middleware's ORIGINAL ctx
// even after its next.ServeHTTP call returns) sees the SAME box — mutating a
// referenced object, rather than replacing an immutable ctx value, is what
// makes the data visible upward too.
type contextFieldBox struct {
	mu     sync.Mutex
	values map[any]any // key (one per declared ContextField) -> decoded value
}

// EnsureContextFields pre-allocates the shared ContextField box on ctx if
// not already present, and returns the resulting context. Called ONCE by
// each adapter's request pipeline (e.g. nethttp/chi's Serve dispatch,
// ports.File.Read/Write, mcpgo's handlers), BEFORE any attached Fn runs.
// Idempotent — safe to call more than once on the same ctx chain.
func EnsureContextFields(ctx context.Context) context.Context {
	if _, ok := ctx.Value(ctxFieldBoxKey{}).(*contextFieldBox); ok {
		return ctx
	}
	return context.WithValue(ctx, ctxFieldBoxKey{}, &contextFieldBox{values: map[any]any{}})
}

// ContextField is a codec-typed, ctx-carried value slot — a middleware
// decodes+verifies via ITS OWN dedicated codec and publishes the typed
// result via [ContextField.Set]; the handler (or any other attached
// middleware, regardless of shape or attachment order) retrieves it
// fully-typed via [ContextField.Get], with zero `any` type-assertion for the
// consumer. Declared ONCE, at package level, shared by every
// producer/consumer that needs this SAME piece of cross-cutting data.
//
// Use this instead of adding security-specific fields to every route's own
// Req type — a route/channel stays free of concerns some deployments never
// even attach.
type ContextField[V any] struct {
	key   any // unique per field
	codec codex.Codec[V]
}

// NewContextField declares a ContextField backed by codec — the "dedicated
// codec" for this piece of cross-cutting data.
func NewContextField[V any](codec codex.Codec[V]) ContextField[V] {
	return ContextField[V]{key: new(int), codec: codec}
}

// Set validates raw via f's codec and writes the decoded value INTO the box
// already present on ctx — mutates in place, and returns only an error (NOT
// a new ctx). Callable from ANY Fn shape (general-purpose,
// security-specific, credential-providing, decorator, session-shaped) since
// none of them need to hand a modified ctx back to anyone — the box IS the
// shared channel. Returns [ContextFieldNotPreparedError] if the owning
// adapter never called [EnsureContextFields].
func (f ContextField[V]) Set(ctx context.Context, raw any) error {
	v, err := f.codec.Decode(raw)
	if err != nil {
		return err
	}
	box, ok := ctx.Value(ctxFieldBoxKey{}).(*contextFieldBox)
	if !ok {
		return ContextFieldNotPreparedError{}
	}
	box.mu.Lock()
	box.values[f.key] = v
	box.mu.Unlock()
	return nil
}

// Get retrieves the value published by [ContextField.Set], from the SAME
// shared box. ok is false if never Set, or if the box was never
// pre-allocated (mirrors stats.ObserverFromContext's no-op-when-absent
// safety — Get never panics or errors).
func (f ContextField[V]) Get(ctx context.Context) (V, bool) {
	var zero V
	box, ok := ctx.Value(ctxFieldBoxKey{}).(*contextFieldBox)
	if !ok {
		return zero, false
	}
	box.mu.Lock()
	defer box.mu.Unlock()
	v, ok := box.values[f.key]
	if !ok {
		return zero, false
	}
	return v.(V), true
}

// ContextFieldNotPreparedError is returned by [ContextField.Set] when the
// owning adapter never called [EnsureContextFields] on ctx before running
// any attached Fn — an adapter-wiring mistake, surfaced loudly rather than
// silently degrading to a no-op write.
type ContextFieldNotPreparedError struct{}

func (e ContextFieldNotPreparedError) Error() string {
	return "middleware: ContextField.Set called before EnsureContextFields prepared ctx"
}

// LogValue implements [slog.LogValuer] for structured logging.
func (e ContextFieldNotPreparedError) LogValue() slog.Value {
	return slog.GroupValue(slog.String("error", fmt.Sprint(e)))
}
