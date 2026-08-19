package iotedge

import (
	"context"

	mcpgo "github.com/DaniDeer/go-codex/adapters/mcpgo"
	iothub "github.com/DaniDeer/go-codex/examples/go-edge-models/models/azure/iothub"
	"github.com/DaniDeer/go-codex/examples/go-edge-models/models/docker"
	"github.com/DaniDeer/go-codex/examples/go-edge-models/models/iotedge/modulesummary"
	"github.com/DaniDeer/go-codex/examples/go-edge-models/models/iotedge/updatemoduleimage"
	"github.com/DaniDeer/go-codex/ports"
)

// ── UpdateModuleImage as an MCP tool ──────────────────────────────────────────

// NewUpdateModuleImageToolHandler returns an mcpgo.HandlerFunc that
// parses req.ImageURL and applies it as req.ModuleName's new image.
// req.ModuleName may name a REGULAR module OR one of the two reserved
// system module names, "edgeAgent"/"edgeHub" (see
// modulesummary.IsSystemModuleName) — dispatched to
// UpdateUseCase(System)ModuleImage (the use case template) when
// req.DeviceID is empty, or UpdateDevice(System)ModuleImage (that ONE
// device's own config file, template and every other device untouched)
// when set — returning the module's UPDATED modulesummary.Summary,
// reflecting whichever scope was updated. Both update paths
// AUTO-PROMOTE to a full override when the target is currently resolved
// only via a LOWER layer (see UpdateUseCaseModuleImage/
// UpdateUseCaseSystemModuleImage's own doc comments).
//
// Usage:
//
//	tool, _ := updatemoduleimage.Tool.Register(mcpBuilder)
//	_, handlerFn := mcpgo.ToolHandler(tool,
//	    iotedgeapp.NewUpdateModuleImageToolHandler(ports.FileOptions{}),
//	    mcpgo.Options{})
func NewUpdateModuleImageToolHandler(opts ports.FileOptions) mcpgo.HandlerFunc[updatemoduleimage.Req, modulesummary.Summary] {
	return func(ctx context.Context, req updatemoduleimage.Req) (modulesummary.Summary, error) {
		image, err := docker.ImageCodec.Decode(req.ImageURL)
		if err != nil {
			return modulesummary.Summary{}, err
		}
		// Confirm the module already exists BEFORE attempting the patch —
		// effectiveManifest is baseline-aware, so this now ALSO succeeds
		// for a baseline-only module/system-module; a clean, typed
		// ModuleNotFoundError still surfaces for a genuinely unknown name.
		// Uses the SAME scope (template or device) the update itself will
		// target, via req.DeviceID.
		if _, err := readModuleSummary(req.BasePath, req.UseCaseName, req.DeviceID, req.ModuleName, opts); err != nil {
			return modulesummary.Summary{}, err
		}

		isSystemModule := modulesummary.IsSystemModuleName(req.ModuleName)
		systemModuleName := iothub.SystemModuleName(req.ModuleName)

		switch {
		case req.DeviceID == "" && isSystemModule:
			err = UpdateUseCaseSystemModuleImage(req.BasePath, req.UseCaseName, systemModuleName, image, opts)
		case req.DeviceID == "":
			err = UpdateUseCaseModuleImage(req.BasePath, req.UseCaseName, req.ModuleName, image, opts)
		case isSystemModule:
			err = UpdateDeviceSystemModuleImage(req.BasePath, req.UseCaseName, req.DeviceID, systemModuleName, image, opts)
		default:
			err = UpdateDeviceModuleImage(req.BasePath, req.UseCaseName, req.DeviceID, req.ModuleName, image, opts)
		}
		if err != nil {
			return modulesummary.Summary{}, err
		}
		return readModuleSummary(req.BasePath, req.UseCaseName, req.DeviceID, req.ModuleName, opts)
	}
}
