package registry

import (
	"strings"

	c "github.com/DaniDeer/go-codex/codex"
	v "github.com/DaniDeer/go-codex/validate"
)

// ── ImageRef ──────────────────────────────────────────────────────────────────

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
// the actual field extraction/reconstruction (client.go's ParseImageRef is
// now a thin wrapper around ImageRefCodec.Decode), and imageRefStructCodec
// validates the parsed fields — the exact three-layer MapCodecValidated
// pattern docker.BindCodec already established for
// "host:container[:mode]" bind-mount specs.
var ImageRefCodec = c.MapCodecValidated(
	c.String().Refine(v.ContainerImage), imageRefStructCodec,
	parseImageRefString, formatImageRefString,
)

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

// ── Credentials / RegistryCredentials ─────────────────────────────────────────

// CredentialsCodec validates a Credentials value — Password must be
// non-empty; Username is OPTIONAL: some registries (e.g. GHCR with a
// personal access token) authenticate correctly with an empty/arbitrary
// username and the actual token carried entirely in Password, so
// Username is deliberately not constrained here. Lets a caller decode
// Credentials/RegistryCredentials from an external config file (JSON/
// YAML/TOML) via format.<Format>(CredentialsCodec)/(RegistryCredentialsCodec).
var CredentialsCodec = c.Struct[Credentials](
	c.OptionalField("username", c.String(),
		func(cr Credentials) string { return cr.Username },
		func(cr *Credentials, val string) { cr.Username = val },
	),
	c.RequiredField("password", c.String().Refine(v.NonEmptyString),
		func(cr Credentials) string { return cr.Password },
		func(cr *Credentials, val string) { cr.Password = val },
	),
)

// RegistryCredentialsCodec validates a RegistryCredentials map. Keys are
// restricted via validate.OneOf(knownRegistryHosts...) to the registries
// this package is proven against end-to-end — registry-1.docker.io
// (Docker Hub's actual API host), ghcr.io, mcr.microsoft.com. See
// RegistryCredentials' doc comment for the WithCredentials escape hatch
// when a different registry is needed.
var RegistryCredentialsCodec = c.Map(
	c.String().Refine(v.OneOf(knownRegistryHosts...)),
	CredentialsCodec,
)

// ── TagsList ──────────────────────────────────────────────────────────────────

// TagsListCodec is the canonical codec for the GET /v2/<name>/tags/list
// response body.
var TagsListCodec = c.Struct[TagsList](
	c.RequiredField("name", c.String(),
		func(t TagsList) string { return t.Name },
		func(t *TagsList, v string) { t.Name = v },
	),
	// OptionalField: some registries omit "tags" entirely for a repository
	// with zero tags, rather than returning an empty array.
	c.OptionalField("tags", c.SliceOf(c.String()),
		func(t TagsList) []string { return t.Tags },
		func(t *TagsList, v []string) { t.Tags = v },
	),
)
