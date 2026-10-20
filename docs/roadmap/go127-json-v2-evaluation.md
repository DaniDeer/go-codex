# `encoding/json/v2` evaluation (flag-only) — `format`

> **Status:** Idea only — not designed, no use case yet. Explicitly
> scoped OUT of this round's work.
> [← Back to Roadmap](index.md)
>
> Spun out of a coverage review of
> [Go 1.27 Generic Methods](../design/d-0012-go127-generic-methods.md), which also
> surveyed this toolchain's actual `api/go1.27.txt` for other relevant
> stdlib changes beyond generic methods. This doc is a placeholder
> flagging the idea, not an investigation — see "Why this stays a
> placeholder" below.

## The idea

This toolchain ships stable `encoding/json/v2` and
`encoding/json/jsontext` packages (confirmed directly against
`$(go env GOROOT)/api/go1.27.txt` — includes `jsonv2`-style option
constructors such as `json.OmitZeroWithLegacySemantics`,
`json.FormatByteArrayAsArray`, `json.MatchCaseSensitiveDelimiter`, and a
`DefaultOptionsV1()` escape hatch for exact v1 compatibility). This
repo's `encoding/json` import is not a tiny surface — confirmed via a
repo-wide grep, 36 non-test `.go` files import it today, almost
certainly including go-codex's own JSON `format.Format[T]`
implementation.

## Why this stays a placeholder, not an investigation

Unlike the `uuid` swap or the `synctest` test-only idea, a `json/v2`
migration touches go-codex's own wire-format contract — the exact
rendering of every JSON-codec'd value this library produces/consumes.
That has direct interaction with go-codex-specific semantics already
documented elsewhere (`OmitEmptyField`'s zero-value heuristic,
`PartialField`'s pointer-based omission, `Nullable[T]`'s wire-null
meaning, `MaybeCodec`'s "always-shown" contract) that would all need
individually re-verified against `json/v2`'s own (sometimes
intentionally different) default semantics before any change lands.
That is real design work, not a quick win — it deserves its own
dedicated investigation round, not a few bullet points here.

## Open questions for that future round (not resolved here)

1. Does go-codex's JSON format wrapper (`format/json.go` or
   equivalent) use `encoding/json` directly, or through some
   intermediate layer that would make a `v2` swap more or less
   localized?
2. Which `json/v2` behavior changes (documented in Go's own `json/v2`
   design doc) actually diverge from this codebase's existing,
   documented omit/null/present semantics — if any?
3. Is the motivation performance, stricter semantics, both, or neither
   (i.e., is there a concrete pain point today, or is this purely
   "new stdlib capability exists")? No concrete driver has been
   identified yet.

## Next step

None yet — this stays a flagged idea until a concrete driver (a real
performance problem, a real semantic gap) appears, per this repo's
"Awaiting use case" precedent for other placeholder roadmap entries
(see `vector-store-adapter.md`, `openai-multimodal-content.md`).
