package registry

import (
	genericmodels "github.com/DaniDeer/go-codex/examples/go-edge-models/models/docker/registry/generic"
	"github.com/DaniDeer/go-codex/stats"
)

// Option configures [NewGenericClient]'s behavior — the GENERIC fallback
// path's own options, built around models/docker/registry/generic's
// Credentials/RegistryCredentials (a plain alias of
// models/docker/registry/auth.Credentials, unlike
// app/registry/dockerhub/.../ghcr/.../mcr's own per-registry DISTINCT
// Credentials types — the generic fallback has no single known registry
// to compile-time-guard against, so it keeps the untyped-alias shape).
// The zero value (no options passed) preserves anonymous-pull behavior —
// Option is purely additive.
type Option func(*options)

type options struct {
	credentials           *genericmodels.Credentials
	credentialsByRegistry genericmodels.RegistryCredentials
	observer              stats.Observer
}

func resolveOptions(opts []Option) options {
	var o options
	for _, opt := range opts {
		opt(&o)
	}
	return o
}

// WithCredentials supplies Basic-auth credentials for the auth-token
// exchange step — needed for private repositories on registries that
// require Basic auth to mint an access token. Anonymous/public pulls
// need no Credentials at all. CONSTRUCTION-TIME ONLY for a HELD
// [NewGenericClient] — a different per-call WithCredentials on an
// already-constructed Client has no effect; construct a separate Client
// for a different identity.
func WithCredentials(creds genericmodels.Credentials) Option {
	return func(o *options) { o.credentials = &creds }
}

// WithCredentialsByRegistry supplies a full registry-host → Credentials
// map, looked up by [NewGenericClient]'s own host at construction time.
// See genericmodels.RegistryCredentials' own doc comment for the exact set
// of supported registry-host keys and the WithCredentials escape hatch
// for others. If BOTH WithCredentials and WithCredentialsByRegistry are
// supplied, WithCredentials wins (it is the more specific override).
func WithCredentialsByRegistry(creds genericmodels.RegistryCredentials) Option {
	return func(o *options) { o.credentialsByRegistry = creds }
}

// WithObserver supplies a construction-time stats.Observer for every HTTP
// call a [NewGenericClient] makes (Ping/token exchange, plus the actual
// GetTagsRoute/GetManifestRoute calls). Usually NOT needed:
// GetTags/GetImageMetadata already fall back to
// stats.ObserverFromContext(ctx) whenever no explicit Observer is set. A
// caller wanting a DIFFERENT, PER-CALL Observer (unlike WithCredentials/
// WithCredentialsByRegistry above, which are construction-time only)
// attaches it directly to ctx before calling (stats.WithObserver(ctx,
// obs)) — threaded via ctx, since a BoundClientMiddleware's Fn has no
// opts parameter slot of its own to receive it through directly.
func WithObserver(obs stats.Observer) Option {
	return func(o *options) { o.observer = obs }
}
