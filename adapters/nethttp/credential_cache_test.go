package nethttp_test

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	nethttp "github.com/DaniDeer/go-codex/adapters/nethttp"
	"github.com/DaniDeer/go-codex/route"
	"github.com/DaniDeer/go-codex/stats"
)

// credentialCacheObserverSpy records CredentialCacheObserver calls for assertion.
type credentialCacheObserverSpy struct {
	stats.NoopObserver
	mu        sync.Mutex
	hits      int
	refreshes int
	locations []string
	successes []bool
}

func (s *credentialCacheObserverSpy) RecordCredentialCacheHit(location string, _ time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.hits++
	s.locations = append(s.locations, location)
}

func (s *credentialCacheObserverSpy) RecordCredentialCacheRefresh(location string, success bool, _ time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.refreshes++
	s.locations = append(s.locations, location)
	s.successes = append(s.successes, success)
}

func makeCredentialHeader(v string) http.Header {
	h := make(http.Header)
	h.Set("Authorization", v)
	return h
}

func TestNewCachingCredentialFunc_CachesWithinTTL(t *testing.T) {
	var calls int32
	inner := func(context.Context, []route.SecurityRequirement) (http.Header, error) {
		atomic.AddInt32(&calls, 1)
		return makeCredentialHeader("Bearer token-1"), nil
	}
	fn, _ := nethttp.NewCachingCredentialFunc(inner, nethttp.CachingCredentialFuncOptions{TTL: time.Hour})

	for i := 0; i < 5; i++ {
		h, err := fn(context.Background(), nil)
		if err != nil {
			t.Fatalf("call %d: unexpected error: %v", i, err)
		}
		if got := h.Get("Authorization"); got != "Bearer token-1" {
			t.Errorf("call %d: got header %q", i, got)
		}
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Errorf("want inner invoked once, got %d", got)
	}
}

func TestNewCachingCredentialFunc_RefreshesAfterTTL(t *testing.T) {
	var calls int32
	inner := func(context.Context, []route.SecurityRequirement) (http.Header, error) {
		n := atomic.AddInt32(&calls, 1)
		return makeCredentialHeader("Bearer token-" + string(rune('0'+n))), nil
	}
	fn, _ := nethttp.NewCachingCredentialFunc(inner, nethttp.CachingCredentialFuncOptions{TTL: 20 * time.Millisecond})

	if _, err := fn(context.Background(), nil); err != nil {
		t.Fatalf("first call: unexpected error: %v", err)
	}
	time.Sleep(40 * time.Millisecond)
	if _, err := fn(context.Background(), nil); err != nil {
		t.Fatalf("second call: unexpected error: %v", err)
	}
	if got := atomic.LoadInt32(&calls); got != 2 {
		t.Errorf("want inner invoked twice after TTL expiry, got %d", got)
	}
}

func TestNewCachingCredentialFunc_ConcurrentCallsDuringMiss_SingleInnerInvocation(t *testing.T) {
	var calls int32
	release := make(chan struct{})
	inner := func(context.Context, []route.SecurityRequirement) (http.Header, error) {
		atomic.AddInt32(&calls, 1)
		<-release
		return makeCredentialHeader("Bearer shared-token"), nil
	}
	fn, _ := nethttp.NewCachingCredentialFunc(inner, nethttp.CachingCredentialFuncOptions{TTL: time.Hour})

	const n = 10
	var wg sync.WaitGroup
	results := make([]http.Header, n)
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			h, err := fn(context.Background(), nil)
			results[i] = h
			errs[i] = err
		}(i)
	}
	// Give every goroutine a chance to reach the blocked inner() call before
	// releasing it, so they all join the same in-flight call.
	time.Sleep(20 * time.Millisecond)
	close(release)
	wg.Wait()

	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Fatalf("want inner invoked exactly once, got %d", got)
	}
	for i, err := range errs {
		if err != nil {
			t.Errorf("caller %d: unexpected error: %v", i, err)
		}
		if got := results[i].Get("Authorization"); got != "Bearer shared-token" {
			t.Errorf("caller %d: got header %q", i, got)
		}
	}
}

func TestNewCachingCredentialFunc_InnerError_NotCached(t *testing.T) {
	var calls int32
	wantErr := errors.New("auth server unavailable")
	inner := func(context.Context, []route.SecurityRequirement) (http.Header, error) {
		atomic.AddInt32(&calls, 1)
		return nil, wantErr
	}
	fn, _ := nethttp.NewCachingCredentialFunc(inner, nethttp.CachingCredentialFuncOptions{TTL: time.Hour})

	if _, err := fn(context.Background(), nil); !errors.Is(err, wantErr) {
		t.Fatalf("first call: got error %v, want %v", err, wantErr)
	}
	if _, err := fn(context.Background(), nil); !errors.Is(err, wantErr) {
		t.Fatalf("second call: got error %v, want %v", err, wantErr)
	}
	if got := atomic.LoadInt32(&calls); got != 2 {
		t.Errorf("want inner invoked again immediately after an error (not cached), got %d", got)
	}
}

func TestNewCachingCredentialFunc_Invalidate_ForcesRefreshOnNextCall(t *testing.T) {
	var calls int32
	inner := func(context.Context, []route.SecurityRequirement) (http.Header, error) {
		atomic.AddInt32(&calls, 1)
		return makeCredentialHeader("Bearer token"), nil
	}
	fn, invalidate := nethttp.NewCachingCredentialFunc(inner, nethttp.CachingCredentialFuncOptions{TTL: time.Hour})

	if _, err := fn(context.Background(), nil); err != nil {
		t.Fatalf("first call: unexpected error: %v", err)
	}
	invalidate()
	if _, err := fn(context.Background(), nil); err != nil {
		t.Fatalf("second call: unexpected error: %v", err)
	}
	if got := atomic.LoadInt32(&calls); got != 2 {
		t.Errorf("want inner invoked again after invalidate, got %d", got)
	}
}

func TestNewCachingCredentialFunc_Observer_RecordsHitAndRefresh(t *testing.T) {
	inner := func(context.Context, []route.SecurityRequirement) (http.Header, error) {
		return makeCredentialHeader("Bearer token"), nil
	}
	spy := &credentialCacheObserverSpy{}
	fn, _ := nethttp.NewCachingCredentialFunc(inner, nethttp.CachingCredentialFuncOptions{
		TTL:      time.Hour,
		Observer: spy,
	})
	reqs := []route.SecurityRequirement{{"bearerAuth": nil}}

	if _, err := fn(context.Background(), reqs); err != nil {
		t.Fatalf("first call: unexpected error: %v", err)
	}
	if _, err := fn(context.Background(), reqs); err != nil {
		t.Fatalf("second call: unexpected error: %v", err)
	}

	spy.mu.Lock()
	defer spy.mu.Unlock()
	if spy.refreshes != 1 {
		t.Errorf("want 1 refresh event, got %d", spy.refreshes)
	}
	if spy.hits != 1 {
		t.Errorf("want 1 hit event, got %d", spy.hits)
	}
	for _, loc := range spy.locations {
		if loc != "bearerAuth" {
			t.Errorf("want location %q, got %q", "bearerAuth", loc)
		}
	}
	if len(spy.successes) != 1 || !spy.successes[0] {
		t.Errorf("want refresh success=true, got %v", spy.successes)
	}
}

func TestNewCachingCredentialFunc_NilObserver_NoPanic(t *testing.T) {
	inner := func(context.Context, []route.SecurityRequirement) (http.Header, error) {
		return makeCredentialHeader("Bearer token"), nil
	}
	fn, invalidate := nethttp.NewCachingCredentialFunc(inner, nethttp.CachingCredentialFuncOptions{TTL: time.Hour})

	if _, err := fn(context.Background(), nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	invalidate() // must not panic with a nil Observer
	if _, err := fn(context.Background(), nil); err != nil {
		t.Fatalf("unexpected error after invalidate: %v", err)
	}
}
