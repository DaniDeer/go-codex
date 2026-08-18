package iotedge

import (
	"context"

	mcpgo "github.com/DaniDeer/go-codex/adapters/mcpgo"
	"github.com/DaniDeer/go-codex/examples/go-edge-models/models/docker"
	"github.com/DaniDeer/go-codex/examples/go-edge-models/models/iotedge/modulesummary"
	"github.com/DaniDeer/go-codex/examples/go-edge-models/models/iotedge/updatemoduleimage"
	"github.com/DaniDeer/go-codex/ports"
)

// ── UpdateModuleImage as an MCP tool ──────────────────────────────────────────

// NewUpdateModuleImageToolHandler returns an mcpgo.HandlerFunc that
// parses req.ImageURL and applies it as req.ModuleName's new image —
// via UpdateUseCaseModuleImage (the use case template) when req.DeviceID
// is empty, or via UpdateDeviceModuleImage (that ONE device's own
// config file, template and every other device untouched) when set —
// returning the module's UPDATED modulesummary.Summary, reflecting
// whichever scope was updated.
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
		// otherwise the deep-merge would add an incomplete entry (missing
		// type/status/restartPolicy/version) and fail with a confusing
		// low-level ports.FileDecodeError instead of a clean, typed
		// ModuleNotFoundError. Uses the SAME scope (template or device)
		// the update itself will target, via req.DeviceID.
		if _, err := readModuleSummary(req.BasePath, req.UseCaseName, req.DeviceID, req.ModuleName, opts); err != nil {
			return modulesummary.Summary{}, err
		}
		if req.DeviceID == "" {
			if err := UpdateUseCaseModuleImage(req.BasePath, req.UseCaseName, req.ModuleName, image, opts); err != nil {
				return modulesummary.Summary{}, err
			}
		} else {
			if err := UpdateDeviceModuleImage(req.BasePath, req.UseCaseName, req.DeviceID, req.ModuleName, image, opts); err != nil {
				return modulesummary.Summary{}, err
			}
		}
		return readModuleSummary(req.BasePath, req.UseCaseName, req.DeviceID, req.ModuleName, opts)
	}
}
