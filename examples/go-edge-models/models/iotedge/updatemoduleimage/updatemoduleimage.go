package updatemoduleimage

import (
	mcp "github.com/DaniDeer/go-codex/api/mcp"
	c "github.com/DaniDeer/go-codex/codex"
	iothub "github.com/DaniDeer/go-codex/examples/go-edge-models/models/azure/iothub"
	"github.com/DaniDeer/go-codex/examples/go-edge-models/models/iotedge/modulesummary"
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
	// BasePath is the root directory holding the "usecases/" subtree —
	// see usecase.NewFile's own doc comment for the templated
	// "{basePath}/usecases/{usecase_name}.json" path this composes into.
	BasePath string
	// UseCaseName is the use case whose deployment manifest to update.
	UseCaseName string
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
	// value) means "update the use case template's shared manifest" —
	// this package deliberately stays decoupled from
	// models/iotedge/usecase (a plain string, not usecase.DeviceID),
	// matching BasePath/UseCaseName's own convention above.
	DeviceID string
}

// ReqCodec validates a Req value — BasePath/UseCaseName/ModuleName/
// ImageURL are required; DeviceID is OPTIONAL (empty = template scope).
var ReqCodec = c.Struct[Req](
	c.RequiredField("basePath",
		c.String().Refine(v.NonEmptyString).WithDescription(
			"The root directory holding the \"usecases/\" subtree.",
		),
		func(r Req) string { return r.BasePath },
		func(r *Req, val string) { r.BasePath = val },
	),
	c.RequiredField("useCaseName",
		c.String().Refine(v.NonEmptyString).WithDescription(
			"The use case whose deployment manifest to update.",
		),
		func(r Req) string { return r.UseCaseName },
		func(r *Req, val string) { r.UseCaseName = val },
	),
	c.RequiredField("moduleName",
		modulesummary.ModuleOrSystemModuleNameCodec.WithDescription(
			"The name of the module to update, e.g. \"factory-dashboard\" — "+
				"or \"edgeAgent\"/\"edgeHub\" for a system module's own image.",
		),
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
	c.OptionalField("deviceID",
		c.String().WithDescription(
			"If set, the update is written to THIS DEVICE'S OWN config "+
				"file only — the use case template and every other "+
				"device are left untouched.",
		),
		func(r Req) string { return r.DeviceID },
		func(r *Req, val string) { r.DeviceID = val },
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
