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

### Adapters

- [ ] **`adapters/chi` or `adapters/gin`** _(medium effort)_
      `adapters/nethttp` wraps `RouteHandle` for the standard library. Most Go services use a
      router (Chi, Gin, Echo). A Chi adapter is ~50 lines; Gin similar. High discoverability
      value — these are the ecosystems most users land in. Chi is preferred as it builds
      directly on `net/http` with no magic.

---

## Completed (reference)

Items are removed when done. See git log for implementation history.
