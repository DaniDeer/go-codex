package modulesummary

import (
	mcp "github.com/DaniDeer/go-codex/api/mcp"
	c "github.com/DaniDeer/go-codex/codex"
	manifesttemplate "github.com/DaniDeer/go-codex/examples/go-edge-models/models/iotedge/manifesttemplate"
	v "github.com/DaniDeer/go-codex/validate"
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
	// BasePath is the root directory holding the "usecases/" subtree —
	// see usecase.NewFile's own doc comment for the templated
	// "{basePath}/usecases/{usecase_name}.json" path this composes into.
	BasePath string
	// UseCaseName is the use case whose deployment manifest to read.
	UseCaseName string
	// ModuleName is the module to summarize, e.g. "factory-dashboard".
	ModuleName manifesttemplate.ModuleName
}

// ReadReqCodec validates a ReadReq value — all three fields are
// required.
var ReadReqCodec = c.Struct[ReadReq](
	c.RequiredField("basePath",
		c.String().Refine(v.NonEmptyString).WithDescription(
			"The root directory holding the \"usecases/\" subtree.",
		),
		func(r ReadReq) string { return r.BasePath },
		func(r *ReadReq, val string) { r.BasePath = val },
	),
	c.RequiredField("useCaseName",
		c.String().Refine(v.NonEmptyString).WithDescription(
			"The use case whose deployment manifest to read.",
		),
		func(r ReadReq) string { return r.UseCaseName },
		func(r *ReadReq, val string) { r.UseCaseName = val },
	),
	c.RequiredField("moduleName",
		manifesttemplate.ModuleNameCodec.WithDescription(
			"The name of the module to summarize, e.g. \"factory-dashboard\".",
		),
		func(r ReadReq) manifesttemplate.ModuleName { return r.ModuleName },
		func(r *ReadReq, val manifesttemplate.ModuleName) { r.ModuleName = val },
	),
)

// ReadTool is the declared, UNREGISTERED MCP tool contract for reading
// one module's reduced summary (image, host-mapped ports, binds,
// status, restart policy) from a deployment manifest file — the same
// "declare once, register anywhere" pattern registry.GetTagsTool
// follows: a caller registers it against their own mcp.Builder
// (ReadTool.Register(builder)) and pairs the resulting handle with
// app/iotedge's NewReadModuleSummaryToolHandler via mcpgo.ToolHandler.
var ReadTool = mcp.NewTool[ReadReq, Summary](
	"read_module_summary", ReadReqCodec, SummaryCodec,
	mcp.ToolMeta{
		Description: "Read a reduced, operational summary of one module " +
			"from a deployment manifest file: its image, host-mapped " +
			"ports, bind mounts, status, and restart policy.",
	},
)
