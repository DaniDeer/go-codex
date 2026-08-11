# Partial/Patch Struct Codec — `codex`

> **Status:** Design draft — awaiting direction (keep as roadmap vs. implement now).
> [← Back to Roadmap](index.md)
>
> Motivated by a concrete consumer: `examples/go-edge-models/models/iotedge/modulepatch.ModuleFieldsPatch`/`ModuleFieldsPatchCodec` (hand-rolled today).

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
| `PartialStruct[T any](fields ...PartialFieldCodec[T]) Codec[T]` — sparse Encode (omit nil fields), decode-only-what's-present, all-optional Schema | JSON Merge Patch (RFC 7396) semantics — no "explicit `null` means delete this field" distinction; Phase 1 only supports "omit = untouched", not "set = remove" |
| Recursive nesting: a `PartialStruct`-built `Codec[F]` used as another `PartialField[Outer, F]`'s `codec` — parent auto-omits the wrapping key when the nested result is an empty `map[string]any` | Deep JSON-Pointer-style partial paths (e.g. patching `a.b.c` without declaring `b`'s full shape) — every nesting LEVEL must still be an explicitly declared `PartialStruct` |
| Reuse of `ports.PatchEncoded` UNCHANGED — a `PartialStruct`-built `Codec[P]` is just another `codex.Codec[P]`, needs zero adapter/ports changes | New `ports`-level "patch port" abstraction — `PatchEncoded` already generalizes over any patch codec |
| Refactor `examples/go-edge-models/models/iotedge/modulepatch.ModuleFieldsPatch`/`Codec` to use the new primitives, as the flagship real-world consumer (proves the design against a real, already-shipped, already-tested use case) | Refactoring any OTHER existing hand-rolled sparse codec in the repo (none currently exist besides this one) |

## API surface

New file: `codex/partial.go`.

```go
// PartialFieldCodec is the sealed interface [PartialStruct] composes — the
// "may be entirely absent from the encoded object" counterpart to
// [FieldCodec]. Implemented by the value [PartialField] returns.
type PartialFieldCodec[T any] interface {
    // encode reports whether this field is present on v (i.e. its backing
    // pointer is non-nil, AND — for a nested PartialStruct-backed field —
    // that nested encode did not itself collapse to an empty object).
    // present == false means: omit this key from the encoded object
    // entirely (not null, not zero-value — absent).
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
//
// Recursive nesting: pass a PartialStruct-built Codec[F] as another
// PartialField's codec (for a nested "settings"-shaped sub-object, for
// example) — if that nested encode produces an empty map[string]any (no
// sub-fields were set), the OUTER field is ALSO omitted automatically, so
// sparseness collapses correctly through nesting with no extra
// bookkeeping at each call site.
func PartialStruct[T any](fields ...PartialFieldCodec[T]) Codec[T]
```

### Why NOT reuse `FieldCodec[T]`/`OptionalField` directly

`FieldCodec[T]`'s sealed `encode` method returns `(string, any, error)` —
no room for a "was this actually set" signal without either (a) a
breaking interface change touching every existing `Field[T,F]` consumer,
or (b) an ad-hoc sentinel value threaded through `any`, which is exactly
the kind of implicit-behavior hack this library's design explicitly
avoids elsewhere. A parallel, purely ADDITIVE interface
(`PartialFieldCodec[T]`) with its own 4-return `encode` avoids touching
`FieldCodec`/`Struct` at all — zero risk to any existing `codex.Struct`
consumer in the repo or downstream.

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
| `TestPartialField_NestedPartialStruct_CollapsesEmptyToOmitted` | A field whose `Codec[F]` is itself `PartialStruct`-built, with ALL its own sub-fields nil, causes the OUTER field to be omitted too (recursive collapse) |
| `TestPartialField_NestedPartialStruct_IncludesWhenAnySubFieldSet` | Same nested setup, but one sub-field IS set — outer field IS present, containing only that one sub-key |
| `TestModuleFieldsPatch_RefactoredCodec_MatchesOldBehavior` (flagship consumer, in `examples/go-edge-models`) | The refactored `ModuleFieldsPatchCodec` (built on `PartialStruct`/`PartialField`) produces byte-identical `map[string]any` output to the CURRENT hand-rolled version, for every existing test case in `modulefieldspatch_test.go` |

## Files to create / change

| File | Responsibility |
|---|---|
| `codex/partial.go` | NEW — `PartialFieldCodec[T]`, `PartialField`, `PartialStruct` |
| `codex/partial_test.go` | NEW — full unit test plan above (core-level tests, not tied to go-edge-models) |
| `.github/instructions/go-codex.instructions.md` | `codex` package summary row gets `PartialField`/`PartialStruct` mention; new "Partial/patch structs" subsection near the existing `Struct[T]`/`HasCodec[T]` sections |
| `docs/concepts/codec.md` | New subsection (mirrors the `HasCodec[T]` subsection's style) — the "why not `OptionalField`" explanation belongs here, front and center, so a future reader doesn't rediscover the same limitation |
| `examples/go-edge-models/models/iotedge/modulepatch/modulefieldspatch.go` | REFACTOR (flagship consumer) — replace the hand-rolled `Encode`/`Decode` closures with `PartialField`/`PartialStruct` declarations; keep the OUTER `MapCodecSafe` wrapper (ModuleName-keyed `$edgeAgent`/`modulesContent` nesting) and `EmptyPatchError` exactly as they are today — those parts are inherent to the domain's wire shape, not something `PartialStruct` replaces |
| `examples/go-edge-models/models/iotedge/modulepatch/modulefieldspatch_test.go` | Existing tests must all still pass UNCHANGED after the refactor (proves behavior-preserving) |

## Open design decisions

1. **Naming**: `PartialField`/`PartialStruct` (TypeScript `Partial<T>`
   association, matches how the user described the idea) vs.
   `PatchField`/`PatchStruct` (emphasizes the PatchEncoded use case
   directly) vs. `SparseField`/`SparseStruct` (emphasizes the
   encode-time mechanism). Leaning `PartialField`/`PartialStruct` —
   resolve before implementation.
2. **Nested-empty-collapse heuristic**: checking `len(encodedMap) == 0`
   via a type assertion to `map[string]any` is a bit "sniffing the
   result shape" rather than an explicit signal. An alternative: give
   `PartialFieldCodec`'s hidden interface an extra marker so a
   `PartialStruct`-built `Codec[F]` could self-report "I produced an
   empty patch" more explicitly (e.g. a package-level type check `codec,
   ok := any(codec).(interface{ isEmptyPartial(F) bool })`). The
   type-assertion approach is simpler and has no extra API surface;
   worth confirming it doesn't produce surprising behavior for a
   legitimately-empty-but-meaningful `map[string]any` result from an
   unrelated (non-Partial) `Codec[F]` — e.g. a `StringMap[V]` field that
   happens to be empty would ALSO collapse/omit under this heuristic,
   which may or may not be desired. Needs a decision: restrict the
   collapse check to ONLY `Codec[F]` values actually built by
   `PartialStruct` (requires an internal marker), or accept the broader
   "any empty map collapses" behavior as intentional and document it.
3. **Should Phase 1 also add a `PartialFrom`/similar bridge that lets a
   caller build a `PartialStruct`'s pointer fields FROM an existing full
   `T` value (diffing against a base/previous value to auto-populate only
   the CHANGED fields)?** Not requested, not scoped into Phase 1 — flagged
   here since "diff two structs into a patch" is the natural next question
   once patch construction itself is easy. Would need per-field equality,
   which `comparable`-constrained `F` could support generically, but many
   `F` types here (`docker.Image`, `docker.CreateOptions`) are structs
   with slice fields (not `comparable` in Go's strict sense) — would need
   `reflect.DeepEqual` (reflection) or `Codec[F]`'s own `Encode` result
   compared by value — an open question, not resolved here.
