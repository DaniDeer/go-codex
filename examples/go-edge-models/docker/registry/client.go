package registry

import (
	"context"
	"net/http"
	"strings"

	nethttp "github.com/DaniDeer/go-codex/adapters/nethttp"
	"github.com/DaniDeer/go-codex/examples/go-edge-models/docker/registry/internal"
	"github.com/DaniDeer/go-codex/stats"
)

// This file is the general client wiring built ON TOP of routes.go's
// plain [rest.Route] values (see routes.go's doc comment for the full
// layering rationale). A consumer who wants full control over the HTTP
// client or retries can bypass this file entirely and call routes.go's
// routes directly via adapters/nethttp.Call/ClientHandle.
//
// Observer wiring does NOT require bypassing this file at all: GetTags/
// GetImageMetadata's internal nethttp.CallHandle invocations already fall
// back to stats.ObserverFromContext(ctx) whenever CallOptions.Observer is
// nil — the exact same mechanism every nethttp.Call caller gets for free.
// A caller can attach an observer to ctx once (ctx = stats.WithObserver(ctx,
// obs)) and every HTTP call this package makes (including auth.go's Ping +
// token exchange) is observed automatically, with zero calls into this
// package's own API. WithObserver (auth.go) is an ADDITIONAL, explicit
// per-call override on top of that — see its own doc comment.
//
// Everything related to AUTHENTICATION (the Bearer-token challenge/token
// exchange flow, the optional Basic-auth Credentials escape hatch, and
// their Format*/error types) lives in the sibling auth.go instead —
// GetTags/GetImageMetadata below just build a
// newAuthCredentialFunc(httpClient, ref.Registry, ref.Repository, opts...)
// value (same package) and pass it straight through as
// nethttp.CallOptions.CredentialFunc; the actual Ping + challenge +
// token-exchange dance runs LAZILY, inside that credentialFunc, only when
// a secured route (GetTagsRoute/GetManifestRoute) is actually called —
// this file never calls authenticate() directly, nor threads a raw token
// string through fetchManifest (it threads a credentialFunc instead, see
// fetchManifest's own doc comment for why the SAME value is reused across
// GetImageMetadata's two manifest fetches). Package-level constants
// (Docker Hub defaults, acceptManifestTypes) live in constants.go; every
// exported error type this package returns (both client-wiring and auth
// errors) lives in errors.go. This file only holds: image-reference
// parsing (ParseImageRef/FormatImageRef/splitDockerDomain), the
// manifest-list-to-single-platform resolution logic (fetchManifest/
// platformMatches), and the two public entry points (GetTags/
// GetImageMetadata) that tie routes + auth + resolution together.
//
// EVERY request/response aspect below flows through a route + codec —
// zero manual HTTP request building, zero manual response parsing
// anywhere in this file. path/query vars via
// rest.NewPathParam/NewOptionalQueryParam merge fields (consumed
// automatically by nethttp.CallHandle); the Docker-Content-Digest response
// header via rest.NewRequiredResponseHeaderParam (also automatic); and
// image-reference parsing via ImageRefCodec (codecs.go) — ParseImageRef is
// a thin wrapper around its Decode direction, FormatImageRef/
// FormatPlatformSelector around the Encode direction of
// ImageRefCodec/internal.PlatformSelectorCodec.

// ── ParseImageRef ─────────────────────────────────────────────────────────────

// ParseImageRef parses a container image reference into its registry host,
// repository path, and tag/digest reference — replicating Docker Hub's
// default-registry + "library/" prefix convention: an image URL with no
// explicit registry host (e.g. "alpine:latest") resolves to
// Registry="registry-1.docker.io", Repository="library/alpine"; an image
// URL with no tag or digest (e.g. "alpine") defaults Reference to "latest".
//
// This is a thin wrapper around ImageRefCodec.Decode (codecs.go) — the
// actual parsing logic lives in the codec, kept as a public convenience
// entry point so callers don't need to reach for the codec directly.
// Returns ImageRefParseError wrapping the codec's own validation/parse
// error.
func ParseImageRef(raw string) (ImageRef, error) {
	ref, err := ImageRefCodec.Decode(raw)
	if err != nil {
		return ImageRef{}, ImageRefParseError{Input: raw, Err: err}
	}
	return ref, nil
}

// FormatImageRef reconstructs an image reference string from ref — a thin
// wrapper around ImageRefCodec.Encode (codecs.go), the Encode-direction
// counterpart of ParseImageRef. Exported so callers building test
// fixtures, mock servers, or their own tooling around ImageRef can
// construct a valid image reference string without hand-concatenating
// "registry/repository:reference" themselves.
func FormatImageRef(ref ImageRef) (string, error) {
	raw, err := ImageRefCodec.Encode(ref)
	if err != nil {
		return "", err
	}
	return raw.(string), nil
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

// splitDockerDomain splits name into a registry host and repository path,
// applying Docker Hub's conventions: a first path segment is treated as a
// registry host only if it contains "." or ":" or is exactly "localhost"
// (otherwise the whole name is a Docker Hub repository path); Docker Hub's
// reference domain rewrites to the actual reachable registry API host
// (registry-1.docker.io); a single-segment Docker Hub repository name gets
// the "library/" prefix. Used by codecs.go's parseImageRefString (the
// to-direction of ImageRefCodec).
func splitDockerDomain(name string) (domain, repository string) {
	i := strings.IndexByte(name, '/')
	if i == -1 || !strings.ContainsAny(name[:i], ".:") && name[:i] != "localhost" {
		domain, repository = dockerHubDomain, name
	} else {
		domain, repository = name[:i], name[i+1:]
	}
	if domain == dockerHubLegacyDomain || domain == dockerHubDomain {
		domain = dockerHubRegistryHost
		if !strings.Contains(repository, "/") {
			repository = dockerHubOfficialPrefix + "/" + repository
		}
	}
	return domain, repository
}

// registryBaseURL returns the HTTPS base URL to dial for a registry host
// — a small named helper (not a codec: deriving a dial address from an
// already-validated ImageRef.Registry value has no wire decode direction
// to model) replacing three separate inline "https://"+host concatenations.
func registryBaseURL(host string) string {
	return "https://" + host
}

// ── GetTags ───────────────────────────────────────────────────────────────────

// GetTags lists every tag for imageURL's repository. imageURL's own
// Reference segment (if any) is ignored — only Registry and Repository
// are used. Pass WithCredentials(...) for a private repository that
// requires Basic auth at the token-exchange step; anonymous pulls need
// no options at all. Pass WithObserver(...) to receive
// stats.Observer.RecordRequest metrics for every HTTP call this makes
// (including the auth-realm Ping/token exchange, when required).
func GetTags(ctx context.Context, httpClient *http.Client, imageURL string, opts ...Option) (TagsList, error) {
	ref, err := ParseImageRef(imageURL)
	if err != nil {
		return TagsList{}, err
	}

	o := resolveOptions(opts)
	handle := GetTagsRoute.ClientHandle()
	baseURL := registryBaseURL(ref.Registry)
	credFn := newAuthCredentialFunc(httpClient, ref.Registry, ref.Repository, opts...)
	callOpts := nethttp.CallOptions{CredentialFunc: credFn, Observer: o.observer}
	return nethttp.CallHandle(ctx, httpClient, baseURL, handle, GetTagsReq{Name: ref.Repository}, callOpts)
}

// ── GetImageMetadata ──────────────────────────────────────────────────────────

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
	handle := GetManifestRoute.ClientHandle()
	opts := nethttp.CallOptions{
		ExtraHeaders:   http.Header{"Accept": []string{acceptManifestTypes}},
		CredentialFunc: credFn,
		Observer:       obs,
	}
	return nethttp.CallHandle(ctx, httpClient, baseURL, handle,
		GetManifestReq{Name: repository, Reference: reference}, opts)
}

// platformMatches reports whether d's platform matches selector — a plain
// struct-field comparison over already-decoded values (d.Platform via
// internal.ManifestDescriptorCodec, selector via internal.PlatformSelectorCodec)
// — no string
// splitting in business logic.
func platformMatches(d internal.ManifestDescriptor, selector internal.PlatformSelector) bool {
	return d.Platform != nil && d.Platform.OS == selector.OS && d.Platform.Architecture == selector.Architecture
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
func GetImageMetadata(ctx context.Context, httpClient *http.Client, req GetImageMetadataReq, opts ...Option) (ManifestMetadata, error) {
	platformStr := req.Platform
	if platformStr == "" {
		platformStr = defaultPlatform
	}
	selector, err := internal.PlatformSelectorCodec.Decode(platformStr)
	if err != nil {
		return ManifestMetadata{}, err
	}

	ref, err := ParseImageRef(req.ImageURL)
	if err != nil {
		return ManifestMetadata{}, err
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
		return ManifestMetadata{}, err
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
			return ManifestMetadata{}, PlatformNotFoundError{Platform: platformStr, Available: available}
		}

		env, err = fetchManifest(ctx, httpClient, baseURL, ref.Repository, resolvedDigest, credFn, o.observer)
		if err != nil {
			return ManifestMetadata{}, err
		}
		if env.List != nil {
			return ManifestMetadata{}, NestedManifestListError{
				Registry: ref.Registry, Repository: ref.Repository, Reference: resolvedDigest,
			}
		}
	}

	single := env.Single
	total := single.Config.Size
	for _, layer := range single.Layers {
		total += layer.Size
	}

	return ManifestMetadata{
		SchemaVersion:  single.SchemaVersion,
		MediaType:      single.MediaType,
		Digest:         env.Digest,
		TotalSizeBytes: total,
	}, nil
}
