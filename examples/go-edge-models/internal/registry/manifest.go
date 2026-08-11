package internal

import (
	"fmt"

	c "github.com/DaniDeer/go-codex/codex"
	v "github.com/DaniDeer/go-codex/validate"
)

// This file holds the manifest/manifest-list envelope concept — the
// struct(s), constraints, and codecs mirroring the Docker Distribution /
// OCI Distribution Spec manifest JSON shapes closely enough to decode
// real registry responses. The parent registry package never constructs
// these directly except via codec Decode/Encode.

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
// the signal, decoded via a try-in-order UntaggedUnion (below).
//
// Digest is a PEER of Single/List, not nested inside either — it is
// populated from the Docker-Content-Digest RESPONSE HEADER via the parent
// package's getimagemetadata.go GetManifestRoute response-header merge field
// (rest.NewRequiredResponseHeaderParam), never from the JSON body, so it
// applies regardless of which of Single/List the body decoded to.
type ManifestEnvelope struct {
	Digest string
	Single *SingleManifestWire
	List   *ManifestListWire
}

// DigestConstraint validates a bare content digest's "algorithm:hex" shape
// — a thin re-export of validate.Digest under this package's own name
// (used for ManifestDescriptor.Digest and ManifestEnvelope.Digest, which
// appear as standalone fields, not only as an optional suffix of a full
// image reference like validate.ContainerImage checks).
var DigestConstraint = v.Digest

// DigestCodec validates a bare content digest's "algorithm:hex" shape —
// used for ManifestDescriptor.Digest (wire body field) and
// ManifestEnvelope.Digest (response-header merge field, see the parent
// package's getimagemetadata.go), so every digest value flows through the same
// constraint.
var DigestCodec = c.String().Refine(DigestConstraint)

// PlatformDescriptorCodec decodes/encodes the "platform" object nested in
// each manifest-list entry.
var PlatformDescriptorCodec = c.Struct[PlatformDescriptor](
	c.RequiredField("architecture", c.String(),
		func(p PlatformDescriptor) string { return p.Architecture },
		func(p *PlatformDescriptor, v string) { p.Architecture = v },
	),
	c.RequiredField("os", c.String(),
		func(p PlatformDescriptor) string { return p.OS },
		func(p *PlatformDescriptor, v string) { p.OS = v },
	),
	c.OptionalField("variant", c.String(),
		func(p PlatformDescriptor) string { return p.Variant },
		func(p *PlatformDescriptor, v string) { p.Variant = v },
	),
)

// ManifestDescriptorCodec decodes/encodes a "config" object, a "layers"
// entry, or a manifest-list "manifests" entry — all three share this exact
// shape; only manifest-list entries populate "platform".
var ManifestDescriptorCodec = c.Struct[ManifestDescriptor](
	c.RequiredField("mediaType", c.String(),
		func(d ManifestDescriptor) string { return d.MediaType },
		func(d *ManifestDescriptor, v string) { d.MediaType = v },
	),
	c.RequiredField("digest", DigestCodec,
		func(d ManifestDescriptor) string { return d.Digest },
		func(d *ManifestDescriptor, v string) { d.Digest = v },
	),
	c.RequiredField("size", c.Int64(),
		func(d ManifestDescriptor) int64 { return d.Size },
		func(d *ManifestDescriptor, v int64) { d.Size = v },
	),
	c.OptionalField("platform", PlatformDescriptorCodec,
		func(d ManifestDescriptor) PlatformDescriptor {
			if d.Platform == nil {
				return PlatformDescriptor{}
			}
			return *d.Platform
		},
		func(d *ManifestDescriptor, v PlatformDescriptor) { d.Platform = &v },
	),
)

// SingleManifestWireCodec decodes/encodes a Docker Distribution Manifest V2
// Schema 2 or OCI Image Manifest response body — both share this shape.
var SingleManifestWireCodec = c.Struct[SingleManifestWire](
	c.RequiredField("schemaVersion", c.Int(),
		func(m SingleManifestWire) int { return m.SchemaVersion },
		func(m *SingleManifestWire, v int) { m.SchemaVersion = v },
	),
	c.RequiredField("mediaType", c.String(),
		func(m SingleManifestWire) string { return m.MediaType },
		func(m *SingleManifestWire, v string) { m.MediaType = v },
	),
	c.RequiredField("config", ManifestDescriptorCodec,
		func(m SingleManifestWire) ManifestDescriptor { return m.Config },
		func(m *SingleManifestWire, v ManifestDescriptor) { m.Config = v },
	),
	c.RequiredField("layers", c.SliceOf(ManifestDescriptorCodec),
		func(m SingleManifestWire) []ManifestDescriptor { return m.Layers },
		func(m *SingleManifestWire, v []ManifestDescriptor) { m.Layers = v },
	),
)

// ManifestListWireCodec decodes/encodes a Docker Manifest List or OCI Image
// Index response body — both share this shape.
var ManifestListWireCodec = c.Struct[ManifestListWire](
	c.RequiredField("schemaVersion", c.Int(),
		func(m ManifestListWire) int { return m.SchemaVersion },
		func(m *ManifestListWire, v int) { m.SchemaVersion = v },
	),
	c.RequiredField("mediaType", c.String(),
		func(m ManifestListWire) string { return m.MediaType },
		func(m *ManifestListWire, v string) { m.MediaType = v },
	),
	c.RequiredField("manifests", c.SliceOf(ManifestDescriptorCodec),
		func(m ManifestListWire) []ManifestDescriptor { return m.Manifests },
		func(m *ManifestListWire, v []ManifestDescriptor) { m.Manifests = v },
	),
)

// singleManifestVariantCodec / listVariantCodec are ManifestEnvelopeCodec's
// two UntaggedUnion branches. Decode dispatch relies on the wire shape's
// OWN required fields: a manifest-list body has no "config"/"layers" (so
// SingleManifestWireCodec.Decode fails first), and a single-manifest body
// has no "manifests" (so ManifestListWireCodec.Decode fails) — the same
// try-in-order-until-one-succeeds pattern as iotedge.EnvVarValueCodec.
var singleManifestVariantCodec = c.MapCodecSafe(
	SingleManifestWireCodec,
	func(m SingleManifestWire) ManifestEnvelope { return ManifestEnvelope{Single: &m} },
	func(e ManifestEnvelope) (SingleManifestWire, error) {
		if e.Single == nil {
			return SingleManifestWire{}, fmt.Errorf("not a single manifest envelope")
		}
		return *e.Single, nil
	},
)

var listVariantCodec = c.MapCodecSafe(
	ManifestListWireCodec,
	func(m ManifestListWire) ManifestEnvelope { return ManifestEnvelope{List: &m} },
	func(e ManifestEnvelope) (ManifestListWire, error) {
		if e.List == nil {
			return ManifestListWire{}, fmt.Errorf("not a manifest-list envelope")
		}
		return *e.List, nil
	},
)

// ManifestEnvelopeCodec tries the single-manifest shape first, then the
// manifest-list shape. Anything matching neither (malformed body, or an
// unexpected third shape) fails both branches, returning codex.EitherError
// listing both underlying failures.
var ManifestEnvelopeCodec = c.UntaggedUnion[ManifestEnvelope](
	func(e ManifestEnvelope) int {
		if e.List != nil {
			return 1
		}
		return 0
	},
	c.UntaggedVariant[ManifestEnvelope]{Name: "manifest", Codec: singleManifestVariantCodec},
	c.UntaggedVariant[ManifestEnvelope]{Name: "manifestList", Codec: listVariantCodec},
)
