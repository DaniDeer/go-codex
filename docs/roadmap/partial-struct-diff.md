# `PartialFrom` — Diff Two Structs Into a Patch — `codex`

> **Status:** Idea only — not designed, no use case yet.
> [← Back to Roadmap](index.md)
>
> Follow-on to the now-SHIPPED `codex.PartialField`/`codex.PartialStruct`
> (see `docs/concepts/codec.md`'s "`PartialField`/`PartialStruct`: patching
> an existing struct" subsection and `.github/instructions/go-codex.instructions.md`)
> — split out as its own roadmap entry since it is a genuinely separate
> design question, not part of that feature's Phase 1. That feature's own
> roadmap doc was retired once implemented; this follow-on idea remains open.

## The idea

Once `codex.PartialField`/`codex.PartialStruct` make it easy to
*construct* a patch, the natural next
question is whether go-codex can also help *derive* one: given a
previous/base `T` value and an updated `T` value, auto-populate a
`PartialStruct`-shaped patch type with only the fields that actually
changed between the two — instead of the caller manually comparing
fields and setting pointers by hand.

```go
// Sketch only — not a real signature yet.
func PartialFrom[T, P any](base, updated T, ...) P
```

## Why this isn't scoped into Phase 1

- Not requested by any concrete use case yet (the IoT-edge consumer
  always constructs a patch directly — e.g. "set the image to X" — never
  by diffing two full manifests).
- Needs a real per-field equality answer, and go-codex's usual "no
  reflection" constraint bites here in a new way: many of the concrete
  `F` types involved (`docker.Image`, `docker.CreateOptions`) are structs
  with slice fields, so they are NOT `comparable` in Go's strict generic
  sense — a naive `comparable`-constrained `F` won't compile for them.
  The two realistic options are `reflect.DeepEqual` (reintroduces
  reflection, which every other part of this library deliberately
  avoids) or comparing each field's own `Codec[F].Encode(...)` result by
  value (works without reflection, but only proves the WIRE
  representations are equal, which is usually — but not provably always
  — what "equal" should mean here).

## Next step (when a use case appears)

Write a proper roadmap doc (following the standard template) once there
is a concrete driver — at minimum resolve the equality-mechanism question
above before sketching an API surface.
