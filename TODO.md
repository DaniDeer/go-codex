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

### Spec generation

- [ ] **`$ref` auto-deduplication** *(deferred — high risk)*
      Repeated schemas (e.g. a shared `Address` codec used in multiple places) are inlined
      everywhere rather than deduplicated into `components/schemas`.
      Requires schema canonicalization, a naming registry, a second render pass, and cycle
      detection. Changes output shape in potentially surprising ways.
      Current workaround: explicit `SchemaName` on `Body`/`Response` + `AddSchema` on the
      builder — intentional and sufficient for the common case.

---

## Completed (reference)

Items are removed when done. See git log for implementation history.
