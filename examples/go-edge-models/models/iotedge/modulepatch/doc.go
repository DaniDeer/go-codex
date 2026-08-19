// Package modulepatch holds representations DERIVED FROM — not part of —
// the base IoT-Edge manifest wire schema in the
// models/azure/iothub package (Go package iothub): a
// partial document assembled by reusing iothub's already-exported
// field-level codecs (iothub.ModuleNameCodec, iothub.ImageCodec,
// docker.CreateOptionsCodec, iothub.EnvVarsCodec, iothub.TypeCodec,
// iothub.StatusCodec, iothub.RestartPolicyCodec, iothub.VersionCodec)
// AND its wire-key vocabulary (iothub.ModulesContentKey/
// EdgeAgentKey — see azure/iothub/keys.go, the single source of
// truth for these; FieldsPatchCodec's outer wrapping is built from
// them, never re-hardcoded) to target one module's fields within the
// real manifest shape. This package depends ONLY on azure/iothub
// (+docker) — it has ZERO dependency on models/iotedge itself, since
// everything it needs is pure wire model, not a derived/constructed
// concept.
//
// This is a SEPARATE package from azure/iothub itself (not a file
// inside it) so that:
//   - azure/iothub stays focused on the base wire schema (ModuleConfig,
//     ModuleSettings, EnvVars, LayeredDeployment, ...), never accumulating
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
//
// FieldsBodyCodec (FieldsPatch's own patchable fields, encoded WITHOUT
// FieldsPatchCodec's outer module-name-keyed wrapping) is EXPORTED
// specifically to serve as the BRIDGE between this package's
// TEMPLATE-level typed patches and the sibling
// models/iotedge/deviceconfig package's DEVICE-level raw dotted-path
// patches — encoding a FieldsPatch via FieldsBodyCodec produces exactly
// the raw object a caller assigns to
// deviceconfig.Patch.EdgeAgent[string(patch.ModuleName)] (see
// app/iotedge's PatchDeviceModule/UpdateDeviceModuleImage), reusing ALL
// of this package's Image/CreateOptions/Env/Type/Status/RestartPolicy/
// Version validation at the device level with ZERO duplicated logic.
// NonEmptyFieldsPatch is the same "at least one field is set" guard
// FieldsPatchCodec.Encode enforces internally (there, via the richer
// EmptyPatchError{ModuleName}) — exported as a standalone constraint for
// direct FieldsBodyCodec callers, but deliberately NOT wired via .Refine
// onto FieldsBodyCodec itself (that would change FieldsPatchCodec's
// existing EmptyPatchError into a generic ConstraintError — a breaking
// behavior change this package avoids).
package modulepatch
