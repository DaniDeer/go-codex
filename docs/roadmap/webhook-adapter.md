# Webhook Adapter — `adapters/webhook`

> **Status:** Design complete — not yet implemented. Phase 0 prerequisite
> identified: a small, additive `nethttp.IngestAdapterOptions.Middleware`
> field must ship first (see "Design update (2026)" below) — the original
> "zero-core-change" premise held for `chi`/`route`/`render/*` but not for
> `adapters/nethttp` after `Handler` was removed.
> [← Back to Roadmap](index.md)

## Motivation

Webhooks are the dominant integration pattern between SaaS providers (GitHub,
Stripe, Shopify, Slack…) and consumer services: the provider POSTs an event
payload to a URL the consumer registers, and — less commonly but still
real — a consumer's own service pushes signed notifications out to its own
subscribers' URLs. go-codex already has the transport-level building blocks
for both directions (`nethttp.IngestAdapter` for inbound HTTP-push-to-pipeline,
`nethttp.DrainCallAdapter` for outbound fire-and-forget HTTP POST, `api/rest`
for OpenAPI, `api/events` for AsyncAPI), but the one mechanism that makes an
HTTP endpoint a *webhook* rather than a generic REST route — **HMAC-SHA256
signature verification/signing over the raw, undecoded payload bytes** — is
not currently possible: both the server-side declarative security mechanism
(a route's `.Use(mw)`-declared requirement paired with a
`.HandleMW(mw, fn)`-attached `middleware.ServerImplementation`) and the
client-side credential mechanism (`.ClientMW(mw, fn)`-attached
`middleware.ClientImplementation`) run against the already-DECODED `Req`
value — by the time either runs, the body has already been read and decoded
(and, server-side, `r.Body` is drained by then with no fresh reader
substituted), so neither hook can access the exact bytes a sender/receiver
signed. `adapters/webhook` closes that one real gap while reusing everything
else it can — see "Design update (2026)" below for what changed on the
inbound side since this doc was first written (a small, additive
`nethttp.IngestAdapter` change is now a Phase 0 prerequisite; the outbound
side and everything else remains a zero-core-change addition).

## Scope decisions (what's in Phase 1, what's deferred)

| In scope | Out of scope |
|---|---|
| Inbound: `ReceiveAdapter[T]` → `ports.SourceAdapter[T]` — HTTP receiver with HMAC signature verification before decode | Non-HMAC signature schemes (RSA/ECDSA payload signing — e.g. some providers sign with asymmetric keys); revisit if a real provider needs it |
| Outbound: `DeliverAdapter[T]` → `ports.SinkAdapter[T]` — signed HTTP POST per item, with retry + exponential backoff | Fan-out to multiple subscriber URLs per item (Phase 2 candidate — needs a subscription registry, a different feature) |
| `VerifySignature`/`SignPayload` — standalone, provider-agnostic HMAC-SHA256 helpers (constant-time compare) | Provider-specific quirks (GitHub's `sha256=` prefix, Stripe's `t=,v1=` multi-part header, Slack's version prefix) as first-class API — Phase 1 ships the raw primitive + documents how to wrap it per provider; a provider-specific convenience layer is a Phase 2 candidate once ≥1 real integration exists |
| Optional replay protection: timestamp header + tolerance window, verified alongside the signature | Persistent replay-nonce tracking (store-backed dedup) — stateless timestamp-window check only |
| OpenAPI generation — reuse `RESTPattern`/`rest.NewRoute` unchanged (works today) | OpenAPI 3.1 top-level `webhooks:` field (describes provider→consumer operations from the *provider's* spec) — `render/openapi` doesn't support it yet; Phase 1 documents the inbound receiver as a normal `paths:` operation (accurate from the *receiver's* point of view, which is what this adapter implements) |
| AsyncAPI generation — reuse `EventPattern`/`events.NewChannel` for a channel description of the payload as a message (protocol `"https"`/`"webhook"` on the server) — dual REST+AsyncAPI description on the same payload codec is optional per route, not mandatory | New AsyncAPI HTTP-callback binding objects — plain `Server{Protocol: "https"}` is descriptive enough for Phase 1 |
| Narrow interface: none needed — `net/http.Client`/`*http.ServeMux` used directly (stdlib-only, mirrors `nethttp`) | — |

## Design update (2026): the original "zero-core-change" premise no longer holds for the inbound side

> **This section supersedes an earlier version of this doc that assumed
> `nethttp.Handler(handle, fn, opts)` — a standalone function turning a
> `*rest.RouteHandle` + handler fn + `Options` directly into an
> `http.Handler` — was still available for `ReceiveAdapter` to build on
> "exactly like `nethttp.IngestAdapter` does internally." That function
> was REMOVED during
> [Middleware Workflow Simplification](../design/middleware-workflow-simplification.md)'s
> old-door deletion phase, and `IngestAdapter` itself now calls an
> UNEXPORTED replacement (`handlerFunc`) — inaccessible from an external
> package like `adapters/webhook`. The **outbound** side is unaffected
> (see below); only the **inbound** side needs a real design update,
> spelled out here.

**Inbound** — a body-preserving verification middleware still wraps the
final `http.Handler`, unchanged in shape:

```go
func verifyMiddleware(secret []byte, headerName string, tolerance time.Duration, next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        body, err := io.ReadAll(r.Body)
        // ... verify HMAC over body against r.Header.Get(headerName) ...
        r.Body = io.NopCloser(bytes.NewReader(body)) // restore for downstream decode
        next.ServeHTTP(w, r)
    })
}
```

But `ReceiveAdapter` can no longer build the wrapped handler itself the way
the old design assumed — there is today NO exported function that turns a
bare `*rest.RouteHandle[T, struct{}]` into an `http.Handler` (the whole
decode/validate/security/dispatch pipeline lives behind `Serve`, which
consumes a `*rest.Server`'s *already-registered* routes — a bare handle
can't be re-added to a fresh `Builder`, since registration needs the
original `rest.Route` value, not a handle). **This makes a small, additive
change to `nethttp.IngestAdapter` a genuine Phase 0 PREREQUISITE for this
roadmap** — not a "zero-core-change" nice-to-have, and not new-`nethttp`-
API scope creep: it is the SAME "inject middleware into `IngestAdapter`"
hook this doc's own "Open design decision #1" already flagged as worth
revisiting "if a second use case wants that." `ReceiveAdapter` genuinely IS
that second use case, so the decision is resolved (see below), not merely
revisited:

```go
// adapters/nethttp/binding.go — additive field, existing behavior when nil
type IngestAdapterOptions struct {
    Options    Options
    Buffer     int
    // Middleware, when non-nil, wraps the internally-built http.Handler
    // BEFORE it is registered on mux — e.g. a body-preserving signature
    // check that must run before decode. Nil preserves today's behavior
    // exactly (registers the handler directly, no wrapping).
    Middleware func(http.Handler) http.Handler
}
```

With that one additive field, `webhook.ReceiveAdapter` becomes pure
COMPOSITION over `nethttp.IngestAdapter` (no duplicated forwarding-channel/
`PipelineFullError`/`Activate` logic in `adapters/webhook` at all):

```go
func ReceiveAdapter[T any](mux *http.ServeMux, handle *rest.RouteHandle[T, struct{}], opts ReceiveAdapterOptions) ports.SourceAdapter[T] {
    return nethttp.IngestAdapter(mux, handle, nethttp.IngestAdapterOptions{
        Options:    opts.Options,
        Buffer:     opts.Buffer,
        Middleware: verifyMiddleware(opts.Secret, opts.SignatureHeader, opts.Tolerance, opts /* ... */),
    })
}
```

**Outbound is unaffected** — `handle.EncodeRequest(item)` (already exported
on `rest.RouteHandle`) gives the exact marshaled bytes *before* the HTTP
call is made. `DeliverAdapter` calls it once to compute the signature, adds
the signature (and optionally a timestamp) header via
`nethttp.CallOptions.ExtraHeaders`, then calls **`nethttp.CallWithHandle`**
directly — the handle-based primitive `nethttp.Call` wraps internally,
still fully exported (unlike `Handler`) specifically for callers holding a
handle but no `rest.Route` value (the same primitive `ports`' own binding
adapters and `adapters/mcprest` use) — full reuse of path/query/cookie/
header validation, any handle-declared credential-providing
`middleware.ClientImplementation`, observer, and tracing. Retry-with-backoff
wraps the `CallWithHandle` invocation, mirroring `adapters/websocket`'s
reconnect-backoff loop (`initialBackoff`, doubling, capped at `MaxBackoff`)
rather than introducing a new stdlib dependency.

**Signature representation in specs**: the signature header is just
documented as an `apiKey`-in-header `route.SecurityScheme` (e.g.
`route.APIKeyScheme("X-Webhook-Signature", "header")`) — this already renders
correctly in both OpenAPI (`components.securitySchemes`) and AsyncAPI
(`events.SecurityScheme`) today. No new `route.SecuritySchemeType` is added;
the actual HMAC verification is an adapter-layer runtime check, same
separation as `rest`'s existing scheme-declares-shape /
`SecurityFunc`-enforces-it split.

## API surface

```go
package webhook // adapters/webhook

// ── Signature primitives (provider-agnostic) ─────────────────────────────

// SignPayload returns the hex-encoded HMAC-SHA256 of body using secret.
// Callers wrap this per-provider convention (e.g. prefix with "sha256=").
func SignPayload(secret, body []byte) string

// VerifySignature reports whether sig (as produced by SignPayload, or by
// the provider using the same algorithm) matches the HMAC-SHA256 of body
// under secret. Uses hmac.Equal (constant-time) internally.
func VerifySignature(secret, body []byte, sig string) bool

// ── Inbound: ReceiveAdapter ───────────────────────────────────────────────

// ReceiveAdapterOptions configures [ReceiveAdapter].
type ReceiveAdapterOptions struct {
    // Options is passed through to the underlying nethttp.IngestAdapter.
    Options nethttp.Options
    // SignatureHeader is the header carrying the sender's signature
    // (e.g. "X-Hub-Signature-256"). Required.
    SignatureHeader string
    // Secret is the shared HMAC secret. Required, non-empty.
    Secret []byte
    // VerifyFunc overrides the default VerifySignature-based check — use
    // for provider-specific formats (GitHub's "sha256=" prefix, Stripe's
    // "t=...,v1=..." multi-part header). Nil uses VerifySignature directly
    // against the raw header value.
    VerifyFunc func(secret, body []byte, headerValue string) bool
    // TimestampHeader, when non-empty, additionally requires a timestamp
    // header (unix seconds) within Tolerance of time.Now() — stateless
    // replay-window protection (no nonce store).
    TimestampHeader string
    Tolerance       time.Duration // default 5 minutes when TimestampHeader is set
    Buffer          int
}

// ReceiveAdapter returns a [ports.SourceAdapter] that verifies the inbound
// request's HMAC signature over the RAW body BEFORE handing off to the
// standard nethttp.IngestAdapter decode/validate/dispatch pipeline. A signature
// (or timestamp-tolerance) failure never reaches the item codec — it is
// rejected 401 with [SignatureError], reported via
// [stats.SecurityObserver.RecordSecurityRejection] with scheme
// "webhook-hmac", and never forwarded to the pipeline. Use with
// [ports.SourcePort.Bind]:
//
//	domain.InboundEvents.Bind(ctx, webhook.ReceiveAdapter(
//	    mux, ingestHandle, webhook.ReceiveAdapterOptions{
//	        SignatureHeader: "X-Hub-Signature-256",
//	        Secret:          secret,
//	    }))
func ReceiveAdapter[T any](
    mux *http.ServeMux,
    handle *rest.RouteHandle[T, struct{}],
    opts ReceiveAdapterOptions,
) ports.SourceAdapter[T]

// ── Outbound: DeliverAdapter ──────────────────────────────────────────────

// DeliverAdapterOptions configures [DeliverAdapter].
type DeliverAdapterOptions struct {
    Vars     map[string]string
    CallOpts nethttp.CallOptions
    // SignatureHeader is the header the signature is sent under.
    SignatureHeader string
    // Secret is the shared HMAC secret used to sign the outgoing payload.
    Secret []byte
    // SignFunc overrides the default SignPayload-based signing — use to
    // match a provider's expected header format (e.g. prefix "sha256=").
    // Nil uses SignPayload's hex output directly as the header value.
    SignFunc func(secret, body []byte) string
    // MaxRetries caps delivery attempts per item (0 = no retry, one attempt).
    MaxRetries int
    // MaxBackoff caps the exponential retry backoff. Default 30s, mirrors
    // adapters/websocket's reconnect backoff (250ms initial, doubling).
    MaxBackoff time.Duration
    OnError    func(error) // called after MaxRetries exhausted
}

// DeliverAdapter returns a [ports.SinkAdapter] that signs and POSTs each
// item to baseURL, retrying transient failures (5xx, network errors) with
// exponential backoff up to MaxRetries. 4xx responses are NOT retried
// (treated as a permanent rejection — matches Stripe/GitHub delivery
// semantics). Use with [ports.SinkPort.Bind]:
//
//	domain.OutboundEvents.Bind(ctx, webhook.DeliverAdapter(
//	    client, "https://subscriber.example.com/hooks", deliverHandle,
//	    webhook.DeliverAdapterOptions{
//	        SignatureHeader: "X-Webhook-Signature",
//	        Secret:          secret,
//	        MaxRetries:      5,
//	    }))
func DeliverAdapter[T any](
    client *http.Client,
    baseURL string,
    handle *rest.RouteHandle[T, struct{}],
    opts DeliverAdapterOptions,
) ports.SinkAdapter[T]
```

Declaration reuses `RESTPattern` (OpenAPI) unchanged; an `EventPattern` may
additionally be declared on the same port for AsyncAPI description of the
payload as a message — both are optional, neither requires new `ports.Pattern`
plumbing:

```go
var InboundEvents = codex.Must(ports.NewSourcePort[Event]("inboundEvents", eventCodec,
    ports.PortOptions{
        RESTBuilder: restBuilder,
        Patterns: []ports.Pattern{
            ports.RESTPattern{Method: "POST", Path: "/hooks/provider"},
        },
    }))
```

## Structured errors (all implement `slog.LogValuer`)

```go
// SignatureError is returned when an inbound signature/timestamp check fails.
type SignatureError struct {
    Path   string // route path
    Header string // the header that failed verification
    Reason string // "mismatch", "missing_header", "stale_timestamp", "invalid_timestamp"
}

func (e SignatureError) Error() string {
    return fmt.Sprintf("webhook: signature check failed at %s (%s): %s", e.Path, e.Header, e.Reason)
}
// no Err field — SignatureError has no wrapped cause, so no Unwrap()

func (e SignatureError) LogValue() slog.Value {
    return slog.GroupValue(
        slog.String("path", e.Path),
        slog.String("header", e.Header),
        slog.String("reason", e.Reason),
    )
}

// DeliveryError wraps a failed outbound delivery after retries are exhausted.
type DeliveryError struct {
    URL     string
    Attempt int
    Err     error
}

func (e DeliveryError) Error() string {
    return fmt.Sprintf("webhook: delivery to %s failed after %d attempts: %v", e.URL, e.Attempt, e.Err)
}
func (e DeliveryError) Unwrap() error { return e.Err }
func (e DeliveryError) LogValue() slog.Value {
    return slog.GroupValue(
        slog.String("url", e.URL),
        slog.Int("attempt", e.Attempt),
        slog.Any("err", e.Err),
    )
}
```

## Observer integration

No new `stats.Observer` extension — reuses existing hooks, same rationale as
the Redis pub/sub roadmap ("the whole point of transport-agnostic hooks"):

- `stats.Observer.RecordRequest` fires for every inbound attempt (via the
  wrapped `nethttp.IngestAdapter`, unchanged) — status 401 on signature rejection.
- `stats.SecurityObserver.RecordSecurityRejection(path, "webhook-hmac")` —
  type-asserted, fires on signature AND timestamp-tolerance failures.
- `stats.Observer.RecordRequest` (client-side, via `nethttp.CallWithHandle`,
  reused as-is) fires per outbound attempt, including retries — each retry
  is a distinct `RecordRequest` call, so retry count is directly observable.
- Nil observer → `stats.ObserverFromContext(ctx)` (both directions have a
  `ctx` at the point the observer is resolved — inbound via the request
  context, outbound via the `Activate(ctx, ...)` parameter).

## Unit test plan

| ID | Test | Verifies |
|---|---|---|
| S1 | `VerifySignature` happy path | valid HMAC → true |
| S2 | `VerifySignature` tampered body | mismatched HMAC → false |
| S3 | `SignPayload` / `VerifySignature` round-trip | sign then verify succeeds |
| R1 | `ReceiveAdapter` valid signature → decode → port stream | happy path, body available to decode after middleware |
| R2 | `ReceiveAdapter` invalid signature → 401 | `SignatureError{Reason:"mismatch"}`, item never forwarded |
| R3 | `ReceiveAdapter` missing signature header → 401 | `SignatureError{Reason:"missing_header"}` |
| R4 | `ReceiveAdapter` stale timestamp → 401 | `SignatureError{Reason:"stale_timestamp"}` (when `TimestampHeader` set) |
| R5 | `ReceiveAdapter` custom `VerifyFunc` | provider-specific format (e.g. `"sha256=" + hex`) honored |
| R6 | `ReceiveAdapter` observer | `RecordSecurityRejection` called with scheme `"webhook-hmac"` on R2–R4; `RecordRequest` called on every path incl. success |
| R7 | `SignatureError.LogValue` | `slog.KindGroup` + keys `path`/`header`/`reason` |
| D1 | `DeliverAdapter` happy path | signs via `EncodeRequest`, header set, POST succeeds |
| D2 | `DeliverAdapter` 5xx → retry → success | backoff observed (fake clock or short durations), eventual success, no `OnError` |
| D3 | `DeliverAdapter` 4xx → no retry | single attempt, `OnError` called immediately with `DeliveryError{Attempt:1}` |
| D4 | `DeliverAdapter` retries exhausted | `MaxRetries` attempts made, `OnError` called with final `DeliveryError` |
| D5 | `DeliveryError.LogValue` | `slog.KindGroup` + keys `url`/`attempt`/`err`; `errors.As` reaches inner error |
| D6 | `DeliverAdapter` custom `SignFunc` | provider-specific header format honored |
| D7 | Nil observer (both directions) → no panic | graceful `NoopObserver` fallback |
| D8 | Plain `stats.Observer` (no `SecurityObserver`) → no panic | type-assertion guard fallback |
| — | `ExampleReceiveAdapter` / `ExampleDeliverAdapter` | deterministic, `httptest.Server`-backed |

## Files to create

| File | Responsibility |
|---|---|
| `adapters/nethttp/binding.go` (Phase 0, EDIT not new) | add `IngestAdapterOptions.Middleware func(http.Handler) http.Handler`, apply it (when non-nil) to the handler before `mux.Handle` — nil-safe, zero behavior change for existing callers |
| `adapters/webhook/doc.go` | package overview: inbound composes over `nethttp.IngestAdapter`'s Middleware hook, outbound reuses `nethttp.CallWithHandle`, both directions, spec story |
| `adapters/webhook/signature.go` | `SignPayload`, `VerifySignature` |
| `adapters/webhook/binding.go` | `ReceiveAdapter`, `DeliverAdapter` + options structs, internal `verifyMiddleware` |
| `adapters/webhook/errors.go` | `SignatureError`, `DeliveryError` |
| `adapters/webhook/*_test.go` | S1–S3, R1–R7, D1–D8 + Examples (`httptest.Server`-backed, no live provider) |
| `ports/source_port.go` / `sink_port.go` | add `webhook.ReceiveAdapter` / `webhook.DeliverAdapter` to "Implemented by" godoc lists |
| `docs/features/webhook.md` | new feature page: both directions, signature verification/signing, spec generation |
| `docs/guides/webhook.md` | step-by-step: register a provider webhook, verify + decode; sign + deliver to a subscriber |
| `docs/reference/project-structure.md` | new `adapters/webhook/` entry |
| `.github/instructions/go-codex.instructions.md` | new adapter row + `ports` "Implemented by" list updates |
| `examples/webhook-receive/main.go` and/or `examples/webhook-deliver/main.go` | runnable demo(s), `httptest.Server`-backed |
| review-skill history + known-facts | usual three-surface + R-history sync |

## Out of scope (Phase 2)

- **Fan-out delivery** (one event → N subscriber URLs, with a subscription
  registry) — needs its own design (storage for subscriber list, per-subscriber
  secret, delivery-status tracking); revisit once a concrete multi-subscriber
  use case exists.
- **Provider-specific convenience wrappers** (`webhook.GitHubVerify`,
  `webhook.StripeVerify`, …) — ship the raw primitive first; add named
  wrappers once ≥1 real integration's exact header format is confirmed.
- **OpenAPI 3.1 `webhooks:` top-level field** — needs a `render/openapi`
  enhancement (new document section, separate from `paths:`); the inbound
  receiver is already fully described via `paths:` today, so this is a
  documentation-quality improvement, not a functional gap.
- **Persistent replay-nonce store** — Phase 1's timestamp-window check is
  stateless (no dedup store); a durable nonce cache is a distinct feature
  (would likely lean on `ports.Cache`/`adapters/redis`) — revisit if a
  provider's threat model requires exact-once delivery guarantees.
- **Delivery status webhooks / dead-letter queue** — what happens to an item
  after `MaxRetries` is exhausted beyond calling `OnError` (e.g. persisting to
  a dead-letter file/table) is left to the caller's `OnError` today.

## Open design decisions (to resolve before/during implementation)

1. **RESOLVED — `ReceiveAdapter` reuse vs. duplication of `IngestAdapter`'s
   forwarding plumbing.** No longer optional: since `nethttp.Handler` was
   removed (see "Design update (2026)" above), duplicating `IngestAdapter`'s
   buffered-channel/`PipelineFullError` logic in `adapters/webhook` would
   mean re-implementing already-unexported internals from scratch, with no
   guarantee of staying in sync. `webhook.ReceiveAdapter` genuinely IS the
   "second middleware-wrapping use case" this decision originally deferred
   on — so Phase 0 of this roadmap now includes a small, additive
   `nethttp.IngestAdapterOptions.Middleware func(http.Handler) http.Handler`
   field (nil-safe, zero behavior change for existing callers), and
   `ReceiveAdapter` composes over `nethttp.IngestAdapter` directly instead of
   duplicating its logic.
2. **Default `VerifyFunc`/`SignFunc` header format** — Phase 1's default
   (`VerifySignature` against the raw header value, `SignPayload`'s bare hex
   output) doesn't match any specific provider's convention out of the box
   (GitHub prefixes `sha256=`, Stripe uses a multi-part `t=...,v1=...` format).
   Callers supply `VerifyFunc`/`SignFunc` for their provider. Leaning: keep
   the bare-primitive default — don't guess a "most common" provider format
   with no real integration to validate against.
3. **Retry policy shape** — `MaxRetries int` + fixed exponential backoff
   (mirrors `adapters/websocket`) is the simplest option. An alternative is a
   pluggable `RetryPolicy func(attempt int, err error) (retry bool, wait time.Duration)`
   for callers who want jittered backoff or per-status-code policies. Leaning:
   ship the simple fixed policy in Phase 1; revisit if a real caller needs
   custom policy shape.
4. **Should `ReceiveAdapter` support multiple valid secrets (key rotation)?**
   Real providers (e.g. GitHub) support verifying against N configured
   secrets during a rotation window. `Secret []byte` (singular) is simplest
   for Phase 1; `Secrets [][]byte` (try each, first match wins) is a
   low-risk Phase 1 addition if a rotation use case is anticipated up front —
   otherwise defer to Phase 2 when a real rotation need appears.
5. **AsyncAPI dual-description default** — should `ReceiveAdapter`/
   `DeliverAdapter` require an explicit `EventPattern` opt-in for AsyncAPI
   (current lean, matches "REST is the primary shape, AsyncAPI is
   supplementary"), or should the roadmap instead recommend `RESTPattern`
   only and treat AsyncAPI webhook description as purely an
   `api/events`-level user choice outside the adapter's concern entirely?
