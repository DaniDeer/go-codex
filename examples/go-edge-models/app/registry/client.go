package registry

import (
	"context"
	"net/http"

	"github.com/DaniDeer/go-codex/examples/go-edge-models/app/registry/internal"
	regmodels "github.com/DaniDeer/go-codex/examples/go-edge-models/models/docker/registry"
	"github.com/DaniDeer/go-codex/examples/go-edge-models/models/docker/registry/auth"
)

// This file holds the shared Client contract every per-registry client
// implements — app/registry/dockerhub.Client, .../ghcr.Client,
// .../mcr.Client, and this package's own NewGenericClient: the FALLBACK
// implementation for any OCI-compliant registry NOT specifically modeled
// by one of those 3 sibling packages, preserving this module's "works
// with ANY OCI-compliant registry" guarantee. All 4 are now THIN
// wrappers around ONE shared *internal.RegistryClient (the construction/
// operation mechanics) — see app/registry/internal/registryclient.go's
// own doc comment — each keeping only its OWN distinct Credentials type/
// Option (or, for genericClient, the untyped generic
// Credentials/RegistryCredentials); genericClient satisfies Client
// STRUCTURALLY, the same way dockerhub.Client/ghcr.Client/mcr.Client do,
// with no import in either direction.

// Client is implemented by every per-registry client — a concrete,
// CONSTRUCTED object bound to ONE fixed registry host. repository is a
// plain path (e.g. "library/alpine"), never a full image URL — the
// concrete Client already knows its own host.
// A per-call observer override (the ONLY thing an earlier round of this
// interface used a variadic opts parameter for) is reached via ctx
// instead (stats.WithObserver(ctx, obs) before calling) — not a method
// parameter here — specifically so dockerhub.Client/ghcr.Client/
// mcr.Client/genericClient can all satisfy this SAME interface despite
// each having its OWN, differently-named Option type (Go requires exact
// method-signature equality for interface satisfaction; a variadic
// ...dockerhub.Option parameter does not satisfy ...Option here, even
// though both are shaped func(*options)).
type Client interface {
	// GetTags lists every tag for repository on this Client's registry.
	GetTags(ctx context.Context, repository string) (regmodels.TagsList, error)
	// GetImageMetadata fetches lean manifest metadata for repository at
	// reference (a tag or digest) on this Client's registry, resolving a
	// manifest list to platform's specific manifest (platform defaults to
	// "linux/amd64" when empty).
	GetImageMetadata(ctx context.Context, repository, reference, platform string) (regmodels.ManifestMetadata, error)
}

// genericClient is [NewGenericClient]'s own unexported implementation —
// a THIN wrapper holding ONE *internal.RegistryClient (built against a
// RUNTIME host, unlike dockerhub/ghcr/mcr's own compile-time-fixed
// hostmodels.RegistryHost — genericClient was already host-parameterized
// before this consolidation, so it maps onto internal.RegistryClient even
// more directly than its 3 siblings did). baseOpts (WithCredentials/
// WithCredentialsByRegistry/WithObserver, supplied once at construction)
// are CONSTRUCTION-TIME ONLY — a persistent, HELD genericClient's
// credentials/observer are fixed for its whole lifetime; a different
// per-call WithCredentials/WithCredentialsByRegistry is a no-op (a
// different per-call WithObserver still works, threaded via ctx).
type genericClient struct {
	rc *internal.RegistryClient
}

// NewGenericClient returns a [Client] bound to host — the fallback
// implementation for ANY OCI-compliant registry not specifically modeled
// by app/registry/dockerhub/.../ghcr/.../mcr. Builds ONE
// *internal.RegistryClient (GetTagsRoute/GetManifestRoute wired AND
// explicitly registered, see internal.NewRegistryClient's own doc
// comment) and holds it. opts (WithCredentials/WithCredentialsByRegistry/
// WithObserver) become this Client's fixed identity for its whole
// lifetime.
func NewGenericClient(httpClient *http.Client, host string, opts ...Option) Client {
	creds := resolveGenericCreds(host, opts)
	rc, err := internal.NewRegistryClient(httpClient, host, creds, resolveOptions(opts).observer)
	if err != nil {
		// Only ever a genuine programmer error in THIS package's own
		// transport wiring, never a runtime/registry condition —
		// NewGenericClient has no error return (mirrors
		// Route.ClientHandle's own established "infallible construction,
		// panic on a genuine programmer error" convention), so a failure
		// here is deliberately fatal, not silently swallowed.
		panic("app/registry: NewGenericClient: " + err.Error())
	}
	return &genericClient{rc: rc}
}

// resolveGenericCreds converts opts' CONSTRUCTION-TIME generic
// Credentials/RegistryCredentials (see WithCredentials/
// WithCredentialsByRegistry) into the plain auth.Credentials shape
// internal.NewRegistryClient operates on — nil for anonymous access. A
// single WithCredentials value is the more specific override and wins
// over WithCredentialsByRegistry when both are supplied; the registry
// map is looked up by host.
func resolveGenericCreds(host string, opts []Option) *auth.Credentials {
	o := resolveOptions(opts)
	creds := o.credentials
	if creds == nil {
		if cr, ok := o.credentialsByRegistry[host]; ok {
			creds = &cr
		}
	}
	if creds == nil {
		return nil
	}
	return &auth.Credentials{Username: creds.Username, Password: creds.Password}
}

func (gc *genericClient) GetTags(ctx context.Context, repository string) (regmodels.TagsList, error) {
	return gc.rc.GetTags(ctx, repository)
}

// GetImageMetadata fetches lean manifest metadata for repository at
// reference, transparently resolving a multi-arch manifest list / OCI
// image index to platform's specific manifest (defaulting to
// "linux/amd64" when empty).
func (gc *genericClient) GetImageMetadata(ctx context.Context, repository, reference, platform string) (regmodels.ManifestMetadata, error) {
	return gc.rc.GetImageMetadata(ctx, repository, reference, platform)
}

var _ Client = (*genericClient)(nil)

// FormatPlatformSelector reconstructs an "os/arch" platform selector
// string — a thin re-export of internal.FormatPlatformSelector.
func FormatPlatformSelector(osName, arch string) (string, error) {
	return internal.FormatPlatformSelector(osName, arch)
}

// NestedManifestListError/PlatformNotFoundError/RegistryAuthChallengeError/
// RegistryAuthError are PUBLIC type aliases for app/registry/internal's
// own underlying types — see that package's own doc comment for why
// every consuming package re-exports these the same way, and
// app/registry/dockerhub's own identical aliases.
type (
	NestedManifestListError    = internal.NestedManifestListError
	PlatformNotFoundError      = internal.PlatformNotFoundError
	RegistryAuthChallengeError = internal.RegistryAuthChallengeError
	RegistryAuthError          = internal.RegistryAuthError
)
