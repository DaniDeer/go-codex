package ports

import (
	"github.com/DaniDeer/go-codex/api/events"
	apimcp "github.com/DaniDeer/go-codex/api/mcp"
	"github.com/DaniDeer/go-codex/api/reqreply"
	"github.com/DaniDeer/go-codex/api/rest"
)

// namedPort is implemented by every port type ([SourcePort], [SinkPort],
// [IOPort], [ToolPort]). Used to enrich [MissingPatternError] with the port's
// name; a failed type assertion just omits the name rather than failing.
type namedPort interface {
	Name() string
}

func portName(port any) string {
	if np, ok := port.(namedPort); ok {
		return np.Name()
	}
	return ""
}

// RegisterREST replays port's declared [RESTPattern] against b, adding it to
// b's OpenAPI document. Call after the port has already been declared and
// bound to an adapter — this builds the spec *from* the binding, rather than
// requiring the route to be declared and registered separately beforehand.
//
// Returns [MissingPatternError] if the port declared no [RESTPattern].
func RegisterREST[Req, Resp any](b *rest.Builder, port any) error {
	ph, ok := port.(patternHolder)
	if !ok {
		return MissingPatternError{Port: portName(port), Kind: patternKindREST}
	}
	v, ok := ph.patternSpec(patternKindREST)
	if !ok {
		return MissingPatternError{Port: portName(port), Kind: patternKindREST}
	}
	route, ok := v.(rest.Route[Req, Resp])
	if !ok {
		return MissingPatternError{Port: portName(port), Kind: patternKindREST}
	}
	_, err := route.Register(b)
	return err
}

// RegisterSSE replays a [SinkPort]'s declared [RESTPattern] (SSE shape)
// against b, adding the SSE route to b's OpenAPI document. Call after the
// port has already been declared and bound to an adapter.
//
// Returns [MissingPatternError] if the port declared no [RESTPattern] (or the
// pattern was built for a different port role/type), or the underlying
// rest error if b rejects the route.
func RegisterSSE[Event any](b *rest.Builder, port any) error {
	ph, ok := port.(patternHolder)
	if !ok {
		return MissingPatternError{Port: portName(port), Kind: patternKindREST}
	}
	v, ok := ph.patternSpec(patternKindREST)
	if !ok {
		return MissingPatternError{Port: portName(port), Kind: patternKindREST}
	}
	route, ok := v.(rest.SSERoute[struct{}, Event])
	if !ok {
		return MissingPatternError{Port: portName(port), Kind: patternKindREST}
	}
	_, err := route.Register(b)
	return err
}

// RegisterEvent replays port's declared [EventPattern] against b, adding it to
// b's AsyncAPI document. Call after the port has already been declared and
// bound to an adapter.
//
// Returns [MissingPatternError] if the port declared no [EventPattern].
func RegisterEvent[T any](b *events.Builder, port any) error {
	ph, ok := port.(patternHolder)
	if !ok {
		return MissingPatternError{Port: portName(port), Kind: patternKindEvent}
	}
	v, ok := ph.patternSpec(patternKindEvent)
	if !ok {
		return MissingPatternError{Port: portName(port), Kind: patternKindEvent}
	}
	channel, ok := v.(events.Channel[T])
	if !ok {
		return MissingPatternError{Port: portName(port), Kind: patternKindEvent}
	}
	_, err := channel.Register(b)
	return err
}

// RegisterReqReply replays port's declared [ReqReplyPattern] against b,
// adding it to b's AsyncAPI document. Call after the port has already been
// declared and bound to an adapter.
//
// Returns [MissingPatternError] if the port declared no [ReqReplyPattern].
func RegisterReqReply[Req, Resp any](b *reqreply.Builder, port any) error {
	ph, ok := port.(patternHolder)
	if !ok {
		return MissingPatternError{Port: portName(port), Kind: patternKindReqReply}
	}
	v, ok := ph.patternSpec(patternKindReqReply)
	if !ok {
		return MissingPatternError{Port: portName(port), Kind: patternKindReqReply}
	}
	route, ok := v.(reqreply.Route[Req, Resp])
	if !ok {
		return MissingPatternError{Port: portName(port), Kind: patternKindReqReply}
	}
	_, err := route.Register(b)
	return err
}

// RegisterMCP replays port's declared [MCPPattern] against b, adding it to
// b's MCP spec document. Call after the port has already been declared and
// bound to an adapter.
//
// Returns [MissingPatternError] if the port declared no [MCPPattern].
func RegisterMCP[In, Out any](b *apimcp.Builder, port any) error {
	ph, ok := port.(patternHolder)
	if !ok {
		return MissingPatternError{Port: portName(port), Kind: patternKindMCP}
	}
	v, ok := ph.patternSpec(patternKindMCP)
	if !ok {
		return MissingPatternError{Port: portName(port), Kind: patternKindMCP}
	}
	tool, ok := v.(apimcp.Tool[In, Out])
	if !ok {
		return MissingPatternError{Port: portName(port), Kind: patternKindMCP}
	}
	_, err := tool.Register(b)
	return err
}
