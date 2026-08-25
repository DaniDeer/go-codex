// Package usecase holds everything DERIVED/CONSTRUCTED for a "use case"
// AND the devices nested under it — never the wire formats themselves.
// Device functionality lives here rather than its own separate
// subpackage because devices nest INSIDE a use case in the real
// filesystem layout ("{basePath}/devices/{usecase_name}/{device_id}.json"),
// so the two concepts belong together as one aggregate.
//
// The pure wire formats live in
// [github.com/DaniDeer/go-codex/examples/go-edge-models/models/azure/iothub]
// (both the global base deployment AND the layered deployment manifest
// shapes) and the sibling
// [github.com/DaniDeer/go-codex/examples/go-edge-models/models/iotedge/deviceconfig]
// (device config) packages — this package imports both and builds
// templated ports.File/ports.Dir constructors, domain composition
// structs, and read/write orchestration on top.
//
// File layout:
//
//   - config.go — the SINGLE SOURCE OF TRUTH for this package's
//     filesystem layout AND for its three path-derived identifier
//     types: every directory name, path-variable name, and derived
//     path template/filename-shape constant used by the file ports
//     below (useCasesDirName/devicesDirName,
//     useCaseNameVar/deviceIDVar/useCaseEntryVar,
//     baselinePathPattern/useCasePathPattern/useCaseEntryShape/
//     deviceDirPathPattern/deviceFilePathPattern/deviceEntryShape —
//     all unexported; three of the six ALSO substitute vars and are
//     wrapped as a [codex.Template[T]] (useCasePathPattern/
//     deviceDirPathPattern as Template[Name], deviceFilePathPattern as
//     Template[deviceFileVars]) — codex.Template.Build is a validated,
//     concrete-path-producing accessor substituting an
//     already-validated Name/DeviceID into the template directly, with
//     zero ports.File I/O involved; the other three (no vars to
//     substitute) stay a plain [codex.Const[string]] (validated ONCE at
//     package init via codex.MustConst — an empty or malformed pattern
//     panics immediately, not silently, at first use); a
//     readability/consolidation refactor, not a runtime config
//     surface); nameCodec/deviceIDCodec (the raw Codec[string]
//     validators ports.FilePathParam/DirPathParam/EntryParam.Codec
//     require); and the EXPORTED [Name]/[DeviceID]/[BasePath] named
//     types, [NameCodec]/[DeviceIDCodec]/[BasePathCodec] (re-typing the
//     same constraints via c.MapCodecSafe — BasePathCodec additionally
//     canonicalizes via filepath.Clean and carries its OWN description
//     of what "basePath" means, reused as-is anywhere a caller builds a
//     BasePath-typed field, e.g. the sibling modulesummary package's
//     own MCP tool request types), and [NewName]/[NewDeviceID]/
//     [NewBasePath] smart constructors — this package's public, typed
//     vocabulary for "a use case's name" / "a device's ID" / "the
//     use-case layout's root directory", used throughout the rest of
//     this package's API (every basePath parameter below is a [BasePath],
//     never a bare string).
//   - usecase.go — the FULL use-case model + composition: NewBaselineFile
//     (a declared file port over the SINGLE GLOBAL
//     "{basePath}/baseline/baseline.json" — no template variables,
//     unlike every other path here — wrapping
//     iothub.BaseDeployment); FileFormat
//     and NewFile (a declared, TEMPLATED file port over
//     "{basePath}/usecases/{usecase_name}.json" wrapping
//     iothub.LayeredDeployment — usecase_name is a plain,
//     non-merge ports.FilePathParam, validated but never merged into
//     the wire struct); DirEntryPattern, NewDir, and ListNames
//     (discovers every usecase_name under "{basePath}/usecases",
//     returning []Name); UseCase (pairs a Name with its PURE
//     iothub.LayeredDeployment AND every DeviceConfig nested
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
//     DeviceConfig.Merge (the "one call" convenience for "baseline +
//     template + device config, layered on top" — delegates to the
//     sibling
//     [github.com/DaniDeer/go-codex/examples/go-edge-models/models/iotedge/finaldeviceconfig]
//     package's Merge function), and ReadEffective (a further
//     convenience combining NewBaselineFile's Read + NewFile's Read +
//     ReadDeviceConfig + DeviceConfig.Merge into ONE call, returning a
//     iothub.BaseDeployment — the primitive app/iotedge's device-scoped
//     handlers delegate to instead of duplicating read+merge logic).
package usecase
