package modulepatch

import (
	c "github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/examples/go-edge-models/models/docker"
	"github.com/DaniDeer/go-codex/examples/go-edge-models/models/iotedge"
)

// ModulePatch is a single module's desired image, addressed by module name.
// Its codec (ModulePatchCodec, below) encodes directly into the
// manifest's full nested wire shape needed for ports.PatchEncoded:
//
//	modulesContent -> $edgeAgent -> <dotted module key> -> settings -> image
//
// so a caller can apply it as a targeted patch without decoding or
// re-encoding the rest of the manifest document.
type ModulePatch struct {
	ModuleName iotedge.ModuleName
	ImageURL   string
}

// imageSettingsPatch mirrors iotedge.ModuleSettings but with ONLY "image"
// present — the minimal wire shape needed to patch settings.image without
// touching settings.createOptions. Image is docker.Image (matching
// iotedge.ModuleSettings.Image's own type) — ModulePatch.ImageURL (the
// PUBLIC, caller-facing field) stays a plain string for ergonomics;
// ModulePatchCodec parses/validates it into a docker.Image internally via
// docker.ImageCodec before it ever reaches this type.
type imageSettingsPatch struct {
	Image docker.Image
}

// moduleConfigPatch mirrors iotedge.ModuleConfig, but with ONLY its
// "settings" field present (env/type/status/restartPolicy/version are all
// left untouched by this patch).
type moduleConfigPatch struct {
	Settings imageSettingsPatch
}

// modulesContentPatch mirrors iotedge.ModulesContent, but its EdgeAgent map
// holds only the single module entry being patched (never the full module
// set).
type modulesContentPatch struct {
	EdgeAgent map[iotedge.ModuleName]moduleConfigPatch
}

// manifestImagePatch mirrors iotedge.DeploymentManifest — the top-level
// wire shape ModulePatchCodec threads a ModulePatch through so its
// Encode/Decode output matches the real manifest document's nesting
// exactly.
type manifestImagePatch struct {
	ModulesContent modulesContentPatch
}

// imageSettingsPatchCodec validates the single "image" field via the SAME
// iotedge.ImageCodec iotedge.ModuleSettingsCodec itself uses — no new
// constraint is defined here.
var imageSettingsPatchCodec = c.Struct[imageSettingsPatch](
	c.RequiredField("image", iotedge.ImageCodec,
		func(s imageSettingsPatch) docker.Image { return s.Image },
		func(s *imageSettingsPatch, val docker.Image) { s.Image = val },
	),
)

// moduleConfigPatchCodec wraps imageSettingsPatchCodec under "settings" —
// mirrors iotedge.ModuleConfigCodec's "settings" field, but declares none of
// ModuleConfig's other fields (env/type/status/restartPolicy/version), so
// those are left untouched by the deep-merge.
var moduleConfigPatchCodec = c.Struct[moduleConfigPatch](
	c.RequiredField("settings", imageSettingsPatchCodec,
		func(m moduleConfigPatch) imageSettingsPatch { return m.Settings },
		func(m *moduleConfigPatch, val imageSettingsPatch) { m.Settings = val },
	),
)

// edgeAgentImagePatchCodec merges the dotted module key (via the SAME
// iotedge.ModuleNameCodec iotedge.ModulesCodec itself uses) with the
// moduleConfigPatch value — a single-entry map[ModuleName]moduleConfigPatch
// in practice, since ModulePatchCodec below only ever populates one entry.
var edgeAgentImagePatchCodec = c.Map[iotedge.ModuleName, moduleConfigPatch](iotedge.ModuleNameCodec, moduleConfigPatchCodec)

var modulesContentPatchCodec = c.Struct[modulesContentPatch](
	c.RequiredField("$edgeAgent", edgeAgentImagePatchCodec,
		func(mc modulesContentPatch) map[iotedge.ModuleName]moduleConfigPatch { return mc.EdgeAgent },
		func(mc *modulesContentPatch, val map[iotedge.ModuleName]moduleConfigPatch) { mc.EdgeAgent = val },
	),
)

var manifestImagePatchCodec = c.Struct[manifestImagePatch](
	c.RequiredField("modulesContent", modulesContentPatchCodec,
		func(m manifestImagePatch) modulesContentPatch { return m.ModulesContent },
		func(m *manifestImagePatch, val modulesContentPatch) { m.ModulesContent = val },
	),
)

// ModulePatchCodec converts the flat ModulePatch{ModuleName, ImageURL} to
// and from the fully-nested manifestImagePatch wire shape via MapCodecSafe —
// the same building-block pattern used throughout iotedge's own codecs
// (e.g. TypeCodec/StatusCodec wrapping a plain string as a named type),
// here wrapping a whole nested document shape instead of a scalar.
//
// ModulePatch.ImageURL stays a plain string — the deliberately simple,
// caller-facing shape this package exists for — but the encode direction
// now PARSES+VALIDATES it via docker.ImageCodec.Decode before embedding
// it as a docker.Image (matching iotedge.ModuleSettings.Image's own
// type): a malformed ImageURL is now caught here, before ever reaching
// ports.PatchEncoded/the real manifest file. The decode direction calls
// docker.Image's Stringer to flatten back to ImageURL — round-tripping
// through the SAME validated representation on both directions.
//
// Encode(ModulePatch) returns exactly:
//
//	{"modulesContent":{"$edgeAgent":{"<dotted key>":{"settings":{"image":"<url>"}}}}}
//
// — a map[string]any (required by ports.PatchEncoded) shaped so
// format.DeepMerge only overwrites settings.image for the named module,
// leaving every other field/module in the real manifest untouched.
var ModulePatchCodec = c.MapCodecSafe(
	manifestImagePatchCodec,
	func(w manifestImagePatch) ModulePatch {
		for name, m := range w.ModulesContent.EdgeAgent {
			return ModulePatch{ModuleName: name, ImageURL: m.Settings.Image.String()}
		}
		return ModulePatch{}
	},
	func(p ModulePatch) (manifestImagePatch, error) {
		img, err := docker.ImageCodec.Decode(p.ImageURL)
		if err != nil {
			return manifestImagePatch{}, err
		}
		return manifestImagePatch{
			ModulesContent: modulesContentPatch{
				EdgeAgent: map[iotedge.ModuleName]moduleConfigPatch{
					p.ModuleName: {Settings: imageSettingsPatch{Image: img}},
				},
			},
		}, nil
	},
)
