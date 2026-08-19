// Package iothub models the GENERIC, documented Azure IoT Hub
// edgeAgent/edgeHub device-twin specification —
// https://learn.microsoft.com/en-us/azure/iot-edge/module-edgeagent-edgehub
// — and NOTHING else. Every exported value here is a plain struct +
// [codex.Codec] pair describing the real wire structure: no file I/O,
// no ports.File/ports.Dir, no derived/reduced views, no domain
// composition, and ZERO knowledge of any particular application's own
// device-configuration LAYERING strategy (see the
// models/iotedge package for THAT — this package only models what
// Azure itself defines).
//
// Azure IoT Hub itself distinguishes TWO deployment document shapes,
// both modeled here:
//
//   - [BaseDeployment] — the FULL, nested base deployment (schema
//     version, IoT Edge runtime settings, the two SYSTEM modules —
//     edgeAgent/edgeHub themselves — every common container, edgeHub's
//     routes, and store-and-forward configuration) — applied at
//     priority 0 to every device matching a target condition.
//   - [LayeredDeployment] — the flat-dotted-key LAYERED deployment PATCH
//     form ("properties.desired.modules.<name>" as ONE literal JSON key
//     per module) — an overlay merged on top of a BaseDeployment (or
//     another LayeredDeployment) via IoT Hub's own priority/target-
//     condition system.
//
// This package has NO opinion on how many layers an application
// actually uses, in what order, or how they're located on disk — that
// is entirely the CONSUMING application's own design. See the
// [github.com/DaniDeer/go-codex/examples/go-edge-models/models/iotedge]
// package's own README.md for this repo's example application's
// specific choice (a GLOBAL baseline + per-use-case templates +
// per-device patches, three-way merged).
//
// Every exported value is a small, independently reusable piece. Files
// are organized ONE PER DOMAIN CONCEPT — each file holds that concept's
// struct(s), any validate.Constraint values it needs, and its
// codex.Codec[T] values together:
//
//   - lifecycle.go — Type, Status, RestartPolicy, Version, StartupOrder
//     and their codecs (TypeCodec, StatusCodec, RestartPolicyCodec,
//     VersionCodec, StartupOrderCodec).
//   - moduleconfig.go — ModuleConfig and ModuleConfigCodec — one
//     regular module's full desired-state configuration.
//   - modulesettings.go — ModuleSettings, the ImageCodec re-export, and
//     CreateOptionsFieldCodec/ModuleSettingsCodec.
//   - envvars.go — EnvVarName, EnvVars, EnvVarValue, EnvVar and every
//     env-var codec, plus FlattenEnvVars (the one-direction mapper to
//     docker.Env).
//   - systemmodules.go — SystemModuleConfig (a looser-optionality
//     sibling of ModuleConfig — edgeAgent/edgeHub lack "version", and
//     edgeAgent additionally lacks "status"/"restartPolicy") and
//     SystemModules (BaseDeployment's own, always-both-present
//     edgeAgent/edgeHub pair) and their codecs.
//   - runtime.go — RegistryCredential/RegistryCredentials/
//     RuntimeSettings/Runtime and their codecs — $edgeAgent's "runtime"
//     document (minimum Docker version, registry credentials for
//     private image pulls).
//   - storeandforward.go — StoreAndForwardConfiguration and its codec
//     — $edgeHub's message retention window.
//   - schemaversion.go — SchemaVersion and SchemaVersionCodec.
//   - keys.go — the SINGLE SOURCE OF TRUTH for the WHOLE spec's
//     wire-key vocabulary: top-level wrapper key names
//     (ModulesContentKey/EdgeAgentKey/EdgeHubKey), PropertiesDesiredKey
//     (the single flat key a BaseDeployment's "$edgeAgent"/"$edgeHub"
//     value wraps its whole document under), the dotted-key namespaces
//     a LayeredDeployment's entries live under (ModuleKeyPrefix/
//     RouteKeyPrefix/SystemModuleKeyPrefix), their name-segment codecs
//     (ModuleNameCodec/RouteNameCodec/SystemModuleNameCodec), and the
//     BARE (non-prefixed) codecs a BaseDeployment's nested objects use
//     instead (BaseModulesCodec/BaseRoutesCodec). Every package that
//     hand-rolls a codec touching these same wire buckets —
//     models/iotedge/modulepatch, models/iotedge/deviceconfig,
//     models/iotedge/finaldeviceconfig — imports these constants
//     instead of re-hardcoding the same literal strings.
//   - layereddeployment.go — ModuleName, Modules, LayeredModulesContent,
//     LayeredDeployment, and their codecs (built from keys.go's
//     constants/codecs). LayeredDeployment stays PURE wire/file content
//     — no application-specific identity field.
//   - edgehub.go — RouteName, Routes (mirrors ModuleName/Modules's own
//     dotted-key extraction pattern exactly, one namespace over, built
//     from keys.go); RouteTargetKind/RouteTarget/NewBrokeredEndpoint/
//     UpstreamTarget (a route's INTO target: a specific module endpoint,
//     or the literal $upstream); Route and its HAND-ROLLED RouteCodec
//     (the wire value is a single "FROM <path> INTO ..." STRING, not a
//     JSON object, so codex.Struct cannot express it).
//   - basedeployment.go — EdgeAgentProperties/EdgeHubProperties,
//     BaseModulesContent, BaseDeployment, and their codecs, assembling
//     every other file's pieces into the full document.
//
// This package imports the `docker` package for
// ModuleSettings.CreateOptions — the dependency is one-directional
// (docker never imports iothub), so `docker` stays independently
// reusable for non-IoT-Edge use cases.
//
// Each field's codec is its own named value (e.g. ImageCodec,
// ModuleNameCodec, RouteNameCodec) so a caller assembling a NEW wire
// codec — for example a "patch this module's image, keyed by module
// name" codec (see the models/iotedge/modulepatch package, which
// depends ONLY on this package, never on models/iotedge's own
// layering concept) — can reuse the exact same field-level codec rather
// than re-deriving it.
package iothub
