package modulepatch

import (
	"fmt"
	"log/slog"

	c "github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/examples/go-edge-models/models/docker"
	"github.com/DaniDeer/go-codex/examples/go-edge-models/models/iotedge"
	"github.com/DaniDeer/go-codex/schema"
)

// This file holds ModuleFieldsPatch — a GENERAL, multi-field patch for
// one module. Every field is independently optional; only the ones
// actually set are included when encoding, leaving every other field on
// this module — and everything else in the manifest — untouched.
//
// Why this can't be built with codex.Struct: codex.Struct's own Encode
// unconditionally writes EVERY declared field into its output map (see
// codex/object.go), whether RequiredField or OptionalField — there is no
// built-in "omit this field from the encoded map when it was never set"
// behavior. ModuleFieldsPatchCodec is therefore a HAND-ROLLED
// codex.Codec[ModuleFieldsPatch] (Decode/Encode written directly, not
// composed via codex.Struct for the outer shape). Each field that IS
// present still gets validated/encoded via its own EXISTING codec
// (iotedge.ImageCodec, docker.CreateOptionsCodec, etc.) — no new
// validation logic, only the sparse-inclusion mechanism is new.

// ModuleFieldsPatch is a partial set of one module's fields to update —
// mirrors iotedge.ModuleConfig's own field set (Image/CreateOptions
// together form ModuleConfig.Settings on the wire; Env/Type/Status/
// RestartPolicy/Version are ModuleConfig's remaining top-level fields).
// Only non-nil pointer fields (and non-nil Env, which is already a map —
// nil means "untouched", the same convention ModuleConfig.Env itself
// uses) are included when [ModuleFieldsPatchCodec] encodes a value.
type ModuleFieldsPatch struct {
	ModuleName    iotedge.ModuleName
	Image         *docker.Image
	CreateOptions *docker.CreateOptions
	Env           iotedge.EnvVars
	Type          *iotedge.Type
	Status        *iotedge.Status
	RestartPolicy *iotedge.RestartPolicy
	Version       *iotedge.Version
}

// EmptyPatchError reports that a ModuleFieldsPatch had NOTHING set
// (every pointer field nil and Env empty) — encoding it would produce a
// no-op patch, which is almost certainly a caller mistake (an empty
// patch silently "succeeding" would hide that mistake), so
// ModuleFieldsPatchCodec.Encode returns this instead. Implements
// slog.LogValuer for structured logging.
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

// moduleFieldsPatchSchema is hand-built (not derived from codex.Struct,
// for the same reason the Encode/Decode functions below are hand-written)
// — one property per patchable field, pulling each field's own sub-codec
// Schema; none are Required (every field is optional in a patch).
var moduleFieldsPatchSchema = schema.Schema{
	Type: "object",
	Properties: []schema.Property{
		{Name: "image", Schema: iotedge.ImageCodec.Schema},
		{Name: "createOptions", Schema: docker.CreateOptionsCodec.Schema},
		{Name: "env", Schema: iotedge.EnvVarsCodec.Schema},
		{Name: "type", Schema: iotedge.TypeCodec.Schema},
		{Name: "status", Schema: iotedge.StatusCodec.Schema},
		{Name: "restartPolicy", Schema: iotedge.RestartPolicyCodec.Schema},
		{Name: "version", Schema: iotedge.VersionCodec.Schema},
	},
}

// ModuleFieldsPatchCodec encodes a ModuleFieldsPatch into the manifest's
// full nested wire shape (modulesContent -> $edgeAgent -> <dotted key> ->
// {settings:{image?,createOptions?}, env?, type?, status?,
// restartPolicy?, version?}), including ONLY the fields actually set —
// see this file's own top-of-file doc comment for why codex.Struct
// cannot express this. Decode is the exact inverse (reads whichever keys
// are present, leaving the rest nil/zero) — provided for symmetry and
// testability, even though ports.PatchEncoded itself only ever calls
// Encode.
var ModuleFieldsPatchCodec = c.Codec[ModuleFieldsPatch]{
	Encode: func(p ModuleFieldsPatch) (any, error) {
		settings := map[string]any{}
		if p.Image != nil {
			raw, err := iotedge.ImageCodec.Encode(*p.Image)
			if err != nil {
				return nil, err
			}
			settings["image"] = raw
		}
		if p.CreateOptions != nil {
			raw, err := docker.CreateOptionsCodec.Encode(*p.CreateOptions)
			if err != nil {
				return nil, err
			}
			settings["createOptions"] = raw
		}

		moduleObj := map[string]any{}
		if len(settings) > 0 {
			moduleObj["settings"] = settings
		}
		if p.Env != nil {
			raw, err := iotedge.EnvVarsCodec.Encode(p.Env)
			if err != nil {
				return nil, err
			}
			moduleObj["env"] = raw
		}
		if p.Type != nil {
			raw, err := iotedge.TypeCodec.Encode(*p.Type)
			if err != nil {
				return nil, err
			}
			moduleObj["type"] = raw
		}
		if p.Status != nil {
			raw, err := iotedge.StatusCodec.Encode(*p.Status)
			if err != nil {
				return nil, err
			}
			moduleObj["status"] = raw
		}
		if p.RestartPolicy != nil {
			raw, err := iotedge.RestartPolicyCodec.Encode(*p.RestartPolicy)
			if err != nil {
				return nil, err
			}
			moduleObj["restartPolicy"] = raw
		}
		if p.Version != nil {
			raw, err := iotedge.VersionCodec.Encode(*p.Version)
			if err != nil {
				return nil, err
			}
			moduleObj["version"] = raw
		}

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

		var p ModuleFieldsPatch
		for rawKey, rawModule := range edgeAgent {
			name, err := iotedge.ModuleNameCodec.Decode(rawKey)
			if err != nil {
				return ModuleFieldsPatch{}, err
			}
			p.ModuleName = name

			moduleObj, ok := rawModule.(map[string]any)
			if !ok {
				return ModuleFieldsPatch{}, c.TypeMismatchError{Expected: "object", Got: typeName(rawModule)}
			}

			if rawSettings, ok := moduleObj["settings"]; ok {
				settings, ok := rawSettings.(map[string]any)
				if !ok {
					return ModuleFieldsPatch{}, c.TypeMismatchError{Expected: "object", Got: typeName(rawSettings)}
				}
				if rawImage, ok := settings["image"]; ok {
					img, err := iotedge.ImageCodec.Decode(rawImage)
					if err != nil {
						return ModuleFieldsPatch{}, err
					}
					p.Image = &img
				}
				if rawCO, ok := settings["createOptions"]; ok {
					co, err := docker.CreateOptionsCodec.Decode(rawCO)
					if err != nil {
						return ModuleFieldsPatch{}, err
					}
					p.CreateOptions = &co
				}
			}
			if rawEnv, ok := moduleObj["env"]; ok {
				env, err := iotedge.EnvVarsCodec.Decode(rawEnv)
				if err != nil {
					return ModuleFieldsPatch{}, err
				}
				p.Env = env
			}
			if rawType, ok := moduleObj["type"]; ok {
				t, err := iotedge.TypeCodec.Decode(rawType)
				if err != nil {
					return ModuleFieldsPatch{}, err
				}
				p.Type = &t
			}
			if rawStatus, ok := moduleObj["status"]; ok {
				s, err := iotedge.StatusCodec.Decode(rawStatus)
				if err != nil {
					return ModuleFieldsPatch{}, err
				}
				p.Status = &s
			}
			if rawRP, ok := moduleObj["restartPolicy"]; ok {
				rp, err := iotedge.RestartPolicyCodec.Decode(rawRP)
				if err != nil {
					return ModuleFieldsPatch{}, err
				}
				p.RestartPolicy = &rp
			}
			if rawVersion, ok := moduleObj["version"]; ok {
				ver, err := iotedge.VersionCodec.Decode(rawVersion)
				if err != nil {
					return ModuleFieldsPatch{}, err
				}
				p.Version = &ver
			}
			break // exactly one entry is ever produced by Encode above.
		}
		return p, nil
	},
	Schema: moduleFieldsPatchSchema,
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
	return ModuleFieldsPatchCodec.New(ModuleFieldsPatch{ModuleName: moduleName, Image: &image})
}
