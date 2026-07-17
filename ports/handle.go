package ports

import (
	"fmt"

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
	patternKindCache    = "cache"
	patternKindSocket   = "socket"
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

// SSEHandle returns the [rest.SSERouteHandle] built from a [SinkPort]'s
// declared [RESTPattern] (SSE shape: events are the port's payload, requests
// carry no body), or (nil, false) if the port declared no [RESTPattern].
func SSEHandle[Event any](port any) (*rest.SSERouteHandle[struct{}, Event], bool) {
	ph, ok := port.(patternHolder)
	if !ok {
		return nil, false
	}
	v, ok := ph.patternHandle(patternKindREST)
	if !ok {
		return nil, false
	}
	h, ok := v.(*rest.SSERouteHandle[struct{}, Event])
	return h, ok
}

// CacheHandle returns the [Cache] built from port's declared [CachePattern],
// or (zero, false) if the port declared no [CachePattern]. On a [SinkPort]
// or [LatestPort], T is the port's value type; on an [IOPort], T is the
// port's response type (the cached value is the port's response).
func CacheHandle[T any](port any) (Cache[T], bool) {
	ph, ok := port.(patternHolder)
	if !ok {
		return Cache[T]{}, false
	}
	v, ok := ph.patternHandle(patternKindCache)
	if !ok {
		return Cache[T]{}, false
	}
	h, ok := v.(Cache[T])
	return h, ok
}

// SocketHandle returns the [Socket] built from port's declared
// [SocketPattern], or (zero, false) if the port declared no [SocketPattern].
// On a [DuplexPort], In/Out are the port's codec pair; on a [SourcePort]
// use Socket[T, struct{}], on a [SinkPort] Socket[struct{}, T].
func SocketHandle[In, Out any](port any) (Socket[In, Out], bool) {
	ph, ok := port.(patternHolder)
	if !ok {
		return Socket[In, Out]{}, false
	}
	v, ok := ph.patternHandle(patternKindSocket)
	if !ok {
		return Socket[In, Out]{}, false
	}
	h, ok := v.(Socket[In, Out])
	return h, ok
}

// buildSocket constructs a Socket handle: the upgrade-validation route (GET,
// empty req/resp codecs — only path/header/security opts matter) plus the
// frame formats.
func buildSocket[In, Out any](
	portName string,
	pat SocketPattern,
	inCodec codex.Codec[In],
	outCodec codex.Codec[Out],
	restBuilder *rest.Builder,
) (Socket[In, Out], error) {
	b := restBuilder
	if b == nil {
		b = rest.NewBuilder(rest.Info{})
	}
	route := rest.NewRoute[struct{}, struct{}]("GET", pat.Path,
		codex.Struct[struct{}](), codex.Struct[struct{}](), pat.Opts...)
	handle, err := route.Register(b)
	if err != nil {
		return Socket[In, Out]{}, PatternRegisterError{Port: portName, Kind: patternKindSocket, Err: err}
	}
	inFmt, err := resolveFormat(portName, patternKindSocket, pat.Format, pat.CustomFormat, inCodec)
	if err != nil {
		return Socket[In, Out]{}, err
	}
	outFmt, err := resolveFormat(portName, patternKindSocket, pat.Format, pat.CustomFormat, outCodec)
	if err != nil {
		return Socket[In, Out]{}, err
	}
	return Socket[In, Out]{
		Path:         pat.Path,
		Subprotocols: pat.Subprotocols,
		Route:        handle,
		InFormat:     inFmt,
		OutFormat:    outFmt,
	}, nil
}

// buildDuplexPatternHandles scans patterns for a [DuplexPort] — currently
// [SocketPattern] is the only pattern a duplex port accepts; any other
// declared pattern kind is rejected (a duplex boundary is not HTTP-, topic-,
// file-, SQL-, or cache-shaped).
func buildDuplexPatternHandles[In, Out any](
	portName string,
	patterns []Pattern,
	inCodec codex.Codec[In],
	outCodec codex.Codec[Out],
	restBuilder *rest.Builder,
) (handles map[string]any, specs map[string]any, err error) {
	handles = make(map[string]any, len(patterns))
	specs = make(map[string]any, len(patterns))
	for _, p := range patterns {
		switch pat := p.(type) {
		case SocketPattern:
			s, err := buildSocket(portName, pat, inCodec, outCodec, restBuilder)
			if err != nil {
				return nil, nil, err
			}
			handles[patternKindSocket] = s
			specs[patternKindSocket] = pat
		default:
			return nil, nil, PatternRegisterError{
				Port: portName, Kind: patternKindSocket,
				Err: fmt.Errorf("DuplexPort accepts only SocketPattern; got %T", p),
			}
		}
	}
	return handles, specs, nil
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

// resolveFormat is the CustomFormat-aware counterpart of fileFormatFor,
// used by [FilePattern], [CachePattern], and [SocketPattern] builds.
// customFormat, when non-nil, must hold a format.Format[T] value matching
// T — a mismatch returns [PatternRegisterError]. The struct{} side of a
// one-directional [SocketPattern] (Socket[T,struct{}]/Socket[struct{},T])
// is exempt: customFormat is never asserted against T == struct{}, since
// that side carries no real frames — it always gets the JSON default,
// which is built but never used.
func resolveFormat[T any](portName, kind string, fileKind FileFormatKind, customFormat any, codec codex.Codec[T]) (format.Format[T], error) {
	if customFormat != nil {
		if _, isUnused := any(*new(T)).(struct{}); !isUnused {
			f, ok := customFormat.(format.Format[T])
			if !ok {
				return format.Format[T]{}, PatternRegisterError{
					Port: portName, Kind: kind,
					Err: fmt.Errorf("CustomFormat: want format.Format[%T], got %T", *new(T), customFormat),
				}
			}
			return f, nil
		}
	}
	return fileFormatFor(fileKind, codec), nil
}

// portRole distinguishes which single-codec port type a pattern is built for
// — the same [RESTPattern] builds different handle shapes per role (ingest
// route on a source, SSE route on a sink).
type portRole int

const (
	roleSource portRole = iota
	roleSink
)

// buildEventPatternHandles scans patterns for an [EventPattern] and builds a
// *events.ChannelHandle[T] via [events.Channel.Register] — the SAME call a
// hand-declared channel makes. Used by [SourcePort] (subscribe) and [SinkPort]
// (publish) construction — both are single-codec ports, matching EventPattern's
// single payload type. It also handles [RESTPattern] (role-dependent: HTTP
// ingest rest.Route[T, struct{}] on a source, SSE rest.SSERoute[struct{}, T]
// on a sink), [FilePattern] (building a format.File[T] from the port's codec,
// infallible), and [SQLPattern] (metadata-only, stored for [SQLMeta] /
// [WithSQLMeta] propagation).
//
// eventBuilder/restBuilder are used when non-nil (giving the handle full
// parity with a hand-registered channel/route: security schemes, global
// security, topic/path constraints, shared spec accumulation). When nil, a
// private, single-use Builder is created for that one Register call — the
// same zero-ceremony default as before, through the identical Register code
// path (there is no separate, weaker construction path — see
// [PortOptions.EventBuilder]/[PortOptions.RESTBuilder]).
//
// Returns both the built handles (for [EventHandle]/[RESTHandle]/[SSEHandle])
// and the original spec values (for [RegisterEvent]/[RegisterREST]/
// [RegisterSSE] to later replay against a different real Builder).
func buildEventPatternHandles[T any](
	portName string,
	patterns []Pattern,
	codec codex.Codec[T],
	role portRole,
	builder *events.Builder,
	restBuilder *rest.Builder,
) (handles map[string]any, specs map[string]any, err error) {
	handles = make(map[string]any, len(patterns))
	specs = make(map[string]any, len(patterns))
	for _, p := range patterns {
		switch pat := p.(type) {
		case RESTPattern:
			b := restBuilder
			if b == nil {
				b = rest.NewBuilder(rest.Info{})
			}
			switch role {
			case roleSource:
				// HTTP ingest: request body is the port's payload; the
				// response is empty (200 with empty body; the adapter maps a
				// full buffer to 503 + PipelineFullError).
				route := rest.NewRoute[T, struct{}](pat.Method, pat.Path, codec, codex.Struct[struct{}](), pat.Opts...)
				handle, err := route.Register(b)
				if err != nil {
					return nil, nil, PatternRegisterError{Port: portName, Kind: patternKindREST, Err: err}
				}
				handles[patternKindREST] = handle
				specs[patternKindREST] = route
			case roleSink:
				// SSE: events are the port's payload. SSE routes are always
				// GET (rest.NewSSERoute hardcodes it) — reject any other
				// declared method instead of silently ignoring it.
				if pat.Method != "" && pat.Method != "GET" {
					return nil, nil, PatternRegisterError{
						Port: portName, Kind: patternKindREST,
						Err: fmt.Errorf("RESTPattern on a SinkPort declares an SSE route, which is always GET; got method %q", pat.Method),
					}
				}
				route := rest.NewSSERoute[struct{}, T](pat.Path, codex.Struct[struct{}](), codec, pat.Opts...)
				handle, err := route.Register(b)
				if err != nil {
					return nil, nil, PatternRegisterError{Port: portName, Kind: patternKindREST, Err: err}
				}
				handles[patternKindREST] = handle
				specs[patternKindREST] = route
			}
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
			fFmt, err := resolveFormat(portName, patternKindFile, pat.Format, pat.CustomFormat, codec)
			if err != nil {
				return nil, nil, err
			}
			f := format.NewFile(pat.Path, fFmt, pat.Opts...)
			handles[patternKindFile] = f
			specs[patternKindFile] = f
		case SQLPattern:
			// Metadata-only: no handle to build, no spec document. Stored for
			// [SQLMeta] and propagated to adapters via [WithSQLMeta] at Bind.
			handles[patternKindSQL] = pat
			specs[patternKindSQL] = pat
		case CachePattern:
			// A cache does not produce a stream — reject on sources.
			if role == roleSource {
				return nil, nil, PatternRegisterError{
					Port: portName, Kind: patternKindCache,
					Err: fmt.Errorf("CachePattern is not supported on a SourcePort — a cache does not produce a stream"),
				}
			}
			// Write-through sink: the cached value is the port's payload.
			cFmt, err := resolveFormat(portName, patternKindCache, pat.Format, pat.CustomFormat, codec)
			if err != nil {
				return nil, nil, err
			}
			c := Cache[T]{Key: pat.Key, TTL: pat.TTL, Format: cFmt}
			handles[patternKindCache] = c
			specs[patternKindCache] = pat
		case SocketPattern:
			switch role {
			case roleSource:
				// Inbound-only socket: clients send T frames.
				s, err := buildSocket[T, struct{}](portName, pat, codec, codex.Struct[struct{}](), restBuilder)
				if err != nil {
					return nil, nil, err
				}
				handles[patternKindSocket] = s
				specs[patternKindSocket] = pat
			case roleSink:
				// Broadcast-only socket: server pushes T frames (WS sibling of SSE).
				s, err := buildSocket[struct{}, T](portName, pat, codex.Struct[struct{}](), codec, restBuilder)
				if err != nil {
					return nil, nil, err
				}
				handles[patternKindSocket] = s
				specs[patternKindSocket] = pat
			}
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
	cacheAllowed bool,
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
			// retrieval reads a format.File[Resp].
			fFmt, err := resolveFormat(portName, patternKindFile, pat.Format, pat.CustomFormat, respCodec)
			if err != nil {
				return nil, nil, err
			}
			f := format.NewFile(pat.Path, fFmt, pat.Opts...)
			handles[patternKindFile] = f
			specs[patternKindFile] = f
		case SQLPattern:
			// Metadata-only — see buildEventPatternHandles.
			handles[patternKindSQL] = pat
			specs[patternKindSQL] = pat
		case CachePattern:
			// Accepted on IOPort/LatestPort; rejected on ToolPort — a cache
			// is not a tool surface.
			if !cacheAllowed {
				return nil, nil, PatternRegisterError{
					Port: portName, Kind: patternKindCache,
					Err: fmt.Errorf("CachePattern is not supported on a ToolPort — a cache is not a tool surface"),
				}
			}
			// The cached value is the port's RESPONSE type — mirrors FilePattern.
			cFmt, err := resolveFormat(portName, patternKindCache, pat.Format, pat.CustomFormat, respCodec)
			if err != nil {
				return nil, nil, err
			}
			c := Cache[Resp]{Key: pat.Key, TTL: pat.TTL, Format: cFmt}
			handles[patternKindCache] = c
			specs[patternKindCache] = pat
		case SocketPattern:
			// Sockets carry uncorrelated frame streams — per-message
			// request/reply is ReqReplyPattern territory. Rejected on
			// IOPort, LatestPort, and ToolPort.
			return nil, nil, PatternRegisterError{
				Port: portName, Kind: patternKindSocket,
				Err: fmt.Errorf("SocketPattern is only supported on SourcePort, SinkPort, and DuplexPort"),
			}
		}
	}
	return handles, specs, nil
}
