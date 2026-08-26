package reqreply

import (
	"fmt"
	"strings"
	"unicode"

	"github.com/DaniDeer/go-codex/api/internal"
	"github.com/DaniDeer/go-codex/codex"
	asyncapi "github.com/DaniDeer/go-codex/render/asyncapi/v3"
	"github.com/DaniDeer/go-codex/route"
	"github.com/DaniDeer/go-codex/schema"
)

// Builder accumulates [Route] registrations and produces an AsyncAPI 3.0
// document with request-reply operations.
//
// Create a Builder with [NewBuilder], add servers via [AddServer], register
// routes via [Route.Register], and call [AsyncAPISpec] to produce the document.
type Builder struct {
	docBuilder      *asyncapi.DocumentBuilder
	topics          map[string]struct{} // guard against duplicate topic registration
	securitySchemes map[string]SecurityScheme
	globalSecurity  []route.SecurityRequirement
	topicCodec      *codex.Codec[string]
}

// BuilderOption configures a [Builder] at construction time.
type BuilderOption func(*Builder)

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
//	b := reqreply.NewBuilder(info, reqreply.WithTopicConstraints(validate.MQTTPublishTopic))
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
//	b := reqreply.NewBuilder(info, reqreply.WithTopicConstraints(deviceLevel))
func WithTopicConstraints(cons ...codex.Constraint[string]) BuilderOption {
	c := codex.String().Refine(cons...)
	return WithTopicCodec(c)
}

// NewBuilder returns a Builder initialised with the given Info.
func NewBuilder(info Info, opts ...BuilderOption) *Builder {
	b := &Builder{
		docBuilder:      asyncapi.NewDocumentBuilder(info),
		topics:          make(map[string]struct{}),
		securitySchemes: make(map[string]SecurityScheme),
	}
	for _, opt := range opts {
		opt(b)
	}
	return b
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

// AddServer registers a named server in the AsyncAPI document. Servers appear
// in output in registration order.
//
// Use Protocol: "zmq" for ZeroMQ servers, "mqtt5" for MQTT 5.0, etc.
func (b *Builder) AddServer(name string, s Server) *Builder {
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
			},
			Reply: &asyncapi.OperationReply{Channel: replyChannelKey},
		},
	})

	// Register reply channel (receive-only — exempt from subscribe/publish validation).
	b.docBuilder.AddReplyChannel(replyChannelKey, asyncapi.ChannelItem{
		Address:    topic + "/reply",
		Parameters: params,
		Subscribe: &asyncapi.Operation{
			OperationID: recvOpID,
			Message: asyncapi.Message{
				Schema:     respSchema,
				SchemaName: meta.RespSchemaName,
			},
		},
	})

	for i, er := range errorReplies {
		suffix := "Error"
		if er.Code != "" {
			suffix += capitalise(topicToID(er.Code))
		} else {
			suffix += fmt.Sprintf("%d", i+1)
		}
		errReplyChannelKey := replyChannelKey + suffix
		errReplyAddress := topic + "/reply/error"
		if er.Code != "" {
			errReplyAddress += "/" + er.Code
		}
		if er.ChannelAddress != "" {
			errReplyAddress = er.ChannelAddress
		}
		errRecvOpID := "receive" + capitalise(base) + "Reply" + suffix
		if er.OperationID != "" {
			errRecvOpID = er.OperationID
		}
		b.docBuilder.AddReplyChannel(errReplyChannelKey, asyncapi.ChannelItem{
			Address: errReplyAddress,
			Subscribe: &asyncapi.Operation{
				OperationID: errRecvOpID,
				Description: er.Description,
				Message: asyncapi.Message{
					Schema:     er.Schema,
					SchemaName: er.SchemaName,
				},
			},
		})
	}
}

// AsyncAPISpec builds and returns the accumulated AsyncAPI 3.0 document.
// Returns an error if any registered channel is invalid.
func (b *Builder) AsyncAPISpec() (asyncapi.Document, error) {
	// Aggregate SecuritySchemes from every registered route's own
	// [WithSecurityScheme] declarations (there is no per-route entry list
	// to iterate here, unlike rest/events — schemes are accumulated
	// directly into b.securitySchemes as each route registers, in
	// [Route.Register]). Collision policy is last-registered-wins,
	// matching [rest.Builder.OpenAPISpec]/[events.Builder.AsyncAPISpec].
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
// [api/events.Builder] in a single AsyncAPI 3.0 document:
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
