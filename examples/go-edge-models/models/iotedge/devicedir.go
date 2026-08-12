package iotedge

import (
	"github.com/DaniDeer/go-codex/ports"
)

// ── DeviceDir ─────────────────────────────────────────────────────────────────

// DeviceDirEntryPattern declares the filename SHAPE for device manifest
// files inside ONE use case's device directory: each file is one
// device, and the filename (minus ".json") IS that device's ID — e.g.
// "sensor-42.json" is device "sensor-42"'s manifest. Mirrors
// [ConfigDirEntryPattern]'s role exactly, one level down.
var DeviceDirEntryPattern = ports.EntryPattern{
	Template: "{device_id}.json",
	Params:   []ports.EntryParam{{Name: "device_id", Codec: &deviceIDCodec}},
}

// NewDeviceDir declares the directory-listing port for ONE use case's
// devices under basePath — "{basePath}/devices/{usecase_name}" — mirrors
// [NewConfigDir]'s shape: a thin, pure (no I/O) constructor over
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
	return ports.NewDir(basePath+"/devices/{usecase_name}",
		ports.DirPathParam{Name: "usecase_name", Codec: &useCaseNameCodec},
		ports.WithEntryPattern(DeviceDirEntryPattern),
	)
}

// ListDeviceIDs returns every discovered device_id for useCaseName under
// basePath's devices directory — a thin convenience wrapping
// [NewDeviceDir] + [ports.Dir.List], extracting each entry's captured
// "device_id" var.
func ListDeviceIDs(basePath, useCaseName string, opts ports.DirOptions) ([]string, error) {
	entries, err := NewDeviceDir(basePath).List(map[string]string{"usecase_name": useCaseName}, opts)
	if err != nil {
		return nil, err
	}
	ids := make([]string, len(entries))
	for i, e := range entries {
		ids[i] = e.Vars["device_id"]
	}
	return ids, nil
}
