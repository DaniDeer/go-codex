package events

import (
	"fmt"
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
// AddSecurityScheme registers it with the builder. The spec fields flow into the
// AsyncAPI document; Codec, when non-nil, is used by adapters to validate the
// raw credential string before SecurityFunc is called.
//
// MQTT note: paho.mqtt.golang (MQTT 3.1.1) does not expose per-message credentials.
// Codec-level extraction is a no-op for standard MQTT. Use SecurityFunc + closure
// (credentials passed at MQTT CONNECT time) for runtime enforcement.
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
//	b.AddSecurityScheme("apiKey", events.SecurityScheme{
//	    SecurityScheme: route.APIKeyScheme("X-API-Key", "header"),
//	}.WithCodec(codex.String().Refine(validate.NonEmptyString)))
func (s SecurityScheme) WithCodec(c codex.Codec[string]) SecurityScheme {
	s.Codec = &c
	return s
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

// ChannelOpt is the sealed interface for variadic [NewChannel] options.
//
// The following types implement ChannelOpt:
//   - [ChannelMeta] — channel-level metadata (title, summary, description, tags)
//   - [Subscribe] — subscribe operation metadata (application receives messages)
//   - [Publish] — publish operation metadata (application sends messages)
//   - [TopicParam] — topic template variable with optional codec and description
type ChannelOpt interface{ applyChannel(*channelBuilder) }

// channelBuilder accumulates ChannelOpt values before building the channel descriptor.
type channelBuilder struct {
	meta        ChannelMeta
	subscribe   *Subscribe
	publish     *Publish
	topicParams []TopicParam
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
	// Populated from Builder.AddSecurityScheme when AddChannel is called.
	// Adapters use this map to extract and validate credentials per scheme.
	SecuritySchemes map[string]SecurityScheme

	// GlobalSecurity holds the builder-level security requirements that apply
	// when the channel operation's Security field is nil (i.e. the channel
	// inherits global security). Adapters resolve the effective requirements as:
	//   reqs := handle.Descriptor.Subscribe.Security
	//   if reqs == nil { reqs = handle.GlobalSecurity }
	// Set via [Builder.AddGlobalSecurity]. nil when no global security is declared.
	GlobalSecurity []route.SecurityRequirement
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
	// Build codec lookup map from topicParams.
	codecMap := make(map[string]*codex.Codec[string], len(h.topicParams))
	for i := range h.topicParams {
		if h.topicParams[i].Codec != nil {
			codecMap[h.topicParams[i].Name] = h.topicParams[i].Codec
		}
	}
	result, err := internal.BuildFromTemplate(h.Topic, vars, codecMap,
		func(name string) error { return MissingTopicVarError{Name: name} },
		func(name, value string, err error) error {
			return TopicParamError{Name: name, Value: value, Err: err}
		},
	)
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
	for i := range h.topicParams {
		p := &h.topicParams[i]
		if p.Codec == nil {
			continue
		}
		val, ok := vars[p.Name]
		if !ok {
			return MissingTopicVarError{Name: p.Name}
		}
		if err := p.Codec.Validate(val); err != nil {
			return TopicParamError{Name: p.Name, Value: val, Err: err}
		}
	}
	return nil
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
type TopicParamError struct {
	Name  string // the {varName} that failed
	Value string // the value that was rejected
	Err   error  // the underlying codec error
}

func (e TopicParamError) Error() string {
	return fmt.Sprintf("invalid value %q for topic variable {%s}: %s", e.Value, e.Name, e.Err.Error())
}

// Unwrap allows errors.As and errors.Is to traverse the underlying codec error.
func (e TopicParamError) Unwrap() error { return e.Err }

// MissingTopicVarError is returned by [ChannelHandle.BuildTopic] when a {varName}
// placeholder in the topic template has no corresponding entry in the vars map.
//
// Use errors.As to extract the missing variable name:
//
//	var missingErr events.MissingTopicVarError
//	if errors.As(err, &missingErr) {
//	    log.Printf("caller forgot to supply topic variable {%s}", missingErr.Name)
//	}
type MissingTopicVarError struct {
	Name string // the variable name (without braces) that had no value
}

func (e MissingTopicVarError) Error() string {
	return fmt.Sprintf("missing value for topic variable {%s}", e.Name)
}

// InvalidTopicParamError is returned by [Channel.Register] when a [TopicParam] entry
// names a variable that does not appear in the topic template.
//
// Use errors.As to extract the offending name and the topic template:
//
//	var paramErr events.InvalidTopicParamError
//	if errors.As(err, &paramErr) {
//	    log.Printf("TopicParam %q not in topic %q", paramErr.Name, paramErr.Topic)
//	}
type InvalidTopicParamError struct {
	Name  string // the variable name (without braces) that is not in the template
	Topic string // the topic template that was validated against
}

func (e InvalidTopicParamError) Error() string {
	return fmt.Sprintf("api/events: TopicParams entry %q not found in topic template %q", e.Name, e.Topic)
}

// channelEntry is the type-erased interface stored inside Builder.
type channelEntry interface {
	topic() string
	descriptor() asyncapi.ChannelItem
}

// typedChannelEntry stores a pointer to the ChannelHandle so that the builder
// always sees the live descriptor at AsyncAPISpec() time.
type typedChannelEntry[T any] struct {
	topicStr string
	handle   *ChannelHandle[T]
}

func (e *typedChannelEntry[T]) topic() string                    { return e.topicStr }
func (e *typedChannelEntry[T]) descriptor() asyncapi.ChannelItem { return e.handle.Descriptor }

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
	info            Info
	servers         []namedServer
	entries         []channelEntry
	schemas         map[string]schema.Schema
	topicCodec      *codex.Codec[string]
	securitySchemes map[string]SecurityScheme
	globalSecurity  []route.SecurityRequirement
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
		info:            info,
		schemas:         make(map[string]schema.Schema),
		securitySchemes: make(map[string]SecurityScheme),
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

// AddSecurityScheme registers a named security scheme with the builder.
// The spec fields flow into the AsyncAPI document via AsyncAPISpec; Codec, when
// non-nil, is used by adapters to validate extracted credentials before
// SecurityFunc is called (MQTT adapters skip Codec validation — use SecurityFunc).
//
// The name must match those used in [route.Require] calls on Subscribe/Publish
// Security fields and in [Builder.AddGlobalSecurity].
func (b *Builder) AddSecurityScheme(name string, s SecurityScheme) *Builder {
	b.securitySchemes[name] = s
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

// Channel is a declarative event channel spec: topic, codec, and options.
// It is a value type — define it once, store it, pass it around, and register
// it with one or more [Builder] instances via [Channel.Register].
//
// Create a Channel with [NewChannel].
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

	templateVars := internal.ParseTemplateVars(c.topic)
	for _, tp := range cb.topicParams {
		if !templateVars[tp.Name] {
			return nil, InvalidTopicParamError{Name: tp.Name, Topic: c.topic}
		}
	}

	frozen := buildChannelItem(c.topic, c.codec, cb)

	jsonFmt := format.JSON(c.codec)

	schemes := make(map[string]SecurityScheme, len(b.securitySchemes))
	for k, v := range b.securitySchemes {
		schemes[k] = v
	}
	h := &ChannelHandle[T]{
		Topic:           c.topic,
		Descriptor:      frozen,
		Decode:          func(payload []byte) (T, error) { return jsonFmt.Unmarshal(payload) },
		Encode:          func(msg T) ([]byte, error) { return jsonFmt.Marshal(msg) },
		topicParams:     cb.topicParams,
		topicCodec:      b.topicCodec,
		SecuritySchemes: schemes,
		GlobalSecurity:  slices.Clone(b.globalSecurity),
	}
	entry := &typedChannelEntry[T]{topicStr: c.topic, handle: h}
	b.entries = append(b.entries, entry)
	return h, nil
}

// AsyncAPISpec builds a complete AsyncAPI 3.0 document from all registered channels.
// Returns an error if any non-empty SchemaName references a schema that will not
// be present in components/schemas (a dangling $ref).
func (b *Builder) AsyncAPISpec() (asyncapi.Document, error) {
	if err := b.checkDanglingRefs(); err != nil {
		return asyncapi.Document{}, err
	}
	ab := asyncapi.NewDocumentBuilder(b.info)
	for _, ns := range b.servers {
		ab.AddServer(ns.name, ns.server)
	}
	for name, s := range b.schemas {
		ab.AddSchema(name, s)
	}
	for name, s := range b.securitySchemes {
		ab.AddSecurityScheme(name, s.SecurityScheme)
	}
	for _, e := range b.entries {
		ab.AddChannel(e.topic(), e.descriptor())
	}
	return ab.Build()
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
