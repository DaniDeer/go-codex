package modulepatch

import (
	"fmt"
	"log/slog"

	c "github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/examples/go-edge-models/models/docker"
	"github.com/DaniDeer/go-codex/examples/go-edge-models/models/iotedge"
)

// This file holds ModuleFieldsPatch — a GENERAL, multi-field patch for
// one module. Every field is independently optional (a pointer, nil =
// untouched — a uniform rule with zero exceptions); only the ones
// actually set are included when encoding, leaving every other field on
// this module — and everything else in the manifest — untouched.
//
// ModuleFieldsPatch's own fields (Settings/Env/Type/Status/
// RestartPolicy/Version) are declared via codex.PartialField and
// composed into moduleFieldsBodyCodec via codex.PartialStruct — the
// sparse "omit unset fields entirely" mechanism (see
// docs/concepts/codec.md's "PartialField/PartialStruct" subsection for
// the full design). Settings
// itself groups Image/CreateOptions the SAME way — a nested
// ModuleSettingsPatch, also PartialStruct-built — mirroring how
// iotedge.ModuleConfig.Settings groups those same two fields on the
// base (non-patch) type. Nesting needs no special mechanism: presence
// for "settings" is decided exactly like any other field — is
// ModuleFieldsPatch.Settings nil?
//
// The ONE part that genuinely can't be expressed as a fixed field list
// (and therefore stays hand-written): the manifest wraps each patch
// under a RUNTIME-DETERMINED module-name key
// (modulesContent -> $edgeAgent -> <dotted key> -> {...}) — that key is
// data, not a static field name, so ModuleFieldsPatchCodec's own
// Encode/Decode still hand-assembles that outer wrapping (reusing
// iotedge.ModuleNameCodec for the key itself) around
// moduleFieldsBodyCodec's declarative result.

// ModuleSettingsPatch groups the two fields the wire format nests under
// "settings" — mirrors iotedge.ModuleSettings, but every field is
// independently optional.
//
// Gotcha: presence is decided by "is this pointer non-nil", not "does it
// have anything set inside" — so a non-nil-but-entirely-empty
// &ModuleSettingsPatch{} (neither Image nor CreateOptions set) still
// encodes as a present-but-empty "settings": {} key on the outer
// ModuleFieldsPatch, not an absent key. Only allocate a
// *ModuleSettingsPatch when you're about to set at least one field
// inside it.
type ModuleSettingsPatch struct {
	Image         *docker.Image
	CreateOptions *docker.CreateOptions
}

// ModuleSettingsPatchCodec — each present field is validated/encoded via
// the SAME codec iotedge.ModuleSettingsCodec itself uses
// (iotedge.ImageCodec/docker.CreateOptionsCodec) — no new validation
// logic, only the sparse-inclusion mechanism.
var ModuleSettingsPatchCodec = c.PartialStruct[ModuleSettingsPatch](
	c.PartialField("image", iotedge.ImageCodec,
		func(s ModuleSettingsPatch) *docker.Image { return s.Image },
		func(s *ModuleSettingsPatch, v *docker.Image) { s.Image = v },
	),
	c.PartialField("createOptions", docker.CreateOptionsCodec,
		func(s ModuleSettingsPatch) *docker.CreateOptions { return s.CreateOptions },
		func(s *ModuleSettingsPatch, v *docker.CreateOptions) { s.CreateOptions = v },
	),
)

// ModuleFieldsPatch is a partial set of one module's fields to update —
// mirrors iotedge.ModuleConfig's own field set (Settings groups Image/
// CreateOptions, exactly like ModuleConfig.Settings does on the base
// type; Env/Type/Status/RestartPolicy/Version are ModuleConfig's
// remaining top-level fields). Every field is a pointer; nil means
// "untouched" — including Env, which (unlike the base ModuleConfig.Env)
// is *iotedge.EnvVars here rather than a bare EnvVars map, so every
// field in this struct follows the exact same nil-means-unset
// convention with no exceptions.
type ModuleFieldsPatch struct {
	ModuleName    iotedge.ModuleName
	Settings      *ModuleSettingsPatch
	Env           *iotedge.EnvVars
	Type          *iotedge.Type
	Status        *iotedge.Status
	RestartPolicy *iotedge.RestartPolicy
	Version       *iotedge.Version
}

// EmptyPatchError reports that a ModuleFieldsPatch had NOTHING set
// (every pointer field nil) — encoding it would produce a no-op patch,
// which is almost certainly a caller mistake (an empty patch silently
// "succeeding" would hide that mistake), so ModuleFieldsPatchCodec.Encode
// returns this instead. Implements slog.LogValuer for structured logging.
type EmptyPatchError struct {
	ModuleName iotedge.ModuleName
}

func (e EmptyPatchError) Error() string {
	return "modulepatch: empty ModuleFieldsPatch for module " + string(e.ModuleName) + ": nothing to patch"
}

// LogValue implements slog.LogValuer for structured logging.
func (e EmptyPatchError) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("module_name", string(e.ModuleName)),
	)
}

// envVarsCodec re-types iotedge.EnvVarsCodec's underlying
// Codec[map[EnvVarName]EnvVar] as Codec[iotedge.EnvVars] — a trivial
// identity conversion needed only because c.Map[K,V] itself returns a
// Codec over the plain map type, not the named EnvVars type PartialField
// needs to match ModuleFieldsPatch.Env's own *iotedge.EnvVars field type.
var envVarsCodec = c.MapCodecSafe(
	iotedge.EnvVarsCodec,
	func(m map[iotedge.EnvVarName]iotedge.EnvVar) iotedge.EnvVars { return iotedge.EnvVars(m) },
	func(v iotedge.EnvVars) (map[iotedge.EnvVarName]iotedge.EnvVar, error) { return v, nil },
)

// moduleFieldsBodyCodec declaratively encodes/decodes ModuleFieldsPatch's
// OWN patchable fields (everything except ModuleName, which the outer
// ModuleFieldsPatchCodec handles separately as the dynamic map key) —
// this is the entire "which fields were set" sparse-inclusion logic,
// expressed as plain field declarations instead of hand-rolled
// "if p.X != nil {...}" branches.
var moduleFieldsBodyCodec = c.PartialStruct[ModuleFieldsPatch](
	c.PartialField("settings", ModuleSettingsPatchCodec,
		func(p ModuleFieldsPatch) *ModuleSettingsPatch { return p.Settings },
		func(p *ModuleFieldsPatch, v *ModuleSettingsPatch) { p.Settings = v },
	),
	c.PartialField("env", envVarsCodec,
		func(p ModuleFieldsPatch) *iotedge.EnvVars { return p.Env },
		func(p *ModuleFieldsPatch, v *iotedge.EnvVars) { p.Env = v },
	),
	c.PartialField("type", iotedge.TypeCodec,
		func(p ModuleFieldsPatch) *iotedge.Type { return p.Type },
		func(p *ModuleFieldsPatch, v *iotedge.Type) { p.Type = v },
	),
	c.PartialField("status", iotedge.StatusCodec,
		func(p ModuleFieldsPatch) *iotedge.Status { return p.Status },
		func(p *ModuleFieldsPatch, v *iotedge.Status) { p.Status = v },
	),
	c.PartialField("restartPolicy", iotedge.RestartPolicyCodec,
		func(p ModuleFieldsPatch) *iotedge.RestartPolicy { return p.RestartPolicy },
		func(p *ModuleFieldsPatch, v *iotedge.RestartPolicy) { p.RestartPolicy = v },
	),
	c.PartialField("version", iotedge.VersionCodec,
		func(p ModuleFieldsPatch) *iotedge.Version { return p.Version },
		func(p *ModuleFieldsPatch, v *iotedge.Version) { p.Version = v },
	),
)

// ModuleFieldsPatchCodec encodes a ModuleFieldsPatch into the manifest's
// full nested wire shape (modulesContent -> $edgeAgent -> <dotted key> ->
// {settings?, env?, type?, status?, restartPolicy?, version?}),
// including ONLY the fields actually set (delegated to
// moduleFieldsBodyCodec — see this file's own top-of-file doc comment
// for why the OUTER module-name-keyed wrapping stays hand-written while
// everything else is declarative).
var ModuleFieldsPatchCodec = c.Codec[ModuleFieldsPatch]{
	Encode: func(p ModuleFieldsPatch) (any, error) {
		rawBody, err := moduleFieldsBodyCodec.Encode(p)
		if err != nil {
			return nil, err
		}
		moduleObj := rawBody.(map[string]any)
		if len(moduleObj) == 0 {
			return nil, EmptyPatchError{ModuleName: p.ModuleName}
		}

		key, err := iotedge.ModuleNameCodec.Encode(p.ModuleName)
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
	Decode: func(raw any) (ModuleFieldsPatch, error) {
		obj, err := asObject(raw, "modulesContent")
		if err != nil {
			return ModuleFieldsPatch{}, err
		}
		modulesContent, err := asObject(obj["modulesContent"], "$edgeAgent")
		if err != nil {
			return ModuleFieldsPatch{}, err
		}
		edgeAgent, ok := modulesContent["$edgeAgent"].(map[string]any)
		if !ok {
			return ModuleFieldsPatch{}, c.TypeMismatchError{Expected: "object", Got: typeName(modulesContent["$edgeAgent"])}
		}

		for rawKey, rawModule := range edgeAgent {
			name, err := iotedge.ModuleNameCodec.Decode(rawKey)
			if err != nil {
				return ModuleFieldsPatch{}, err
			}
			p, err := moduleFieldsBodyCodec.Decode(rawModule)
			if err != nil {
				return ModuleFieldsPatch{}, err
			}
			p.ModuleName = name
			return p, nil // exactly one entry is ever produced by Encode above.
		}
		return ModuleFieldsPatch{}, nil
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

// ── NewUpdateModuleImagePatch ─────────────────────────────────────────────────

// NewUpdateModuleImagePatch builds a ModuleFieldsPatch that updates ONLY
// moduleName's image — a named smart constructor for the single most
// common patch operation, built on the general ModuleFieldsPatch
// mechanism rather than a separate ad-hoc type. Validates via
// ModuleFieldsPatchCodec.New, which — since .New calls Encode and
// discards the result — validates BOTH moduleName (must be a valid slug,
// per iotedge.ModuleNameCodec) and image (Name/Tag/Digest constraints,
// per iotedge.ImageCodec) before the patch is ever applied to a real
// manifest via ports.PatchEncoded.
func NewUpdateModuleImagePatch(moduleName iotedge.ModuleName, image docker.Image) (ModuleFieldsPatch, error) {
	return ModuleFieldsPatchCodec.New(ModuleFieldsPatch{
		ModuleName: moduleName,
		Settings:   &ModuleSettingsPatch{Image: &image},
	})
}
