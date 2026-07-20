package redis

import (
	"context"
	"errors"
	"time"

	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/ports"
	"github.com/DaniDeer/go-codex/stats"
	gstream "github.com/DaniDeer/go-codex/stream"
)

// ── GetAdapter ────────────────────────────────────────────────────────────────

// GetAdapterOptions configures [GetAdapter].
type GetAdapterOptions struct {
	// MissIsError routes a cache miss to Stream.Errors as a [CacheError]
	// wrapping [ErrCacheMiss]. Default false: a miss emits nothing for the
	// item (the IOAdapter 0..N contract) — the idiomatic read-through shape
	// where downstream handles only cache hits.
	MissIsError bool
	// Buffer sizes the output channels. Default 0 (unbuffered).
	Buffer int
	// Observer receives [stats.CacheObserver] hit/miss events and per-field
	// validation reports. Resolved from ctx when nil.
	Observer stats.Observer
}

// GetAdapter returns a [ports.IOAdapter] that looks each Req up in the cache.
// The key is built from cache.Key ([ports.CacheHandle]) with vars extracted
// by keyFn; the stored bytes are decoded and codec-validated through
// cache.Format. Use with [ports.IOPort.Bind]:
//
//	cacheHandle, _ := ports.CacheHandle[User](userPort)
//	userPort.Bind(ctx, redis.GetAdapter(client, cacheHandle,
//	    func(q UserQuery) map[string]string { return map[string]string{"id": q.ID} },
//	    redis.GetAdapterOptions{}))
//
// Hit → decoded Resp downstream + RecordCacheHit. Miss → skip (or
// [CacheError] wrapping [ErrCacheMiss] when MissIsError) + RecordCacheMiss.
// Key-build, transport, and decode failures → [CacheError] on Stream.Errors.
//
// Looks up via [GetMerged] — when cache declares merge-capable key params
// (via [ports.NewCacheKeyParam]), the vars derived from keyFn(Req) are
// ADDITIONALLY merged into the decoded Resp (e.g. a key-derived id is
// populated onto Resp automatically). Identical to a bare [Get] when cache
// declares no merge fields.
func GetAdapter[Req, Resp any](
	client Commands,
	cache ports.Cache[Resp],
	keyFn func(Req) map[string]string,
	opts GetAdapterOptions,
) ports.IOAdapter[Req, Resp] {
	return &redisGetAdapter[Req, Resp]{client: client, cache: cache, keyFn: keyFn, opts: opts}
}

type redisGetAdapter[Req, Resp any] struct {
	client Commands
	cache  ports.Cache[Resp]
	keyFn  func(Req) map[string]string
	opts   GetAdapterOptions
}

func (a *redisGetAdapter[Req, Resp]) AdapterName() string { return "redis.GetAdapter" }

func (a *redisGetAdapter[Req, Resp]) Transform(ctx context.Context, src gstream.Stream[Req]) gstream.Stream[Resp] {
	obs := a.opts.Observer
	if obs == nil {
		obs = stats.ObserverFromContext(ctx)
	}
	getOpts := GetOptions{MissIsError: a.opts.MissIsError, Observer: obs}

	outCh := make(chan Resp, a.opts.Buffer)
	errCh := make(chan error, a.opts.Buffer)

	emitErr := func(err error) bool {
		select {
		case errCh <- err:
			return true
		case <-ctx.Done():
			return false
		}
	}

	go func() {
		defer close(outCh)
		defer close(errCh)
		valCh := src.Values
		srcErrCh := src.Errors
		for valCh != nil || srcErrCh != nil {
			select {
			case <-ctx.Done():
				return
			case v, ok := <-valCh:
				if !ok {
					valCh = nil
					continue
				}
				resp, hit, err := GetMerged(ctx, a.client, a.cache, a.keyFn(v), getOpts)
				if err != nil {
					if !emitErr(err) {
						return
					}
					continue
				}
				if !hit {
					continue // miss, not an error (default MissIsError=false)
				}
				select {
				case outCh <- resp:
				case <-ctx.Done():
					return
				}
			case e, ok := <-srcErrCh:
				if !ok {
					srcErrCh = nil
					continue
				}
				if !emitErr(e) {
					return
				}
			}
		}
	}()
	return gstream.Stream[Resp]{Values: outCh, Errors: errCh}
}

// ── Get ───────────────────────────────────────────────────────────────────────

// GetOptions configures [Get].
type GetOptions struct {
	// MissIsError, when true, returns a [CacheError] wrapping [ErrCacheMiss]
	// on a cache miss instead of (zero, false, nil).
	MissIsError bool
	// Observer receives [stats.CacheObserver] hit/miss events and per-field
	// validation reports. Resolved from ctx when nil.
	Observer stats.Observer
}

// Get looks up a single value in the cache — full codec validation (key
// vars AND value), no [ports.IOAdapter], no [gstream.Stream] involved. This
// is the plain-function standalone entrypoint for a non-pipeline
// application, mirroring [ports.File.Read]/[adapters/sql.Validate]:
// [ports.Cache] is the declarative descriptor (built via [ports.NewCache] or
// a [ports.CachePattern]), and Get is the concrete redis implementation of
// a read against it — the same relationship [ports.File] has to its
// Read/Write methods, or a route handle has to [adapters/nethttp.Call].
//
//	v, ok, err := redis.Get(ctx, client, userCache,
//	    map[string]string{"id": userID}, redis.GetOptions{})
//
// [GetAdapter] delegates to Get per item — calling Get directly and driving
// [GetAdapter] through a bound [ports.IOPort] produce identical behavior.
//
// Returns (zero, false, nil) on a miss by default (an empty cache is not an
// error); set MissIsError to get a [CacheError] wrapping [ErrCacheMiss]
// instead. Key-build, transport, and decode failures return [CacheError].
func Get[T any](ctx context.Context, client Commands, cache ports.Cache[T], vars map[string]string, opts GetOptions) (T, bool, error) {
	var zero T
	obs := opts.Observer
	if obs == nil {
		obs = stats.ObserverFromContext(ctx)
	}
	co, hasCO := obs.(stats.CacheObserver)

	key, err := cache.BuildKey(vars)
	if err != nil {
		return zero, false, CacheError{Key: cache.Key, Op: "get", Err: err}
	}
	start := time.Now()
	raw, err := client.Get(ctx, key)
	if err != nil {
		if isMiss(err) {
			if hasCO {
				co.RecordCacheMiss(key, time.Since(start))
			}
			if opts.MissIsError {
				return zero, false, CacheError{Key: key, Op: "get", Err: ErrCacheMiss}
			}
			return zero, false, nil
		}
		return zero, false, CacheError{Key: key, Op: "get", Err: err}
	}
	v, err := cache.Format.Unmarshal(raw)
	if err != nil {
		stats.ReportErrors(obs, "payload", err)
		return zero, false, CacheError{Key: key, Op: "get", Err: err}
	}
	if hasCO {
		co.RecordCacheHit(key, time.Since(start))
	}
	return v, true, nil
}

// GetMerged is the decode-merge convenience: it looks up exactly like [Get],
// then ADDITIONALLY merges vars into the SAME returned value via
// [codex.DecodeVars], using the merge-capable fields registered via
// [ports.NewCacheKeyParam] — mirrors [ports.File.ReadMerged]/
// [events.ChannelHandle.DecodeMerged] for the cache boundary.
//
// Additive — [Get] is unchanged; GetMerged behaves identically to a bare
// Get when the cache declares no merge-capable key params
// ([ports.Cache.MergeFields] is empty) or on a miss (nothing to merge into).
//
// Example — key template "user:{id}" declared with [ports.NewCacheKeyParam],
// so the extracted id is merged into the returned struct's own field:
//
//	user, ok, err := redis.GetMerged(ctx, client, userCache, map[string]string{"id": id}, redis.GetOptions{})
//	// ok && user.ID == id, no manual assignment needed.
func GetMerged[T any](ctx context.Context, client Commands, cache ports.Cache[T], vars map[string]string, opts GetOptions) (T, bool, error) {
	v, hit, err := Get(ctx, client, cache, vars, opts)
	if err != nil || !hit {
		return v, hit, err
	}
	mergeFields := cache.MergeFields()
	if len(mergeFields) == 0 {
		return v, hit, nil
	}
	if err := codex.DecodeVars(&v, vars, mergeFields...); err != nil {
		var zero T
		return zero, false, err
	}
	return v, true, nil
}

// ── SetAdapter ────────────────────────────────────────────────────────────────

// SetAdapterOptions configures [SetAdapter] and [DrainSetAdapter].
type SetAdapterOptions struct {
	// TTL overrides the cache handle's declared TTL when non-zero.
	TTL time.Duration
	// Buffer sizes the output channels ([SetAdapter] only). Default 0.
	Buffer int
	// OnError receives each write error ([DrainSetAdapter] only — SetAdapter
	// routes errors to its output Stream.Errors).
	OnError func(error)
	// Observer receives [stats.CacheObserver] write events. Resolved from
	// ctx when nil.
	Observer stats.Observer
}

// SetAdapter returns a [ports.IOAdapter] that writes each item to the cache
// (write-through) and passes it through unchanged. Encode and write failures
// go to Stream.Errors as [CacheError] — the item is STILL passed through:
// a cache write failure must not drop pipeline data.
//
//	port.Bind(ctx, redis.SetAdapter(client, cacheHandle,
//	    func(u User) map[string]string { return map[string]string{"id": u.ID} },
//	    redis.SetAdapterOptions{}))
//
// keyFn may be nil when cache declares merge-capable key params (via
// [ports.NewCacheKeyParam]): vars are then derived PER-ITEM from each
// item's own merge fields automatically via [SetHandle] — the same "one
// struct, one call" convenience [mqtt5.PublishHandle] provides. Pass a
// non-nil keyFn to keep building the map yourself (e.g. no merge fields
// declared, or vars come from a field the cached type doesn't have).
func SetAdapter[T any](
	client Commands,
	cache ports.Cache[T],
	keyFn func(T) map[string]string,
	opts SetAdapterOptions,
) ports.IOAdapter[T, T] {
	return &redisSetAdapter[T]{client: client, cache: cache, keyFn: keyFn, opts: opts}
}

type redisSetAdapter[T any] struct {
	client Commands
	cache  ports.Cache[T]
	keyFn  func(T) map[string]string
	opts   SetAdapterOptions
}

func (a *redisSetAdapter[T]) AdapterName() string { return "redis.SetAdapter" }

func (a *redisSetAdapter[T]) Transform(ctx context.Context, src gstream.Stream[T]) gstream.Stream[T] {
	obs := a.opts.Observer
	if obs == nil {
		obs = stats.ObserverFromContext(ctx)
	}

	outCh := make(chan T, a.opts.Buffer)
	errCh := make(chan error, a.opts.Buffer)

	go func() {
		defer close(outCh)
		defer close(errCh)
		valCh := src.Values
		srcErrCh := src.Errors
		for valCh != nil || srcErrCh != nil {
			select {
			case <-ctx.Done():
				return
			case v, ok := <-valCh:
				if !ok {
					valCh = nil
					continue
				}
				vars, err := keyVarsFor(a.keyFn, a.cache, v)
				if err != nil {
					select {
					case errCh <- CacheError{Key: a.cache.Key, Op: "set", Err: err}:
					case <-ctx.Done():
						return
					}
				} else if err := writeThrough(ctx, a.client, a.cache, vars, a.opts.TTL, obs, v); err != nil {
					select {
					case errCh <- err:
					case <-ctx.Done():
						return
					}
				}
				// Pass through regardless — a cache failure must not drop data.
				select {
				case outCh <- v:
				case <-ctx.Done():
					return
				}
			case e, ok := <-srcErrCh:
				if !ok {
					srcErrCh = nil
					continue
				}
				select {
				case errCh <- e:
				case <-ctx.Done():
					return
				}
			}
		}
	}()
	return gstream.Stream[T]{Values: outCh, Errors: errCh}
}

// keyVarsFor resolves the vars map for a [SetAdapter]/[DrainSetAdapter] item:
// keyFn(v) when non-nil (today's behavior, unchanged); otherwise derives
// vars automatically via [codex.EncodeVars] using cache's merge-capable key
// params (the [SetHandle] convenience, inlined here to avoid a redundant
// BuildKey/Marshal round-trip through [Set]).
func keyVarsFor[T any](keyFn func(T) map[string]string, cache ports.Cache[T], v T) (map[string]string, error) {
	if keyFn != nil {
		return keyFn(v), nil
	}
	vars, err := codex.EncodeVars(v, cache.MergeFields()...)
	if err != nil {
		return nil, err
	}
	if len(vars) == 0 {
		return nil, nil
	}
	return vars, nil
}

// writeThrough builds the key, encodes v, and writes it with the effective
// TTL, firing RecordCacheWrite on both outcomes. Shared by [Set],
// [SetAdapter], and [DrainSetAdapter]. ttlOverride of zero means "use
// cache.TTL".
func writeThrough[T any](
	ctx context.Context,
	client Commands,
	cache ports.Cache[T],
	vars map[string]string,
	ttlOverride time.Duration,
	obs stats.Observer,
	v T,
) error {
	co, hasCO := obs.(stats.CacheObserver)
	key, err := cache.BuildKey(vars)
	if err != nil {
		return CacheError{Key: cache.Key, Op: "set", Err: err}
	}
	data, err := cache.Format.Marshal(v)
	if err != nil {
		stats.ReportErrors(obs, "payload", err)
		return CacheError{Key: key, Op: "set", Err: err}
	}
	ttl := cache.TTL
	if ttlOverride != 0 {
		ttl = ttlOverride
	}
	start := time.Now()
	err = client.Set(ctx, key, data, ttl)
	if hasCO {
		co.RecordCacheWrite(key, "set", err == nil, time.Since(start))
	}
	if err != nil {
		return CacheError{Key: key, Op: "set", Err: err}
	}
	return nil
}

// ── Set ───────────────────────────────────────────────────────────────────────

// SetOptions configures [Set].
type SetOptions struct {
	// TTL overrides the cache descriptor's declared TTL when non-zero.
	TTL time.Duration
	// Observer receives [stats.CacheObserver] write events. Resolved from
	// ctx when nil.
	Observer stats.Observer
}

// Set writes a single value to the cache — full codec validation (key vars
// AND value), no [ports.IOAdapter], no [gstream.Stream] involved. This is
// the plain-function standalone entrypoint for a non-pipeline application,
// mirroring [ports.File.Write]/[adapters/sql.Validate]: [ports.Cache] is
// the declarative descriptor, and Set is the concrete redis implementation
// of a write against it.
//
//	err := redis.Set(ctx, client, userCache,
//	    map[string]string{"id": user.ID}, user, redis.SetOptions{})
//
// [SetAdapter] and [DrainSetAdapter] delegate to Set per item — calling Set
// directly and driving them through a bound port produce identical
// behavior. Errors are [CacheError]; unlike the pipeline adapters, Set
// returns the error directly instead of routing it past a "pass through
// regardless" step, since there is no downstream to protect.
func Set[T any](ctx context.Context, client Commands, cache ports.Cache[T], vars map[string]string, v T, opts SetOptions) error {
	obs := opts.Observer
	if obs == nil {
		obs = stats.ObserverFromContext(ctx)
	}
	return writeThrough(ctx, client, cache, vars, opts.TTL, obs, v)
}

// SetHandle is the single-call convenience wrapper around [Set]: it derives
// the key vars from v automatically via [codex.EncodeVars](v,
// [ports.Cache.MergeFields]()...) — one struct in, no manual vars map —
// mirroring [mqtt5.PublishHandle]/[ports.WriteHandle]'s convenience for the
// cache boundary.
//
// [Set] remains available as the lower-level escape hatch for callers that
// build the vars map themselves (e.g. no merge-capable key params
// declared, or vars come from a non-struct source).
//
//	err := redis.SetHandle(ctx, client, userCache, user, redis.SetOptions{})
//	// key derived from user's own ID field — no manual vars map.
func SetHandle[T any](ctx context.Context, client Commands, cache ports.Cache[T], v T, opts SetOptions) error {
	vars, err := codex.EncodeVars(v, cache.MergeFields()...)
	if err != nil {
		return err
	}
	if len(vars) == 0 {
		vars = nil
	}
	return Set(ctx, client, cache, vars, v, opts)
}

// ── DrainSetAdapter ───────────────────────────────────────────────────────────

// DrainSetAdapter returns a [ports.SinkAdapter] that writes every item to the
// cache — the terminal variant of [SetAdapter] for pipeline ends. Write
// errors go to [SetAdapterOptions.OnError] (dropped when nil); source stream
// errors are also forwarded to OnError.
//
//	sinkPort.Bind(ctx, redis.DrainSetAdapter(client, cacheHandle,
//	    func(u User) map[string]string { return map[string]string{"id": u.ID} },
//	    redis.SetAdapterOptions{}))
//
// keyFn may be nil under the same conditions as [SetAdapter] — vars are
// then derived PER-ITEM from each item's own merge-capable key fields.
func DrainSetAdapter[T any](
	client Commands,
	cache ports.Cache[T],
	keyFn func(T) map[string]string,
	opts SetAdapterOptions,
) ports.SinkAdapter[T] {
	return &redisDrainSetAdapter[T]{client: client, cache: cache, keyFn: keyFn, opts: opts}
}

type redisDrainSetAdapter[T any] struct {
	client Commands
	cache  ports.Cache[T]
	keyFn  func(T) map[string]string
	opts   SetAdapterOptions
}

func (a *redisDrainSetAdapter[T]) AdapterName() string { return "redis.DrainSetAdapter" }

func (a *redisDrainSetAdapter[T]) Activate(ctx context.Context, src gstream.Stream[T]) {
	obs := a.opts.Observer
	if obs == nil {
		obs = stats.ObserverFromContext(ctx)
	}
	onErr := a.opts.OnError
	gstream.Drain(ctx, src,
		func(ctx context.Context, v T) error {
			vars, err := keyVarsFor(a.keyFn, a.cache, v)
			if err != nil {
				if onErr != nil {
					onErr(CacheError{Key: a.cache.Key, Op: "set", Err: err})
				}
				return nil
			}
			if err := writeThrough(ctx, a.client, a.cache, vars, a.opts.TTL, obs, v); err != nil && onErr != nil {
				onErr(err)
			}
			return nil
		},
		func(err error) {
			if onErr != nil {
				onErr(err)
			}
		},
		gstream.DrainOptions{Observer: obs})
}

// ── Seed ──────────────────────────────────────────────────────────────────────

// SeedOptions configures [Seed].
type SeedOptions struct {
	// Observer receives the [stats.CacheObserver] hit/miss event.
	// Resolved from ctx when nil.
	Observer stats.Observer
}

// Seed reads the cache handle's var-free key and returns the decoded,
// codec-validated value — the warm-restart read for a durable
// [ports.LatestPort]: persist updates with [SetAdapter]/[DrainSetAdapter] on
// the feeding stream, then Seed the first item after a restart. A thin
// wrapper around [Get] with nil vars — the one case that must run before
// any stream exists, so it stays a dedicated zero-vars function rather than
// asking every LatestPort warm-restart caller to pass an empty map.
//
//	if v, ok, err := redis.Seed(ctx, client, cacheHandle, redis.SeedOptions{}); ok && err == nil {
//	    seeded := stream.Single(ctx, v)
//	    // Merge seeded with the live stream before latestPort.Feed.
//	}
//
// Returns (zero, false, nil) on a miss — an empty cache is not an error.
// Decode and transport failures return (zero, false, [CacheError]).
func Seed[T any](ctx context.Context, client Commands, cache ports.Cache[T], opts SeedOptions) (T, bool, error) {
	return Get(ctx, client, cache, nil, GetOptions{Observer: opts.Observer})
}

// isMiss reports whether err denotes a missing key.
func isMiss(err error) bool { return errors.Is(err, ErrCacheMiss) }
