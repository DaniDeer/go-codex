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
// The example shows ten patterns:
//
//  1. Fixed dotted key — [codex.RequiredField] with a dotted Name literal.
//  2. Dynamic key via [File.Patch] — build the key with string concatenation.
//  3. Typed update via [PatchEncoded] + [codex.StringMap] — validates values.
//  4. Key + value validation via [PatchEncoded] + [codex.Map] — validates both.
//  5. Adding new keys not in the file codec — field survival via patchCodec.
//  6. Schema rendering — twinCodec, moduleCodec, modulePatchCodec as OpenAPI YAML.
//  7. Error cases + structured logging — FileEncodeError, FileDecodeError, FileReadError.
//  8. Observer summary — per-operation metrics collected by FileMetricsObserver.
//  9. Key+value merge with [codex.EntrySlice] (single field) — decode flat object into
//     []Container where container name is extracted from the key.
//  10. Multi-field key extraction — two segments (tenant + name) extracted from the key
//     into a struct using a [codex.Struct] domain codec + [codex.MapCodecValidated].
//  11. Static key — the container name is a compile-time constant, not a wire value.
//     [codex.MapCodecSafe] injects the constant into the decoded struct; encode drops it.
//
// Run with: go run ./examples/flat-key-patch
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"sort"
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

// ── Section 9: codecs for EntrySlice key+value merge ────────────────────────────

// Container combines the module name (from the key) with its config (from the value).
// codex.EntrySlice decodes the flat JSON/YAML/TOML object directly into []Container —
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
var containersCodec = codex.EntrySlice(
	containerKeyCodec,
	moduleCodec,
	func(name string, m ModuleConfig) Container {
		return Container{Name: name, Image: m.Image, Status: m.Status}
	},
	func(c Container) (string, ModuleConfig) {
		return c.Name, ModuleConfig{Image: c.Image, Status: c.Status}
	},
)

// ── Section 10: multi-field key extraction ────────────────────────────────────
//
// Key format: "properties.desired.modules.<tenant>.<container-name>"
// Two segments are extracted into a ModuleKey struct.
// K is a comparable struct — EntrySlice accepts any comparable K, not only string.

// ModuleKey holds the two segments parsed from the flat dotted key.
// Must be comparable (all fields are strings → Go structs are comparable).
type ModuleKey struct {
	Tenant string // e.g. "tenant-acme"
	Name   string // e.g. "cv-writer-kvrocks"
}

// TenantContainer is the result type — all three domain fields in one struct.
type TenantContainer struct {
	Tenant string
	Name   string
	Image  string
	Status string
}

const twoPartPrefix = "properties.desired.modules."

// twoPartKeyConstraint validates the full two-segment key on the wire.
var twoPartKeyConstraint = codex.Constraint[string]{
	Name: "two-part-module-key",
	Check: func(v string) bool {
		rest := strings.TrimPrefix(v, twoPartPrefix)
		parts := strings.SplitN(rest, ".", 2)
		return strings.HasPrefix(v, twoPartPrefix) && len(parts) == 2 &&
			len(parts[0]) > 0 && len(parts[1]) > 0
	},
	Message: func(v string) string {
		return fmt.Sprintf("key %q must be %q<tenant>.<name>", v, twoPartPrefix)
	},
}

// tenantConstraint validates the tenant segment of the key.
var tenantConstraint = codex.Constraint[string]{
	Name:  "tenant-name",
	Check: func(v string) bool { return len(v) > 0 && !strings.ContainsAny(v, " /_") },
	Message: func(v string) string {
		return fmt.Sprintf("tenant %q must be non-empty with no spaces, slashes, or underscores", v)
	},
}

// moduleKeyStructCodec validates each extracted field of ModuleKey independently.
var moduleKeyStructCodec = codex.Struct[ModuleKey](
	codex.RequiredField("tenant", codex.String().Refine(tenantConstraint),
		func(k ModuleKey) string { return k.Tenant },
		func(k *ModuleKey, v string) { k.Tenant = v },
	),
	codex.RequiredField("name", codex.String().Refine(containerNameConstraint),
		func(k ModuleKey) string { return k.Name },
		func(k *ModuleKey, v string) { k.Name = v },
	),
)

// twoPartKeyCodec: wire validates the full key; domain validates each segment.
//
//	Decode: "properties.desired.modules.tenant-acme.cv-writer-kvrocks"
//	      → split on first "." after prefix → ModuleKey{Tenant:"tenant-acme", Name:"cv-writer-kvrocks"}
//	      → moduleKeyStructCodec validates each field
//
//	Encode: ModuleKey → validate fields → reassemble → validate full key.
var twoPartKeyCodec = codex.MapCodecValidated(
	codex.String().Refine(twoPartKeyConstraint),
	moduleKeyStructCodec,
	func(fullKey string) (ModuleKey, error) {
		rest := strings.TrimPrefix(fullKey, twoPartPrefix)
		parts := strings.SplitN(rest, ".", 2)
		if len(parts) != 2 {
			return ModuleKey{}, fmt.Errorf("key %q: expected <tenant>.<name> after prefix", fullKey)
		}
		return ModuleKey{Tenant: parts[0], Name: parts[1]}, nil
	},
	func(k ModuleKey) (string, error) {
		return twoPartPrefix + k.Tenant + "." + k.Name, nil
	},
)

// tenantContainersCodec: K = ModuleKey (struct), V = ModuleConfig.
// EntrySlice accepts any comparable K — structs with only comparable fields qualify.
var tenantContainersCodec = codex.EntrySlice(
	twoPartKeyCodec,
	moduleCodec,
	func(k ModuleKey, m ModuleConfig) TenantContainer {
		return TenantContainer{Tenant: k.Tenant, Name: k.Name, Image: m.Image, Status: m.Status}
	},
	func(c TenantContainer) (ModuleKey, ModuleConfig) {
		return ModuleKey{Tenant: c.Tenant, Name: c.Name},
			ModuleConfig{Image: c.Image, Status: c.Status}
	},
)

// ── Section 11: static key — constant name injected via MapCodecSafe ──────────
//
// When the container name is known at compile time it is NOT present in the value
// object on the wire. The full dotted key is the field name in the Struct codec;
// the name is injected as a constant during decode and dropped on encode.
//
// Wire format:
//   {
//     "properties.desired.modules.cv-writer-kvrocks": {"image": "...", "status": "running"}
//   }
// Result type:  Container{Name:"cv-writer-kvrocks", Image:"...", Status:"running"}
// Name appears ONLY as the struct field name literal — not in the value object.

const (
	cvWriterKeyName = "cv-writer-kvrocks"
	cvWriterKey     = moduleKeyPrefix + cvWriterKeyName
)

// cvWriterValueCodec wraps moduleCodec to inject the constant name into Container.
// On decode: ModuleConfig → Container{Name: cvWriterKeyName, ...}
// On encode: Container → ModuleConfig (Name is dropped — it lives in the key, not the value)
var cvWriterValueCodec = codex.MapCodecSafe(
	moduleCodec,
	func(m ModuleConfig) Container {
		return Container{Name: cvWriterKeyName, Image: m.Image, Status: m.Status}
	},
	func(c Container) (ModuleConfig, error) {
		return ModuleConfig{Image: c.Image, Status: c.Status}, nil
	},
)

// cvWriterCodec: Struct[Container] where the field name is the full dotted key.
// Dots are valid codec field names — the codec uses the literal string as the JSON key.
var cvWriterCodec = codex.Struct[Container](
	codex.RequiredField(cvWriterKey, cvWriterValueCodec,
		func(c Container) Container { return c },
		func(outer *Container, inner Container) { *outer = inner },
	),
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

	// ── 9. codex.EntrySlice — key+value merge ─────────────────────────────────
	runEntriesDemo()

	// ── 10. Multi-field key extraction (struct key type) ───────────────────
	runMultiFieldKeyDemo()

	// ── 11. Static key — constant name injected via MapCodecSafe ───────────
	runStaticKeyDemo()
}

func readFile(path string) string {
	b, _ := os.ReadFile(path)
	return string(b)
}

// printContainers prints a sorted, human-readable container list.
// Sorting by name makes the output deterministic despite JSON object key order.
func printContainers(label string, cs []Container) {
	sort.Slice(cs, func(i, j int) bool { return cs[i].Name < cs[j].Name })
	fmt.Printf("  %s (%d containers):\n", label, len(cs))
	for _, c := range cs {
		fmt.Printf("    %-30s  image=%-35s  status=%s\n", c.Name, c.Image, c.Status)
	}
}

// runEntriesDemo shows Section 9: decoding a list of containers from a flat
// dotted-key JSON object using codex.EntrySlice.
//
// Wire representation (Azure IoT Edge device twin style):
//
//	{
//	  "properties.desired.modules.cv-writer-kvrocks":  {"image": "...", "status": "running"},
//	  "properties.desired.modules.cv-writer-gateway":  {"image": "...", "status": "running"},
//	  "properties.desired.modules.analytics-engine":   {"image": "...", "status": "stopped"},
//	}
//
// Each key encodes the container name after the fixed prefix.
// containersCodec (Codec[[]Container]) decodes this directly into a Go slice —
// no post-processing loop, no manual prefix stripping.
func runEntriesDemo() {
	fmt.Println("\n=== 9. EntrySlice: flat object → []Container ===")

	// Three containers as they arrive from the device twin API.
	rawJSON := map[string]any{
		moduleKeyPrefix + "cv-writer-kvrocks": map[string]any{
			"image":  "myregistry/cv-writer:1.0",
			"status": "running",
		},
		moduleKeyPrefix + "cv-writer-gateway": map[string]any{
			"image":  "myregistry/gateway:3.1",
			"status": "running",
		},
		moduleKeyPrefix + "analytics-engine": map[string]any{
			"image":  "myregistry/analytics:2.1",
			"status": "stopped",
		},
	}

	// ── 9a. Decode: flat object → []Container ──────────────────────────────
	//
	// containersCodec.Decode iterates every key, strips the prefix via
	// containerKeyCodec, and builds a Container per entry. The name field
	// comes from the key; image/status come from the value object.
	fmt.Println()
	containers, err := containersCodec.Decode(rawJSON)
	if err != nil {
		fmt.Println("  decode error:", err)
		return
	}
	printContainers("Decoded from JSON intermediate", containers)

	// ── 9b. Encode: []Container → flat dotted-key JSON ─────────────────────
	//
	// containersCodec.Encode calls split(c) → (name, ModuleConfig), then
	// containerKeyCodec.Encode(name) re-adds the prefix before writing to
	// the output map. Each container becomes one key-value pair.
	fmt.Println()
	jsonFmt := format.JSON(containersCodec)
	jsonBytes, err := jsonFmt.Marshal(containers)
	if err != nil {
		fmt.Println("  JSON marshal error:", err)
		return
	}
	// Pretty-print to show the flat dotted-key structure.
	var pretty map[string]any
	_ = json.Unmarshal(jsonBytes, &pretty)
	fmt.Printf("  Encoded JSON keys (%d):\n", len(pretty))
	keys := make([]string, 0, len(pretty))
	for k := range pretty {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		fmt.Printf("    %s\n", k)
	}

	// ── 9c. Round-trip via JSON bytes ───────────────────────────────────────
	fmt.Println()
	roundtripped, err := jsonFmt.Unmarshal(jsonBytes)
	if err != nil {
		fmt.Println("  JSON unmarshal error:", err)
		return
	}
	printContainers("Round-tripped through JSON bytes", roundtripped)

	// ── 9d. YAML round-trip ─────────────────────────────────────────────────
	//
	// YAML quoted keys ("properties.desired.modules.cv-writer-kvrocks":)
	// are preserved verbatim — the dotted path stays flat.
	fmt.Println()
	yamlFmt := format.YAML(containersCodec)
	yamlBytes, err := yamlFmt.Marshal(containers)
	if err != nil {
		fmt.Println("  YAML marshal error:", err)
		return
	}
	yamlDecoded, err := yamlFmt.Unmarshal(yamlBytes)
	if err != nil {
		fmt.Println("  YAML unmarshal error:", err)
		return
	}
	printContainers("Round-tripped through YAML", yamlDecoded)

	// ── 9e. TOML round-trip (quoted table headers) ──────────────────────────
	//
	// TOML quoted table headers preserve the dotted key flat:
	//   ["properties.desired.modules.cv-writer-kvrocks"]
	//   image = "..."
	//   status = "running"
	//
	// NOTE: bare dotted keys ([properties.desired.modules]) produce nested
	// objects per the TOML spec and will NOT work with containersCodec.
	fmt.Println()
	tomlFmt := format.TOML(containersCodec)
	tomlBytes, err := tomlFmt.Marshal(containers)
	if err != nil {
		fmt.Println("  TOML marshal error:", err)
		return
	}
	tomlDecoded, err := tomlFmt.Unmarshal(tomlBytes)
	if err != nil {
		fmt.Println("  TOML unmarshal error:", err)
		return
	}
	printContainers("Round-tripped through TOML", tomlDecoded)

	// ── 9f. Encode validation: invalid container name ───────────────────────
	//
	// The key codec validates the container name before adding the prefix.
	// "bad_name" contains an underscore → containerNameConstraint fails →
	// KeyError{Key: "bad_name", Err: ConstraintError{Name: "container-name"}}.
	fmt.Println()
	bad := []Container{
		{Name: "bad_name", Image: "img:1", Status: "running"}, // underscore → invalid
	}
	_, err = containersCodec.Encode(bad)
	if err != nil {
		var ke codex.KeyError
		if errors.As(err, &ke) {
			fmt.Printf("  Encode validation: KeyError{Key: %q, Err: %v}\n", ke.Key, ke.Err)
		}
	}
}

// runMultiFieldKeyDemo shows Section 10: two segments extracted from the key into
// a comparable struct (ModuleKey), giving each element Tenant + Name + Image + Status.
//
// Wire format:
//
//	{
//	  "properties.desired.modules.tenant-acme.cv-writer-kvrocks":  {"image":"...","status":"running"},
//	  "properties.desired.modules.tenant-acme.cv-writer-gateway":  {"image":"...","status":"running"},
//	  "properties.desired.modules.tenant-beta.analytics-engine":   {"image":"...","status":"stopped"},
//	}
//
// K = ModuleKey{Tenant, Name} — struct key, not a plain string.
// EntrySlice accepts any comparable K; structs with all-comparable fields qualify.
func runMultiFieldKeyDemo() {
	fmt.Println("\n=== 10. Multi-field key extraction (K = struct) ===")

	raw := map[string]any{
		twoPartPrefix + "tenant-acme.cv-writer-kvrocks": map[string]any{
			"image":  "myregistry/cv-writer:1.0",
			"status": "running",
		},
		twoPartPrefix + "tenant-acme.cv-writer-gateway": map[string]any{
			"image":  "myregistry/gateway:3.1",
			"status": "running",
		},
		twoPartPrefix + "tenant-beta.analytics-engine": map[string]any{
			"image":  "myregistry/analytics:2.1",
			"status": "stopped",
		},
	}

	// ── 10a. Decode ─────────────────────────────────────────────────────────
	//
	// For each key "...tenant-acme.cv-writer-kvrocks":
	//   twoPartKeyCodec.Decode strips prefix, splits on ".", validates each segment
	//   → ModuleKey{Tenant:"tenant-acme", Name:"cv-writer-kvrocks"}
	//   merge(ModuleKey, ModuleConfig) → TenantContainer with all four fields set.
	fmt.Println()
	containers, err := tenantContainersCodec.Decode(raw)
	if err != nil {
		fmt.Println("  decode error:", err)
		return
	}
	sort.Slice(containers, func(i, j int) bool {
		if containers[i].Tenant != containers[j].Tenant {
			return containers[i].Tenant < containers[j].Tenant
		}
		return containers[i].Name < containers[j].Name
	})
	fmt.Printf("  Decoded %d TenantContainers:\n", len(containers))
	for _, c := range containers {
		fmt.Printf("    Tenant=%-15s Name=%-25s Image=%-30s Status=%s\n",
			c.Tenant, c.Name, c.Image, c.Status)
	}

	// ── 10b. Encode ─────────────────────────────────────────────────────────
	//
	// For each TenantContainer:
	//   split(c) → (ModuleKey{Tenant, Name}, ModuleConfig{Image, Status})
	//   twoPartKeyCodec.Encode(ModuleKey) validates both fields, assembles full key.
	fmt.Println()
	enc, err := tenantContainersCodec.Encode(containers)
	if err != nil {
		fmt.Println("  encode error:", err)
		return
	}
	m := enc.(map[string]any)
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	fmt.Printf("  Encoded %d wire keys:\n", len(keys))
	for _, k := range keys {
		fmt.Printf("    %s\n", k)
	}

	// ── 10c. Validation: bad tenant segment ─────────────────────────────────
	//
	// "tenant_acme" contains an underscore — fails tenantConstraint at the
	// domain-field level, not just the full-key level. The error reports
	// field "tenant" specifically, not just "the key is invalid".
	// Error path: moduleKeyStructCodec validates ModuleKey →
	//   ValidationErrors[{Field:"tenant", Err:ConstraintError{Name:"tenant-name"}}]
	//   → wrapped in KeyError{Key: "{tenant_acme writer}", Err: ...}
	fmt.Println()
	bad := []TenantContainer{
		{Tenant: "tenant_acme", Name: "writer", Image: "img:1", Status: "running"},
	}
	_, err = tenantContainersCodec.Encode(bad)
	if err != nil {
		var ke codex.KeyError
		if errors.As(err, &ke) {
			fmt.Printf("  Validation (bad tenant): KeyError{Key: %q, Err: %v}\n", ke.Key, ke.Err)
		}
	}
}

// runStaticKeyDemo shows Section 11: static key where the container name is a
// compile-time constant, not decoded from the wire.
//
// Key insight: in the static case the name lives ONLY as the field name literal
// in codex.Struct. It does not appear inside the JSON value object at all.
// MapCodecSafe injects it as a constant during decode; encode drops it.
//
// Compare with EntrySlice (Section 9/10) which handles DYNAMIC keys where
// the container name varies at runtime and must be decoded from the wire key.
func runStaticKeyDemo() {
	fmt.Println("\n=== 11. Static key — constant name injected via MapCodecSafe ===")

	// Wire document: the key is always "properties.desired.modules.cv-writer-kvrocks".
	// The name "cv-writer-kvrocks" appears as the JSON key — NOT inside the value object.
	raw := map[string]any{
		cvWriterKey: map[string]any{
			"image":  "myregistry/cv-writer:1.0",
			"status": "running",
		},
	}

	// ── 11a. Decode ─────────────────────────────────────────────────────────
	//
	// cvWriterCodec.Decode:
	//   1. Looks up field "properties.desired.modules.cv-writer-kvrocks" in the object.
	//   2. Decodes the value {"image","status"} via moduleCodec → ModuleConfig.
	//   3. cvWriterValueCodec.MapCodecSafe injects the constant name → Container.
	fmt.Println()
	container, err := cvWriterCodec.Decode(raw)
	if err != nil {
		fmt.Println("  decode error:", err)
		return
	}
	fmt.Printf("  Decoded: Name=%-25s Image=%-30s Status=%s\n",
		container.Name, container.Image, container.Status)
	fmt.Printf("  Name came from: compile-time constant %q\n", cvWriterKeyName)

	// ── 11b. Encode ─────────────────────────────────────────────────────────
	//
	// cvWriterCodec.Encode:
	//   1. cvWriterValueCodec.MapCodecSafe.from(c) → ModuleConfig{Image,Status} (drops Name)
	//   2. moduleCodec encodes ModuleConfig → {"image":"...","status":"..."}
	//   3. Written under key "properties.desired.modules.cv-writer-kvrocks"
	//
	// The encoded value object does NOT contain a "name" key — Name is only in the
	// outer JSON key, not in the value object.
	fmt.Println()
	enc, err := cvWriterCodec.Encode(container)
	if err != nil {
		fmt.Println("  encode error:", err)
		return
	}
	encMap := enc.(map[string]any)
	valObj := encMap[cvWriterKey].(map[string]any)
	fmt.Printf("  Encoded wire key: %s\n", cvWriterKey)
	fmt.Printf("  Encoded value: %v\n", valObj)
	_, nameInValue := valObj["name"]
	fmt.Printf("  'name' in value object: %v (correct — name lives in the key, not the value)\n",
		nameInValue)

	// ── 11c. Round-trip via JSON ─────────────────────────────────────────────
	fmt.Println()
	jsonFmt := format.JSON(cvWriterCodec)
	jsonBytes, err := jsonFmt.Marshal(container)
	if err != nil {
		fmt.Println("  JSON marshal error:", err)
		return
	}
	roundtripped, err := jsonFmt.Unmarshal(jsonBytes)
	if err != nil {
		fmt.Println("  JSON unmarshal error:", err)
		return
	}
	fmt.Printf("  Round-trip: Name=%s Image=%s Status=%s\n",
		roundtripped.Name, roundtripped.Image, roundtripped.Status)
	fmt.Printf("  Wire JSON: %s\n", jsonBytes)
}
