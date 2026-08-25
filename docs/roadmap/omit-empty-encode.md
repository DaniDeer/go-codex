# Omit Zero-Value Fields on Encode — `codex`

> **Status:** Design complete — not yet implemented.
> [← Back to Roadmap](index.md)

## Motivation

`codex.Struct[T]`'s `Encode` unconditionally writes EVERY declared field into
its output map — `RequiredField`, `OptionalField`, and `DefaultField` alike.
This is documented, deliberate behavior (see `docs/concepts/codec.md`'s
"Encode note": *"there is no 'omit if zero' logic on encode — every field is
always written to the output object"*). In practice this means re-encoding a
value that never populated some optional fields still renders them at their
Go zero value — `build: null`, `command: []`, `domainname: ""`, `volumes: []`
— noisy, sometimes misleading wire output that a hand-authored file would
never contain.

The only existing escape hatch, `PartialField`/`PartialStruct`, already solves
"omit unset" — but it requires converting **every** field of the struct to a
pointer (`*F`), a shape appropriate for a dedicated "patch"/"partial update"
type, not for retrofitting onto an ordinary value-typed domain struct (e.g.
`examples/go-edge-models/models/docker/dockercompose.Service`) just to reduce
noise on a handful of fields.

This feature gives an **opt-in, per-field** way to omit a zero-valued field
from `Encode` **without changing the struct's field types**, usable
side-by-side with `RequiredField`/`OptionalField`/`DefaultField` in the same
`Struct(...)` call.

## Critical design constraint — zero-value ambiguity

Comparing a field's current value to Go's zero value (`""`, `0`, `nil`, `[]`)
to decide "omit it" is **fundamentally ambiguous** whenever the zero value is
ALSO a legitimate, deliberately-chosen value (e.g. `Retries: 0` meaning
"explicitly no retries", not "field never touched"). This is the exact same
long-standing criticism of `encoding/json`'s `omitempty` tag, and it is **not
solvable** without switching to pointer semantics — which is precisely what
`PartialField`/`PartialStruct` already does, at the cost of restructuring the
whole type.

**Hard usage rule (not a soft suggestion):** this mechanism must be used ONLY
for fields whose own documented type convention already treats the Go zero
value as a first-class "absent" sentinel — mirroring the pattern already
established in this codebase for `dockercompose.Build.Context==""`,
`MemLimit==0`, `Healthcheck==Healthcheck{}`. It must NEVER be used for a field
where the zero value is itself meaningful, distinguishable data (e.g. a
`Priority int` where `0` is a real priority level) — for that case
`PartialField`/`PartialStruct` (pointer-based) remains the correct, and only
correct, tool.

## Why not modify `OptionalField`/`DefaultField` directly

An obvious alternative to adding new constructors would be to simply change
`OptionalField` (and/or `DefaultField`) to omit a zero/default-equal value on
encode by default. **Rejected, for both, for concrete reasons:**

### `OptionalField`

1. **Blast radius is large and mostly outside this feature's control.**
   `OptionalField` is used in 35 non-test `.go` files today across
   `api/rest/builder.go`, `ports/pattern.go`, and every `go-edge-models`
   model package — `dockercompose.serviceFieldsCodec` alone declares 15
   `OptionalField`s (`image`, `build`, `ports`, `volumes`, `environment`,
   `command`, `entrypoint`, `hostname`, `domainname`, `restart`,
   `healthcheck`, `mem_limit`, `mem_reservation`, `ulimits`, plus the nested
   `Build` object's own `dockerfile`/`args`/`target`). Flipping
   `OptionalField`'s encode behavior would silently change wire output for
   EVERY one of those existing consumers overnight — not a compile-time
   break a caller could catch and fix, but a silent WIRE FORMAT change
   (REST response bodies, AsyncAPI examples, YAML files written to disk) for
   every existing caller, with no migration path.
2. **`OptionalField` is also used where zero is legitimate, meaningful
   data.** A `Retries int`/`MinInstances int` field declared `OptionalField`
   may deliberately be `0` — `OptionalField` itself has no way to know which
   of its 35 current uses fall into "zero means absent" vs. "zero is real
   data." Only the field's OWN domain convention knows that, which is
   exactly why this feature is scoped as an explicit, per-field opt-in, never
   a blanket default.
3. **Violates this library's explicit-over-implicit philosophy.**
   `.github/instructions/go-codex.instructions.md`'s Design Philosophy states
   "Codecs are values, not magic" — every behavioral choice in go-codex
   (Required vs. Optional vs. Default, Nullable vs. plain, Strict vs.
   permissive) is a DECLARED, per-field choice, never an automatic inference
   from the field's current runtime value.

**Conclusion:** `OptionalField` keeps its current "always encode the Go zero
value" contract unchanged. `OmitEmptyField`/`OmitEmptyFieldFunc` remain
separate, opt-in constructors.

### `DefaultField`

Comparing "current value == the DECLARED default" is *narrower* and *less
ambiguous* than comparing to Go's blind zero value — `DefaultField` already
requires the codec author to explicitly name what "nothing new to say" means
for this field, so equating that declared value with "omit" is a more
defensible inference than an arbitrary zero-value guess. **But the same two
blocking concerns still apply:**

- Still can't distinguish "never touched, still at default" from
  "explicitly reset back to exactly the default value" — a caller who
  explicitly writes `log_level: info` back to a config file (where `"info"`
  happens to be the declared default), e.g. for auditability of a generated
  config, may WANT that explicit — silently omitting it changes their file's
  content without consent.
- `DefaultField` is used today (e.g. `config/env.go`, `examples/env-config`)
  specifically to always resolve AND always show the effective value when a
  config is re-serialized (the "print the fully-resolved config" use case).
  Flipping its encode behavior would break that use case for every existing
  caller.

**Conclusion:** do not modify `DefaultField` either. Instead, add ONE new,
purpose-built sibling constructor that composes the two existing behaviors
additively — see `OmitDefaultField` in the API surface below.

## Reflect-based `IsZero()` detection, in detail

### How `reflect.Value.IsZero()` actually works (Go 1.13+)

`reflect.ValueOf(v).IsZero()` performs a KIND-aware zero check, recursively
where needed, with NO `comparable` constraint required at the Go type-system
level — this is what makes it tempting: it works for slices, maps, funcs,
and other types Go's `==` operator flatly refuses to compile for.

| Kind | Zero check |
|---|---|
| Numeric (`Int*`/`Uint*`/`Float*`/`Complex*`) | `== 0` |
| `Bool` | `== false` |
| `String` | `== ""` |
| `Slice`/`Map`/`Chan`/`Func`/`Ptr`/`UnsafePointer` | `v.IsNil()` — **NIL check, not length/emptiness check** |
| `Interface` | underlying value is nil |
| `Array` | every element is recursively zero |
| `Struct` | every field is recursively zero |

A minimal wrapper is needed because a generic `F any` boxed through `any` for
a nil interface produces an **invalid** `reflect.Value`, and calling
`.IsZero()` on an invalid Value panics:

```go
func isZeroReflect[F any](v F) bool {
    rv := reflect.ValueOf(v)
    if !rv.IsValid() {
        return true // nil interface / untyped nil
    }
    return rv.IsZero()
}
```

### A genuinely valuable correctness insight this surfaces

`reflect.IsZero()`'s slice/map rule is **"is it nil,"** not **"is its length
zero."** This is actually MORE correct than the naive
`isEmpty: func(s []T) bool { return len(s) == 0 }` predicate originally
sketched for the `Func` form's test plan: a `len(s)==0` check conflates two
genuinely different wire states —

- `Command == nil` — the field's key was ABSENT on decode (exactly what
  `OptionalField`'s own decode already produces for a missing key: the `set`
  function is never called, leaving Go's zero value, `nil`) — this IS the
  "nothing to say" case that should be omitted.
- `Command == []string{}` — the wire EXPLICITLY decoded a present, empty
  array (a real `"command": []` key) — a DIFFERENT wire state than "key was
  never there." `reflect.IsZero()` correctly preserves this distinction
  (`IsNil()` is `false` for a non-nil empty slice); a plain `len(s)==0`
  predicate would silently erase it.

**Action regardless of the reflection decision below:** the test plan's
slice-predicate example is corrected to demonstrate a nil-check (`s == nil`),
not `len(s) == 0` — see the Unit test plan table.

### Why this should NOT become a default/implicit mechanism

1. **Directly contradicts a foundational, load-bearing design rule.**
   `.github/instructions/go-codex.instructions.md`'s Design Philosophy states,
   verbatim: *"No reflection, no struct tags for codec logic; all wiring is
   explicit in Go code."* This is one of five bullets defining what
   go-codex fundamentally IS, and part of what differentiates it from
   `encoding/json`'s reflect-and-struct-tag model. Baking `reflect.IsZero()`
   into a DEFAULT code path inside `OmitEmptyField` would violate that
   guarantee for the first time anywhere in `codex/`.
2. **Silent correctness mismatch vs. domain-specific "empty" rules.** A
   blind structural zero-check knows NOTHING about a type's own business
   meaning of "unset." This session's own `dockercompose.Build` is the
   counter-example: `Build.IsSet()` is deliberately defined as `Context !=
   ""` ONLY — a `Build{Dockerfile: "custom.Dockerfile"}` (Context empty,
   Dockerfile set) is `IsSet() == false` by this package's own documented
   convention, but `reflect.ValueOf(b).IsZero()` would return `false` too
   (since `Dockerfile` is non-zero) — the two checks actively disagree. A
   generic reflect default would silently do the WRONG thing for any type
   with this kind of asymmetric "empty" rule, with no compiler or test
   signal unless someone specifically checks for it.
3. **Real performance cost on a hot path.** `reflect` calls are measurably
   slower than a `==` comparison or a direct `len()`/`nil` check.
   `Struct[T].Encode`/`Decode` are exactly the hot path
   `docs/roadmap/fuzz-benchmark-testing.md` already flags for benchmarking
   (`b.ReportAllocs()` on `Struct` encode/decode) — making reflection the
   default path for every `OmitEmptyField` call would regress that benchmark
   silently for a widely-used primitive, not an opt-in one.

### Resolution: expose it, but only as an explicit, named, opt-in helper

Rather than rejecting the idea outright, ship ONE small reusable utility
function a caller can explicitly choose to pass as the `isEmpty` predicate —
reflection stays 100% visible and OPT-IN at the call site, never hidden
inside a constructor's default behavior. See `codex.IsZeroValue` in the API
surface below. This resolves the "ship `comparable` shorthand vs. `Func`
predicate form" open question too: keep BOTH `OmitEmptyField[T, F
comparable]` (fast `==`, no reflection, no lambda at the call site — the
ergonomic default for scalars) AND `OmitEmptyFieldFunc[T, F any]` (the
necessary escape hatch for slices/maps/domain types) exactly as already
planned; `codex.IsZeroValue` is simply a ready-made predicate a caller can
plug into the `Func` form instead of hand-writing `s == nil` themselves —
zero new constructors beyond it, reflection never automatic.

| In scope | Out of scope |
|---|---|
| `OmitEmptyField[T, F comparable]` — zero-value comparison via `==` | Reflect-based automatic zero-detection AS A DEFAULT/IMPLICIT mechanism inside `OmitEmptyField` itself — see "Reflect-based `IsZero()` detection, in detail" below for why this is explicitly rejected |
| `OmitEmptyFieldFunc[T, F any]` — explicit `isEmpty func(F) bool` predicate (required for slices/maps/custom "empty" like `Build.IsSet()`) | Modifying `OptionalField`'s or `DefaultField`'s own behavior — see "Why not modify `OptionalField`/`DefaultField` directly" below |
| `OmitDefaultField[T any, F comparable]` — decodes like `DefaultField`, omits on encode when current value equals the declared default (additive sibling, NOT a `DefaultField` modification) | A new top-level `SparseStruct`/`OmitEmptyStruct` composer (rejected — see "Why a type-assertion, not a new composer" below) |
| `codex.IsZeroValue[F any]` — an explicit, OPT-IN reflect-based helper a caller may pass to `OmitEmptyFieldFunc`'s `isEmpty` parameter (reflection stays visible at the call site, never a hidden default) | Changing `PartialField`/`PartialStruct` at all — this is a fully separate, additive mechanism |
| Mixing omit-empty/omit-default fields with `Required`/`Optional`/`Default` fields in ONE `Struct(...)` call — additive change to `Struct`'s `Encode` loop only (type-assertion check, fully backward compatible) | Applying this to any specific consuming package (e.g. `dockercompose.Service`) — that is a separate, later decision; this roadmap only covers the general `codex` mechanism |
| Decode behaves EXACTLY like `OptionalField`/`DefaultField` respectively (absent key → Go zero value or declared default; field is never `Required`; no schema `"required"` entry) | |

## Why a type-assertion, not a new composer

`PartialStruct` was deliberately kept as a *separate* composer (not a
modification of `Struct`) because `FieldCodec[T]`'s `encode` signature has no
room for a "was this omitted" signal without a breaking interface change (see
`docs/concepts/codec.md`'s "Why not just extend `OptionalField`?"). This
feature reuses that same insight but resolves it differently: instead of
requiring an entirely separate composer plus a pointer-typed struct (as
`PartialStruct` does), the new field constructors return a value that
implements `FieldCodec[T]` as normal, AND *optionally* an unexported
companion interface (`sparseFieldCodec[T]`) that `Struct`'s `Encode` loop
checks via type-assertion. A `FieldCodec[T]` that doesn't implement the
companion interface (every existing `RequiredField`/`OptionalField`/
`DefaultField`) keeps today's "always write" behavior completely unchanged —
this is purely additive and needs no new top-level function name, and lets a
single `Struct(...)` call freely mix both kinds of field.

## API surface

```go
// sparseFieldCodec is an OPTIONAL companion capability a FieldCodec[T] may
// implement — checked via type-assertion inside Struct's Encode loop only.
// Fields that don't implement it (every existing Required/Optional/Default
// field) keep Struct's current "always write every field" behavior.
type sparseFieldCodec[T any] interface {
    // encodeSparse reports whether this field's key should be OMITTED
    // from the encoded object entirely (present == false) rather than
    // written with its current value.
    encodeSparse(v T) (name string, val any, present bool, err error)
}

// OmitEmptyField declares a field that decodes EXACTLY like OptionalField
// (absent key -> Go zero value, never Required) but is OMITTED from the
// encoded object -- not written as null/""/[]/0 -- whenever its current
// value equals F's Go zero value.
//
// CRITICAL: comparing to the zero value cannot distinguish "never set"
// from "deliberately set to the zero-equivalent value". Only use this for
// a field whose OWN documented convention already treats the zero value
// as a first-class "absent" sentinel (mirrors dockercompose's existing
// Build.Context==""/MemLimit==0/Healthcheck==Healthcheck{} convention).
// Never use it for a field where the zero value is itself meaningful data
// (e.g. a Priority int where 0 is a real, distinct priority level) -- use
// PartialField/PartialStruct instead when that distinction matters.
func OmitEmptyField[T any, F comparable](
    name string, codec Codec[F], get func(T) F, set func(*T, F),
) FieldCodec[T]

// OmitEmptyFieldFunc is OmitEmptyField with an explicit isEmpty predicate
// instead of a zero-value comparison -- required whenever F is not
// comparable (slices, maps) or "empty" means something other than Go's
// zero value.
func OmitEmptyFieldFunc[T, F any](
    name string, codec Codec[F], get func(T) F, set func(*T, F),
    isEmpty func(F) bool,
) FieldCodec[T]

// OmitDefaultField decodes EXACTLY like DefaultField (absent key -> the
// declared default is applied) but is OMITTED from the encoded object
// whenever the field's CURRENT value equals that same declared default --
// giving a "minimal diff" round trip (only fields that differ from their
// default appear on the wire) without changing DefaultField's own,
// currently-relied-upon "always show the resolved value" contract.
//
// Same ambiguity caveat as OmitEmptyField applies: this cannot distinguish
// "never touched" from "explicitly reset to the default" -- use only when
// that distinction doesn't matter for this field. Internally this composes
// the same sparseFieldCodec mechanism with an isEmpty predicate of
// `func(v F) bool { return v == defaultVal }` plus DefaultField's existing
// decode-default logic -- no new mechanism, one new named entry point.
func OmitDefaultField[T any, F comparable](
    name string, codec Codec[F], defaultVal F, get func(T) F, set func(*T, F),
) FieldCodec[T]

// IsZeroValue reports whether v is F's Go zero value, via reflection --
// unlike a plain "==" comparison, this works for ANY type including
// slices/maps/funcs/pointers that Go's comparable constraint excludes, and
// (for slices/maps/pointers specifically) checks NIL-ness, not
// length/emptiness -- so an explicitly-decoded empty slice/map ([]T{},
// map[K]V{}) is correctly treated as NOT zero, distinct from a
// never-populated nil one.
//
// This is go-codex's ONLY use of reflection, and it is entirely opt-in:
// pass it explicitly to OmitEmptyFieldFunc's isEmpty parameter when you
// want generic nil/zero detection without writing your own predicate --
//
//	codex.OmitEmptyFieldFunc("command", codex.SliceOf(codex.String()),
//	    get, set, codex.IsZeroValue)
//
// Domain types with their OWN asymmetric "empty" rule (e.g. Build.IsSet(),
// which checks ONLY Context=="") must still write their own predicate --
// IsZeroValue only knows Go's structural zero value, not business meaning.
func IsZeroValue[F any](v F) bool {
    rv := reflect.ValueOf(v)
    if !rv.IsValid() {
        return true
    }
    return rv.IsZero()
}
```

`Struct`'s `Encode` loop gains a small, backward-compatible check:

```go
for _, f := range fields {
    if sf, ok := f.(sparseFieldCodec[T]); ok {
        name, val, present, err := sf.encodeSparse(v)
        if err != nil {
            errs = append(errs, ValidationError{Field: name, Err: err})
            continue
        }
        if present {
            obj[name] = val
        }
        continue
    }
    name, val, err := f.encode(v) // existing path, unchanged
    if err != nil {
        errs = append(errs, ValidationError{Field: name, Err: err})
    } else {
        obj[name] = val
    }
}
```

`Decode` and `Schema` are unaffected — `OmitEmptyField`/`OmitEmptyFieldFunc`'s
`decode`/`schema` methods mirror `OptionalField`'s exactly (absent key → zero
value, never in the schema's `required` array).

### Interaction with `Template`/`DottedKeyCodec`/`DecodeVars`/`EncodeVars`

`FieldCodec[T]` has FOUR consumers besides `Struct`/`StrictStruct`:
`DottedKeyCodec`, `Template`'s `NewTemplate`/`Fields()`, and
`DecodeVars`/`EncodeVars` (see `codex/dottedkey.go`, `codex/template.go`,
`codex/varfields.go`). None of these call through `Struct`'s Encode loop —
they invoke a field's plain `encode()` method directly. **Resolved design
decision:** the plain `encode()` method these new field types implement (to
satisfy `FieldCodec[T]`) always behaves like a normal, always-present field —
IDENTICAL to what `OptionalField`/`DefaultField` already do — regardless of
the current value. Only `encodeSparse()` (consulted EXCLUSIVELY by `Struct`'s
own Encode loop via the type-assertion) honors the omit rule. This means
`OmitEmptyField`/`OmitEmptyFieldFunc`/`OmitDefaultField` behave exactly like
`OptionalField`/`DefaultField` when (mis)used inside a `Template`/
`DottedKeyCodec`/`DecodeVars`/`EncodeVars` declaration — no silent path/topic
var ever goes missing from a URI/topic build merely because its value
happened to be zero. (In practice this scenario shouldn't arise: path/topic
template vars are conventionally always-required — see the "no `Required`
field on template vars" rule in the review checklist — so there's no real
use case for an omit-empty var; this is a safety/consistency guarantee, not
an expected usage pattern.)

## Structured errors (all implement `slog.LogValuer`)

No new error type is needed. `encodeSparse`'s error return flows into the
same `ValidationErrors`/`ValidationError{Field, Err}` shape `Struct.Encode`
already produces for ordinary fields — both already implement
`slog.LogValuer` (see `codex/errors.go`).

## Observer integration

None needed. `Struct.Encode` has no observer hook today — codec-level
validation observer calls only fire via `stats.ReportErrors` at adapter
boundaries — and this feature doesn't change that.

## Unit test plan

| Test | Verifies |
|---|---|
| `TestOmitEmptyField_EncodeOmitsZeroValue` | zero-valued field → key absent from encoded map |
| `TestOmitEmptyField_EncodeWritesNonZeroValue` | non-zero value → key present with correct value |
| `TestOmitEmptyField_DecodeAbsentKeyLeavesZeroValue` | matches `OptionalField` decode semantics |
| `TestOmitEmptyField_DecodePresentKeyAssigns` | present key decodes normally |
| `TestOmitEmptyField_SchemaNeverRequired` | schema `required` array never includes this field |
| `TestOmitEmptyFieldFunc_SliceNilPredicate` | `s == nil` predicate omits a never-populated slice while correctly KEEPING an explicitly-decoded, non-nil empty slice (`[]string{}`) — the nil-vs-empty distinction |
| `TestOmitEmptyFieldFunc_CustomIsSetPredicate` | e.g. reusing a `Build.IsSet()`-style predicate |
| `TestStruct_MixesOmitEmptyAndRequiredFields` | one `Struct(...)` call combining both kinds; only the omit-empty field is conditionally written |
| `TestStruct_ExistingFieldsUnaffected` | pre-existing `RequiredField`/`OptionalField`/`DefaultField` behavior byte-for-byte unchanged (regression guard) |
| `TestOmitDefaultField_EncodeOmitsWhenEqualToDefault` | current value equals declared default → key absent |
| `TestOmitDefaultField_EncodeWritesWhenDifferentFromDefault` | current value differs from default → key present with actual value |
| `TestOmitDefaultField_DecodeAppliesDefaultWhenAbsent` | mirrors `DefaultField`'s own decode test |
| `TestIsZeroValue_ScalarsAndNilSlicesAreZero` | `0`, `""`, `nil` slice/map/pointer all report zero |
| `TestIsZeroValue_NonNilEmptySliceIsNotZero` | `[]string{}`/`map[string]int{}` report NOT zero — the key correctness case |
| `TestIsZeroValue_NilInterfaceGuard` | boxed nil interface value does not panic, reports zero |
| `TestOmitEmptyField_EncodeVarsIgnoresSparseRule` | an `OmitEmptyField` passed to the PUBLIC `codex.EncodeVars` (which calls a field's plain `encode()`, not `Struct`'s sparse-aware loop) still produces a var for a zero value — confirms the omit rule is `Struct`-loop-exclusive, per package-external test conventions (`codex_test`, matching this repo's existing test file package) |

## Files to create

| File | Responsibility |
|---|---|
| `codex/omitempty.go` | `sparseFieldCodec[T]`, `OmitEmptyField`, `OmitEmptyFieldFunc`, `OmitDefaultField`, `IsZeroValue` |
| `codex/omitempty_test.go` | Unit test plan above |
| `codex/object.go` | Small additive type-assertion check in `Struct`'s `Encode` loop |
| `docs/concepts/codec.md` | New subsection near "PartialField/PartialStruct"; update the "Encode note" callout to cross-reference this feature |
| `.github/instructions/go-codex.instructions.md` | Document new exported symbols |

## Out of scope (Phase 2)

- Reflect-based automatic `IsZero()` detection AS A DEFAULT/IMPLICIT
  mechanism inside `OmitEmptyField` itself — rejected outright, not just
  deferred (see "Reflect-based `IsZero()` detection, in detail" above); the
  scoped-in `codex.IsZeroValue` helper is the correct middle ground and ships
  in Phase 1.
- Modifying `OptionalField`'s or `DefaultField`'s own behavior — rejected
  outright (see "Why not modify `OptionalField`/`DefaultField` directly"
  above); `OmitDefaultField` (additive sibling) ships in Phase 1 instead.
- An `IsZeroer`-style interface (a la `time.Time.IsZero()`) letting a type
  declare its own custom zero-check that `OmitEmptyField` would consult
  automatically — a plausible Phase 2 idea if `codex.IsZeroValue`'s blind
  structural check proves insufficient in practice, but not designed here.
- Applying this mechanism to any specific consuming package (e.g.
  `examples/go-edge-models/models/docker/dockercompose.Service`'s
  `Build`/`Command`/`Entrypoint`/`Domainname`/`Volumes` fields, which are good
  real-world candidates since that package already treats their zero values
  as "absent" by convention) — a separate decision to make once this general
  mechanism ships.

## Open design decisions (to resolve before/during implementation)

- Exact final naming: `OmitEmptyField`/`OmitEmptyFieldFunc` (chosen for
  familiarity with `encoding/json`'s `omitempty` vocabulary) vs. `SparseField`
  (chosen to avoid implying JSON-only semantics). **Resolved: `OmitEmptyField`**
  (and, by the same reasoning, `OmitDefaultField`/`IsZeroValue`).
- Whether to ship the `comparable`-constrained shorthand at all, or only the
  `Func` predicate form (simplicity vs. ergonomic parity with
  `RequiredField`/`DefaultField`'s own shorthand-vs-explicit pattern).
  **Resolved: ship both**, mirroring existing precedent — `codex.IsZeroValue`
  additionally covers the "I don't want to write my own predicate for a
  slice/map" case without requiring a third constructor.
