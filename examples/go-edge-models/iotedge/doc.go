// Package iotedge models an Azure IoT-Edge deployment manifest: modules
// keyed by a dotted desired-properties path, each module's settings (image
// + Docker create-options), environment variables, and lifecycle metadata
// (type/status/restartPolicy/version).
//
// This package imports the sibling `docker` package for its
// ModuleSettings.CreateOptions field — the dependency is one-directional
// (docker never imports iotedge), so `docker` stays independently reusable
// for non-IoT-Edge use cases.
//
// Every exported value is a small, independently reusable piece. Files are
// organized ONE PER DOMAIN CONCEPT — each file holds that concept's
// struct(s), any validate.Constraint values it needs, and its codex.Codec[T]
// values together:
//
//   - lifecycle.go — Type, Status, RestartPolicy, Version, StartupOrder and
//     their codecs (TypeCodec, StatusCodec, RestartPolicyCodec,
//     VersionCodec, StartupOrderCodec).
//   - moduleconfig.go — ModuleConfig and ModuleConfigCodec.
//   - modulesettings.go — ModuleSettings, the ImageCodec re-export, and
//     CreateOptionsFieldCodec/ModuleSettingsCodec.
//   - envvars.go — EnvVarName, EnvVars, EnvVarValue, EnvVar and every
//     env-var codec, plus FlattenEnvVars (the one-direction iotedge ->
//     docker.Env mapper).
//   - modules.go — ModuleName, Modules, ModulesContent, DeploymentManifest,
//     ModuleKeyPrefix/moduleKeyConstraint, and their codecs.
//
// Each field's codec is its own named value (e.g. ImageCodec,
// ModuleNameCodec) so a caller assembling a NEW wire codec — for example a
// "patch this module's image, keyed by module name" codec — can reuse the
// exact same field-level codec rather than re-deriving it.
package iotedge
