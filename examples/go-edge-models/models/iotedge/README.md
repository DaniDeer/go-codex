# iotedge

Our OWN layering strategy for organizing/locating Azure IoT Edge device
configuration — not a wire format itself. The generic, Azure-documented
device-twin spec this layering is built on top of
(https://learn.microsoft.com/en-us/azure/iot-edge/module-edgeagent-edgehub)
lives in the [`models/azure/iothub`](../azure/iothub) package instead,
with zero knowledge of anything in this directory.

## The layering

Three layers, applied in order, produce the FINAL, deployable-to-IoT-Hub
config for one device:

```
  baseline (fleet-wide)  →  usecase template (shared per device group)  →  device config (per-device patch)
        │                          │                                          │
        ▼                          ▼                                          ▼
  iothub.BaseDeployment    iothub.LayeredDeployment                 deviceconfig.Patch
   "{basePath}/baseline/          "{basePath}/usecases/           "{basePath}/devices/
    baseline.json"              {usecase_name}.json"          {usecase_name}/{device_id}.json"
        │                          │                                          │
        └──────────────────────────┴──────────────────────────────────────────┘
                                    │
                                    ▼
                         finaldeviceconfig.Merge
                                    │
                                    ▼
                  FINAL iothub.BaseDeployment for that ONE device
```

1. **Baseline** — the SINGLE GLOBAL base deployment every device in the
   fleet shares (priority 0): schema version, Docker runtime settings,
   registry credentials, `$edgeAgent`/`$edgeHub` system modules, and any
   regular modules/routes that should exist EVERYWHERE by default (e.g. a
   security scanner). One file, no template variables.
2. **Use case template** — a shared, named manifest for ONE GROUP of
   devices (e.g. "factory-floor-sensors"): the regular modules/routes
   specific to that group, plus optional system-module overrides.
   Layered ON TOP OF baseline (template wins on name collision).
3. **Device config** — a SPARSE PATCH for ONE device: dotted keys
   reaching to arbitrary depth inside a module or route, isolated and
   reversible without touching the template or any other device.

Merge is OVERWRITE/ADD ONLY (no RFC 7396 null-means-remove semantics) —
each layer may set or replace a field, never delete one a lower layer
already declared.

## Packages

- [`deviceconfig`](deviceconfig) — one device's config file's PURE WIRE FORMAT (a patch over its use case's `iothub.LayeredDeployment`).
- [`finaldeviceconfig`](finaldeviceconfig) — `Merge`, the derived three-way layering operation described above.
- [`usecase`](usecase) — the derived use-case AND device model/composition: templated `ports.File`/`ports.Dir` constructors (including the single global baseline file port), domain composition structs, and `ReadEffective` (baseline + template + device config, merged in one call).
- [`modulesummary`](modulesummary) — a reduced, read-only module view (regular OR system module) + its MCP read tool contract.
- [`updatemoduleimage`](updatemoduleimage) — the derived MCP tool contract for updating one module's image, at template or device scope.
- [`modulepatch`](modulepatch) — the derived, general multi-field module patch mechanism shared by both template- and device-scoped updates.

See [`models/azure/iothub`](../azure/iothub)'s own README for the
underlying wire spec (`BaseDeployment`/`LayeredDeployment`,
`ModuleConfig`, `SystemModules`, routes, environment variables, ...) all
of the above is built on top of, and [`app/iotedge`](../../app/iotedge)
for the concrete implementation (client functions, MCP tool handlers)
built on these declared contracts.
