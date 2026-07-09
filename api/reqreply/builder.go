package reqreply

import (
	"strings"
	"unicode"

	asyncapi "github.com/DaniDeer/go-codex/render/asyncapi/v3"
	"github.com/DaniDeer/go-codex/schema"
)

// Builder accumulates [Route] registrations and produces an AsyncAPI 3.0
// document with request-reply operations.
//
// Create a Builder with [NewBuilder], add servers via [AddServer], register
// routes via [Route.Register], and call [AsyncAPISpec] to produce the document.
type Builder struct {
	docBuilder *asyncapi.DocumentBuilder
	topics     map[string]struct{} // guard against duplicate topic registration
}

// NewBuilder returns a Builder initialised with the given Info.
func NewBuilder(info Info) *Builder {
	return &Builder{
		docBuilder: asyncapi.NewDocumentBuilder(info),
		topics:     make(map[string]struct{}),
	}
}

// AddServer registers a named server in the AsyncAPI document. Servers appear
// in output in registration order.
//
// Use Protocol: "zmq" for ZeroMQ servers, "mqtt5" for MQTT 5.0, etc.
func (b *Builder) AddServer(name string, s Server) *Builder {
	b.docBuilder.AddServer(name, s)
	return b
}

// registerRoute is called by [Route.Register] to record the two AsyncAPI
// channels and operations for a route.
func (b *Builder) registerRoute(topic string, reqSchema, respSchema schema.Schema, meta RouteMeta) {
	b.topics[topic] = struct{}{}

	base := meta.OperationID
	if base == "" {
		base = topicToID(topic)
	}
	reqChannelKey := base
	replyChannelKey := base + "Reply"
	sendOpID := "send" + capitalise(base)
	recvOpID := "receive" + capitalise(base) + "Reply"

	// Register request channel.
	b.docBuilder.AddChannel(reqChannelKey, asyncapi.ChannelItem{
		Address: topic,
		Summary: meta.Summary,
		Tags:    meta.Tags,
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
		Address: topic + "/reply",
		Subscribe: &asyncapi.Operation{
			OperationID: recvOpID,
			Message: asyncapi.Message{
				Schema:     respSchema,
				SchemaName: meta.RespSchemaName,
			},
		},
	})
}

// AsyncAPISpec builds and returns the accumulated AsyncAPI 3.0 document.
// Returns an error if any registered channel is invalid.
func (b *Builder) AsyncAPISpec() (asyncapi.Document, error) {
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
