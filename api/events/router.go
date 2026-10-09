package events

import (
	"fmt"
	"log/slog"
	"slices"
	"strings"

	"github.com/DaniDeer/go-codex/internal/middleware"
	"github.com/DaniDeer/go-codex/internal/router"
)

// routable is an internal alias for [router.Routable][*Client] — kept
// under its pre-consolidation unexported name so every existing call site
// in this package (HandleOpt, Subscriber.Handle, etc.) keeps compiling
// unchanged. See docs/design/d-0009-internalize-shared-mechanics.md's Phase 1 for
// the full consolidation this package's Router now sits on top of.
//
// Events' leaves implement [router.MethodReporter] too (role(), renamed
// RouteMethod() — see [Subscriber.RouteMethod]/[Publisher.RouteMethod])
// — a DIFFERENT concept from REST's HTTP verb, reported under the SAME
// optional interface (see docs/design/d-0009-internalize-shared-mechanics.md's
// Phase 1 Design Decision #4 for the full investigation that resolved
// this).
type routable = router.Routable[*Client]

// joinRouterTopic joins a prefix and a topic with exactly ONE "/" between
// them, regardless of whether either side already has a leading/trailing
// separator. An empty prefix or topic contributes nothing. Unlike
// [api/rest]'s joinRouterPath, the result NEVER gets a forced leading
// "/" — MQTT/ZeroMQ-style topics don't use a leading separator.
func joinRouterTopic(prefix, topic string) string {
	prefix = strings.TrimSuffix(prefix, "/")
	topic = strings.TrimPrefix(topic, "/")
	switch {
	case prefix == "" && topic == "":
		return ""
	case prefix == "":
		return topic
	case topic == "":
		return prefix
	default:
		return prefix + "/" + topic
	}
}

// Router declares a topic PREFIX once and groups any number of
// independently-declared [Subscriber]/[Publisher] values under it,
// optionally attaching reusable-class [middleware.RouteMiddleware] to
// every leaf registered under it in one declaration — events' own thin
// wrapper around the shared [router.Router][*Client] core (not a type
// alias — see docs/design/d-0009-internalize-shared-mechanics.md's Phase 1 Design
// Decision #2 for why a pure alias is incompatible with RouterEntry's
// per-package Method/Role/neither field shape), adapted for the
// subscribe/publish role axis instead of REST's HTTP-method axis.
//
//	rt := events.NewRouter("sensors").
//	    Use(authMiddleware).
//	    Route(temperatureChannel.WithSubscribe(events.Subscribe{}).WithHandler(onTemp)).
//	    Route(humidityChannel.WithPublish(events.Publish{}))
//	err := rt.Register(client)
type Router struct{ inner router.Router[*Client] }

// RouterOpt configures a [Router] at construction time — reserved for
// future extension; no concrete RouterOpt implementations ship yet. See
// [api/rest.RouterOpt]'s doc comment for the shared rationale.
type RouterOpt interface{ applyRouter(*Router) }

// NewRouter declares a Router with a STATIC topic prefix (no `{var}`
// placeholders for v1 — see docs/roadmap/declarative-router-groups.md's
// "Out of scope (Phase 2+)"). opts is reserved for future extension; no
// concrete [RouterOpt] implementations ship yet.
func NewRouter(prefix string, opts ...RouterOpt) Router {
	rt := Router{inner: router.NewRouter[*Client](prefix, joinRouterTopic, wrapRouterPrefixError)}
	for _, o := range opts {
		o.applyRouter(&rt)
	}
	return rt
}

// Use returns a NEW Router with mws appended to its own accumulated,
// permanent middleware list — dispatched BEFORE every grouped leaf's own
// middleware (Router-first, outer-to-inner ordering), and before any
// nested [Router.Mount]/[Router.Group] child's own mws.
func (rt Router) Use(mws ...middleware.RouteMiddleware) Router {
	return Router{inner: rt.inner.Use(mws...)}
}

// Tags returns a NEW Router with tags appended to its own accumulated,
// permanent tag list — merged into every grouped leaf's OWN
// [ChannelMeta.Tags] at [Router.Walk]/[Router.Register] time (Router's
// own tags first, then the leaf's own). Repeated `.Tags(a).Tags(b)`
// calls ACCUMULATE, mirroring [Router.Use]'s identical semantics — no
// one-shot/[Router.With]-equivalent exists for tags in this first pass.
// See [api/rest.Router.Tags]'s doc comment for why tags are merged AFTER
// the leaf's own opts resolve (an append, not a prepend, unlike mws) —
// identical rationale, identical mechanism here.
//
// Targets [ChannelMeta.Tags] (channel-item level) ONLY — NOT
// [Subscribe.Tags]/[Publish.Tags] (operation level) — simpler, and
// arguably the more natural target ("this whole channel belongs to
// group X").
func (rt Router) Tags(tags ...string) Router {
	return Router{inner: rt.inner.Tags(tags...)}
}

// With returns a NEW Router whose NEXT [Router.Route] call ONLY receives
// mws, in addition to (never in place of) rt's own permanent middleware —
// one-shot, not permanent. See [api/rest.Router.With]'s doc comment for
// the full accumulate-semantics contract (identical here), including the
// Route-SCOPED-ONLY restriction — With does NOT pair with [Router.Mount]/
// [Router.Group]; a preceding .With(mw) is silently DROPPED at Mount time
// rather than leaking onto a later, unrelated .Route() call.
func (rt Router) With(mws ...middleware.RouteMiddleware) Router {
	return Router{inner: rt.inner.With(mws...)}
}

// Route returns a NEW Router with leaf attached — leaf's own prefix/
// middleware composition happens later, during [Router.Walk]/
// [Router.Register], once every ancestor [Router.Mount]/[Router.Group]'s
// own contribution is known.
func (rt Router) Route(leaf routable) Router {
	return Router{inner: rt.inner.Route(leaf)}
}

// Mount returns a NEW Router with sub attached as a nested child — a NEW
// topic segment (rt's prefix + sub's own prefix) and a FRESH middleware
// stack for everything under sub.
func (rt Router) Mount(sub Router) Router {
	return Router{inner: rt.inner.Mount(sub.inner)}
}

// Group returns a NEW Router with fn's built-up child incorporated — SAME
// prefix as rt (NO new topic segment), just a scoped middleware subset
// for a SUBSET of channels sharing rt's own topic prefix. fn receives an
// EMPTY child Router and MUST explicitly return its built-up value. See
// [api/rest.Router.Group]'s doc comment for the shared rationale,
// including why rt.Group(fn) returns rt ITSELF (the parent), not the
// child.
//
// Events' "role axis" (subscribe vs. publish) has no structural
// enforcement at the Group level — it is purely a naming/organization
// convention a caller may choose to follow (e.g. one Group per role),
// never checked or required by Router itself.
func (rt Router) Group(fn func(sub Router) Router) Router {
	return Router{inner: rt.inner.Group(func(sub router.Router[*Client]) router.Router[*Client] {
		return fn(Router{inner: sub}).inner
	})}
}

// RouterEntry describes one leaf's FINAL, fully-assembled view, returned
// by [Router.Routes]/[Router.Walk].
type RouterEntry struct {
	// Role is this leaf's role: "subscribe" or "publish".
	Role string
	// Path is the FINAL, prefix-applied topic.
	Path string
	// MiddlewareNames lists every reusable-class middleware name that will
	// apply to this leaf, in dispatch order — Router-contributed names
	// first (outermost ancestor first), then the leaf's own.
	MiddlewareNames []string
	// Tags lists every tag that will apply to this leaf's spec entry —
	// Router-contributed tags first (outermost ancestor first), then the
	// leaf's own [ChannelMeta.Tags].
	Tags []string
}

// WalkFunc is called once per LEAF (never per intermediate Router/Group),
// after full prefix + middleware composition. A non-nil error stops the
// walk immediately and is returned as-is (no wrapping) — mirrors chi's own
// Walk short-circuit behavior.
type WalkFunc func(entry RouterEntry) error

// Walk is the one true primitive — recurses through every Mount/Group,
// composing prefixes and middleware exactly as [Router.Register] would,
// calling fn once per leaf, in declaration order.
func (rt Router) Walk(fn WalkFunc) error {
	return rt.inner.Walk(func(e router.RouterEntry) error {
		return fn(RouterEntry{
			Role:            e.Method,
			Path:            e.Path,
			MiddlewareNames: e.MiddlewareNames,
			Tags:            e.Tags,
		})
	})
}

// Routes is a convenience wrapper over [Router.Walk] — collects every leaf
// into a flat, declaration-ordered slice. Walkable and printable WITHOUT
// calling [Router.Register] (no [Client] needed).
func (rt Router) Routes() []RouterEntry {
	var entries []RouterEntry
	_ = rt.Walk(func(e RouterEntry) error {
		entries = append(entries, e)
		return nil
	})
	return entries
}

// Register walks the tree, composes every leaf's final prefix+middleware,
// and registers each one with c — the SAME, UNCHANGED [Subscriber.Register]/
// [Publisher.Handle] each leaf's own type already implements; Router
// performs ZERO new validation logic.
//
// A leaf whose final, prefix-applied topic fails the SAME validation its
// own Register/Handle would apply standalone surfaces [RouterPrefixError],
// wrapping the real cause. Every OTHER error a leaf's own Register/Handle
// can return (ChannelTypeConflictError, MissingHandlerError, security
// coverage failures, etc.) propagates COMPLETELY UNWRAPPED, exactly as it
// would from a direct, Router-less call.
func (rt Router) Register(c *Client) error {
	return rt.inner.Register(c)
}

// wrapRouterPrefixError is the [router.PrefixErrorFunc] supplied to
// [router.NewRouter] — does exactly what this package's inline
// asInvalidTopicError + RouterPrefixError{...} construction did before
// this consolidation, now relocated into a closure.
func wrapRouterPrefixError(prefix, composed string, err error) (error, bool) {
	var topicErr InvalidTopicError
	if asInvalidTopicError(err, &topicErr) {
		return RouterPrefixError{Prefix: prefix, ComposedTopic: composed, Err: err}, true
	}
	return nil, false
}

// asInvalidTopicError reports whether err is (or wraps) an
// [InvalidTopicError] — a tiny local helper so [wrapRouterPrefixError]
// doesn't need to import "errors" solely for one errors.As call.
func asInvalidTopicError(err error, target *InvalidTopicError) bool {
	type unwrapper interface{ Unwrap() error }
	for {
		if v, ok := err.(InvalidTopicError); ok {
			*target = v
			return true
		}
		u, ok := err.(unwrapper)
		if !ok {
			return false
		}
		err = u.Unwrap()
		if err == nil {
			return false
		}
	}
}

// RouterPrefixError is returned by [Router.Register] ONLY when a leaf's
// final, prefix-applied topic fails the SAME validation its own Register/
// Handle would apply standalone (e.g. events' topic-codec check via
// [WithTopicCodec]/[WithTopicConstraints]).
//
// Carries ONLY Prefix+ComposedTopic+Err — all of which Router already
// knows/computes without any extra accessor on the leaf itself. Every
// OTHER error a leaf's own Register/Handle can return propagates
// COMPLETELY UNWRAPPED. See [api/rest.RouterPrefixError]'s doc comment for
// the shared rationale.
type RouterPrefixError struct {
	Prefix        string // the Router's own (or accumulated-nested) prefix
	ComposedTopic string // the leaf's FINAL, prefix-applied topic that actually failed
	Err           error  // the underlying error from the leaf's own Register/Handle
}

func (e RouterPrefixError) Error() string {
	return fmt.Sprintf("api/events: router: prefix %q: topic %q: %s", e.Prefix, e.ComposedTopic, e.Err.Error())
}

// Unwrap allows errors.As/errors.Is to traverse the underlying cause.
func (e RouterPrefixError) Unwrap() error { return e.Err }

// LogValue implements [slog.LogValuer] for structured logging.
func (e RouterPrefixError) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("prefix", e.Prefix),
		slog.String("composed_topic", e.ComposedTopic),
		slog.Any("err", e.Err),
	)
}

// channelTagsOpt is the ChannelOpt a [Router] APPENDS (never prepends) to
// a leaf's channel's own opts list via [Router.Tags] — see that method's
// doc comment for why tags must be merged AFTER the leaf's own
// [ChannelMeta] opt resolves, unlike middleware (which is prepended to
// run BEFORE). Targets [ChannelMeta.Tags] (channel-item level) ONLY.
type channelTagsOpt struct{ tags []string }

func (o channelTagsOpt) applyChannel(cb *channelBuilder) {
	cb.meta.Tags = append(append([]string{}, o.tags...), cb.meta.Tags...)
}

// WithRouterPrefix implements [router.Routable] for [Subscriber]. mws are
// PREPENDED by first dispatching them, in isolation, through a fresh,
// empty Subscriber[T]'s own [Subscriber.Use] (reusing its EXISTING,
// battle-tested legacy/codec-backed-middleware dispatch logic) and then
// prepending the resulting mws/middlewareHandlers to s's own
// ALREADY-dispatched lists — avoiding any need to reimplement or replay
// that dispatch logic here. tags are APPENDED directly onto
// s.channel.opts (a distinct mutation point from mws/middlewareHandlers
// above — see [channelTagsOpt]'s doc comment for why tags merge
// differently than mws).
func (s Subscriber[T]) WithRouterPrefix(prefix string, mws []middleware.RouteMiddleware, tags []string) (routable, string) {
	s.channel.topic = joinRouterTopic(prefix, s.channel.topic)
	if len(mws) > 0 {
		var pre Subscriber[T]
		pre = pre.Use(mws...)
		s.mws = append(slices.Clone(pre.mws), s.mws...)
		s.middlewareHandlers = append(slices.Clone(pre.middlewareHandlers), s.middlewareHandlers...)
	}
	if len(tags) > 0 {
		s.channel.opts = append(slices.Clone(s.channel.opts), channelTagsOpt{tags: tags})
	}
	return s, s.channel.topic
}

// RouteMethod implements [router.MethodReporter] for [Subscriber] —
// reports events' role ("subscribe"), reusing REST's method-equivalent
// optional interface under its generic name (see [routable]'s doc
// comment for why this is a deliberately shared interface, unlike
// [router.Routable] itself's other methods).
func (s Subscriber[T]) RouteMethod() string { return "subscribe" }

// MiddlewareNames implements [router.Routable] for [Subscriber].
func (s Subscriber[T]) MiddlewareNames() []string {
	// s.mws covers every legacy/codec-backed .Use()-attached middleware.
	// s.middlewareHandlers ALSO needs including — [Subscriber.SubscribeBoundMW]
	// attaches bound middleware there DIRECTLY (never touching s.mws at
	// all), so omitting it would silently drop every bound middleware's
	// name from [RouterEntry.MiddlewareNames] — a confirmed, previously-real
	// gap (SubscribeBoundMW is a documented, actively-used feature, not an
	// obscure corner). MiddlewareHandler already carries its own Name field,
	// so no new plumbing is needed here, unlike api/rest/api/reqreply's
	// bound opt types.
	names := make([]string, 0, len(s.mws)+len(s.middlewareHandlers))
	for _, mw := range s.mws {
		names = append(names, mw.Name)
	}
	for _, h := range s.middlewareHandlers {
		names = append(names, h.Name)
	}
	return names
}

// RouteTags implements [router.Routable] for [Subscriber] — reports s's
// OWN, directly-declared [ChannelMeta.Tags], BEFORE any Router
// involvement. Resolves s.channel's own opts through a scratch
// [channelBuilder] (read-only; never mutates s) — NOT a naive per-opt
// iteration — because [ChannelMeta.applyChannel] is a WHOLE-STRUCT
// OVERWRITE (`cb.meta = m`): if a channel declares 2+ separate
// ChannelMeta opts, only the LAST one's Tags survive into the real
// registered spec. Replaying through a scratch channelBuilder guarantees
// this accessor reports EXACTLY what Register will produce. Mirrors
// [api/reqreply]'s identical mechanism.
func (s Subscriber[T]) RouteTags() []string {
	var cb channelBuilder
	for _, opt := range s.channel.opts {
		opt.applyChannel(&cb)
	}
	return cb.meta.Tags
}

// RegisterAny implements [router.Routable] for [Subscriber] — delegates
// to [Subscriber.Register] (not [Subscriber.Handle]): Register is the
// primary, handler-requiring registration path (populates
// [Client.SubscriberEntries] for a future whole-client
// ServeSubscribers), and is the ONLY Subscriber method with "Register" in
// its contract — mirroring [api/rest]'s own Route.Register choice.
func (s Subscriber[T]) RegisterAny(c *Client) error {
	return s.Register(c)
}

// WithRouterPrefix implements [router.Routable] for [Publisher]. See
// [Subscriber.WithRouterPrefix]'s doc comment for the shared
// prepend-via-fresh-value rationale (mws) and tags-merge rationale.
func (p Publisher[T]) WithRouterPrefix(prefix string, mws []middleware.RouteMiddleware, tags []string) (routable, string) {
	p.channel.topic = joinRouterTopic(prefix, p.channel.topic)
	if len(mws) > 0 {
		var pre Publisher[T]
		pre = pre.Use(mws...)
		p.mws = append(slices.Clone(pre.mws), p.mws...)
		p.clientMiddlewareHandlers = append(slices.Clone(pre.clientMiddlewareHandlers), p.clientMiddlewareHandlers...)
	}
	if len(tags) > 0 {
		p.channel.opts = append(slices.Clone(p.channel.opts), channelTagsOpt{tags: tags})
	}
	return p, p.channel.topic
}

// RouteMethod implements [router.MethodReporter] for [Publisher] —
// reports events' role ("publish").
func (p Publisher[T]) RouteMethod() string { return "publish" }

// MiddlewareNames implements [router.Routable] for [Publisher].
func (p Publisher[T]) MiddlewareNames() []string {
	// See [Subscriber.MiddlewareNames]'s identical comment above —
	// p.clientMiddlewareHandlers holds [Publisher.PublishBoundMW]-attached
	// bound middleware, never touching p.mws.
	names := make([]string, 0, len(p.mws)+len(p.clientMiddlewareHandlers))
	for _, mw := range p.mws {
		names = append(names, mw.Name)
	}
	for _, h := range p.clientMiddlewareHandlers {
		names = append(names, h.Name)
	}
	return names
}

// RouteTags implements [router.Routable] for [Publisher] — reports p's
// OWN, directly-declared [ChannelMeta.Tags], BEFORE any Router
// involvement.
func (p Publisher[T]) RouteTags() []string {
	// See [Subscriber.RouteTags]'s identical doc comment above — same
	// scratch-channelBuilder-replay rationale applies unchanged.
	var cb channelBuilder
	for _, opt := range p.channel.opts {
		opt.applyChannel(&cb)
	}
	return cb.meta.Tags
}

// RegisterAny implements [router.Routable] for [Publisher] — [Publisher]
// has no Register method (only [Subscriber] does; a Publisher never
// needs a declare-time handler to dispatch, so there is no equivalent
// SubscriberEntries-style registry for it) — delegates to
// [Publisher.Handle], discarding the returned handle.
func (p Publisher[T]) RegisterAny(c *Client) error {
	_, err := p.Handle(c)
	return err
}

// HandleOpt configures a [Subscriber.Handle]/[Publisher.Handle] call —
// currently only [WithRouter]. Operates on the [routable] interface (the
// SAME one [Router] itself uses internally) rather than a generic
// Subscriber[T]/Publisher[T] directly, since an interface method cannot
// introduce new type parameters — Handle converts to/from routable
// internally.
//
// Supersedes docs/design/d-0008-declarative-router-groups.md's original
// "No ClientHandle/WithRouter equivalent needed for api/events" finding
// — that finding assumed every caller either goes through
// [Router.Register] + [Client.ServeSubscribers]/[Router.Routes] (true for
// the SUBSCRIBE role, which has no bare/standalone accessor at all) or
// never calls [Publisher.Handle]/[Subscriber.Handle] directly outside a
// Router. In practice, a channel Mounted under a REAL (non-empty) prefix
// can ALSO be published/subscribed via a bare, standalone
// [Publisher.Handle]/[Subscriber.Handle] call (or passed directly to
// [Client.Publish]/[Client.Subscribe]) from code that never touches the
// Router value — exactly the same gap [api/rest.WithRouter]/
// [api/reqreply.WithRouter] already closed. WithRouter closes it here
// too, for full 3-pattern parity.
type HandleOpt interface {
	applyHandle(r routable) routable
}

// WithRouter tells [Subscriber.Handle]/[Publisher.Handle] to apply rt's
// CURRENT accumulated prefix+middleware before building the handle — the
// EXACT SAME composition `rt.Route(sub)`/`rt.Route(pub)` + `rt.Register(c)`
// would have produced for the SAME leaf registered through rt. rt is the
// single, unambiguous source of truth for the prefix, eliminating the
// forgot-to-reapply-the-prefix-string risk a raw-string-based alternative
// would have had.
//
// Does NOT require the leaf to have actually been [Router.Route]'d into
// rt — it is a pure composition convenience, not a validation that the
// pairing is registered (mirrors [api/rest.WithRouter]'s own existing
// infallible, non-validating character). For a MULTI-LEVEL nested
// [Router.Mount]/[Router.Group], pass the SPECIFIC (innermost) Router
// value the leaf was (or will be) [Router.Route]'d into — its own
// effective prefix already composes all ancestor Routers' contributions
// transitively, consistent with [Router.Register]'s own existing
// nested-composition behavior.
func WithRouter(rt Router) HandleOpt { return withRouterOpt{rt: rt} }

type withRouterOpt struct{ rt Router }

func (o withRouterOpt) applyHandle(r routable) routable {
	transformed, _ := r.WithRouterPrefix(o.rt.inner.Prefix(), o.rt.inner.OwnMiddleware(), o.rt.inner.OwnTags())
	return transformed
}
