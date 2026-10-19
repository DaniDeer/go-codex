# Go 1.27 Generic Methods — retrofitting `any`-typed/free-function workarounds

> **Status:** Idea / investigation — Explore mode. Scope confirmed with
> the user (both tiers in scope, old signatures removed outright as a
> breaking change); full before/after signature design and a
> blast-radius audit are still open work. No code changes yet.
> [← Back to Roadmap](index.md)

## Motivation

Go 1.27 (already this repo's `go.mod` floor — `go 1.27.2`) ships
**generic methods**: a method can now declare its own type parameter
list, in addition to whatever type parameters its receiver already
carries:

```go
func (r Receiver[T]) Method[U any](...) ... // U is the METHOD's own,
                                             // on top of the receiver's T
```

This codebase has **many** confirmed places where the PRIOR absence of
this feature forced a specific, explicitly-documented workaround: an
operation that is conceptually "a method on some generic receiver" had
to be written as a **free function** instead, because a method could
only ever use its receiver's own type parameters — never introduce a
new one. Several of these workarounds carry doc comments that
explicitly cite this exact Go limitation as their own rationale (not
just incidental style), confirmed via a repo-wide grep, not assumed:

- `codex/maybe.go` (`MaybeMap`/`MaybeFlatMap`)
- `api/file/builder.go` (`WithPatch`)
- `forge/function.go` (`NewFunction`)
- `ports/file.go` (`PatchEncoded`)
- `internal/middleware/context_field.go` (`ContextFieldSetter`'s own
  doc comment, describing why `Middleware[In,Out].SetContextFieldFromIn`
  could not simply take a `ContextField[V]` directly)
- `api/events/builder.go` (`Client.transport`'s own doc comment, citing
  "Decision 5" of the SHIPPED `docs/design/d-0002-pubsub-workflow-simplification.md`)

Now that Go 1.27 lifts this restriction for methods on concrete
(non-interface) types, this doc captures what a retrofit would look
like, the places it clearly helps, and — just as important — the
places it structurally **cannot** help at all.

## The governing restriction (bounds everything below)

**Interface methods still cannot declare type parameters, and a
generic method cannot satisfy/implement a non-generic interface
method.** This is unchanged in Go 1.27 and is the single biggest
scoping constraint on this whole effort:

- Every adapter-implemented interface in this codebase —
  `events.Transport`/`rest.ClientTransport`/`rest.ServerTransport`/
  `reqreply.ClientTransport`/`reqreply.ServerTransport`/
  `api/file.Backend` (+ its ~10 optional extension interfaces) — is
  **permanently excluded** from ever becoming generic. These interfaces
  stay exactly as they are today: `any`-typed methods, dispatched via
  reflection inside each adapter.
- `internal/middleware`'s own `RouteMiddleware` interface is a second,
  smaller confirmed example of the SAME restriction already being
  worked around on purpose (see `api/rest/middleware.go`'s
  `routeMiddlewareContributor` doc comment: "RouteMiddleware carries no
  type parameters on its own interface method, so the generic work
  happens inside `applyAgnosticRoute`, where `In`/`Out` are still
  concrete").
- **Only methods on concrete types** — like `*events.Client`,
  `*reqreply.Client`, `*rest.Client`, `*api/file.Client`,
  `codex.Maybe[T]`, `ports.File[T]` — are eligible for this retrofit at
  all. Nothing described below touches an adapter-implemented
  interface's own shape.

## Tier 1 — small, low-risk candidates (confirmed via doc-comment grep)

These are each a standalone free function today, explicitly because a
method couldn't introduce its own extra type parameter. Converting them
is expected to be mechanical — no interface is involved, so nothing
outside each symbol's own direct callers should need to change beyond
the call-site rewrite itself.

| Symbol (today) | File | Sketch (Go 1.27 shape) |
|---|---|---|
| `codex.MaybeMap[T, R](m Maybe[T], fn func(T) R) Maybe[R]` | `codex/maybe.go` | `func (m Maybe[T]) Map[R any](fn func(T) R) Maybe[R]` |
| `codex.MaybeFlatMap[T, R](m Maybe[T], fn func(T) Maybe[R]) Maybe[R]` | `codex/maybe.go` | `func (m Maybe[T]) FlatMap[R any](fn func(T) Maybe[R]) Maybe[R]` |
| `api/file.WithPatch[T, P](r Resource[T], patchCodec codex.Codec[P], op Patch) Patcher[T, P]` | `api/file/builder.go` | `func (r Resource[T]) WithPatch[P any](patchCodec codex.Codec[P], op Patch) Patcher[T, P]` |
| `forge.NewFunction[In, Out](...)` | `forge/function.go` | Needs its own look — `NewFunction` is a top-level constructor, not clearly "a method on an existing generic receiver" the way the others are; may stay a free function regardless, or may become a method on a yet-to-exist builder type. Flagged as the one Tier 1 candidate needing its own small design decision, not a mechanical rename. |
| `ports.PatchEncoded[T, P](fh File[T], vars, patchCodec codex.Codec[P], patch P, opts) error` | `ports/file.go` | `func (fh File[T]) PatchEncoded[P any](vars map[string]string, patchCodec codex.Codec[P], patch P, opts ...FileOptions) error` |
| `internal/middleware`'s `SetContextFieldFromIn`/`SetContextFieldFromOut`-style constructors (`context_field.go`, `bound.go`) | `internal/middleware/` | Needs checking whether these are free functions BECAUSE of a method-type-param limitation, or because they return a `RouteMiddleware`-satisfying value independent of any receiver — may turn out NOT to be a true Tier 1 case once re-examined; flagged for the audit, not pre-judged here. |
| `usecase.PatchUseCaseFile[P]`/`PatchDeviceFile[P]` (this session's own code) | `examples/go-edge-models/models/iotedge/usecase/*.go` | `func (s *Scope) PatchUseCaseFile[P any](ctx, useCaseName Name, patchCodec codex.Codec[P], patch P, opts ...file.WriteOptions) error` (same shape, `*Scope` already exists as the natural receiver) |

Each of these would need, at minimum: the new method signature, removal
of the old free function (confirmed user decision: remove outright, no
deprecated wrapper), and every call site updated
(`fn(x, ...)` → `x.Method(...)`).

## Tier 2 — the big, foundational finding

`events.Client.Publish`/`.Subscribe`, `reqreply.Client.Call`/
`.CallAsync`, `rest.Client.Call`/`.Consume`, and ALL 14 of
`api/file.Client`'s own dispatch methods are `any`-typed +
reflection-based **specifically** because of this exact Go limitation —
confirmed via their own doc comments, not inferred. `events.Client`'s
own `transport` field doc comment cites this as "Decision 5" of the
shipped `docs/design/d-0002-pubsub-workflow-simplification.md` by name.

Confirmed current signatures (verified directly against the real code,
not assumed from memory):

```go
// api/events/builder.go
func (c *Client) Publish(ctx context.Context, pub any, msg any, opts ...ClientPublishOptions) error
func (c *Client) Subscribe(ctx context.Context, sub any, fn any, opts ...ClientSubscribeOptions) error

// api/reqreply/client.go
func (c *Client) Call(ctx context.Context, route any, req any, opts ...ClientCallOptions) (any, error)
func (c *Client) CallAsync(ctx context.Context, route any, req any, opts ...ClientCallOptions) (any, error)

// api/rest/builder.go
func (c *Client) Call(ctx context.Context, route any, req any, opts ...ClientCallOptions) (any, error)
func (c *Client) Consume(ctx context.Context, sseRoute any, req any, fn any, opts ...ClientConsumeOptions) error

// api/file/client.go — 14 dispatch methods total (confirmed via grep), e.g.:
func (c *Client) Read(ctx context.Context, r any, vars map[string]string) (any, error)
func (c *Client) Create(ctx context.Context, cr any, val any, vars map[string]string, opts ...WriteOptions) (createdDirs []string, err error)
func (c *Client) Patch(ctx context.Context, p any, patch any, vars map[string]string, opts ...WriteOptions) error
// ... Delete/List/Exists/Copy/Move/Mkdir/BatchDelete/OpenRead/OpenWrite/ListDir/DeleteDir, same shape
```

### The sketch

```go
func (c *Client) Publish[T any](ctx context.Context, pub Publisher[T], msg T, opts ...ClientPublishOptions) error
func (c *Client) Call[Req, Resp any](ctx context.Context, route Route[Req, Resp], req Req, opts ...ClientCallOptions) (Resp, error)
func (c *Client) Read[T any](ctx context.Context, r Reader[T], vars map[string]string) (T, error)
// ... and so on for every Tier 2 method
```

**Expected benefit**: near-zero caller-syntax change in the common case
(Go's method type-param inference should still let
`client.Publish(ctx, pub, msg)` compile unchanged, `T` inferred from
`msg`'s type), while gaining genuine compile-time type safety and
potentially eliminating an entire family of runtime-only error types
(`TransportTypeMismatchError` in `events`/`reqreply`/`rest`,
`BackendTypeMismatchError` in `api/file` — all confirmed to exist today,
one per package).

This directly revisits a shipped, documented design decision —
`docs/design/d-0002-pubsub-workflow-simplification.md`'s own Decision
5 — and spans the central dispatch mechanism of 4 major `api/*`
packages. It is, by a wide margin, the single biggest architectural
implication of this whole investigation.

### What is NOT yet verified (needs confirming during the actual design round, not assumed here)

1. **Does Go 1.27 allow a method with 2 of its OWN type parameters**,
   e.g. a `Patch[T, P]`-shaped method where `T` is NOT the receiver's
   own type (since `*Client` isn't itself generic) but `P` is a 2nd,
   genuinely new type parameter alongside whatever other type
   parameters the method needs? `api/file.Client.Patch` and
   `ports.PatchEncoded` both need exactly this shape. Needs a small,
   real Go file compiled under 1.27 to confirm, not just reasoned
   about.
2. **Does `TransportTypeMismatchError`/`BackendTypeMismatchError`
   become genuinely dead code**, or does it stay reachable for some
   edge case the compiler can't catch (e.g. a caller constructing a
   `Publisher[T]` value via reflection/interface boxing in a way that
   defeats static type inference)? Each package's own existing tests
   for this error type would need re-examining.
3. **Does the INTERNAL reflection-based dispatch core also simplify**,
   or does it stay exactly as-is underneath a newly type-safe
   caller-facing signature? Given the governing restriction above
   (adapter-implemented `Transport`/`Backend` interfaces can never
   become generic), the expectation is: the caller-facing win is
   certain, but the dispatch core still has to reach into an
   `any`-typed interface method one layer down — so internal
   simplification, if any, is likely partial at best. Needs confirming
   per-package during the actual design round, not guessed here.
4. **`forge.NewFunction`'s own Tier 1 classification** (see the table
   above) — may need to move to Tier 2 or be dropped from this doc
   entirely once re-examined, since it isn't obviously "a method on an
   existing generic receiver" the way the other Tier 1 candidates are.

## Confirmed NOT retrofittable, either way

The internal `Transport`/`ClientTransport`/`ServerTransport`/
`api/file.Backend` interfaces themselves — what every adapter
implements (`adapters/nethttp`, `adapters/chi`, `adapters/mqtt5`,
`adapters/mqtt`, `adapters/zeromq`, `adapters/file`, `adapters/s3`,
`adapters/smb`, …) — stay `any`-typed/reflection-based regardless of
this retrofit. Interface methods can never gain type parameters in Go,
1.27 or any future version under the language's current generics
design. This is a hard, permanent boundary, not a temporary gap.

## Scope decisions (confirmed via `ask_user` this session)

- **Scope: both tiers.** Investigate and design Tier 2's full
  `Client` dispatch-family conversion, not just Tier 1's smaller wins.
- **Fate of old signatures: remove outright.** This repo has a single
  maintainer/user and an established precedent of accepting breaking
  changes when they improve maintainability (mirrors
  `docs/design/d-0005-*.md`'s own stated policy, referenced elsewhere
  in this codebase) — no deprecated wrapper, no transition period.

## Breaking-change / blast-radius estimate (rough, grep-based — needs a real audit pass, not final)

| Symbol family | Non-test callers (repo-wide grep) |
|---|---|
| `events.Client.Publish`/`.Subscribe` | ~29 files |
| `reqreply.Client.Call`/`.CallAsync` | ~39 files |
| `rest.Client.Call`/`.Consume` | ~42 files |
| `api/file.Client`'s 14 dispatch methods | ~20 files |

These counts are a rough `grep -rln` sweep over non-test `.go` files
calling each method pattern — a real audit (todo `go127-audit-blast-radius`)
should break this down per-symbol, include test files and examples
explicitly, and confirm each hit is a genuine call site (not a false
positive from an unrelated `.Call(`/`.Read(` method on a different
type).

## Toolchain / dependency decisions

None — this is pure language-feature usage against the stdlib compiler.
The `go 1.27.2` `go.mod` floor this retrofit depends on is **already
in place**, committed during the `adapters/smb` implementation round
(`go127-bump-gomod`, done).

## Unit test plan (representative sample — not exhaustive)

- Tier 1: one regression test per converted symbol proving identical
  behavior to the removed free function (e.g.
  `TestMaybe_Map_SameBehaviorAsOldMaybeMap`).
- Tier 2, per package: a compile-time proof that the new generic method
  infers `T`/`Req`/`Resp` correctly from a plain, unannotated call site
  (`client.Publish(ctx, pub, msg)` with no explicit `[T]`), a test
  proving a GENUINE type mismatch is now a **compile** error (via a
  `// want` comment in a `testdata`-style negative-compile test, if this
  repo has a precedent for that pattern — check during the design
  round) rather than the old runtime `TransportTypeMismatchError`, and
  one regression test per package confirming existing passing tests
  still pass unmodified wherever behavior (not just syntax) must stay
  identical.

## Files to create/touch (rough inventory — refine during the real audit)

- `codex/maybe.go` (+ `maybe_test.go`)
- `api/file/builder.go`, `api/file/dispatch.go`, `api/file/client.go` (+ tests)
- `api/events/builder.go` (+ tests), every adapter/example calling
  `Client.Publish`/`.Subscribe`
- `api/reqreply/client.go` (+ tests), every adapter/example calling
  `Client.Call`/`.CallAsync`
- `api/rest/builder.go` (+ tests), every adapter/example calling
  `Client.Call`/`.Consume`
- `forge/function.go` (+ tests) — pending Tier 1/2 classification
- `ports/file.go` (+ tests)
- `internal/middleware/context_field.go`, `internal/middleware/bound.go`
  (+ tests) — pending re-examination (see Tier 1 table)
- `examples/go-edge-models/models/iotedge/usecase/*.go` (+ tests)
- Every example under `examples/` exercising any Tier 2 method directly

## Out of scope

- The adapter-facing interfaces themselves
  (`Transport`/`ClientTransport`/`ServerTransport`/`api/file.Backend`
  and its ~10 optional extension interfaces) — confirmed permanently
  un-retrofittable, not a future phase of this same doc.
- Any OTHER Go 1.27 feature unrelated to generic methods — not
  investigated this round.
- A broader "modernize the whole codebase for Go 1.27" sweep — this
  doc is scoped strictly to the specific, doc-comment-confirmed
  workarounds enumerated above.

## Open design decisions

1. **Staging/sequencing** — proposed: Tier 1 first, as a smaller,
   lower-risk proving round (validates the retrofit mechanics and the
   "near-zero caller-syntax change" expectation on a handful of
   low-stakes symbols); Tier 2 as one or more later, larger rounds
   (given its size — 4 major packages' central dispatch mechanism).
   Not yet confirmed with the user.
2. **`TransportTypeMismatchError`/`BackendTypeMismatchError`'s fate** —
   remove entirely once proven dead code, or keep as a defensive
   fallback? Needs the Tier 2 audit's own verification (see "What is
   NOT yet verified," item 2) before deciding.
3. **`forge.NewFunction`'s Tier 1/Tier 2 classification** — needs
   re-examination; flagged, not resolved, in this draft.
4. **`internal/middleware`'s `ContextField`-setter workaround** —
   needs confirming whether it's a true Tier 1 case (a method-type-param
   limitation) or a structurally different situation (a value
   independent of any receiver); flagged, not resolved.
5. **Full Tier 1 inventory** — the table above reflects this session's
   own grep-based survey (doc comments explicitly citing the
   limitation); a dedicated audit pass may find more, structurally
   identical cases that simply lack an explicit doc-comment callout.
6. **Full Tier 2 per-package signature design** — this doc sketches the
   SHAPE, not the complete, final Go source for each of the ~16+ Tier 2
   methods across 4 packages; that level of rigor is deferred to the
   dedicated design round once staging (decision 1) is settled.

## Next steps

1. `go127-audit-tier1-full-inventory` — complete, verified Tier 1
   inventory with before/after signatures (confirm/resolve open
   decisions 3 and 4 above along the way).
2. `go127-audit-tier2-signatures` — verified Tier 2 signature sketches
   for all 4 Client packages, resolving the 3 "not yet verified"
   questions above with real, compiled Go 1.27 code (not just
   reasoning).
3. `go127-audit-blast-radius` — a real, per-symbol blast-radius audit
   (replacing the rough estimate above).
4. Draft the full, Go-signature-rigor design once the audit lands —
   this doc, at that point, graduates from Explore to Refine depth.
