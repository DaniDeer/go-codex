# Validated Value Containers — `Getter[T]`/`Setter[T]`, `Const[T]`, `Immutable[T]` — `codex`

> **Status:** SHIPPED — both phases complete.
> Phase 1: `Getter[T]`/`Setter[T]`/`GetterSetter[T]` (`codex/getter.go`),
> `Const[T]`/`MustConst` and `Immutable[T]`/`NewImmutable`/
> `ImmutableAlreadySetError` (`codex/const.go`) — implemented, tested,
> and documented (`.github/instructions/go-codex.instructions.md`,
> `docs/concepts/codec.md`'s new "Getter/Setter: validated value
> containers built on Codec[T]" subsection).
> Phase 2: `examples/go-edge-models`'s `usecase` package adopted
> `codex.Const[string]` for its 3 non-templated path patterns and
> `codex.Template[T]` (built via `codex.NewTemplate` + `IdentityField`/
> `RequiredField`) for its 3 templated ones — see "Phase 2" below for
> how this superseded an earlier, since-replaced typed-wrapper design.
> **Follow-on:** the "Out of scope (Phase 3)" section's `NewConst[T]`/
> `Mutable[T]` ideas are now designed in their own roadmap doc —
> [Reloadable Value Containers](reloadable-value-containers.md).
> [← Back to Roadmap](index.md)

## Motivation

go-codex validates *runtime* values against a declared `Codec[T]`. But a
great many packages also author **compile-time constants** whose shape
is just as constrained as any wire value — a fixed path template
("usecases/{usecase_name}.json"), a protocol version string, a default
enum value, a magic byte sequence — and today these are just bare
`const`/`var` string/int literals with no validation at all. A typo in
one of these (a missing `{var}` placeholder, an empty pattern, a version
string that doesn't match the expected format) is caught only by luck —
whenever a test happens to exercise that exact literal — not by the same
declarative validation the rest of the library gives every runtime
value.

A closely related but distinct need: **runtime values set exactly
once** — a config value or environment variable read once at process
startup, then treated as read-only for the rest of the process's
lifetime. Unlike a compile-time constant, this value is genuinely
external input (so an invalid value must return an error, not panic the
process), and unlike an ordinary mutable variable, it should be
impossible to accidentally reassign later.

The concrete driver for the constant half: `examples/go-edge-models/models/iotedge/usecase/config.go`
defines six file/dir path PATTERN strings (`baselinePathPattern`,
`useCasePathPattern`, `useCaseEntryShape`, `deviceDirPathPattern`,
`deviceFilePathPattern`, `deviceEntryShape`) as plain package-level
`var`s built via `fmt.Sprintf`. The user wants each pattern modeled as
its own type — pattern text kept **private**, exposed only through a
**getter** — with a validated, concrete-path-producing accessor built on
top (substituting a caller's already-validated `Name`/`DeviceID` into
the template), plus a `String()` method exposing the raw,
unsubstituted pattern text (e.g. for the existing `ports.NewFile`/
`ports.NewDir` `{var}`-templated-path constructors, which still do their
own substitution).

This roadmap doc scopes the REUSABLE core primitives
(`codex.Getter[T]`/`Setter[T]`, `codex.Const[T]`, `codex.Immutable[T]`);
the `examples/go-edge-models` adoption (typed `PathPattern` wrappers
with a `Resolve` method) is a follow-on, package-local design that
BUILDS ON these primitives once they exist — not part of this doc's own
API surface.

## Scope decisions (what's in Phase 1, what's deferred)

| In scope | Out of scope |
|---|---|
| `codex.Getter[T]` — a minimal, one-method interface (`Get() T`) any validated-value wrapper can satisfy | A general "property"/"computed getter" framework (this is NOT a reflection-based struct-field getter generator) |
| `codex.Setter[T]` — the write-side counterpart (`Set(T) error`), plus `codex.GetterSetter[T]` combining both | A generic "observable property" with change callbacks/hot-reload — no driver yet, see "Out of scope (Phase 2)" |
| `codex.Const[T]` — an immutable struct wrapping a private `T` value, validated ONCE at construction via `MustConst`, implements ONLY `Getter[T]` | A non-panicking `NewConst` returning `(Const[T], error)` — deferred; see "Open design decisions" |
| `codex.Immutable[T]` — a pointer-based, mutex-guarded cell set EXACTLY ONCE at runtime via `Set`, validated against a `Codec[T]`, implements `GetterSetter[T]` | A `Mutable[T]` that allows reassignment/hot-reload — no driver yet |
| `Const[T].Get()`/`String()`; `Immutable[T].Get()`/`TryGet()`/`Set()` | Type-specific `String()` overrides/customization hooks |
| Package-level doc + unit tests in `codex` | Any change to `Struct`/`Refine`/`MapCodecSafe`/etc. — this is a new, additive family of types |

## Conceptual layering — is this "codecs of a higher kind"?

Go generics have no true higher-kinded types (no abstraction over type
*constructors*, only over concrete types), so `Const[T]`/`Immutable[T]`
are not literally "higher-kinded codecs" in the type-theory sense. What
IS accurate — and worth carrying into `docs/concepts/codec.md`'s
narrative once this ships — is a THIRD layer sitting on top of the two
that already exist:

| Layer | What it validates | Reference |
|---|---|---|
| 0 — `Codec[T]` | A wire SHAPE, on every `Encode`/`Decode` call | `codex/codec.go` |
| 1 — `HasCodec[T]` | "This TYPE knows its own Codec" — generic helpers (`Validate`/`New`/`EncodeSelf`/`DecodeAs`/`SchemaOf`) work on any type implementing it | `codex/hascodec.go` |
| 2 — `Getter[T]`/`Setter[T]` + `Const[T]`/`Immutable[T]` (NEW) | "This VALUE's identity, at a specific point in its OWN lifecycle (authored-at-compile-time vs. assigned-once-at-runtime), is validated and then frozen" — generic CONTAINERS parameterized by an externally supplied `Codec[T]` | `codex/const.go` (this feature) |

This is a SMALL, GENERAL recipe — wrap a `Codec[T]`, add ONE lifecycle
rule, expose `Getter`/`Setter` — that any FUTURE similar primitive
(e.g. a hypothetical `Mutable[T]` with hot-reload/observer hooks —
explicitly out of scope here, no driver yet) would follow, not a
one-off pair of types.

### Relationship to `Codec[T]`'s `Encode`/`Decode`

`Codec[T].Encode`/`Decode` are WIRE-shape transformations — they convert
a Go value to/from an intermediate `any` representation (JSON/YAML/TOML-
shaped data). `Getter[T]`/`Setter[T]` (and `Const[T]`/`Immutable[T]`)
operate ENTIRELY on native, in-memory `T` values — they never touch a
wire representation at all. The two layers connect at exactly ONE
point: `Const`'s `MustConst` and `Immutable[T]`'s `Set` both validate
their incoming `T` by calling the SAME `Codec[T].Validate` every other
part of go-codex already uses (`Codec.Validate` is `Encode` immediately
followed by `Decode`, reusing the SAME `Refine` constraints a caller
already declared for wire validation). Nothing new is invented at the
codec level — `Const`/`Immutable` are thin, ADDITIVE consumers of
`Codec[T]`, in exactly the same spirit as `HasCodec[T]`'s
`Validate`/`New`/`EncodeSelf`/`DecodeAs` generic helpers — they just
apply that validation at a DIFFERENT lifecycle point (once, at
construction/assignment) instead of on every individual encode/decode
call a transport adapter makes.

### `Const[T]` vs. `Immutable[T]`

| | `Const[T]` | `Immutable[T]` |
|---|---|---|
| Value origin | Compile-time-authored (a package `var` literal) | Runtime-supplied, exactly once (e.g. env var / config load at startup) |
| Validation timing | Eager, at construction (`MustConst`) | Lazy, at the ONE `Set` call |
| Invalid value | **Panics** (an authored constant is a programming error if invalid) | **Returns a typed error** from `Set` (real, external runtime input — must not panic a process over bad input) |
| Concurrency | None needed — value type, fully valid at all times, safe to copy freely | Needs a mutex — has a genuine "not yet set" state before the first `Set`, so `*Immutable[T]` (pointer semantics, not copyable) |
| Re-assignment | N/A — never mutates after construction | A second `Set` call fails (typed `ImmutableAlreadySetError`) — "set once" is enforced, not just documented |
| Interface | `Getter[T]` (`Get() T`) only — no `Setter[T]`: a `Const`'s value is fixed forever at construction, so there is no runtime "assign" to expose | Both `Getter[T]` and `Setter[T]` (i.e. `GetterSetter[T]`) — `Get()` PANICS if called before any `Set` (a real bug: reading config before startup finished loading it); a separate `TryGet() (T, bool)` gives safe/optional access |

This asymmetry documents the conceptual difference well: `Getter[T]`
alone means "this value's identity is already settled"; `Getter[T]` +
`Setter[T]` together means "this value's identity can still be
settled, exactly once, by someone."

## API surface

```go
package codex

// Getter is implemented by any type that exposes a single, read-only
// value of type T — the minimal contract [Const] and [Immutable]
// satisfy. Deliberately ONE method, mirroring [HasCodec]'s own
// minimalism — a caller who only needs "give me the validated T" can
// depend on Getter[T] instead of a concrete container type.
type Getter[T any] interface {
	Get() T
}

// Setter is the write-side counterpart to Getter — implemented by any
// type that accepts a validated assignment of T, fallibly (the
// assignment itself may be invalid per some Codec[T], or may be
// rejected for a lifecycle reason like "already set"). Deliberately
// the mirror image of Getter[T]'s single-method minimalism.
type Setter[T any] interface {
	Set(T) error
}

// GetterSetter is the natural combination for a caller that wants to
// depend on "a validated, readable-AND-writable cell of T" without
// naming the concrete Immutable[T] type.
type GetterSetter[T any] interface {
	Getter[T]
	Setter[T]
}

// Const is an immutable, validated constant of type T. The underlying
// value is unexported — Get() is the ONLY way to read it. Construct
// via [MustConst], never a struct literal (the zero value has no
// validated contents and is not a meaningful Const). Implements ONLY
// Getter[T] — a Const has no runtime "assign" operation to expose.
type Const[T any] struct {
	value T
}

// MustConst validates value against codec and returns a Const[T],
// PANICKING if validation fails. A Const is always compile-time-
// authored (a package-level var, e.g. a path-pattern template or a
// protocol version constant) — an invalid one is a programming error
// that should fail LOUDLY and IMMEDIATELY (at package init / first
// use), not be silently propagated as a runtime error a caller has to
// check. This mirrors [Must]/[Must2]'s existing panic-on-invalid
// convention for exactly this "the input is not really dynamic, it's
// authored" case.
func MustConst[T any](value T, codec Codec[T]) Const[T] {
	if err := codec.Validate(value); err != nil {
		panic(fmt.Sprintf("codex.MustConst: invalid constant %v: %v", value, err))
	}
	return Const[T]{value: value}
}

// Get returns the validated value — Const[T] satisfies Getter[T].
func (c Const[T]) Get() T { return c.value }

// String implements fmt.Stringer for ANY T via fmt.Sprint — a
// Const[string] (the common case, e.g. a path-pattern template) prints
// its plain text directly; a Const[int]/Const[MyEnum] still gets a
// sensible default representation with zero extra code.
func (c Const[T]) String() string { return fmt.Sprint(c.value) }

// Immutable is a runtime value set EXACTLY ONCE and validated against
// a Codec[T] at that one Set call — the "config/env var loaded once at
// startup, then read-only for the rest of the process" shape. Unlike
// Const[T] (compile-time-authored, always valid, freely copyable),
// Immutable[T] has a genuine "not yet set" lifecycle state before the
// first Set, so it is used via a pointer and guards its internal state
// with a mutex. Construct via [NewImmutable], not a struct literal, so
// the codec is always present.
type Immutable[T any] struct {
	mu    sync.Mutex
	value T
	set   bool
	codec Codec[T]
}

// NewImmutable returns an unset Immutable[T] validated against codec —
// call Set exactly once before any Get.
func NewImmutable[T any](codec Codec[T]) *Immutable[T] {
	return &Immutable[T]{codec: codec}
}

// Set validates value against the Immutable's codec and stores it,
// PERMANENTLY locking further Set calls. Returns
// [ImmutableAlreadySetError] if already set (an external caller's bug
// — e.g. loading config twice), or the codec's own validation error if
// value is invalid. Real runtime input (unlike Const's authored
// value), so this returns an error rather than panicking.
func (im *Immutable[T]) Set(value T) error {
	im.mu.Lock()
	defer im.mu.Unlock()
	if im.set {
		return ImmutableAlreadySetError{}
	}
	if err := im.codec.Validate(value); err != nil {
		return err
	}
	im.value, im.set = value, true
	return nil
}

// Get returns the set value — PANICS if called before any successful
// Set (a real bug: reading config before startup finished loading it).
// Satisfies Getter[T], mirroring Const[T].Get()'s signature exactly.
func (im *Immutable[T]) Get() T {
	im.mu.Lock()
	defer im.mu.Unlock()
	if !im.set {
		panic("codex: Immutable[T].Get called before Set")
	}
	return im.value
}

// TryGet is Get's safe/optional counterpart — returns (value, true) if
// set, or (zero, false) if not, never panicking.
func (im *Immutable[T]) TryGet() (T, bool) {
	im.mu.Lock()
	defer im.mu.Unlock()
	return im.value, im.set
}

// ImmutableAlreadySetError is returned by Set when called more than
// once on the same Immutable[T].
type ImmutableAlreadySetError struct{}

func (ImmutableAlreadySetError) Error() string { return "codex: Immutable already set" }

// LogValue implements slog.LogValuer for structured logging (empty
// group — this error carries no fields, mirroring other zero-field
// sentinel errors' documented LogValue convention).
func (ImmutableAlreadySetError) LogValue() slog.Value { return slog.GroupValue() }
```

Usage sketch (the motivating `usecase` package pattern, illustrative
only — NOT part of this doc's own API surface, see "Motivation" above
and "Follow-on" below):

```go
var pathPatternCodec = c.String().Refine(v.NonEmptyString)

// UseCasePathPattern's underlying text stays private; String() exposes
// it for ports.NewFile's own {var} substitution.
var useCasePathPattern = codex.MustConst(
	fmt.Sprintf("%s/{%s}.json", useCasesDirName, useCaseNameVar),
	pathPatternCodec,
)
```

And for `Immutable[T]` (config/env var loaded once at startup):

```go
var apiBaseURL = codex.NewImmutable(codex.String().Refine(validate.NonEmptyString))

func main() {
	if err := apiBaseURL.Set(os.Getenv("API_BASE_URL")); err != nil {
		log.Fatal(err)
	}
	// ... elsewhere, for the rest of the process's lifetime:
	url := apiBaseURL.Get()
}
```

## Structured errors (all implement `slog.LogValuer`)

- `ImmutableAlreadySetError{}` — returned by `Immutable[T].Set` when
  called more than once. No fields (nothing to report beyond the fact
  itself); `LogValue()` returns an empty `slog.GroupValue()`, mirroring
  other zero-field sentinel errors' documented convention.
- `MustConst` does NOT define a new error type — it panics with a
  formatted message embedding the underlying codec's own validation
  error (no structured error needed for a panic path; see "Structured
  errors" rationale below).
- `Immutable[T].Set`'s OTHER failure mode (an invalid value) returns the
  codec's own existing validation error type unchanged (`ValidationError`/
  `ValidationErrors`/`ConstraintError`/etc.) — no new wrapper type
  needed; `errors.As` already reaches it exactly as it does today for
  any other `Codec[T].Validate` call.

`MustConst` panics rather than returning an error — see "Scope
decisions" for why (a `Const` is authored, not received at runtime).
There is no typed error type to define there, and nothing to wrap with
`slog.LogValuer` (a panic message is a programmer-facing string, not a
structured value callers are expected to inspect programmatically).

## Observer integration

Not applicable. `Const`/`Immutable`/`Getter`/`Setter` involve no I/O, no
adapter boundary, and no runtime decode/encode call a caller makes
repeatedly — `MustConst` runs once, typically at package `var`
initialization; `Immutable[T].Set` runs once, typically at process
startup. There is nothing for a `stats.Observer` to record (mirrors why
`Struct`/`Refine`/`MapCodecSafe` themselves have no observer integration
either — that is an adapter-layer concern, not a codec-construction-time
one).

## Unit test plan

| Test | Verifies |
|---|---|
| `TestMustConst_ValidValue_ReturnsConst` | A valid value + codec produces a `Const[T]` whose `Get()` returns it unchanged |
| `TestMustConst_InvalidValue_Panics` | An invalid value + codec panics (`recover()` + assert panic message contains the validation error) |
| `TestConst_Get_ReturnsUnderlyingValue` | `Get()` round-trips the exact value passed to `MustConst` |
| `TestConst_String_UsesFmtSprint` | `String()` on a `Const[string]` returns the plain string; on a `Const[int]` returns its decimal text |
| `TestConst_ImplementsGetterInterface` | Compile-time assertion: `var _ Getter[string] = Const[string]{}` |
| `TestImmutable_SetValid_GetReturnsIt` | Valid `Set` then `Get()` returns the same value |
| `TestImmutable_SetInvalid_ReturnsCodecError` | Invalid value → `Set` returns the codec's own validation error, stays unset |
| `TestImmutable_SetTwice_ReturnsAlreadySetError` | Second `Set` call (even with a different, valid value) → `ImmutableAlreadySetError`, first value unchanged |
| `TestImmutable_GetBeforeSet_Panics` | `Get()` on a fresh `*Immutable[T]` panics |
| `TestImmutable_TryGetBeforeSet_ReturnsFalse` | `TryGet()` on a fresh `*Immutable[T]` returns `(zero, false)`, no panic |
| `TestImmutable_TryGetAfterSet_ReturnsTrue` | `TryGet()` after `Set` returns `(value, true)` |
| `TestImmutable_ImplementsGetterSetterInterface` | Compile-time assertion: `var _ GetterSetter[string] = (*Immutable[string])(nil)` |
| `TestImmutableAlreadySetError_LogValue` | `LogValue()` returns `slog.KindGroup` (empty group) |
| `ExampleMustConst` | pkg.go.dev-visible usage sketch (a validated constant, e.g. a fixed-format version string) |
| `ExampleImmutable` | pkg.go.dev-visible usage sketch — e.g. loading a required env var once at startup |

## Files to create

| File | Responsibility |
|---|---|
| `codex/getter.go` (SHIPPED here instead of `const.go` — kept decoupled from any one container implementation) | `Getter[T]`, `Setter[T]`, `GetterSetter[T]` |
| `codex/const.go` | `Const[T]`, `MustConst`, `Immutable[T]`, `NewImmutable`, `ImmutableAlreadySetError` |
| `codex/const_test.go` | Full unit test plan above (Const + Immutable + interface satisfaction tests) |
| `docs/concepts/codec.md` (doc-only, at implementation time) | New "Layer 2: validated value containers built on Codec[T]" subsection — the 3-layer table + Getter/Setter asymmetry, mirroring the existing `PartialField`/`PartialStruct` subsection's style |

## Phase 2 — SHIPPED: `examples/go-edge-models` `usecase` package adoption

`models/iotedge/usecase/config.go`'s six path-pattern strings are all
validated once at package init against a shared `pathPatternCodec`
(non-empty). Three of the six — `baselinePathPattern`,
`useCaseEntryShape`, `deviceEntryShape` — have no `{var}`s to substitute
(a single fixed path, or a filename SHAPE a caller's var is EXTRACTED
from, not substituted into) and stay plain `codex.Const[string]`, read
via `String()` only.

The other three — `useCasePathPattern`, `deviceDirPathPattern`,
`deviceFilePathPattern` — substitute vars into their pattern. These
were ORIGINALLY shipped as dedicated typed wrapper structs
(`useCasePathPatternType`/`deviceDirPathPatternType`/
`deviceFilePathPatternType`, each embedding `codex.Const[string]`) with
a bespoke `Resolve` method. A LATER session (the `codex.Template[T]`/
`Param` unification effort — see `docs/concepts/codec.md`'s
`Template[T]` subsection) superseded that bespoke design: all three are
now plain `codex.Template[V]` values built via `codex.NewTemplate` +
`codex.IdentityField`/`RequiredField` (the single-var patterns use `Name`
directly via `codex.IdentityField`; the two-var `deviceFilePathPattern`
uses a small `deviceFileVars` struct via `codex.RequiredField`), and
`Template.Build(vars)` — not a package-local `Resolve` method — produces
the concrete, validated path. This is a strictly more general
replacement (the SAME reusable primitive every other `{var}`-templated
declaration in go-codex now shares, rather than a package-local pattern
reinvented per package) and is functionally equivalent to the original
design's outcome. Every existing `ports.NewFile`/`NewDir` call site uses
`.String()` on the STATIC `Const[string]` patterns or the templated
patterns' own raw template text — unchanged in shape from before this
refactor, just now validated (and, for the three template-based ones,
built) via `codex`'s shared primitives instead of ad hoc code.

## Out of scope (Phase 3)

- **A non-panicking `NewConst[T]` and a `Mutable[T]` with reassignment/
  hot-reload + observer hooks — now DESIGNED (not yet implemented) in
  their own follow-on roadmap doc:**
  [Reloadable Value Containers — `Mutable`, `NewConst`](reloadable-value-containers.md).
  A concrete driver emerged for both (key/credential rotation for
  `SecurityFunc`/`CredentialFunc` closures) — see that doc for the full
  design.
- ~~Any generic "template resolution" helper in `codex`/`ports`...~~ —
  **ALREADY SHIPPED**, via a different, more general mechanism than
  originally anticipated here: `internal/templatematch.Build` is the
  ONE shared substitution core `codex.BuildFromParams` (rest/events/
  reqreply Param types) AND `codex.Template[T].Build` (mcp Resources,
  `go-edge-models`'s path patterns) both delegate to; `ports.File`/
  `ports.Dir` also call `codex.BuildFromParams` directly for their own
  `{var}` substitution. No further dedup work remains here.

## Open design decisions

- **Panic vs. error on `Const` construction.** Locked to panic-only for Phase 1 (see rationale above). **Resolved (design, not yet implemented):** a non-panicking `NewConst[T]` is now designed in [Reloadable Value Containers](reloadable-value-containers.md) — `MustConst` itself stays unchanged/panic-only.
- **Should `Const[T]`/`Immutable[T]` be comparable/usable as a map key?** `T` itself may not be `comparable` (e.g. a `Const[[]string]`). Phase 1 makes no promises either way and does not restrict `T` to `comparable` — keeps the primitive maximally general; document this rather than constrain it. `Immutable[T]` is pointer-based regardless, so this question only meaningfully applies to `Const[T]`.
- **Naming: `Const`/`Immutable` vs. alternatives.** `Const` avoids colliding with the existing `codex.Constraint[T]` name and reads naturally at the call site (`codex.MustConst(...)`). `Immutable` was chosen over `Once` specifically to avoid confusion with stdlib `sync.Once`/`sync.OnceValue`'s "compute once" semantics — `Immutable[T]` means "an EXTERNAL caller assigns it once," a different shape (confirmed via user discussion). Revisit either name if something clearer surfaces during implementation.
- **File name.** `codex/const.go` may become `codex/validated_value.go` or similar once both `Const` and `Immutable` exist side by side and `const.go` reads as too narrow a name — a small, low-stakes naming call to make at implementation time. This question recurs (and gets more pressing) as `Mutable[T]`/`Cacheable[T]` are added — see [Reloadable Value Containers](reloadable-value-containers.md)/[Cache Template Parity + `Cacheable[T]`](cache-parity-and-cacheable.md)'s own "Open design decisions."
