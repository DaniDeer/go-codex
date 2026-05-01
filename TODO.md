Public API:

- [ ] How to use this codec in a GO CLI scenario? Discuss this and add an example to the README since GO is a favourite language for CLI tools. The obvious use case are config files that CLI tools always read (YAML/TOML). But is codec also applicable for parsing the command line inputs with commands, flags, arguments?

Error handling:

- [ ] The error model is too simple
      Right now:
  ```GO
  (T, error)
  ```
  Example:
  ```GO
  {
    "width": -1,
    "height": -2
  }
  ```
  You only get:
  ```TEXT
  width invalid
  ```
  But user want:
  ```TEXT
  width invalid
  height invalid
  ```
  Introduce:
  ```GO
  type ValidationError struct {
      Path string
      Msg  string
  }
  ```
  So that you can return multiple errors:
  ```GO
  type Result[T any] struct {
      Value  T
      Errors []ValidationError
  }
  ```

Schema:

- [ ] Schema is underpowered
      We have:
  ```GO
  Type, Properties, Required, OneOf
  ```
  But we also need:
  - additionalProperties: false
  - minimum, maximum
  - pattern
  - nullable
  - discriminator (OpenAPI)

Constraints:

Questions:

- Question: Can my codec also support HTML? Is it usefull to have something similar for HTML maybe together with templ in GO?
  Answer: Don´t force HTML into a codec - use templ directly for rendering, but a mapper/transformer between your data models and templ components is genuinely useful. Use cases:
  - Mapping API responses -> templ props
  - Sanitizing/escaping HTML content
  - Converting markdown/rich text -> HTML
    But not: Full HTML encode/decode

---

## Architecture review findings (2026-05-01)

### Schema

- [x] **Schema property order is nondeterministic** *(done 2026-05-01)*
      `schema.Schema.Properties` changed to `[]schema.Property{Name string; Schema Schema}`.
      Added `Schema.Prop(name) (Schema, bool)` lookup helper.

- [ ] **TaggedUnion has no discriminator in generated schema**
      `codex.TaggedUnion` uses a tag field at encode/decode time, but `schema.Schema` has
      no discriminator concept. The generated OpenAPI/AsyncAPI spec does not describe the
      polymorphism — a spec validator sees an opaque object.
      Fix: add `Discriminator *DiscriminatorSchema` to `schema.Schema`; render it in both
      `render/openapi` and `render/asyncapi`.

### Codec

- [ ] **Encode does not validate — silent footgun**
      `Refine` constraints only run on `Decode`. Calling `Marshal` on a struct with
      invalid field values (e.g. invalid email, out-of-range int) silently succeeds.
      At minimum, add an explicit `Format[T].Validate(T) error` method and document
      that publish-side callers must call it before `Marshal`. Optionally add a
      `ValidateOnMarshal bool` option to `Format[T]`.

- [ ] **Missing practical codec types**
      Real domain models routinely need types that don't exist yet:
      - `time.Time` (ISO 8601, `format: date-time` in schema)
      - Nullable / pointer fields (`*T` → `nullable: true`)
      - `map[string]V` (→ `additionalProperties` in schema)
      - Byte slices / base64 strings
      - Recursive types (self-referential structs)

- [ ] **Struct codec boilerplate is verbose**
      Each field requires `Name`, `Codec`, `Get`, `Set` (+ optional `Required`).
      A 5-field struct = 25+ lines of lambdas. A `SimpleField` helper for the common case
      (no custom getter/setter beyond direct field access) would reduce noise significantly.

### API design

- [ ] **`AddServer` signatures are inconsistent**
      `api/rest`: `b.AddServer(Server)` — no name.
      `api/events`: `b.AddServer(name, Server)` — named.
      Align both to `b.AddServer(name, Server)`.

- [x] **`route.Param` leaks into `api/rest` caller imports** *(done 2026-05-01)*
      Added `type Param = route.Param` alias in `api/rest`.

- [x] **`isBodyMethod` duplicated in `api/rest` and `adapters/nethttp`** *(done 2026-05-01)*
      `adapters/nethttp` now checks `handle.Descriptor.RequestBody != nil` instead of replicating method logic.

- [x] **`cloneStrings` duplicated in `api/rest` and `api/events`** *(done 2026-05-01)*
      Replaced with `slices.Clone` (Go 1.21+ stdlib); helpers removed.

### Spec generation

- [ ] **Schema-to-map rendering logic duplicated across renderers**
      `render/openapi/openapi.go` and `render/asyncapi/asyncapi.go` each have their own
      `schemaObject` function. Adding a new `schema.Schema` field requires updating both
      renderers independently with no compiler enforcement.
      Fix: extract to a shared `internal/schemarender` package.

- [ ] **Dangling `$ref` possible — no build-time check**
      Setting `SchemaName` on a response/operation generates a `$ref` without verifying
      that the named schema is registered in `components`. A typo silently produces an
      invalid spec. The builder should cross-check all `SchemaName` references.

### Adapters

- [ ] **`adapters/nethttp`: no path parameter extraction**
      `GET /users/{id}` always receives zero `Req`. `http.Request.PathValue("id")`
      (Go 1.22+) is available but never used. Options:
      (a) pass `*http.Request` to the handler via context, or
      (b) extend `HandlerFunc` to `func(ctx, r *http.Request, req Req) (Resp, error)`.

- [x] **`adapters/nethttp`: unbounded `io.ReadAll`** *(done 2026-05-01)*
      Wrapped body with `http.MaxBytesReader` (1 MiB default via `maxRequestBodyBytes` const).

- [ ] **`adapters/nethttp`: hardcoded error envelope**
      `{"error":"..."}` is fixed. Add an `ErrorHandler func(w, r, status, err)` option.

- [ ] **`adapters/mqtt`: untyped error callback**
      `onErr func(error)` gives callers no way to distinguish decode errors, validation
      errors, and handler errors without string matching.
      Define a `SubscribeError` type with a `Kind` field (`KindDecode`, `KindHandler`).

