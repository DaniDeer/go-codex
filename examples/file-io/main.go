// Package main demonstrates [ports.File]: the declarative typed file descriptor
// for reading, writing, and updating files with full codec validation.
//
// Scenario: an IoT sensor data pipeline that:
//   - Loads a TOML service config on startup using a static [ports.File]
//   - Applies a single env var override via [format.FromEnvVar]
//   - Writes and reads per-sensor JSON measurement files using a template path
//   - Validates path variables (date format) with a [ports.FilePathParam] codec
//   - Handles all typed file errors with [errors.As]
//   - Wires a [stats.FileObserver] for per-operation metrics + slog for structured errors
//   - Writes and reads raw binary (PNG) files using [format.Binary] + [validate.PNG]
//
// Key patterns shown:
//   - [ports.NewFile] — declare once, use anywhere (mirrors rest.Route / events.Channel)
//   - [ports.File.BuildPath] — pre-flight path validation without any I/O
//   - [ports.File.Update] — atomic read-modify-write in one call
//   - [ports.FileOptions] — wiring observer + custom file permission
//   - [ports.FilePathParamError], [ports.MissingFilePathVarError],
//     [ports.FileReadError] — navigate via [errors.As]
//   - [format.Binary] — raw binary file I/O with magic-byte validation
//
// Run with: go run ./examples/file-io
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/format"
	"github.com/DaniDeer/go-codex/ports"
	"github.com/DaniDeer/go-codex/stats"
	"github.com/DaniDeer/go-codex/validate"
)

// ── Domain models ─────────────────────────────────────────────────────────────

// ServiceConfig is the static configuration loaded on startup.
type ServiceConfig struct {
	LogLevel      string `json:"log_level"`
	DataDir       string `json:"data_dir"`
	RetentionDays int    `json:"retention_days"`
}

// Measurement is a single sensor reading written to a per-sensor file.
type Measurement struct {
	SensorID  string    `json:"sensor_id"`
	Timestamp time.Time `json:"timestamp"`
	Value     float64   `json:"value"`
	Unit      string    `json:"unit"`
}

// ── Codecs ────────────────────────────────────────────────────────────────────

var configCodec = codex.Struct[ServiceConfig](
	codex.DefaultField("log_level",
		codex.String().Refine(validate.OneOf("debug", "info", "warn", "error")),
		"info",
		func(c ServiceConfig) string { return c.LogLevel },
		func(c *ServiceConfig, v string) { c.LogLevel = v },
	),
	codex.RequiredField("data_dir",
		codex.String().Refine(validate.NonEmptyString).
			WithDescription("Root directory for sensor data files."),
		func(c ServiceConfig) string { return c.DataDir },
		func(c *ServiceConfig, v string) { c.DataDir = v },
	),
	codex.RequiredField("retention_days",
		codex.Int().Refine(validate.RangeInt(1, 365)).
			WithDescription("How many days to retain measurement files (1–365)."),
		func(c ServiceConfig) int { return c.RetentionDays },
		func(c *ServiceConfig, v int) { c.RetentionDays = v },
	),
)

var measurementCodec = codex.Struct[Measurement](
	codex.RequiredField("sensor_id",
		codex.String().Refine(validate.NonEmptyString),
		func(m Measurement) string { return m.SensorID },
		func(m *Measurement, v string) { m.SensorID = v },
	),
	codex.RequiredField("timestamp",
		codex.Time(),
		func(m Measurement) time.Time { return m.Timestamp },
		func(m *Measurement, v time.Time) { m.Timestamp = v },
	),
	codex.RequiredField("value",
		codex.Float64(),
		func(m Measurement) float64 { return m.Value },
		func(m *Measurement, v float64) { m.Value = v },
	),
	codex.RequiredField("unit",
		codex.String().Refine(validate.OneOf("celsius", "humidity", "hpa", "lux")),
		func(m Measurement) string { return m.Unit },
		func(m *Measurement, v string) { m.Unit = v },
	),
)

// ── File descriptors — declared once, used anywhere ───────────────────────────

// configFile is a static-path TOML file descriptor. No template variables.
// Declare at package level; pass to Read/Write/Update across the whole service.
var configFile = ports.NewFile(
	"", // path set at runtime via a wrapper — see loadConfig below
	format.TOML(configCodec),
)

// measurementFile is a template-path JSON file descriptor. The {date} variable
// is validated against the ISO date format; {sensor} has no codec (any string).
var measurementFile = ports.NewFile(
	"data/{date}/{sensor}.json",
	format.JSON(measurementCodec),
	ports.FilePathParam{
		Name:        "date",
		Description: "ISO date (YYYY-MM-DD). Validated against the date format.",
	}.WithCodec(codex.String().Refine(validate.Date)),
	ports.FilePathParam{
		Name:        "sensor",
		Description: "Sensor identifier (any non-empty string).",
	},
)

// ── Observer — metrics + structured logging ───────────────────────────────────

// CountingObserver collects file I/O metrics and implements both
// [stats.Observer] (for validation errors) and [stats.FileObserver]
// (for read/write lifecycle events).
//
// [stats.NoopObserver] is embedded to satisfy the full [stats.Observer]
// interface (RecordRequest, RecordSubscribe, RecordPublish) without boilerplate.
// In production, replace the counters with prometheus.CounterVec.
type CountingObserver struct {
	stats.NoopObserver // satisfies RecordRequest, RecordSubscribe, RecordPublish

	mu         sync.Mutex
	reads      int
	writes     int
	readFails  int
	writeFails int
	valErrors  int
}

func (o *CountingObserver) RecordValidationError(location, constraint, field string) {
	o.mu.Lock()
	o.valErrors++
	o.mu.Unlock()
}

// RecordFileRead implements [stats.FileObserver].
func (o *CountingObserver) RecordFileRead(path string, success bool, d time.Duration) {
	o.mu.Lock()
	if success {
		o.reads++
	} else {
		o.readFails++
	}
	o.mu.Unlock()
}

// RecordFileWrite implements [stats.FileObserver].
func (o *CountingObserver) RecordFileWrite(path string, success bool, d time.Duration) {
	o.mu.Lock()
	if success {
		o.writes++
	} else {
		o.writeFails++
	}
	o.mu.Unlock()
}

var _ stats.FileObserver = (*CountingObserver)(nil)

// ── Helpers ───────────────────────────────────────────────────────────────────

// loadConfig reads config from the given TOML path using a runtime-pathed File.
func loadConfig(ctx context.Context, path string) (ServiceConfig, error) {
	f := ports.NewFile(path, format.TOML(configCodec))
	return f.Read(nil, ports.FileOptions{Context: ctx}) // observer from ctx
}

// writeConfig writes config to the given path.
func writeConfig(ctx context.Context, path string, cfg ServiceConfig) error {
	f := ports.NewFile(path, format.TOML(configCodec))
	return f.Write(nil, cfg, ports.FileOptions{Context: ctx, Perm: 0600}) // observer from ctx
}

// ── main ──────────────────────────────────────────────────────────────────────

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug}))
	slog.SetDefault(logger)

	metrics := &CountingObserver{}
	obs := stats.NewFanout(metrics, stats.NewLoggingObserver(logger.With("component", "file-io")))
	// Store obs in a context — ports.File.Read/Write/Update/Patch all resolve it
	// automatically via FileOptions.Context when FileOptions.Observer is nil.
	ctx := stats.WithObserver(context.Background(), obs)

	// ── Section 1: Static config file ─────────────────────────────────────────

	fmt.Println("\n── Section 1: Static TOML config ─────────────────────────────")

	// Write a sample config to a temp file so the example is self-contained.
	cfgPath := writeTempConfig()
	defer os.Remove(cfgPath)

	cfg, err := loadConfig(ctx, cfgPath)
	if err != nil {
		slog.Error("failed to load config", "err", err)
		os.Exit(1)
	}
	fmt.Printf("  loaded config: log_level=%q data_dir=%q retention_days=%d\n",
		cfg.LogLevel, cfg.DataDir, cfg.RetentionDays)

	// FromEnvVar: single env var override for log level.
	// Returns zero value (no error) when the variable is not set.
	if v := os.Getenv("LOG_LEVEL"); v != "" {
		level, err := format.FromEnvVar("LOG_LEVEL",
			codex.String().Refine(validate.OneOf("debug", "info", "warn", "error")))
		if err != nil {
			var envErr format.EnvVarError
			if errors.As(err, &envErr) {
				slog.Warn("LOG_LEVEL env var invalid",
					"key", envErr.Key,
					"cause", envErr.Err,
				)
				stats.ReportErrors(obs, "env", envErr.Err)
			}
		} else {
			cfg.LogLevel = level
			fmt.Printf("  LOG_LEVEL override applied: %q\n", cfg.LogLevel)
		}
	} else {
		fmt.Println("  LOG_LEVEL not set — using config value")
	}

	// Update: bump retention_days by 10 via atomic read-modify-write.
	f := ports.NewFile(cfgPath, format.TOML(configCodec))
	if err := f.Update(nil, func(c ServiceConfig) ServiceConfig {
		c.RetentionDays += 10
		return c
	}, ports.FileOptions{Context: ctx}); err != nil {
		slog.Error("config update failed", "err", err)
	} else {
		updated, _ := loadConfig(ctx, cfgPath)
		fmt.Printf("  after Update: retention_days=%d\n", updated.RetentionDays)

		// writeConfig demonstrates writing a config value directly.
		// It uses the same TOML codec and file path — same contract, different helper.
		if err := writeConfig(ctx, cfgPath, updated); err != nil {
			slog.Error("writeConfig failed", "err", err)
		}
	}

	// ── Section 2: Template measurement files ─────────────────────────────────

	fmt.Println("\n── Section 2: Template measurement files ──────────────────────")

	// Create the data directory for this example.
	dataDir := os.TempDir() + "/go-codex-file-io-demo"
	if err := os.MkdirAll(dataDir+"/data/2024-01-15", 0755); err != nil {
		slog.Error("mkdir failed", "err", err)
		os.Exit(1)
	}
	defer os.RemoveAll(dataDir)

	// measurementFile shows the declared template (relative path, no I/O).
	// BuildPath validates the variables without touching the filesystem.
	if templatePath, err := measurementFile.BuildPath(map[string]string{
		"date": "2024-01-15", "sensor": "temp-42",
	}); err != nil {
		slog.Error("template BuildPath failed", "err", err)
	} else {
		fmt.Printf("  template path: %s\n", templatePath)
	}

	// sensorFile uses the temp dir prefix for actual I/O in this example.
	sensorFile := ports.NewFile(
		dataDir+"/data/{date}/{sensor}.json",
		format.JSON(measurementCodec),
		ports.FilePathParam{Name: "date"}.WithCodec(codex.String().Refine(validate.Date)),
		ports.FilePathParam{Name: "sensor"},
	)

	vars := map[string]string{
		"date":   "2024-01-15",
		"sensor": "temp-42",
	}

	// Pre-flight: build concrete path without I/O.
	path, err := sensorFile.BuildPath(vars)
	if err != nil {
		slog.Error("BuildPath failed", "err", err)
	} else {
		fmt.Printf("  resolved path: %s\n", path)
	}

	// Write a measurement.
	m := Measurement{
		SensorID:  "temp-42",
		Timestamp: time.Date(2024, 1, 15, 9, 0, 0, 0, time.UTC),
		Value:     23.7,
		Unit:      "celsius",
	}
	if err := sensorFile.Write(vars, m, ports.FileOptions{Context: ctx}); err != nil {
		slog.Error("Write failed", "err", err)
	} else {
		fmt.Printf("  written: sensor=%s value=%.1f%s\n", m.SensorID, m.Value, m.Unit)
	}

	// Read it back.
	got, err := sensorFile.Read(vars, ports.FileOptions{Context: ctx})
	if err != nil {
		slog.Error("Read failed", "err", err)
	} else {
		fmt.Printf("  read back: sensor=%s value=%.1f%s at %s\n",
			got.SensorID, got.Value, got.Unit, got.Timestamp.Format(time.RFC3339))
	}

	// ── Section 3: Error handling ─────────────────────────────────────────────

	fmt.Println("\n── Section 3: Typed error handling ────────────────────────────")

	// Missing path variable → MissingFilePathVarError.
	_, err = sensorFile.Read(map[string]string{"date": "2024-01-15"}, ports.FileOptions{})
	if err != nil {
		var missingErr ports.MissingFilePathVarError
		if errors.As(err, &missingErr) {
			fmt.Printf("  MissingFilePathVarError: param=%q\n", missingErr.Name)
		}
	}

	// Invalid path variable → FilePathParamError (date codec rejects this).
	_, err = sensorFile.BuildPath(map[string]string{"date": "not-a-date", "sensor": "s1"})
	if err != nil {
		var paramErr ports.FilePathParamError
		if errors.As(err, &paramErr) {
			fmt.Printf("  FilePathParamError: param=%q value=%q\n", paramErr.Name, paramErr.Value)
		}
	}

	// File not found → FileReadError.
	absent := ports.NewFile("/nonexistent/dir/item.json", format.JSON(measurementCodec))
	_, err = absent.Read(nil, ports.FileOptions{Context: ctx})
	if err != nil {
		var readErr ports.FileReadError
		if errors.As(err, &readErr) {
			fmt.Printf("  FileReadError: path=%q\n", readErr.Path)
			slog.Warn("file not found", "path", readErr.Path, "cause", readErr.Err)
		}
	}

	// ── Section 4: Binary file I/O with format.Binary ────────────────────────

	fmt.Println("\n── Section 4: Binary (PNG) file I/O ───────────────────────────")

	// pngFile is a typed file descriptor for raw PNG data.
	// format.Binary writes and reads []byte as-is (unlike Gob, which adds framing).
	// validate.PNG checks the 8-byte PNG magic bytes on every read and write.
	pngFile := ports.NewFile(
		dataDir+"/images/{name}.png",
		format.Binary(
			codex.Bytes().
				Refine(validate.MaxBytes(10*1024*1024)).
				Refine(validate.PNG),
		).WithContentType("image/png"),
		ports.FilePathParam{Name: "name", Description: "Image name (no extension)."},
	)

	if err := os.MkdirAll(dataDir+"/images", 0755); err != nil {
		slog.Error("mkdir failed", "err", err)
		os.Exit(1)
	}

	// Minimal syntactically-valid 1×1 pixel PNG (67 bytes).
	validPNG := []byte{
		0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A, // PNG signature
		0x00, 0x00, 0x00, 0x0D, 0x49, 0x48, 0x44, 0x52, // IHDR chunk length + type
		0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01, // width=1, height=1
		0x08, 0x02, 0x00, 0x00, 0x00, 0x90, 0x77, 0x53, // bit depth, color type, CRC
		0xDE, 0x00, 0x00, 0x00, 0x0C, 0x49, 0x44, 0x41, // IDAT chunk
		0x54, 0x08, 0xD7, 0x63, 0xF8, 0xCF, 0xC0, 0x00, // compressed pixel data
		0x00, 0x00, 0x02, 0x00, 0x01, 0xE2, 0x21, 0xBC, // IDAT CRC
		0x33, 0x00, 0x00, 0x00, 0x00, 0x49, 0x45, 0x4E, // IEND chunk
		0x44, 0xAE, 0x42, 0x60, 0x82, // IEND CRC
	}

	pngVars := map[string]string{"name": "chart"}

	// Write — FileObserver.RecordFileWrite is called after the write.
	if err := pngFile.Write(pngVars, validPNG, ports.FileOptions{Context: ctx}); err != nil {
		slog.Error("PNG write failed", "err", err)
	} else {
		fmt.Printf("  written PNG: %d bytes\n", len(validPNG))
	}

	// Read — FileObserver.RecordFileRead is called after the read.
	data, err := pngFile.Read(pngVars, ports.FileOptions{Context: ctx})
	if err != nil {
		slog.Error("PNG read failed", "err", err)
	} else {
		fmt.Printf("  read PNG: %d bytes, first 4: %X\n", len(data), data[:4])
	}

	// Write invalid data (missing PNG magic bytes) — constraint failure.
	notPNG := []byte("this is not a PNG file")
	if err := pngFile.Write(pngVars, notPNG, ports.FileOptions{Context: ctx}); err != nil {
		var encErr ports.FileEncodeError
		if errors.As(err, &encErr) {
			fmt.Printf("  FileEncodeError (constraint): path=%q\n", encErr.Path)
			slog.Warn("PNG write rejected", "path", encErr.Path, "cause", encErr.Err)
		}
	}

	// ── Section 5: Partial file updates — Patch and PatchEncoded ─────────────

	fmt.Println("\n── Section 5: Partial file updates (Patch + PatchEncoded) ─────")

	// ── Choosing the right write operation ────────────────────────────────────
	//
	//   Write(vars, T, opts)                      — full overwrite, no re-read
	//   Update(vars, func(T)T, opts)              — read current → transform → write
	//   Patch(vars, map[string]any, opts)         — explicit partial field update (RFC 7396)
	//   ports.PatchEncoded(file, vars, Codec[P], P, opts) — typed partial update via patch codec
	//
	// If you already have the decoded value and want to write it back: use Write.
	// Update re-reads the file — only use it when you need the latest file state.
	// Patch and PatchEncoded preserve fields not in the patch.

	type appCfg struct {
		Port       int    `json:"port"`
		LogLevel   string `json:"log_level"`
		MaxWorkers int    `json:"max_workers"`
	}
	appCfgCodec := codex.Struct[appCfg](
		codex.RequiredField("port", codex.Int().Refine(validate.RangeInt(1, 65535)),
			func(c appCfg) int { return c.Port }, func(c *appCfg, v int) { c.Port = v }),
		codex.RequiredField("log_level", codex.String().Refine(validate.OneOf("debug", "info", "warn", "error")),
			func(c appCfg) string { return c.LogLevel }, func(c *appCfg, v string) { c.LogLevel = v }),
		codex.RequiredField("max_workers", codex.Int().Refine(validate.RangeInt(1, 256)),
			func(c appCfg) int { return c.MaxWorkers }, func(c *appCfg, v int) { c.MaxWorkers = v }),
	)
	appCfgFile := ports.NewFile(dataDir+"/app-config.json", format.JSON(appCfgCodec))

	initial := appCfg{Port: 8080, LogLevel: "info", MaxWorkers: 4}
	if err := appCfgFile.Write(nil, initial, ports.FileOptions{Context: ctx}); err != nil {
		slog.Error("Write initial config failed", "err", err)
		os.Exit(1)
	}
	fmt.Printf("  initial: port=%d log_level=%q max_workers=%d\n",
		initial.Port, initial.LogLevel, initial.MaxWorkers)

	// ── Patch: explicit map[string]any ────────────────────────────────────────
	// Only port and log_level change; max_workers is absent from the map and preserved.
	if err := appCfgFile.Patch(nil, map[string]any{
		"port":      9090,
		"log_level": "debug",
	}, ports.FileOptions{Context: ctx}); err != nil {
		slog.Error("Patch failed", "err", err)
	} else {
		after, _ := appCfgFile.Read(nil, ports.FileOptions{})
		fmt.Printf("  after Patch: port=%d log_level=%q max_workers=%d (unchanged)\n",
			after.Port, after.LogLevel, after.MaxWorkers)
	}

	// ── PatchEncoded: typed patch via a separate patch codec ──────────────────
	// Declare a patch type containing ONLY the fields you want to update.
	// Fields absent from the patch type are ALWAYS preserved in the file —
	// this is enforced by the type system, not by runtime checks.
	//
	// Field survival rules:
	//   - Fields in the FILE codec     → re-written with their current values
	//   - Fields in BOTH codecs        → updated to patch value
	//   - Fields in PATCH codec only   → written to file (validated by patchCodec)
	//   - Fields in NEITHER codec      → dropped
	type appCfgPatch struct {
		Port         int    `json:"port"`
		LogLevel     string `json:"log_level"`
		NewDebugMode bool   `json:"debug_mode"` // NEW: not in appCfgCodec — will be written
		// MaxWorkers intentionally absent — can never be changed via this codec
	}
	appCfgPatchCodec := codex.Struct[appCfgPatch](
		codex.RequiredField("port", codex.Int().Refine(validate.RangeInt(1, 65535)),
			func(p appCfgPatch) int { return p.Port }, func(p *appCfgPatch, v int) { p.Port = v }),
		codex.RequiredField("log_level", codex.String().Refine(validate.OneOf("debug", "info", "warn", "error")),
			func(p appCfgPatch) string { return p.LogLevel }, func(p *appCfgPatch, v string) { p.LogLevel = v }),
		codex.RequiredField("debug_mode", codex.Bool(),
			func(p appCfgPatch) bool { return p.NewDebugMode }, func(p *appCfgPatch, v bool) { p.NewDebugMode = v }),
	)

	// Reset to initial values.
	_ = appCfgFile.Write(nil, initial, ports.FileOptions{})
	fmt.Printf("  reset: port=%d log_level=%q max_workers=%d\n",
		initial.Port, initial.LogLevel, initial.MaxWorkers)

	// PatchEncoded writes all patchCodec fields — including debug_mode which is
	// not in appCfgCodec. After this call, "debug_mode" will be in the JSON file.
	if err := ports.PatchEncoded(appCfgFile, nil, appCfgPatchCodec,
		appCfgPatch{Port: 7777, LogLevel: "warn", NewDebugMode: true},
		ports.FileOptions{Context: ctx}); err != nil {
		slog.Error("PatchEncoded failed", "err", err)
	} else {
		after, _ := appCfgFile.Read(nil, ports.FileOptions{})
		fmt.Printf("  after PatchEncoded: port=%d log_level=%q max_workers=%d (always unchanged)\n",
			after.Port, after.LogLevel, after.MaxWorkers)
		// Read raw bytes to show debug_mode was persisted despite not being in appCfgCodec
		raw, _ := os.ReadFile(dataDir + "/app-config.json")
		fmt.Printf("  raw JSON includes debug_mode: %v\n", strings.Contains(string(raw), `"debug_mode":true`))
	}

	// ── Caveat: already decoded struct → use Write ────────────────────────────
	// If you've already decoded the struct and modified it, Write is the right choice.
	// Update re-reads unnecessarily if you already have the value in memory.
	decoded, _ := appCfgFile.Read(nil, ports.FileOptions{})
	decoded.Port = 5050                                                 // modify in memory
	_ = appCfgFile.Write(nil, decoded, ports.FileOptions{Context: ctx}) // Write directly — no re-read
	fmt.Printf("  after Write (decoded→modified): port=%d\n", decoded.Port)

	// ── FilePatchNotSupportedError for Gob ────────────────────────────────────
	gobFile := ports.NewFile("/tmp/example.gob", format.Gob(appCfgCodec))
	if err := gobFile.Patch(nil, map[string]any{"port": 1234}, ports.FileOptions{}); err != nil {
		var patchErr ports.FilePatchNotSupportedError
		if errors.As(err, &patchErr) {
			fmt.Printf("  FilePatchNotSupportedError: %s\n", patchErr.Error())
			slog.Warn("patch not supported", "error", patchErr) // slog.LogValuer fires
		}
	}

	// ── Section 6: Observer summary ───────────────────────────────────────────

	fmt.Println("\n── Section 6: Observer summary ────────────────────────────────")
	metrics.mu.Lock()
	defer metrics.mu.Unlock()
	fmt.Printf("  successful reads:  %d\n", metrics.reads)
	fmt.Printf("  successful writes: %d\n", metrics.writes)
	fmt.Printf("  failed reads:      %d\n", metrics.readFails)
	fmt.Printf("  failed writes:     %d\n", metrics.writeFails)
	fmt.Printf("  validation errors: %d\n", metrics.valErrors)

	_ = configFile // suppress unused warning — configFile is shown as a declaration pattern
}

// writeTempConfig writes a minimal TOML config to a temp file and returns its path.
func writeTempConfig() string {
	const content = `log_level = "info"
data_dir = "/var/sensor-data"
retention_days = 30
`
	f, err := os.CreateTemp("", "go-codex-config-*.toml")
	if err != nil {
		panic(err)
	}
	defer f.Close()
	if _, err := f.WriteString(content); err != nil {
		panic(err)
	}
	return f.Name()
}
