// Package iotedge is the CONCRETE IMPLEMENTATION built on top of the
// sibling models/iotedge package's declared contract: the ConfigFile
// port (models/iotedge/configfile.go), the ModuleSummary reduced view
// (models/iotedge/modulesummary.go), the ReadModuleSummaryTool/
// UpdateModuleImageTool MCP contracts
// (models/iotedge/readmodulesummary.go,
// models/iotedge/updatemoduleimage.go), and the ModuleFieldsPatch patch
// mechanism (models/iotedge/modulepatch). This package holds everything
// that actually touches disk: reading a deployment manifest file,
// patching one, and both MCP tool handler bindings.
//
// If you only need to declare a request/response shape, generate a spec,
// or build your OWN file-reading/patching logic against these wire
// formats, import models/iotedge (and models/iotedge/modulepatch)
// instead — they have zero dependency on this package (the dependency
// only ever goes this direction: app/iotedge imports models/iotedge,
// never the reverse) and pull in no concrete ports.File machinery beyond
// what a caller explicitly constructs themselves.
//
// File layout:
//
//   - configfile.go: ReadConfig (reads a deployment manifest file),
//     PatchModule (applies a modulepatch.ModuleFieldsPatch — any subset
//     of one module's fields), and UpdateModuleImage (a thin convenience
//     over PatchModule for the single most common case: updating one
//     module's image, validated via
//     modulepatch.NewUpdateModuleImagePatch).
//   - readmodulesummary.go: NewReadModuleSummaryToolHandler,
//     readModuleSummary (the shared "read manifest, find module,
//     summarize" helper also used by updatemoduleimage.go), and
//     ModuleNotFoundError (the module name wasn't present in the
//     manifest).
//   - updatemoduleimage.go: NewUpdateModuleImageToolHandler (parses the
//     tool's plain ImageURL string, checks the module already exists —
//     reusing readModuleSummary — before patching, so a nonexistent
//     module fails with a clean ModuleNotFoundError rather than a
//     confusing low-level decode error, applies the update, then returns
//     the module's UPDATED summary).
//
// This package's PUBLIC SURFACE:
//
//  1. ReadConfig/PatchModule/UpdateModuleImage (configfile.go) — the
//     PRIMARY, batteries-included entry points. A caller supplies a
//     concrete manifest file path per call; this package never assumes
//     or hardcodes where a manifest lives.
//  2. NewReadModuleSummaryToolHandler/NewUpdateModuleImageToolHandler
//     (readmodulesummary.go/updatemoduleimage.go) — ready-made MCP tool
//     handler bindings wrapping (1) directly. Pair a returned handler
//     with models/iotedge's matching declared Tool
//     (Tool.Register(mcpBuilder)) via mcpgo.ToolHandler; see either
//     constructor's own doc comment for the full usage snippet.
package iotedge
