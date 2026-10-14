package chi

import "github.com/DaniDeer/go-codex/adapters/internal/restdispatch"

// httpCarrier is PROMOTED (Phase 4, REST family dispatch-core
// consolidation — see docs/roadmap/adapter-integration-architecture.md)
// to [restdispatch.HTTPCarrier] — aliased here (not copied), mirroring
// adapters/nethttp's identical promotion, so every existing call site in
// this package (e.g. adapter.go's sseHandlerFunc) keeps compiling and
// behaving identically. Call sites use the keyed literal
// `httpCarrier{R: r}` (not positional) since `go vet` flags unkeyed
// literals for a struct now defined in a different package.
//
// httpTransport was ALSO aliased here through the REST family's steps
// 1-6, then became genuinely dead once buildRouteHandler/
// buildSSERouteHandler were rewritten to call
// restdispatch.ValidateRouteConstruction directly — deleted, caught by
// `just check`'s staticcheck pass (U1000), mirroring adapters/nethttp's
// identical cleanup.
type httpCarrier = restdispatch.HTTPCarrier
