package iotedge

import (
	"context"

	mcpgo "github.com/DaniDeer/go-codex/adapters/mcpgo"
	"github.com/DaniDeer/go-codex/examples/go-edge-models/models/docker"
	regiotedge "github.com/DaniDeer/go-codex/examples/go-edge-models/models/iotedge"
	"github.com/DaniDeer/go-codex/ports"
)

// ── UpdateModuleImage as an MCP tool ──────────────────────────────────────────

// NewUpdateModuleImageToolHandler returns an mcpgo.HandlerFunc that
// parses req.ImageURL, applies it as req.ModuleName's new image via
// UpdateModuleImage, and returns the module's UPDATED
// regiotedge.ModuleSummary — binding models/iotedge's declared
// UpdateModuleImageTool to UpdateModuleImage.
//
// Usage:
//
//	tool, _ := regiotedge.UpdateModuleImageTool.Register(mcpBuilder)
//	_, handlerFn := mcpgo.ToolHandler(tool,
//	    iotedgeapp.NewUpdateModuleImageToolHandler(ports.FileOptions{}),
//	    mcpgo.Options{})
func NewUpdateModuleImageToolHandler(opts ports.FileOptions) mcpgo.HandlerFunc[regiotedge.UpdateModuleImageReq, regiotedge.ModuleSummary] {
	return func(ctx context.Context, req regiotedge.UpdateModuleImageReq) (regiotedge.ModuleSummary, error) {
		image, err := docker.ImageCodec.Decode(req.ImageURL)
		if err != nil {
			return regiotedge.ModuleSummary{}, err
		}
		// Confirm the module already exists BEFORE attempting the patch —
		// otherwise the deep-merge would add an incomplete entry (missing
		// type/status/restartPolicy/version) and fail with a confusing
		// low-level ports.FileDecodeError instead of a clean, typed
		// ModuleNotFoundError.
		if _, err := readModuleSummary(req.BasePath, req.UseCaseName, req.ModuleName, opts); err != nil {
			return regiotedge.ModuleSummary{}, err
		}
		if err := UpdateModuleImage(req.BasePath, req.UseCaseName, req.ModuleName, image, opts); err != nil {
			return regiotedge.ModuleSummary{}, err
		}
		return readModuleSummary(req.BasePath, req.UseCaseName, req.ModuleName, opts)
	}
}
