# Common-Base + Per-Pattern-Derived Middleware Types — `middleware`, `api/rest`, `api/reqreply`, `api/events`, future `ports`

> **Status:** SUPERSEDED by
> [D-0003 — Codec-Declared Middlewares](../design/d-0003-codec-declared-middlewares.md), which
> resolves this doc's core finding (a single shared `middleware.Middleware` struct
> carrying REST-only fields unused by `api/events`/`api/reqreply`) via an ADDITIVE
> `middleware.RouteMiddleware` marker interface + NEW per-pattern generic types
> (`rest.Middleware[In,Out]`/`events.Middleware[In,Out]`) — not this doc's original
> proposal to retrofit/split `middleware.Middleware` itself, which would have been a
> breaking change to already-shipped code. Kept for historical context; see the
> superseding doc for the finalized, ready-to-implement design.
>
> Original status was: Idea only — no driver yet. Spun out of
> [Pub/Sub Workflow Simplification](../design/d-0002-pubsub-workflow-simplification.md)'s
> critical-review finding (F6) that `middleware.Middleware` — a SINGLE,
> FLAT struct carrying `Name`, `Security *SecurityDeclaration`, AND 5
> REST-only param-contribution fields (`RequestHeaderParams`/
> `RequestCookieParams`/`RequestQueryParams`/`ResponseHeaderParams`/
> `ResponseCookieParams`) — is imported DIRECTLY by every layer
> (`api/rest`, `api/reqreply`, and now `api/events`), even though only
> REST has any use for the param-contribution fields. `api/reqreply`
> (topic-only, no headers/cookies/query, confirmed via code) and
> `api/events` (same) both carry this unused baggage on every `.Use()`
> call today. Fixing this properly means retrofitting REST's and
> reqreply's ALREADY-SHIPPED, in-use `middleware.Middleware`-consuming
> code — a genuinely bigger, cross-cutting, likely-breaking refactor,
> deliberately NOT undertaken as part of the pub/sub redesign. Pub/sub's
> OWN doc ships a pragmatic, non-breaking INTERIM fix instead (eager
> validation rejecting non-empty REST-only fields on pub/sub middleware
> — see that doc's F6 resolution) while this doc investigates the
> proper long-term fix. **Update, resolved in three steps since**: first
> superseded by D-0003 (above); D-0003's OWN relationship to
> [Feature](protocol-native-features.md) (formerly "Protocol-Native
> Feature Declarations," then "Feature/Provider") was then reopened, and
> has SINCE been RESOLVED there via a confirmed 4-stage lifecycle model
> (§3 of that doc) — `middleware.Middleware`/D-0003's `Declaration[In,Out]`
> and `Feature`'s sealed `Capability` mechanism turn out to occupy the
> SAME declare-time, spec-contributing lifecycle stage, without merging
> into one Go type. This doc's ORIGINAL struct-splitting proposal remains
> superseded by D-0003 either way — the further, now-RESOLVED question
> about D-0003's relationship to `Capability` was always a separate,
> later-stage matter, not a reopening of THIS doc's finding.
> [← Back to Roadmap](index.md)

## The idea

Restructure `middleware.Middleware` into a COMMON base type plus
per-API-pattern DERIVED types:

```go
// middleware — the common base, carries ONLY what's truly universal
// across every API pattern (REST, events, reqreply, future MCP/ports):
type Common struct {
    Name     string
    Security *SecurityDeclaration
}

// api/rest — REST-specific derived type, embeds Common + REST-only
// param-contribution fields:
type Middleware struct {
    middleware.Common
    RequestHeaderParams  []middleware.HeaderParamSpec
    RequestCookieParams  []middleware.CookieParamSpec
    RequestQueryParams   []middleware.QueryParamSpec
    ResponseHeaderParams []middleware.ResponseHeaderParamSpec
    ResponseCookieParams []middleware.ResponseCookieParamSpec
}

// api/events — events-specific derived type, embeds Common ONLY (no
// param-contribution fields exist for pub/sub's topic-only boundary):
type Middleware struct {
    middleware.Common
}

// api/reqreply — same as events (topic-only boundary):
type Middleware struct {
    middleware.Common
}
```

`Subscriber[T].Use(mws ...events.Middleware)`/`Publisher[T].Use(...)`
would then accept ONLY `events.Middleware` — a REST-oriented
`rest.Middleware` value literally CANNOT be passed (a Go compile error,
not a runtime check) — eliminating F6's mistake category entirely at
the TYPE level, stronger than the eager-validation runtime check.

## Why this is NOT undertaken as part of the pub/sub redesign

- **Breaking change to ALREADY-SHIPPED code.** `api/rest`'s
  `Route.Use(mws ...middleware.Middleware)` and `api/reqreply`'s
  `Route.Use(mws ...middleware.Middleware)` (shipped earlier THIS
  SESSION) would both need their signatures changed to accept their
  OWN derived type instead of the shared `middleware.Middleware` —
  every existing `.Use(...)` call site in both packages' examples/tests
  would need updating.
- **`middleware.ServerImplementation`/`ClientImplementation`/
  `CheckScopes`/`SecurityScheme`/`FromSecurityScheme`-style bridging
  functions** would all need re-examining: do they operate on `Common`
  now, or do they need to become generic over the derived type? Not
  investigated here.
- **Scope creep risk**: pub/sub's OWN redesign doc is already large;
  folding in a retroactive REST/reqreply refactor would conflate two
  genuinely separate efforts.

## Open questions (not answered — for a future design session)

- Does `Common` need to be exported as its own standalone type, or
  could Go's embedding achieve the same effect with `Common` staying
  unexported inside each derived type (trading external reusability for
  a smaller public API surface)?
- Do `ServerImplementation`/`ClientImplementation` (the runtime-Fn-
  carrying counterparts to declare-time `Middleware`) have the SAME
  "shared flat type, some fields unused per-boundary" issue? Not
  checked in this pass — a companion investigation.
- Should this restructuring happen INCREMENTALLY (e.g. ship
  `events.Middleware`/`reqreply.Middleware` as NEW, additive types
  first, deprecate direct `middleware.Middleware` use over time) or as
  one coordinated breaking change across all 3 packages at once?
- Is there a simpler middle ground — e.g. keep ONE shared
  `middleware.Middleware` struct, but make the REST-only fields
  themselves package-scoped/unexported outside `api/rest`'s own
  bridging functions (so `api/events`/`api/reqreply` literally cannot
  SET them, even though the fields still exist on the shared type)?
  Worth comparing against full type-level derivation before committing
  to the bigger refactor.

No implementation, no locked API — this doc exists to hold the finding
and scope a future investigation, not to answer it.
