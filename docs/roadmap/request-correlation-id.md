# Request/Event Correlation ID — a first-class, cross-cutting observability primitive

> **Status:** Design draft — exploring feasibility, not yet implemented.
> [← Back to Roadmap](index.md)

## Motivation

Confirmed via code: **no first-class request/event/call correlation ID
exists anywhere in go-codex today.** `stats.TraceObserver`
(`StartSpan`/`EndSpan`, confirmed `stats/observer.go:103-147`) handles
distributed TRACING specifically, but its span identifier is owned
entirely by the backing tracer implementation (OpenTelemetry, etc.) — a
plain `LoggingObserver` or a custom metrics-only `Observer` has no way to
tag `RecordRequest`/`RecordSubscribe`/`RecordPublish` with a correlating
ID, because **these methods take no `ctx context.Context` parameter at
all** (confirmed `stats/observer.go:50-72`) — there is nowhere for such
an ID to even be read from, structurally, without a new mechanism.

`adapters/mqtt5/reqreply.go`'s `Call`/`Serve` ALREADY generates a real
per-call correlation ID (`corrID := uuid.New()`) but it exists ONLY as
wire-protocol data (`CorrelationData []byte`, used purely for MQTT5
reply matching) — invisible to Observer/logging entirely. Every other
boundary (REST, events, ports) has no equivalent concept. Confirmed via
grep: existing "RequestID"-shaped mentions in this codebase
(`examples/rest-api/routes/models.go`'s `RequestID string` field,
`adapters/nethttp/transform.go`'s doc-comment
`req.CorrelationID = r.Header.Get("X-Correlation-ID")`) are user-declared
domain fields, invented ad hoc per application — not a go-codex
mechanism a caller can rely on across boundaries.

**The gap this doc explores:** a single, protocol-agnostic way to tag
"this one logical unit of work" (one REST request, one event
dispatch/publish, one reqreply `Call`/`Serve` invocation, one
`ports.File`/`Cache`/`SQL` call) with an ID that reaches structured logs
and — if a design for it is confirmed — `Observer` calls, without every
application reinventing it.

## Scope decisions (what's in Phase 1, what's deferred)

| In scope | Out of scope |
|---|---|
| A ctx-carried correlation ID, generated once per logical unit of work | Full distributed tracing (already covered by `TraceObserver`/OpenTelemetry integration) — this is a lighter-weight, NOT-a-span-tree primitive |
| Retrieval helpers for application code (handlers, Fns, middleware) | Auto-propagation ACROSS process boundaries (e.g. auto-injecting into outgoing HTTP headers or MQTT user properties) — Phase 2 at best |
| Reaching structured logs | Pluggable ID generation strategy (ULID vs UUID vs snowflake) — default only, Phase 1 |
| REST/events/reqreply adapter entry points setting it once per request/message/call | `ports.File`/`Cache`/`SQL`/`Dir` coverage — the user explicitly asked for this; flagged as a genuinely open question below, not force-included (ports lack the per-request dispatch loop REST/events/reqreply have, the SAME structural point `protocol-native-features.md`'s Review-7 already raised for capability-like mechanisms) |
| Whether/how it reaches `stats.Observer` at all | — still open, see "Observer integration" below (structural blocker: existing methods take no `ctx`) |

## Prior art already in this codebase

- `stats.WithObserver(ctx, obs)` / `stats.ObserverFromContext(ctx)` — the
  exact ctx-storage/retrieval pattern this doc's `WithRequestID`/
  `RequestIDFromContext` should mirror.
- `middleware.ContextField[V]` (ALREADY SHIPPED,
  `middleware/context_field.go`) — the OTHER existing ctx-carried-value
  precedent, but codec-typed and DECLARED per route/channel (a value a
  specific `Middleware[In,Out]` writes/reads). A request ID is
  AMBIENT — set automatically by the adapter itself, not declared per
  route — closer in spirit to `Observer`'s own ctx pattern than to
  `ContextField`'s. (See Open design decision 1.)
- `stats.TraceObserver.StartSpan`/`EndSpan` — the closest EXISTING
  "per-call identifier" concept, but owned entirely by the tracer
  implementation, never exposed as a plain string ID an application or a
  non-tracing `Observer` could read.
- `adapters/mqtt5/reqreply.go`'s own `corrID := uuid.New()` /
  `CorrelationData []byte` — proves the underlying NEED already exists
  at the protocol level; it has simply never been surfaced to the
  app/observability layer.

## Relationship to distributed tracing (`TraceObserver`) — the key design question

This was the sharpest open question raised while drafting this doc: if a
trace span already exists (potentially spanning many services), does a
per-call correlation ID need to BE the trace/span ID, or is it something
else entirely? **Confirmed answer, grounded in the real interface shape,
not assumption: these should stay two DELIBERATELY SEPARATE concepts,
connected by convention, not merged.**

**Three distinct ID concepts are actually in play here — do not conflate
them:**

1. **Distributed trace/span ID** — owned entirely by `TraceObserver`'s
   backing tracer (OpenTelemetry, etc.), cross-service, span-tree shaped
   (a trace has many spans; a span has a parent). Out of this doc's
   scope to redesign — `TraceObserver` already exists and works.
2. **Wire-level reqreply correlation ID** — `adapters/mqtt5/reqreply.go`'s
   existing `corrID`/`CorrelationData`, a PROTOCOL mechanism whose sole
   job is matching an async reply back to its request over MQTT5. It is
   ephemeral, per-call, and MQTT5-specific — it has no life outside that
   one `Call` invocation's `replyCh` wait.
3. **This doc's actual scope: an application/business-level Request/Event
   ID** — generated once per logical unit of work, for logs/metrics/
   support-facing correlation ("what's your request ID?"), independent
   of whether distributed tracing is even configured at all. This is the
   ID a `LoggingObserver` or a plain metrics sink needs, and it must work
   even with `NoopObserver`/no tracer attached.

**The confirmed, clean connection mechanism — validated by the EXISTING
`TraceObserver` interface, not a new capability:**
`TraceObserver.StartSpan(ctx context.Context, operation, name string)
context.Context` (confirmed `stats/observer.go:138-144`) already RECEIVES
`ctx`. If the adapter sets this doc's request ID into `ctx` (via
`stats.WithRequestID`) **before** calling `to.StartSpan(ctx, ...)`, a
`TraceObserver` implementation (e.g. an OpenTelemetry wrapper) can read
`stats.RequestIDFromContext(ctx)` itself, internally, and attach it as a
**span attribute** —

```go
func (t *MyTracer) StartSpan(ctx context.Context, operation, name string) context.Context {
    attrs := []attribute.KeyValue{attribute.String("name", name)}
    if id := stats.RequestIDFromContext(ctx); id != "" {
        attrs = append(attrs, attribute.String("app.request_id", id))
    }
    _, ctx = otel.Tracer("go-codex").Start(ctx, operation, otel.WithAttributes(attrs...))
    return ctx
}
```

**— zero change to `TraceObserver`'s interface is needed.** This mirrors
OpenTelemetry's OWN established practice directly: a business/correlation
ID is attached as a span ATTRIBUTE (or via the Baggage API) ALONGSIDE the
trace's own ID system, never merged into or replacing the trace/span ID
itself. Keeping the two concepts genuinely separate is the
industry-standard approach here, not a workaround this design is forced
into.

**The one real requirement this imposes:** adapter wiring order matters
— `stats.WithRequestID(ctx, id)` must run BEFORE `to.StartSpan(ctx, ...)`
at each adapter's entry point, so the tracer can see it. This is a
documented ADAPTER-WIRING CONVENTION, not an interface change — flagged
for `stats.TraceObserver`'s own doc comment to mention once this ships
(see "Files to create" below).

**Not decided — flagged as Open design decision 6:** whether reqreply's
existing WIRE-LEVEL `corrID` (concept 2 above) should ever be REUSED as
this doc's Request ID (concept 3) for convenience — i.e., one ID visible
in BOTH the application's logs AND on the MQTT5 wire as
`CorrelationData`. This is a genuine trade-off (convenience of one
visible ID vs. overloading a pure protocol-matching mechanism with
human/business semantics it was never designed to carry) — not
force-resolved here.

## API surface (sketch, not locked)

```go
// package stats — co-located with WithObserver/ObserverFromContext,
// the exact pattern this mirrors.

// WithRequestID attaches id to ctx, retrievable via RequestIDFromContext.
func WithRequestID(ctx context.Context, id string) context.Context

// RequestIDFromContext retrieves the ID attached via WithRequestID, or
// "" if none was set — mirrors ObserverFromContext's NoopObserver{}
// default pattern (a zero value, never an error).
func RequestIDFromContext(ctx context.Context) string

// NewRequestID generates a new opaque ID for an adapter to call
// automatically at its own entry point. Default generator not yet
// locked (see Open design decision 4) — likely a UUID, matching
// adapters/mqtt5/reqreply.go's existing uuid.New() precedent.
func NewRequestID() string
```

Adapters call `stats.WithRequestID(ctx, stats.NewRequestID())` ONCE at
their own entry point (`nethttp`/`chi`'s `Serve` dispatch, `mqtt5`/
`mqtt`/`zeromq`'s `ServeSubscribers`/`Publish`/reqreply `Serve`/`Call`) —
mirroring `middleware.EnsureContextFields`'s existing "pre-allocate once
per request, propagate via ctx" pattern — BEFORE calling `TraceObserver.
StartSpan` (see the tracing section above for why the ordering matters).

## Structured errors

None anticipated — this is a value-propagation mechanism, not an
operation that can fail. `RequestIDFromContext` returns a zero value
(`""`), never an error, mirroring `ObserverFromContext`'s own contract.

## Observer integration — the open structural question

`stats.Observer`'s existing methods (`RecordRequest`, `RecordSubscribe`,
`RecordPublish`) do NOT take `ctx context.Context` — confirmed
`stats/observer.go:50-72`. This means a correlation ID stored only in
ctx is invisible to these calls without either:

- **(a) a breaking signature change** — rejected on sight, same
  "don't compromise an existing shipped contract" bar this codebase
  applies elsewhere (e.g. `protocol-native-features.md`'s Review-8
  explicitly rejected changing `RecordSubscribe`'s `success bool`
  meaning for the same reason); or
- **(b) a NEW, additive extension interface**, mirroring
  `SecurityObserver`/`DispositionObserver`'s exact pattern:

```go
type RequestIDObserver interface {
    RecordRequestID(id string)
}
```

  called by the SAME adapter code that already resolves `obs` from ctx,
  immediately after resolving the ID. **Not locked** — needs a
  throwaway prototype (following this project's established
  spike-then-delete methodology) to confirm ordering/composability with
  the OTHER optional observer extensions (`SecurityObserver`,
  `TraceObserver`, `DispositionObserver`) attached to the SAME `Observer`
  value, before committing to this shape.

## Unit test plan (sketch)

- `WithRequestID`/`RequestIDFromContext` round trip.
- `RequestIDFromContext` on a ctx with none set returns `""`.
- `NewRequestID` uniqueness (basic sanity check, not a full collision
  test).
- Each adapter's entry point sets a NEW id per call/request/message
  (never reused across calls).
- `stats.WithRequestID` called before `TraceObserver.StartSpan` — a
  test confirming a stub `TraceObserver` can read the ID back via
  `RequestIDFromContext(ctx)` inside its own `StartSpan` implementation.
- If `RequestIDObserver` (Observer integration option (b)) is confirmed:
  additive-behavior tests mirroring `DispositionObserver`'s own test
  shape (nil-safe, fires correctly, doesn't break existing `Observer`
  implementations that don't implement it).

## Files to create (sketch)

| File | Responsibility |
|---|---|
| `stats/request_id.go` | `WithRequestID`/`RequestIDFromContext`/`NewRequestID` |
| Each adapter's existing dispatch entry point | One-line addition: `ctx = stats.WithRequestID(ctx, stats.NewRequestID())` BEFORE the existing `TraceObserver.StartSpan` call |
| `stats/observer.go`'s `TraceObserver` doc comment | Documentation-only addition (once this ships): recommend implementations read `stats.RequestIDFromContext(ctx)` and attach it as a span attribute — a convention note, not an interface change |

## Out of scope (Phase 2)

- Cross-process propagation (auto-injecting into outgoing HTTP headers
  or MQTT user properties) — a real future extension, not designed here.
- Pluggable ID generation strategy (ULID vs UUID vs snowflake) — Phase 1
  ships one default only.
- `ports.File`/`Cache`/`SQL`/`Dir` coverage — see Open design decision 3.

## Open design decisions

1. **Does this reuse `middleware.ContextField[V]`, or get its own,
   simpler ctx-storage pair mirroring `stats.WithObserver`?** Leaning:
   its own pair — `ContextField` is codec-typed and declared per
   route/channel by the application; a request ID is ambient and
   adapter-set, structurally closer to `Observer`'s own ctx pattern.
   Not locked.
2. **Does `Observer` gain a new `RequestIDObserver` extension, and if
   so, what's its precise call-ordering relative to
   `SecurityObserver`/`TraceObserver`/`DispositionObserver`** when
   several are attached to the same `Observer` value? Needs a throwaway
   prototype before locking (see "Observer integration" above).
3. **Should `ports.File`/`Cache`/`SQL`/`Dir` get this too?** The user
   explicitly asked for this. Worth a concrete, dedicated investigation
   — but ports' `Read`/`Write`/`Get`/`Set` call shape has no per-request
   dispatch loop or `Attach` step the way REST/events/reqreply do, the
   SAME structural point `protocol-native-features.md`'s Review-7
   already raised for capability-like mechanisms on ports. NOT assumed
   away — flagged for a dedicated future round, not force-resolved here.
4. **ID generation default** — a UUID is the obvious default (matches
   `uuid.New()` already used in `adapters/mqtt5/reqreply.go`), but not
   fully locked (a shorter/sortable ID like ULID might be preferable for
   log readability — not evaluated yet).
5. **Naming** — `RequestID` (REST-flavored) vs. a more protocol-neutral
   name (`CorrelationID`, `CallID` — `TraceID` is unavailable, already
   implicitly "owned" by `TraceObserver`'s concept). Not decided.
6. **Should reqreply's existing wire-level `corrID` ever be REUSED as
   this doc's Request ID, for convenience** (one ID visible in both logs
   and on the wire)? A genuine trade-off (convenience vs. overloading a
   pure protocol-matching mechanism with business semantics) — see the
   "Relationship to distributed tracing" section above. Not decided.
   Cross-referenced from [Feature: Codec-Declared Middleware](../features/codec-declared-middleware.md)'s
   own "Relationship to... `request-correlation-id.md`" section — that doc
   defers entirely to THIS one for correlation ID design, noting its own
   Phase 1b User-Property mechanism could eventually serve as the
   attachment surface for a future auto-propagation phase (decision 7
   below), without designing that integration itself.
7. **If/when auto-propagation onto the wire is designed** (item still
   "Phase 2 at best" per the scope table above), MQTT5's natural carrier
   would be a User Property (e.g. `"X-Request-ID"`) — [Feature: Security &
   Auth](../features/security.md)'s Phase 1b (`mqtt5.
   FromUserPropertyParam`/`FromResponseUserPropertyParam`, once shipped)
   would be the natural DECLARATION mechanism for it, reusing the SAME
   named-param-as-middleware pattern any other User Property gets — noted
   here as a forward pointer only, not a commitment; this doc still owns
   the actual design decision of whether/how to do this.

## See also

- [Protocol-Native Features](protocol-native-features.md) — §8 Handler
  Disposition and its Review-8 `DispositionObserver` are the closest
  existing precedent for "add a new, additive Observer extension
  interface, don't change an existing method's signature." The same
  discipline applies to this doc's `RequestIDObserver` candidate.
- [D-0004 — ReqReply Workflow Simplification](../design/d-0004-reqreply-workflow-simplification.md)
  — `mqtt5.Call`'s existing `corrID`/`CorrelationData` is the concrete,
  already-shipped evidence motivating this doc, and Open design decision
  6 above is a direct cross-reference to it.
