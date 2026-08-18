# deviceconfig

The pure **wire format** for one device's device-specific config file —
the exact JSON shape found on disk at
`<basePath>/devices/<usecase_name>/<device_id>.json`. A real device
config file IS a **patch** over its use case's own deployment manifest
— dotted keys reaching to arbitrary depth inside a module (a whole
module, one env var, one settings field, ...), plus whole-route
add/override under `$edgeHub`. Plain struct + codec only: no file I/O,
no `ports`, no derived/reduced views, and — deliberately — no merge
logic (see [`finaldeviceconfig`](../finaldeviceconfig) for that).

## What's here

- `Patch` — `EdgeAgent map[string]any` (bare dotted paths, e.g. `"factory-opcua-gateway"` or `"factory-opcua-gateway.env.API_URL"`) + `EdgeHub map[manifesttemplate.RouteName]manifesttemplate.Route` (whole-route add/override).
- `PatchCodec` — hand-rolled codec for the `{"modulesContent": {"$edgeAgent"?: {...}, "$edgeHub"?: {...}}}` wire shape; each bucket omitted when empty.
- `EmptyPatchError` — returned when a patch has nothing set (would be a no-op).

## What a Patch can reach

- **Every `ModuleConfig` field** — `settings.image`, `settings.createOptions`, `env` (whole map or one `env.KEY`), `type`, `status`, `restartPolicy`, `version` — all reachable via dotted paths, since `Merge` deep-merges maps key-by-key at every level.
- **An entirely new module** — a bare module-name key (no dotted suffix) whose value is a whole `ModuleConfig`-shaped map introduces a module that doesn't exist in the use case template yet. Build it ergonomically with a fully-populated `modulepatch.FieldsPatch` encoded via `modulepatch.FieldsBodyCodec` (see `app/iotedge.PatchDeviceModule`), rather than hand-rolling `map[string]any`.
- **Limitation**: `settings.createOptions` is patchable only as ONE atomic, already-JSON-escaped string (matching its own wire shape) — reaching further inside it (e.g. `...createOptions.HostConfig.Binds`) is NOT supported and fails with a generic `codex.TypeMismatchError` at merge time. This is intentional — encode a whole new `createOptions` string instead.

See the sibling [`finaldeviceconfig`](../finaldeviceconfig) package for
`Merge` (layering a `Patch` onto a `manifesttemplate.DeploymentManifest`
to produce the final config), and [`models/iotedge/usecase`](../usecase)
for the templated file/dir ports and domain composition (`DeviceConfig`,
`ReadDeviceConfig`/`WriteDeviceConfig`, `DeviceConfig.Merge`) built on
top of this type.
