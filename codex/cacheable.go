package codex

import (
	"sync"
	"time"
)

// Cacheable is an in-process value cell with an explicit validity
// window — a TTL, an explicit [Cacheable.Invalidate] call, or both.
// Builds on the SAME re-validated-cell shape as [Mutable] (every [Set]
// re-validates against the container's Codec[T] and replaces the
// current value on success) and adds exactly one new concept: staleness.
//
// Unlike [Mutable], Get returns (T, bool) — the second value is false
// when the cached value has expired (TTL elapsed) OR been explicitly
// [Cacheable.Invalidate]d — so a caller can implement "stale-while-
// revalidate" (serve the stale value immediately, trigger a background
// Set) instead of the all-or-nothing [Mutable.Get]. Satisfies
// [FreshGetter][T], not [Getter][T] (the two-value return shape doesn't
// match Getter[T]'s single-value contract).
//
// This is deliberately an in-process, single-value memoization cell —
// NOT a competing implementation of [github.com/DaniDeer/go-codex/ports.Cache]/
// adapters/redis. ports.Cache is about a REMOTE, keyed, potentially-shared
// cache backed by an actual Redis server; Cacheable is about ONE local Go
// value a single process wants to avoid recomputing/refetching too often,
// with no network, no key template, and no sharing across processes.
type Cacheable[T any] struct {
	mu        sync.RWMutex
	value     T
	codec     Codec[T]
	ttl       time.Duration // zero = never expires from TTL alone
	expiresAt time.Time
	invalid   bool // explicit Invalidate() flag, independent of TTL
	location  string
	// obs is untyped (any, not stats.Observer) — codex has zero
	// dependency on stats. Type-asserted to [ReloadObserver]/
	// [InvalidateObserver] at each call site — see [Mutable] for the
	// full rationale (shared design).
	obs any
}

// CacheableOpt configures a [Cacheable] at construction time.
type CacheableOpt[T any] func(*Cacheable[T])

// WithCacheableReloadObserver mirrors [WithReloadObserver] — reuses the
// SAME [ReloadObserver]/[InvalidateObserver] interfaces [Mutable] uses,
// so a caller monitoring both containers needs only one Observer
// implementation, wired in with the same call at both construction
// sites. Accepts any value — most callers pass their existing
// stats.Observer-based implementation directly (it satisfies these
// interfaces structurally once it defines RecordReload/RecordInvalidate,
// with zero import of stats from codex — see stats.AsReloadObserver/
// stats.AsInvalidateObserver for bridging a stats.Observer-typed
// variable). Defaults to nil.
func WithCacheableReloadObserver[T any](obs any) CacheableOpt[T] {
	return func(c *Cacheable[T]) { c.obs = obs }
}

// NewCacheable validates initial against codec and returns a
// *Cacheable[T] whose value is fresh for ttl (zero = never expires from
// TTL alone — only [Cacheable.Invalidate] can make it stale). Mirrors
// [NewMutable]'s fallible construction exactly. location identifies this
// cell in [ReloadObserver]/[InvalidateObserver] events.
func NewCacheable[T any](location string, initial T, codec Codec[T], ttl time.Duration, opts ...CacheableOpt[T]) (*Cacheable[T], error) {
	if err := codec.Validate(initial); err != nil {
		return nil, err
	}
	c := &Cacheable[T]{value: initial, codec: codec, ttl: ttl, location: location}
	if ttl > 0 {
		c.expiresAt = time.Now().Add(ttl)
	}
	for _, opt := range opts {
		opt(c)
	}
	return c, nil
}

// isStaleLocked reports staleness — caller must hold c.mu (read or write).
func (c *Cacheable[T]) isStaleLocked() bool {
	if c.invalid {
		return true
	}
	return c.ttl > 0 && time.Now().After(c.expiresAt)
}

// Get returns the current value and whether it is still fresh (true) or
// stale (false — TTL elapsed, or [Cacheable.Invalidate] was called since
// the last successful [Cacheable.Set]). NEVER panics — like [Mutable],
// construction guarantees a valid value always exists; a stale value is
// still returned, just flagged, for stale-while-revalidate callers who
// prefer a stale value over none at all. Satisfies [FreshGetter][T].
func (c *Cacheable[T]) Get() (T, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.value, !c.isStaleLocked()
}

// Set validates value, and on success replaces the current value, resets
// the TTL window, and clears any prior [Cacheable.Invalidate]. On
// failure the current value/freshness is UNCHANGED (last-good-value-
// wins, exactly like [Mutable.Set]) and the codec's own validation error
// is returned. Fires [ReloadObserver.RecordReload] exactly like
// [Mutable.Set] (same interface, same call-site pattern). Satisfies
// [Setter][T].
func (c *Cacheable[T]) Set(value T) error {
	start := time.Now()
	err := c.codec.Validate(value)
	if err == nil {
		c.mu.Lock()
		c.value = value
		c.invalid = false
		if c.ttl > 0 {
			c.expiresAt = time.Now().Add(c.ttl)
		}
		c.mu.Unlock()
	}
	if ro, ok := c.obs.(ReloadObserver); ok {
		ro.RecordReload(c.location, err == nil, time.Since(start))
	}
	return err
}

// Invalidate marks the current value stale immediately, independent of
// the TTL window — for callers who learn a value changed out-of-band (a
// webhook, a Redis keyspace notification). The value itself is NOT
// cleared — [Cacheable.Get] still returns it, with fresh=false — so a
// caller can still serve it (stale-while-revalidate) while triggering a
// refresh. Fires [InvalidateObserver.RecordInvalidate] if c's configured
// Observer implements it — a SEPARATE event from RecordReload, since an
// explicit invalidation is not a failed Set.
func (c *Cacheable[T]) Invalidate() {
	c.mu.Lock()
	c.invalid = true
	c.mu.Unlock()
	if io, ok := c.obs.(InvalidateObserver); ok {
		io.RecordInvalidate(c.location)
	}
}

// IsStale reports whether the current value is stale (TTL elapsed or
// Invalidate called) WITHOUT reading the value itself — for a caller
// that wants to decide whether to refresh before paying for a Get.
func (c *Cacheable[T]) IsStale() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.isStaleLocked()
}
