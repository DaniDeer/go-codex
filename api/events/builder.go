// Package events provides a transport-agnostic event channel builder for go-codex.
//
// Define channels with codec-backed payload types; the builder returns a
// [ChannelHandle] with typed Decode and Encode helpers. Pass those helpers to
// any message broker (MQTT, AMQP, Kafka, NATS) — this package does not import
// any messaging library.
//
// Spec generation is also available: [Builder.AsyncAPISpec] derives a complete
// AsyncAPI 2.6 document from the registered channels.
//
// Typical usage:
//
//	b := events.NewBuilder(events.Info{Title: "User Events", Version: "1.0.0"})
//	b.AddServer("production", events.Server{
//	    URL:      "mqtt://broker.example.com",
//	    Protocol: "mqtt",
//	})
//
//	userCreated := events.AddChannel[UserCreated](b, "user/created", userCreatedCodec,
//	    events.ChannelConfig{
//	        Subscribe: &events.OperationConfig{
//	            Summary:    "A user was created",
//	            SchemaName: "UserCreatedEvent",
//	        },
//	    })
//
//	// In your broker callback (any library):
//	event, err := userCreated.Decode(msg.Payload())   // JSON → UserCreated, validates
//	payload, err := userCreated.Encode(event)          // UserCreated → JSON
//
//	// AsyncAPI 2.6 spec:
//	doc, err := b.AsyncAPISpec()
//	yaml, _  := doc.MarshalYAML()
//
// Encoding is JSON only. AddChannel uses [format.JSON] internally; for other
// formats construct a [format.Format] directly and call its Unmarshal/Marshal.
package events

import (
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/DaniDeer/go-codex/api/internal"
	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/format"
	"github.com/DaniDeer/go-codex/render/asyncapi"
	"github.com/DaniDeer/go-codex/schema"
)

// Info is an alias for [asyncapi.Info]. Using the alias avoids duplicating
// fields and keeps the two in sync automatically.
type Info = asyncapi.Info

// Server is an alias for [asyncapi.Server].
type Server = asyncapi.Server

// OperationConfig holds metadata for one direction (subscribe or publish) on a
// channel. It controls the operation entry in the AsyncAPI spec.
type OperationConfig struct {
	Summary     string
	Description string
	Tags        []string

	// SchemaName, when non-empty, emits a $ref for the payload schema in the
	// spec and registers the schema under that name in components/schemas.
	SchemaName string
}

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
type TopicParam struct {
	// Name is the variable name (without braces) as it appears in the topic template.
	Name string
	// Description is shown in the AsyncAPI spec for this parameter.
	Description string
	// Codec validates substituted values at [ChannelHandle.BuildTopic] time.
	// When non-nil, the codec's schema is also emitted in the AsyncAPI spec.
	// Nil means no runtime validation; the spec defaults to {type: string}.
	Codec *codex.Codec[string]
}

// ChannelConfig holds metadata for a channel registration.
//
// At least one of Subscribe or Publish must be non-nil. When both are set, the
// same payload codec is used for both directions.
type ChannelConfig struct {
	Description string

	// Subscribe describes the operation where the application receives messages.
	// Set to nil to omit the subscribe operation from the spec.
	Subscribe *OperationConfig

	// Publish describes the operation where the application sends messages.
	// Set to nil to omit the publish operation from the spec.
	Publish *OperationConfig

	// TopicParams describes {varName} placeholder variables in the topic template.
	// Each entry can add a description and/or a codec for runtime validation.
	// The codec schema is also emitted in the AsyncAPI parameters: block.
	//
	// TopicParams is optional: the builder auto-derives a minimal parameter entry
	// ({type: string}) for every {varName} in the topic template. Only specify
	// TopicParams when you need a description or runtime validation for a variable.
	//
	// Entry names must correspond to {varName} placeholders in the topic template;
	// unknown names cause [AddChannel] to return an error immediately.
	TopicParams []TopicParam
}

// ChannelHandle is returned by [AddChannel]. It holds the frozen spec
// descriptor and codec-backed Decode/Encode helpers.
type ChannelHandle[T any] struct {
	// Topic is the channel name (e.g. "user/created", "orders.placed").
	Topic string

	// Descriptor is the frozen asyncapi.ChannelItem built at registration time.
	Descriptor asyncapi.ChannelItem

	// Decode deserialises and validates a JSON payload into T.
	// All Refine constraints on the payload codec run automatically.
	Decode func(payload []byte) (T, error)

	// Encode serialises T to JSON bytes.
	Encode func(msg T) ([]byte, error)

	// topicParams holds per-variable params registered via TopicParams.
	topicParams []TopicParam

	// topicCodec is the builder-level topic codec (may be nil).
	// Used to re-validate the final assembled topic in BuildTopic.
	topicCodec *codex.Codec[string]
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
// Note: unlike [AddChannel], which validates a template-stripped topic, this method
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
		val := vars[p.Name]
		if err := p.Codec.Validate(val); err != nil {
			return TopicParamError{Name: p.Name, Value: val, Err: err}
		}
	}
	return nil
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

// InvalidTopicParamError is returned by [AddChannel] when a [TopicParam] entry
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

// typedChannelEntry stores the frozen topic and descriptor for one channel.
type typedChannelEntry[T any] struct {
	topicStr string
	frozen   asyncapi.ChannelItem
}

func (e *typedChannelEntry[T]) topic() string                    { return e.topicStr }
func (e *typedChannelEntry[T]) descriptor() asyncapi.ChannelItem { return e.frozen }

// InvalidTopicError is returned by [AddChannel] when the topic fails builder-level
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

// Builder accumulates channel registrations and produces AsyncAPI specs.
// Create one with [NewBuilder].
type Builder struct {
	info       Info
	servers    map[string]Server
	entries    []channelEntry
	schemas    map[string]schema.Schema
	topicCodec *codex.Codec[string]
}

// BuilderOption configures a [Builder] at construction time.
type BuilderOption func(*Builder)

// WithTopicCodec sets a codec used to validate every topic passed to [AddChannel].
// If the topic is invalid, [AddChannel] returns an error immediately.
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
	c := codex.Refine(codex.String(), cons...)
	return WithTopicCodec(c)
}

// NewBuilder returns a Builder initialised with the given API metadata.
func NewBuilder(info Info, opts ...BuilderOption) *Builder {
	b := &Builder{
		info:    info,
		servers: make(map[string]Server),
		schemas: make(map[string]schema.Schema),
	}
	for _, opt := range opts {
		opt(b)
	}
	return b
}

// AddServer registers a named server entry in the spec.
func (b *Builder) AddServer(name string, s Server) *Builder {
	b.servers[name] = s
	return b
}

// AddSchema registers a named schema in components/schemas.
// Use this to register reusable schemas that are referenced by SchemaName in
// channel configs but not inlined in any codec.
func (b *Builder) AddSchema(name string, s schema.Schema) *Builder {
	b.schemas[name] = s
	return b
}

// AddChannel registers a channel with the builder and returns a [ChannelHandle].
//
// codec is used to decode and validate incoming payloads and to encode outgoing
// messages. The same codec applies to both subscribe and publish directions.
//
// If the builder was created with [WithTopicCodec] or [WithTopicConstraints],
// the topic is validated immediately. An error is returned if validation fails —
// no channel is registered in that case.
//
// If config.TopicParams is non-empty, each entry name is verified to be a
// {varName} present in the topic template. An unknown name is a programming
// error and causes AddChannel to return an error.
//
// AddChannel is a free function (not a method) because Go requires type
// parameters to appear on free functions, not on method receivers.
//
// The descriptor is built and frozen at call time; later mutations to config
// do not affect the registered channel or the returned handle.
func AddChannel[T any](
	b *Builder,
	topic string,
	codec codex.Codec[T],
	config ChannelConfig,
) (*ChannelHandle[T], error) {
	if b.topicCodec != nil {
		if err := b.topicCodec.Validate(internal.StripTemplateVars(topic)); err != nil {
			return nil, InvalidTopicError{Topic: topic, Err: err}
		}
	}

	templateVars := internal.ParseTemplateVars(topic)
	for _, tp := range config.TopicParams {
		if !templateVars[tp.Name] {
			return nil, InvalidTopicParamError{Name: tp.Name, Topic: topic}
		}
	}

	frozen := buildChannelItem(topic, codec, config)

	entry := &typedChannelEntry[T]{topicStr: topic, frozen: frozen}
	b.entries = append(b.entries, entry)

	jsonFmt := format.JSON(codec)

	return &ChannelHandle[T]{
		Topic:       topic,
		Descriptor:  frozen,
		Decode:      func(payload []byte) (T, error) { return jsonFmt.Unmarshal(payload) },
		Encode:      func(msg T) ([]byte, error) { return jsonFmt.Marshal(msg) },
		topicParams: config.TopicParams,
		topicCodec:  b.topicCodec,
	}, nil
}

// AsyncAPISpec builds a complete AsyncAPI 2.6 document from all registered channels.
// Returns an error if any non-empty SchemaName references a schema that will not
// be present in components/schemas (a dangling $ref).
func (b *Builder) AsyncAPISpec() (asyncapi.Document, error) {
	if err := b.checkDanglingRefs(); err != nil {
		return asyncapi.Document{}, err
	}
	ab := asyncapi.NewDocumentBuilder(b.info)
	for name, s := range b.servers {
		ab.AddServer(name, s)
	}
	for name, s := range b.schemas {
		ab.AddSchema(name, s)
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

// buildChannelItem constructs a frozen asyncapi.ChannelItem from the topic,
// codec schema, and config. Deep-copies all slices to prevent later mutation
// from affecting the registered channel.
//
// Channel parameters are auto-derived from {varName} placeholders in topic.
// The schema for each parameter comes from the TopicParam.Codec (if set);
// TopicParam.Description adds a human-readable description.
func buildChannelItem[T any](topic string, codec codex.Codec[T], config ChannelConfig) asyncapi.ChannelItem {
	item := asyncapi.ChannelItem{
		Description: config.Description,
		Parameters:  buildTopicParameters(topic, config.TopicParams),
	}

	if config.Subscribe != nil {
		op := config.Subscribe
		item.Subscribe = &asyncapi.Operation{
			Summary:     op.Summary,
			Description: op.Description,
			Tags:        slices.Clone(op.Tags),
			Message: asyncapi.Message{
				Schema:     codec.Schema,
				SchemaName: op.SchemaName,
			},
		}
	}

	if config.Publish != nil {
		op := config.Publish
		item.Publish = &asyncapi.Operation{
			Summary:     op.Summary,
			Description: op.Description,
			Tags:        slices.Clone(op.Tags),
			Message: asyncapi.Message{
				Schema:     codec.Schema,
				SchemaName: op.SchemaName,
			},
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
