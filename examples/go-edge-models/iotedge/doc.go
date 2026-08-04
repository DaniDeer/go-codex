// Package iotedge models an Azure IoT-Edge deployment manifest: modules
// keyed by a dotted desired-properties path, each module's settings (image
// + Docker create-options), environment variables, and lifecycle metadata
// (type/status/restartPolicy/version).
//
// This package imports the sibling `docker` package for its
// ModuleSettings.CreateOptions field — the dependency is one-directional
// (docker never imports iotedge), so `docker` stays independently reusable
// for non-IoT-Edge use cases.
//
// Every exported value is a small, independently reusable piece:
//
//   - types.go — plain Go structs and named types (ModuleConfig,
//     ModuleSettings, EnvVars, ModuleName, DeploymentManifest, ...), no
//     codec logic.
//   - constraints.go — validate.Constraint values used by the codecs below.
//   - codecs.go — codex.Codec[T] values, built by composing the types and
//     constraints above via RequiredField/OptionalField. Each field's codec
//     is its own named value (e.g. ImageCodec, ModuleNameCodec) so a caller
//     assembling a NEW wire codec — for example a "patch this module's
//     image, keyed by module name" codec — can reuse the exact same
//     field-level codec rather than re-deriving it.
package iotedge
