## Open items

### Codec

- [ ] **Lazy[T] / recursive types** _(deferred — complex)_
      Self-referential structs (e.g. tree nodes) cannot reference their own codec during construction.
      Needs `Lazy[T](fn func() Codec[T]) Codec[T]` using `sync.Once` to defer resolution.
      Schema problem: a recursive schema cannot be inlined — it must emit a `$ref` to a named
      component. So `Lazy` only works correctly when combined with `SchemaName` registration
      in the API builder. Significant design work required before implementation.

- [ ] **Struct codec boilerplate — accept and document**
      `SimpleField` with reflection was evaluated and rejected: field-name typos and type
      mismatches become runtime panics, breaking the compile-time type-safety guarantee that
      is the library's primary strength. The `RequiredField`/`OptionalField` helpers are the
      intended idiomatic pattern. Document with examples; consider a `go generate` code
      generator as a future separate tool.

- ~~**`context.Context` codec**~~ _(evaluated and rejected)_
  Context values are in-process application state, not protocol-boundary data. They have
  no wire format, no OpenAPI/AsyncAPI equivalent, and never cross a protocol boundary —
  so the codec-as-spec-source-of-truth pattern does not apply. Idiomatic Go solves typed
  context access with a typed key + `FromContext(ctx) (T, bool)` accessor; no framework
  needed. The adapter escape hatches (`nethttp.RequestFromContext`, future
  `amqp.DeliveryFromContext`) already cover access to protocol-level envelope data.
  Not in scope for go-codex.

- [ ] **`go generate` struct codec generator** _(medium effort — separate binary/module)_
      A `//go:generate go-codex-gen` tool that reads a Go struct's field names and types and
      emits the `codex.Struct[T](RequiredField..., OptionalField...)` boilerplate removes the
      primary ergonomic friction of the library. Must be a separate `cmd/go-codex-gen` binary
      (or separate module) — not part of the importable library. Struct tags (e.g.
      `codex:"name,required"`) could drive field name and required/optional.

- [ ] **`codex.Map[K, V]` — typed-key maps**
      `StringMap[V]` only supports `string` keys. A `Map[K, V]` with a key codec would enable
      validated `map[uuid.UUID]T` patterns and richer schema output. Requires a new codec
      primitive and schema support (JSON only allows string keys; YAML/TOML allow more).
      Medium complexity; depends on whether schema can express a key type.

### Validate

### Format

- [ ] **`format/protobuf` — protobuf binary format** _(medium effort)_
      A `format.Protobuf[T proto.Message]` function that bridges `[]byte` (protobuf binary) ↔
      `T` (proto-generated Go struct). Enables codecs whose `Encode`/`Decode` operate on
      protobuf binary payloads rather than JSON `map[string]any` — useful for MQTT brokers,
      Kafka, or other binary transports where payloads happen to be protobuf-encoded.
      Complements `adapters/grpc` for end-to-end typed validation on the binary wire format.
      Schema output for proto binary is a stub (proto has its own schema language — `.proto` IDL)
      but the codec's `Refine` constraints and `ValidationErrors` work normally.
      New dependency: `google.golang.org/protobuf` in `format/` only; core library stays
      dependency-free.

### Spec generation

- [ ] **`$ref` auto-deduplication** _(deferred — high risk)_
      Repeated schemas (e.g. a shared `Address` codec used in multiple places) are inlined
      everywhere rather than deduplicated into `components/schemas`.
      Requires schema canonicalization, a naming registry, a second render pass, and cycle
      detection. Changes output shape in potentially surprising ways.
      Current workaround: explicit `SchemaName` on `Body`/`Response` + `AddSchema` on the
      builder — intentional and sufficient for the common case.

- [ ] **`$ref` for path/topic parameter schemas** _(deferred — low priority)_
      Path parameter schemas (from `PathParam.Codec`) and AsyncAPI channel parameter schemas
      (from `TopicParam.Codec`) are always inlined in the spec output. For projects that
      reuse the same parameter codec across many routes (e.g. a UUID codec for dozens of
      `{id}` parameters), this produces repetitive YAML.
      This is only noticeable at scale and requires the `$ref` auto-deduplication machinery
      to be in place first; block on that item.

- [ ] **`render/graphql` — GraphQL SDL generation** _(medium effort)_
      Generate GraphQL SDL `type` and `input` definitions from `codex.Struct[T]` codecs —
      same direction as `render/openapi` and `render/asyncapi`. Mapping rules:
      struct fields → SDL fields, `Required: true` → `!` (non-null), `Nullable[T]` → nullable
      field, `SliceOf[T]` → `[T!]!`, `codex.Union` → SDL union type, `schema.Format`
      annotations → SDL `scalar` declarations. `input` types (for mutations) and `type`
      definitions (for queries/subscriptions) are separate renderers.
      New package `render/graphql`; no external dependency (pure string generation like
      `render/openapi`).
      Note: GraphQL subscriptions carry the same push semantics as `api/events.ChannelHandle`
      channels — a `render/asyncapi`-style AsyncAPI output that targets GraphQL subscriptions
      as a transport is a natural extension.

- [ ] **OpenAPI / AsyncAPI version targeting** _(medium effort)_
      Currently renders AsyncAPI 2.x and OpenAPI 3.0. Explicit version targeting (e.g.
      `RenderOpenAPI31()`, `RenderAsyncAPI30()`) with per-version schema differences handled
      (e.g. OpenAPI 3.1 uses `type: [string, null]` instead of `nullable: true`) would make
      output spec-compliant for strict validators and tooling (Redoc, Spectral, etc.).

### API builders

- [ ] **Generic type inference on body-less routes** _(watch — Go limitation)_
      `AddRoute[struct{}, Resp]` and `AddChannel[T]` are ergonomic for typed payloads, but
      Go's type inference occasionally fails to infer `struct{}` as the `Req` type parameter
      without an explicit annotation. This is a Go compiler limitation, not a library bug;
      the workaround is to state the type parameters explicitly:
      `rest.AddRoute[struct{}, User](b, "GET", "/users/{id}", ...)`.
      Track Go release notes for improvements to partial type argument inference.

### Adapters

#### net/http adapter gaps

#### MQTT adapter gaps

- [ ] **`Observer`: `RecordRejection` hook for protocol-level request rejections** _(medium effort — breaking interface change)_
      `RecordRequest` already captures 413 and 415 status codes, so Prometheus users can
      filter by status code bucket. However, if a custom `ErrorHandler` rewrites the status,
      the observer loses the distinction between body-too-large and codec validation failures.
      A dedicated `RecordRejection(reason string)` method on `Observer` (e.g.
      `"body-too-large"`, `"unsupported-media-type"`) would give observers a semantic signal
      without relying on status codes.
      **Breaking change**: all existing `Observer` implementations would need to add the new
      method. Consider using a separate optional interface (`RejectionObserver`) or a
      version-compatible extension pattern (e.g. type-assert before calling).

- [ ] **`adapters/mqtt`: MQTT 5.0 User Properties** _(medium effort — MQTT 5.0 only)_
      See "MQTT message header validation" in the API builders section above — this adapter
      item is the wire-up half: extract User Properties from the MQTT 5.0 message envelope
      using the `paho.mqtt.golang` v5 API and pass them to `ChannelHandle.ValidateHeaders`.
      MQTT 3.x has no User Properties; the adapter must skip validation gracefully.

- [ ] **`adapters/grpc` — gRPC unary validation interceptor** _(medium-high effort)_
      Proto-generated code decodes wire format correctly but has no business validation (field
      length, format, range). go-codex fills this gap: write a `codex.Struct[*pb.T]` with
      `Refine` constraints, then register it with a `UnaryServerInterceptor` that runs codec
      validation after gRPC decodes the message and before the handler is called.
      Per-field errors map to `google.rpc.BadRequest.FieldViolation` (gRPC rich error details),
      returning status code `InvalidArgument` with structured field diagnostics. The same codec's
      `Schema` can feed an OpenAPI spec for grpc-gateway REST transcoding — single source of
      truth for both validation and documentation. `stats.Observer.RecordValidationError`
      integration applies without modification.
      Streaming RPC (ServerStream, ClientStream, BiDi) is deferred — requires a separate
      streaming interceptor design with per-message validation semantics.
      New dependency: `google.golang.org/grpc` in `adapters/grpc` only; core library stays
      dependency-free.

- [ ] **`adapters/websocket` — WebSocket typed message adapter** _(medium effort)_
      Maps `api/events.ChannelHandle[T]` to a WebSocket connection lifecycle: upgrade →
      decode incoming messages → typed handler → encode outgoing messages. Direct parallel to
      `adapters/mqtt`: the handler signature, `stats.Observer` integration, and
      `MessageFromContext` escape hatch follow the same patterns. AsyncAPI `ws` binding
      documents the channel in the spec.
      Library choice: `nhooyr.io/websocket` (context-aware, stdlib-compatible, actively
      maintained) preferred over gorilla/websocket (archived).
      New dependency: `nhooyr.io/websocket` in `adapters/websocket` only.

- [ ] **`adapters/nethttp`: SSE (Server-Sent Events) streaming responses** _(medium effort)_
      HTTP SSE (`text/event-stream`) allows the server to push a typed event stream over an
      open connection. Requires a `StreamHandle[T]` concept in `api/rest` (or a streaming
      variant of `RouteHandle`) and a `StreamHandler[T]` in `adapters/nethttp` that encodes
      events as `data: <json>\n\n` frames. Flush is called per event.
      Works on both HTTP/1.1 (chunked transfer encoding) and HTTP/2 (multiplexed stream) — no
      protocol-specific code; `net/http` handles the transport difference transparently.
      OpenAPI spec: the streaming route would render with `text/event-stream` response media type.
      Note on HTTP/3: `adapters/nethttp` is transport-agnostic (`http.Handler`). HTTP/3 servers
      (`quic-go`, cloudflare quiche-go) expose the same interface — no adapter change needed.
      Security note worth documenting: non-idempotent routes (POST/PUT/DELETE) should not be
      served as 0-RTT early data in HTTP/3 without replay protection.

- [ ] **MQTT message header validation** _(medium effort — MQTT 5.0 only)_
      MQTT 5.0 introduced User Properties: arbitrary key-value string pairs attached to any
      message. AsyncAPI models these via `message.headers` (a JSON Schema object). The
      `api/events` builder currently has no `MessageHeader` concept.
      Proposal: introduce `MessageHeader{Name, Description string, Required bool, Codec *codex.Codec[string]}`
      and `ChannelConfig.MessageHeaders []MessageHeader`. The AsyncAPI spec output renders
      these as the `headers` schema on the message object. Add
      `ChannelHandle.ValidateHeaders(map[string]string) error` + `MessageHeaderError{Name, Value, Err}`.
      The `adapters/mqtt` adapter extracts User Properties from `paho.mqtt.golang` v5 messages
      and calls `ValidateHeaders` on receive.
      Caveats: MQTT 3.1.1 has no User Properties — this feature is MQTT 5.0 only. The adapter
      should skip header validation silently when the broker connection is v3. Consider a
      build tag or runtime version check.

- [ ] **`adapters/graphql` — GraphQL input validation middleware** _(medium effort)_
      Middleware for gqlgen resolvers (or graphql-go): validate resolver `Input` structs using
      a registered `codex.Struct[InputType]` before the resolver runs. Per-field errors map to
      GraphQL error extensions (`{"extensions": {"field": "email", "constraint": "email"}}`),
      returning a typed error list instead of an opaque server error.
      This is the runtime complement to `render/graphql` — the same codec drives both SDL
      generation and runtime validation. `stats.Observer.RecordValidationError` integration
      applies with `location = "input"`.
      New dependency: `github.com/99designs/gqlgen` (or graphql-go) in `adapters/graphql` only.

- [ ] **`adapters/amqp` — AMQP 0-9-1 adapter** _(high effort)_
      Add an adapter for AMQP 0-9-1 (RabbitMQ and compatible brokers) using
      `github.com/rabbitmq/amqp091-go`. The `api/events` builder is transport-agnostic and
      already produces `ChannelHandle` with `Decode`/`Encode` — the adapter wires these into
      the AMQP publish/consume lifecycle.

      **Exchange model mapping:**
      AMQP replaces the flat topic string with an exchange + routing key pair. `ChannelHandle`
      should carry optional AMQP binding metadata — exchange name, exchange type
      (`direct`/`fanout`/`topic`/`headers`), routing key, and queue name. This metadata drives
      both the AsyncAPI AMQP channel binding block and the adapter's `ch.QueueBind` / `ch.Publish`
      calls.

      **Message properties:**
      AMQP messages carry structured properties: `ContentType`, `ContentEncoding`,
      `CorrelationId`, `ReplyTo`, `MessageId`, `Expiration`, `Priority`, `DeliveryMode`,
      `Timestamp`, `AppId`, and a free-form `Headers` table (equivalent to MQTT User
      Properties). The adapter should expose these via a context value (analogous to
      `nethttp.RequestFromContext`) so handler functions can read/write envelope metadata
      without breaking the typed `Decode`/`Encode` contract.

      **Ack / Nack:**
      AMQP consumers must explicitly acknowledge messages. The adapter should ack after a
      successful handler return and nack (with optional requeue) on `Decode` failure or handler
      error. Expose an `AckFromContext` / `NackFromContext` escape hatch for handlers that need
      manual ack control (e.g. partial batch processing).

      **AsyncAPI AMQP bindings:**
      AsyncAPI 2.x defines `amqp` binding objects for channels (`exchange`, `queue`),
      operations (`expiration`, `cc`, `priority`, `deliveryMode`, `mandatory`, `ack`,
      `replyTo`), and messages (`contentEncoding`, `messageType`). The `api/events` builder
      should accept an optional `AMQPChannelBinding` config that flows into the spec output
      alongside the existing channel descriptor.

      **Scope boundary:**
      Connection management (dialing, reconnection, channel pool) is the caller's
      responsibility — the adapter accepts an `*amqp091.Channel`, same as how `adapters/mqtt`
      accepts a connected `mqtt.Client`. This keeps the adapter dependency-light and testable.

      **New dependency:** `github.com/rabbitmq/amqp091-go` (in `adapters/amqp` only — core
      library stays dependency-free).

### Tooling

- [ ] **Spec → codec generator** _(high effort — separate binary/module)_
      The library currently goes one direction: codec → spec (OpenAPI / AsyncAPI YAML output).
      The inverse — reading an existing OpenAPI 3.x or AsyncAPI 2.x/3.x spec and emitting
      `codex.Struct[T](RequiredField..., OptionalField...)` Go source — would make adoption
      dramatically easier for teams with existing APIs.
      Implementation notes: - Parse spec YAML/JSON (`$ref` resolution, `allOf`/`oneOf`/`anyOf` flattening) - Map JSON Schema types to Go primitives (`string` → `codex.String()`, `integer` →
      `codex.Int()`, `array` → `codex.SliceOf(...)`, `object` → `codex.Struct[T](...)`) - Emit idiomatic Go source with `RequiredField`/`OptionalField` based on `required` array - Attach `validate.*` constraints from known `format` values (e.g. `format: uuid` →
      `Refine(validate.UUID)`, `format: email` → `Refine(validate.Email)`) - Must be a separate `cmd/go-codex-gen` binary (or separate module) — not part of the
      importable library - High effort: spec parsing, type inference, code generation, edge cases (`nullable`,
      `discriminator`, circular `$ref`s). Significant design work before implementation.

- [ ] **Proto IDL → codec generator** _(high effort — separate binary/module)_
      Reads a `.proto` file and emits `codex.Struct[*pb.T](...)` Go source alongside the
      existing proto-generated Go code. Complements the "Spec → codec generator" item.
      Mapping: proto `string` + field options → `codex.String()` + constraints; `int32/int64`
      → `codex.Int()`; `repeated T` → `codex.SliceOf(...)`; `optional T` → `codex.Nullable(...)`;
      `message` → `codex.Struct[T]`. Known `google.protobuf.*` well-known types map to
      typed codecs (e.g. `google.protobuf.Timestamp` → `codex.Time()`).
      `buf.validate` / `protoc-gen-validate` annotations → `Refine(validate.*)` constraints.
      Must be a separate `protoc` plugin binary or `cmd/go-codex-proto` tool — not part of the
      importable library.

- [ ] **GraphQL SDL → codec generator** _(high effort — separate binary/module)_
      Reads a GraphQL SDL schema file and emits `codex.Struct[T](...)` Go source for each
      `type` and `input` definition. Complements `render/graphql` (inverse direction).
      Mapping: SDL `String!` → `codex.String()` (required); `String` → `codex.Nullable(codex.String())`;
      `[T!]!` → `codex.SliceOf(...)`; `scalar Email` → `codex.String().Refine(validate.Email)`.
      Custom `@constraint` directives map to `Refine` constraints; `enum` → `codex.String().Refine(validate.OneOf(...))`.
      Must be a separate `cmd/go-codex-graphql` binary or module.

---

## Completed (reference)

Items are removed when done. See git log for implementation history.
