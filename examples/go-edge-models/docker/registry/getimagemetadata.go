package registry

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"

	mcpgo "github.com/DaniDeer/go-codex/adapters/mcpgo"
	mcprest "github.com/DaniDeer/go-codex/adapters/mcprest"
	nethttp "github.com/DaniDeer/go-codex/adapters/nethttp"
	mcp "github.com/DaniDeer/go-codex/api/mcp"
	"github.com/DaniDeer/go-codex/api/rest"
	c "github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/examples/go-edge-models/docker"
	"github.com/DaniDeer/go-codex/examples/go-edge-models/docker/registry/internal"
	"github.com/DaniDeer/go-codex/stats"
	v "github.com/DaniDeer/go-codex/validate"
)

// This file holds everything specific to the GetImageMetadata operation:
// the underlying single-manifest route (GetManifestRoute) it composes,
// GetImageMetadata's own request/response contract (GetImageMetadataReq/
// ManifestMetadata), the manifest-list-to-single-platform resolution
// logic (fetchManifest/platformMatches), the batteries-included client
// function (GetImageMetadata), and its ready-made MCP tool
// (GetImageMetadataTool/NewGetImageMetadataToolHandler).

// ── GetManifestReq / GetManifestRoute ──────────────────────────────────────────

// GetManifestReq is GetManifestRoute's request — Name and Reference merge
// automatically into the {name}/{reference} path variables via
// nethttp.CallHandle.
type GetManifestReq struct {
	Name      string
	Reference string
}

// GetManifestRoute is GET /v2/{name}/manifests/{reference} — fetches a
// manifest (single-platform) or a manifest list / OCI image index,
// dispatched automatically by internal.ManifestEnvelopeCodec based on the
// response shape. {reference} is a tag or a digest. Req is GetManifestReq,
// whose Name/Reference fields merge into {name}/{reference} automatically
// via nethttp.CallHandle. Resp additionally merges the
// Docker-Content-Digest RESPONSE HEADER directly into
// internal.ManifestEnvelope.Digest via rest.NewRequiredResponseHeaderParam
// — nethttp.Call/CallHandle applies this merge automatically on every
// successful (2xx) response, so this file never needs a manual HTTP call
// just to read that header.
//
// Resp is internal.ManifestEnvelope — a consumer calling
// GetManifestRoute.ClientHandle() directly (bypassing the GetImageMetadata
// convenience function below) receives a value of this type. Since
// docker/registry/internal is a true Go internal package, that type
// cannot be NAMED from outside docker/registry — but its EXPORTED fields
// (Digest, Single, List) remain readable via ordinary Go type inference
// (e.g. `env, _ := nethttp.Call(...); env.Digest`). This is intentional:
// GetManifestRoute stays usable standalone for advanced/low-level cases,
// while the internal package boundary makes it unambiguous that the raw
// envelope shape is plumbing — GetImageMetadata is the supported, fully
// resolved public result.
//
// The registry's response media type is negotiated via the Accept request
// header — fetchManifest below sends all four supported media types
// (Docker Schema 2 manifest, OCI manifest, Docker manifest list, OCI
// image index) so the registry can return whichever shape is appropriate
// for {reference}. This route does not declare Accept as a
// rest.HeaderParam because its value is a fixed protocol-negotiation
// constant, not a caller-supplied value — see acceptManifestTypes below.
var GetManifestRoute = rest.NewRoute[GetManifestReq, internal.ManifestEnvelope](
	"GET", "/v2/{name}/manifests/{reference}",
	c.Struct[GetManifestReq](), internal.ManifestEnvelopeCodec,
	rest.RouteMeta{
		OperationID:    "getManifest",
		Summary:        "Fetch a manifest or manifest list for a repository reference",
		RespSchemaName: "ManifestEnvelope",
		Security:       bearerAuthSecurity,
	},
	rest.WithSecurityScheme("bearerAuth", bearerAuthScheme),
	rest.NewPathParam("name",
		c.String(),
		func(r GetManifestReq) string { return r.Name },
		func(r *GetManifestReq, v string) { r.Name = v },
	).WithDescription("Repository path"),
	rest.NewPathParam("reference",
		c.String(),
		func(r GetManifestReq) string { return r.Reference },
		func(r *GetManifestReq, v string) { r.Reference = v },
	).WithDescription("Tag or digest"),
	rest.NewRequiredResponseHeaderParam("Docker-Content-Digest",
		internal.DigestCodec,
		func(e internal.ManifestEnvelope) string { return e.Digest },
		func(e *internal.ManifestEnvelope, v string) { e.Digest = v },
	).WithDescription("The manifest's own content digest"),
)

// acceptManifestTypes is the fixed Accept header value sent with every
// manifest fetch — all four media types the Docker Registry HTTP API v2 /
// OCI Distribution Spec may return, so the registry can pick whichever
// shape is appropriate for the requested reference. This is a protocol
// negotiation constant, not a caller-supplied value (see GetManifestRoute
// above for why it isn't a rest.HeaderParam).
const acceptManifestTypes = "application/vnd.docker.distribution.manifest.v2+json," +
	"application/vnd.oci.image.manifest.v1+json," +
	"application/vnd.docker.distribution.manifest.list.v2+json," +
	"application/vnd.oci.image.index.v1+json"

// defaultPlatform is used when GetImageMetadataReq.Platform is empty.
const defaultPlatform = "linux/amd64"

// ── GetImageMetadata's own request/response contract ─────────────────────────
//
// GetImageMetadataReq/ManifestMetadata are NOT GetManifestRoute's Req/Resp
// types — GetImageMetadata is a multi-call CLIENT-SIDE orchestration, not
// one dialable HTTP endpoint: it parses ImageURL into an ImageRef, then
// calls GetManifestRoute up to TWICE (once to resolve a multi-arch
// manifest list / OCI image index to a single platform, once more for
// that platform's actual manifest), and reduces the result into a
// computed summary (TotalSizeBytes sums every layer's size; per-layer
// detail is deliberately excluded). There is no single Method+Path this
// operation could be declared against, so it is NOT a rest.NewRoute value
// (that would require a real, dialable Method/Path — declaring one that's
// never actually used to make the underlying HTTP call would be
// misleading, not a genuine contract).
//
// It still gets the SAME "single declared source of truth for this
// shape" benefit routes get, via a plain codex.Struct-based codec pair
// (GetImageMetadataReqCodec/ManifestMetadataCodec) instead of a route:
// the fields are defined ONCE, here, with the same
// RequiredField/OptionalField machinery GetTagsReq/TagsList/ImageRef use
// — GetImageMetadata itself constructs/reads these types directly in Go
// (no wire encode/decode actually happens for them at runtime), but the
// codecs remain available for a caller who wants to serialize a
// GetImageMetadataReq/ManifestMetadata value themselves (e.g. logging,
// caching, or exposing it through their own API) — and they are reused
// AS-IS as GetImageMetadataTool's In/Out below (already a single,
// registry-agnostic struct shape — no separate tool-request type needed,
// unlike GetTags/GetTagsToolReq).

// GetImageMetadataReq is the input to GetImageMetadata.
type GetImageMetadataReq struct {
	// ImageURL is the raw image reference string, e.g.
	// "quay.io/prometheus/prometheus:v2.53.0" or "alpine:latest".
	ImageURL string
	// Platform selects which platform-specific manifest to resolve when
	// ImageURL points at a multi-arch manifest list / OCI image index,
	// formatted "os/arch" (e.g. "linux/amd64"). Defaults to "linux/amd64"
	// when empty.
	Platform string
}

// GetImageMetadataReqCodec validates a GetImageMetadataReq value —
// ImageURL is required (GetImageMetadata itself further validates it via
// ImageRefCodec.Decode at call time; this codec only checks non-empty
// since the FULL image-reference shape isn't yet known here); Platform is
// optional (see GetImageMetadataReq.Platform's own doc comment for the
// default).
var GetImageMetadataReqCodec = c.Struct[GetImageMetadataReq](
	c.RequiredField("imageURL",
		c.String().Refine(v.NonEmptyString).WithDescription(
			"The full container image reference to fetch metadata for, e.g. "+
				`"quay.io/prometheus/prometheus:v2.53.0" or "alpine:latest" — `+
				"any registry host embedded in the reference is resolved "+
				"automatically; an absent registry host defaults to Docker Hub.",
		),
		func(r GetImageMetadataReq) string { return r.ImageURL },
		func(r *GetImageMetadataReq, val string) { r.ImageURL = val },
	),
	c.OptionalField("platform",
		c.String().WithDescription(
			`Which platform-specific manifest to resolve when imageURL points `+
				`at a multi-arch manifest list / OCI image index, formatted `+
				`"os/arch" (e.g. "linux/amd64", "linux/arm64"). Defaults to `+
				`"linux/amd64" when omitted.`,
		),
		func(r GetImageMetadataReq) string { return r.Platform },
		func(r *GetImageMetadataReq, val string) { r.Platform = val },
	),
)

// ManifestMetadata is the lean, caller-facing result of GetImageMetadata —
// deliberately excludes per-layer detail (config digest, individual layer
// digests/sizes). If the resolved image reference pointed at a multi-arch
// manifest list / OCI image index, this is the metadata of the SINGLE
// platform-specific manifest GetImageMetadata transparently resolved to
// (see GetImageMetadataReq.Platform) — the caller never sees the list/index
// shape itself.
type ManifestMetadata struct {
	// Image is the resolved image identity as a single docker.Image
	// domain value — Name/Tag from the parsed ImageURL (via
	// ImageRef.ToImage()) and Digest set to the ACTUAL resolved content
	// digest (from the Docker-Content-Digest response header), which is
	// authoritative regardless of what Tag/Digest the caller originally
	// supplied (e.g. after multi-arch manifest-list resolution, the
	// resolved digest differs from any list-level digest).
	Image         docker.Image
	SchemaVersion int
	MediaType     string
	// TotalSizeBytes is Config.Size plus the sum of every entry in
	// Layers[].Size — the total on-disk size Docker would need to pull
	// this image, without exposing the individual layer breakdown.
	TotalSizeBytes int64
}

// ManifestMetadataCodec validates a ManifestMetadata value — every field
// is required since this type is always fully machine-constructed by
// GetImageMetadata, never partially populated from an external source.
var ManifestMetadataCodec = c.Struct[ManifestMetadata](
	c.RequiredField("image", docker.ImageCodec,
		func(m ManifestMetadata) docker.Image { return m.Image },
		func(m *ManifestMetadata, val docker.Image) { m.Image = val },
	),
	c.RequiredField("schemaVersion", c.Int(),
		func(m ManifestMetadata) int { return m.SchemaVersion },
		func(m *ManifestMetadata, val int) { m.SchemaVersion = val },
	),
	c.RequiredField("mediaType", c.String().Refine(v.NonEmptyString),
		func(m ManifestMetadata) string { return m.MediaType },
		func(m *ManifestMetadata, val string) { m.MediaType = val },
	),
	c.RequiredField("totalSizeBytes", c.Int64().Refine(v.PositiveInt64),
		func(m ManifestMetadata) int64 { return m.TotalSizeBytes },
		func(m *ManifestMetadata, val int64) { m.TotalSizeBytes = val },
	),
)

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

	// Reuse the EXISTING ImageRef -> docker.Image mapper (imageref.go) for
	// Name/Tag, then override Digest with the ACTUAL resolved content
	// digest — ref.ToImage()'s own Digest (derived from ref.Reference)
	// would be stale/wrong after manifest-list resolution, since
	// ref.Reference is the ORIGINALLY requested tag/digest, not
	// necessarily the platform-resolved one.
	img := ref.ToImage()
	img.Digest = docker.Digest(env.Digest)

	return ManifestMetadata{
		Image:          img,
		SchemaVersion:  single.SchemaVersion,
		MediaType:      single.MediaType,
		TotalSizeBytes: total,
	}, nil
}

// ── GetImageMetadata as an MCP tool ─────────────────────────────────────────────
//
// GetImageMetadataTool/NewGetImageMetadataToolHandler wrap the
// batteries-included GetImageMetadata FUNCTION above (registry-agnostic
// — it resolves the target registry per call from req.ImageURL, same as
// GetTagsTool's rationale in gettags.go), NOT GetManifestRoute directly
// — there's no single fixed REST endpoint to bridge, and GetImageMetadata
// may issue up to two HTTP calls per invocation for manifest-list
// resolution, so this is a plain closure, not an mcprest bridge.

// GetImageMetadataTool is the declared, UNREGISTERED MCP tool contract
// for GetImageMetadata — reuses GetImageMetadataReq/ManifestMetadata AND
// their codecs directly (already a single, registry-agnostic struct
// shape — no separate tool-request type needed, unlike GetTagsToolReq).
// ToolMeta.Description tells an LLM client WHEN to call this tool;
// GetImageMetadataReqCodec's own per-field .WithDescription (above) tells
// it WHAT to pass for imageURL/platform — both flow into the JSON Schema
// an MCP client shows the LLM.
var GetImageMetadataTool = mcp.NewTool[GetImageMetadataReq, ManifestMetadata](
	"get_image_metadata", GetImageMetadataReqCodec, ManifestMetadataCodec,
	append([]mcp.ToolOpt{
		mcp.ToolMeta{
			Description: "Fetch lean manifest metadata — schema version, media " +
				"type, resolved image digest, and total pull size — for a " +
				"container image, from any OCI-compliant registry. Automatically " +
				"resolves a multi-arch manifest list / OCI image index to the " +
				"requested platform.",
		},
	}, mcprest.DefaultErrorPatterns()...)...,
)

// NewGetImageMetadataToolHandler returns an mcpgo.HandlerFunc that calls
// GetImageMetadata against httpClient/opts for every tool invocation.
//
// Usage:
//
//	tool, _ := registry.GetImageMetadataTool.Register(mcpBuilder)
//	_, handlerFn := mcpgo.ToolHandler(tool,
//	    registry.NewGetImageMetadataToolHandler(httpClient, registry.WithObserver(obs)),
//	    mcpgo.Options{})
func NewGetImageMetadataToolHandler(httpClient *http.Client, opts ...Option) mcpgo.HandlerFunc[GetImageMetadataReq, ManifestMetadata] {
	return func(ctx context.Context, req GetImageMetadataReq) (ManifestMetadata, error) {
		return GetImageMetadata(ctx, httpClient, req, opts...)
	}
}
