package iotedge

import (
	c "github.com/DaniDeer/go-codex/codex"
	f "github.com/DaniDeer/go-codex/format"
	"github.com/DaniDeer/go-codex/ports"
	v "github.com/DaniDeer/go-codex/validate"
)

// ── DeviceManifest / DeviceFile ───────────────────────────────────────────────

// DeviceManifest is the PURE wire/file content for ONE device's
// device-specific config — a small, minimal illustrative override
// document (mirrors [DeploymentManifest]'s "pure content" role at the
// device level). It carries NO identity fields (no device ID, no use
// case name) — those live in the file's own PATH, not its body; see
// [DeviceConfig] for the domain-level composition that pairs a
// DeviceManifest with its DeviceID.
type DeviceManifest struct {
	// DisplayName is a human-friendly label for this device.
	DisplayName string
	// Enabled reports whether this device is currently active.
	Enabled bool
}

// DeviceManifestCodec validates a DeviceManifest value.
var DeviceManifestCodec = c.Struct[DeviceManifest](
	c.RequiredField("displayName", c.String().Refine(v.NonEmptyString),
		func(d DeviceManifest) string { return d.DisplayName },
		func(d *DeviceManifest, val string) { d.DisplayName = val },
	),
	c.RequiredField("enabled", c.Bool(),
		func(d DeviceManifest) bool { return d.Enabled },
		func(d *DeviceManifest, val bool) { d.Enabled = val },
	),
)

// DeviceManifestFormat is the declared, reusable format/codec pairing for
// a device manifest file — mirrors [ConfigFileFormat]'s role.
var DeviceManifestFormat = f.JSON(DeviceManifestCodec)

// deviceIDCodec validates the {device_id} segment extracted from a
// device file's path — reuses the same NonEmptyString constraint
// [useCaseNameCodec] applies to use case names.
var deviceIDCodec = c.String().Refine(v.NonEmptyString)

// NewDeviceFile declares the templated file port for ONE device's
// manifest under basePath — "{basePath}/devices/{usecase_name}/{device_id}.json"
// — a thin, pure (no I/O) constructor over ports.NewFile, using
// DeviceManifestFormat. usecase_name AND device_id are both validated via
// PLAIN (non-merge) [ports.FilePathParam]s — [DeviceManifest] stays pure
// wire/file content; neither var is merged into it.
//
// The returned port is reused for both reading (File.Read) and writing
// (File.Write) — see [ReadDeviceConfig]/[WriteDeviceConfig] for the
// higher-level convenience that pairs a DeviceManifest with its DeviceID
// into one [DeviceConfig] value.
func NewDeviceFile(basePath string) ports.File[DeviceManifest] {
	return ports.NewFile[DeviceManifest](basePath+"/devices/{usecase_name}/{device_id}.json", DeviceManifestFormat,
		ports.FilePathParam{Name: "usecase_name", Codec: &useCaseNameCodec},
		ports.FilePathParam{Name: "device_id", Codec: &deviceIDCodec},
	)
}
