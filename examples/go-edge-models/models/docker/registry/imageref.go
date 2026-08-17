package registry

import (
	"fmt"
	"log/slog"
	"strings"

	c "github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/examples/go-edge-models/models/docker"
	v "github.com/DaniDeer/go-codex/validate"
)

// ── Docker Hub defaults ───────────────────────────────────────────────────────
//
// Constants used only by this file's own parsing/formatting logic
// (parseImageRefString/splitDockerDomain/ImageRefFromImage).

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
	// reachable registry API host; "registry-1.docker.io" is. Also
	// referenced directly by credentials.go's knownRegistryHosts (same
	// package, no import needed).
	dockerHubRegistryHost = "registry-1.docker.io"
	// dockerHubOfficialPrefix is prepended to a single-segment repository
	// name resolved against Docker Hub (e.g. "alpine" -> "library/alpine").
	dockerHubOfficialPrefix = "library"
	// defaultReference is used when an image URL has neither a tag nor a
	// digest.
	defaultReference = "latest"
)

// ── ImageRef ──────────────────────────────────────────────────────────────────

// ImageRef is a parsed container image reference: registry host, repository
// path, and reference (a tag or a digest).
type ImageRef struct {
	// Registry is the registry host (and optional port), e.g. "ghcr.io" or
	// "registry-1.docker.io" (the Docker Hub default when the input image
	// URL has no explicit registry host).
	Registry string
	// Repository is the repository path, e.g. "prometheus/prometheus"
	// or "library/alpine" (the Docker Hub "library/" default for
	// single-segment repository names).
	Repository string
	// Reference is a tag (e.g. "latest") or a digest (e.g. "sha256:...").
	Reference string
}

// imageRefStructCodec validates each of ImageRef's own fields once parsed.
var imageRefStructCodec = c.Struct[ImageRef](
	c.RequiredField("registry", c.String().Refine(v.NonEmptyString),
		func(r ImageRef) string { return r.Registry },
		func(r *ImageRef, val string) { r.Registry = val },
	),
	c.RequiredField("repository", c.String().Refine(v.NonEmptyString),
		func(r ImageRef) string { return r.Repository },
		func(r *ImageRef, val string) { r.Repository = val },
	),
	c.RequiredField("reference", c.String().Refine(v.NonEmptyString),
		func(r ImageRef) string { return r.Reference },
		func(r *ImageRef, val string) { r.Reference = val },
	),
)

// ImageRefCodec decodes/encodes a container image reference string
// ("ghcr.io/org/repo:tag", "alpine:latest", "org/repo@sha256:...") to/from
// the structured ImageRef — the codec-based replacement for what used to
// be an ad-hoc parser: the wire codec validates the overall shape
// (validate.ContainerImage), parseImageRefString/formatImageRefString do
// the actual field extraction/reconstruction (this file's own ParseImageRef
// is a thin wrapper around ImageRefCodec.Decode), and imageRefStructCodec
// validates the parsed fields — the exact three-layer MapCodecValidated
// pattern docker.BindCodec already established for
// "host:container[:mode]" bind-mount specs.
var ImageRefCodec = c.MapCodecValidated(
	c.String().Refine(v.ContainerImage), imageRefStructCodec,
	parseImageRefString, formatImageRefString,
)

// Codec implements [codex.HasCodec][ImageRef], returning [ImageRefCodec].
func (ImageRef) Codec() c.Codec[ImageRef] { return ImageRefCodec }

// NewImageRef is a named per-field smart constructor: validates
// registry/repository/reference (all non-empty, AND the reconstructed
// "registry/repository:reference" or "registry/repository@reference"
// string must itself be a valid container image reference — both layers
// run via ImageRefCodec.New) and returns the constructed ImageRef, or the
// zero value and the first failing constraint's error.
//
// NewImageRef complements — it does NOT replace — [ParseImageRef]:
// ParseImageRef parses a full raw image URL string (applying Docker Hub's
// default-registry/"library/" conventions along the way); NewImageRef is
// for a caller that already has the three parts in hand (e.g. building a
// ref against a specific known registry, or test fixtures) and wants them
// validated together without hand-formatting a string first.
func NewImageRef(registry, repository, reference string) (ImageRef, error) {
	return ImageRefCodec.New(ImageRef{Registry: registry, Repository: repository, Reference: reference})
}

// parseImageRefString splits raw into registry/repository/reference,
// applying Docker Hub's default-registry + "library/" prefix convention
// (see splitDockerDomain) and defaulting Reference to "latest" when
// neither a tag nor a digest is present.
func parseImageRefString(raw string) (ImageRef, error) {
	name := raw
	var digest string
	if i := strings.Index(name, "@"); i != -1 {
		digest = name[i+1:]
		name = name[:i]
	}

	var tag string
	lastSlash := strings.LastIndex(name, "/")
	lastColon := strings.LastIndex(name, ":")
	if lastColon > lastSlash {
		tag = name[lastColon+1:]
		name = name[:lastColon]
	}

	domain, repository := splitDockerDomain(name)

	reference := tag
	if reference == "" {
		reference = digest
	}
	if reference == "" {
		reference = defaultReference
	}

	return ImageRef{Registry: domain, Repository: repository, Reference: reference}, nil
}

// formatImageRefString reconstructs an image reference string from ref —
// the Encode-direction round trip for ImageRefCodec. Reference is
// reassembled as "@digest" when it contains ":" (only digests do; Docker
// tags never contain ":"), otherwise as ":tag".
func formatImageRefString(ref ImageRef) (string, error) {
	sep := ":"
	if strings.Contains(ref.Reference, ":") {
		sep = "@"
	}
	return ref.Registry + "/" + ref.Repository + sep + ref.Reference, nil
}

// ── Image mapper (registry.ImageRef <-> docker.Image) ─────────────────────────
//
// ImageRef and docker.Image model the SAME underlying concept — a
// container image reference — but with genuinely different shapes: this
// package's ImageRef splits the registry host out separately (required to
// build the HTTP base URL for a SPECIFIC registry's API) and combines
// tag-or-digest into one Reference field (the URL path segment
// GetManifestRoute needs); docker.Image keeps any registry host embedded
// in Name and splits Tag/Digest apart. Because the shapes genuinely
// differ, ImageRefCodec is NOT replaced by docker.ImageCodec (unlike
// manifesttemplate.ImageCodec, which re-exports docker.ImageCodec directly because
// its wire shape is identical) — instead, these two functions MAP between
// the two representations.

// ToImage converts r to the general docker.Image domain type — Registry
// is folded back into Name as a prefix (Docker's own convention: a full
// image reference embeds any registry host directly in the repository
// path), and Reference is reinterpreted as Tag or Digest using the SAME
// heuristic formatImageRefString already uses in reverse: a digest has an
// "algorithm:hex" form (contains ":"); anything else is a tag.
func (r ImageRef) ToImage() docker.Image {
	name := r.Repository
	if r.Registry != "" {
		name = r.Registry + "/" + r.Repository
	}
	if strings.Contains(r.Reference, ":") {
		return docker.Image{Name: name, Digest: docker.Digest(r.Reference)}
	}
	return docker.Image{Name: name, Tag: docker.Tag(r.Reference)}
}

// ImageRefFromImage builds an ImageRef for registryHost from a plain
// docker.Image — the reverse mapping, for a caller that already has a
// generic Image (e.g. one decoded from an iotedge manifest) and wants to
// query a SPECIFIC registry's HTTP API for it. img.Name is re-split via
// splitDockerDomain (the SAME Docker-Hub-normalization
// parseImageRefString itself uses) to strip any embedded registry prefix
// and apply the "library/" single-segment convention — registryHost is
// the caller's explicit, authoritative choice of which registry to query,
// and always wins over whatever (if anything) was embedded in img.Name.
func ImageRefFromImage(img docker.Image, registryHost string) ImageRef {
	_, repository := splitDockerDomain(img.Name)

	reference := string(img.Tag)
	if reference == "" {
		reference = string(img.Digest)
	}
	if reference == "" {
		reference = defaultReference
	}

	return ImageRef{Registry: registryHost, Repository: repository, Reference: reference}
}

// ── ParseImageRef / FormatImageRef ─────────────────────────────────────────────

// ParseImageRef parses a container image reference into its registry host,
// repository path, and tag/digest reference — replicating Docker Hub's
// default-registry + "library/" prefix convention: an image URL with no
// explicit registry host (e.g. "alpine:latest") resolves to
// Registry="registry-1.docker.io", Repository="library/alpine"; an image
// URL with no tag or digest (e.g. "alpine") defaults Reference to "latest".
//
// This is a thin wrapper around ImageRefCodec.Decode — the actual parsing
// logic lives in the codec above, kept as a public convenience entry
// point so callers don't need to reach for the codec directly. Returns
// ImageRefParseError wrapping the codec's own validation/parse error.
func ParseImageRef(raw string) (ImageRef, error) {
	ref, err := ImageRefCodec.Decode(raw)
	if err != nil {
		return ImageRef{}, ImageRefParseError{Input: raw, Err: err}
	}
	return ref, nil
}

// FormatImageRef reconstructs an image reference string from ref — a thin
// wrapper around ImageRefCodec.Encode, the Encode-direction counterpart
// of ParseImageRef. Exported so callers building test fixtures, mock
// servers, or their own tooling around ImageRef can construct a valid
// image reference string without hand-concatenating
// "registry/repository:reference" themselves.
func FormatImageRef(ref ImageRef) (string, error) {
	raw, err := ImageRefCodec.Encode(ref)
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
// the "library/" prefix. Used by this file's own parseImageRefString (the
// to-direction of ImageRefCodec) and ImageRefFromImage.
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
