package iotedge

import (
	"context"
	"fmt"
	"log/slog"

	mcpgo "github.com/DaniDeer/go-codex/adapters/mcpgo"
	regiotedge "github.com/DaniDeer/go-codex/examples/go-edge-models/models/iotedge"
	"github.com/DaniDeer/go-codex/ports"
)

// ── ReadModuleSummary as an MCP tool ─────────────────────────────────────────

// ModuleNotFoundError is returned by NewReadModuleSummaryToolHandler's
// handler when req.ModuleName is not present in the manifest's
// $edgeAgent map.
type ModuleNotFoundError struct {
	ModuleName regiotedge.ModuleName
}

func (e ModuleNotFoundError) Error() string {
	return fmt.Sprintf("module %q not found in manifest", string(e.ModuleName))
}

// LogValue implements slog.LogValuer for structured logging.
func (e ModuleNotFoundError) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("module_name", string(e.ModuleName)),
	)
}

// readModuleSummary reads the deployment manifest at manifestPath, looks
// up moduleName, and maps it to a regiotedge.ModuleSummary — shared by
// NewReadModuleSummaryToolHandler (below) and
// NewUpdateModuleImageToolHandler (updatemoduleimage.go), which both need
// "read manifest, find module, summarize" as their final step.
func readModuleSummary(manifestPath string, moduleName regiotedge.ModuleName, opts ports.FileOptions) (regiotedge.ModuleSummary, error) {
	manifest, err := ReadConfig(manifestPath, opts)
	if err != nil {
		return regiotedge.ModuleSummary{}, err
	}
	mc, ok := manifest.ModulesContent.EdgeAgent[moduleName]
	if !ok {
		return regiotedge.ModuleSummary{}, ModuleNotFoundError{ModuleName: moduleName}
	}
	return regiotedge.NewModuleSummary(mc), nil
}

// NewReadModuleSummaryToolHandler returns an mcpgo.HandlerFunc that reads
// the deployment manifest at req.ManifestPath, looks up req.ModuleName,
// and maps it to a regiotedge.ModuleSummary — binding
// models/iotedge's declared ReadModuleSummaryTool to ReadConfig.
//
// Usage:
//
//	tool, _ := regiotedge.ReadModuleSummaryTool.Register(mcpBuilder)
//	_, handlerFn := mcpgo.ToolHandler(tool,
//	    iotedgeapp.NewReadModuleSummaryToolHandler(ports.FileOptions{}),
//	    mcpgo.Options{})
func NewReadModuleSummaryToolHandler(opts ports.FileOptions) mcpgo.HandlerFunc[regiotedge.ReadModuleSummaryReq, regiotedge.ModuleSummary] {
	return func(ctx context.Context, req regiotedge.ReadModuleSummaryReq) (regiotedge.ModuleSummary, error) {
		return readModuleSummary(req.ManifestPath, req.ModuleName, opts)
	}
}
