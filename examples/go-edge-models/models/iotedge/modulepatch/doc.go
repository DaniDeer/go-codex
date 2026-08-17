// Package modulepatch holds representations DERIVED FROM — not part of —
// the base IoT-Edge manifest wire schema in the sibling
// models/iotedge/manifesttemplate package (Go package manifesttemplate): a
// partial document assembled by reusing manifesttemplate's already-exported
// field-level codecs (manifesttemplate.ModuleNameCodec, manifesttemplate.ImageCodec,
// docker.CreateOptionsCodec, manifesttemplate.EnvVarsCodec, manifesttemplate.TypeCodec,
// manifesttemplate.StatusCodec, manifesttemplate.RestartPolicyCodec, manifesttemplate.VersionCodec)
// to target one module's fields within the real manifest shape. This
// package depends ONLY on manifesttemplate (+docker) — it has ZERO
// dependency on models/iotedge itself, since everything it needs is pure
// wire model, not a derived/constructed concept.
//
// This is a SEPARATE package from manifesttemplate itself (not a file
// inside it) so that:
//   - manifesttemplate stays focused on the base wire schema (ModuleConfig,
//     ModuleSettings, EnvVars, DeploymentManifest, ...), never accumulating
//     one file per derived representation.
//   - a consumer who only needs to patch a module doesn't pull in any
//     other derived representation that might be added later.
//
// This package is a single domain concept (patching one module's fields),
// so it stays in ONE file — fieldspatch.go — holding
// FieldsPatch, FieldsPatchCodec (a HAND-ROLLED codex.Codec —
// see its own doc comment for why codex.Struct cannot express this),
// EmptyPatchError, and NewUpdateModuleImage (a named smart
// constructor for the single most common patch operation) together,
// rather than being split by layer (types.go/codecs.go).
//
// FieldsPatch mirrors ModuleConfig's own field set — EVERY field is
// independently optional and patchable (Image, CreateOptions, Env, Type,
// Status, RestartPolicy, Version), so there is no need for a separate
// sibling package per field (e.g. a "statuspatch"/"restartpolicypatch") —
// any subset of fields is expressed as one FieldsPatch value.
package modulepatch
