package updatemoduleimage

import (
	mcp "github.com/DaniDeer/go-codex/api/mcp"
	c "github.com/DaniDeer/go-codex/codex"
	iothub "github.com/DaniDeer/go-codex/examples/go-edge-models/models/azure/iothub"
	"github.com/DaniDeer/go-codex/examples/go-edge-models/models/iotedge/modulesummary"
	usecase "github.com/DaniDeer/go-codex/examples/go-edge-models/models/iotedge/usecase"
	v "github.com/DaniDeer/go-codex/validate"
)

// This file holds the DECLARATIVE contract for updating one module's
// container image in a deployment manifest file — the request
// type+codec and the MCP tool declaration. Pure data — no I/O, no
// ports.File. The concrete implementation (which actually parses the
// image, applies the patch, and re-reads the module's summary) lives in
// the sibling app/iotedge package's NewUpdateModuleImageToolHandler,
// built on top of this declaration.

// Req is Tool's input.
type Req struct {
	// BasePath is the root directory holding this use case's ENTIRE
	// on-disk layout — see [usecase.BasePath]'s own doc comment (this
	// package now depends directly on models/iotedge/usecase for this
	// ONE field, unlike DeviceID below, which deliberately stays
	// decoupled).
	BasePath usecase.BasePath
	// UseCaseName is the use case whose deployment manifest to update —
	// see [usecase.Name]'s own doc comment.
	UseCaseName usecase.Name
	// ModuleName is the module to update, e.g. "factory-dashboard" — or
	// "edgeAgent"/"edgeHub" for a system module's own image (see
	// modulesummary.IsSystemModuleName).
	ModuleName iothub.ModuleName
	// ImageURL is the full container image reference to set, e.g.
	// "ghcr.io/org/repo:1.2.3" — a plain string (not a structured
	// Name/Tag/Digest shape), matching the flat-string convention already
	// used by registry.GetTagsToolReq.ImageURL. Parsed/validated
	// server-side via docker.ImageCodec.Decode (see
	// app/iotedge.NewUpdateModuleImageToolHandler).
	ImageURL string
	// DeviceID OPTIONALLY scopes the update to ONE device's OWN config
	// file — the use case template and every OTHER device stay
	// completely untouched (isolated, reversible). Empty (the zero
	// value, [usecase.DeviceID]("")) means "update the use case
	// template's shared manifest."
	DeviceID usecase.DeviceID
}

// ReqCodec validates a Req value — BasePath/UseCaseName/ModuleName/
// ImageURL are required; DeviceID is OPTIONAL (empty = template scope).
// BasePath/UseCaseName/ModuleName/DeviceID field declarations are shared
// with modulesummary.ReadReq via [modulesummary.BasePathField]/
// [modulesummary.UseCaseNameField]/[modulesummary.ModuleNameField]/
// [modulesummary.DeviceIDField] — single source of truth for the key
// names, codecs, and description text both request types share.
var ReqCodec = c.Struct[Req](
	modulesummary.BasePathField(
		func(r Req) usecase.BasePath { return r.BasePath },
		func(r *Req, val usecase.BasePath) { r.BasePath = val },
	),
	modulesummary.UseCaseNameField(
		func(r Req) usecase.Name { return r.UseCaseName },
		func(r *Req, val usecase.Name) { r.UseCaseName = val },
	),
	modulesummary.ModuleNameField(
		func(r Req) iothub.ModuleName { return r.ModuleName },
		func(r *Req, val iothub.ModuleName) { r.ModuleName = val },
	),
	c.RequiredField("imageURL",
		c.String().Refine(v.NonEmptyString).WithDescription(
			"The full container image reference to set, e.g. \"ghcr.io/org/repo:1.2.3\".",
		),
		func(r Req) string { return r.ImageURL },
		func(r *Req, val string) { r.ImageURL = val },
	),
	modulesummary.DeviceIDField(
		func(r Req) usecase.DeviceID { return r.DeviceID },
		func(r *Req, val usecase.DeviceID) { r.DeviceID = val },
	),
)

// Tool is the declared, UNREGISTERED MCP tool contract for updating one
// module's container image in a deployment manifest file — OR, when
// DeviceID is set, ONE device's own config file only (see
// Req.DeviceID) — returning the module's UPDATED reduced summary
// (image, host-mapped ports, binds, status, restart policy) — the same
// "declare once, register anywhere" pattern modulesummary.ReadTool
// follows: a caller registers it against their own mcp.Builder
// (Tool.Register(builder)) and pairs the resulting handle with
// app/iotedge's NewUpdateModuleImageToolHandler via mcpgo.ToolHandler.
var Tool = mcp.NewTool[Req, modulesummary.Summary](
	"update_module_image", ReqCodec, modulesummary.SummaryCodec,
	mcp.ToolMeta{
		Description: "Update one module's container image, returning the " +
			"module's updated summary (image, host-mapped ports, binds, " +
			"status, restart policy). If deviceID is set, the update is " +
			"written to that device's OWN config only — the use case " +
			"template and every other device stay untouched; otherwise " +
			"the update is written to the use case template's shared " +
			"manifest.",
	},
)
