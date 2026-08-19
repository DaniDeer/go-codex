# modulesummary

A reduced, read-only view of one module's `iothub.ModuleConfig`
— image, host-mapped ports, bind mounts, status, and restart policy —
plus the declarative MCP tool contract for reading it.

## What's here

- `Summary`/`SummaryCodec` — the reduced view and its codec. `Status`/`RestartPolicy` are OPTIONAL (`*iothub.Status`/`*iothub.RestartPolicy`, nil = not applicable) since a system module's own config may genuinely lack both; `SummaryCodec` is hand-rolled so nil values are omitted on encode instead of failing their enum constraint.
- `NewSummary` — pure mapping from `iothub.ModuleConfig` to `Summary` (regular modules — always sets Status/RestartPolicy).
- `NewSummaryFromSystemModule` — pure mapping from `iothub.SystemModuleConfig` to `Summary` (system modules — Status/RestartPolicy stay nil when unset).
- `IsSystemModuleName`/`SystemModuleConfigFor` — dispatch helpers: is a given `iothub.ModuleName` one of the two reserved system module names ("edgeAgent"/"edgeHub"), and looking one up in an `iothub.SystemModules` value.
- `ModuleOrSystemModuleNameCodec` — the tool-input name codec `ReadReq`/`updatemoduleimage.Req` use, accepting EITHER a regular slug-shaped module name OR "edgeAgent"/"edgeHub" (unlike `iothub.ModuleNameCodec`, which validates a full dotted WIRE key, not a bare tool-input name).
- `ReadReq`/`ReadReqCodec` — the read tool's input (base path, use case name, module name — regular OR system module — and an OPTIONAL device ID — set, returns that device's actual configured module; empty, returns the use case template's own module).
- `ReadTool` — the declared, unregistered MCP tool contract (`"read_module_summary"`); implemented by `app/iotedge`'s `NewReadModuleSummaryToolHandler`.

The sibling [`updatemoduleimage`](../updatemoduleimage) package depends
on this one for `Summary` — updating a module's image returns its
updated summary.
