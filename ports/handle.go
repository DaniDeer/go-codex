package ports

import (
	"github.com/DaniDeer/go-codex/api/events"
	apimcp "github.com/DaniDeer/go-codex/api/mcp"
	"github.com/DaniDeer/go-codex/api/reqreply"
	"github.com/DaniDeer/go-codex/api/rest"
	"github.com/DaniDeer/go-codex/codex"
)

// pattern kind keys — internal bookkeeping for the handles map.
const (
	patternKindREST     = "rest"
	patternKindEvent    = "event"
	patternKindReqReply = "reqreply"
	patternKindMCP      = "mcp"
)

// patternHolder is implemented by every port type that supports [Pattern]
// declarations. It is unexported — callers never implement or reference it
// directly; they just pass their port value to [RESTHandle], [EventHandle],
// [ReqReplyHandle], [MCPHandle], [RegisterREST], [RegisterEvent],
// [RegisterReqReply], or [RegisterMCP].
type patternHolder interface {
	patternHandle(kind string) (any, bool)
	patternSpec(kind string) (any, bool)
}

// RESTHandle returns the [rest.RouteHandle] built from port's declared
// [RESTPattern], or (nil, false) if the port declared no [RESTPattern].
func RESTHandle[Req, Resp any](port any) (*rest.RouteHandle[Req, Resp], bool) {
	ph, ok := port.(patternHolder)
	if !ok {
		return nil, false
	}
	v, ok := ph.patternHandle(patternKindREST)
	if !ok {
		return nil, false
	}
	h, ok := v.(*rest.RouteHandle[Req, Resp])
	return h, ok
}

// EventHandle returns the [events.ChannelHandle] built from port's declared
// [EventPattern], or (nil, false) if the port declared no [EventPattern].
func EventHandle[T any](port any) (*events.ChannelHandle[T], bool) {
	ph, ok := port.(patternHolder)
	if !ok {
		return nil, false
	}
	v, ok := ph.patternHandle(patternKindEvent)
	if !ok {
		return nil, false
	}
	h, ok := v.(*events.ChannelHandle[T])
	return h, ok
}

// ReqReplyHandle returns the [reqreply.RouteHandle] built from port's declared
// [ReqReplyPattern], or (nil, false) if the port declared no [ReqReplyPattern].
func ReqReplyHandle[Req, Resp any](port any) (*reqreply.RouteHandle[Req, Resp], bool) {
	ph, ok := port.(patternHolder)
	if !ok {
		return nil, false
	}
	v, ok := ph.patternHandle(patternKindReqReply)
	if !ok {
		return nil, false
	}
	h, ok := v.(*reqreply.RouteHandle[Req, Resp])
	return h, ok
}

// MCPHandle returns the [apimcp.ToolHandle] built from port's declared
// [MCPPattern], or (nil, false) if the port declared no [MCPPattern].
func MCPHandle[In, Out any](port any) (*apimcp.ToolHandle[In, Out], bool) {
	ph, ok := port.(patternHolder)
	if !ok {
		return nil, false
	}
	v, ok := ph.patternHandle(patternKindMCP)
	if !ok {
		return nil, false
	}
	h, ok := v.(*apimcp.ToolHandle[In, Out])
	return h, ok
}

// buildEventPatternHandles scans patterns for an [EventPattern] and builds a
// *events.ChannelHandle[T], builder-free, via [events.Channel.ClientHandle].
// Used by [SourcePort] (subscribe) and [SinkPort] (publish) construction —
// both are single-codec ports, matching EventPattern's single payload type.
//
// Returns both the built handles (for [EventHandle]) and the original
// [events.Channel] spec values (for [RegisterEvent] to later replay against a
// real [events.Builder]).
//
// Infallible — [events.Channel.ClientHandle] never errors — so
// [NewSourcePort]/[NewSinkPort] do not need an error-returning signature.
func buildEventPatternHandles[T any](patterns []Pattern, codec codex.Codec[T]) (handles map[string]any, specs map[string]any) {
	handles = make(map[string]any, len(patterns))
	specs = make(map[string]any, len(patterns))
	for _, p := range patterns {
		ep, ok := p.(EventPattern)
		if !ok {
			continue
		}
		channel := events.NewChannel[T](ep.Topic, codec, ep.Opts...)
		handles[patternKindEvent] = channel.ClientHandle()
		specs[patternKindEvent] = channel
	}
	return handles, specs
}

// buildDualCodecPatternHandles scans patterns for a [RESTPattern],
// [ReqReplyPattern], and [MCPPattern] and builds the corresponding handle for
// each found, builder-free. Used by [IOPort] (client call) and [ToolPort]
// (server pipeline) construction — both are dual-codec ports.
//
// Returns both the built handles (for [RESTHandle]/[ReqReplyHandle]/[MCPHandle])
// and the original rest.Route/reqreply.Route/apimcp.Tool spec values (for
// [RegisterREST]/[RegisterReqReply]/[RegisterMCP] to later replay against a
// real Builder).
func buildDualCodecPatternHandles[Req, Resp any](
	portName string,
	patterns []Pattern,
	reqCodec codex.Codec[Req],
	respCodec codex.Codec[Resp],
) (handles map[string]any, specs map[string]any, err error) {
	handles = make(map[string]any, len(patterns))
	specs = make(map[string]any, len(patterns))
	for _, p := range patterns {
		switch pat := p.(type) {
		case RESTPattern:
			route := rest.NewRoute[Req, Resp](pat.Method, pat.Path, reqCodec, respCodec, pat.Opts...)
			handles[patternKindREST] = route.ClientHandle()
			specs[patternKindREST] = route
		case ReqReplyPattern:
			route := reqreply.NewRoute[Req, Resp](pat.Topic, reqCodec, respCodec, pat.Opts...)
			handles[patternKindReqReply] = route.ClientHandle()
			specs[patternKindReqReply] = route
		case MCPPattern:
			tool := apimcp.NewTool[Req, Resp](pat.Name, reqCodec, respCodec, pat.Opts...)
			handle, err := tool.ClientHandle()
			if err != nil {
				return nil, nil, PatternRegisterError{Port: portName, Kind: patternKindMCP, Err: err}
			}
			handles[patternKindMCP] = handle
			specs[patternKindMCP] = tool
		}
	}
	return handles, specs, nil
}
