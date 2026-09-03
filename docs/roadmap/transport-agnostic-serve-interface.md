# Transport-Agnostic `Serve`/`Caller` Interface — `api/rest`, `adapters/nethttp`, `adapters/chi`

> **Status:** Idea only — no driver yet, spun out of
> [Pub/Sub Workflow Simplification](pubsub-workflow-simplification.md)'s
> new `events.SubscriberServer` interface (each pub/sub adapter's
> `Caller` implements a shared, transport-agnostic
> `ServeSubscribers(ctx context.Context) error` method, letting
> application code start consuming registered channels without caring
> whether mqtt5/mqtt3/zeromq is underneath — pub/sub also gained a
> matching `events.PublisherClient[T]` interface for the publish side,
> completing that doc's own transport-agnostic symmetry). This doc
> investigates whether REST's `Serve`/`Caller` should get an analogous
> shared interface across `nethttp`/`chi` — but found a real SHAPE
> MISMATCH before any design could be proposed, which is this doc's
> actual scope: investigate, not yet resolve. **See "Next steps" at the
> end of this doc** for a broader reminder tied to the pub/sub doc's own
> finalization.
> [← Back to Roadmap](index.md)

## Why this isn't a drop-in mirror of pub/sub's design

Pub/sub's `ServeSubscribers(ctx) error` **blocks** — it actually RUNS
every registered subscription (one goroutine per channel) until `ctx`
is cancelled. REST's `Serve(mux *http.ServeMux, b *rest.Builder) error`
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
   string, b *rest.Builder) error`, internally wiring the mux (today's
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

**This doc's recommendation for a future investigation session:**
option 1 (give REST an ADDITIONAL blocking variant) is the more
promising direction, since it's purely additive (existing `Serve`
behavior is completely unchanged) and would let `nethttp`/`chi`'s NEW
blocking variant satisfy a shared interface identical in shape to
pub/sub's `SubscriberServer`. Not decided — needs its own dedicated
design pass once a concrete driver/use case appears (e.g. an
application wanting to write ONE generic "start serving" call across
its REST API and its pub/sub channels, in a single unified startup
routine).

## Proposed shared interface shape (sketch only, NOT finalized)

```go
// Hypothetical — lives in api/rest (mirrors events.SubscriberServer
// living in api/events: a neutral, transport-agnostic location every
// adapter already depends on).
type Server interface {
    Serve(ctx context.Context) error
}
```

`nethttp.ServeAndRun`/`chi.ServeAndRun` (or similarly named — naming not
decided) would need to return a value implementing this, OR `*Caller`-
equivalent types would need to gain this method directly (REST doesn't
currently have a `Caller`-shaped SERVER-side value at all — `Serve`
takes `mux`+`builder` directly, no bundling type exists to attach a
method to, unlike pub/sub's `Caller`). This asymmetry (REST has no
server-side bundling type; pub/sub's `Caller` bundles BOTH client and
server-ish responsibilities) is itself worth investigating further —
not resolved here.

## Open questions (not answered — for the future investigation session)

- Does `nethttp.ServeAndRun`/`chi.ServeAndRun` (or equivalent) belong at
  all, given REST's deliberate "caller owns their own `http.Server`
  config" philosophy? Would this new function need its OWN `Options`
  for TLS/timeouts/etc., duplicating what `http.Server` already offers?
- What server-side bundling type (if any) would REST need to attach a
  `Serve(ctx) error` method to, given it has no `Caller`-equivalent
  today?
- Is there a genuine cross-cutting use case (an app wanting ONE
  generic startup loop across REST + pub/sub) that justifies this, or
  is "call `nethttp.Serve` then `http.ListenAndServe` yourself,
  separately from `mqtt5.Caller.ServeSubscribers`" already sufficient
  in practice?

No implementation, no locked API — this doc exists to hold the
shape-mismatch finding and scope a future investigation, not to answer
it.

## Next steps — reminder tied to the pub/sub doc's own finalization

This doc's shape-mismatch finding is ONE confirmed gap in REST's own
workflow, found only because
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
