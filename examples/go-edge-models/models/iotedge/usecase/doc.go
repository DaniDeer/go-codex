// Package usecase holds everything DERIVED/CONSTRUCTED for a "use case"
// AND the devices nested under it — never the wire formats themselves.
// Device functionality lives here rather than its own separate
// subpackage because devices nest INSIDE a use case in the real
// filesystem layout ("{basePath}/devices/{usecase_name}/{device_id}.json"),
// so the two concepts belong together as one aggregate.
//
// The pure wire formats live in the sibling
// [github.com/DaniDeer/go-codex/examples/go-edge-models/models/iotedge/manifesttemplate]
// (deployment manifest) and
// [github.com/DaniDeer/go-codex/examples/go-edge-models/models/iotedge/deviceconfig]
// (device config) packages — this package imports both and builds
// templated ports.File/ports.Dir constructors, domain composition
// structs, and read/write orchestration on top.
//
// File layout:
//
//   - config.go — the SINGLE SOURCE OF TRUTH for this package's
//     filesystem layout AND for its two path-derived identifier types:
//     every directory name, path-variable name, and derived path
//     template/filename-shape constant used by the file ports below
//     (useCasesDirName/devicesDirName,
//     useCaseNameVar/deviceIDVar/useCaseEntryVar,
//     useCasePathPattern/useCaseEntryShape/deviceDirPathPattern/
//     deviceFilePathPattern/deviceEntryShape — all unexported, a
//     readability/consolidation refactor, not a runtime config
//     surface); nameCodec/deviceIDCodec (the raw Codec[string]
//     validators ports.FilePathParam/DirPathParam/EntryParam.Codec
//     require); and the EXPORTED [Name]/[DeviceID] named types,
//     [NameCodec]/[DeviceIDCodec] (re-typing the same constraints via
//     c.MapCodecSafe), and [NewName]/[NewDeviceID] smart constructors —
//     this package's public, typed vocabulary for "a use case's name" /
//     "a device's ID", used throughout the rest of this package's API.
//   - usecase.go — the FULL use-case model + composition: FileFormat
//     and NewFile (a declared, TEMPLATED file port over
//     "{basePath}/usecases/{usecase_name}.json" wrapping
//     manifesttemplate.DeploymentManifest — usecase_name is a plain,
//     non-merge ports.FilePathParam, validated but never merged into
//     the wire struct); DirEntryPattern, NewDir, and ListNames
//     (discovers every usecase_name under "{basePath}/usecases",
//     returning []Name); UseCase (pairs a Name with its PURE
//     manifesttemplate.DeploymentManifest AND every DeviceConfig nested
//     under it) and Read/Write — the "one struct, one call" convenience
//     for the FULL usecase+devices tree.
//   - device.go — the FULL device model + composition, mirroring
//     usecase.go's file+dir+composition-in-one-file pattern one level
//     down: DeviceFileFormat and NewDeviceFile (a declared, templated
//     file port over
//     "{basePath}/devices/{usecase_name}/{device_id}.json" wrapping
//     deviceconfig.Patch — a device config file IS a patch over its use
//     case's own template, not a standalone document); DeviceDirEntryPattern,
//     NewDeviceDir, and ListDeviceIDs (discovers every device_id for ONE
//     given usecase_name — plain named-var substitution, no glob/
//     wildcard — returning []DeviceID); DeviceConfig (pairs a DeviceID
//     with its PURE deviceconfig.Patch — the domain-level composition,
//     one level down from UseCase), ReadDeviceConfig/WriteDeviceConfig,
//     DeviceConfig.Merge (the "one call" convenience for "template +
//     device config, layered on top" — delegates to the sibling
//     [github.com/DaniDeer/go-codex/examples/go-edge-models/models/iotedge/finaldeviceconfig]
//     package's Merge function), and ReadEffective (a further
//     convenience combining NewFile's Read + ReadDeviceConfig +
//     DeviceConfig.Merge into ONE call — the primitive app/iotedge's
//     device-scoped handlers delegate to instead of duplicating
//     read+merge logic).
package usecase
