package redis

import (
	"context"
	"errors"
	"time"

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
	co, hasCO := obs.(stats.CacheObserver)

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
				key, err := a.cache.BuildKey(a.keyFn(v))
				if err != nil {
					if !emitErr(CacheError{Key: a.cache.Key, Op: "get", Err: err}) {
						return
					}
					continue
				}
				start := time.Now()
				raw, err := a.client.Get(ctx, key)
				if err != nil {
					if isMiss(err) {
						if hasCO {
							co.RecordCacheMiss(key, time.Since(start))
						}
						if a.opts.MissIsError {
							if !emitErr(CacheError{Key: key, Op: "get", Err: ErrCacheMiss}) {
								return
							}
						}
						continue
					}
					if !emitErr(CacheError{Key: key, Op: "get", Err: err}) {
						return
					}
					continue
				}
				resp, err := a.cache.Format.Unmarshal(raw)
				if err != nil {
					stats.ReportErrors(obs, "payload", err)
					if !emitErr(CacheError{Key: key, Op: "get", Err: err}) {
						return
					}
					continue
				}
				select {
				case outCh <- resp:
					if hasCO {
						co.RecordCacheHit(key, time.Since(start))
					}
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
				if err := writeThrough(ctx, a.client, a.cache, a.keyFn, a.opts, obs, v); err != nil {
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

// writeThrough builds the key, encodes v, and writes it with the effective
// TTL, firing RecordCacheWrite on both outcomes. Shared by [SetAdapter] and
// [DrainSetAdapter].
func writeThrough[T any](
	ctx context.Context,
	client Commands,
	cache ports.Cache[T],
	keyFn func(T) map[string]string,
	opts SetAdapterOptions,
	obs stats.Observer,
	v T,
) error {
	co, hasCO := obs.(stats.CacheObserver)
	key, err := cache.BuildKey(keyFn(v))
	if err != nil {
		return CacheError{Key: cache.Key, Op: "set", Err: err}
	}
	data, err := cache.Format.Marshal(v)
	if err != nil {
		stats.ReportErrors(obs, "payload", err)
		return CacheError{Key: key, Op: "set", Err: err}
	}
	ttl := cache.TTL
	if opts.TTL != 0 {
		ttl = opts.TTL
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

// ── DrainSetAdapter ───────────────────────────────────────────────────────────

// DrainSetAdapter returns a [ports.SinkAdapter] that writes every item to the
// cache — the terminal variant of [SetAdapter] for pipeline ends. Write
// errors go to [SetAdapterOptions.OnError] (dropped when nil); source stream
// errors are also forwarded to OnError.
//
//	sinkPort.Bind(ctx, redis.DrainSetAdapter(client, cacheHandle,
//	    func(u User) map[string]string { return map[string]string{"id": u.ID} },
//	    redis.SetAdapterOptions{}))
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
			if err := writeThrough(ctx, a.client, a.cache, a.keyFn, a.opts, obs, v); err != nil && onErr != nil {
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
// the feeding stream, then Seed the first item after a restart.
//
//	if v, ok, err := redis.Seed(ctx, client, cacheHandle, redis.SeedOptions{}); ok && err == nil {
//	    seeded := stream.Single(ctx, v)
//	    // Merge seeded with the live stream before latestPort.Feed.
//	}
//
// Returns (zero, false, nil) on a miss — an empty cache is not an error.
// Decode and transport failures return (zero, false, [CacheError]).
func Seed[T any](ctx context.Context, client Commands, cache ports.Cache[T], opts SeedOptions) (T, bool, error) {
	var zero T
	obs := opts.Observer
	if obs == nil {
		obs = stats.ObserverFromContext(ctx)
	}
	co, hasCO := obs.(stats.CacheObserver)

	key, err := cache.BuildKey(nil)
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

// isMiss reports whether err denotes a missing key.
func isMiss(err error) bool { return errors.Is(err, ErrCacheMiss) }
