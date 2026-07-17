# Project Structure

> Back to [Package Reference](index.md)

```text
go-codex/
├── go.mod
├── README.md

├── codex/                  # ⭐ PUBLIC API: codecs, primitives, struct, union, slice
│   ├── codec.go            # Codec[T], WithDescription, WithTitle, WithExample, WithDeprecated, Validate, New
│   ├── either.go           # Either[A,B] type, Either2 codec
│   ├── errors.go           # ValidationError, ValidationErrors, EitherError
│   ├── map.go              # MapCodecSafe, MapCodecValidated, Downcast
│   ├── must.go             # Must[T] — generic panic-on-error helper
│   ├── nullable.go         # Nullable[T]
│   ├── object.go           # Field[T,F], RequiredField, OptionalField, DefaultField, Struct[T]
│   ├── primitives.go       # Int, Int32, Int64, Uint, Uint64, Float32, Float64, String, Bool, Bytes, Base64, Any, Pure
│   ├── refine.go           # Constraint[T], Refine, RefineFunc, Eq (Constraint.Schema for schema reflection)
│   ├── slice.go            # SliceOf[T]
│   ├── stringmap.go        # StringMap[V], Map[K, V]
│   ├── time.go             # Time(), Date(), Duration()
│   └── union.go            # TaggedUnion[T], UntaggedUnion[T], UntaggedVariant[T]
│
├── format/                 # format bridges: JSON, YAML, TOML, Gob, Binary (raw bytes), streaming
│   ├── format.go           # Format[T], JSON(), YAML(), TOML(), Gob(), Binary(), New(), NewTyped(), NewStreamed(),
│   │                       #   Codec(), UnmarshalRaw(), MarshalRaw(), PatchInto(), DeepMerge() — low-level
│   │                       #   primitives ports.File.Patch/ports.PatchEncoded are built on
│   ├── embedded.go         # EmbeddedJSON[T], EmbeddedYAML[T], EmbeddedTOML[T],
│   │                       #   EmbeddedDecodeError, EmbeddedEncodeError
│   └── env.go              # FromEnv[T], FromEnvVar[T], EnvVarError
│
├── route/                  # HTTP route descriptors (no renderer logic)
│   └── route.go            # Route, Param, Body, Response, SecurityScheme, SecurityRequirement
│
├── api/                    # transport-agnostic API builders
│   ├── internal/           # shared helpers (not public API)
│   │   └── template.go     # ParseTemplateVars, BuildFromTemplate, StripTemplateVars
│   ├── rest/               # REST API builder: typed Decode/Encode + OpenAPI spec
│   │   └── builder.go      # Builder, Route[Req,Resp]/NewRoute, SSERoute[Req,Event]/NewSSERoute,
│   │                       #   RouteHandle (Decode, Encode, EncodeRequest, DecodeResponse, ClientHandle),
│   │                       #   SSERouteHandle, BuildPath, AddServer, AddSchema, AddSecurityScheme,
│   │                       #   AddGlobalSecurity, PathParam, QueryParam, CookieParam, HeaderParam,
│   │                       #   ResponseHeaderParam, ResponseCookieParam, RouteMeta, SecurityScheme
│   ├── events/             # Event channel builder: typed Decode/Encode + AsyncAPI spec
│   │   └── builder.go      # Builder, Channel[T]/NewChannel, ChannelHandle, BuildTopic,
│   │                       #   AddServer, AddSchema, AddSecurityScheme, AddGlobalSecurity,
│   │                       #   TopicParam, ChannelMeta, Subscribe, Publish, SecurityScheme
│   ├── mcp/                # MCP server builder: Tools, Resources, Prompts
│   │   ├── builder.go      # Builder, NewTool[In,Out], NewResource[T], NewPrompt,
│   │   │                   #   ToolHandle, ResourceHandle, PromptHandle, MCPSpec
│   │   └── errors.go       # ToolInputError, ToolOutputError, ResourceEncodeError,
│   │                       #   ResourceParamError, MissingResourceVarError, PromptArgError, …
│   └── reqreply/           # transport-agnostic request-reply spec builder (ZMQ, MQTT 5, AMQP, …)
│       └── route.go        # NewRoute[Req,Resp], Route, RouteHandle, BuildTopic, RouteOpt,
│                           #   RouteParam, DuplicateRouteError, RouteParamError, MissingRouteParamError
│
├── adapters/               # transport-specific adapters
│   ├── nethttp/            # net/http adapter — server + client
│   │   ├── adapter.go      # Handler, Register, SSEHandler, RegisterSSE, RequestFromContext,
│   │   │                   #   WithResponseHeaders, ResponseHeadersFromContext,
│   │   │                   #   WithResponseCookies, ResponseCookiesFromContext, Options
│   │   ├── client.go       # Call[Req,Resp], CallOptions, UnexpectedStatusError,
│   │   │                   #   RequestBuildError, RequestError, ResponseBodyError
│   │   ├── cookie.go       # SetCookie, CookieOptions, PendingCookie
│   │   ├── stream.go       # HandlerLatest, RegisterLatest, PipelineHandler, RegisterPipeline,
│   │   │                   #   SSEFromHub, PipelineHandlerFunc
│   │   └── binding.go      # ports.SourceAdapter/SinkAdapter/IOAdapter/ToolAdapter constructors:
│   │                       #   IngestAdapter, PollAdapter, SSEAdapter, DrainCallAdapter,
│   │                       #   CallAdapter, PipelineAdapter
│   ├── chi/                # chi adapter for api/rest RouteHandles (github.com/go-chi/chi/v5)
│   │   ├── adapter.go      # Handler, Register, SSEHandler, RegisterSSE, RequestFromContext,
│   │   │                   #   WithResponseHeaders, WithResponseCookies, SetCookie, CookieOptions, Options
│   │   ├── stream.go       # HandlerLatest, RegisterLatest, PipelineHandler, RegisterPipeline, SSEFromHub
│   │   ├── binding.go      # IngestAdapter, SSEAdapter, PipelineAdapter (ports.SourceAdapter/
│   │   │                   #   SinkAdapter/ToolAdapter constructors — chi is server-only)
│   │   └── socket.go       # IngestSocketAdapter, BroadcastSocketAdapter, DuplexSocketAdapter
│   │                       #   (chi-safe swap-handler variants of adapters/websocket)
│   ├── mqtt/               # Paho MQTT 3.1.1 adapter for api/events ChannelHandles
│   │   ├── adapter.go      # SubscribeHandler, SubscribeOptions, Publish, PublishOptions,
│   │   │                   #   SubscribeError, ErrorKind, MessageFromContext
│   │   ├── topicvars.go    # TopicVarsFromMessage, TopicMismatchError
│   │   └── binding.go      # SubscribeAdapter, PublishAdapter (ports.SourceAdapter/SinkAdapter)
│   ├── mqtt5/              # MQTT 5.0 adapter (paho.golang) — PUB/SUB + request-reply
│   │   ├── adapter.go      # Subscribe, Publish, SubscribeOptions, PublishOptions,
│   │   │                   #   UserPropertyParam, ReplyTopicBuilder, UUIDReplyTopic, SharedReplyTopic
│   │   ├── reqreply.go     # Serve[Req,Resp], Call[Req,Resp], ServeOptions, CallOptions,
│   │   │                   #   ServeError, CallError, BrokerError, UserPropertyError
│   │   ├── stream.go       # AsPipelineFunc
│   │   └── binding.go      # SubscribeAdapter, PublishAdapter, CallAdapter, ServeAdapter
│   │                       #   (ports.SourceAdapter/SinkAdapter/IOAdapter/ToolAdapter)
│   ├── zeromq/             # ZeroMQ adapter — PUB/SUB, REQ/REP, DEALER/ROUTER
│   │   ├── adapter.go      # Subscribe, Publish, Serve, Call, ServeRouter, CallDealer
│   │   ├── errors.go       # SubscribeError, PublishEncodeError, ServeError, CallError
│   │   ├── socket.go       # FramedSocket interface, ErrTimeout
│   │   ├── stream.go       # AsPipelineFunc, ServeLatest
│   │   └── binding.go      # SubscribeAdapter, PublishAdapter, CallAdapter, ServeAdapter
│   │                       #   (ports.SourceAdapter/SinkAdapter/IOAdapter/ToolAdapter)
│   ├── file/               # stdlib-only file IO adapter (no external dependencies)
│   │   ├── errors.go       # ScanError, WatchError, WriteError, ReadError — all slog.LogValuer
│   │   └── binding.go      # package doc + ScanAdapter, WatchAdapter, ReadEachAdapter,
│   │                       #   DrainWriteAdapter, DrainWriteFileAdapter,
│   │                       #   DrainPatchAdapter, DrainPatchEncodedAdapter
│   │                       #   (ports.SourceAdapter/IOAdapter/SinkAdapter)
│   ├── sql/                # SQL adapter — goose migrations + codec-level row validation
│   │   ├── doc.go          # package overview
│   │   ├── validate.go     # Validate[T], ValidateOptions
│   │   ├── migrate.go      # Migrator, NewMigrator, Up, Down, Status, MigrationStatus, MigrateOptions
│   │   ├── errors.go       # RowValidationError, MigrationError, QueryStreamError,
│   │   │                   #   InsertStreamError — all slog.LogValuer
│   │   └── binding.go      # QueryAdapter, DrainInsertAdapter, QueryEachAdapter
│   │                       #   (ports.SourceAdapter/SinkAdapter/IOAdapter)
│   ├── websocket/          # server-side WebSocket adapter (gorilla/websocket behind narrow Socket/Upgrader interfaces)
│   │   ├── doc.go          # package overview
│   │   ├── socket.go       # Socket, Upgrader, NewUpgrader (gorilla shim — keepalive, read limits)
│   │   ├── hub.go          # Hub, NewHub — session registry (SessionInfo, targeted send, broadcast, drop-on-full)
│   │   ├── errors.go       # SocketError, ErrFrameDropped — slog.LogValuer
│   │   ├── binding.go      # IngestSocketAdapter (ports.SourceAdapter), BroadcastSocketAdapter
│   │   │                   #   (ports.SinkAdapter), DuplexSocketAdapter (ports.DuplexAdapter)
│   │   └── client.go       # Dialer, NewDialer, DialSourceAdapter, DialSinkAdapter,
│   │                       #   DialDuplexAdapter — auto-reconnect w/ backoff, gap SocketErrors
│   ├── redis/              # typed cache adapter (github.com/redis/go-redis/v9 behind a narrow interface)
│   │   ├── doc.go          # package overview
│   │   ├── commands.go     # Commands (narrow client interface), NewCommands (go-redis shim)
│   │   ├── errors.go       # ErrCacheMiss, CacheError — slog.LogValuer
│   │   └── binding.go      # GetAdapter, SetAdapter (ports.IOAdapter),
│   │                       #   DrainSetAdapter (ports.SinkAdapter), Seed (warm restart)
│   ├── mcpgo/              # mark3labs/mcp-go adapter for api/mcp handles
│   │   ├── adapter.go      # ToolHandler, ResourceHandler, PromptHandler,
│   │   │                   #   RegisterTool, RegisterResource, RegisterPrompt, Options
│   │   ├── stream.go       # ToolLatestHandler, ToolPipelineHandler, RegisterToolLatest,
│   │   │                   #   RegisterToolPipeline
│   │   └── binding.go      # ToolPipelineAdapter (ports.ToolAdapter), LatestAdapter (ports.LatestAdapter)
│   └── templ/              # templ SSR format plug-in for api/rest RouteHandles
│       └── adapter.go      # Format[Props], StreamingFormat[Props], DecodeNotSupportedError
│
├── app/                    # application lifecycle: root ctx + observer injection, supervised goroutines (fail-fast), LIFO shutdown hooks (Run/Shutdown)
├── ports/                  # protocol-agnostic IO enforcement points — inside-out pipeline wiring
│   ├── doc.go              # package overview
│   ├── io_param.go         # IOParam, PortOptions
│   ├── port_errors.go      # PortBindError, PortNoAdapterError, PortNoPipelineError — slog.LogValuer
│   ├── source_port.go      # SourcePort[T], SourceAdapter[T], NewSourcePort — fan-in
│   ├── sink_port.go        # SinkPort[T], SinkAdapter[T], NewSinkPort — fan-out
│   ├── io_port.go          # IOPort[Req,Resp], IOAdapter[Req,Resp], NewIOPort — 1 adapter only
│   ├── tool_port.go        # ToolPort[In,Out], ToolAdapter[In,Out], NewToolPort — request/response
│   ├── latest_port.go      # LatestPort[T], LatestAdapter[T], NewLatestPort — reactive cache
│   ├── duplex_port.go      # DuplexPort[In,Out], DuplexAdapter[In,Out], Framed[T], Session — bidirectional sessions
│   ├── file.go             # NewFile, File[T], FilePathParam, FileOptions, PatchEncoded, PathParamSchemas,
│   │                       #   FilePathParamError, MissingFilePathVarError, FileReadError,
│   │                       #   FileDecodeError, FileEncodeError, FileWriteError, FilePatchNotSupportedError
│   │                       #   (moved from format — protocol-agnostic addressing descriptor, mirrors Cache[T])
│   └── test_adapters.go    # ChanSourceAdapter[T], ChanSinkAdapter[T], FuncIOAdapter[Req,Resp]
│
├── stream/                 # reactive stream pipelines — bridges MQTT/ZeroMQ sources with forge functions
│   ├── stream.go           # Stream[T]{Values <-chan T, Errors <-chan error}
│   ├── errors.go           # StreamDecodeError, StreamApplyError — slog.LogValuer
│   ├── source.go           # From[T], FromCodec[T](format.Format[T]), SourceOptions
│   ├── transform.go        # Apply[In,Out], Filter[T], Tap[T], MapErr[T], Retry[T], ApplyOptions
│   ├── fanout.go           # Merge[T], Tee[T]
│   ├── combine.go          # CombineLatest2[A,B,Out]
│   ├── time.go             # Buffer[T], Debounce[T], Throttle[T]
│   ├── sink.go             # Drain[T], Collect[T], DrainOptions
│   └── topology.go         # Topology, TopologySpec, NewTopology, WithApply[In,Out], StepKind*
│
├── forge/                  # governed KPI computation pipeline (Layer 3)
│   ├── forge.go            # Measured[T], MeasuredCodec[T], Function[In,Out], NewFunction,
│   │                       #   Compose, Registry, PipelineSpec, PipelineInfo, FunctionMeta
│   ├── collection.go       # Map, Filter, Reduce, MapValues, MapValuesK collection ops
│   └── compose.go          # Compose — type-safe function chaining
│
├── render/                 # spec renderers (no runtime codec logic)
│   ├── internal/
│   │   └── schemarender/   # shared schema-to-map renderer (used by openapi + asyncapi)
│   │       └── schemarender.go  # SchemaObject
│   ├── openapi/            # OpenAPI 3.1 renderer
│   │   ├── openapi.go      # SchemaObject, ComponentsSchemas, MarshalJSON, MarshalYAML
│   │   └── document.go     # DocumentBuilder, Document, Info, Server — full 3.1 spec
│   ├── asyncapi/
│   │   ├── v2/             # AsyncAPI 2.6 renderer (frozen)
│   │   │   └── document.go # DocumentBuilder, Document, ChannelItem, Operation, Message
│   │   └── v3/             # AsyncAPI 3.0 renderer
│   │       └── document.go # DocumentBuilder, Document, Server, Operation, ChannelItem (Address)
│   ├── jsonschema/         # plain JSON Schema renderer (used by api/mcp)
│   │   └── jsonschema.go   # Schema(s schema.Schema) json.RawMessage
│   ├── pipeline/           # pipeline YAML renderer (for forge.PipelineSpec)
│   │   └── pipeline.go     # Render(spec) []byte
│   └── stream/             # stream topology YAML renderer (for stream.TopologySpec)
│       └── render.go       # Render(spec stream.TopologySpec) ([]byte, error)
│
├── schema/                 # schema model (pure data, zero dependencies)
│   └── schema.go           # Schema, Property, DiscriminatorSchema
│
├── validate/               # reusable constraints (reflect into schema automatically)
│   ├── binary.go           # PNG, JPEG, GIF, WebP, PDF, ZIP — predefined magic-byte constraints
│   ├── bytes.go            # MaxBytes(n), MinBytes(n), HasPrefix(prefix)
│   ├── duration.go         # PositiveDuration, NonNegativeDuration, MinDuration, MaxDuration
│   ├── float.go            # PositiveFloat, NegativeFloat, NonZeroFloat, MinFloat, MaxFloat, RangeFloat
│   ├── format.go           # format constraints: Email, UUID, URL, URLWithSchemes, URI, Hostname,
│   │                       #   IPv4, IPv6, IP, Date, Time, DateTime, SemVer, Slug, CIDR,
│   │                       #   ContainerImage, MQTTTopic, MQTTPublishTopic, HTTPPath,
│   │                       #   IntString, PositiveIntString, NonNegativeIntString,
│   │                       #   IntStringInRange, BearerToken, JWT, EnvVarName, EnvVarPrefix
│   ├── int.go              # PositiveInt, NegativeInt, NonZeroInt, MinInt, MaxInt, RangeInt; int32 + int64 variants
│   ├── uint.go             # PositiveUint, MinUint, MaxUint, RangeUint; uint64 variants
│   └── string.go           # string constraints: NonEmptyString, MinLen, MaxLen, Pattern, OneOf
│
├── stats/                  # dependency-free metrics observer interfaces
│   └── observer.go         # ValidationObserver, Observer, PipelineObserver, SecurityObserver,
│                           #   FileObserver, SQLObserver, TraceObserver,
│                           #   NoopObserver (all seven), LoggingObserver, NewFanout
│
└── examples/               # usage demonstrations — not importable by library packages
    │
    │   # ── Codec (Layer 1) ────────────────────────────────────────────────────
    ├── construction/       # New + Must: construction-time validation demo
    ├── decode-errors/      # multi-field ValidationErrors + errors.As demo
    ├── error-types/        # every structured error type: ValidationError, TypeMismatch, etc.
    ├── codec-mapping/      # shared field codecs, sub-codec reuse, MapCodecSafe, MapCodecValidated
    ├── enum-union-sum/     # tagged union + OneOf enum + sum type patterns
    ├── formats/            # builtin format constraints demo (Email, UUID, URL, …)
    ├── html-sanitize/      # sanitizing untrusted HTML input with a codec
    ├── multiformat/        # JSON / YAML / TOML with one codec
    ├── order/              # nested structs, SliceOf, Time, Nullable, StringMap demo
    ├── shape/              # tagged union + Downcast demo
    └── validate/           # explicit Validate before marshal
    │
    │   # ── REST / HTTP (Layer 2) ───────────────────────────────────────────────
    ├── api-rest/               # REST API builder: typed helpers + OpenAPI spec
    ├── openapi/                # OpenAPI components/schemas generation from a Codec
    ├── rest-api/               # full OpenAPI 3.1 document from route descriptors
    ├── adapters-nethttp/       # net/http adapter: three-layer pipeline, multi-format bodies, observer
    ├── adapters-nethttp-security/   # net/http adapter: bearer JWT, scopes, SecurityFunc, observer
    ├── adapters-nethttp-client/     # codec-as-contract HTTP client: shared contract/, Call, CredentialFunc
    │   └── contract/               #   shared Route specs, codecs, types (importable by both sides)
    ├── adapters-chi/           # chi adapter: wiring api/rest to chi.Router
    ├── adapters-chi-security/  # chi adapter: bearer JWT security, per-route scopes
    ├── adapters-sse/           # SSE: NewSSERoute, SSEHandler, path codec, OpenAPI spec
    ├── adapters-streaming-sse-templ/ # chunked streaming + SSE HTML fragments via templ components
    ├── adapters-templ/         # templ SSR: same route serves HTML and JSON; observer wired
    └── png-upload/             # binary payload upload + download: format.Binary, validate.PNG, codex.Bytes()
    │
    │   # ── Events / MQTT (Layer 2) ─────────────────────────────────────────────
    ├── api-events/             # Event channel builder: typed helpers + AsyncAPI spec
    ├── event-driven/           # full AsyncAPI 2.6 document from channel descriptors
    ├── adapters-mqtt/          # Paho MQTT 3.1.1: three-layer pipeline, multi-format pub/sub, wildcard
    ├── adapters-mqtt-security/ # Paho MQTT: security credentials, SecurityFunc, observer
    ├── adapters-mqtt-contract/ # codec-as-contract MQTT: shared contract/, producer + consumer
    │   └── contract/           #   shared Channel specs, codecs, types (importable by both sides)
    ├── adapters-mqtt5/         # MQTT 5.0: PUB/SUB + request-reply (Serve/Call), User Properties, ContentType
    └── gob-contract/           # Go library as contract: gob wire encoding, no code-gen
        └── contract/           #   shared Channel, codec, Gob format — compiler-enforced contract
    │
    │   # ── ZeroMQ (Layer 2) ─────────────────────────────────────────────────────
    ├── adapters-zeromq/            # ZeroMQ PUB/SUB: three-layer pipeline, FramedSocket, observer
    ├── adapters-zeromq-reqrep/     # ZeroMQ REQ/REP: Serve/Call blocking pattern
    └── adapters-zeromq-dealer-router/ # ZeroMQ DEALER/ROUTER: concurrent request-reply
    │
    │   # ── MCP (Layer 2) ────────────────────────────────────────────────────────
    └── adapters-mcp/           # MCP server: Tools, Resources, Prompts, MCPSpec, observer
    │
    │   # ── Forge / Pipeline (Layer 3) ──────────────────────────────────────────
    ├── forge-oee/          # forge pipeline: OEE KPI computation, governance, Compose, MeasuredCodec
    ├── forge-collection/   # forge collection ops: Map, Filter, Reduce, MapValues on MQTT sensor batches
    └── oee-chain/          # full three-layer chain: codex + api/events + forge with AsyncAPI + pipeline spec
    │
    │   # ── Config / CLI / Observability ─────────────────────────────────────────
    ├── cli-config/         # CLI tool config: TOML file + env var overlay with codecs
    ├── env-config/         # format.FromEnv: schema-driven env var loading with defaults
    ├── file-io/            # ports.File[T]: static + template files, Patch, PatchEncoded, FileObserver
    ├── flat-key-patch/     # flat dotted-key JSON: Patch + PatchEncoded with Map key validation
    ├── stats-observer/              # stats.ValidationObserver wired to codecs directly (no adapter)
    ├── http-trace-span-propagation/ # TraceObserver with OTel: parent span from traceparent header
    ├── redis-cache/                 # typed cache boundary: CachePattern + GetAdapter/SetAdapter/Seed against an in-memory Commands fake (no live Redis)
    ├── websocket-duplex/            # DuplexPort over a real loopback WebSocket: typed commands in, targeted replies out, app-supervised Feed, observer via app.Options.Observer (upgrade/frame/validation metrics)
    ├── pattern-custom-format/       # ports.Pattern CustomFormat escape hatch: FilePattern+format.Binary (raw PNG) and CachePattern+format.Gob (typed binary cache entry)
    ├── websocket-client/            # client-side DialDuplexAdapter: two go-codex processes over one WS connection, reconnect gap semantics, RegisterSocket AsyncAPI spec
    ├── stream-pipeline/             # stream operator showcase: From, Apply, CombineLatest2, Tee, Merge, FlatMapSlice, Buffer, Window, Debounce, Throttle, MapErr, Switch, GroupBy, Topology YAML
    ├── stream-oee/                  # forge + stream integration: governed OEE (Availability×Performance×Quality) from machine events; Window→Apply(computeOEEFromWindow)→Filter→Drain; governance + topology YAML
    └── sensor-service/              # flagship: one coherent use case (MQTT ingest → SQL persist → env-configured alert → REST time series → REST-triggered file export) structured as a real project — domain/, pipeline/, ioports/, observability/, adapters/, db/, main.go (wiring) + demo.go + README.md
```
