# usecase

Everything DERIVED/CONSTRUCTED for a "use case" and the devices nested
under it: templated `ports.File`/`ports.Dir` constructors, domain
composition structs, and read/write orchestration. The pure wire formats
live in [`azure/iothub`](../../azure/iothub) (base deployment + layered
deployment manifest) and the sibling [`deviceconfig`](../deviceconfig)
(device config) package.

## What's here

**Filesystem layout + identifier types (`config.go`)**

- Single source of truth for every directory name, path-variable name, and derived path template used below (unexported — not a runtime config surface, just consolidation for readability).
- Each of the six path patterns (`baselinePathPattern`/`useCasePathPattern`/`useCaseEntryShape`/`deviceDirPathPattern`/`deviceFilePathPattern`/`deviceEntryShape`) is a `codex.Const[string]` — validated ONCE at package init via `codex.MustConst` (an empty/malformed pattern panics immediately, not silently, at first use) instead of a bare `fmt.Sprintf`-built string. Three of the six (`useCasePathPattern`/`deviceDirPathPattern`/`deviceFilePathPattern` — the ones with `{var}` placeholders a CALLER substitutes, as opposed to `useCaseEntryShape`/`deviceEntryShape`, whose vars ports' own `EntryPattern` EXTRACTS from a discovered filename) additionally get a typed wrapper struct with a `Resolve(name Name[, deviceID DeviceID]) (string, error)` method — a validated, concrete-path-producing accessor that substitutes an already-validated `Name`/`DeviceID` into the template directly, with zero `ports.File` I/O involved. `ports.NewFile`/`NewDir`'s own `{var}` substitution is unaffected — it still consumes each pattern's raw, unsubstituted text via the embedded `Const[string]`'s `String()`. See [`docs/roadmap/validated-const-getter.md`](../../../../../docs/roadmap/validated-const-getter.md) for the underlying `codex.Const[T]`/`Getter[T]` design.
- `Name`/`DeviceID` — named string types for a use case's name and a device's ID, used throughout this package's exported API instead of bare `string`.
- `NameCodec`/`DeviceIDCodec` — validate a `Name`/`DeviceID` (non-empty) and each carry its OWN field description (single source of truth, reused as-is by `modulesummary.UseCaseNameField`; `DeviceIDField` uses its own lenient variant instead — see that package's own doc comment for why an OPTIONAL field can't reuse a non-empty-on-both-directions codec as-is).
- `NewName`/`NewDeviceID` — smart constructors: validate a plain string and return the typed value, or an error.
- `BasePath` — named string type for the root directory holding this ENTIRE package's on-disk layout (baseline + usecases + devices), used by EVERY function below instead of a bare `string`.
- `BasePathCodec` — validates a `BasePath` (non-empty) and canonicalizes it via `filepath.Clean` on decode; carries its OWN description of what "basePath" means for this layout (single source of truth — reused as-is by `modulesummary.BasePathField`, so it also becomes an MCP tool field's LLM-facing description with zero duplicated text).
- `NewBasePath` — smart constructor: validates+canonicalizes a plain string via `BasePathCodec.Decode`, returning the typed value or an error — THE one conversion point a caller (an MCP tool request decode, a CLI flag, this package's own examples) needs; every function below accepts `BasePath` directly.

**Use case (`usecase.go`)**

- `NewBaselineFile` — file port over the SINGLE GLOBAL `"{basePath}/baseline/baseline.json"` (no template variables) wrapping `iothub.BaseDeployment`.
- `UseCase` — pairs a use case's `Name` with its `iothub.LayeredDeployment` and every nested `DeviceConfig`.
- `Read`/`Write` — one-call read/write of the FULL usecase+devices tree.
- `NewFile` — templated file port over `"{basePath}/usecases/{usecase_name}.json"`.
- `NewDir`/`ListNames` — discover every use case `Name` under `"{basePath}/usecases"`.
- `FileFormat`, `BaselineFileFormat`, `DirEntryPattern` — the format/entry-pattern values `NewFile`/`NewBaselineFile`/`NewDir` are built from.

**Devices (`device.go`)**

- `DeviceConfig` — pairs a `DeviceID` with its `deviceconfig.Patch` (a device config file IS a patch over its use case's own template, not a standalone document).
- `ReadDeviceConfig`/`WriteDeviceConfig` — one-call read/write of ONE device's patch.
- `DeviceConfig.Merge(base, template)` — the "one call" convenience for "baseline + template + device config, layered on top"; delegates to the sibling [`finaldeviceconfig`](../finaldeviceconfig) package.
- `ReadEffective(basePath, useCaseName, deviceID, opts) (iothub.BaseDeployment, error)` — reads the GLOBAL baseline, the template, AND the device's config, merging all three in ONE call (combines `NewBaselineFile.Read` + `NewFile.Read` + `ReadDeviceConfig` + `DeviceConfig.Merge`).
- `NewDeviceFile` — templated file port over `"{basePath}/devices/{usecase_name}/{device_id}.json"`.
- `NewDeviceDir`/`ListDeviceIDs` — discover every device `DeviceID` for ONE given use case.
- `DeviceFileFormat`, `DeviceDirEntryPattern` — the format/entry-pattern values the device constructors are built from.
