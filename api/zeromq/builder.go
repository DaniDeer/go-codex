package zeromq

import (
	"fmt"
	"strings"
	"unicode"

	"github.com/DaniDeer/go-codex/api/rest"
	asyncapi "github.com/DaniDeer/go-codex/render/asyncapi/v3"
	"github.com/DaniDeer/go-codex/schema"
)

// Builder accumulates ZMQ REQ/REP socket registrations and produces an
// AsyncAPI 3.0 document with request-reply operations.
//
// Create a Builder with [NewBuilder], add servers via [AddServer], register
// routes via [Register], and call [AsyncAPISpec] to produce the document.
type Builder struct {
	docBuilder *asyncapi.DocumentBuilder
}

// NewBuilder returns a Builder initialised with the given Info.
func NewBuilder(info Info) *Builder {
	return &Builder{
		docBuilder: asyncapi.NewDocumentBuilder(info),
	}
}

// AddServer registers a named server in the AsyncAPI document. Servers appear
// in output in registration order.
//
// Use Protocol: "zmq" for ZeroMQ servers.
func (b *Builder) AddServer(name string, s Server) *Builder {
	b.docBuilder.AddServer(name, s)
	return b
}

// Register registers route with b for AsyncAPI spec generation and returns a
// [rest.RouteHandle] for use with [adapters/zeromq].
//
// Internally, Register adds two channels (request + reply) and one send
// operation with a reply: block, plus one receive operation on the reply
// channel. The returned handle is codec-backed and ready for use with
// zmqadapter.Serve and zmqadapter.Call — no extra wiring required.
//
// Register is a free generic function (Go generics do not allow adding methods
// to external types such as rest.Route).
//
// Example:
//
//	handle, err := zeromq.Register(builder, ComputeRoute,
//	    zeromq.SocketMeta{OperationID: "compute", Summary: "Add integers."})
func Register[Req, Resp any](b *Builder, route rest.Route[Req, Resp], meta SocketMeta) (*rest.RouteHandle[Req, Resp], error) {
	// Obtain a RouteHandle by registering with a throwaway rest.Builder.
	// The throwaway builder is used only to trigger codec initialisation;
	// its OpenAPI spec is discarded.
	throwaway := rest.NewBuilder(rest.Info{})
	handle, err := route.Register(throwaway)
	if err != nil {
		return nil, fmt.Errorf("zeromq: register route: %w", err)
	}

	path := handle.Descriptor.Path

	// Derive operation IDs and channel keys.
	base := meta.OperationID
	if base == "" {
		base = pathToID(path)
	}
	reqChannelKey := base
	replyChannelKey := base + "Reply"
	sendOpID := "send" + capitalise(base)
	recvOpID := "receive" + capitalise(base) + "Reply"

	// Extract request schema from the RouteHandle descriptor.
	var reqSchema schema.Schema
	if handle.Descriptor.RequestBody != nil {
		reqSchema = handle.Descriptor.RequestBody.Schema
	}

	// Extract response schema from the first response.
	var respSchema schema.Schema
	if len(handle.Descriptor.Responses) > 0 && handle.Descriptor.Responses[0].Schema != nil {
		respSchema = *handle.Descriptor.Responses[0].Schema
	}

	// Register request channel.
	b.docBuilder.AddChannel(reqChannelKey, asyncapi.ChannelItem{
		Address: path,
		Summary: meta.Summary,
		Tags:    tagsToSlice(meta.Tags),
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

	// Register reply channel (receive-only).
	b.docBuilder.AddReplyChannel(replyChannelKey, asyncapi.ChannelItem{
		Address: path + "/reply",
		Subscribe: &asyncapi.Operation{
			OperationID: recvOpID,
			Message: asyncapi.Message{
				Schema:     respSchema,
				SchemaName: meta.RespSchemaName,
			},
		},
	})

	return handle, nil
}

// AsyncAPISpec builds and returns the accumulated AsyncAPI 3.0 document.
// Returns an error if any registered channel is invalid.
func (b *Builder) AsyncAPISpec() (asyncapi.Document, error) {
	return b.docBuilder.Build()
}

// pathToID converts a route path like "/compute" or "/users/{id}" to a
// camelCase identifier like "compute" or "usersId".
func pathToID(path string) string {
	// Strip leading slash and template braces.
	path = strings.TrimPrefix(path, "/")
	path = strings.NewReplacer("{", "", "}", "", "/", "_").Replace(path)
	if path == "" {
		return "socket"
	}
	// Convert to camelCase: split on _ and capitalise each word except the first.
	parts := strings.Split(path, "_")
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

// tagsToSlice converts a slice of tag strings into the value that
// asyncapi.ChannelItem.Tags expects ([]string).
func tagsToSlice(tags []string) []string {
	return tags
}
