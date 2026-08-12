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
//     DeploymentManifest stays PURE wire/file content — no use case
//     identity field; see usecase.go's UseCase for that composition.
//   - configfile.go — ConfigFileFormat and NewConfigFile (a declared,
//     TEMPLATED file port over "{basePath}/usecases/{usecase_name}.json" —
//     usecase_name is a plain, non-merge ports.FilePathParam, validated
//     but never merged into DeploymentManifest).
//   - configdir.go — ConfigDirEntryPattern, NewConfigDir, and
//     ListUseCaseNames (discovers every usecase_name under
//     "{basePath}/usecases").
//   - devicefile.go — DeviceManifest (the device-level analogue of
//     DeploymentManifest — pure wire/file content, no identity fields),
//     DeviceManifestCodec, and NewDeviceFile (a declared, templated file
//     port over "{basePath}/devices/{usecase_name}/{device_id}.json").
//   - devicedir.go — DeviceDirEntryPattern, NewDeviceDir, and
//     ListDeviceIDs (discovers every device_id for ONE given
//     usecase_name — plain named-var substitution, no glob/wildcard).
//   - deviceconfig.go — DeviceConfig (pairs a device_id with its PURE
//     DeviceManifest — the domain-level composition, one level down
//     from UseCase) and ReadDeviceConfig/WriteDeviceConfig.
//   - usecase.go — UseCase (pairs a usecase_name with its PURE
//     DeploymentManifest AND every DeviceConfig nested under it) and
//     ReadUseCase/WriteUseCase — the "one struct, one call" convenience
//     for the FULL usecase+devices tree (combines NewConfigFile,
//     ListDeviceIDs, and ReadDeviceConfig/WriteDeviceConfig internally).
//   - modulesummary.go — ModuleSummary, ModuleSummaryCodec, and
//     NewModuleSummary (a reduced, read-only module view).
//   - readmodulesummary.go — ReadModuleSummaryReq/Codec (BasePath +
//     UseCaseName + ModuleName) and the declared, unregistered
//     ReadModuleSummaryTool MCP contract.
//   - updatemoduleimage.go — UpdateModuleImageReq/Codec (BasePath +
//     UseCaseName + ModuleName + ImageURL) and the declared,
//     unregistered UpdateModuleImageTool MCP contract.
//
// Each field's codec is its own named value (e.g. ImageCodec,
// ModuleNameCodec) so a caller assembling a NEW wire codec — for example a
// "patch this module's image, keyed by module name" codec — can reuse the
// exact same field-level codec rather than re-deriving it.
package iotedge
