package forge

import (
	"github.com/DaniDeer/go-codex/codex"
)

// PatchInput bundles a base value with the patch to apply onto it — the
// two-port input shape [Patch]'s Function takes. Both values are supplied
// PER-CALL (unlike [Reduce]'s curried, construction-time init accumulator),
// since the real motivating use case (applying a device-specific patch onto
// a use-case template that varies per call) needs both values fresh every
// time.
type PatchInput[T, P any] struct {
	Base  T
	Patch P
}

// Patch returns a *Function[PatchInput[T,P], T] that applies patch onto base
// via [codex.ApplyPatch]. The FunctionSpec carries Kind=FunctionKindPatch and
// TWO named input ports ("base", "patch") — this falls out of [NewFunction]'s
// existing inputSpecs machinery automatically, since a codex.Struct-shaped
// input codec is already split into one PortSpec per field; Patch needs no
// special-casing to get this.
//
// Patch delegates entirely to [NewFunction] — governance options
// ([FunctionMeta], [WithRefinement]) and error wiring (InputError/
// OutputError/RefinementError) all work exactly as they do for any other
// forge Function. WithRefinement, if supplied, receives the WHOLE
// PatchInput[T,P] value (base + patch together) — use it for cross-value
// constraints spanning both.
//
// Panics if name or version is empty — same convention as every other forge
// constructor.
//
//	deviceModulePatch := forge.Patch("applyModulePatch", "1.0.0",
//	    manifesttemplate.ModuleConfigCodec, modulepatch.FieldsBodyCodec,
//	    forge.FunctionMeta{Description: "Applies a FieldsPatch onto a ModuleConfig."},
//	)
//	updated, err := deviceModulePatch.Apply(forge.PatchInput[manifesttemplate.ModuleConfig, modulepatch.FieldsPatch]{
//	    Base: currentConfig, Patch: patch,
//	})
func Patch[T, P any](
	name, version string,
	baseCodec codex.Codec[T],
	patchCodec codex.Codec[P],
	opts ...FunctionOpt,
) *Function[PatchInput[T, P], T] {
	inputCodec := codex.Struct[PatchInput[T, P]](
		codex.RequiredField("base", baseCodec,
			func(in PatchInput[T, P]) T { return in.Base },
			func(in *PatchInput[T, P], v T) { in.Base = v },
		),
		codex.RequiredField("patch", patchCodec,
			func(in PatchInput[T, P]) P { return in.Patch },
			func(in *PatchInput[T, P], v P) { in.Patch = v },
		),
	)
	fn := NewFunction(name, version, inputCodec, baseCodec,
		func(in PatchInput[T, P]) (T, error) {
			return codex.ApplyPatch(in.Base, baseCodec, in.Patch, patchCodec)
		},
		opts...,
	)
	fn.Spec.Kind = FunctionKindPatch
	return fn
}
