package nethttp

import (
	"net/http"
)

// Consumer is a client-side convenience holder for SSE consumption,
// removing repeated client/baseURL boilerplate across many [Consume]
// calls to the same API — the STRICT SSE-consumption EQUIVALENT of the
// package's internal caller type (caller is for one-shot request/response
// via the internal call helper, reachable publicly via [CallWithHandle];
// Consumer is for long-lived event streams via [Consume]). Field-for-
// field, method-for-method identical shape to caller: no defaultMws, no
// per-Consumer credential slot — client-side credential fulfillment is
// declared PER-ROUTE via [rest.SSERoute.ClientMW], mirroring how Caller
// defers entirely to [rest.Route.ClientMW].
//
// Consumer is a DISTINCT type from Caller (not a reuse), even though both
// are structurally identical today: they name the SPECIFIC role each
// session object plays (a "caller" makes request/response calls; a
// "consumer" consumes an event stream) — mirroring Route/SSERoute
// themselves already being distinct types for the same reason.
type Consumer struct {
	client  *http.Client
	baseURL string
}

// NewConsumer builds a [Consumer] bound to client and baseURL. Mirrors
// the package's internal newCaller constructor exactly.
func NewConsumer(client *http.Client, baseURL string) *Consumer {
	return &Consumer{client: client, baseURL: baseURL}
}

// WithBaseURL returns a NEW [Consumer] sharing c's *http.Client but bound
// to a different baseURL. Mirrors the internal caller type's WithBaseURL
// exactly — same non-mutating-copy semantics, same "ergonomic sugar, not
// a structural requirement" rationale (a fresh Consumer is always cheap
// to construct directly via [NewConsumer]).
func (c *Consumer) WithBaseURL(baseURL string) *Consumer {
	return &Consumer{client: c.client, baseURL: baseURL}
}
