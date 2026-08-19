# iothub

Models the GENERIC, documented Azure IoT Hub `edgeAgent`/`edgeHub`
device-twin specification —
https://learn.microsoft.com/en-us/azure/iot-edge/module-edgeagent-edgehub
— and nothing else. Plain struct + `codex.Codec[T]` pairs only: no file
I/O, no `ports.File`/`ports.Dir`, no derived/reduced views, no domain
composition, and ZERO knowledge of any particular application's own
device-configuration LAYERING strategy (see the
[`models/iotedge`](../../iotedge) package for one example of that — this
package only models what Azure itself defines).

## Two document shapes

Azure IoT Hub itself distinguishes two deployment document shapes, both
modeled here:

- **`BaseDeployment`** — the FULL, nested base deployment (schema
  version, IoT Edge runtime settings, the two SYSTEM modules —
  `edgeAgent`/`edgeHub` themselves — every common container, `edgeHub`'s
  routes, and store-and-forward configuration) — applied at priority 0
  to every device matching a target condition.
- **`LayeredDeployment`** — the flat-dotted-key LAYERED deployment PATCH
  form (`"properties.desired.modules.<name>"` as ONE literal JSON key
  per module) — an overlay merged on top of a `BaseDeployment` (or
  another `LayeredDeployment`) via IoT Hub's own priority/target-
  condition system.

This package has NO opinion on how many layers an application actually
uses, in what order, or how they're located on disk — that's entirely
the consuming application's own design. See
[`models/iotedge`](../../iotedge)'s own README for this repo's example
application's specific choice (a global baseline + per-use-case
templates + per-device patches, three-way merged).

## File / type layout

| File | Contents |
|---|---|
| `lifecycle.go` | `Type`, `Status`, `RestartPolicy`, `Version`, `StartupOrder` + codecs |
| `moduleconfig.go` | `ModuleConfig`/`ModuleConfigCodec` — one regular module's full desired-state config |
| `modulesettings.go` | `ModuleSettings`, `ImageCodec` re-export, `CreateOptionsFieldCodec` |
| `envvars.go` | `EnvVarName`/`EnvVars`/`EnvVarValue`/`EnvVar`, `FlattenEnvVars` (one-direction mapper to `docker.Env`) |
| `systemmodules.go` | `SystemModuleConfig` (looser-optionality sibling of `ModuleConfig`), `SystemModules` (`BaseDeployment`'s always-both-present edgeAgent/edgeHub pair) |
| `runtime.go` | `RegistryCredential`/`RegistryCredentials`/`RuntimeSettings`/`Runtime` — `$edgeAgent`'s "runtime" document |
| `storeandforward.go` | `StoreAndForwardConfiguration` — `$edgeHub`'s message retention window |
| `schemaversion.go` | `SchemaVersion` |
| `keys.go` | single source of truth for the whole spec's wire-key vocabulary — `ModulesContentKey`/`EdgeAgentKey`/`EdgeHubKey`, `PropertiesDesiredKey`, dotted-key prefixes (`ModuleKeyPrefix`/`RouteKeyPrefix`/`SystemModuleKeyPrefix`), name-segment codecs, and the bare `BaseModulesCodec`/`BaseRoutesCodec` |
| `layereddeployment.go` | `ModuleName`, `Modules`, `LayeredModulesContent`, `LayeredDeployment` + codecs |
| `edgehub.go` | `RouteName`, `Routes`, `RouteTarget`/`NewBrokeredEndpoint`/`UpstreamTarget`, `Route`/`RouteCodec` (hand-rolled — wire value is a single string, not an object) |
| `basedeployment.go` | `EdgeAgentProperties`/`EdgeHubProperties`, `BaseModulesContent`, `BaseDeployment` + codecs |

This package imports `models/docker` for `ModuleSettings.CreateOptions`
— one-directional (`docker` never imports `iothub`), so `docker` stays
independently reusable for non-IoT-Edge use cases.

Every field's codec is its own named, reusable value (e.g. `ImageCodec`,
`ModuleNameCodec`, `RouteNameCodec`) so a caller assembling a NEW wire
codec on top — e.g. `models/iotedge/modulepatch`'s "patch this module's
image, keyed by module name" codec — can reuse the exact field-level
codec rather than re-deriving it.
