package ports

import (
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
