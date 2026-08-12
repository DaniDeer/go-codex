# Partial/Patch Struct Codec — `codex`

> **Status:** Design refined (v2) — awaiting direction (keep as roadmap vs. implement now).
> [← Back to Roadmap](index.md)
>
> Motivated by a concrete consumer: `examples/go-edge-models/models/iotedge/modulepatch.ModuleFieldsPatch`/`ModuleFieldsPatchCodec` (hand-rolled today).
>
> **v2 revision note**: go-codex currently has exactly one consumer
> (this repo), so breaking changes are acceptable whenever they make the
> API simpler or easier to maintain — the only hard requirement is a
> simple, declarative, consistent workflow for the (single) user. This
> revision uses that freedom to REMOVE the "nested-empty-collapse
> heuristic" open design decision entirely (see below) and to restructure
> `ModuleFieldsPatch` itself for full internal consistency, rather than
> picking one of the original two heuristic options.

## Motivation

`examples/go-edge-models` needed a "patch one module's fields" mechanism:
a caller builds a value with only SOME fields set (image, status,
restart policy, ...), and only those fields get written into the real
manifest file — everything else, on that module and elsewhere in the
document, stays untouched. This was implemented by hand
(`modulepatch.ModuleFieldsPatch` + a ~150-line hand-rolled
`codex.Codec[T]{Decode, Encode}` that manually builds/reads a sparse
`map[string]any`), because **`codex.Struct[T]`'s `Encode` unconditionally
writes every declared field into its output map** (`RequiredField` and
`OptionalField` alike) — there is no built-in "omit this field from the
encoded object when it was never set" behavior.

The question this roadmap answers: **can go-codex generalize "patch an
existing struct" into a reusable core primitive**, so a caller doesn't
have to hand-write the sparse Encode/Decode logic every time, and instead
"inherits" each field's already-declared codec (e.g. reusing
`iotedge.ImageCodec`/`iotedge.StatusCodec` unchanged) — the same way
`codex.Struct` itself lets a caller compose already-declared field codecs
into a new struct?

**Important scope boundary, established up front**: this is NOT a
reflection-based "derive `Partial<T>` from an arbitrary existing `T`"
mechanism (like TypeScript's `Partial<T>` utility type). go-codex has no
reflection and no struct tags (see
`.github/instructions/go-codex.instructions.md`'s existing "no
auto-derived constructor" decision, which rests on the same constraint) —
Go generics cannot enumerate an arbitrary struct's fields at compile
time. The caller must still declare the patch struct's shape and field
list by hand (a new `type FooPatch struct { A *X; B *Y }` and a list of
field declarations) — exactly as much declaration work as
`codex.Struct[Foo]` itself already requires for the BASE type. What
*is* achievable, and is the actual value this feature adds: each
patch field's VALIDATION/ENCODING is *inherited* by reusing the exact
same `Codec[F]` value the base struct's own field declaration already
uses — no new validation logic, no re-deriving constraints — and the
"omit when unset" sparseness mechanism itself becomes a reusable,
tested, general building block instead of bespoke per-type code.

## Scope decisions

| In scope (Phase 1) | Out of scope |
|---|---|
| `PartialField[T, F any](name string, codec Codec[F], get func(T) *F, set func(*T, *F)) PartialFieldCodec[T]` — one patchable field, `F`'s codec reused unchanged | Reflection-based auto-derivation of a patch type FROM an existing struct type (no field enumeration without reflection — see Motivation) |
| `PartialStruct[T any](fields ...PartialFieldCodec[T]) Codec[T]` — sparse Encode (omit nil fields entirely, never a `null`/zero placeholder), decode-only-what's-present, all-optional Schema | JSON Merge Patch (RFC 7396) semantics — no "explicit `null` means delete this field" distinction; Phase 1 only supports "omit = untouched", not "set = remove" (confirmed: the IoT-edge use case never needs to REMOVE a field, only ever set new values, so this scoping is correct as-is) |

Note on nesting (not a separate scope decision): a `PartialStruct`-built
`Codec[F]` can be used as another `PartialField[Outer, F]`'s `codec`
(e.g. `ModuleSettingsPatch`'s codec as `ModuleFieldsPatch`'s `"settings"`
field) with ZERO extra mechanism — `PartialField` accepts any `Codec[F]`,
and `PartialStruct` returns a plain `Codec[T]`, so this is ordinary
`Codec[T]` composability (the same way `Struct` already nests inside
`Struct` today), not a feature we implement. See "Nesting is not special"
below for the full explanation. Still out of scope: deep JSON-Pointer-style
partial paths (e.g. patching `a.b.c` without declaring `b`'s full shape) —
every nesting LEVEL must still be an explicitly declared `PartialStruct`
behind its own pointer.
| Reuse of `ports.PatchEncoded` UNCHANGED — a `PartialStruct`-built `Codec[P]` is just another `codex.Codec[P]`, needs zero adapter/ports changes | New `ports`-level "patch port" abstraction — `PatchEncoded` already generalizes over any patch codec |
| Refactor `examples/go-edge-models/models/iotedge/modulepatch.ModuleFieldsPatch`/`Codec` to use the new primitives, as the flagship real-world consumer (proves the design against a real, already-shipped, already-tested use case) | Refactoring any OTHER existing hand-rolled sparse codec in the repo (none currently exist besides this one) |

## API surface

New file: `codex/partial.go`.

```go
// PartialFieldCodec is the sealed interface [PartialStruct] composes — the
// "may be entirely absent from the encoded object" counterpart to
// [FieldCodec]. Implemented by the value [PartialField] returns.
type PartialFieldCodec[T any] interface {
    // encode reports whether this field is present on v — i.e. its
    // backing pointer is non-nil (see "Nesting is not special": for a
    // nested PartialStruct-backed field, presence is STILL just this
    // same nil-check, nothing more). present == false means: omit this
    // key from the encoded object entirely (not null, not zero-value —
    // absent).
    encode(v T) (name string, val any, present bool, err error)
    // decode sets T's field ONLY when name is present in obj — an absent
    // key leaves the corresponding pointer field nil (unset), exactly
    // mirroring how a caller would construct T by hand.
    decode(obj map[string]any, target *T) error
    // schema returns this field's schema — used by PartialStruct to build
    // T's overall Schema. Patch fields are NEVER required (nothing to mark
    // required in a "some subset of fields" shape).
    schema() (string, schema.Schema)
}

// PartialField declares one patchable field of T — T's own field for this
// name MUST be a pointer (*F): nil means "not set, leave untouched" when
// encoding, non-nil means "set to this value". codec is the SAME
// field-level Codec[F] an existing full-struct declaration for this
// concept already uses (e.g. iotedge.ImageCodec, docker.CreateOptionsCodec)
// — reused completely unchanged, "inheriting" that field's own
// constraints/validation with zero new logic.
//
//	type ModuleFieldsPatch struct {
//	    Image  *docker.Image
//	    Status *iotedge.Status
//	}
//	var patchCodec = codex.PartialStruct[ModuleFieldsPatch](
//	    codex.PartialField("image", iotedge.ImageCodec,
//	        func(p ModuleFieldsPatch) *docker.Image { return p.Image },
//	        func(p *ModuleFieldsPatch, v *docker.Image) { p.Image = v }),
//	    codex.PartialField("status", iotedge.StatusCodec,
//	        func(p ModuleFieldsPatch) *iotedge.Status { return p.Status },
//	        func(p *ModuleFieldsPatch, v *iotedge.Status) { p.Status = v }),
//	)
func PartialField[T, F any](
    name string,
    codec Codec[F],
    get func(T) *F,
    set func(*T, *F),
) PartialFieldCodec[T]

// PartialStruct builds a Codec[T] for a "patch"/"partial update" struct —
// every one of T's fields is independently optional (all pointers).
// Unlike [Struct], Encode OMITS the wire key entirely for any field whose
// pointer is nil (never writes a placeholder/null/zero value for an unset
// field), and Decode only assigns fields actually present in the input,
// leaving the rest nil. Schema marks NO fields Required.
func PartialStruct[T any](fields ...PartialFieldCodec[T]) Codec[T]
```

### Nesting is not special (v2 simplification — resolves the original open decision #2)

A "settings"-shaped sub-object (grouping e.g. `Image`+`CreateOptions`
under one wire key) is expressed as an ORDINARY `PartialField[Outer, F]`
where `F` is itself a type whose `Codec[F]` happens to be
`PartialStruct`-built, referenced through the outer struct's own `*F`
pointer:

```go
type ModuleSettingsPatch struct {
    Image         *docker.Image
    CreateOptions *docker.CreateOptions
}
var moduleSettingsPatchCodec = codex.PartialStruct[ModuleSettingsPatch](
    codex.PartialField("image", iotedge.ImageCodec, ...),
    codex.PartialField("createOptions", docker.CreateOptionsCodec, ...),
)

type ModuleFieldsPatch struct {
    ModuleName iotedge.ModuleName
    Settings   *ModuleSettingsPatch // nil = no settings changes at all
    Status     *iotedge.Status
    // ...
}
var moduleFieldsPatchCodec = codex.PartialStruct[ModuleFieldsPatch](
    codex.PartialField("settings", moduleSettingsPatchCodec, ...),
    codex.PartialField("status", iotedge.StatusCodec, ...),
    // ...
)
```

Presence for the `"settings"` key is decided EXACTLY like every other
field: is `ModuleFieldsPatch.Settings` nil? The caller (or a factory like
`NewUpdateModuleImagePatch`) allocates `&ModuleSettingsPatch{Image: &img}`
only when it actually wants to include a settings change — no different
from allocating any other pointer field. `PartialField`'s `encode` needs
NO special case to detect "the nested struct happened to end up empty" —
that scenario cannot arise, because a caller only ever allocates the
nested pointer when they mean to set something inside it. This is
strictly simpler than the original v1 draft's "auto-collapse an
empty-map nested encode result" heuristic (which required sniffing the
encoded shape and risked false-triggering on an unrelated empty
`StringMap[V]` field) — nesting composes for free via ordinary `Codec[F]`
composition, exactly like every other `codex` primitive already does.

### Why NOT reuse `FieldCodec[T]`/`OptionalField` directly

`FieldCodec[T]`'s sealed `encode` method returns `(string, any, error)` —
no room for a "was this actually set" signal without either (a) a
breaking interface change touching every existing `Field[T,F]` consumer,
or (b) an ad-hoc sentinel value threaded through `any`, which is exactly
the kind of implicit-behavior hack this library's design explicitly
avoids elsewhere. A parallel interface (`PartialFieldCodec[T]`) with its
own 4-return `encode` avoids touching `FieldCodec`/`Struct` at all — even
though breaking `FieldCodec` itself is now an acceptable OPTION (single
consumer), a parallel interface remains the better choice on its own
merits: it keeps `Struct`'s well-tested, simple "always-write-every-field"
semantics completely separate from `PartialStruct`'s "omit-when-unset"
semantics, rather than overloading one function/interface with two
different presence models a reader would have to hold in their head
simultaneously. Two clearly-named, single-purpose entry points
(`Struct` for full documents, `PartialStruct` for patches) is the more
declarative and consistent shape for the user, even with breaking changes
on the table.

## Structured errors

**None new.** `PartialStruct`/`PartialField` reuse whatever errors the
per-field `Codec[F]` values already produce on `Decode` failure
(`ValidationError`/`ValidationErrors`, propagated exactly like `Struct`
does today) — no new error type is needed at the core-codec level. The
existing convention of a composing wrapper choosing to return a
domain-specific "empty patch" error (like
`modulepatch.EmptyPatchError` already does today, checking whether the
`PartialStruct`-encoded map came back empty) stays a CALLER concern, not
something `PartialStruct` itself needs to know about (it has no way to
distinguish "an intentionally empty patch" from "a domain-level mistake"
— that's inherently caller/domain-specific).

## Observer integration

**None.** `PartialStruct`/`PartialField` are pure codec-construction/
codec-composition primitives with no I/O — exactly like `Struct`,
`UntaggedUnion`, `TaggedUnion`, and every other `codex` composition
helper, none of which call into `stats.Observer`. Observer integration
happens at the ADAPTER/PORT layer (`ports.PatchEncoded` already reports
via `FileObserver` when applicable) — unchanged by this feature.

## Unit test plan

| Test | Verifies |
|---|---|
| `TestPartialField_Encode_OmitsNilPointer` | A nil-pointer field is absent from the encoded map entirely (not `null`, not present with a zero value) |
| `TestPartialField_Encode_IncludesSetPointer` | A non-nil pointer field encodes via its own `Codec[F]`, present in the map |
| `TestPartialField_Encode_PropagatesFieldCodecError` | An invalid value (fails the reused `Codec[F]`'s own constraint) surfaces that field's own typed error unchanged |
| `TestPartialStruct_Encode_MultipleFieldsSparse` | Encoding a value with 2 of 5 fields set produces a map with exactly those 2 keys |
| `TestPartialStruct_Encode_AllFieldsNil_ReturnsEmptyMap` | All-nil input encodes to `map[string]any{}` (not an error — caller's job to reject if that's meaningless for their domain) |
| `TestPartialStruct_Decode_OnlySetsPresentFields` | Decoding a map with 1 of 3 keys present leaves the other 2 target fields nil |
| `TestPartialStruct_Decode_RoundTrip` | Encode → Decode reproduces the original sparse value exactly |
| `TestPartialStruct_Schema_NoFieldsRequired` | Generated Schema has an empty `Required` list regardless of field count |
| `TestPartialField_NestedPartialStruct_ComposesUnchanged` | A field whose `Codec[F]` is itself `PartialStruct`-built works with NO special-casing: nil outer pointer → omitted; non-nil outer pointer → encodes via the nested `PartialStruct`'s own (possibly further-sparse) result, present under the outer key. Proves nesting needs zero extra mechanism — see "Nesting is not special" |
| `TestModuleFieldsPatch_RefactoredCodec_MatchesNewShape` (flagship consumer, in `examples/go-edge-models`) | The refactored `ModuleFieldsPatchCodec` (built on `PartialStruct`/`PartialField`, with the RESTRUCTURED `Settings *ModuleSettingsPatch` shape — see the flagship-refactor row below) produces the same wire output as the ORIGINAL hand-rolled version for every equivalent case in `modulefieldspatch_test.go`, adjusted for the new `Settings`-nested/`Env`-now-pointer shape (a deliberate breaking change to `ModuleFieldsPatch` itself, acceptable since this repo is go-codex's only consumer) |

## Files to create / change

| File | Responsibility |
|---|---|
| `codex/partial.go` | NEW — `PartialFieldCodec[T]`, `PartialField`, `PartialStruct` |
| `codex/partial_test.go` | NEW — full unit test plan above (core-level tests, not tied to go-edge-models) |
| `.github/instructions/go-codex.instructions.md` | `codex` package summary row gets `PartialField`/`PartialStruct` mention; new "Partial/patch structs" subsection near the existing `Struct[T]`/`HasCodec[T]` sections |
| `docs/concepts/codec.md` | New subsection (mirrors the `HasCodec[T]` subsection's style) — the "why not `OptionalField`" explanation belongs here, front and center, so a future reader doesn't rediscover the same limitation |
| `examples/go-edge-models/models/iotedge/modulepatch/modulefieldspatch.go` | REFACTOR (flagship consumer) + BREAKING RESHAPE of `ModuleFieldsPatch` (acceptable — single consumer): (1) introduce `ModuleSettingsPatch{Image *docker.Image; CreateOptions *docker.CreateOptions}` built via `PartialStruct`, matching `ModuleConfig.Settings`'s own real nesting; (2) `ModuleFieldsPatch.Image`/`CreateOptions` become `ModuleFieldsPatch.Settings *ModuleSettingsPatch`; (3) `ModuleFieldsPatch.Env` changes from `iotedge.EnvVars` to `*iotedge.EnvVars` — removes the ONE field that wasn't already pointer-typed, so "every field is a pointer, nil = untouched" becomes a uniform rule with zero exceptions; (4) the entire hand-rolled `Encode`/`Decode` closures are replaced by declarative `PartialField`/`PartialStruct` composition (both for `ModuleFieldsPatch` and the new `ModuleSettingsPatch`); (5) the OUTER `MapCodecSafe` wrapper (ModuleName-keyed `$edgeAgent`/`modulesContent` nesting) and `EmptyPatchError` stay exactly as they are today — the dynamic module-name key can't be expressed as a fixed field list, so this part correctly remains hand-written |
| `examples/go-edge-models/models/iotedge/modulepatch/modulefieldspatch_test.go` | Updated for the new `Settings`-nested/`Env`-pointer shape; same test INTENT (sparse encode, decode round-trip, empty-patch error) preserved, only field-access syntax changes (e.g. `patch.Settings.Image` instead of `patch.Image`) |
| `examples/go-edge-models/models/iotedge/modulepatch/modulesettingspatch_test.go` | NEW — dedicated tests for `ModuleSettingsPatch`/`ModuleSettingsPatchCodec` (sparse encode of the 2-field group, decode round-trip) |
| `examples/go-edge-models/models/iotedge/modulepatch/modulefieldspatch.go` (`NewUpdateModuleImagePatch`, same file) | Update its ONE hand-built `ModuleFieldsPatch{...}` literal to allocate `Settings: &ModuleSettingsPatch{Image: &image}` instead of the flat `Image: &image` — the only place outside the codec declarations themselves that needs a code change; `app/iotedge.UpdateModuleImage`/`PatchModule` are unaffected (they treat `ModuleFieldsPatch` as opaque and call this factory unchanged) |

## Resolved design decisions (v2)

1. **Naming — RESOLVED**: `PartialField`/`PartialStruct` (matches the
   user's own "partial struct" framing, TypeScript `Partial<T>`
   association is a helpful mental model even though the mechanism
   differs — see Motivation's scope boundary).
2. **Nested-empty-collapse heuristic — RESOLVED BY ELIMINATION**: no
   heuristic is needed at all. Presence is uniformly "is this pointer
   nil", decided explicitly by whoever constructs the value — including
   for nested `PartialStruct`-produced fields (see "Nesting is not
   special" above). This is possible specifically because breaking
   changes are on the table: `ModuleFieldsPatch` itself is restructured
   (`Settings *ModuleSettingsPatch` instead of flat `Image`/
   `CreateOptions`) so that "settings" grouping is expressed as an
   ordinary nested pointer field, not a special case the codec layer
   needs to detect.

## Open design decisions

None remaining for Phase 1. A related but genuinely separate idea —
auto-deriving a patch by diffing two full struct values (`PartialFrom`)
— is split out into its own roadmap entry:
[`partial-struct-diff.md`](partial-struct-diff.md) (idea only, no use
case yet, not part of this feature's scope).
