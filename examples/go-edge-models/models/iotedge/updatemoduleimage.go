package iotedge

import (
	mcp "github.com/DaniDeer/go-codex/api/mcp"
	c "github.com/DaniDeer/go-codex/codex"
	v "github.com/DaniDeer/go-codex/validate"
)

// This file holds the DECLARATIVE contract for updating one module's
// container image in a deployment manifest file — the request
// type+codec and the MCP tool declaration. Pure data — no I/O, no
// ports.File. The concrete implementation (which actually parses the
// image, applies the patch, and re-reads the module's summary) lives in
// the sibling app/iotedge package's NewUpdateModuleImageToolHandler,
// built on top of this declaration.

// UpdateModuleImageReq is UpdateModuleImageTool's input.
type UpdateModuleImageReq struct {
	// BasePath is the root directory holding the "usecases/" subtree —
	// see iotedge.NewConfigFile's own doc comment for the templated
	// "{basePath}/usecases/{usecase_name}.json" path this composes into.
	BasePath string
	// UseCaseName is the use case whose deployment manifest to update.
	UseCaseName string
	// ModuleName is the module to update, e.g. "factory-dashboard".
	ModuleName ModuleName
	// ImageURL is the full container image reference to set, e.g.
	// "ghcr.io/org/repo:1.2.3" — a plain string (not a structured
	// Name/Tag/Digest shape), matching the flat-string convention already
	// used by registry.GetTagsToolReq.ImageURL. Parsed/validated
	// server-side via docker.ImageCodec.Decode (see
	// app/iotedge.NewUpdateModuleImageToolHandler).
	ImageURL string
}

// UpdateModuleImageReqCodec validates an UpdateModuleImageReq value —
// all four fields are required.
var UpdateModuleImageReqCodec = c.Struct[UpdateModuleImageReq](
	c.RequiredField("basePath",
		c.String().Refine(v.NonEmptyString).WithDescription(
			"The root directory holding the \"usecases/\" subtree.",
		),
		func(r UpdateModuleImageReq) string { return r.BasePath },
		func(r *UpdateModuleImageReq, val string) { r.BasePath = val },
	),
	c.RequiredField("useCaseName",
		c.String().Refine(v.NonEmptyString).WithDescription(
			"The use case whose deployment manifest to update.",
		),
		func(r UpdateModuleImageReq) string { return r.UseCaseName },
		func(r *UpdateModuleImageReq, val string) { r.UseCaseName = val },
	),
	c.RequiredField("moduleName",
		ModuleNameCodec.WithDescription(
			"The name of the module to update, e.g. \"factory-dashboard\".",
		),
		func(r UpdateModuleImageReq) ModuleName { return r.ModuleName },
		func(r *UpdateModuleImageReq, val ModuleName) { r.ModuleName = val },
	),
	c.RequiredField("imageURL",
		c.String().Refine(v.NonEmptyString).WithDescription(
			"The full container image reference to set, e.g. \"ghcr.io/org/repo:1.2.3\".",
		),
		func(r UpdateModuleImageReq) string { return r.ImageURL },
		func(r *UpdateModuleImageReq, val string) { r.ImageURL = val },
	),
)

// UpdateModuleImageTool is the declared, UNREGISTERED MCP tool contract
// for updating one module's container image in a deployment manifest
// file, returning the module's UPDATED reduced summary (image,
// host-mapped ports, binds, status, restart policy) — the same
// "declare once, register anywhere" pattern ReadModuleSummaryTool
// follows: a caller registers it against their own mcp.Builder
// (UpdateModuleImageTool.Register(builder)) and pairs the resulting
// handle with app/iotedge's NewUpdateModuleImageToolHandler via
// mcpgo.ToolHandler.
var UpdateModuleImageTool = mcp.NewTool[UpdateModuleImageReq, ModuleSummary](
	"update_module_image", UpdateModuleImageReqCodec, ModuleSummaryCodec,
	mcp.ToolMeta{
		Description: "Update one module's container image in a deployment " +
			"manifest file, returning the module's updated summary " +
			"(image, host-mapped ports, binds, status, restart policy).",
	},
)
