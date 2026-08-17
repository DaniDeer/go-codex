// Package modulesummary holds a REDUCED, read-only view of one
// module's manifesttemplate.ModuleConfig — image, host-mapped ports,
// bind mounts, status, and restart policy — plus the DECLARATIVE
// contract for reading it via an MCP tool.
//
// File layout:
//
//   - modulesummary.go — Summary (the reduced view), SummaryCodec, and
//     NewSummary (a pure mapping from manifesttemplate.ModuleConfig).
//   - readmodulesummary.go — ReadReq/ReadReqCodec (the tool's input) and
//     ReadTool (the declared, UNREGISTERED MCP tool contract; the
//     concrete implementation lives in app/iotedge's
//     NewReadModuleSummaryToolHandler).
//
// The [github.com/DaniDeer/go-codex/examples/go-edge-models/models/iotedge/updatemoduleimage]
// package depends on this one for Summary — updating a module's image
// returns its updated Summary.
package modulesummary
