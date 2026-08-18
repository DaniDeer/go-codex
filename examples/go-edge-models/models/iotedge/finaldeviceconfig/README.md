# finaldeviceconfig

The derived operation that layers one device's `deviceconfig.Patch` onto
its use case's own `manifesttemplate.DeploymentManifest`, producing the
FINAL, deployable config for that device: "template + device config,
layered on top". Kept as its own package — separate from BOTH
`manifesttemplate` and `deviceconfig` — since it depends on both wire
formats at once, a dependency shape neither wire package may take on
itself.

## What's here

- `Merge(base, patch) (manifesttemplate.DeploymentManifest, error)` — deep-merges `patch` onto `base`, then re-validates the result through `manifesttemplate.DeploymentManifestCodec`.

Merge semantics: **overwrite/add only** — a device config can set or
replace any field at arbitrary depth, but never delete one the template
already declared. See [`models/iotedge/usecase`](../usecase)'s
`DeviceConfig.Merge` for the ergonomic one-call wrapper most callers
should use instead of calling this package directly.
