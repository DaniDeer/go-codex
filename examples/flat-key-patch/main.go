// Package main demonstrates flat dotted-key JSON patching with go-codex.
//
// Some JSON formats use flat dotted keys instead of nested objects:
//
//	{
//	  "properties.desired.modules.my-container":    {"image": "...", "status": "running"},
//	  "properties.desired.modules.other-container": {"image": "...", "status": "running"},
//	  "properties.desired.schemaVersion":           "1.0"
//	}
//
// This pattern is common in Azure IoT Edge device twins, Kubernetes ConfigMaps
// with dot-separated keys, and flat namespaced configuration stores.
//
// The example shows nine patterns:
//
//  1. Fixed dotted key — [codex.RequiredField] with a dotted Name literal.
//  2. Dynamic key via [File.Patch] — build the key with string concatenation.
//  3. Typed update via [PatchEncoded] + [codex.StringMap] — validates values.
//  4. Key + value validation via [PatchEncoded] + [codex.Map] — validates both.
//  5. Adding new keys not in the file codec — field survival via patchCodec.
//  6. Schema rendering — twinCodec, moduleCodec, modulePatchCodec as OpenAPI YAML.
//  7. Error cases + structured logging — FileEncodeError, FileDecodeError, FileReadError.
//  8. Observer summary — per-operation metrics collected by FileMetricsObserver.
//  9. Key+value merge with [codex.Entries] — decode flat object into []Container where
//     container name is extracted from the key. Shows JSON, YAML, and TOML (quoted headers).
//
// Run with: go run ./examples/flat-key-patch
package main

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/format"
	"github.com/DaniDeer/go-codex/render/openapi"
	"github.com/DaniDeer/go-codex/schema"
	"github.com/DaniDeer/go-codex/stats"
	"github.com/DaniDeer/go-codex/validate"
)

// ── Domain types ──────────────────────────────────────────────────────────────

// ModuleConfig describes a single IoT Edge module in the device twin.
type ModuleConfig struct {
	Image  string `json:"image"`
	Status string `json:"status"`
}

// DeviceTwin holds the known fields of the flat-key device twin document.
// Fields not declared here are not decoded but can be added via PatchEncoded.
type DeviceTwin struct {
	MyContainer    ModuleConfig
	OtherContainer ModuleConfig
	SchemaVersion  string
}

// ── Key constants ─────────────────────────────────────────────────────────────

const (
	// moduleKeyPrefix is the fixed namespace for all module keys.
	moduleKeyPrefix = "properties.desired.modules."
	keySchema       = "properties.desired.schemaVersion"
	keyMyContainer  = moduleKeyPrefix + "my-container"
	keyOther        = moduleKeyPrefix + "other-container"
)

// ── Codecs ────────────────────────────────────────────────────────────────────

// moduleCodec validates ModuleConfig values.
var moduleCodec = codex.Struct[ModuleConfig](
	codex.RequiredField("image",
		codex.String().Refine(validate.NonEmptyString).
			WithDescription("Container image reference (registry/name:tag)."),
		func(m ModuleConfig) string { return m.Image },
		func(m *ModuleConfig, v string) { m.Image = v },
	),
	codex.RequiredField("status",
		codex.String().Refine(validate.OneOf("running", "stopped")).
			WithDescription("Desired module status."),
		func(m ModuleConfig) string { return m.Status },
		func(m *ModuleConfig, v string) { m.Status = v },
	),
)

// twinCodec decodes and encodes the known dotted keys.
// The field Name is the literal JSON key — dots are valid in codec field names.
var twinCodec = codex.Struct[DeviceTwin](
	codex.RequiredField(keyMyContainer, // "properties.desired.modules.my-container"
		moduleCodec,
		func(t DeviceTwin) ModuleConfig { return t.MyContainer },
		func(t *DeviceTwin, v ModuleConfig) { t.MyContainer = v },
	),
	codex.RequiredField(keyOther, // "properties.desired.modules.other-container"
		moduleCodec,
		func(t DeviceTwin) ModuleConfig { return t.OtherContainer },
		func(t *DeviceTwin, v ModuleConfig) { t.OtherContainer = v },
	),
	codex.RequiredField(keySchema, // "properties.desired.schemaVersion"
		codex.String().Refine(validate.NonEmptyString),
		func(t DeviceTwin) string { return t.SchemaVersion },
		func(t *DeviceTwin, v string) { t.SchemaVersion = v },
	),
)

// moduleKeyConstraint validates that a key starts with the module namespace
// and has a non-empty module name after the prefix.
var moduleKeyConstraint = codex.Constraint[string]{
	Name: "module-key-path",
	Check: func(v string) bool {
		suffix := strings.TrimPrefix(v, moduleKeyPrefix)
		return strings.HasPrefix(v, moduleKeyPrefix) && len(suffix) > 0
	},
	Message: func(v string) string {
		return fmt.Sprintf("key %q must be %q followed by a module name", v, moduleKeyPrefix)
	},
}

// modulePatchCodec validates both the key format (must start with the module
// namespace prefix) and the ModuleConfig value for each entry.
// Use this with format.PatchEncoded for strict typed module patching.
var modulePatchCodec = codex.Map[string, ModuleConfig](
	codex.String().Refine(moduleKeyConstraint),
	moduleCodec,
)

// ── Section 9: codecs for Entries key+value merge ────────────────────────────

// Container combines the module name (from the key) with its config (from the value).
// codex.Entries decodes the flat JSON/YAML/TOML object directly into []Container —
// no post-processing loop needed.
type Container struct {
	Name   string // extracted from wire key, e.g. "cv-writer-kvrocks"
	Image  string
	Status string
}

// containerNameConstraint validates the module name segment (after the prefix).
var containerNameConstraint = codex.Constraint[string]{
	Name: "container-name",
	Check: func(v string) bool {
		return len(v) > 0 && !strings.ContainsAny(v, " /_")
	},
	Message: func(v string) string {
		return fmt.Sprintf("container name %q must be non-empty and contain no spaces, slashes, or underscores", v)
	},
}

// containerKeyCodec: two-layer validation via MapCodecValidated.
//   - wire codec validates the full dotted key ("properties.desired.modules.cv-writer")
//   - domain codec validates the extracted container name ("cv-writer")
//
// On decode: full key → strip prefix → validate name → return name.
// On encode: validate name → add prefix → validate full key → return full key.
var containerKeyCodec = codex.MapCodecValidated(
	codex.String().Refine(moduleKeyConstraint),
	codex.String().Refine(containerNameConstraint),
	func(fullKey string) (string, error) {
		return strings.TrimPrefix(fullKey, moduleKeyPrefix), nil
	},
	func(name string) (string, error) {
		return moduleKeyPrefix + name, nil
	},
)

// containersCodec decodes a flat JSON/YAML/TOML object into []Container.
// K = string (container name, after prefix stripping).
// V = ModuleConfig (image + status).
// merge injects K into Container.Name — no strings.TrimPrefix in merge/split.
var containersCodec = codex.Entries(
	containerKeyCodec,
	moduleCodec,
	func(name string, m ModuleConfig) Container {
		return Container{Name: name, Image: m.Image, Status: m.Status}
	},
	func(c Container) (string, ModuleConfig) {
		return c.Name, ModuleConfig{Image: c.Image, Status: c.Status}
	},
)

// ── Observer — metrics (counting) ─────────────────────────────────────────────

// FileMetricsObserver counts file I/O events for metrics.
// In production, replace the counters with prometheus.CounterVec.With(...).Inc().
// Metrics and logging are intentionally separate concerns — no slog calls here.
// Embed [stats.NoopObserver] to satisfy [stats.Observer] without boilerplate.
type FileMetricsObserver struct {
	stats.NoopObserver
	mu         sync.Mutex
	reads      int
	writes     int
	readFails  int
	writeFails int
	valErrors  int
}

func (o *FileMetricsObserver) RecordValidationError(_, _, _ string) {
	o.mu.Lock()
	o.valErrors++
	o.mu.Unlock()
}

func (o *FileMetricsObserver) RecordFileRead(_ string, success bool, _ time.Duration) {
	o.mu.Lock()
	if success {
		o.reads++
	} else {
		o.readFails++
	}
	o.mu.Unlock()
}

func (o *FileMetricsObserver) RecordFileWrite(_ string, success bool, _ time.Duration) {
	o.mu.Lock()
	if success {
		o.writes++
	} else {
		o.writeFails++
	}
	o.mu.Unlock()
}

var _ stats.FileObserver = (*FileMetricsObserver)(nil)

// ── main ──────────────────────────────────────────────────────────────────────

func main() {
	// Structured logging — separate from metrics.
	// In production: configure slog.Handler for JSON output, OpenTelemetry, etc.
	fileLogger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug})).
		With("component", "device-twin")
	slog.SetDefault(fileLogger)

	dir, err := os.MkdirTemp("", "go-codex-flat-key-*")
	if err != nil {
		fmt.Fprintln(os.Stderr, "MkdirTemp:", err)
		os.Exit(1)
	}
	defer os.RemoveAll(dir)

	// Metrics — separate from logging. Swap FileMetricsObserver for Prometheus in production.
	metrics := &FileMetricsObserver{}

	// Fanout: delegates to both metrics and the library-provided logging observer.
	obs := stats.NewFanout(metrics, stats.NewLoggingObserver(fileLogger))
	opts := format.FileOptions{Observer: obs}

	twinPath := dir + "/device-twin.json"
	twinFile := format.NewFile(twinPath, format.JSON(twinCodec))

	initial := DeviceTwin{
		MyContainer:    ModuleConfig{Image: "registry.io/my-app:v1", Status: "running"},
		OtherContainer: ModuleConfig{Image: "registry.io/other:v1", Status: "running"},
		SchemaVersion:  "1.0",
	}

	// ── 1. Write and read ──────────────────────────────────────────────────────

	fmt.Println("=== 1. Write and read (flat dotted keys) ===")

	if err := twinFile.Write(nil, initial, opts); err != nil {
		fmt.Fprintln(os.Stderr, "Write:", err)
		os.Exit(1)
	}
	fmt.Printf("raw JSON:\n%s\n", readFile(twinPath))

	twin, _ := twinFile.Read(nil, opts)
	fmt.Printf("decoded: my-container=%s  other=%s  schema=%s\n\n",
		twin.MyContainer.Image, twin.OtherContainer.Image, twin.SchemaVersion)

	// ── 2. File.Patch — dynamic module name via string concatenation ───────────

	fmt.Println("=== 2. File.Patch — dynamic key (string concatenation) ===")

	// deepMerge recurses into the value map: only "image" changes, "status" preserved.
	moduleName := "my-container"
	if err := twinFile.Patch(nil, map[string]any{
		moduleKeyPrefix + moduleName: map[string]any{
			"image": "registry.io/my-app:v2",
		},
	}, opts); err != nil {
		fmt.Fprintln(os.Stderr, "Patch:", err)
	} else {
		after, _ := twinFile.Read(nil, opts)
		fmt.Printf("my-container=%s  other=%s (unchanged)\n\n",
			after.MyContainer.Image, after.OtherContainer.Image)
	}

	// ── 3. PatchEncoded + StringMap — typed value validation ───────────────────

	fmt.Println("=== 3. PatchEncoded with StringMap — typed value validation ===")

	// codex.StringMap(moduleCodec) validates each ModuleConfig value;
	// any string key is accepted (no key format enforcement).
	if err := format.PatchEncoded(twinFile, nil, codex.StringMap(moduleCodec),
		map[string]ModuleConfig{
			moduleKeyPrefix + "my-container": {Image: "registry.io/my-app:v3", Status: "running"},
		}, opts); err != nil {
		fmt.Fprintln(os.Stderr, "PatchEncoded:", err)
	} else {
		after, _ := twinFile.Read(nil, opts)
		fmt.Printf("my-container=%s\n\n", after.MyContainer.Image)
	}

	// ── 4. PatchEncoded + Map — key format + value validated ───────────────────

	fmt.Println("=== 4. PatchEncoded with Map — key format + value validated ===")

	// modulePatchCodec validates:
	//   key:   must start with moduleKeyPrefix + non-empty module name
	//   value: full ModuleConfig validation (image non-empty, status oneOf)
	if err := format.PatchEncoded(twinFile, nil, modulePatchCodec,
		map[string]ModuleConfig{
			moduleKeyPrefix + "my-container": {Image: "registry.io/my-app:v4", Status: "running"},
		}, opts); err != nil {
		fmt.Fprintln(os.Stderr, "PatchEncoded:", err)
	} else {
		after, _ := twinFile.Read(nil, opts)
		fmt.Printf("my-container=%s\n\n", after.MyContainer.Image)
	}

	// ── 5. Adding a NEW module not in twinCodec ────────────────────────────────

	fmt.Println("=== 5. Adding new module (key not in twinCodec, written via patchCodec) ===")

	// "new-module" is not in twinCodec — twinCodec.Decode ignores it.
	// PatchEncoded marshals the MERGED MAP directly (not re-encodes T), so
	// "new-module" survives in the file. Field survival rule:
	//   field in patchCodec but not in file codec → WRITTEN.
	if err := format.PatchEncoded(twinFile, nil, modulePatchCodec,
		map[string]ModuleConfig{
			moduleKeyPrefix + "new-module": {Image: "registry.io/new:v1", Status: "running"},
		}, opts); err != nil {
		fmt.Fprintln(os.Stderr, "PatchEncoded:", err)
	} else {
		fmt.Printf("raw JSON (new-module present):\n%s\n\n", readFile(twinPath))
	}

	// ── 6. Schema rendering ────────────────────────────────────────────────────

	fmt.Println("=== 6. Schema rendering ===")
	fmt.Println()

	// All three codec schemas are rendered as OpenAPI components/schemas YAML.
	// twinCodec:       flat dotted-key property names in the JSON Schema
	// moduleCodec:     ModuleConfig object with image + status constraints
	// modulePatchCodec: object with propertyNames constraint + additionalProperties
	yamlBytes, err := openapi.MarshalYAML(map[string]schema.Schema{
		"DeviceTwin":      twinCodec.Schema,
		"ModuleConfig":    moduleCodec.Schema,
		"ModulesPatchMap": modulePatchCodec.Schema,
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "schema render:", err)
	} else {
		fmt.Println(string(yamlBytes))
	}

	// ── 7. Error cases + structured logging ───────────────────────────────────

	fmt.Println("=== 7. Error cases + structured logging ===")
	fmt.Println()

	// 7a. Bad value: status "restarting" not in OneOf("running","stopped").
	//     FileEncodeError implements slog.LogValuer — slog outputs structured attrs.
	err = format.PatchEncoded(twinFile, nil, modulePatchCodec,
		map[string]ModuleConfig{
			moduleKeyPrefix + "my-container": {Image: "img:v1", Status: "restarting"},
		}, opts)
	if err != nil {
		var encErr format.FileEncodeError
		if errors.As(err, &encErr) {
			slog.Warn("patch rejected — bad status",
				"error", encErr, // LogValue(): error.path + error.cause
				"path", encErr.Path, // explicit field access also works
			)
			fmt.Printf("FileEncodeError.Err: %v\n\n", encErr.Err)
		}
	}

	// 7b. Bad key: wrong namespace prefix — rejected by modulePatchCodec key codec.
	err = format.PatchEncoded(twinFile, nil, modulePatchCodec,
		map[string]ModuleConfig{
			"wrong.namespace.my-container": {Image: "img:v1", Status: "running"},
		}, opts)
	if err != nil {
		var encErr format.FileEncodeError
		if errors.As(err, &encErr) {
			slog.Warn("patch rejected — bad key prefix", "error", encErr)
			fmt.Printf("FileEncodeError.Err: %v\n\n", encErr.Err)
		}
	}

	// 7c. FileDecodeError via File.Patch: patching a known field to an invalid
	//     value passes deepMerge but fails twinCodec.Decode validation.
	//     (Patch uses map[string]any — no codec check on the patch map itself.)
	err = twinFile.Patch(nil, map[string]any{
		keyMyContainer: map[string]any{"status": "restarting"},
	}, opts)
	if err != nil {
		var decErr format.FileDecodeError
		if errors.As(err, &decErr) {
			slog.Warn("patch produced invalid state",
				"error", decErr, // LogValue(): error.path + error.cause
			)
			fmt.Printf("FileDecodeError.Err: %v\n\n", decErr.Err)
		}
	}

	// 7d. FileReadError: Patch on a non-existent file.
	missingFile := format.NewFile(dir+"/missing.json", format.JSON(twinCodec))
	err = missingFile.Patch(nil, map[string]any{keySchema: "2.0"}, opts)
	if err != nil {
		var readErr format.FileReadError
		if errors.As(err, &readErr) {
			slog.Warn("patch failed — file not found",
				"error", readErr, // LogValue(): error.path + error.cause
			)
			fmt.Printf("FileReadError.Path: %s\n\n", readErr.Path)
		}
	}

	// ── 8. Observer summary ────────────────────────────────────────────────────

	fmt.Println("=== 8. Observer summary (metrics) ===")
	// metrics and logging are independent — read counters from the metrics observer only.
	metrics.mu.Lock()
	defer metrics.mu.Unlock()
	fmt.Printf("successful reads:  %d\n", metrics.reads)
	fmt.Printf("successful writes: %d\n", metrics.writes)
	fmt.Printf("failed reads:      %d\n", metrics.readFails)
	fmt.Printf("failed writes:     %d\n", metrics.writeFails)
	fmt.Printf("validation errors: %d\n", metrics.valErrors)

	// ── 9. codex.Entries — key+value merge ─────────────────────────────────
	runEntriesDemo()
}

func readFile(path string) string {
	b, _ := os.ReadFile(path)
	return string(b)
}

// runEntriesDemo shows Section 9: key+value merge with codex.Entries.
func runEntriesDemo() {
	fmt.Println("\n=== 9. Key+value merge with codex.Entries ===")

	// The flat JSON object — keys encode the container name.
	rawJSON := map[string]any{
		moduleKeyPrefix + "cv-writer-kvrocks": map[string]any{
			"image":  "myregistry/cv-writer:1.0",
			"status": "running",
		},
		moduleKeyPrefix + "analytics-engine": map[string]any{
			"image":  "myregistry/analytics:2.1",
			"status": "stopped",
		},
	}

	// ── 9a. Decode JSON intermediate → []Container ─────────────────────────
	fmt.Println("\n9a. Decode: flat object → []Container (name extracted from key)")
	containers, err := containersCodec.Decode(rawJSON)
	if err != nil {
		fmt.Println("  decode error:", err)
		return
	}
	for _, c := range containers {
		fmt.Printf("  Name=%-25s Image=%-35s Status=%s\n", c.Name, c.Image, c.Status)
	}

	// ── 9b. Encode []Container → flat object ───────────────────────────────
	fmt.Println("\n9b. Encode: []Container → flat dotted-key object")
	enc, err := containersCodec.Encode(containers)
	if err != nil {
		fmt.Println("  encode error:", err)
		return
	}
	m := enc.(map[string]any)
	for k := range m {
		fmt.Printf("  key: %s\n", k)
	}

	// ── 9c. YAML round-trip ─────────────────────────────────────────────────
	fmt.Println("\n9c. YAML round-trip (quoted keys preserve dotted path)")
	yamlFmt := format.YAML(containersCodec)
	yamlBytes, err := yamlFmt.Marshal(containers)
	if err != nil {
		fmt.Println("  YAML marshal error:", err)
		return
	}
	decoded, err := yamlFmt.Unmarshal(yamlBytes)
	if err != nil {
		fmt.Println("  YAML unmarshal error:", err)
		return
	}
	fmt.Printf("  decoded %d container(s) from YAML\n", len(decoded))

	// ── 9d. TOML round-trip (quoted table headers) ──────────────────────────
	fmt.Println("\n9d. TOML round-trip (quoted headers keep dotted key flat)")
	tomlFmt := format.TOML(containersCodec)
	tomlBytes, err := tomlFmt.Marshal(containers)
	if err != nil {
		fmt.Println("  TOML marshal error:", err)
		return
	}
	decoded, err = tomlFmt.Unmarshal(tomlBytes)
	if err != nil {
		fmt.Println("  TOML unmarshal error:", err)
		return
	}
	fmt.Printf("  decoded %d container(s) from TOML\n", len(decoded))

	// ── 9e. Encode validation: invalid container name ───────────────────────
	fmt.Println("\n9e. Encode with invalid container name → KeyError")
	bad := []Container{{Name: "bad_name", Image: "img:1", Status: "running"}}
	_, err = containersCodec.Encode(bad)
	if err != nil {
		var ke codex.KeyError
		if errors.As(err, &ke) {
			fmt.Printf("  KeyError: key=%q err=%v\n", ke.Key, ke.Err)
		}
	}
}
