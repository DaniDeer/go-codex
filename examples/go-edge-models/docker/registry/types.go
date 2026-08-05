package registry

// ── Public domain types ──────────────────────────────────────────────────────

// ImageRef is a parsed container image reference: registry host, repository
// path, and reference (a tag or a digest).
type ImageRef struct {
	// Registry is the registry host (and optional port), e.g. "ghcr.io" or
	// "registry-1.docker.io" (the Docker Hub default when the input image
	// URL has no explicit registry host).
	Registry string
	// Repository is the repository path, e.g. "bosch-cc-mfd/edge-gateway"
	// or "library/alpine" (the Docker Hub "library/" default for
	// single-segment repository names).
	Repository string
	// Reference is a tag (e.g. "latest") or a digest (e.g. "sha256:...").
	Reference string
}

// TagsList is the decoded response body of GET /v2/<name>/tags/list.
type TagsList struct {
	Name string
	Tags []string
}

// GetTagsReq is GetTagsRoute's request — Name merges automatically into
// the {name} path variable via nethttp.CallHandle (see routes.go).
type GetTagsReq struct {
	Name string
}

// GetManifestReq is GetManifestRoute's request — Name and Reference merge
// automatically into the {name}/{reference} path variables via
// nethttp.CallHandle (see routes.go).
type GetManifestReq struct {
	Name      string
	Reference string
}

// GetImageMetadataReq is the input to GetImageMetadata.
type GetImageMetadataReq struct {
	// ImageURL is the raw image reference string, e.g.
	// "ghcr.io/bosch-cc-mfd/edge-gateway:0.12.5.0" or "alpine:latest".
	ImageURL string
	// Platform selects which platform-specific manifest to resolve when
	// ImageURL points at a multi-arch manifest list / OCI image index,
	// formatted "os/arch" (e.g. "linux/amd64"). Defaults to "linux/amd64"
	// when empty.
	Platform string
}

// ManifestMetadata is the lean, caller-facing result of GetImageMetadata —
// deliberately excludes per-layer detail (config digest, individual layer
// digests/sizes). If the resolved image reference pointed at a multi-arch
// manifest list / OCI image index, this is the metadata of the SINGLE
// platform-specific manifest GetImageMetadata transparently resolved to
// (see GetImageMetadataReq.Platform) — the caller never sees the list/index
// shape itself.
type ManifestMetadata struct {
	SchemaVersion int
	MediaType     string
	// Digest is the manifest's own content digest, taken from the
	// Docker-Content-Digest response header (NOT part of the manifest
	// body itself — the registry computes it as the sha256 of the exact
	// bytes returned).
	Digest string
	// TotalSizeBytes is Config.Size plus the sum of every entry in
	// Layers[].Size — the total on-disk size Docker would need to pull
	// this image, without exposing the individual layer breakdown.
	TotalSizeBytes int64
}

// Credentials supplies Basic-auth credentials for the auth-token exchange
// step (getTokenRoute) — needed for private repositories on registries
// that require Basic auth to mint a Bearer token, e.g. a private GHCR
// package authenticated with a GitHub username + a PAT with
// read:packages scope. Anonymous/public pulls need no Credentials at
// all — passing WithCredentials is purely additive; GetTags/
// GetImageMetadata behave exactly as before when it is omitted.
type Credentials struct {
	Username string
	Password string
}
