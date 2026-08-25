// Package iotedge is the CONCRETE IMPLEMENTATION built on top of the
// sibling models/iotedge subpackages' declared contracts: the global
// baseline deployment (models/azure/iothub), the UseCase file port
// (models/iotedge/usecase), the Summary reduced view
// (models/iotedge/modulesummary), the ReadTool/Tool MCP contracts
// (models/iotedge/modulesummary, models/iotedge/updatemoduleimage), and
// the FieldsPatch patch mechanism (models/iotedge/modulepatch). This
// package holds everything that actually touches disk: reading a
// deployment manifest file (baseline + template + device config,
// merged), patching one, and both MCP tool handler bindings.
//
// A module name may be a REGULAR module OR one of the two reserved
// system module names, "edgeAgent"/"edgeHub" (see
// modulesummary.IsSystemModuleName) — every read/update entry point
// below dispatches on this uniformly, so callers never need a separate
// concept for "the system module itself" vs. "a regular container".
//
// If you only need to declare a request/response shape, generate a spec,
// or build your OWN file-reading/patching logic against these wire
// formats, import the relevant models/iotedge subpackage (usecase,
// modulesummary, updatemoduleimage, modulepatch) instead — they have
// zero dependency on this package (the dependency only ever goes this
// direction: app/iotedge imports models/iotedge/*, never the reverse)
// and pull in no concrete ports.File machinery beyond what a caller
// explicitly constructs themselves.
//
// File layout:
//
//   - usecase.go: ReadUseCase (reads useCaseName's deployment manifest
//     under basePath — named distinctly from usecase.Read, which
//     additionally assembles every nested device into one composed
//     value), PatchUseCaseModule/UpdateUseCaseModuleImage (patch the
//     use case's shared TEMPLATE — modulepatch.FieldsPatch, any subset
//     of one module's fields; UpdateUseCaseModuleImage AUTO-PROMOTES to
//     a FULL patch via modulepatch.NewUpdateModuleImageFromBase when
//     the module is currently baseline-only), and their DEVICE-scoped
//     analogues PatchDeviceModule/UpdateDeviceModuleImage (patch ONE
//     device's OWN config file only — isolated and reversible, template
//     and every other device untouched; reuse
//     modulepatch.FieldsBodyCodec to bridge the SAME typed FieldsPatch
//     validation into deviceconfig.Patch.EdgeAgent, handling both
//     "device already has a config file" (deep-merge via
//     ports.PatchEncoded) and "device's first-ever override" (direct
//     write) cases). PatchUseCaseSystemModule/UpdateUseCaseSystemModuleImage
//     and PatchDeviceSystemModule/UpdateDeviceSystemModuleImage mirror
//     all of the above exactly, one bucket over, for the two reserved
//     system module names ("edgeAgent"/"edgeHub" —
//     iothub.SystemModuleKeyPrefix, a genuinely separate wire
//     bucket from regular modules).
//   - readmodulesummary.go: NewReadModuleSummaryToolHandler,
//     readModuleSummary/effectiveManifest (the shared "read the FULLY
//     layered baseline+template[+device patch] manifest, find the
//     module OR system module, summarize" helper also used by
//     updatemoduleimage.go — ALWAYS baseline-aware, even with no
//     deviceID, so baseline-only modules/system-modules resolve at the
//     template scope too), and ModuleNotFoundError (the name wasn't
//     present in either bucket).
//   - updatemoduleimage.go: NewUpdateModuleImageToolHandler (parses the
//     tool's plain ImageURL string, checks the module/system-module
//     already exists — reusing readModuleSummary, scoped the SAME way
//     the update itself will be — before patching, so an unknown name
//     fails with a clean ModuleNotFoundError rather than a confusing
//     low-level decode error, dispatches to the regular-module or
//     system-module update path via modulesummary.IsSystemModuleName,
//     applies the update to the template or ONE device depending on the
//     request's DeviceID, then returns the UPDATED summary reflecting
//     that same scope).
//
// This package's PUBLIC SURFACE:
//
//  1. ReadUseCase/PatchUseCaseModule/UpdateUseCaseModuleImage
//     (usecase.go) — the PRIMARY, batteries-included TEMPLATE-scoped
//     entry points. A caller supplies a usecase.BasePath + useCaseName
//     per call; this package never assumes or hardcodes where a use
//     case's manifest lives.
//  2. PatchDeviceModule/UpdateDeviceModuleImage (usecase.go) — the
//     DEVICE-scoped analogues, additionally taking a deviceID; write
//     into that device's OWN config file only.
//  3. PatchUseCaseSystemModule/UpdateUseCaseSystemModuleImage and
//     PatchDeviceSystemModule/UpdateDeviceSystemModuleImage (usecase.go)
//     — the system-module ("edgeAgent"/"edgeHub") analogues of (1)/(2).
//  4. NewReadModuleSummaryToolHandler/NewUpdateModuleImageToolHandler
//     (readmodulesummary.go/updatemoduleimage.go) — ready-made MCP tool
//     handler bindings wrapping (1)/(2) directly, selecting template vs.
//     device scope via the declared Req's OPTIONAL DeviceID field (see
//     modulesummary.ReadReq/updatemoduleimage.Req). Pair a returned
//     handler with the matching declared Tool from modulesummary/
//     updatemoduleimage (Tool.Register(mcpBuilder)) via
//     mcpgo.ToolHandler; see either constructor's own doc comment for
//     the full usage snippet.
package iotedge
