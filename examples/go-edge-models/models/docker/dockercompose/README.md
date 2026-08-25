# dockercompose

Models the SUBSET of the Docker Compose file format commonly needed by
container-orchestration tooling: a project's named services, each
service's image (or a `build:` marker), exposed/bound ports, volumes,
environment, command/entrypoint, restart policy, healthcheck, and
resource limits (memory, ulimits). Zero dependency on any orchestrator
OTHER than Docker itself — no Azure IoT Edge, Kubernetes, or other
deployment-target knowledge (see the sibling
[`models/iotedge/fromcompose`](../../iotedge/fromcompose) package for
the one place that bridges this package's types to
[`models/azure/iothub`](../../azure/iothub)'s).

## Layering

- **[`models/docker`](..)** — general Docker/CLI concepts (zero Compose
  knowledge). This package depends on it directly, reusing
  `docker.Bind`/`docker.Env`/`docker.Port`/`docker.PortBinding`/
  `docker.PortMapping`/`docker.PortMappingCodec`/`docker.Ulimit`/
  `docker.UlimitNameCodec`/`docker.Healthcheck`/`docker.Image`/
  `docker.MemBytesCodec`/`docker.CLIDurationCodec` wherever Compose's
  own wire shape happens to be IDENTICAL to (or reuses the exact same
  convention as) a real `docker`/`docker run` concept.
- **`dockercompose`** (this package) — the Compose FILE FORMAT itself:
  the short-syntax STRING forms (ports/volumes), the `ulimits` bare-int-
  or-object YAML shorthand, and the `disable: true` healthcheck flag —
  genuinely Compose-only conventions with no `docker` CLI equivalent.
- **[`models/iotedge/fromcompose`](../../iotedge/fromcompose)** — the
  ONLY package that knows about both this package and IoT Edge.

## What's here

- `project.go` — `ServiceName`/`Project`/`ProjectCodec`, plus the
  exported `ServicesCodec` (`codex.Map[ServiceName, Service]`) — a
  project's named services map. `ServicesCodec` is reused directly by
  the sibling `fromcompose` package's own Project↔LayeredDeployment
  transcoding.
- `service.go` — `Service`/`ServiceCodec` (image, `build` decoded via
  `BuildCodec` into a real `Build` value — Compose's SHORT string form
  (`build: ./app`) AND LONG object form (`{context, dockerfile, args,
  target}`), always re-encoding to the long object form; `Build.IsSet()`
  is the "no build: key at all" zero-value sentinel, mirroring
  `Service.HasImage()`,
  `ports` decoded DIRECTLY into `[]docker.PortMapping`, volumes,
  environment, command/entrypoint, hostname/domainname, restart,
  `healthcheck` decoded DIRECTLY into `docker.Healthcheck`,
  mem_limit/mem_reservation, `ulimits` decoded DIRECTLY into
  `[]docker.Ulimit`) plus `Service.HasImage()`. `ServiceCodec` enforces
  TWO cross-field invariants via `.Refine(...)` (see
  `docs/concepts/codec.md`'s "Whole-struct (cross-field) constraints"
  for the mechanism): **`image-or-build-required`** — a service
  declaring neither `image:` nor `build:` is a genuine Compose spec
  violation and is rejected at decode time (both together IS allowed);
  **`mem-reservation-not-exceeding-mem-limit`** — `mem_reservation` must
  not exceed `mem_limit` when both are set. `ServiceCodec` is built on
  top of the unexported, UNCONSTRAINED `serviceFieldsCodec` — see
  **`CreateOptionsFromServiceCodec`** below for why that split exists.
  `CreateOptionsFromServiceCodec` — a `Codec[docker.CreateOptions]`
  backed by `serviceFieldsCodec` (NOT the constrained `ServiceCodec` —
  its Service side is always PARTIAL, Image/Build/Restart intentionally
  left unset, so it can never satisfy `image-or-build-required` by
  construction), built via `codex.MapCodecValidated`, the genuine
  "Docker ↔ docker-compose" mapping. `Service.CreateOptionsFor()`/
  `ServiceFromCreateOptions()` are thin, ergonomic Service-typed
  wrappers around that same codec's to/from functions — zero
  orchestrator vocabulary whatsoever. A malformed `ports:` entry now
  fails `ServiceCodec.Decode` itself
  (matching every other field's already-strict behavior), rather than
  being collected as a per-service warning.
- `healthcheck.go` — `ComposeHealthcheck`/`ComposeHealthcheckCodec` (the
  Compose-wire-shape intermediate, reusing `docker.CLIDurationCodec` per
  timing field) and **`HealthcheckFromComposeCodec`** —
  `Codec[docker.Healthcheck]` backed by that wire shape, resolving
  Compose's `disable: true` into Docker's own `Test:["NONE"]` sentinel.
- `ulimit.go` — `ComposeUlimit`/`ComposeUlimitCodec` — the bare-int-or-
  `{soft,hard}`-object union (`codex.UntaggedUnion`, mirrors
  `iothub.EnvVarValueCodec`'s pattern) — and **`UlimitsCodec`** —
  `Codec[[]docker.Ulimit]` built via `codex.EntrySlice`, reusing
  `docker.UlimitNameCodec` as the key codec (so Compose ulimit names are
  now validated against Docker's real `--ulimit` allow-list too).

## What's NOT modeled

Silently ignored on decode (`codex.Struct` is forward-compatible by
default — see its own doc comment): top-level `networks:`/`volumes:`/
`configs:`/`secrets:`; per-service `depends_on`/`networks`/`labels`/
`deploy`/`profiles`/`logging`; `command`/`entrypoint` STRING form (list
form only); `environment` MAP form (list form only — a map-form
`environment:` produces a clear decode error for that field instead).
