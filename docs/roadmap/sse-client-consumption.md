# SSE Client Consumption — `adapters/nethttp`

> **Status:** Design complete — not yet implemented.
> [← Back to Roadmap](index.md)
>
> Spun out from [Middleware Workflow Simplification](../design/middleware-workflow-simplification.md)'s
> final critical review pass (item 6: `SSERoute`'s full chainable method
> set) while confirming what a client-side SSE story would need, then
> deliberately deferred until that doc's implementation shipped. This
> revision (below "Confirmed current state") replaces the earlier
> "findings only" checklist with resolved decisions, now that the
> prerequisite work has shipped and could be investigated directly
> against real code.

---

## Why this exists

While closing a gap in `middleware-workflow-simplification.md` (spelling
out `SSERoute`'s full new chainable method set — `WithHandler`/
`HandleMW`/`WithOptions`/`Register`/`RegisterHandle`), it became clear that
doc could only resolve the SERVER side of SSE (serving events to
connected clients) — because **no first-class, declarative SSE
CLIENT-consumption mechanism exists anywhere in go-codex today.** Unlike
security/client-credentials (which HAD an old, pre-`middleware`-package
design to redesign), this is a GENUINELY NEW capability with no existing
shape to mirror on the client side — though, as this revision found, the
*server* side isn't as fully symmetric as first assumed either (see
"Prerequisite" below).

## Confirmed current state (via code inspection, not assumption)

- **`examples/adapters-sse/main.go` reads its own SSE responses via a
  hand-rolled helper**, not a reusable mechanism:
  ```go
  // ── Helper: read all SSE data lines from a response ──────────────
  func readSSELines(resp *http.Response) []string
  ```
  Raw line-by-line parsing of the HTTP response body — no codec
  validation, no typed `Event` decoding, no reconnect/retry handling.
- **`adapters/nethttp/stream_errors.go` still carries two SSE-client-
  shaped error types whose doc comments reference a function that
  doesn't exist:**
  ```go
  // SSEConnectError is sent to [Stream.Errors] by [SSEClientStream] when an HTTP
  // connection attempt to the SSE endpoint fails. The stream retries after backoff;
  // this error is informational per reconnect attempt.
  type SSEConnectError struct { URL string; Attempt int; Err error }

  // SSEParseError is sent to [Stream.Errors] by [SSEClientStream] when an SSE
  // data line cannot be decoded using the provided format — malformed JSON, failed
  // codec validation, or other decode failure.
  type SSEParseError struct { /* ... */ }
  ```
  `SSEClientStream` — confirmed via repo-wide grep — does NOT exist
  anywhere in the current codebase. This is an apparent leftover from an
  earlier, since-removed stream-bridge helper (this repo has a
  documented history of removing old stream-bridge helpers in favor of
  port adapters) that was never given a declarative replacement. Both
  types are REUSED as-is by this design (see "Structured errors" below) —
  only their stale doc-link needs fixing, from `[SSEClientStream]` to
  `[CallSSEAdapter]`.
- **The server side (`adapters/nethttp.SSEAdapter`, a `ports.SinkAdapter`)
  is unaffected and unrelated** — it SERVES events out over SSE to
  connected clients; it has nothing to do with CONSUMING a remote SSE
  endpoint as a client, which is the gap this doc is about.

## Prerequisite (Phase 0): `SSERouteHandle` has zero `Req`-side merge-field support today, on EITHER side

The earlier version of this doc assumed the server side of `SSERoute` was
already fully symmetric (true for the `.Use`/`.HandleMW`/`.WithHandler`/
`.Register` CHAIN — see middleware-workflow-simplification's "Decision:
`SSERoute`'s full chainable method set"), and framed the remaining gap as
purely client-side. Investigating the actual merge-field machinery for
this doc found a DEEPER, currently-silent gap:

- **`SSERouteHandle` has exactly ONE `MergeFields()` method, and it
  operates on `Event`** (server writing request-derived vars INTO an
  outgoing event via `MergeEvent`) — there is NO `PathMergeFields()`/
  `QueryMergeFields()`/`HeaderMergeFields()`/`CookieMergeFields()` for
  `Req` at all. Contrast `RouteHandle`, which has all four.
- **Confirmed in `SSERoute.registerHandle`** (`api/rest/builder.go`): the
  shared `routeBuilder` accumulates Req-side merge fields into FOUR
  SEPARATE, role-scoped fields — `pathMergeFields`, `queryMergeFields`,
  `headerMergeFields`, `cookieMergeFields` (registered via
  `NewRequiredPathParam[T]`/`NewRequiredQueryParam[T]`/etc.) — the SAME
  four fields `Route.registerHandle` reads to populate `RouteHandle`'s
  matching accessors. `SSERoute.registerHandle`'s struct literal reads
  NONE of these four; only `rb.sseEventMergeFields` (a fifth, DIFFERENT
  field — Event-side, via `NewRequiredSSEEventParam`/
  `NewOptionalSSEEventParam`) is used. **This is silent**: no error, no
  warning — a user declaring a merge-capable path param on an SSE route
  today gets validate-only behavior, identical to a plain `PathParam`,
  with no indication their merge-capable constructor's extra capability
  was dropped.
- **Confirmed in `adapters/nethttp/serve_sse.go`'s `buildSSERouteHandler`**:
  the handler always builds `reqPtr := reflect.New(reqType)` — a
  ZERO-VALUE `Req` — and nothing merges path/query/header/cookie values
  into it before calling the handler. `SSERouteHandle.Decode`'s own doc
  comment already documents this as DELIBERATE ("read path and query
  parameter values from your HTTP framework's request context instead"),
  so the SERVER side's lack of Req-merge is an intentional, accepted
  design (SSE handlers read params from context manually) — **not a bug
  in itself.**
- **But the CLIENT side genuinely needs the ENCODE direction** (`Req` →
  path/query/header/cookie, to build the connection URL) regardless of
  what the server does with decoding — this is the SAME direction
  `CallWithHandle` already performs for regular routes via
  `codex.EncodeVars(req, handle.PathMergeFields()...)` (and the query/
  header/cookie equivalents). Without these accessors on `SSERouteHandle`,
  a `Consume`/`CallSSEAdapter`-style function has no declarative way to turn a `Req`
  struct into a connection URL — defeating "one struct in, one call" for
  the SSE boundary specifically.

**Decision: add the four `Req`-side merge-field accessors to
`SSERouteHandle` as Phase 0 of this roadmap**, wiring `rb.pathMergeFields`/
`rb.queryMergeFields`/`rb.headerMergeFields`/`rb.cookieMergeFields` through
in `SSERoute.registerHandle` exactly the way `Route.registerHandle` already
does for `RouteHandle`. This closes a genuine, currently-silent
gap independently of the client-consumption feature (a user COULD already
be relying on merge-capable param constructors on an SSE route today,
silently getting validate-only behavior) — and it is also the necessary
prerequisite for `CallSSEAdapter`'s "one struct in" promise. The SERVER side's
existing manual-read-from-context behavior for SSE handlers is UNCHANGED
by this — Phase 0 only adds the missing ENCODE-direction accessors to the
handle; it does not change how `ServeSSE` builds `Req` for the handler.

## Scope decisions (what's in Phase 1, what's deferred)

> **Design correction (post-review):** an earlier revision of this doc
> proposed `nethttp.CallSSE(...) stream.Stream[Event]` — a bare function
> returning a stream directly, paired with `*nethttp.Caller` reuse framed
> as a consistency win. Review against the actual codebase found this was
> BACKWARDS: every streaming transport in go-codex (`mqtt`, `mqtt5`,
> `zeromq`, `nethttp`'s own `IngestAdapter`/`SSEAdapter`/`CallAdapter`/
> `DrainCallAdapter`, `adapters/websocket`'s `DialSourceAdapter`/
> `DialSinkAdapter`, `file`, `sql`, `redis`) exposes streaming EXCLUSIVELY
> as a `ports.SourceAdapter[T]`/`SinkAdapter[T]`/`IOAdapter[Req,Resp]`
> implementation — zero exceptions found. `dialLoop` (the reconnect-backoff
> idiom this doc's design already borrowed) lives INSIDE
> `websocket.DialSourceAdapter`, which itself returns `ports.SourceAdapter[T]`
> — the earlier revision copied the backoff BEHAVIOR but not the
> Adapter-returning SHAPE, an inconsistency with go-codex's own established
> pattern (and the exact bare-stream-function shape this codebase
> deliberately removed in an earlier round — see
> `docs/design/middleware-workflow-simplification.md`'s reference to
> removed `SubscribeStream`/`DrainPublish`/`CallStream`/`HandlerIngest`
> helpers). This revision replaces `CallSSE` with a PAIR —
> `nethttp.Consume` (direct, blocking, callback-based, takes a `*Consumer`)
> and `nethttp.CallSSEAdapter` (port-adapter, delegates to the same
> internal loop, plain args) — matching `CallAdapter`/`DrainCallAdapter`'s
> exact parameter shape (`client *http.Client, baseURL string`, no
> session object) for the adapter, and `Call`/`Caller`'s exact shape
> (a session object passed to the call, holding client+baseURL) for the
> direct function — see "`Consumer`: the strict SSE equivalent of
> `Caller`" below. See also "Why a callback-parameter function AND a port
> adapter, not a chained `.WithClientHandler`?" for the fn-as-parameter
> rationale — both were re-derived from EXISTING codebase precedent, not
> invented fresh.

| In scope | Out of scope |
|---|---|
| Phase 0: `SSERouteHandle.PathMergeFields()`/`QueryMergeFields()`/`HeaderMergeFields()`/`CookieMergeFields()` for `Req` (wiring `rb.pathMergeFields`/`rb.queryMergeFields`/`rb.headerMergeFields`/`rb.cookieMergeFields` through, mirroring `RouteHandle`) | Changing `ServeSSE`'s server-side manual-read-from-context behavior — that stays exactly as-is, deliberately |
| `SSERoute.ClientMW(mw, fn)` + `SSERoute.ClientHandle()` — client-side credential fulfillment, mirroring `Route.ClientMW`/`Route.ClientHandle()` exactly | A separate SSE-specific security scheme type — reuses the SAME `route.SecurityScheme`/`middleware.SecurityScheme` vocabulary as regular routes |
| `SSERouteHandle.DecodeEvent(data []byte) (Event, error)` — complement of the existing `EncodeEvent` | Per-provider/per-format decode customization beyond `SSERouteHandle.Formats` (already exists, reused as-is) |
| `nethttp.Consumer` (own file, `adapters/nethttp/consumer.go`) + `NewConsumer(client, baseURL) *Consumer` + `Consumer.WithBaseURL(baseURL) *Consumer` — the STRICT, field-for-field, method-for-method equivalent of `Caller`/`NewCaller`/`Caller.WithBaseURL`, for SSE consumption | Any Consumer-level default credential/middleware slot — mirrors `Caller`'s existing "no defaultMws" design exactly; credentials stay per-route via `SSERoute.ClientMW` |
| `nethttp.Consume[Req, Event](ctx, consumer *Consumer, sseRoute, req, fn, opts) error` — direct, blocking, callback-based entry point taking a `*Consumer` (mirrors `Call(ctx, caller, route, req, opts)`'s parameter order exactly), with `fn func(ctx, Event) error` inserted where `Call` has nothing extra (mirrors `mqtt5.Subscribe`/`zeromq.Subscribe`'s existing callback-PARAMETER shape, not a chained handler-attachment method) | `Last-Event-ID` resume support (stateless full-reconnect-from-scratch only in Phase 1 — see "Out of scope" below) |
| `nethttp.CallSSEAdapter[Req, Event](client, baseURL, handle, req, opts) ports.SourceAdapter[Event]` — port-adapter entry point, DELEGATES to the same internal loop `Consume` uses (mirrors `zeromq.SubscribeAdapter`'s documented "Activate delegates to Subscribe" relationship exactly). Deliberately stays on PLAIN `client, baseURL` params, NOT `*Consumer` — matches `CallAdapter`/`DrainCallAdapter`'s existing precedent (`Call` takes `*Caller`; `CallAdapter` does not) — bound via `ports.SourcePort.Bind` + `.Stream(ctx)`, the SAME idiom every other transport adapter already uses | A pluggable/caller-configurable retry policy (fixed backoff shape only in Phase 1) |
| Reconnect-with-backoff, mirroring `adapters/websocket`'s `dialLoop` idiom exactly (lives inside the SHARED unexported loop both entry points call) | Changes to `adapters/chi` — chi has no client-side story at all (confirmed: no `caller.go`/client file exists there), so this is `adapters/nethttp`-only, same scoping as `Call`/`Caller` |
| `CallSSEAdapter` stays on plain `client *http.Client, baseURL string` parameters — matching `CallAdapter`/`DrainCallAdapter`/`IngestAdapter` exactly; `*Consumer` is reserved for the direct `Consume` call only, exactly mirroring `*Caller`'s scoping to `Call` only | A new `SSERoute.WithClientHandler` chained method (considered, then rejected — see "Why a callback-parameter function..." below) |
| `fn`'s returned error is NON-FATAL — reported via `opts.OnError` as a new `SSEHandlerError`, consumption continues with the next event (matches `mqtt5.Subscribe`/`zeromq.Subscribe`'s existing `SubscribeError{Kind: KindHandler}`-via-`OnError` convention exactly, confirmed via code) | A caller-configurable fatal-vs-non-fatal policy (Phase 1 ships non-fatal only, matching the existing pub/sub precedent) |
| Merge-field vars AND the `ClientMW` credential are BOTH re-derived on EVERY reconnect attempt, never cached across attempts — the correct analogue of `Call`'s "derive fresh per invocation," since one `Consume` call spans many reconnects | A Consume-level credential CACHE of its own — re-derivation is the caller's `ClientMW` Fn's own concern (e.g. via `NewCachingCredentialFunc`), same as `Call` |
| `ConsumeOptions.OnCredentialRejected func()` — fires on a 401 connect response with an engaged `ClientMW` credential, mirroring `CallOptions.OnCredentialRejected` exactly (same trigger condition) | Automatic credential refresh/retry — `OnCredentialRejected` is a notification hook only, same as `CallOptions`'; the NEXT reconnect attempt re-deriving fresh (per the row above) is what actually recovers |
| Client-side format resolution: `Consume`/`CallSSEAdapter`'s internal loop reads the response's `Content-Type` ONCE per connection and matches it against `handle.Formats`, falling back to `DecodeEvent`'s JSON default — symmetric with the server's connect-once `Accept`-negotiation (confirmed via code, same `chosen`-captured-once pattern) | A caller-facing `DecodeEvent(data, contentType)` overload — format resolution stays fully internal to the shared loop, never a second public decode method |

## API surface

```go
// ── SSERouteHandle additions (api/rest/builder.go) ──────────────────────
package rest // api/rest

// PathMergeFields/QueryMergeFields/HeaderMergeFields/CookieMergeFields
// return the Req-side merge-capable fields registered via
// NewRequiredPathParam[T]/NewRequiredQueryParam[T]/etc. — mirrors
// RouteHandle's four identically-named accessors exactly. Wired from
// the SAME rb.pathMergeFields/rb.queryMergeFields/rb.headerMergeFields/
// rb.cookieMergeFields fields SSERoute.registerHandle already accumulates
// but previously discarded.
func (h *SSERouteHandle[Req, Event]) PathMergeFields() []codex.FieldCodec[Req]
func (h *SSERouteHandle[Req, Event]) QueryMergeFields() []codex.FieldCodec[Req]
func (h *SSERouteHandle[Req, Event]) HeaderMergeFields() []codex.FieldCodec[Req]
func (h *SSERouteHandle[Req, Event]) CookieMergeFields() []codex.FieldCodec[Req]

// DecodeEvent deserialises and validates one SSE "data:" line's raw
// bytes into an Event — the complement of EncodeEvent, using the SAME
// eventCodec the route was declared with (or the negotiated Formats
// entry, when set).
func (h *SSERouteHandle[Req, Event]) DecodeEvent(data []byte) (Event, error)

// ClientImplementations holds every [middleware.ClientImplementation]
// attached via [SSERoute.ClientMW], in attachment order — mirrors
// [RouteHandle.ClientImplementations] exactly; consumed by
// [nethttp.CallSSEAdapter] the same way nethttp.Call consumes
// RouteHandle's field. (New field on SSERouteHandle.)

// ── SSERoute additions (api/rest/middleware.go — ClientMW; api/rest/builder.go — ClientHandle) ──

// ClientMW attaches a client-side credential-providing implementation,
// identical Satisfies-gating mechanics to [Route.ClientMW]: mw non-nil
// with Security set gates fn to run only when the route's declared
// security requirements include that scheme; mw nil (or Security nil)
// marks fn general-purpose (always runs). Lives in api/rest/middleware.go,
// the SAME file as Route.ClientMW.
func (s SSERoute[Req, Event]) ClientMW(mw *middleware.Middleware, fn any) SSERoute[Req, Event]

// ClientHandle returns a handle without registering with a Builder —
// mirrors [Route.ClientHandle] exactly (no spec, no path codec
// validation): builds a scratch routeBuilder, calls
// applyMiddlewareSecurityForClient(&rb) — the SAME infallible,
// conflict-detection-free merge function Route.ClientHandle uses (NOT
// applyMiddlewareDeclarations, which is the fallible Register-only path)
// — for client-only scenarios where no OpenAPI spec is needed. Builds its
// OWN *SSERouteHandle struct literal directly, the SAME SEPARATE
// construction path Route.ClientHandle already uses (confirmed via code:
// Route.ClientHandle does NOT call Route.registerHandle) — it must NOT
// delegate to SSERoute.registerHandle/Register, which requires a real
// *Builder, runs the fallible checkImplementationsDeclared/coverage
// checks, and appends to b.entries — none of which ClientHandle wants.
// Lives in api/rest/builder.go, the SAME file as Route.ClientHandle.
func (s SSERoute[Req, Event]) ClientHandle() *SSERouteHandle[Req, Event]

// ── Consumer (adapters/nethttp/consumer.go — NEW file, mirrors
// caller.go's file-per-concept convention) ───────────────────────────────
package nethttp // adapters/nethttp

// Consumer is a client-side convenience holder for SSE consumption,
// removing repeated client/baseURL boilerplate across many [Consume]
// calls to the same API — the STRICT SSE-consumption EQUIVALENT of
// [Caller] ([Caller] is for one-shot request/response via [Call];
// Consumer is for long-lived event streams via [Consume]). Field-for-
// field, method-for-method identical shape to Caller: no defaultMws, no
// per-Consumer credential slot — client-side credential fulfillment is
// declared PER-ROUTE via [rest.SSERoute.ClientMW], mirroring how Caller
// defers entirely to [rest.Route.ClientMW].
type Consumer struct {
    client  *http.Client
    baseURL string
}

// NewConsumer builds a [Consumer] bound to client and baseURL. Mirrors
// [NewCaller] exactly.
func NewConsumer(client *http.Client, baseURL string) *Consumer {
    return &Consumer{client: client, baseURL: baseURL}
}

// WithBaseURL returns a NEW [Consumer] sharing c's *http.Client but bound
// to a different baseURL. Mirrors [Caller.WithBaseURL] exactly — same
// non-mutating-copy semantics, same "ergonomic sugar, not a structural
// requirement" rationale (a fresh Consumer is always cheap to construct
// directly via [NewConsumer]).
func (c *Consumer) WithBaseURL(baseURL string) *Consumer {
    return &Consumer{client: c.client, baseURL: baseURL}
}

// ── Consume + CallSSEAdapter (adapters/nethttp/binding.go — EDIT,
// same file as CallAdapter/DrainCallAdapter/IngestAdapter/SSEAdapter) ────

// ConsumeOptions configures [Consume]/[CallSSEAdapter].
type ConsumeOptions struct {
    // QueryParams/CookieParams/HeaderParams/ExtraHeaders — same shape and
    // precedence as [CallOptions] (explicit wins over Req-derived).
    QueryParams  map[string]string
    CookieParams map[string]string
    HeaderParams map[string]string
    ExtraHeaders http.Header

    // MaxBackoff caps the exponential reconnect backoff. Default 30s
    // (initial step 250ms, doubling per consecutive failure, reset after
    // a connection that delivered at least one successfully-decoded
    // event) — identical shape to [adapters/websocket.DialAdapterOptions].
    MaxBackoff time.Duration

    // OnError is called for EVERY non-fatal failure: a failed connection
    // attempt ([SSEConnectError]), a malformed event ([SSEParseError]),
    // or fn returning a non-nil error for one event ([SSEHandlerError]).
    // Consumption always continues after each — mirrors
    // [mqtt5.Subscribe]/[zeromq.Subscribe]'s existing OnError convention
    // exactly. nil is a valid no-op default.
    OnError func(error)

    // OnCredentialRejected, when non-nil, is called when a reconnect
    // attempt gets HTTP 401 AND a [rest.SSERoute.ClientMW]-declared
    // credential was attached to that attempt — mirrors
    // [CallOptions.OnCredentialRejected] exactly, same trigger condition
    // (401 + engaged credential fn). MORE important here than for Call:
    // since Consume retries forever, a caching credential wrapper with no
    // way to invalidate would otherwise resend the SAME rejected
    // credential on every subsequent reconnect attempt, forever.
    // [NewCachingCredentialFunc]'s invalidate function is designed to be
    // wired here, identically to CallOptions' own field.
    OnCredentialRejected func()

    // Observer receives RecordRequest per connect attempt. Resolved from
    // ctx when nil.
    Observer stats.Observer
}

// Consume opens a long-lived SSE connection to sseRoute against
// consumer's baseURL — the STRICT equivalent of [Call] for a stream of
// many events instead of one Resp. Signature mirrors Call's parameter
// ORDER exactly (ctx, consumer, route, req, ..., opts), with fn inserted
// where Call has nothing extra (Call returns Resp directly; a stream
// produces MANY values over time, so fn receives each one as it
// arrives). Derives path/query/header/cookie values from req via
// sseRoute's declared merge fields (same "one struct in" mechanics as
// [Call]) and fulfills any [rest.SSERoute.ClientMW]-declared credential
// exactly like [Call] does. BLOCKS until ctx is cancelled or a fatal
// connection-setup error occurs (e.g. sseRoute's merge fields fail to
// encode req) — mirrors [zeromq.Subscribe]'s blocking shape exactly: HTTP
// has no external background-dispatch thread the way an MQTT client
// does, so the CALLER'S OWN goroutine must run the loop (typically via
// `go nethttp.Consume(...)`).
//
// The merge-field vars AND the ClientMW credential are BOTH RE-DERIVED
// on EVERY reconnect attempt, not just the first — this is the correct
// analogue of Call's "derive fresh on every invocation" behavior, since
// one Consume call IS the whole multi-reconnect session (unlike Call's
// single request). A short-lived Bearer token (the common case) MUST be
// re-fetched on each attempt, or every reconnect after the token expires
// would fail forever with the exact same stale credential.
//
// fn is called once per decoded Event. fn's returned error is NON-FATAL:
// wrapped in [SSEHandlerError] and reported via opts.OnError, then
// consumption continues with the next event — matches
// [mqtt5.Subscribe]/[zeromq.Subscribe]'s existing convention exactly (an
// [SubscribeError]-shaped wrap via OnError, loop continues).
//
// Connection drops trigger automatic reconnect with exponential backoff
// internally (mirrors [adapters/websocket]'s dialLoop) — a dropped/
// reconnected stream is NOT resumed from the last event (see "Out of
// scope": Last-Event-ID resume is a Phase 2 candidate); every reconnect
// starts the SAME request from scratch (including a fresh credential
// derivation, per above). [SSEConnectError] is reported via opts.OnError
// per failed attempt (informational, keeps retrying); [SSEParseError] is
// reported the same way per malformed event (that one event is dropped,
// the stream continues). A 401 response with an engaged ClientMW
// credential additionally fires opts.OnCredentialRejected (see
// ConsumeOptions below) — same trigger condition as
// [CallOptions.OnCredentialRejected], letting a caching credential
// wrapper invalidate its cache before the NEXT reconnect attempt (without
// this, a cached bad credential would be resent forever).
//
// Usage:
//
//	consumer := nethttp.NewConsumer(httpClient, baseURL)
//	go func() {
//	    err := nethttp.Consume(ctx, consumer, notifRoute, req,
//	        func(ctx context.Context, event Notification) error {
//	            return handleNotification(event)
//	        }, nethttp.ConsumeOptions{})
//	    if err != nil { /* fatal setup error, e.g. bad merge-field encoding */ }
//	}()
func Consume[Req, Event any](
    ctx context.Context,
    consumer *Consumer,
    sseRoute rest.SSERoute[Req, Event],
    req Req,
    fn func(ctx context.Context, event Event) error,
    opts ConsumeOptions,
) error

// CallSSEAdapter returns a [ports.SourceAdapter] that DELEGATES to the
// SAME underlying connect+decode+reconnect loop [Consume] uses — mirrors
// [zeromq.SubscribeAdapter]'s documented "Activate delegates to
// Subscribe" relationship exactly (confirmed via code: zeromq's Activate
// wraps a dst-channel-push closure as its fn and calls Subscribe
// internally; CallSSEAdapter does the identical thing for Consume). Takes
// a pre-built *rest.SSERouteHandle (not a bare [rest.SSERoute]) —
// matching [CallAdapter]/[DrainCallAdapter]'s existing handle-based
// convention for port-adapter constructors. Deliberately stays on PLAIN
// client/baseURL parameters, NOT a [Consumer] — matching
// [CallAdapter]/[DrainCallAdapter]'s existing precedent exactly ([Call]
// takes a [Caller]; [CallAdapter] does not — [Consumer] is likewise
// scoped to [Consume] only). Not to be confused with [SSEAdapter], the
// SERVER-side counterpart that SERVES events out — CallSSEAdapter
// CONSUMES a remote SSE endpoint as a client. Use with
// [ports.SourcePort.Bind], the SAME idiom every other transport adapter
// in this codebase already uses:
//
//	port, _ := ports.NewSourcePort[Event]("sseEvents", eventCodec, ports.PortOptions{})
//	port.Bind(ctx, nethttp.CallSSEAdapter(httpClient, baseURL, sseHandle, req, opts))
//	events := port.Stream(ctx)
//
// [SSEHandlerError] cannot occur here — there is no caller-supplied fn to
// fail; the internal dst-push closure never returns an error.
func CallSSEAdapter[Req, Event any](
    client *http.Client,
    baseURL string,
    handle *rest.SSERouteHandle[Req, Event],
    req Req,
    opts ConsumeOptions,
) ports.SourceAdapter[Event]
```

### `Consumer`: the strict SSE equivalent of `Caller`

`Consumer` is deliberately a DISTINCT type from `Caller`, not a reuse —
even though both are structurally `{client *http.Client, baseURL string}`
today. Two independent, session-scoped concepts justify keeping them
separate: (1) `Consumer` is the natural home for SSE-specific amortized
defaults that a one-shot `Call` never needs (e.g. a future shared
`MaxBackoff` default across many `Consume` calls against the same API —
not added in Phase 1, since `ConsumeOptions.MaxBackoff` already covers the
per-call case, but the TYPE boundary is drawn now so such a default has an
obvious home later without a breaking change); (2) `Caller`/`Consumer`
name the SPECIFIC role each session object plays (a "caller" makes
request/response calls; a "consumer" consumes an event stream) — this
mirors `Route`/`SSERoute` themselves already being distinct types for the
same reason (a route responds once; an SSE route streams), rather than
one type serving two conceptually different roles.

`CallSSEAdapter`, the port-adapter shape, does NOT take a `*Consumer` —
this exactly replicates `CallAdapter`/`DrainCallAdapter`'s existing
precedent, where `*Caller` is scoped ONLY to the direct `Call` function,
never to a port-adapter constructor (which instead takes plain
`client, baseURL` args and a pre-built handle). Extending `*Consumer` to
`CallSSEAdapter` would have been a NEW asymmetry versus REST's own
established pattern, not a consistency win.

### Why a callback-parameter function AND a port adapter, not a chained `.WithClientHandler`?

An earlier draft of this design considered a NEW chained method,
`SSERoute.WithClientHandler(fn) SSERoute[Req, Event]`, mirroring
`.WithHandler`'s server-side naming. Re-examining `Call`'s actual
signature — `Call(ctx, caller, route, req, opts)`, where `route` and `req`
are BOTH passed as call PARAMETERS, with no chained handler-attachment
step anywhere on the client side — showed this would have been LESS
consistent with REST's real shape, not more: `Route`'s client side never
needed a chained handler because `Call` returns the single `Resp` value
directly; the "attach a handler ahead of time" concept only exists
SERVER-side, where a request arrives at an unpredictable time and needs a
pre-registered function waiting for it.

For a STREAM of many events over one long-lived connection, a single
return value doesn't work — SOME mechanism for "what happens per event"
is unavoidable. The MORE consistent choice, re-derived directly from
`mqtt5.Subscribe`/`zeromq.Subscribe` (which face the EXACT same "many
values over time" shape and already ship in this codebase), is to pass
`fn` as a plain PARAMETER to the consuming call — not to invent a new
chained builder method. This also directly answers why
`SSERouteHandle.DecodeEvent` exists at all: it is internal decode
machinery, called by `Consume`/`CallSSEAdapter`'s shared loop, NEVER
touched by whoever implements `fn` — exactly mirroring how `EncodeEvent`
is already internal machinery `ServeSSE` uses, invisible to whoever
implements the server's `send func(Event) error` callback. `fn` receives
an already-decoded `Event`; there is nothing left for a handler
implementor to decode.

### Multi-format decode resolution: symmetric with the server's connect-once negotiation

Confirmed via code (`adapters/nethttp/serve_sse.go`'s
`buildSSERouteHandler`): the SERVER resolves which `handle.Formats` entry
to encode with ONCE per connection, via `Accept`-header negotiation at
connect time (`negotiateFormatReflect(formatsVal, accept)`) — the chosen
format is captured in a closure and reused for EVERY event on that
connection; there is no per-event renegotiation (SSE has no per-line
Content-Type — the whole stream is one `text/event-stream` response with
ONE overall body `Content-Type`, if `handle.Formats` are declared at all).

The CLIENT side needs the symmetric counterpart, entirely INTERNAL to the
shared `consumeSSE` loop (no new public API): on connecting, read the
response's `Content-Type` header ONCE, match it against
`handle.Formats` (mirroring the server's negotiation, in reverse — the
CLIENT is choosing which decoder matches what the server actually sent,
not asking the server to choose), and use that ONE resolved format's
`Unmarshal` for every event on the connection. When `handle.Formats` is
empty, or no entry matches the response's `Content-Type`, fall back to
`DecodeEvent` (the plain JSON default) — mirroring `handle.Decode`'s
existing role as the request-body default when no `Formats` are declared.
`SSERouteHandle.DecodeEvent(data []byte) (Event, error)` itself stays
EXACTLY as declared above — a single, format-agnostic default path; the
per-connection format RESOLUTION step lives in `consumeSSE`'s unexported
internals, never exposed as a `DecodeEvent` parameter or a second public
decode method. This mirrors the split already established server-side:
`SSERouteHandle.EncodeEvent` is likewise the plain default, while the
ACTUAL per-connection format choice happens inside `ServeSSE`'s
unexported handler-building logic, never on `EncodeEvent` itself.

An `Accept` header is NOT sent by `Consume`/`CallSSEAdapter` by default —
the server's existing negotiation logic already falls back to its first
declared format (or `EncodeEvent`'s JSON default) when `Accept` is absent
or doesn't match (confirmed via code:
`if strings.Contains(accept, "text/event-stream") { chosen =
formatsVal.Index(0) }`), so an SSE consumer with no format preference
gets a sane default without needing to construct an `Accept` header at
all. A caller needing a SPECIFIC non-default format sets it via
`ConsumeOptions.ExtraHeaders["Accept"]` — the existing escape hatch, no
new field needed.

## Structured errors (all implement `slog.LogValuer`)

`SSEConnectError`/`SSEParseError` already exist in
`adapters/nethttp/stream_errors.go` with correct shape, `Unwrap()`, and
`LogValue()` — only their stale doc comments need fixing. ONE genuinely
new type is needed: `SSEHandlerError`, wrapping `fn`'s non-fatal error per
event (mirrors `mqtt5.SubscribeError{Kind: KindHandler}`'s existing
convention, adapted to SSE's simpler single-error-kind shape — SSE has no
decode-vs-security-vs-handler `Kind` enum the way mqtt5 does, since
`DecodeEvent`/`ClientMW` failures already have their OWN distinct types
`SSEParseError`/`SecurityCredentialError`).

```go
// SSEConnectError is sent to opts.OnError by [Consume]/[CallSSEAdapter]
// when an HTTP connection attempt to the SSE endpoint fails. Consumption
// retries after backoff; this error is informational per reconnect attempt.
type SSEConnectError struct { URL string; Attempt int; Err error }

// SSEParseError is sent to opts.OnError by [Consume]/[CallSSEAdapter]
// when an SSE data line cannot be decoded using the route's event codec —
// malformed JSON, failed codec validation, or other decode failure.
// Consumption continues; only the one failing event is dropped.
type SSEParseError struct { URL string; Line string; Err error }

// SSEHandlerError is sent to opts.OnError by [Consume] when fn returns
// a non-nil error for one decoded event — mirrors
// [mqtt5.SubscribeError]/[zeromq.SubscribeError]'s existing
// handler-error-is-non-fatal convention exactly. Consumption continues
// with the next event. Never occurs for [CallSSEAdapter] — its internal
// fn (a channel push) never returns an error.
type SSEHandlerError struct {
    URL string
    Err error
}

func (e SSEHandlerError) Error() string {
    return fmt.Sprintf("http sse handler %s: %v", e.URL, e.Err)
}
func (e SSEHandlerError) Unwrap() error { return e.Err }
func (e SSEHandlerError) LogValue() slog.Value {
    return slog.GroupValue(
        slog.String("url", e.URL),
        slog.Any("err", e.Err),
    )
}
```

(`SSEConnectError`/`SSEParseError` already had `Unwrap()`/`LogValue()`
correctly implemented before this doc — reused verbatim; only their
`[SSEClientStream]` doc-link references change to
`[Consume]`/`[CallSSEAdapter]`, and their "sent to `[Stream.Errors]`"
wording changes to "sent to `opts.OnError`" — matching `ConsumeOptions`'s
callback shape, not `stream.Stream`'s field names.)

## Observer integration

Reuses `stats.Observer` — no new extension:

- `stats.Observer.RecordRequest(http.MethodGet, path, statusCode,
  duration)` fires once per connection attempt (status 0 on a pre-flight
  vars-derivation/security-credential failure — same status-0 convention
  `Call` already uses; status matching the actual HTTP response status on
  a real attempt, mirroring `adapters/websocket.dialLoop`'s
  `RecordRequest` call per dial).
- `stats.SecurityObserver.RecordSecurityRejection` — type-asserted, fires
  if a `ClientMW`-attached credential fails format validation against the
  declared scheme's Codec before the connection is attempted (same
  pre-flight check `Call` already performs).
- Nil observer → `stats.ObserverFromContext(ctx)` (has a direct `ctx`
  parameter, so the standard nil-guard applies, not the closure-resolved
  pattern).
- `opts.OnError` is a SEPARATE, orthogonal mechanism from Observer (same
  relationship `mqtt5.SubscribeOptions.OnError`/`stats.Observer` already
  have) — Observer is for METRICS (aggregate counts/durations), OnError is
  for PER-EVENT ERROR HANDLING (the caller deciding what to do with one
  failure). Both fire independently on the same failure.

## Unit test plan

| ID | Test | Verifies |
|---|---|---|
| N1 | `NewConsumer` + `Consumer.WithBaseURL` | returns a distinct `*Consumer`, does not mutate the original — mirrors `TestCaller_WithBaseURL_ReturnsNewInstance`/`TestCaller_WithBaseURL_DoesNotMutateOriginal` exactly |
| M1 | `SSERouteHandle.PathMergeFields`/`QueryMergeFields`/`HeaderMergeFields`/`CookieMergeFields` | non-nil, correct fields, wired from `rb.pathMergeFields`/`rb.queryMergeFields`/`rb.headerMergeFields`/`rb.cookieMergeFields` respectively |
| M2 | SSE route declared with a merge-capable path param, registered via `Register` | `RouteHandle`-equivalent merge fields present on the resulting `SSERouteHandle` (regression guard for the Phase 0 fix) |
| D1 | `SSERouteHandle.DecodeEvent` happy path | valid bytes → correct `Event` |
| D2 | `SSERouteHandle.DecodeEvent` malformed bytes | typed decode error, `errors.As` reachable |
| C1 | `SSERoute.ClientMW` + `Consume` happy path | credential header sent, connection succeeds |
| C2 | `SSERoute.ClientMW` Satisfies-gating | an unrelated-scheme `ClientMW` does NOT run (mirrors `TestCall_ClientMWSatisfiesGating_UnrelatedImplNotRun`) |
| S1 | `Consume` happy path, single event | `Req` merge fields correctly build the URL, `fn` called once with the correctly decoded `Event` |
| S2 | `Consume` happy path, multiple events over one connection | `fn` called once per event, in order |
| S3 | `Consume` connection failure | `SSEConnectError{Attempt: N}` reported via `opts.OnError`, retried with backoff |
| S4 | `Consume` malformed event mid-stream | `SSEParseError` reported via `opts.OnError`, `fn` NOT called for that event, consumption continues, subsequent valid events still decoded and passed to `fn` |
| S5 | `Consume` `fn` returns an error for one event | `SSEHandlerError` reported via `opts.OnError` wrapping `fn`'s error, consumption CONTINUES, `fn` still called for the NEXT event (non-fatal policy) |
| S6 | `Consume` backoff doubling + cap | 2+ consecutive failed attempts observe doubling backoff, capped at `MaxBackoff` |
| S7 | `Consume` backoff reset after success | a successful event resets backoff to the initial step on the NEXT drop |
| S8 | `Consume` context cancellation | returns promptly, no goroutine leak |
| S9 | `Consume` observer | `RecordRequest` called per connection attempt, correct status |
| S10 | `Consume` nil observer | no panic, falls back to `stats.ObserverFromContext` |
| S11 | `Consume` re-derives credential per reconnect | a `ClientMW` Fn returning a DIFFERENT value on each call (e.g. an incrementing counter) sends a NEW value on every reconnect attempt — proves no caching/memoization happens inside `Consume` itself |
| S12 | `Consume` `OnCredentialRejected` on 401 | a reconnect attempt receiving HTTP 401 with an engaged `ClientMW` credential fires `opts.OnCredentialRejected`; a control case with NO engaged credential does NOT fire it (mirrors `Call`'s own gating exactly) |
| S13 | `Consume` multi-format decode resolution | a route declaring 2+ `Formats`; the response's `Content-Type` header selects the matching format for the WHOLE connection (all events decoded with the SAME resolved format, not re-negotiated per event) |
| S14 | `Consume` format fallback | no `Formats` declared (or none match the response `Content-Type`) → falls back to `DecodeEvent`'s JSON default |
| A1 | `CallSSEAdapter` happy path | delegates to the SAME internal loop as `Consume` — one `Event` emitted on the dst channel per decoded event (regression guard: proves the shared-loop delegation, not a reimplementation) |
| A2 | `CallSSEAdapter` malformed event mid-stream | `SSEParseError` emitted on the errs channel (via the SAME internal OnError path `Consume` uses), dst channel unaffected, consumption continues |
| A3 | `CallSSEAdapter` never produces `SSEHandlerError` | confirms the internal channel-push `fn` never returns an error (its own contract) |
| — | `ExampleConsume` | deterministic, `httptest.Server`-backed, full roundtrip via `examples/adapters-sse` |

## Files to create

| File | Responsibility |
|---|---|
| `api/rest/builder.go` (EDIT) | add 4 `Req`-side merge accessors + `DecodeEvent` + `ClientImplementations` field to `SSERouteHandle`; add `SSERoute.ClientHandle` (same file as `Route.ClientHandle`); wire `rb.pathMergeFields`/`rb.queryMergeFields`/`rb.headerMergeFields`/`rb.cookieMergeFields` through in `SSERoute.registerHandle` (Phase 0 fix) |
| `api/rest/middleware.go` (EDIT) | add `SSERoute.ClientMW` (same file as `Route.ClientMW`) |
| `adapters/nethttp/consumer.go` (NEW — mirrors `caller.go`'s file-per-concept convention) | `Consumer`, `NewConsumer`, `Consumer.WithBaseURL` — the strict SSE equivalent of `Caller`/`NewCaller`/`Caller.WithBaseURL` |
| `adapters/nethttp/binding.go` (EDIT — same file as `CallAdapter`/`DrainCallAdapter`/`IngestAdapter`/`SSEAdapter`) | `Consume`, `CallSSEAdapter`, `ConsumeOptions`, the SHARED unexported `consumeSSE` loop (both entry points delegate to it) + reconnect-backoff logic (mirrors `adapters/websocket`'s `dialLoop`) |
| `adapters/nethttp/stream_errors.go` (EDIT) | add `SSEHandlerError`; fix `SSEConnectError`/`SSEParseError`'s stale `[SSEClientStream]` doc-links to `[Consume]`/`[CallSSEAdapter]` and their "Stream.Errors" wording to "opts.OnError" |
| `adapters/nethttp/consumer_test.go` (NEW — mirrors `caller_test.go`) | N1 |
| `adapters/nethttp/binding_test.go` (EDIT — same file as `CallAdapter`/`DrainCallAdapter`'s tests) | M1–M2, D1–D2, C1–C2, S1–S14, A1–A3 + Example |
| `docs/features/sse-streaming.md` (EDIT) | add a client-consumption section alongside the existing server-side coverage |
| `.github/instructions/go-codex.instructions.md` (EDIT) | document `Consumer`/`NewConsumer`/`Consume`/`CallSSEAdapter`/`SSERoute.ClientMW`/`ClientHandle`/`DecodeEvent` in the `api/rest`/`adapters/nethttp` rows |
| `examples/adapters-sse/main.go` (EDIT) | rework into a FULL ROUNDTRIP demo: the SAME example both serves the SSE route (existing `.WithHandler`/`.Register`/`ServeSSE`, unchanged) AND consumes it back via a `Consumer` + `Consume` call directly (simpler single call for a demo — showcases the more ergonomic of the two entry points), replacing the hand-rolled `readSSELines` helper entirely |

## Out of scope (Phase 2)

- **`Last-Event-ID` resume + pluggable retry policy** — SPUN OUT into
  their own roadmap doc, [SSE Resume & Retry Policy](sse-resume-and-retry-policy.md).
  Both turned out to be genuinely client+server features (confirmed via
  code: the SSE writer never emits `id:`/`retry:` protocol fields
  anywhere today, and `stream.BroadcastHub` has no past-event buffer for
  a server-side replay mechanism), not client-only follow-ons — too large
  a scope to keep as a two-bullet aside in this doc. Phase 1 here always
  reconnects from scratch (stateless) with a fixed exponential backoff;
  see the new doc for the full client+server design space.

(The former "`chi` client-side SSE consumption" bullet was removed from
this section — it stated a permanent structural fact, not a deferred
Phase-2 feature; see the "Scope decisions" table above, which already
covers it.)

## Open design decisions (to resolve before/during implementation)

1. **Should Phase 0's `Req`-merge-field fix ship independently of
   `Consume`/`CallSSEAdapter`, as its own small bugfix PR?** It is a
   genuine, currently-silent gap unrelated to client consumption (a user
   could already be affected today). Leaning: ship it together in one
   round, since it has no other consumer today and splitting adds
   process overhead for zero benefit — but flag for reconsideration if
   implementation reveals the fix is larger than expected.
2. **`ConsumeOptions.MaxBackoff` default value** — Phase 1 leans toward
   the SAME 30s default `adapters/websocket.DialAdapterOptions` uses
   (consistency), but SSE and WebSocket have different reconnect-cost
   profiles (SSE is a lighter GET request); revisit if a real caller's
   preferred default differs meaningfully.
3. **Does a connection failure BEFORE the first successful event count
   differently from one AFTER?** (E.g., should consumption give up
   entirely after N consecutive pre-first-event failures, versus retrying
   forever once at least one event has been seen?) Leaning: retry forever
   in both cases for Phase 1 (matches `adapters/websocket`'s unconditional
   retry loop) — no caller-configurable give-up threshold yet; revisit if
   a real caller needs one. A caller-configurable give-up threshold is
   exactly the kind of policy [SSE Resume & Retry Policy](sse-resume-and-retry-policy.md)'s
   pluggable `RetryPolicy` would let a caller express directly — revisit
   THIS item once that doc's design resolves, rather than solving it here
   in isolation.
4. **Should `Consume` spawn its own goroutine internally, or always
   block the caller's?** Phase 1 leans toward ALWAYS blocking (matching
   `zeromq.Subscribe`'s existing precedent exactly, letting the caller
   decide whether/how to run it in the background via a plain `go`
   statement) rather than adding an internal-goroutine variant — avoids a
   second API shape for zero clear benefit; revisit only if a real caller
   finds the blocking contract awkward in practice.
