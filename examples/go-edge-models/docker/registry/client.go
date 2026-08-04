package registry

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	nethttp "github.com/DaniDeer/go-codex/adapters/nethttp"
	"github.com/DaniDeer/go-codex/examples/go-edge-models/docker/registry/internal"
	"github.com/DaniDeer/go-codex/route"
)

// This file is the convenience layer built ON TOP of routes.go's plain
// [rest.Route] values (see routes.go's doc comment for the full layering
// rationale). A consumer who wants full control over the HTTP client,
// retries, or observer wiring can bypass this file entirely and call
// routes.go's routes directly via adapters/nethttp.Call/ClientHandle.
//
// EVERY request/response aspect below flows through a route + codec —
// zero manual HTTP request building, zero manual response parsing
// anywhere in this file. path/query vars via
// rest.NewPathParam/NewOptionalQueryParam merge fields (consumed
// automatically by nethttp.CallHandle); the Docker-Content-Digest response
// header via rest.NewRequiredResponseHeaderParam (also automatic); the
// Bearer Authorization header via RouteMeta.Security + CallOptions.
// CredentialFunc; and every string-parsing routine (image reference,
// WWW-Authenticate challenge, "os/arch" platform selector) via its own
// codec — either this package's own ImageRefCodec (codecs.go), or the
// generic OCI/Docker Distribution Spec codecs in
// docker/registry/internal (ChallengeCodec, PlatformSelectorCodec,
// DockerScopeCodec, BearerTokenCodec — a real Go internal package,
// importable only from here, keeping those wire-format/auth-flow
// internals out of this library's public surface). ParseImageRef and
// parseChallenge below are thin wrappers around
// ImageRefCodec.Decode/internal.WWWAuthenticateCodec.Decode; the
// Format* functions are thin wrappers around the Encode direction of
// each — none of them require a caller to import the internal package.
//
// authenticate's Ping/401-detection step is now ALSO a plain
// nethttp.CallHandle call (previously a manual net/http request) — reading
// the WWW-Authenticate challenge header on the 401 response uses
// nethttp.UnexpectedStatusError.Header, a declarative escape hatch added
// to adapters/nethttp for exactly this class of problem: a response header
// only present on a non-2xx response, which rest.NewRequiredResponseHeaderParam's
// success-path-only merge cannot reach. This file performs no I/O of its
// own beyond calling nethttp.Call/CallHandle — every request/response is
// route+codec driven.

// ── Docker Hub defaults ───────────────────────────────────────────────────────

const (
	// dockerHubLegacyDomain is the domain name Docker CLI accepts as an
	// alias for dockerHubDomain in an image reference (e.g.
	// "index.docker.io/library/alpine").
	dockerHubLegacyDomain = "docker.io"
	// dockerHubDomain is the canonical Docker Hub domain as it appears in
	// an image reference when no other registry host is given.
	dockerHubDomain = "docker.io"
	// dockerHubRegistryHost is the ACTUAL host GetTags/GetImageMetadata
	// call — Docker Hub's reference domain ("docker.io") is NOT itself a
	// reachable registry API host; "registry-1.docker.io" is.
	dockerHubRegistryHost = "registry-1.docker.io"
	// dockerHubOfficialPrefix is prepended to a single-segment repository
	// name resolved against Docker Hub (e.g. "alpine" -> "library/alpine").
	dockerHubOfficialPrefix = "library"
	// defaultReference is used when an image URL has neither a tag nor a
	// digest.
	defaultReference = "latest"
	// defaultPlatform is used when GetImageMetadataReq.Platform is empty.
	defaultPlatform = "linux/amd64"
)

// acceptManifestTypes is the fixed Accept header value sent with every
// manifest fetch — all four media types the Docker Registry HTTP API v2 /
// OCI Distribution Spec may return, so the registry can pick whichever
// shape is appropriate for the requested reference. This is a protocol
// negotiation constant, not a caller-supplied value (see routes.go's
// GetManifestRoute doc comment for why it isn't a rest.HeaderParam).
const acceptManifestTypes = "application/vnd.docker.distribution.manifest.v2+json," +
	"application/vnd.oci.image.manifest.v1+json," +
	"application/vnd.docker.distribution.manifest.list.v2+json," +
	"application/vnd.oci.image.index.v1+json"

// ── Errors ────────────────────────────────────────────────────────────────────

// ImageRefParseError is returned by ParseImageRef when the input string
// does not match a valid container image reference shape.
type ImageRefParseError struct {
	Input string
	Err   error
}

func (e ImageRefParseError) Error() string {
	return fmt.Sprintf("parse image reference %q: %s", e.Input, e.Err)
}
func (e ImageRefParseError) Unwrap() error { return e.Err }
func (e ImageRefParseError) LogValue() slog.Value {
	return slog.GroupValue(slog.String("input", e.Input), slog.Any("cause", e.Err))
}

// RegistryAuthChallengeError is returned when a registry's 401 response
// carries a malformed or missing WWW-Authenticate header.
type RegistryAuthChallengeError struct {
	Header string
	Err    error
}

func (e RegistryAuthChallengeError) Error() string {
	return fmt.Sprintf("parse WWW-Authenticate challenge %q: %s", e.Header, e.Err)
}
func (e RegistryAuthChallengeError) Unwrap() error { return e.Err }
func (e RegistryAuthChallengeError) LogValue() slog.Value {
	return slog.GroupValue(slog.String("header", e.Header), slog.Any("cause", e.Err))
}

// RegistryAuthError is returned when the auth realm's token endpoint call
// fails, or the ping request itself fails for a reason other than a clean
// 401 challenge.
type RegistryAuthError struct {
	Registry string
	Err      error
}

func (e RegistryAuthError) Error() string {
	return fmt.Sprintf("authenticate with registry %q: %s", e.Registry, e.Err)
}
func (e RegistryAuthError) Unwrap() error { return e.Err }
func (e RegistryAuthError) LogValue() slog.Value {
	return slog.GroupValue(slog.String("registry", e.Registry), slog.Any("cause", e.Err))
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

// FormatChallenge reconstructs a WWW-Authenticate Bearer challenge header
// value from its realm/service/scope parameters — a thin wrapper around
// internal.ChallengeCodec.Encode. Exported so callers building mock
// registry/auth servers for tests or demos can construct a valid
// challenge header without hand-concatenating
// `Bearer realm="...",service="...",scope="..."` themselves, and without
// needing to know about the internal.Challenge type.
func FormatChallenge(realm, service, scope string) (string, error) {
	raw, err := internal.ChallengeCodec.Encode(internal.Challenge{Realm: realm, Service: service, Scope: scope})
	if err != nil {
		return "", err
	}
	return raw.(string), nil
}

// FormatDockerScope reconstructs a Docker Distribution auth scope string
// from its resourceType/name/actions parameters — a thin wrapper around
// internal.DockerScopeCodec.Encode. Exported so callers building mock
// auth servers, or requesting a scope with custom actions, can construct
// a valid scope string without hand-concatenating
// "type:name:action1,action2" themselves.
func FormatDockerScope(resourceType, name string, actions []string) (string, error) {
	raw, err := internal.DockerScopeCodec.Encode(internal.DockerScope{ResourceType: resourceType, Name: name, Actions: actions})
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

// FormatBearerToken formats token as an "Authorization: Bearer <token>"
// header value — a thin wrapper around internal.BearerTokenCodec.Encode,
// which never fails for a plain string, so this returns just the
// formatted string (no error) for ergonomic call sites.
func FormatBearerToken(token string) string {
	raw, _ := internal.BearerTokenCodec.Encode(token)
	return raw.(string)
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

// ── Auth challenge parsing ────────────────────────────────────────────────────

// parseChallenge decodes header's "WWW-Authenticate" entry into an
// internal.Challenge (RFC 6750 / Docker Distribution's auth spec: realm/
// service/scope parameters). Thin wrapper around
// internal.WWWAuthenticateCodec.Decode — the header extraction AND the
// parsing both happen inside that single codec Decode call, not a plain
// Header.Get(...) here. Returns RegistryAuthChallengeError wrapping the
// codec's own parse/validation error (missing "Bearer " prefix,
// missing/invalid "realm").
func parseChallenge(header http.Header) (internal.Challenge, error) {
	ch, err := internal.WWWAuthenticateCodec.Decode(header)
	if err != nil {
		return internal.Challenge{}, RegistryAuthChallengeError{Header: header.Get("WWW-Authenticate"), Err: err}
	}
	return ch, nil
}

// registryBaseURL returns the HTTPS base URL to dial for a registry host
// — a small named helper (not a codec: deriving a dial address from an
// already-validated ImageRef.Registry value has no wire decode direction
// to model) replacing three separate inline "https://"+host concatenations.
func registryBaseURL(host string) string {
	return "https://" + host
}

// ── authenticate ──────────────────────────────────────────────────────────
// authenticate probes registryHost's base endpoint (GET /v2/) and, if it
// requires auth (401 + WWW-Authenticate challenge), fetches a Bearer token
// scoped to "repository:<repository>:pull" from the challenge's realm via
// GetTokenRoute (a normal, fully declarative nethttp.CallHandle call).
// Returns "" (no error) when the registry does not require auth.
func authenticate(ctx context.Context, httpClient *http.Client, registryHost, repository string) (string, error) {
	baseURL := registryBaseURL(registryHost)
	pingHandle := PingRoute.ClientHandle()
	_, err := nethttp.CallHandle(ctx, httpClient, baseURL, pingHandle, struct{}{}, nethttp.CallOptions{})
	if err == nil {
		return "", nil // 2xx — registry requires no auth for this request.
	}

	var statusErr nethttp.UnexpectedStatusError
	if !errors.As(err, &statusErr) || statusErr.StatusCode != http.StatusUnauthorized {
		return "", RegistryAuthError{Registry: registryHost, Err: err}
	}

	// nethttp.UnexpectedStatusError.Header is the declarative escape hatch
	// for a response header only present on a non-2xx response (the
	// success-path response-header merge, rest.NewRequiredResponseHeaderParam,
	// only applies to 2xx responses). parseChallenge (WWWAuthenticateCodec
	// under the hood) extracts AND decodes the "WWW-Authenticate" entry in
	// one codec Decode call — no manual HTTP request/response handling, no
	// plain Header.Get(...), anywhere in this function.
	challenge, err := parseChallenge(statusErr.Header)
	if err != nil {
		return "", err
	}
	scope := challenge.Scope
	if scope == "" {
		scope, err = FormatDockerScope("repository", repository, []string{"pull"})
		if err != nil {
			return "", err
		}
	}

	tokenHandle := GetTokenRoute.ClientHandle()
	tr, err := nethttp.CallHandle(ctx, httpClient, challenge.Realm, tokenHandle,
		GetTokenReq{Service: challenge.Service, Scope: scope}, nethttp.CallOptions{})
	if err != nil {
		return "", RegistryAuthError{Registry: registryHost, Err: err}
	}
	if tr.Token != "" {
		return tr.Token, nil
	}
	return tr.AccessToken, nil
}

// bearerCredentialFunc returns a nethttp.CallOptions.CredentialFunc that
// supplies the Authorization: Bearer header for token — the declarative
// replacement for setting CallOptions.ExtraHeaders by hand. Invoked
// automatically by nethttp.Call/CallHandle for any route declaring
// RouteMeta.Security (see routes.go's bearerAuthSecurity). Returns an
// empty header (no-op) when token is "" (registry requires no auth).
func bearerCredentialFunc(token string) func(context.Context, []route.SecurityRequirement) (http.Header, error) {
	return func(context.Context, []route.SecurityRequirement) (http.Header, error) {
		if token == "" {
			return nil, nil
		}
		h := make(http.Header, 1)
		h.Set("Authorization", FormatBearerToken(token))
		return h, nil
	}
}

// ── GetTags ───────────────────────────────────────────────────────────────────

// GetTags lists every tag for imageURL's repository. imageURL's own
// Reference segment (if any) is ignored — only Registry and Repository
// are used.
func GetTags(ctx context.Context, httpClient *http.Client, imageURL string) (TagsList, error) {
	ref, err := ParseImageRef(imageURL)
	if err != nil {
		return TagsList{}, err
	}
	token, err := authenticate(ctx, httpClient, ref.Registry, ref.Repository)
	if err != nil {
		return TagsList{}, err
	}

	handle := GetTagsRoute.ClientHandle()
	baseURL := registryBaseURL(ref.Registry)
	opts := nethttp.CallOptions{CredentialFunc: bearerCredentialFunc(token)}
	return nethttp.CallHandle(ctx, httpClient, baseURL, handle, GetTagsReq{Name: ref.Repository}, opts)
}

// ── GetImageMetadata ──────────────────────────────────────────────────────────

// fetchManifest is a single, fully declarative nethttp.CallHandle call for
// one manifest reference — GetManifestRoute's response-header merge field
// already populates the returned internal.ManifestEnvelope's Digest from
// Docker-Content-Digest automatically, so no manual HTTP or header
// reading is needed here (contrast with the Ping step in authenticate,
// which genuinely cannot use this mechanism — see the file-level doc
// comment).
func fetchManifest(ctx context.Context, httpClient *http.Client, baseURL, repository, reference, token string) (internal.ManifestEnvelope, error) {
	handle := GetManifestRoute.ClientHandle()
	opts := nethttp.CallOptions{
		ExtraHeaders:   http.Header{"Accept": []string{acceptManifestTypes}},
		CredentialFunc: bearerCredentialFunc(token),
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
// req.Platform is empty).
func GetImageMetadata(ctx context.Context, httpClient *http.Client, req GetImageMetadataReq) (ManifestMetadata, error) {
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
	token, err := authenticate(ctx, httpClient, ref.Registry, ref.Repository)
	if err != nil {
		return ManifestMetadata{}, err
	}

	baseURL := registryBaseURL(ref.Registry)
	env, err := fetchManifest(ctx, httpClient, baseURL, ref.Repository, ref.Reference, token)
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

		env, err = fetchManifest(ctx, httpClient, baseURL, ref.Repository, resolvedDigest, token)
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
