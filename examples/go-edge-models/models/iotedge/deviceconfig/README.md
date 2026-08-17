# deviceconfig

The pure **wire format** for one device's device-specific config file —
the exact JSON shape found on disk at
`<basePath>/devices/<usecase_name>/<device_id>.json`. Plain struct +
codec only: no file I/O, no `ports`, no derived/reduced views.

## What's here

- `Manifest` — a device's config content (`DisplayName`, `Enabled`).
- `ManifestCodec` — validates a `Manifest` value.

See the sibling [`models/iotedge/usecase`](../usecase) package for the
templated file/dir ports and domain composition (`DeviceConfig`,
`ReadDeviceConfig`/`WriteDeviceConfig`) built on top of this type.
