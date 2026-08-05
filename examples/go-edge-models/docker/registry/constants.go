package registry

// This file holds the package-level constants shared by client.go
// (image-reference parsing / manifest fetch defaults) — extracted
// alongside the client.go/auth.go and errors.go splits so that all
// "declared once, used everywhere" literal values live in one
// predictable place instead of being buried inside the logic that
// consumes them.

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

	// ghcrRegistryHost is GitHub Container Registry's host.
	ghcrRegistryHost = "ghcr.io"
	// mcrRegistryHost is Microsoft Container Registry's host.
	mcrRegistryHost = "mcr.microsoft.com"
)

// knownRegistryHosts lists every registry host WithCredentialsByRegistry/
// RegistryCredentialsCodec accepts as a map key — exactly the registries
// this package has been proven against end-to-end (see
// registry_integration_test.go). This is the SOLE source of truth
// RegistryCredentialsCodec's key constraint is built from (via
// validate.OneOf(knownRegistryHosts...)) — add a new registry here (and
// prove it end-to-end) to expand what WithCredentialsByRegistry accepts;
// there is no separate list to keep in sync.
var knownRegistryHosts = []string{dockerHubRegistryHost, ghcrRegistryHost, mcrRegistryHost}

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
