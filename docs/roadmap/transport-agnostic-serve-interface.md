# Transport-Agnostic `Serve`/`Caller` Interface — `api/rest`, `adapters/nethttp`, `adapters/chi`

> **Status: RESOLVED and IMPLEMENTED** (`go build`/`go vet`/`go test`/
> `just check`/`just examples` all green): `rest.ServerTransport`
> interface + `Server.Attach`/`.Serve(ctx)`, `nethttp.AttachMux`/
> `chi.AttachRouter`, and `rest.Client`/`nethttp.Attach`/`.Call` all
> shipped. **Reworked (mirroring
> [Pub/Sub Workflow Simplification](pubsub-workflow-simplification.md)'s
> `events.Client` design exactly, per direct user request): the client
> side lives as a domain-level `rest.Client` type in `api/rest` (NOT an
> adapter-level `nethttp.Caller` method) — `nethttp.Caller`/`NewCaller`/
> `Call[Req,Resp]` remain unchanged, lower-level primitives that
> `nethttp.Attach` wraps internally.**
>
> **PLANNED follow-up (Decision 6, tracked in the pub/sub doc): a
> further user request ("consistent workflow across the rest and event
> api ... the single only [workflow] the framework provides without
> escape hatches") removes `nethttp`/`chi`'s `NewCaller`/`Caller`/
> `Call[Req,Resp]`/`CallWithHandle[Req,Resp]`/`Serve`/`ServeOne`/
> `ServeSSE` from the PUBLIC API entirely — relocated to unexported
> internals that `Attach`/`AttachMux`/`AttachRouter`/`clientTransport`
> and the ports binding adapters (`CallAdapter`, `IngestAdapter`, etc.,
> which stay public unchanged) call internally. Also fixes a real gap:
> `AttachMux`/`AttachRouter` currently wire ONLY plain routes, never SSE
> — SSE becomes reachable through `Attach`+`Serve(ctx)` too. See
> [Pub/Sub Workflow Simplification](pubsub-workflow-simplification.md)'s
> Decision 6 for the full removal list and execution plan (this doc's
> REST side is executed as sub-phases 17e/17f of that plan).**
> This doc was "idea only, no driver yet" — a direct user request for a
> literal `Client.Publish`/`.Subscribe` call shape on the pub/sub side
> (see [Pub/Sub Workflow Simplification](pubsub-workflow-simplification.md)'s
> Decision 5) is the concrete driver this doc was waiting for, and
> resolves the shape-mismatch finding below via Option 1 (the option
> this doc itself already recommended): REST gets a genuinely NEW,
> ADDITIVE, opt-in blocking `Server.Serve(ctx)` (paired with
> `Server.Attach(transport)`), while today's existing `Serve(mux,
> builder)`/`Serve(r, builder)` (wire-only, caller owns their own
> `http.Server`) stay COMPLETELY UNCHANGED. See "Resolved design" below
> — this replaces the earlier "Proposed shared interface shape (sketch
> only)"/"Open questions" sections, which are now answered.
> [← Back to Roadmap](index.md)

## Why this isn't a drop-in mirror of pub/sub's design

Pub/sub's `ServeSubscribers(ctx) error` **blocks** — it actually RUNS
every registered subscription (one goroutine per channel) until `ctx`
is cancelled. REST's `Serve(mux *http.ServeMux, b *rest.Server) error`
does something categorically different: it takes **no `ctx` at all**,
**wires** every handler-bearing route onto `mux` via `mux.Handle(...)`
calls, and **returns immediately** — actual request-serving happens
later, via a SEPARATELY-called `http.Server.ListenAndServe()` (or
equivalent) that the caller invokes themselves, entirely outside
`rest`/`nethttp`'s control.

This means a shared interface literally named `Serve(ctx) error` cannot
mean the same thing in both places without picking one of two
directions:

1. **Give REST a genuinely BLOCKING variant too** — e.g.
   `nethttp.ServeAndRun(ctx context.Context, mux *http.ServeMux, addr
   string, b *rest.Server) error`, internally wiring the mux (today's
   `Serve`) THEN calling `(&http.Server{Addr: addr, Handler:
   mux}).ListenAndServe()` in a way that respects `ctx` cancellation
   (e.g. via `http.Server.Shutdown` on `ctx.Done()`). This is NEW
   capability, not a rename — REST has never owned the "run the actual
   server" responsibility; it has always stopped at "wire the mux," by
   deliberate design (the caller supplies their own `http.Server`
   config — TLS, timeouts, etc.). A shared interface would need this
   new variant to have a matching SHAPE to pub/sub's blocking one.
2. **Keep REST's `Serve` as "wire only," and give pub/sub's interface a
   matching TWO-STEP shape instead** — e.g. split `ServeSubscribers`
   into a non-blocking "wire" step (attach goroutine-starting closures
   without actually starting them) and a separate "run" step —
   mirroring REST's mux-then-listen split. This would be a MORE
   INVASIVE change to the pub/sub design already written into
   `pubsub-workflow-simplification.md`'s Decision 1 than simply adding
   REST a blocking variant — not recommended without strong
   justification, since pub/sub's "one call starts everything" shape is
   arguably more ergonomic for its own use case (there is no equivalent
   to "hand off to a separately-configured `http.Server`" in pub/sub —
   a broker connection has no analogous "server config" layer the
   caller might want control over).

**Resolved: option 1 (give REST an ADDITIONAL blocking variant) is the
chosen direction** — purely additive (existing `Serve` behavior is
completely unchanged), now with a concrete driver:
[Pub/Sub Workflow Simplification](pubsub-workflow-simplification.md)'s
Decision 5 gives `events.Client` a literal `Attach(transport)` +
`Subscribe`/`Publish`/`ServeSubscribers` call shape; this doc gives
`api/rest` the matching counterpart so an application can use the SAME
`Attach`-then-call vocabulary across its REST API and its pub/sub
channels.

## Resolved design

### Server side — `rest.Server.Attach`/`.Serve(ctx)` (NEW, additive)

```go
// api/rest — new:
type ServerTransport interface {
    Serve(ctx context.Context) error
}

// Server (existing type) gains:
func (b *Server) Attach(t ServerTransport) error  // returns TransportAlreadyAttachedError on a 2nd call
func (b *Server) Serve(ctx context.Context) error {
    if b.transport == nil { return ErrNoTransportAttached }
    return b.transport.Serve(ctx)  // BLOCKS
}
```

`nethttp.AttachMux(builder, mux, addr string) error` / `chi.AttachRouter(builder,
r, addr string) error` each build an internal `ServerTransport` whose
`Serve(ctx)`:
1. Wires every handler-bearing route onto `mux`/`r` — reuses TODAY'S
   EXISTING `nethttp.Serve(mux, builder)`/`chi.Serve(r, builder)` logic
   UNCHANGED, internally.
2. Constructs its OWN `&http.Server{Addr: addr, Handler: mux}` (or
   equivalent for chi's router) and calls `ListenAndServe()`.
3. On `ctx.Done()`, calls `http.Server.Shutdown(context.Background())`
   for a graceful stop, returning nil (or the shutdown error).

**Today's existing `nethttp.Serve(mux, builder)`/`chi.Serve(r,
builder)` (wire-only, non-blocking, no `http.Server` ownership) are
KEPT COMPLETELY UNCHANGED** — `Attach`+`Serve(ctx)` is a NEW, additive,
OPT-IN convenience for callers wanting ONE unified startup call; callers
needing full control over their `http.Server` (custom TLS config,
timeouts, etc.) keep using today's `Serve(mux, builder)` +
`http.ListenAndServe` themselves, exactly as today.

### Client side — `rest.Client`/`nethttp.Attach` (mirrors `events.Client` exactly, NOT an adapter-level type)

**Revised (superseding the original draft below the fold): the
caller-facing method lives on a NEW, domain-level `rest.Client` type in
`api/rest` — NOT on the adapter-level `nethttp.Caller` — mirroring
`events.Client.Publish`/`.Subscribe`'s design exactly, per direct user
request ("I want a rest.Client and a rest.Server not an adapter
nethttp.Caller to mirror the exact design of the event api").**

```go
// api/rest — new:
type ClientTransport interface {
    Call(ctx context.Context, route any, req any) (any, error)
}
type Client struct { /* unexported: mu sync.RWMutex; transport ClientTransport */ }
func NewClient() *Client
func (c *Client) Attach(t ClientTransport) error  // returns ClientTransportAlreadyAttachedError on a 2nd call
func (c *Client) Call(ctx context.Context, route any, req any) (any, error) {
    if c.transport == nil { return nil, NoClientTransportAttachedError{} }
    return c.transport.Call(ctx, route, req)
}
```

`nethttp.Attach(client *rest.Client, httpClient *http.Client, baseURL
string) error` builds an internal `ClientTransport` WRAPPING the
EXISTING, unchanged `nethttp.Caller`/`nethttp.NewCaller`/
`nethttp.Call[Req,Resp]` (mirrors `zeromq.Attach`/`mqtt5.Attach`
wrapping their own `*Caller` internally) and calls `client.Attach(...)`.

Same Go constraint and same reflection technique as
[Pub/Sub Workflow Simplification](pubsub-workflow-simplification.md)'s
Decision 5 `Client.Publish`/`.Subscribe`: a method cannot introduce its
own `[Req, Resp any]` type parameters, so `route`/`req` are `any`
(dynamic types `rest.Route[Req,Resp]`/`Req`), and the internal
transport recovers `Req`/`Resp` via reflection against the route's
ALREADY-CONCRETE `Decode`/`Encode`/`BuildPath` closures (mirrors
`buildSubscriberRoute`'s proven technique), then performs the actual
HTTP round-trip via the wrapped `*Caller`'s `*http.Client` (concrete,
no reflection needed there). Returns `any` (dynamic type `Resp`) — the
caller must type-assert the result; a `route`/`req` type mismatch is a
`rest.TransportTypeMismatchError` at call time, not a compile error —
same explicit, scoped trade-off as pub/sub's Decision 5.

Resulting workflow:

```go
client := rest.NewClient()
_ = nethttp.Attach(client, httpClient, baseURL)   // attach adapter to client
respAny, err := client.Call(ctx, getUserRoute, req) // ACTUAL Client method
resp := respAny.(GetUserResp)
```

**`chi` has no client side to unify** — confirmed via code: chi is
server-routing-only (no `Caller`, no `Call` function exists in
`adapters/chi` at all). This section applies ONLY to `nethttp`.

`rest.Client` has NO spec/registry (unlike `rest.Server`, which keeps
ALL of `Server`'s existing spec-accumulation responsibilities) — a pure
connection/transport holder, same scope `nethttp.Caller` had before
this rework, just relocated to the `api/rest` domain level.

### Answers to this doc's own previously-open questions

- **"Does `ServeAndRun` belong at all, given REST's 'caller owns their
  own `http.Server`' philosophy?"** → Yes, as a purely OPT-IN additive
  convenience (`Server.Serve(ctx)` after `Attach`) — NOT a replacement
  for today's `Serve(mux, builder)`. Both coexist, serving different
  callers' needs. `AttachMux`/`AttachRouter` take an `addr string`
  directly (no separate `Options` struct for TLS/timeouts in the FIRST
  cut — a caller needing those keeps using the unchanged
  `Serve(mux, builder)` + their own `http.Server{...}` today; an
  `Options`-carrying variant can be added later if a real need appears,
  mirroring this doc's own "don't add speculatively" convention).
- **"What server-side bundling type would REST need?"** → `rest.Server`
  itself gains the `Attach`/transport field directly — no NEW bundling
  type needed (`Server` already exists and is the natural place,
  mirroring how `events.Client` — also a pre-existing spec/registry
  holder — is Decision 5's natural attach point).
- **"Is there a genuine cross-cutting use case?"** → Yes: THIS
  unification itself (an app wanting ONE generic `Attach`-then-`Serve`/
  `Publish`/`Subscribe`/`Call` vocabulary across its REST API and its
  pub/sub channels) is now a concrete, motivated driver, not
  hypothetical.

## Next steps — reminder tied to the pub/sub doc's own finalization

This doc's shape-mismatch finding is now RESOLVED (see "Resolved
design" above) — implementation is tracked alongside
[Pub/Sub Workflow Simplification](pubsub-workflow-simplification.md)'s
Decision 5. The broader reminder below still stands unchanged: this
finding was ONE confirmed gap in REST's own workflow, found only
because
[Pub/Sub Workflow Simplification](pubsub-workflow-simplification.md)
was being reviewed against its own two headline goals (simple
declarative workflow for the user; transport/protocol-agnostic
abstraction for the pub/sub pattern) and REST's `Serve`/`Caller` shape
came up for comparison along the way. **It was NOT the product of a
dedicated, from-scratch review of REST's OWN workflow against those
SAME two goals** — REST's shipped design
(`docs/design/middleware-workflow-simplification.md`) has not itself
been walked step-by-step the way pub/sub's was (declaration workflow
diagrams, escape-hatch-by-escape-hatch review, design-gap closure
pass).

**Reminder: once `pubsub-workflow-simplification.md` reaches its own
finalization milestone, come back and do that SAME kind of full review
pass on REST's shipped design** — confirm whether this doc's shape-
mismatch finding is REST's ONLY gap against the 2 goals, or whether a
dedicated review (mirroring the diagrams/escape-hatch/design-gap-
closure methodology already applied to pub/sub) surfaces others. Do not
treat this doc's single finding as a complete substitute for that
review — it was a byproduct of a DIFFERENT doc's review, not the
review itself.
