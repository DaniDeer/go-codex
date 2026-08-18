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
- `ModulesContentKey`/`EdgeAgentKey`/`EdgeHubKey`/`ModuleKeyPrefix`/`RouteKeyPrefix`/`ModuleNameCodec`/`RouteNameCodec` (`keys.go`) — the SINGLE SOURCE OF TRUTH for the manifest's wire-key vocabulary (top-level wrapper keys, dotted-key namespaces, name-segment codecs). `modulepatch`/`deviceconfig`/`finaldeviceconfig` all import these instead of re-hardcoding the same literal strings.
- `ModuleName`/`Modules`/`ModulesContent`/`DeploymentManifest` (`modules.go`) — the full manifest shape, built from `keys.go`.
- `RouteName`/`Routes`/`RouteTarget`/`Route` (`edgehub.go`) — `$edgeHub` route definitions (`"FROM <path> INTO BrokeredEndpoint(\"<topic>\")"` or `"FROM <path> INTO $upstream"`), built from `keys.go`. `ModulesContent.EdgeHub` is optional.

See the sibling [`usecase`](../usecase), [`modulesummary`](../modulesummary),
[`updatemoduleimage`](../updatemoduleimage), and [`deviceconfig`](../deviceconfig)
packages for everything built on top of these types.
