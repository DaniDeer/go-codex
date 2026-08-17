package deviceconfig

import (
	c "github.com/DaniDeer/go-codex/codex"
	v "github.com/DaniDeer/go-codex/validate"
)

// ── Manifest ──────────────────────────────────────────────────────────────────

// Manifest is the PURE wire/file content for ONE device's device-specific
// config — a small, minimal illustrative override document (mirrors
// manifesttemplate.DeploymentManifest's "pure content" role at the
// device level). It carries NO identity fields (no device ID, no use
// case name) — those live in the file's own PATH, not its body; see
// models/iotedge/usecase's DeviceConfig for the domain-level composition
// that pairs a Manifest with its DeviceID.
type Manifest struct {
	// DisplayName is a human-friendly label for this device.
	DisplayName string
	// Enabled reports whether this device is currently active.
	Enabled bool
}

// ManifestCodec validates a Manifest value.
var ManifestCodec = c.Struct[Manifest](
	c.RequiredField("displayName", c.String().Refine(v.NonEmptyString),
		func(d Manifest) string { return d.DisplayName },
		func(d *Manifest, val string) { d.DisplayName = val },
	),
	c.RequiredField("enabled", c.Bool(),
		func(d Manifest) bool { return d.Enabled },
		func(d *Manifest, val bool) { d.Enabled = val },
	),
)
