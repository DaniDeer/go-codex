# modulesummary

A reduced, read-only view of one module's `manifesttemplate.ModuleConfig`
— image, host-mapped ports, bind mounts, status, and restart policy —
plus the declarative MCP tool contract for reading it.

## What's here

- `Summary`/`SummaryCodec` — the reduced view and its codec.
- `NewSummary` — pure mapping from `manifesttemplate.ModuleConfig` to `Summary`.
- `ReadReq`/`ReadReqCodec` — the read tool's input (base path, use case name, module name, and an OPTIONAL device ID — set, returns that device's actual configured module; empty, returns the use case template's own module).
- `ReadTool` — the declared, unregistered MCP tool contract (`"read_module_summary"`); implemented by `app/iotedge`'s `NewReadModuleSummaryToolHandler`.

The sibling [`updatemoduleimage`](../updatemoduleimage) package depends
on this one for `Summary` — updating a module's image returns its
updated summary.
