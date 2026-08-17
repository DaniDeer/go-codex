package usecase

import (
	deviceconfig "github.com/DaniDeer/go-codex/examples/go-edge-models/models/iotedge/deviceconfig"
	f "github.com/DaniDeer/go-codex/format"
	"github.com/DaniDeer/go-codex/ports"
)

// ── Device model ──────────────────────────────────────────────────────────────
//
// This file holds EVERYTHING that describes "what is a device" one
// level down from a use case: its file/dir port CONSTRUCTORS
// ([NewDeviceFile]/[NewDeviceDir]/[ListDeviceIDs]), and the domain-level
// composition ([DeviceConfig]/[ReadDeviceConfig]/[WriteDeviceConfig])
// that pairs a device with its PURE [deviceconfig.Manifest] wire
// content. Mirrors [usecase.go]'s file+dir+composition-in-one-file
// pattern exactly, consolidated into a single file for the same reason:
// a device is this package's SECOND aggregate, nested inside a use case.
//
// See config.go for the filesystem-layout constants this file's port
// constructors are built from (deviceFilePathPattern,
// deviceDirPathPattern, deviceEntryShape, useCaseNameVar, deviceIDVar),
// the raw Codec[string] validators (nameCodec/deviceIDCodec) used
// below, and the exported [DeviceID] type/[DeviceIDCodec]/[NewDeviceID]
// this file's Go-level API is typed with. See the sibling
// models/iotedge/deviceconfig package for the pure WIRE model
// (deviceconfig.Manifest/ManifestCodec) [NewDeviceFile] consumes —
// everything here is DERIVED/CONSTRUCTED on top of it.

// ── DeviceFile ────────────────────────────────────────────────────────────────

// DeviceFileFormat is the declared, reusable format/codec pairing for a
// device manifest file — mirrors [FileFormat]'s role.
var DeviceFileFormat = f.JSON(deviceconfig.ManifestCodec)

// NewDeviceFile declares the templated file port for ONE device's
// manifest under basePath — "{basePath}/devices/{usecase_name}/{device_id}.json"
// — a thin, pure (no I/O) constructor over ports.NewFile, using
// DeviceFileFormat. usecase_name AND device_id are both validated via
// PLAIN (non-merge) [ports.FilePathParam]s — [deviceconfig.Manifest]
// stays pure wire/file content; neither var is merged into it.
//
// The returned port is reused for both reading (File.Read) and writing
// (File.Write) — see [ReadDeviceConfig]/[WriteDeviceConfig] for the
// higher-level convenience that pairs a Manifest with its DeviceID into
// one [DeviceConfig] value.
func NewDeviceFile(basePath string) ports.File[deviceconfig.Manifest] {
	return ports.NewFile[deviceconfig.Manifest](basePath+"/"+deviceFilePathPattern, DeviceFileFormat,
		ports.FilePathParam{Name: useCaseNameVar, Codec: &nameCodec},
		ports.FilePathParam{Name: deviceIDVar, Codec: &deviceIDCodec},
	)
}

// ── DeviceDir ─────────────────────────────────────────────────────────────────

// DeviceDirEntryPattern declares the filename SHAPE for device manifest
// files inside ONE use case's device directory: each file is one
// device, and the filename (minus ".json") IS that device's ID — e.g.
// "sensor-42.json" is device "sensor-42"'s manifest. Mirrors
// [DirEntryPattern]'s role exactly, one level down.
var DeviceDirEntryPattern = ports.EntryPattern{
	Template: deviceEntryShape,
	Params:   []ports.EntryParam{{Name: deviceIDVar, Codec: &deviceIDCodec}},
}

// NewDeviceDir declares the directory-listing port for ONE use case's
// devices under basePath — "{basePath}/devices/{usecase_name}" — mirrors
// [NewDir]'s shape: a thin, pure (no I/O) constructor over
// [ports.NewDir]. usecase_name is a [ports.DirPathParam] SUPPLIED by the
// caller at [ports.Dir.List] time (narrowing the listing to that one use
// case's device directory) — plain named-var substitution, no glob/
// wildcard needed. [ports.WithEntryPattern] then discovers which
// devices exist and extracts each one's ID.
//
// The returned port is read-only (listing has no write/patch operation)
// — pair its result with [NewDeviceFile]/[ReadDeviceConfig] to read a
// SPECIFIC discovered device's manifest; see [ListDeviceIDs].
func NewDeviceDir(basePath string) ports.Dir {
	return ports.NewDir(basePath+"/"+deviceDirPathPattern,
		ports.DirPathParam{Name: useCaseNameVar, Codec: &nameCodec},
		ports.WithEntryPattern(DeviceDirEntryPattern),
	)
}

// ListDeviceIDs returns every discovered device_id for useCaseName under
// basePath's devices directory — a thin convenience wrapping
// [NewDeviceDir] + [ports.Dir.List], extracting each entry's captured
// "device_id" var.
func ListDeviceIDs(basePath string, useCaseName Name, opts ports.DirOptions) ([]DeviceID, error) {
	entries, err := NewDeviceDir(basePath).List(map[string]string{useCaseNameVar: string(useCaseName)}, opts)
	if err != nil {
		return nil, err
	}
	ids := make([]DeviceID, len(entries))
	for i, e := range entries {
		ids[i] = DeviceID(e.Vars[deviceIDVar])
	}
	return ids, nil
}

// ── DeviceConfig ──────────────────────────────────────────────────────────────

// DeviceConfig is the domain-level composition struct for ONE device: its
// ID (from the file's own path, not its body) paired with its PURE
// [deviceconfig.Manifest] wire content. Mirrors [UseCase]'s composition
// role one level up.
type DeviceConfig struct {
	DeviceID       DeviceID
	DeviceManifest deviceconfig.Manifest
}

// ReadDeviceConfig reads deviceID's manifest under basePath/useCaseName
// and wraps it into a [DeviceConfig], one call.
func ReadDeviceConfig(basePath string, useCaseName Name, deviceID DeviceID, opts ports.FileOptions) (DeviceConfig, error) {
	manifest, err := NewDeviceFile(basePath).Read(map[string]string{
		useCaseNameVar: string(useCaseName),
		deviceIDVar:    string(deviceID),
	}, opts)
	if err != nil {
		return DeviceConfig{}, err
	}
	return DeviceConfig{DeviceID: deviceID, DeviceManifest: manifest}, nil
}

// WriteDeviceConfig writes cfg.DeviceManifest at
// "basePath/devices/{useCaseName}/{cfg.DeviceID}.json" — the inverse of
// [ReadDeviceConfig], one call.
func WriteDeviceConfig(basePath string, useCaseName Name, cfg DeviceConfig, opts ports.FileOptions) (createdDirs []string, err error) {
	return NewDeviceFile(basePath).Write(map[string]string{
		useCaseNameVar: string(useCaseName),
		deviceIDVar:    string(cfg.DeviceID),
	}, cfg.DeviceManifest, opts)
}
