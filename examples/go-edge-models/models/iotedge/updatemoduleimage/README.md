# updatemoduleimage

The declarative MCP tool contract for updating one module's container
image in a deployment manifest file — pure data, no I/O.

## What's here

- `Req`/`ReqCodec` — the tool's input (base path, use case name, module name — a regular module OR one of the two reserved system module names, "edgeAgent"/"edgeHub", see [`modulesummary.IsSystemModuleName`](../modulesummary) — image URL, and an OPTIONAL device ID — set, updates ONLY that device's own config; empty, updates the use case template). `moduleName` validates via [`modulesummary.ModuleOrSystemModuleNameCodec`](../modulesummary).
- `Tool` — the declared, unregistered MCP tool contract (`"update_module_image"`); returns the module's updated [`modulesummary.Summary`](../modulesummary); implemented by `app/iotedge`'s `NewUpdateModuleImageToolHandler`, which dispatches to the regular-module or system-module update path and AUTO-PROMOTES to a full override when the target currently resolves only via a lower layer (baseline).

Kept as its own package, separate from
[`modulesummary`](../modulesummary), mirroring `modulepatch`'s existing
separateness from `azure/iothub`.
