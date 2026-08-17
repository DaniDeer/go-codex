package docker

import (
	"fmt"
	"regexp"
	"strings"

	c "github.com/DaniDeer/go-codex/codex"
	v "github.com/DaniDeer/go-codex/validate"
)

// ── Image ─────────────────────────────────────────────────────────────────────

// Image is a parsed container image reference — the general
// "which-image, at what tag/digest" domain type shared by every package
// in this module that has to work with a plain image reference string
// (iotedge's module settings, docker/registry's Image mapper, etc.). See
// [ImageCodec] for the wire string <-> Image codec.
type Image struct {
	// Name is the image's repository path, e.g. "alpine" or
	// "ghcr.io/org/repo" — any registry host stays embedded in Name
	// (Docker's own convention: the Engine API has no separate registry
	// field, unlike docker/registry's own ImageRef, which DOES split the
	// registry host out because its HTTP API needs it separately).
	Name string
	// Tag is the image's tag (e.g. "latest", "1.2.3") — optional; Docker
	// itself defaults an absent tag to "latest" without requiring a
	// caller to spell it out.
	Tag Tag
	// Digest is the image's content-addressable digest (e.g.
	// "sha256:...") — optional; a digest-pinned reference may have no tag
	// at all.
	Digest Digest
}

// Tag is a validated Docker image tag (e.g. "latest", "1.2.3") — the
// shared domain type for any package that needs to work with a bare tag
// string, not only as part of a full [Image] reference. See
// [registry.TagsList.Tags], which reuses Tag directly (a registry's
// `GET /tags/list` response is a list of the SAME concept).
type Tag string

// Digest is a validated content-addressable digest in "algorithm:hex"
// form (e.g. "sha256:..."), the OCI/Docker Distribution Spec convention —
// the shared domain type for any package that needs to work with a bare
// digest string, not only as part of a full [Image] reference. See
// [registry.ManifestMetadata.Digest], which reuses Digest directly (a
// manifest's own content-addressable digest is the SAME concept).
type Digest string

// String formats i back into a plain image reference string
// ("name[:tag][@digest]") — implements [fmt.Stringer] so i prints
// sensibly via %s/%q/%v without callers needing to know about
// [ImageCodec]. Never fails: an [Image] that reached this point already
// passed [ImageCodec]'s validation (Name non-empty), the only way
// formatting could fail.
func (i Image) String() string {
	s, _ := formatImage(i)
	return s
}

// Codec implements [codex.HasCodec][Image], returning [ImageCodec] — lets
// the generic codex.Validate/New/EncodeSelf/DecodeAs/SchemaOf helpers work
// on Image without repeating ImageCodec's name at the call site.
func (Image) Codec() c.Codec[Image] { return ImageCodec }

// NewImage is a named per-field smart constructor: validates name/tag/digest
// via ImageCodec.New (Name non-empty; Tag/Digest format-checked when
// non-empty) and returns the constructed Image, or the zero value and the
// first failing constraint's error.
func NewImage(name string, tag Tag, digest Digest) (Image, error) {
	return ImageCodec.New(Image{Name: name, Tag: tag, Digest: digest})
}

// NewImageFromStr is a convenience wrapper around ImageCodec.Decode: parses
// a plain image reference string ("name[:tag][@digest]") into the structured
// Image, validating Name non-empty and Tag/Digest format when present. Returns
// the zero value and an error if validation fails.
func NewImageFromStr(image string) (Image, error) {
	return ImageCodec.Decode(image)
}

// reTag matches a Docker image tag — the SAME tag-segment shape
// validate.ContainerImage itself checks after ":" (`[\w][\w\.\-]{0,127}`,
// the Docker Distribution Spec's tag format), extracted here as its own
// standalone constraint since Image.Tag validates a tag on its own, not
// only as an optional suffix of a full image reference. Kept
// package-local (unlike Digest, which was promoted to validate.Digest) —
// there is no OTHER package in this module that needs a bare-tag
// constraint independent of a full image reference today.
var reTag = regexp.MustCompile(`^[\w][\w.\-]{0,127}$`)

// tagConstraint validates a bare Docker image tag's shape. Empty string
// passes too — Image.Tag is optional (an absent tag defaults to
// "latest" without a caller spelling it out), so the zero value must
// stay valid for Encode-time re-validation (imageStructCodec's
// OptionalField still calls Encode unconditionally; only Decode skips
// entirely-absent keys).
var tagConstraint = c.Constraint[string]{
	Name:  "docker-tag",
	Check: func(s string) bool { return s == "" || reTag.MatchString(s) },
	Message: func(s string) string {
		return fmt.Sprintf("invalid image tag %q: must match [\\w][\\w.-]{0,127}", s)
	},
}

// digestConstraint validates a bare Docker/OCI content digest, reusing
// validate.Digest's own "algorithm:hex" check. Empty string passes too —
// Image.Digest is optional (a tag-only reference has no digest), for the
// same Encode-time re-validation reason tagConstraint documents above.
var digestConstraint = c.Constraint[string]{
	Name:  v.Digest.Name,
	Check: func(s string) bool { return s == "" || v.Digest.Check(s) },
	Message: func(s string) string {
		return v.Digest.Message(s)
	},
}

// tagFieldCodec/digestFieldCodec wrap the bare-string tagConstraint/
// digestConstraint into the named Tag/Digest domain types — the SAME
// MapCodecSafe pattern iotedge's own Type/Status/Version codecs use (see
// iotedge/lifecycle.go): the decode direction (string -> named type) is
// infallible casting, the encode direction (named type -> string) is
// infallible too since Tag/Digest are just string under the hood.
var tagFieldCodec = c.MapCodecSafe(
	c.String().Refine(tagConstraint),
	func(s string) Tag { return Tag(s) },
	func(t Tag) (string, error) { return string(t), nil },
)

var digestFieldCodec = c.MapCodecSafe(
	c.String().Refine(digestConstraint),
	func(s string) Digest { return Digest(s) },
	func(d Digest) (string, error) { return string(d), nil },
)

// imageStructCodec validates Image's own fields once parsed — Name is
// required (the repository path); Tag and Digest are both optional (see
// Image's own field doc comments for why) but, when present, must match
// their respective constraint (tagConstraint/digestConstraint above).
var imageStructCodec = c.Struct[Image](
	c.RequiredField("name", c.String().Refine(v.NonEmptyString),
		func(i Image) string { return i.Name },
		func(i *Image, val string) { i.Name = val },
	),
	c.OptionalField("tag", tagFieldCodec,
		func(i Image) Tag { return i.Tag },
		func(i *Image, val Tag) { i.Tag = val },
	),
	c.OptionalField("digest", digestFieldCodec,
		func(i Image) Digest { return i.Digest },
		func(i *Image, val Digest) { i.Digest = val },
	),
)

// ImageCodec decodes/encodes a container image reference string
// ("alpine", "alpine:3.18", "ghcr.io/org/repo@sha256:...",
// "ghcr.io/org/repo:1.2.3@sha256:...") to/from the structured Image — the
// SAME three-layer MapCodecValidated pattern BindCodec (bind.go) and
// docker/registry's own ImageRefCodec both use: the wire codec validates
// the overall shape (validate.ContainerImage), parseImage/formatImage do
// the field extraction/reconstruction, and imageStructCodec validates the
// parsed fields.
//
// manifesttemplate.ImageCodec re-exports this codec directly (IoT Edge's module
// settings "image" field is the SAME plain image-reference string) rather
// than defining its own — see manifest-template/modulesettings.go's ImageCodec doc
// comment.
var ImageCodec = c.MapCodecValidated(
	c.String().Refine(v.ContainerImage), imageStructCodec,
	parseImage, formatImage,
)

// parseImage splits raw into Name/Tag/Digest — the SAME lastSlash/
// lastColon technique docker/registry's own parseImageRefString uses,
// minus the registry-host split (Name keeps any registry prefix
// embedded; see Image.Name's own doc comment for why).
func parseImage(raw string) (Image, error) {
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

	return Image{Name: name, Tag: Tag(tag), Digest: Digest(digest)}, nil
}

// formatImage reconstructs an image reference string from img — the
// Encode-direction round trip for ImageCodec (and Image.String()'s own
// implementation).
func formatImage(img Image) (string, error) {
	if img.Name == "" {
		return "", fmt.Errorf("docker: Image.Name must not be empty")
	}
	s := img.Name
	if img.Tag != "" {
		s += ":" + string(img.Tag)
	}
	if img.Digest != "" {
		s += "@" + string(img.Digest)
	}
	return s, nil
}
