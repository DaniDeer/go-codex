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
- `targetfields.go` — `BasePathField`/`UseCaseNameField`/`ModuleNameField`/`DeviceIDField`: generic `codex.FieldCodec[T]` builders shared by `ReadReq` and `updatemoduleimage.Req` — single source of truth for the key names/codecs/descriptions both request types share. `BasePathField`/`UseCaseNameField` use `usecase.BasePathCodec`/`usecase.NameCodec` directly (this package now depends on `models/iotedge/usecase`). `DeviceIDField` uses its OWN `deviceIDFieldCodec` — typed as `usecase.DeviceID`, but deliberately NOT `usecase.DeviceIDCodec` as-is: `codex.Struct.Encode` unconditionally writes every field (including an unset OPTIONAL one at its zero value), so `usecase.DeviceIDCodec`'s non-empty constraint (which applies on both Encode and Decode via `Refine`) would break round-tripping a "no device" request; `deviceIDFieldCodec` accepts empty on both directions instead — see its own doc comment.
- `ReadReq`/`ReadReqCodec` — the read tool's input (`BasePath usecase.BasePath`, `UseCaseName usecase.Name`, module name — regular OR system module — and an OPTIONAL `DeviceID usecase.DeviceID` — set, returns that device's actual configured module; empty, returns the use case template's own module). `BasePath`/`UseCaseName`'s field descriptions (surfaced to an LLM caller) live ENTIRELY on `usecase.BasePathCodec`/`usecase.NameCodec` — not duplicated here.
- `ReadTool` — the declared, unregistered MCP tool contract (`"read_module_summary"`); implemented by `app/iotedge`'s `NewReadModuleSummaryToolHandler`.

The sibling [`updatemoduleimage`](../updatemoduleimage) package depends
on this one for `Summary` — updating a module's image returns its
updated summary.
