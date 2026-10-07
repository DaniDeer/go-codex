package events_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/DaniDeer/go-codex/api/events"
	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/middleware"
)

// ── fixtures ─────────────────────────────────────────────────────────────

type routerTestPayload struct{ Name string }

var routerTestPayloadCodec = codex.Struct[routerTestPayload](
	codex.RequiredField("name", codex.String(),
		func(p routerTestPayload) string { return p.Name },
		func(p *routerTestPayload, v string) { p.Name = v },
	),
)

func routerTestHandler(context.Context, routerTestPayload) error { return nil }

func newRouterTestSubscriber(topic string) events.Subscriber[routerTestPayload] {
	return events.NewChannel[routerTestPayload](topic, routerTestPayloadCodec).
		WithSubscribe(events.Subscribe{}).
		WithHandler(routerTestHandler)
}

func newRouterTestPublisher(topic string) events.Publisher[routerTestPayload] {
	return events.NewChannel[routerTestPayload](topic, routerTestPayloadCodec).
		WithPublish(events.Publish{})
}

// ── tests ────────────────────────────────────────────────────────────────

func TestRouter_Route_ComposesPrefixCorrectly(t *testing.T) {
	sub := newRouterTestSubscriber("users/created")
	rt := events.NewRouter("api/v1").Route(sub)

	entries := rt.Routes()
	if len(entries) != 1 {
		t.Fatalf("want 1 entry, got %d", len(entries))
	}
	if entries[0].Path != "api/v1/users/created" {
		t.Errorf("want path %q, got %q", "api/v1/users/created", entries[0].Path)
	}
	if entries[0].Role != "subscribe" {
		t.Errorf("want role subscribe, got %q", entries[0].Role)
	}
}

func TestRouter_Route_NoLeadingSlashForced(t *testing.T) {
	sub := newRouterTestSubscriber("x")
	rt := events.NewRouter("").Route(sub)
	entries := rt.Routes()
	if entries[0].Path != "x" {
		t.Errorf("events topics must never get a forced leading '/', got %q", entries[0].Path)
	}
}

func TestRouter_Mount_ComposesNestedPrefixesTransitively(t *testing.T) {
	inner := events.NewRouter("users").Route(newRouterTestSubscriber("{id}/created"))
	outer := events.NewRouter("api/v1").Mount(inner)

	entries := outer.Routes()
	if len(entries) != 1 {
		t.Fatalf("want 1 entry, got %d", len(entries))
	}
	want := "api/v1/users/{id}/created"
	if entries[0].Path != want {
		t.Errorf("want path %q, got %q", want, entries[0].Path)
	}
}

func TestRouter_Use_AppliesToEveryGroupedLeaf(t *testing.T) {
	mw := middleware.Middleware{Name: "audit"}

	client := events.NewClient()
	rt := events.NewRouter("api").
		Use(mw).
		Route(newRouterTestSubscriber("a")).
		Route(newRouterTestSubscriber("b"))

	if err := rt.Register(client); err != nil {
		t.Fatalf("Register: %v", err)
	}

	for _, e := range rt.Routes() {
		found := false
		for _, name := range e.MiddlewareNames {
			if name == "audit" {
				found = true
			}
		}
		if !found {
			t.Errorf("topic %q missing Router-contributed middleware %q", e.Path, "audit")
		}
	}
}

func TestRouter_Use_DeclarationOrderIsDispatchOrder(t *testing.T) {
	outer := middleware.Middleware{Name: "outer"}
	sub := newRouterTestSubscriber("x").Use(middleware.Middleware{Name: "inner"})
	rt := events.NewRouter("api").Use(outer).Route(sub)

	entries := rt.Routes()
	if len(entries) != 1 {
		t.Fatalf("want 1 entry, got %d", len(entries))
	}
	names := entries[0].MiddlewareNames
	if len(names) != 2 || names[0] != "outer" || names[1] != "inner" {
		t.Errorf("want [outer inner], got %v", names)
	}
}

func TestRouter_Group_SharesPrefixAddsScopedMiddlewareOnly(t *testing.T) {
	scoped := middleware.Middleware{Name: "scoped"}
	rt := events.NewRouter("api").
		Route(newRouterTestSubscriber("open")).
		Group(func(sub events.Router) events.Router {
			return sub.Use(scoped).Route(newRouterTestPublisher("open"))
		})

	entries := rt.Routes()
	if len(entries) != 2 {
		t.Fatalf("want 2 entries, got %d", len(entries))
	}
	for _, e := range entries {
		if e.Path != "api/open" {
			t.Errorf("want path %q (Group adds NO new segment), got %q", "api/open", e.Path)
		}
		hasScoped := false
		for _, n := range e.MiddlewareNames {
			if n == "scoped" {
				hasScoped = true
			}
		}
		if e.Role == "publish" && !hasScoped {
			t.Errorf("publish leaf (inside Group) should have scoped middleware")
		}
		if e.Role == "subscribe" && hasScoped {
			t.Errorf("subscribe leaf (outside Group) should NOT have scoped middleware")
		}
	}
}

func TestRouter_With_AppliesToNextRouteOnlyNotSiblings(t *testing.T) {
	mw := middleware.Middleware{Name: "oneshot"}
	rt := events.NewRouter("api")
	rt = rt.With(mw).Route(newRouterTestSubscriber("a"))
	rt = rt.Route(newRouterTestSubscriber("b"))

	entries := rt.Routes()
	if len(entries) != 2 {
		t.Fatalf("want 2 entries, got %d", len(entries))
	}
	for _, e := range entries {
		has := false
		for _, n := range e.MiddlewareNames {
			if n == "oneshot" {
				has = true
			}
		}
		switch e.Path {
		case "api/a":
			if !has {
				t.Errorf("a should have the one-shot middleware")
			}
		case "api/b":
			if has {
				t.Errorf("b should NOT have the one-shot middleware")
			}
		}
	}
}

func TestRouter_With_RepeatedCallsAccumulateNotOverwrite(t *testing.T) {
	mw1 := middleware.Middleware{Name: "mw1"}
	mw2 := middleware.Middleware{Name: "mw2"}
	rt := events.NewRouter("api").With(mw1).With(mw2).Route(newRouterTestSubscriber("a"))

	entries := rt.Routes()
	names := entries[0].MiddlewareNames
	if len(names) != 2 || names[0] != "mw1" || names[1] != "mw2" {
		t.Errorf("want both mw1 and mw2 accumulated, got %v", names)
	}
}

func TestRouter_Immutability_OriginalValueUnaffectedByChaining(t *testing.T) {
	rt := events.NewRouter("api")
	mw := middleware.Middleware{Name: "mw"}
	rt2 := rt.Use(mw).Route(newRouterTestSubscriber("a"))

	if len(rt.Routes()) != 0 {
		t.Errorf("original Router mutated: want 0 routes, got %d", len(rt.Routes()))
	}
	if len(rt2.Routes()) != 1 {
		t.Errorf("want 1 route on rt2, got %d", len(rt2.Routes()))
	}
}

func TestRouter_Routes_EquivalentToWalkCollected(t *testing.T) {
	rt := events.NewRouter("api").
		Route(newRouterTestSubscriber("a")).
		Route(newRouterTestPublisher("b"))

	var walked []events.RouterEntry
	err := rt.Walk(func(e events.RouterEntry) error {
		walked = append(walked, e)
		return nil
	})
	if err != nil {
		t.Fatalf("Walk: %v", err)
	}
	routes := rt.Routes()
	if len(walked) != len(routes) {
		t.Fatalf("want %d walked entries, got %d", len(routes), len(walked))
	}
	for i := range routes {
		a, b := walked[i], routes[i]
		if a.Role != b.Role || a.Path != b.Path || len(a.MiddlewareNames) != len(b.MiddlewareNames) {
			t.Errorf("entry %d mismatch: walked=%+v routes=%+v", i, a, b)
		}
	}
}

func TestRouter_Walk_StopsOnFirstError(t *testing.T) {
	sentinel := errors.New("stop")
	rt := events.NewRouter("api").
		Route(newRouterTestSubscriber("a")).
		Route(newRouterTestSubscriber("b"))

	calls := 0
	err := rt.Walk(func(events.RouterEntry) error {
		calls++
		return sentinel
	})
	if !errors.Is(err, sentinel) {
		t.Errorf("want sentinel error, got %v", err)
	}
	if calls != 1 {
		t.Errorf("want exactly 1 call before stopping, got %d", calls)
	}
}

func TestRouter_Register_IndistinguishableFromDirectRegister(t *testing.T) {
	clientA := events.NewClient()
	clientB := events.NewClient()

	directSub := newRouterTestSubscriber("users")
	if err := directSub.Register(clientA); err != nil {
		t.Fatalf("direct Register: %v", err)
	}

	var routedHandle *events.ChannelHandle[routerTestPayload]
	sub := events.NewChannel[routerTestPayload]("users", routerTestPayloadCodec,
		events.WithHandleCallback(func(h *events.ChannelHandle[routerTestPayload]) { routedHandle = h }),
	).WithSubscribe(events.Subscribe{}).WithHandler(routerTestHandler)

	rt := events.NewRouter("").Route(sub)
	if err := rt.Register(clientB); err != nil {
		t.Fatalf("Router Register: %v", err)
	}
	if routedHandle == nil {
		t.Fatal("want WithHandleCallback to fire")
	}

	entriesA := clientA.SubscriberEntries()
	if len(entriesA) != 1 || entriesA[0].Topic() != routedHandle.Topic {
		t.Errorf("want same topic registered via Router as via direct Register")
	}
}

func TestRouter_NonPrefixErrors_PropagateUnwrapped(t *testing.T) {
	client := events.NewClient()
	// A Subscriber with no handler attached -> a genuine, Router-unrelated
	// MissingHandlerError. Confirms Router.Register does NOT wrap every
	// possible leaf error in RouterPrefixError, only topic-composition
	// failures.
	sub := events.NewChannel[routerTestPayload]("x", routerTestPayloadCodec).WithSubscribe(events.Subscribe{})
	rt := events.NewRouter("api").Route(sub)

	err := rt.Register(client)
	var prefixErr events.RouterPrefixError
	if errors.As(err, &prefixErr) {
		t.Fatalf("non-prefix errors must propagate UNWRAPPED, got RouterPrefixError: %v", err)
	}
	var missingErr events.MissingHandlerError
	if !errors.As(err, &missingErr) {
		t.Fatalf("want MissingHandlerError, got %v (%T)", err, err)
	}
}

func TestRouter_InvalidComposedTopic_ReturnsRouterPrefixError(t *testing.T) {
	denyDoubleSlash := codex.Constraint[string]{
		Name:    "no-double-slash",
		Check:   func(v string) bool { return !containsDoubleSlash(v) },
		Message: func(v string) string { return "topic must not contain //" },
	}
	client := events.NewClient(events.WithTopicConstraints(denyDoubleSlash))
	rt := events.NewRouter("api").Route(newRouterTestSubscriber("//users"))

	err := rt.Register(client)
	var prefixErr events.RouterPrefixError
	if !errors.As(err, &prefixErr) {
		t.Fatalf("want RouterPrefixError, got %v (%T)", err, err)
	}
}

func containsDoubleSlash(v string) bool {
	for i := 0; i+1 < len(v); i++ {
		if v[i] == '/' && v[i+1] == '/' {
			return true
		}
	}
	return false
}

func TestRouter_ConcurrentReads_SafeByImmutability(t *testing.T) {
	base := events.NewRouter("api")
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r := base.Route(newRouterTestSubscriber("x"))
			_ = r.Routes()
		}()
	}
	wg.Wait()
	if len(base.Routes()) != 0 {
		t.Errorf("base Router must remain untouched by concurrent derivations")
	}
}

func TestWithHandleCallback_FiresOnBothRoles(t *testing.T) {
	var subGot, pubGot bool

	var gotSub *events.ChannelHandle[routerTestPayload]
	sub := events.NewChannel[routerTestPayload]("a", routerTestPayloadCodec,
		events.WithHandleCallback(func(h *events.ChannelHandle[routerTestPayload]) { gotSub = h; subGot = true }),
	).WithSubscribe(events.Subscribe{}).WithHandler(routerTestHandler)
	if _, err := sub.Handle(nil); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if !subGot || gotSub == nil {
		t.Error("want WithHandleCallback to fire for subscribe role")
	}

	var gotPub *events.ChannelHandle[routerTestPayload]
	pub := events.NewChannel[routerTestPayload]("b", routerTestPayloadCodec,
		events.WithHandleCallback(func(h *events.ChannelHandle[routerTestPayload]) { gotPub = h; pubGot = true }),
	).WithPublish(events.Publish{})
	if _, err := pub.Handle(nil); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if !pubGot || gotPub == nil {
		t.Error("want WithHandleCallback to fire for publish role")
	}
}

func TestHandle_WithRouter_MatchesRegisteredTopic_Subscribe(t *testing.T) {
	sub := newRouterTestSubscriber("users")
	rt := events.NewRouter("api/v1")

	handle, err := sub.Handle(nil, events.WithRouter(rt))
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	const want = "api/v1/users"
	if handle.Topic != want {
		t.Errorf("want topic %q, got %q", want, handle.Topic)
	}

	// Confirm this EXACTLY matches what rt.Route(sub)+rt.Register(client)
	// would have composed, server-side.
	client := events.NewClient(events.WithInfo(events.Info{Title: "t", Version: "1"}))
	if err := rt.Route(newRouterTestSubscriber("users")).Register(client); err != nil {
		t.Fatalf("Register: %v", err)
	}
	entries := rt.Route(newRouterTestSubscriber("users")).Routes()
	if entries[len(entries)-1].Path != want {
		t.Errorf("want registered path %q, got %q", want, entries[len(entries)-1].Path)
	}
}

func TestHandle_WithRouter_MatchesRegisteredTopic_Publish(t *testing.T) {
	pub := newRouterTestPublisher("alerts")
	rt := events.NewRouter("sensor")

	handle, err := pub.Handle(nil, events.WithRouter(rt))
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	const want = "sensor/alerts"
	if handle.Topic != want {
		t.Errorf("want topic %q, got %q", want, handle.Topic)
	}
}

func TestHandle_WithRouter_DoesNotMutateOriginal(t *testing.T) {
	// [Subscriber]/[Publisher] are immutable value types; WithRouter must
	// never mutate the original (unrouted) value — mirrors every other
	// decoration method's (.Use, .SubscribeMW, ...) existing guarantee.
	sub := newRouterTestSubscriber("users")
	rt := events.NewRouter("api/v1")

	if _, err := sub.Handle(nil, events.WithRouter(rt)); err != nil {
		t.Fatalf("Handle: %v", err)
	}

	plain, err := sub.Handle(nil)
	if err != nil {
		t.Fatalf("Handle (original): %v", err)
	}
	if plain.Topic != "users" {
		t.Errorf("original Subscriber must remain untouched — want topic %q, got %q", "users", plain.Topic)
	}
}

func TestHandle_ZeroArgs_StillCompilesAndWorks(t *testing.T) {
	sub := newRouterTestSubscriber("plain")
	handle, err := sub.Handle(nil)
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if handle.Topic != "plain" {
		t.Errorf("want topic %q, got %q", "plain", handle.Topic)
	}
}

func TestWithSubscribeHandleCallback_FiresOnlyForSubscribe(t *testing.T) {
	fired := false
	sub := events.NewChannel[routerTestPayload]("a", routerTestPayloadCodec,
		events.WithSubscribeHandleCallback(func(*events.ChannelHandle[routerTestPayload]) { fired = true }),
	).WithSubscribe(events.Subscribe{}).WithHandler(routerTestHandler)
	if _, err := sub.Handle(nil); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if !fired {
		t.Error("want WithSubscribeHandleCallback to fire for subscribe role")
	}

	fired = false
	pub := events.NewChannel[routerTestPayload]("b", routerTestPayloadCodec,
		events.WithSubscribeHandleCallback(func(*events.ChannelHandle[routerTestPayload]) { fired = true }),
	).WithPublish(events.Publish{})
	if _, err := pub.Handle(nil); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if fired {
		t.Error("WithSubscribeHandleCallback must NOT fire for publish role")
	}
}

func TestWithPublishHandleCallback_FiresOnlyForPublish(t *testing.T) {
	fired := false
	pub := events.NewChannel[routerTestPayload]("b", routerTestPayloadCodec,
		events.WithPublishHandleCallback(func(*events.ChannelHandle[routerTestPayload]) { fired = true }),
	).WithPublish(events.Publish{})
	if _, err := pub.Handle(nil); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if !fired {
		t.Error("want WithPublishHandleCallback to fire for publish role")
	}

	fired = false
	sub := events.NewChannel[routerTestPayload]("a", routerTestPayloadCodec,
		events.WithPublishHandleCallback(func(*events.ChannelHandle[routerTestPayload]) { fired = true }),
	).WithSubscribe(events.Subscribe{}).WithHandler(routerTestHandler)
	if _, err := sub.Handle(nil); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if fired {
		t.Error("WithPublishHandleCallback must NOT fire for subscribe role")
	}
}

func TestRouter_Tags_AccumulateNotOverwrite(t *testing.T) {
	rt := events.NewRouter("api").Tags("a").Tags("b").Route(newRouterTestSubscriber("x"))
	entries := rt.Routes()
	tags := entries[0].Tags
	if len(tags) != 2 || tags[0] != "a" || tags[1] != "b" {
		t.Errorf("want both tags accumulated, got %v", tags)
	}
}

func TestRouter_Tags_SurviveLeafsOwnChannelMeta(t *testing.T) {
	sub := events.NewChannel[routerTestPayload]("x", routerTestPayloadCodec,
		events.ChannelMeta{Tags: []string{"own-tag"}},
	).WithSubscribe(events.Subscribe{}).WithHandler(routerTestHandler)
	rt := events.NewRouter("api").Tags("router-tag").Route(sub)

	entries := rt.Routes()
	tags := entries[0].Tags
	if len(tags) != 2 || tags[0] != "router-tag" || tags[1] != "own-tag" {
		t.Errorf("want [router-tag own-tag] (Router's own first, then the leaf's), got %v", tags)
	}

	client := events.NewClient()
	if err := rt.Register(client); err != nil {
		t.Fatalf("Register: %v", err)
	}
}

func TestRouter_Tags_AccumulateAcrossMount(t *testing.T) {
	inner := events.NewRouter("users").Tags("users").Route(newRouterTestSubscriber("created"))
	outer := events.NewRouter("api").Tags("api").Mount(inner)

	entries := outer.Routes()
	tags := entries[0].Tags
	if len(tags) != 2 || tags[0] != "api" || tags[1] != "users" {
		t.Errorf("want [api users] (outermost ancestor first), got %v", tags)
	}
}

func TestRouter_Tags_RoutesEquivalentToWalk(t *testing.T) {
	rt := events.NewRouter("api").Tags("t1").Route(newRouterTestSubscriber("a"))
	var walked []events.RouterEntry
	_ = rt.Walk(func(e events.RouterEntry) error {
		walked = append(walked, e)
		return nil
	})
	routes := rt.Routes()
	if len(walked[0].Tags) != len(routes[0].Tags) || walked[0].Tags[0] != routes[0].Tags[0] {
		t.Errorf("Walk and Routes disagree on Tags: %v vs %v", walked[0].Tags, routes[0].Tags)
	}
}
