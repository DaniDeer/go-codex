package ghcr

import (
	"context"
	"net/http"

	"github.com/DaniDeer/go-codex/examples/go-edge-models/app/registry/internal"
	"github.com/DaniDeer/go-codex/examples/go-edge-models/models/docker"
	regmodels "github.com/DaniDeer/go-codex/examples/go-edge-models/models/docker/registry"
	"github.com/DaniDeer/go-codex/examples/go-edge-models/models/docker/registry/auth"
	hostmodels "github.com/DaniDeer/go-codex/examples/go-edge-models/models/docker/registry/ghcr"
	"github.com/DaniDeer/go-codex/stats"
)

// This file holds GHCR's registry client — a THIN wrapper around a
// single, construction-time-built *internal.RegistryClient (the shared
// MECHANICS — rest.NewClient/Authenticator/bound-routes/RegisterRoute/
// Attach wiring, plus GetTags/GetTagsFiltered/GetImageMetadata/
// ctxWithObserver — app/registry/internal/registryclient.go's own doc
// comment has the full rationale for why this is now a single shared
// implementation rather than ~150 duplicated lines per registry
// package). This Client keeps its OWN distinct Credentials type
// (hostmodels.Credentials) + Option/WithCredentials/WithObserver — the
// part that genuinely differs per registry (compile-time
// registry-identity safety: passing GHCR credentials to a Docker Hub
// Client is a compile error, not a silent misconfiguration) — and
// forwards every operation method directly to its rc field.

// Client is GHCR's concrete registry client.
type Client struct {
	rc *internal.RegistryClient
}

// Option configures New. The zero value (no options passed) preserves
// anonymous-pull behavior — Option is purely additive.
type Option func(*options)

type options struct {
	credentials *hostmodels.Credentials
	observer    stats.Observer
}

func resolveOptions(opts []Option) options {
	var o options
	for _, opt := range opts {
		opt(&o)
	}
	return o
}

// WithCredentials supplies GHCR-specific Basic-auth credentials for
// the auth-token exchange step — needed for a private repository.
// Anonymous/public pulls need no Credentials at all. CONSTRUCTION-TIME
// ONLY — this Client represents one identity for its whole lifetime; a
// caller needing a different identity constructs a SEPARATE Client.
func WithCredentials(creds hostmodels.Credentials) Option {
	return func(o *options) { o.credentials = &creds }
}

// WithObserver supplies a construction-time stats.Observer for every HTTP
// call this Client makes (Ping/token exchange, plus the actual
// GetTagsRoute/GetManifestRoute calls). Usually NOT needed: this Client
// already falls back to stats.ObserverFromContext(ctx) via the ctx a
// caller passes to GetTags/GetImageMetadata.
func WithObserver(obs stats.Observer) Option {
	return func(o *options) { o.observer = obs }
}

// New constructs a persistent, GHCR-bound Client — builds ONE
// *internal.RegistryClient (GetTagsRoute/GetManifestRoute wired AND
// explicitly registered, see internal.NewRegistryClient's own doc
// comment) and holds it. opts (WithCredentials/WithObserver) become this
// Client's fixed identity for its whole lifetime.
func New(httpClient *http.Client, opts ...Option) *Client {
	o := resolveOptions(opts)

	var creds *auth.Credentials
	if o.credentials != nil {
		creds = &auth.Credentials{Username: o.credentials.Username, Password: o.credentials.Password}
	}

	rc, err := internal.NewRegistryClient(httpClient, hostmodels.RegistryHost, creds, o.observer)
	if err != nil {
		// Only ever a genuine programmer error in this package's own
		// transport wiring, never a runtime/registry condition — New has
		// no error return (mirrors Route.ClientHandle's own established
		// "infallible construction, panic on a genuine programmer error"
		// convention), so a failure here is deliberately fatal, not
		// silently swallowed.
		panic("app/registry/ghcr: New: " + err.Error())
	}
	return &Client{rc: rc}
}

// GetTags lists every tag for repository on GHCR.
func (c *Client) GetTags(ctx context.Context, repository string) (regmodels.TagsList, error) {
	return c.rc.GetTags(ctx, repository)
}

// GetTagsFiltered calls GetTags, then sorts/limits the result's Tags via
// docker.FilterTags(list.Tags, filterOpts...) — a thin, client-side
// convenience: no new HTTP call, no new wire type.
func (c *Client) GetTagsFiltered(ctx context.Context, repository string, filterOpts []docker.FilterTagsOpt) (regmodels.TagsList, error) {
	return c.rc.GetTagsFiltered(ctx, repository, filterOpts)
}

// GetImageMetadata fetches lean manifest metadata for repository at
// reference on GHCR, transparently resolving a multi-arch manifest
// list / OCI image index to platform's specific manifest (defaulting to
// "linux/amd64" when empty).
func (c *Client) GetImageMetadata(ctx context.Context, repository, reference, platform string) (regmodels.ManifestMetadata, error) {
	return c.rc.GetImageMetadata(ctx, repository, reference, platform)
}

// FormatPlatformSelector reconstructs an "os/arch" platform selector
// string — a thin re-export of internal.FormatPlatformSelector.
func FormatPlatformSelector(osName, arch string) (string, error) {
	return internal.FormatPlatformSelector(osName, arch)
}

// NestedManifestListError/PlatformNotFoundError/RegistryAuthChallengeError/
// RegistryAuthError are PUBLIC type aliases for app/registry/internal's
// own underlying types — see that package's own doc comment for why
// every self-contained client re-exports these the same way.
type (
	NestedManifestListError    = internal.NestedManifestListError
	PlatformNotFoundError      = internal.PlatformNotFoundError
	RegistryAuthChallengeError = internal.RegistryAuthChallengeError
	RegistryAuthError          = internal.RegistryAuthError
)
