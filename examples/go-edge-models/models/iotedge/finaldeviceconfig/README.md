# finaldeviceconfig

The derived operation that layers a use case's own
`azure/iothub.LayeredDeployment` and one device's own
`deviceconfig.Patch` onto the GLOBAL `azure/iothub.BaseDeployment`,
producing the FINAL, deployable-to-IoT-Hub config for that device:
"baseline + template + device config, layered on top". Kept as its own
package — separate from `azure/iothub` AND `deviceconfig` — since it
depends on both wire formats at once, a dependency shape neither of
them may take on themselves.

## What's here

- `Merge(base iothub.BaseDeployment, template iothub.LayeredDeployment, patch deviceconfig.Patch) (iothub.BaseDeployment, error)` — three-way merge: a Go-level map UNION for baseline+template's Modules/SystemModules/Routes (template wins on name collision), then `codex.ApplyDottedPatch` for the device patch, then re-validates the result through `iothub.BaseDeploymentCodec`. `schemaVersion`/`runtime`/`storeAndForwardConfiguration` are baseline-only and pass through unchanged.

Merge semantics: **overwrite/add only** — a use case or device config can
set or replace any field at arbitrary depth, but never delete one a
lower layer already declared. See [`models/iotedge/usecase`](../usecase)'s
`DeviceConfig.Merge` for the ergonomic one-call wrapper most callers
should use instead of calling this package directly.
