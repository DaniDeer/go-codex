# fromcompose

Converts BIDIRECTIONALLY between a Docker Compose
[`dockercompose.Project`](../../docker/dockercompose) and a SCAFFOLD
Azure IoT Edge `iothub.LayeredDeployment` — the ONLY package depending on
BOTH `dockercompose` AND [`models/azure/iothub`](../../azure/iothub) at
once (mirrors the sibling [`finaldeviceconfig`](../finaldeviceconfig)
package's own "derived, depends on both wire formats, kept separate
from either" shape).

## The core idea: one bidirectional codec, not a pair of hand-rolled functions

The actual VALUE transformation is ONE declaration:

```go
var ModuleConfigFromServiceCodec = codex.MapCodecValidated(
    dockercompose.ServiceCodec, iothub.ModuleConfigCodec,
    serviceToModuleConfig, moduleConfigToService,
)
```

`codex.MapCodecValidated` always builds **both** Encode and Decode from
one declaration, so this single value — a `codex.Codec[iothub.ModuleConfig]`
backed by `dockercompose.ServiceCodec`'s own wire shape — works directly
with `format.YAML`/`format.JSON` in **either** direction, with zero
hand-rolled JSON/YAML walking anywhere in this package. It also gets
`iothub.ModuleConfigCodec`'s own `Refine` constraints (the `Status`/
`RestartPolicy` enums, `Type`'s fixed value, `Version`'s non-empty check)
enforced **automatically** via `MapCodecValidated`'s built-in
`cb.Validate(b)` call — zero duplicated validation code here.

`ServicesToModulesCodec` extends the same idea to the whole-project
level: a `codex.Map[iothub.ModuleName, iothub.ModuleConfig]` pairing a
KEY codec (`ServiceName`→`ModuleName` sanitization, via
`codex.MapCodecSafe`) with `ModuleConfigFromServiceCodec` as the VALUE
codec. `ConvertProject`/`ConvertDeployment` transcode an **entire**
project↔deployment by re-encoding one side to wire via
`dockercompose.ServicesCodec` and decoding that **same** wire value via
`ServicesToModulesCodec` (or the reverse) — "map Codec A to Codec B"
applied at the whole-collection level, not just per-field.

## What's here

- `service.go` — `ModuleConfigFromServiceCodec` (the bidirectional
  codec above), `ConvertService`/`ConvertModuleConfig` (single-service
  entry points, routing through the codec), and the pure, name-agnostic
  `serviceToModuleConfig`/`moduleConfigToService` mapping functions.
  Warnings live OUTSIDE the pure codec — `warningsForService`/
  `warningsForModuleConfig` re-derive the same facts (sanitized name,
  placeholder image, approximated restart policy, unsupported port) by
  comparing input to output, reusing the exact predicates the pure
  functions call internally.
- `project.go` — `ServicesToModulesCodec` (the whole-project codec
  above), `ConvertProject`/`ConvertDeployment` (project-level entry
  points).
- `restartpolicy.go` — `restartPolicyFor`/`composeRestartFor`: the
  bidirectional `restart:` string ↔ `iothub.RestartPolicy` enum mapping
  table (`"unless-stopped"` and `iothub.RestartPolicy("on-unhealthy")`
  are each flagged as APPROXIMATED — no exact equivalent in the other
  format).
- `warning.go` — `Warning`/`WarningKind` — a plain `[]Warning`, NOT an
  error; conversion ALWAYS SUCCEEDS in either direction for a
  syntactically valid input. "Syntactically valid" is enforced one
  layer down, by `dockercompose.ServiceCodec` itself: a service
  declaring NEITHER `image:` NOR `build:` is a genuine Compose spec
  violation (Compose itself requires at least one), so it is rejected
  at `ServiceCodec`/`ProjectCodec` decode time — before it ever reaches
  this package. A service declaring `build:` alone (no `image:`) IS
  syntactically valid and reaches this package normally, which is where
  the placeholder-image substitution + `WarningPlaceholderImage` below
  applies.

## Scaffold, not full fidelity — in BOTH directions

- **Forward** (Compose → IoT Edge): `$edgeHub` routes always empty
  (never mechanically derivable from Compose); system modules/runtime/
  registry-credentials/store-and-forward never populated (belong to the
  GLOBAL baseline layer — see the parent [`models/iotedge`](../) layering
  strategy).
- **Reverse** (IoT Edge → Compose): a module's real build CONTEXT can't
  be reconstructed (a placeholder image correctly reverses to a minimal
  `Build{Context: "."}`, but nothing more specific — no dockerfile/args/
  target); `mem_reservation` has no corresponding
  `docker.HostConfig` field at all (permanent, documented one-way loss);
  a service's original name casing/underscores are unrecoverable once
  sanitized; `$edgeHub` routes have nothing to reverse onto (simply
  absent from the reconstructed project).

See the sibling [`app/iotedge`](../../../app/iotedge) package's
`ImportDockerComposeAsUseCase`/`ExportUseCaseAsDockerCompose` for the
concrete file-I/O wrappers built on top of this package's pure,
in-memory conversion functions.
