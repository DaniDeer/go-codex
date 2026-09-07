package events

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/middleware"
)

// Middleware is a codec-backed, events-specific middleware declaration —
// the per-pattern counterpart to [middleware.Declaration], adding events'
// own topic-var merge vocabulary. Unlike [rest.Middleware][In, Out] (which
// has independent request AND response merge vocabularies, since REST has
// header/cookie/query on BOTH sides), events has only ONE wire location
// per role — the topic — and subscribe/publish are asymmetric (subscribe
// has no reply channel to encode a value INTO; publish has no incoming
// message to decode a value FROM). Concretely:
//   - Subscribe (RECEIVING role): In is decoded from incoming topic vars
//     (via topicMergeFieldsIn + Declaration.InCodec) — Out is UNUSED.
//   - Publish (SENDING role): Out is encoded into outgoing topic vars (via
//     topicMergeFieldsOut + Declaration.OutCodec) — In is UNUSED.
//
// Built via [NewMiddleware], populated via [Middleware.WithSubscribeTopic]/
// [Middleware.WithPublishTopic] — REUSING the SAME [NewTopicParam]
// constructor a channel's own Item already uses (generic over any T, not
// hardcoded to a channel's own payload type). No new events-side param
// constructor exists for this.
//
// Middleware supports TWO attachment styles, mirroring [rest.Middleware]
// exactly:
//   - Channel-BOUND: attached via [Transform]/[ClientTransform], whose fn
//     additionally receives the channel's own msg *T/msg T (read/enrich
//     access on subscribe; caller's own already-built value on publish) —
//     for concerns whose fn genuinely needs that access.
//   - Channel-AGNOSTIC: a T-FREE fn bundled directly onto this value via
//     [Middleware.WithReceive]/[Middleware.WithSend], attached via plain
//     .Use(mw) — reusable verbatim across many channels.
//
// A single Middleware value must use EXACTLY ONE style, never both — see
// [AmbiguousMiddlewareAttachmentError].
type Middleware[In, Out any] struct {
	middleware.Declaration[In, Out]

	topicMergeFieldsIn  []codex.FieldCodec[In]
	topicMergeFieldsOut []codex.FieldCodec[Out]

	// receiveFn/sendFn, when set (via WithReceive/WithSend below), carry a
	// T-FREE runtime Fn directly on the value itself — enabling
	// channel-AGNOSTIC attachment via plain .Use(mw). Left nil for the
	// channel-BOUND case, where Transform/ClientTransform supply a
	// msg-accessing fn separately instead (never both — combining is
	// rejected as ambiguous via [AmbiguousMiddlewareAttachmentError]).
	//
	// sendFn deliberately returns (Out, error), NOT (In, error) — the
	// roadmap doc's original draft showed WithSend producing In (a
	// leftover mirroring rest.Middleware's own WithSend, not updated for
	// events' In/Out role swap); corrected here for consistency with
	// [ClientTransform]'s own (Out, error)-producing fn shape, since
	// publish's OWN Declaration.OutCodec is what validates and encodes
	// whatever a publish-side Fn produces, regardless of attachment
	// style. See docs/design/d-0003-codec-declared-middlewares.md's
	// "Lessons learned" section.
	receiveFn func(ctx context.Context, in In) error
	sendFn    func(ctx context.Context) (Out, error)
}

// NewMiddleware builds a [Middleware] from a [middleware.Declaration] —
// chain [Middleware.WithSubscribeTopic]/[Middleware.WithPublishTopic] to
// populate its topic merge-field vocabulary, then attach via
// [Transform]/[ClientTransform] (channel-BOUND) or [Middleware.WithReceive]/
// [Middleware.WithSend] + plain .Use(mw) (channel-AGNOSTIC).
func NewMiddleware[In, Out any](decl middleware.Declaration[In, Out]) Middleware[In, Out] {
	return Middleware[In, Out]{Declaration: decl}
}

// WithSubscribeTopic registers one topic-var merge field into mw's OWN In
// vocabulary — reuses [NewTopicParam][In, V] directly (no new events-side
// constructor). Only meaningful for a topic var the channel's OWN template
// declares but that the channel's own Item does NOT already merge via its
// OWN NewTopicParam (e.g. a template "sensors/{sensorID}/{region}/data"
// where the channel merges only sensorID, leaving region for this
// middleware to claim instead) — see
// docs/design/d-0003-codec-declared-middlewares.md's events section for
// the full scope rationale.
func (m Middleware[In, Out]) WithSubscribeTopic(p MergedTopicParam[In]) Middleware[In, Out] {
	m.topicMergeFieldsIn = append(cloneFieldCodecs(m.topicMergeFieldsIn), p.Field)
	return m
}

// WithPublishTopic is [Middleware.WithSubscribeTopic]'s publish-side
// sibling — registers one topic-var merge field into mw's OWN Out
// vocabulary, encoded into the outgoing publish's topic vars once
// [ClientTransform]'s (or a bundled [Middleware.WithSend]'s) fn produces
// an Out value.
func (m Middleware[In, Out]) WithPublishTopic(p MergedTopicParam[Out]) Middleware[In, Out] {
	m.topicMergeFieldsOut = append(cloneFieldCodecs(m.topicMergeFieldsOut), p.Field)
	return m
}

// cloneFieldCodecs is a tiny generic helper avoiding aliasing bugs across
// chained With* calls — mirrors rest.Middleware's identical use of
// slices.Clone for the same reason.
func cloneFieldCodecs[T any](fs []codex.FieldCodec[T]) []codex.FieldCodec[T] {
	out := make([]codex.FieldCodec[T], len(fs))
	copy(out, fs)
	return out
}

// WithReceive attaches a channel-AGNOSTIC Fn directly to mw — NEITHER
// WithReceive NOR WithSend below mentions T, so the returned
// Middleware[In,Out] value (fn included) can be passed to .Use(...)
// verbatim, on as many different channels as needed. Use [Transform]
// instead when fn genuinely needs msg *T access.
func (m Middleware[In, Out]) WithReceive(fn func(ctx context.Context, in In) error) Middleware[In, Out] {
	m.receiveFn = fn
	return m
}

// WithSend is [Middleware.WithReceive]'s publish-side sibling — fn
// produces mw's own Out value (the SAME shape [ClientTransform]'s bound fn
// produces — see [Middleware]'s doc comment for why this is Out, not In).
func (m Middleware[In, Out]) WithSend(fn func(ctx context.Context) (Out, error)) Middleware[In, Out] {
	m.sendFn = fn
	return m
}

// RouteMiddlewareMarker makes Middleware[In,Out] satisfy
// [middleware.RouteMiddleware] — EXPORTED (unlike [ports.Pattern]'s
// unexported-method sealing) because Go's unexported-method interface
// satisfaction is scoped per package: a type declared in api/events can
// never satisfy an interface whose method is unexported in package
// middleware, no matter the name — see [middleware.RouteMiddleware]'s doc
// comment. So a channel's plain .Use(...) can recognize and dispatch a
// channel-agnostic Middleware value carrying a WithReceive/WithSend fn.
func (Middleware[In, Out]) RouteMiddlewareMarker() {}

// applyAgnosticSubscriber implements the events-side routeMiddlewareContributor
// pattern — called by [Subscriber.Use] for a bundled Middleware value. In
// is concrete here, so it can build the SAME runtime dispatch handler
// [Transform] produces for the channel-BOUND case.
func (m Middleware[In, Out]) applyAgnosticSubscriber() (MiddlewareHandler, bool) {
	if m.receiveFn == nil {
		return MiddlewareHandler{}, false
	}
	return buildAgnosticMiddlewareHandler(m), true
}

// applyAgnosticPublisher is [applyAgnosticSubscriber]'s publish-side
// sibling — called by [Publisher.Use] for a bundled Middleware value.
func (m Middleware[In, Out]) applyAgnosticPublisher() (ClientMiddlewareHandler, bool) {
	if m.sendFn == nil {
		return ClientMiddlewareHandler{}, false
	}
	return buildAgnosticClientMiddlewareHandler(m), true
}

// isBundled reports whether mw carries a WithReceive/WithSend Fn — used by
// [checkMiddlewareNameUniquenessAndAttachment]'s D7 ambiguous-dual-attachment
// check, mirroring rest's identical technique.
func (m Middleware[In, Out]) isBundled() bool {
	return m.receiveFn != nil || m.sendFn != nil
}

// MiddlewareInputError is returned when a [Middleware]'s In value fails to
// decode/validate from the raw incoming topic vars (subscribe side only —
// publish has no In to decode).
//
// Use errors.As to extract the middleware name:
//
//	var mie events.MiddlewareInputError
//	if errors.As(err, &mie) {
//	    log.Printf("middleware %q: invalid input: %v", mie.Name, mie.Err)
//	}
type MiddlewareInputError struct {
	Name string
	Err  error
}

func (e MiddlewareInputError) Error() string {
	return fmt.Sprintf("api/events: middleware %q: invalid input: %s", e.Name, e.Err.Error())
}

func (e MiddlewareInputError) Unwrap() error { return e.Err }

// LogValue implements [slog.LogValuer] for structured logging.
func (e MiddlewareInputError) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("name", e.Name),
		slog.Any("err", e.Err),
	)
}

// MiddlewareError is returned when a [Transform]/[ClientTransform] fn
// (or a bundled WithReceive/WithSend fn) returns its own business error
// that does NOT match any declared [ErrorChannel] pattern — D2's fallback,
// mirrors [rest.MiddlewareError] exactly.
type MiddlewareError struct {
	Name string
	Err  error
}

func (e MiddlewareError) Error() string {
	return fmt.Sprintf("api/events: middleware %q: %s", e.Name, e.Err.Error())
}

func (e MiddlewareError) Unwrap() error { return e.Err }

// LogValue implements [slog.LogValuer] for structured logging.
func (e MiddlewareError) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("name", e.Name),
		slog.Any("err", e.Err),
	)
}

// DuplicateMiddlewareNameError is returned by [Subscriber.Handle]/
// [Publisher.Handle] when two [Middleware] values with the SAME
// Declaration.Name are attached to one channel (D6(b)) — mirrors
// [rest.DuplicateMiddlewareNameError] exactly.
type DuplicateMiddlewareNameError struct {
	Topic string
	Name  string
}

func (e DuplicateMiddlewareNameError) Error() string {
	return fmt.Sprintf("api/events: channel %q: duplicate middleware name %q", e.Topic, e.Name)
}

// LogValue implements [slog.LogValuer] for structured logging.
func (e DuplicateMiddlewareNameError) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("topic", e.Topic),
		slog.String("name", e.Name),
	)
}

// AmbiguousMiddlewareAttachmentError is returned by [Subscriber.Handle]/
// [Publisher.Handle] when a SINGLE [Middleware] value carries a bundled
// WithReceive/WithSend Fn AND is ALSO passed to [Transform]/[ClientTransform]
// on the SAME channel (D7) — mirrors
// [rest.AmbiguousMiddlewareAttachmentError] exactly.
type AmbiguousMiddlewareAttachmentError struct {
	Name string
}

func (e AmbiguousMiddlewareAttachmentError) Error() string {
	return fmt.Sprintf("api/events: middleware %q: attached via BOTH .Use() (bundled fn) and Transform/ClientTransform (separate fn) — ambiguous, use only one attachment style per value", e.Name)
}

// LogValue implements [slog.LogValuer] for structured logging.
func (e AmbiguousMiddlewareAttachmentError) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("name", e.Name),
	)
}

// checkEventsMiddlewareNameUniquenessAndAttachment enforces D6(b) and D7
// from docs/design/d-0003-codec-declared-middlewares.md — mirrors
// api/rest's identical checkMiddlewareNameUniquenessAndAttachment exactly,
// adapted for events' split subscribe/publish handler lists (a channel's
// SAME topic can have independent Subscriber/Publisher declarations, so
// names are checked separately per role, matching how [Subscriber.Handle]/
// [Publisher.Handle] are themselves independent calls).
func checkEventsMiddlewareNameUniquenessAndAttachment(topic string, middlewareHandlers []MiddlewareHandler, clientMiddlewareHandlers []ClientMiddlewareHandler) error {
	seen := make(map[string]bool, len(middlewareHandlers)+len(clientMiddlewareHandlers))
	for _, h := range middlewareHandlers {
		if seen[h.Name] {
			return DuplicateMiddlewareNameError{Topic: topic, Name: h.Name}
		}
		seen[h.Name] = true
		if h.dualAttached {
			return AmbiguousMiddlewareAttachmentError{Name: h.Name}
		}
	}
	seen = make(map[string]bool, len(clientMiddlewareHandlers))
	for _, h := range clientMiddlewareHandlers {
		if seen[h.Name] {
			return DuplicateMiddlewareNameError{Topic: topic, Name: h.Name}
		}
		seen[h.Name] = true
		if h.dualAttached {
			return AmbiguousMiddlewareAttachmentError{Name: h.Name}
		}
	}
	return nil
}

// eventsMiddlewareContributor is implemented by [Middleware][In, Out] (via
// its unexported applyAgnosticSubscriber/applyAgnosticPublisher methods)
// to let [Subscriber.Use]/[Publisher.Use] apply a channel-AGNOSTIC
// codec-backed middleware's runtime dispatch handler WITHOUT Use itself
// needing to know In/Out — mirrors api/rest's identical
// routeMiddlewareContributor pattern.
type eventsMiddlewareContributor interface {
	middleware.RouteMiddleware
	applyAgnosticSubscriber() (MiddlewareHandler, bool)
	applyAgnosticPublisher() (ClientMiddlewareHandler, bool)
}
