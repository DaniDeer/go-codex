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
// This package is a single domain concept (ModulePatch), so it stays in
// ONE file — modulepatch.go — holding the struct, its internal wire-shape
// mirror types (imageSettingsPatch/moduleConfigPatch/modulesContentPatch/
// manifestImagePatch), and ModulePatchCodec together, rather than being
// split by layer (types.go/codecs.go).
//
// Any FUTURE derived representation (e.g. a status-only or
// restart-policy-only patch) should live in its own sibling package under
// iotedge/ (e.g. iotedge/statuspatch), following the same
// single-file-per-concept pattern, importing iotedge directly — never the
// reverse.
package modulepatch
