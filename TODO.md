Public API:

- [x] How to use this codec in a GO CLI scenario? Discuss this and add an example to the README since GO is a favourite language for CLI tools. The obvious use case are config files that CLI tools always read (YAML/TOML). But is codec also applicable for parsing the command line inputs with commands, flags, arguments?
      Added "CLI Tools" section to README: codec suits config file decoding (YAML/TOML/JSON) with free validation and schema docs. cobra/pflag recommended for flag/arg parsing.

Error handling:

- [x] The error model is too simple
      Implemented `codex.ValidationError` (single field error: `Field string`, `Err error`) and `codex.ValidationErrors` (`[]ValidationError`) in `codex/errors.go`. Struct decode now collects ALL field errors before returning. Callers use `errors.As(err, &ve)` to extract `ValidationErrors`.

Schema:

- [x] Schema is underpowered
      Added: `additionalProperties: false` (`AdditionalProperties *bool`), `nullable: true` (`Nullable bool`), `discriminator` (`Discriminator *DiscriminatorSchema`). Numeric bounds (`Minimum`/`Maximum`/Exclusive*), string constraints (`MinLength`/`MaxLength`/`Pattern`) were already present. All rendered via shared `render/internal/schemarender`.

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

- [x] **TaggedUnion has no discriminator in generated schema** *(done)*
      `codex.TaggedUnion` now sets `Schema.Discriminator = &schema.DiscriminatorSchema{PropertyName: tag}`. Rendered in both `render/openapi` and `render/asyncapi` via shared `render/internal/schemarender`.

### Codec

- [x] **Encode does not validate — silent footgun** *(done — via Codec.Validate)*
      `format.Format[T]` already exposes `Validate(T) error` that runs all `Refine` constraints. Document that publish-side callers must call `Validate` before `Marshal`. Optionally add `ValidateOnMarshal bool` in the future.

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

- [x] **`AddServer` signatures are inconsistent** *(done 2026-05-01)*
      `api/rest.AddServer` now takes `(name string, s Server)`. `name` used as `Description` if empty. Both builders consistent.

- [x] **`route.Param` leaks into `api/rest` caller imports** *(done 2026-05-01)*
      Added `type Param = route.Param` alias in `api/rest`.

- [x] **`isBodyMethod` duplicated in `api/rest` and `adapters/nethttp`** *(done 2026-05-01)*
      `adapters/nethttp` now checks `handle.Descriptor.RequestBody != nil` instead of replicating method logic.

- [x] **`cloneStrings` duplicated in `api/rest` and `api/events`** *(done 2026-05-01)*
      Replaced with `slices.Clone` (Go 1.21+ stdlib); helpers removed.

### Spec generation

- [x] **Schema-to-map rendering logic duplicated across renderers** *(done)*
      Extracted to `render/internal/schemarender`. Both `render/openapi` and `render/asyncapi` delegate to `schemarender.SchemaObject`. New `schema.Schema` fields need only one update.

- [x] **Dangling `$ref` possible — no build-time check** *(done)*
      `api/rest.Builder.OpenAPISpec()` and `api/events.Builder.AsyncAPISpec()` now cross-check all SchemaNames. Added `AddSchema(name, schema)` to both builders for registering reusable schemas. Returns error listing unregistered names.

### Adapters

- [x] **`adapters/nethttp`: no path parameter extraction** *(done 2026-05-01)*
      `RequestFromContext(ctx) (*http.Request, bool)` added. Handler stores `*http.Request` in context via `contextKey{}` before calling fn.

- [x] **`adapters/nethttp`: unbounded `io.ReadAll`** *(done 2026-05-01)*
      Wrapped body with `http.MaxBytesReader` (1 MiB default via `maxRequestBodyBytes` const).

- [x] **`adapters/nethttp`: hardcoded error envelope** *(done 2026-05-01)*
      Added `Options{ErrorHandler}` and `HandlerWithOptions`/`RegisterWithOptions`. `Handler` keeps old signature with default JSON envelope.

- [x] **`adapters/mqtt`: untyped error callback** *(done 2026-05-01)*
      Added `ErrorKind` (`KindDecode`, `KindHandler`) and `SubscribeError{Kind, Topic, Err}`. `onErr` changed to `func(SubscribeError)`.

