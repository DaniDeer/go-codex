# REST Client-Side General-Purpose Middleware — `middleware`, `api/rest`, `adapters/nethttp`, `adapters/chi`

> **Status:** Idea only — no driver yet. Spun out of
> [Pub/Sub Workflow Simplification](../design/d-0002-pubsub-workflow-simplification.md)'s
> critical review of the middleware concept for non-spec-adding
> middlewares (like an Observer). That review traced REST's own
> dispatch code precisely and found a genuine, pre-existing gap:
> `Route.HandleMW` (server-side) recognizes TWO Fn shapes — the
> security-Fn shape and a general-purpose `func(http.Handler) http.Handler`
> (confirmed via `validateImplementationShapesReflect`) — but
> `Route.ClientMW` (client-side) recognizes only ONE shape, the
> credential-Fn shape (confirmed via `validateClientImplementationShapes`),
> hard-erroring on anything else. There is NO general-purpose hook on
> `ClientMW` at all, for observability or any other cross-cutting
> concern. Pub/sub designed its OWN general-purpose hooks for BOTH
> `SubscribeMW` and `PublishMW` directly in its own doc (not deferred),
> since the user explicitly asked for observer-and-beyond on both
> sides there — but the analogous REST-side fix is bigger (retrofitting
> already-shipped `ClientMW`/`Call` code) and deliberately NOT
> undertaken as part of that review. This doc investigates the REST
> side specifically.
> [← Back to Roadmap](index.md)

## Why client-side never needed a general-purpose hook for Observer specifically

`nethttp.Call`/`CallWithHandle` are single, self-contained functions —
unlike `Serve`, which dispatches MANY registered routes through ONE
shared `http.ServeMux` and therefore genuinely needs external
`func(http.Handler) http.Handler` composition to hook into each route's
lifecycle generically. `Call` already calls `obs.RecordRequest(...)`
DIRECTLY inside its own function body, driven by
ctx-injected/`CallOptions.Observer`-resolved `obs` (confirmed via
`docs/features/observer.md`'s context-observer table — `nethttp.Call`
resolves the SAME way MQTT/ZeroMQ client-side calls do). No middleware
wrapper was ever needed for THAT specific concern — `CallOptions.Observer`
staying a permanent per-call field (never migrated to `ClientMW`) is
CORRECT, not an oversight.

## The actual gap

`ClientMW`'s dispatch (`validateClientImplementationShapes`) recognizes
EXACTLY one Fn shape:
`func(context.Context, []route.SecurityRequirement) (http.Header, error)`
— anything else, including `nil`, either continues (nil) or returns
`middleware.MiddlewareShapeError`. There is no way today to attach ANY
general-purpose, non-credential behavior to `ClientMW` — not custom
request logging, not request/response transformation, not retry logic,
not distributed tracing beyond `stats.TraceObserver`. Every one of
these use cases currently requires either (a) wrapping `nethttp.Call`
itself in caller-side code (works, but loses the "attach once, declared
alongside the route" ergonomics `HandleMW`'s general-purpose hook
already gives server-side authors), or (b) inventing a bespoke,
route-specific mechanism each time.

## Proposed shape (sketch only, mirrors pub/sub's own design for symmetry)

Pub/sub's `PublishMW` faced an analogous problem (no caller-supplied
handler function to wrap — `Publish` just takes `msg T` directly) and
resolved it by wrapping the adapter's OWN internal encode+transmit
step:

```go
func(next func(context.Context, T) error) func(context.Context, T) error
```

REST's `Call` has an analogous internal step ("send the request over
the wire, get the response back") that a general-purpose `ClientMW` hook
could wrap the SAME way:

```go
func(next func(context.Context, Req) (Resp, error)) func(context.Context, Req) (Resp, error)
```

An UNPAIRED (general-purpose, `mw` nil or `Security` nil) `ClientMW`-
attached Fn of this shape would run UNCONDITIONALLY, composing around
`Call`'s actual "do the HTTP round trip" step — enabling custom
tracing, request/response transformation, retry logic, or any other
cross-cutting concern, declared once via `.ClientMW(nil, fn)` and
applied consistently to every `Call` against that route.

## Open questions (not answered — for a future design session)

- Does this shape correctly account for REST's PATH/QUERY/HEADER/COOKIE
  merge-field derivation, which happens BEFORE `Call`'s network step
  today? Should the general-purpose hook wrap the WHOLE `Call` function
  (including merge-field derivation) or only the network round-trip
  specifically? Pub/sub's `PublishMW` equivalent wraps only the
  encode+transmit step, not topic-var derivation — worth confirming
  the same split is correct for REST, or whether REST's additional
  param-merge complexity changes the answer.
- Does `CallOptions.Observer` stay UNCHANGED (a permanent per-call
  field, per the reasoning above), with the new hook purely ADDITIVE
  for other use cases — or does adding a general-purpose hook create
  pressure to ALSO offer an `Observability`-equivalent for the client
  side, duplicating what ctx-injection already provides? (Likely
  answer: keep `CallOptions.Observer` unchanged, the new hook is for
  OTHER concerns — but not decided here.)
- Should `middleware.MiddlewareShapeError` be reused as-is (it already
  is transport/boundary-agnostic by name and shape, confirmed via
  pub/sub's own design reusing it directly), or does REST warrant its
  own client-specific shape-error variant?
- Is there a genuine concrete use case driving this (an app that
  actually needs client-side request transformation/custom retry logic
  TODAY), or is this purely a symmetry-driven, no-driver-yet idea? This
  doc's own "Idea only" status reflects that no concrete driver has
  been identified yet.

No implementation, no locked API — this doc exists to hold the
confirmed gap and scope a future investigation, not to answer it.
