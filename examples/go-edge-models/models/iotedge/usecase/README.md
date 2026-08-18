# usecase

Everything DERIVED/CONSTRUCTED for a "use case" and the devices nested
under it: templated `ports.File`/`ports.Dir` constructors, domain
composition structs, and read/write orchestration. The pure wire formats
live in the sibling [`manifesttemplate`](../manifesttemplate) (deployment
manifest) and [`deviceconfig`](../deviceconfig) (device config) packages.

## What's here

**Filesystem layout + identifier types (`config.go`)**

- Single source of truth for every directory name, path-variable name, and derived path template used below (unexported — not a runtime config surface, just consolidation for readability).
- `Name`/`DeviceID` — named string types for a use case's name and a device's ID, used throughout this package's exported API instead of bare `string`.
- `NameCodec`/`DeviceIDCodec` — validate a `Name`/`DeviceID` (non-empty).
- `NewName`/`NewDeviceID` — smart constructors: validate a plain string and return the typed value, or an error.

**Use case (`usecase.go`)**

- `UseCase` — pairs a use case's `Name` with its `manifesttemplate.DeploymentManifest` and every nested `DeviceConfig`.
- `Read`/`Write` — one-call read/write of the FULL usecase+devices tree.
- `NewFile` — templated file port over `"{basePath}/usecases/{usecase_name}.json"`.
- `NewDir`/`ListNames` — discover every use case `Name` under `"{basePath}/usecases"`.
- `FileFormat`, `DirEntryPattern` — the format/entry-pattern values `NewFile`/`NewDir` are built from.

**Devices (`device.go`)**

- `DeviceConfig` — pairs a `DeviceID` with its `deviceconfig.Patch` (a device config file IS a patch over its use case's own template, not a standalone document).
- `ReadDeviceConfig`/`WriteDeviceConfig` — one-call read/write of ONE device's patch.
- `DeviceConfig.Merge(template)` — the "one call" convenience for "template + device config, layered on top"; delegates to the sibling [`finaldeviceconfig`](../finaldeviceconfig) package.
- `ReadEffective(basePath, useCaseName, deviceID, opts)` — reads the template AND the device's config, merging them in ONE call (combines `NewFile.Read` + `ReadDeviceConfig` + `DeviceConfig.Merge`).
- `NewDeviceFile` — templated file port over `"{basePath}/devices/{usecase_name}/{device_id}.json"`.
- `NewDeviceDir`/`ListDeviceIDs` — discover every device `DeviceID` for ONE given use case.
- `DeviceFileFormat`, `DeviceDirEntryPattern` — the format/entry-pattern values the device constructors are built from.
