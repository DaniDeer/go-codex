package codex

import (
	"sync"
	"time"
)

// Mutable is a runtime value that can be replaced more than once, each
// replacement validated against the SAME Codec[T] used at construction
// — the "config that can be hot-reloaded without a restart" shape (a
// rotating JWKS key set, a rotating shared API key, a live-tunable
// feature flag). Unlike [Immutable] (set EXACTLY once, panics on a
// second Set), Mutable always holds a valid value from construction
// onward — Get() never panics, and every valid Set replaces the
// current value; an invalid Set leaves the current value UNCHANGED
// (last-good-value-wins) and returns the codec's own validation error.
//
// Reads (Get) vastly outnumber writes (Set) for this container's
// intended use (every request/message reads the current key material;
// reloads happen on a schedule measured in minutes/hours) — guarded by
// sync.RWMutex rather than sync.Mutex for that reason. Construct via
// [NewMutable], not a struct literal, so the codec is always present.
type Mutable[T any] struct {
	mu       sync.RWMutex
	value    T
	codec    Codec[T]
	location string
	// obs is untyped (any, not stats.Observer) — codex has zero
	// dependency on stats. Type-asserted to [ReloadObserver] at the one
	// call site that needs it (see Set) — never embedded directly.
	obs any
}

// MutableOpt configures a [Mutable] at construction time.
type MutableOpt[T any] func(*Mutable[T])

// WithReloadObserver sets the value whose [ReloadObserver] extension
// (if implemented) receives a RecordReload event on every [Mutable.Set]
// call, success or failure. Accepts any value — most callers will pass
// their existing stats.Observer-based implementation directly (it
// satisfies [ReloadObserver] structurally once it defines RecordReload,
// with zero import of stats from codex — see stats.AsReloadObserver
// for bridging a stats.Observer-typed variable). Defaults to nil —
// Mutable works with no Observer at all, same as every other codex
// container.
func WithReloadObserver[T any](obs any) MutableOpt[T] {
	return func(m *Mutable[T]) { m.obs = obs }
}

// NewMutable validates initial against codec and returns a *Mutable[T],
// or an error if initial fails validation — mirrors why [Immutable]'s
// Set returns an error rather than panicking: initial is real runtime
// input (e.g. the FIRST JWKS fetch at startup), not an authored
// constant. location identifies this cell in [ReloadObserver] events
// (e.g. "jwks-signing-keys") — one Mutable instance = one location.
func NewMutable[T any](location string, initial T, codec Codec[T], opts ...MutableOpt[T]) (*Mutable[T], error) {
	if err := codec.Validate(initial); err != nil {
		return nil, err
	}
	m := &Mutable[T]{value: initial, codec: codec, location: location}
	for _, opt := range opts {
		opt(m)
	}
	return m, nil
}

// Get returns the current value — never panics (construction guarantees
// a valid value always exists). Satisfies [Getter][T].
func (m *Mutable[T]) Get() T {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.value
}

// Set validates value against m's codec; on success it atomically
// replaces the current value and returns nil. On failure the current
// value is left UNCHANGED (last-good-value-wins) and the codec's own
// validation error is returned. Either way, if m's configured Observer
// implements [ReloadObserver], RecordReload(m.location, success,
// duration) fires. Satisfies [Setter][T].
func (m *Mutable[T]) Set(value T) error {
	start := time.Now()
	err := m.codec.Validate(value)
	if err == nil {
		m.mu.Lock()
		m.value = value
		m.mu.Unlock()
	}
	if ro, ok := m.obs.(ReloadObserver); ok {
		ro.RecordReload(m.location, err == nil, time.Since(start))
	}
	return err
}
