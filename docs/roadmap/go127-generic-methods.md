# Go 1.27 Generic Methods — retrofitting `any`-typed/free-function workarounds

> **Status: Tier 1 AND Workstream A (Tier 2's `api/file` half) BOTH
> IMPLEMENTED and SHIPPED.** Workstream B (`events`/`reqreply`/`rest`
> Client dispatch — caller-facing-only win, confirmed lower value, see
> the Tier 2 review below) remains explicitly **deferred to a separate
> future round** — not attempted.
>
> **Workstream A shipped**: all 14 `api/file.Client` dispatch methods
> converted off `any`-typed parameters. 4 genuinely needed a real Go
> 1.27 generic method (return value depends on `T`/`P`): `Read[T
> any]`, `Create[T any]`, `Patch[T, P any]`, `List[T any]` (returning
> `[]ListResult[T]` directly, no longer boxed in `any`). The other 10 —
> `Delete`, `Exists`, `Copy`, `Move`, `Mkdir`, `BatchDelete`,
> `OpenRead`, `OpenWrite`, `ListDir`, `DeleteDir` — needed NO Go 1.27
> feature at all, just a concrete (non-generic) dispatcher-interface
> parameter type instead of `any` (their own interface method never
> mentions `T`) — a finding made DURING this implementation round, not
> predicted by the earlier review (see "Workstream A — SHIPPED" below
> for the full per-method signature table and the `OpenRead`/
> `OpenWrite` reclassification). `BackendTypeMismatchError` is KEPT
> (not fully removed) — 15 of its 16 original raise sites are now dead
> code (a non-conforming type fails to compile instead), but one
> genuinely different raise site (`builder.go`'s `ErrorPatterns[B]`
> mismatch, unrelated to dispatch-role matching) stays reachable.
> Verified: `go build ./...`, `go vet ./...`, `go test ./...`,
> `just check`, `just examples` all green.
>
> A Tier 2 REVIEW round (no code changes) preceded this and corrected
> the original "all 14 `api/file` methods, same shape" claim, resolved
> open questions #2/#3 with real evidence (split per package-family —
> see "Correction" and the resolved numbered items under Tier 2 below),
> and recommended splitting Tier 2 implementation into 2 independent
> workstreams, `api/file` first — the recommendation this round
> followed.
>
> A follow-up coverage review (see "Additional Tier 1 candidates" and
> "A second, equally permanent category" below) widened the Tier 1
> survey, added a new permanent cross-package exclusion, resolved open
> question #1 via a real `go1.27.2` compile check, and spun off three
> separate stubs for other Go 1.27 features
> ([go127-stdlib-uuid.md](go127-stdlib-uuid.md),
> [go127-synctest-stream-tests.md](go127-synctest-stream-tests.md),
> [go127-json-v2-evaluation.md](go127-json-v2-evaluation.md)).
>
> An implementation round then SHIPPED every true Tier 1 candidate as a
> generic method, removing the old free function outright (breaking
> change, no deprecated wrapper, per this doc's own scope decision):
> `codex.Maybe[T].Map`/`.FlatMap`, `codex.Either[A,B].MapLeft`/
> `.MapRight`/`.Fold`, `api/file.Resource[T].WithPatch`,
> `ports.File[T].PatchEncoded`, `stream.Stream[In].Apply`,
> `stream.(*Topology).WithApply`, and the go-edge-models example's
> `usecase.(*Scope).PatchDeviceFile`/`.PatchUseCaseFile`. `Route[Req,
> Resp].WithHandleCallback` shipped too, but ADDITIVELY (the free
> function stays — see "`WithHandleCallback`: resolved fluent-chain
> design" below).
>
> Two design rounds concluded their targets should STAY free functions
> (no code change): the 4 top-level constructors (`forge.NewFunction`/
> `api/llm.NewCall`/`api/reqreply.NewRoute`/`api/rest.NewRoute` — see
> "Resolved: the 4 constructor symbols") and `internal/middleware`'s
> `ContextField` setters/`BoundNameOf` (see "Resolved:
> `internal/middleware`'s `ContextField` setters and `bound.go`").
>
> Verified after implementation: `go build ./...`, `go vet ./...`,
> `go test ./...`, `just check` (staticcheck + gosec + gofmt), and
> `just examples` (full example sweep) all pass with zero failures.
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
  could not simply take a `ContextField[V]` directly) — **RESOLVED, see
  "Resolved: `internal/middleware`'s `ContextField` setters" below: this
  is NOT a free-function workaround at all.
  `SetContextFieldFromIn`/`SetContextFieldFromOut` are already shipped,
  callable METHODS on `Middleware[In,Out]`
  (`api/rest`/`api/events`/`api/reqreply/middleware_declaration.go:233`/
  `264`/`193`, confirmed via real call sites, e.g.
  `examples/rest-api/auth/middleware.go:121`). The cited doc comment
  documents why a *naive, rejected* signature
  (`SetContextFieldFromIn(field ContextField[V], ...)`) doesn't compile
  — the shipped fix is the `ContextFieldSetter` interface
  (type-erasing `V`), which keeps the method a method with ZERO new
  type parameters, not a free function. Go 1.27 changes nothing here.**
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
| `forge.NewFunction[In, Out](...)` | `forge/function.go` | **RESOLVED — stays a free function.** See "Resolved: the 4 constructor symbols (`forge.NewFunction`/`api/llm.NewCall`/`api/reqreply.NewRoute`/`api/rest.NewRoute`)" below for the full design round and compile-check evidence. |
| `ports.PatchEncoded[T, P](fh File[T], vars, patchCodec codex.Codec[P], patch P, opts) error` | `ports/file.go` | `func (fh File[T]) PatchEncoded[P any](vars map[string]string, patchCodec codex.Codec[P], patch P, opts ...FileOptions) error` |
| ~~`internal/middleware`'s `SetContextFieldFromIn`/`SetContextFieldFromOut`-style constructors (`context_field.go`, `bound.go`)~~ | `internal/middleware/` | **RESOLVED — NOT a Tier 1 case, removed from this table.** See "Resolved: `internal/middleware`'s `ContextField` setters and `bound.go`" below for the full re-examination. |
| `usecase.PatchUseCaseFile[P]`/`PatchDeviceFile[P]` (this session's own code) | `examples/go-edge-models/models/iotedge/usecase/*.go` | `func (s *Scope) PatchUseCaseFile[P any](ctx, useCaseName Name, patchCodec codex.Codec[P], patch P, opts ...file.WriteOptions) error` (same shape, `*Scope` already exists as the natural receiver) |

Each of these would need, at minimum: the new method signature, removal
of the old free function (confirmed user decision: remove outright, no
deprecated wrapper), and every call site updated
(`fn(x, ...)` → `x.Method(...)`).

### Additional Tier 1 candidates (found during a follow-up coverage review)

A repo-wide grep for this same doc-comment language turned up several
more doc-comment-confirmed cases the original survey missed:

| Symbol (today) | File | Sketch (Go 1.27 shape) |
|---|---|---|
| `codex.EitherMapLeft[A, B, C](e Either[A, B], fn func(A) C) Either[C, B]` | `codex/either.go` | `func (e Either[A, B]) MapLeft[C any](fn func(A) C) Either[C, B]` |
| `codex.EitherMapRight[A, B, C](e Either[A, B], fn func(B) C) Either[A, C]` | `codex/either.go` | `func (e Either[A, B]) MapRight[C any](fn func(B) C) Either[A, C]` |
| `codex.EitherFold[A, B, R](e Either[A, B], onLeft func(A) R, onRight func(B) R) R` | `codex/either.go` | `func (e Either[A, B]) Fold[R any](onLeft func(A) R, onRight func(B) R) R` |
| `api/llm.NewCall[Req, Resp](...)` | `api/llm/call.go` | **RESOLVED — stays a free function.** See "Resolved: the 4 constructor symbols" below. |
| `api/reqreply.NewRoute[Req, Resp](...)` | `api/reqreply/route.go` | **RESOLVED — stays a free function.** See "Resolved: the 4 constructor symbols" below. |
| `api/rest.NewRoute[Req, Resp](...)` (a second, previously unlisted `NewRoute` twin, found during this round's blast-radius check — `api/rest/builder.go:3712`) | `api/rest/builder.go` | **RESOLVED — stays a free function.** Same design round as `api/reqreply.NewRoute` (the two are structurally identical: `method/topic, path/topic-template, reqCodec, respCodec, opts...`). See "Resolved: the 4 constructor symbols" below. |
| `api/reqreply.WithHandleCallback[Req, Resp](fn func(*RouteHandle[Req, Resp])) RouteOpt` | `api/reqreply/router.go` | **RESOLVED** (see `design-withhandlecallback` below) — `func (r Route[Req, Resp]) WithHandleCallback(fn func(*RouteHandle[Req, Resp])) Route[Req, Resp] { return r.WithOpt(handleCallbackOpt{fn: fn}) }`. Needs ZERO new type parameters (`Req`/`Resp` are already bound on the receiver) — the doc comment's "Go forbids new type parameters on a method" claim was investigated and found to be the WRONG historical reason (verified: `Req`/`Resp` on `handleCallbackOpt`'s captured closure already match `Route[Req, Resp]`'s own). The REAL reason it was a free function was never stated in the doc comment at all; converting to a method is purely upside. |
| `api/rest`'s analogous `WithHandleCallback`-shaped helper (tied to `docs/design/d-0007-declarative-middleware-layering.md`) | `api/rest/router.go` | **RESOLVED**, identical to the `api/reqreply` twin — `func (r Route[Req, Resp]) WithHandleCallback(fn func(*RouteHandle[Req, Resp])) Route[Req, Resp] { return r.WithOpt(handleCallbackOpt{fn: fn}) }`. Same investigation, same conclusion, structurally identical signature. |

`codex/either.go`'s `Swap` method is the existing proof this pattern
already works today for the NO-new-type-param case (it only reorders
`Either[A,B]`'s own `A`/`B`); `MapLeft`/`MapRight`/`Fold` are the
genuinely blocked, new-type-parameter siblings that `MaybeMap` already
established the precedent for.

### Resolved: `internal/middleware`'s `ContextField` setters and `bound.go`

Re-examined per `design-contextfield-setters`: read
`internal/middleware/context_field.go` and `internal/middleware/bound.go`
COMPLETE, grepped every real call site across the repo, and traced the
actual shipped signatures. Conclusion for every symbol previously
flagged "needs checking": **neither file has a true Tier 1 case.** Both
symbols are correctly classified as free functions ALREADY, for reasons
unrelated to Go's "no new type params on a method" restriction — Go
1.27 changes nothing for either one.

**1. `Middleware[In,Out].SetContextFieldFromIn`/`SetContextFieldFromOut`
— NOT free functions at all; already shipped METHODS.**

```go
// api/rest/middleware_declaration.go:233 (events:264, reqreply:193 — identical shape)
func (m Middleware[In, Out]) SetContextFieldFromIn(field ContextFieldSetter, get func(In) any) Middleware[In, Out]
func (m Middleware[In, Out]) SetContextFieldFromOut(field ContextFieldSetter, get func(Out) any) Middleware[In, Out]
```

Confirmed via real call sites, all method-chained on an existing
`Middleware[In,Out]` value, never called as a bare function:

- `examples/rest-api/auth/middleware.go:121` —
  `....SetContextFieldFromIn(GrantedScopesUserIDField, func(in AuthIn) any { return in.Token })`
- `examples/events-api/auth/middleware.go:135` — same pattern
- `examples/reqreply-api/auth/route.go:67` —
  `....SetContextFieldFromOut(GrantedScopesUserIDField, func(out GrantedScopesAuthOut) any { return out.Subject })`

The `ContextFieldSetter` doc comment in `context_field.go` (around line
103) cites "a confirmed, compile-verified design error" — but that
error describes a *rejected, never-shipped* naive signature,
`func (m Middleware[In, Out]) SetContextFieldFromIn(field ContextField[V], ...)`,
which indeed does not compile (`V` undefined on the method). The
SHIPPED fix is NOT "make it a free function" — it's the
`ContextFieldSetter` interface (`interface { Set(ctx context.Context, raw any) error }`),
which every `ContextField[V]` implements REGARDLESS of `V`. Passing a
`ContextFieldSetter` instead of `ContextField[V]` directly lets
`SetContextFieldFromIn` stay a genuine method with ZERO new type
parameters — `V` is erased at the call boundary, never needed in the
method's own signature. This is actually a BETTER outcome than a Go
1.27 method-type-param retrofit would produce: a 1.27 signature
(`func (m Middleware[In, Out]) SetContextFieldFromIn[V any](field ContextField[V], get func(In) any) Middleware[In, Out]`)
would force every call site to either rely on inference from `field`'s
own concrete type (works) but would gain nothing in return — the
existing interface-based method is already exactly as ergonomic, is
already shipped, already has full test/example coverage, and avoids
coupling the method's own signature to `V` at all. **Conclusion: stays
exactly as-is. No code change, now or ever, tracked here.**

**2. `middleware.BoundNameOf(bm any) string` — correctly a free
function, for a DIFFERENT reason than the method-type-param limitation;
same "value independent of any receiver" shape as `WithHandleCallback`.**

```go
// internal/middleware/bound.go:86
func BoundNameOf(bm any) string {
	if n, ok := bm.(BoundNamed); ok {
		return n.MiddlewareName()
	}
	return ""
}
```

`bm` is deliberately `any` — it's a type-erased bound-middleware value
whose CONCRETE type varies per call site and per API pattern
(`rest.BoundMiddleware[Req,In,Out]`, `events.BoundSubscribeMiddleware[T,In,Out]`,
`reqreply.BoundClientMiddleware[Req,In,Out]`, ...). Confirmed via real
call sites — both of `internal/middleware`'s own consumers forward it
unchanged, never adding a type parameter of their own:

```go
// api/rest/bound_middleware.go:394 (reqreply/bound_middleware.go:370 — identical)
func boundNameOf(bm any) string { return middleware.BoundNameOf(bm) }
```

There is no "new type parameter a method can't introduce" here at
all — `BoundNameOf` introduces zero type parameters, generic or
otherwise; it operates purely via a runtime interface type-assertion
against `BoundNamed`. It also has **no natural receiver type in
`internal/middleware` itself** to hang a method on: the bound
middleware VALUES (`rest.BoundMiddleware[Req,In,Out]`, etc.) are each
pattern-specific types declared in `api/rest`/`api/events`/`api/reqreply`,
never in `internal/middleware` — and per this codebase's own
cross-package method rule, a method can only be added in the SAME
package as its receiver type, so `internal/middleware` could not add a
`.Name()` method to any of them even if it wanted to. This is exactly
the same shape already identified for `api/rest`/`api/reqreply`'s
(resolved) `WithHandleCallback`-style helpers above: a value-independent
free function that was never blocked by the method-type-param
restriction in the first place. **Conclusion: stays a free function —
correct today, and Go 1.27 provides no mechanism that would change
this (it lifts a method-type-param restriction; `BoundNameOf`'s free-
function shape is driven by "no natural in-package receiver exists",
an orthogonal constraint 1.27 does not touch).**

**Net outcome for `impl-contextfield-setters`**: no implementation
work is needed — both files are correctly shaped already. That todo
should conclude "no change needed" per its own stated fallback.

### Resolved: the 4 constructor symbols (`forge.NewFunction`/`api/llm.NewCall`/`api/reqreply.NewRoute`/`api/rest.NewRoute`)

(`design-builder-constructors`, this session's own design round.) All
four were flagged above as "needs its own small design decision" because,
unlike `MaybeMap`/`WithPatch`/`PatchEncoded`/`EitherMapLeft`/etc., they are
not methods on a pre-existing generic receiver forced into free-function
form — they are **top-level constructors**: there is no `Function[In,
?]`/`Call[Req, ?]`/`Route[Req, ?]` value in existence before the second
type parameter (`Out`/`Resp`) is chosen. The candidate shape investigated
was a two-stage builder:

```go
func NewXBuilder[In any](... In-only params ...) *XBuilder[In]
func (b *XBuilder[In]) Build[Out any](... Out-only params ...) *X[In, Out]
```

#### 1. Real, current signatures (read directly, not paraphrased)

```go
// forge/function.go:96
func NewFunction[In, Out any](
    name, version string,
    input codex.Codec[In],
    output codex.Codec[Out],
    apply func(In) (Out, error),
    opts ...FunctionOpt,
) *Function[In, Out]

// api/llm/call.go:114
func NewCall[Req, Resp any](
    name string,
    reqCodec codex.Codec[Req],
    respCodec codex.Codec[Resp],
    opts ...CallOpt,
) Call[Req, Resp]

// api/reqreply/route.go:1046
func NewRoute[Req, Resp any](
    topic string,
    reqCodec codex.Codec[Req],
    respCodec codex.Codec[Resp],
    opts ...RouteOpt,
) Route[Req, Resp]

// api/rest/builder.go:3712
func NewRoute[Req, Resp any](
    method, path string,
    reqCodec codex.Codec[Req],
    respCodec codex.Codec[Resp],
    opts ...RouteOpt,
) Route[Req, Resp]
```

#### 2. The `opts` split — real, per-symbol, not assumed

Every `opts` element type was read individually (not just the sealed
interface declaration) to check whether it references `In`/`Out` (or
`Req`/`Resp`):

- **`forge.FunctionOpt`** (`forge/options.go`): `FunctionMeta` (plain
  strings, no type param) and `WithRefinement[In any](fn func(In) error)
  FunctionOpt` (captures `In` only, stored as `functionOptions.refinement
  func(any) error`). **Zero `Out`-dependent variants exist.** The whole
  `opts` list is therefore cleanly In-only — it can move to Stage 1
  (`NewFunctionBuilder`) with no loss.
- **`api/llm.CallOpt`** (`api/llm/call.go`): `SystemPromptOpt`/
  `SystemPromptFileOpt`/`IncludeRequestSchemaOpt`/`CallMeta` (no type
  param) and `UserMessageOpt[Req any]{fn func(Req) (string, error)}`
  (captures `Req` only). **Zero `Resp`-dependent variants exist.** Same
  conclusion: `opts` is cleanly Req-only, movable to Stage 1
  (`NewCallBuilder`).
- **`api/reqreply.RouteOpt`** (`api/reqreply/route.go`): `TopicParam`/
  `RouteMeta`/`securitySchemeOpt`/`ErrorReplyMeta` (no type param),
  `MergedTopicParam[Req]`/`requestFormatsOpt[Req]` (via
  `RequestFormats[Req]`, Req-only) — **but also `formatsOpt[Resp]`
  (via `Formats[Resp any](fmts ...format.Format[Resp]) RouteOpt`,
  line 164), which is genuinely `Resp`-only.** `RouteOpt` MIXES Req-only
  and Resp-only variants in one sealed interface — there is no clean
  split point. Forcing one would mean either splitting `RouteOpt` itself
  into two sealed interfaces (a much larger, separately-breaking change,
  out of scope for this retrofit) or accepting that `opts` can only be
  supplied once both `Req` and `Resp` are concrete — i.e. at Stage 2.
- **`api/rest.RouteOpt`** (`api/rest/builder.go`): same shape, same
  conclusion, more pronounced — `PathParam`/`QueryParam`/`CookieParam`/
  `HeaderParam`/`RouteMeta`/`ResponseMeta` (no type param),
  `MergedPathParam[Req]`/`RequestFormats[Req]` (Req-only), AND
  `ResponseHeaderParam`/`ResponseCookieParam`'s `Merged*Param[Resp]`
  variants plus `Formats[Resp]` (Resp-only). **Same mixed-dependency
  finding as `api/reqreply.RouteOpt` — no clean split exists.**

This is the one genuinely asymmetric finding of this design round:
`forge`/`llm`'s option families split cleanly (In/Req-only); `reqreply`/
`rest`'s do not (both directions represented). Any future implementation
MUST NOT force a bad split on the latter two — if a two-stage shape is
ever adopted for them, `opts` stays bundled entirely in Stage 2 (`Build`).

#### 3. Compile-check: does Go 1.27 method-type-param inference actually work here?

A throwaway file (`go1.27.2`, deleted after this check — never
committed) mirrored all 4 shapes with stand-in `Codec[T]`/opt types,
built as follows, then run with `go run`:

```go
type FunctionBuilder[In any] struct{ /* name, version, input, opts */ }
func NewFunctionBuilder[In any](name, version string, input Codec[In], opts ...FunctionOpt) *FunctionBuilder[In]
func (b *FunctionBuilder[In]) Build[Out any](output Codec[Out], apply func(In) (Out, error)) *Function[In, Out]

type CallBuilder[Req any] struct{ /* name, reqCodec, opts */ }
func NewCallBuilder[Req any](name string, reqCodec Codec[Req], opts ...CallOpt) *CallBuilder[Req]
func (b *CallBuilder[Req]) Build[Resp any](respCodec Codec[Resp]) Call[Req, Resp]

type RouteBuilder[Req any] struct{ /* topic, reqCodec — no opts */ }
func NewRouteBuilder[Req any](topic string, reqCodec Codec[Req]) *RouteBuilder[Req]
func (b *RouteBuilder[Req]) Build[Resp any](respCodec Codec[Resp], opts ...RouteOpt) Route[Req, Resp]

// Call sites — ZERO explicit type arguments anywhere, at either stage:
fb := NewFunctionBuilder("calc", "1.0.0", inputCodec, metaOpt{...})
fn := fb.Build(outputCodec, func(in SensorData) (Grade, error) { ... })

cb := NewCallBuilder("summarize", inputCodec, sysPrompt{...})
call := cb.Build(outputCodec)

rb := NewRouteBuilder("compute/add", inputCodec)
route := rb.Build(outputCodec, topicParam{...})
```

**Result: compiled and ran successfully on the first try.** `In`/`Req` is
inferred at `NewXBuilder` from the codec argument's type; `Out`/`Resp` is
inferred at `Build` from the output-codec/apply-closure argument's type —
**no call site anywhere needs an explicit `[In]`/`[Out]` type argument at
either stage.** This confirms Go 1.27's method-type-param inference is a
complete, no-regression replacement for the free-function form,
mechanically.

#### 4. But does the free function ALREADY have this same inference property? — yes, confirmed via real repo code

The mechanical compile-check above proves the two-stage shape is *possible*
and loses no inference ergonomics. It does **not** by itself prove the
two-stage shape is *better* than today's single-call free function,
because Go's ordinary argument-type inference already lets a free
function with two type parameters omit both, in one call, when both
appear in argument position — exactly `NewFunction[In, Out]`'s/
`NewRoute[Req, Resp]`'s own shape. This is confirmed by real, already-
merged, already-compiling repo code, not assumed:

- `examples/sensor-service/ioports/ports.go:236` —
  `var CreateRoute = rest.NewRoute("POST", "/readings", ...)` — **zero
  explicit type arguments**, `Req`/`Resp` fully inferred from the codec
  arguments, compiles today (`go build ./examples/sensor-service/...`
  passes).
- Every one of `forge.NewFunction`'s 22 real repo call sites
  (`grep -rn "forge\.NewFunction\[" | grep -v _test.go` → 0 hits;
  `grep -rn "= forge\.NewFunction("` → 22 hits) **already omits explicit
  type arguments** — 100% inference-only, not a theoretical possibility.
- `api/rest.NewRoute`/`api/reqreply.NewRoute` call sites that DO write
  `[Req, Resp]` explicitly (the majority — see blast-radius below) do so
  by convention/clarity, not necessity; the explicit form and the
  inferred form are both already available today, with zero migration.

**This means the two-stage builder's headline ergonomic benefit —
"caller never writes an explicit type argument" — is not a NEW
capability at all for these 4 symbols.** The free-function form already
has it. The only thing a two-stage split could add is letting a caller
supply the `In`/`Req`-side arguments once and reuse the resulting builder
across several different `Out`/`Resp` variants — a real but narrow
use case with no existing demand found in this repo (every `Function`/
`Call`/`Route` declaration in the codebase is a single, one-shot package
`var`, never re-parameterized over a shared In-side builder).

#### 5. Blast-radius correction (the todo's own premise was checked, and found wrong)

The todo's background claimed "~2 real top-level app call sites each" for
`api/reqreply.NewRoute`/`api/rest.NewRoute`, citing
`examples/sensor-service/ioports/ports.go` as one of them. A real,
repo-wide grep (non-test `.go` files, own package excluded) found this
premise does not hold:

| Symbol | Real non-test call sites outside own package |
|---|---|
| `forge.NewFunction` | 22 (100% inference-only, see §4) |
| `api/llm.NewCall` | 1 (`ports/handle.go`'s own generic dispatch — Req/Resp already in scope there; every other hit is in a `_test.go` file) |
| `api/reqreply.NewRoute` | 29 (mostly `examples/reqreply-api/routes/*.go`, plus `ports/handle.go`) |
| `api/rest.NewRoute` | 66 (examples across the repo: `rest-api`, `rest-builder`, `go-edge-models`, `rest-nested-binary`, `typed-params`, `rest-redirect`, …, plus `ports/handle.go`) |

`api/llm.NewCall` genuinely matches the "rare, mostly internal" premise.
`api/reqreply.NewRoute`/`api/rest.NewRoute` do **not** — both have
dozens of real, independent call sites, making a breaking signature
change here carry real migration cost repo-wide, not a small one.

#### 6. Final per-symbol decision

| Symbol | Decision | Reasoning |
|---|---|---|
| `forge.NewFunction[In, Out]` | **Stays a free function.** | Top-level constructor, not a receiver retrofit. `opts` splits cleanly (In-only) but the free form already has full argument-type inference (22/22 real call sites already inference-only) — a two-stage builder would only reshape one call into two, for a "reuse the In-side builder across several Outs" use case with zero current demand. |
| `api/llm.NewCall[Req, Resp]` | **Stays a free function.** | Same reasoning as `forge.NewFunction`; `opts` also splits cleanly (Req-only), and real-world usage is genuinely rare (1 non-test call site), so there is no meaningful blast radius either way — but no positive case for the change either. |
| `api/reqreply.NewRoute[Req, Resp]` | **Stays a free function.** | `RouteOpt` does NOT split cleanly (`Formats[Resp]` is genuinely Resp-only, alongside Req-only variants) — a two-stage shape would have to bundle ALL opts into Stage 2 anyway, making Stage 1 nearly pointless (topic + reqCodec only). Combined with 29 real call sites and zero inference benefit over today's free function, there is no net win. |
| `api/rest.NewRoute[Req, Resp]` | **Stays a free function.** | Same `RouteOpt` mixed-dependency finding as `api/reqreply.NewRoute`, and the LARGEST real blast radius of all 4 (66 call sites) — converting this one has the highest cost and, per the inference findings above, no corresponding ergonomic benefit. |

**General finding for this whole investigation**: the original Tier 1
table's working assumption — "a free function exists today because a
method couldn't add a new type parameter, so Go 1.27 now lets it become
a method" — does not apply to any of these 4 symbols. That restriction
only ever applied to a method on an **already-existing** generic
receiver (`Maybe[T]`, `Either[A,B]`, `Resource[T]`, `File[T]`) gaining a
*second* type parameter. A top-level constructor has no pre-existing
receiver at all — the free-function form was never a workaround for
these 4; it is the natural, and already fully capable (via argument-type
inference), shape for "build a brand-new two-type-parameter value from
scratch." No implementation work follows from this todo for any of the
4 symbols — `forge/function.go`, `api/llm/call.go`,
`api/reqreply/route.go`, `api/rest/builder.go` all stay unchanged, and
the 4 planned implementation todos should be closed as "no change
needed," mirroring `impl-contextfield-setters`'s own precedent above.

### A newly identified Tier 1 candidate outside the original survey: `stream`

`stream.Apply[In, Out]` (`stream/transform.go`) and `stream.WithApply[In,
Out]` (`stream/topology.go`, operating on `*Topology`) both introduce a
genuinely new `Out` type parameter not already present on their
`Stream[In]`/`*Topology` argument — the same shape as the Tier 1 table
above. Confirmed current signatures:

```go
// stream/transform.go
func Apply[In, Out any](ctx context.Context, src Stream[In], fn *forge.Function[In, Out], opts ApplyOptions) Stream[Out]

// stream/topology.go
func WithApply[In, Out any](t *Topology, fn *forge.Function[In, Out]) *Topology
```

Sketch: `func (s Stream[In]) Apply[Out any](ctx context.Context, fn
*forge.Function[In, Out], opts ApplyOptions) Stream[Out]` and `func (t
*Topology) WithApply[In, Out any](fn *forge.Function[In, Out])
*Topology`. Note `WithApply`'s `In` is NOT inferable from `*Topology`
(untyped receiver) but IS inferable from `fn`'s own type — same
caller-side inference story Tier 2 relies on.

**Important non-candidates in the same package**, to avoid
misclassifying them in a future audit: `stream.Buffer`, `.Debounce`,
`.Throttle`, `.Window`, `.SlidingWindow`, `.Drain`, and `.Collect` are
all free functions purely by STYLE choice, not necessity — none of them
introduces a type parameter beyond the `Stream[T]` they already take, so
they could already be ordinary (non-generic-method) methods on
`Stream[T]` today, with or without Go 1.27. They are explicitly OUT of
scope for this retrofit.

### `WithHandleCallback`: resolved fluent-chain design (`design-withhandlecallback`)

Investigated per `design-withhandlecallback`'s own framing: does
`WithHandleCallback` need ZERO new type parameters if converted to a
method on `Route[Req, Resp]`, and — separately — can that method
preserve every real call site's one-line fluent-chain style?

**Finding 1 — the doc comment's stated reason is wrong.** Both
`api/rest/router.go:310-326` and `api/reqreply/router.go:293-305`
justify the free-function shape with: "A free function (not a method) —
Go forbids new type parameters on a method; this is the SAME resolution
docs/design/d-0007-declarative-middleware-layering.md established for
[BoundMiddleware]'s identical constraint." This is INCORRECT for this
specific symbol: `handleCallbackOpt{fn: fn}`'s captured `fn
func(*RouteHandle[Req, Resp])` already uses the SAME `Req`/`Resp` as the
enclosing `Route[Req, Resp]` it would be attached to as a method
receiver — converting it to `func (r Route[Req, Resp])
WithHandleCallback(fn func(*RouteHandle[Req, Resp])) RouteOpt` (or the
`Route[Req, Resp]`-returning variant below) introduces ZERO new type
parameters, not even in the Go-1.27-generic-methods sense. The doc
comment's citation of `d-0007`'s `BoundMiddleware` precedent does not
apply here — `BoundMiddleware`'s own free-function constructors
(`NewBoundMiddleware` etc.) genuinely need a fresh `In`/`Out` the
receiver doesn't have; `WithHandleCallback` never did. The real
historical reason was never stated and is not reconstructible from the
code/docs alone — likely simple precedent-copying from the adjacent
`BoundMiddleware` doc comment, not a distinct technical constraint.

**Finding 2 — every real call site is a one-line fluent chain ending in
`.WithOpt(pkg.WithHandleCallback(fn))` on a `Route[Req, Resp]` value.**
Confirmed via a full-repo grep across `examples/reqreply-api/{zeromqserver,mqtt5server}/server.go`,
`examples/rest-api/{nethttpserver,chiserver}/server.go`, and both
packages' own `router_test.go`/`route_test.go`. Representative shapes:

```go
// examples/rest-api/nethttpserver/server.go:106-107
getUserRoute := routes.GetUserRoute.WithHandler(...).
    HandleBoundMW(...).HandleMW(nil, obsFn).HandleMW(nil, timingFn).WithOptions(opts).
    WithOpt(rest.WithHandleCallback(func(h *rest.RouteHandle[routes.GetUserReq, routes.User]) { getUserHandle = h }))

// examples/reqreply-api/zeromqserver/server.go:89-94
oauthRoute := routes.OAuthComputeRoute.
    HandleBoundMW(...).HandleMW(nil, ...).WithHandler(handlers.AddOAuth).
    WithOpt(reqreply.WithHandleCallback(func(h *reqreply.RouteHandle[...]) { oauthHandle = h }))
```

There is NO call site where `WithHandleCallback` is invoked before a
`Route[Req, Resp]` value exists (unlike, say, a `RouteOpt` passed inline
to `NewRoute`'s own variadic opts) — confirmed by also checking
`api/rest/router_test.go`/`api/reqreply/router_test.go`'s own
`TestRouter_Register_IndistinguishableFromDirectRegister`-style tests,
which pass it as one of `NewRoute`'s variadic opts (a genuinely
route-free call) ALONGSIDE the `WithOpt`-chained form
(`TestRoute_WithOpt_AttachesHandleCallbackToAlreadyDeclaredRoute`,
`decorated := declared.WithOpt(rest.WithHandleCallback(...))` at
`api/rest/router_test.go:507`/`api/reqreply/router_test.go:494`) — i.e.
BOTH shapes are real, tested usages today.

**Decision: keep `WithHandleCallback` as a free function returning
`RouteOpt`, AND add a new, separate `Route[Req, Resp]`-returning
chainable method with the SAME name, mirroring `WithOpt`'s own
relationship to `RouteOpt`.** Neither "stays a free function" nor "the
free function is dropped entirely" is correct in isolation — the
`NewRoute(..., pkg.WithHandleCallback(fn))` inline-opt call site (proven
real above, no `Route` value exists yet to be a receiver) means the
`RouteOpt`-returning free function cannot be removed; the dominant
`.WithOpt(pkg.WithHandleCallback(fn))` chain-ending call site (proven
real above) means the fluent-chain ergonomics ARE worth capturing as a
method. The resolved shape adds the method WITHOUT removing the
function — both coexist, exactly as `WithOptions`/`optionsOpt` and
`WithOpt`/`RouteOpt` already coexist today:

```go
// api/rest/router.go — UNCHANGED, stays exactly as today:
func WithHandleCallback[Req, Resp any](fn func(*RouteHandle[Req, Resp])) RouteOpt {
    return handleCallbackOpt{fn: fn}
}

// api/rest/middleware.go — NEW, added alongside WithOpt/Use/HandleMW/HandleBoundMW:
func (r Route[Req, Resp]) WithHandleCallback(fn func(*RouteHandle[Req, Resp])) Route[Req, Resp] {
    return r.WithOpt(handleCallbackOpt{fn: fn})
}
```

```go
// api/reqreply/router.go — UNCHANGED, stays exactly as today:
func WithHandleCallback[Req, Resp any](fn func(*RouteHandle[Req, Resp])) RouteOpt {
    return handleCallbackOpt{fn: fn}
}

// api/reqreply/route.go — NEW, added alongside WithOpt/Use/HandleMW/HandleBoundMW:
func (r Route[Req, Resp]) WithHandleCallback(fn func(*RouteHandle[Req, Resp])) Route[Req, Resp] {
    return r.WithOpt(handleCallbackOpt{fn: fn})
}
```

Both new methods need ZERO new type parameters (not even a "generic
method" in the Go 1.27 sense — `Req`/`Resp` come entirely from the
receiver) and are available the moment `go.mod`'s existing `go 1.27.2`
floor compiles, with no retrofit dependency at all; `impl-withhandlecallback`
may ship this independently of the rest of this roadmap's Go-1.27-method
conversions. **No naming collision**: confirmed via `grep -n "^func (r
Route\[Req, Resp\])"` across `api/rest/*.go`/`api/reqreply/*.go` — no
existing `WithHandleCallback` method on either `Route[Req, Resp]` type
today. Every real call site converts mechanically and losslessly:
`.WithOpt(pkg.WithHandleCallback(fn))` → `.WithHandleCallback(fn)` (the
chain-ending form) stays available as a strict ergonomic upgrade; call
sites passing it as a bare `NewRoute`/`Router_Register_Indistinguishable`-
style inline opt are UNCHANGED, still using the free function. This is
classified as **Tier 1-adjacent, not a true Tier 1 conversion** — unlike
the table's other Tier 1 entries (which REMOVE the old free function
per the project's "no deprecated wrapper" policy), this case keeps BOTH
forms permanently, since the free function has a second, genuinely
route-less call shape the method cannot serve.

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

// api/file/client.go — 14 dispatch methods total (confirmed via grep):
func (c *Client) Read(ctx context.Context, r any, vars map[string]string) (any, error)
func (c *Client) Create(ctx context.Context, cr any, val any, vars map[string]string, opts ...WriteOptions) (createdDirs []string, err error)
func (c *Client) Patch(ctx context.Context, p any, patch any, vars map[string]string, opts ...WriteOptions) error
func (c *Client) Delete(ctx context.Context, d any, vars map[string]string, opts ...DeleteOptions) (existed bool, err error)
func (c *Client) List(ctx context.Context, l any, vars map[string]string, opts ...ListOptions) (any, error)
func (c *Client) Exists(ctx context.Context, e any, vars map[string]string) (Entry, bool, error)
func (c *Client) Copy(ctx context.Context, cp any, srcVars, dstVars map[string]string) error
func (c *Client) Move(ctx context.Context, mv any, srcVars, dstVars map[string]string) error
func (c *Client) Mkdir(ctx context.Context, m any, vars map[string]string, opts ...MkdirOptions) (created bool, err error)
func (c *Client) BatchDelete(ctx context.Context, bd any, varsList []map[string]string) ([]BatchDeleteResult, error)
func (c *Client) OpenRead(ctx context.Context, sr any, vars map[string]string, opts ...StreamReadOptions) (io.ReadCloser, error)
func (c *Client) OpenWrite(ctx context.Context, sw any, vars map[string]string, opts ...WriteOptions) (io.WriteCloser, error)
func (c *Client) ListDir(ctx context.Context, ld any, vars map[string]string, opts ...DirOptions) ([]DirEntry, error)
func (c *Client) DeleteDir(ctx context.Context, dd any, vars map[string]string, opts ...DirOptions) ([]string, error)
```

### Correction (found during a Tier 2 coverage review): these 14 are NOT "the same shape"

A follow-up review read every one of these 14 signatures and their
role types directly — the original claim above ("same shape") does not
hold. The real picture:

| Method | Role type | `T` appears in return? | Notes |
|---|---|---|---|
| `Read` | `Reader[T]`/`*ReadHandle[T]` | yes — bare `T` | True Tier 1-shaped candidate |
| `Create` | `Creator[T]`/`*CreateHandle[T]` | no (`val any` is the `T`-shaped input, not the return) | `T` types BOTH the role and `val` |
| `Patch` | `Patcher[T,P]`/`*PatchHandle[T,P]` | no (`patch any` is `P`-shaped) | 2 type params (`T`, `P`) — the exact shape Round 1 compile-verified |
| `Delete` | `Deleter[T]`/`*DeleteHandle[T]` | no (`bool`) | `T` only types the role, unused in the body's return |
| `List` | `Lister[T]`/`*ListHandle[T]` | **no — returns `[]ListResult[T]` boxed as `any`**, per its own doc comment, NOT a bare `T` | Needs its own return-type design, not `(T, error)` |
| `Exists` | `Exister[T]`/`*ExistsHandle[T]` | no (`Entry, bool`) | `T` only types the role |
| `Copy` | `Copier[T]`/`*CopyHandle[T]` | no (plain `error`) | `T` only types the role |
| `Move` | `Mover[T]`/`*MoveHandle[T]` | no (plain `error`) | `T` only types the role |
| `Mkdir` | `MkdirCreator[T]`/`*MkdirHandle[T]` | no (`bool`) | `T` only types the role |
| `BatchDelete` | `BatchDeleter[T]`/`*BatchDeleteHandle[T]` | no (`[]BatchDeleteResult`) | `T` only types the role |
| `OpenRead` | `StreamReader[T]`/`*StreamReadHandle[T]` | no (`io.ReadCloser`) | `T` only types the role (codec-bypassing raw stream, per its own doc comment) |
| `OpenWrite` | `StreamWriter[T]`/`*StreamWriteHandle[T]` | no (`io.WriteCloser`) | `T` only types the role |
| `ListDir` | **`ListDirer` — confirmed via grep to be a PLAIN, NON-generic struct, no `[T any]` at all** | n/a | See callout below — does not need Go 1.27 |
| `DeleteDir` | **`DeleteDirer` — confirmed via grep to be a PLAIN, NON-generic struct, no `[T any]` at all** | n/a | See callout below — does not need Go 1.27 |

**`ListDir`/`DeleteDir` are a separate, lower-tier, Go-version-independent
fix.** Since their role types (`ListDirer`/`DeleteDirer`, `api/file/dir.go`)
carry no type parameter whatsoever, `Client.ListDir`/`.DeleteDir` don't
need a GENERIC method at all — they could become ordinary (non-generic)
methods simply accepting the concrete `ListDirer`/`DeleteDirer` type
instead of `any`, at ANY Go version, independent of this whole retrofit.
Recommend scoping them OUT of "Tier 2" proper and tracking them as their
own small, separate fix.

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

### What was NOT yet verified — now mostly resolved by review rounds, not the original design round

1. **RESOLVED (confirmed during a follow-up coverage review):** yes —
   Go 1.27 allows a method with its own NEW type parameter on top of a
   receiver that is itself already generic over a different type.
   Verified directly by compiling a throwaway `go1.27.2` file with
   `func (b Box[T]) Map[R any](fn func(T) R) Box[R]` and a second file
   mirroring `ports.PatchEncoded`'s exact shape — `func (p
   Patcher[T]) Patch[P any](patch P, fn func(T, P) T) Patcher[T]` —
   both compiled and ran successfully. `api/file.Client.Patch` and
   `ports.PatchEncoded` can safely assume this shape works.
2. **RESOLVED (Tier 2 coverage review), split per package-family —
   NOT uniform across all 4 packages as originally framed:**
   - **`api/file`: `BackendTypeMismatchError` can become FULLY dead
     code.** Grepped every one of its 16 real construction sites
     (`BackendTypeMismatchError{...}`) repo-wide: ALL 16 live inside
     `api/file` itself (`dispatch.go`, `dir.go`, `builder.go`) — ZERO
     live inside any adapter (`adapters/file`, `adapters/s3`,
     `adapters/smb`, …). `api/file`'s `Backend` interface operates on
     raw bytes/paths, not typed values, so there is no per-request
     reflection happening inside adapters at all for this family.
   - **`events`/`reqreply`/`rest`: `TransportTypeMismatchError` stays
     ALIVE permanently — cannot be removed.** Grepped every
     `TransportTypeMismatchError{...}` construction site repo-wide: the
     large majority (~26 of ~30) live INSIDE adapters
     (`adapters/mqtt5`, `adapters/zeromq`, `adapters/nethttp`,
     `adapters/mqtt`), via reflection, behind the `Transport`/
     `ClientTransport` interfaces — confirmed permanently non-generic
     per the governing restriction above. Only a handful of raise
     sites live client-side in `api/reqreply`/`api/rest` themselves
     (`client_registry.go`, `handletransport.go`, `route.go`,
     `dead_letter_replay.go`) — those few MIGHT become unreachable
     under a Tier 2 retrofit, but the error type itself cannot be
     deleted, since most of its raise sites are structurally
     unreachable by ANY caller-facing-only retrofit.
3. **RESOLVED (Tier 2 coverage review), split per package-family, and
   confirmed with a real `go1.27.2` compile check, not guessed:**
   - **`api/file`: full internal simplification is plausible.** Its
     dual-accept mechanism (bare role builder OR pre-built
     `*XxxHandle[T]`) is implemented via an unexported per-operation
     marker interface (e.g. `readDispatcher`) that both already
     implement, and its internal `xCore[T]` helpers (e.g.
     `readCore[T]`) already produce a genuinely concrete `T` — today
     only boxed to `any` at the return type. A throwaway compile check
     made the interface itself generic —
     `type readDispatcher[T any] interface { dispatchRead(vars map[string]string) (T, error) }`
     — with a matching generic method `func (c *Client) Read[T
     any](r readDispatcher[T], vars map[string]string) (T, error)`:
     BOTH dual-accept call shapes (`c.Read(bareRoleValue, vars)` and
     `c.Read(preBuiltHandle, vars)`) compiled and resolved `T` via
     inference with ZERO explicit type arguments, zero reflection,
     zero `any` boxing. `api/file`'s internal dispatch core can
     plausibly become FULLY generic, not just its caller-facing
     signature.
   - **`events`/`reqreply`/`rest`: internal simplification stays
     near-ZERO, as originally expected.** Confirmed via
     `api/reqreply/client.go`'s real `Call` body: it forwards
     `route any, req any` straight to `t.Call(ctx, route, req,
     opts...)` where `t` is the `ClientTransport` INTERFACE — there is
     no package-local type-assertion/dispatch layer to simplify at all
     in these 3 packages; all the real reflection-based type recovery
     happens one layer down, inside each adapter, permanently
     unreachable by this retrofit (same governing restriction). Only
     the caller-facing signature gets safer; the dispatch mechanism
     underneath is untouched.
4. **RESOLVED (`design-builder-constructors`, this session):**
   `forge.NewFunction`'s own Tier 1 classification — dropped from this
   doc's Tier 1 table entirely. Confirmed, via a real compile check AND
   a repo-wide blast-radius grep, to be a top-level constructor (no
   pre-existing receiver), not a method-retrofit case at all — see
   "Resolved: the 4 constructor symbols" above. Same resolution applies
   to its 3 siblings (`api/llm.NewCall`/`api/reqreply.NewRoute`/
   `api/rest.NewRoute`), all 4 confirmed to stay free functions.
5. **RECOMMENDED (Tier 2 coverage review): split Tier 2 implementation
   into 2 independent workstreams, not one combined round**, given
   points 2 and 3 above show these 2 groups have genuinely different
   risk/benefit profiles:
   - **Workstream A — `api/file`'s 12 true-generic dispatch methods**
     (`Read`/`Create`/`Patch`/`Delete`/`List`/`Exists`/`Copy`/`Move`/
     `Mkdir`/`BatchDelete`/`OpenRead`/`OpenWrite` — NOT `ListDir`/
     `DeleteDir`, see the correction table above). Higher value: full
     internal dispatch-core simplification AND
     `BackendTypeMismatchError` dead-code removal are both plausible,
     confirmed via a real compile check. Recommended to go FIRST.
   - **Workstream B — `events`/`reqreply`/`rest`'s `Publish`/
     `Subscribe`/`Call`/`CallAsync`/`Call`/`Consume`.** Lower
     confirmed value: caller-facing compile-time-safety win only,
     `TransportTypeMismatchError` stays alive permanently, zero
     internal dispatch simplification (confirmed — the real type
     recovery lives one layer down, inside each adapter, behind a
     permanently non-generic interface). Recommended to go SECOND, as
     its own separate planning round.
   - **`api/file`'s `ListDir`/`DeleteDir`** are a third, independent,
     even-smaller fix (see the correction table above) — not tied to
     Go 1.27 at all, could ship on its own schedule.

## Confirmed NOT retrofittable, either way

The internal `Transport`/`ClientTransport`/`ServerTransport`/
`api/file.Backend` interfaces themselves — what every adapter
implements (`adapters/nethttp`, `adapters/chi`, `adapters/mqtt5`,
`adapters/mqtt`, `adapters/zeromq`, `adapters/file`, `adapters/s3`,
`adapters/smb`, …) — stay `any`-typed/reflection-based regardless of
this retrofit. Interface methods can never gain type parameters in Go,
1.27 or any future version under the language's current generics
design. This is a hard, permanent boundary, not a temporary gap.

**A second, equally permanent category, found during a follow-up
coverage review**: Go only allows defining a method on a type in the
SAME package where that type is declared — a free function in a
DIFFERENT package can never become a method on a type it merely
imports, no matter how many of this restriction's doc comments it
cites. Two confirmed examples that look like Tier 1 candidates but are
NOT:

- `adapters/nethttp.ServeOne[Req, Resp](r rest.Route[Req, Resp])
  (http.Handler, error)` — its own doc comment cites "Go forbids
  generic methods," but even post-1.27 it can never become a method on
  `rest.Route[Req, Resp]`, since `rest.Route` is declared in `api/rest`,
  not `adapters/nethttp`.
- `adapters/mqtt/caller.go`'s analogous package-level generic function
  (its own doc comment explicitly says "not a method on `[*caller]`" for
  the same reason) — same cross-package restriction, independent of the
  interface-method restriction above.

Any future audit should check EVERY doc-comment hit against this
cross-package rule before counting it as a true Tier 1 candidate.

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
- `codex/either.go` (+ `either_test.go`) — `MapLeft`/`MapRight`/`Fold`
  (added during coverage review)
- `api/file/builder.go`, `api/file/dispatch.go`, `api/file/client.go` (+ tests)
- `api/events/builder.go` (+ tests), every adapter/example calling
  `Client.Publish`/`.Subscribe`
- `api/reqreply/client.go` (+ tests), every adapter/example calling
  `Client.Call`/`.CallAsync`
- `api/reqreply/route.go` (+ tests) — ADD `Route[Req,
  Resp].WithHandleCallback` method (resolved design, see
  `design-withhandlecallback` above); `api/reqreply/router.go`'s free
  `WithHandleCallback[Req, Resp]` function stays UNCHANGED (still needed
  for the route-less `NewRoute(..., WithHandleCallback(fn))` inline-opt
  call shape) — `NewRoute` itself still flagged separately (added during
  coverage review)
- `api/rest/builder.go` (+ tests), every adapter/example calling
  `Client.Call`/`.Consume`
- `api/rest/middleware.go` (+ tests) — ADD `Route[Req,
  Resp].WithHandleCallback` method (resolved design, see
  `design-withhandlecallback` above); `api/rest/router.go`'s free
  `WithHandleCallback[Req, Resp]` function stays UNCHANGED (same
  route-less-call-shape rationale as the `api/reqreply` twin)
- `api/llm/call.go` (+ tests) — `NewCall` (added during coverage review)
- `forge/function.go` (+ tests) — pending Tier 1/2 classification
- `ports/file.go` (+ tests)
- `stream/transform.go`, `stream/topology.go` (+ tests) — `Apply`/
  `WithApply` (added during coverage review)
- ~~`internal/middleware/context_field.go`, `internal/middleware/bound.go`~~
  — **RESOLVED, no touch needed.** Re-examination (see "Resolved:
  `internal/middleware`'s `ContextField` setters and `bound.go`" above)
  found neither file has a true Tier 1 case: `SetContextFieldFromIn`/
  `SetContextFieldFromOut` are already shipped methods (not free
  functions), and `BoundNameOf` is a correctly-shaped free function for
  a reason unrelated to the method-type-param restriction.
- `examples/go-edge-models/models/iotedge/usecase/*.go` (+ tests)
- Every example under `examples/` exercising any Tier 2 method directly

## Out of scope

- The adapter-facing interfaces themselves
  (`Transport`/`ClientTransport`/`ServerTransport`/`api/file.Backend`
  and its ~10 optional extension interfaces) — confirmed permanently
  un-retrofittable, not a future phase of this same doc.
- Any OTHER Go 1.27 feature unrelated to generic methods — not
  investigated this round. A follow-up coverage review DID survey this
  toolchain's actual `api/go1.27.txt` and spun off three separate
  Explore-depth stubs rather than folding them into this doc: the new
  stdlib `uuid` package ([go127-stdlib-uuid.md](go127-stdlib-uuid.md)),
  `testing/synctest` for `stream` package tests
  ([go127-synctest-stream-tests.md](go127-synctest-stream-tests.md)),
  and a flag-only note on `encoding/json/v2`
  ([go127-json-v2-evaluation.md](go127-json-v2-evaluation.md)).
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
3. **`forge.NewFunction`'s Tier 1/Tier 2 classification — RESOLVED**
   (`design-builder-constructors`). Confirmed a top-level constructor
   (no pre-existing receiver to retrofit a method onto), not Tier 1 or
   Tier 2 — dropped from both. Its 3 "same flag" siblings
   (`api/llm.NewCall`/`api/reqreply.NewRoute`/`api/rest.NewRoute`) got
   the identical resolution in the same round. See "Resolved: the 4
   constructor symbols" above for the full compile-checked evidence.
4. **`internal/middleware`'s `ContextField`-setter workaround —
   RESOLVED.** Re-examined (`design-contextfield-setters`): NOT a true
   Tier 1 case. `SetContextFieldFromIn`/`SetContextFieldFromOut` are
   already shipped methods on `Middleware[In,Out]` (the
   `ContextFieldSetter` interface erases `V`, so no new type parameter
   was ever needed); `BoundNameOf` is a correctly-shaped free function
   for the SAME "value independent of any receiver" reason already
   established for `WithHandleCallback` (no natural in-package receiver
   type exists for a type-erased `bm any`). See "Resolved:
   `internal/middleware`'s `ContextField` setters and `bound.go`" above
   for the full trace. No code change needed; `impl-contextfield-setters`
   concludes as a no-op.
5. **Full Tier 1 inventory** — the table above reflects this session's
   own grep-based survey (doc comments explicitly citing the
   limitation); a dedicated audit pass may find more, structurally
   identical cases that simply lack an explicit doc-comment callout.
6. **Full Tier 2 per-package signature design** — this doc sketches the
   SHAPE, not the complete, final Go source for each of the ~16+ Tier 2
   methods across 4 packages; that level of rigor is deferred to the
   dedicated design round once staging (decision 1) is settled.

## Next steps

1. ~~`go127-audit-tier1-full-inventory`~~ — **DONE.** Tier 1 fully
   implemented and shipped (see "Tier 1 implementation — SHIPPED"
   below); open decisions 3 and 4 resolved along the way.
2. ~~`go127-audit-tier2-signatures`~~ — **PARTIALLY DONE** via the Tier
   2 coverage review above: the per-method return-shape correction
   table, the `ListDir`/`DeleteDir` non-generic finding, and the
   package-family split (resolved items 2/3 above) are now backed by
   real evidence, including a compile-verified generic dual-accept
   interface shape for `api/file`. STILL OPEN: the complete, final Go
   signature for each of `api/file`'s 12 true Workstream-A methods
   (only `Read`'s shape has been compile-checked end-to-end so far) —
   do this as the FIRST step of the Workstream A implementation round,
   per the recommended sequencing (Open design decisions item 5).
3. `go127-audit-blast-radius` — a real, per-symbol blast-radius audit
   (replacing the rough estimate below) — still fully open; do this at
   the START of the Workstream A implementation round.
4. `go127-audit-cross-package-rule` — apply the newly documented
   same-package-as-receiver rule (see "Confirmed NOT retrofittable")
   against every remaining doc-comment hit repo-wide, to rule out any
   further false positives beyond `adapters/nethttp.ServeOne` and
   `adapters/mqtt`'s analog.
5. **Recommended immediate next round: plan Workstream A
   (`api/file`'s 12 true-generic dispatch methods) implementation** —
   higher confirmed value than Workstream B (see Open design decisions
   item 5), starting with items 2 and 3 above. Workstream B
   (`events`/`reqreply`/`rest`) and `ListDir`/`DeleteDir`'s own small
   fix stay separate, later rounds.
6. Draft the full, Go-signature-rigor design once the audit lands —
   this doc, at that point, graduates from Explore to Refine depth.

## Tier 1 implementation — SHIPPED

Every confirmed true Tier 1 candidate has been implemented, with the
old free function removed outright (breaking change, no deprecated
wrapper, per the "Scope decisions" above) and every real call site
updated:

| Symbol (old) | Symbol (new) | Files changed |
|---|---|---|
| `codex.MaybeMap`/`MaybeFlatMap` | `Maybe[T].Map`/`.FlatMap` | `codex/maybe.go`, `codex/maybe_test.go` |
| `codex.EitherMapLeft`/`MapRight`/`Fold` | `Either[A,B].MapLeft`/`.MapRight`/`.Fold` | `codex/either.go`, `codex/either_test.go` |
| `api/file.WithPatch` | `Resource[T].WithPatch` | `api/file/builder.go`, `api/file/client_test.go`, `examples/file-api/*`, `examples/file-io/main.go`, `examples/go-edge-models/models/iotedge/usecase/*` |
| `ports.PatchEncoded` | `File[T].PatchEncoded` | `ports/file.go`, `ports/file_test.go`, `adapters/file/binding.go`, `examples/flat-key-patch/main.go` (+ doc-comment-only mentions across `adapters/file`, `format`, `ports`, `codex`, `api/file`, `examples/go-edge-models`) |
| `stream.Apply` | `Stream[In].Apply` | `stream/transform.go`, `stream/transform_test.go`, `examples/ports-plain-go/main.go`, `examples/stream-oee/main.go`, `examples/stream-pipeline/main.go` (+ doc-comment-only mentions across `forge`, `stats`, `adapters/mqtt5`, `adapters/nethttp`, `adapters/zeromq`, `adapters/mcpgo`, `ports`, `stream`, `render/stream`) |
| `stream.WithApply` | `(*Topology).WithApply` | `stream/topology.go`, `stream/topology_test.go`, `examples/stream-oee/main.go`, `examples/stream-pipeline/main.go` |
| `usecase.PatchDeviceFile`/`PatchUseCaseFile` | `(*Scope).PatchDeviceFile`/`.PatchUseCaseFile` | `examples/go-edge-models/models/iotedge/usecase/device.go`/`usecase.go`, `examples/go-edge-models/app/iotedge/usecase.go` |
| *(additive, old kept)* `WithHandleCallback` free function (`api/rest`, `api/reqreply`) | NEW `Route[Req,Resp].WithHandleCallback` method | `api/rest/middleware.go`, `api/reqreply/route.go`, + 4 example server files migrated to the chain-friendly method call |

Confirmed resolved as **no code change** (stay free functions):
`forge.NewFunction`, `api/llm.NewCall`, `api/reqreply.NewRoute`,
`api/rest.NewRoute` (see "Resolved: the 4 constructor symbols"),
`internal/middleware`'s `ContextField` setters and `BoundNameOf` (see
"Resolved: `internal/middleware`'s `ContextField` setters and
`bound.go`").

**Post-implementation verification** (whole repo, not just touched
packages): `go build ./...` ✅, `go vet ./...` ✅, `go test ./...` ✅
(all packages `ok`), `just check` ✅ (staticcheck + gosec: 0 issues;
gofmt: clean), `just examples` ✅ (full example sweep, all exit 0).
`.github/instructions/go-codex.instructions.md` updated for every
renamed symbol.

**Tier 2 remains fully deferred** — no Tier 2 work was attempted this
round; see "Tier 2 — the big, foundational finding" above and the
"Next steps" list for the still-open audit work before any Tier 2
implementation round.

## Workstream A (`api/file`'s Tier 2 half) — SHIPPED

Following the Tier 2 review's recommendation (see "Open design
decisions" item 5), Workstream A was implemented and shipped in full —
all 14 `api/file.Client` dispatch methods converted off `any`-typed
parameters. A finding made DURING implementation (not predicted by the
review) further split this into 3 groups, confirmed by reading every
real dispatcher interface in `api/file/dispatch.go`/`dir.go`:

| Method | Group | New signature |
|---|---|---|
| `Read` | A1 (real generic method) | `Read[T any](ctx, r readDispatcher[T], vars) (T, error)` |
| `Create` | A1 | `Create[T any](ctx, cr createDispatcher[T], val T, vars, opts ...WriteOptions) (createdDirs []string, err error)` |
| `Patch` | A1 | `Patch[T, P any](ctx, p patchDispatcher[T, P], patch P, vars, opts ...WriteOptions) error` — callers MUST supply explicit `[T, P]` type arguments; Go cannot infer `T` through an interface-typed role parameter here (confirmed via real compile errors during implementation, not assumed) |
| `List` | A1 | `List[T any](ctx, l listDispatcher[T], vars, opts ...ListOptions) ([]ListResult[T], error)` — no longer boxed in `any` |
| `Delete` | A2 (no Go 1.27 needed) | `Delete(ctx, d deleteDispatcher, vars, opts ...DeleteOptions) (existed bool, err error)` |
| `Exists` | A2 | `Exists(ctx, e existsDispatcher, vars) (Entry, bool, error)` |
| `Copy` | A2 | `Copy(ctx, cp copyDispatcher, srcVars, dstVars) error` |
| `Move` | A2 | `Move(ctx, mv moveDispatcher, srcVars, dstVars) error` |
| `Mkdir` | A2 | `Mkdir(ctx, m mkdirDispatcher, vars, opts ...MkdirOptions) (created bool, err error)` |
| `BatchDelete` | A2 | `BatchDelete(ctx, bd batchDeleteDispatcher, varsList) ([]BatchDeleteResult, error)` |
| `OpenRead` | **Reclassified A1→A2 during implementation** | `OpenRead(ctx, sr openReadDispatcher, vars, opts ...StreamReadOptions) (io.ReadCloser, error)` — `T` never appears in the interface method's own return (`io.ReadCloser`), so genericity added no value |
| `OpenWrite` | **Reclassified A1→A2 during implementation** | `OpenWrite(ctx, sw openWriteDispatcher, vars, opts ...WriteOptions) (io.WriteCloser, error)` — same reasoning |
| `ListDir` | A3 (already known non-generic) | `ListDir(ctx, ld listDirDispatcher, vars, opts ...DirOptions) ([]DirEntry, error)` |
| `DeleteDir` | A3 | `DeleteDir(ctx, dd deleteDirDispatcher, vars, opts ...DirOptions) ([]string, error)` |

Every one of the 14 now-redundant `dispatchXxx` `any`-typed free
wrapper functions was deleted, folded directly into the `Client`
method body.

**`BackendTypeMismatchError` final disposition: KEPT, not removed.**
15 of its 16 original raise sites are now genuinely dead code (a
non-conforming type fails to COMPILE instead of raising this at
runtime) — but one raise site, in `api/file/builder.go`'s
`withErrorPatterns` (an `ErrorPatterns[B]` type mismatch), is a
genuinely different kind of mismatch unrelated to dispatch-role
matching and stays reachable. This is a partial deviation from the
Round 4 review's "can become fully dead code" framing — confirmed
during implementation, not a guess.

**Files changed**: `api/file/client.go`, `api/file/dispatch.go`,
`api/file/dir.go` (core conversion); `api/file/client_test.go`,
`api/file/stream_test.go`, `api/file/filetest/filetest.go`,
`adapters/file/conditional_write_test.go`, `ports/file.go` (one line)
(within-module callers); `examples/dir-io/main.go`,
`examples/file-api/*.go` (9 files), `examples/file-io/main.go`,
`examples/go-edge-models/models/iotedge/usecase/device.go`/`usecase.go`
(external call sites — mostly removing now-unnecessary `any`
type-assertions on the old boxed returns, plus adding explicit `[T,
P]` type arguments at every `Patch` call site).

**Post-implementation verification** (whole repo): `go build ./...` ✅,
`go vet ./...` ✅, `go test ./...` ✅ (all packages `ok`), `just check`
✅ (staticcheck + gosec: 0 issues; gofmt: clean), `just examples` ✅
(full example sweep, all exit 0).
`.github/instructions/go-codex.instructions.md` updated for the full
`api/file.Client` dispatch-method signature table.

**Workstream B (`events`/`reqreply`/`rest`) remains fully deferred** —
confirmed lower value per the Tier 2 review (caller-facing-only win,
`TransportTypeMismatchError` stays alive permanently, zero internal
dispatch simplification) — not attempted this round.
