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

- [ ] **`CookieParam` — HTTP cookie validation** _(low effort — mirrors QueryParam)_
      `api/rest` has no concept of cookies. Introduce `CookieParam{Name, Description string,
    Required bool, Codec *codex.Codec[string]}` (same shape as `QueryParam`) and
      `RouteConfig.CookieParams []CookieParam`. Add `RouteHandle.ValidateCookies(map[string]string) error`
      and a matching `CookieParamError{Name, Value, Err}`. In the OpenAPI spec, cookie params
      render as `in: cookie`. The `adapters/nethttp` adapter extracts cookies via
      `r.Cookie(name)` and calls `ValidateCookies` automatically before the handler — same
      pattern as query param auto-validation. Zero new dependencies.

- [ ] **`HeaderParam` — HTTP header validation** _(low effort — mirrors CookieParam)_
      Introduce `HeaderParam{Name, Description string, Required bool, Codec *codex.Codec[string]}`
      and `RouteConfig.HeaderParams []HeaderParam`. Add `RouteHandle.ValidateHeaders(map[string]string) error`
      and `HeaderParamError{Name, Value, Err}`. In the OpenAPI spec, header params render as
      `in: header`. The `adapters/nethttp` adapter extracts headers via `r.Header.Get(name)`
      (canonicalised by `net/http`) and calls `ValidateHeaders` before the handler.
      Note: OpenAPI reserves `Accept`, `Content-Type`, and `Authorization` — these should not
      be declared as `HeaderParam` entries (standard convention; worth a doc note).

### Adapters

#### net/http adapter gaps

- [ ] **`adapters/nethttp`: configurable body size limit** _(low effort)_
      `maxRequestBodyBytes` is hardcoded at 1 MiB. Add `Options.MaxBodyBytes int64` (zero
      means use the default). Callers hosting large file uploads or tiny IoT payloads need
      control over this limit without forking the adapter.

- [ ] **`adapters/nethttp`: `Content-Type` validation on request** _(low effort)_
      The adapter currently reads and decodes the body regardless of the `Content-Type` header.
      A client sending `Content-Type: application/xml` gets a confusing JSON decode error.
      For body-bearing methods (POST, PUT, PATCH), reject requests whose `Content-Type` does
      not match the route's expected media type (default: `application/json`), returning 415
      Unsupported Media Type. The expected media type should be derivable from the route
      descriptor or overridable in `Options`.

- [ ] **`adapters/nethttp`: multi-value query parameter support** _(low effort)_
      `queryValues` currently drops all but the first value for repeated keys
      (`?tags=a&tags=b` → only `"a"` is validated). Add `Options.MultiValueQueryParams bool`
      or a separate `ValidateQueryMulti(map[string][]string) error` on `RouteHandle` so
      repeated keys can be validated as a slice. OpenAPI allows `style: form, explode: true`
      for this pattern.

- [ ] **`adapters/nethttp`: content negotiation (`Accept` header)** _(medium effort)_
      The adapter hardcodes `Content-Type: application/json` on responses. Add
      `Options.Formats []format.Format[Resp]` so the adapter can select the response format
      based on the client's `Accept` header. The `format` package already provides
      `format.JSON`, `format.YAML`, and `format.TOML`. When no acceptable format is found,
      return 406 Not Acceptable. The OpenAPI spec `produces` list would reflect the registered
      formats.

- [ ] **`adapters/nethttp`: response header control** _(medium effort)_
      Handlers return `(Resp, error)` with no way to set response headers such as `Location`
      (for 201 Created), `ETag`, `Cache-Control`, or custom headers. Options:
      (a) Expose `http.ResponseWriter` via context (escape hatch — breaks the typed contract);
      (b) Introduce a `ResponseEnvelope[Resp]` wrapper that carries both the value and a
      `http.Header` map — the adapter detects this type and merges headers before writing;
      (c) A `WithResponseHeaders(ctx, headers)` / `ResponseHeadersFromContext(ctx)` pair where
      the handler deposits headers into context and the adapter reads them after `fn` returns.
      Option (c) is the least intrusive and most composable. Needs design decision.

#### MQTT adapter gaps

- [ ] **`adapters/mqtt`: wildcard topic variable extraction** _(medium effort)_
      When subscribing to a template topic like `sensors/{sensorID}/temperature` (compiled to
      the MQTT wildcard `sensors/+/temperature`), the concrete incoming topic is e.g.
      `sensors/abc123/temperature`. The handler currently has no automatic way to get
      `sensorID = "abc123"` — it must parse the concrete topic string manually.
      Add `mqtt.TopicVarsFromMessage(handle, msg) (map[string]string, error)` that matches
      the concrete topic against the channel's template and extracts variable values. Ties
      into the existing `BuildTopic` template logic; inverse operation of variable substitution.

- [ ] **`adapters/mqtt`: MQTT 5.0 User Properties** _(medium effort — MQTT 5.0 only)_
      See "MQTT message header validation" in the API builders section above — this adapter
      item is the wire-up half: extract User Properties from the MQTT 5.0 message envelope
      using the `paho.mqtt.golang` v5 API and pass them to `ChannelHandle.ValidateHeaders`.
      MQTT 3.x has no User Properties; the adapter must skip validation gracefully.

- [ ] **`adapters/chi` or `adapters/gin`** _(medium effort)_
      `adapters/nethttp` wraps `RouteHandle` for the standard library. Most Go services use a
      router (Chi, Gin, Echo). A Chi adapter is ~50 lines; Gin similar. High discoverability
      value — these are the ecosystems most users land in. Chi is preferred as it builds
      directly on `net/http` with no magic.

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

---

## Completed (reference)

Items are removed when done. See git log for implementation history.
