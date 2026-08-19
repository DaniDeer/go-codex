package manifesttemplate

import (
	c "github.com/DaniDeer/go-codex/codex"
	v "github.com/DaniDeer/go-codex/validate"
)

// This file is the SINGLE SOURCE OF TRUTH for the deployment manifest's
// WIRE-KEY vocabulary: the top-level wrapper key names
// ([ModulesContentKey]/[EdgeAgentKey]/[EdgeHubKey]), the dotted-key
// PREFIXES module/route entries live under ([ModuleKeyPrefix]/
// [RouteKeyPrefix]), and the codex.DottedKeyCodec-built codecs that
// validate a FULL dotted key and extract/wrap its name segment
// ([ModuleNameCodec]/[RouteNameCodec]). Every OTHER package that
// hand-rolls a codec
// touching these same wire buckets — models/iotedge/modulepatch,
// models/iotedge/deviceconfig, models/iotedge/finaldeviceconfig — reuses
// these exact constants/codecs instead of re-hardcoding the same
// literal strings, so a caller answering "what does this wire key
// actually look like" or "how do I validate one" reads ONE file rather
// than grepping four packages.
//
// The real, on-the-wire shape this file's constants describe:
//
//	{
//	  "modulesContent": {
//	    "$edgeAgent": {"properties.desired.modules.<name>": {...ModuleConfig...}, ...},
//	    "$edgeHub":   {"properties.desired.routes.<name>":  "FROM ... INTO ...", ...}
//	  }
//	}
//
// NOT included here: ordinary codex.Struct/PartialField FIELD NAMES
// ("settings"/"env"/"type"/"status"/"restartPolicy"/"version"/"image"/
// "createOptions") — these follow the STANDARD codex.Struct/PartialField
// idiom used identically throughout the whole go-codex ecosystem (every
// full/partial type pair repeats field names this way); consolidating
// those into constants would be a much larger, unrelated refactor
// touching a pattern that has never needed it elsewhere. This file
// scopes strictly to the DOTTED-KEY EXTRACTION vocabulary — the part
// that had actually drifted into ad-hoc, un-sourced duplication.

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

// ── ModuleName dotted-key extraction ──────────────────────────────────────────
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

// ── RouteName dotted-key extraction ────────────────────────────────────────────
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
