// Resources
// - examples/flat-key-patch -> demonstrates dotted-key JSON patching with go-codex
package main

import (
	_ "embed"
	"fmt"
	"log"
	"os"
	"sort"

	"github.com/DaniDeer/go-codex/examples/go-edge-models/iotedge"
	"github.com/DaniDeer/go-codex/examples/go-edge-models/iotedge/modulepatch"
	"github.com/DaniDeer/go-codex/format"
	"github.com/DaniDeer/go-codex/ports"
)

//go:embed examples/usecase1.json
var usecase1JSON []byte

func main() {
	// Decode a REAL IoT-Edge deployment manifest end-to-end:
	// modulesContent -> $edgeAgent -> {<dotted module key>: ModuleConfig, ...}
	//
	// iotedge.ModuleNameCodec strips iotedge.ModuleKeyPrefix from each
	// dotted key and validates the remaining segment via validate.Slug,
	// producing a map[ModuleName]ModuleConfig — this is the codex.Map[K,V]
	// key-extraction pattern this example demonstrates, mirroring
	// examples/flat-key-patch's containerKeyCodec/containersCodec section
	// but producing a MAP (Modules) instead of a merged []Container slice
	// (via codex.EntrySlice).
	jManifest := format.JSON(iotedge.DeploymentManifestCodec)
	manifest, err := jManifest.Unmarshal(usecase1JSON)
	if err != nil {
		log.Fatal(err)
	}

	modules := manifest.ModulesContent.EdgeAgent
	names := make([]iotedge.ModuleName, 0, len(modules))
	for name := range modules {
		names = append(names, name)
	}
	sort.Slice(names, func(i, j int) bool { return names[i] < names[j] })

	fmt.Printf("=== %d modules extracted via ModuleNameCodec (codex.Map[ModuleName, ModuleConfig]) ===\n", len(names))
	for _, name := range names {
		m := modules[name]
		fmt.Printf("  %-32s image=%-45s type=%-8s status=%-8s restartPolicy=%s\n",
			name, m.Settings.Image, m.Type, m.Status, m.RestartPolicy)
	}

	// ── CreateOptions detail for one module with a rich create-options doc ──
	multiAccess := modules["cv-writer-multi-access-service"]
	fmt.Println("\n=== cv-writer-multi-access-service: CreateOptions detail ===")
	fmt.Println("ExposedPorts:", multiAccess.Settings.CreateOptions.ExposedPorts)
	fmt.Printf("HostConfig.Binds: %+v\n", multiAccess.Settings.CreateOptions.HostConfig.Binds)
	fmt.Printf("HostConfig.PortBindings: %+v\n", multiAccess.Settings.CreateOptions.HostConfig.PortBindings)

	// ── EnvVarValue's 3-way string/int/float union in action ───────────────
	// cv-writer-web's AUTO_REFRESH_INTERVAL is a bare JSON number (15000),
	// while most other env vars are JSON strings — EnvVarValueCodec (via
	// codex.UntaggedUnion, tried string-then-int-then-float) dispatches each
	// to the correct branch automatically.
	web := modules["cv-writer-web"]
	fmt.Println("\n=== cv-writer-web: env var value union (string vs number) ===")
	for name, ev := range web.Env {
		switch {
		case ev.Value.StringValue != nil:
			fmt.Printf("  %-16s StringValue=%q\n", name, *ev.Value.StringValue)
		case ev.Value.IntValue != nil:
			fmt.Printf("  %-16s IntValue=%d\n", name, *ev.Value.IntValue)
		case ev.Value.FloatValue != nil:
			fmt.Printf("  %-16s FloatValue=%v\n", name, *ev.Value.FloatValue)
		}
	}

	// ── cv-writer-kvrocks has NO "env" key at all (OptionalField) ───────────
	kvrocks := modules["cv-writer-kvrocks"]
	fmt.Printf("\ncv-writer-kvrocks.Env (no \"env\" key on the wire): %v (len=%d)\n", kvrocks.Env, len(kvrocks.Env))

	// ── cv-writer-metrics has createOptions:"" (empty string) ───────────────
	metrics := modules["cv-writer-metrics"]
	fmt.Printf("cv-writer-metrics.Settings.CreateOptions (createOptions was \"\" on the wire): %+v\n", metrics.Settings.CreateOptions)

	// ── Round trip: re-encode the entire manifest ───────────────────────────
	reEncoded, err := jManifest.Marshal(manifest)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("\nre-encoded manifest: %d bytes (original: %d bytes)\n", len(reEncoded), len(usecase1JSON))

	// ── ModulePatch: patch one module's image via ports.File + PatchEncoded ──
	//
	// modulepatch.ModulePatchCodec (iotedge/modulepatch) encodes a flat
	// ModulePatch{ModuleName, ImageURL} into the manifest's full nested wire
	// shape (modulesContent -> $edgeAgent -> <dotted key> -> settings ->
	// image) — reusing iotedge.ModuleNameCodec and iotedge.ImageCodec
	// directly, no new constraints. ports.PatchEncoded deep-merges that
	// shape onto the real file on disk, touching ONLY settings.image for
	// the named module — every other field, and every other module, is
	// left untouched. Same pattern as examples/flat-key-patch.
	fmt.Println("\n=== ModulePatch: patch cv-writer-web's image on disk ===")

	dir, err := os.MkdirTemp("", "go-edge-models-patch-*")
	if err != nil {
		log.Fatal(err)
	}
	defer os.RemoveAll(dir)

	manifestPath := dir + "/usecase1.json"
	if err := os.WriteFile(manifestPath, usecase1JSON, 0o644); err != nil {
		log.Fatal(err)
	}
	manifestFile := ports.NewFile(manifestPath, jManifest)

	before, err := manifestFile.Read(nil, ports.FileOptions{})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("before: cv-writer-web image=%q\n", before.ModulesContent.EdgeAgent["cv-writer-web"].Settings.Image)

	patch := modulepatch.ModulePatch{ModuleName: "cv-writer-web", ImageURL: "ghcr.io/bosch-cc-mfd/edge-curve-viewer-web:2.0.0"}
	if err := ports.PatchEncoded(manifestFile, nil, modulepatch.ModulePatchCodec, patch, ports.FileOptions{}); err != nil {
		log.Fatal(err)
	}

	after, err := manifestFile.Read(nil, ports.FileOptions{})
	if err != nil {
		log.Fatal(err)
	}
	patchedWeb := after.ModulesContent.EdgeAgent["cv-writer-web"]
	fmt.Printf("after:  cv-writer-web image=%q\n", patchedWeb.Settings.Image)
	fmt.Printf("        cv-writer-web env still has %d entries (untouched): %v\n", len(patchedWeb.Env), patchedWeb.Env != nil)

	// Confirm every OTHER module is byte-for-byte unaffected by the patch.
	unaffected := true
	for name, m := range modules {
		if name == "cv-writer-web" {
			continue
		}
		if after.ModulesContent.EdgeAgent[name].Settings.Image != m.Settings.Image {
			unaffected = false
			break
		}
	}
	fmt.Printf("        all %d other modules unaffected: %v\n", len(modules)-1, unaffected)
}
