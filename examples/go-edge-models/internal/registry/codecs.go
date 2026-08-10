package internal

import (
	"encoding/base64"
	"fmt"
	"net/http"
	"strings"

	c "github.com/DaniDeer/go-codex/codex"
	v "github.com/DaniDeer/go-codex/validate"
)

// PlatformCodec validates the "os/arch" shape of a platform selector.
var PlatformCodec = c.String().Refine(PlatformConstraint)

// DigestCodec validates a bare content digest's "algorithm:hex" shape —
// used for ManifestDescriptor.Digest (wire body field) and
// ManifestEnvelope.Digest (response-header merge field, see the parent
// package's getimagemetadata.go), so every digest value flows through the same
// constraint.
var DigestCodec = c.String().Refine(DigestConstraint)

// ── Challenge (WWW-Authenticate) ─────────────────────────────────────────────

// challengeStructCodec validates Challenge's Realm field once parsed —
// Service/Scope are OptionalField since real registries vary in which of
// the two they actually populate in a challenge.
var challengeStructCodec = c.Struct[Challenge](
	c.RequiredField("realm", c.String().Refine(v.URI),
		func(ch Challenge) string { return ch.Realm },
		func(ch *Challenge, val string) { ch.Realm = val },
	),
	c.OptionalField("service", c.String(),
		func(ch Challenge) string { return ch.Service },
		func(ch *Challenge, val string) { ch.Service = val },
	),
	c.OptionalField("scope", c.String(),
		func(ch Challenge) string { return ch.Scope },
		func(ch *Challenge, val string) { ch.Scope = val },
	),
)

// ChallengeCodec decodes/encodes a WWW-Authenticate Bearer challenge
// header value to/from the structured Challenge.
var ChallengeCodec = c.MapCodecValidated(
	c.String(), challengeStructCodec,
	ParseChallengeString, FormatChallengeString,
)

// headerCodec is a trivial passthrough Codec[http.Header] — Decode
// type-asserts the input as http.Header (an in-memory Go value, not a
// JSON document, so there is nothing to parse at this layer); it exists
// purely so WWWAuthenticateCodec below can compose via the SAME
// MapCodecValidated pattern every other codec in this package uses.
var headerCodec = c.Codec[http.Header]{
	Decode: func(v any) (http.Header, error) {
		h, ok := v.(http.Header)
		if !ok {
			return nil, fmt.Errorf("expected http.Header, got %T", v)
		}
		return h, nil
	},
	Encode: func(h http.Header) (any, error) { return h, nil },
}

// WWWAuthenticateCodec decodes a Challenge DIRECTLY from an http.Header
// set — extracting the "WWW-Authenticate" entry and delegating to the
// SAME ParseChallengeString/FormatChallengeString ChallengeCodec itself
// uses — so header extraction is a single codec Decode call, not a plain
// Header.Get(...) followed by a separate parse step.
var WWWAuthenticateCodec = c.MapCodecValidated(
	headerCodec, challengeStructCodec,
	func(h http.Header) (Challenge, error) { return ParseChallengeString(h.Get("WWW-Authenticate")) },
	func(ch Challenge) (http.Header, error) {
		s, err := FormatChallengeString(ch)
		if err != nil {
			return nil, err
		}
		h := make(http.Header, 1)
		h.Set("WWW-Authenticate", s) // Set canonicalizes the key, unlike a map literal.
		return h, nil
	},
)

// ── PlatformSelector ──────────────────────────────────────────────────────────

var platformSelectorStructCodec = c.Struct[PlatformSelector](
	c.RequiredField("os", c.String().Refine(v.NonEmptyString),
		func(p PlatformSelector) string { return p.OS },
		func(p *PlatformSelector, val string) { p.OS = val },
	),
	c.RequiredField("architecture", c.String().Refine(v.NonEmptyString),
		func(p PlatformSelector) string { return p.Architecture },
		func(p *PlatformSelector, val string) { p.Architecture = val },
	),
)

// PlatformSelectorCodec decodes/encodes an "os/arch" platform selector
// string to/from the structured PlatformSelector.
var PlatformSelectorCodec = c.MapCodecValidated(
	c.String().Refine(PlatformConstraint), platformSelectorStructCodec,
	func(s string) (PlatformSelector, error) {
		parts := strings.SplitN(s, "/", 2)
		return PlatformSelector{OS: parts[0], Architecture: parts[1]}, nil
	},
	func(p PlatformSelector) (string, error) {
		return p.OS + "/" + p.Architecture, nil
	},
)

// ── Manifest wire shapes ──────────────────────────────────────────────────────

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

// ── Token response ────────────────────────────────────────────────────────────

// TokenResponseCodec decodes/encodes the registry token endpoint's JSON
// body. Both Token and AccessToken are OptionalField — registries vary
// between the two key names for the same value (the parent package's
// auth.go picks whichever is non-empty).
var TokenResponseCodec = c.Struct[TokenResponse](
	c.OptionalField("token", c.String(),
		func(t TokenResponse) string { return t.Token },
		func(t *TokenResponse, v string) { t.Token = v },
	),
	c.OptionalField("access_token", c.String(),
		func(t TokenResponse) string { return t.AccessToken },
		func(t *TokenResponse, v string) { t.AccessToken = v },
	),
	c.OptionalField("expires_in", c.Int(),
		func(t TokenResponse) int { return t.ExpiresIn },
		func(t *TokenResponse, v int) { t.ExpiresIn = v },
	),
)

// ── DockerScope ───────────────────────────────────────────────────────────────

var dockerScopeStructCodec = c.Struct[DockerScope](
	c.RequiredField("resourceType", c.String().Refine(v.NonEmptyString),
		func(s DockerScope) string { return s.ResourceType },
		func(s *DockerScope, val string) { s.ResourceType = val },
	),
	c.RequiredField("name", c.String().Refine(v.NonEmptyString),
		func(s DockerScope) string { return s.Name },
		func(s *DockerScope, val string) { s.Name = val },
	),
	c.RequiredField("actions", c.SliceOf(c.String()).Refine(ActionsConstraint),
		func(s DockerScope) []string { return s.Actions },
		func(s *DockerScope, val []string) { s.Actions = val },
	),
)

// DockerScopeCodec decodes/encodes a Docker Distribution auth scope
// string ("repository:library/alpine:pull") to/from the structured
// DockerScope. Same three-layer MapCodecValidated pattern as
// ImageRefCodec/ChallengeCodec (parent package): the wire codec accepts
// any string, ParseDockerScopeString/FormatDockerScopeString do the
// actual field extraction/reconstruction, and dockerScopeStructCodec
// validates the parsed fields.
var DockerScopeCodec = c.MapCodecValidated(
	c.String(), dockerScopeStructCodec,
	ParseDockerScopeString, FormatDockerScopeString,
)

// ── Bearer token ──────────────────────────────────────────────────────────────

// BearerTokenCodec converts a bare token string to/from the
// "Bearer <token>" Authorization header value. The decode direction
// (stripping the prefix) is provided for symmetry/reuse even though this
// package currently only ever encodes (constructs outgoing Authorization
// headers); a server-side or verification caller could reuse the same
// codec to extract a bare token from an incoming header.
var BearerTokenCodec = c.MapCodecSafe(
	c.String(),
	func(s string) string { return strings.TrimPrefix(s, "Bearer ") },
	func(token string) (string, error) { return "Bearer " + token, nil },
)

// ── Basic auth ────────────────────────────────────────────────────────────────

// BasicAuthCodec converts a username/password pair to/from an HTTP Basic
// "Authorization" header value ("Basic <base64(username:password)>"). The
// decode direction is a best-effort inverse (base64-decode, split on the
// first ":") — provided for symmetry, same rationale as BearerTokenCodec;
// this package currently only ever encodes (constructs outgoing
// Authorization headers for the auth-token exchange).
var BasicAuthCodec = c.MapCodecSafe(
	c.String(),
	func(s string) BasicCredentials {
		encoded := strings.TrimPrefix(s, "Basic ")
		decoded, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil {
			return BasicCredentials{}
		}
		user, pass, ok := strings.Cut(string(decoded), ":")
		if !ok {
			return BasicCredentials{}
		}
		return BasicCredentials{Username: user, Password: pass}
	},
	func(creds BasicCredentials) (string, error) {
		encoded := base64.StdEncoding.EncodeToString([]byte(creds.Username + ":" + creds.Password))
		return "Basic " + encoded, nil
	},
)
