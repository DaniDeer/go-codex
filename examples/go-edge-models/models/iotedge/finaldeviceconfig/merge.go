package finaldeviceconfig

import (
	"fmt"
	"strings"

	c "github.com/DaniDeer/go-codex/codex"
	deviceconfig "github.com/DaniDeer/go-codex/examples/go-edge-models/models/iotedge/deviceconfig"
	manifesttemplate "github.com/DaniDeer/go-codex/examples/go-edge-models/models/iotedge/manifesttemplate"
)

// ── Merge ──────────────────────────────────────────────────────────────────────
//
// Merge layers a deviceconfig.Patch onto a manifesttemplate.DeploymentManifest,
// producing the FINAL, deployable config for one device: "template +
// device config, layered on top". Every EdgeAgent entry's dotted path is
// walked/created inside the relevant module's own raw JSON shape via
// codex.ApplyDottedPatch; every EdgeHub entry is added/overwritten
// wholesale (routes are atomic). The result is re-encoded through
// manifesttemplate.DeploymentManifestCodec, so any merge that produces
// an invalid manifest fails HERE, not silently.
//
// Merge is OVERWRITE/ADD ONLY: a patch value always either creates a new
// key or replaces an existing one; there is no way to DELETE a field the
// base template already set (no RFC 7396 null-means-remove semantics) —
// codex.ApplyDottedPatch's own documented behavior.
//
// This is a DERIVED operation — not part of either wire format — so it
// lives in its own package rather than manifesttemplate or deviceconfig,
// mirroring modulepatch's own "derived, not wire" positioning one level
// up: Merge depends on BOTH manifesttemplate (the template) AND
// deviceconfig (the patch), a dependency shape neither wire package
// itself may take on (they must stay independently reusable, with zero
// knowledge of each other).
//
// The dotted-merge ALGORITHM itself (this file used to hand-roll it as
// buildNestedPatch/deepMerge) is now promoted to
// codex.BuildDottedPatch/codex.ApplyDottedPatch — see
// docs/concepts/codec.md's "Applying a patch" subsection. This file's
// own job is narrowed to the manifest-specific bookkeeping ApplyDottedPatch cannot
// know about: patch.EdgeAgent's keys are BARE module names (deviceconfig
// strips manifesttemplate.ModuleKeyPrefix on decode — see
// deviceconfig.go), but edgeAgent's own map keys carry that prefix
// (e.g. "properties.desired.modules.factory-gw") — so this function
// strips the prefix before calling ApplyDottedPatch (whose first-dotted-
// segment matching assumes base's top-level keys are exactly what a
// patch key's first segment names) and re-adds it afterward.

// Merge applies patch on top of base, returning the fully layered
// manifesttemplate.DeploymentManifest.
func Merge(base manifesttemplate.DeploymentManifest, patch deviceconfig.Patch) (manifesttemplate.DeploymentManifest, error) {
	rawBase, err := manifesttemplate.DeploymentManifestCodec.Encode(base)
	if err != nil {
		return manifesttemplate.DeploymentManifest{}, err
	}
	modulesContent, err := asObject(rawBase, manifesttemplate.ModulesContentKey)
	if err != nil {
		return manifesttemplate.DeploymentManifest{}, err
	}
	mc, err := asObject(modulesContent[manifesttemplate.ModulesContentKey], manifesttemplate.EdgeAgentKey)
	if err != nil {
		return manifesttemplate.DeploymentManifest{}, err
	}

	edgeAgent, _ := mc[manifesttemplate.EdgeAgentKey].(map[string]any)
	if edgeAgent == nil {
		edgeAgent = map[string]any{}
	}
	bareEdgeAgent := make(map[string]any, len(edgeAgent))
	for key, value := range edgeAgent {
		bareEdgeAgent[strings.TrimPrefix(key, manifesttemplate.ModuleKeyPrefix)] = value
	}
	merged := c.ApplyDottedPatch(bareEdgeAgent, patch.EdgeAgent)
	edgeAgent = make(map[string]any, len(merged))
	for name, value := range merged {
		edgeAgent[manifesttemplate.ModuleKeyPrefix+name] = value
	}
	mc[manifesttemplate.EdgeAgentKey] = edgeAgent

	if len(patch.EdgeHub) > 0 {
		edgeHub, _ := mc[manifesttemplate.EdgeHubKey].(map[string]any)
		if edgeHub == nil {
			edgeHub = map[string]any{}
		}
		for name, route := range patch.EdgeHub {
			key, err := manifesttemplate.RouteNameCodec.Encode(name)
			if err != nil {
				return manifesttemplate.DeploymentManifest{}, err
			}
			raw, err := manifesttemplate.RouteCodec.Encode(route)
			if err != nil {
				return manifesttemplate.DeploymentManifest{}, err
			}
			edgeHub[key.(string)] = raw
		}
		mc[manifesttemplate.EdgeHubKey] = edgeHub
	}

	return manifesttemplate.DeploymentManifestCodec.Decode(rawBase)
}

// asObject type-asserts raw as map[string]any, returning a
// c.TypeMismatchError mentioning the FIRST key the caller is about to
// look up (wantKeyHint) if the assertion fails — purely a more useful
// error message, not a functional check on that key's presence. A
// small, LOCAL copy of the same helper modulepatch/deviceconfig each
// keep their own copy of — a deliberate micro-duplication precedent
// already established in this codebase, not worth a shared package for
// ~10 lines.
func asObject(raw any, wantKeyHint string) (map[string]any, error) {
	obj, ok := raw.(map[string]any)
	if !ok {
		hint := "object"
		if wantKeyHint != "" {
			hint = "object (containing " + wantKeyHint + ")"
		}
		return nil, c.TypeMismatchError{Expected: hint, Got: typeName(raw)}
	}
	return obj, nil
}

func typeName(v any) string {
	if v == nil {
		return "nil"
	}
	return fmt.Sprintf("%T", v)
}
