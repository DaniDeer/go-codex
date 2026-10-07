# Declarative Router Groups — `api/rest`, `api/events`, `api/reqreply` (Phases A+B+C)

> **Status:** `api/rest`, `api/events`, and `api/reqreply` all shipped. A
> deferred-item review and documentation graduation to `docs/design/`
> remain — see
> [`docs/roadmap/declarative-router-groups.md`](../roadmap/declarative-router-groups.md)
> for the full cross-pattern design.
>
> See also: [API Builder](api-builder.md) · [Codec-Declared Middleware](codec-declared-middleware.md)

`Router` groups independently-declared `rest.Route`/`rest.SSERoute` values
under a shared path PREFIX, optionally attaching reusable-class
`middleware.RouteMiddleware` to every leaf registered under it in one
declaration — instead of repeating the full path string and the same
`.Use(mw)` call on every single route.

**The codec-declaration step itself never changes.** A route declared with
`rest.NewRoute(...)` + `.WithHandler(fn)` is EXACTLY the same whether or not
it ever gets grouped under a Router — Router is purely an additive,
optional assembly layer on top of an unchanged declaration step, so routes
can be declared independently, possibly in entirely different packages, and
assembled later in one place.

## Quick example

```go
listRoute := rest.NewRoute[ListReq, []Item]("GET", "", listReqCodec, itemsCodec).
    WithHandler(listItems)
createRoute := rest.NewRoute[Item, Item]("POST", "", itemCodec, itemCodec).
    WithHandler(createItem)

authMiddleware := middleware.Middleware{
    Name: "bearer-auth",
    Security: &middleware.SecurityDeclaration{
        SchemeName: "bearerAuth",
        Scheme:     route.BearerScheme("JWT"),
        Scopes:     []string{"items:write"},
    },
}

itemsRouter := rest.NewRouter("/items").
    Use(authMiddleware).
    Route(listRoute).
    Route(createRoute)

apiRouter := rest.NewRouter("/api/v1").Mount(itemsRouter)

// Inspect the fully-assembled tree BEFORE registering anything:
for _, e := range apiRouter.Routes() {
    fmt.Printf("%-7s %-20s %v\n", e.Method, e.Path, e.MiddlewareNames)
}
// GET     /api/v1/items        [bearer-auth]
// POST    /api/v1/items        [bearer-auth]

err := apiRouter.Register(server)
```

See `examples/rest-api/demo_router_groups.go` for a runnable version.

## API surface

| Method | Behavior |
|---|---|
| `NewRouter(prefix string, opts ...RouterOpt) Router` | Declares a Router with a STATIC path prefix (no `{var}` placeholders for v1). |
| `Use(mws ...middleware.RouteMiddleware) Router` | Appends to the Router's own PERMANENT middleware list — dispatched BEFORE every grouped leaf's own middleware (Router-first ordering). |
| `With(mws ...middleware.RouteMiddleware) Router` | One-shot: applies ONLY to the NEXT `.Route()` call, never leaks to a subsequent sibling. |
| `Route(r routable) Router` | Attaches a leaf (`Route[Req,Resp]`/`SSERoute[Req,Event]`). |
| `Mount(sub Router) Router` | Attaches a nested sub-Router — a NEW path segment + a fresh middleware stack for everything under it. |
| `Group(fn func(sub Router) Router) Router` | SAME prefix, scoped middleware subset for a SUBSET of routes (e.g. "auth only on POST/PUT/DELETE, GET stays open") — no new path segment. `fn` must explicitly `return` its built-up value. |
| `Register(b *Server) error` | Walks the tree, composes every leaf's final prefix+middleware, and registers each one — the SAME, unchanged `Register` each leaf's own type already implements. |
| `Routes() []RouterEntry` | A flat, declaration-ordered inspection of the fully-assembled tree — callable WITHOUT a `*Server`. |
| `Walk(fn WalkFunc) error` | The recursive visitor `Routes()` wraps; stops on the first non-nil error returned by `fn`. |

`Router` is a fully **immutable value type** — every method returns a new
value, matching `Route`/`Channel`/`Subscriber`/`Publisher` exactly.
Concurrent reads/derivations of the same `Router` value need no
synchronization at all.

## Recovering a typed handle

The overwhelmingly common case (discard the returned handle) needs no
extra step. A route that needs its `*RouteHandle` back — even when grouped
via a Router — attaches `WithHandleCallback` directly at declaration time:

```go
var listHandle *rest.RouteHandle[ListReq, []Item]
listRoute := rest.NewRoute[ListReq, []Item]("GET", "", listReqCodec, itemsCodec,
    rest.WithHandleCallback(func(h *rest.RouteHandle[ListReq, []Item]) { listHandle = h }),
).WithHandler(listItems)
```

## Client-side: avoiding a silent path mismatch

`Route.ClientHandle()` is a bare accessor — it has no way to know a Router
grouped the SAME route under a prefix somewhere else. Pass `WithRouter` to
apply that Router's CURRENT prefix+middleware before building the client
handle, so client and server never disagree about the final path:

```go
// serverSideRouterVar is the SAME exported Router the server package
// registers through.
handle := route.ClientHandle(rest.WithRouter(serverSideRouterVar))
```

## Errors

`RouterPrefixError{Prefix, ComposedPath, Err}` is returned by `Register`
ONLY when a leaf's FINAL, prefix-composed path fails the SAME validation
standalone `Register` would apply. Every OTHER leaf error
(`DuplicateMiddlewareNameError`, `BoundMiddlewareReqMismatchError`, security
coverage failures, etc.) propagates completely UNWRAPPED — `Router` adds no
new validation logic of its own.

## `api/events` (Phase B)

`events.Router` is the SAME shape as `rest.Router` — a separately-compiled,
per-package copy — adapted for pub/sub's subscribe/publish role axis
instead of REST's HTTP-method axis. Leaves are `events.Subscriber[T]`/
`events.Publisher[T]`; grouping assembles a shared TOPIC prefix instead of
a path prefix.

```go
temperatureSub := events.NewChannel[Reading]("temperature", readingCodec).
    WithSubscribe(events.Subscribe{}).WithHandler(onTemperature)
humiditySub := events.NewChannel[Reading]("humidity", readingCodec).
    WithSubscribe(events.Subscribe{}).WithHandler(onHumidity)

auditMiddleware := middleware.Middleware{Name: "audit-log"}

readingsRouter := events.NewRouter("readings").
    Use(auditMiddleware).
    Route(temperatureSub).
    Route(humiditySub)

sensorsRouter := events.NewRouter("sensors").Mount(readingsRouter)

for _, e := range sensorsRouter.Routes() {
    fmt.Printf("%-10s %-28s %v\n", e.Role, e.Path, e.MiddlewareNames)
}
// subscribe  sensors/readings/temperature [audit-log]
// subscribe  sensors/readings/humidity    [audit-log]

err := sensorsRouter.Register(client)
```

See `examples/events-api/demo_router_groups.go` for a runnable version.

### Differences from `api/rest`

| Aspect | `api/rest` | `api/events` |
|---|---|---|
| Leaf types | `Route[Req,Resp]`/`SSERoute[Req,Event]` | `Subscriber[T]`/`Publisher[T]` |
| Second axis | HTTP method (`RouterEntry.Method`) | role: `"subscribe"`/`"publish"` (`RouterEntry.Role`) |
| Prefix joining | `joinRouterPath` — ALWAYS forces a leading `/` | `joinRouterTopic` — NEVER forces a leading separator (MQTT/ZeroMQ topics don't use one) |
| `Register` target | `*rest.Server` | `*events.Client` |
| `RouterPrefixError` field | `ComposedPath` | `ComposedTopic` |
| `registerAny` delegates to | `Route.Register`/`RegisterHandle` | `Subscriber.Register` (NOT `Handle` — Register is the handler-requiring, `Client.SubscriberEntries`-populating path) / `Publisher.Handle` (no `Register` method exists for `Publisher`) |
| Handle-callback opts | `WithHandleCallback[Req,Resp]` only | `WithHandleCallback[T]` (both roles) **+** `WithSubscribeHandleCallback[T]`/`WithPublishHandleCallback[T]` (role-targeted) — mirrors `Formats`/`SubscribeFormats`/`PublishFormats`' additive 3-way split |
| `ClientHandle`'s `WithRouter` opt | Yes (`Route.ClientHandle(rest.WithRouter(rt))`) | N/A — no bare zero-argument accessor exists (`Subscriber.Handle(client)`/`Publisher.Handle(client)` already take a builder argument) |

### Role-targeted handle callbacks

```go
var subHandle *events.ChannelHandle[Reading]
sub := events.NewChannel[Reading]("temperature", readingCodec,
    events.WithSubscribeHandleCallback(func(h *events.ChannelHandle[Reading]) { subHandle = h }),
).WithSubscribe(events.Subscribe{}).WithHandler(onTemperature)
```

`events.WithHandleCallback` fires for BOTH roles built from the same
`Channel` (if both a `Subscriber` and a `Publisher` are built from it);
`WithSubscribeHandleCallback`/`WithPublishHandleCallback` fire for only
one.

### A Router-contributed Security middleware still needs a per-leaf implementation

Router-contributed middleware (via `.Use()` at the Router level) reaches
each leaf's spec-level `mws`/`middlewareHandlers` ONLY — it can never
populate a leaf's `impls` (`middleware.ServerImplementation`) list, since
that requires a concrete `fn` attached directly via `Subscriber.SubscribeMW`/
`Publisher.PublishMW` on each leaf. A Security-carrying middleware
Router-grouped this way will still fail `events.CheckCoverage` unless each
leaf separately attaches a satisfying implementation — Router only composes
prefixes and spec-level middleware names, never implementations.

## `api/reqreply` (Phase C)

`reqreply.Router` is the SAME shape as `rest.Router`/`events.Router` —
a separately-compiled, per-package copy — but SIMPLER than both: a single
`reqreply.Route[Req,Resp]` is already the complete, final leaf (topic +
both codecs + handler, one `Register` call), so there is no second axis
(no HTTP method, no subscribe/publish role) for `RouterEntry` or `Group`
to target.

```go
addRoute := reqreply.NewRoute[AddReq, AddResp]("add", addReqCodec, addRespCodec).
    WithHandler(add)
subtractRoute := reqreply.NewRoute[AddReq, AddResp]("subtract", addReqCodec, addRespCodec).
    WithHandler(subtract)

auditMiddleware := middleware.Middleware{Name: "audit-log"}

computeRouter := reqreply.NewRouter("compute/v1").
    Use(auditMiddleware).
    Route(addRoute).
    Route(subtractRoute)

for _, e := range computeRouter.Routes() {
    fmt.Printf("%-18s %v\n", e.Path, e.MiddlewareNames)
}
// compute/v1/add      [audit-log]
// compute/v1/subtract [audit-log]

err := computeRouter.Register(server)
```

See `examples/reqreply-api/demo_router_groups.go` for a runnable version.

### Differences from `api/rest`/`api/events`

| Aspect | `api/rest` | `api/events` | `api/reqreply` |
|---|---|---|---|
| Leaf type | `Route[Req,Resp]`/`SSERoute[Req,Event]` | `Subscriber[T]`/`Publisher[T]` | `Route[Req,Resp]` |
| Second axis | HTTP method | role (subscribe/publish) | **none** — a Route is already the complete leaf |
| `RouterEntry` fields | `Method`, `Path`, `MiddlewareNames` | `Role`, `Path`, `MiddlewareNames` | `Path`, `MiddlewareNames` only |
| `Group`'s scoping criterion | method-adjacent (chi precedent) | role-adjacent (naming convention) | chi's ORIGINAL baseline — any user-chosen subset sharing a prefix |
| `Register` target | `*rest.Server` | `*events.Client` | `*reqreply.Builder` (alias of `*reqreply.Server`) |
| `WithHandleCallback` | fires once | fires per role (3-way split) | fires once (same shape as REST) |
| `ClientHandle`'s `WithRouter` opt | Yes | N/A | Yes (same shape as REST) |

### `WithRouter` and multi-level `Mount` — a known limitation (shared with `api/rest`)

`WithRouter(rt)` applies ONLY `rt`'s own `prefix`/`mws` fields — it does
NOT walk `rt`'s ancestors. For a Router built via `NewRouter("compute").
Mount(v1Router)`, `WithRouter(v1Router)` would only contribute `v1Router`'s
own prefix segment, NOT `"compute"`. When a client-side handle via
`WithRouter` is needed, keep the Router FLAT — one `NewRouter("compute/v1")`
call — rather than nesting via `Mount`:

```go
// Works: a single, flat Router drives BOTH Register and WithRouter.
computeRouter := reqreply.NewRouter("compute/v1").Route(addRoute)
addRoute.ClientHandle(reqreply.WithRouter(computeRouter)) // topic: "compute/v1/add"
```
