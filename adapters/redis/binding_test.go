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
	"github.com/DaniDeer/go-codex/api/events"
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
	), userCodec, ports.PortOptions{})
	if err != nil {
		panic(err)
	}
	c, err := port.PluginCachePattern(ports.CachePattern{Key: "user:{id}", TTL: 15 * time.Minute})
	if err != nil {
		panic(err)
	}
	return c
}

// userCacheMerged mirrors userCache but declares "id" as a merge-capable key
// param via [ports.NewCacheKeyParam] — used for GetMerged/SetHandle tests.
func userCacheMerged() ports.Cache[user] {
	port, err := ports.NewIOPort[userQuery, user]("user-cache-merged", codex.Struct[userQuery](
		codex.RequiredField("id", codex.String(),
			func(q userQuery) string { return q.ID },
			func(q *userQuery, v string) { q.ID = v },
		),
	), userCodec, ports.PortOptions{})
	if err != nil {
		panic(err)
	}
	c, err := port.PluginCachePattern(ports.CachePattern{Key: "user:{id}", TTL: 15 * time.Minute,
		Opts: []ports.CacheOpt{
			ports.NewCacheKeyParam("id", codex.String(),
				func(u user) string { return u.ID },
				func(u *user, v string) { u.ID = v }),
		},
	})
	if err != nil {
		panic(err)
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

// ── Get / Set — plain-function standalone entrypoints ─────────────────────────

// G1: Get hit — same behavior as GetAdapter, no ports.IOAdapter/stream involved.
func TestGet_Hit(t *testing.T) {
	ctx := context.Background()
	fake := newFake()
	fake.store["user:42"] = []byte(`{"id":"42","name":"Ada"}`)

	v, ok, err := adapterredis.Get(ctx, fake, userCache(), map[string]string{"id": "42"}, adapterredis.GetOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok || v.Name != "Ada" {
		t.Errorf("want hit with Name=Ada, got ok=%v v=%+v", ok, v)
	}
}

// G2: Get miss, default — (zero, false, nil), not an error.
func TestGet_Miss_Default(t *testing.T) {
	obs := &cacheObserverSpy{}
	ctx := stats.WithObserver(context.Background(), obs)
	fake := newFake()

	v, ok, err := adapterredis.Get(ctx, fake, userCache(), map[string]string{"id": "42"}, adapterredis.GetOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok || v != (user{}) {
		t.Errorf("want (zero, false), got (%+v, %v)", v, ok)
	}
	if obs.misses != 1 {
		t.Errorf("want 1 RecordCacheMiss, got %d", obs.misses)
	}
}

// G3: Get miss, MissIsError — CacheError wrapping ErrCacheMiss.
func TestGet_Miss_AsError(t *testing.T) {
	ctx := context.Background()
	fake := newFake()

	_, ok, err := adapterredis.Get(ctx, fake, userCache(), map[string]string{"id": "42"},
		adapterredis.GetOptions{MissIsError: true})
	if ok {
		t.Error("want ok=false on miss")
	}
	if !errors.Is(err, adapterredis.ErrCacheMiss) {
		t.Errorf("want errors.Is(ErrCacheMiss), got %v", err)
	}
	var ce adapterredis.CacheError
	if !errors.As(err, &ce) || ce.Op != "get" {
		t.Errorf("want CacheError{op get}, got %v", err)
	}
}

// G4: Get decode/validation failure.
func TestGet_DecodeFailure(t *testing.T) {
	ctx := context.Background()
	fake := newFake()
	fake.store["user:42"] = []byte(`{"id":"42","name":""}`) // fails minLength(1)

	_, ok, err := adapterredis.Get(ctx, fake, userCache(), map[string]string{"id": "42"}, adapterredis.GetOptions{})
	if ok {
		t.Error("want ok=false on decode failure")
	}
	var ce adapterredis.CacheError
	if !errors.As(err, &ce) || ce.Op != "get" {
		t.Errorf("want CacheError{op get}, got %v", err)
	}
}

// G5: Get missing key var — CacheKeyError, no I/O.
func TestGet_MissingKeyVar(t *testing.T) {
	ctx := context.Background()
	fake := newFake()

	_, _, err := adapterredis.Get(ctx, fake, userCache(), map[string]string{}, adapterredis.GetOptions{})
	var ke ports.CacheKeyError
	if !errors.As(err, &ke) || ke.Var != "id" {
		t.Errorf("want CacheKeyError{Var: id}, got %v", err)
	}
}

// G6: Set writes a single value — no ports.IOAdapter/stream involved.
func TestSet_WritesValue(t *testing.T) {
	obs := &cacheObserverSpy{}
	ctx := stats.WithObserver(context.Background(), obs)
	fake := newFake()

	err := adapterredis.Set(ctx, fake, userCache(), map[string]string{"id": "42"},
		user{ID: "42", Name: "Ada"}, adapterredis.SetOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(fake.store["user:42"]) == "" {
		t.Error("want value written to store")
	}
	if obs.writes[true] != 1 {
		t.Errorf("want 1 successful RecordCacheWrite, got %d", obs.writes[true])
	}
}

// G7: Set TTL — cache default, then option override.
func TestSet_TTL(t *testing.T) {
	ctx := context.Background()
	fake := newFake()

	if err := adapterredis.Set(ctx, fake, userCache(), map[string]string{"id": "1"},
		user{ID: "1", Name: "A"}, adapterredis.SetOptions{}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if fake.ttls["user:1"] != 15*time.Minute {
		t.Errorf("want pattern TTL 15m, got %v", fake.ttls["user:1"])
	}

	if err := adapterredis.Set(ctx, fake, userCache(), map[string]string{"id": "2"},
		user{ID: "2", Name: "B"}, adapterredis.SetOptions{TTL: time.Hour}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if fake.ttls["user:2"] != time.Hour {
		t.Errorf("want option TTL 1h override, got %v", fake.ttls["user:2"])
	}
}

// G8: Set write failure — CacheError from the underlying client.
func TestSet_WriteFailure(t *testing.T) {
	ctx := context.Background()
	fake := newFake()
	fake.setErr = errors.New("connection refused")

	err := adapterredis.Set(ctx, fake, userCache(), map[string]string{"id": "42"},
		user{ID: "42", Name: "Ada"}, adapterredis.SetOptions{})
	var ce adapterredis.CacheError
	if !errors.As(err, &ce) || ce.Op != "set" {
		t.Errorf("want CacheError{op set}, got %v", err)
	}
}

// G9: GetAdapter/SetAdapter delegation regression — pipeline behavior is
// byte-identical to calling Get/Set directly (same observer counts, same
// stored bytes), proving the Rule B1 refactor didn't change behavior.
func TestGetSetAdapter_DelegatesToGetSet_SameBehavior(t *testing.T) {
	ctx := context.Background()
	fakeDirect := newFake()
	fakeAdapter := newFake()

	// Direct calls.
	if err := adapterredis.Set(ctx, fakeDirect, userCache(), map[string]string{"id": "42"},
		user{ID: "42", Name: "Ada"}, adapterredis.SetOptions{}); err != nil {
		t.Fatalf("Set: %v", err)
	}
	direct, ok, err := adapterredis.Get(ctx, fakeDirect, userCache(), map[string]string{"id": "42"}, adapterredis.GetOptions{})
	if err != nil || !ok {
		t.Fatalf("Get: ok=%v err=%v", ok, err)
	}

	// Same sequence via the pipeline adapters.
	setOut := adapterredis.SetAdapter[user](fakeAdapter, userCache(), keyByUser,
		adapterredis.SetAdapterOptions{}).Transform(ctx, userStream(user{ID: "42", Name: "Ada"}))
	stream.Collect(ctx, setOut)
	getOut := adapterredis.GetAdapter[userQuery, user](fakeAdapter, userCache(), keyByID,
		adapterredis.GetAdapterOptions{}).Transform(ctx, queryStream(userQuery{ID: "42"}))
	viaAdapter, _ := stream.Collect(ctx, getOut)

	if len(viaAdapter) != 1 || viaAdapter[0] != direct {
		t.Errorf("want identical result via adapter and direct call, got %+v vs %+v", viaAdapter, direct)
	}
	if string(fakeDirect.store["user:42"]) != string(fakeAdapter.store["user:42"]) {
		t.Errorf("want identical stored bytes, got %q vs %q", fakeDirect.store["user:42"], fakeAdapter.store["user:42"])
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

// R1C-2: Cache (Redis) is an internal store boundary with no caller — the
// shared respond/handle/log action model is realized by composing the
// existing OnError hook with a declared events.ErrorChannel (Phase 1B), the
// same pattern as SQL/File. This locks that composition actually works.
type cacheWriteErr struct{ msg string }

func (e cacheWriteErr) Error() string { return "cache write: " + e.msg }

type cacheErrPayload struct {
	Code    string
	Message string
}

func (e cacheErrPayload) Error() string { return "cache error " + e.Code }

var cacheErrPayloadCodec = codex.Struct[cacheErrPayload](
	codex.RequiredField("code", codex.String().Refine(validate.NonEmptyString),
		func(e cacheErrPayload) string { return e.Code },
		func(e *cacheErrPayload, v string) { e.Code = v },
	),
	codex.RequiredField("message", codex.String(),
		func(e cacheErrPayload) string { return e.Message },
		func(e *cacheErrPayload, v string) { e.Message = v },
	),
)

func TestDrainSetAdapter_OnError_ComposesWithEventsErrorChannel(t *testing.T) {
	ctx := context.Background()
	fake := newFake()
	fake.setErr = cacheWriteErr{msg: "connection refused"}

	b := events.NewBuilder(events.Info{Title: "Test", Version: "1.0.0"})
	errHandle, err := events.NewChannel[user]("users/cache", userCodec,
		events.ErrorChannel[cacheWriteErr, cacheErrPayload](
			"users/cache/errors", cacheErrPayloadCodec,
			func(e cacheWriteErr) (cacheErrPayload, error) {
				return cacheErrPayload{Code: "cache_write", Message: e.msg}, nil
			},
		),
	).Register(b)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}

	var publishedTopic string
	var loggedErr error

	adapterredis.DrainSetAdapter[user](fake, userCache(), keyByUser,
		adapterredis.SetAdapterOptions{OnError: func(e error) {
			resp, matched, mapErr := errHandle.ErrorResponseFor(e)
			if matched && mapErr == nil && resp.Action == events.ErrorRespond {
				publishedTopic = resp.Topic
				return
			}
			loggedErr = e
		}},
	).Activate(ctx, userStream(user{ID: "1", Name: "A"}))

	if loggedErr != nil {
		t.Errorf("want no fallback log for matched error, got %v", loggedErr)
	}
	if publishedTopic != "users/cache/errors" {
		t.Errorf("got topic %q, want users/cache/errors", publishedTopic)
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

	// SourcePort/ToolPort: rejected — neither type carries a
	// PluginCachePattern method at all (compile-time absence, stronger
	// than the old runtime PatternRegisterError check: "a cache does not
	// produce a stream"/"a cache is not a tool surface").

	// SinkPort: accepted, handle uses the payload codec.
	sink, err := ports.NewSinkPort[user]("sink", userCodec, ports.PortOptions{})
	if err != nil {
		t.Fatalf("SinkPort: %v", err)
	}
	if _, err := sink.PluginCachePattern(pat); err != nil {
		t.Errorf("SinkPort: want cache handle, got err %v", err)
	}

	// LatestPort: accepted.
	latest, err := ports.NewLatestPort[user]("latest", userCodec, ports.PortOptions{})
	if err != nil {
		t.Fatalf("LatestPort: %v", err)
	}
	if _, err := latest.PluginCachePattern(ports.CachePattern{Key: "latest-user"}); err != nil {
		t.Errorf("LatestPort: want cache handle, got err %v", err)
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

// ── G2: cache merge-field convenience (GetMerged / SetHandle wiring) ────────

// G2-1: GetMerged merges key vars into the decoded value when the cache
// declares merge-capable key params.
func TestGetMerged_MergesKeyVarsIntoDecodedValue(t *testing.T) {
	ctx := context.Background()
	fake := newFake()
	// Stored body JSON deliberately carries a DIFFERENT id — merge must
	// OVERWRITE it with the value extracted from the key vars.
	fake.store["user:42"] = []byte(`{"id":"stale","name":"Ada"}`)

	v, ok, err := adapterredis.GetMerged(ctx, fake, userCacheMerged(), map[string]string{"id": "42"}, adapterredis.GetOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok {
		t.Fatal("want hit")
	}
	if v.ID != "42" {
		t.Errorf("ID: want merged from key vars, got %q", v.ID)
	}
	if v.Name != "Ada" {
		t.Errorf("Name: want Ada, got %q", v.Name)
	}
}

// G2-1 (regression guard): a cache with NO merge-capable key params behaves
// identically to a bare Get.
func TestGetMerged_NoMergeFields_MatchesPlainGet(t *testing.T) {
	ctx := context.Background()
	fake := newFake()
	fake.store["user:42"] = []byte(`{"id":"42","name":"Ada"}`)

	viaGet, _, err := adapterredis.Get(ctx, fake, userCache(), map[string]string{"id": "42"}, adapterredis.GetOptions{})
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	viaGetMerged, _, err := adapterredis.GetMerged(ctx, fake, userCache(), map[string]string{"id": "42"}, adapterredis.GetOptions{})
	if err != nil {
		t.Fatalf("GetMerged: %v", err)
	}
	if viaGet != viaGetMerged {
		t.Errorf("GetMerged should match plain Get when no merge fields declared: %+v vs %+v", viaGet, viaGetMerged)
	}
}

// GetMerged on a miss behaves like Get — no merge attempted, no error.
func TestGetMerged_Miss_NoMergeAttempted(t *testing.T) {
	ctx := context.Background()
	fake := newFake()
	_, ok, err := adapterredis.GetMerged(ctx, fake, userCacheMerged(), map[string]string{"id": "404"}, adapterredis.GetOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok {
		t.Error("want miss, got hit")
	}
}

// G2-2: SetHandle derives key vars from v's own merge-field-declared struct
// fields — no manual vars map needed.
func TestSetHandle_DerivesVarsFromValue(t *testing.T) {
	ctx := context.Background()
	fake := newFake()

	if err := adapterredis.SetHandle(ctx, fake, userCacheMerged(), user{ID: "77", Name: "Grace"}, adapterredis.SetOptions{}); err != nil {
		t.Fatalf("SetHandle: %v", err)
	}
	if _, ok := fake.store["user:77"]; !ok {
		t.Error("want value stored under key derived from user.ID")
	}
}

// G2-2 (regression guard): SetHandle with no merge fields declared behaves
// identically to a bare Set with an explicit vars map... actually SetHandle
// with no merge fields derives an EMPTY vars map, so it only matches a Set
// call with no {var} placeholders in the key template (a var-free key).
func TestSetHandle_NoMergeFields_VarFreeKey(t *testing.T) {
	ctx := context.Background()
	fake := newFake()
	cache := ports.Cache[user]{Key: "singleton-user", Format: userCache().Format}

	if err := adapterredis.SetHandle(ctx, fake, cache, user{ID: "1", Name: "Ada"}, adapterredis.SetOptions{}); err != nil {
		t.Fatalf("SetHandle: %v", err)
	}
	if _, ok := fake.store["singleton-user"]; !ok {
		t.Error("want value stored under the var-free key")
	}
}

// G2-1 (adapter wiring): GetAdapter merges key vars into the decoded Resp
// via GetMerged automatically.
func TestGetAdapter_MergesKeyVarsIntoDecodedValue(t *testing.T) {
	ctx := context.Background()
	fake := newFake()
	fake.store["user:42"] = []byte(`{"id":"stale","name":"Ada"}`)

	adapter := adapterredis.GetAdapter[userQuery, user](fake, userCacheMerged(), keyByID, adapterredis.GetAdapterOptions{})
	out := adapter.Transform(ctx, queryStream(userQuery{ID: "42"}))
	vals, errs := stream.Collect(ctx, out)
	if len(errs) != 0 {
		t.Fatalf("want 0 errors, got %v", errs)
	}
	if len(vals) != 1 || vals[0].ID != "42" {
		t.Errorf("want merged ID, got %v", vals)
	}
}

// G2-3: SetAdapter derives key vars PER-ITEM from each item's own merge
// fields when keyFn is nil — two items with different IDs must write to
// two different concrete keys.
func TestSetAdapter_DerivesVarsPerItem_WhenKeyFnNil(t *testing.T) {
	ctx := context.Background()
	fake := newFake()

	// keyFn left nil -> per-item derivation via SetHandle.
	adapter := adapterredis.SetAdapter[user](fake, userCacheMerged(), nil, adapterredis.SetAdapterOptions{})
	out := adapter.Transform(ctx, userStream(
		user{ID: "1", Name: "Ada"},
		user{ID: "2", Name: "Grace"},
	))
	vals, errs := stream.Collect(ctx, out)
	if len(errs) != 0 {
		t.Fatalf("want 0 errors, got %v", errs)
	}
	if len(vals) != 2 {
		t.Fatalf("want 2 passthrough values, got %d", len(vals))
	}
	if _, ok := fake.store["user:1"]; !ok {
		t.Error("want value stored under user:1")
	}
	if _, ok := fake.store["user:2"]; !ok {
		t.Error("want value stored under user:2")
	}
}

// G2-3: DrainSetAdapter derives key vars PER-ITEM when keyFn is nil.
func TestDrainSetAdapter_DerivesVarsPerItem_WhenKeyFnNil(t *testing.T) {
	ctx := context.Background()
	fake := newFake()

	adapter := adapterredis.DrainSetAdapter[user](fake, userCacheMerged(), nil, adapterredis.SetAdapterOptions{})
	p, err := ports.NewSinkPort[user]("drain-set-merged", userCodec, ports.PortOptions{Buffer: 4})
	if err != nil {
		t.Fatalf("construct port: %v", err)
	}
	p.Bind(ctx, adapter)
	p.Feed(ctx, userStream(user{ID: "1", Name: "Ada"}, user{ID: "2", Name: "Grace"}))

	if _, ok := fake.store["user:1"]; !ok {
		t.Error("want value stored under user:1")
	}
	if _, ok := fake.store["user:2"]; !ok {
		t.Error("want value stored under user:2")
	}
}

// Explicit (non-nil) keyFn still wins — regression guard matching today's
// behavior when set.
func TestSetAdapter_ExplicitKeyFnStillWins(t *testing.T) {
	ctx := context.Background()
	fake := newFake()

	adapter := adapterredis.SetAdapter[user](fake, userCacheMerged(),
		func(user) map[string]string { return map[string]string{"id": "static-id"} },
		adapterredis.SetAdapterOptions{})
	out := adapter.Transform(ctx, userStream(user{ID: "ignored", Name: "Ada"}))
	_, errs := stream.Collect(ctx, out)
	if len(errs) != 0 {
		t.Fatalf("want 0 errors, got %v", errs)
	}
	if _, ok := fake.store["user:static-id"]; !ok {
		t.Error("want static key used")
	}
	if _, ok := fake.store["user:ignored"]; ok {
		t.Error("want derived key NOT used when keyFn is explicit")
	}
}
