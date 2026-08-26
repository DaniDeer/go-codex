package codex_test

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/validate"
)

// ── Interface satisfaction ───────────────────────────────────────────────────

var _ codex.FreshGetter[string] = (*codex.Cacheable[string])(nil)
var _ codex.Setter[string] = (*codex.Cacheable[string])(nil)

// TestCacheable_ImplementsGetterInterface verifies Cacheable[T] does NOT
// satisfy Getter[T]/GetterSetter[T] — Get()'s two-value return breaks the
// single-value contract, so it satisfies FreshGetter[T] instead (see the
// var _ assertions above, which are the actual compile-time proof; this
// test documents the fact for readers of the test file).
func TestCacheable_ImplementsGetterInterface(t *testing.T) {
	var c any = (*codex.Cacheable[string])(nil)
	if _, ok := c.(codex.Getter[string]); ok {
		t.Error("Cacheable[T] must NOT satisfy Getter[T] (two-value Get breaks the contract)")
	}
	if _, ok := c.(codex.FreshGetter[string]); !ok {
		t.Error("Cacheable[T] must satisfy FreshGetter[T]")
	}
}

// ── Construction ─────────────────────────────────────────────────────────────

func TestNewCacheable_ValidInitial_ReturnsCacheable(t *testing.T) {
	c, err := codex.NewCacheable("test", "hello", codex.String().Refine(validate.NonEmptyString), time.Hour)
	if err != nil {
		t.Fatalf("NewCacheable: unexpected error: %v", err)
	}
	val, fresh := c.Get()
	if val != "hello" || !fresh {
		t.Errorf("Get() = (%q, %v), want (\"hello\", true)", val, fresh)
	}
}

func TestNewCacheable_InvalidInitial_ReturnsError(t *testing.T) {
	_, err := codex.NewCacheable("test", "", codex.String().Refine(validate.NonEmptyString), time.Hour)
	if err == nil {
		t.Fatal("NewCacheable with invalid initial: want error, got nil")
	}
}

// ── Freshness / TTL ──────────────────────────────────────────────────────────

func TestCacheable_Get_FreshBeforeTTLElapses(t *testing.T) {
	c, err := codex.NewCacheable("test", "v1", codex.String(), time.Hour)
	if err != nil {
		t.Fatalf("NewCacheable: unexpected error: %v", err)
	}
	val, fresh := c.Get()
	if val != "v1" || !fresh {
		t.Errorf("Get() = (%q, %v), want (\"v1\", true)", val, fresh)
	}
}

func TestCacheable_Get_StaleAfterTTLElapses(t *testing.T) {
	c, err := codex.NewCacheable("test", "v1", codex.String(), time.Millisecond)
	if err != nil {
		t.Fatalf("NewCacheable: unexpected error: %v", err)
	}
	time.Sleep(5 * time.Millisecond)
	val, fresh := c.Get()
	if val != "v1" {
		t.Errorf("Get() value = %q, want unchanged %q", val, "v1")
	}
	if fresh {
		t.Error("Get() fresh = true, want false after TTL elapsed")
	}
}

func TestCacheable_ZeroTTL_NeverExpiresFromTTLAlone(t *testing.T) {
	c, err := codex.NewCacheable("test", "v1", codex.String(), 0)
	if err != nil {
		t.Fatalf("NewCacheable: unexpected error: %v", err)
	}
	time.Sleep(5 * time.Millisecond)
	_, fresh := c.Get()
	if !fresh {
		t.Error("Get() fresh = false with ttl=0, want true (never expires from TTL alone)")
	}
}

// ── Invalidate ───────────────────────────────────────────────────────────────

func TestCacheable_Invalidate_MarksStaleImmediately(t *testing.T) {
	c, err := codex.NewCacheable("test", "v1", codex.String(), time.Hour)
	if err != nil {
		t.Fatalf("NewCacheable: unexpected error: %v", err)
	}
	c.Invalidate()
	val, fresh := c.Get()
	if val != "v1" {
		t.Errorf("Get() value after Invalidate = %q, want unchanged %q", val, "v1")
	}
	if fresh {
		t.Error("Get() fresh = true after Invalidate, want false")
	}
}

// ── Set ───────────────────────────────────────────────────────────────────

func TestCacheable_SetValid_ResetsFreshnessAndTTL(t *testing.T) {
	c, err := codex.NewCacheable("test", "v1", codex.String(), time.Millisecond)
	if err != nil {
		t.Fatalf("NewCacheable: unexpected error: %v", err)
	}
	time.Sleep(5 * time.Millisecond)
	if _, fresh := c.Get(); fresh {
		t.Fatal("expected stale before Set")
	}
	if err := c.Set("v2"); err != nil {
		t.Fatalf("Set: unexpected error: %v", err)
	}
	val, fresh := c.Get()
	if val != "v2" || !fresh {
		t.Errorf("Get() after Set = (%q, %v), want (\"v2\", true)", val, fresh)
	}
}

func TestCacheable_SetValid_ClearsInvalidate(t *testing.T) {
	c, err := codex.NewCacheable("test", "v1", codex.String(), time.Hour)
	if err != nil {
		t.Fatalf("NewCacheable: unexpected error: %v", err)
	}
	c.Invalidate()
	if err := c.Set("v2"); err != nil {
		t.Fatalf("Set: unexpected error: %v", err)
	}
	if _, fresh := c.Get(); !fresh {
		t.Error("Get() fresh = false after Set following Invalidate, want true")
	}
}

func TestCacheable_SetInvalid_KeepsPreviousValueAndFreshness(t *testing.T) {
	c, err := codex.NewCacheable("test", "v1", codex.String().Refine(validate.NonEmptyString), time.Hour)
	if err != nil {
		t.Fatalf("NewCacheable: unexpected error: %v", err)
	}
	if err := c.Set(""); err == nil {
		t.Fatal("Set(\"\"): want error, got nil")
	}
	val, fresh := c.Get()
	if val != "v1" || !fresh {
		t.Errorf("Get() after failed Set = (%q, %v), want unchanged (\"v1\", true)", val, fresh)
	}
}

// ── IsStale ──────────────────────────────────────────────────────────────────

func TestCacheable_IsStale_MatchesGetFreshness(t *testing.T) {
	c, err := codex.NewCacheable("test", "v1", codex.String(), time.Millisecond)
	if err != nil {
		t.Fatalf("NewCacheable: unexpected error: %v", err)
	}
	_, fresh := c.Get()
	if c.IsStale() != !fresh {
		t.Errorf("IsStale() = %v, want %v (inverse of fresh=%v)", c.IsStale(), !fresh, fresh)
	}
	time.Sleep(5 * time.Millisecond)
	_, fresh = c.Get()
	if c.IsStale() != !fresh {
		t.Errorf("IsStale() = %v, want %v (inverse of fresh=%v)", c.IsStale(), !fresh, fresh)
	}
}

// ── Concurrency ──────────────────────────────────────────────────────────────

func TestCacheable_ConcurrentGetSetInvalidate_NoRace(t *testing.T) {
	c, err := codex.NewCacheable("test", 0, codex.Int(), time.Millisecond)
	if err != nil {
		t.Fatalf("NewCacheable: unexpected error: %v", err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(3)
		go func(n int) {
			defer wg.Done()
			_ = c.Set(n)
		}(i)
		go func() {
			defer wg.Done()
			_, _ = c.Get()
		}()
		go func() {
			defer wg.Done()
			c.Invalidate()
		}()
	}
	wg.Wait()
}

// ── Observer integration ─────────────────────────────────────────────────────

type recordingCacheableObserver struct {
	mu          sync.Mutex
	reloads     []reloadCall
	invalidates []string
}

func (r *recordingCacheableObserver) RecordReload(location string, success bool, _ time.Duration) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.reloads = append(r.reloads, reloadCall{location, success})
}

func (r *recordingCacheableObserver) RecordInvalidate(location string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.invalidates = append(r.invalidates, location)
}

var _ codex.ReloadObserver = (*recordingCacheableObserver)(nil)
var _ codex.InvalidateObserver = (*recordingCacheableObserver)(nil)

func TestCacheable_Set_CallsReloadObserver(t *testing.T) {
	obs := &recordingCacheableObserver{}
	c, err := codex.NewCacheable("memo", "v1", codex.String(), time.Hour, codex.WithCacheableReloadObserver[string](obs))
	if err != nil {
		t.Fatalf("NewCacheable: unexpected error: %v", err)
	}
	if err := c.Set("v2"); err != nil {
		t.Fatalf("Set: unexpected error: %v", err)
	}
	if len(obs.reloads) != 1 || obs.reloads[0].location != "memo" || !obs.reloads[0].success {
		t.Errorf("unexpected reload calls: %+v", obs.reloads)
	}
}

func TestCacheable_Invalidate_CallsInvalidateObserver(t *testing.T) {
	obs := &recordingCacheableObserver{}
	c, err := codex.NewCacheable("memo", "v1", codex.String(), time.Hour, codex.WithCacheableReloadObserver[string](obs))
	if err != nil {
		t.Fatalf("NewCacheable: unexpected error: %v", err)
	}
	c.Invalidate()
	if len(obs.invalidates) != 1 || obs.invalidates[0] != "memo" {
		t.Errorf("unexpected invalidate calls: %+v", obs.invalidates)
	}
}

func TestCacheable_NilObserver_NoPanic(t *testing.T) {
	c, err := codex.NewCacheable("memo", "v1", codex.String(), time.Hour)
	if err != nil {
		t.Fatalf("NewCacheable: unexpected error: %v", err)
	}
	if err := c.Set("v2"); err != nil {
		t.Fatalf("Set: unexpected error: %v", err)
	}
	c.Invalidate()
}

// ── Example ──────────────────────────────────────────────────────────────────

func ExampleCacheable() {
	memo, err := codex.NewCacheable("expensive-computation", 42, codex.Int(), time.Hour)
	if err != nil {
		panic(err)
	}
	value, fresh := memo.Get()
	fmt.Println("value:", value, "fresh:", fresh)

	// An upstream event (webhook, keyspace notification) tells us the
	// value changed before the TTL naturally expires:
	memo.Invalidate()
	_, fresh = memo.Get()
	fmt.Println("fresh after invalidate:", fresh)

	// Stale-while-revalidate: serve the stale value, refresh in the background.
	if err := memo.Set(43); err != nil {
		panic(err)
	}
	value, fresh = memo.Get()
	fmt.Println("value:", value, "fresh:", fresh)
	// Output:
	// value: 42 fresh: true
	// fresh after invalidate: false
	// value: 43 fresh: true
}
