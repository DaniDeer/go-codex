package internal

// These mirror the Docker Distribution / OCI Distribution Spec JSON shapes
// exactly enough to decode real registry responses — codecs.go composes
// them; the parent registry package never constructs them directly except
// via codec Decode/Encode.

// ManifestDescriptor is the shared shape for a manifest's "config" field,
// each entry in "layers", and each entry in a manifest list's "manifests"
// array (a manifest-list entry additionally carries "platform").
type ManifestDescriptor struct {
	MediaType string
	Digest    string
	Size      int64
	Platform  *PlatformDescriptor
}

// PlatformDescriptor identifies one platform-specific manifest within a
// manifest list / OCI image index.
type PlatformDescriptor struct {
	Architecture string
	OS           string
	// Variant disambiguates ARM variants (e.g. "v7", "v8"); empty for most
	// platforms.
	Variant string
}

// SingleManifestWire mirrors a Docker Distribution Manifest V2 Schema 2 or
// OCI Image Manifest response body — both share this exact JSON shape.
type SingleManifestWire struct {
	SchemaVersion int
	MediaType     string
	Config        ManifestDescriptor
	Layers        []ManifestDescriptor
}

// ManifestListWire mirrors a Docker Manifest List or OCI Image Index
// response body — both share this exact JSON shape.
type ManifestListWire struct {
	SchemaVersion int
	MediaType     string
	Manifests     []ManifestDescriptor
}

// ManifestEnvelope holds EXACTLY ONE of a single manifest or a manifest
// list — the wire shape's own required fields ("config"/"layers" vs
// "manifests") make the two branches mutually exclusive, so this mirrors
// iotedge.EnvVarValue's pointer-discriminator pattern: nil-vs-non-nil is
// the signal, decoded via a try-in-order UntaggedUnion (codecs.go).
//
// Digest is a PEER of Single/List, not nested inside either — it is
// populated from the Docker-Content-Digest RESPONSE HEADER via the parent
// package's routes.go GetManifestRoute response-header merge field
// (rest.NewRequiredResponseHeaderParam), never from the JSON body, so it
// applies regardless of which of Single/List the body decoded to.
type ManifestEnvelope struct {
	Digest string
	Single *SingleManifestWire
	List   *ManifestListWire
}

// TokenResponse is the decoded response body of the registry token
// endpoint. Registries vary between "token" and "access_token" as the key
// name for the same value — both are modeled and callers should use
// whichever is non-empty.
type TokenResponse struct {
	Token       string
	AccessToken string
	ExpiresIn   int
}

// Challenge is a parsed WWW-Authenticate Bearer challenge (RFC 6750 / the
// Docker Distribution auth spec), decoded/encoded via ChallengeCodec
// (codecs.go).
type Challenge struct {
	Realm   string
	Service string
	Scope   string
}

// PlatformSelector is a parsed "os/arch" platform selector (e.g.
// "linux/amd64"), decoded/encoded via PlatformSelectorCodec (codecs.go).
type PlatformSelector struct {
	OS           string
	Architecture string
}

// DockerScope is a parsed Docker Distribution auth scope, e.g.
// "repository:library/alpine:pull" — the format request/response bodies
// and query parameters carry when negotiating pull/push permissions for a
// resource. Decoded/encoded via DockerScopeCodec (codecs.go).
type DockerScope struct {
	// ResourceType is the resource kind, e.g. "repository".
	ResourceType string
	// Name is the resource name, e.g. "library/alpine".
	Name string
	// Actions is the requested permissions, e.g. ["pull"], ["pull","push"].
	Actions []string
}

// BasicCredentials is a username/password pair encoded/decoded to/from an
// HTTP Basic "Authorization" header value via BasicAuthCodec (codecs.go).
// Needed for the auth-token exchange on registries/repositories that
// require Basic auth to mint a Bearer token (e.g. a private GHCR package,
// authenticated with a GitHub username + a PAT with read:packages scope).
type BasicCredentials struct {
	Username string
	Password string
}
