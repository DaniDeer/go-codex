package modulepatch

import (
	c "github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/examples/go-edge-models/iotedge"
)

// imageSettingsPatchCodec validates the single "image" field via the SAME
// iotedge.ImageCodec iotedge.ModuleSettingsCodec itself uses — no new
// constraint is defined here.
var imageSettingsPatchCodec = c.Struct[imageSettingsPatch](
	c.RequiredField("image", iotedge.ImageCodec,
		func(s imageSettingsPatch) string { return s.Image },
		func(s *imageSettingsPatch, val string) { s.Image = val },
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
// the same building-block pattern used throughout iotedge/codecs.go (e.g.
// TypeCodec/StatusCodec wrapping a plain string as a named type), here
// wrapping a whole nested document shape instead of a scalar.
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
			return ModulePatch{ModuleName: name, ImageURL: m.Settings.Image}
		}
		return ModulePatch{}
	},
	func(p ModulePatch) (manifestImagePatch, error) {
		return manifestImagePatch{
			ModulesContent: modulesContentPatch{
				EdgeAgent: map[iotedge.ModuleName]moduleConfigPatch{
					p.ModuleName: {Settings: imageSettingsPatch{Image: p.ImageURL}},
				},
			},
		}, nil
	},
)
