# OpenAPI/AsyncAPI Spec Endpoint — `api/rest` (+ `api/events`)

> **Status:** Exploration only — not yet designed in detail, not implemented.
> [← Back to Roadmap](index.md)

## Motivation

`rest.Server.OpenAPISpec() (openapi.Document, error)` already returns a fully
built, in-memory OpenAPI 3.1 document from a server's declared routes — but
go-codex has **no declarative way to serve that document over HTTP**. Every
example that wants to expose its spec (including `examples/rest-api`, the
most complete REST example in the repo) has to hand-roll a plain
`http.HandlerFunc`/chi handler: marshal `OpenAPISpec()` once at startup,
close over the bytes, and register it directly on the mux/router *before*
`nethttp.AttachMux`/`chiadapter.AttachRouter` wires the rest of the routes
(chi's router cannot safely receive new handlers once serving starts).

This is a real, repeated pattern (every REST example that dumps its spec
today only prints it to stdout, never serves it — `examples/rest-api` is the
first to serve it, and only via hand-rolled code) — a strong signal this
belongs as a declarative one-call convenience, consistent with go-codex's
"declare → assemble → register" philosophy for everything else in `api/rest`.

`api/events`' `AsyncAPISpec()` has the exact same gap for symmetry, though
"serving" an AsyncAPI document over HTTP is a much less common need for a
pub/sub API (there is no natural "spec endpoint" transport the way REST has
GET routes) — noted as an open question, not assumed to need the same fix.

## What exists today (confirmed via code)

- `rest.Server.OpenAPISpec() (openapi.Document, error)` — builds the document
  from all registered routes' specs. Called once, typically at the end of
  `main()`, purely for local marshal-and-print.
- No `rest.Route` variant, no `Server`-level opt-in, no adapter constructor
  anywhere in `adapters/nethttp`/`adapters/chi` serves this document over the
  wire. Confirmed via grep — zero hits for any "spec route"/"spec handler"
  concept in `api/rest`, `adapters/nethttp`, `adapters/chi`.
- `examples/rest-api`'s `chiserver/server.go` and `nethttpserver/server.go`
  hand-roll a `GET /openapi.yaml` handler each: call `Build(...)`'s
  `b.OpenAPISpec()` → `.MarshalYAML()` once at server-build time → close over
  the resulting bytes → serve them with `Content-Type: application/yaml` on
  every request, registered before `AttachRouter`/`AttachMux`. This is the
  reference pattern this roadoc doc would turn into a one-line convenience.

## Open questions (not resolved — this is Explore mode)

1. **Does this need a new `rest.Route` variant, or a simpler `Server`-level
   mechanism outside `Route` entirely?**
   A spec endpoint has no `Req` (no path/query/body to decode) and its `Resp`
   is "whatever OpenAPI document format" — that doesn't cleanly fit the
   existing `Route[Req, Resp]` two-sided codec-pair shape (there's no natural
   "codec" for a `openapi.Document` in a way that composes with
   `RequestFormats`/`Formats`). A `Server`-level opt-in
   (e.g. `Server.ServeSpec(path string, format SpecFormat) error` /
   `WithSpecRoute(...)` builder option, mirroring how `AttachMux`/
   `AttachRouter` already special-case wiring) is likely a better fit than
   forcing a `Route`-shaped abstraction onto a fundamentally different kind
   of endpoint. Needs a closer design pass before committing to either.
2. **Where does the actual HTTP registration happen?** Following the "thin
   adapter, no adapter-invented escape hatch" principle
   (`docs/concepts/ports-and-adapters.md`), this would need a real
   `ServerTransport`-compatible mechanism — most likely something
   `AttachMux`/`AttachRouter` themselves grow (an additional opt-in parameter
   or a `Server`-level flag consulted during `Attach`), rather than a
   bespoke per-adapter constructor, to keep both chi and net/http consistent
   with zero duplicated logic.
3. **Format choice** — JSON vs YAML vs both (content-negotiated via `Accept`,
   mirroring how route response bodies already negotiate)? The example's
   hand-rolled version only serves YAML; a real convenience likely wants to
   support both, matching the flexibility routes already have via
   `Formats`/`RequestFormats`.
4. **Static vs dynamic** — is the spec computed once at `Attach`/`Serve`
   time (current example's approach — correct as long as no routes are
   added after startup, which matches go-codex's overall "declare once,
   assemble, then serve" model), or should it be recomputed per-request
   (unnecessary cost, no known use case needs post-startup route mutation)?
   Leaning towards once-at-startup, matching the example.
5. **`events.Client`/`AsyncAPISpec()` symmetry** — does pub/sub actually
   want an equivalent "serve my own AsyncAPI doc over HTTP" convenience?
   AsyncAPI documents are typically consumed by tooling (codegen, doc sites)
   rather than fetched at runtime by a pub/sub client the way an OpenAPI
   document is fetched by REST tooling/clients — this may simply not be a
   real need. Flag for future research, do not assume it belongs in scope.

## Reference implementation (informal, for illustration only — NOT a committed API surface)

The example's hand-rolled version (see `examples/rest-api/chiserver/server.go`
and `examples/rest-api/nethttpserver/server.go`) is the shape a future
convenience would formalize:

```go
// Illustrative only — not a proposed final signature.
spec, err := b.OpenAPISpec()
yamlBytes, err := spec.MarshalYAML()
mux.HandleFunc("GET /openapi.yaml", func(w http.ResponseWriter, r *http.Request) {
    w.Header().Set("Content-Type", "application/yaml")
    w.Write(yamlBytes)
})
```

A one-call convenience might look something like:

```go
// Sketch only — exact signature TBD in a future Refine/Implement pass.
err := chiadapter.AttachRouter(b, router, addr,
    nethttp.WithSpecRoute("/openapi.yaml", nethttp.SpecFormatYAML))
```

## Next steps

This doc captures the idea and open questions only. A future session should:
1. Research whether other libraries/frameworks (e.g. how `swaggo`, `go-chi/render`
   deal with self-serving specs) have prior art worth following.
2. Resolve Open Question 1 (Route variant vs. Server-level mechanism) before
   writing any API surface.
3. Decide events' scope (Open Question 5) with a concrete use case, not
   speculatively.
4. Move to a full API-surface design (Refine mode) once these are resolved.
