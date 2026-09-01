package events

import (
	"fmt"
	"log/slog"
	"slices"
	"sort"
	"strings"

	"github.com/DaniDeer/go-codex/api/internal"
	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/format"
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
// document (aggregated from all registered channels by [Builder.AsyncAPISpec]);
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
// THIS channel. It is the ONLY way to declare a security scheme — there is no
// builder-level equivalent. Both [Channel.Register] and [Channel.ClientHandle]
// populate [ChannelHandle.SecuritySchemes] from this declaration, so the SAME
// channel value — including its security scheme — builds a server-side handle
// (Register) and a client-side handle (ClientHandle) with IDENTICAL
// credential-format enforcement on both sides. Mirrors [rest.WithSecurityScheme]
// exactly.
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
// [Builder.AsyncAPISpec] resolves the conflict last-registered-wins (no
// error) — define the scheme once as a shared value (as above) to avoid this
// entirely.
func WithSecurityScheme(name string, scheme SecurityScheme) ChannelOpt {
	return securitySchemeOpt{name: name, scheme: scheme}
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
	// nil (default) inherits global security declared via [Builder.AddGlobalSecurity].
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
	// nil (default) inherits global security declared via [Builder.AddGlobalSecurity].
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
// unknown names cause [Channel.Register] to return an error immediately.
// TopicParam mirrors [codex.Param]'s shape field-for-field — the shared,
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
// [Channel.Register]. Declarable inline in [NewChannel]'s variadic opts,
// which means it also works through ports.EventPattern.Opts with zero
// changes to the ports package:
//
//	events.NewChannel[Image]("images/{id}", imageCodec,
//	    events.Formats(format.Binary(pngCodec).WithContentType("image/png")),
//	)
//
// A mismatched type (fmts holding format.Format[X] where the channel's
// payload type is not X) is only detectable once T is concrete —
// [Channel.Register] returns [FormatOptError] in that case.
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

// FormatOptError is returned by [Channel.Register] when [Formats],
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

// MergeFieldTypeError is returned by [Channel.Register] when a merge field
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

// mustAssertMergeFields is assertMergeFields for infallible callers
// (ClientHandle has no error return) — panics on a type mismatch, same
// class as ports.NewFile's panic-on-misuse precedent.
func mustAssertMergeFields[T any](caller string, raw []any) []codex.FieldCodec[T] {
	fields, err := assertMergeFields[T](raw)
	if err != nil {
		panic(fmt.Sprintf("api/events: %s: %s", caller, err.Error()))
	}
	return fields
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
	// resolved generically in [Channel.Register] where T is concrete. See
	// [FormatOptError].
	formats          any
	subscribeFormats any
	publishFormats   any
	// mergeFields holds type-erased codex.FieldCodec[T] values registered
	// via [NewTopicParam] — resolved to []codex.FieldCodec[T] in
	// [Channel.Register]/[Channel.ClientHandle]. Unlike REST's four
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

// ChannelHandle is returned by [Channel.Register]. It holds the spec
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
	// Set via [Builder.AddGlobalSecurity]. nil when no global security is declared.
	GlobalSecurity []route.SecurityRequirement

	// mergeFields holds the merge-capable fields registered via
	// [NewTopicParam] — see [MergeFields] and [DecodeMerged].
	mergeFields []codex.FieldCodec[T]

	// errorChannelRules holds per-channel error patterns declared via
	// [ErrorChannel] — see [ChannelHandle.ErrorResponseFor].
	errorChannelRules []errorChannelRule
}

// MergeFields returns the merge-capable fields registered via
// [NewTopicParam] — feed them directly into [codex.DecodeVars]/
// [codex.EncodeVars], or use [ChannelHandle.DecodeMerged] for the
// closed-loop convenience method. Unlike REST's role-scoped
// PathMergeFields/QueryMergeFields/etc., there is only ONE var
// destination for events (the topic), so this single flat slice is safe
// for both directions — no cross-role leak risk exists here.
func (h *ChannelHandle[T]) MergeFields() []codex.FieldCodec[T] {
	return h.mergeFields
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
// Note: unlike [Channel.Register], which validates a template-stripped topic, this method
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
// Call it after [Channel.Register] to configure non-JSON payload serialisation:
//
//	ch = ch.WithFormats(format.YAML(measurementCodec))
//
//	// Adapter uses YAML automatically — no format arg needed:
//	client.Subscribe(topic, 1, amqtt.SubscribeHandler(ctx, ch, fn, opts))
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

// InvalidTopicParamError is returned by [Channel.Register] when a [TopicParam] entry
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

// channelEntry is the type-erased interface stored inside Builder.
type channelEntry interface {
	topic() string
	descriptor() asyncapi.ChannelItem
	// securitySchemes returns the channel's own [WithSecurityScheme]
	// declarations (from the live handle) so [Builder.AsyncAPISpec] can
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

// InvalidTopicError is returned by [Channel.Register] when the topic fails builder-level
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

// Builder accumulates channel registrations and produces AsyncAPI specs.
// Create one with [NewBuilder].
type Builder struct {
	info           Info
	servers        []namedServer
	entries        []channelEntry
	schemas        map[string]schema.Schema
	topicCodec     *codex.Codec[string]
	globalSecurity []route.SecurityRequirement
}

// BuilderOption configures a [Builder] at construction time.
type BuilderOption func(*Builder)

// WithTopicCodec sets a codec used to validate every topic passed to [Channel.Register].
// If the topic is invalid, [Channel.Register] returns an error immediately.
//
// Use [WithTopicConstraints] for the common case of stacking one or more
// [codex.Constraint] values; use WithTopicCodec when you need a fully-custom
// [codex.Codec].
//
// Example — enforce MQTT publish topic rules:
//
//	import "github.com/DaniDeer/go-codex/validate"
//
//	b := events.NewBuilder(info, events.WithTopicConstraints(validate.MQTTPublishTopic))
func WithTopicCodec(c codex.Codec[string]) BuilderOption {
	return func(b *Builder) { b.topicCodec = &c }
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
//	b := events.NewBuilder(info, events.WithTopicConstraints(validate.MQTTPublishTopic, sensorLevel))
func WithTopicConstraints(cons ...codex.Constraint[string]) BuilderOption {
	c := codex.String().Refine(cons...)
	return WithTopicCodec(c)
}

// NewBuilder returns a Builder initialised with the given API metadata.
func NewBuilder(info Info, opts ...BuilderOption) *Builder {
	b := &Builder{
		info:    info,
		schemas: make(map[string]schema.Schema),
	}
	for _, opt := range opts {
		opt(b)
	}
	return b
}

// AddServer registers a named server entry in the spec.
// Servers appear in the AsyncAPI output in registration order.
// If s.Description is empty, name is used as the description.
func (b *Builder) AddServer(name string, s Server) *Builder {
	if s.Description == "" {
		s.Description = name
	}
	b.servers = append(b.servers, namedServer{name: name, server: s})
	return b
}

// AddSchema registers a named schema in components/schemas.
// Use this to register reusable schemas that are referenced by SchemaName in
// channel configs but not inlined in any codec.
func (b *Builder) AddSchema(name string, s schema.Schema) *Builder {
	b.schemas[name] = s
	return b
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
func (b *Builder) AddGlobalSecurity(reqs ...route.SecurityRequirement) *Builder {
	b.globalSecurity = append(b.globalSecurity, reqs...)
	return b
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
// [NewChannelFromTopic] + [Channel.Register], where it is enforced exactly
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
// it with one or more [Builder] instances via [Channel.Register].
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
// TopicParam template consistency) runs at [Channel.Register] time.
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

// Register registers the channel with b and returns a [ChannelHandle].
//
// If the builder was created with [WithTopicCodec] or [WithTopicConstraints],
// the topic is validated immediately and an error is returned if it fails — no
// channel is registered in that case.
//
// Any [TopicParam] entry whose name does not appear as a {varName} placeholder
// in the topic template causes Register to return an error immediately.
func (c Channel[T]) Register(b *Builder) (*ChannelHandle[T], error) {
	if b.topicCodec != nil {
		if err := b.topicCodec.Validate(internal.StripTemplateVars(c.topic)); err != nil {
			return nil, InvalidTopicError{Topic: c.topic, Err: err}
		}
	}

	var cb channelBuilder
	for _, opt := range c.opts {
		opt.applyChannel(&cb)
	}

	if err := codex.ValidateDeclaredParams(c.topic, toCodexParams(cb.topicParams)); err != nil {
		return nil, err
	}

	frozen := buildChannelItem(c.topic, c.codec, cb)

	jsonFmt := format.JSON(c.codec)

	schemes := make(map[string]SecurityScheme, len(cb.securitySchemes))
	for k, v := range cb.securitySchemes {
		schemes[k] = v
	}
	h := &ChannelHandle[T]{
		Topic:             c.topic,
		Descriptor:        frozen,
		Decode:            func(payload []byte) (T, error) { return jsonFmt.Unmarshal(payload) },
		Encode:            func(msg T) ([]byte, error) { return jsonFmt.Marshal(msg) },
		topicParams:       cb.topicParams,
		topicCodec:        b.topicCodec,
		SecuritySchemes:   schemes,
		GlobalSecurity:    slices.Clone(b.globalSecurity),
		errorChannelRules: cb.errorChannelRules,
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

	entry := &typedChannelEntry[T]{topicStr: c.topic, handle: h}
	b.entries = append(b.entries, entry)
	return h, nil
}

// ClientHandle returns a [ChannelHandle] for client-side use without registering
// with a [Builder]. No AsyncAPI spec registration occurs (the handle never
// appears in any [Builder.AsyncAPISpec] output), but [ChannelHandle.SecuritySchemes]
// IS populated from the channel's own [WithSecurityScheme] declarations — the
// same declarations [Channel.Register] uses — so a channel's security scheme
// is enforced identically whether the handle came from Register (server) or
// ClientHandle (client). GlobalSecurity is left empty (there is no [Builder]
// to source it from); a channel relying on inherited global security has
// nothing to inherit via ClientHandle — declare Security explicitly on the
// channel if client-side enforcement is needed without a Builder.
//
// Use ClientHandle when only the codec and topic definitions are needed (no
// AsyncAPI spec, no server), or when sharing a [Channel] definition between
// publisher and subscriber in the same binary without a builder registration.
//
// The returned handle has the same Decode / Encode helpers and BuildTopic /
// ValidateTopicVars methods as a handle returned by [Channel.Register].
//
// Example — client-only usage (no builder required):
//
//	var SensorChannel = events.NewChannel[SensorReading]("sensors/{sensorID}/data",
//	    sensorCodec, events.TopicParam{Name: "sensorID"}.WithCodec(sensorIDCodec),
//	)
//
//	handle := SensorChannel.ClientHandle()
//	domain.SensorReadings.Bind(ctx, mqtt5.SubscribeAdapter(client, router, handle, 0, fmt, opts))
//
// Mirrors [rest.Route.ClientHandle] and [reqreply.Route.ClientHandle].
func (c Channel[T]) ClientHandle() *ChannelHandle[T] {
	var cb channelBuilder
	for _, opt := range c.opts {
		opt.applyChannel(&cb)
	}

	frozen := buildChannelItem(c.topic, c.codec, cb)
	jsonFmt := format.JSON(c.codec)

	schemes := make(map[string]SecurityScheme, len(cb.securitySchemes))
	for k, v := range cb.securitySchemes {
		schemes[k] = v
	}

	h := &ChannelHandle[T]{
		Topic:             c.topic,
		Descriptor:        frozen,
		Decode:            func(payload []byte) (T, error) { return jsonFmt.Unmarshal(payload) },
		Encode:            func(msg T) ([]byte, error) { return jsonFmt.Marshal(msg) },
		topicParams:       cb.topicParams,
		mergeFields:       mustAssertMergeFields[T]("ClientHandle", cb.mergeFields),
		errorChannelRules: cb.errorChannelRules,
		SecuritySchemes:   schemes,
	}
	// Apply any inline Formats/SubscribeFormats/PublishFormats ChannelOpt
	// declared on the Channel -- the SAME cb.formats/cb.subscribeFormats/
	// cb.publishFormats fields Register applies server-side. Without
	// this, ClientHandle silently ignored a declared wire format and
	// mqtt5/zeromq client-side calls always fell back to JSON regardless
	// of what was declared (a confirmed bug).
	if cb.formats != nil {
		fmts, ok := cb.formats.([]format.Format[T])
		if !ok {
			panic(fmt.Sprintf("api/events: ClientHandle: %s", FormatOptError{Direction: "both",
				Err: fmt.Errorf("want []format.Format[%T], got %T", *new(T), cb.formats)}.Error()))
		}
		h.WithFormats(fmts...)
	}
	if cb.subscribeFormats != nil {
		fmts, ok := cb.subscribeFormats.([]format.Format[T])
		if !ok {
			panic(fmt.Sprintf("api/events: ClientHandle: %s", FormatOptError{Direction: "subscribe",
				Err: fmt.Errorf("want []format.Format[%T], got %T", *new(T), cb.subscribeFormats)}.Error()))
		}
		h.WithSubscribeFormats(fmts...)
	}
	if cb.publishFormats != nil {
		fmts, ok := cb.publishFormats.([]format.Format[T])
		if !ok {
			panic(fmt.Sprintf("api/events: ClientHandle: %s", FormatOptError{Direction: "publish",
				Err: fmt.Errorf("want []format.Format[%T], got %T", *new(T), cb.publishFormats)}.Error()))
		}
		h.WithPublishFormats(fmts...)
	}
	return h
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
// [Channel.Register]).
func (e *rawChannelEntry) securitySchemes() map[string]SecurityScheme { return nil }

// AddChannelItem registers a pre-built [asyncapi.ChannelItem] under topic.
// Use this for channels the single-codec [Channel] declaration cannot
// express — a duplex socket whose inbound and outbound frames are different
// types (ports.RegisterSocket builds the item from a SocketPattern).
//
// The builder-level topic codec is NOT applied — the topic may be an HTTP
// upgrade path (e.g. "/live/{room}") rather than an MQTT-style topic.
// SchemaName references in the item's operations participate in the usual
// dangling-$ref validation at [Builder.AsyncAPISpec] time.
func (b *Builder) AddChannelItem(topic string, item asyncapi.ChannelItem) *Builder {
	b.entries = append(b.entries, &rawChannelEntry{topicStr: topic, item: item})
	return b
}

// AsyncAPISpec builds a complete AsyncAPI 3.0 document from all registered channels.
// Returns an error if any non-empty SchemaName references a schema that will not
// be present in components/schemas (a dangling $ref).
func (b *Builder) AsyncAPISpec() (asyncapi.Document, error) {
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
	// [rest.Builder.OpenAPISpec]'s documented behavior. Entry order
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

// AppendTo writes all channels registered on this Builder into db, which
// must have been created by [asyncapi.NewDocumentBuilder]. Servers, schemas,
// and security schemes owned by this Builder are NOT written — the caller is
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
func (b *Builder) AppendTo(db *asyncapi.DocumentBuilder) error {
	return b.buildInto(db)
}

// buildInto writes all registered channels into db.
func (b *Builder) buildInto(db *asyncapi.DocumentBuilder) error {
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
// or when the name was explicitly registered via [Builder.AddSchema].
func (b *Builder) checkDanglingRefs() error {
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
