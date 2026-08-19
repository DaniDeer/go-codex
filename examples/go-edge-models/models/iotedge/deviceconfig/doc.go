// Package deviceconfig models the WIRE FORMAT of one device's
// device-specific config file — the exact JSON shape found on disk at
// "<basePath>/devices/<usecase_name>/<device_id>.json" — and NOTHING
// else. A real device config file IS a PATCH over its use case's own
// manifesttemplate.DeploymentManifest — see deviceconfig.go's Patch/
// PatchCodec for the wire shape
// ({"modulesContent": {"$edgeAgent"?: {...}, "$edgeHub"?: {...}}},
// dotted keys reaching to arbitrary depth inside a module, or a whole
// route by name) — the SAME top-level wire-key names
// (manifesttemplate.ModulesContentKey/EdgeAgentKey/EdgeHubKey) and
// dotted-key prefix (manifesttemplate.ModuleKeyPrefix) manifesttemplate
// itself uses, imported from there rather than re-hardcoded here (see
// manifesttemplate/keys.go, the single source of truth for that SHARED
// vocabulary). This package's OWN unique dotted-key vocabulary — the
// EdgeAgentPatchTemplate/edgeAgentPatchCodec pair validating
// Patch.EdgeAgent's wire bucket — lives in this package's own
// keys.go instead.
//
// Applying a Patch to produce the FINAL, layered config is a DERIVED
// operation — NOT part of this wire format — so it lives in the
// sibling
// [github.com/DaniDeer/go-codex/examples/go-edge-models/models/iotedge/finaldeviceconfig]
// package's Merge function instead, keeping this package importable
// standalone by anything that only needs to decode/encode/validate a
// device config file, with zero dependency on manifesttemplate beyond
// the few field-level codecs/constants Patch reuses (ModulesContentKey,
// EdgeAgentKey, EdgeHubKey, ModuleKeyPrefix, RouteNameCodec, RouteCodec).
//
// See the sibling models/iotedge/usecase package for everything ELSE
// BUILT on top of this type: the templated file port (NewDeviceFile),
// the directory-listing port (NewDeviceDir/ListDeviceIDs), and the
// domain composition (DeviceConfig, ReadDeviceConfig/WriteDeviceConfig,
// DeviceConfig.Merge) that pairs a Patch with its device ID.
//
// See Patch's own doc comment in deviceconfig.go for exactly what a
// Patch.EdgeAgent entry can reach: EVERY ModuleConfig field (including
// introducing an entirely new module via a bare module-name key), with
// one accepted limitation — "settings.createOptions" is patchable only
// as one atomic, already-JSON-escaped string, never reached into
// deeper.
package deviceconfig
