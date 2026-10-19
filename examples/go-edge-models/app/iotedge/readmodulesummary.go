package iotedge

import (
	"context"
	"fmt"
	"log/slog"

	mcpgo "github.com/DaniDeer/go-codex/adapters/mcpgo"
	iothub "github.com/DaniDeer/go-codex/examples/go-edge-models/models/azure/iothub"
	deviceconfig "github.com/DaniDeer/go-codex/examples/go-edge-models/models/iotedge/deviceconfig"
	finaldeviceconfig "github.com/DaniDeer/go-codex/examples/go-edge-models/models/iotedge/finaldeviceconfig"
	"github.com/DaniDeer/go-codex/examples/go-edge-models/models/iotedge/modulesummary"
	"github.com/DaniDeer/go-codex/examples/go-edge-models/models/iotedge/usecase"
)

// ── ReadModuleSummary as an MCP tool ─────────────────────────────────────────

// ModuleNotFoundError is returned by NewReadModuleSummaryToolHandler's
// handler when req.ModuleName is not present in the manifest's
// modules OR system-modules bucket.
type ModuleNotFoundError struct {
	ModuleName iothub.ModuleName
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

// readModuleSummary reads useCaseName's EFFECTIVE manifest under
// basePath (baseline + template, OR — when deviceID is non-empty —
// baseline + template + that device's own config, all merged via
// effectiveManifest) — looks up moduleName, and maps it to a
// modulesummary.Summary — shared by NewReadModuleSummaryToolHandler
// (below) and NewUpdateModuleImageToolHandler (updatemoduleimage.go),
// which both need "read the effective manifest, find module, summarize"
// as their final step.
//
// moduleName may name a REGULAR module (looked up in
// ModulesContent.EdgeAgent.Modules) OR one of the two reserved system
// module names, "edgeAgent"/"edgeHub" (see
// modulesummary.IsSystemModuleName — looked up in
// ModulesContent.EdgeAgent.SystemModules instead, via
// modulesummary.NewSummaryFromSystemModule).
func readModuleSummary(ctx context.Context, scope *usecase.Scope, useCaseName usecase.Name, deviceID usecase.DeviceID, moduleName iothub.ModuleName) (modulesummary.Summary, error) {
	manifest, err := effectiveManifest(ctx, scope, useCaseName, deviceID)
	if err != nil {
		return modulesummary.Summary{}, err
	}
	return summaryFor(manifest, moduleName)
}

// summaryFor looks up moduleName in manifest's Modules or SystemModules
// bucket (see readModuleSummary's own doc comment for the dispatch
// rule) and maps it to a modulesummary.Summary.
func summaryFor(manifest iothub.BaseDeployment, moduleName iothub.ModuleName) (modulesummary.Summary, error) {
	if modulesummary.IsSystemModuleName(moduleName) {
		smc, ok := modulesummary.SystemModuleConfigFor(manifest.ModulesContent.EdgeAgent.SystemModules, iothub.SystemModuleName(moduleName))
		if !ok {
			return modulesummary.Summary{}, ModuleNotFoundError{ModuleName: moduleName}
		}
		return modulesummary.NewSummaryFromSystemModule(smc), nil
	}
	mc, ok := manifest.ModulesContent.EdgeAgent.Modules[moduleName]
	if !ok {
		return modulesummary.Summary{}, ModuleNotFoundError{ModuleName: moduleName}
	}
	return modulesummary.NewSummary(mc), nil
}

// effectiveManifest reads the FULLY layered iothub.BaseDeployment for
// useCaseName under basePath — baseline + template, with an EMPTY
// device patch when deviceID is empty, or baseline + template + that
// device's OWN config when deviceID is set.
//
// Always baseline-aware (even with no deviceID) — this is what lets
// baseline-only modules/system-modules (e.g. "vulnerability-scanner",
// "edgeAgent"/"edgeHub" themselves) resolve at the USE-CASE-TEMPLATE
// scope too, not just the device scope.
func effectiveManifest(ctx context.Context, scope *usecase.Scope, useCaseName usecase.Name, deviceID usecase.DeviceID) (iothub.BaseDeployment, error) {
	base, err := scope.ReadBaseline(ctx)
	if err != nil {
		return iothub.BaseDeployment{}, err
	}
	template, err := ReadUseCase(ctx, scope, useCaseName)
	if err != nil {
		return iothub.BaseDeployment{}, err
	}
	if deviceID == "" {
		return finaldeviceconfig.Merge(base, template, deviceconfig.Patch{})
	}
	return scope.ReadEffective(ctx, useCaseName, deviceID)
}

// NewReadModuleSummaryToolHandler returns an mcpgo.HandlerFunc that reads
// req.UseCaseName's EFFECTIVE manifest under req.BasePath — OR, when
// req.DeviceID is set, that device's ACTUAL configured manifest — looks
// up req.ModuleName, and maps it to a modulesummary.Summary — binding
// modulesummary's declared ReadTool to effectiveManifest. store (the
// long-lived connection) is captured in the returned closure
// (constructed ONCE, e.g. in main.go); req.BasePath — a genuine
// per-request wire field — is bound to a fresh [usecase.Scope] via
// [usecase.Store.For] on EVERY call, since basePath varies per request.
//
// Usage:
//
//	tool, _ := modulesummary.ReadTool.Register(mcpBuilder)
//	_, handlerFn := mcpgo.ToolHandler(tool,
//	    iotedgeapp.NewReadModuleSummaryToolHandler(store),
//	    mcpgo.Options{})
func NewReadModuleSummaryToolHandler(store *usecase.Store) mcpgo.HandlerFunc[modulesummary.ReadReq, modulesummary.Summary] {
	return func(ctx context.Context, req modulesummary.ReadReq) (modulesummary.Summary, error) {
		scope, err := store.For(req.BasePath)
		if err != nil {
			return modulesummary.Summary{}, err
		}
		return readModuleSummary(ctx, scope, req.UseCaseName, req.DeviceID, req.ModuleName)
	}
}
