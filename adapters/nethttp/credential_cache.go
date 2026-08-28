package nethttp

import (
	"context"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/DaniDeer/go-codex/route"
	"github.com/DaniDeer/go-codex/stats"
)

// CachingCredentialFuncOptions configures [NewCachingCredentialFunc].
type CachingCredentialFuncOptions struct {
	// TTL is how long a successfully-obtained credential is reused before
	// inner is invoked again. Required — a zero TTL means "never cache"
	// (every call invokes inner), which is a valid but unusual choice.
	TTL time.Duration

	// Observer, when non-nil, receives cache hit/refresh events via
	// [stats.CredentialCacheObserver]. Defaults to [stats.NoopObserver].
	Observer stats.Observer
}

// credentialCacheEntry is a single cached credential and its expiry.
type credentialCacheEntry struct {
	header    http.Header
	expiresAt time.Time
}

// credentialCacheCall represents an in-flight inner() invocation shared by
// every concurrent caller that observes a cache miss at the same time —
// the hand-rolled single-flight join point. Callers that arrive while a
// call is in flight block on done instead of invoking inner themselves.
type credentialCacheCall struct {
	done   chan struct{}
	header http.Header
	err    error
}

// NewCachingCredentialFunc wraps inner with TTL-based caching: inner is
// invoked at most once per TTL window, with concurrent callers during a
// cache miss sharing the SAME in-flight call (hand-rolled single-flight —
// no thundering herd on the auth server, no external dependency).
//
// Returns (fn, invalidate): fn is a [CredentialFunc] — wrap it in a
// middleware.Middleware{Fn: fn} to attach it to [Call]/[CallHandle];
// invalidate immediately expires the cached credential — wire it to
// [CallOptions.OnCredentialRejected] so a 401 causes the NEXT call to fetch
// a fresh credential:
//
//	credFn, invalidate := nethttp.NewCachingCredentialFunc(inner, nethttp.CachingCredentialFuncOptions{TTL: time.Hour})
//	callOpts := nethttp.CallOptions{OnCredentialRejected: invalidate}
//	credMw := middleware.Middleware{Fn: credFn}
//	resp, err := nethttp.CallHandle(ctx, client, url, handle, req, callOpts, credMw)
//	var statusErr nethttp.UnexpectedStatusError
//	if errors.As(err, &statusErr) && statusErr.StatusCode == http.StatusUnauthorized {
//	    resp, err = nethttp.CallHandle(ctx, client, url, handle, req, callOpts, credMw) // fresh credential now
//	}
//
// One NewCachingCredentialFunc instance = one cache entry. Construct a
// separate instance per credential scope (e.g. per host/registry) if a
// caller's routes need independently-cached credentials.
func NewCachingCredentialFunc(inner CredentialFunc, opts CachingCredentialFuncOptions) (fn CredentialFunc, invalidate func()) {
	obs := opts.Observer
	if obs == nil {
		obs = stats.NoopObserver{}
	}
	ccObs, _ := obs.(stats.CredentialCacheObserver)

	var (
		mu       sync.Mutex
		current  *credentialCacheEntry
		inflight *credentialCacheCall
	)

	fn = func(ctx context.Context, reqs []route.SecurityRequirement) (http.Header, error) {
		start := time.Now()
		location := credentialCacheLocation(reqs)

		mu.Lock()
		if current != nil && start.Before(current.expiresAt) {
			h := current.header
			mu.Unlock()
			if ccObs != nil {
				ccObs.RecordCredentialCacheHit(location, time.Since(start))
			}
			return h, nil
		}

		// Cache miss or expired — join an in-flight refresh if one is
		// already running, otherwise become the leader that invokes inner.
		if inflight != nil {
			call := inflight
			mu.Unlock()
			<-call.done
			return call.header, call.err
		}

		call := &credentialCacheCall{done: make(chan struct{})}
		inflight = call
		mu.Unlock()

		innerStart := time.Now()
		header, err := inner(ctx, reqs)
		duration := time.Since(innerStart)

		mu.Lock()
		if err == nil {
			current = &credentialCacheEntry{header: header, expiresAt: innerStart.Add(opts.TTL)}
		} else {
			current = nil
		}
		inflight = nil
		call.header, call.err = header, err
		mu.Unlock()
		close(call.done)

		if ccObs != nil {
			ccObs.RecordCredentialCacheRefresh(location, err == nil, duration)
		}
		return header, err
	}

	invalidate = func() {
		mu.Lock()
		current = nil
		mu.Unlock()
	}

	return fn, invalidate
}

// credentialCacheLocation derives a stable, human-readable label for
// observer events from the security scheme names in reqs — there is no
// dedicated "location" option, since the caller already identifies the
// credential scope by choosing which route(s)/reqs use this cache instance.
func credentialCacheLocation(reqs []route.SecurityRequirement) string {
	names := make(map[string]struct{})
	for _, req := range reqs {
		for name := range req {
			names[name] = struct{}{}
		}
	}
	if len(names) == 0 {
		return ""
	}
	sorted := make([]string, 0, len(names))
	for name := range names {
		sorted = append(sorted, name)
	}
	sort.Strings(sorted)
	return strings.Join(sorted, ",")
}
