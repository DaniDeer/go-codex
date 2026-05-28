## Open items

### Codec

- [ ] **Lazy[T] / recursive types** *(deferred — complex)*
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

- [ ] **`schema.Schema` zero-value proxy** *(technical debt — low risk)*
      Several places use `Schema.Type == ""` as a proxy for "schema not explicitly set"
      (e.g. `mergePathParams` in `api/rest/builder.go`, `buildTopicParameters` in
      `api/events/builder.go`). This is fragile: `schema.Schema` contains a `[]Property`
      slice so `==` panics, and adding new fields to `schema.Schema` could silently break
      the proxy logic.
      Proposed fix: add `func (s Schema) IsZero() bool` to `schema/schema.go` and replace
      all `Schema.Type == ""` checks with `s.IsZero()`. Low-risk, self-contained change.

### Spec generation

- [ ] **`$ref` auto-deduplication** *(deferred — high risk)*
      Repeated schemas (e.g. a shared `Address` codec used in multiple places) are inlined
      everywhere rather than deduplicated into `components/schemas`.
      Requires schema canonicalization, a naming registry, a second render pass, and cycle
      detection. Changes output shape in potentially surprising ways.
      Current workaround: explicit `SchemaName` on `Body`/`Response` + `AddSchema` on the
      builder — intentional and sufficient for the common case.

- [ ] **`$ref` for path/topic parameter schemas** *(deferred — low priority)*
      Path parameter schemas (from `PathParamCodecs`) and AsyncAPI channel parameter schemas
      (from `TopicParamCodecs`) are always inlined in the spec output. For projects that
      reuse the same parameter codec across many routes (e.g. a UUID codec for dozens of
      `{id}` parameters), this produces repetitive YAML.
      This is only noticeable at scale and requires the `$ref` auto-deduplication machinery
      to be in place first; block on that item.

### API builders

- [ ] **Generic type inference on body-less routes** *(watch — Go limitation)*
      `AddRoute[struct{}, Resp]` and `AddChannel[T]` are ergonomic for typed payloads, but
      Go's type inference occasionally fails to infer `struct{}` as the `Req` type parameter
      without an explicit annotation. This is a Go compiler limitation, not a library bug;
      the workaround is to state the type parameters explicitly:
      `rest.AddRoute[struct{}, User](b, "GET", "/users/{id}", ...)`.
      Track Go release notes for improvements to partial type argument inference.

---

## Completed (reference)

Items are removed when done. See git log for implementation history.
