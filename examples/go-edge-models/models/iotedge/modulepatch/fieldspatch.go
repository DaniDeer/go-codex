package modulepatch

import (
	"fmt"
	"log/slog"

	c "github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/examples/go-edge-models/models/docker"
	manifesttemplate "github.com/DaniDeer/go-codex/examples/go-edge-models/models/iotedge/manifesttemplate"
)

// This file holds FieldsPatch — a GENERAL, multi-field patch for
// one module. Every field is independently optional (a pointer, nil =
// untouched — a uniform rule with zero exceptions); only the ones
// actually set are included when encoding, leaving every other field on
// this module — and everything else in the manifest — untouched.
//
// FieldsPatch's own fields (Settings/Env/Type/Status/
// RestartPolicy/Version) are declared via codex.PartialField and
// composed into moduleFieldsBodyCodec via codex.PartialStruct — the
// sparse "omit unset fields entirely" mechanism (see
// docs/concepts/codec.md's "PartialField/PartialStruct" subsection for
// the full design). Settings
// itself groups Image/CreateOptions the SAME way — a nested
// SettingsPatch, also PartialStruct-built — mirroring how
// manifesttemplate.ModuleConfig.Settings groups those same two fields on the
// base (non-patch) type. Nesting needs no special mechanism: presence
// for "settings" is decided exactly like any other field — is
// FieldsPatch.Settings nil?
//
// The ONE part that genuinely can't be expressed as a fixed field list
// (and therefore stays hand-written): the manifest wraps each patch
// under a RUNTIME-DETERMINED module-name key
// (modulesContent -> $edgeAgent -> <dotted key> -> {...}) — that key is
// data, not a static field name, so FieldsPatchCodec's own
// Encode/Decode still hand-assembles that outer wrapping (reusing
// manifesttemplate.ModuleNameCodec for the key itself) around
// moduleFieldsBodyCodec's declarative result.

// SettingsPatch groups the two fields the wire format nests under
// "settings" — mirrors manifesttemplate.ModuleSettings, but every field is
// independently optional.
//
// Gotcha: presence is decided by "is this pointer non-nil", not "does it
// have anything set inside" — so a non-nil-but-entirely-empty
// &SettingsPatch{} (neither Image nor CreateOptions set) still
// encodes as a present-but-empty "settings": {} key on the outer
// FieldsPatch, not an absent key. Only allocate a
// *SettingsPatch when you're about to set at least one field
// inside it.
type SettingsPatch struct {
	Image         *docker.Image
	CreateOptions *docker.CreateOptions
}

// SettingsPatchCodec — each present field is validated/encoded via
// the SAME codec manifesttemplate.ModuleSettingsCodec itself uses
// (manifesttemplate.ImageCodec/docker.CreateOptionsCodec) — no new validation
// logic, only the sparse-inclusion mechanism.
var SettingsPatchCodec = c.PartialStruct[SettingsPatch](
	c.PartialField("image", manifesttemplate.ImageCodec,
		func(s SettingsPatch) *docker.Image { return s.Image },
		func(s *SettingsPatch, v *docker.Image) { s.Image = v },
	),
	c.PartialField("createOptions", docker.CreateOptionsCodec,
		func(s SettingsPatch) *docker.CreateOptions { return s.CreateOptions },
		func(s *SettingsPatch, v *docker.CreateOptions) { s.CreateOptions = v },
	),
)

// FieldsPatch is a partial set of one module's fields to update —
// mirrors manifesttemplate.ModuleConfig's own field set (Settings groups Image/
// CreateOptions, exactly like ModuleConfig.Settings does on the base
// type; Env/Type/Status/RestartPolicy/Version are ModuleConfig's
// remaining top-level fields). Every field is a pointer; nil means
// "untouched" — including Env, which (unlike the base ModuleConfig.Env)
// is *manifesttemplate.EnvVars here rather than a bare EnvVars map, so every
// field in this struct follows the exact same nil-means-unset
// convention with no exceptions.
type FieldsPatch struct {
	ModuleName    manifesttemplate.ModuleName
	Settings      *SettingsPatch
	Env           *manifesttemplate.EnvVars
	Type          *manifesttemplate.Type
	Status        *manifesttemplate.Status
	RestartPolicy *manifesttemplate.RestartPolicy
	Version       *manifesttemplate.Version
}

// EmptyPatchError reports that a FieldsPatch had NOTHING set
// (every pointer field nil) — encoding it would produce a no-op patch,
// which is almost certainly a caller mistake (an empty patch silently
// "succeeding" would hide that mistake), so FieldsPatchCodec.Encode
// returns this instead. Implements slog.LogValuer for structured logging.
type EmptyPatchError struct {
	ModuleName manifesttemplate.ModuleName
}

func (e EmptyPatchError) Error() string {
	return "modulepatch: empty FieldsPatch for module " + string(e.ModuleName) + ": nothing to patch"
}

// LogValue implements slog.LogValuer for structured logging.
func (e EmptyPatchError) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("module_name", string(e.ModuleName)),
	)
}

// envVarsCodec re-types manifesttemplate.EnvVarsCodec's underlying
// Codec[map[EnvVarName]EnvVar] as Codec[manifesttemplate.EnvVars] — a trivial
// identity conversion needed only because c.Map[K,V] itself returns a
// Codec over the plain map type, not the named EnvVars type PartialField
// needs to match FieldsPatch.Env's own *manifesttemplate.EnvVars field type.
var envVarsCodec = c.MapCodecSafe(
	manifesttemplate.EnvVarsCodec,
	func(m map[manifesttemplate.EnvVarName]manifesttemplate.EnvVar) manifesttemplate.EnvVars {
		return manifesttemplate.EnvVars(m)
	},
	func(v manifesttemplate.EnvVars) (map[manifesttemplate.EnvVarName]manifesttemplate.EnvVar, error) {
		return v, nil
	},
)

// moduleFieldsBodyCodec declaratively encodes/decodes FieldsPatch's
// OWN patchable fields (everything except ModuleName, which the outer
// FieldsPatchCodec handles separately as the dynamic map key) —
// this is the entire "which fields were set" sparse-inclusion logic,
// expressed as plain field declarations instead of hand-rolled
// "if p.X != nil {...}" branches.
var moduleFieldsBodyCodec = c.PartialStruct[FieldsPatch](
	c.PartialField("settings", SettingsPatchCodec,
		func(p FieldsPatch) *SettingsPatch { return p.Settings },
		func(p *FieldsPatch, v *SettingsPatch) { p.Settings = v },
	),
	c.PartialField("env", envVarsCodec,
		func(p FieldsPatch) *manifesttemplate.EnvVars { return p.Env },
		func(p *FieldsPatch, v *manifesttemplate.EnvVars) { p.Env = v },
	),
	c.PartialField("type", manifesttemplate.TypeCodec,
		func(p FieldsPatch) *manifesttemplate.Type { return p.Type },
		func(p *FieldsPatch, v *manifesttemplate.Type) { p.Type = v },
	),
	c.PartialField("status", manifesttemplate.StatusCodec,
		func(p FieldsPatch) *manifesttemplate.Status { return p.Status },
		func(p *FieldsPatch, v *manifesttemplate.Status) { p.Status = v },
	),
	c.PartialField("restartPolicy", manifesttemplate.RestartPolicyCodec,
		func(p FieldsPatch) *manifesttemplate.RestartPolicy { return p.RestartPolicy },
		func(p *FieldsPatch, v *manifesttemplate.RestartPolicy) { p.RestartPolicy = v },
	),
	c.PartialField("version", manifesttemplate.VersionCodec,
		func(p FieldsPatch) *manifesttemplate.Version { return p.Version },
		func(p *FieldsPatch, v *manifesttemplate.Version) { p.Version = v },
	),
)

// FieldsPatchCodec encodes a FieldsPatch into the manifest's
// full nested wire shape (modulesContent -> $edgeAgent -> <dotted key> ->
// {settings?, env?, type?, status?, restartPolicy?, version?}),
// including ONLY the fields actually set (delegated to
// moduleFieldsBodyCodec — see this file's own top-of-file doc comment
// for why the OUTER module-name-keyed wrapping stays hand-written while
// everything else is declarative).
var FieldsPatchCodec = c.Codec[FieldsPatch]{
	Encode: func(p FieldsPatch) (any, error) {
		rawBody, err := moduleFieldsBodyCodec.Encode(p)
		if err != nil {
			return nil, err
		}
		moduleObj := rawBody.(map[string]any)
		if len(moduleObj) == 0 {
			return nil, EmptyPatchError{ModuleName: p.ModuleName}
		}

		key, err := manifesttemplate.ModuleNameCodec.Encode(p.ModuleName)
		if err != nil {
			return nil, err
		}

		return map[string]any{
			"modulesContent": map[string]any{
				"$edgeAgent": map[string]any{
					key.(string): moduleObj,
				},
			},
		}, nil
	},
	Decode: func(raw any) (FieldsPatch, error) {
		obj, err := asObject(raw, "modulesContent")
		if err != nil {
			return FieldsPatch{}, err
		}
		modulesContent, err := asObject(obj["modulesContent"], "$edgeAgent")
		if err != nil {
			return FieldsPatch{}, err
		}
		edgeAgent, ok := modulesContent["$edgeAgent"].(map[string]any)
		if !ok {
			return FieldsPatch{}, c.TypeMismatchError{Expected: "object", Got: typeName(modulesContent["$edgeAgent"])}
		}

		for rawKey, rawModule := range edgeAgent {
			name, err := manifesttemplate.ModuleNameCodec.Decode(rawKey)
			if err != nil {
				return FieldsPatch{}, err
			}
			p, err := moduleFieldsBodyCodec.Decode(rawModule)
			if err != nil {
				return FieldsPatch{}, err
			}
			p.ModuleName = name
			return p, nil // exactly one entry is ever produced by Encode above.
		}
		return FieldsPatch{}, nil
	},
	Schema: moduleFieldsBodyCodec.Schema,
}

// asObject type-asserts raw as map[string]any, returning a
// TypeMismatchError mentioning the FIRST key the caller is about to look
// up (wantKeyHint) if the assertion fails — purely a more useful error
// message, not a functional check on that key's presence.
func asObject(raw any, wantKeyHint string) (map[string]any, error) {
	obj, ok := raw.(map[string]any)
	if !ok {
		return nil, c.TypeMismatchError{Expected: "object (containing " + wantKeyHint + ")", Got: typeName(raw)}
	}
	return obj, nil
}

func typeName(v any) string {
	if v == nil {
		return "nil"
	}
	return fmt.Sprintf("%T", v)
}

// ── NewUpdateModuleImage ─────────────────────────────────────────────────

// NewUpdateModuleImage builds a FieldsPatch that updates ONLY
// moduleName's image — a named smart constructor for the single most
// common patch operation, built on the general FieldsPatch
// mechanism rather than a separate ad-hoc type. Validates via
// FieldsPatchCodec.New, which — since .New calls Encode and
// discards the result — validates BOTH moduleName (must be a valid slug,
// per manifesttemplate.ModuleNameCodec) and image (Name/Tag/Digest constraints,
// per manifesttemplate.ImageCodec) before the patch is ever applied to a real
// manifest via ports.PatchEncoded.
func NewUpdateModuleImage(moduleName manifesttemplate.ModuleName, image docker.Image) (FieldsPatch, error) {
	return FieldsPatchCodec.New(FieldsPatch{
		ModuleName: moduleName,
		Settings:   &SettingsPatch{Image: &image},
	})
}
