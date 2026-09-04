package events

import (
	"context"
	"fmt"
	"log/slog"
	"slices"
	"sort"
	"strings"
	"sync"

	"github.com/DaniDeer/go-codex/api/internal"
	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/format"
	"github.com/DaniDeer/go-codex/middleware"
	asyncapi "github.com/DaniDeer/go-codex/render/asyncapi/v3"
	"github.com/DaniDeer/go-codex/route"
	"github.com/DaniDeer/go-codex/schema"
)

// Info is an alias for [asyncapi.Info]. Using the alias avoids duplicating
// fields and keeps the two in sync automatically.
type Info = asyncapi.Info

// Server is an alias for [asyncapi.Server].
type Server = asyncapi.Server

// SecurityScheme combines [route.SecurityScheme] spec metadata with optional
// runtime credential validation for message broker adapters.
//
// [WithSecurityScheme] declares a SecurityScheme on a channel — the ONLY way
// to declare one; there is no builder-level equivalent (mirrors
// [rest.WithSecurityScheme] exactly). The spec fields flow into the AsyncAPI
// document (aggregated from all registered channels by [Client.AsyncAPISpec]);
// Codec, when non-nil, is used by MQTT5 adapters to validate the raw
// credential string extracted from a message's User Properties before
// SecurityFunc is called (server-side, [adapters/mqtt5.Subscribe]) or before
// the message is published (client-side, [adapters/mqtt5.Publish]).
//
// MQTT 3.1.1 ([adapters/mqtt]) and ZeroMQ ([adapters/zeromq]) pub/sub have no
// per-message metadata channel — Codec-level extraction only applies to MQTT5
// ([adapters/mqtt5]); use SecurityFunc + closure (credentials passed at
// connect time) for runtime enforcement on those transports instead.
//
// Use [SecurityScheme.WithCodec] to set the Codec field inline without a temporary
// variable: events.SecurityScheme{SecurityScheme: route.APIKeyScheme(...)}.WithCodec(c)
type SecurityScheme struct {
	route.SecurityScheme
	// Codec, when non-nil, validates the extracted raw credential string.
	// Nil means no format validation; SecurityFunc receives the message as-is.
	Codec *codex.Codec[string]
}

// WithCodec returns a copy of s with Codec set to c. It avoids the
// temporary-variable + address-of pattern required when setting Codec inline:
//
//	events.WithSecurityScheme("apiKey", events.SecurityScheme{
//	    SecurityScheme: route.APIKeyScheme("X-API-Key", "header"),
//	}.WithCodec(codex.String().Refine(validate.NonEmptyString)))
func (s SecurityScheme) WithCodec(c codex.Codec[string]) SecurityScheme {
	s.Codec = &c
	return s
}

// securitySchemeOpt is the [ChannelOpt] returned by [WithSecurityScheme].
type securitySchemeOpt struct {
	name   string
	scheme SecurityScheme
}

func (o securitySchemeOpt) applyChannel(cb *channelBuilder) {
	if cb.securitySchemes == nil {
		cb.securitySchemes = make(map[string]SecurityScheme, 1)
	}
	cb.securitySchemes[o.name] = o.scheme
}

// WithSecurityScheme declares scheme's spec metadata and optional Codec for
// THIS channel — a legacy, pre-Decision-1 declaration mechanism, kept for
// backward compatibility (see the Deprecated note below). Both
// [Subscriber.Handle] and [Publisher.Handle] (and [Subscriber.Register])
// populate [ChannelHandle.SecuritySchemes] from this declaration, so the SAME
// channel value — including its security scheme — builds a subscribe-side
// handle and a publish-side handle with IDENTICAL credential-format
// enforcement on both sides. Mirrors [rest.WithSecurityScheme] exactly.
//
// Define a scheme once as a package-level value and reuse it across every
// channel that shares it:
//
//	var bearerAuth = events.SecurityScheme{SecurityScheme: route.BearerScheme("JWT")}.
//	    WithCodec(codex.String().Refine(validate.BearerToken))
//
//	var UserCreated = events.NewChannel[UserCreated]("user/created", userCreatedCodec,
//	    events.Subscribe{Security: []route.SecurityRequirement{route.Require("bearerAuth")}},
//	    events.WithSecurityScheme("bearerAuth", bearerAuth),
//	)
//
// When multiple channels declare the SAME scheme name with DIFFERENT values,
// [Client.AsyncAPISpec] resolves the conflict last-registered-wins (no
// error) — define the scheme once as a shared value (as above) to avoid this
// entirely.
//
// Deprecated: WithSecurityScheme duplicates what [FromSecurityScheme] +
// [Subscriber.Use]/[Publisher.Use] now express (mirrors REST's OWN Revision 2
// removal of rest.WithSecurityScheme in favor of rest.FromSecurityScheme).
// New code declaring security through [Channel.WithSubscribe]/
// [Channel.WithPublish] should use [FromSecurityScheme] instead:
//
//	sub := channel.WithSubscribe(events.Subscribe{...}).
//	    Use(events.FromSecurityScheme("bearerAuth", bearerAuth, []string{"subscribe:sensors"}))
//
// WithSecurityScheme is kept, unremoved, ONLY because this package's OWN
// regression test suite still exercises it as a legitimate backward-compat
// declaration path (an inline [ChannelOpt] passed to [NewChannel], now
// consumed via [Channel.WithSubscribe]/[Channel.WithPublish] +
// [Subscriber.Handle]/[Publisher.Handle] like any other channel — the older
// [Channel.Register]/[Channel.ClientHandle] methods it originally paired
// with were fully removed) — full removal of WithSecurityScheme itself
// remains a distinct, not-yet-started later phase of
// docs/design/d-0002-pubsub-workflow-simplification.md.
func WithSecurityScheme(name string, scheme SecurityScheme) ChannelOpt {
	return securitySchemeOpt{name: name, scheme: scheme}
}

// FromSecurityScheme bridges an existing [SecurityScheme] value (e.g. a
// package-level var shared across several channels) into a real
// [middleware.Middleware], usable with [Subscriber.Use]/[Publisher.Use] —
// the SOLE way to declare a channel's security scheme going forward. Mirrors
// [rest.FromSecurityScheme] exactly (found during a critical review:
// [middleware.SecurityDeclaration] already carries a strict superset of
// [SecurityScheme]'s fields, so there is nothing [WithSecurityScheme] could
// express that this bridge cannot).
//
//	var bearerAuth = events.SecurityScheme{SecurityScheme: route.BearerScheme("JWT")}.
//	    WithCodec(codex.String().Refine(validate.BearerToken))
//
//	sub := channel.WithSubscribe(events.Subscribe{...}).
//	    Use(events.FromSecurityScheme("bearerAuth", bearerAuth, []string{"subscribe:sensors"}))
func FromSecurityScheme(schemeName string, scheme SecurityScheme, scopes []string) middleware.Middleware {
	return middleware.SecurityScheme(schemeName, scheme.SecurityScheme, scopes, scheme.Codec)
}

// SecurityCredentialError is returned when credential format validation via
// SecurityScheme.Codec fails (MQTT5 only — MQTT 3.1.1 and ZeroMQ have no
// per-message credential extraction). It is distinct from [SecurityError],
// which wraps rejections from SecurityFunc.
//
// Use [errors.As] to extract the scheme name and underlying constraint error:
//
//	var credErr events.SecurityCredentialError
//	if errors.As(err, &credErr) {
//	    log.Printf("security scheme %q: invalid credential: %v", credErr.Scheme, credErr.Err)
//	}
type SecurityCredentialError struct {
	Scheme string // security scheme name
	Err    error  // codec constraint error
}

func (e SecurityCredentialError) Error() string {
	return fmt.Sprintf("security scheme %q: invalid credential: %s", e.Scheme, e.Err)
}

// Unwrap allows errors.As and errors.Is to traverse the underlying constraint error.
func (e SecurityCredentialError) Unwrap() error { return e.Err }

// LogValue implements [slog.LogValuer] for structured logging.
func (e SecurityCredentialError) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("scheme", e.Scheme),
		slog.Any("err", e.Err),
	)
}

// SecurityError is returned when SecurityFunc rejects a message.
// It is distinct from [SecurityCredentialError], which covers codec format failures.
//
// Use [errors.As] to extract the underlying error from SecurityFunc:
//
//	var secErr events.SecurityError
//	if errors.As(err, &secErr) {
//	    log.Printf("security check failed: %v", secErr.Err)
//	}
type SecurityError struct {
	Err error
}

func (e SecurityError) Error() string {
	return fmt.Sprintf("security check failed: %s", e.Err)
}

// Unwrap allows errors.As and errors.Is to traverse the underlying SecurityFunc error.
func (e SecurityError) Unwrap() error { return e.Err }

// LogValue implements [slog.LogValuer] for structured logging.
func (e SecurityError) LogValue() slog.Value {
	return slog.GroupValue(slog.Any("err", e.Err))
}

// Subscribe describes the subscribe operation on a channel (application receives).
// It controls the subscribe entry in the AsyncAPI spec.
//
// Subscribe implements [ChannelOpt]: pass it directly to [NewChannel].
type Subscribe struct {
	// OperationID is the unique identifier for the subscribe operation in the
	// AsyncAPI spec. Used by code generators and documentation tools.
	OperationID string
	Summary     string
	Description string
	Tags        []string

	// SchemaName, when non-empty, emits a $ref for the payload schema in the
	// spec and registers the schema under that name in components/schemas.
	SchemaName string

	// Security, when non-nil, overrides global security for this operation.
	// Pass an empty slice to declare "no auth required" for this subscription.
	// nil (default) inherits global security declared via [Client.AddGlobalSecurity].
	Security []route.SecurityRequirement
}

func (s Subscribe) applyChannel(cb *channelBuilder) { cb.subscribe = &s }

// Publish describes the publish operation on a channel (application sends).
// It controls the publish entry in the AsyncAPI spec.
//
// Publish implements [ChannelOpt]: pass it directly to [NewChannel].
type Publish struct {
	// OperationID is the unique identifier for the publish operation in the
	// AsyncAPI spec. Used by code generators and documentation tools.
	OperationID string
	Summary     string
	Description string
	Tags        []string

	// SchemaName, when non-empty, emits a $ref for the payload schema in the
	// spec and registers the schema under that name in components/schemas.
	SchemaName string

	// Security, when non-nil, overrides global security for this operation.
	// Pass an empty slice to declare "no auth required" for this publish operation.
	// nil (default) inherits global security declared via [Client.AddGlobalSecurity].
	Security []route.SecurityRequirement
}

func (p Publish) applyChannel(cb *channelBuilder) { cb.publish = &p }

// ChannelMeta holds channel-level metadata for a channel registration:
// title, summary, description, and tags. All fields are optional.
// The values flow into the generated AsyncAPI ChannelItem.
//
// ChannelMeta implements [ChannelOpt]: pass it directly to [NewChannel].
type ChannelMeta struct {
	Title       string
	Summary     string
	Description string
	Tags        []string
}

func (m ChannelMeta) applyChannel(cb *channelBuilder) { cb.meta = m }

// TopicParam describes a {varName} placeholder in a topic template for AsyncAPI
// spec generation and runtime validation.
//
// TopicParam is the single configuration point for a topic variable: it carries
// spec metadata (description) and an optional codec for runtime validation.
// The codec schema is also used to enrich the AsyncAPI parameters: block.
//
// TopicParam is optional: the events builder auto-derives parameters from the
// topic template. Use TopicParam to add a description or register a codec for
// a specific variable.
//
// Note: all topic variables are always required — a template cannot be resolved
// without every {varName} placeholder present. There is no Required field.
//
// TopicParam implements [ChannelOpt]: pass it directly to [NewChannel].
//
// Entry names must correspond to {varName} placeholders in the topic template;
// unknown names cause [Subscriber.Handle]/[Publisher.Handle] to return an
// error immediately. TopicParam mirrors [codex.Param]'s shape field-for-field — the shared,
// VALIDATE-ONLY escape hatch (see codex/param.go's own doc comment for the
// cross-package rationale). Kept as a flat, non-embedded struct (rather
// than embedding codex.Param) so existing `events.TopicParam{Name: "id"}`
// struct literals keep compiling unchanged (Go requires keyed literal
// fields to be the struct's OWN fields, not promoted ones).
type TopicParam struct {
	// Name is the variable name (without braces) as it appears in the topic template.
	Name string
	// Description is shown in the AsyncAPI spec for this parameter.
	Description string
	// Codec validates topic parameter values at [ChannelHandle.ValidateTopicVars] and
	// [ChannelHandle.BuildTopic] time.
	// When non-nil, the codec's schema is also emitted in the AsyncAPI spec.
	// Nil means no runtime validation; the spec defaults to {type: string}.
	Codec *codex.Codec[string]
}

func (p TopicParam) applyChannel(cb *channelBuilder) {
	cb.topicParams = append(cb.topicParams, p)
}

// WithCodec sets the validation codec and returns the updated TopicParam.
func (p TopicParam) WithCodec(c codex.Codec[string]) TopicParam { p.Codec = &c; return p }

// toParam converts p to the shared [codex.Param] shape.
func (p TopicParam) toParam() codex.Param {
	return codex.Param{Name: p.Name, Description: p.Description, Codec: p.Codec}
}

// MergedTopicParam is returned by [NewTopicParam]. It wraps
// [codex.MergedParam][T] — the shared merge-capable counterpart to
// [TopicParam]. It is the events-boundary mirror of [rest.MergedPathParam]:
// on subscribe, the registered field's setter merges the extracted topic
// variable into the decoded payload via [ChannelHandle.DecodeMerged]; on
// publish, the field's getter extracts the topic variable's value from the
// payload via [ChannelHandle.MergeFields] + [codex.EncodeVars] (or
// [PublishHandle] in adapters/mqtt5, which does this automatically).
type MergedTopicParam[T any] struct {
	codex.MergedParam[T]
}

// NewTopicParam declares a topic variable that is BOTH validated against
// codec AND automatically merged into T by [ChannelHandle.DecodeMerged] —
// one declaration instead of a TopicParam plus a separate codex.Field. All
// topic variables are always required (a template cannot be resolved
// without every {varName} placeholder present), matching plain
// [TopicParam]'s existing "no Required field" rationale.
//
//	events.NewChannel[SensorReading]("sensors/{sensorID}/readings", sensorReadingCodec,
//	    events.NewTopicParam("sensorID", codex.String().Refine(validate.UUID),
//	        func(r SensorReading) string { return r.SensorID },
//	        func(r *SensorReading, v string) { r.SensorID = v },
//	    ),
//	)
//
// V need not be string — see [codex.NewParam] for merging a topic segment
// directly into an int/UUID/etc.
func NewTopicParam[T, V any](
	name string,
	codec codex.Codec[V],
	get func(T) V,
	set func(*T, V),
) MergedTopicParam[T] {
	return MergedTopicParam[T]{MergedParam: codex.NewParam(name, codec, get, set)}
}

// WithDescription sets the PARAMETER-level description and returns the
// updated value.
func (p MergedTopicParam[T]) WithDescription(desc string) MergedTopicParam[T] {
	p.MergedParam = p.MergedParam.WithDescription(desc)
	return p
}

func (p MergedTopicParam[T]) applyChannel(cb *channelBuilder) {
	cb.topicParams = append(cb.topicParams, TopicParam{Name: p.Name, Description: p.Description, Codec: p.Codec})
	cb.mergeFields = append(cb.mergeFields, p.Field)
}

// toCodexParams converts topicParams to []codex.Param for [codex.BuildFromParams]/
// [codex.ValidateParams]/[codex.ValidateDeclaredParams].
func toCodexParams(topicParams []TopicParam) []codex.Param {
	out := make([]codex.Param, len(topicParams))
	for i, p := range topicParams {
		out[i] = p.toParam()
	}
	return out
}

// formatsOpt / subscribeFormatsOpt / publishFormatsOpt are unexported
// ChannelOpt implementations backing [Formats]/[SubscribeFormats]/
// [PublishFormats] — see those constructors.
type formatsOpt[T any] struct{ fmts []format.Format[T] }

func (o formatsOpt[T]) applyChannel(cb *channelBuilder) { cb.formats = o.fmts }

// Formats declares the default payload format for a channel — the
// [ChannelOpt] equivalent of calling [ChannelHandle.WithFormats] after
// [Subscriber.Handle]/[Publisher.Handle]. Declarable inline in [NewChannel]'s
// variadic opts, which means it also works through ports.EventPattern.Opts
// with zero changes to the ports package:
//
//	events.NewChannel[Image]("images/{id}", imageCodec,
//	    events.Formats(format.Binary(pngCodec).WithContentType("image/png")),
//	)
//
// A mismatched type (fmts holding format.Format[X] where the channel's
// payload type is not X) is only detectable once T is concrete —
// [Subscriber.Handle]/[Publisher.Handle] returns [FormatOptError] in that case.
func Formats[T any](fmts ...format.Format[T]) ChannelOpt {
	return formatsOpt[T]{fmts: fmts}
}

type subscribeFormatsOpt[T any] struct{ fmts []format.Format[T] }

func (o subscribeFormatsOpt[T]) applyChannel(cb *channelBuilder) { cb.subscribeFormats = o.fmts }

// SubscribeFormats declares the payload format for the subscribe (receive)
// direction only — the [ChannelOpt] equivalent of
// [ChannelHandle.WithSubscribeFormats]. Use for asymmetric channels (e.g.
// YAML in, JSON out) alongside [PublishFormats].
func SubscribeFormats[T any](fmts ...format.Format[T]) ChannelOpt {
	return subscribeFormatsOpt[T]{fmts: fmts}
}

type publishFormatsOpt[T any] struct{ fmts []format.Format[T] }

func (o publishFormatsOpt[T]) applyChannel(cb *channelBuilder) { cb.publishFormats = o.fmts }

// PublishFormats declares the payload format for the publish (send)
// direction only — the [ChannelOpt] equivalent of
// [ChannelHandle.WithPublishFormats]. See [SubscribeFormats].
func PublishFormats[T any](fmts ...format.Format[T]) ChannelOpt {
	return publishFormatsOpt[T]{fmts: fmts}
}

// FormatOptError is returned by [Subscriber.Handle]/[Publisher.Handle] when [Formats],
// [SubscribeFormats], or [PublishFormats] was declared with formats for a
// type that does not match the channel's actual payload type parameter.
type FormatOptError struct {
	// Direction is "both" ([Formats]), "subscribe", or "publish".
	Direction string
	Err       error
}

func (e FormatOptError) Error() string {
	return fmt.Sprintf("api/events: %s format option: %v", e.Direction, e.Err)
}

// Unwrap allows [errors.Is] and [errors.As] to reach the underlying error.
func (e FormatOptError) Unwrap() error { return e.Err }

// LogValue implements [slog.LogValuer] for structured logging.
func (e FormatOptError) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("direction", e.Direction),
		slog.Any("err", e.Err),
	)
}

// MergeFieldTypeError is returned by [Subscriber.Handle]/[Publisher.Handle] when a merge field
// registered via [NewTopicParam] has the wrong type parameter for the
// channel's payload type — mirrors [rest.MergeFieldTypeError] exactly.
type MergeFieldTypeError struct {
	Err error
}

func (e MergeFieldTypeError) Error() string {
	return fmt.Sprintf("api/events: merge field: %v", e.Err)
}

// Unwrap allows [errors.Is] and [errors.As] to reach the underlying error.
func (e MergeFieldTypeError) Unwrap() error { return e.Err }

// LogValue implements [slog.LogValuer] for structured logging.
func (e MergeFieldTypeError) LogValue() slog.Value {
	return slog.GroupValue(slog.Any("err", e.Err))
}

// assertMergeFields type-asserts each element of raw (declared as []any on
// channelBuilder to keep the builder non-generic) against
// codex.FieldCodec[T]. Returns MergeFieldTypeError on the first mismatch —
// a caller programming error (mixing a merge field built for one payload
// type into a Channel declared with another).
func assertMergeFields[T any](raw []any) ([]codex.FieldCodec[T], error) {
	if len(raw) == 0 {
		return nil, nil
	}
	out := make([]codex.FieldCodec[T], len(raw))
	for i, mf := range raw {
		fc, ok := mf.(codex.FieldCodec[T])
		if !ok {
			return nil, MergeFieldTypeError{
				Err: fmt.Errorf("want codex.FieldCodec[%T], got %T", *new(T), mf)}
		}
		out[i] = fc
	}
	return out, nil
}

// ChannelOpt is the sealed interface for variadic [NewChannel] options.
//
// The following types implement ChannelOpt:
//   - [ChannelMeta] — channel-level metadata (title, summary, description, tags)
//   - [Subscribe] — subscribe operation metadata (application receives messages)
//   - [Publish] — publish operation metadata (application sends messages)
//   - [TopicParam] — topic template variable with optional codec and description
//   - [ErrorChannel] — per-channel typed error pattern with declared error-output topic
type ChannelOpt interface{ applyChannel(*channelBuilder) }

// channelBuilder accumulates ChannelOpt values before building the channel descriptor.
type channelBuilder struct {
	meta        ChannelMeta
	subscribe   *Subscribe
	publish     *Publish
	topicParams []TopicParam
	// formats/subscribeFormats/publishFormats hold []format.Format[T]
	// type-erased (any) — set by [Formats]/[SubscribeFormats]/[PublishFormats],
	// resolved generically in [Subscriber.Handle]/[Publisher.Handle] where T
	// is concrete. See [FormatOptError].
	formats          any
	subscribeFormats any
	publishFormats   any
	// mergeFields holds type-erased codex.FieldCodec[T] values registered
	// via [NewTopicParam] — resolved to []codex.FieldCodec[T] in
	// [Subscriber.Handle]/[Publisher.Handle]. Unlike REST's four
	// path/query/header/cookie roles, events has exactly ONE var
	// destination (topic), so a single flat slice is always safe for both
	// the decode (subscribe) and encode (publish) directions — no
	// role-aware split is needed here (see [ChannelHandle.MergeFields]).
	mergeFields []any
	// errorChannelRules hold per-channel error-type -> error-output-topic
	// declarations from [ErrorChannel].
	errorChannelRules []errorChannelRule
	// securitySchemes holds this channel's own [WithSecurityScheme]
	// declarations — the ONLY source of [ChannelHandle.SecuritySchemes]
	// (there is no builder-level equivalent; mirrors
	// [rest's routeBuilder.securitySchemes]).
	securitySchemes map[string]SecurityScheme
}

// ChannelHandle is returned by [Subscriber.Handle]/[Publisher.Handle]. It holds the spec
// descriptor and codec-backed Decode/Encode helpers.
type ChannelHandle[T any] struct {
	// Topic is the channel name (e.g. "user/created", "orders.placed").
	Topic string

	// Descriptor is the live asyncapi.ChannelItem descriptor. It reflects the
	// current configuration and is used by the builder at AsyncAPISpec() time.
	Descriptor asyncapi.ChannelItem

	// Decode deserialises and validates a JSON payload into T.
	// All Refine constraints on the payload codec run automatically.
	Decode func(payload []byte) (T, error)

	// Encode serialises T to JSON bytes.
	Encode func(msg T) ([]byte, error)

	// Formats, when non-empty, specifies the default payload format for both
	// subscribe (decode) and publish (encode). The adapter uses Formats[0] when
	// no call-time format override is provided. Defaults to JSON when empty.
	// Configure via [ChannelHandle.WithFormats].
	// Use [ChannelHandle.WithSubscribeFormats] / [ChannelHandle.WithPublishFormats]
	// for asymmetric channels where decode and encode use different formats.
	Formats []format.Format[T]

	// SubscribeFormats, when non-empty, overrides Formats for the subscribe
	// (receive / decode) direction only. The adapter uses SubscribeFormats[0]
	// instead of Formats[0] when decoding incoming messages.
	// Configure via [ChannelHandle.WithSubscribeFormats].
	SubscribeFormats []format.Format[T]

	// PublishFormats, when non-empty, overrides Formats for the publish
	// (send / encode) direction only. The adapter uses PublishFormats[0]
	// instead of Formats[0] when encoding outgoing messages.
	// Configure via [ChannelHandle.WithPublishFormats].
	PublishFormats []format.Format[T]

	// topicParams holds per-variable params registered via TopicParam options.
	topicParams []TopicParam

	// topicCodec is the builder-level topic codec (may be nil).
	// Used to re-validate the final assembled topic in BuildTopic.
	topicCodec *codex.Codec[string]

	// SecuritySchemes maps scheme name to SecurityScheme (with runtime Codec).
	// Populated from the channel's own [WithSecurityScheme] declarations —
	// the ONLY source (there is no builder-level equivalent). Adapters use
	// this map to extract and validate credentials per scheme.
	SecuritySchemes map[string]SecurityScheme

	// GlobalSecurity holds the builder-level security requirements that apply
	// when the channel operation's Security field is nil (i.e. the channel
	// inherits global security). Adapters resolve the effective requirements as:
	//   reqs := handle.Descriptor.Subscribe.Security
	//   if reqs == nil { reqs = handle.GlobalSecurity }
	// Set via [Client.AddGlobalSecurity]. nil when no global security is declared.
	GlobalSecurity []route.SecurityRequirement

	// mergeFields holds the merge-capable fields registered via
	// [NewTopicParam] — see [MergeFields] and [DecodeMerged].
	mergeFields []codex.FieldCodec[T]

	// errorChannelRules holds per-channel error patterns declared via
	// [ErrorChannel] — see [ChannelHandle.ErrorResponseFor].
	errorChannelRules []errorChannelRule

	// Implementations holds server-side security-enforcing/general-purpose
	// implementations for the SUBSCRIBE side — populated ONLY by
	// [Subscriber.Handle] (never [Publisher.Handle]), reflecting whatever [Subscriber.SubscribeMW]
	// calls were made on the [Subscriber] before Handle() was called; see
	// [CheckCoverage]'s doc comment.
	Implementations []middleware.ServerImplementation

	// ClientImplementations holds client-side credential-supplying
	// implementations for the PUBLISH side — populated ONLY by
	// [Publisher.Handle] (never [Subscriber.Handle]), reflecting whatever
	// [Publisher.PublishMW] calls were made on the [Publisher] before
	// Handle() was called.
	ClientImplementations []middleware.ClientImplementation

	// Handler holds the declare-time handler attached via
	// [Subscriber.WithHandler], populated ONLY on the SUBSCRIBE side (nil
	// for a [Publisher]-built handle — [Publisher] has no equivalent
	// WithHandler, mirroring the existing publish-side asymmetry). This is
	// consumed EXCLUSIVELY by a future whole-client ServeSubscribers
	// (Phase 5+, per-adapter) — [Subscribe]/[SubscribeHandle]'s own
	// call-time fn parameter NEVER reads this field, even when both are
	// attached to the same declaration. See
	// docs/design/d-0002-pubsub-workflow-simplification.md's "Subscriber[T].WithHandler
	// REINSTATED" subsection.
	Handler func(context.Context, T) error

	// HandlerOpts holds the type-erased adapter Options value attached via
	// [Subscriber.WithOptions] (e.g. an MQTT QoS level) — mirrors
	// rest's RouteHandle.HandlerOpts/Route.WithOptions exactly. nil means
	// "adapter zero-value options apply". The adapter recovers the
	// concrete type via a type assertion at dispatch time (see rest's
	// resolveOptions pattern).
	HandlerOpts any
}

// MergeFields returns the merge-capable fields registered via
// [NewTopicParam] — feed them directly into [codex.DecodeVars]/
// [codex.EncodeVars], or use [ChannelHandle.DecodeMerged]/
// [ChannelHandle.EncodeVars] for the closed-loop convenience methods.
// Unlike REST's role-scoped PathMergeFields/QueryMergeFields/etc., there
// is only ONE var destination for events (the topic), so this single
// flat slice is safe for both directions — no cross-role leak risk
// exists here.
func (h *ChannelHandle[T]) MergeFields() []codex.FieldCodec[T] {
	return h.mergeFields
}

// EncodeVars derives the topic variables map from msg's merge-capable
// [NewTopicParam] fields, via [codex.EncodeVars] internally — the
// publish-side mirror of [ChannelHandle.DecodeMerged], and each
// adapter's existing PublishHandle convenience's OWN internal step,
// exposed here as its own method so it can be invoked via reflection
// with T unknown at the call site (needed by [Transport]'s
// implementations — [codex.EncodeVars] itself is a generic function and
// [codex.FieldCodec]'s methods are unexported, so neither is reachable
// from another package via reflection; this method IS, since T is fixed
// once *ChannelHandle[T] is already concretely instantiated). Behaves
// identically to an empty map when the channel declares no merge-capable
// topic params (MergeFields() is empty).
func (h *ChannelHandle[T]) EncodeVars(msg T) (map[string]string, error) {
	return codex.EncodeVars(msg, h.mergeFields...)
}

// DecodeMerged decodes payload (via the channel's registered format) AND
// merges every [NewTopicParam]-registered topic variable into the SAME T
// value, using [codex.DecodeVars] internally — the events-boundary mirror
// of [rest.RouteHandle.DecodeMerged]. Additive — [ChannelHandle.Decode] is
// unchanged; DecodeMerged behaves identically to a bare Decode when the
// channel declares no merge-capable topic params (MergeFields() is empty).
//
// The payload decode error (if any) is returned FIRST, before the
// topic-var merge step runs — matching [rest.RouteHandle.DecodeMerged]'s
// precedent. The merge step itself collects every field's failure via
// [codex.DecodeVars] (never stops at the first one).
func (h *ChannelHandle[T]) DecodeMerged(payload []byte, topicVars map[string]string) (T, error) {
	var msg T
	var err error
	if len(payload) > 0 {
		msg, err = h.Decode(payload)
		if err != nil {
			return msg, err
		}
	}
	if len(h.mergeFields) == 0 {
		return msg, nil
	}
	if err := codex.DecodeVars(&msg, topicVars, h.mergeFields...); err != nil {
		return msg, err
	}
	return msg, nil
}

// EffectivePublishFormats resolves the CANDIDATE format list for
// publish/encode, in priority order: formats (a call-time override) >
// h.PublishFormats > h.Formats — the single source of truth every
// adapter's Publish (escape-hatch AND [Transport]/Client.Attach)
// delegates to, instead of each duplicating this resolution inline.
// Returns the FULL candidate slice (not just the winning format) for
// adapters that need to scan every candidate themselves (e.g. mqtt5's
// ContentType-property auto-selection); [EncodeWithFormats] is the
// simpler "resolve AND encode in one call" convenience built on top of
// this for adapters that only need the single winning format.
func (h *ChannelHandle[T]) EffectivePublishFormats(formats ...format.Format[T]) []format.Format[T] {
	if len(formats) > 0 {
		return formats
	}
	if len(h.PublishFormats) > 0 {
		return h.PublishFormats
	}
	return h.Formats
}

// EncodeWithFormats is the canonical "encode using whatever format THIS
// channel declares" method — resolves via [EffectivePublishFormats] then
// encodes with the winning format, falling back to plain
// [ChannelHandle.Encode] when unresolved. Reflection-callable (T fixed
// by the receiver) — see [ChannelHandle.EncodeVars]'s doc comment for
// why this matters for [Transport] implementations.
func (h *ChannelHandle[T]) EncodeWithFormats(msg T, formats ...format.Format[T]) ([]byte, error) {
	effectiveFmts := h.EffectivePublishFormats(formats...)
	if len(effectiveFmts) > 0 {
		return effectiveFmts[0].Marshal(msg)
	}
	return h.Encode(msg)
}

// EffectiveSubscribeFormats resolves the CANDIDATE format list for
// subscribe/decode, in priority order: formats (a call-time override) >
// h.SubscribeFormats > h.Formats — the subscribe-side mirror of
// [EffectivePublishFormats]; see its doc comment for the shared
// rationale (mqtt5's ContentType-property auto-selection is the adapter
// that needs the full candidate slice, not just the winning format).
func (h *ChannelHandle[T]) EffectiveSubscribeFormats(formats ...format.Format[T]) []format.Format[T] {
	if len(formats) > 0 {
		return formats
	}
	if len(h.SubscribeFormats) > 0 {
		return h.SubscribeFormats
	}
	return h.Formats
}

// DecodeWithFormats is the canonical "decode using whatever format THIS
// channel declares" method, WITHOUT the topic-var merge step — the
// decode-only half of [DecodeMergedWithFormats], split out for adapters
// that need to run their OWN topic-var extraction/merge step separately
// (e.g. to report a topic-var failure under a DIFFERENT stats.Observer
// location than a payload-decode failure — see mqtt5/mqtt(v3)/zeromq's
// own subscribe primitives). Resolves via [EffectiveSubscribeFormats]
// then decodes with the winning format, falling back to plain
// [ChannelHandle.Decode] when unresolved.
func (h *ChannelHandle[T]) DecodeWithFormats(payload []byte, formats ...format.Format[T]) (T, error) {
	effectiveFmts := h.EffectiveSubscribeFormats(formats...)
	if len(effectiveFmts) > 0 {
		return effectiveFmts[0].Unmarshal(payload)
	}
	return h.Decode(payload)
}

// DecodeMergedWithFormats mirrors [ChannelHandle.DecodeMerged], but
// decodes via [DecodeWithFormats] first (so the channel's OWN declared
// format is honored) before merging topic vars — the canonical
// "decode+merge using whatever format THIS channel declares" method
// every adapter's Subscribe (escape-hatch AND [Transport]/Client.Attach)
// delegates to when it doesn't need decode/merge failures reported
// separately (e.g. Client.Attach's reflection shim, which has no
// per-failure-kind callback at all). See [EncodeWithFormats]'s doc
// comment for the shared rationale.
func (h *ChannelHandle[T]) DecodeMergedWithFormats(payload []byte, topicVars map[string]string, formats ...format.Format[T]) (T, error) {
	var msg T
	var err error
	if len(payload) > 0 {
		msg, err = h.DecodeWithFormats(payload, formats...)
		if err != nil {
			return msg, err
		}
	}
	if len(h.mergeFields) == 0 {
		return msg, nil
	}
	if err := codex.DecodeVars(&msg, topicVars, h.mergeFields...); err != nil {
		return msg, err
	}
	return msg, nil
}

// BuildTopic substitutes {varName} placeholders in the channel's topic template
// with the values provided in vars, validating each against its registered
// codec (if any).
//
// All template variables must be present in vars; missing variables return an
// error. Values are validated before substitution; codec failures return a
// [TopicParamError] that identifies the variable name and the failing value.
// Keys in vars that do not appear in the template are silently ignored.
//
// If the builder was created with [WithTopicCodec] or [WithTopicConstraints],
// the final assembled topic is also validated against that codec. A failure
// returns an [InvalidTopicError] with the concrete topic (not the template).
//
// Example:
//
//	topic, err := sensorChannel.BuildTopic(map[string]string{"sensorID": "f47ac10b-..."})
//	// topic = "sensors/f47ac10b-.../measurements"
func (h *ChannelHandle[T]) BuildTopic(vars map[string]string) (string, error) {
	result, err := codex.BuildFromParams(h.Topic, toCodexParams(h.topicParams), vars)
	if err != nil {
		return "", err
	}
	if h.topicCodec != nil {
		if err := h.topicCodec.Validate(result); err != nil {
			return "", InvalidTopicError{Topic: result, Err: err}
		}
	}
	return result, nil
}

// ValidateTopic validates a received concrete topic string against the builder-level
// topic codec (set via [WithTopicCodec] or [WithTopicConstraints]).
//
// Call this after a wildcard subscription delivers a message to verify the concrete
// topic satisfies the same constraints applied at channel registration time.
// Returns [InvalidTopicError] on failure; returns nil if no topic codec is registered.
//
// Note: unlike [Subscriber.Handle]/[Publisher.Handle], which validate a template-stripped topic, this method
// validates the concrete topic as-is (with real segment values in place).
func (h *ChannelHandle[T]) ValidateTopic(topic string) error {
	if h.topicCodec == nil {
		return nil
	}
	if err := h.topicCodec.Validate(topic); err != nil {
		return InvalidTopicError{Topic: topic, Err: err}
	}
	return nil
}

// ValidateTopicVars validates extracted topic variable values against the registered
// [TopicParam] codecs. Call this after [TopicVarsFromMessage] has extracted the vars
// map to ensure each variable satisfies its codec constraints.
//
// Returns [TopicParamError] for the first variable that fails its codec.
// Variables without a registered codec are skipped.
func (h *ChannelHandle[T]) ValidateTopicVars(vars map[string]string) error {
	return codex.ValidateParams(toCodexParams(h.topicParams), vars)
}

// WithFormats sets the default payload format for this channel. The adapter
// uses Formats[0] for both subscribe (decode) and publish (encode) when no
// call-time format override is provided. Defaults to JSON when empty.
//
// WithFormats also updates the live AsyncAPI descriptor: if fmts is non-empty,
// Message.ContentType on each registered operation (Subscribe/Publish) is set to
// fmts[0].ContentType(). Calling WithFormats with no arguments clears both
// Formats and the content-type override (restoring the AsyncAPI default).
//
// This mirrors [rest.RouteHandle.WithFormats] for event channels.
// Call it after [Subscriber.Handle]/[Publisher.Handle] to configure non-JSON payload serialisation:
//
//	ch = ch.WithFormats(format.YAML(measurementCodec))
//
//	// Adapter uses YAML automatically — no format arg needed:
//	transport := amqtt.NewSubscribeTransport[Reading](client, 1, opts)
//	SubscribeHandle(ctx, sub, transport, fn)
func (h *ChannelHandle[T]) WithFormats(fmts ...format.Format[T]) *ChannelHandle[T] {
	h.Formats = slices.Clone(fmts)
	ct := ""
	if len(fmts) > 0 {
		ct = fmts[0].ContentType()
	}
	if h.Descriptor.Subscribe != nil {
		h.Descriptor.Subscribe.Message.ContentType = ct
	}
	if h.Descriptor.Publish != nil {
		h.Descriptor.Publish.Message.ContentType = ct
	}
	return h
}

// WithSubscribeFormats sets the default payload format for the subscribe
// (receive / decode) direction only, leaving the publish direction unchanged.
// The adapter uses SubscribeFormats[0] when decoding incoming messages.
// Calling with no arguments clears the subscribe-specific override (Formats is used).
//
// Use this for asymmetric channels where inbound and outbound payloads use
// different serialisation formats (e.g. YAML in, JSON out).
func (h *ChannelHandle[T]) WithSubscribeFormats(fmts ...format.Format[T]) *ChannelHandle[T] {
	h.SubscribeFormats = slices.Clone(fmts)
	ct := ""
	if len(fmts) > 0 {
		ct = fmts[0].ContentType()
	}
	if h.Descriptor.Subscribe != nil {
		h.Descriptor.Subscribe.Message.ContentType = ct
	}
	return h
}

// WithPublishFormats sets the default payload format for the publish
// (send / encode) direction only, leaving the subscribe direction unchanged.
// The adapter uses PublishFormats[0] when encoding outgoing messages.
// Calling with no arguments clears the publish-specific override (Formats is used).
//
// Use this for asymmetric channels where inbound and outbound payloads use
// different serialisation formats (e.g. YAML in, JSON out).
func (h *ChannelHandle[T]) WithPublishFormats(fmts ...format.Format[T]) *ChannelHandle[T] {
	h.PublishFormats = slices.Clone(fmts)
	ct := ""
	if len(fmts) > 0 {
		ct = fmts[0].ContentType()
	}
	if h.Descriptor.Publish != nil {
		h.Descriptor.Publish.Message.ContentType = ct
	}
	return h
}

// TopicParamError is returned by [ChannelHandle.BuildTopic] and
// [ChannelHandle.ValidateTopicVars] when a topic variable fails its registered
// codec check.
//
// Use errors.As to extract it and inspect the failing variable:
//
//	var paramErr events.TopicParamError
//	if errors.As(err, &paramErr) {
//	    log.Printf("bad value %q for {%s}: %v", paramErr.Value, paramErr.Name, paramErr.Err)
//	}
//
// A type ALIAS for [codex.ParamError] — the SAME underlying type, so
// existing errors.As(&events.TopicParamError{}) calls keep working
// unchanged; see codex/param.go for the canonical definition.
type TopicParamError = codex.ParamError

// MissingTopicVarError is returned by [ChannelHandle.BuildTopic] when a {varName}
// placeholder in the topic template has no corresponding entry in the vars map.
// A type ALIAS for [codex.MissingParamError] — see [TopicParamError]'s own
// doc comment for the rationale.
//
// Use errors.As to extract the missing variable name:
//
//	var missingErr events.MissingTopicVarError
//	if errors.As(err, &missingErr) {
//	    log.Printf("caller forgot to supply topic variable {%s}", missingErr.Name)
//	}
type MissingTopicVarError = codex.MissingParamError

// InvalidTopicParamError is returned by [Subscriber.Handle]/[Publisher.Handle] when a [TopicParam] entry
// names a variable that does not appear in the topic template. A type ALIAS
// for [codex.InvalidParamError] — see [TopicParamError]'s own doc comment
// for the rationale. NOTE: the field is named Template (not Topic) on the
// shared type.
//
// Use errors.As to extract the offending name and the topic template:
//
//	var paramErr events.InvalidTopicParamError
//	if errors.As(err, &paramErr) {
//	    log.Printf("TopicParam %q not in topic %q", paramErr.Name, paramErr.Template)
//	}
type InvalidTopicParamError = codex.InvalidParamError

// channelEntry is the type-erased interface stored inside Client.
type channelEntry interface {
	topic() string
	descriptor() asyncapi.ChannelItem
	// securitySchemes returns the channel's own [WithSecurityScheme]
	// declarations (from the live handle) so [Client.AsyncAPISpec] can
	// aggregate every channel's schemes into the document — mirrors
	// [rest.routeEntry.securitySchemes] exactly.
	securitySchemes() map[string]SecurityScheme
}

// typedChannelEntry stores a pointer to the ChannelHandle so that the builder
// always sees the live descriptor at AsyncAPISpec() time.
type typedChannelEntry[T any] struct {
	topicStr string
	handle   *ChannelHandle[T]
}

func (e *typedChannelEntry[T]) topic() string                    { return e.topicStr }
func (e *typedChannelEntry[T]) descriptor() asyncapi.ChannelItem { return e.handle.Descriptor }
func (e *typedChannelEntry[T]) securitySchemes() map[string]SecurityScheme {
	return e.handle.SecuritySchemes
}

// InvalidTopicError is returned by [Subscriber.Handle]/[Publisher.Handle] when the topic fails builder-level
// topic codec validation.
//
// Use errors.As to extract it and inspect the failing topic or the underlying
// constraint error:
//
//	var topicErr events.InvalidTopicError
//	if errors.As(err, &topicErr) {
//	    log.Printf("bad topic %q: %v", topicErr.Topic, topicErr.Err)
//	}
type InvalidTopicError struct {
	Topic string // the topic that failed validation
	Err   error  // the underlying constraint or codec error
}

func (e InvalidTopicError) Error() string {
	return fmt.Sprintf("invalid topic %q: %s", e.Topic, e.Err.Error())
}

// Unwrap allows errors.As and errors.Is to traverse the underlying constraint error.
func (e InvalidTopicError) Unwrap() error { return e.Err }

// namedServer pairs a server name with its Server value for deterministic iteration.
type namedServer struct {
	name   string
	server Server
}

// Client accumulates channel registrations and produces AsyncAPI specs. It
// is the spec-owning value a pub/sub application's own role(s) — subscriber,
// publisher, or both — register their channels against (see
// [Channel.WithSubscribe]/[Channel.WithPublish]/[Subscriber.Handle]/
// [Publisher.Handle]). Create one with [NewClient].
//
// Client was named Builder prior to this package's client-centric role
// redesign; the deprecated Builder/NewBuilder aliases have since been
// removed (docs/design/d-0002-pubsub-workflow-simplification.md's exhaustive
// migration pass) — every caller now uses Client/NewClient directly.
type Client struct {
	// mu guards every field below that can be mutated after construction
	// (entries, specByTopic, subscriberByTopic) — mirrors rest.Server's
	// own mu sync.RWMutex exactly. Two DECOUPLED slots share this single
	// mutex: slot 1 (entries/specByTopic, spec-copy values feeding
	// [Client.AsyncAPISpec]) and slot 2 (subscriberByTopic, REPLACEABLE
	// *SubscriberEntry references feeding a future whole-client
	// ServeSubscribers) — see [Subscriber.Register]'s doc comment for why
	// these two slots are written by completely different call paths and
	// must never cross-contaminate.
	mu             sync.RWMutex
	info           Info
	servers        []namedServer
	entries        []channelEntry
	schemas        map[string]schema.Schema
	topicCodec     *codex.Codec[string]
	globalSecurity []route.SecurityRequirement
	// specByTopic dedups [Subscriber.Handle]/[Publisher.Handle] spec
	// registrations by topic (first-registered-wins on descriptor
	// content) — see [ChannelTypeConflictError]. Slot 1 of the registry
	// described on mu's doc comment.
	specByTopic map[string]specDedupEntry
	// subscriberByTopic holds slot 2 of the registry described on mu's doc
	// comment: the latest handler-bearing [SubscriberEntry] per topic,
	// populated EXCLUSIVELY by [Subscriber.Register] (never by
	// [Subscriber.Handle]/[Publisher.Handle]) — own last-registered-wins
	// policy, scoped only to repeated Register calls for the same
	// topic+client. See [Client.SubscriberEntries].
	subscriberByTopic map[string]SubscriberEntry
	// transport is the optional, adapter-provided [Transport] attached via
	// [Client.Attach] (e.g. by zeromq.Attach/mqtt5.Attach/mqtt.Attach) —
	// nil until Attach is called. See [Client.Publish]/[Client.Subscribe]/
	// [Client.ServeSubscribers]'s doc comments and Decision 5 of
	// docs/design/d-0002-pubsub-workflow-simplification.md for the full design
	// and rationale (why this is `any`-typed/reflection-based rather than
	// a compile-time type-safe generic method — Go forbids methods from
	// introducing their own type parameters).
	transport Transport
}

// ClientOption configures a [Client] at construction time.
type ClientOption func(*Client)

// WithTopicCodec sets a codec used to validate every topic passed to
// [Subscriber.Handle]/[Publisher.Handle]. If the topic is invalid, an error is returned immediately.
//
// Use [WithTopicConstraints] for the common case of stacking one or more
// [codex.Constraint] values; use WithTopicCodec when you need a fully-custom
// [codex.Codec].
//
// Example — enforce MQTT publish topic rules:
//
//	import "github.com/DaniDeer/go-codex/validate"
//
//	c := events.NewClient(events.WithTopicConstraints(validate.MQTTPublishTopic))
func WithTopicCodec(c codex.Codec[string]) ClientOption {
	return func(cl *Client) { cl.topicCodec = &c }
}

// WithTopicConstraints is a convenience wrapper around [WithTopicCodec] that
// builds a codec from [codex.String] refined with the given constraints.
// Multiple constraints are applied in order; all must pass.
//
// Users can mix built-in constraints from the validate package with their own:
//
//	sensorLevel := codex.Constraint[string]{
//	    Name:    "sensor-prefix",
//	    Check:   func(v string) bool { return strings.HasPrefix(v, "sensors/") },
//	    Message: func(v string) string { return fmt.Sprintf("topic must start with sensors/, got %q", v) },
//	}
//	c := events.NewClient(events.WithTopicConstraints(validate.MQTTPublishTopic, sensorLevel))
func WithTopicConstraints(cons ...codex.Constraint[string]) ClientOption {
	c := codex.String().Refine(cons...)
	return WithTopicCodec(c)
}

// WithInfo sets a [Client]'s API metadata (title/version/description).
// Optional — a [Client] built without WithInfo has a zero-value Info{}
// (empty Title/Version/Description in [Client.AsyncAPISpec]'s output) —
// per Decision 7 of docs/design/d-0002-pubsub-workflow-simplification.md, spec
// metadata is no longer mandatory ceremony for callers who only want the
// [Client.Attach]/[Client.Publish]/[Client.Subscribe] call surface (or the
// no-Client-at-all [PublishHandle]/[SubscribeHandle] surface) and never
// call [Client.AsyncAPISpec] at all.
//
//	c := events.NewClient(events.WithInfo(events.Info{Title: "Sensor Network", Version: "1.0.0"}))
func WithInfo(info Info) ClientOption {
	return func(cl *Client) { cl.info = info }
}

// NewClient returns a new, empty Client. Pass [WithInfo] to set API
// metadata (title/version/description) for [Client.AsyncAPISpec] — Info
// is no longer a mandatory positional argument (BREAKING change from
// prior versions: NewClient(info Info, opts ...ClientOption) →
// NewClient(opts ...ClientOption), per Decision 7). A Client built
// without WithInfo has a zero-value Info{} — fully usable for
// [Client.Attach]/[Client.Publish]/[Client.Subscribe] (or never even
// attaching a transport, using [PublishHandle]/[SubscribeHandle]
// directly against a role-scoped [Publisher]/[Subscriber] value instead)
// without ever needing a spec.
func NewClient(opts ...ClientOption) *Client {
	c := &Client{
		schemas: make(map[string]schema.Schema),
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// AddServer registers a named server entry in the spec.
// Servers appear in the AsyncAPI output in registration order.
// If s.Description is empty, name is used as the description.
func (c *Client) AddServer(name string, s Server) *Client {
	if s.Description == "" {
		s.Description = name
	}
	c.servers = append(c.servers, namedServer{name: name, server: s})
	return c
}

// AddSchema registers a named schema in components/schemas.
// Use this to register reusable schemas that are referenced by SchemaName in
// channel configs but not inlined in any codec.
func (c *Client) AddSchema(name string, s schema.Schema) *Client {
	c.schemas[name] = s
	return c
}

// AddGlobalSecurity appends security requirements that apply to all channels
// by default. The requirements flow into runtime enforcement: channels with
// nil Subscribe.Security or Publish.Security inherit these requirements at the
// adapter layer via [ChannelHandle.GlobalSecurity].
//
// AsyncAPI 3.0 has no document-level global security field; these requirements
// do NOT appear in the AsyncAPI spec output. To annotate per-channel security in
// the spec, set [Subscribe.Security] or [Publish.Security] explicitly.
//
// To mark a specific channel as explicitly unsecured (exempt from global
// security), set Security to an empty slice: Security: []route.SecurityRequirement{}.
func (c *Client) AddGlobalSecurity(reqs ...route.SecurityRequirement) *Client {
	c.globalSecurity = append(c.globalSecurity, reqs...)
	return c
}

// Topic bundles a topic template with its declared [TopicParam] variables —
// the payload-independent "shape" of a channel's topic (the SAME state
// [ChannelHandle.BuildTopic]/[ChannelHandle.ValidateTopicVars] already use
// internally, extracted into its own value).
//
// The plain-string form remains the default and primary way to declare a
// channel — pass a topic template string directly to [NewChannel], exactly
// as always. Reach for Topic ONLY when you find yourself declaring the SAME
// template+params shape for two or more channels (of different payload
// types) and want that shape to have exactly one source of truth, or when
// you need to build/validate a topic string standalone, with no payload
// codec involved at all:
//
//	var deviceTelemetryTopic = events.NewTopic("devices/{deviceID}/telemetry/{kind}",
//	    events.TopicParam{Name: "deviceID", Codec: &deviceIDCodec},
//	)
//
//	var temperatureChannel = events.NewChannelFromTopic(deviceTelemetryTopic, temperatureCodec,
//	    events.NewTopicParam("deviceID", deviceIDCodec,
//	        func(r TemperatureReading) string { return r.DeviceID },
//	        func(r *TemperatureReading, v string) { r.DeviceID = v }),
//	)
//	var pressureChannel = events.NewChannelFromTopic(deviceTelemetryTopic, pressureCodec,
//	    events.NewTopicParam("deviceID", deviceIDCodec,
//	        func(r PressureReading) string { return r.DeviceID },
//	        func(r *PressureReading, v string) { r.DeviceID = v }),
//	)
//
// A channel declared via [NewChannelFromTopic] is byte-for-byte identical to
// one declared via [NewChannel] with the same template and [TopicParam]
// values passed inline — nothing downstream (adapters, Register, spec
// generation) can tell the difference. Topic captures ONLY the
// template+params shape; every other [ChannelOpt] ([ChannelMeta],
// [Subscribe], [Publish], [WithSecurityScheme], formats, error patterns, …)
// is passed to [NewChannelFromTopic] exactly as it would be to [NewChannel]
// — nothing about the richer declaration surface is restricted by using a
// Topic for the template+params portion.
type Topic struct {
	// Template is the topic template, e.g. "devices/{deviceID}/telemetry".
	Template string
	// Params holds the topic template's variable declarations.
	Params []TopicParam
}

// NewTopic declares a Topic from a template and its TopicParam variables.
func NewTopic(template string, params ...TopicParam) Topic {
	return Topic{Template: template, Params: params}
}

// BuildTopic substitutes {varName} placeholders in t.Template with the
// values in vars, validating each against its registered [TopicParam.Codec]
// (if any). Mirrors [ChannelHandle.BuildTopic] exactly (same underlying
// engine, same error types), MINUS any builder-level topic codec — that
// only applies once a Topic-based channel is registered via
// [NewChannelFromTopic] + [Subscriber.Handle]/[Publisher.Handle], where it is enforced exactly
// as it would be for a plain-string channel.
func (t Topic) BuildTopic(vars map[string]string) (string, error) {
	return codex.BuildFromParams(t.Template, toCodexParams(t.Params), vars)
}

// ValidateTopicVars validates extracted topic variable values against t's
// registered [TopicParam] codecs. Mirrors [ChannelHandle.ValidateTopicVars]
// exactly (same error types); variables without a registered codec are
// skipped.
func (t Topic) ValidateTopicVars(vars map[string]string) error {
	return codex.ValidateParams(toCodexParams(t.Params), vars)
}

// Channel is a declarative event channel spec: topic, codec, and options.
// It is a value type — define it once, store it, pass it around, and register
// it with one or more [Client] instances via [Subscriber.Handle]/[Publisher.Handle]
// (or [Subscriber.Register] for the whole-client ServeSubscribers registry).
//
// Create a Channel with [NewChannel], or with a pre-built [Topic] via
// [NewChannelFromTopic].
type Channel[T any] struct {
	topic string
	codec codex.Codec[T]
	opts  []ChannelOpt
}

// NewChannel creates a [Channel] spec from a topic, codec, and variadic opts.
// NewChannel is infallible — it only captures the spec. Validation (topic codec,
// TopicParam template consistency) runs at [Subscriber.Handle]/[Publisher.Handle] time.
//
// Pass any combination of [ChannelMeta], [Subscribe], [Publish], and [TopicParam]
// as opts. All opts are optional.
//
// NewChannel is a free function (not a method) because Go requires type
// parameters to appear on free functions, not on method receivers.
//
// Typical usage:
//
//	var userCreated = events.NewChannel[UserCreated]("user/created", userCreatedCodec,
//	    events.ChannelMeta{Description: "A user was created"},
//	    events.Subscribe{Summary: "Receive user created events", SchemaName: "UserCreatedEvent"},
//	)
//
//	// Later, register with a builder:
//	handle, err := userCreated.Register(b)
func NewChannel[T any](
	topic string,
	codec codex.Codec[T],
	opts ...ChannelOpt,
) Channel[T] {
	return Channel[T]{
		topic: topic,
		codec: codec,
		opts:  opts,
	}
}

// NewChannelFromTopic declares a Channel using a pre-built [Topic] instead
// of a raw topic-template string — see [Topic]'s doc comment for when to
// reach for this. Produces the IDENTICAL [Channel] [NewChannel] would
// produce from topic.Template plus topic.Params passed inline, since
// [TopicParam] already implements [ChannelOpt].
func NewChannelFromTopic[T any](
	topic Topic,
	codec codex.Codec[T],
	opts ...ChannelOpt,
) Channel[T] {
	allOpts := make([]ChannelOpt, 0, len(topic.Params)+len(opts))
	for _, p := range topic.Params {
		allOpts = append(allOpts, p)
	}
	allOpts = append(allOpts, opts...)
	return NewChannel(topic.Template, codec, allOpts...)
}

// eventsSecurityContribution is one source's declaration for a single
// security scheme name, tracked for conflict detection. Mirrors
// api/rest/middleware.go's securityContribution exactly.
type eventsSecurityContribution struct {
	source     string // "manual" or a middleware's Name
	schemeType route.SecuritySchemeType
	scopes     []string
}

// sameScopeSet reports whether a and b contain the SAME scopes, ignoring
// order. Mirrors api/rest/middleware.go's sameScopeSet exactly.
func sameScopeSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	as := slices.Clone(a)
	bs := slices.Clone(b)
	sort.Strings(as)
	sort.Strings(bs)
	return slices.Equal(as, bs)
}

// applyEventsSecurityDeclarations merges every mws' [middleware.Middleware.Security]
// contribution into *security (the role's own manual Subscribe.Security or
// Publish.Security field, mutated in place) and schemes (this role's
// [ChannelHandle.SecuritySchemes]), detecting a conflict whenever the SAME
// scheme name is declared with DIFFERENT scheme types or scopes by two
// different sources (the manual declaration or a specific middleware's
// Name). Ports REST's applySecurityDeclarations (api/rest/middleware.go),
// run SEPARATELY per role — once for [Subscriber.Handle] against the
// channel's own Subscribe.Security, once (independently) for
// [Publisher.Handle] against Publish.Security — mirroring the SAME per-role
// independence [Subscriber]/[Publisher] already established for [Use].
//
// Unlike REST (which needs a SEPARATE infallible variant for
// Route.ClientHandle), this ONE fallible function suffices for pub/sub:
// [Subscriber.Handle]/[Publisher.Handle] are ALREADY universally fallible,
// even with a nil client, so there is no infallibility constraint left to
// preserve (see docs/design/d-0002-pubsub-workflow-simplification.md's Decision 1).
//
// Returns the MERGED SecuritySchemes map — the manual declaration's scheme
// metadata is passed in as the starting schemes map (from
// [WithSecurityScheme], for callers still migrating), and each
// middleware-contributed scheme is added FIRST-registered-wins WITHIN this
// one role's own merge loop (later middlewares naming an ALREADY-scheme'd
// name only contribute their scopes to the requirement merge above, never
// overriding the stored scheme metadata) — this is the missing "populate
// SecuritySchemes" half of the port, not a separate mechanism.
func applyEventsSecurityDeclarations(topic string, security *[]route.SecurityRequirement, mws []middleware.Middleware, schemes map[string]SecurityScheme) (map[string]SecurityScheme, error) {
	contributions := map[string][]eventsSecurityContribution{}

	for _, req := range *security {
		for schemeName, scopes := range req {
			var schemeType route.SecuritySchemeType
			if s, ok := schemes[schemeName]; ok {
				schemeType = s.Type
			}
			contributions[schemeName] = append(contributions[schemeName], eventsSecurityContribution{
				source: "manual", schemeType: schemeType, scopes: scopes,
			})
		}
	}
	for _, mw := range mws {
		if mw.Security == nil {
			continue
		}
		contributions[mw.Security.SchemeName] = append(contributions[mw.Security.SchemeName], eventsSecurityContribution{
			source: mw.Name, schemeType: mw.Security.Scheme.Type, scopes: mw.Security.Scopes,
		})
	}

	for schemeName, list := range contributions {
		first := list[0]
		for _, c := range list[1:] {
			// A source with no declared scheme type (e.g. a manual
			// Security entry with no matching SecurityScheme declared) is
			// treated as "unspecified, compatible with anything" — only
			// compared on scopes. Source NAME equality is NOT used to
			// skip comparison — see api/rest/middleware.go's identical
			// reasoning.
			typesDiffer := first.schemeType != "" && c.schemeType != "" && first.schemeType != c.schemeType
			if typesDiffer || !sameScopeSet(c.scopes, first.scopes) {
				return nil, ConflictingSecurityDeclarationError{
					Topic: topic, Scheme: schemeName,
					FirstSource: first.source, SecondSource: c.source,
					FirstScopes: first.scopes, SecondScopes: c.scopes,
				}
			}
		}
	}

	merged := make(map[string]SecurityScheme, len(schemes))
	for k, v := range schemes {
		merged[k] = v
	}
	for _, mw := range mws {
		if mw.Security == nil {
			continue
		}
		if len(*security) == 0 {
			*security = []route.SecurityRequirement{{}}
		}
		(*security)[0][mw.Security.SchemeName] = mw.Security.Scopes
		if _, exists := merged[mw.Security.SchemeName]; !exists {
			merged[mw.Security.SchemeName] = SecurityScheme{
				SecurityScheme: mw.Security.Scheme,
				Codec:          mw.Security.Codec,
			}
		}
	}
	return merged, nil
}

// checkUnsupportedMiddlewareParams rejects any mws entry carrying a
// REST-only param contribution (RequestHeaderParams/RequestCookieParams/
// RequestQueryParams/ResponseHeaderParams/ResponseCookieParams) — fields
// [middleware.Middleware] carries for api/rest's header/cookie/query
// boundary, meaningless for pub/sub's topic-only boundary. This is an
// INTERIM fix (see docs/design/d-0002-pubsub-workflow-simplification.md's
// "middleware.Middleware's REST-only fields, rejected eagerly" subsection);
// the long-term fix is a common-base + per-pattern-derived middleware type
// hierarchy, tracked separately.
func checkUnsupportedMiddlewareParams(topic string, mws []middleware.Middleware) error {
	for _, mw := range mws {
		if len(mw.RequestHeaderParams) > 0 || len(mw.RequestCookieParams) > 0 || len(mw.RequestQueryParams) > 0 ||
			len(mw.ResponseHeaderParams) > 0 || len(mw.ResponseCookieParams) > 0 {
			return UnsupportedMiddlewareParamsError{Topic: topic, Middleware: mw.Name}
		}
	}
	return nil
}

// CheckCoverage verifies that every security scheme named anywhere in
// secReqs has at least one [middleware.ServerImplementation] in impls whose
// Satisfies names it — otherwise the channel would enforce nothing at
// runtime despite declaring a scheme in its spec. Returns
// [MissingSecurityMiddlewareError] on the first uncovered scheme found.
// Mirrors [rest.CheckCoverage]/reqreply's equivalent exactly.
//
// Called UNCONDITIONALLY by [Subscriber.Handle] (never [Publisher.Handle] —
// this only ever validates the subscribe/server side, the SAME asymmetry
// REST/reqreply already establish: coverage is a "will this incoming
// message actually be verified" question, which only ever applies to the
// receiving/subscribing side).
//
// impls reflects whatever [Subscriber.SubscribeMW] calls were made on the
// [Subscriber] before [Subscriber.Handle] built h — a [Subscriber]
// declaring a security scheme (via the manual Subscribe.Security field or
// [FromSecurityScheme]+[Subscriber.Use]) WITHOUT a matching SubscribeMW
// attachment fails [Subscriber.Handle] with
// [MissingSecurityMiddlewareError]; attaching a SubscribeMW whose
// Satisfies names the scheme resolves it.
func CheckCoverage(topic string, secReqs []route.SecurityRequirement, impls []middleware.ServerImplementation) error {
	for _, req := range secReqs {
		for schemeName := range req {
			satisfied := false
			for _, impl := range impls {
				if slices.Contains(impl.Satisfies, schemeName) {
					satisfied = true
					break
				}
			}
			if !satisfied {
				return MissingSecurityMiddlewareError{Topic: topic, Scheme: schemeName}
			}
		}
	}
	return nil
}

// MissingSecurityMiddlewareError is returned by [Subscriber.Handle] when a
// declared security scheme has no attached [middleware.ServerImplementation]
// satisfying it — see [CheckCoverage].
type MissingSecurityMiddlewareError struct {
	Topic  string
	Scheme string
}

func (e MissingSecurityMiddlewareError) Error() string {
	return fmt.Sprintf("api/events: topic %q declares security scheme %q with no attached implementation satisfying it", e.Topic, e.Scheme)
}

// LogValue implements [slog.LogValuer] for structured logging.
func (e MissingSecurityMiddlewareError) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("topic", e.Topic),
		slog.String("scheme", e.Scheme),
	)
}

// checkImplementationsDeclared is the REVERSE-direction sibling to
// [CheckCoverage]: instead of "every DECLARED scheme has a covering
// implementation," it verifies "every IMPLEMENTED (non-empty-Satisfies)
// scheme was actually declared" — catching a [Subscriber.SubscribeMW] call
// PAIRED against a security scheme name that was never `.Use()`'d on the
// SAME [Subscriber] (e.g. a copy-paste mistake reusing a different
// channel's [middleware.Middleware]). Called UNCONDITIONALLY by
// [Subscriber.Handle]/[Subscriber.Register] — mirrors [rest.Route.Register]'s
// identically-named helper exactly (a gap found during a post-implementation
// audit: this doc's own Decision 1 explicitly promised this check, but it
// was never actually added when api/events was implemented).
func checkImplementationsDeclared(topic string, mws []middleware.Middleware, impls []middleware.ServerImplementation) error {
	declared := make(map[string]bool, len(mws))
	for _, mw := range mws {
		if mw.Security != nil {
			declared[mw.Security.SchemeName] = true
		}
	}
	for _, impl := range impls {
		for _, scheme := range impl.Satisfies {
			if !declared[scheme] {
				return UnknownMiddlewareImplementationError{Topic: topic, Scheme: scheme}
			}
		}
	}
	return nil
}

// UnknownMiddlewareImplementationError is returned by [Subscriber.Handle]/
// [Subscriber.Register] when a [Subscriber.SubscribeMW] call is PAIRED
// (non-nil mw with non-nil Security) against a security scheme name that
// was never `.Use()`'d on the SAME [Subscriber] — the reverse-direction
// sibling to [MissingSecurityMiddlewareError]/[CheckCoverage]. Mirrors
// [rest.UnknownMiddlewareImplementationError] exactly.
type UnknownMiddlewareImplementationError struct {
	Topic  string
	Scheme string
}

func (e UnknownMiddlewareImplementationError) Error() string {
	return fmt.Sprintf("api/events: topic %q attaches an implementation satisfying security scheme %q, which was never declared via .Use() on this channel", e.Topic, e.Scheme)
}

// LogValue implements [slog.LogValuer] for structured logging.
func (e UnknownMiddlewareImplementationError) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("topic", e.Topic),
		slog.String("scheme", e.Scheme),
	)
}

// ConflictingSecurityDeclarationError is returned by [Subscriber.Handle]/
// [Publisher.Handle] when two DIFFERENT sources (the manual
// Subscribe.Security/Publish.Security declaration, or a specific
// middleware's Name) declare the SAME security scheme with a DIFFERENT
// scheme type or scopes for that role. Identical redundant declarations for
// the same scheme are allowed silently — only DIFFERING ones conflict.
// Mirrors [rest.ConflictingSecurityDeclarationError] exactly (Route renamed
// Topic, pub/sub's own identifier vocabulary).
//
// Use [errors.As] to extract the topic, scheme, and conflicting sources:
//
//	var conflictErr events.ConflictingSecurityDeclarationError
//	if errors.As(err, &conflictErr) {
//	    log.Printf("topic %q: scheme %q: %q wants %v, %q wants %v",
//	        conflictErr.Topic, conflictErr.Scheme,
//	        conflictErr.FirstSource, conflictErr.FirstScopes,
//	        conflictErr.SecondSource, conflictErr.SecondScopes)
//	}
type ConflictingSecurityDeclarationError struct {
	Topic                     string
	Scheme                    string
	FirstSource, SecondSource string
	FirstScopes, SecondScopes []string
}

func (e ConflictingSecurityDeclarationError) Error() string {
	return fmt.Sprintf("api/events: topic %q: conflicting security declaration for scheme %q: %q declares scopes %v, but %q declares scopes %v",
		e.Topic, e.Scheme, e.FirstSource, e.FirstScopes, e.SecondSource, e.SecondScopes)
}

// LogValue implements [slog.LogValuer] for structured logging.
func (e ConflictingSecurityDeclarationError) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("topic", e.Topic),
		slog.String("scheme", e.Scheme),
		slog.String("first_source", e.FirstSource),
		slog.Any("first_scopes", e.FirstScopes),
		slog.String("second_source", e.SecondSource),
		slog.Any("second_scopes", e.SecondScopes),
	)
}

// UnsupportedMiddlewareParamsError is returned by [Subscriber.Handle]/
// [Publisher.Handle] when a [.Use]-attached [middleware.Middleware] carries
// a non-empty RequestHeaderParams/RequestCookieParams/RequestQueryParams/
// ResponseHeaderParams/ResponseCookieParams field — REST-only param
// contributions that are meaningless for pub/sub's topic-only boundary
// (e.g. a [middleware.Middleware] accidentally built via
// rest.FromHeaderParam and attached directly to a channel). See
// docs/design/d-0002-pubsub-workflow-simplification.md's "middleware.Middleware's
// REST-only fields, rejected eagerly" subsection.
//
// Use [errors.As] to extract the topic and middleware name:
//
//	var paramsErr events.UnsupportedMiddlewareParamsError
//	if errors.As(err, &paramsErr) {
//	    log.Printf("topic %q: middleware %q carries unsupported REST-only params",
//	        paramsErr.Topic, paramsErr.Middleware)
//	}
type UnsupportedMiddlewareParamsError struct {
	Topic      string
	Middleware string
}

func (e UnsupportedMiddlewareParamsError) Error() string {
	return fmt.Sprintf("api/events: topic %q: middleware %q carries REST-only param contributions (RequestHeaderParams/RequestCookieParams/RequestQueryParams/ResponseHeaderParams/ResponseCookieParams), unsupported for pub/sub's topic-only boundary", e.Topic, e.Middleware)
}

// LogValue implements [slog.LogValuer] for structured logging.
func (e UnsupportedMiddlewareParamsError) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("topic", e.Topic),
		slog.String("middleware", e.Middleware),
	)
}

// Subscriber is a role-scoped builder for a channel's subscribe side,
// returned by [Channel.WithSubscribe]. It carries the underlying [Channel]
// declaration (topic, codec, and every other [ChannelOpt], INCLUDING the
// [Subscribe] value passed to WithSubscribe) plus its OWN, independent
// [middleware.Middleware] declarations attached via [Subscriber.Use] — a
// [Publisher] for the SAME channel declares its middleware completely
// separately, never shared with a Subscriber's.
//
// Build the runtime handle with [Subscriber.Handle].
type Subscriber[T any] struct {
	channel Channel[T]
	// mws holds every [middleware.Middleware] attached via [Subscriber.Use].
	// [Subscriber.Handle] merges these declarations' Security with the
	// channel's own Subscribe.Security (conflict-checked via
	// applyEventsSecurityDeclarations, mirrors REST's
	// applySecurityDeclarations) and populates ChannelHandle.SecuritySchemes
	// from them; it also rejects unsupported middleware params
	// (checkUnsupportedMiddlewareParams) and enforces security coverage
	// (CheckCoverage) — see those functions' doc comments.
	mws []middleware.Middleware
	// handler holds the declare-time handler attached via
	// [Subscriber.WithHandler] — copied onto [ChannelHandle.Handler] by
	// [Subscriber.Handle]. Distinct from, and never read by,
	// [Subscribe]/[SubscribeHandle]'s own call-time fn parameter.
	handler func(context.Context, T) error
	// opts holds the type-erased adapter options attached via
	// [Subscriber.WithOptions] — copied onto [ChannelHandle.HandlerOpts].
	opts any
	// impls holds every [middleware.ServerImplementation] attached via
	// [Subscriber.SubscribeMW], in attachment order — copied onto
	// [ChannelHandle.Implementations] by [Subscriber.Handle]. Mirrors
	// [api/rest]'s routeBuilder.impls field, populated by
	// [rest.Route.HandleMW].
	impls []middleware.ServerImplementation
}

// Publisher is a role-scoped builder for a channel's publish side, returned
// by [Channel.WithPublish]. It is the publish-side mirror of [Subscriber] —
// see its doc comment for the shared rationale.
//
// Build the runtime handle with [Publisher.Handle].
type Publisher[T any] struct {
	channel Channel[T]
	// mws holds every [middleware.Middleware] attached via [Publisher.Use].
	// [Publisher.Handle] runs the SAME merge/conflict-detection
	// responsibility as [Subscriber.mws], independently against the
	// channel's own Publish.Security — see [Subscriber.mws]'s doc comment.
	// [Publisher.Handle] never runs [CheckCoverage] (subscribe-side-only
	// asymmetry, mirrors REST/reqreply).
	mws []middleware.Middleware
	// clientImpls holds every [middleware.ClientImplementation] attached
	// via [Publisher.PublishMW], in attachment order — copied onto
	// [ChannelHandle.ClientImplementations] by [Publisher.Handle]. Mirrors
	// [api/rest]'s routeBuilder.clientImpls field, populated by
	// [rest.Route.ClientMW].
	clientImpls []middleware.ClientImplementation
}

// WithSubscribe returns a [Subscriber] for this channel's subscribe side,
// carrying s as the channel's [Subscribe] operation metadata.
//
// Subscribe/Publish no longer need to be passed as [NewChannel] opts to
// participate in subscribe/publish declaration — WithSubscribe/WithPublish
// are the preferred entry points going forward — but [Subscribe]/[Publish]
// still implement [ChannelOpt] for existing callers during this codebase's
// phased migration (see docs/design/d-0002-pubsub-workflow-simplification.md).
func (c Channel[T]) WithSubscribe(s Subscribe) Subscriber[T] {
	ch := c
	ch.opts = append(append([]ChannelOpt{}, c.opts...), s)
	return Subscriber[T]{channel: ch}
}

// WithPublish returns a [Publisher] for this channel's publish side,
// carrying p as the channel's [Publish] operation metadata. See
// [Channel.WithSubscribe]'s doc comment for the shared rationale.
func (c Channel[T]) WithPublish(p Publish) Publisher[T] {
	ch := c
	ch.opts = append(append([]ChannelOpt{}, c.opts...), p)
	return Publisher[T]{channel: ch}
}

// Use returns a copy of s with mws appended to its own, independent
// middleware declarations — completely separate from any [Publisher] built
// from the SAME underlying channel. There is no channel-level Use(): a
// requirement shared by both roles is declared once per role.
func (s Subscriber[T]) Use(mws ...middleware.Middleware) Subscriber[T] {
	s.mws = append(append([]middleware.Middleware{}, s.mws...), mws...)
	return s
}

// WithHandler returns a copy of s carrying fn as its declare-time handler.
// This is DIFFERENT from, and fully independent of, [Subscribe]/
// [SubscribeHandle]'s own call-time fn parameter (which stays
// unchanged, required, and read-only-by-that-call — see
// docs/design/d-0002-pubsub-workflow-simplification.md's "Why Subscribe(fn)
// itself never became declare-time-only" subsection). fn is consumed
// EXCLUSIVELY by a future whole-client ServeSubscribers (Phase 5+,
// per-adapter), reached via [Subscriber.Register] populating
// [Client.SubscriberEntries] — never by [Subscriber.Handle] alone (which
// copies fn onto the returned [ChannelHandle.Handler] field but never
// retains anything on client).
func (s Subscriber[T]) WithHandler(fn func(context.Context, T) error) Subscriber[T] {
	s.handler = fn
	return s
}

// WithOptions returns a copy of s carrying opts as its declare-time,
// type-erased per-channel adapter options (e.g. an MQTT QoS level) —
// mirrors rest's Route.WithOptions/RouteHandle.HandlerOpts pattern
// exactly. Copied onto the built [ChannelHandle.HandlerOpts] field by
// [Subscriber.Handle]; the adapter recovers the concrete type via a type
// assertion at dispatch time.
func (s Subscriber[T]) WithOptions(opts any) Subscriber[T] {
	s.opts = opts
	return s
}

// buildServerImplementation builds a [middleware.ServerImplementation] from
// mw/fn — mw non-nil with Security set derives Satisfies from
// mw.Security.SchemeName (the PAIRED, security-verifying case, matched
// against a previously-.Use()'d declaration); mw nil (or Security nil)
// leaves Satisfies empty (UNPAIRED, general-purpose — runs
// unconditionally). Mirrors [api/rest]'s buildServerImplementation exactly.
func buildServerImplementation(mw *middleware.Middleware, fn any) middleware.ServerImplementation {
	if mw != nil && mw.Security != nil {
		return middleware.ServerImplementation{
			Name:      "implement:" + mw.Security.SchemeName,
			Satisfies: []string{mw.Security.SchemeName},
			Fn:        fn,
		}
	}
	return middleware.ServerImplementation{Name: "implement:general", Fn: fn}
}

// SubscribeMW is the ONLY server-side implementation-attachment method for
// a [Subscriber] — mw is NILABLE:
//   - non-nil AND mw.Security != nil: PAIRED — fn is matched against a
//     PREVIOUSLY-.Use()'d security declaration, mw being the SAME
//     middleware.Middleware value (not a re-typed string) — matched by
//     [CheckCoverage] at [Subscriber.Handle] time.
//   - nil (or mw.Security == nil): UNPAIRED, general-purpose — fn runs
//     unconditionally, nothing to satisfy.
//
// fn is deliberately untyped (any) — resolved by the SPECIFIC adapter
// (adapters/mqtt5, adapters/mqtt, adapters/zeromq) at Register/Subscribe
// time, mirroring [middleware.ServerImplementation.Fn]'s existing
// type-erasure; recognizing "security-shaped" vs. "general-purpose
// wrapping-shaped" Fn values is each adapter's OWN responsibility, not
// this package's — see docs/design/d-0002-pubsub-workflow-simplification.md's
// Decision 3. A wrong-shaped fn fails with a typed error at that point,
// never silently.
//
// Every call appends to s's own implementations slice, in attachment
// order — multiple SubscribeMW calls accumulate, they never overwrite one
// another. [Subscriber.Handle] copies the accumulated slice onto
// [ChannelHandle.Implementations] verbatim. Mirrors [rest.Route.HandleMW]
// exactly.
func (s Subscriber[T]) SubscribeMW(mw *middleware.Middleware, fn any) Subscriber[T] {
	s.impls = append(slices.Clone(s.impls), buildServerImplementation(mw, fn))
	return s
}

// Use returns a copy of p with mws appended to its own, independent
// middleware declarations. See [Subscriber.Use]'s doc comment for the
// shared rationale.
func (p Publisher[T]) Use(mws ...middleware.Middleware) Publisher[T] {
	p.mws = append(append([]middleware.Middleware{}, p.mws...), mws...)
	return p
}

// PublishMW is the ONLY client-side implementation-attachment method for a
// [Publisher] — the CLIENT-side mirror of [Subscriber.SubscribeMW]. mw is
// NILABLE with the SAME derivation rule: non-nil with Security set PAIRS
// fn against a previously-.Use()'d declaration (Satisfies gates which
// implementations the adapter runs, vs. the channel's declared security
// requirements); nil (or Security nil) leaves Satisfies empty —
// general-purpose, always runs.
//
// fn is deliberately untyped (any) for the SAME reason as SubscribeMW's —
// resolved by the specific client adapter (e.g. an mqtt5 credential-
// supplying closure) at Publish time.
//
// Every call appends to p's own implementations slice, in attachment
// order — multiple PublishMW calls accumulate, they never overwrite one
// another. [Publisher.Handle] copies the accumulated slice onto
// [ChannelHandle.ClientImplementations] verbatim. Mirrors
// [rest.Route.ClientMW] exactly.
func (p Publisher[T]) PublishMW(mw *middleware.Middleware, fn any) Publisher[T] {
	idx := len(p.clientImpls)
	impl := middleware.ClientImplementation{Fn: fn}
	if mw != nil && mw.Security != nil {
		impl.Name = fmt.Sprintf("fulfill:%s#%d", mw.Security.SchemeName, idx)
		impl.Satisfies = []string{mw.Security.SchemeName}
	} else {
		impl.Name = fmt.Sprintf("fulfill:general#%d", idx)
	}
	p.clientImpls = append(slices.Clone(p.clientImpls), impl)
	return p
}

// Handle builds a fresh, independent [ChannelHandle] for s's subscribe-side
// declaration. client is optional — nil builds a spec-free handle (no
// [Client] registration); a non-nil client additionally dedups this
// channel's topic against client's spec registry (first-registered-wins on
// descriptor content for repeat calls with the SAME payload type; a
// DIFFERENT payload type previously registered for the SAME topic returns
// [ChannelTypeConflictError]).
//
// The FULL validation suite (topic-param-name-matches-template, merge-field
// type checking, format-type checking) runs UNCONDITIONALLY, regardless of
// client's nilness — unlike the old, removed Channel.Register/ClientHandle
// split, client's nilness affects ONLY spec registration, never which
// validations run.
//
// Every call returns its OWN freshly-built handle — never a shared or
// mutated pointer, even on a dedup hit.
func (s Subscriber[T]) Handle(client *Client) (*ChannelHandle[T], error) {
	return buildChannelHandle(s.channel, client, roleSubscribe, s.mws, s.handler, s.opts, s.impls, nil)
}

// Handle builds a fresh, independent [ChannelHandle] for p's publish-side
// declaration. See [Subscriber.Handle]'s doc comment for the shared
// nil-client/dedup/unconditional-validation/fresh-handle contract.
func (p Publisher[T]) Handle(client *Client) (*ChannelHandle[T], error) {
	return buildChannelHandle(p.channel, client, rolePublish, p.mws, nil, nil, nil, p.clientImpls)
}

// specDedupEntry records the first-registered descriptor-level info for one
// topic in [Client.specByTopic] — used by [Subscriber.Handle]/
// [Publisher.Handle] to dedup repeat registrations against the SAME
// [Client] (topic + a type witness, via typeName, for the
// [ChannelTypeConflictError] check).
type specDedupEntry struct {
	typeName string
	entry    channelEntry
}

// channelRole distinguishes the SUBSCRIBE side from the PUBLISH side inside
// [buildChannelHandle] — which of cb.subscribe.Security/cb.publish.Security
// [applyEventsSecurityDeclarations] merges mws into, and whether
// [CheckCoverage] runs at all (subscribe-only, mirrors REST/reqreply).
type channelRole int

const (
	roleSubscribe channelRole = iota
	rolePublish
)

// buildChannelHandle is the shared construction path for [Subscriber.Handle]
// and [Publisher.Handle]: builds a fresh [ChannelHandle] from ch's
// declaration, running the full validation suite unconditionally, then —
// only when client is non-nil — dedups the topic's spec entry against
// client's registry.
func buildChannelHandle[T any](ch Channel[T], client *Client, role channelRole, mws []middleware.Middleware, handler func(context.Context, T) error, opts any, impls []middleware.ServerImplementation, clientImpls []middleware.ClientImplementation) (*ChannelHandle[T], error) {
	var cb channelBuilder
	for _, opt := range ch.opts {
		opt.applyChannel(&cb)
	}

	// Unconditional validation (Decision 1): runs regardless of client's
	// nilness. Reuses the existing, shared codex.InvalidParamError — no new
	// pub/sub-local error type is needed for this check.
	if err := codex.ValidateDeclaredParams(ch.topic, toCodexParams(cb.topicParams)); err != nil {
		return nil, err
	}

	// Unconditional validation (Decision 1, "middleware.Middleware's
	// REST-only fields" subsection): rejects a REST-oriented middleware
	// accidentally attached directly to a pub/sub channel.
	if err := checkUnsupportedMiddlewareParams(ch.topic, mws); err != nil {
		return nil, err
	}

	// Unconditional validation (Decision 1, "Merge/conflict-detection for
	// security REQUIREMENTS" subsection): merges mws' Security into the
	// role's OWN manual Subscribe.Security/Publish.Security field (mutated
	// in place on cb.subscribe/cb.publish, which buildChannelItem below
	// reads) and populates SecuritySchemes. [Subscriber.Handle] and
	// [Publisher.Handle] each hold a Subscribe/Publish set as the LAST opt
	// applied by [Channel.WithSubscribe]/[Channel.WithPublish], so
	// cb.subscribe/cb.publish is guaranteed non-nil for the matching role.
	var securityField *[]route.SecurityRequirement
	switch role {
	case roleSubscribe:
		securityField = &cb.subscribe.Security
	case rolePublish:
		securityField = &cb.publish.Security
	}
	mergedSchemes, err := applyEventsSecurityDeclarations(ch.topic, securityField, mws, cb.securitySchemes)
	if err != nil {
		return nil, err
	}
	cb.securitySchemes = mergedSchemes

	var topicCodec *codex.Codec[string]
	var globalSecurity []route.SecurityRequirement
	if client != nil {
		if client.topicCodec != nil {
			if err := client.topicCodec.Validate(internal.StripTemplateVars(ch.topic)); err != nil {
				return nil, InvalidTopicError{Topic: ch.topic, Err: err}
			}
		}
		topicCodec = client.topicCodec
		globalSecurity = slices.Clone(client.globalSecurity)
	}

	frozen := buildChannelItem(ch.topic, ch.codec, cb)
	jsonFmt := format.JSON(ch.codec)

	schemes := make(map[string]SecurityScheme, len(cb.securitySchemes))
	for k, v := range cb.securitySchemes {
		schemes[k] = v
	}

	h := &ChannelHandle[T]{
		Topic:                 ch.topic,
		Descriptor:            frozen,
		Decode:                func(payload []byte) (T, error) { return jsonFmt.Unmarshal(payload) },
		Encode:                func(msg T) ([]byte, error) { return jsonFmt.Marshal(msg) },
		topicParams:           cb.topicParams,
		topicCodec:            topicCodec,
		SecuritySchemes:       schemes,
		GlobalSecurity:        globalSecurity,
		errorChannelRules:     cb.errorChannelRules,
		Handler:               handler,
		HandlerOpts:           opts,
		Implementations:       impls,
		ClientImplementations: clientImpls,
	}
	if cb.formats != nil {
		fmts, ok := cb.formats.([]format.Format[T])
		if !ok {
			return nil, FormatOptError{Direction: "both",
				Err: fmt.Errorf("want []format.Format[%T], got %T", *new(T), cb.formats)}
		}
		h.WithFormats(fmts...)
	}
	if cb.subscribeFormats != nil {
		fmts, ok := cb.subscribeFormats.([]format.Format[T])
		if !ok {
			return nil, FormatOptError{Direction: "subscribe",
				Err: fmt.Errorf("want []format.Format[%T], got %T", *new(T), cb.subscribeFormats)}
		}
		h.WithSubscribeFormats(fmts...)
	}
	if cb.publishFormats != nil {
		fmts, ok := cb.publishFormats.([]format.Format[T])
		if !ok {
			return nil, FormatOptError{Direction: "publish",
				Err: fmt.Errorf("want []format.Format[%T], got %T", *new(T), cb.publishFormats)}
		}
		h.WithPublishFormats(fmts...)
	}
	var mergeErr error
	h.mergeFields, mergeErr = assertMergeFields[T](cb.mergeFields)
	if mergeErr != nil {
		return nil, mergeErr
	}

	// Unconditional coverage enforcement (Decision 1): subscribe-side only,
	// mirrors REST/reqreply's asymmetry — a Publisher never enforces
	// anything against an incoming message, so it never needs coverage.
	// h.Implementations reflects whatever [Subscriber.SubscribeMW] calls
	// were made on s before this Handle() call — see [CheckCoverage]'s
	// doc comment.
	if role == roleSubscribe {
		if err := checkImplementationsDeclared(ch.topic, mws, h.Implementations); err != nil {
			return nil, err
		}
		if err := CheckCoverage(ch.topic, *securityField, h.Implementations); err != nil {
			return nil, err
		}
	}

	if client == nil {
		return h, nil
	}

	typeName := fmt.Sprintf("%T", *new(T))

	client.mu.Lock()
	defer client.mu.Unlock()

	if existing, ok := client.specByTopic[ch.topic]; ok {
		if existing.typeName != typeName {
			return nil, ChannelTypeConflictError{Topic: ch.topic, Want: existing.typeName, Got: typeName}
		}
		// Hit, same T: first-registered-wins on descriptor content — do not
		// append a duplicate spec entry. The freshly-built handle above is
		// still returned to the caller unchanged.
		return h, nil
	}

	entry := &typedChannelEntry[T]{topicStr: ch.topic, handle: h}
	if client.specByTopic == nil {
		client.specByTopic = make(map[string]specDedupEntry)
	}
	client.specByTopic[ch.topic] = specDedupEntry{typeName: typeName, entry: entry}
	client.entries = append(client.entries, entry)
	return h, nil
}

// ChannelTypeConflictError is returned by [Subscriber.Handle]/
// [Publisher.Handle] when client already has a DIFFERENT payload type
// registered for the same Topic — a genuine caller error (reusing a topic
// with an incompatible payload type), not an escape hatch.
//
// Use [errors.As] to extract the topic and the conflicting type names:
//
//	var conflictErr events.ChannelTypeConflictError
//	if errors.As(err, &conflictErr) {
//	    log.Printf("topic %q already registered as %s, got %s",
//	        conflictErr.Topic, conflictErr.Want, conflictErr.Got)
//	}
type ChannelTypeConflictError struct {
	// Topic is the channel topic with conflicting payload type registrations.
	Topic string
	// Want is the payload type already registered for Topic (the
	// first-registered call's type).
	Want string
	// Got is the payload type of this call, which does not match Want.
	Got string
}

func (e ChannelTypeConflictError) Error() string {
	return fmt.Sprintf("api/events: topic %q already registered with payload type %s, got %s",
		e.Topic, e.Want, e.Got)
}

// LogValue implements [slog.LogValuer] for structured logging.
func (e ChannelTypeConflictError) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("topic", e.Topic),
		slog.String("want", e.Want),
		slog.String("got", e.Got),
	)
}

// MissingHandlerError is returned by [Subscriber.Register] when s has no
// handler attached (i.e. [Subscriber.WithHandler] was never called before
// Register). A handler-less Register call is a caller mistake, caught
// EAGERLY here — never silently accepted, never a silent no-op — since
// slot 2 of [Client]'s registry (feeding a future whole-client
// ServeSubscribers) exists specifically to hold invokable handlers; an
// entry with no handler would be useless to it. See
// docs/design/d-0002-pubsub-workflow-simplification.md's "A blocking gap, found
// and fixed this pass" subsection.
//
// Use [errors.As] to extract the topic:
//
//	var missingErr events.MissingHandlerError
//	if errors.As(err, &missingErr) {
//	    log.Printf("topic %q: Register called with no WithHandler attached", missingErr.Topic)
//	}
type MissingHandlerError struct {
	// Topic is the channel topic Register was called for.
	Topic string
}

func (e MissingHandlerError) Error() string {
	return fmt.Sprintf("api/events: topic %q: Register called with no handler attached (call Subscriber.WithHandler first)", e.Topic)
}

// LogValue implements [slog.LogValuer] for structured logging.
func (e MissingHandlerError) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("topic", e.Topic),
	)
}

// SubscriberEntry is a read-only, reflection-friendly view of one
// [Subscriber] registered against a [Client] via [Subscriber.Register] —
// returned by [Client.SubscriberEntries]. It exists so a future per-adapter
// ServeSubscribers can walk a HETEROGENEOUS collection of subscribers (each
// with a DIFFERENT payload type) without api/events itself needing reflect:
// Handle() returns the concrete *[ChannelHandle][T] type-erased to any; the
// consuming adapter recovers T via reflect.Value.Call against the handle's
// ALREADY-concrete exported closures (Decode, Handler) — mirrors
// [rest.RouteEntry] exactly, including its "Serve's generic dispatch
// mechanism" rationale (see
// docs/design/d-0002-pubsub-workflow-simplification.md's
// "Client.SubscriberEntries() + ServeSubscribers's generic dispatch
// mechanism" subsection). The isSubscriberEntry marker method seals this
// interface to api/events' own implementation.
type SubscriberEntry interface {
	// Topic returns the channel's topic.
	Topic() string
	// HasHandler reports whether [Subscriber.WithHandler] was ever called.
	// Always true for entries returned by [Client.SubscriberEntries] — a
	// handler-less [Subscriber.Register] call is rejected eagerly with
	// [MissingHandlerError] and never reaches the registry — but exposed
	// here as a defensive, belt-and-braces guard mirroring
	// [rest.RouteEntry.HasHandler]'s own precedent.
	HasHandler() bool
	// Handle returns the underlying *ChannelHandle[T], type-erased.
	Handle() any
	isSubscriberEntry()
}

// typedSubscriberEntry stores a pointer to the ChannelHandle so that any
// future mutation remains visible through the registry — mirrors
// api/rest's typedRouteEntry exactly.
type typedSubscriberEntry[T any] struct {
	topicStr string
	handle   *ChannelHandle[T]
}

func (e *typedSubscriberEntry[T]) Topic() string      { return e.topicStr }
func (e *typedSubscriberEntry[T]) HasHandler() bool   { return e.handle.Handler != nil }
func (e *typedSubscriberEntry[T]) Handle() any        { return e.handle }
func (e *typedSubscriberEntry[T]) isSubscriberEntry() {}

// Register attaches s to client's slot-2 registry — the ONLY way to feed a
// future whole-client ServeSubscribers (Phase 5+, per-adapter). Requires s
// to have a handler attached via [Subscriber.WithHandler]; a handler-less
// call returns [MissingHandlerError] immediately and touches nothing.
//
// Register ALSO contributes to client's slot-1 spec registry — the SAME
// dedup logic [Subscriber.Handle] uses (first-registered-wins on
// descriptor content, [ChannelTypeConflictError] on a payload-type
// mismatch) — so a caller wiring a channel for ServeSubscribers calls ONLY
// Register, never needing a separate [Subscriber.Handle] call too.
//
// Slot 2 has its OWN last-registered-wins policy, scoped ONLY to repeated
// Register calls for the SAME topic+client — it is NEVER affected by any
// [Subscriber.Handle] call, no matter how many times or with what
// [Subscriber] value Handle is called afterward for the same topic (e.g.
// the internal Handle call [Subscribe]/[SubscribeHandle] make on their
// own, handler-less [Subscriber] value). This separation fixes a real bug
// found during design review — see
// docs/design/d-0002-pubsub-workflow-simplification.md's "A blocking gap, found
// and fixed this pass" subsection for the full story.
//
// There is no way to un-register a previously-registered entry — a
// Register call with no handler attached is a hard error, never a silent
// no-op and never a "clear" operation.
func (s Subscriber[T]) Register(client *Client) error {
	if s.handler == nil {
		return MissingHandlerError{Topic: s.channel.topic}
	}

	h, err := buildChannelHandle(s.channel, client, roleSubscribe, s.mws, s.handler, s.opts, s.impls, nil)
	if err != nil {
		return err
	}

	client.mu.Lock()
	defer client.mu.Unlock()
	if client.subscriberByTopic == nil {
		client.subscriberByTopic = make(map[string]SubscriberEntry)
	}
	client.subscriberByTopic[s.channel.topic] = &typedSubscriberEntry[T]{topicStr: s.channel.topic, handle: h}
	return nil
}

// SubscriberEntries returns every [Subscriber] registered against c via
// [Subscriber.Register], as read-only [SubscriberEntry] views — for use by
// a future per-adapter whole-client ServeSubscribers (Phase 5+) to walk and
// start consuming every registered channel in one call. Entries populated
// ONLY via [Subscriber.Handle] (never [Subscriber.Register]) are NEVER
// included — see [Subscriber.Register]'s doc comment for why the two are
// fully decoupled. Every entry returned is guaranteed to have
// HasHandler() == true, by construction.
//
// ServeSubscribers is expected to take a one-time snapshot of this slice at
// call time — it does not watch the registry for later changes.
func (c *Client) SubscriberEntries() []SubscriberEntry {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make([]SubscriberEntry, 0, len(c.subscriberByTopic))
	topics := make([]string, 0, len(c.subscriberByTopic))
	for topic := range c.subscriberByTopic {
		topics = append(topics, topic)
	}
	sort.Strings(topics)
	for _, topic := range topics {
		out = append(out, c.subscriberByTopic[topic])
	}
	return out
}

// SubscriberServer is a shared, transport-agnostic interface satisfied by
// each adapter's internal caller type via its own ServeSubscribers method
// (Go structural typing — no explicit "implements" declaration needed;
// the caller type itself is unexported in every adapter, reachable only
// through [Client.Attach]/[Client.ServeSubscribers]). It lives in
// api/events — zero adapter imports, mirrors how io.Reader/io.Writer live
// in io: a neutral location every adapter already depends on, avoiding
// circular imports. Lets application code write ONE generic "start
// consuming" call across transports, without caring which adapter is
// underneath — reachable uniformly via [Client.Attach] (e.g.
// mqtt5.Attach, mqtt.Attach, zeromq.Attach) followed by
// [Client.ServeSubscribers].
type SubscriberServer interface {
	// ServeSubscribers walks every [Subscriber] registered against the
	// underlying [Client] via [Subscriber.Register] and starts consuming
	// each one, blocking until ctx is cancelled or all consumption
	// goroutines exit.
	ServeSubscribers(ctx context.Context) error
}

// PublisherClient completes SubscriberServer's transport-agnostic symmetry
// on the PUBLISH side — lives alongside SubscriberServer, same rationale
// (neutral, transport-agnostic location, zero adapter imports). Named
// PublisherClient, NOT Publisher — [Publisher] is already the role-scoped
// BUILDER type from [Channel.WithPublish]; PublisherClient emphasizes this
// is a bound RUNTIME client object, distinct from the declare-time
// builder. The single-workflow path satisfying this interface today is
// [Client.Publish] itself, via each adapter's Attach-returned [Transport]
// (e.g. [zeromq.Attach], mqtt5.Attach, mqtt.Attach) — no separate
// per-channel binding type is publicly exposed.
type PublisherClient[T any] interface {
	// Publish sends msg on the bound channel.
	Publish(ctx context.Context, msg T) error
}

// Transport is implemented by each adapter's internal, unexported binding
// attached to a [Client] via an adapter-specific Attach function (e.g.
// [zeromq.Attach], mqtt5.Attach, mqtt.Attach) — see [Client.Attach]. It
// gives [Client] itself a literal Publish/Subscribe/ServeSubscribers call
// shape (Decision 5 of docs/design/d-0002-pubsub-workflow-simplification.md).
//
// Methods are necessarily `any`-typed: Go forbids methods from
// introducing their own type parameters, so a compile-time type-safe
// Publish[T]/Subscribe[T] method on the single, non-generic [Client] type
// is impossible. Each adapter's Transport implementation recovers the
// concrete payload type internally via reflection against the
// ALREADY-CONCRETE closures on the type-erased [ChannelHandle] (the SAME
// technique [SubscriberServer]'s existing per-adapter implementations
// already use for decode/dispatch — never via reflecting a generic
// FUNCTION, which Go does not support). A pub/sub/fn argument whose
// dynamic type doesn't match what the OTHER argument expects surfaces as
// [TransportTypeMismatchError] at call time, not a compile error — an
// explicit, narrowly-scoped trade-off for this ONE convenience surface;
// every other declarative API in this package remains fully
// compile-time type-safe.
type Transport interface {
	// Publish sends msg (dynamic type T) on the channel pub (dynamic type
	// [Publisher][T]) describes.
	Publish(ctx context.Context, pub any, msg any) error
	// Subscribe starts consuming the channel sub (dynamic type
	// [Subscriber][T]) describes, calling fn (dynamic type
	// func(context.Context, T) error) for each message. Blocks until ctx
	// is cancelled, mirroring the adapter's own Subscribe[T]/
	// SubscribeTransport semantics.
	Subscribe(ctx context.Context, sub any, fn any) error
	// ServeSubscribers walks every [Subscriber] registered against the
	// owning [Client] via [Subscriber.Register] and starts consuming each
	// one — mirrors [SubscriberServer.ServeSubscribers] exactly (a
	// Transport-attached [Client] satisfies [SubscriberServer] too).
	ServeSubscribers(ctx context.Context) error
}

// Attach binds t to c as c's transport — the "attach the adapter to the
// client" step behind [Client.Publish]/[Client.Subscribe]/
// [Client.ServeSubscribers]. Each adapter provides its own entry point
// (e.g. zeromq.Attach(client, sock)) that builds an internal Transport
// implementation and calls this method internally; application code
// calls the ADAPTER's Attach function, not this method directly, in the
// common case (though nothing prevents a custom [Transport]
// implementation from calling this directly).
//
// Returns [TransportAlreadyAttachedError] if c already has a transport
// attached — Attach is exclusive, never silently replaces an existing
// transport (avoids silently swapping the wire underneath already-in-
// flight [Client.Publish]/[Client.Subscribe] calls). A caller wanting a
// different transport builds a fresh [Client] — cheap, no different from
// today.
//
// A [Client] with an attached [Transport] is no longer side-effect-free:
// unlike every OTHER Client method (pure spec/registry manipulation),
// [Client.Publish]/[Client.Subscribe]/[Client.ServeSubscribers] perform
// real I/O once attached.
func (c *Client) Attach(t Transport) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.transport != nil {
		return TransportAlreadyAttachedError{}
	}
	c.transport = t
	return nil
}

// Publish sends msg on the channel pub describes, via c's attached
// [Transport] — see [Transport]'s doc comment for the full design
// rationale (why this is `any`-typed/reflection-based) and
// [Client.Attach] for how a Transport gets attached. Returns
// [NoTransportAttachedError] if [Client.Attach] was never called.
//
//	client := events.NewClient(events.WithInfo(events.Info{...}))
//	_ = zeromq.Attach(client, sock)
//	pub := ReadingsChannel.WithPublish(events.Publish{...})
//	err := client.Publish(ctx, pub, reading)
func (c *Client) Publish(ctx context.Context, pub any, msg any) error {
	c.mu.RLock()
	t := c.transport
	c.mu.RUnlock()
	if t == nil {
		return NoTransportAttachedError{}
	}
	return t.Publish(ctx, pub, msg)
}

// Subscribe starts consuming the channel sub describes, via c's attached
// [Transport] — see [Transport]'s doc comment for the full design
// rationale and [Client.Attach] for how a Transport gets attached.
// Returns [NoTransportAttachedError] if [Client.Attach] was never called.
// Blocks until ctx is cancelled, mirroring the underlying adapter's own
// Subscribe[T]/SubscribeTransport semantics.
//
//	client := events.NewClient(events.WithInfo(events.Info{...}))
//	_ = zeromq.Attach(client, sock)
//	sub := ReadingsChannel.WithSubscribe(events.Subscribe{...})
//	err := client.Subscribe(ctx, sub, func(ctx context.Context, r SensorReading) error { ... })
func (c *Client) Subscribe(ctx context.Context, sub any, fn any) error {
	c.mu.RLock()
	t := c.transport
	c.mu.RUnlock()
	if t == nil {
		return NoTransportAttachedError{}
	}
	return t.Subscribe(ctx, sub, fn)
}

// ServeSubscribers walks every [Subscriber] registered against c via
// [Subscriber.Register] and starts consuming each one, via c's attached
// [Transport] — mirrors [SubscriberServer.ServeSubscribers] exactly
// (unchanged semantics from today's per-adapter internal caller's
// ServeSubscribers, just relocated onto Client itself, which already owns
// the registry this walks). Returns [NoTransportAttachedError] if
// [Client.Attach] was never called.
func (c *Client) ServeSubscribers(ctx context.Context) error {
	c.mu.RLock()
	t := c.transport
	c.mu.RUnlock()
	if t == nil {
		return NoTransportAttachedError{}
	}
	return t.ServeSubscribers(ctx)
}

// TransportAlreadyAttachedError is returned by [Client.Attach] when c
// already has a [Transport] attached — Attach is exclusive, see its doc
// comment for the rationale.
type TransportAlreadyAttachedError struct{}

func (e TransportAlreadyAttachedError) Error() string {
	return "api/events: Client already has a Transport attached (Attach is exclusive; build a fresh Client for a different transport)"
}

// LogValue implements [slog.LogValuer] for structured logging.
func (e TransportAlreadyAttachedError) LogValue() slog.Value {
	return slog.GroupValue()
}

// NoTransportAttachedError is returned by [Client.Publish]/
// [Client.Subscribe]/[Client.ServeSubscribers] when [Client.Attach] was
// never called.
type NoTransportAttachedError struct{}

func (e NoTransportAttachedError) Error() string {
	return "api/events: Client has no Transport attached (call an adapter's Attach function first, e.g. zeromq.Attach(client, sock))"
}

// LogValue implements [slog.LogValuer] for structured logging.
func (e NoTransportAttachedError) LogValue() slog.Value {
	return slog.GroupValue()
}

// TransportTypeMismatchError is returned by a [Transport] implementation
// (via [Client.Publish]/[Client.Subscribe]) when the dynamic types of its
// `any`-typed arguments don't match each other as expected — e.g. msg's
// concrete type doesn't match pub's declared payload type T. This is the
// explicit, narrowly-scoped runtime-type-safety cost of [Client.Publish]/
// [Client.Subscribe]'s literal method call shape — see [Transport]'s doc
// comment.
type TransportTypeMismatchError struct {
	// Topic is the channel topic involved.
	Topic string
	// Want describes the expected type (e.g. "events.Publisher[SensorReading]").
	Want string
	// Got describes the actual dynamic type provided.
	Got string
}

func (e TransportTypeMismatchError) Error() string {
	return fmt.Sprintf("api/events: topic %q: Transport type mismatch: want %s, got %s", e.Topic, e.Want, e.Got)
}

// LogValue implements [slog.LogValuer] for structured logging.
func (e TransportTypeMismatchError) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("topic", e.Topic),
		slog.String("want", e.Want),
		slog.String("got", e.Got),
	)
}

// rawChannelEntry stores a pre-built ChannelItem — used by channels whose
// two directions carry DIFFERENT payload types (e.g. a duplex WebSocket
// endpoint registered via ports.RegisterSocket), which the single-codec
// [Channel] cannot express.
type rawChannelEntry struct {
	topicStr string
	item     asyncapi.ChannelItem
}

func (e *rawChannelEntry) topic() string                    { return e.topicStr }
func (e *rawChannelEntry) descriptor() asyncapi.ChannelItem { return e.item }

// securitySchemes returns nil — a raw pre-built ChannelItem carries no
// [WithSecurityScheme] declarations of its own (it never goes through
// [Subscriber.Handle]/[Publisher.Handle]).
func (e *rawChannelEntry) securitySchemes() map[string]SecurityScheme { return nil }

// AddChannelItem registers a pre-built [asyncapi.ChannelItem] under topic.
// Use this for channels the single-codec [Channel] declaration cannot
// express — a duplex socket whose inbound and outbound frames are different
// types (ports.RegisterSocket builds the item from a SocketPattern).
//
// The builder-level topic codec is NOT applied — the topic may be an HTTP
// upgrade path (e.g. "/live/{room}") rather than an MQTT-style topic.
// SchemaName references in the item's operations participate in the usual
// dangling-$ref validation at [Client.AsyncAPISpec] time.
func (b *Client) AddChannelItem(topic string, item asyncapi.ChannelItem) *Client {
	b.mu.Lock()
	b.entries = append(b.entries, &rawChannelEntry{topicStr: topic, item: item})
	b.mu.Unlock()
	return b
}

// AsyncAPISpec builds a complete AsyncAPI 3.0 document from all registered channels.
// Returns an error if any non-empty SchemaName references a schema that will not
// be present in components/schemas (a dangling $ref).
func (b *Client) AsyncAPISpec() (asyncapi.Document, error) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	ab := asyncapi.NewDocumentBuilder(b.info)
	for _, ns := range b.servers {
		ab.AddServer(ns.name, ns.server)
	}
	for name, s := range b.schemas {
		ab.AddSchema(name, s)
	}
	// Aggregate SecuritySchemes from every registered channel's own
	// [WithSecurityScheme] declarations (there is no builder-level
	// registry) — collision policy is last-registered-wins, matching
	// [rest.Server.OpenAPISpec]'s documented behavior. Entry order
	// matches b.entries' registration order, so "last" means
	// "most-recently-registered channel", not map-iteration order.
	for _, e := range b.entries {
		for name, s := range e.securitySchemes() {
			ab.AddSecurityScheme(name, s.SecurityScheme)
		}
	}
	if err := b.buildInto(ab); err != nil {
		return asyncapi.Document{}, err
	}
	return ab.Build()
}

// AppendTo writes all channels registered on this Client into db, which
// must have been created by [asyncapi.NewDocumentBuilder]. Servers, schemas,
// and security schemes owned by this Client are NOT written — the caller is
// responsible for configuring those on db.
//
// Use AppendTo to combine pub/sub channels with request-reply channels from
// [api/reqreply.Builder] in a single AsyncAPI 3.0 document:
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
func (b *Client) AppendTo(db *asyncapi.DocumentBuilder) error {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.buildInto(db)
}

// buildInto writes all registered channels into db.
func (b *Client) buildInto(db *asyncapi.DocumentBuilder) error {
	if err := b.checkDanglingRefs(); err != nil {
		return err
	}
	for _, e := range b.entries {
		db.AddChannel(e.topic(), e.descriptor())
	}
	return nil
}

// checkDanglingRefs verifies that every non-empty SchemaName used in channels
// resolves to a schema that will be registered in components/schemas.
// A name is resolvable when the accompanying Schema is non-empty (non-zero),
// or when the name was explicitly registered via [Client.AddSchema].
func (b *Client) checkDanglingRefs() error {
	resolvable := make(map[string]bool, len(b.schemas))
	for name := range b.schemas {
		resolvable[name] = true
	}
	for _, e := range b.entries {
		ch := e.descriptor()
		collectResolvable(ch.Subscribe, resolvable)
		collectResolvable(ch.Publish, resolvable)
	}

	seen := make(map[string]bool)
	var unresolved []string
	for _, e := range b.entries {
		ch := e.descriptor()
		checkOp(ch.Subscribe, resolvable, seen, &unresolved)
		checkOp(ch.Publish, resolvable, seen, &unresolved)
	}
	if len(unresolved) > 0 {
		sort.Strings(unresolved)
		return fmt.Errorf("unregistered schema names (dangling $ref): %s", strings.Join(unresolved, ", "))
	}
	return nil
}

func collectResolvable(op *asyncapi.Operation, resolvable map[string]bool) {
	if op == nil || op.Message.SchemaName == "" {
		return
	}
	// Schema is a value type (schema.Schema); it's always present alongside SchemaName.
	resolvable[op.Message.SchemaName] = true
}

func checkOp(op *asyncapi.Operation, resolvable, seen map[string]bool, unresolved *[]string) {
	if op == nil || op.Message.SchemaName == "" {
		return
	}
	name := op.Message.SchemaName
	if !resolvable[name] && !seen[name] {
		seen[name] = true
		*unresolved = append(*unresolved, name)
	}
}

// buildChannelItem constructs an asyncapi.ChannelItem from the topic, codec
// schema, and channelBuilder options.
//
// Channel parameters are auto-derived from {varName} placeholders in topic.
// The schema for each parameter comes from the TopicParam.Codec (if set);
// TopicParam.Description adds a human-readable description.
func buildChannelItem[T any](topic string, codec codex.Codec[T], cb channelBuilder) asyncapi.ChannelItem {
	item := asyncapi.ChannelItem{
		Address:     topic,
		Title:       cb.meta.Title,
		Summary:     cb.meta.Summary,
		Description: cb.meta.Description,
		Tags:        slices.Clone(cb.meta.Tags),
		Parameters:  buildTopicParameters(topic, cb.topicParams),
	}

	if cb.subscribe != nil {
		op := cb.subscribe
		item.Subscribe = &asyncapi.Operation{
			OperationID: op.OperationID,
			Summary:     op.Summary,
			Description: op.Description,
			Tags:        slices.Clone(op.Tags),
			Message: asyncapi.Message{
				Schema:     codec.Schema,
				SchemaName: op.SchemaName,
			},
			Security: slices.Clone(op.Security),
		}
	}

	if cb.publish != nil {
		op := cb.publish
		item.Publish = &asyncapi.Operation{
			OperationID: op.OperationID,
			Summary:     op.Summary,
			Description: op.Description,
			Tags:        slices.Clone(op.Tags),
			Message: asyncapi.Message{
				Schema:     codec.Schema,
				SchemaName: op.SchemaName,
			},
			Security: slices.Clone(op.Security),
		}
	}

	return item
}

// buildTopicParameters derives the AsyncAPI channel parameters map from a
// topic template and optional TopicParam entries.
//
// Priority for each variable's schema:
//  1. TopicParam.Codec.Schema — when a codec is registered for the variable
//  2. Default: {type: string}
func buildTopicParameters(
	topic string,
	params []TopicParam,
) map[string]asyncapi.Parameter {
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
