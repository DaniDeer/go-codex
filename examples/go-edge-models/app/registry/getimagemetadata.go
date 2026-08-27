package registry

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"

	mcpgo "github.com/DaniDeer/go-codex/adapters/mcpgo"
	nethttp "github.com/DaniDeer/go-codex/adapters/nethttp"
	"github.com/DaniDeer/go-codex/examples/go-edge-models/internal/registry"
	"github.com/DaniDeer/go-codex/examples/go-edge-models/models/docker"
	regmodels "github.com/DaniDeer/go-codex/examples/go-edge-models/models/docker/registry"
	"github.com/DaniDeer/go-codex/middleware"
	"github.com/DaniDeer/go-codex/stats"
)

// This file holds the CONCRETE IMPLEMENTATION of the GetImageMetadata
// operation: the manifest-list-to-single-platform resolution logic
// (fetchManifest/platformMatches/FormatPlatformSelector), its error
// types (NestedManifestListError/PlatformNotFoundError), the
// batteries-included client function (GetImageMetadata), and
// NewGetImageMetadataToolHandler (which BINDS
// regmodels.GetImageMetadataTool — the declared MCP tool contract — to
// this implementation). The declarative contract itself
// (GetManifestRoute, GetImageMetadataReq/ManifestMetadata and their
// codecs, GetImageMetadataTool) lives in the sibling
// models/docker/registry package instead — see this package's doc.go for
// the full models/ vs app/ rationale.

// acceptManifestTypes is the fixed Accept header value sent with every
// manifest fetch — all four media types the Docker Registry HTTP API v2 /
// OCI Distribution Spec may return, so the registry can pick whichever
// shape is appropriate for the requested reference. This is a protocol
// negotiation constant, not a caller-supplied value (see
// regmodels.GetManifestRoute's own doc comment for why it isn't a
// rest.HeaderParam).
const acceptManifestTypes = "application/vnd.docker.distribution.manifest.v2+json," +
	"application/vnd.oci.image.manifest.v1+json," +
	"application/vnd.docker.distribution.manifest.list.v2+json," +
	"application/vnd.oci.image.index.v1+json"

// defaultPlatform is used when GetImageMetadataReq.Platform is empty.
const defaultPlatform = "linux/amd64"

// fetchManifest is a single, fully declarative nethttp.CallHandle call for
// one manifest reference — GetManifestRoute's response-header merge field
// already populates the returned internal.ManifestEnvelope's Digest from
// Docker-Content-Digest automatically, so no manual HTTP or header
// reading is needed here (contrast with the Ping step in authenticate,
// which genuinely cannot use this mechanism — see auth.go's file doc
// comment). credFn is a single newAuthCredentialFunc(...) value shared
// across every fetchManifest call in one GetImageMetadata invocation, so
// the auth flow it performs lazily on first use stays memoized across
// calls (see GetImageMetadata's manifest-list resolution below, which may
// call fetchManifest twice for one request).
func fetchManifest(ctx context.Context, httpClient *http.Client, baseURL, repository, reference string, credFn credentialFunc, obs stats.Observer) (internal.ManifestEnvelope, error) {
	handle := regmodels.GetManifestRoute.ClientHandle()
	opts := nethttp.CallOptions{
		ExtraHeaders: http.Header{"Accept": []string{acceptManifestTypes}},
		Observer:     obs,
	}
	return nethttp.CallHandle(ctx, httpClient, baseURL, handle,
		regmodels.GetManifestReq{Name: repository, Reference: reference}, opts,
		middleware.Middleware{Fn: credFn})
}

// platformMatches reports whether d's platform matches selector — a plain
// struct-field comparison over already-decoded values (d.Platform via
// internal.ManifestDescriptorCodec, selector via internal.PlatformSelectorCodec)
// — no string splitting in business logic.
func platformMatches(d internal.ManifestDescriptor, selector internal.PlatformSelector) bool {
	return d.Platform != nil && d.Platform.OS == selector.OS && d.Platform.Architecture == selector.Architecture
}

// FormatPlatformSelector reconstructs an "os/arch" platform selector
// string from its os/arch parameters — a thin wrapper around
// internal.PlatformSelectorCodec.Encode.
func FormatPlatformSelector(osName, arch string) (string, error) {
	raw, err := internal.PlatformSelectorCodec.Encode(internal.PlatformSelector{OS: osName, Architecture: arch})
	if err != nil {
		return "", err
	}
	return raw.(string), nil
}

// NestedManifestListError is returned by GetImageMetadata when a resolved
// manifest-list entry's digest ALSO resolves to a manifest list — this
// package supports exactly one level of manifest-list resolution.
type NestedManifestListError struct {
	Registry   string
	Repository string
	Reference  string
}

func (e NestedManifestListError) Error() string {
	return fmt.Sprintf("nested manifest list at %s/%s:%s (only one level of resolution is supported)",
		e.Registry, e.Repository, e.Reference)
}
func (e NestedManifestListError) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("registry", e.Registry),
		slog.String("repository", e.Repository),
		slog.String("reference", e.Reference),
	)
}

// PlatformNotFoundError is returned by GetImageMetadata when a manifest
// list has no entry matching the requested platform.
type PlatformNotFoundError struct {
	Platform  string
	Available []string
}

func (e PlatformNotFoundError) Error() string {
	return fmt.Sprintf("platform %q not found in manifest list (available: %v)", e.Platform, e.Available)
}
func (e PlatformNotFoundError) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("platform", e.Platform),
		slog.Any("available", e.Available),
	)
}

// GetImageMetadata fetches lean manifest metadata for req.ImageURL,
// transparently resolving a multi-arch manifest list / OCI image index to
// req.Platform's specific manifest (defaulting to "linux/amd64" when
// req.Platform is empty). Pass WithCredentials(...) for a private
// repository that requires Basic auth at the token-exchange step;
// anonymous pulls need no options at all. Pass WithObserver(...) to
// receive stats.Observer.RecordRequest metrics for every HTTP call this
// makes (auth-realm Ping/token exchange, plus one or two GetManifestRoute
// calls depending on manifest-list resolution).
func GetImageMetadata(ctx context.Context, httpClient *http.Client, req regmodels.GetImageMetadataReq, opts ...Option) (regmodels.ManifestMetadata, error) {
	platformStr := req.Platform
	if platformStr == "" {
		platformStr = defaultPlatform
	}
	selector, err := internal.PlatformSelectorCodec.Decode(platformStr)
	if err != nil {
		return regmodels.ManifestMetadata{}, err
	}

	ref, err := regmodels.ParseImageRef(req.ImageURL)
	if err != nil {
		return regmodels.ManifestMetadata{}, err
	}

	o := resolveOptions(opts)
	baseURL := registryBaseURL(ref.Registry)
	// One credFn shared across both fetchManifest calls below (list
	// resolution + platform-specific fetch) — newAuthCredentialFunc
	// memoizes its own Ping/token-exchange work, so reusing this single
	// value means that work happens at most once per GetImageMetadata call.
	credFn := newAuthCredentialFunc(httpClient, ref.Registry, ref.Repository, opts...)
	env, err := fetchManifest(ctx, httpClient, baseURL, ref.Repository, ref.Reference, credFn, o.observer)
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
			if platformMatches(m, selector) {
				resolvedDigest = m.Digest
				break
			}
		}
		if resolvedDigest == "" {
			return regmodels.ManifestMetadata{}, PlatformNotFoundError{Platform: platformStr, Available: available}
		}

		env, err = fetchManifest(ctx, httpClient, baseURL, ref.Repository, resolvedDigest, credFn, o.observer)
		if err != nil {
			return regmodels.ManifestMetadata{}, err
		}
		if env.List != nil {
			return regmodels.ManifestMetadata{}, NestedManifestListError{
				Registry: ref.Registry, Repository: ref.Repository, Reference: resolvedDigest,
			}
		}
	}

	single := env.Single
	total := single.Config.Size
	for _, layer := range single.Layers {
		total += layer.Size
	}

	// Reuse the EXISTING ImageRef -> docker.Image mapper
	// (models/docker/registry/imageref.go) for Name/Tag, then override
	// Digest with the ACTUAL resolved content digest — ref.ToImage()'s
	// own Digest (derived from ref.Reference) would be stale/wrong after
	// manifest-list resolution, since ref.Reference is the ORIGINALLY
	// requested tag/digest, not necessarily the platform-resolved one.
	img := ref.ToImage()
	img.Digest = docker.Digest(env.Digest)

	return regmodels.ManifestMetadata{
		Image:          img,
		SchemaVersion:  single.SchemaVersion,
		MediaType:      single.MediaType,
		TotalSizeBytes: total,
	}, nil
}

// ── GetImageMetadata as an MCP tool ─────────────────────────────────────────────
//
// NewGetImageMetadataToolHandler wraps the batteries-included
// GetImageMetadata FUNCTION above (registry-agnostic — it resolves the
// target registry per call from req.ImageURL, same as GetTagsTool's
// rationale in gettags.go), NOT GetManifestRoute directly — there's no
// single fixed REST endpoint to bridge, and GetImageMetadata may issue up
// to two HTTP calls per invocation for manifest-list resolution, so this
// is a plain closure, not an mcprest bridge.

// NewGetImageMetadataToolHandler returns an mcpgo.HandlerFunc that calls
// GetImageMetadata against httpClient/opts for every tool invocation.
//
// Usage:
//
//	tool, _ := regmodels.GetImageMetadataTool.Register(mcpBuilder)
//	_, handlerFn := mcpgo.ToolHandler(tool,
//	    registryapp.NewGetImageMetadataToolHandler(httpClient, registryapp.WithObserver(obs)),
//	    mcpgo.Options{})
func NewGetImageMetadataToolHandler(httpClient *http.Client, opts ...Option) mcpgo.HandlerFunc[regmodels.GetImageMetadataReq, regmodels.ManifestMetadata] {
	return func(ctx context.Context, req regmodels.GetImageMetadataReq) (regmodels.ManifestMetadata, error) {
		return GetImageMetadata(ctx, httpClient, req, opts...)
	}
}
