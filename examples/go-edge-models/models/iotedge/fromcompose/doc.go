// Package fromcompose converts BIDIRECTIONALLY between a Docker Compose
// [github.com/DaniDeer/go-codex/examples/go-edge-models/models/docker/dockercompose.Project]
// and a SCAFFOLD Azure IoT Edge
// [github.com/DaniDeer/go-codex/examples/go-edge-models/models/azure/iothub.LayeredDeployment]:
// [ConvertProject]/[ConvertService] convert Compose -> IoT Edge;
// [ConvertDeployment]/[ConvertModuleConfig] convert IoT Edge -> Compose.
//
// This is the ONLY package that depends on BOTH
// [github.com/DaniDeer/go-codex/examples/go-edge-models/models/docker/dockercompose]
// AND
// [github.com/DaniDeer/go-codex/examples/go-edge-models/models/azure/iothub]
// at once — mirrors the sibling
// [github.com/DaniDeer/go-codex/examples/go-edge-models/models/iotedge/finaldeviceconfig]
// package's own "derived, depends on both wire formats, kept separate
// from either" shape.
//
// # A single bidirectional codec, not a pair of hand-rolled functions
//
// The actual VALUE transformation is ONE declaration:
// [ModuleConfigFromServiceCodec] = codex.MapCodecValidated(dockercompose.ServiceCodec,
// iothub.ModuleConfigCodec, serviceToModuleConfig, moduleConfigToService)
// — a codex.Codec[iothub.ModuleConfig] backed by dockercompose.ServiceCodec's
// OWN wire shape. Because codex.MapCodecValidated always builds BOTH
// Encode and Decode from one declaration, this single value works
// directly with format.YAML/format.JSON in EITHER direction, with ZERO
// hand-rolled JSON/YAML walking anywhere in this package — and
// iothub.ModuleConfigCodec's own Refine constraints (Status's oneOf,
// RestartPolicy's oneOf, Type's fixed value, Version's non-empty check)
// are enforced AUTOMATICALLY via MapCodecValidated's built-in
// cb.Validate(b) call, with zero duplicated validation code here.
//
// [ServicesToModulesCodec] (project.go) extends the SAME idea to the
// whole-project level: a codex.Map[iothub.ModuleName, iothub.ModuleConfig]
// pairing a KEY codec (ServiceName -> ModuleName sanitization, via
// codex.MapCodecSafe) with [ModuleConfigFromServiceCodec] as the VALUE
// codec — [ConvertProject]/[ConvertDeployment] transcode an ENTIRE
// project<->deployment by re-encoding one side to wire via
// dockercompose.ServicesCodec and decoding that SAME wire value via
// ServicesToModulesCodec (or the reverse) — "map Codec A to Codec B"
// applied at the whole-collection level, not just per-field.
//
// Everything that is a PURE "Compose <-> Docker" mapping concern
// (assembling/reconstructing a docker.CreateOptions from/to a Compose
// Service) already lives in dockercompose itself
// ([dockercompose.Service.CreateOptionsFor]/[dockercompose.ServiceFromCreateOptions]);
// this package stays thin, covering ONLY the mappings that genuinely
// require IoT Edge vocabulary:
//
//   - restartPolicyFor/composeRestartFor — Compose's `restart:`
//     string <-> iothub.RestartPolicy enum (the two vocabularies don't
//     match 1:1 in EITHER direction).
//   - Compose service name <-> iothub.ModuleName slug sanitization (IoT
//     Edge's own naming constraint; see serviceNameToModuleNameCodec).
//   - The "no image, only build:" placeholder-image policy, and its
//     reverse ("this image is one of our own placeholders, reverse it
//     to build:true instead of reproducing the placeholder text").
//   - Status/Type/Version field defaults (forward direction) and
//     [iothub.LayeredDeployment]/[dockercompose.Project] aggregation
//     (both directions).
//
// # Warnings live OUTSIDE the pure codec
//
// codex.MapCodecValidated's `to`/`from` functions are pure `(A) ->
// (B, error)` — no side-channel for "this succeeded, but here's what
// was lossy/approximated." serviceToModuleConfig/moduleConfigToService
// therefore stay pure, deterministic, and warning-free;
// warningsForService/warningsForModuleConfig (service.go) RE-DERIVE the
// exact same facts afterward by comparing the ORIGINAL input to the
// codec's output, reusing the SAME predicates (sanitizeModuleName,
// restartPolicyFor/composeRestartFor, isPlaceholderImage) the pure
// to/from functions call internally — never a re-implementation of the
// conversion logic itself, only cheap fact re-checking.
//
// # Scaffold, not full fidelity — in BOTH directions
//
// Neither direction ever fails for a syntactically valid input; every
// lossy or approximated decision is recorded as a [Warning] instead:
//
//   - Forward (Compose -> IoT Edge): `$edgeHub` routes are ALWAYS EMPTY
//     — routing is never mechanically derivable from Compose's
//     `depends_on`/`networks` (a human must design routing). System
//     modules, runtime settings (Docker version, registry credentials),
//     and store-and-forward configuration are NEVER populated — those
//     belong to the GLOBAL baseline layer (see the sibling
//     [github.com/DaniDeer/go-codex/examples/go-edge-models/models/iotedge]
//     package's own layering strategy), unrelated to importing one
//     project's services.
//   - Reverse (IoT Edge -> Compose): a module's Build CONTEXT (Dockerfile
//     path, build args) was never captured going forward, so it can
//     never be reconstructed going back (a placeholder image correctly
//     reverses to `build: true`/no image, but nothing more specific);
//     `mem_reservation` has no corresponding docker.HostConfig field at
//     all (a PERMANENT, documented one-way loss); a service's ORIGINAL
//     name casing/underscores are unrecoverable once sanitized (the
//     module name IS what comes back as the Compose service name).
//     `$edgeHub` routes have NOTHING to reverse onto (Compose has no
//     routing concept) — simply absent from the reconstructed Project,
//     not an error or Warning.
//
// See the sibling `app/iotedge` package's ImportDockerComposeAsUseCase/
// ExportUseCaseAsDockerCompose for the concrete file-I/O wrappers built
// on top of this package's pure, in-memory conversion functions.
package fromcompose
