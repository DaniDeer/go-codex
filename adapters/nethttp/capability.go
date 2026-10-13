package nethttp

import "github.com/DaniDeer/go-codex/adapters/internal/restdispatch"

// httpCarrier/httpTransport are PROMOTED (Phase 4, REST family
// dispatch-core consolidation — see
// docs/roadmap/adapter-integration-architecture.md) to
// [restdispatch.HTTPCarrier]/[restdispatch.HTTPTransport] — aliased here
// (not copied) so every existing call site in this package (including
// adapters/nethttp/serve_sse.go, deliberately untouched by this
// consolidation pass) keeps compiling and behaving identically. Call
// sites use the keyed literal `httpCarrier{R: r}` (not positional) since
// `go vet` flags unkeyed literals for a struct now defined in a
// different package.
type httpCarrier = restdispatch.HTTPCarrier

var httpTransport = restdispatch.HTTPTransport
