package ports

import (
	"github.com/DaniDeer/go-codex/api/events"
	apimcp "github.com/DaniDeer/go-codex/api/mcp"
	"github.com/DaniDeer/go-codex/api/reqreply"
	"github.com/DaniDeer/go-codex/api/rest"
	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/format"
)

// pattern kind keys — internal bookkeeping for the handles map.
const (
	patternKindREST     = "rest"
	patternKindEvent    = "event"
	patternKindReqReply = "reqreply"
	patternKindMCP      = "mcp"
	patternKindFile     = "file"
	patternKindSQL      = "sql"
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

// FileHandle returns the [format.File] built from port's declared
// [FilePattern], or (zero, false) if the port declared no [FilePattern].
// On a [SinkPort], T is the port's payload type; on an [IOPort], T is the
// port's response type (the file's content is the port's response).
func FileHandle[T any](port any) (format.File[T], bool) {
	ph, ok := port.(patternHolder)
	if !ok {
		return format.File[T]{}, false
	}
	v, ok := ph.patternHandle(patternKindFile)
	if !ok {
		return format.File[T]{}, false
	}
	h, ok := v.(format.File[T])
	return h, ok
}

// SQLMeta returns the [SQLPattern] metadata declared on port, or
// (zero, false) if the port declared no [SQLPattern].
func SQLMeta(port any) (SQLPattern, bool) {
	ph, ok := port.(patternHolder)
	if !ok {
		return SQLPattern{}, false
	}
	v, ok := ph.patternHandle(patternKindSQL)
	if !ok {
		return SQLPattern{}, false
	}
	m, ok := v.(SQLPattern)
	return m, ok
}

// fileFormatFor maps a [FileFormatKind] to a concrete [format.Format] built
// from the port's codec. JSON is the default (zero value).
func fileFormatFor[T any](kind FileFormatKind, codec codex.Codec[T]) format.Format[T] {
	switch kind {
	case FileFormatYAML:
		return format.YAML(codec)
	case FileFormatTOML:
		return format.TOML(codec)
	default:
		return format.JSON(codec)
	}
}

// buildEventPatternHandles scans patterns for an [EventPattern] and builds a
// *events.ChannelHandle[T] via [events.Channel.Register] — the SAME call a
// hand-declared channel makes. Used by [SourcePort] (subscribe) and [SinkPort]
// (publish) construction — both are single-codec ports, matching EventPattern's
// single payload type. It also handles [FilePattern] (building a
// format.File[T] from the port's codec, infallible) and [SQLPattern]
// (metadata-only, stored for [SQLMeta] / [WithSQLMeta] propagation).
//
// builder is used when non-nil (giving the handle full parity with a
// hand-registered channel: security schemes, global security, topic
// constraints, shared spec accumulation). When nil, a private, single-use
// *events.Builder is created for this one Register call — the same
// zero-ceremony default as before, through the identical Register code path
// (there is no separate, weaker construction path — see [PortOptions.EventBuilder]).
//
// Returns both the built handles (for [EventHandle]) and the original
// [events.Channel] spec values (for [RegisterEvent] to later replay against a
// different real [events.Builder]).
func buildEventPatternHandles[T any](
	portName string,
	patterns []Pattern,
	codec codex.Codec[T],
	builder *events.Builder,
) (handles map[string]any, specs map[string]any, err error) {
	handles = make(map[string]any, len(patterns))
	specs = make(map[string]any, len(patterns))
	for _, p := range patterns {
		switch pat := p.(type) {
		case EventPattern:
			channel := events.NewChannel[T](pat.Topic, codec, pat.Opts...)
			b := builder
			if b == nil {
				b = events.NewBuilder(events.Info{})
			}
			handle, err := channel.Register(b)
			if err != nil {
				return nil, nil, PatternRegisterError{Port: portName, Kind: patternKindEvent, Err: err}
			}
			handles[patternKindEvent] = handle
			specs[patternKindEvent] = channel
		case FilePattern:
			// format.NewFile is infallible — spec-capture only.
			f := format.NewFile(pat.Path, fileFormatFor(pat.Format, codec), pat.Opts...)
			handles[patternKindFile] = f
			specs[patternKindFile] = f
		case SQLPattern:
			// Metadata-only: no handle to build, no spec document. Stored for
			// [SQLMeta] and propagated to adapters via [WithSQLMeta] at Bind.
			handles[patternKindSQL] = pat
			specs[patternKindSQL] = pat
		}
	}
	return handles, specs, nil
}

// buildDualCodecPatternHandles scans patterns for a [RESTPattern],
// [ReqReplyPattern], and [MCPPattern] and builds the corresponding handle for
// each found via Register — the SAME call a hand-declared route/tool makes.
// Used by [IOPort] (client call) and [ToolPort] (server pipeline) construction
// — both are dual-codec ports. It also handles [FilePattern] (building a
// format.File[Resp] from the port's RESPONSE codec, infallible) and
// [SQLPattern] (metadata-only).
//
// restBuilder/reqReplyBuilder/mcpBuilder are used when non-nil (full parity
// with a hand-registered route/tool); when nil, a private, single-use Builder
// is created for that one Register call — see [buildEventPatternHandles] and
// [PortOptions.RESTBuilder]/[PortOptions.ReqReplyBuilder]/[PortOptions.MCPBuilder].
//
// Returns both the built handles (for [RESTHandle]/[ReqReplyHandle]/[MCPHandle])
// and the original rest.Route/reqreply.Route/apimcp.Tool spec values (for
// [RegisterREST]/[RegisterReqReply]/[RegisterMCP] to later replay against a
// different real Builder).
func buildDualCodecPatternHandles[Req, Resp any](
	portName string,
	patterns []Pattern,
	reqCodec codex.Codec[Req],
	respCodec codex.Codec[Resp],
	restBuilder *rest.Builder,
	reqReplyBuilder *reqreply.Builder,
	mcpBuilder *apimcp.Builder,
) (handles map[string]any, specs map[string]any, err error) {
	handles = make(map[string]any, len(patterns))
	specs = make(map[string]any, len(patterns))
	for _, p := range patterns {
		switch pat := p.(type) {
		case RESTPattern:
			route := rest.NewRoute[Req, Resp](pat.Method, pat.Path, reqCodec, respCodec, pat.Opts...)
			b := restBuilder
			if b == nil {
				b = rest.NewBuilder(rest.Info{})
			}
			handle, err := route.Register(b)
			if err != nil {
				return nil, nil, PatternRegisterError{Port: portName, Kind: patternKindREST, Err: err}
			}
			handles[patternKindREST] = handle
			specs[patternKindREST] = route
		case ReqReplyPattern:
			route := reqreply.NewRoute[Req, Resp](pat.Topic, reqCodec, respCodec, pat.Opts...)
			b := reqReplyBuilder
			if b == nil {
				b = reqreply.NewBuilder(reqreply.Info{})
			}
			handle, err := route.Register(b)
			if err != nil {
				return nil, nil, PatternRegisterError{Port: portName, Kind: patternKindReqReply, Err: err}
			}
			handles[patternKindReqReply] = handle
			specs[patternKindReqReply] = route
		case MCPPattern:
			tool := apimcp.NewTool[Req, Resp](pat.Name, reqCodec, respCodec, pat.Opts...)
			b := mcpBuilder
			if b == nil {
				b = apimcp.NewBuilder(apimcp.Info{})
			}
			handle, err := tool.Register(b)
			if err != nil {
				return nil, nil, PatternRegisterError{Port: portName, Kind: patternKindMCP, Err: err}
			}
			handles[patternKindMCP] = handle
			specs[patternKindMCP] = tool
		case FilePattern:
			// The file's content is the port's RESPONSE type — a per-item
			// retrieval reads a format.File[Resp]. format.NewFile is infallible.
			f := format.NewFile(pat.Path, fileFormatFor(pat.Format, respCodec), pat.Opts...)
			handles[patternKindFile] = f
			specs[patternKindFile] = f
		case SQLPattern:
			// Metadata-only — see buildEventPatternHandles.
			handles[patternKindSQL] = pat
			specs[patternKindSQL] = pat
		}
	}
	return handles, specs, nil
}
