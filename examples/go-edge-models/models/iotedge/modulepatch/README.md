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
- `EmptyPatchError` — returned when a patch has nothing set (would be a no-op).
- `NewUpdateModuleImage` — named smart constructor for the single most common patch: updating just a module's image.
