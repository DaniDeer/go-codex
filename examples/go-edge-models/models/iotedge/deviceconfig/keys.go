package deviceconfig

import (
	c "github.com/DaniDeer/go-codex/codex"
	iothub "github.com/DaniDeer/go-codex/examples/go-edge-models/models/azure/iothub"
	v "github.com/DaniDeer/go-codex/validate"
)

// This file is the SINGLE SOURCE OF TRUTH for this package's OWN dotted-
// key vocabulary — [EdgeAgentPatchTemplate] (the MQTT-style template
// pattern Patch.EdgeAgent's wire bucket must match) and
// [edgeAgentPatchCodec] (the codec built from it). Mirrors
// azure/iothub/keys.go's role one package over, but scoped strictly
// to what is UNIQUE to this package's own wire shape — the shared
// top-level wire-key names ([iothub.ModulesContentKey]/
// [iothub.EdgeAgentKey]/[iothub.EdgeHubKey]) and the
// dotted-key PREFIX ([iothub.ModuleKeyPrefix]) both stay
// imported from the generic azure/iothub package UNCHANGED, not
// re-declared or re-exported here — azure/iothub/keys.go remains the
// single source of truth for THAT shared vocabulary (see
// deviceconfig/doc.go).

// EdgeAgentPatchTemplate is the MQTT-style dotted-key template every
// Patch.EdgeAgent wire key must match: a [iothub.ModuleKeyPrefix]-
// prefixed module name, followed by a trailing "#" (matching zero or
// more further segments — a bare module key, one field, or a full
// dotted path reaching arbitrarily deep into that module's own JSON
// shape). Exported so callers building their own [codex.DottedKeyError]
// (e.g. tests) can reference the exact same template instead of
// duplicating the literal.
const EdgeAgentPatchTemplate = iothub.ModuleKeyPrefix + "{moduleName}.#"

// edgeAgentPatchCodec validates/wraps Patch.EdgeAgent's own wire bucket
// using an MQTT-style DOTTED-KEY TEMPLATE (the same {varName}/+/#
// vocabulary MQTT topic templates already use, "." as the level
// delimiter instead of "/" — see codex.DottedPatchMapCodec): each wire
// key must start with iothub.ModuleKeyPrefix, the immediately
// following segment ("{moduleName}") must be a valid slug — the SAME
// constraint iothub.ModuleNameCodec applies to a whole module
// key — and the trailing "#" explicitly declares what was previously an
// IMPLICIT "everything after the module name is opaque, arbitrary
// depth" rule (matching a bare module key with zero remaining segments,
// one field like ".status", or a full dotted path like
// ".env.API_URL" — see deviceconfig.go's Patch doc comment for the
// complete list of what's reachable).
//
// Generalizes what used to be a hand-rolled Constraint
// (edgeAgentKeyConstraint) + manual per-key encode/decode loop — see
// docs/guides/wire-vocabulary.md's dotted-key decision guide for the
// full design rationale (PrefixedKeyCodec vs. DottedKeyCodec vs.
// DottedPatchMapCodec).
var edgeAgentPatchCodec = c.DottedPatchMapCodec(
	EdgeAgentPatchTemplate,
	c.KeyVarConstraint{Name: "moduleName", Constraint: v.Slug},
)

// SystemModulePatchTemplate mirrors EdgeAgentPatchTemplate exactly, one
// bucket over — every Patch.SystemModules wire key must match a
// [iothub.SystemModuleKeyPrefix]-prefixed system module name
// ("edgeAgent"/"edgeHub" only, not an open slug namespace), followed by
// a trailing "#". A GENUINELY SEPARATE wire bucket from
// EdgeAgentPatchTemplate — system modules never live under
// iothub.ModuleKeyPrefix on the real wire.
const SystemModulePatchTemplate = iothub.SystemModuleKeyPrefix + "{systemModuleName}.#"

// systemModulePatchCodec validates/wraps Patch.SystemModules' own wire
// bucket — mirrors edgeAgentPatchCodec exactly, above, constrained to
// the two real system module names instead of an open slug namespace.
var systemModulePatchCodec = c.DottedPatchMapCodec(
	SystemModulePatchTemplate,
	c.KeyVarConstraint{Name: "systemModuleName", Constraint: v.OneOf("edgeAgent", "edgeHub")},
)
