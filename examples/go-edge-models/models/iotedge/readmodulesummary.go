package iotedge

import (
	mcp "github.com/DaniDeer/go-codex/api/mcp"
	c "github.com/DaniDeer/go-codex/codex"
	v "github.com/DaniDeer/go-codex/validate"
)

// This file holds the DECLARATIVE contract for reading one module's
// reduced summary out of a deployment manifest file — the request
// type+codec and the MCP tool declaration. Pure data — no I/O, no
// ports.File. The concrete implementation (which actually reads the
// file and looks up the named module) lives in the sibling app/iotedge
// package's NewReadModuleSummaryToolHandler, built on top of this
// declaration.

// ReadModuleSummaryReq is ReadModuleSummaryTool's input.
type ReadModuleSummaryReq struct {
	// ManifestPath is the concrete deployment-manifest file to read —
	// see iotedge.NewConfigFile's own doc comment for why this is a
	// plain caller-supplied path, not a path template variable.
	ManifestPath string
	// ModuleName is the module to summarize, e.g. "factory-dashboard".
	ModuleName ModuleName
}

// ReadModuleSummaryReqCodec validates a ReadModuleSummaryReq value — both
// fields are required.
var ReadModuleSummaryReqCodec = c.Struct[ReadModuleSummaryReq](
	c.RequiredField("manifestPath",
		c.String().Refine(v.NonEmptyString).WithDescription(
			"The file path of the deployment manifest to read.",
		),
		func(r ReadModuleSummaryReq) string { return r.ManifestPath },
		func(r *ReadModuleSummaryReq, val string) { r.ManifestPath = val },
	),
	c.RequiredField("moduleName",
		ModuleNameCodec.WithDescription(
			"The name of the module to summarize, e.g. \"factory-dashboard\".",
		),
		func(r ReadModuleSummaryReq) ModuleName { return r.ModuleName },
		func(r *ReadModuleSummaryReq, val ModuleName) { r.ModuleName = val },
	),
)

// ReadModuleSummaryTool is the declared, UNREGISTERED MCP tool contract
// for reading one module's reduced summary (image, host-mapped ports,
// binds, status, restart policy) from a deployment manifest file — the
// same "declare once, register anywhere" pattern
// registry.GetTagsTool follows: a caller registers it against their own
// mcp.Builder (ReadModuleSummaryTool.Register(builder)) and pairs the
// resulting handle with app/iotedge's NewReadModuleSummaryToolHandler via
// mcpgo.ToolHandler.
var ReadModuleSummaryTool = mcp.NewTool[ReadModuleSummaryReq, ModuleSummary](
	"read_module_summary", ReadModuleSummaryReqCodec, ModuleSummaryCodec,
	mcp.ToolMeta{
		Description: "Read a reduced, operational summary of one module " +
			"from a deployment manifest file: its image, host-mapped " +
			"ports, bind mounts, status, and restart policy.",
	},
)
