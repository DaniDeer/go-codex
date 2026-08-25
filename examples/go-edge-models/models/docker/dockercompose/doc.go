// Package dockercompose models the SUBSET of the Docker Compose file
// format commonly needed by container-orchestration tooling: a
// project's named services, each service's image (or a `build:`
// marker), exposed/bound ports, volumes, environment, command/
// entrypoint, restart policy, healthcheck, and resource limits
// (memory, ulimits).
//
// This package has NO dependency on any orchestrator OTHER than Docker
// itself — it does not know about Azure IoT Edge, Kubernetes, or any
// other deployment target (see the sibling
// [github.com/DaniDeer/go-codex/examples/go-edge-models/models/iotedge/fromcompose]
// package for the ONE place that bridges this package's types to
// [github.com/DaniDeer/go-codex/examples/go-edge-models/models/azure/iothub]'s).
// It DOES depend on the parent
// [github.com/DaniDeer/go-codex/examples/go-edge-models/models/docker]
// package — reusing [docker.Bind]/[docker.Env]/[docker.Port]/
// [docker.PortBinding]/[docker.PortMapping]/[docker.PortMappingCodec]/
// [docker.Ulimit]/[docker.UlimitNameCodec]/[docker.Healthcheck]/
// [docker.Image]/[docker.MemBytesCodec]/[docker.CLIDurationCodec]
// directly wherever Compose's own wire shape happens to be IDENTICAL to
// (or reuses the exact same convention as) a real `docker`/`docker run`
// concept — mirrors [iothub]'s own one-directional dependency on
// `docker`, one level over.
//
// What IS modeled:
//
//   - A project's services map (project.go: [Project]/[ServiceName]).
//   - Per service (service.go: [Service]): every field is OMITTED from
//     Encode when it's at its zero (absent) value — via
//     [github.com/DaniDeer/go-codex/codex.OmitEmptyField]/
//     [github.com/DaniDeer/go-codex/codex.OmitEmptyFieldFunc] — instead
//     of always writing a `build: null`/`command: []`/`domainname: ""`
//     placeholder; every field below already documents its own
//     zero-means-absent convention, which is exactly [codex.OmitEmptyField]'s
//     hard usage rule. `image` (optional — a
//     service may declare `build:` instead, see [Service.HasImage]),
//     `build` (decoded via [BuildCodec] into a real [Build] value —
//     both Compose's SHORT string form, e.g. `build: ./app`, and its
//     LONG object form, e.g. `{context, dockerfile, args, target}`,
//     always re-encoding to the long object form; see [Build.IsSet]
//     for the "no build: key at all" zero-value sentinel), `ports`
//     (short-syntax entries, e.g. "8080:80", decoded
//     DIRECTLY into [docker.PortMapping] via
//     [docker.PortMappingCodec] — a malformed entry now fails decode,
//     matching every other Service field's behavior), `volumes`
//     (short-syntax strings, reusing [docker.Bind] directly),
//     `environment` (LIST form only, reusing [docker.Env] directly),
//     `command`/`entrypoint` (LIST form only), `hostname`/`domainname`,
//     `restart` (kept as the RAW Compose string — mapping to an IoT
//     Edge restart-policy enum is [fromcompose]'s job, not this
//     package's), `healthcheck` (healthcheck.go: decoded DIRECTLY into
//     [docker.Healthcheck] via [HealthcheckFromComposeCodec] — Compose's
//     OWN duration-STRING convention, reusing [docker.CLIDurationCodec]
//     per timing field, plus the `disable` flag Compose adds, resolved
//     into Docker's own Test:["NONE"] sentinel), `mem_limit`/
//     `mem_reservation` (reusing [docker.MemBytesCodec] directly), and
//     `ulimits` (ulimit.go: decoded DIRECTLY into []docker.Ulimit via
//     [UlimitsCodec] — codex.EntrySlice over Compose's own map-keyed,
//     bare-int-or-object shorthand [ComposeUlimit]/[ComposeUlimitCodec],
//     which has NO `docker` CLI equivalent, so THAT intermediate shape
//     is modeled here rather than in the parent package — but reuses
//     [docker.UlimitNameCodec] as the key codec, so ulimit names are
//     validated against Docker's real `--ulimit` allow-list).
//   - [CreateOptionsFromServiceCodec] is a Codec[docker.CreateOptions]
//     backed directly by [ServiceCodec]'s own Compose wire shape — the
//     direct "Docker <-> docker-compose" mapping, built via
//     codex.MapCodecValidated. [Service.CreateOptionsFor]/
//     [ServiceFromCreateOptions] are thin, ergonomic Service-typed
//     wrappers around the same underlying mapping functions. This is
//     PURE "Compose -> Docker" mapping requiring ZERO orchestrator
//     vocabulary, so it lives in THIS package (not one level up in
//     fromcompose, despite feeding directly into an IoT Edge module's
//     own settings there).
//
// What is explicitly NOT modeled (silently ignored on decode, since
// [codex.Struct] is forward-compatible by default — see
// [github.com/DaniDeer/go-codex/codex.Struct]'s own doc comment — not a
// gap requiring extra defensive code here):
//
//   - Top-level `networks:`, `volumes:` (the top-level named-volume
//     declaration block, distinct from a service's own `volumes:`
//     list), `configs:`, `secrets:`.
//   - Per-service `depends_on`, `networks`, `labels`, `deploy`,
//     `profiles`, `logging`.
//   - `command`/`entrypoint` STRING form (list form only).
//   - `environment` MAP form (`{KEY: VALUE}`) — list form only; a
//     map-form `environment:` produces a clear decode error for THAT
//     field rather than silently losing data.
//
// Files are organized ONE PER DOMAIN CONCEPT, mirroring the parent
// `docker` package's own convention:
//
//   - project.go — ServiceName, Project, ProjectCodec, and the exported
//     ServicesCodec (Project's "services" field codec, reused directly
//     by the sibling fromcompose package for its own map transcoding).
//   - service.go — Service, ServiceCodec, Service.HasImage,
//     [CreateOptionsFromServiceCodec], Service.CreateOptionsFor, and its
//     reverse, [ServiceFromCreateOptions] (reconstructs the
//     reconstructable subset of Service's fields purely from a
//     docker.CreateOptions value).
//   - healthcheck.go — ComposeHealthcheck, ComposeHealthcheckCodec (the
//     Compose-wire-shape intermediate), and [HealthcheckFromComposeCodec]
//     (Codec[docker.Healthcheck] backed by that wire shape).
//   - ulimit.go — ComposeUlimit, ComposeUlimitCodec (the bare-int-or-
//     object union), and [UlimitsCodec] (Codec[[]docker.Ulimit] built
//     via codex.EntrySlice).
package dockercompose
