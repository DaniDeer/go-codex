// Package manifesttemplate models the WIRE FORMAT of an Azure IoT-Edge
// deployment manifest — the exact JSON shape found on disk at
// "<basePath>/usecases/<usecase_name>.json" — and NOTHING else. Every
// exported value here is a plain struct + [codex.Codec] pair describing
// the real wire structure: no file I/O, no ports.File/ports.Dir, no
// derived/reduced views, no domain composition. See the sibling
// models/iotedge/usecase, models/iotedge/modulesummary, and
// models/iotedge/updatemoduleimage packages for everything BUILT on top
// of these types (templated file/dir ports, the UseCase domain
// composition, the reduced Summary view, and the MCP tool contracts).
//
// This package imports the `docker` package for
// ModuleSettings.CreateOptions — the dependency is one-directional
// (docker never imports manifesttemplate), so `docker` stays
// independently reusable for non-IoT-Edge use cases.
//
// Every exported value is a small, independently reusable piece. Files
// are organized ONE PER DOMAIN CONCEPT — each file holds that concept's
// struct(s), any validate.Constraint values it needs, and its
// codex.Codec[T] values together:
//
//   - lifecycle.go — Type, Status, RestartPolicy, Version, StartupOrder and
//     their codecs (TypeCodec, StatusCodec, RestartPolicyCodec,
//     VersionCodec, StartupOrderCodec).
//   - moduleconfig.go — ModuleConfig and ModuleConfigCodec.
//   - modulesettings.go — ModuleSettings, the ImageCodec re-export, and
//     CreateOptionsFieldCodec/ModuleSettingsCodec.
//   - envvars.go — EnvVarName, EnvVars, EnvVarValue, EnvVar and every
//     env-var codec, plus FlattenEnvVars (the one-direction
//     manifesttemplate -> docker.Env mapper).
//   - keys.go — the SINGLE SOURCE OF TRUTH for the deployment manifest's
//     WIRE-KEY vocabulary: ModulesContentKey/EdgeAgentKey/EdgeHubKey
//     (the top-level wrapper key names), ModuleKeyPrefix/RouteKeyPrefix
//     (the dotted-key namespaces), their full-key constraints
//     (moduleKeyConstraint/routeKeyConstraint), name-segment codecs
//     (moduleNameCodec/routeNameCodec), and the exported two-layer
//     ModuleNameCodec/RouteNameCodec. Every package that hand-rolls a
//     codec touching these same wire buckets — modulepatch,
//     deviceconfig, finaldeviceconfig — imports these constants instead
//     of re-hardcoding the same literal strings.
//   - modules.go — ModuleName, Modules, ModulesContent,
//     DeploymentManifest, and their codecs (built from keys.go's
//     constants/codecs). DeploymentManifest stays PURE wire/file
//     content — no use case identity field; see models/iotedge/usecase's
//     usecase.go (UseCase) for that composition.
//   - edgehub.go — RouteName, Routes (mirrors ModuleName/Modules's own
//     dotted-key extraction pattern exactly, one namespace over, built
//     from keys.go); RouteTargetKind/RouteTarget/NewBrokeredEndpoint/
//     UpstreamTarget (a route's INTO target: a specific module endpoint,
//     or the literal $upstream); Route and its HAND-ROLLED RouteCodec
//     (the wire value is a single "FROM <path> INTO ..." STRING, not a
//     JSON object, so codex.Struct cannot express it — mirrors why
//     modulepatch.FieldsPatchCodec is hand-rolled too). ModulesContent.
//     EdgeHub (OPTIONAL — most use cases declare no routes at the
//     template level; routes are equally often added/overridden
//     entirely by a device config's patch — see the sibling
//     models/iotedge/deviceconfig package's Patch/Merge).
//
// Each field's codec is its own named value (e.g. ImageCodec,
// ModuleNameCodec, RouteNameCodec) so a caller assembling a NEW wire
// codec — for example a "patch this module's image, keyed by module
// name" codec (see the sibling models/iotedge/modulepatch package, which
// depends ONLY on this package, never on models/iotedge) — can reuse the
// exact same field-level codec rather than re-deriving it.
package manifesttemplate
