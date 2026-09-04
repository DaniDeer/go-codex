package ports

import (
	"fmt"

	"github.com/DaniDeer/go-codex/api/events"
	"github.com/DaniDeer/go-codex/api/llm"
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
	patternKindLLM      = "llm"
)

// patternHolder is implemented by every port type that supports [Pattern]
// declarations. It is unexported — callers never implement or reference it
// directly. Only [SQLMeta] (metadata query, not a handle — SQLPattern
// builds no handle to return synchronously from a Plugin call) and
// [RegisterREST]/[RegisterEvent]/[RegisterReqReply]/[RegisterMCP] (replay a
// declared spec against a different builder) still use it — the direct
// handle-lookup family (RESTHandle/EventHandle/etc.) that used to live here
// is superseded by each port's PluginXxxPattern methods, which return the
// handle directly at the point of registration; there is no later "does
// this port have a Handle of kind X?" question left to ask.
type patternHolder interface {
	patternHandle(kind string) (any, bool)
	patternSpec(kind string) (any, bool)
}

// socketHandleFor is the unexported counterpart of the removed public
// SocketHandle — [RegisterSocket] is the one remaining internal caller that
// needs the actual built [Socket] (InFormat/OutFormat/Path), not just the
// original [SocketPattern] spec [patternSpec] would give it.
func socketHandleFor[In, Out any](port any) (Socket[In, Out], bool) {
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
	restBuilder *rest.Server,
) (Socket[In, Out], error) {
	var sb socketMergeBuilder
	for _, opt := range pat.InOpts {
		opt.applySocketIn(&sb)
	}
	for _, opt := range pat.OutOpts {
		opt.applySocketOut(&sb)
	}
	inMergeFields, err := assertSocketMergeFields[In](sb.inMergeFieldsRaw)
	if err != nil {
		return Socket[In, Out]{}, PatternRegisterError{Port: portName, Kind: patternKindSocket, Err: err}
	}
	outMergeFields, err := assertSocketMergeFields[Out](sb.outMergeFieldsRaw)
	if err != nil {
		return Socket[In, Out]{}, PatternRegisterError{Port: portName, Kind: patternKindSocket, Err: err}
	}

	b := restBuilder
	if b == nil {
		b = rest.NewServer(rest.Info{})
	}
	route := rest.NewRoute[struct{}, struct{}]("GET", pat.Path,
		codex.Struct[struct{}](), codex.Struct[struct{}](), pat.Opts...)
	handle, err := route.RegisterHandle(b)
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
		Path:           pat.Path,
		Subprotocols:   pat.Subprotocols,
		Route:          handle,
		InFormat:       inFmt,
		OutFormat:      outFmt,
		inMergeFields:  inMergeFields,
		outMergeFields: outMergeFields,
	}, nil
}

func assertSocketMergeFields[T any](raw []any) ([]codex.FieldCodec[T], error) {
	out := make([]codex.FieldCodec[T], 0, len(raw))
	for i := range raw {
		f, ok := raw[i].(codex.FieldCodec[T])
		if !ok {
			return nil, fmt.Errorf("socket merge field[%d]: want codex.FieldCodec[%T], got %T", i, *new(T), raw[i])
		}
		out = append(out, f)
	}
	return out, nil
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
	restBuilder *rest.Server,
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
// *events.ChannelHandle[T] via [events.Channel.WithSubscribe]/[events.Channel.WithPublish]
// followed by [events.Subscriber.Handle]/[events.Publisher.Handle] — the SAME
// calls a hand-declared channel makes. Used by [SourcePort] (subscribe) and
// [SinkPort] (publish) construction — both are single-codec ports, matching
// EventPattern's single payload type. It also handles [RESTPattern]
// (role-dependent: HTTP ingest rest.Route[T, struct{}] on a source, SSE
// rest.SSERoute[struct{}, T] on a sink), [FilePattern] (building a File[T]
// from the port's codec — infallible on the enum-only path; a declared
// CustomFormat type mismatch returns [PatternRegisterError]), and
// [SQLPattern] (metadata-only, stored for [SQLMeta] / [WithSQLMeta]
// propagation).
//
// client is passed straight through to Handle — nil already builds a valid,
// spec-free handle there (see [events.Subscriber.Handle]), so no private
// fallback client is needed. restBuilder, by contrast, is used when non-nil
// and otherwise replaced with a private, single-use Builder for that one
// Register call — the same zero-ceremony default as before, through the
// identical construction code path (see [PortOptions.EventClient]/
// [PortOptions.RESTBuilder]).
//
// Returns both the built handles (for [EventHandle]/[RESTHandle]/[SSEHandle])
// and the original spec values (for [RegisterEvent]/[RegisterREST]/
// [RegisterSSE] to later replay against a different real Builder).
func buildEventPatternHandles[T any](
	portName string,
	patterns []Pattern,
	codec codex.Codec[T],
	role portRole,
	client *events.Client,
	restBuilder *rest.Server,
) (handles map[string]any, specs map[string]any, err error) {
	handles = make(map[string]any, len(patterns))
	specs = make(map[string]any, len(patterns))
	for _, p := range patterns {
		switch pat := p.(type) {
		case RESTPattern:
			b := restBuilder
			if b == nil {
				b = rest.NewServer(rest.Info{})
			}
			switch role {
			case roleSource:
				// HTTP ingest: request body is the port's payload; the
				// response is empty (200 with empty body; the adapter maps a
				// full buffer to 503 + PipelineFullError).
				route := rest.NewRoute[T, struct{}](pat.Method, pat.Path, codec, codex.Struct[struct{}](), pat.Opts...)
				handle, err := route.RegisterHandle(b)
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
				handle, err := route.RegisterHandle(b)
				if err != nil {
					return nil, nil, PatternRegisterError{Port: portName, Kind: patternKindREST, Err: err}
				}
				handles[patternKindREST] = handle
				specs[patternKindREST] = route
			}
		case EventPattern:
			channel := events.NewChannel[T](pat.Topic, codec, pat.Opts...)
			switch role {
			case roleSource:
				// Publish is a SinkPort-only field — declaring it here would
				// be silently discarded, which is exactly the kind of
				// silent-divergence gap this redesign eliminates elsewhere;
				// mirrors the RESTPattern-on-SinkPort non-GET rejection above.
				if pat.Publish != nil {
					return nil, nil, PatternRegisterError{
						Port: portName, Kind: patternKindEvent,
						Err: fmt.Errorf("EventPattern on a SourcePort declares Publish, which only takes effect on a SinkPort"),
					}
				}
				sub := events.Subscribe{}
				if pat.Subscribe != nil {
					sub = *pat.Subscribe
				}
				subscriber := channel.WithSubscribe(sub)
				handle, err := subscriber.Handle(client)
				if err != nil {
					return nil, nil, PatternRegisterError{Port: portName, Kind: patternKindEvent, Err: err}
				}
				handles[patternKindEvent] = handle
				specs[patternKindEvent] = subscriber
			case roleSink:
				if pat.Subscribe != nil {
					return nil, nil, PatternRegisterError{
						Port: portName, Kind: patternKindEvent,
						Err: fmt.Errorf("EventPattern on a SinkPort declares Subscribe, which only takes effect on a SourcePort"),
					}
				}
				pub := events.Publish{}
				if pat.Publish != nil {
					pub = *pat.Publish
				}
				publisher := channel.WithPublish(pub)
				handle, err := publisher.Handle(client)
				if err != nil {
					return nil, nil, PatternRegisterError{Port: portName, Kind: patternKindEvent, Err: err}
				}
				handles[patternKindEvent] = handle
				specs[patternKindEvent] = publisher
			}
		case FilePattern:
			fFmt, err := resolveFormat(portName, patternKindFile, pat.Format, pat.CustomFormat, codec)
			if err != nil {
				return nil, nil, err
			}
			f := NewFile(pat.Path, fFmt, pat.Opts...)
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
			// Delegate to NewCache (same constructor hand-built Cache[T]
			// values use) rather than reconstructing the struct field-by-field
			// — this is what correctly type-asserts pat.Opts' NewCacheKeyParam
			// merge fields into []codex.FieldCodec[T]; a prior version of this
			// build path only copied cb.params, silently dropping merge fields
			// registered via NewCacheKeyParam for every CachePattern-built Cache.
			c := NewCache[T](pat.Key, cFmt, pat.Opts...)
			c.TTL = pat.TTL
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
// File[Resp] from the port's RESPONSE codec — infallible on the
// enum-only path; a declared CustomFormat type mismatch returns
// [PatternRegisterError]) and [SQLPattern] (metadata-only).
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
	restBuilder *rest.Server,
	reqReplyBuilder *reqreply.Builder,
	mcpBuilder *apimcp.Builder,
	cacheAllowed bool,
	llmBuilder *llm.Builder,
	llmAllowed bool,
) (handles map[string]any, specs map[string]any, err error) {
	handles = make(map[string]any, len(patterns))
	specs = make(map[string]any, len(patterns))
	for _, p := range patterns {
		switch pat := p.(type) {
		case RESTPattern:
			route := rest.NewRoute[Req, Resp](pat.Method, pat.Path, reqCodec, respCodec, pat.Opts...)
			b := restBuilder
			if b == nil {
				b = rest.NewServer(rest.Info{})
			}
			handle, err := route.RegisterHandle(b)
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
			// retrieval reads a File[Resp].
			fFmt, err := resolveFormat(portName, patternKindFile, pat.Format, pat.CustomFormat, respCodec)
			if err != nil {
				return nil, nil, err
			}
			f := NewFile(pat.Path, fFmt, pat.Opts...)
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
			// Delegate to NewCache (same constructor hand-built Cache[Resp]
			// values use) rather than reconstructing the struct field-by-field
			// — see buildEventPatternHandles's CachePattern case for why.
			c := NewCache[Resp](pat.Key, cFmt, pat.Opts...)
			c.TTL = pat.TTL
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
		case LLMPattern:
			// An LLM completion is an outbound call the pipeline makes — the
			// same category as CachePattern/FilePattern being IOPort/
			// LatestPort-shaped, not a tool/event surface. Only IOPort
			// passes llmAllowed=true.
			if !llmAllowed {
				return nil, nil, PatternRegisterError{
					Port: portName, Kind: patternKindLLM,
					Err: fmt.Errorf("LLMPattern is only supported on IOPort — an LLM completion is an outbound call, not a request-serving surface"),
				}
			}
			call := llm.NewCall[Req, Resp](pat.Name, reqCodec, respCodec, pat.Opts...)
			b := llmBuilder
			if b == nil {
				b = llm.NewBuilder(llm.Info{})
			}
			handle, err := call.Register(b)
			if err != nil {
				return nil, nil, PatternRegisterError{Port: portName, Kind: patternKindLLM, Err: err}
			}
			handles[patternKindLLM] = handle
			specs[patternKindLLM] = call
		}
	}
	return handles, specs, nil
}
