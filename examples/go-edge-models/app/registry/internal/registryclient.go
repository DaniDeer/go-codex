package internal

import (
	"context"
	"net/http"

	nethttp "github.com/DaniDeer/go-codex/adapters/nethttp"
	"github.com/DaniDeer/go-codex/api/rest"
	"github.com/DaniDeer/go-codex/examples/go-edge-models/models/docker"
	regmodels "github.com/DaniDeer/go-codex/examples/go-edge-models/models/docker/registry"
	"github.com/DaniDeer/go-codex/examples/go-edge-models/models/docker/registry/auth"
	"github.com/DaniDeer/go-codex/examples/go-edge-models/models/util"
	"github.com/DaniDeer/go-codex/stats"
)

// This file holds RegistryClient — the SHARED construction/wiring AND
// operation logic every self-contained per-registry client
// (app/registry/dockerhub.Client, .../ghcr.Client, .../mcr.Client) and
// app/registry's own genericClient are now THIN wrappers around. Earlier
// rounds shared only the low-level auth/manifest-resolution HELPERS
// (Authenticator, BearerAuthClientMW, PlatformMatches,
// FormatPlatformSelector) while each of the 4 outer Clients duplicated
// ~150 lines of IDENTICAL construction (rest.NewClient/Authenticator/
// bound routes/RegisterRoute/Attach) and operation logic (GetTags/
// GetTagsFiltered/GetImageMetadata/ctxWithObserver) verbatim — confirmed
// via `diff`, the dockerhub/ghcr/mcr versions were byte-identical except
// for 3 panic-message string prefixes, and genericClient's own version
// differed only by receiver name and host (a runtime field there,
// vs. a compile-time package constant in the other 3). RegistryClient
// is the genuine extraction of that duplicated ~150 lines into ONE
// place — every outer Client keeps its OWN distinct Credentials type +
// Option/WithCredentials (preserving the deliberate compile-time
// registry-identity safety that was always the real reason for 4
// separate outer types), but delegates every method body here.
type RegistryClient struct {
	client        *rest.Client
	auth          *Authenticator
	tagsRoute     rest.Route[regmodels.GetTagsReq, regmodels.TagsList]
	manifestRoute rest.Route[regmodels.GetManifestReq, util.ManifestEnvelope]
	host          string
	observer      stats.Observer
}

// NewRegistryClient constructs a persistent, host-bound RegistryClient:
// a real *rest.Client, its own *Authenticator, GetTagsRoute/
// GetManifestRoute bound to BearerAuthClientMW, both EXPLICITLY
// registered (RegisterRoute — not strictly required, Client.Call
// auto-registers a route the first time it's called, but mirrors
// events.Client's own explicit registration workflow for consistency;
// see docs/features/http-client.md's "Auto-registration vs. explicit
// Client.RegisterRoute"), and the transport attached. creds is nil for
// anonymous access; observer is the caller's construction-time
// stats.Observer (nil is fine — GetTags/GetImageMetadata fall back to
// stats.ObserverFromContext(ctx) via ctxWithObserver).
//
// Returns an error instead of panicking — unlike this package's own
// NewAuthenticator (whose failure modes are all hardcoded, genuinely
// unreachable programmer errors), a RegistryClient construction failure
// should still be fatal, but EACH caller (dockerhub.New/ghcr.New/mcr.New/
// NewGenericClient) panics with its OWN distinctly-prefixed message
// ("app/registry/dockerhub: New: ...", etc.) — preserving the exact
// panic text each package already surfaced before this extraction.
func NewRegistryClient(httpClient *http.Client, host string, creds *auth.Credentials, observer stats.Observer) (*RegistryClient, error) {
	rc := &RegistryClient{host: host, observer: observer}
	rc.client = rest.NewClient()
	rc.auth = NewAuthenticator(rc.client, httpClient, host)

	rc.tagsRoute = regmodels.GetTagsRoute.ClientBoundMW(
		BearerAuthClientMW(rc.auth, creds, func(r regmodels.GetTagsReq) string { return r.Name }),
	)
	rc.manifestRoute = regmodels.GetManifestRoute.ClientBoundMW(
		BearerAuthClientMW(rc.auth, creds, func(r regmodels.GetManifestReq) string { return r.Name }),
	)
	if err := rc.client.RegisterRoute(rc.tagsRoute); err != nil {
		return nil, err
	}
	if err := rc.client.RegisterRoute(rc.manifestRoute); err != nil {
		return nil, err
	}
	if err := rc.client.Attach(nethttp.NewClientTransport(nethttp.ClientTransportOptions{
		HTTPClient: httpClient, BaseURL: RegistryBaseURL(host),
	})); err != nil {
		return nil, err
	}
	return rc, nil
}

// ctxWithObserver wraps ctx via stats.WithObserver with rc.observer
// (construction-time) ONLY when ctx doesn't already carry an explicit
// Observer (detected via stats.ObserverFromContext returning the
// zero-value stats.NoopObserver{} — the same "nothing set yet" signal
// that fallback itself uses) — a caller wanting a DIFFERENT Observer for
// one specific call attaches it directly to ctx BEFORE calling
// (stats.WithObserver(ctx, obs)), and that explicit per-call value then
// correctly wins over rc's own construction-time default.
func (rc *RegistryClient) ctxWithObserver(ctx context.Context) context.Context {
	if _, explicit := stats.ObserverFromContext(ctx).(stats.NoopObserver); explicit && rc.observer != nil {
		ctx = stats.WithObserver(ctx, rc.observer)
	}
	return ctx
}

// GetTags lists every tag for repository on rc's registry host.
func (rc *RegistryClient) GetTags(ctx context.Context, repository string) (regmodels.TagsList, error) {
	ctx = rc.ctxWithObserver(ctx)
	resp, err := rc.client.Call(ctx, rc.tagsRoute, regmodels.GetTagsReq{Name: repository})
	if err != nil {
		return regmodels.TagsList{}, err
	}
	return resp, nil
}

// GetTagsFiltered calls GetTags, then sorts/limits the result's Tags via
// docker.FilterTags(list.Tags, filterOpts...) — a thin, client-side
// convenience: no new HTTP call, no new wire type.
func (rc *RegistryClient) GetTagsFiltered(ctx context.Context, repository string, filterOpts []docker.FilterTagsOpt) (regmodels.TagsList, error) {
	list, err := rc.GetTags(ctx, repository)
	if err != nil {
		return regmodels.TagsList{}, err
	}
	list.Tags = docker.FilterTags(list.Tags, filterOpts...)
	return list, nil
}

// GetImageMetadata fetches lean manifest metadata for repository at
// reference on rc's registry host, transparently resolving a multi-arch
// manifest list / OCI image index to platform's specific manifest
// (defaulting to "linux/amd64" when empty). The 2 rc.client.Call calls
// below (list resolution + platform-specific fetch) share the SAME
// repository, so the Authenticator's token cache is hit on the 2nd.
func (rc *RegistryClient) GetImageMetadata(ctx context.Context, repository, reference, platform string) (regmodels.ManifestMetadata, error) {
	ctx = rc.ctxWithObserver(ctx)

	platformEither, err := util.DefaultPlatformCodec.Decode(platform)
	if err != nil {
		return regmodels.ManifestMetadata{}, err
	}
	selector := util.DefaultPlatformSelector
	platformStr := util.DefaultPlatform
	if platformEither.IsRight() {
		selector = *platformEither.Right
		platformStr = platform
	}

	env, err := rc.client.Call(ctx, rc.manifestRoute, regmodels.GetManifestReq{Name: repository, Reference: reference})
	if err != nil {
		return regmodels.ManifestMetadata{}, err
	}

	if env.List != nil {
		available := make([]string, 0, len(env.List.Manifests))
		var resolvedDigest string
		for _, m := range env.List.Manifests {
			if m.Platform != nil {
				if s, err := FormatPlatformSelector(m.Platform.OS, m.Platform.Architecture); err == nil {
					available = append(available, s)
				}
			}
			if PlatformMatches(m, selector) {
				resolvedDigest = m.Digest
				break
			}
		}
		if resolvedDigest == "" {
			return regmodels.ManifestMetadata{}, PlatformNotFoundError{Platform: platformStr, Available: available}
		}

		env, err = rc.client.Call(ctx, rc.manifestRoute, regmodels.GetManifestReq{Name: repository, Reference: resolvedDigest})
		if err != nil {
			return regmodels.ManifestMetadata{}, err
		}
		if env.List != nil {
			return regmodels.ManifestMetadata{}, NestedManifestListError{
				Registry: rc.host, Repository: repository, Reference: resolvedDigest,
			}
		}
	}

	single := env.Single
	total := single.Config.Size
	for _, layer := range single.Layers {
		total += layer.Size
	}

	ref := regmodels.ImageRef{Registry: rc.host, Repository: repository, Reference: reference}
	img := ref.ToImage()
	img.Digest = docker.Digest(env.Digest)

	return regmodels.ManifestMetadata{
		Image:          img,
		SchemaVersion:  single.SchemaVersion,
		MediaType:      single.MediaType,
		TotalSizeBytes: total,
	}, nil
}
