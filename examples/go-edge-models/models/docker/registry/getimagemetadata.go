package registry

import (
	"context"
	"fmt"
	"strings"

	mcprest "github.com/DaniDeer/go-codex/adapters/mcprest"
	mcp "github.com/DaniDeer/go-codex/api/mcp"
	"github.com/DaniDeer/go-codex/api/rest"
	c "github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/examples/go-edge-models/models/docker"
	util "github.com/DaniDeer/go-codex/examples/go-edge-models/models/util"
	"github.com/DaniDeer/go-codex/schema"
	v "github.com/DaniDeer/go-codex/validate"
)

// This file holds the DECLARATIVE contract for the GetImageMetadata
// operation: the underlying single-manifest route (GetManifestRoute),
// GetImageMetadata's own request/response contract (GetImageMetadataReq/
// ManifestMetadata and their codecs), and its MCP tool declaration
// (GetImageMetadataTool). Pure data — no I/O, no *http.Client, no
// manifest-list resolution logic. The concrete implementation
// (app/registry/internal.RegistryClient.GetImageMetadata — the shared
// mechanics every self-contained Client's own GetImageMetadata method
// forwards to — and its PlatformMatches/FormatPlatformSelector helpers
// and NestedManifestListError/PlatformNotFoundError error types) lives in
// the sibling app/registry package tree, built on top of this file's
// declarations; the registry-agnostic, imageURL-resolving convenience —
// including the ready-made
// NewGetImageMetadataToolHandler() method that BINDS GetImageMetadataTool
// to that implementation — lives one level up in
// app/registry.Dispatcher instead. See this package's doc.go for the
// full models/ vs app/ rationale.

// ── GetManifestReq / GetManifestRoute ──────────────────────────────────────────

// GetManifestReq is GetManifestRoute's request — Name and Reference merge
// automatically into the {name}/{reference} path variables via
// nethttp.Call/rest.CallWithTransport.
type GetManifestReq struct {
	Name      string
	Reference string
}

// ManifestMediaType is one of the OCI Distribution Spec / Docker
// Registry HTTP API v2 media types GetManifestRoute advertises support
// for via its Accept header (below) — a genuine, codec-declared ENUM
// (ManifestMediaTypeCodec, via validate.OneOf), matching the established
// named-string-enum pattern models/azure/iothub.SchemaVersionCodec uses,
// rather than an opaque joined string blob.
type ManifestMediaType string

const (
	// DockerManifestV2Type is a Docker Distribution Manifest V2 Schema 2
	// single-platform manifest.
	DockerManifestV2Type ManifestMediaType = "application/vnd.docker.distribution.manifest.v2+json"
	// OCIManifestV1Type is an OCI Image Manifest single-platform manifest.
	OCIManifestV1Type ManifestMediaType = "application/vnd.oci.image.manifest.v1+json"
	// DockerManifestListV2Type is a Docker Manifest List (multi-arch).
	DockerManifestListV2Type ManifestMediaType = "application/vnd.docker.distribution.manifest.list.v2+json"
	// OCIImageIndexV1Type is an OCI Image Index (multi-arch).
	OCIImageIndexV1Type ManifestMediaType = "application/vnd.oci.image.index.v1+json"
)

// ManifestMediaTypeCodec validates a single media-type string against the
// 4 known, supported values above — an ENUM (validate.OneOf), not an
// open string.
var ManifestMediaTypeCodec = c.MapCodecSafe(
	c.String().Refine(v.OneOf(
		string(DockerManifestV2Type), string(OCIManifestV1Type),
		string(DockerManifestListV2Type), string(OCIImageIndexV1Type),
	)),
	func(s string) ManifestMediaType { return ManifestMediaType(s) },
	func(m ManifestMediaType) (string, error) { return string(m), nil },
)

// SupportedManifestMediaTypes is the FIXED, always-sent list
// GetManifestRoute's Accept header advertises — every entry is a
// ManifestMediaTypeCodec-valid enum member, so the registry can return
// whichever shape is appropriate for the requested reference.
var SupportedManifestMediaTypes = []ManifestMediaType{
	DockerManifestV2Type, OCIManifestV1Type, DockerManifestListV2Type, OCIImageIndexV1Type,
}

// acceptHeaderCodec encodes/decodes the Accept header's comma-separated
// media-type list to/from []ManifestMediaType — each element individually
// validated via ManifestMediaTypeCodec, matching Accept's real HTTP
// semantics (a LIST of acceptable media types, not a single value). The
// comma-only (no space) join matches this registry API's own convention,
// preserved byte-for-byte from this module's original hand-built Accept
// header value.
var acceptHeaderCodec = c.Codec[[]ManifestMediaType]{
	Encode: func(types []ManifestMediaType) (any, error) {
		parts := make([]string, len(types))
		for i, t := range types {
			raw, err := ManifestMediaTypeCodec.Encode(t)
			if err != nil {
				return nil, err
			}
			parts[i] = raw.(string)
		}
		return strings.Join(parts, ","), nil
	},
	Decode: func(raw any) ([]ManifestMediaType, error) {
		s, ok := raw.(string)
		if !ok {
			return nil, fmt.Errorf("expected string, got %T", raw)
		}
		parts := strings.Split(s, ",")
		result := make([]ManifestMediaType, len(parts))
		for i, p := range parts {
			mt, err := ManifestMediaTypeCodec.Decode(strings.TrimSpace(p))
			if err != nil {
				return nil, err
			}
			result[i] = mt
		}
		return result, nil
	},
	Schema: schema.Schema{Type: "string", Items: &ManifestMediaTypeCodec.Schema},
}

// AcceptManifestTypesDeclaration attaches GetManifestRoute's fixed Accept
// header, declared via the SAME codec-backed Middleware mechanism
// BearerAuthDeclaration uses — but UNLIKE Bearer auth (which genuinely
// needs PER-CLIENT credential injection, hence deferred attachment in
// app/registry), the Accept header is a TRUE CONSTANT, identical for
// every client. It is therefore attached directly HERE, at declaration
// time (GetManifestRoute's own .Use(...) below), rather than deferred —
// every caller of GetManifestRoute gets it automatically, with no
// per-client/per-call wiring needed. Carries no real In/Out — WithSend's
// Fn always returns the zero struct{}{}; SupportedManifestMediaTypes
// (not a per-call value) is what actually gets merged into the header.
var AcceptManifestTypesDeclaration = rest.NewMiddleware(
	rest.NewDeclaration[struct{}, struct{}]("acceptManifestTypes", c.Struct[struct{}](), c.Struct[struct{}]()),
).
	WithRequestHeader(rest.NewRequiredHeaderParam("Accept", acceptHeaderCodec,
		func(struct{}) []ManifestMediaType { return SupportedManifestMediaTypes },
		func(*struct{}, []ManifestMediaType) {},
	)).
	WithSend(func(context.Context) (struct{}, error) { return struct{}{}, nil })

// GetManifestRoute is GET /v2/{name}/manifests/{reference} — fetches a
// manifest (single-platform) or a manifest list / OCI image index,
// dispatched automatically by util.ManifestEnvelopeCodec based on the
// response shape. {reference} is a tag or a digest. Req is GetManifestReq,
// whose Name/Reference fields merge into {name}/{reference} automatically
// via nethttp.Call/rest.CallWithTransport. Resp additionally merges the
// Docker-Content-Digest RESPONSE HEADER directly into
// util.ManifestEnvelope.Digest via rest.NewRequiredResponseHeaderParam
// — nethttp.Call/rest.CallWithTransport applies this merge automatically on every
// successful (2xx) response, so app/registry/internal.RegistryClient's
// own GetImageMetadata never needs a manual HTTP call just to read that
// header.
//
// Resp is util.ManifestEnvelope — a consumer calling
// GetManifestRoute.ClientHandle() directly (bypassing
// app/registry.Client/app/registry.Dispatcher's GetImageMetadata
// convenience) receives a value of this type.
// Since app/registry/internal is a true Go internal package, that type
// cannot be NAMED from outside this module's go-edge-models tree — but
// its EXPORTED fields (Digest, Single, List) remain readable via
// ordinary Go type inference (e.g. `env, _ := nethttp.Call(...);
// env.Digest`). This is intentional: GetManifestRoute stays usable
// standalone for advanced/low-level cases, while the internal package
// boundary makes it unambiguous that the raw envelope shape is plumbing
// — GetImageMetadata is the supported, fully resolved public result.
//
// The registry's response media type is negotiated via the Accept request
// header — AcceptManifestTypesDeclaration (above) attaches it directly to
// this route (.Use(...) below), sending all four supported media types
// (Docker Schema 2 manifest, OCI manifest, Docker manifest list, OCI
// image index) so the registry can return whichever shape is appropriate
// for {reference}. Declared via a codec-backed Middleware (not a plain
// rest.HeaderParam on GetManifestReq itself) specifically so the 4 valid
// values stay a genuine ENUM (ManifestMediaTypeCodec) rendered into the
// generated spec, without polluting GetManifestReq with a field that
// isn't genuine request business data.
//
// GetManifestRoute does NOT attach BearerAuthDeclaration itself — same
// rationale as GetTagsRoute (see its own doc comment): a reusable
// Middleware[In,Out]'s Fn travels EMBEDDED in the SAME .Use() call
// (docs/design/d-0003-codec-declared-middlewares.md's Addendum 7), so a caller chains
// .Use(BearerAuthDeclaration.WithSend(credentialFn)) before calling
// .ClientHandle() to both declare the "bearerAuth" requirement AND
// authenticate outgoing calls. AcceptManifestTypesDeclaration, by
// contrast, IS attached here directly — unlike Bearer auth, it carries no
// per-client credential, so there is nothing for a caller to supply via
// .WithSend(...): the SAME fixed value applies to every caller.
var GetManifestRoute = rest.NewRoute[GetManifestReq, util.ManifestEnvelope](
	"GET", "/v2/{name}/manifests/{reference}",
	c.Struct[GetManifestReq](), util.ManifestEnvelopeCodec,
	rest.RouteMeta{
		OperationID:    "getManifest",
		Summary:        "Fetch a manifest or manifest list for a repository reference",
		RespSchemaName: "ManifestEnvelope",
	},
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
		util.DigestCodec,
		func(e util.ManifestEnvelope) string { return e.Digest },
		func(e *util.ManifestEnvelope, v string) { e.Digest = v },
	).WithDescription("The manifest's own content digest"),
).Use(AcceptManifestTypesDeclaration)

// ── GetImageMetadata's own request/response contract ─────────────────────────
//
// GetImageMetadataReq/ManifestMetadata are NOT GetManifestRoute's Req/Resp
// types — GetImageMetadata (app/registry/internal.RegistryClient's own
// GetImageMetadata, which every self-contained Client's GetImageMetadata
// method forwards to) is a multi-call CLIENT-SIDE orchestration, not one
// dialable HTTP endpoint: it parses ImageURL into an ImageRef, then calls
// GetManifestRoute up to TWICE (once to resolve a
// multi-arch manifest list / OCI image index to a single platform, once
// more for that platform's actual manifest), and reduces the result into
// a computed summary (TotalSizeBytes sums every layer's size; per-layer
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
// — app/registry.Dispatcher's GetImageMetadata method constructs/
// reads these types directly in Go (no wire encode/decode actually
// happens for them at runtime), but
// the codecs remain available for a caller who wants to serialize a
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
// ImageURL is required (app/registry.Dispatcher's GetImageMetadata
// method further validates it via ImageRefCodec.Decode at call time;
// this codec only checks non-empty since the FULL image-reference shape
// isn't yet known here);
// Platform is optional (see GetImageMetadataReq.Platform's own doc
// comment for the default).
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

// ── GetImageMetadata as an MCP tool ─────────────────────────────────────────────
//
// GetImageMetadataTool declares the MCP contract for GetImageMetadata —
// reuses GetImageMetadataReq/ManifestMetadata AND their codecs directly
// (already a single, registry-agnostic struct shape — no separate
// tool-request type needed, unlike GetTagsToolReq).
// app/registry.Dispatcher's own NewGetImageMetadataToolHandler()
// method binds this declared tool to its concrete, registry-agnostic
// GetImageMetadata method — NOT GetManifestRoute directly, since there's
// no single fixed REST endpoint to bridge, and GetImageMetadata may issue
// up to two HTTP calls per
// invocation for manifest-list resolution. ToolMeta.Description tells an
// LLM client WHEN to call this tool; GetImageMetadataReqCodec's own
// per-field .WithDescription (above) tells it WHAT to pass for
// imageURL/platform — both flow into the JSON Schema an MCP client shows
// the LLM.
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
