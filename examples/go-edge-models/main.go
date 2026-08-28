// Also demonstrates: spec generation (iothub.LayeredDeploymentCodec.Schema
// rendered as OpenAPI components/schemas YAML via render/openapi.MarshalYAML),
// observer integration (the codec-only stats.ReportErrors path for iotedge
// manifest decoding, and registryapp.WithObserver for docker/registry's HTTP
// calls), and modulesummary/updatemoduleimage resolving a module declared
// ONLY in the GLOBAL baseline.json (never in a use case's own template) —
// for both a regular module and a system module (edgeAgent) — bridged as
// live MCP tools the same way registry.GetTagsTool/GetImageMetadataTool are.
//
// Resources
// - examples/flat-key-patch -> demonstrates dotted-key JSON patching with go-codex
// - examples/formats -> demonstrates schema.Schema -> OpenAPI YAML rendering
// - examples/stats-observer -> demonstrates the codec-only observability path
package main

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"sort"
	"strings"
	"time"

	mcpmsg "github.com/mark3labs/mcp-go/mcp"
	mcpgoserver "github.com/mark3labs/mcp-go/server"

	mcpgo "github.com/DaniDeer/go-codex/adapters/mcpgo"
	mcprest "github.com/DaniDeer/go-codex/adapters/mcprest"
	nethttp "github.com/DaniDeer/go-codex/adapters/nethttp"
	"github.com/DaniDeer/go-codex/api/mcp"
	"github.com/DaniDeer/go-codex/codex"
	iotedgeapp "github.com/DaniDeer/go-codex/examples/go-edge-models/app/iotedge"
	registryapp "github.com/DaniDeer/go-codex/examples/go-edge-models/app/registry"
	iothub "github.com/DaniDeer/go-codex/examples/go-edge-models/models/azure/iothub"
	"github.com/DaniDeer/go-codex/examples/go-edge-models/models/docker"
	"github.com/DaniDeer/go-codex/examples/go-edge-models/models/docker/registry"
	"github.com/DaniDeer/go-codex/examples/go-edge-models/models/iotedge/modulepatch"
	"github.com/DaniDeer/go-codex/examples/go-edge-models/models/iotedge/modulesummary"
	"github.com/DaniDeer/go-codex/examples/go-edge-models/models/iotedge/updatemoduleimage"
	"github.com/DaniDeer/go-codex/examples/go-edge-models/models/iotedge/usecase"
	"github.com/DaniDeer/go-codex/format"
	"github.com/DaniDeer/go-codex/middleware"
	"github.com/DaniDeer/go-codex/ports"
	"github.com/DaniDeer/go-codex/render/openapi"
	"github.com/DaniDeer/go-codex/route"
	"github.com/DaniDeer/go-codex/schema"
	"github.com/DaniDeer/go-codex/stats"
	gstream "github.com/DaniDeer/go-codex/stream"
	"github.com/DaniDeer/go-codex/validate"
)

//go:embed examples/usecases/usecase1.json
var usecase1JSON []byte

//go:embed examples/devices/usecase1/sensor-1.json
var sensor1JSON []byte

//go:embed examples/docker-compose/docker-compose.yml
var factoryComposeYAML []byte

// baselineJSON is the REAL, GLOBAL priority-0 base deployment every
// device shares (schemaVersion/runtime/systemModules/modules) — the
// same "{basePath}/baseline/baseline.json" shape usecase.NewBaselineFile
// wraps (see models/azure/iothub's own doc comment for what a
// BaseDeployment is). Declares TWO common modules
// ("vulnerability-scanner"/"baseline-metrics") deployed to every device
// regardless of use case, demonstrating modules that resolve via
// modulesummary/updatemoduleimage WITHOUT ever being declared in
// usecase1's own template — see runModuleSummaryDemo.
//
//go:embed examples/baseline/baseline.json
var baselineJSON []byte

// logger is used for every fatal error path below via structured slog
// attributes, NOT the stdlib log package — every go-codex error type
// (codex.ValidationErrors, registryapp.RegistryAuthError,
// rest.SecurityCredentialError, ...) implements slog.LogValuer, so passing
// err as a slog attribute (logger.Error("...", "error", err)) automatically
// resolves its LogValue() into structured fields instead of just its
// Error() string — see docs/features/error-handling.md.
var logger = slog.New(slog.NewTextHandler(os.Stdout, nil))

func main() {
	runManifestCodecDemo()

	// basePath mirrors examples/go-edge-models/examples' own mock layout —
	// "baseline/baseline.json" + "usecases/<usecase_name>.json" +
	// "devices/<usecase_name>/<device_id>.json" — copied from the
	// embedded mock files into a writable temp dir (the embed.FS mock
	// tree itself is read-only; the patch demos below need to write).
	dir, err := os.MkdirTemp("", "go-edge-models-patch-*")
	if err != nil {
		logger.Error("create temp dir", "error", err)
		os.Exit(1)
	}
	defer os.RemoveAll(dir)

	// The ONE string->usecase.BasePath conversion this whole example
	// needs — every usecase.*/iotedgeapp.* call below takes basePath
	// directly; dir itself stays around only for the handful of raw
	// os.MkdirAll/os.WriteFile calls (runUseCaseWriteDemo) and the
	// MCP tool-call argument maps (which are wire-level JSON, still a
	// plain string on the wire — decoded into a usecase.BasePath by
	// ReadReqCodec/ReqCodec themselves).
	basePath, err := usecase.NewBasePath(dir)
	if err != nil {
		logger.Error("construct basePath", "error", err)
		os.Exit(1)
	}

	const useCaseName = "usecase1"
	const deviceID = "sensor-1"

	runUseCaseWriteDemo(dir, basePath, useCaseName, deviceID)
	runModulePatchDemo(basePath, useCaseName, deviceID)
	runModuleSummaryDemo(dir, basePath, useCaseName, deviceID)
	runFromComposeDemo(dir, basePath)

	// ── docker/registry: fetch tags + lean manifest metadata for one image ──
	//
	// This section is PURE WIRING — it just spins up two local httptest
	// servers (simulating a registry host + a SEPARATE auth-realm host,
	// exactly like Docker Hub's registry-1.docker.io vs auth.docker.io) and
	// calls docker/registry's exported GetTags/GetImageMetadata. All the
	// actual logic (auth-challenge flow, manifest-list-to-single-platform
	// resolution, lean metadata computation) lives in the docker/registry
	// package itself — reusable independent of this example.
	fmt.Println("\n=== docker/registry: tags + manifest metadata for factory-dashboard ===")
	runRegistryDemo()
}

// runManifestCodecDemo decodes usecase1.json end-to-end and demonstrates
// the codec-level features it exercises — module extraction via
// codex.Map[K,V], CreateOptions detail, the string/int/float env-var
// union, round-tripping, OpenAPI schema generation, and the codec-only
// observer path. Entirely self-contained (no temp dir, no writes).
func runManifestCodecDemo() {
	// Decode a REAL IoT-Edge deployment manifest end-to-end:
	// modulesContent -> $edgeAgent -> {<dotted module key>: ModuleConfig, ...}
	//
	// iothub.ModuleNameCodec strips iothub.ModuleKeyPrefix from each
	// dotted key and validates the remaining segment via validate.Slug,
	// producing a map[ModuleName]ModuleConfig — this is the codex.Map[K,V]
	// key-extraction pattern this example demonstrates, mirroring
	// examples/flat-key-patch's containerKeyCodec/containersCodec section
	// but producing a MAP (Modules) instead of a merged []Container slice
	// (via codex.EntrySlice).
	jManifest := format.JSON(iothub.LayeredDeploymentCodec)
	manifest, err := jManifest.Unmarshal(usecase1JSON)
	if err != nil {
		logger.Error("decode manifest", "error", err)
		os.Exit(1)
	}

	modules := manifest.ModulesContent.EdgeAgent
	names := make([]iothub.ModuleName, 0, len(modules))
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
	edgeProxy := modules["factory-edge-proxy"]
	fmt.Println("\n=== factory-edge-proxy: CreateOptions detail ===")
	fmt.Println("ExposedPorts:", edgeProxy.Settings.CreateOptions.ExposedPorts)
	fmt.Printf("HostConfig.Binds: %+v\n", edgeProxy.Settings.CreateOptions.HostConfig.Binds)
	fmt.Printf("HostConfig.PortBindings: %+v\n", edgeProxy.Settings.CreateOptions.HostConfig.PortBindings)

	// ── EnvVarValue's 3-way string/int/float union in action ───────────────
	// factory-dashboard's AUTO_REFRESH_INTERVAL is a bare JSON integer (15000)
	// and REFRESH_RATE_HZ is a bare JSON fractional number (0.5), while most
	// other env vars are JSON strings — EnvVarValueCodec (via
	// codex.UntaggedUnion, tried string-then-int-then-float) dispatches each
	// to the correct branch automatically.
	dashboard := modules["factory-dashboard"]
	fmt.Println("\n=== factory-dashboard: env var value union (string vs int vs float) ===")
	for name, ev := range dashboard.Env {
		switch {
		case ev.Value.StringValue != nil:
			fmt.Printf("  %-16s StringValue=%q\n", name, *ev.Value.StringValue)
		case ev.Value.IntValue != nil:
			fmt.Printf("  %-16s IntValue=%d\n", name, *ev.Value.IntValue)
		case ev.Value.FloatValue != nil:
			fmt.Printf("  %-16s FloatValue=%v\n", name, *ev.Value.FloatValue)
		}
	}

	// ── factory-cache has NO "env" key at all (OptionalField) ───────────
	cache := modules["factory-cache"]
	fmt.Printf("\nfactory-cache.Env (no \"env\" key on the wire): %v (len=%d)\n", cache.Env, len(cache.Env))

	// ── factory-metrics-collector has createOptions:"" (empty string) ───────────────
	metrics := modules["factory-metrics-collector"]
	fmt.Printf("factory-metrics-collector.Settings.CreateOptions (createOptions was \"\" on the wire): %+v\n", metrics.Settings.CreateOptions)

	// ── Round trip: re-encode the entire manifest ───────────────────────────
	reEncoded, err := jManifest.Marshal(manifest)
	if err != nil {
		logger.Error("re-encode manifest", "error", err)
		os.Exit(1)
	}
	fmt.Printf("\nre-encoded manifest: %d bytes (original: %d bytes)\n", len(reEncoded), len(usecase1JSON))

	// ── Spec generation: iothub.LayeredDeploymentCodec.Schema as OpenAPI YAML ──
	//
	// Every codex.Codec[T] carries a schema.Schema — render/openapi.MarshalYAML
	// turns any map of named schemas into standalone OpenAPI-style
	// components/schemas YAML, with zero api/rest Route/Builder involved.
	// Same pattern as examples/formats' "OpenAPI schema" section.
	fmt.Println("\n=== Spec generation: iothub.LayeredDeploymentCodec (OpenAPI components/schemas) ===")
	specYAML, err := openapi.MarshalYAML(map[string]schema.Schema{
		"LayeredDeployment": iothub.LayeredDeploymentCodec.Schema,
	})
	if err != nil {
		logger.Error("render OpenAPI spec", "error", err)
		os.Exit(1)
	}
	fmt.Println(string(specYAML))

	// ── Observer integration: iotedge (codec-only path, no adapter) ─────────
	//
	// iotedge has no HTTP/MQTT adapter — it's decoded directly via
	// format.JSON(...).Unmarshal, so the codec-only observability path
	// applies (see examples/stats-observer): implement stats.ValidationObserver
	// (one method) and call stats.ReportErrors after each Decode/Unmarshal.
	// The happy-path manifest decoded above reports zero errors; decoding
	// ONE deliberately-invalid manifest (an out-of-enum restartPolicy value)
	// demonstrates RecordValidationError actually firing.
	fmt.Println("\n=== Observer integration: iotedge manifest decode ===")
	manifestObs := &ManifestObserver{}
	_, happyErr := jManifest.Unmarshal(usecase1JSON)
	stats.ReportErrors(manifestObs, "manifest", happyErr)

	invalidJSON := strings.Replace(string(usecase1JSON), `"restartPolicy": "always"`, `"restartPolicy": "sometimes"`, 1)
	_, invalidErr := jManifest.Unmarshal([]byte(invalidJSON))
	stats.ReportErrors(manifestObs, "manifest", invalidErr)

	fmt.Printf("validation errors observed: %d\n", len(manifestObs.errors))
	for _, e := range manifestObs.errors {
		fmt.Printf("  location=%s constraint=%s field=%s\n", e.location, e.constraint, e.field)
	}
}

// runUseCaseWriteDemo copies the embedded mock use case/device files, plus
// the real embedded baseline fixture, into dir — the "usecases/
// <usecase_name>.json" + "devices/<usecase_name>/<device_id>.json" +
// "baseline/baseline.json" layout every other demo function below reads
// from and patches. This is where UpdateModuleImage/PatchUseCaseModule's
// underlying deep-merge (below, in runModulePatchDemo) gets something to
// write to — the embed.FS mock tree itself is read-only.
func runUseCaseWriteDemo(dir string, basePath usecase.BasePath, useCaseName, deviceID string) {
	fmt.Println("\n=== app/iotedge: writing use case + device + baseline mock files to a scratch dir ===")

	usecasesDir := dir + "/usecases"
	if err := os.MkdirAll(usecasesDir, 0o755); err != nil {
		logger.Error("create usecases dir", "error", err)
		os.Exit(1)
	}
	if err := os.WriteFile(usecasesDir+"/"+useCaseName+".json", usecase1JSON, 0o644); err != nil {
		logger.Error("write manifest to disk", "error", err)
		os.Exit(1)
	}
	devicesDir := dir + "/devices/" + useCaseName
	if err := os.MkdirAll(devicesDir, 0o755); err != nil {
		logger.Error("create devices dir", "error", err)
		os.Exit(1)
	}
	if err := os.WriteFile(devicesDir+"/"+deviceID+".json", sensor1JSON, 0o644); err != nil {
		logger.Error("write device manifest to disk", "error", err)
		os.Exit(1)
	}

	// ── baseline/baseline.json: the GLOBAL, priority-0 base deployment
	// every device shares (schemaVersion/runtime/systemModules/modules),
	// applied BEFORE the use case's own modules/routes and a device's
	// own patch — see the models/azure/iothub package's own doc comment.
	// usecase.NewBaselineFile has NO template variables (one fixed path,
	// unlike NewFile/NewDeviceFile). baselineJSON is decoded first
	// (rather than written raw like usecase1JSON/sensor1JSON above) to
	// demonstrate NewBaselineFile's typed ports.File[iothub.BaseDeployment]
	// Write path specifically.
	baseline, err := format.JSON(iothub.BaseDeploymentCodec).Unmarshal(baselineJSON)
	if err != nil {
		logger.Error("decode baseline.json", "error", err)
		os.Exit(1)
	}
	if _, err := usecase.NewBaselineFile(basePath).Write(nil, baseline, ports.FileOptions{CreateDirs: true}); err != nil {
		logger.Error("write baseline.json to disk", "error", err)
		os.Exit(1)
	}
}

// runModulePatchDemo demonstrates the ports.Dir discovery, usecase.Read/
// ListDeviceIDs/ReadDeviceConfig composition, DeviceConfig.Merge (three-
// way baseline+template+device layering), and the app/iotedge template-
// and device-scoped patch functions — all against the mock files
// runUseCaseWriteDemo wrote to dir.
func runModulePatchDemo(basePath usecase.BasePath, useCaseName usecase.Name, deviceID usecase.DeviceID) {
	usecasesDir := string(basePath) + "/usecases"

	// ── ports.Dir: discover which iotedge "use case" config files exist in
	// a directory — a declarative `ls`, not `cat`. Each file in the config
	// directory represents one use case, and the filename (minus ".json")
	// IS that use case's name; usecase.DirEntryPattern extracts it,
	// validated the same way usecase.NewFile validates its own path
	// variables. List's result feeds directly into NewFile below —
	// discover, then read, without hand-rolled os.ReadDir/filepath.Glob code.
	fmt.Println("\n=== ports.Dir: list iotedge config files (use cases) in a directory ===")

	useCaseDir := usecase.NewDir(usecasesDir)
	useCaseEntries, err := useCaseDir.List(nil, ports.DirOptions{})
	if err != nil {
		logger.Error("list config directory", "error", err)
		os.Exit(1)
	}
	for _, e := range useCaseEntries {
		fmt.Printf("found use case %q (file %q, kind=%s)\n", e.Vars["useCase"], e.Name, e.Kind)
	}

	// ── usecase.ListDeviceIDs / ReadDeviceConfig / Read: the
	// "devices/{usecase_name}/{device_id}.json" side of the same
	// templated-file story ───────────────────────────────────────────────
	//
	// DeploymentManifest/DeviceManifest stay PURE wire/file content — no
	// usecase_name/device_id field on either — usecase.UseCase/DeviceConfig
	// are the DOMAIN composition structs that pair each with its identity
	// (from the path, not the body). Read combines NewFile + ListDeviceIDs
	// + ReadDeviceConfig into ONE call.
	fmt.Println("\n=== usecase.ListDeviceIDs / Read: usecase_name + device_id, composed ===")

	deviceIDs, err := usecase.ListDeviceIDs(basePath, useCaseName, ports.DirOptions{})
	if err != nil {
		logger.Error("list device ids", "error", err)
		os.Exit(1)
	}
	fmt.Printf("device_ids for use case %q: %v\n", useCaseName, deviceIDs)

	deviceCfg, err := usecase.ReadDeviceConfig(basePath, useCaseName, deviceID, ports.FileOptions{})
	if err != nil {
		logger.Error("read device config", "error", err)
		os.Exit(1)
	}
	fmt.Printf("device %q: patches %d $edgeAgent key(s), %d $edgeHub route(s)\n",
		deviceCfg.DeviceID, len(deviceCfg.Patch.EdgeAgent), len(deviceCfg.Patch.EdgeHub))

	useCase, err := usecase.Read(basePath, useCaseName, ports.FileOptions{})
	if err != nil {
		logger.Error("read use case", "error", err)
		os.Exit(1)
	}
	fmt.Printf("usecase.Read(%q): %d module(s), %d route(s), %d device(s)\n",
		useCase.Name, len(useCase.DeploymentManifest.ModulesContent.EdgeAgent),
		len(useCase.DeploymentManifest.ModulesContent.EdgeHub), len(useCase.Devices))

	// ── deviceCfg.Merge: "template + device config, layered on top" ─────
	//
	// The use case's OWN template still has factory-mqtt-gateway-1's
	// BROKER_URL set to the placeholder "ToDo: Override value in device
	// layers." — sensor-1's device config patches exactly that field
	// (plus adds a device-specific $edgeHub route). Merge produces the
	// FINAL, deployable manifest for this ONE device: the placeholder is
	// gone, replaced by the device's own value, and everything else the
	// template declared survives untouched.
	fmt.Println("\n=== DeviceConfig.Merge: template + device config, layered ===")

	baseline, err := usecase.NewBaselineFile(basePath).Read(nil, ports.FileOptions{})
	if err != nil {
		logger.Error("read baseline for merge", "error", err)
		os.Exit(1)
	}
	merged, err := deviceCfg.Merge(baseline, useCase.DeploymentManifest)
	if err != nil {
		logger.Error("merge device config onto template", "error", err)
		os.Exit(1)
	}
	gw := merged.ModulesContent.EdgeAgent.Modules["factory-mqtt-gateway-1"]
	fmt.Printf("factory-mqtt-gateway-1 BROKER_URL after merge: %q\n", *gw.Env["BROKER_URL"].Value.StringValue)
	fmt.Printf("factory-mqtt-gateway-1 status still %q (untouched by this device's patch)\n", gw.Status)
	fmt.Printf("merged route count: %d (template's %d + device's %d)\n",
		len(merged.ModulesContent.EdgeHub.Routes), len(useCase.DeploymentManifest.ModulesContent.EdgeHub), len(deviceCfg.Patch.EdgeHub))

	// ── app/iotedge.UpdateDeviceModuleImage: an ISOLATED, device-scoped
	// image update ────────────────────────────────────────────────────
	//
	// Unlike UpdateUseCaseModuleImage (which patches the shared TEMPLATE
	// — demonstrated earlier for factory-dashboard), UpdateDeviceModuleImage
	// writes into sensor-1's OWN config file only: the template and every
	// other device stay completely untouched. Reuses
	// modulepatch.FieldsBodyCodec internally to bridge the SAME typed
	// image validation modulepatch already provides at the template
	// level down to this device-scoped write.
	fmt.Println("\n=== app/iotedge.UpdateDeviceModuleImage: isolated, device-scoped image update ===")

	deviceOnlyImage := docker.Image{Name: "ghcr.io/example-org/factory-cache", Tag: "3.0.0-sensor1-only"}
	if err := iotedgeapp.UpdateDeviceModuleImage(basePath, useCaseName, deviceID, "factory-cache", deviceOnlyImage, ports.FileOptions{}); err != nil {
		logger.Error("update device module image", "error", err)
		os.Exit(1)
	}

	deviceEffective, err := usecase.ReadEffective(basePath, useCaseName, deviceID, ports.FileOptions{})
	if err != nil {
		logger.Error("read effective device config", "error", err)
		os.Exit(1)
	}
	fmt.Printf("sensor-1 EFFECTIVE factory-cache image: %q\n", deviceEffective.ModulesContent.EdgeAgent.Modules["factory-cache"].Settings.Image.String())

	templateAfterDeviceUpdate, err := iotedgeapp.ReadUseCase(basePath, useCaseName, ports.FileOptions{})
	if err != nil {
		logger.Error("read use case after device update", "error", err)
		os.Exit(1)
	}
	fmt.Printf("TEMPLATE factory-cache image still: %q (untouched by the device-scoped update)\n",
		templateAfterDeviceUpdate.ModulesContent.EdgeAgent["factory-cache"].Settings.Image.String())

	before, err := iotedgeapp.ReadUseCase(basePath, useCaseName, ports.FileOptions{})
	if err != nil {
		logger.Error("read manifest before patch", "error", err)
		os.Exit(1)
	}
	fmt.Printf("before: factory-dashboard image=%q\n", before.ModulesContent.EdgeAgent["factory-dashboard"].Settings.Image)

	newImage := docker.Image{Name: "ghcr.io/example-org/factory-dashboard", Tag: "2.0.0"}
	if err := iotedgeapp.UpdateUseCaseModuleImage(basePath, useCaseName, "factory-dashboard", newImage, ports.FileOptions{}); err != nil {
		logger.Error("update module image", "error", err)
		os.Exit(1)
	}

	afterImage, err := iotedgeapp.ReadUseCase(basePath, useCaseName, ports.FileOptions{})
	if err != nil {
		logger.Error("read manifest after image update", "error", err)
		os.Exit(1)
	}
	patchedWeb := afterImage.ModulesContent.EdgeAgent["factory-dashboard"]
	fmt.Printf("after:  factory-dashboard image=%q\n", patchedWeb.Settings.Image)
	fmt.Printf("        factory-dashboard env still has %d entries (untouched): %v\n", len(patchedWeb.Env), patchedWeb.Env != nil)

	// ── app/iotedge: patch MULTIPLE fields at once via the general
	// PatchUseCaseModule + modulepatch.FieldsPatch ─────────────────
	//
	// FieldsPatch mirrors ModuleConfig's own field set — every field
	// is independently optional (a pointer, or nil for Env), and
	// FieldsPatchCodec (a HAND-ROLLED codex.Codec, since
	// codex.Struct's Encode always writes every declared field and cannot
	// express "omit if unset") includes ONLY the fields actually set.
	// Here we patch Status AND RestartPolicy together in ONE call, leaving
	// the image we just updated above (and every other field) untouched.
	fmt.Println("\n=== app/iotedge.PatchUseCaseModule: patch status+restartPolicy together ===")

	newStatus := iothub.Status("stopped")
	newRestartPolicy := iothub.RestartPolicy("on-failure")
	fieldsPatch := modulepatch.FieldsPatch{
		ModuleName:    "factory-dashboard",
		Status:        &newStatus,
		RestartPolicy: &newRestartPolicy,
	}
	if err := iotedgeapp.PatchUseCaseModule(basePath, useCaseName, fieldsPatch, ports.FileOptions{}); err != nil {
		logger.Error("patch module fields", "error", err)
		os.Exit(1)
	}

	after, err := iotedgeapp.ReadUseCase(basePath, useCaseName, ports.FileOptions{})
	if err != nil {
		logger.Error("read manifest after fields patch", "error", err)
		os.Exit(1)
	}
	dashboardFinal := after.ModulesContent.EdgeAgent["factory-dashboard"]
	fmt.Printf("after:  factory-dashboard status=%q restartPolicy=%q\n", dashboardFinal.Status, dashboardFinal.RestartPolicy)
	fmt.Printf("        factory-dashboard image still %q (untouched by this patch): %v\n",
		dashboardFinal.Settings.Image, dashboardFinal.Settings.Image.String() == newImage.String())

	// Confirm every OTHER module is byte-for-byte unaffected by BOTH
	// patches — useCase.DeploymentManifest (read further above, BEFORE
	// either patch) is the "before" snapshot to compare against.
	origModules := useCase.DeploymentManifest.ModulesContent.EdgeAgent
	unaffected := true
	for name, m := range origModules {
		if name == "factory-dashboard" {
			continue
		}
		if after.ModulesContent.EdgeAgent[name].Settings.Image != m.Settings.Image {
			unaffected = false
			break
		}
	}
	fmt.Printf("        all %d other modules unaffected: %v\n", len(origModules)-1, unaffected)
}

// runModuleSummaryDemo demonstrates modulesummary/updatemoduleimage's
// "resolves via ANY layer" story: "vulnerability-scanner" is declared
// ONLY in baseline/baseline.json, never in usecase1's own template, yet
// modulesummary.NewSummary resolves it through usecase.ReadEffective
// exactly like a template-declared module would. Also demonstrates the
// SAME resolution + update path for a SYSTEM module (edgeAgent), and
// bridges both modulesummary.ReadTool and updatemoduleimage.Tool as live
// MCP tools — the SAME "declare once, register anywhere" pattern
// registry.GetTagsTool/GetImageMetadataTool demonstrate below, since
// both are ALREADY native MCP tool contracts (no adapters/mcprest
// bridge needed, unlike GetTagsRoute — a plain REST route — above).
func runModuleSummaryDemo(dir string, basePath usecase.BasePath, useCaseName usecase.Name, deviceID usecase.DeviceID) {
	opts := ports.FileOptions{}

	fmt.Println("\n=== modulesummary: a baseline-only module resolves without ever being in the template ===")

	effective, err := usecase.ReadEffective(basePath, useCaseName, deviceID, opts)
	if err != nil {
		logger.Error("read effective config for module summary", "error", err)
		os.Exit(1)
	}

	scanner := modulesummary.NewSummary(effective.ModulesContent.EdgeAgent.Modules["vulnerability-scanner"])
	fmt.Printf("vulnerability-scanner (baseline-only, absent from usecase1's own template): image=%s status=%v restartPolicy=%v\n",
		scanner.Image, *scanner.Status, *scanner.RestartPolicy)

	// ── system module (edgeAgent) summary + image update ─────────────────
	//
	// edgeAgent/edgeHub are the two RESERVED system modules every
	// BaseDeployment declares (see iothub.SystemModules) — a genuinely
	// separate wire bucket from regular modules, but modulesummary
	// resolves/summarizes both through the SAME Summary shape via
	// NewSummaryFromSystemModule + SystemModuleConfigFor.
	fmt.Println("\n=== modulesummary: system module (edgeAgent) summary + image update ===")

	edgeAgentCfg, ok := modulesummary.SystemModuleConfigFor(effective.ModulesContent.EdgeAgent.SystemModules, "edgeAgent")
	if !ok {
		logger.Error("edgeAgent system module not found", "error", nil)
		os.Exit(1)
	}
	fmt.Printf("edgeAgent BEFORE update: image=%s\n", modulesummary.NewSummaryFromSystemModule(edgeAgentCfg).Image)

	newEdgeAgentImage := docker.Image{Name: "mcr.microsoft.com/azureiotedge-agent", Tag: "1.6.0"}
	if err := iotedgeapp.UpdateUseCaseSystemModuleImage(basePath, useCaseName, "edgeAgent", newEdgeAgentImage, opts); err != nil {
		logger.Error("update edgeAgent system module image", "error", err)
		os.Exit(1)
	}

	effectiveAfter, err := usecase.ReadEffective(basePath, useCaseName, deviceID, opts)
	if err != nil {
		logger.Error("re-read effective config after system module update", "error", err)
		os.Exit(1)
	}
	edgeAgentCfgAfter, _ := modulesummary.SystemModuleConfigFor(effectiveAfter.ModulesContent.EdgeAgent.SystemModules, "edgeAgent")
	fmt.Printf("edgeAgent AFTER update:  image=%s\n", modulesummary.NewSummaryFromSystemModule(edgeAgentCfgAfter).Image)

	// ── Bridge modulesummary.ReadTool / updatemoduleimage.Tool as MCP tools ──
	fmt.Println("\n=== MCP bridge: modulesummary.ReadTool/updatemoduleimage.Tool (baseline-only + system module) ===")

	moduleSummaryToolsBuilder := mcp.NewBuilder(mcp.Info{Name: "go-edge-models module summary MCP tools", Version: "1.0.0"})

	readSummaryHandle, err := modulesummary.ReadTool.Register(moduleSummaryToolsBuilder)
	if err != nil {
		logger.Error("register read_module_summary tool", "error", err)
		os.Exit(1)
	}
	_, readSummaryHandlerFn := mcpgo.ToolHandler(readSummaryHandle,
		iotedgeapp.NewReadModuleSummaryToolHandler(opts),
		mcpgo.Options{},
	)
	callMCPTool(readSummaryHandlerFn, "read_module_summary(vulnerability-scanner)", map[string]any{
		"basePath": dir, "useCaseName": string(useCaseName), "moduleName": "vulnerability-scanner",
	})
	callMCPTool(readSummaryHandlerFn, "read_module_summary(edgeAgent)", map[string]any{
		"basePath": dir, "useCaseName": string(useCaseName), "moduleName": "edgeAgent",
	})

	updateImageHandle, err := updatemoduleimage.Tool.Register(moduleSummaryToolsBuilder)
	if err != nil {
		logger.Error("register update_module_image tool", "error", err)
		os.Exit(1)
	}
	_, updateImageHandlerFn := mcpgo.ToolHandler(updateImageHandle,
		iotedgeapp.NewUpdateModuleImageToolHandler(opts),
		mcpgo.Options{},
	)
	callMCPTool(updateImageHandlerFn, "update_module_image(edgeAgent, 1.6.1)", map[string]any{
		"basePath": dir, "useCaseName": string(useCaseName), "moduleName": "edgeAgent",
		"imageURL": "mcr.microsoft.com/azureiotedge-agent:1.6.1",
	})
}

// runFromComposeDemo demonstrates the iotedge/fromcompose package's
// bidirectional Docker Compose ↔ IoT Edge conversion, end to end:
//
//  1. Import: iotedgeapp.ImportDockerComposeAsUseCase reads the embedded
//     5-service factory-edge docker-compose.yml, converts it into a
//     scaffold iothub.LayeredDeployment via fromcompose.ConvertProject,
//     and writes it as a NEW use case ("factory-stack") — reusing
//     usecase.NewFile's existing templated file port, zero new
//     file-layout code (see ImportDockerComposeAsUseCase's own doc
//     comment). Every fromcompose.Warning is printed: the compose file
//     deliberately declares THREE services with restart: unless-stopped,
//     which has no exact IoT Edge equivalent (see fromcompose's own
//     README's "scaffold, not full fidelity" contract) — each produces
//     an approximated-restart-policy warning.
//  2. Read back: the just-written use case is read via ReadUseCase,
//     proving the conversion produced a genuinely USABLE deployment —
//     prints the resolved module count and each module's image.
//  3. Export (reverse direction): ExportUseCaseAsDockerCompose converts
//     the SAME use case back into a Compose YAML file, closing the loop.
//     Reverse warnings are printed too (e.g. a placeholder image
//     reversing to a bare `build: true`, if any module lacked an image).
func runFromComposeDemo(dir string, basePath usecase.BasePath) {
	fmt.Println("\n=== iotedge/fromcompose: import + read back + export (bidirectional) ===")

	factoryUseCaseName, err := usecase.NewName("factory-stack")
	if err != nil {
		logger.Error("construct factory-stack use case name", "error", err)
		os.Exit(1)
	}

	composeFilePath := dir + "/factory-docker-compose.yml"
	if err := os.WriteFile(composeFilePath, factoryComposeYAML, 0o644); err != nil {
		logger.Error("write embedded docker-compose.yml to disk", "error", err)
		os.Exit(1)
	}

	opts := ports.FileOptions{CreateDirs: true}

	importWarnings, err := iotedgeapp.ImportDockerComposeAsUseCase(basePath, factoryUseCaseName, composeFilePath, opts)
	if err != nil {
		logger.Error("import docker-compose.yml as use case", "error", err)
		os.Exit(1)
	}
	fmt.Printf("imported %q from %s — %d warning(s):\n", factoryUseCaseName, composeFilePath, len(importWarnings))
	for _, w := range importWarnings {
		fmt.Printf("  - %s: %s\n", w.Kind, w.Message)
	}

	deployment, err := iotedgeapp.ReadUseCase(basePath, factoryUseCaseName, opts)
	if err != nil {
		logger.Error("read back imported use case", "error", err)
		os.Exit(1)
	}
	fmt.Printf("read back %d module(s):\n", len(deployment.ModulesContent.EdgeAgent))
	for name, mod := range deployment.ModulesContent.EdgeAgent {
		fmt.Printf("  - %s: image=%s restartPolicy=%s\n", name, mod.Settings.Image, mod.RestartPolicy)
	}

	exportedPath := dir + "/factory-docker-compose.exported.yml"
	exportWarnings, err := iotedgeapp.ExportUseCaseAsDockerCompose(basePath, factoryUseCaseName, exportedPath, opts)
	if err != nil {
		logger.Error("export use case back to docker-compose.yml", "error", err)
		os.Exit(1)
	}
	fmt.Printf("exported %q to %s — %d warning(s):\n", factoryUseCaseName, exportedPath, len(exportWarnings))
	for _, w := range exportWarnings {
		fmt.Printf("  - %s: %s\n", w.Kind, w.Message)
	}
}

// runRegistryDemo wires two local httptest servers together (a registry
// host and a separate auth-realm host) and calls registryapp.GetTags /
// registryapp.GetImageMetadata against a synthetic multi-arch image — the
// image name reuses "factory-dashboard" for narrative continuity with the rest
// of this example. See docker/registry's own package doc for the reusable
// client API this demonstrates.
func runRegistryDemo() {
	const fakeToken = "demo-token"

	authSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("service") == "" || r.URL.Query().Get("scope") == "" {
			http.Error(w, "missing service/scope", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"token": fakeToken})
	}))
	defer authSrv.Close()

	// This mock plays the role of a REAL registry/auth server — docker/registry
	// is a client only, so it has no exported helpers for constructing these
	// values (that machinery is a private implementation detail of its own
	// auth flow). Building the WWW-Authenticate challenge, scope, and
	// Authorization value here is plain string formatting per the Docker
	// Distribution / RFC 6750 wire format a real registry server would emit.
	scope := "repository:factory-dashboard:pull"
	challenge := fmt.Sprintf(`Bearer realm=%q,service=%q,scope=%q`, authSrv.URL+"/token", "demo-registry", scope)
	bearerAuth := "Bearer " + fakeToken

	mux := http.NewServeMux()
	mux.HandleFunc("/v2/", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != bearerAuth {
			w.Header().Set("WWW-Authenticate", challenge)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/v2/factory-dashboard/tags/list", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"name": "factory-dashboard",
			"tags": []string{"1.8.16.1", "1.8.15.0", "latest"},
		})
	})
	// Explicit 404 for any OTHER repository's tags — more specific than the
	// "/v2/" prefix pattern above, so ServeMux prefers this wildcard match
	// instead of silently falling through to the auth ping handler (which
	// would otherwise return a confusing 200-with-empty-body for an unknown
	// image). Used by runMCPBridgeDemo to demonstrate a real
	// nethttp.UnexpectedStatusError reaching mcprest.DefaultErrorPatterns.
	mux.HandleFunc("/v2/{name}/tags/list", func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	})
	mux.HandleFunc("/v2/factory-dashboard/manifests/", func(w http.ResponseWriter, r *http.Request) {
		reference := strings.TrimPrefix(r.URL.Path, "/v2/factory-dashboard/manifests/")
		w.Header().Set("Content-Type", "application/json")
		switch reference {
		case "latest":
			// A multi-arch manifest list — GetImageMetadata resolves this
			// transparently to the linux/amd64 entry below.
			w.Header().Set("Docker-Content-Digest", "sha256:"+strings.Repeat("11", 32))
			_ = json.NewEncoder(w).Encode(map[string]any{
				"schemaVersion": 2,
				"mediaType":     "application/vnd.docker.distribution.manifest.list.v2+json",
				"manifests": []map[string]any{
					{
						"mediaType": "application/vnd.docker.distribution.manifest.v2+json",
						"digest":    "sha256:f6f6f6f6f6f6f6f6f6f6f6f6f6f6f6f6f6f6f6f6f6f6f6f6f6f6f6f6f6f6f6f6",
						"size":      1234,
						"platform":  map[string]any{"architecture": "amd64", "os": "linux"},
					},
					{
						"mediaType": "application/vnd.docker.distribution.manifest.v2+json",
						"digest":    "sha256:0707070707070707070707070707070707070707070707070707070707070707",
						"size":      1235,
						"platform":  map[string]any{"architecture": "arm64", "os": "linux"},
					},
				},
			})
		case "sha256:f6f6f6f6f6f6f6f6f6f6f6f6f6f6f6f6f6f6f6f6f6f6f6f6f6f6f6f6f6f6f6f6":
			w.Header().Set("Docker-Content-Digest", "sha256:f6f6f6f6f6f6f6f6f6f6f6f6f6f6f6f6f6f6f6f6f6f6f6f6f6f6f6f6f6f6f6f6")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"schemaVersion": 2,
				"mediaType":     "application/vnd.docker.distribution.manifest.v2+json",
				"config":        map[string]any{"mediaType": "application/vnd.docker.container.image.v1+json", "digest": "sha256:c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3", "size": 5000},
				"layers": []map[string]any{
					{"mediaType": "application/vnd.docker.image.rootfs.diff.tar.gzip", "digest": "sha256:d4d4d4d4d4d4d4d4d4d4d4d4d4d4d4d4d4d4d4d4d4d4d4d4d4d4d4d4d4d4d4d4", "size": 30000000},
					{"mediaType": "application/vnd.docker.image.rootfs.diff.tar.gzip", "digest": "sha256:e5e5e5e5e5e5e5e5e5e5e5e5e5e5e5e5e5e5e5e5e5e5e5e5e5e5e5e5e5e5e5e5", "size": 12000000},
				},
			})
		default:
			http.NotFound(w, r)
		}
	})
	registrySrv := httptest.NewServer(mux)
	defer registrySrv.Close()

	// client.go always dials "https://<registryHost>" — httptest.Server
	// serves plain HTTP, so this Transport rewrites just the scheme.
	httpsToHTTP := &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			req = req.Clone(req.Context())
			if req.URL.Scheme == "https" {
				req.URL.Scheme = "http"
			}
			return http.DefaultTransport.RoundTrip(req)
		}),
	}

	registryHost, err := url.Parse(registrySrv.URL)
	if err != nil {
		logger.Error("parse registry URL", "error", err)
		os.Exit(1)
	}
	imageURL, err := registry.FormatImageRef(registry.ImageRef{
		Registry: registryHost.Host, Repository: "factory-dashboard", Reference: "latest",
	})
	if err != nil {
		logger.Error("format image ref", "error", err)
		os.Exit(1)
	}

	ctx := context.Background()

	// registryapp.WithObserver wires a stats.Observer through every
	// nethttp.CallHandle invocation GetTags/GetImageMetadata make,
	// including the Ping/token-exchange auth flow — the same mechanism
	// demonstrated for iotedge above, applied to an HTTP client instead of
	// a plain codec Decode. This explicit Option isn't strictly required:
	// docker/registry builds on nethttp.Call/CallHandle internally, which
	// ALREADY falls back to stats.ObserverFromContext(ctx) when no
	// Observer is set — `ctx = stats.WithObserver(ctx, regObs)` before
	// these calls would give the exact same result with zero registry
	// package options touched. WithObserver is used here only to
	// demonstrate the explicit per-call override.
	//
	// stats.NewFanout combines the counting RegistryObserver above with a
	// real stats.NewLoggingObserver — one value plugs into every layer, no
	// type assertions needed at the call site (the same composition story
	// docs/guides/observer.md documents for the whole library). Running
	// this example prints a structured slog line for EVERY HTTP call
	// (Ping, token exchange, tags, manifest fetches) via LoggingObserver,
	// alongside the RegistryObserver's own summary below.
	regObs := &RegistryObserver{}
	logObs := stats.NewLoggingObserver(logger)
	obs := stats.NewFanout(regObs, logObs)

	tags, err := registryapp.GetTags(ctx, httpsToHTTP, imageURL, registryapp.WithObserver(obs))
	if err != nil {
		logger.Error("get tags", "error", err)
		os.Exit(1)
	}
	fmt.Printf("tags for %s: %v\n", tags.Name, tags.Tags)

	meta, err := registryapp.GetImageMetadata(ctx, httpsToHTTP, registry.GetImageMetadataReq{ImageURL: imageURL}, registryapp.WithObserver(obs))
	if err != nil {
		logger.Error("get image metadata", "error", err)
		os.Exit(1)
	}
	fmt.Printf("manifest metadata (resolved from a multi-arch list to linux/amd64):\n")
	fmt.Printf("  schemaVersion=%d mediaType=%s\n", meta.SchemaVersion, meta.MediaType)
	fmt.Printf("  image=%s totalSizeBytes=%d\n", meta.Image, meta.TotalSizeBytes)

	fmt.Println("\n=== Observer integration: docker/registry HTTP calls (RegistryObserver summary) ===")
	regObs.Print()

	runMCPBridgeDemo(httpsToHTTP, registryHost.Host, fakeToken)
}

// runMCPBridgeDemo wraps registry.GetTagsRoute as an MCP tool via
// adapters/mcprest — the SAME REST client machinery (path merge fields,
// security scheme) as registryapp.GetTags demonstrated above, now exposed as
// something an LLM agent could call directly. Reuses the running fake
// registry server from runRegistryDemo (client, registryHost, fakeToken).
func runMCPBridgeDemo(client *http.Client, registryHost, fakeToken string) {
	baseURL := "https://" + registryHost

	// A credential-providing middleware.ClientMiddleware for the MCP demo:
	// the real registryapp.GetTags call above goes through the full
	// Ping/challenge/token-exchange dance (auth.go, package-private) —
	// this demo already knows the fake server's expected token, so it
	// supplies it directly. A real integration would plug in its own
	// token source here (a cached OAuth token, an API key, etc.) — the
	// middleware is FIXED for every call made through the returned MCP
	// tool handler, matching every other client-adapter binding in
	// go-codex (see adapters/mcprest's package doc).
	//
	// registry.GetTagsRoute already declares its "bearerAuth" requirement
	// itself (regmodels.BearerAuthDeclaration, attached via .Use(...) —
	// see the route's own doc comment) — credMw only needs to FULFILL it,
	// via .UseClient(...); it carries no Security of its own (a
	// middleware.ClientMiddleware structurally cannot). Declare
	// (GetTagsRoute) → chain (UseClient) → build (ClientHandle) — the
	// same pattern app/registry's GetTags itself uses.
	credMw := middleware.ClientMiddleware{
		Name: "fake-registry-auth",
		Fn: func(context.Context, []route.SecurityRequirement) (http.Header, error) {
			h := make(http.Header)
			h.Set("Authorization", "Bearer "+fakeToken)
			return h, nil
		},
	}
	callOpts := nethttp.CallOptions{}
	restHandle := registry.GetTagsRoute.UseClient(credMw).ClientHandle()

	mcpBuilder := mcp.NewBuilder(mcp.Info{Name: "go-edge-models MCP bridge demo", Version: "1.0.0"})

	// ── ToolHandler: the identity case ──────────────────────────────────────
	//
	// GetTagsRoute's OWN request codec is intentionally EMPTY —
	// registry.GetTagsReq.Name merges into the route's {name} path segment,
	// never the body (see routes.go). For an MCP tool there is no separate
	// path-var mechanism — every input field must flow through the tool's
	// OWN input codec — so this example declares one that DOES include Name.
	fmt.Println("\n=== MCP bridge: docker/registry.GetTagsRoute as an MCP tool (mcprest.ToolHandler) ===")

	mcpGetTagsReqCodec := codex.Struct[registry.GetTagsReq](
		codex.RequiredField("name", codex.String().Refine(validate.NonEmptyString),
			func(r registry.GetTagsReq) string { return r.Name },
			func(r *registry.GetTagsReq, v string) { r.Name = v }),
	)

	getTagsTool, err := mcp.NewTool[registry.GetTagsReq, registry.TagsList](
		"get_tags", mcpGetTagsReqCodec, registry.TagsListCodec,
		mcprest.DefaultErrorPatterns()...,
	).Register(mcpBuilder)
	if err != nil {
		logger.Error("register get_tags MCP tool", "error", err)
		os.Exit(1)
	}
	// credMw is NOT passed here again — restHandle already carries it via
	// .UseClient(credMw) above, and CallHandle (which ToolHandler calls
	// internally) picks it up automatically from restHandle.ClientMiddlewares.
	_, getTagsHandlerFn := mcpgo.ToolHandler(getTagsTool,
		mcprest.ToolHandler(client, baseURL, restHandle, callOpts),
		mcpgo.Options{},
	)

	callMCPTool(getTagsHandlerFn, "get_tags(factory-dashboard)", map[string]any{"name": "factory-dashboard"})
	callMCPTool(getTagsHandlerFn, "get_tags(unknown-image) — triggers DefaultErrorPatterns", map[string]any{"name": "unknown-image"})

	// ── MappedToolHandler: a simplified, LLM-facing shape ───────────────────
	fmt.Println("\n=== MCP bridge: MappedToolHandler with a simplified LLM-facing shape ===")

	searchInputCodec := codex.Struct[searchTagsInput](
		codex.RequiredField("image", codex.String().Refine(validate.NonEmptyString),
			func(in searchTagsInput) string { return in.Image },
			func(in *searchTagsInput, v string) { in.Image = v }),
	)
	searchOutputCodec := codex.Struct[searchTagsOutput](
		codex.RequiredField("tags", codex.SliceOf(codex.String()),
			func(o searchTagsOutput) []string { return o.Tags },
			func(o *searchTagsOutput, v []string) { o.Tags = v }),
	)

	searchTool, err := mcp.NewTool[searchTagsInput, searchTagsOutput](
		"search_tags", searchInputCodec, searchOutputCodec,
		mcprest.DefaultErrorPatterns()...,
	).Register(mcpBuilder)
	if err != nil {
		logger.Error("register search_tags MCP tool", "error", err)
		os.Exit(1)
	}
	_, searchHandlerFn := mcpgo.ToolHandler(searchTool,
		mcprest.MappedToolHandler(client, baseURL, restHandle, callOpts,
			func(in searchTagsInput) (registry.GetTagsReq, error) {
				return registry.GetTagsReq{Name: in.Image}, nil
			},
			func(resp registry.TagsList) (searchTagsOutput, error) {
				tags := make([]string, len(resp.Tags))
				for i, t := range resp.Tags {
					tags[i] = string(t)
				}
				return searchTagsOutput{Tags: tags}, nil
			},
		),
		mcpgo.Options{},
	)
	callMCPTool(searchHandlerFn, "search_tags(factory-dashboard)", map[string]any{"image": "factory-dashboard"})

	// ── ports.ToolPort composition: same handler, bindable to ANY transport ──
	//
	// mcprest.ToolHandler's return value is exactly the
	// func(context.Context, In) (Out, error) shape ports.ToolPort.SetFunc
	// already accepts — no adaptation needed. Once bound to a ToolPort
	// (rather than directly to mcpgo.ToolHandler), the SAME REST-backed
	// logic could ALSO be exposed as a REST endpoint or reqreply endpoint
	// from the same port declaration, simultaneously.
	fmt.Println("\n=== MCP bridge: composing with ports.ToolPort.SetFunc ===")

	domainPort, err := ports.NewToolPort[registry.GetTagsReq, registry.TagsList](
		"get_tags", mcpGetTagsReqCodec, registry.TagsListCodec, ports.PortOptions{},
	)
	if err != nil {
		logger.Error("new tool port", "error", err)
		os.Exit(1)
	}
	domainPort.SetFunc(mcprest.ToolHandler(client, baseURL, restHandle, callOpts))

	adapter := &demoToolAdapter{}
	if err := domainPort.Bind(context.Background(), adapter); err != nil {
		logger.Error("bind demo tool adapter", "error", err)
		os.Exit(1)
	}
	values, errs := gstream.Collect(context.Background(),
		adapter.fn(context.Background(), registry.GetTagsReq{Name: "factory-dashboard"}))
	if len(errs) > 0 {
		logger.Error("ports.ToolPort demo call", "error", errs[0])
		os.Exit(1)
	}
	fmt.Printf("ports.ToolPort demo: tags for %s: %v\n", values[0].Name, values[0].Tags)

	// ── Package-provided MCP tools: GetTagsTool/GetImageMetadataTool ─────────
	//
	// Unlike the ToolHandler/MappedToolHandler sections above (which wrap
	// GetTagsRoute directly via adapters/mcprest — fixed to ONE registry's
	// baseURL), registry.GetTagsTool/GetImageMetadataTool wrap the
	// batteries-included GetTags/GetImageMetadata FUNCTIONS themselves —
	// registry-agnostic, exactly like calling them directly above (each
	// resolves its target registry per call from the tool input's
	// ImageURL). No mcprest bridge is involved: NewGetTagsToolHandler/
	// NewGetImageMetadataToolHandler are plain closures over
	// registryapp.GetTags/GetImageMetadata.
	fmt.Println("\n=== Package-provided MCP tools: registry.GetTagsTool/GetImageMetadataTool (registry-agnostic) ===")

	imageURL, err := registry.FormatImageRef(registry.ImageRef{
		Registry: registryHost, Repository: "factory-dashboard", Reference: "latest",
	})
	if err != nil {
		logger.Error("format image ref", "error", err)
		os.Exit(1)
	}

	// A SEPARATE Builder from mcpBuilder above — GetTagsTool's name
	// ("get_tags") collides with the ad-hoc "get_tags" tool the
	// ToolHandler section above already registered on mcpBuilder.
	packageToolsBuilder := mcp.NewBuilder(mcp.Info{Name: "go-edge-models package-provided MCP tools", Version: "1.0.0"})

	getTagsToolHandle, err := registry.GetTagsTool.Register(packageToolsBuilder)
	if err != nil {
		logger.Error("register get_tags (package tool)", "error", err)
		os.Exit(1)
	}
	_, getTagsToolHandlerFn := mcpgo.ToolHandler(getTagsToolHandle,
		registryapp.NewGetTagsToolHandler(client),
		mcpgo.Options{},
	)
	callMCPTool(getTagsToolHandlerFn, fmt.Sprintf("get_tags(%s)", imageURL), map[string]any{"imageURL": imageURL})

	getMetaToolHandle, err := registry.GetImageMetadataTool.Register(packageToolsBuilder)
	if err != nil {
		logger.Error("register get_image_metadata (package tool)", "error", err)
		os.Exit(1)
	}
	_, getMetaToolHandlerFn := mcpgo.ToolHandler(getMetaToolHandle,
		registryapp.NewGetImageMetadataToolHandler(client),
		mcpgo.Options{},
	)
	callMCPTool(getMetaToolHandlerFn, fmt.Sprintf("get_image_metadata(%s)", imageURL), map[string]any{"imageURL": imageURL})
}

// searchTagsInput/searchTagsOutput are a simplified, LLM-facing tool
// shape — deliberately different from registry.GetTagsReq/TagsList (fewer
// fields, renamed) to demonstrate mcprest.MappedToolHandler's mapping layer.
type searchTagsInput struct{ Image string }
type searchTagsOutput struct{ Tags []string }

// callMCPTool invokes an MCP tool handler function in-process (no real
// server/client needed) and prints the result — same pattern as
// examples/adapters-mcp's callTool helper.
func callMCPTool(handler mcpgoserver.ToolHandlerFunc, label string, args map[string]any) {
	fmt.Printf("  call: %s\n", label)
	result, err := handler(context.Background(), mcpmsg.CallToolRequest{
		Params: mcpmsg.CallToolParams{Arguments: args},
	})
	if err != nil {
		logger.Error("protocol error", "error", err)
		return
	}
	if len(result.Content) > 0 {
		if tc, ok := result.Content[0].(mcpmsg.TextContent); ok {
			if result.IsError {
				fmt.Printf("  → tool error (IsError=true): %s\n", tc.Text)
			} else {
				fmt.Printf("  → success: %s\n", tc.Text)
			}
		}
	}
}

// demoToolAdapter implements ports.ToolAdapter — captures the pipeline
// function ToolPort.Bind hands it, so this example can invoke it directly
// without starting a real MCP/REST/reqreply server.
type demoToolAdapter struct {
	fn func(context.Context, registry.GetTagsReq) gstream.Stream[registry.TagsList]
}

func (a *demoToolAdapter) Bind(_ context.Context, fn func(context.Context, registry.GetTagsReq) gstream.Stream[registry.TagsList]) error {
	a.fn = fn
	return nil
}

func (a *demoToolAdapter) AdapterName() string { return "demoToolAdapter" }

// roundTripFunc adapts a plain function to http.RoundTripper.
type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

// ManifestObserver implements stats.ValidationObserver — the codec-only
// observability path (see examples/stats-observer): embed
// stats.NoopObserver to satisfy the rest of stats.Observer without
// boilerplate, and just record what stats.ReportErrors reports.
type ManifestObserver struct {
	stats.NoopObserver
	errors []manifestValidationError
}

type manifestValidationError struct {
	location, constraint, field string
}

// RecordValidationError implements stats.ValidationObserver.
func (o *ManifestObserver) RecordValidationError(location, constraint, field string) {
	o.errors = append(o.errors, manifestValidationError{location: location, constraint: constraint, field: field})
}

// RegistryObserver implements stats.Observer — records every HTTP call
// registryapp.GetTags/GetImageMetadata make (via registryapp.WithObserver),
// including the auth-realm Ping/token exchange, not just the final
// tags/manifest fetch.
type RegistryObserver struct {
	stats.NoopObserver
	requests []registryRequest
}

type registryRequest struct {
	method, path string
	statusCode   int
}

// RecordRequest implements stats.Observer.
func (o *RegistryObserver) RecordRequest(method, path string, statusCode int, _ time.Duration) {
	o.requests = append(o.requests, registryRequest{method: method, path: path, statusCode: statusCode})
}

// Print reports a summary of every HTTP call observed so far.
func (o *RegistryObserver) Print() {
	fmt.Printf("%d HTTP call(s) observed:\n", len(o.requests))
	for _, r := range o.requests {
		fmt.Printf("  %-4s %-40s status=%d\n", r.method, r.path, r.statusCode)
	}
}
