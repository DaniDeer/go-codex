// Package iotedge is an ORGANIZATIONAL directory only — it holds no Go
// files of its own beyond this doc comment. It contains OUR OWN
// layering strategy for organizing/locating Azure IoT Edge device
// configuration — a GLOBAL baseline (fleet-wide defaults) + per-use-case
// TEMPLATES (one shared manifest per group of devices) + per-device
// PATCHES, merged into the final, deployable-to-IoT-Hub deployment. See
// this directory's README.md for the full layering overview.
//
// The generic, Azure-documented IoT Hub device-twin wire specification
// (https://learn.microsoft.com/en-us/azure/iot-edge/module-edgeagent-edgehub)
// this layering builds ON TOP OF — $edgeAgent/$edgeHub module
// configuration, the base-vs-layered deployment distinction, routes,
// system modules, environment variables — lives in the
// [github.com/DaniDeer/go-codex/examples/go-edge-models/models/azure/iothub]
// package instead, since it has ZERO knowledge of and zero dependency
// on this package's own layering concept (mirrors
// models/docker's "pure wire spec, zero orchestrator-specific
// knowledge" convention).
//
// Every DERIVED/CONSTRUCTED concept built on top of azure/iothub for
// OUR OWN layering lives in one of the subpackages nested directly
// under this directory:
//
//   - [github.com/DaniDeer/go-codex/examples/go-edge-models/models/iotedge/deviceconfig] —
//     one device's config file's PURE WIRE FORMAT (a patch over its use
//     case's azure/iothub.LayeredDeployment).
//   - [github.com/DaniDeer/go-codex/examples/go-edge-models/models/iotedge/finaldeviceconfig] —
//     Merge, the derived operation that layers a use case's
//     azure/iothub.LayeredDeployment and one device's own
//     deviceconfig.Patch onto the GLOBAL azure/iothub.BaseDeployment,
//     producing the FINAL, deployable-to-IoT-Hub config for that
//     device.
//   - [github.com/DaniDeer/go-codex/examples/go-edge-models/models/iotedge/usecase] —
//     the derived use-case AND device model/composition (templated
//     ports.File/ports.Dir constructors, domain composition structs,
//     the SINGLE GLOBAL baseline file port) built on top of azure/iothub
//     and deviceconfig.
//   - [github.com/DaniDeer/go-codex/examples/go-edge-models/models/iotedge/modulesummary] —
//     the derived, reduced module view + its MCP read tool contract.
//   - [github.com/DaniDeer/go-codex/examples/go-edge-models/models/iotedge/updatemoduleimage] —
//     the derived MCP tool contract for updating one module's image.
//   - [github.com/DaniDeer/go-codex/examples/go-edge-models/models/iotedge/modulepatch] —
//     the derived, general multi-field module patch mechanism.
//
// Each subpackage has its own README.md (short, GitHub-browsable
// overview) alongside its own doc.go (full godoc reference) — see those
// for what each one actually contains.
package iotedge
