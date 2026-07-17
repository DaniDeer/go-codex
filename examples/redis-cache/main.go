// Package redis-cache demonstrates the typed cache boundary of
// adapters/redis: an IOPort declared with a CachePattern, a read-through
// GetAdapter, a write-through SetAdapter, the standalone Get/Set plain
// functions (no ports.IOAdapter, no stream — the non-pipeline entrypoint
// GetAdapter/SetAdapter delegate to), a per-key-variable codec
// (CacheKeyParam) rejecting a malformed key var, the Seed warm-restart
// helper, and the standalone ports.NewCache constructor (no port/pipeline
// involved).
//
// # Why a fake client?
//
// All adapter constructors accept the narrow redis.Commands interface, never
// a concrete client — so this example runs WITHOUT a Redis server, using the
// same in-memory fake style as the unit tests. In production, construct the
// client once and wrap it:
//
//	client := redis.NewCommands(goredis.NewUniversalClient(&goredis.UniversalOptions{
//	    Addrs: []string{"localhost:6379"},
//	}))
//
// # Running
//
// go run ./examples/redis-cache
package main

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	adapterredis "github.com/DaniDeer/go-codex/adapters/redis"
	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/format"
	"github.com/DaniDeer/go-codex/ports"
	"github.com/DaniDeer/go-codex/stats"
	"github.com/DaniDeer/go-codex/stream"
	"github.com/DaniDeer/go-codex/validate"
)

// ── Domain ────────────────────────────────────────────────────────────────────

// User is the cached value type — codec-validated on every read AND write.
type User struct {
	ID   string
	Name string
}

var userCodec = codex.Struct[User](
	codex.RequiredField("id", codex.String().Refine(validate.NonEmptyString),
		func(u User) string { return u.ID },
		func(u *User, v string) { u.ID = v },
	),
	codex.RequiredField("name", codex.String().Refine(validate.NonEmptyString),
		func(u User) string { return u.Name },
		func(u *User, v string) { u.Name = v },
	),
)

// UserQuery is the lookup request flowing INTO the cache port.
type UserQuery struct{ ID string }

var queryCodec = codex.Struct[UserQuery](
	codex.RequiredField("id", codex.String(),
		func(q UserQuery) string { return q.ID },
		func(q *UserQuery, v string) { q.ID = v },
	),
)

// numericIDCodec validates that a cache key's "id" variable is all-digits —
// declared on CachePattern.Opts via ports.CacheKeyParam, it rejects a
// malformed key var before any Redis round-trip.
var numericIDCodec = codex.String().Refine(codex.Constraint[string]{
	Name: "numeric",
	Check: func(v string) bool {
		return v != "" && strings.IndexFunc(v, func(r rune) bool { return r < '0' || r > '9' }) < 0
	},
	Message: func(v string) string { return fmt.Sprintf("must be all-digits, got %q", v) },
})

// ── In-memory Commands fake (stands in for a real Redis) ─────────────────────

type memoryCache struct {
	mu    sync.Mutex
	store map[string][]byte
}

func (m *memoryCache) Get(_ context.Context, key string) ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	b, ok := m.store[key]
	if !ok {
		return nil, adapterredis.ErrCacheMiss
	}
	return b, nil
}

func (m *memoryCache) Set(_ context.Context, key string, value []byte, _ time.Duration) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.store[key] = value
	return nil
}

func (m *memoryCache) Del(_ context.Context, keys ...string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, k := range keys {
		delete(m.store, k)
	}
	return nil
}

// ── Observer: count hits/misses/writes ────────────────────────────────────────

type cacheMetrics struct {
	stats.NoopObserver
	hits, misses, writes int
}

func (m *cacheMetrics) RecordCacheHit(_ string, _ time.Duration)  { m.hits++ }
func (m *cacheMetrics) RecordCacheMiss(_ string, _ time.Duration) { m.misses++ }
func (m *cacheMetrics) RecordCacheWrite(_, _ string, _ bool, _ time.Duration) {
	m.writes++
}

func main() {
	metrics := &cacheMetrics{}
	ctx := stats.WithObserver(context.Background(), metrics)
	client := &memoryCache{store: map[string][]byte{}}

	// ── Declare the port ONCE: key template, TTL, wire format ────────────
	// Opts declares a per-key-variable codec: "id" must be all-digits —
	// Cache.BuildKey rejects malformed values BEFORE any Redis round-trip.
	userPort, err := ports.NewIOPort[UserQuery, User]("user-cache",
		queryCodec, userCodec, ports.PortOptions{
			Patterns: []ports.Pattern{
				ports.CachePattern{
					Key: "user:{id}", TTL: 15 * time.Minute,
					Opts: []ports.CacheOpt{
						ports.CacheKeyParam{Name: "id"}.WithCodec(numericIDCodec),
					},
				},
			},
		})
	if err != nil {
		panic(err)
	}
	cache, _ := ports.CacheHandle[User](userPort)
	fmt.Printf("declared: key=%q ttl=%s\n", cache.Key, cache.TTL)

	// ── Section 1: write-through with SetAdapter ─────────────────────────
	fmt.Println("\n─── Section 1: Write-through (SetAdapter)")

	users := make(chan User, 2)
	users <- User{ID: "42", Name: "Ada"}
	users <- User{ID: "7", Name: "Grace"}
	close(users)

	written := adapterredis.SetAdapter[User](client, cache,
		func(u User) map[string]string { return map[string]string{"id": u.ID} },
		adapterredis.SetAdapterOptions{},
	).Transform(ctx, stream.From(ctx, users))
	vals, _ := stream.Collect(ctx, written)
	fmt.Printf("  wrote %d users through the cache (items passed through unchanged)\n", len(vals))

	// ── Section 2: read-through with GetAdapter ──────────────────────────
	fmt.Println("\n─── Section 2: Read-through (GetAdapter)")

	queries := make(chan UserQuery, 3)
	queries <- UserQuery{ID: "42"}  // hit
	queries <- UserQuery{ID: "404"} // miss → skipped silently (default)
	queries <- UserQuery{ID: "7"}   // hit
	close(queries)

	found := adapterredis.GetAdapter[UserQuery, User](client, cache,
		func(q UserQuery) map[string]string { return map[string]string{"id": q.ID} },
		adapterredis.GetAdapterOptions{},
	).Transform(ctx, stream.From(ctx, queries))
	hits, _ := stream.Collect(ctx, found)
	for _, u := range hits {
		fmt.Printf("  hit: %s → %s\n", u.ID, u.Name)
	}

	// ── Section 3: standalone Get/Set — no port/pipeline involved ────────
	fmt.Println("\n─── Section 3: Standalone Get/Set (no ports.IOAdapter, no stream)")

	// Get/Set are plain functions — the building block for an application
	// that doesn't use ports/stream pipelines at all, mirroring
	// format.File.Read/.Write and adapters/sql.Validate. GetAdapter/
	// SetAdapter (Sections 1-2) delegate to these exact functions per item.
	if err := adapterredis.Set(ctx, client, cache, map[string]string{"id": "99"},
		User{ID: "99", Name: "Turing"}, adapterredis.SetOptions{}); err != nil {
		panic(err)
	}
	if v, ok, err := adapterredis.Get(ctx, client, cache, map[string]string{"id": "99"},
		adapterredis.GetOptions{}); err == nil && ok {
		fmt.Printf("  standalone get/set round-trip: %s → %s\n", v.ID, v.Name)
	}

	// ── Section 4: per-key-variable codec rejects a malformed key var ────
	fmt.Println("\n─── Section 4: Per-key-variable codec (CacheKeyParam)")

	if _, _, err := adapterredis.Get(ctx, client, cache, map[string]string{"id": "abc"},
		adapterredis.GetOptions{}); err != nil {
		fmt.Println("  rejected malformed id:", err)
	}

	// ── Section 5: warm restart with Seed + standalone ports.NewCache ────
	fmt.Println("\n─── Section 5: Warm restart (Seed, standalone ports.NewCache)")

	// ports.NewCache builds a Cache[T] with no port/pipeline involved — the
	// same declarative descriptor a CachePattern builds internally, usable
	// directly with any adapters/redis constructor.
	latestCache := ports.NewCache("latest-user", format.JSON(userCodec))
	_ = client.Set(ctx, "latest-user", []byte(`{"id":"42","name":"Ada"}`), 0)

	if v, ok, err := adapterredis.Seed(ctx, client, latestCache, adapterredis.SeedOptions{}); err == nil && ok {
		fmt.Printf("  restored latest value after restart: %s\n", v.Name)
	}

	// ── Observer summary ──────────────────────────────────────────────────
	fmt.Printf("\nmetrics: hits=%d misses=%d writes=%d\n",
		metrics.hits, metrics.misses, metrics.writes)
}
