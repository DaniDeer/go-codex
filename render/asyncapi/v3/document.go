package v3

import (
	"encoding/json"
	"fmt"
	"sort"
	"unicode"

	"github.com/DaniDeer/go-codex/route"
	"github.com/DaniDeer/go-codex/schema"
	"gopkg.in/yaml.v3"
)

// Info holds the metadata for an AsyncAPI document.
type Info struct {
	Title       string
	Version     string
	Description string
}

// Server describes one entry in the AsyncAPI 3.0 servers map.
//
// URL holds the server address (host[:port]).  In AsyncAPI 3.0 the field is
// called "host"; the renderer emits URL as the "host" value so that code
// written against AsyncAPI 2.6 (events.Server{URL: "...", Protocol: "..."})
// continues to compile without change.
type Server struct {
	URL         string // emitted as "host" in AsyncAPI 3.0
	Protocol    string // e.g. "amqp", "mqtt", "kafka", "https"
	Description string
	// Security, when non-nil, lists security requirements for all operations on
	// this server. Per-operation security in ChannelItem.Subscribe/Publish takes
	// precedence.
	Security []route.SecurityRequirement
}

// Message describes the payload of an AsyncAPI 3.0 operation.
//
// When SchemaName is non-empty, the renderer emits a $ref in the payload and
// registers Schema under that name in components/schemas automatically.
// When SchemaName is empty, Schema is inlined as the payload.
type Message struct {
	Name string
	// Schema is the payload schema. Required when SchemaName is non-empty.
	Schema schema.Schema
	// SchemaName, when non-empty, emits a $ref and registers Schema in components/schemas.
	SchemaName  string
	ContentType string // defaults to "application/json"
}

// Parameter describes a channel parameter in an AsyncAPI 3.0 document.
//
// Channel parameters correspond to {varName} placeholders in topic templates.
// Each parameter may carry a description and a JSON Schema.
type Parameter struct {
	Description string
	Schema      schema.Schema
}

// OperationReply describes the reply leg of a request-reply operation in an
// AsyncAPI 3.0 document. It is embedded in the sending operation to declare
// which channel carries the reply messages.
//
// In AsyncAPI 3.0 this maps to the `reply:` block inside an operation:
//
//	operations:
//	  sendRequest:
//	    action: send
//	    channel: { $ref: '#/channels/request' }
//	    reply:
//	      channel: { $ref: '#/channels/reply' }
type OperationReply struct {
	// Channel is the key of the reply channel as registered in the document
	// (e.g. via [DocumentBuilder.AddChannel] or [DocumentBuilder.AddReplyChannel]).
	// The renderer emits this as a $ref to #/channels/<Channel>.
	Channel string
}

// Operation describes a subscribe (receive) or publish (send) operation.
type Operation struct {
	OperationID string
	Summary     string
	Description string
	Tags        []string
	Message     Message
	// Reply, when non-nil, declares the reply channel for request-reply patterns.
	// Set this on the sending (action: send) operation only. The reply channel
	// must be registered in the same document via [DocumentBuilder.AddReplyChannel].
	Reply *OperationReply
	// Security, when non-nil, sets per-operation security requirements.
	// nil means the operation inherits server-level security.
	// An empty slice marks the operation as unsecured.
	Security []route.SecurityRequirement
}

// ChannelItem describes one channel with optional subscribe and publish operations.
// In AsyncAPI 3.0 the channel key is a logical identifier; Address is the actual
// topic string. Operations are emitted as a separate top-level map.
type ChannelItem struct {
	// Address is the actual topic address (e.g. "user/created"). When empty,
	// the channel key is used as the address.
	Address     string
	Title       string
	Summary     string
	Description string
	Tags        []string
	// Parameters describes the {varName} placeholders in the topic template.
	// Keyed by variable name (without braces).
	Parameters map[string]Parameter
	// Subscribe is the operation where the application receives messages.
	Subscribe *Operation
	// Publish is the operation where the application sends messages.
	Publish *Operation
}

// namedServer pairs a server name with its Server value for deterministic output.
type namedServer struct {
	name   string
	server Server
}

// Document is a full AsyncAPI 3.0 document produced by DocumentBuilder.
// Use MarshalJSON or MarshalYAML to serialise it.
type Document struct {
	info            Info
	servers         []namedServer
	channels        map[string]ChannelItem
	replyChannels   map[string]struct{}
	schemas         map[string]schema.Schema
	securitySchemes map[string]route.SecurityScheme
}

// DocumentBuilder accumulates channels and named schemas, then produces a Document.
type DocumentBuilder struct {
	info            Info
	servers         []namedServer
	channels        map[string]ChannelItem
	replyChannels   map[string]struct{} // keys of reply-only channels; skip publish/subscribe validation
	schemas         map[string]schema.Schema
	securitySchemes map[string]route.SecurityScheme
}

// NewDocumentBuilder returns a builder initialised with the given Info.
func NewDocumentBuilder(info Info) *DocumentBuilder {
	return &DocumentBuilder{
		info:            info,
		channels:        make(map[string]ChannelItem),
		replyChannels:   make(map[string]struct{}),
		schemas:         make(map[string]schema.Schema),
		securitySchemes: make(map[string]route.SecurityScheme),
	}
}

// AddServer registers a named server in the document. Servers appear in output
// in registration order.
func (b *DocumentBuilder) AddServer(name string, s Server) *DocumentBuilder {
	b.servers = append(b.servers, namedServer{name: name, server: s})
	return b
}

// AddChannel registers a named channel in the document.
func (b *DocumentBuilder) AddChannel(name string, c ChannelItem) *DocumentBuilder {
	b.channels[name] = c
	return b
}

// AddSchema registers a named schema in components/schemas.
// Explicitly registered schemas take precedence over schemas inferred from channels.
func (b *DocumentBuilder) AddSchema(name string, s schema.Schema) *DocumentBuilder {
	b.schemas[name] = s
	return b
}

// AddSecurityScheme registers a named security scheme in components/securitySchemes.
// The name must match those used in SecurityRequirement maps on operations and servers.
func (b *DocumentBuilder) AddSecurityScheme(name string, s route.SecurityScheme) *DocumentBuilder {
	b.securitySchemes[name] = s
	return b
}

// AddReplyChannel registers a reply-only channel — one that carries reply messages
// in a request-reply pattern (e.g. ZMQ REQ/REP, AMQP RPC).
//
// Unlike [AddChannel], reply channels are not required to have a Subscribe or
// Publish operation set: the sending operation references them via
// [OperationReply.Channel] rather than declaring a separate operation.
//
// Typical usage with [api/reqreply]:
//
//	b.AddReplyChannel("computeReply", asyncapi.ChannelItem{
//	    Address: "/compute/reply",
//	    Subscribe: &asyncapi.Operation{
//	        OperationID: "receiveComputeReply",
//	        Message:     asyncapi.Message{Schema: respSchema},
//	    },
//	})
func (b *DocumentBuilder) AddReplyChannel(name string, c ChannelItem) *DocumentBuilder {
	b.channels[name] = c
	b.replyChannels[name] = struct{}{}
	return b
}

// AppendChannelsTo copies all channels registered on b into other.
// Servers, schemas, and security schemes are NOT copied — the caller is
// responsible for registering those on other directly. This method is
// intended for combining two builders (e.g. one from [api/events] and one
// from [api/reqreply]) into a single document:
//
//	doc := asyncapi.NewDocumentBuilder(info)
//	doc.AddServer("mqtt5", server)
//
//	eventsBuilder.AppendTo(doc)   // pub/sub channels
//	reqreplyBuilder.AppendTo(doc) // request-reply channels
//
//	spec, err := doc.Build()
func (b *DocumentBuilder) AppendChannelsTo(other *DocumentBuilder) {
	for name, ch := range b.channels {
		if _, isReply := b.replyChannels[name]; isReply {
			other.AddReplyChannel(name, ch)
		} else {
			other.AddChannel(name, ch)
		}
	}
}

// Build validates the accumulated channels and produces a Document.
//
// Validation:
//   - Each channel must have at least one of Subscribe or Publish set.
func (b *DocumentBuilder) Build() (Document, error) {
	// Collect schemas from channel messages; explicit schemas take precedence.
	schemas := make(map[string]schema.Schema)
	for _, ch := range b.channels {
		collectMessageSchema(ch.Subscribe, schemas)
		collectMessageSchema(ch.Publish, schemas)
	}
	for k, v := range b.schemas {
		schemas[k] = v // explicit wins
	}

	// Validate channels. Reply-only channels are exempt from the
	// "must have subscribe or publish" requirement — they are referenced by
	// the reply: block of a sending operation, not by their own operations.
	for name, ch := range b.channels {
		if _, isReply := b.replyChannels[name]; isReply {
			continue
		}
		if ch.Subscribe == nil && ch.Publish == nil {
			return Document{}, fmt.Errorf("channel %q has no subscribe or publish operation", name)
		}
	}

	return Document{
		info:            b.info,
		servers:         b.servers,
		channels:        b.channels,
		replyChannels:   b.replyChannels,
		schemas:         schemas,
		securitySchemes: b.securitySchemes,
	}, nil
}

// collectMessageSchema extracts a named schema from an operation's message.
func collectMessageSchema(op *Operation, schemas map[string]schema.Schema) {
	if op == nil {
		return
	}
	if op.Message.SchemaName != "" {
		schemas[op.Message.SchemaName] = op.Message.Schema
	}
}

// MarshalJSON encodes the document as JSON bytes.
func (d Document) MarshalJSON() ([]byte, error) {
	return json.MarshalIndent(d.toMap(), "", "  ")
}

// MarshalYAML encodes the document as YAML bytes.
func (d Document) MarshalYAML() ([]byte, error) {
	return yaml.Marshal(d.toMap())
}

// toMap converts the document to a map[string]any suitable for JSON/YAML marshaling.
func (d Document) toMap() map[string]any {
	doc := map[string]any{
		"asyncapi": "3.0.0",
		"info":     buildInfo(d.info),
	}

	if len(d.servers) > 0 {
		doc["servers"] = buildServers(d.servers)
	}

	channels, operations := buildChannelsAndOperations(d.channels)
	if len(channels) > 0 {
		doc["channels"] = channels
	}
	if len(operations) > 0 {
		doc["operations"] = operations
	}

	components := map[string]any{}
	if len(d.schemas) > 0 {
		components["schemas"] = buildComponentsSchemas(d.schemas)
	}
	if len(d.securitySchemes) > 0 {
		components["securitySchemes"] = buildSecuritySchemes(d.securitySchemes)
	}
	if len(components) > 0 {
		doc["components"] = components
	}

	return doc
}

// buildInfo produces the AsyncAPI info object.
func buildInfo(info Info) map[string]any {
	m := map[string]any{
		"title":   info.Title,
		"version": info.Version,
	}
	if info.Description != "" {
		m["description"] = info.Description
	}
	return m
}

// buildServers produces the AsyncAPI 3.0 servers map in insertion order.
func buildServers(servers []namedServer) map[string]any {
	out := make(map[string]any, len(servers))
	for _, ns := range servers {
		srv := map[string]any{
			"host":     ns.server.URL,
			"protocol": ns.server.Protocol,
		}
		if ns.server.Description != "" {
			srv["description"] = ns.server.Description
		}
		if len(ns.server.Security) > 0 {
			srv["security"] = buildSecurityRequirements(ns.server.Security)
		}
		out[ns.name] = srv
	}
	return out
}

// buildChannelsAndOperations produces the AsyncAPI 3.0 channels map and
// operations map from ChannelItems. In 3.0 these are separate top-level keys.
func buildChannelsAndOperations(channels map[string]ChannelItem) (map[string]any, map[string]any) {
	chOut := make(map[string]any, len(channels))
	opOut := map[string]any{}

	// Sort channel keys for deterministic output.
	keys := make([]string, 0, len(channels))
	for k := range channels {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	for _, key := range keys {
		ch := channels[key]
		chItem := map[string]any{}

		address := ch.Address
		if address == "" {
			address = key
		}
		chItem["address"] = address

		if ch.Title != "" {
			chItem["title"] = ch.Title
		}
		if ch.Summary != "" {
			chItem["summary"] = ch.Summary
		}
		if ch.Description != "" {
			chItem["description"] = ch.Description
		}
		if len(ch.Tags) > 0 {
			tags := make([]any, len(ch.Tags))
			for i, t := range ch.Tags {
				tags[i] = map[string]any{"name": t}
			}
			chItem["tags"] = tags
		}
		if len(ch.Parameters) > 0 {
			chItem["parameters"] = buildParameters(ch.Parameters)
		}

		// Collect messages from operations into the channel messages map.
		messages := map[string]any{}
		if ch.Subscribe != nil && ch.Subscribe.Message.SchemaName != "" {
			messages[ch.Subscribe.Message.SchemaName] = buildMessage(ch.Subscribe.Message)
		} else if ch.Subscribe != nil {
			messages["subscribeMessage"] = buildMessage(ch.Subscribe.Message)
		}
		if ch.Publish != nil && ch.Publish.Message.SchemaName != "" {
			messages[ch.Publish.Message.SchemaName] = buildMessage(ch.Publish.Message)
		} else if ch.Publish != nil {
			messages["publishMessage"] = buildMessage(ch.Publish.Message)
		}
		if len(messages) > 0 {
			chItem["messages"] = messages
		}

		chOut[key] = chItem

		// Emit subscribe operation (action: receive).
		if ch.Subscribe != nil {
			opID := ch.Subscribe.OperationID
			if opID == "" {
				opID = "receive" + capitalise(key)
			}
			opOut[opID] = buildOperation(ch.Subscribe, key, "receive")
		}

		// Emit publish operation (action: send).
		if ch.Publish != nil {
			opID := ch.Publish.OperationID
			if opID == "" {
				opID = "send" + capitalise(key)
			}
			opOut[opID] = buildOperation(ch.Publish, key, "send")
		}
	}

	return chOut, opOut
}

// buildParameters converts a map of channel parameters into the AsyncAPI parameters object.
func buildParameters(params map[string]Parameter) map[string]any {
	out := make(map[string]any, len(params))
	for name, p := range params {
		entry := map[string]any{}
		if p.Description != "" {
			entry["description"] = p.Description
		}
		if !p.Schema.IsZero() {
			entry["schema"] = schemaRef(p.Schema, "")
		} else {
			entry["schema"] = map[string]any{"type": "string"}
		}
		out[name] = entry
	}
	return out
}

// buildOperation converts an Operation into an AsyncAPI 3.0 operation object.
func buildOperation(op *Operation, channelKey, action string) map[string]any {
	o := map[string]any{
		"action":  action,
		"channel": map[string]any{"$ref": "#/channels/" + channelKey},
	}
	if op.Summary != "" {
		o["summary"] = op.Summary
	}
	if op.Description != "" {
		o["description"] = op.Description
	}
	if len(op.Tags) > 0 {
		tags := make([]any, len(op.Tags))
		for i, tag := range op.Tags {
			tags[i] = map[string]any{"name": tag}
		}
		o["tags"] = tags
	}
	if op.Reply != nil && op.Reply.Channel != "" {
		o["reply"] = map[string]any{
			"channel": map[string]any{"$ref": "#/channels/" + op.Reply.Channel},
		}
	}
	if op.Security != nil {
		o["security"] = buildSecurityRequirements(op.Security)
	}
	return o
}

// buildMessage converts a Message into an AsyncAPI 3.0 message object.
func buildMessage(m Message) map[string]any {
	msg := map[string]any{
		"payload": schemaRef(m.Schema, m.SchemaName),
	}
	if m.Name != "" {
		msg["name"] = m.Name
	}
	if m.ContentType != "" {
		msg["contentType"] = m.ContentType
	}
	return msg
}

// capitalise returns s with the first rune uppercased.
func capitalise(s string) string {
	if s == "" {
		return ""
	}
	runes := []rune(s)
	runes[0] = unicode.ToUpper(runes[0])
	return string(runes)
}
