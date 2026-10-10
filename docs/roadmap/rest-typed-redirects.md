# Typed HTTP Redirects — `api/rest`

> **Status:** Design draft — no code written yet. Spun out of a direct
> user question ("how do I send HTTP 3xx responses, especially a 303 See
> Other redirect?") that exposed a genuine gap, not a documentation gap.
> Critically re-reviewed against the actual code once (not just the
> initial sketch) — see "Resolved design decisions" below; only 3 small
> items remain genuinely open, all in one subsection (the client-side
> route registry).
> [← Back to Roadmap](index.md)

## Why this exists

`api/rest` has no first-class concept of an HTTP redirect today. A user
asked specifically how to send a 303 See Other (and 3xx in general), and
proposed a concrete design: let a declared route point directly at
ANOTHER declared route as its redirect target — server-side AND
client-side — so neither end has to know the destination as a bare
string URL.

## Confirmed current state (via code inspection, not assumption)

- **`RouteMeta.RespStatus`** is the only existing status-code
  customization point, and it is a STATIC per-route string (default
  `"201"` for POST, `"200"` otherwise) — not a runtime/per-request
  decision. A route could be declared with `RespStatus: "303"`, but every
  response from that route would then always be a 303, which is not
  what a redirect means.
- A `Location` response header CAN already be declared today, as a
  workaround, via the existing generic
  `NewRequiredResponseHeaderParam`/`ResponseHeaderParam` mechanism (it
  accepts any header name) — but this is a manual, unenforced convention,
  not a typed concept the framework understands.
- **The adapter always encodes and writes the handler's `Resp` as the
  response body** (`adapters/nethttp/adapter.go`, `adapters/chi/adapter.go`)
  — there is no way for a handler to suppress the body and return "just a
  redirect" today.
- **Confirmed client-side risk**: `adapters/nethttp` never overrides
  `http.Client.CheckRedirect` anywhere in the codebase (grepped
  exhaustively) — Go's default `net/http` behavior silently auto-follows
  3xx responses, up to 10 hops, with no caller visibility. If the
  `RespStatus="303"` + manual `Location` header workaround above were used
  today, a go-codex client would transparently follow it and decode the
  redirect TARGET's body as if it were the ORIGINAL route's `Resp` type —
  a silent type mismatch, not an error.
- **`adapters/chi` has NO client transport at all** (grepped
  exhaustively, zero matches) — it is server-dispatch-only. The entire
  client-side half of this feature (`CheckRedirect` override,
  `Client.Call`/`CallWithTransport` redirect handling) is scoped to
  `adapters/nethttp` ONLY. `adapters/chi` only ever needs the
  server-side half (recognizing `RedirectError`, writing `Location` +
  status, suppressing body) — already shared with nethttp's server
  dispatch shape, no separate design question there.
- **Related but distinct**: `docs/roadmap/idea-codec-defined-hateoas.md`
  covers typed links embedded INSIDE a response body for client
  navigation. This feature is about an actual protocol-level 3xx status +
  `Location` header — not a body convention. Cross-referenced, not
  merged.

## Scope decisions (confirmed with the user)

1. **Status codes**: the general redirect family — 301, 302, 303, 307,
   308 — not just 303.
2. **Server-side shape**: a route's redirect points DIRECTLY at another
   registered `rest.Route`/`rest.SSERoute` (a typed target, not a bare
   string), with the `Location` resolved from the target's OWN path
   template plus caller-supplied vars — reusing the existing
   `PathParam`/merge-field machinery rather than inventing a second one.
3. **Client-side shape**: `rest.Client.Call` transparently follows a
   recognized redirect and decodes the target route's typed `Resp`, via
   a NEW client-side route registry (see "Client-side route registry"
   below — this needed real design work, it wasn't free). `rest.
   CallWithTransport` does NOT auto-follow (confirmed structural reason,
   see "Resolved design decisions" #2 below).
4. **Disable `http.Client`'s automatic redirect-following** by default
   for go-codex-managed client transports, so every redirect becomes an
   explicit, typed, caller-visible concept instead of `net/http`'s silent
   default.

## Resolved design decisions (closed via direct code inspection)

The original draft flagged 5 "open design decisions." A critical
re-review, reading the actual implementation rather than re-guessing,
closed 4 of them outright and corrected one that was simply wrong. Only
the client-side registry (not one of the original 5 — a new finding)
needed real design work; see its own subsection below.

1. **Spec rendering already supports multiple response entries — the
   original "biggest scope-creep risk" claim was WRONG.**
   `ResponseMeta` (`api/rest/builder.go`) already exists for exactly this
   ("additional response entries (error codes, redirects, etc.)" — its
   own doc comment already says "redirects"), and
   `render/openapi/document.go`'s `buildResponses` already iterates EVERY
   entry in `r.Responses`, not just `[0]`. `adapters/nethttp/
   adapter.go`'s `primaryStatusFor`'s `Responses[0]`-only read is a
   RUNTIME dispatch concern (which status to actually send on a given
   call), structurally unrelated to spec rendering, which already
   supports multiple entries. **No new spec mechanism needed** — a
   redirect-capable route documents its possible 3xx via a normal
   `ResponseMeta{Status: "303", ...}` passed to `NewRoute` alongside the
   primary response, exactly like today's existing 400/404
   extra-response pattern.
2. **How a handler signals "redirect" — the error-return approach is
   confirmed as the right (and cheaper) choice.**
   `adapters/nethttp/adapter.go`'s `resp, err = fn(ctx, req); if err !=
   nil { ... }` is the exact, already-existing hook point. A
   `RedirectError` recognized via `errors.As` right there — BEFORE
   `tryRespondErrorPatternGeneric`/`handle.ErrorStatusFor` — is
   architecturally free; no new per-success-path signal (e.g. a
   context-based mechanism mirroring `WithResponseHeaders`) is needed.
3. **307/308 method preservation is directly enforceable.**
   `Route.Descriptor.Method` is directly readable. `Redirect()` validates
   `target.Method == originating route's Method` when `status` is
   307/308 (method/body MUST be preserved per RFC 9110) and returns a
   typed `RedirectMethodMismatchError` if they differ; 301/302/303 have
   no such constraint (the client is allowed to switch to GET).
4. **Cross-`Client`/`Server` redirect targets are structurally out of
   scope, not just deferred.** `Redirect[TReq,TResp](status, target
   Route[TReq,TResp], vars)` requires an actual in-process `Route` value.
   A different server/process's route has no such value in THIS
   process's memory at all — cross-Client/Server targets aren't merely
   unsupported, they're not representable by this API shape at all.
5. **`CallWithTransport`'s redirect behavior is now confirmed, not just
   sketched.** Since `Resp` is statically fixed at that call's generic
   signature and `CallWithTransport` has no `Client` to hold a registry
   against (see next subsection), it stays registry-free by design: on
   receiving a redirect, it returns a typed `RedirectError` as its `err`,
   and the caller makes a SEPARATE, explicit follow-up call themselves
   with the right `Resp` type. This is a confirmed, intentional
   asymmetry with `Client.Call` (not an oversight) — `Client.Call`'s
   whole reason to exist is the registry/attach ceremony;
   `CallWithTransport` is explicitly the no-ceremony, single-route escape
   hatch, so it's consistent for it to also skip the auto-follow
   convenience.
6. **`http.Client.CheckRedirect` cannot simply be "disabled by default"
   — `ClientTransportOptions.HTTPClient` is caller-supplied.** Confirmed
   via code: `newCaller` stores the caller's `*http.Client` as-is;
   `client.Do(...)` is called directly at 4 call sites across
   `clienttransport.go`/`binding.go`/`client.go`. go-codex never
   constructs the `*http.Client` itself, so it cannot unilaterally flip a
   global default on someone else's value. **Resolution**:
   `NewClientTransport` makes a SHALLOW COPY of the caller's `*http.
   Client` (`http.Client`'s fields — `Transport`, `Jar`, `Timeout`,
   `CheckRedirect` — are all directly copyable) with `CheckRedirect`
   overridden to return `http.ErrUseLastResponse` (so `Do` returns the
   3xx response directly instead of auto-following). This does NOT
   mutate the caller's own shared client — a safe, standard Go idiom,
   zero caller action required for the "explicit by default" goal.
7. **Location construction reuses an existing method, confirmed.**
   `RouteHandle.BuildPath(vars map[string]string) (string, error)`
   already exists (used today for client-side path construction) —
   `Redirect` reuses this directly via `target.ClientHandle().
   BuildPath(vars)`; no new path-building mechanism needed.

## Client-side route registry (the one genuinely new mechanism)

**This registry is not merely a mechanism the redirect feature happens to
need — it completes REST's client-side parity with `api/events`'
already-established PRIMARY workflow, and should be documented as such.**
`docs/features/http-client.md` already calls `rest.Client.Call` (bound
via `Client.Attach`) **"the single-workflow"**, with `rest.
CallWithTransport` explicitly named "the lower-level, handle-based
primitive" — i.e. the primary-vs-escape-hatch framing already exists for
REST today. `docs/design/d-0006-protocol-native-capabilities.md`'s
Phase 5a section (status: Implemented) independently, authoritatively
states the SAME framing for `api/events`: *"events.SubscribeHandle/
PublishHandle (the 'attach and drive' verbs) ALREADY live in api/events
itself... This is the reference shape Phase 5a brings REST and reqreply
to."* What Phase 5a's analysis did NOT cover (redirects weren't a
concept yet) is that `events.Client` ALSO already has an
incoming-dispatch registry (`specByTopic`/`subscriberByTopic`) backing
its primary workflow — something `rest.Client` never had, since REST's
`Client.Call` only ever needed to look UP (caller→server), never to
resolve an arbitrary INCOMING identifier back to a known route. This
registry closes exactly that one remaining asymmetry: once it ships,
`Client.Call`+registry is accurately described as THE documented primary
REST client workflow, symmetric to `events.Client.Publish`/`Subscribe`'s
registry-backed primary workflow — not a redirect-only side mechanism.
(Updating `http-client.md`/`events.md` themselves to state this
cross-reference explicitly is deferred to this roadmap's last phase,
once the registry is actually implemented — tracked here so that step
isn't lost.)

**A related comparison to explicitly rule out**: `CallWithTransport`
(REST) mirrors `events.PublishHandle`/`SubscribeHandle` (events) exactly
— both are the single-endpoint, no-`Client`/no-registry escape hatch,
already cross-referenced in `docs/features/events.md` today ("mirrors
rest.CallWithTransport"). `adapters/nethttp.ServeOne` is a DIFFERENT,
unrelated concept and does NOT belong in this comparison, despite
surface-level "also feels like a lower-ceremony option" intuition:
`d-0006` itself already resolved this explicitly — `ServeOne` builds a
bare `http.Handler` to mount into a caller-owned, EXTERNAL
`*http.ServeMux`/app router, an inherently HTTP-specific use case with,
quoting the design doc directly, "NO structural equivalent in
MQTT/ZeroMQ/reqreply." It is not part of the
`CallWithTransport`/`PublishHandle`/`SubscribeHandle` no-ceremony tier at
all — a different axis (embedding into a 3rd-party router) from the one
this registry discussion is about (resolving an incoming identifier back
to a known route/channel).

**The gap**: `rest.Client` (api/rest/builder.go) is a thin wrapper —
`struct { mu; transport ClientTransport }` — nothing else. `Client.Call
(ctx, route, req)` takes the route value FRESH at every call; there is
no persisted mapping from a path back to a route anywhere. This means
"Client.Call auto-follows and decodes the target route's typed Resp" is
not actually buildable as originally stated — there's nothing to decode
an arbitrary 3xx `Location` against without first knowing which route it
corresponds to.

**Precedent, confirmed by direct comparison with `api/events`**: does
`events.Client` have an equivalent registry? Yes — `specByTopic`
(populated by `Publisher.Handle`/`Subscriber.Handle`) and
`subscriberByTopic` (populated by `Subscriber.Register`) are both exactly
this shape: a client-side registry keyed by topic, accumulated as
channels get declared/registered against the Client, consulted later by
`ServeSubscribers`/`AsyncAPISpec`. Giving `rest.Client` an equivalent
registry is therefore NOT an unprecedented new mechanism — it brings
`rest.Client` in line with `events.Client`'s already-established
pattern, just keyed by Method+PathTemplate with pattern MATCHING instead
of topic-string equality (since REST paths carry `{var}` segments).

**Matching mechanism already exists and is reusable**:
`internal/templatematch.MatchNonWildcard(template, concrete,
wrapMismatch)` is the exact reverse of `Build` (concrete path → vars,
given a template) — already used by the router for incoming-request
dispatch. The client-side registry reuses this directly for resolving an
incoming `Location` against every registered template; no new matching
algorithm needed.

**Design, confirmed with the user:**

- `rest.Client` gains an internal `routes map[routeKey]*registeredRoute`
  (routeKey = Method + PathTemplate), guarded by the existing `mu`.
- **Auto-populated for free**: every `Client.Call(ctx, route, req)`
  already receives a route value — the first time a given route is seen,
  the client indexes it into `routes` automatically (via the same
  reflection `clientTransport.Call` already does to recover a
  `*RouteHandle`).
- **Explicit opt-in escape hatch**: a new `Client.RegisterRoute(route
  any) error` (mirrors `events.Subscriber.Handle`'s "register without a
  live call" shape) for warming the registry with a route that's only
  ever a redirect TARGET, never called directly.
- **Resolution on receiving a redirect**: `clientTransport.Call`
  (nethttp) detects a 3xx via the overridden `CheckRedirect`, reads the
  `Location` header, and consults the registry: for each registered
  route whose Method matches (GET for 301/302/303; the ORIGINATING
  request's method for 307/308), tries `templatematch.MatchNonWildcard
  (routeTemplate, location, ...)`; on a match, it skips decoding the
  (typically empty) 3xx body and immediately issues the follow-up request
  itself (reusing the matched `*RouteHandle`'s own Call path), returning
  that decoded `Resp` — this is "transparent auto-follow" as originally
  scoped. On no match, it returns `UnrecognizedRedirectError{Location,
  Status}`.
- **Ambiguous-match policy, confirmed: first-registered-wins** — mirrors
  `events.Client`'s own `specByTopic` dedup policy exactly, no new
  precedent invented, if two registered routes' templates both match the
  same concrete `Location`+Method.
- **`UnrecognizedRedirectError` carries the raw `Location`+`Status`**,
  confirmed — a caller can fall back to a manual, untyped follow-up
  (e.g. their own `http.Get`) when the registry has no match; it is not
  a hard, opaque stop.
- **`CallWithTransport` stays registry-free** — see "Resolved design
  decisions" #5 above; this is the confirmed, intentional asymmetry
  between the two call shapes.
- **New idempotency requirement, surfaced while planning the examples
  rework below**: a single underlying route commonly has MULTIPLE
  credential-bound `ClientHandle` variants in real code (e.g.
  `examples/rest-api/client`'s `CreateUserRouteAsAlice`/`AsAdmin`/
  `Unauthenticated` — identical Method+PathTemplate, only the attached
  `ClientBoundMW`/`ClientMW` credential-providing closures differ). This
  is NOT the "ambiguous match" case above (which is reserved for two
  UNRELATED routes whose DIFFERENT templates both happen to match the
  same concrete path) — these variants share the exact same routeKey.
  **Registering (via `RegisterRoute`) or auto-populating (via `Call`)
  the SAME routeKey more than once, through different credential-bound
  variants of the identical underlying route, MUST be a safe, silent
  no-op — never an error, and never an overwrite that could change the
  Resp-decoding behavior** (every variant decodes the identical `Resp`
  type; only the credential plumbing differs, which the registry's own
  job — "what does an incoming path decode as" — has no reason to care
  about).

**Two examples, demonstrating the registry's two distinct population
paths** (both kept deliberately, not a replacement of one by the other —
see "Files to create" below for the full file list):

- `examples/rest-redirect/main.go` (standalone, minimal) demonstrates
  **auto-populate-via-`Call`** — the zero-ceremony default: call the
  target route once normally, then a redirect to it "just works."
- `examples/rest-api/demo_redirect.go` (the existing flagship,
  dual-adapter example) demonstrates the **explicit `RegisterRoute`
  construction-time** half instead: that example's
  `examples/rest-api/client`'s `Build()` function is reworked to loop
  over every one of its ~17 distinct underlying routes
  (`CreateUserRoute`, `GetUserRoute`, `ProfileRoute`, `AdminActionRoute`,
  `LoginRoute`, etc.) and call `Client.RegisterRoute` for each — using
  each route's plain, non-credential-bound base declaration (registering
  any ONE credential-bound variant would be equally correct per the
  idempotency rule above, but the base declaration is the clearest,
  least-redundant choice for a demo meant to show the registry plainly)
  — right after `Client.Attach`, making the client's full route
  vocabulary an explicit, deterministic part of the "assemble the
  client" phase (the package's own existing doc comment already calls
  this the "assemble phase, client variant") instead of leaving registry
  population to incidental demo-call order. `demo_redirect.go` itself
  then shows: the primary workflow (calling the redirect-source route,
  auto-follow succeeds because the TARGET was already known from
  `Build()`, not from an earlier demo happening to call it first) plus a
  secondary, explicitly-labeled `CallWithTransport` scenario (manual
  follow-up, bypassing the shared Client/registry entirely) — mirroring
  `examples/adapters-nethttp-client`'s own Section-0-then-escape-hatch
  narration style, in a realistic, multi-route, dual-adapter,
  already-secured-and-observed context.

**Forward-looking note — do NOT extract a shared `internal/registry` package now.** The
question was evaluated explicitly (go-codex already has precedent for consolidating
cross-pattern mechanics into `internal/<name>` packages — `internal/middleware`,
`internal/route`, `internal/router`). Conclusion: premature today. `events.Client` already has
5 registry-shaped maps (`specByTopic`, `subscriberByTopic`, `deadLetterTopicsRegistered`,
`schemas`, `connectSecuritySchemes`), but each has bespoke dedup semantics not duplicated with
each other (type-conflict-checked first-wins, last-wins, plain existence-check, plain
overwrite) — consolidating them today would mostly add indirection, not remove real
duplication. This planned `rest.Client` registry needs genuinely harder REVERSE PATTERN
MATCHING (concrete `Location` → best-matching registered template, via
`templatematch.MatchNonWildcard`) that none of `events.Client`'s maps need (they only ever do
exact-string key lookups) — structurally different enough that designing a shared abstraction
AHEAD of a real, working `rest.Client` implementation risks guessing the wrong boundary.
`internal/router` itself was only extracted after the SAME mechanics were independently,
near-identically implemented 3 TIMES across rest/events/reqreply — proven duplication, not
speculation; today there is 1 real registry (events') + 1 structurally-different plan (this
one) — not yet that bar. **Action**: once this registry ships, a FOLLOW-UP round should compare
it against `events.Client`'s maps and decide THEN whether `internal/registry` consolidation is
warranted. (A related idea — multiple registered operations/codecs per `ports.File`/`Cache`,
e.g. full-reads vs. partial-reads vs. partial-updates — was also evaluated and found to be a
SEPARATE, PRIOR, unresolved design question: `ports.File`/`Cache` have no `Client`/`Server`-like
registry-bearing object at all today, confirmed via `docs/roadmap/
mcp-ports-declarative-middleware.md`'s existing investigation — a registry question doesn't
even apply until that prerequisite is designed. Not pursued further here.)

**Spun out into its own tracked roadmap doc**: whether `api/reqreply.Client`
should get an equivalent registry (it currently has none — its own doc
comment calls it "purely a dispatch handle," a claim that stops being fully
accurate across all 3 `api/*` `Client` types once THIS registry ships) is
now tracked separately in
[`reqreply-client-registry.md`](reqreply-client-registry.md) — "idea only,"
explicitly sequenced to start only after this doc's registry ships, with 3
candidate motivations recorded and deliberately left unresolved there.

## Proposed API surface (tentative — sketch only, not final)

```go
// RedirectError signals that a handler wants the caller redirected to
// another registered route, instead of returning a normal response body.
// Implements error and slog.LogValuer per the structured-errors convention.
type RedirectError struct {
    Status int // one of 301, 302, 303, 307, 308
    // unexported: resolved target path + the originating route, for
    // the adapter to render the Location header and for the client
    // to recognize and decode against.
}

func (e RedirectError) Error() string
func (e RedirectError) LogValue() slog.Value

// Redirect resolves target's path template against vars (via
// target.ClientHandle().BuildPath) and returns a RedirectError a handler
// can return as its error value. Fails with a typed
// RedirectTargetVarError if a var is missing or fails the target
// route's own codec constraint, or RedirectMethodMismatchError if status
// is 307/308 and target's Method differs from the originating route's.
func Redirect[TReq, TResp any](status int, target Route[TReq, TResp], vars map[string]string) (RedirectError, error)

// RedirectToSSE mirrors Redirect for an SSERoute target.
func RedirectToSSE[TReq, TEvent any](status int, target SSERoute[TReq, TEvent], vars map[string]string) (RedirectError, error)

// RegisterRoute warms Client's redirect-target registry with route
// without making a live call against it — for a route that is only
// ever reached as a redirect target. Routes called directly via
// Client.Call are indexed automatically; this is only needed for
// targets never called directly.
func (c *Client) RegisterRoute(route any) error

// UnrecognizedRedirectError is returned by Client.Call when a received
// redirect's Location does not match any route registered on c (via a
// prior Call or RegisterRoute) — carries the raw Location+Status so a
// caller can fall back to a manual follow-up.
type UnrecognizedRedirectError struct {
    Location string
    Status   int
}
```

- Adapter dispatch (`nethttp`, `chi`) recognizes `RedirectError` via
  `errors.As`, BEFORE `ErrorPattern`/`ErrorStatus` matching — this
  mirrors the already-established sentinel-error convention elsewhere in
  the codebase, not a new dispatch shape.
- `adapters/nethttp`'s client transport construction overrides
  `http.Client.CheckRedirect` (on a shallow copy of the caller-supplied
  client) to `http.ErrUseLastResponse` by default, so `net/http`'s own
  auto-follow never fires underneath go-codex's explicit handling.

## Structured errors

Every new error type (`RedirectError`, `RedirectTargetVarError`,
`RedirectMethodMismatchError`, `UnrecognizedRedirectError`) MUST
implement `slog.LogValuer` per the repo's standing structured-errors
rule — no exception for this feature. Per the also-standing
internal-package-error-aliasing rule
(`.github/instructions/go-codex.instructions.md`), if any of these are
built on a shared `internal/*` mechanism (e.g. `templatematch`'s own
mismatch errors), the SAME-named public alias must exist in `api/rest`'s
own vocabulary from day one — not retrofitted later.

## Observer integration

A redirect taken (server-side) and a redirect followed (client-side) are
both candidate `stats.Observer`-style events — exact hook names/shapes
not yet decided; should be designed alongside implementation now that
the dispatch hook point (the existing `if err != nil` branch) and the
registry resolution point (`clientTransport.Call`) are both concretely
identified above.

## Unit test plan (sketch, to refine during implementation)

| Area | Test |
|---|---|
| Server | A route returning `RedirectError` renders the correct status + `Location` header, no body |
| Server | `Redirect` with a missing/invalid var returns `RedirectTargetVarError`, not a panic |
| Server | `Redirect` with status 307/308 and a mismatched target Method returns `RedirectMethodMismatchError` |
| Server | `ResponseMeta{Status: "303", ...}` on a redirect-capable route renders correctly in the generated OpenAPI spec alongside the primary response |
| Client | `Client.Call` auto-follows a recognized redirect (registered via a prior `Call`) and decodes the TARGET route's `Resp` |
| Client | `Client.Call` auto-follows a recognized redirect registered ONLY via `Client.RegisterRoute` (never called directly) |
| Client | Two routes with overlapping templates matching the same Location+Method — first-registered-wins |
| Client | `Client.Call` returns `UnrecognizedRedirectError{Location, Status}` for a `Location` not matching any registered route |
| Client | `http.Client.CheckRedirect` is confirmed disabled by default on the shallow-copied client (no silent double-follow), and the CALLER's own `*http.Client` is confirmed unmutated |
| Client | `CallWithTransport` returns a typed `RedirectError` (no auto-follow) on receiving a redirect |
| Client | Registering (or auto-populating) the SAME routeKey twice via two DIFFERENT credential-bound `ClientHandle` variants of the identical route is a safe no-op, not an error |
| chi/nethttp parity | Server-side behavior (RedirectError recognition, Location + status, no body) is identical in both adapters |

## Files to create (once approved for implementation)

| File | Purpose |
|---|---|
| `api/rest/redirect.go` + `redirect_test.go` | `RedirectError`, `RedirectTargetVarError`, `RedirectMethodMismatchError`, `Redirect`, `RedirectToSSE` |
| `api/rest/client_registry.go` (or inline in `builder.go`) + test | `Client`'s Method+PathTemplate registry, `RegisterRoute`, auto-populate-on-Call, `UnrecognizedRedirectError`, same-routeKey-reregistration idempotency |
| `adapters/nethttp/redirect.go` (or inline in `adapter.go`/`clienttransport.go`) + test | Server dispatch recognition + `Location` rendering; `CheckRedirect` shallow-copy override; registry-driven auto-follow/decode in `clientTransport.Call` |
| `adapters/chi/redirect.go` (or inline) + test | Server-side behavior only (no client transport exists in chi) |
| `examples/rest-redirect/main.go` | Minimal, standalone, self-contained worked example — demonstrates the auto-populate-via-`Call` registry path (see "Client-side route registry" above) |
| `examples/rest-api/demo_redirect.go` + rework of `examples/rest-api/client`'s `Build()` | Richer demo in the existing dual-adapter (chi/nethttp) flagship example — `Build()` reworked to explicitly `Client.RegisterRoute` every distinct route at construction time; the demo itself shows primary (auto-follow via the construction-time-populated registry) + secondary (`CallWithTransport`, manual follow-up) in one file, mirroring `examples/adapters-nethttp-client`'s Section-0-then-escape-hatch style (see "Client-side route registry" above) |
| `docs/features/rest-redirects.md`, `docs/guides/rest-redirects.md` | User-facing docs once shipped |
| `docs/roadmap/index.md` row, `zensical.toml` nav entry | Already added by this doc's own creation |

## Out of scope (for now)

- Cross-`Client`/cross-`Server` redirect targets (redirecting to a route
  registered on a DIFFERENT `Client`/`Server` instance) — confirmed
  structurally out of scope, see "Resolved design decisions" #4.
- Redirecting to a bare, unregistered URL (e.g. an external site) — the
  user's own design explicitly wants a TYPED target route, not a string;
  an escape hatch for external URLs is not precluded by this design but
  is not scoped here either.
- `docs/roadmap/idea-codec-defined-hateoas.md`'s in-body typed links —
  related, intentionally not merged (see "Why this exists" above).
