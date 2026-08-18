# updatemoduleimage

The declarative MCP tool contract for updating one module's container
image in a deployment manifest file — pure data, no I/O.

## What's here

- `Req`/`ReqCodec` — the tool's input (base path, use case name, module name, image URL, and an OPTIONAL device ID — set, updates ONLY that device's own config; empty, updates the use case template).
- `Tool` — the declared, unregistered MCP tool contract (`"update_module_image"`); returns the module's updated [`modulesummary.Summary`](../modulesummary); implemented by `app/iotedge`'s `NewUpdateModuleImageToolHandler`.

Kept as its own package, separate from
[`modulesummary`](../modulesummary), mirroring `modulepatch`'s existing
separateness from `manifesttemplate`.
