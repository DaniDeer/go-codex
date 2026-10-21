# `encoding/json/v2` evaluation (flag-only) — `format`

> **Status:** Idea only — not designed, no use case yet. Explicitly
> scoped OUT of this round's work. **A later review pass found the
> performance driver is already moot: `encoding/json` is automatically
> v2-backed on this toolchain (`go 1.27.2`) with v1 semantics preserved —
> see open question 3 below.**
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

## Open questions for that future round — partially answered by a
later review pass (still no concrete driver; status unchanged)

1. **Answered — direct, single call site, highly localized.**
   `format/format.go`'s `JSON[T](c codex.Codec[T]) Format[T]` calls
   `json.Marshal`/`json.Unmarshal` directly (2 call sites), and
   `format/embedded.go`'s `EmbeddedJSON[T]` does the same (2 more call
   sites) — 4 total. Crucially, both operate on `any` values that are
   already `map[string]any`/slices/primitives produced by
   `codex.Codec[T].Encode`, never on the user's typed `T` struct via
   reflection or struct tags (confirmed against `codex/object.go`:
   `Struct[T]`/`StrictStruct[T]` build/consume `map[string]any`
   end-to-end — a direct consequence of this library's own "no
   reflection, no struct tags for codec logic" design rule). This means
   `OmitEmptyField`/`PartialField`/`Nullable[T]`/`MaybeCodec`'s
   omit/null/present semantics are ALL decided by codex's own code
   *before* `json.Marshal` ever sees a value — they control map-key
   presence/absence directly, never via a struct tag `json/v2` could
   reinterpret differently.
2. **Narrowed — no risk identified on the CORE wire-format path; 2 new,
   smaller, previously-unflagged risk areas found OUTSIDE it.**
   Because of finding (1), most of `json/v2`'s documented default
   changes (`omitzero` vs `omitempty` semantics, case-sensitive field
   matching, etc.) are struct-tag-specific and simply don't apply to a
   `map[string]any` → `json.Marshal` call. `codex.Bytes()`/`Base64()`
   also pre-convert `[]byte` to a string representation before it ever
   reaches `json.Marshal`, so `json/v2`'s `FormatByteArrayAsArray`
   option (which only affects raw `[N]byte` arrays) is irrelevant here
   too. However, this later pass found the original "36 files import
   `encoding/json`" scope note undercounted the real risk surface: most
   of those 36 are spec/document renderers using plain `map[string]any`
   (`render/openapi`, `render/asyncapi/v2`/`v3` — confirmed zero
   `json:"..."` struct tags anywhere in those packages), but 2 genuinely
   DO use real Go struct tags + reflection-based `encoding/json`, never
   reached by the original investigation:
   - **`forge/hash.go`'s `computeHash`** — hashes `json.Marshal` of a
     real tagged struct (`Name`/`Version`/`Inputs`/`Output`, each with a
     `json:"..."` tag) via SHA-256 to produce every `forge.Function`'s
     stable contract hash (`FunctionSpec.Hash`, rendered into
     `render/pipeline`/`render/stream` YAML). A `json/v2` swap here is a
     genuine **hash-stability risk**: if v2's struct-tag marshaling byte
     output differs AT ALL from v1's for an ordinary tagged struct
     (field order, spacing, escaping), every existing `forge.Function`'s
     computed hash changes on upgrade — a breaking change, not just an
     internal implementation detail. This file should stay pinned to
     `encoding/json` v1 (or its exact byte-for-byte behavior) even if a
     broader migration is ever undertaken elsewhere.
   - **`api/mcp/builder.go` / `api/llm/builder.go` / `api/llm/handle.go`**
     — `MCPSpec`/`CallSpec`/`CallHandle`'s `InputSchema`/`OutputSchema`/
     `RequestSchema`/`ResponseSchema json.RawMessage` fields use real
     `json:"...,omitempty"` struct tags on externally-facing spec
     documents. Lower risk than `computeHash` (no hash-stability
     contract, and `omitempty` on a `json.RawMessage` field is a simple,
     well-trodden case), but still outside the original investigation's
     stated scope ("the library's own JSON `format.Format[T]`
     implementation").
3. **Resolved, and the resolution changes the whole premise of this
   question — the performance driver is already moot.** Verified
   directly against Go's own 1.27 release notes
   (`go.dev/doc/go1.27`, "New encoding/json/v2 and encoding/json/jsontext
   packages" section) and this toolchain's actual `GOEXPERIMENT` state
   (confirmed empty/default — `nojsonv2` is NOT set):

   > "The `encoding/json` package is now backed by the v2
   > implementation. Marshaling and unmarshaling behavior is preserved,
   > but the exact text of error messages may differ. ... Marshal
   > performance is broadly at parity with the previous implementation,
   > while unmarshal performance is significantly faster. Users who
   > encounter compatibility problems ... may disable it by setting
   > `GOEXPERIMENT=nojsonv2` ... This opt-out is expected to be removed
   > in a future release."

   This means, on this repo's OWN `go.mod` floor (`go 1.27.2`),
   **go-codex is already running on the `json/v2` engine today,
   automatically, with v1 semantics preserved and zero code changes** —
   every existing `"encoding/json"` import (`format/format.go`,
   `forge/hash.go`, etc.) already gets the faster unmarshal path for
   free. There is no pending "switch" to wait for on the performance
   axis — it already happened, transparently, the moment `go.mod` moved
   to 1.27.

   **The only thing explicitly importing `encoding/json/v2` would still
   buy is a deliberate SEMANTIC change, not a performance one**: the
   `json/v2` package (used directly, via its own `Marshal`/`Unmarshal`)
   defaults to *rejecting* invalid UTF-8 and *rejecting* duplicate names
   within a JSON object — `encoding/json` v1's API (even though
   v2-backed internally) deliberately preserves the old, more permissive
   defaults (invalid UTF-8 replaced, duplicate keys silently
   last-value-wins) for exactly this compatibility reason. Opting into
   `json/v2`'s stricter decode defaults for untrusted wire input is a
   real, independently-evaluable trade-off — available today, not
   blocked on "waiting for the ecosystem" — but it is a DIFFERENT,
   smaller, and still-driver-free question than the performance one this
   doc originally posed. No correctness bug or complaint currently
   motivates taking on that stricter-decode trade-off either.

## Next step

**No code change recommended.** The free performance win
(`encoding/json` already v2-backed) is already banked with zero action.
Explicitly adopting the `encoding/json/v2` import path only makes sense
if/when there's a deliberate reason to want ITS stricter decode
semantics (duplicate-key rejection, strict UTF-8) for untrusted JSON
input — a distinct, smaller, well-scoped follow-up question from the
performance question this doc originally posed, not a "wait and see"
one. If that question is ever pursued, start from the 2 flagged risk
items above (`forge.computeHash` hash stability — re-confirm its
marshal output is byte-identical before ever switching that file;
`api/mcp`/`api/llm` spec struct tags) rather than re-deriving them from
scratch. Until then, this stays a flagged idea per this repo's "Awaiting
use case" precedent for other placeholder roadmap entries (see
`vector-store-adapter.md`, `openai-multimodal-content.md`).
