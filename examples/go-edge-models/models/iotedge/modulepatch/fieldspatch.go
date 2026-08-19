package modulepatch

import (
	"fmt"
	"log/slog"

	c "github.com/DaniDeer/go-codex/codex"
	iothub "github.com/DaniDeer/go-codex/examples/go-edge-models/models/azure/iothub"
	"github.com/DaniDeer/go-codex/examples/go-edge-models/models/docker"
)

// This file holds FieldsPatch — a GENERAL, multi-field patch for
// one module. Every field is independently optional (a pointer, nil =
// untouched — a uniform rule with zero exceptions); only the ones
// actually set are included when encoding, leaving every other field on
// this module — and everything else in the manifest — untouched.
//
// FieldsPatch's own fields (Settings/Env/Type/Status/
// RestartPolicy/Version) are declared via codex.PartialField and
// composed into FieldsBodyCodec via codex.PartialStruct — the
// sparse "omit unset fields entirely" mechanism (see
// docs/concepts/codec.md's "PartialField/PartialStruct" subsection for
// the full design). Settings
// itself groups Image/CreateOptions the SAME way — a nested
// SettingsPatch, also PartialStruct-built — mirroring how
// iothub.ModuleConfig.Settings groups those same two fields on the
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
// iothub.ModuleNameCodec for the key itself) around
// FieldsBodyCodec's declarative result.

// SettingsPatch groups the two fields the wire format nests under
// "settings" — mirrors iothub.ModuleSettings, but every field is
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
// the SAME codec iothub.ModuleSettingsCodec itself uses
// (iothub.ImageCodec/iothub.CreateOptionsFieldCodec) —
// no new validation logic, only the sparse-inclusion mechanism.
// CreateOptions uses CreateOptionsFieldCodec specifically (NOT
// docker.CreateOptionsCodec directly) since the wire shape is a
// JSON-ESCAPED STRING, not a raw object — the same "settings.
// createOptions" field iothub.ModuleSettingsCodec itself
// wraps.
var SettingsPatchCodec = c.PartialStruct[SettingsPatch](
	c.PartialField("image", iothub.ImageCodec,
		func(s SettingsPatch) *docker.Image { return s.Image },
		func(s *SettingsPatch, v *docker.Image) { s.Image = v },
	),
	c.PartialField("createOptions", iothub.CreateOptionsFieldCodec,
		func(s SettingsPatch) *docker.CreateOptions { return s.CreateOptions },
		func(s *SettingsPatch, v *docker.CreateOptions) { s.CreateOptions = v },
	),
)

// FieldsPatch is a partial set of one module's fields to update —
// mirrors iothub.ModuleConfig's own field set (Settings groups Image/
// CreateOptions, exactly like ModuleConfig.Settings does on the base
// type; Env/Type/Status/RestartPolicy/Version are ModuleConfig's
// remaining top-level fields). Every field is a pointer; nil means
// "untouched" — including Env, which (unlike the base ModuleConfig.Env)
// is *iothub.EnvVars here rather than a bare EnvVars map, so every
// field in this struct follows the exact same nil-means-unset
// convention with no exceptions.
type FieldsPatch struct {
	ModuleName    iothub.ModuleName
	Settings      *SettingsPatch
	Env           *iothub.EnvVars
	Type          *iothub.Type
	Status        *iothub.Status
	RestartPolicy *iothub.RestartPolicy
	Version       *iothub.Version
}

// NonEmptyFieldsPatch is a Constraint checking that AT LEAST ONE of
// FieldsPatch's pointer fields is set — the same "nothing to patch"
// predicate FieldsPatchCodec.Encode enforces internally (there, via the
// richer EmptyPatchError{ModuleName}), exported here as a standalone,
// reusable building block for callers using [FieldsBodyCodec] directly
// (e.g. to assemble a device-level patch — see
// models/iotedge/deviceconfig's Patch) who want the SAME "reject a no-op
// patch" guard without going through FieldsPatchCodec's own
// module-name-keyed wrapping. NOT applied via .Refine on
// [FieldsBodyCodec] itself — doing so would change
// FieldsPatchCodec.Encode's existing EmptyPatchError into a generic
// ConstraintError, a breaking behavior change this constraint is
// deliberately kept opt-in to avoid.
//
// A GENERIC version of this exact guard now exists as
// codex.NonEmptyPatch/codex.IsEmptyPatch/codex.EmptyPatchError for any
// PartialStruct-built patch type that doesn't need the richer
// module-name-carrying error this file's own EmptyPatchError provides —
// this file's own NonEmptyFieldsPatch/EmptyPatchError are UNCHANGED,
// not migrated, since they carry that extra context the generic version
// intentionally omits. See docs/guides/wire-vocabulary.md's "Applying a
// patch" section.
var NonEmptyFieldsPatch = c.Constraint[FieldsPatch]{
	Name: "non-empty-fields-patch",
	Check: func(p FieldsPatch) bool {
		return p.Settings != nil || p.Env != nil || p.Type != nil ||
			p.Status != nil || p.RestartPolicy != nil || p.Version != nil
	},
	Message: func(p FieldsPatch) string {
		return fmt.Sprintf("modulepatch: empty FieldsPatch for module %q: nothing to patch", p.ModuleName)
	},
}

// EmptyPatchError reports that a FieldsPatch had NOTHING set
// (every pointer field nil) — encoding it would produce a no-op patch,
// which is almost certainly a caller mistake (an empty patch silently
// "succeeding" would hide that mistake), so FieldsPatchCodec.Encode
// returns this instead. Implements slog.LogValuer for structured logging.
type EmptyPatchError struct {
	ModuleName iothub.ModuleName
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

// envVarsCodec re-types iothub.EnvVarsCodec's underlying
// Codec[map[EnvVarName]EnvVar] as Codec[iothub.EnvVars] — a trivial
// identity conversion needed only because c.Map[K,V] itself returns a
// Codec over the plain map type, not the named EnvVars type PartialField
// needs to match FieldsPatch.Env's own *iothub.EnvVars field type.
var envVarsCodec = c.MapCodecSafe(
	iothub.EnvVarsCodec,
	func(m map[iothub.EnvVarName]iothub.EnvVar) iothub.EnvVars {
		return iothub.EnvVars(m)
	},
	func(v iothub.EnvVars) (map[iothub.EnvVarName]iothub.EnvVar, error) {
		return v, nil
	},
)

// FieldsBodyCodec declaratively encodes/decodes FieldsPatch's
// OWN patchable fields (everything except ModuleName, which the outer
// FieldsPatchCodec handles separately as the dynamic map key) —
// this is the entire "which fields were set" sparse-inclusion logic,
// expressed as plain field declarations instead of hand-rolled
// "if p.X != nil {...}" branches.
//
// EXPORTED so this SAME typed validation reuses at the DEVICE level too
// — encoding a FieldsPatch via FieldsBodyCodec produces exactly the raw
// {settings?, env?, type?, status?, restartPolicy?, version?} object a
// caller can assign directly to
// models/iotedge/deviceconfig.Patch.EdgeAgent[string(patch.ModuleName)],
// bridging modulepatch's TEMPLATE-level typed patch mechanism and
// deviceconfig's DEVICE-level raw dotted-path patch mechanism with ZERO
// duplicated validation logic (see app/iotedge's PatchDeviceModule).
var FieldsBodyCodec = c.PartialStruct[FieldsPatch](
	c.PartialField("settings", SettingsPatchCodec,
		func(p FieldsPatch) *SettingsPatch { return p.Settings },
		func(p *FieldsPatch, v *SettingsPatch) { p.Settings = v },
	),
	c.PartialField("env", envVarsCodec,
		func(p FieldsPatch) *iothub.EnvVars { return p.Env },
		func(p *FieldsPatch, v *iothub.EnvVars) { p.Env = v },
	),
	c.PartialField("type", iothub.TypeCodec,
		func(p FieldsPatch) *iothub.Type { return p.Type },
		func(p *FieldsPatch, v *iothub.Type) { p.Type = v },
	),
	c.PartialField("status", iothub.StatusCodec,
		func(p FieldsPatch) *iothub.Status { return p.Status },
		func(p *FieldsPatch, v *iothub.Status) { p.Status = v },
	),
	c.PartialField("restartPolicy", iothub.RestartPolicyCodec,
		func(p FieldsPatch) *iothub.RestartPolicy { return p.RestartPolicy },
		func(p *FieldsPatch, v *iothub.RestartPolicy) { p.RestartPolicy = v },
	),
	c.PartialField("version", iothub.VersionCodec,
		func(p FieldsPatch) *iothub.Version { return p.Version },
		func(p *FieldsPatch, v *iothub.Version) { p.Version = v },
	),
)

// FieldsPatchCodec encodes a FieldsPatch into the manifest's
// full nested wire shape (modulesContent -> $edgeAgent -> <dotted key> ->
// {settings?, env?, type?, status?, restartPolicy?, version?}),
// including ONLY the fields actually set (delegated to
// FieldsBodyCodec — see this file's own top-of-file doc comment
// for why the OUTER module-name-keyed wrapping stays hand-written while
// everything else is declarative).
var FieldsPatchCodec = c.Codec[FieldsPatch]{
	Encode: func(p FieldsPatch) (any, error) {
		rawBody, err := FieldsBodyCodec.Encode(p)
		if err != nil {
			return nil, err
		}
		moduleObj := rawBody.(map[string]any)
		if len(moduleObj) == 0 {
			return nil, EmptyPatchError{ModuleName: p.ModuleName}
		}

		key, err := iothub.ModuleNameCodec.Encode(p.ModuleName)
		if err != nil {
			return nil, err
		}

		return map[string]any{
			iothub.ModulesContentKey: map[string]any{
				iothub.EdgeAgentKey: map[string]any{
					key.(string): moduleObj,
				},
			},
		}, nil
	},
	Decode: func(raw any) (FieldsPatch, error) {
		obj, err := asObject(raw, iothub.ModulesContentKey)
		if err != nil {
			return FieldsPatch{}, err
		}
		modulesContent, err := asObject(obj[iothub.ModulesContentKey], iothub.EdgeAgentKey)
		if err != nil {
			return FieldsPatch{}, err
		}
		edgeAgent, ok := modulesContent[iothub.EdgeAgentKey].(map[string]any)
		if !ok {
			return FieldsPatch{}, c.TypeMismatchError{Expected: "object", Got: typeName(modulesContent[iothub.EdgeAgentKey])}
		}

		for rawKey, rawModule := range edgeAgent {
			name, err := iothub.ModuleNameCodec.Decode(rawKey)
			if err != nil {
				return FieldsPatch{}, err
			}
			p, err := FieldsBodyCodec.Decode(rawModule)
			if err != nil {
				return FieldsPatch{}, err
			}
			p.ModuleName = name
			return p, nil // exactly one entry is ever produced by Encode above.
		}
		return FieldsPatch{}, nil
	},
	Schema: FieldsBodyCodec.Schema,
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
// per iothub.ModuleNameCodec) and image (Name/Tag/Digest constraints,
// per iothub.ImageCodec) before the patch is ever applied to a real
// manifest via ports.PatchEncoded.
func NewUpdateModuleImage(moduleName iothub.ModuleName, image docker.Image) (FieldsPatch, error) {
	return FieldsPatchCodec.New(FieldsPatch{
		ModuleName: moduleName,
		Settings:   &SettingsPatch{Image: &image},
	})
}

// NewUpdateModuleImageFromBase builds a FULL FieldsPatch — every field
// set, seeded from base, with ONLY Image changed — for the case
// [NewUpdateModuleImage]'s SPARSE patch cannot safely handle: writing
// moduleName's image for the FIRST TIME at a scope (a use case template
// or device config) where moduleName has no EXISTING entry of its own
// yet (it currently only resolves via a LOWER layer, e.g.
// models/azure/iothub's global base deployment). A sparse
// patch deep-merged onto NOTHING would produce an incomplete entry
// missing required fields (status/restartPolicy/version); seeding every
// field from the already-resolved base avoids that entirely — the
// written entry is immediately valid on its own, matching what
// [iothub.ModuleConfigCodec] requires.
//
// Validates via FieldsPatchCodec.New exactly like [NewUpdateModuleImage].
func NewUpdateModuleImageFromBase(moduleName iothub.ModuleName, base iothub.ModuleConfig, image docker.Image) (FieldsPatch, error) {
	createOptions := base.Settings.CreateOptions
	env := base.Env
	typ := base.Type
	status := base.Status
	restartPolicy := base.RestartPolicy
	version := base.Version
	return FieldsPatchCodec.New(FieldsPatch{
		ModuleName:    moduleName,
		Settings:      &SettingsPatch{Image: &image, CreateOptions: &createOptions},
		Env:           &env,
		Type:          &typ,
		Status:        &status,
		RestartPolicy: &restartPolicy,
		Version:       &version,
	})
}
