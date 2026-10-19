package iotedge

import (
	"context"
	"fmt"

	"github.com/DaniDeer/go-codex/api/file"
	"github.com/DaniDeer/go-codex/codex"
	iothub "github.com/DaniDeer/go-codex/examples/go-edge-models/models/azure/iothub"
	"github.com/DaniDeer/go-codex/examples/go-edge-models/models/docker"
	deviceconfig "github.com/DaniDeer/go-codex/examples/go-edge-models/models/iotedge/deviceconfig"
	"github.com/DaniDeer/go-codex/examples/go-edge-models/models/iotedge/modulepatch"
	"github.com/DaniDeer/go-codex/examples/go-edge-models/models/iotedge/modulesummary"
	"github.com/DaniDeer/go-codex/examples/go-edge-models/models/iotedge/usecase"
)

// ── ReadUseCase ───────────────────────────────────────────────────────────────

// ReadUseCase reads useCaseName's deployment manifest under basePath via
// store — a thin wrapper over models/iotedge/usecase's declared file
// resource. Named distinctly from [usecase.Store.Read] (which
// additionally reads every nested device into one composed
// usecase.UseCase value) — this function returns just the deployment
// manifest.
//
// Stage D2 (receiver-style migration to api/file.Client — see
// models/iotedge/usecase/store.go's own doc comment): every function in
// this file now takes a leading ctx+store pair instead of threading
// ports.FileOptions.Context/.Observer implicitly; opts becomes
// file.WriteOptions (the api/file equivalent, same
// Perm/CreateDirs/DirPerm/Strict/DryRun/Metadata/IfMatch fields).
func ReadUseCase(ctx context.Context, store *usecase.Store, basePath usecase.BasePath, useCaseName usecase.Name) (iothub.LayeredDeployment, error) {
	return store.ReadUseCaseFile(ctx, basePath, useCaseName)
}

// ── PatchUseCaseModule / UpdateUseCaseModuleImage ────────────────────────────

// PatchUseCaseModule applies patch — any subset of one module's fields
// (see modulepatch.FieldsPatch's own doc comment) — to useCaseName's
// deployment manifest under basePath, leaving every other field on that
// module, and every other module, untouched.
func PatchUseCaseModule(ctx context.Context, store *usecase.Store, basePath usecase.BasePath, useCaseName usecase.Name, patch modulepatch.FieldsPatch, opts file.WriteOptions) error {
	return usecase.PatchUseCaseFile(ctx, store, basePath, useCaseName, modulepatch.FieldsPatchCodec, patch, opts)
}

// UpdateUseCaseModuleImage updates ONE module's image — a thin
// convenience over [PatchUseCaseModule] for the single most common patch
// operation. The patch itself is built and validated by
// modulepatch.NewUpdateModuleImage (moduleName's slug shape and image's
// Name/Tag/Digest constraints are both checked there, before this ever
// touches disk).
//
// AUTO-PROMOTES to a FULL patch (modulepatch.NewUpdateModuleImageFromBase)
// when moduleName has no EXISTING entry in the template's OWN Modules
// map yet (it currently only resolves via
// models/azure/iothub's global base deployment) — a SPARSE
// patch deep-merged directly onto the template FILE (not the merged
// baseline+template VIEW) would otherwise produce an incomplete,
// unreadable entry there (missing status/restartPolicy/version). Returns
// ModuleNotFoundError if moduleName resolves via NEITHER the template
// NOR baseline (a genuinely brand-new module needs a caller-built FULL
// modulepatch.FieldsPatch via [PatchUseCaseModule] instead — this
// convenience always assumes an EXISTING config to update, one layer or
// another).
func UpdateUseCaseModuleImage(ctx context.Context, store *usecase.Store, basePath usecase.BasePath, useCaseName usecase.Name, moduleName iothub.ModuleName, image docker.Image, opts file.WriteOptions) error {
	template, err := ReadUseCase(ctx, store, basePath, useCaseName)
	if err != nil {
		return err
	}
	if _, ok := template.ModulesContent.EdgeAgent[moduleName]; ok {
		patch, err := modulepatch.NewUpdateModuleImage(moduleName, image)
		if err != nil {
			return err
		}
		return PatchUseCaseModule(ctx, store, basePath, useCaseName, patch, opts)
	}

	base, err := store.ReadBaseline(ctx, basePath)
	if err != nil {
		return err
	}
	baseModule, ok := base.ModulesContent.EdgeAgent.Modules[moduleName]
	if !ok {
		return ModuleNotFoundError{ModuleName: moduleName}
	}
	patch, err := modulepatch.NewUpdateModuleImageFromBase(moduleName, baseModule, image)
	if err != nil {
		return err
	}
	return PatchUseCaseModule(ctx, store, basePath, useCaseName, patch, opts)
}

// ── PatchDeviceModule / UpdateDeviceModuleImage ──────────────────────────────
//
// The DEVICE-scoped analogues of PatchUseCaseModule/UpdateUseCaseModuleImage:
// instead of patching the use case's shared TEMPLATE, these write into
// ONE device's OWN config file — isolated and reversible, never
// touching the template or any other device.

// PatchDeviceModule applies patch — any subset of one module's fields
// (see modulepatch.FieldsPatch's own doc comment) — to deviceID's OWN
// config file under basePath/useCaseName, leaving the use case template
// and every OTHER device completely untouched. Reuses
// modulepatch.FieldsBodyCodec to encode patch's typed fields into the
// SAME raw shape deviceconfig.Patch.EdgeAgent expects — bridging
// modulepatch's TEMPLATE-level typed validation to the DEVICE level with
// zero duplicated logic.
//
// Two cases, both ending in the SAME deep-merge guarantee (every OTHER
// override already on this device's file survives untouched):
//   - The device already has a config file: deep-merges patch's delta
//     into it via [usecase.PatchDeviceFile] (same mechanism
//     PatchUseCaseModule uses on the template).
//   - This is the device's FIRST-EVER override: [usecase.PatchDeviceFile]
//     requires an EXISTING file to merge into, so this case writes
//     patch's delta directly via [usecase.Store.WriteDeviceConfig]
//     instead — detected via [usecase.IsNotFound] on the read attempt.
//     Any OTHER read error (e.g. an existing-but-malformed device file)
//     propagates as-is, never silently overwritten.
func PatchDeviceModule(ctx context.Context, store *usecase.Store, basePath usecase.BasePath, useCaseName usecase.Name, deviceID usecase.DeviceID, patch modulepatch.FieldsPatch, opts file.WriteOptions) error {
	rawBody, err := modulepatch.FieldsBodyCodec.Encode(patch)
	if err != nil {
		return err
	}
	delta := deviceconfig.Patch{EdgeAgent: map[string]any{string(patch.ModuleName): rawBody}}

	_, readErr := store.ReadDeviceConfig(ctx, basePath, useCaseName, deviceID)
	switch {
	case readErr == nil:
		return usecase.PatchDeviceFile(ctx, store, basePath, useCaseName, deviceID, deviceconfig.PatchCodec, delta, opts)
	case usecase.IsNotFound(readErr):
		// First-ever override for this device: the "devices/{useCaseName}/"
		// directory may not exist yet either — force CreateDirs regardless
		// of what the caller passed, since a caller reasonably expects a
		// FIRST override to "just work" without knowing in advance whether
		// this device already has a config file.
		firstWriteOpts := opts
		firstWriteOpts.CreateDirs = true
		_, writeErr := store.WriteDeviceConfig(ctx, basePath, useCaseName, usecase.DeviceConfig{DeviceID: deviceID, Patch: delta}, firstWriteOpts)
		return writeErr
	default:
		return readErr
	}
}

// UpdateDeviceModuleImage updates ONE module's image for ONE device only
// — a thin convenience over [PatchDeviceModule] for the single most
// common patch operation, mirroring [UpdateUseCaseModuleImage] exactly
// but scoped to deviceID's own config file. The patch itself is built
// and validated by modulepatch.NewUpdateModuleImage (moduleName's slug
// shape and image's Name/Tag/Digest constraints are both checked there,
// before this ever touches disk).
func UpdateDeviceModuleImage(ctx context.Context, store *usecase.Store, basePath usecase.BasePath, useCaseName usecase.Name, deviceID usecase.DeviceID, moduleName iothub.ModuleName, image docker.Image, opts file.WriteOptions) error {
	patch, err := modulepatch.NewUpdateModuleImage(moduleName, image)
	if err != nil {
		return err
	}
	return PatchDeviceModule(ctx, store, basePath, useCaseName, deviceID, patch, opts)
}

// ── System-module patching (edgeAgent/edgeHub themselves) ────────────────────
//
// The system-module analogues of PatchUseCaseModule/UpdateUseCaseModuleImage
// and PatchDeviceModule/UpdateDeviceModuleImage — for the two RESERVED
// system module names, "edgeAgent"/"edgeHub" (see
// modulesummary.IsSystemModuleName), a GENUINELY SEPARATE wire bucket
// from regular modules (iothub.SystemModuleKeyPrefix, not
// ModuleKeyPrefix). Unlike regular modules, there is no dedicated
// modulepatch.FieldsPatch-style sparse patch type for system modules —
// patchFields IS the raw wire-shaped value to deep-merge (e.g.
// map[string]any{"settings": map[string]any{"image": "..."}}).

// systemModulePatchIdentityCodec passes a pre-built raw
// {"modulesContent": {"$edgeAgent": {...}}} map through unchanged — used
// by [PatchUseCaseSystemModule]/[usecase.PatchUseCaseFile] since there
// is no FieldsPatch-style typed patch type for system modules to reuse.
var systemModulePatchIdentityCodec = codex.Codec[map[string]any]{
	Encode: func(m map[string]any) (any, error) { return m, nil },
	Decode: func(v any) (map[string]any, error) {
		m, ok := v.(map[string]any)
		if !ok {
			return nil, codex.TypeMismatchError{Expected: "object", Got: fmt.Sprintf("%T", v)}
		}
		return m, nil
	},
}

// PatchUseCaseSystemModule applies patchFields — any subset of name's
// ("edgeAgent"/"edgeHub") own fields, matching
// iothub.SystemModuleConfigCodec's own wire shape — to
// useCaseName's deployment manifest under basePath.
func PatchUseCaseSystemModule(ctx context.Context, store *usecase.Store, basePath usecase.BasePath, useCaseName usecase.Name, name iothub.SystemModuleName, patchFields map[string]any, opts file.WriteOptions) error {
	fullKey, err := iothub.SystemModuleNameCodec.Encode(name)
	if err != nil {
		return err
	}
	delta := map[string]any{
		iothub.ModulesContentKey: map[string]any{
			iothub.EdgeAgentKey: map[string]any{fullKey.(string): patchFields},
		},
	}
	return usecase.PatchUseCaseFile(ctx, store, basePath, useCaseName, systemModulePatchIdentityCodec, delta, opts)
}

// UpdateUseCaseSystemModuleImage updates ONE system module's image at the
// use case TEMPLATE scope — mirrors [UpdateUseCaseModuleImage]'s
// auto-promote rule exactly, one bucket over: if the template ALREADY
// declares an override for name, a SPARSE "settings.image" patch is
// safe (deep-merges onto the EXISTING, already-complete override);
// otherwise AUTO-PROMOTES to a FULL iothub.SystemModuleConfig
// (seeded from baseline's own resolved value, only Image changed) so
// the newly-written override is immediately valid on its own.
func UpdateUseCaseSystemModuleImage(ctx context.Context, store *usecase.Store, basePath usecase.BasePath, useCaseName usecase.Name, name iothub.SystemModuleName, image docker.Image, opts file.WriteOptions) error {
	template, err := ReadUseCase(ctx, store, basePath, useCaseName)
	if err != nil {
		return err
	}
	if _, ok := template.ModulesContent.SystemModules[name]; ok {
		return PatchUseCaseSystemModule(ctx, store, basePath, useCaseName, name, map[string]any{
			"settings": map[string]any{"image": image.String()},
		}, opts)
	}

	base, err := store.ReadBaseline(ctx, basePath)
	if err != nil {
		return err
	}
	baseModule, ok := modulesummary.SystemModuleConfigFor(base.ModulesContent.EdgeAgent.SystemModules, name)
	if !ok {
		return ModuleNotFoundError{ModuleName: iothub.ModuleName(name)}
	}
	baseModule.Settings.Image = image
	raw, err := iothub.SystemModuleConfigCodec.Encode(baseModule)
	if err != nil {
		return err
	}
	return PatchUseCaseSystemModule(ctx, store, basePath, useCaseName, name, raw.(map[string]any), opts)
}

// PatchDeviceSystemModule applies patchFields — any subset of name's own
// fields (see [PatchUseCaseSystemModule]'s own doc comment) — to
// deviceID's OWN config file under basePath/useCaseName, mirroring
// [PatchDeviceModule]'s own two-case (existing file / first-ever
// override) handling exactly, one bucket over
// (deviceconfig.Patch.SystemModules, not EdgeAgent).
func PatchDeviceSystemModule(ctx context.Context, store *usecase.Store, basePath usecase.BasePath, useCaseName usecase.Name, deviceID usecase.DeviceID, name iothub.SystemModuleName, patchFields map[string]any, opts file.WriteOptions) error {
	delta := deviceconfig.Patch{SystemModules: map[string]any{string(name): patchFields}}

	_, readErr := store.ReadDeviceConfig(ctx, basePath, useCaseName, deviceID)
	switch {
	case readErr == nil:
		return usecase.PatchDeviceFile(ctx, store, basePath, useCaseName, deviceID, deviceconfig.PatchCodec, delta, opts)
	case usecase.IsNotFound(readErr):
		firstWriteOpts := opts
		firstWriteOpts.CreateDirs = true
		_, writeErr := store.WriteDeviceConfig(ctx, basePath, useCaseName, usecase.DeviceConfig{DeviceID: deviceID, Patch: delta}, firstWriteOpts)
		return writeErr
	default:
		return readErr
	}
}

// UpdateDeviceSystemModuleImage updates ONE system module's image for
// ONE device only — a thin convenience over [PatchDeviceSystemModule],
// mirroring [UpdateDeviceModuleImage] exactly. No auto-promote needed
// here (unlike the template-scope [UpdateUseCaseSystemModuleImage]): a
// system module ALWAYS resolves via baseline (both edgeAgent and edgeHub
// are mandatory there — see iothub.SystemModules), so a sparse
// "settings.image" patch always deep-merges onto an already-complete
// base.
func UpdateDeviceSystemModuleImage(ctx context.Context, store *usecase.Store, basePath usecase.BasePath, useCaseName usecase.Name, deviceID usecase.DeviceID, name iothub.SystemModuleName, image docker.Image, opts file.WriteOptions) error {
	return PatchDeviceSystemModule(ctx, store, basePath, useCaseName, deviceID, name, map[string]any{
		"settings": map[string]any{"image": image.String()},
	}, opts)
}
