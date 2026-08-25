package modulesummary

import (
	mcp "github.com/DaniDeer/go-codex/api/mcp"
	c "github.com/DaniDeer/go-codex/codex"
	iothub "github.com/DaniDeer/go-codex/examples/go-edge-models/models/azure/iothub"
	usecase "github.com/DaniDeer/go-codex/examples/go-edge-models/models/iotedge/usecase"
)

// This file holds the DECLARATIVE contract for reading one module's
// reduced summary out of a deployment manifest file — the request
// type+codec and the MCP tool declaration. Pure data — no I/O, no
// ports.File. The concrete implementation (which actually reads the
// file and looks up the named module) lives in the sibling app/iotedge
// package's NewReadModuleSummaryToolHandler, built on top of this
// declaration.

// ReadReq is ReadTool's input.
type ReadReq struct {
	// BasePath is the root directory holding this use case's ENTIRE
	// on-disk layout — see [usecase.BasePath]'s own doc comment (this
	// package now depends directly on models/iotedge/usecase for this
	// ONE field, unlike DeviceID below, which deliberately stays
	// decoupled).
	BasePath usecase.BasePath
	// UseCaseName is the use case whose deployment manifest to read —
	// see [usecase.Name]'s own doc comment.
	UseCaseName usecase.Name
	// ModuleName is the module to summarize, e.g. "factory-dashboard" —
	// or "edgeAgent"/"edgeHub" for a system module's own summary (see
	// IsSystemModuleName).
	ModuleName iothub.ModuleName
	// DeviceID OPTIONALLY scopes the summary to ONE device's ACTUAL
	// configured module — the use case template's manifest, with that
	// device's own config patch layered on top (see
	// models/iotedge/usecase's ReadEffective/DeviceConfig.Merge). Empty
	// (the zero value, [usecase.DeviceID]("")) means "summarize the use
	// case template itself, with no device overrides applied."
	DeviceID usecase.DeviceID
}

// ReadReqCodec validates a ReadReq value — BasePath/UseCaseName/
// ModuleName are required; DeviceID is OPTIONAL (empty = template-only
// scope). Field declarations are shared with updatemoduleimage.Req via
// [BasePathField]/[UseCaseNameField]/[ModuleNameField]/[DeviceIDField]
// (see targetfields.go) — single source of truth for the key names,
// codecs, and description text both request types share.
var ReadReqCodec = c.Struct[ReadReq](
	BasePathField(
		func(r ReadReq) usecase.BasePath { return r.BasePath },
		func(r *ReadReq, val usecase.BasePath) { r.BasePath = val },
	),
	UseCaseNameField(
		func(r ReadReq) usecase.Name { return r.UseCaseName },
		func(r *ReadReq, val usecase.Name) { r.UseCaseName = val },
	),
	ModuleNameField(
		func(r ReadReq) iothub.ModuleName { return r.ModuleName },
		func(r *ReadReq, val iothub.ModuleName) { r.ModuleName = val },
	),
	DeviceIDField(
		func(r ReadReq) usecase.DeviceID { return r.DeviceID },
		func(r *ReadReq, val usecase.DeviceID) { r.DeviceID = val },
	),
)

// ReadTool is the declared, UNREGISTERED MCP tool contract for reading
// one module's reduced summary (image, host-mapped ports, binds,
// status, restart policy) from a deployment manifest file, OPTIONALLY
// scoped to one device's actual configured module (see ReadReq.DeviceID)
// — the same "declare once, register anywhere" pattern
// registry.GetTagsTool follows: a caller registers it against their own
// mcp.Builder (ReadTool.Register(builder)) and pairs the resulting
// handle with app/iotedge's NewReadModuleSummaryToolHandler via
// mcpgo.ToolHandler.
var ReadTool = mcp.NewTool[ReadReq, Summary](
	"read_module_summary", ReadReqCodec, SummaryCodec,
	mcp.ToolMeta{
		Description: "Read a reduced, operational summary of one module: " +
			"its image, host-mapped ports, bind mounts, status, and " +
			"restart policy. If deviceID is set, returns that device's " +
			"ACTUAL configured summary (the use case template with that " +
			"device's own config overrides layered on top); otherwise " +
			"returns the use case template's own summary.",
	},
)
