package asyncapi

import (
	"encoding/json"
	"fmt"

	"github.com/DaniDeer/go-codex/schema"
	"gopkg.in/yaml.v3"
)

// Info holds the metadata for an AsyncAPI document.
type Info struct {
	Title       string
	Version     string
	Description string
}

// Server describes one entry in the AsyncAPI servers map.
type Server struct {
	URL         string
	Protocol    string // e.g. "amqp", "mqtt", "kafka", "https"
	Description string
}

// Message describes the payload of an AsyncAPI operation.
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

// Operation describes a subscribe or publish operation on a channel.
type Operation struct {
	Summary     string
	Description string
	Tags        []string
	Message     Message
}

// ChannelItem describes one channel with optional subscribe and publish operations.
type ChannelItem struct {
	Description string
	// Subscribe is the operation where the application receives messages.
	Subscribe *Operation
	// Publish is the operation where the application sends messages.
	Publish *Operation
}

// Document is a full AsyncAPI 2.6 document produced by DocumentBuilder.
// Use MarshalJSON or MarshalYAML to serialise it.
type Document struct {
	info     Info
	servers  map[string]Server
	channels map[string]ChannelItem
	schemas  map[string]schema.Schema
}

// DocumentBuilder accumulates channels and named schemas, then produces a Document.
type DocumentBuilder struct {
	info     Info
	servers  map[string]Server
	channels map[string]ChannelItem
	schemas  map[string]schema.Schema
}

// NewDocumentBuilder returns a builder initialised with the given Info.
func NewDocumentBuilder(info Info) *DocumentBuilder {
	return &DocumentBuilder{
		info:     info,
		servers:  make(map[string]Server),
		channels: make(map[string]ChannelItem),
		schemas:  make(map[string]schema.Schema),
	}
}

// AddServer registers a named server in the document.
func (b *DocumentBuilder) AddServer(name string, s Server) *DocumentBuilder {
	b.servers[name] = s
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

	// Validate channels.
	for name, ch := range b.channels {
		if ch.Subscribe == nil && ch.Publish == nil {
			return Document{}, fmt.Errorf("channel %q has no subscribe or publish operation", name)
		}
	}

	return Document{
		info:     b.info,
		servers:  b.servers,
		channels: b.channels,
		schemas:  schemas,
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
		"asyncapi": "2.6.0",
		"info":     buildInfo(d.info),
	}

	if len(d.servers) > 0 {
		doc["servers"] = buildServers(d.servers)
	}

	if len(d.channels) > 0 {
		doc["channels"] = buildChannels(d.channels)
	}

	if len(d.schemas) > 0 {
		doc["components"] = map[string]any{
			"schemas": buildComponentsSchemas(d.schemas),
		}
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

// buildServers produces the AsyncAPI servers map.
func buildServers(servers map[string]Server) map[string]any {
	out := make(map[string]any, len(servers))
	for name, s := range servers {
		srv := map[string]any{
			"url":      s.URL,
			"protocol": s.Protocol,
		}
		if s.Description != "" {
			srv["description"] = s.Description
		}
		out[name] = srv
	}
	return out
}

// buildChannels produces the AsyncAPI channels map.
func buildChannels(channels map[string]ChannelItem) map[string]any {
	out := make(map[string]any, len(channels))
	for name, ch := range channels {
		item := map[string]any{}
		if ch.Description != "" {
			item["description"] = ch.Description
		}
		if ch.Subscribe != nil {
			item["subscribe"] = buildOperation(ch.Subscribe)
		}
		if ch.Publish != nil {
			item["publish"] = buildOperation(ch.Publish)
		}
		out[name] = item
	}
	return out
}

// buildOperation converts an Operation into an AsyncAPI operation object.
func buildOperation(op *Operation) map[string]any {
	o := map[string]any{
		"message": buildMessage(op.Message),
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
	return o
}

// buildMessage converts a Message into an AsyncAPI message object.
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
