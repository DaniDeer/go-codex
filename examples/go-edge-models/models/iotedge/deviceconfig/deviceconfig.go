package deviceconfig

import (
	"fmt"
	"log/slog"

	c "github.com/DaniDeer/go-codex/codex"
	manifesttemplate "github.com/DaniDeer/go-codex/examples/go-edge-models/models/iotedge/manifesttemplate"
	"github.com/DaniDeer/go-codex/schema"
)

// ── Patch ──────────────────────────────────────────────────────────────────────
//
// This file holds Patch — the PURE wire/file content for ONE device's
// device-specific config file — and its inverse operation, Merge, which
// layers a Patch onto a use case's own manifesttemplate.DeploymentManifest
// to produce the FINAL, deployable config for that one device.
//
// Patch is a GENERAL, arbitrary-DEPTH patch — unlike
// modulepatch.FieldsPatch (which patches a FIXED, known set of one
// module's top-level fields), a real device config can reach to ANY
// depth inside a module's own JSON shape (e.g. "factory-opcua-gateway.env.API_URL" to
// override one env var, or "factory-opcua-gateway.settings.createOptions" to override
// one settings field, or bare "factory-opcua-gateway" to override/add the WHOLE
// module) — the real-world shape genuinely cannot be expressed as a
// fixed Go struct, so leaf values are validated-opaque (any), matching
// codex.Any()'s "opaque config blob" rationale. $edgeHub route entries
// ARE fully typed (manifesttemplate.Route), since a route's wire value
// is always exactly one atomic string — no deeper nesting is possible
// there.
//
// Merge is OVERWRITE/ADD ONLY — there is no RFC 7396-style null-means-
// delete: a device config can only set or replace a field, never remove
// one the use case template already declared. More precisely, Merge
// deep-merges map values key-by-key AT EVERY LEVEL (not just the
// top): patching "factory-opcua-gateway.env" with a map still MERGES
// that map's keys against the base module's existing env keys rather
// than replacing the whole env map wholesale — the only way to make a
// key disappear is to never have declared it in the use case template
// in the first place.
//
// Every ModuleConfig field is reachable this way — not just env/
// settings.image/status (shown above), but also "type", "restartPolicy",
// and "version" individually, e.g. "factory-opcua-gateway.restartPolicy":
// "always". A device patch MAY ALSO introduce an entirely NEW module:
// use a BARE module-name key (no further dotted segments, e.g.
// "factory-edge-agent-extra") whose value is a whole ModuleConfig-shaped
// map — the module doesn't need to exist in the use case template
// first. The ERGONOMIC way to build that value (rather than a
// hand-rolled map[string]any) is to build a fully-populated
// modulepatch.FieldsPatch (every field set: Settings/Env/Type/Status/
// RestartPolicy/Version) and encode it via modulepatch.FieldsBodyCodec
// — reusing modulepatch's own typed field validation (Image,
// CreateOptions, ...) with zero duplicated logic; see
// app/iotedge.PatchDeviceModule for a worked example of exactly this.
//
// One important LIMITATION: "settings.createOptions" is patchable only
// as ONE ATOMIC, already-JSON-escaped STRING (matching its own wire
// shape in manifesttemplate — see manifesttemplate.CreateOptionsFieldCodec)
// — it is NOT possible to reach further inside it, e.g.
// "factory-opcua-gateway.settings.createOptions.HostConfig.Binds" does
// NOT work. Attempting to do so builds a nested MAP at the
// "createOptions" key, but the base's existing value there is a STRING;
// deepMerge's "both sides must be maps to merge" check fails, so the
// patch's map wholesale-REPLACES the string, and
// manifesttemplate.DeploymentManifestCodec.Decode then fails with a
// generic codex.TypeMismatchError{Expected: "string", Got:
// "map[string]interface {}"} at Merge time — not a purpose-built error,
// but a clear enough signal. This is an intentional, accepted
// limitation: if a specific createOptions shape needs finer-grained
// per-field patching, encode a NEW whole createOptions string with the
// desired change already applied, rather than trying to patch inside
// the existing one.

// Patch is a partial, arbitrary-depth override of a use case's
// manifesttemplate.DeploymentManifest.
type Patch struct {
	// EdgeAgent maps a bare dotted PATH (relative to
	// manifesttemplate.ModuleKeyPrefix, e.g. "factory-opcua-gateway" for a whole-module
	// override, or "factory-opcua-gateway.env.API_URL" to reach one env var) to the raw
	// JSON value to set/overwrite at that path. The FIRST path segment
	// is always a module name (validated as a slug — see
	// manifesttemplate.ModuleNameCodec); any further segments reach
	// deeper into that module's own JSON shape and are NOT validated
	// here (their meaning depends on where they land after Merge).
	EdgeAgent map[string]any
	// EdgeHub maps a route name to its FULL replacement/addition Route
	// — routes are atomic (a route's wire value is one string), so
	// there is no deeper nesting to express here, unlike EdgeAgent.
	EdgeHub map[manifesttemplate.RouteName]manifesttemplate.Route
}

// EmptyPatchError reports that a Patch had NOTHING set (both EdgeAgent
// and EdgeHub empty) — encoding it would produce a no-op device config,
// which is almost certainly a caller mistake, so PatchCodec.Encode
// returns this instead. Implements slog.LogValuer for structured
// logging.
type EmptyPatchError struct{}

func (e EmptyPatchError) Error() string {
	return "deviceconfig: empty Patch: nothing to override or add"
}

// LogValue implements slog.LogValuer for structured logging.
func (e EmptyPatchError) LogValue() slog.Value {
	return slog.GroupValue()
}

// edgeAgentPatchCodec is declared in keys.go, this package's single
// source of truth for its own dotted-key vocabulary.

// PatchCodec is Patch's canonical codec — HAND-ROLLED (like
// modulepatch.FieldsPatchCodec) because Patch's keys are DYNAMIC
// (arbitrary-depth dotted paths for EdgeAgent, route names for
// EdgeHub), not a fixed field list codex.Struct could express.
//
// Wire shape: {"modulesContent": {"$edgeAgent"?: {...}, "$edgeHub"?: {...}}}
// — each top-level bucket is OMITTED ENTIRELY when its map is empty,
// mirroring modulepatch.FieldsPatchCodec's own sparse-inclusion rule.
var PatchCodec = c.Codec[Patch]{
	Encode: func(p Patch) (any, error) {
		modulesContent := map[string]any{}

		if len(p.EdgeAgent) > 0 {
			edgeAgent, err := edgeAgentPatchCodec.Encode(p.EdgeAgent)
			if err != nil {
				return nil, err
			}
			modulesContent[manifesttemplate.EdgeAgentKey] = edgeAgent
		}

		if len(p.EdgeHub) > 0 {
			edgeHub := map[string]any{}
			for name, route := range p.EdgeHub {
				key, err := manifesttemplate.RouteNameCodec.Encode(name)
				if err != nil {
					return nil, err
				}
				raw, err := manifesttemplate.RouteCodec.Encode(route)
				if err != nil {
					return nil, err
				}
				edgeHub[key.(string)] = raw
			}
			modulesContent[manifesttemplate.EdgeHubKey] = edgeHub
		}

		if len(modulesContent) == 0 {
			return nil, EmptyPatchError{}
		}
		return map[string]any{manifesttemplate.ModulesContentKey: modulesContent}, nil
	},
	Decode: func(raw any) (Patch, error) {
		obj, err := asObject(raw, manifesttemplate.ModulesContentKey)
		if err != nil {
			return Patch{}, err
		}
		modulesContentRaw, ok := obj[manifesttemplate.ModulesContentKey]
		if !ok {
			return Patch{}, nil
		}
		modulesContent, err := asObject(modulesContentRaw, manifesttemplate.EdgeAgentKey)
		if err != nil {
			return Patch{}, err
		}

		p := Patch{}
		if edgeAgentRaw, ok := modulesContent[manifesttemplate.EdgeAgentKey]; ok {
			edgeAgent, err := edgeAgentPatchCodec.Decode(edgeAgentRaw)
			if err != nil {
				return Patch{}, err
			}
			p.EdgeAgent = edgeAgent
		}
		if edgeHubRaw, ok := modulesContent[manifesttemplate.EdgeHubKey]; ok {
			edgeHub, err := asObject(edgeHubRaw, "")
			if err != nil {
				return Patch{}, err
			}
			p.EdgeHub = make(map[manifesttemplate.RouteName]manifesttemplate.Route, len(edgeHub))
			for key, val := range edgeHub {
				name, err := manifesttemplate.RouteNameCodec.Decode(key)
				if err != nil {
					return Patch{}, err
				}
				route, err := manifesttemplate.RouteCodec.Decode(val)
				if err != nil {
					return Patch{}, err
				}
				p.EdgeHub[name] = route
			}
		}
		return p, nil
	},
	Schema: schema.Schema{
		Type: "object",
		Properties: []schema.Property{
			{Name: manifesttemplate.ModulesContentKey, Schema: schema.Schema{
				Type: "object",
				Properties: []schema.Property{
					{Name: manifesttemplate.EdgeAgentKey, Schema: schema.Schema{Type: "object", AdditionalProperties: boolPtr(true)}},
					{Name: manifesttemplate.EdgeHubKey, Schema: schema.Schema{Type: "object", AdditionalProperties: boolPtr(true)}},
				},
			}},
		},
	},
}

func boolPtr(b bool) *bool { return &b }

// asObject type-asserts raw as map[string]any, returning a
// TypeMismatchError mentioning the FIRST key the caller is about to look
// up (wantKeyHint) if the assertion fails — purely a more useful error
// message, not a functional check on that key's presence. Mirrors
// modulepatch's own asObject helper.
func asObject(raw any, wantKeyHint string) (map[string]any, error) {
	obj, ok := raw.(map[string]any)
	if !ok {
		hint := "object"
		if wantKeyHint != "" {
			hint = "object (containing " + wantKeyHint + ")"
		}
		return nil, c.TypeMismatchError{Expected: hint, Got: typeName(raw)}
	}
	return obj, nil
}

func typeName(v any) string {
	if v == nil {
		return "nil"
	}
	return fmt.Sprintf("%T", v)
}
