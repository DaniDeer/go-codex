# `Maybe[Nullable[T]]` (Three-State Fields)

> **Status:** Idea only — not yet designed in detail, captured as a
> reminder. No concrete driving use case yet.
> [← Back to Roadmap](index.md)
>
> This doc originally tracked TWO follow-ons to the shipped
> `codex.Maybe[T]`. **Idea 2 (a public `codex.Codec[Maybe[T]]`) has
> SHIPPED** as `codex.MaybeCodec[T]` — see `docs/concepts/codec.md`'s
> "`Maybe[T]`: definitive presence tracking" subsection and
> `.github/instructions/go-codex.instructions.md`'s matching section for
> the shipped design (including how `MaybeField` is now a documented,
> test-proven special case of `OmitEmptyFieldFunc(name, MaybeCodec(codec),
> get, set, isEmpty)`, and how `MaybeCodec` mirrors `Either2`'s existing
> role exactly). Only Idea 1 below remains open.

## Idea 1: three-state fields via `Maybe[Nullable[T]]`

`OptionalField`+`Nullable[T]` today cannot distinguish "key absent" from
"key present with explicit null" — both produce a `nil` `*T` (see
`docs/concepts/codec.md`'s `Maybe[T]` section for the concrete,
already-shipped example this was found in: `RequiredField`+`Nullable` is
the only combination that avoids the ambiguity, since a required key is
guaranteed present).

Composing `Maybe[Nullable[T]]` — i.e. a `Maybe` field whose inner `V` is
itself a `*T` produced by `Nullable` — could give a genuine THREE-state
field:

- **Absent** (`Nothing`) — the key was never in the wire object at all.
- **Present, null** (`Just(nil)`) — the key was present with an explicit
  JSON/YAML `null`.
- **Present, value** (`Just(&v)`) — the key was present with a real value.

This is exactly the shape [RFC 7396 JSON Merge
Patch](https://datatracker.ietf.org/doc/html/rfc7396) needs (absent =
don't touch this field; null = clear/delete it; value = set it) — and
would be cheaper to express than the "double-pointer" shape a naive
`PartialField(*T)` would otherwise require for the same three states.

**Verified: this already composes for free, zero new code needed.**
Confirmed via a throwaway experiment (`MaybeField("note",
codex.Nullable(codex.String()), get, set)` on a `Maybe[*string]` field):
absent → `Nothing` (key omitted on re-encode); `"note": null` → decodes as
`Just(nil)`; `"note": "hi"` → decodes as `Just(&"hi")` — genuinely
distinct, independently-observable states, exactly the three-state shape
RFC 7396 needs. `MaybeField`'s `codec` parameter already accepts any
`Codec[V]`, and `Nullable(inner)` is just a `Codec[*T]` like any other —
no special-casing anywhere in the pipeline.

**What's left, not yet scoped:**

- **No new constructor is needed** — this shrinks to a DOCUMENTATION-only
  follow-on: add a worked `Maybe[Nullable[T]]` example + a dedicated test
  to `codex/maybe_test.go`, and a short subsection in `docs/concepts/codec.md`
  demonstrating the three-state pattern explicitly (today a reader would
  have to independently realize this composition exists).
- Whether real go-codex callers (this repo's own examples, or
  `examples/go-edge-models`) have an actual PATCH/merge-patch use case
  that would justify writing that example — currently speculative; a
  concrete driver would upgrade this from "document an existing
  composition" to "build a dedicated example/guide around it."
