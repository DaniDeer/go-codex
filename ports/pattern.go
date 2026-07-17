package ports

import (
	"strings"
	"time"

	"github.com/DaniDeer/go-codex/api/events"
	apimcp "github.com/DaniDeer/go-codex/api/mcp"
	"github.com/DaniDeer/go-codex/api/reqreply"
	"github.com/DaniDeer/go-codex/api/rest"
	"github.com/DaniDeer/go-codex/format"
)

// Pattern is the sealed interface for a port's declared communication pattern.
// A port declares one Pattern entry per protocol family it will be bound to —
// a [ToolPort] exposed over both HTTP and MQTT 5 declares both a [RESTPattern]
// and a [ReqReplyPattern].
//
// The following types implement Pattern:
//   - [RESTPattern] — HTTP-shaped (nethttp, chi)
//   - [EventPattern] — topic-shaped pub/sub (mqtt, mqtt5, zeromq)
//   - [ReqReplyPattern] — topic-shaped request/reply (mqtt5, zeromq)
//   - [MCPPattern] — MCP tool (mcpgo)
//   - [FilePattern] — typed-file-shaped (file)
//   - [SQLPattern] — SQL metadata (sql)
//   - [CachePattern] — key/value-cache-shaped (redis)
//   - [SocketPattern] — path-addressed duplex socket (websocket)
//
// Each Pattern is a thin wrapper around the existing rest/events/reqreply/mcp/
// format declarative option vocabulary — no new param types are introduced. A
// port builds its own handle from the Pattern at construction time via
// Route/Channel/Tool.Register (against the matching [PortOptions] builder
// field, or a private single-use builder when nil), retrievable with
// [RESTHandle], [EventHandle], [ReqReplyHandle], [MCPHandle], and
// [FileHandle]. [SQLPattern] builds no handle — its metadata is retrievable
// with [SQLMeta] and propagated to adapters via [WithSQLMeta].
type Pattern interface{ isPortPattern() }

// RESTPattern declares an HTTP-shaped communication pattern for a port bound
// to a REST/HTTP adapter (nethttp, chi). Method and Path mirror [rest.NewRoute]'s
// first two arguments; Opts accepts the same option vocabulary
// ([rest.RouteMeta], [rest.PathParam], [rest.QueryParam], [rest.HeaderParam],
// [rest.CookieParam], [rest.ResponseMeta], [rest.ResponseHeaderParam],
// [rest.ResponseCookieParam]).
//
// The port type determines the handle shape built from the pattern:
//
//   - [IOPort]/[ToolPort]: rest.RouteHandle[Req, Resp] from the port's codec
//     pair — retrieve with [RESTHandle].
//
//   - [LatestPort]: rest.RouteHandle[struct{}, T] (GET, cached response) —
//     retrieve with [RESTHandle].
//
//   - [SourcePort]: HTTP ingest — rest.RouteHandle[T, struct{}] (request body
//     is the payload, empty response; pairs with nethttp/chi.IngestAdapter) —
//     retrieve with [RESTHandle].
//
//   - [SinkPort]: SSE — rest.SSERouteHandle[struct{}, T] (events are the
//     payload; always GET — any other Method fails construction; pairs with
//     nethttp/chi.SSEAdapter) — retrieve with [SSEHandle].
//
//     ports.RESTPattern{
//     Method: "POST",
//     Path:   "/sensors/{sensorID}/data",
//     Opts: []rest.RouteOpt{
//     rest.RouteMeta{OperationID: "ingestSensorData"},
//     rest.PathParam{Name: "sensorID"}.WithCodec(sensorIDCodec),
//     },
//     }
type RESTPattern struct {
	// Method is the HTTP method ("GET", "POST", …). Required for SourcePort
	// ingest, IOPort call, and ToolPort pipeline patterns.
	Method string
	// Path is the route path template (e.g. "/sensors/{sensorID}/data").
	Path string
	// Opts carries the same variadic options [rest.NewRoute] accepts.
	Opts []rest.RouteOpt
}

func (RESTPattern) isPortPattern() {}

// EventPattern declares a topic-shaped pub/sub communication pattern for a
// port bound to an event adapter (mqtt, mqtt5, zeromq). Topic mirrors
// [events.NewChannel]'s first argument; Opts accepts the same option
// vocabulary ([events.ChannelMeta], [events.Subscribe], [events.Publish],
// [events.TopicParam]).
//
//	ports.EventPattern{
//	    Topic: "sensors/{sensorID}/data",
//	    Opts: []events.ChannelOpt{
//	        events.Subscribe{Summary: "Sensor reading received"},
//	        events.TopicParam{Name: "sensorID"}.WithCodec(sensorIDCodec),
//	    },
//	}
type EventPattern struct {
	// Topic is the topic template (e.g. "sensors/{sensorID}/data").
	Topic string
	// Opts carries the same variadic options [events.NewChannel] accepts.
	Opts []events.ChannelOpt
}

func (EventPattern) isPortPattern() {}

// ReqReplyPattern declares a request/reply-shaped communication pattern for a
// port bound to a reqreply-style adapter (mqtt5 Call/Serve, zeromq Call/Serve).
// Topic mirrors [reqreply.NewRoute]'s first argument; Opts accepts the same
// option vocabulary ([reqreply.RouteMeta], [reqreply.TopicParam]).
//
//	ports.ReqReplyPattern{
//	    Topic: "compute/{tenantID}/add",
//	    Opts: []reqreply.RouteOpt{
//	        reqreply.TopicParam{Name: "tenantID"}.WithCodec(tenantIDCodec),
//	    },
//	}
type ReqReplyPattern struct {
	// Topic is the request topic template (e.g. "compute/{tenantID}/add").
	Topic string
	// Opts carries the same variadic options [reqreply.NewRoute] accepts.
	Opts []reqreply.RouteOpt
}

func (ReqReplyPattern) isPortPattern() {}

// MCPPattern declares an MCP tool communication pattern for a port bound to
// the mcpgo adapter. Name mirrors [apimcp.NewTool]'s first argument; Opts
// accepts the same option vocabulary ([apimcp.ToolMeta]).
//
//	ports.MCPPattern{
//	    Name: "compute-add",
//	    Opts: []apimcp.ToolOpt{apimcp.ToolMeta{Description: "Adds two numbers"}},
//	}
type MCPPattern struct {
	// Name is the tool name shown to MCP clients.
	Name string
	// Opts carries the same variadic options [apimcp.NewTool] accepts.
	Opts []apimcp.ToolOpt
}

func (MCPPattern) isPortPattern() {}

// FileFormatKind selects the wire format a [FilePattern]-built [format.File]
// uses, applied to the port's own codec. [FileFormatJSON] is the zero value
// and default.
type FileFormatKind int

const (
	// FileFormatJSON encodes/decodes the file as JSON (default).
	FileFormatJSON FileFormatKind = iota
	// FileFormatYAML encodes/decodes the file as YAML.
	FileFormatYAML
	// FileFormatTOML encodes/decodes the file as TOML.
	FileFormatTOML
)

// FilePattern declares a typed-file-shaped pattern for a port bound to the
// file adapter. Path and Opts mirror [format.NewFile]'s first and third
// arguments; the [format.Format] argument is derived from the port's codec
// and the Format kind ([format.JSON] of the codec by default). For a custom
// Format beyond JSON/YAML/TOML, fall back to handle-first wiring
// ([format.NewFile] by hand).
//
// On a [SinkPort], the built handle is a format.File of the port's payload
// type — pairs with file.DrainWriteFileAdapter. On an [IOPort], the built
// handle is a format.File of the port's response type (the file's content is
// the port's response) — pairs with file.ReadAdapter. Retrieve it with
// [FileHandle].
//
//	ports.FilePattern{
//	    Path: "data/{sensorID}/calibration.json",
//	    Opts: []format.FileOpt{
//	        format.FilePathParam{Name: "sensorID"}.WithCodec(sensorIDCodec),
//	    },
//	}
type FilePattern struct {
	// Path is the file path template (e.g. "data/{sensorID}/calibration.json").
	Path string
	// Format selects JSON (default), YAML, or TOML.
	Format FileFormatKind
	// Opts carries the same variadic options [format.NewFile] accepts.
	Opts []format.FileOpt
}

func (FilePattern) isPortPattern() {}

// SQLPattern declares SQL metadata for a port bound to a sql adapter. Unlike
// the handle-building patterns, SQLPattern is deliberately metadata-only: SQL
// query text and bind-parameter syntax are driver-specific and stay in the
// caller's typed queryFn/insertFn closures — there is no template for go-codex
// to parse, no handle, and no spec document.
//
// Table and Op are declared once on the port and propagated to the bound
// adapter via context ([WithSQLMeta] / [SQLMetaFromContext]); the sql
// adapters default their options' Table/Op fields from it when the explicit
// fields are empty. Retrieve the declared metadata with [SQLMeta].
//
//	ports.SQLPattern{Table: "readings", Op: "insert_reading"}
type SQLPattern struct {
	// Table names the table the port reads from / writes to.
	Table string
	// Op names the operation (e.g. "insert_reading", "list_calibrations").
	Op string
}

func (SQLPattern) isPortPattern() {}

// CachePattern declares a key/value-cache-shaped pattern for a port bound to
// a cache adapter (redis). Key is a template with {var} placeholders —
// cache keys are shaped like topics and paths, so they are declared the same
// way. The built handle is a [Cache] of the port's value type, retrievable
// with [CacheHandle].
//
// Port-type acceptance:
//
//   - [IOPort]: cache read-through/write-through step — the cached value is
//     the port's RESPONSE type (mirrors [FilePattern]); pairs with
//     redis.GetAdapter / redis.SetAdapter.
//   - [SinkPort]: terminal write-through — the cached value is the port's
//     payload type; pairs with redis.DrainSetAdapter.
//   - [LatestPort]: durable current state — the cached value is the port's
//     value type; pairs with redis.Seed + a write-through feed.
//   - [SourcePort] and [ToolPort]: rejected at construction with
//     [PatternRegisterError] — a cache does not produce a stream and is not
//     a tool surface.
//
// Key vars are plain strings in this phase (no per-var codecs — mirror the
// explicit varsFor-function style of file.DrainWriteFileAdapter):
//
//	ports.CachePattern{Key: "user:{id}", TTL: 15 * time.Minute}
type CachePattern struct {
	// Key is the key template (e.g. "user:{id}"). Placeholders are expanded
	// per item with [Cache.BuildKey]. A var-free key addresses a single
	// value (the LatestPort case).
	Key string
	// TTL is the default time-to-live applied on writes. Zero = no expiry.
	TTL time.Duration
	// Format selects the value wire format applied to the port's codec:
	// JSON (default), YAML, or TOML. Same enum and same reasoning as
	// [FilePattern.Format] — a generic format.Format cannot live in the
	// non-generic Pattern struct.
	Format FileFormatKind
}

func (CachePattern) isPortPattern() {}

// SocketPattern declares a path-addressed duplex socket endpoint for a port
// bound to a websocket adapter. Path mirrors [RESTPattern.Path] (same {var}
// placeholders); Opts carries [rest.RouteOpt] entries validated once per
// connection at upgrade time (path params, header params, security
// requirements) — not per frame.
//
// Port-type acceptance:
//
//   - [SourcePort]: inbound-only socket (clients send, server never pushes).
//
//   - [SinkPort]: broadcast-only socket (the WebSocket sibling of SSE).
//
//   - [DuplexPort]: full duplex — In decodes inbound, Out encodes outbound.
//
//   - [IOPort], [LatestPort], [ToolPort]: rejected at construction with
//     [PatternRegisterError] — per-message request/reply over a socket is an
//     RPC discipline (ReqReplyPattern territory), not a socket property.
//
//     ports.SocketPattern{Path: "/live/{room}"}
type SocketPattern struct {
	// Path is the HTTP upgrade path template (e.g. "/live/{room}").
	Path string
	// Subprotocols lists acceptable Sec-WebSocket-Protocol values.
	// Empty = accept any.
	Subprotocols []string
	// Format selects the frame wire format applied to the port's codec(s):
	// JSON (default), YAML, or TOML — same enum as [FilePattern.Format].
	Format FileFormatKind
	// Opts carries the same variadic options [rest.NewRoute] accepts,
	// validated once per connection at upgrade time.
	Opts []rest.RouteOpt
}

func (SocketPattern) isPortPattern() {}

// Socket is the handle built from a [SocketPattern]: the upgrade-request
// validator plus the frame formats bound to the port's codec(s). Retrieve it
// with [SocketHandle] and pass it to a websocket adapter constructor.
//
// One-directional ports use struct{} for the unused side: a [SourcePort]
// builds Socket[T, struct{}], a [SinkPort] builds Socket[struct{}, T]
// (the same convention as rest.SSERouteHandle[struct{}, T]).
type Socket[In, Out any] struct {
	// Path is the declared upgrade path template.
	Path string
	// Subprotocols lists acceptable Sec-WebSocket-Protocol values.
	Subprotocols []string
	// Route validates the upgrade request (path vars, headers, security) —
	// built from the pattern's Opts via the rest machinery, once per
	// connection.
	Route *rest.RouteHandle[struct{}, struct{}]
	// InFormat decodes inbound frames through the port's In codec.
	InFormat format.Format[In]
	// OutFormat encodes outbound frames through the port's Out codec.
	OutFormat format.Format[Out]
}

// Cache is the handle built from a [CachePattern]: the key template, the
// default TTL, and the value [format.Format] bound to the port's codec.
// Retrieve it from a port with [CacheHandle] and pass it to a cache adapter
// constructor (redis.GetAdapter, redis.SetAdapter, redis.DrainSetAdapter,
// redis.Seed).
type Cache[T any] struct {
	// Key is the declared key template (e.g. "user:{id}").
	Key string
	// TTL is the declared default time-to-live. Zero = no expiry.
	TTL time.Duration
	// Format encodes/decodes cached values through the port's codec —
	// every cache read and write is codec-validated.
	Format format.Format[T]
}

// BuildKey expands the key template's {var} placeholders from vars.
// A template without placeholders ignores vars. Returns [CacheKeyError]
// when a placeholder has no entry in vars.
func (c Cache[T]) BuildKey(vars map[string]string) (string, error) {
	key := c.Key
	for {
		start := strings.IndexByte(key, '{')
		if start < 0 {
			return key, nil
		}
		end := strings.IndexByte(key[start:], '}')
		if end < 0 {
			return key, nil // unbalanced brace: treat the rest as literal
		}
		name := key[start+1 : start+end]
		val, ok := vars[name]
		if !ok {
			return "", CacheKeyError{Key: c.Key, Var: name}
		}
		key = key[:start] + val + key[start+end+1:]
	}
}
