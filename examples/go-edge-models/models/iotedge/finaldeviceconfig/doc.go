// Package finaldeviceconfig holds Merge — the DERIVED operation that
// layers a use case's iothub.LayeredDeployment and one device's own
// deviceconfig.Patch onto the GLOBAL iothub.BaseDeployment, producing
// the FINAL, deployable-to-IoT-Hub config for that device: "baseline +
// template + device config, layered on top" — the priority-0 base
// deployment every device shares, with the use case's own modules/
// routes/system-module overrides layered in, and that ONE device's own
// patch layered on top of THAT.
//
// This is a SEPARATE package from azure/iothub AND deviceconfig (not a
// file inside either of them) because Merge depends on both wire
// formats at once — a dependency shape neither may take on itself
// (they must stay independently reusable, with zero knowledge of one
// another; azure/iothub never imports deviceconfig or this package;
// deviceconfig never imports azure/iothub beyond a few field-level
// codecs it reuses for Patch's own wire shape). Mirrors
// models/iotedge/modulepatch's own "derived, not wire" positioning one
// level up — modulepatch depends only on azure/iothub; this package
// depends on azure/iothub AND deviceconfig.
//
// Merge is OVERWRITE/ADD ONLY — a patch value always either creates a
// new key or replaces an existing one; there is no way to DELETE a
// field a lower layer already set (no RFC 7396 null-means-remove
// semantics). Three buckets, each merged the same two-step way (see
// merge.go's own doc comment for the full breakdown): a Go-level map
// UNION for baseline+template (template wins on name collision, since
// both sides are already the same typed Go value), then
// codex.ApplyDottedPatch for the device patch (which needs the raw,
// opaque JSON shape since a patch entry may reach arbitrarily deep) —
// Modules, SystemModules, and Routes (routes are atomic — a whole-route
// add/override, no dotted-path reach needed). schemaVersion/runtime/
// storeAndForwardConfiguration are BASELINE-ONLY and pass through
// unchanged. The merged result is re-encoded/re-decoded through
// iothub.BaseDeploymentCodec, so a merge that produces an invalid manifest
// fails at Merge time, not silently.
package finaldeviceconfig
