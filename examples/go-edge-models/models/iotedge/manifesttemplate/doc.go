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
//   - modules.go — ModuleName, Modules, ModulesContent, DeploymentManifest,
//     ModuleKeyPrefix/moduleKeyConstraint, and their codecs.
//     DeploymentManifest stays PURE wire/file content — no use case
//     identity field; see models/iotedge/usecase's usecase.go (UseCase)
//     for that composition.
//
// Each field's codec is its own named value (e.g. ImageCodec,
// ModuleNameCodec) so a caller assembling a NEW wire codec — for
// example a "patch this module's image, keyed by module name" codec
// (see the sibling models/iotedge/modulepatch package, which depends
// ONLY on this package, never on models/iotedge) — can reuse the exact
// same field-level codec rather than re-deriving it.
package manifesttemplate
