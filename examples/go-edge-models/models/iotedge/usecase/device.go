package usecase

import (
	"context"

	"github.com/DaniDeer/go-codex/api/file"
	"github.com/DaniDeer/go-codex/codex"
	iothub "github.com/DaniDeer/go-codex/examples/go-edge-models/models/azure/iothub"
	deviceconfig "github.com/DaniDeer/go-codex/examples/go-edge-models/models/iotedge/deviceconfig"
	finaldeviceconfig "github.com/DaniDeer/go-codex/examples/go-edge-models/models/iotedge/finaldeviceconfig"
	f "github.com/DaniDeer/go-codex/format"
)

// ── Device model ──────────────────────────────────────────────────────────────
//
// This file holds EVERYTHING that describes "what is a device" one
// level down from a use case: its file/dir declarations
// ([deviceFileResource]/[deviceDirResource]), and the domain-level
// composition ([DeviceConfig]/[Store.ReadDeviceConfig]/
// [Store.WriteDeviceConfig]) that pairs a device with its PURE
// [deviceconfig.Patch] wire content — a device config file is itself a
// patch over its use case's own template, not a standalone document;
// see [DeviceConfig.Merge] for producing the FINAL, layered config
// (delegates to the sibling models/iotedge/finaldeviceconfig package).
// Mirrors [usecase.go]'s file+dir+composition-in-one-file pattern
// exactly, consolidated into a single file for the same reason: a
// device is this package's SECOND aggregate, nested inside a use case.
//
// Stage D2 (receiver-style migration to api/file.Client — see
// usecase.go's own top doc comment and store.go for the full design):
// every former free function taking basePath as its own, per-call
// parameter (NewDeviceFile/NewDeviceDir/ListDeviceIDs/ReadDeviceConfig/
// WriteDeviceConfig/ReadEffective) is now a [*Store] METHOD, same
// parameters otherwise. [DeviceConfig.Merge] is UNCHANGED — pure data
// merge, no I/O, nothing to migrate.
//
// See config.go for the filesystem-layout constants this file's
// declarations are built from (deviceFilePathPattern,
// deviceDirPathPattern, deviceEntryShape, useCaseNameVar, deviceIDVar),
// the raw Codec[string] validators (nameCodec/deviceIDCodec) used
// below, and the exported [DeviceID] type/[DeviceIDCodec]/
// [NewDeviceID] this file's Go-level API is typed with. See the
// sibling models/iotedge/deviceconfig package for the pure WIRE model
// (deviceconfig.Patch/PatchCodec) [deviceFileResource] consumes —
// everything here is DERIVED/CONSTRUCTED on top of it.

// ── DeviceFile ────────────────────────────────────────────────────────────────

// DeviceFileFormat is the declared, reusable format/codec pairing for a
// device config file — mirrors [FileFormat]'s role.
var DeviceFileFormat = f.JSON(deviceconfig.PatchCodec)

// deviceFileResource is the package-level Resource declaration for ONE
// device's config — "devices/{usecase_name}/{device_id}.json",
// relative, no basePath baked in. usecase_name AND device_id are both
// validated via PLAIN (non-merge) [file.PathParam]s — [deviceconfig.Patch]
// stays pure wire/file content; neither var is merged into it.
//
// The declared roles built from this resource (WithRead/WithCreate/
// WithPatch) are reused for both reading (Store.ReadDeviceConfig),
// writing (Store.WriteDeviceConfig), and patching
// ([PatchDeviceFile]) — see those for the higher-level convenience
// that pairs a Patch with its DeviceID into one [DeviceConfig] value.
var deviceFileResource = file.NewResource[deviceconfig.Patch](
	deviceFilePathPattern.String(), DeviceFileFormat,
	file.PathParam{Name: useCaseNameVar}.WithCodec(nameCodec),
	file.PathParam{Name: deviceIDVar}.WithCodec(deviceIDCodec),
)

// ── DeviceDir ─────────────────────────────────────────────────────────────────

// DeviceDirEntryPattern declares the filename SHAPE for device manifest
// files inside ONE use case's device directory: each file is one
// device, and the filename (minus ".json") IS that device's ID — e.g.
// "sensor-42.json" is device "sensor-42"'s manifest. Mirrors
// [DirEntryPattern]'s role exactly, one level down.
var DeviceDirEntryPattern = file.EntryPattern{
	Template: deviceEntryShape.String(),
	Params:   []file.EntryParam{file.EntryParam{Name: deviceIDVar}.WithCodec(deviceIDCodec)},
}

// deviceDirResource is the package-level Dir declaration for ONE use
// case's devices — "devices/{usecase_name}", relative, no basePath
// baked in — mirrors [deviceFileResource]'s shape: a thin, pure (no
// I/O) declaration over [file.NewDir]. usecase_name is a
// [file.PathParam] SUPPLIED by the caller at [Store.ListDeviceIDs] time
// (narrowing the listing to that one use case's device directory) —
// plain named-var substitution, no glob/wildcard needed.
// [file.WithEntryPattern] then discovers which devices exist and
// extracts each one's ID.
var deviceDirResource = file.NewDir(deviceDirPathPattern.String(),
	file.PathParam{Name: useCaseNameVar}.WithCodec(nameCodec),
	file.WithEntryPattern(DeviceDirEntryPattern),
)

// ReadDeviceFile reads deviceID's raw config patch under
// basePath/useCaseName — the low-level access [PatchDeviceFile] builds
// on; see [Store.ReadDeviceConfig] for the higher-level convenience
// that wraps the result into a [DeviceConfig].
func (s *Store) ReadDeviceFile(ctx context.Context, basePath BasePath, useCaseName Name, deviceID DeviceID) (deviceconfig.Patch, error) {
	handle, err := deviceFileResource.WithRead(file.Read{}).Handle(s.client, file.WithRouter(routerFor(basePath)))
	if err != nil {
		return deviceconfig.Patch{}, err
	}
	v, err := s.client.Read(ctx, handle, map[string]string{
		useCaseNameVar: string(useCaseName),
		deviceIDVar:    string(deviceID),
	})
	if err != nil {
		return deviceconfig.Patch{}, err
	}
	return v.(deviceconfig.Patch), nil
}

// PatchDeviceFile applies a typed partial update (via patchCodec) to
// deviceID's config file under basePath/useCaseName — the Stage D2
// equivalent of the former ports.PatchEncoded(usecase.NewDeviceFile(basePath),
// ...) call external callers (app/iotedge.PatchDeviceModule) used
// directly. A FREE FUNCTION, not a *Store method — same "Go methods
// cannot introduce a new type parameter" reason as [PatchUseCaseFile].
func PatchDeviceFile[P any](ctx context.Context, s *Store, basePath BasePath, useCaseName Name, deviceID DeviceID, patchCodec codex.Codec[P], patch P, opts file.WriteOptions) error {
	handle, err := file.WithPatch(deviceFileResource, patchCodec, file.Patch{}).Handle(s.client, file.WithRouter(routerFor(basePath)))
	if err != nil {
		return err
	}
	return s.client.Patch(ctx, handle, patch, map[string]string{
		useCaseNameVar: string(useCaseName),
		deviceIDVar:    string(deviceID),
	}, opts)
}

// ListDeviceIDs returns every discovered device_id for useCaseName under
// basePath's devices directory — a thin convenience wrapping
// [deviceDirResource]'s declared List role, extracting each entry's
// captured "device_id" var.
func (s *Store) ListDeviceIDs(ctx context.Context, basePath BasePath, useCaseName Name, opts file.DirOptions) ([]DeviceID, error) {
	handle, err := deviceDirResource.WithList(file.ListDir{}).Handle(s.client, file.WithRouter(routerFor(basePath)))
	if err != nil {
		return nil, err
	}
	entries, err := s.client.ListDir(ctx, handle, map[string]string{useCaseNameVar: string(useCaseName)}, opts)
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
// [deviceconfig.Patch] wire content. Mirrors [UseCase]'s composition
// role one level up.
type DeviceConfig struct {
	DeviceID DeviceID
	Patch    deviceconfig.Patch
}

// ReadDeviceConfig reads deviceID's config under basePath/useCaseName
// and wraps it into a [DeviceConfig], one call.
func (s *Store) ReadDeviceConfig(ctx context.Context, basePath BasePath, useCaseName Name, deviceID DeviceID) (DeviceConfig, error) {
	patch, err := s.ReadDeviceFile(ctx, basePath, useCaseName, deviceID)
	if err != nil {
		return DeviceConfig{}, err
	}
	return DeviceConfig{DeviceID: deviceID, Patch: patch}, nil
}

// WriteDeviceConfig writes cfg.Patch at
// "basePath/devices/{useCaseName}/{cfg.DeviceID}.json" — the inverse of
// [Store.ReadDeviceConfig], one call.
func (s *Store) WriteDeviceConfig(ctx context.Context, basePath BasePath, useCaseName Name, cfg DeviceConfig, opts file.WriteOptions) (createdDirs []string, err error) {
	handle, err := deviceFileResource.WithCreate(file.Create{}).Handle(s.client, file.WithRouter(routerFor(basePath)))
	if err != nil {
		return nil, err
	}
	return s.client.Create(ctx, handle, cfg.Patch, map[string]string{
		useCaseNameVar: string(useCaseName),
		deviceIDVar:    string(cfg.DeviceID),
	}, opts)
}

// Merge layers base (the GLOBAL baseline deployment) and template (the
// use case's own manifest), then cfg's own Patch on top, returning the
// FINAL, deployable-to-IoT-Hub iothub.BaseDeployment for this ONE device —
// the "one call" convenience for "baseline + template + device config,
// layered on top". Delegates to the sibling
// models/iotedge/finaldeviceconfig package's Merge function.
//
// UNCHANGED by Stage D2 — pure data merge, no I/O, no *Store needed.
func (cfg DeviceConfig) Merge(base iothub.BaseDeployment, template iothub.LayeredDeployment) (iothub.BaseDeployment, error) {
	return finaldeviceconfig.Merge(base, template, cfg.Patch)
}

// ReadEffective reads the GLOBAL baseline, useCaseName's template, AND
// deviceID's config, merging all three into ONE effective
// iothub.BaseDeployment — "baseline + template + device config, layered on
// top" — in a single call. Thin composition of Store.ReadBaseline +
// Store.ReadUseCaseFile + Store.ReadDeviceConfig + DeviceConfig.Merge;
// no new merge logic of its own.
//
// A device that has NEVER had a config file written for it (this
// package's ONLY signal for "does this device exist" — there is no
// separate device registry, matching [Store.ListDeviceIDs]'s own
// discover-by-file model) is NOT an error here: its effective config is
// simply baseline+template merged with an EMPTY device patch, since "no
// overrides yet" IS this device's current, valid state — not a caller
// mistake. Any OTHER read error (a missing/invalid BASELINE/TEMPLATE, or
// an existing-but-malformed device file) still propagates as-is.
//
// Checks via [IsNotFound] — NOT errors.Is(err, os.ErrNotExist), the
// pre-Stage-D2 check this function used when built on ports.File.Read
// (which has ALWAYS translated a not-found condition into
// FileReadError{Err: os.ErrNotExist} specifically to make that check
// work — see ports.File.Read's own doc comment). api/file.Client.Read,
// which Store.ReadDeviceConfig now dispatches through directly,
// reports not-found as a bare file.FileNotFoundError with no wrapped
// cause at all — confirmed via a live failing test during Stage D2's
// own rewrite; [IsNotFound] encapsulates that detail.
func (s *Store) ReadEffective(ctx context.Context, basePath BasePath, useCaseName Name, deviceID DeviceID) (iothub.BaseDeployment, error) {
	base, err := s.ReadBaseline(ctx, basePath)
	if err != nil {
		return iothub.BaseDeployment{}, err
	}
	template, err := s.ReadUseCaseFile(ctx, basePath, useCaseName)
	if err != nil {
		return iothub.BaseDeployment{}, err
	}
	cfg, err := s.ReadDeviceConfig(ctx, basePath, useCaseName, deviceID)
	switch {
	case err == nil:
		return cfg.Merge(base, template)
	case IsNotFound(err):
		return finaldeviceconfig.Merge(base, template, deviceconfig.Patch{})
	default:
		return iothub.BaseDeployment{}, err
	}
}
