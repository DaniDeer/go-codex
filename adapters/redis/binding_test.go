package redis_test

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"testing"
	"time"

	adapterredis "github.com/DaniDeer/go-codex/adapters/redis"
	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/ports"
	"github.com/DaniDeer/go-codex/stats"
	"github.com/DaniDeer/go-codex/stream"
	"github.com/DaniDeer/go-codex/validate"
)

// ── Test fixtures ─────────────────────────────────────────────────────────────

type user struct {
	ID   string
	Name string
}

var userCodec = codex.Struct[user](
	codex.RequiredField("id", codex.String().Refine(validate.NonEmptyString),
		func(u user) string { return u.ID },
		func(u *user, v string) { u.ID = v },
	),
	codex.RequiredField("name", codex.String().Refine(validate.NonEmptyString),
		func(u user) string { return u.Name },
		func(u *user, v string) { u.Name = v },
	),
)

type userQuery struct{ ID string }

// fakeCommands is the hand-written Commands fake — an in-memory map plus
// recorded calls. No live Redis, no miniredis.
type fakeCommands struct {
	mu     sync.Mutex
	store  map[string][]byte
	ttls   map[string]time.Duration
	getErr error // non-miss transport error to inject
	setErr error
}

func newFake() *fakeCommands {
	return &fakeCommands{store: map[string][]byte{}, ttls: map[string]time.Duration{}}
}

func (f *fakeCommands) Get(_ context.Context, key string) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.getErr != nil {
		return nil, f.getErr
	}
	b, ok := f.store[key]
	if !ok {
		return nil, adapterredis.ErrCacheMiss
	}
	return b, nil
}

func (f *fakeCommands) Set(_ context.Context, key string, value []byte, ttl time.Duration) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.setErr != nil {
		return f.setErr
	}
	f.store[key] = value
	f.ttls[key] = ttl
	return nil
}

func (f *fakeCommands) Del(_ context.Context, keys ...string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, k := range keys {
		delete(f.store, k)
	}
	return nil
}

// cacheObserverSpy counts CacheObserver events per kind.
type cacheObserverSpy struct {
	stats.NoopObserver
	mu     sync.Mutex
	hits   int
	misses int
	writes map[bool]int // success → count
	fields []string     // RecordValidationError fields
}

func (o *cacheObserverSpy) RecordCacheHit(_ string, _ time.Duration) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.hits++
}

func (o *cacheObserverSpy) RecordCacheMiss(_ string, _ time.Duration) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.misses++
}

func (o *cacheObserverSpy) RecordCacheWrite(_, _ string, success bool, _ time.Duration) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.writes == nil {
		o.writes = map[bool]int{}
	}
	o.writes[success]++
}

func (o *cacheObserverSpy) RecordValidationError(_, _, field string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.fields = append(o.fields, field)
}

func userCache() ports.Cache[user] {
	port, err := ports.NewIOPort[userQuery, user]("user-cache", codex.Struct[userQuery](
		codex.RequiredField("id", codex.String(),
			func(q userQuery) string { return q.ID },
			func(q *userQuery, v string) { q.ID = v },
		),
	), userCodec, ports.PortOptions{
		Patterns: []ports.Pattern{
			ports.CachePattern{Key: "user:{id}", TTL: 15 * time.Minute},
		},
	})
	if err != nil {
		panic(err)
	}
	c, ok := ports.CacheHandle[user](port)
	if !ok {
		panic("no cache handle")
	}
	return c
}

func queryStream(qs ...userQuery) stream.Stream[userQuery] {
	ch := make(chan userQuery, len(qs))
	for _, q := range qs {
		ch <- q
	}
	close(ch)
	errCh := make(chan error)
	close(errCh)
	return stream.Stream[userQuery]{Values: ch, Errors: errCh}
}

func userStream(us ...user) stream.Stream[user] {
	ch := make(chan user, len(us))
	for _, u := range us {
		ch <- u
	}
	close(ch)
	errCh := make(chan error)
	close(errCh)
	return stream.Stream[user]{Values: ch, Errors: errCh}
}

func keyByID(q userQuery) map[string]string { return map[string]string{"id": q.ID} }
func keyByUser(u user) map[string]string    { return map[string]string{"id": u.ID} }

// ── C1: Get hit ───────────────────────────────────────────────────────────────

func TestGetAdapter_Hit(t *testing.T) {
	ctx := context.Background()
	fake := newFake()
	fake.store["user:42"] = []byte(`{"id":"42","name":"Ada"}`)

	out := adapterredis.GetAdapter[userQuery, user](fake, userCache(), keyByID,
		adapterredis.GetAdapterOptions{}).Transform(ctx, queryStream(userQuery{ID: "42"}))
	vals, errs := stream.Collect(ctx, out)
	if len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	if len(vals) != 1 || vals[0].Name != "Ada" {
		t.Errorf("want [Ada], got %v", vals)
	}
}

// ── C2: Get miss, default (skip) ──────────────────────────────────────────────

func TestGetAdapter_Miss_Skips(t *testing.T) {
	obs := &cacheObserverSpy{}
	ctx := stats.WithObserver(context.Background(), obs)
	fake := newFake()

	out := adapterredis.GetAdapter[userQuery, user](fake, userCache(), keyByID,
		adapterredis.GetAdapterOptions{}).Transform(ctx, queryStream(userQuery{ID: "42"}))
	vals, errs := stream.Collect(ctx, out)
	if len(vals) != 0 || len(errs) != 0 {
		t.Errorf("want silent skip, got vals=%v errs=%v", vals, errs)
	}
	if obs.misses != 1 {
		t.Errorf("want 1 RecordCacheMiss, got %d", obs.misses)
	}
}

// ── C3: Get miss, MissIsError ─────────────────────────────────────────────────

func TestGetAdapter_Miss_AsError(t *testing.T) {
	ctx := context.Background()
	fake := newFake()

	out := adapterredis.GetAdapter[userQuery, user](fake, userCache(), keyByID,
		adapterredis.GetAdapterOptions{MissIsError: true}).Transform(ctx, queryStream(userQuery{ID: "42"}))
	_, errs := stream.Collect(ctx, out)
	if len(errs) != 1 {
		t.Fatalf("want 1 error, got %v", errs)
	}
	if !errors.Is(errs[0], adapterredis.ErrCacheMiss) {
		t.Errorf("errors.Is(ErrCacheMiss) must hold through CacheError, got %v", errs[0])
	}
	var ce adapterredis.CacheError
	if !errors.As(errs[0], &ce) || ce.Key != "user:42" || ce.Op != "get" {
		t.Errorf("want CacheError{user:42, get}, got %+v", ce)
	}
}

// ── C4: Get decode/validation failure ─────────────────────────────────────────

func TestGetAdapter_DecodeFailure(t *testing.T) {
	obs := &cacheObserverSpy{}
	ctx := stats.WithObserver(context.Background(), obs)
	fake := newFake()
	fake.store["user:42"] = []byte(`{"id":"42","name":""}`) // fails minLength(1)

	out := adapterredis.GetAdapter[userQuery, user](fake, userCache(), keyByID,
		adapterredis.GetAdapterOptions{}).Transform(ctx, queryStream(userQuery{ID: "42"}))
	vals, errs := stream.Collect(ctx, out)
	if len(vals) != 0 || len(errs) != 1 {
		t.Fatalf("want 0 vals / 1 err, got %v / %v", vals, errs)
	}
	var ce adapterredis.CacheError
	if !errors.As(errs[0], &ce) || ce.Op != "get" {
		t.Errorf("want CacheError{op get}, got %v", errs[0])
	}
	obs.mu.Lock()
	defer obs.mu.Unlock()
	if len(obs.fields) == 0 {
		t.Error("want per-field validation reports via ReportErrors")
	}
}

// ── C5: Set writes and passes through ─────────────────────────────────────────

func TestSetAdapter_WritesAndPassesThrough(t *testing.T) {
	obs := &cacheObserverSpy{}
	ctx := stats.WithObserver(context.Background(), obs)
	fake := newFake()

	out := adapterredis.SetAdapter[user](fake, userCache(), keyByUser,
		adapterredis.SetAdapterOptions{}).Transform(ctx, userStream(user{ID: "42", Name: "Ada"}))
	vals, errs := stream.Collect(ctx, out)
	if len(errs) != 0 || len(vals) != 1 {
		t.Fatalf("want passthrough, got vals=%v errs=%v", vals, errs)
	}
	if string(fake.store["user:42"]) != `{"id":"42","name":"Ada"}` {
		t.Errorf("stored bytes wrong: %s", fake.store["user:42"])
	}
	if obs.writes[true] != 1 {
		t.Errorf("want 1 successful RecordCacheWrite, got %v", obs.writes)
	}
}

// ── C6: Set write failure still passes item through ───────────────────────────

func TestSetAdapter_WriteFailure_PassesThrough(t *testing.T) {
	obs := &cacheObserverSpy{}
	ctx := stats.WithObserver(context.Background(), obs)
	fake := newFake()
	boom := errors.New("connection refused")
	fake.setErr = boom

	out := adapterredis.SetAdapter[user](fake, userCache(), keyByUser,
		adapterredis.SetAdapterOptions{Buffer: 2}).Transform(ctx, userStream(user{ID: "42", Name: "Ada"}))
	vals, errs := stream.Collect(ctx, out)
	if len(vals) != 1 {
		t.Errorf("item must still pass through, got %v", vals)
	}
	if len(errs) != 1 || !errors.Is(errs[0], boom) {
		t.Errorf("want wrapped write error, got %v", errs)
	}
	if obs.writes[false] != 1 {
		t.Errorf("want 1 failed RecordCacheWrite, got %v", obs.writes)
	}
}

// ── C7: TTL from CachePattern + option override ───────────────────────────────

func TestSetAdapter_TTL(t *testing.T) {
	ctx := context.Background()
	fake := newFake()

	out := adapterredis.SetAdapter[user](fake, userCache(), keyByUser,
		adapterredis.SetAdapterOptions{}).Transform(ctx, userStream(user{ID: "1", Name: "A"}))
	stream.Collect(ctx, out)
	if fake.ttls["user:1"] != 15*time.Minute {
		t.Errorf("want pattern TTL 15m, got %v", fake.ttls["user:1"])
	}

	out = adapterredis.SetAdapter[user](fake, userCache(), keyByUser,
		adapterredis.SetAdapterOptions{TTL: time.Hour}).Transform(ctx, userStream(user{ID: "2", Name: "B"}))
	stream.Collect(ctx, out)
	if fake.ttls["user:2"] != time.Hour {
		t.Errorf("want option TTL 1h override, got %v", fake.ttls["user:2"])
	}
}

// ── C8: key template expansion + missing var ──────────────────────────────────

func TestCache_BuildKey(t *testing.T) {
	c := userCache()
	key, err := c.BuildKey(map[string]string{"id": "42"})
	if err != nil || key != "user:42" {
		t.Errorf("want user:42, got %q err=%v", key, err)
	}

	_, err = c.BuildKey(map[string]string{})
	var ke ports.CacheKeyError
	if !errors.As(err, &ke) || ke.Var != "id" {
		t.Errorf("want CacheKeyError{Var: id}, got %v", err)
	}
}

func TestGetAdapter_MissingKeyVar(t *testing.T) {
	ctx := context.Background()
	fake := newFake()

	out := adapterredis.GetAdapter[userQuery, user](fake, userCache(),
		func(userQuery) map[string]string { return nil }, // no vars
		adapterredis.GetAdapterOptions{}).Transform(ctx, queryStream(userQuery{ID: "42"}))
	_, errs := stream.Collect(ctx, out)
	if len(errs) != 1 {
		t.Fatalf("want 1 error, got %v", errs)
	}
	var ke ports.CacheKeyError
	if !errors.As(errs[0], &ke) {
		t.Errorf("errors.As must reach CacheKeyError, got %v", errs[0])
	}
}

// ── C9: DrainSetAdapter drains fully ──────────────────────────────────────────

func TestDrainSetAdapter_DrainsAndWrites(t *testing.T) {
	ctx := context.Background()
	fake := newFake()
	var errsSeen []error

	adapterredis.DrainSetAdapter[user](fake, userCache(), keyByUser,
		adapterredis.SetAdapterOptions{OnError: func(err error) { errsSeen = append(errsSeen, err) }},
	).Activate(ctx, userStream(user{ID: "1", Name: "A"}, user{ID: "2", Name: "B"}))

	if len(fake.store) != 2 {
		t.Errorf("want 2 items written, got %d", len(fake.store))
	}
	if len(errsSeen) != 0 {
		t.Errorf("unexpected errors: %v", errsSeen)
	}
}

// ── C10: Seed warm start ──────────────────────────────────────────────────────

func TestSeed_WarmStart(t *testing.T) {
	obs := &cacheObserverSpy{}
	ctx := stats.WithObserver(context.Background(), obs)
	fake := newFake()

	// LatestPort-style var-free key.
	latestCache := ports.Cache[user]{Key: "latest-user", Format: userCache().Format}
	fake.store["latest-user"] = []byte(`{"id":"42","name":"Ada"}`)

	v, ok, err := adapterredis.Seed(ctx, fake, latestCache, adapterredis.SeedOptions{})
	if err != nil || !ok || v.Name != "Ada" {
		t.Errorf("want warm Ada, got v=%v ok=%v err=%v", v, ok, err)
	}
	if obs.hits != 1 {
		t.Errorf("want 1 RecordCacheHit, got %d", obs.hits)
	}

	// Cold start: miss is NOT an error.
	_, ok, err = adapterredis.Seed(ctx, fake, ports.Cache[user]{Key: "absent", Format: latestCache.Format}, adapterredis.SeedOptions{})
	if ok || err != nil {
		t.Errorf("cold start: want (false, nil), got ok=%v err=%v", ok, err)
	}
}

// ── C11: CacheError.LogValue ──────────────────────────────────────────────────

func TestCacheError_LogValue(t *testing.T) {
	err := adapterredis.CacheError{Key: "user:42", Op: "get", Err: adapterredis.ErrCacheMiss}
	v := err.LogValue()
	if v.Kind() != slog.KindGroup {
		t.Fatalf("want KindGroup, got %v", v.Kind())
	}
	keys := map[string]bool{}
	for _, a := range v.Group() {
		keys[a.Key] = true
	}
	for _, want := range []string{"key", "op", "err"} {
		if !keys[want] {
			t.Errorf("missing LogValue key %q", want)
		}
	}
}

func TestCacheKeyError_LogValue(t *testing.T) {
	err := ports.CacheKeyError{Key: "user:{id}", Var: "id"}
	v := err.LogValue()
	if v.Kind() != slog.KindGroup {
		t.Fatalf("want KindGroup, got %v", v.Kind())
	}
	keys := map[string]bool{}
	for _, a := range v.Group() {
		keys[a.Key] = true
	}
	if !keys["key"] || !keys["var"] {
		t.Errorf("missing LogValue keys, got %v", keys)
	}
}

// ── C12: errors.As chain ──────────────────────────────────────────────────────

func TestCacheError_ErrorsAsChain(t *testing.T) {
	inner := errors.New("io timeout")
	err := error(adapterredis.CacheError{Key: "k", Op: "set", Err: inner})
	if !errors.Is(err, inner) {
		t.Error("errors.Is must reach the wrapped error")
	}
	if got := err.Error(); got != "redis: set k: io timeout" {
		t.Errorf("Error() = %q", got)
	}
}

// ── C13/C14: observer nil-safety + plain Observer fallback ────────────────────

func TestGetAdapter_NilObserver_NoPanic(t *testing.T) {
	ctx := context.Background() // no context observer, nil option observer
	fake := newFake()
	fake.store["user:42"] = []byte(`{"id":"42","name":"Ada"}`)

	out := adapterredis.GetAdapter[userQuery, user](fake, userCache(), keyByID,
		adapterredis.GetAdapterOptions{}).Transform(ctx, queryStream(userQuery{ID: "42"}))
	vals, _ := stream.Collect(ctx, out)
	if len(vals) != 1 {
		t.Errorf("want 1 value, got %v", vals)
	}
}

func TestSetAdapter_PlainObserver_GracefulSkip(t *testing.T) {
	// stats.Observer WITHOUT CacheObserver — cache events skipped, no panic.
	ctx := context.Background()
	fake := newFake()

	out := adapterredis.SetAdapter[user](fake, userCache(), keyByUser,
		adapterredis.SetAdapterOptions{Observer: plainObserver{}}).Transform(ctx, userStream(user{ID: "1", Name: "A"}))
	vals, errs := stream.Collect(ctx, out)
	if len(vals) != 1 || len(errs) != 0 {
		t.Errorf("want clean passthrough, got vals=%v errs=%v", vals, errs)
	}
}

type plainObserver struct{}

func (plainObserver) RecordValidationError(_, _, _ string)              {}
func (plainObserver) RecordRequest(_, _ string, _ int, _ time.Duration) {}
func (plainObserver) RecordSubscribe(_ string, _ bool, _ time.Duration) {}
func (plainObserver) RecordPublish(_ string, _ bool, _ time.Duration)   {}

// ── C15: CachePattern port-type acceptance ────────────────────────────────────

func TestCachePattern_PortAcceptance(t *testing.T) {
	pat := ports.CachePattern{Key: "k:{id}"}

	// SourcePort: rejected.
	_, err := ports.NewSourcePort[user]("src", userCodec, ports.PortOptions{
		Patterns: []ports.Pattern{pat},
	})
	var pre ports.PatternRegisterError
	if !errors.As(err, &pre) || pre.Kind != "cache" {
		t.Errorf("SourcePort: want PatternRegisterError{cache}, got %v", err)
	}

	// SinkPort: accepted, handle uses the payload codec.
	sink, err := ports.NewSinkPort[user]("sink", userCodec, ports.PortOptions{
		Patterns: []ports.Pattern{pat},
	})
	if err != nil {
		t.Fatalf("SinkPort: %v", err)
	}
	if _, ok := ports.CacheHandle[user](sink); !ok {
		t.Error("SinkPort: want cache handle")
	}

	// LatestPort: accepted.
	latest, err := ports.NewLatestPort[user]("latest", userCodec, ports.PortOptions{
		Patterns: []ports.Pattern{ports.CachePattern{Key: "latest-user"}},
	})
	if err != nil {
		t.Fatalf("LatestPort: %v", err)
	}
	if _, ok := ports.CacheHandle[user](latest); !ok {
		t.Error("LatestPort: want cache handle")
	}

	// ToolPort: rejected.
	_, err = ports.NewToolPort[userQuery, user]("tool", codex.Struct[userQuery](
		codex.RequiredField("id", codex.String(),
			func(q userQuery) string { return q.ID },
			func(q *userQuery, v string) { q.ID = v },
		),
	), userCodec, ports.PortOptions{Patterns: []ports.Pattern{pat}})
	if !errors.As(err, &pre) || pre.Kind != "cache" {
		t.Errorf("ToolPort: want PatternRegisterError{cache}, got %v", err)
	}

	// IOPort: accepted — covered by userCache() used throughout.
}

// ── Examples ──────────────────────────────────────────────────────────────────

func ExampleGetAdapter() {
	ctx := context.Background()
	fake := newFake()
	fake.store["user:42"] = []byte(`{"id":"42","name":"Ada"}`)

	cache := userCache()
	adapter := adapterredis.GetAdapter[userQuery, user](fake, cache,
		func(q userQuery) map[string]string { return map[string]string{"id": q.ID} },
		adapterredis.GetAdapterOptions{})

	out := adapter.Transform(ctx, queryStream(userQuery{ID: "42"}, userQuery{ID: "404"}))
	vals, _ := stream.Collect(ctx, out)
	for _, u := range vals {
		fmt.Println(u.Name)
	}
	// Output: Ada
}

func ExampleSeed() {
	ctx := context.Background()
	fake := newFake()
	fake.store["latest-oee"] = []byte(`{"id":"line-1","name":"0.87"}`)

	cache := ports.Cache[user]{Key: "latest-oee", Format: userCache().Format}
	v, ok, err := adapterredis.Seed(ctx, fake, cache, adapterredis.SeedOptions{})
	fmt.Println(ok, err == nil, v.Name)
	// Output: true true 0.87
}
