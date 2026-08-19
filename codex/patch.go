package codex

import (
	"fmt"
	"log/slog"
	"strings"
)

// This file holds the "apply a patch onto a base value" primitives — both
// the FLAT case (a fixed-field PartialStruct-built patch type applied
// onto a base of the same field shape) and the DOTTED-PATH case (a
// map[string]any patch whose keys are dotted paths of arbitrary depth,
// e.g. "factory-gw.env.API_URL"). Both share the same outer skeleton:
// encode base via its own codec → merge patch data onto the encoded map
// → decode the merged map back through the base codec (re-validating all
// constraints) — they differ only in the merge step (DeepMerge directly
// for the flat case; BuildDottedPatch + DeepMerge per key for the dotted
// case). See docs/concepts/codec.md's "Applying a patch" subsection and
// docs/concepts/pipelines.md's "Applying a patch: forge.Patch" subsection
// for the full design rationale.
//
// DeepMerge/BuildDottedPatch/ApplyDottedPatch are PROMOTIONS of logic
// that already existed, hand-rolled once each, elsewhere:
//   - DeepMerge was format.DeepMerge (format.DeepMerge is now a thin
//     wrapper calling this).
//   - BuildDottedPatch/ApplyDottedPatch were
//     examples/go-edge-models/models/iotedge/finaldeviceconfig.Merge's
//     own private buildNestedPatch/deepMerge helpers (that package now
//     calls these directly instead).
//
// None of this is RFC 7396 ("JSON Merge Patch") null-means-delete —
// every one of these functions is OVERWRITE/ADD ONLY: a patch value
// always either creates a new key or replaces an existing one; there is
// no way to DELETE a field the base already set.

// DeepMerge applies src over dst IN PLACE. Nested maps at the same key
// are merged recursively (patch wins on conflicts, dst's other keys
// survive untouched); scalar and array values in src overwrite the
// corresponding value in dst outright.
func DeepMerge(dst, src map[string]any) {
	for k, sv := range src {
		if dv, ok := dst[k]; ok {
			dm, dmOK := dv.(map[string]any)
			sm, smOK := sv.(map[string]any)
			if dmOK && smOK {
				DeepMerge(dm, sm)
				continue
			}
		}
		dst[k] = sv
	}
}

// ApplyPatch encodes base and patch via their own codecs to
// map[string]any intermediates, deep-merges patch over base (patch wins
// on conflicts, base's other keys survive untouched), and decodes the
// merged map back through baseCodec — re-running ALL of baseCodec's own
// Refine constraints on the result.
//
// Both codecs must produce an object (map[string]any) intermediate —
// this only supports struct-shaped codecs, matching PartialStruct's own
// object-shaped design. Returns TypeMismatchError if either codec's
// Encode does not produce a map[string]any (e.g. a scalar or slice
// codec).
//
// ApplyPatch is the FLAT case — patch's fields overwrite the SAME
// top-level keys base's own codec declares. For a patch whose keys are
// DOTTED PATHS reaching deeper than one field, see ApplyDottedPatchTo.
func ApplyPatch[T, P any](base T, baseCodec Codec[T], patch P, patchCodec Codec[P]) (T, error) {
	var zero T
	baseRaw, err := baseCodec.Encode(base)
	if err != nil {
		return zero, err
	}
	baseMap, ok := baseRaw.(map[string]any)
	if !ok {
		return zero, TypeMismatchError{Expected: "object", Got: fmt.Sprintf("%T", baseRaw)}
	}
	patchRaw, err := patchCodec.Encode(patch)
	if err != nil {
		return zero, err
	}
	patchMap, ok := patchRaw.(map[string]any)
	if !ok {
		return zero, TypeMismatchError{Expected: "object", Got: fmt.Sprintf("%T", patchRaw)}
	}
	DeepMerge(baseMap, patchMap)
	return baseCodec.Decode(baseMap)
}

// BuildDottedPatch wraps value in a chain of single-key maps, one per
// "."-delimited segment of path — e.g. path "env.API_URL" and value
// "http://x" builds {"env": {"API_URL": "http://x"}}. An EMPTY path
// returns value UNWRAPPED — used by [ApplyDottedPatch] when a dotted
// key had no segments beyond the top-level key it names (a bare
// module-name-shaped key targets the WHOLE value at that key, not a
// field inside it).
func BuildDottedPatch(path string, value any) any {
	if path == "" {
		return value
	}
	segments := strings.Split(path, ".")
	result := value
	for i := len(segments) - 1; i >= 0; i-- {
		result = map[string]any{segments[i]: result}
	}
	return result
}

// ApplyDottedPatch applies patch onto base, where each patch KEY is a
// dotted path (e.g. "factory-gw.env.API_URL") whose FIRST segment names
// a top-level key in base and remaining segments (if any) reach deeper
// via BuildDottedPatch + DeepMerge. Returns a NEW map — base is not
// mutated.
//
// When base has no existing value at a dotted key's first segment (nil/
// absent), the patch's built value is used WHOLESALE at that key — this
// is what lets a dotted patch introduce an entirely NEW top-level entry,
// not just override fields inside an existing one.
func ApplyDottedPatch(base, patch map[string]any) map[string]any {
	result := make(map[string]any, len(base)+len(patch))
	for k, v := range base {
		result[k] = v
	}
	for path, value := range patch {
		key, rest, found := strings.Cut(path, ".")
		nested := value
		if found {
			nested = BuildDottedPatch(rest, value)
		}
		result[key] = deepMergeAny(result[key], nested)
	}
	return result
}

// deepMergeAny recursively merges patch onto base: when BOTH are
// map[string]any, delegates to DeepMerge (key-by-key recursive merge,
// working on a fresh copy so the original base map is left untouched);
// otherwise patch REPLACES base outright (covers scalars, arrays, nil,
// and type mismatches, and the wholesale-introduce-new-key case above).
func deepMergeAny(base, patch any) any {
	baseMap, baseOK := base.(map[string]any)
	patchMap, patchOK := patch.(map[string]any)
	if !baseOK || !patchOK {
		return patch
	}
	result := make(map[string]any, len(baseMap)+len(patchMap))
	for k, v := range baseMap {
		result[k] = v
	}
	for k, v := range patchMap {
		result[k] = deepMergeAny(result[k], v)
	}
	return result
}

// ApplyDottedPatchTo encodes base via baseCodec, applies patch via
// ApplyDottedPatch, and decodes the merged result back through baseCodec
// — re-running ALL of baseCodec's own Refine constraints. The typed
// convenience wrapping ApplyDottedPatch, mirroring ApplyPatch's own
// encode-merge-decode skeleton exactly, for the DOTTED case.
//
// Returns TypeMismatchError if baseCodec's Encode does not produce a
// map[string]any intermediate.
func ApplyDottedPatchTo[T any](base T, baseCodec Codec[T], patch map[string]any) (T, error) {
	var zero T
	baseRaw, err := baseCodec.Encode(base)
	if err != nil {
		return zero, err
	}
	baseMap, ok := baseRaw.(map[string]any)
	if !ok {
		return zero, TypeMismatchError{Expected: "object", Got: fmt.Sprintf("%T", baseRaw)}
	}
	merged := ApplyDottedPatch(baseMap, patch)
	return baseCodec.Decode(merged)
}

// IsEmptyPatch reports whether patch encodes to an EMPTY object via
// patchCodec — true when every [PartialStruct]-declared field is unset.
// Works for ANY PartialStruct-built Codec[P] — no per-field enumeration
// needed, since PartialStruct's own Encode already omits every unset
// field entirely. An Encode failure is treated as "not empty" (a
// malformed patch is patchCodec.Encode's own concern to surface, not
// this function's).
func IsEmptyPatch[P any](patchCodec Codec[P], patch P) bool {
	enc, err := patchCodec.Encode(patch)
	if err != nil {
		return false
	}
	m, ok := enc.(map[string]any)
	return ok && len(m) == 0
}

// NonEmptyPatch returns a Constraint[P] wrapping [IsEmptyPatch], for
// callers who want to Refine a WRAPPER/DIFFERENT codec with the "reject
// a no-op patch" guard (never patchCodec itself — that would be
// self-referential construction). Mirrors the "reject a no-op patch"
// guard several packages already hand-roll per patch type (e.g.
// examples/go-edge-models/models/iotedge/modulepatch.NonEmptyFieldsPatch)
// — those richer, context-carrying versions remain unchanged; this is a
// generic building block for NEW, simpler cases.
func NonEmptyPatch[P any](patchCodec Codec[P]) Constraint[P] {
	return Constraint[P]{
		Name: "non-empty-patch",
		Check: func(p P) bool {
			return !IsEmptyPatch(patchCodec, p)
		},
		Message: func(p P) string {
			return "empty patch: nothing to patch"
		},
	}
}

// EmptyPatchError is the generic "no fields set" error [IsEmptyPatch]-
// based callers can return when they don't need extra per-patch context
// beyond "this patch was empty" — packages needing richer context (e.g.
// modulepatch.EmptyPatchError{ModuleName}) should keep their own richer
// error type; this is purely additive for new, simpler cases, not a
// replacement. Implements slog.LogValuer for structured logging.
type EmptyPatchError struct{}

func (e EmptyPatchError) Error() string {
	return "codex: empty patch: nothing to patch"
}

// LogValue implements slog.LogValuer for structured logging.
func (e EmptyPatchError) LogValue() slog.Value {
	return slog.GroupValue()
}
