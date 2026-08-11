// Package modulepatch holds representations DERIVED FROM — not part of —
// the base IoT-Edge manifest wire schema in the sibling iotedge package: a
// partial document assembled by reusing iotedge's already-exported
// field-level codecs (iotedge.ModuleNameCodec, iotedge.ImageCodec,
// docker.CreateOptionsCodec, iotedge.EnvVarsCodec, iotedge.TypeCodec,
// iotedge.StatusCodec, iotedge.RestartPolicyCodec, iotedge.VersionCodec)
// to target one module's fields within the real manifest shape.
//
// This is a SEPARATE package from iotedge itself (not a file inside it) so
// that:
//   - iotedge stays focused on the base wire schema (ModuleConfig,
//     ModuleSettings, EnvVars, DeploymentManifest, ...), never accumulating
//     one file per derived representation.
//   - a consumer who only needs to patch a module doesn't pull in any
//     other derived representation that might be added later.
//
// This package is a single domain concept (patching one module's fields),
// so it stays in ONE file — modulefieldspatch.go — holding
// ModuleFieldsPatch, ModuleFieldsPatchCodec (a HAND-ROLLED codex.Codec —
// see its own doc comment for why codex.Struct cannot express this),
// EmptyPatchError, and NewUpdateModuleImagePatch (a named smart
// constructor for the single most common patch operation) together,
// rather than being split by layer (types.go/codecs.go).
//
// ModuleFieldsPatch mirrors ModuleConfig's own field set — EVERY field is
// independently optional and patchable (Image, CreateOptions, Env, Type,
// Status, RestartPolicy, Version), so there is no need for a separate
// sibling package per field (e.g. a "statuspatch"/"restartpolicypatch") —
// any subset of fields is expressed as one ModuleFieldsPatch value.
package modulepatch
