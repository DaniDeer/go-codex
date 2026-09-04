package ports

import (
	"strings"

	"github.com/DaniDeer/go-codex/api/events"
	"github.com/DaniDeer/go-codex/api/llm"
	apimcp "github.com/DaniDeer/go-codex/api/mcp"
	"github.com/DaniDeer/go-codex/api/reqreply"
	"github.com/DaniDeer/go-codex/api/rest"
	asyncapi "github.com/DaniDeer/go-codex/render/asyncapi/v3"
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
func RegisterREST[Req, Resp any](b *rest.Server, port any) error {
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
	return route.Register(b)
}

// RegisterSSE replays a [SinkPort]'s declared [RESTPattern] (SSE shape)
// against b, adding the SSE route to b's OpenAPI document. Call after the
// port has already been declared and bound to an adapter.
//
// Returns [MissingPatternError] if the port declared no [RESTPattern] (or the
// pattern was built for a different port role/type), or the underlying
// rest error if b rejects the route.
func RegisterSSE[Event any](b *rest.Server, port any) error {
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
	return route.Register(b)
}

// RegisterEvent replays port's declared [EventPattern] against b, adding it to
// b's AsyncAPI document. Call after the port has already been declared and
// bound to an adapter.
//
// Returns [MissingPatternError] if the port declared no [EventPattern].
func RegisterEvent[T any](b *events.Client, port any) error {
	ph, ok := port.(patternHolder)
	if !ok {
		return MissingPatternError{Port: portName(port), Kind: patternKindEvent}
	}
	v, ok := ph.patternSpec(patternKindEvent)
	if !ok {
		return MissingPatternError{Port: portName(port), Kind: patternKindEvent}
	}
	// The stored spec is role-scoped — a [SourcePort] stores an
	// [events.Subscriber[T]], a [SinkPort] an [events.Publisher[T]] (see
	// [buildEventPatternHandles]) — try both, matching whichever role the
	// port was built with.
	switch spec := v.(type) {
	case events.Subscriber[T]:
		_, err := spec.Handle(b)
		return err
	case events.Publisher[T]:
		_, err := spec.Handle(b)
		return err
	default:
		return MissingPatternError{Port: portName(port), Kind: patternKindEvent}
	}
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

// RegisterLLM replays port's declared [LLMPattern] against b, adding it to
// b's LLMSpec catalog. Call after the port has already been declared and
// bound to an adapter.
//
// Returns [MissingPatternError] if the port declared no [LLMPattern] (only
// [IOPort] can declare one).
func RegisterLLM[Req, Resp any](b *llm.Builder, port any) error {
	ph, ok := port.(patternHolder)
	if !ok {
		return MissingPatternError{Port: portName(port), Kind: patternKindLLM}
	}
	v, ok := ph.patternSpec(patternKindLLM)
	if !ok {
		return MissingPatternError{Port: portName(port), Kind: patternKindLLM}
	}
	call, ok := v.(llm.Call[Req, Resp])
	if !ok {
		return MissingPatternError{Port: portName(port), Kind: patternKindLLM}
	}
	_, err := call.Register(b)
	return err
}

// RegisterSocket replays port's declared [SocketPattern] against b as an
// AsyncAPI channel — the WebSocket spec story (OpenAPI cannot express
// socket frames). The channel name is the socket path template; the {var}
// placeholders become channel parameters.
//
// Frame direction mapping (application perspective, matching the asyncapi
// renderer's convention):
//
//   - Subscribe operation — frames the application RECEIVES: the port's In
//     type (clients send these).
//   - Publish operation — frames the application SENDS: the port's Out
//     type (targeted replies / broadcasts).
//
// One-directional ports emit only their live direction: a [SourcePort]
// (In only) produces just the Subscribe operation, a [SinkPort] (Out only)
// just the Publish operation — pass struct{} for the unused side, exactly
// as with [SocketHandle]. Declare an events server with Protocol "ws" on b
// for a complete document.
//
// Returns [MissingPatternError] if the port declared no [SocketPattern].
func RegisterSocket[In, Out any](b *events.Client, port any) error {
	handle, ok := socketHandleFor[In, Out](port)
	if !ok {
		return MissingPatternError{Port: portName(port), Kind: patternKindSocket}
	}

	item := asyncapi.ChannelItem{
		Description: "WebSocket endpoint (duplex frame stream)",
	}
	vars := templateVars(handle.Path)
	if len(vars) > 0 {
		item.Parameters = make(map[string]asyncapi.Parameter, len(vars))
		for _, name := range vars {
			item.Parameters[name] = asyncapi.Parameter{
				Description: "socket path variable {" + name + "}",
			}
		}
	}
	// In frames: application receives (clients send) — Subscribe op.
	if _, isEmpty := any(*new(In)).(struct{}); !isEmpty {
		item.Subscribe = &asyncapi.Operation{
			Summary: "Inbound socket frames",
			Message: asyncapi.Message{Schema: handle.InFormat.Schema()},
		}
	}
	// Out frames: application sends — Publish op.
	if _, isEmpty := any(*new(Out)).(struct{}); !isEmpty {
		item.Publish = &asyncapi.Operation{
			Summary: "Outbound socket frames",
			Message: asyncapi.Message{Schema: handle.OutFormat.Schema()},
		}
	}
	b.AddChannelItem(handle.Path, item)
	return nil
}

// templateVars extracts the {var} placeholder names from a path template.
func templateVars(path string) []string {
	var names []string
	for {
		start := strings.IndexByte(path, '{')
		if start < 0 {
			return names
		}
		end := strings.IndexByte(path[start:], '}')
		if end < 0 {
			return names
		}
		names = append(names, path[start+1:start+end])
		path = path[start+end+1:]
	}
}
