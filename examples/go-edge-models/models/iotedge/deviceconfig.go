package iotedge

import (
	"github.com/DaniDeer/go-codex/ports"
)

// ── DeviceConfig ──────────────────────────────────────────────────────────────

// DeviceConfig is the domain-level composition struct for ONE device: its
// ID (from the file's own path, not its body) paired with its PURE
// [DeviceManifest] wire content. Mirrors [UseCase]'s composition role one
// level down.
type DeviceConfig struct {
	DeviceID       string
	DeviceManifest DeviceManifest
}

// ReadDeviceConfig reads deviceID's manifest under basePath/useCaseName
// and wraps it into a [DeviceConfig], one call.
func ReadDeviceConfig(basePath, useCaseName, deviceID string, opts ports.FileOptions) (DeviceConfig, error) {
	manifest, err := NewDeviceFile(basePath).Read(map[string]string{
		"usecase_name": useCaseName,
		"device_id":    deviceID,
	}, opts)
	if err != nil {
		return DeviceConfig{}, err
	}
	return DeviceConfig{DeviceID: deviceID, DeviceManifest: manifest}, nil
}

// WriteDeviceConfig writes cfg.DeviceManifest at
// "basePath/devices/{useCaseName}/{cfg.DeviceID}.json" — the inverse of
// [ReadDeviceConfig], one call.
func WriteDeviceConfig(basePath, useCaseName string, cfg DeviceConfig, opts ports.FileOptions) (createdDirs []string, err error) {
	return NewDeviceFile(basePath).Write(map[string]string{
		"usecase_name": useCaseName,
		"device_id":    cfg.DeviceID,
	}, cfg.DeviceManifest, opts)
}
