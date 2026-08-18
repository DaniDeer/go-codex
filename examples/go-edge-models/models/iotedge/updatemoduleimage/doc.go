// Package updatemoduleimage holds the DECLARATIVE contract for updating
// one module's container image — either in the use case template's
// shared deployment manifest, or (when Req.DeviceID is set) in ONE
// device's OWN config file only, leaving the template and every other
// device untouched — the request type+codec and the MCP tool
// declaration. Pure data — no I/O, no ports.File. The concrete
// implementation (which actually parses the image, applies the patch,
// and re-reads the module's summary) lives in the sibling app/iotedge
// package's NewUpdateModuleImageToolHandler, built on top of this
// declaration.
//
// Kept as its own package, separate from
// [github.com/DaniDeer/go-codex/examples/go-edge-models/models/iotedge/modulesummary],
// mirroring modulepatch's existing separateness from manifesttemplate —
// this package imports modulesummary for its Summary response type.
package updatemoduleimage
