// Package finaldeviceconfig holds Merge — the DERIVED operation that
// layers one device's deviceconfig.Patch onto its use case's own
// manifesttemplate.DeploymentManifest, producing the FINAL, deployable
// config for that device: "template + device config, layered on top".
//
// This is a SEPARATE package from BOTH manifesttemplate and
// deviceconfig (not a file inside either) because Merge depends on BOTH
// wire formats at once — a dependency shape neither wire package may
// take on itself (they must stay independently reusable, with zero
// knowledge of each other; manifesttemplate never imports deviceconfig,
// and deviceconfig never imports manifesttemplate beyond a few
// field-level codecs it reuses for Patch's own wire shape). Mirrors
// models/iotedge/modulepatch's own "derived, not wire" positioning one
// level up — modulepatch depends only on manifesttemplate; this package
// depends on manifesttemplate AND deviceconfig.
//
// Merge is OVERWRITE/ADD ONLY — a patch value always either creates a
// new key or replaces an existing one; there is no way to DELETE a
// field the use case template already set (no RFC 7396 null-means-
// remove semantics). Every EdgeAgent patch entry's dotted path is
// walked/created inside the relevant module's own raw JSON shape via an
// internal deep-merge helper; every EdgeHub entry is added/overwritten
// wholesale (routes are atomic — a route's wire value is one string, so
// there is nothing to merge partially). The merged result is re-encoded
// through manifesttemplate.DeploymentManifestCodec, so a patch that
// produces an invalid manifest fails at Merge time, not silently.
//
// Merge's raw map[string]any traversal reads/writes
// manifesttemplate.ModulesContentKey/EdgeAgentKey/EdgeHubKey (see
// manifesttemplate/keys.go, the single source of truth for these) —
// never re-hardcoded literal strings — so a rename of any of those wire
// keys only ever needs to happen in ONE place.
//
// Algorithm note (deepMerge/buildNestedPatch, in merge.go): a dotted
// EdgeAgent patch path is turned into a chain of single-key maps
// (buildNestedPatch) and then recursively merged against the module's
// existing raw JSON shape (deepMerge) — recursion only continues where
// BOTH sides are already map[string]any; anywhere that isn't true, the
// patch value replaces the base value outright. Two consequences of
// that single rule, both exercised by merge_test.go: (1) a BARE
// module-name key with no existing base module (base is effectively
// absent) makes the WHOLE patch value become the new module —
// introducing a module the template never declared; (2)
// "settings.createOptions" — a STRING on the wire, never a map —
// can only ever be replaced wholesale by a patch, never recursed into,
// because deepMerge's "both must be maps" check fails the moment either
// side isn't a map. Both behaviors fall out of the SAME one-rule
// algorithm, not special-cased field-by-field logic.
package finaldeviceconfig
