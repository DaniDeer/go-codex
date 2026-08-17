// Package iotedge is an ORGANIZATIONAL directory only — it holds no Go
// files of its own beyond this doc comment. Every Azure IoT-Edge wire
// format and every DERIVED/CONSTRUCTED concept built on top of it lives
// in one of the subpackages nested directly under this directory:
//
//   - [github.com/DaniDeer/go-codex/examples/go-edge-models/models/iotedge/manifesttemplate] —
//     the deployment manifest's PURE WIRE FORMAT.
//   - [github.com/DaniDeer/go-codex/examples/go-edge-models/models/iotedge/deviceconfig] —
//     one device's config file's PURE WIRE FORMAT.
//   - [github.com/DaniDeer/go-codex/examples/go-edge-models/models/iotedge/usecase] —
//     the derived use-case AND device model/composition (templated
//     ports.File/ports.Dir constructors, domain composition structs)
//     built on top of the two wire packages above.
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
