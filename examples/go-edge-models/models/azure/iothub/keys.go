package iothub

import (
	c "github.com/DaniDeer/go-codex/codex"
	v "github.com/DaniDeer/go-codex/validate"
)

// This file is the SINGLE SOURCE OF TRUTH for the Azure IoT Hub device-
// twin's WIRE-KEY vocabulary — BOTH document shapes this package models
// (see basedeployment.go/layereddeployment.go): the top-level wrapper
// key names ([ModulesContentKey]/[EdgeAgentKey]/[EdgeHubKey]), the
// SINGLE literal flat key every full [BaseDeployment]'s "$edgeAgent"/
// "$edgeHub" value wraps its entire document under
// ([PropertiesDesiredKey]), the dotted-key PREFIXES a [LayeredDeployment]'s
// module/route/system-module entries live under ([ModuleKeyPrefix]/
// [RouteKeyPrefix]/[SystemModuleKeyPrefix]), the codex.DottedKeyCodec-
// built codecs that validate a FULL dotted key and extract/wrap its
// name segment ([ModuleNameCodec]/[RouteNameCodec]/
// [SystemModuleNameCodec]), and the BARE (non-prefixed) codecs a
// [BaseDeployment]'s nested "modules"/"routes" objects use instead
// ([BaseModulesCodec]/[BaseRoutesCodec]). Every OTHER package that
// hand-rolls a codec touching these same wire buckets —
// models/iotedge/modulepatch, models/iotedge/deviceconfig,
// models/iotedge/finaldeviceconfig — reuses these exact constants/
// codecs instead of re-hardcoding the same literal strings, so a caller
// answering "what does this wire key actually look like" or "how do I
// validate one" reads ONE file rather than grepping across packages.
//
// The real, on-the-wire shape a LayeredDeployment's constants describe:
//
//	{
//	  "modulesContent": {
//	    "$edgeAgent": {"properties.desired.modules.<name>": {...ModuleConfig...}, ...},
//	    "$edgeHub":   {"properties.desired.routes.<name>":  "FROM ... INTO ...", ...}
//	  }
//	}
//
// ...and the real, on-the-wire shape a BaseDeployment's constants
// describe — see basedeployment.go's own doc comment for the complete
// nested example.
//
// NOT included here: ordinary codex.Struct/PartialField FIELD NAMES
// ("settings"/"env"/"type"/"status"/"restartPolicy"/"version"/"image"/
// "createOptions"/"modules"/"runtime"/"schemaVersion"/"systemModules"/
// "routes"/"storeAndForwardConfiguration"/"minDockerVersion"/
// "registryCredentials"/"address"/"timeToLiveSecs") — these follow the
// STANDARD codex.Struct/PartialField idiom used identically throughout
// the whole go-codex ecosystem (every full/partial type pair repeats
// field names this way); consolidating those into constants would be a
// much larger, unrelated refactor touching a pattern that has never
// needed it elsewhere. This file scopes strictly to the DOTTED-KEY/
// WRAPPER EXTRACTION vocabulary — the part that had actually drifted
// into ad-hoc, un-sourced duplication.

// ── Top-level wire-key names ──────────────────────────────────────────────────

// ModulesContentKey is the deployment manifest's top-level wrapper key,
// e.g. {"modulesContent": {...}}.
const ModulesContentKey = "modulesContent"

// EdgeAgentKey is the flat object holding every module entry, keyed by
// ModuleKeyPrefix-prefixed dotted keys, e.g. {"$edgeAgent": {...}}.
const EdgeAgentKey = "$edgeAgent"

// EdgeHubKey is the flat object holding every route entry, keyed by
// RouteKeyPrefix-prefixed dotted keys, e.g. {"$edgeHub": {...}}.
const EdgeHubKey = "$edgeHub"

// PropertiesDesiredKey is the SINGLE, literal flat key (containing a
// dot) every [BaseDeployment]'s "$edgeAgent"/"$edgeHub" value wraps its
// ENTIRE desired-properties document under on the FULL/nested Azure IoT
// Edge manifest wire shape — e.g. {"$edgeAgent": {"properties.desired": {...}}}.
// This is NOT split into nested {"properties": {"desired": {...}}} —
// Azure's own convention keeps it as one flat string key, mirroring how
// a device twin's desired-properties root is addressed via the Device
// Twin API. Distinct from a [LayeredDeployment]'s dotted-key PATCH
// convention (many "properties.desired.modules.<name>"-shaped flat
// keys, one per module) — a BaseDeployment uses PropertiesDesiredKey
// exactly ONCE per "$edgeAgent"/"$edgeHub" value, wrapping a
// normally-nested object.
const PropertiesDesiredKey = "properties.desired"

// ── ModuleName dotted-key extraction (LayeredDeployment) ──────────────────────
//
// Wire: {"properties.desired.modules.factory-mqtt-gateway": {...ModuleConfig...}, ...}
// Go:   map[ModuleName]ModuleConfig{"factory-mqtt-gateway": {...}, ...}
//
// Built on codex.DottedKeyCodec — the SAME {varName}-capture machinery
// (DecodeVars/EncodeVars/FieldCodec) [PathParam]/[TopicParam]/
// [FilePathParam] already use across the codebase, generalized to "." as
// the level delimiter. A single named-var template with one FieldCodec
// is exactly PrefixedKeyCodec's own "prefix + validated-name-segment"
// recipe expressed through the more general mechanism — composes with
// codex.Map (→ map[ModuleName]ModuleConfig), same as examples/
// flat-key-patch's containerKeyCodec composes with codex.EntrySlice
// (→ a merged []Container slice) for the analogous string-key case. Use
// Map when you want the result as a Go map keyed by the extracted
// value; use EntrySlice when you want the extracted key merged into a
// flat slice of combined structs.

// ModuleKeyPrefix is the fixed namespace for all module keys, e.g.
// "properties.desired.modules.factory-mqtt-gateway". Exported so a caller
// composing their own dotted-key codec (e.g. a patch targeting one module
// by name) can reuse the exact same prefix instead of duplicating it.
const ModuleKeyPrefix = "properties.desired.modules."

// moduleNameField declares ModuleName's single captured segment — the
// SAME v.Slug constraint PrefixedKeyCodec used to apply directly, now
// expressed as a codex.RequiredField the DottedKeyCodec template below
// binds to the "{name}" placeholder.
var moduleNameField = c.RequiredField("name", c.String().Refine(v.Slug),
	func(n ModuleName) string { return string(n) },
	func(n *ModuleName, s string) { *n = ModuleName(s) },
)

// ModuleNameCodec: codex.DottedKeyCodec over ModuleKeyPrefix+"{name}" —
// same wire shape, same v.Slug validation, same errors PrefixedKeyCodec
// produced, just built via the shared MQTT-style dotted-key template
// mechanism instead of the narrower prefix-only constructor.
//
// Decode: full key → match template → validate "name" segment (via Slug) → ModuleName.
// Encode: ModuleName → validate "name" segment (via Slug) → substitute into template → full key string.
var ModuleNameCodec = c.DottedKeyCodec[ModuleName](ModuleKeyPrefix+"{name}", moduleNameField)

// ── RouteName dotted-key extraction (LayeredDeployment) ───────────────────────
//
// Wire: {"properties.desired.routes.factory-mqtt-to-ingest": "FROM ... INTO ...", ...}
// Go:   map[RouteName]Route{"factory-mqtt-to-ingest": {...}, ...}
//
// Mirrors ModuleName's own dotted-key extraction exactly, above — same
// codex.DottedKeyCodec single-named-var template, just under a
// different prefix and value shape.

// RouteKeyPrefix is the fixed namespace for all route keys, e.g.
// "properties.desired.routes.factory-mqtt-to-ingest". Exported so a
// caller composing their own dotted-key codec (e.g. a device-config
// patch targeting one route by name) can reuse the exact same prefix
// instead of duplicating it.
const RouteKeyPrefix = "properties.desired.routes."

// routeNameField declares RouteName's single captured segment — mirrors
// moduleNameField exactly, above.
var routeNameField = c.RequiredField("name", c.String().Refine(v.Slug),
	func(n RouteName) string { return string(n) },
	func(n *RouteName, s string) { *n = RouteName(s) },
)

// RouteNameCodec: codex.DottedKeyCodec over RouteKeyPrefix+"{name}" —
// same convenience ModuleNameCodec uses above, just under a different
// prefix. Mirrors ModuleNameCodec exactly.
var RouteNameCodec = c.DottedKeyCodec[RouteName](RouteKeyPrefix+"{name}", routeNameField)

// ── SystemModuleName dotted-key extraction (LayeredDeployment) ───────────────

// Wire: {"properties.desired.systemModules.edgeAgent.settings.image": "...", ...}
// Go:   map[SystemModuleName]SystemModuleConfig{"edgeAgent": {...}, "edgeHub": {...}}
//
// A GENUINELY SEPARATE dotted-key namespace from ModuleKeyPrefix —
// "edgeAgent"/"edgeHub" are NOT valid v.Slug values (camelCase, no
// hyphens) and never belong under the regular "modules" bucket on the
// real wire; conflating the two would produce an invalid manifest.
// SystemModuleKeyPrefix/SystemModuleName/SystemModuleNameCodec are this
// package's system-module-scoped mirror of ModuleKeyPrefix/ModuleName/
// ModuleNameCodec, one bucket over — reused by the
// models/iotedge/deviceconfig package's own system-module patch bucket.

// SystemModuleKeyPrefix is the fixed namespace for system-module dotted
// keys, e.g. "properties.desired.systemModules.edgeAgent". Exported so
// deviceconfig's own system-module patch codec reuses the exact same
// prefix instead of duplicating it.
const SystemModuleKeyPrefix = "properties.desired.systemModules."

// SystemModuleName identifies WHICH system module a dotted key/patch
// entry targets — exactly "edgeAgent" or "edgeHub" (IoT Edge's two
// system modules; no others exist).
type SystemModuleName string

// systemModuleNameField declares SystemModuleName's single captured
// segment — restricted to the two real system module names via
// v.OneOf, unlike moduleNameField's v.Slug (system module names are a
// closed, two-value set, not an open slug namespace).
var systemModuleNameField = c.RequiredField("name", c.String().Refine(v.OneOf("edgeAgent", "edgeHub")),
	func(n SystemModuleName) string { return string(n) },
	func(n *SystemModuleName, s string) { *n = SystemModuleName(s) },
)

// SystemModuleNameCodec: codex.DottedKeyCodec over
// SystemModuleKeyPrefix+"{name}" — mirrors ModuleNameCodec/RouteNameCodec
// exactly, restricted to the two real system module names.
var SystemModuleNameCodec = c.DottedKeyCodec[SystemModuleName](SystemModuleKeyPrefix+"{name}", systemModuleNameField)

// ── Bare (non-prefixed) module/route key codecs (BaseDeployment) ─────────────
//
// Wire: {"modules": {"vulnerability-scanner": {...ModuleConfig...}, ...}}
// Go:   map[ModuleName]ModuleConfig{"vulnerability-scanner": {...}, ...}
//
// Reuses ModuleName/ModuleConfig/ModuleConfigCodec AS-IS (identical
// value shape) — but a BaseDeployment's "modules" bucket nests modules
// under a real, ordinary "modules" JSON key, so each module name is a
// BARE map key (no "properties.desired.modules." prefix to strip/add),
// unlike a LayeredDeployment's ModuleNameCodec (built for the FLAT,
// prefixed patch form). bareModuleNameCodec is the SAME v.Slug
// constraint, just without the dotted-key template machinery.

var bareModuleNameCodec = c.MapCodecSafe(
	c.String().Refine(v.Slug),
	func(s string) ModuleName { return ModuleName(s) },
	func(n ModuleName) (string, error) { return string(n), nil },
)

// BaseModulesCodec decodes/encodes a BaseDeployment's nested "modules"
// object directly into map[ModuleName]ModuleConfig via codex.Map —
// bare-keyed (see bareModuleNameCodec above), unlike a LayeredDeployment's
// own ModulesCodec (flat-dotted-keyed).
var BaseModulesCodec = c.Map[ModuleName, ModuleConfig](
	bareModuleNameCodec, ModuleConfigCodec,
)

// bareRouteNameCodec mirrors bareModuleNameCodec exactly, above, for
// $edgeHub's nested "routes" object.
var bareRouteNameCodec = c.MapCodecSafe(
	c.String().Refine(v.Slug),
	func(s string) RouteName { return RouteName(s) },
	func(n RouteName) (string, error) { return string(n), nil },
)

// BaseRoutesCodec decodes/encodes a BaseDeployment's nested "routes"
// object directly into map[RouteName]Route via codex.Map — bare-keyed,
// mirroring BaseModulesCodec above.
var BaseRoutesCodec = c.Map[RouteName, Route](
	bareRouteNameCodec, RouteCodec,
)
