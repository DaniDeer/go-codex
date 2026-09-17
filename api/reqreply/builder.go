package reqreply

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"unicode"

	"github.com/DaniDeer/go-codex/api/internal"
	"github.com/DaniDeer/go-codex/codex"
	asyncapi "github.com/DaniDeer/go-codex/render/asyncapi/v3"
	"github.com/DaniDeer/go-codex/route"
	"github.com/DaniDeer/go-codex/schema"
)

// Server accumulates [Route] registrations, produces an AsyncAPI 3.0
// document with request-reply operations, AND owns request-reply
// dispatch/transport once a [ServerTransport] is attached via
// [Server.Attach] — mirrors [rest.Server]'s exact unification of spec
// accumulation and dispatch (there is no separate "Builder" type,
// confirmed via docs/design/d-0004-reqreply-workflow-simplification.md's
// Decision 1). [Builder]/[NewBuilder] are DEPRECATED aliases retained
// for existing callers — Server is a strict superset, so no behavior
// changes for pre-existing spec-only usage.
//
// Create a Server with [NewServer], add servers via [AddServer], register
// routes via [Route.Register] (optionally with [Route.WithHandler] to
// attach a dispatch handler), call [AsyncAPISpec] to produce the AsyncAPI
// document, and [Server.Attach] + [Server.Serve] to actually dispatch
// incoming requests.
type Server struct {
	docBuilder      *asyncapi.DocumentBuilder
	topics          map[string]struct{} // guard against duplicate topic registration
	securitySchemes map[string]SecurityScheme
	globalSecurity  []route.SecurityRequirement
	topicCodec      *codex.Codec[string]
	// globalDeadLetter is the Server-level default [DeadLetter]
	// declaration, set via [Server.AddGlobalDeadLetter]. nil when none is
	// declared. Routes with no explicit DeadLetter opt inherit this.
	globalDeadLetter *deadLetterRule
	// deadLetterTopicsRegistered dedups AsyncAPI channel-entry
	// registration for [DeadLetter] destinations by topic — several
	// routes commonly share ONE dead-letter destination (e.g. via
	// [Server.AddGlobalDeadLetter]), so only the FIRST route to resolve
	// a given dead-letter topic registers its spec entry.
	deadLetterTopicsRegistered map[string]bool

	// mu guards transport/dispatchEntries, the fields mutated after
	// construction outside of Route.Register's own topic/schema
	// bookkeeping above (which callers are expected to complete before
	// calling Serve — mirrors [rest.Server]'s identical mu usage note).
	mu sync.RWMutex
	// transport is the optional, adapter-provided [ServerTransport]
	// attached via [Server.Attach] (e.g. by mqtt5.Attach/zeromq.Attach) —
	// nil until Attach is called.
	transport ServerTransport
	// dispatchEntries holds one entry per route registered via
	// [Route.WithHandler]+[Route.Register] — read by [Server.Serve] to
	// dispatch each route CONCURRENTLY (one goroutine per route; see
	// [Server.Serve]'s doc comment for why this must be concurrent, not
	// sequential).
	dispatchEntries []dispatchEntry
}

// dispatchEntry pairs a registered route's type-erased handle/topic with
// its type-erased handler function, read by [Server.Serve].
type dispatchEntry struct {
	topic string
	// handle is the *RouteHandle[Req,Resp] value (any-typed, recovered
	// via reflection by the adapter's ServerTransport.Serve, exactly like
	// [Client.Call]'s route argument).
	handle any
	// fn is the func(context.Context, Req) (Resp, error) domain handler
	// attached via [Route.WithHandler] (any-typed for the same reason).
	fn any
}

// Builder is a DEPRECATED alias for [Server] — kept for existing callers
// during the reqreply-workflow-simplification migration. New code should
// use [Server]/[NewServer] directly.
//
// Deprecated: use [Server].
type Builder = Server

// ServerOption configures a [Server] at construction time.
type ServerOption func(*Server)

// BuilderOption is a DEPRECATED alias for [ServerOption].
//
// Deprecated: use [ServerOption].
type BuilderOption = ServerOption

// WithTopicCodec sets a codec used to validate every topic passed to
// [Route.Register]. If the topic is invalid, [Route.Register] returns an
// [InvalidTopicError] immediately.
//
// Use [WithTopicConstraints] for the common case of stacking one or more
// [codex.Constraint] values; use WithTopicCodec when you need a fully-custom
// [codex.Codec]. Mirrors [events.WithTopicCodec].
//
// Example — enforce a shared topic-prefix rule across every route:
//
//	import "github.com/DaniDeer/go-codex/validate"
//
//	b := reqreply.NewServer(info, reqreply.WithTopicConstraints(validate.MQTTPublishTopic))
func WithTopicCodec(c codex.Codec[string]) BuilderOption {
	return func(b *Builder) { b.topicCodec = &c }
}

// WithTopicConstraints is a convenience wrapper around [WithTopicCodec] that
// builds a codec from [codex.String] refined with the given constraints.
// Multiple constraints are applied in order; all must pass. Mirrors
// [events.WithTopicConstraints].
//
// Users can mix built-in constraints from the validate package with their own:
//
//	deviceLevel := codex.Constraint[string]{
//	    Name:    "device-prefix",
//	    Check:   func(v string) bool { return strings.HasPrefix(v, "device/") },
//	    Message: func(v string) string { return fmt.Sprintf("topic must start with device/, got %q", v) },
//	}
//	b := reqreply.NewServer(info, reqreply.WithTopicConstraints(deviceLevel))
func WithTopicConstraints(cons ...codex.Constraint[string]) BuilderOption {
	c := codex.String().Refine(cons...)
	return WithTopicCodec(c)
}

// NewServer returns a Server initialised with the given Info.
func NewServer(info Info, opts ...ServerOption) *Server {
	s := &Server{
		docBuilder:      asyncapi.NewDocumentBuilder(info),
		topics:          make(map[string]struct{}),
		securitySchemes: make(map[string]SecurityScheme),
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// NewBuilder is a DEPRECATED alias for [NewServer].
//
// Deprecated: use [NewServer].
func NewBuilder(info Info, opts ...BuilderOption) *Builder {
	return NewServer(info, opts...)
}

// AddGlobalSecurity appends security requirements that apply to all routes
// by default. The requirements flow into runtime enforcement: routes with
// nil RouteMeta.Security inherit these requirements at the adapter layer via
// [RouteHandle.GlobalSecurity].
//
// AsyncAPI 3.0 has no document-level global security field; these
// requirements do NOT appear in the AsyncAPI spec output. To annotate
// per-route security in the spec, set [RouteMeta.Security] explicitly.
//
// To mark a specific route as explicitly unsecured (exempt from global
// security), set Security to an empty slice: Security: []route.SecurityRequirement{}.
func (b *Builder) AddGlobalSecurity(reqs ...route.SecurityRequirement) *Builder {
	b.globalSecurity = append(b.globalSecurity, reqs...)
	return b
}

// AddGlobalDeadLetter declares a Server-level default [DeadLetter]
// destination that every route with NO explicit DeadLetter opt inherits
// — mirrors [Builder.AddGlobalSecurity]'s own nil-inherit/empty-override
// precedent. To opt a specific route OUT of this default, declare
// `reqreply.DeadLetter("")` on that route explicitly.
func (b *Builder) AddGlobalDeadLetter(topic string, opts ...DeadLetterOpt) *Builder {
	rule := DeadLetter(topic, opts...).rule
	b.globalDeadLetter = &rule
	return b
}

// AddServer registers a named server entry in the AsyncAPI document.
// Entries appear in output in registration order.
//
// Use Protocol: "zmq" for ZeroMQ servers, "mqtt5" for MQTT 5.0, etc.
func (b *Builder) AddServer(name string, s ServerEntry) *Builder {
	b.docBuilder.AddServer(name, s)
	return b
}

// buildTopicParameters derives the AsyncAPI channel parameters map from a
// topic template and optional TopicParam entries — mirrors
// api/events/builder.go's function of the same name (duplicated rather
// than shared: different package, and this codebase already duplicates
// this small per-boundary conversion helper rather than factoring it into
// a shared non-codex location, matching toCodexParams/convertFileParamErr's
// precedent).
//
// Priority for each variable's schema:
//  1. TopicParam.Codec.Schema — when a codec is registered for the variable
//  2. Default: {type: string}
func buildTopicParameters(topic string, params []TopicParam) map[string]asyncapi.Parameter {
	vars := internal.ParseTemplateVars(topic)
	if len(vars) == 0 {
		return nil
	}

	// Index TopicParams by name for O(1) lookup.
	paramsByName := make(map[string]TopicParam, len(params))
	for _, tp := range params {
		paramsByName[tp.Name] = tp
	}

	result := make(map[string]asyncapi.Parameter, len(vars))
	for name := range vars {
		p := asyncapi.Parameter{}
		if tp, ok := paramsByName[name]; ok {
			p.Description = tp.Description
			if tp.Codec != nil {
				p.Schema = tp.Codec.Schema
			}
		}
		result[name] = p
	}
	return result
}

// registerRoute is called by [Route.Register] to record the two AsyncAPI
// channels and operations for a route.
func (b *Builder) registerRoute(
	topic string,
	reqSchema, respSchema schema.Schema,
	reqHeaders, respHeaders schema.Schema,
	meta RouteMeta,
	errorReplies []ErrorReplyMeta,
	topicParams []TopicParam,
) {
	b.topics[topic] = struct{}{}

	base := meta.OperationID
	if base == "" {
		base = topicToID(topic)
	}
	reqChannelKey := base
	replyChannelKey := base + "Reply"
	sendOpID := "send" + capitalise(base)
	recvOpID := "receive" + capitalise(base) + "Reply"

	// Both channel addresses share the same {varName} tokens (the reply
	// channel's address is topic + "/reply"), so both independently need
	// the same Parameters map — AsyncAPI channels each describe their own
	// address's vars, even when the vars are shared.
	params := buildTopicParameters(topic, topicParams)

	// Register request channel.
	b.docBuilder.AddChannel(reqChannelKey, asyncapi.ChannelItem{
		Address:    topic,
		Summary:    meta.Summary,
		Tags:       meta.Tags,
		Parameters: params,
		Publish: &asyncapi.Operation{
			OperationID: sendOpID,
			Summary:     meta.Summary,
			Description: meta.Description,
			Tags:        meta.Tags,
			Message: asyncapi.Message{
				Schema:     reqSchema,
				SchemaName: meta.ReqSchemaName,
				Headers:    reqHeaders,
			},
			Reply: &asyncapi.OperationReply{Channel: replyChannelKey},
		},
	})

	// Register reply channel (receive-only — exempt from subscribe/publish
	// validation). Topic 3 (docs/roadmap/error-handling-rest-events-reqreply.md):
	// ONE reply channel, ONE receive operation, carrying N message
	// variants (the success shape plus one per declared errorReplies
	// entry) via Operation.Messages — AsyncAPI 3.0's native
	// channel-level "messages" map mechanism, the direct analogue of
	// OpenAPI's per-status responses object. Per spec, an operation with
	// no explicit Messages set applies ALL of its channel's declared
	// messages — so leaving the operation-level array empty here is
	// correct, not an omission.
	replyMessages := make([]asyncapi.Message, 0, len(errorReplies)+1)
	replyMessages = append(replyMessages, asyncapi.Message{
		Name:       "Success",
		Schema:     respSchema,
		SchemaName: meta.RespSchemaName,
		Headers:    respHeaders,
	})
	for i, er := range errorReplies {
		name := "Error"
		if er.Code != "" {
			name += capitalise(topicToID(er.Code))
		} else {
			name += fmt.Sprintf("%d", i+1)
		}
		replyMessages = append(replyMessages, asyncapi.Message{
			Name:       name,
			Schema:     er.Schema,
			SchemaName: er.SchemaName,
		})
	}
	b.docBuilder.AddReplyChannel(replyChannelKey, asyncapi.ChannelItem{
		Address:    topic + "/reply",
		Parameters: params,
		Subscribe: &asyncapi.Operation{
			OperationID: recvOpID,
			Messages:    replyMessages,
		},
	})
}

// AsyncAPISpec builds and returns the accumulated AsyncAPI 3.0 document.
// Returns an error if any registered channel is invalid.
func (b *Builder) AsyncAPISpec() (asyncapi.Document, error) {
	// Aggregate SecuritySchemes from every registered route's own
	// [WithSecurityScheme] declarations (there is no per-route entry list
	// to iterate here, unlike rest/events — schemes are accumulated
	// directly into b.securitySchemes as each route registers, in
	// [Route.Register]). Collision policy is last-registered-wins,
	// matching [rest.Server.OpenAPISpec]/[events.Client.AsyncAPISpec].
	for name, s := range b.securitySchemes {
		b.docBuilder.AddSecurityScheme(name, s.SecurityScheme)
	}
	return b.docBuilder.Build()
}

// AppendTo writes all request-reply channels registered on this Builder into
// db. Servers and schemas owned by this Builder are NOT written — the caller
// is responsible for configuring those on db.
//
// Use AppendTo to combine request-reply channels with pub/sub channels from
// [api/events.Client] in a single AsyncAPI 3.0 document:
//
//	import asyncapi "github.com/DaniDeer/go-codex/render/asyncapi/v3"
//
//	doc := asyncapi.NewDocumentBuilder(info)
//	doc.AddServer("mqtt5", asyncapi.Server{URL: "mqtts://...", Protocol: "mqtt5"})
//
//	eventsB.AppendTo(doc)    // pub/sub channels
//	reqreplyB.AppendTo(doc)  // request-reply channels
//
//	spec, err := doc.Build()
func (b *Builder) AppendTo(db *asyncapi.DocumentBuilder) error {
	b.docBuilder.AppendChannelsTo(db)
	return nil
}

// ServerTransport is implemented by each adapter's internal, unexported
// binding attached to a [Server] via an adapter-specific Attach function
// (e.g. [mqtt5.Attach], [zeromq.Attach]) — see [Server.Attach]. Mirrors
// [rest.ServerTransport]/[events.Transport], with ONE confirmed
// structural difference: Serve is called ONCE PER REGISTERED ROUTE (not
// once for the whole Server the way [rest.ServerTransport.Serve](ctx)
// does), because reqreply transports (mqtt5/zeromq/etc.) have no
// built-in equivalent of an HTTP mux to pre-wire many routes into one
// call — see [Server.Serve]'s doc comment for the confirmed concurrent-
// dispatch requirement this implies.
type ServerTransport interface {
	// Serve dispatches route (dynamic type *[RouteHandle][Req,Resp]) via
	// fn (dynamic type func(context.Context, Req) (Resp, error)). Blocks
	// until ctx is cancelled or a fatal error occurs — the SAME per-call
	// contract [rest.ServerTransport.Serve]/[events.Transport.Subscribe]
	// document, just scoped to one route instead of every route.
	Serve(ctx context.Context, route any, fn any) error
}

// Attach binds t to s as s's server transport — the "attach the adapter to
// the server" step behind [Server.Serve]. Each adapter provides its own
// entry point (e.g. [mqtt5.Attach](server, client, router)) that builds an
// internal ServerTransport implementation and calls this method
// internally; application code calls the ADAPTER's Attach function, not
// this method directly, in the common case.
//
// Returns [ServerTransportAlreadyAttachedError] if s already has a
// transport attached — Attach is exclusive, mirrors [rest.Server.Attach]/
// [events.Client.Attach] exactly.
func (s *Server) Attach(t ServerTransport) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.transport != nil {
		return ServerTransportAlreadyAttachedError{}
	}
	s.transport = t
	return nil
}

// Serve dispatches every route registered via [Route.WithHandler]+
// [Route.Register], via s's attached [ServerTransport], CONCURRENTLY —
// one goroutine per route, NOT a sequential loop. This is CONFIRMED
// necessary (not merely a style choice): some transports' ServerTransport.
// Serve call is non-blocking (registers + returns immediately, e.g.
// mqtt5, where real dispatch happens via the client library's own
// background goroutine), while others BLOCK FOREVER inside their own
// receive loop until ctx is cancelled (e.g. zeromq, which has no
// built-in mux to pre-wire many routes into one call). A sequential loop
// would permanently starve every route after the first blocking-style
// transport call — see docs/design/d-0004-reqreply-workflow-simplification.md's
// Decision 1 "concurrent dispatch" finding for the full confirmed
// evidence.
//
// Serve blocks until ctx is cancelled (waiting for every route's Serve
// call to finish, mirroring [rest.Server.Serve]'s "blocks until ctx
// cancelled" contract) OR one route's Serve call returns a real error
// (which cancels every other still-running route and is returned
// promptly, without waiting for ctx to be cancelled).
//
// Returns [NoServerTransportAttachedError] if [Server.Attach] was never
// called.
func (s *Server) Serve(ctx context.Context) error {
	s.mu.RLock()
	t := s.transport
	entries := append([]dispatchEntry(nil), s.dispatchEntries...)
	s.mu.RUnlock()
	if t == nil {
		return NoServerTransportAttachedError{}
	}
	if len(entries) == 0 {
		<-ctx.Done()
		return nil
	}

	innerCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	errCh := make(chan error, len(entries))
	var wg sync.WaitGroup
	wg.Add(len(entries))
	for _, e := range entries {
		go func(e dispatchEntry) {
			defer wg.Done()
			errCh <- t.Serve(innerCtx, e.handle, e.fn)
		}(e)
	}

	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()

	select {
	case err := <-errCh:
		if err != nil {
			cancel()
			<-done
			return err
		}
		// A route's Serve returned nil early (e.g. a non-blocking
		// transport) — keep waiting for ctx/other routes, mirroring
		// rest.Server.Serve's "blocks until ctx cancelled" contract.
		for {
			select {
			case err := <-errCh:
				if err != nil {
					cancel()
					<-done
					return err
				}
			case <-ctx.Done():
				<-done
				return nil
			}
		}
	case <-ctx.Done():
		<-done
		return nil
	}
}

// Topical is implemented by *[RouteHandle][Req,Resp] (for any Req/Resp
// pair) — a plain, non-generic interface exposing the route's Topic
// without reflection, mirroring [FutureFactory]'s identical type-erasure-
// via-compile-time-method-dispatch technique. Backs [Server.
// RegisteredTopics], which some adapters (e.g. [zeromq.Attach]) use to
// validate full topic/socket coverage at Attach time, before [Server.
// Serve] ever runs.
type Topical interface {
	// TopicOf returns the route's registered topic/address.
	TopicOf() string
}

// RegisteredTopics returns the topic of every route registered via
// [Route.Register] so far (regardless of whether [Route.WithHandler] was
// used) — used by adapters like [zeromq.Attach] to validate topic/socket
// coverage at Attach time (before [Server.Serve] runs), since routes are
// always registered BEFORE Attach is called.
func (s *Server) RegisteredTopics() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	topics := make([]string, 0, len(s.topics))
	for t := range s.topics {
		topics = append(topics, t)
	}
	return topics
}

// registerDispatch records route's handle/fn pair for [Server.Serve] to
// dispatch later — called by [Route.Register] when the route has a
// handler attached via [Route.WithHandler].
func (s *Server) registerDispatch(topic string, handle, fn any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.dispatchEntries = append(s.dispatchEntries, dispatchEntry{topic: topic, handle: handle, fn: fn})
}

// ServerTransportAlreadyAttachedError is returned by [Server.Attach] when
// s already has a [ServerTransport] attached — Attach is exclusive, see
// its doc comment for the rationale.
type ServerTransportAlreadyAttachedError struct{}

func (e ServerTransportAlreadyAttachedError) Error() string {
	return "api/reqreply: Server already has a ServerTransport attached (Attach is exclusive; build a fresh Server for a different transport)"
}

// LogValue implements [slog.LogValuer] for structured logging.
func (e ServerTransportAlreadyAttachedError) LogValue() slog.Value {
	return slog.GroupValue()
}

// NoServerTransportAttachedError is returned by [Server.Serve] when
// [Server.Attach] was never called.
type NoServerTransportAttachedError struct{}

func (e NoServerTransportAttachedError) Error() string {
	return "api/reqreply: Server has no ServerTransport attached (call an adapter's Attach function first, e.g. mqtt5.Attach(server, client, router))"
}

// LogValue implements [slog.LogValuer] for structured logging.
func (e NoServerTransportAttachedError) LogValue() slog.Value {
	return slog.GroupValue()
}

// topicToID converts a topic string like "compute/add" or "sensors/{id}/readings"
// to a camelCase identifier like "computeAdd" or "sensorsIdReadings".
func topicToID(topic string) string {
	topic = strings.NewReplacer("{", "", "}", "", "/", "_").Replace(topic)
	if topic == "" {
		return "route"
	}
	parts := strings.Split(topic, "_")
	var b strings.Builder
	for i, p := range parts {
		if p == "" {
			continue
		}
		if i == 0 {
			b.WriteString(strings.ToLower(p))
		} else {
			b.WriteString(capitalise(p))
		}
	}
	return b.String()
}

// capitalise returns s with its first rune uppercased.
func capitalise(s string) string {
	if s == "" {
		return ""
	}
	runes := []rune(s)
	runes[0] = unicode.ToUpper(runes[0])
	return string(runes)
}
