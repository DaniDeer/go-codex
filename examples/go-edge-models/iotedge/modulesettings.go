package iotedge

import (
	"encoding/json"

	c "github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/examples/go-edge-models/docker"
	f "github.com/DaniDeer/go-codex/format"
)

// ── ModuleSettings ────────────────────────────────────────────────────────────

// ModuleSettings holds a module's image reference and its Docker
// create-options document.
type ModuleSettings struct {
	// Image is the module's parsed container image reference — on the
	// wire it is a plain string (e.g. "ghcr.io/org/repo:1.2.3"); see
	// ImageCodec (which re-exports docker.ImageCodec) for the parse/
	// format/validate story. A docker.Image prints back to that same
	// plain string via its Stringer implementation.
	Image docker.Image
	// CreateOptions is a Docker create-options document. On the wire it is a
	// JSON-escaped STRING (e.g. "{\"ExposedPorts\":{...}}") —
	// CreateOptionsFieldCodec handles the string-escaping transparently, so
	// this field is the fully-typed docker.CreateOptions value, not a raw
	// string.
	CreateOptions docker.CreateOptions
}

// ImageCodec is docker.ImageCodec re-exported under iotedge's own name —
// a module's "settings.image" wire field is a plain OCI image reference
// string, IDENTICAL in shape to docker.Image's own wire format, so no new
// parsing/validation logic is needed here: iotedge just reuses docker's
// codec directly (see docker.ImageCodec's doc comment for the full
// parse/format/validate story). Exported under this name so a caller
// assembling their own image-only codec (e.g. modulepatch's "patch this
// module's image" wire codec keyed by ModuleName) can reuse the exact
// same codec instead of re-deriving it — and so that iotedge's own
// vocabulary doesn't force callers to know the codec actually lives in
// docker.
var ImageCodec = docker.ImageCodec

// CreateOptionsFieldCodec decodes/encodes createOptions with the SAME
// JSON-in-string behavior as format.EmbeddedJSON(docker.CreateOptionsCodec),
// PLUS one tolerance format.EmbeddedJSON doesn't have: an empty string is
// accepted as equivalent to "{}" (the zero-value docker.CreateOptions{}) —
// some deployment manifests ship createOptions:"" instead of omitting the
// field or writing "{}". json.Unmarshal([]byte(""), ...) is not valid JSON
// and would otherwise fail with format.EmbeddedDecodeError for every such
// module. Symmetric on encode: a zero-value docker.CreateOptions re-encodes
// back to "" (not "{}"), so round-tripping this exact document produces the
// same wire shape it started with, not a cosmetically different (but
// equivalent) "{}" string.
var CreateOptionsFieldCodec = c.MapCodecValidated(
	c.String(), docker.CreateOptionsCodec,
	func(s string) (docker.CreateOptions, error) {
		if s == "" {
			return docker.CreateOptions{}, nil
		}
		var raw any
		if err := json.Unmarshal([]byte(s), &raw); err != nil {
			return docker.CreateOptions{}, f.EmbeddedDecodeError{Format: "json", Err: err}
		}
		return docker.CreateOptionsCodec.Decode(raw)
	},
	func(co docker.CreateOptions) (string, error) {
		if docker.IsZeroCreateOptions(co) {
			return "", nil
		}
		intermediate, err := docker.CreateOptionsCodec.Encode(co)
		if err != nil {
			return "", err
		}
		b, err := json.Marshal(intermediate)
		if err != nil {
			return "", f.EmbeddedEncodeError{Format: "json", Err: err}
		}
		return string(b), nil
	},
)

var ModuleSettingsCodec = c.Struct[ModuleSettings](
	c.RequiredField("image",
		ImageCodec,
		func(ms ModuleSettings) docker.Image { return ms.Image },
		func(ms *ModuleSettings, v docker.Image) { ms.Image = v },
	),
	c.OptionalField("createOptions",
		CreateOptionsFieldCodec,
		func(ms ModuleSettings) docker.CreateOptions { return ms.CreateOptions },
		func(ms *ModuleSettings, val docker.CreateOptions) { ms.CreateOptions = val },
	),
)
