package iotedge

import (
	"context"
	"fmt"
	"log/slog"

	mcpgo "github.com/DaniDeer/go-codex/adapters/mcpgo"
	manifesttemplate "github.com/DaniDeer/go-codex/examples/go-edge-models/models/iotedge/manifesttemplate"
	"github.com/DaniDeer/go-codex/examples/go-edge-models/models/iotedge/modulesummary"
	"github.com/DaniDeer/go-codex/ports"
)

// ── ReadModuleSummary as an MCP tool ─────────────────────────────────────────

// ModuleNotFoundError is returned by NewReadModuleSummaryToolHandler's
// handler when req.ModuleName is not present in the manifest's
// $edgeAgent map.
type ModuleNotFoundError struct {
	ModuleName manifesttemplate.ModuleName
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

// readModuleSummary reads useCaseName's deployment manifest under
// basePath, looks up moduleName, and maps it to a
// modulesummary.Summary — shared by NewReadModuleSummaryToolHandler
// (below) and NewUpdateModuleImageToolHandler (updatemoduleimage.go),
// which both need "read manifest, find module, summarize" as their
// final step.
func readModuleSummary(basePath, useCaseName string, moduleName manifesttemplate.ModuleName, opts ports.FileOptions) (modulesummary.Summary, error) {
	manifest, err := ReadUseCase(basePath, useCaseName, opts)
	if err != nil {
		return modulesummary.Summary{}, err
	}
	mc, ok := manifest.ModulesContent.EdgeAgent[moduleName]
	if !ok {
		return modulesummary.Summary{}, ModuleNotFoundError{ModuleName: moduleName}
	}
	return modulesummary.NewSummary(mc), nil
}

// NewReadModuleSummaryToolHandler returns an mcpgo.HandlerFunc that reads
// req.UseCaseName's deployment manifest under req.BasePath, looks up
// req.ModuleName, and maps it to a modulesummary.Summary — binding
// modulesummary's declared ReadTool to ReadUseCase.
//
// Usage:
//
//	tool, _ := modulesummary.ReadTool.Register(mcpBuilder)
//	_, handlerFn := mcpgo.ToolHandler(tool,
//	    iotedgeapp.NewReadModuleSummaryToolHandler(ports.FileOptions{}),
//	    mcpgo.Options{})
func NewReadModuleSummaryToolHandler(opts ports.FileOptions) mcpgo.HandlerFunc[modulesummary.ReadReq, modulesummary.Summary] {
	return func(ctx context.Context, req modulesummary.ReadReq) (modulesummary.Summary, error) {
		return readModuleSummary(req.BasePath, req.UseCaseName, req.ModuleName, opts)
	}
}
