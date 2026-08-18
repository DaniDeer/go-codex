# modulepatch

A partial, sparse-inclusion patch for one module's fields within a
deployment manifest — derived from, not part of, the base wire schema in
[`manifesttemplate`](../manifesttemplate). Depends ONLY on
`manifesttemplate` (+`docker`); zero dependency on `models/iotedge`
itself.

## What's here

- `FieldsPatch` — every field independently optional (nil = untouched); mirrors `manifesttemplate.ModuleConfig`'s field set.
- `SettingsPatch` — the nested `Image`/`CreateOptions` group, mirroring `ModuleConfig.Settings`.
- `FieldsPatchCodec`/`SettingsPatchCodec` — hand-rolled codecs handling the runtime-determined module-name wrapping and sparse-field inclusion.
- `FieldsBodyCodec` — `FieldsPatch`'s own patchable fields WITHOUT the outer module-name wrapping; the BRIDGE to `models/iotedge/deviceconfig`'s device-level patches (`Patch.EdgeAgent[moduleName] = FieldsBodyCodec.Encode(patch)`) — reuses this package's full validation at the device level with zero duplicated logic.
- `NonEmptyFieldsPatch` — the "at least one field is set" guard, exported standalone for direct `FieldsBodyCodec` callers (not wired onto `FieldsBodyCodec` itself, to avoid changing `FieldsPatchCodec`'s existing `EmptyPatchError`).
- `EmptyPatchError` — returned when a patch has nothing set (would be a no-op).
- `NewUpdateModuleImage` — named smart constructor for the single most common patch: updating just a module's image.
