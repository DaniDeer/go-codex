// Package docker models the subset of the Docker Engine API's container
// create-options document commonly needed by container-orchestration
// tooling: exposed ports, port bindings, bind mounts, resource limits
// (memory, ulimits), and a healthcheck.
//
// This package has NO dependency on any orchestrator-specific concept (it
// does not know about IoT-Edge, Kubernetes, or Compose) — it models
// Docker's own wire contract literally, so it composes as a building block
// wherever a "createOptions"/"HostConfig"-shaped document needs to be
// decoded, validated, or re-encoded: IoT-Edge module manifests (see the
// sibling `iotedge` package), Docker Compose service definitions, or a
// plain `docker create`/`docker run` wrapper.
//
// Every exported value is a small, independently reusable piece. Files are
// organized ONE PER DOMAIN CONCEPT — each file holds that concept's
// struct(s), any validate.Constraint values it needs, and its codex.Codec[T]
// values together, so understanding one concept never requires jumping
// across files:
//
//   - image.go — Image (parsed Name/Tag/Digest), the Tag/Digest named types
//     and their constraints, ImageCodec, and Image.String().
//   - port.go — Port, PortBindingEntry, PortBinding, and their codecs
//     (PortCodec, PortNumberCodec, ExposedPortsCodec, PortBindingCodec),
//     plus ParsePortMapping/FormatPortMapping — the `docker run -p`-style
//     short-syntax parser/formatter pair (one CLI port-mapping string <->
//     Port + optional host port).
//   - bind.go — Bind and BindCodec (parses "host:container[:mode]").
//   - ulimit.go — Ulimit, ulimitNameConstraint, UlimitNameCodec, UlimitCodec.
//   - healthcheck.go — Healthcheck and HealthcheckCodec, plus
//     CLIDurationCodec/HealthcheckCLICodec (the `docker run
//     --health-interval=30s`-style duration-STRING wire form of the
//     same struct).
//   - env.go — EnvVar/Env and EnvCodec (parses "KEY=VALUE" entries).
//   - hostconfig.go — HostConfig and CreateOptions (composing every concept
//     above) and their codecs, plus IsZeroCreateOptions.
//   - memory.go — ParseMemBytes/FormatMemBytes/MemBytesCodec, the
//     `docker run --memory`-style human byte-size string convention (an
//     alternate STRING wire form of the SAME int64 byte count
//     HostConfig.Memory/MemorySwap already use via their own c.Int64()
//     field codecs).
//
// Each field's codec is its own named value (e.g. UlimitNameCodec,
// PortCodec, dockerNanosDurationCodec) so a caller assembling a NEW wire
// codec — for example a "patch this module's image" codec that only
// touches one field — can reuse the exact same field-level codec rather
// than re-deriving it.
package docker
