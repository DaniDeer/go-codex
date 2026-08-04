// Package modulepatch holds representations DERIVED FROM — not part of —
// the base IoT-Edge manifest wire schema in the sibling iotedge package: a
// partial document assembled by reusing iotedge's already-exported
// field-level codecs (iotedge.ModuleNameCodec, iotedge.ImageCodec) to target
// one small slice of the real manifest shape.
//
// This is a SEPARATE package from iotedge itself (not a file inside it) so
// that:
//   - iotedge stays focused on the base wire schema (ModuleConfig,
//     ModuleSettings, EnvVars, DeploymentManifest, ...), never accumulating
//     one file per derived representation.
//   - a consumer who only needs the image-patch shape doesn't pull in any
//     other derived representation that might be added later.
//
// Any FUTURE derived representation (e.g. a status-only or
// restart-policy-only patch) should live in its own sibling package under
// iotedge/ (e.g. iotedge/statuspatch), following the same pattern:
// types.go (plain structs) + codecs.go (codecs composed from iotedge's
// exported building blocks), importing iotedge directly — never the
// reverse.
package modulepatch
