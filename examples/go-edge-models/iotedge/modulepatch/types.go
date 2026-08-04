package modulepatch

import "github.com/DaniDeer/go-codex/examples/go-edge-models/iotedge"

// ModulePatch is a single module's desired image, addressed by module name.
// Its codec (ModulePatchCodec, in codecs.go) encodes directly into the
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
// touching settings.createOptions.
type imageSettingsPatch struct {
	Image string
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
