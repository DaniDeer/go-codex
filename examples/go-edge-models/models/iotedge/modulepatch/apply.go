package modulepatch

import (
	c "github.com/DaniDeer/go-codex/codex"
	iothub "github.com/DaniDeer/go-codex/examples/go-edge-models/models/azure/iothub"
)

// ApplyToModule applies patch onto base — an IN-MEMORY alternative to
// going through disk via app/iotedge's PatchUseCaseModule/
// PatchDeviceModule (e.g. for tests, dry-runs, or composing into a forge
// pipeline via forge.Patch).
//
// Delegates to codex.ApplyPatch(base, iothub.ModuleConfigCodec,
// patch, FieldsBodyCodec) — valid because both codecs share EXACTLY the
// same field keys ("settings"/"env"/"type"/"status"/"restartPolicy"/
// "version"), so FieldsBodyCodec's sparse (only-set-fields) encoded
// object merges directly onto ModuleConfigCodec's own full encoded
// object with no translation needed.
func ApplyToModule(base iothub.ModuleConfig, patch FieldsPatch) (iothub.ModuleConfig, error) {
	return c.ApplyPatch(base, iothub.ModuleConfigCodec, patch, FieldsBodyCodec)
}
