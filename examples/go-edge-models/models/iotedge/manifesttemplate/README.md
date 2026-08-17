# manifesttemplate

The pure wire format of an Azure IoT-Edge deployment manifest — the
exact JSON shape found on disk at
`<basePath>/usecases/<usecase_name>.json` — and nothing else. Plain
structs + `codex.Codec` pairs only: no file I/O, no `ports.File`/
`ports.Dir`, no derived/reduced views, no domain composition.

## What's here

- `Type`/`Status`/`RestartPolicy`/`Version`/`StartupOrder` (`lifecycle.go`) — module lifecycle enums and their codecs.
- `ModuleConfig`/`ModuleConfigCodec` (`moduleconfig.go`) — one module's full config.
- `ModuleSettings`/`ModuleSettingsCodec` (`modulesettings.go`) — image + create-options grouping.
- `EnvVarName`/`EnvVars`/`EnvVarValue`/`EnvVar` (`envvars.go`) — the string/int/float env-var union, plus `FlattenEnvVars` (one-direction mapper to `docker.Env`).
- `ModuleName`/`Modules`/`ModulesContent`/`DeploymentManifest` (`modules.go`) — the full manifest shape, dotted-key extraction.

See the sibling [`usecase`](../usecase), [`modulesummary`](../modulesummary),
and [`updatemoduleimage`](../updatemoduleimage) packages for everything
built on top of these types.
