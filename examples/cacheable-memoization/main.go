// Package cacheable-memoization demonstrates codex.Cacheable[T] — an
// in-process, TTL/invalidate-aware value cell — for the driving use
// case: memoizing an expensive computation (or any SecurityFunc/
// CredentialFunc result) without a network round trip, while still
// being able to tell a stale value from a fresh one.
//
// Unlike codex.Mutable[T] (always "current," no notion of time),
// Cacheable[T] adds a validity WINDOW: a TTL, an explicit Invalidate()
// call, or both. Get() returns (value, fresh) instead of a bare value,
// so a caller can implement "stale-while-revalidate" — serve the stale
// value immediately, trigger a background refresh — instead of
// blocking on a fresh recompute every time.
//
// Three scenes:
//   - A fresh value read immediately after construction.
//   - TTL expiry making the SAME value stale (still returned, just
//     flagged) until the next Set.
//   - An explicit Invalidate() — e.g. triggered by an upstream webhook
//     or Redis keyspace notification — marking the value stale BEFORE
//     its TTL naturally expires.
//
// # Running
//
// go run ./examples/cacheable-memoization
package main

import (
	"fmt"
	"time"

	"github.com/DaniDeer/go-codex/codex"
)

// reloadObserver logs every Set/Invalidate — the same shape a real
// stats.Observer-based type would satisfy structurally (see
// stats.AsReloadObserver/stats.AsInvalidateObserver for bridging an
// existing stats.Observer value into this position instead).
type reloadObserver struct{}

func (reloadObserver) RecordReload(location string, success bool, _ time.Duration) {
	fmt.Printf("  [observer] reload %q: success=%v\n", location, success)
}

func (reloadObserver) RecordInvalidate(location string) {
	fmt.Printf("  [observer] invalidate %q\n", location)
}

// expensiveComputation simulates a slow/costly recomputation — a
// database aggregate query, a remote API call, a heavy calculation.
func expensiveComputation(n int) int { return n * n }

func main() {
	// ── Scene 1: fresh immediately after construction ────────────────────
	fmt.Println("=== Scene 1: fresh value immediately after construction ===")

	memo, err := codex.NewCacheable("expensive-computation", expensiveComputation(6), codex.Int(),
		50*time.Millisecond, codex.WithCacheableReloadObserver[int](reloadObserver{}))
	if err != nil {
		panic(err)
	}
	value, fresh := memo.Get()
	fmt.Printf("value=%d fresh=%v\n", value, fresh)

	// ── Scene 2: TTL expiry — stale, but still served ────────────────────
	fmt.Println("\n=== Scene 2: TTL expiry (stale-while-revalidate) ===")

	time.Sleep(60 * time.Millisecond)
	value, fresh = memo.Get()
	fmt.Printf("value=%d fresh=%v (still served, just flagged)\n", value, fresh)

	// A caller can decide: serve the stale value immediately, refresh now.
	if !fresh {
		if err := memo.Set(expensiveComputation(7)); err != nil {
			panic(err)
		}
	}
	value, fresh = memo.Get()
	fmt.Printf("after refresh: value=%d fresh=%v\n", value, fresh)

	// ── Scene 3: explicit Invalidate before TTL naturally expires ────────
	fmt.Println("\n=== Scene 3: explicit Invalidate (e.g. a webhook fired) ===")

	memo.Invalidate()
	value, fresh = memo.Get()
	fmt.Printf("value=%d fresh=%v (marked stale immediately, value unchanged)\n", value, fresh)
	fmt.Printf("IsStale() = %v\n", memo.IsStale())
}
