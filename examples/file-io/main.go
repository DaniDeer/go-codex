// Package main demonstrates [file.Resource]: the declarative typed file
// descriptor for reading, writing, and updating files with full codec
// validation, attached to a [file.Client].
//
// Scenario: an IoT sensor data pipeline that:
//   - Loads a TOML service config on startup using a static [file.Resource]
//   - Applies a single env var override via [config.FromEnvVar]
//   - Writes and reads per-sensor JSON measurement files using a template path
//   - Validates path variables (date format) with a [file.PathParam] codec
//   - Handles all typed file errors with [errors.As]
//   - Wires a [stats.FileObserver] for per-operation metrics + slog for structured errors
//   - Writes and reads raw binary (PNG) files using [format.Binary] + [validate.PNG]
//
// Key patterns shown:
//   - [file.NewResource] — declare once, use anywhere (mirrors rest.Route / events.Channel)
//   - [file.Resource.BuildPath] — pre-flight path validation without any I/O
//   - [file.Resource.MatchPath] + [file.NewPathParam] + [codex.DecodeVars] —
//     the inverse of BuildPath: extracting filename-encoded metadata
//     (date, sensor) into a typed struct, merged alongside the body
//   - Update (read-modify-write) — a CLIENT-SIDE-ONLY composition
//     (Client.Read + transform + Client.Create), not a wire verb of its
//     own — mirrors [ports.File.Update]'s identical design
//   - [file.WriteOptions] — wiring custom file permission/CreateDirs
//   - [file.FilePathParamError], [file.MissingFilePathVarError],
//     [file.FileReadError] — navigate via [errors.As]
//   - [format.Binary] — raw binary file I/O with magic-byte validation
//   - Glob path template segments ("*"/"**" via [file.Resource.MatchPath])
//     — matching, not building: [file.Resource.BuildPath] rejects a
//     glob-enabled template with [file.FileWildcardBuildError]; see
//     examples/dir-io's Section 8 for [Client.ListDir]'s glob-DISCOVERY
//     mode, which Resource[T] itself deliberately does not duplicate
//
// NOTE — 2 disclosed differences from the equivalent ports.File-based
// example this replaces:
//   - api/file has NO FilePathTemplate-equivalent standalone type (a
//     template+params value reusable across multiple declarations
//     without inlining them each time) — this capability was
//     deliberately not carried over when file.Dir dropped its own
//     analogous WithBaseDir option; the plain-string NewResource form
//     remains the only (and primary) declaration path.
//   - file.Resource.WithPatch ALWAYS requires a typed patch codec
//     (mirrors ports.PatchEncoded) — there is no raw, ad hoc
//     map[string]any convenience the way ports.File.Patch(vars,
//     map[string]any, opts) offers; declare a small patch struct instead
//     (shown below).
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

	adapterfile "github.com/DaniDeer/go-codex/adapters/file"
	"github.com/DaniDeer/go-codex/api/file"
	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/config"
	"github.com/DaniDeer/go-codex/format"
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

// ── Resource descriptors — declared once, used anywhere ───────────────────────

// measurementFile is a template-path JSON resource descriptor. The {date}
// variable is validated against the ISO date format; {sensor} has no
// codec (any string). Shown as a pure declaration here (no I/O) — the
// actual temp-dir-rooted resource used for I/O below is sensorFile.
var measurementFile = file.NewResource[Measurement](
	"data/{date}/{sensor}.json",
	format.JSON(measurementCodec),
	file.PathParam{
		Name:        "date",
		Description: "ISO date (YYYY-MM-DD). Validated against the date format.",
	}.WithCodec(codex.String().Refine(validate.Date)),
	file.PathParam{
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

// loadConfig reads config from the given TOML path using a runtime-pathed Resource.
func loadConfig(ctx context.Context, client *file.Client, path string) (ServiceConfig, error) {
	res := file.NewResource[ServiceConfig](path, format.TOML(configCodec))
	respAny, err := client.Read(ctx, res.WithRead(file.Read{}), nil)
	if err != nil {
		return ServiceConfig{}, err
	}
	return respAny.(ServiceConfig), nil
}

// writeConfig writes config to the given path.
func writeConfig(ctx context.Context, client *file.Client, path string, cfg ServiceConfig) error {
	res := file.NewResource[ServiceConfig](path, format.TOML(configCodec))
	_, err := client.Create(ctx, res.WithCreate(file.Create{}), cfg, nil, file.WriteOptions{Perm: 0600})
	return err
}

// updateConfig is a CLIENT-SIDE-ONLY read-modify-write composition —
// mirrors ports.File.Update exactly (no new wire verb, no new adapter
// method; api/file deliberately never shipped an Update role/dispatch
// method of its own for this reason).
func updateConfig(ctx context.Context, client *file.Client, path string, fn func(ServiceConfig) ServiceConfig) error {
	cfg, err := loadConfig(ctx, client, path)
	if err != nil {
		return err
	}
	res := file.NewResource[ServiceConfig](path, format.TOML(configCodec))
	_, err = client.Create(ctx, res.WithCreate(file.Create{}), fn(cfg), nil)
	return err
}

// ── main ──────────────────────────────────────────────────────────────────────

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug}))
	slog.SetDefault(logger)

	metrics := &CountingObserver{}
	obs := stats.NewFanout(metrics, stats.NewLoggingObserver(logger.With("component", "file-io")))
	// Store obs in a context — file.Client's dispatch core resolves it
	// automatically via stats.ObserverFromContext.
	ctx := stats.WithObserver(context.Background(), obs)

	client := file.NewClient()
	if err := client.Attach(adapterfile.NewBackend()); err != nil {
		panic(err)
	}

	// ── Section 1: Static config file ─────────────────────────────────────────

	fmt.Println("\n── Section 1: Static TOML config ─────────────────────────────")

	// Write a sample config to a temp file so the example is self-contained.
	cfgPath := writeTempConfig()
	defer os.Remove(cfgPath)

	cfg, err := loadConfig(ctx, client, cfgPath)
	if err != nil {
		slog.Error("failed to load config", "err", err)
		os.Exit(1)
	}
	fmt.Printf("  loaded config: log_level=%q data_dir=%q retention_days=%d\n",
		cfg.LogLevel, cfg.DataDir, cfg.RetentionDays)

	// FromEnvVar: single env var override for log level.
	// Returns zero value (no error) when the variable is not set.
	if v := os.Getenv("LOG_LEVEL"); v != "" {
		level, err := config.FromEnvVar("LOG_LEVEL",
			codex.String().Refine(validate.OneOf("debug", "info", "warn", "error")))
		if err != nil {
			var envErr config.EnvVarError
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

	// Update: bump retention_days by 10 via atomic read-modify-write
	// (a client-side composition — see updateConfig's own doc comment).
	if err := updateConfig(ctx, client, cfgPath, func(c ServiceConfig) ServiceConfig {
		c.RetentionDays += 10
		return c
	}); err != nil {
		slog.Error("config update failed", "err", err)
	} else {
		updated, _ := loadConfig(ctx, client, cfgPath)
		fmt.Printf("  after Update: retention_days=%d\n", updated.RetentionDays)

		// writeConfig demonstrates writing a config value directly.
		// It uses the same TOML codec and file path — same contract, different helper.
		if err := writeConfig(ctx, client, cfgPath, updated); err != nil {
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
	sensorFile := file.NewResource[Measurement](
		dataDir+"/data/{date}/{sensor}.json",
		format.JSON(measurementCodec),
		file.PathParam{Name: "date"}.WithCodec(codex.String().Refine(validate.Date)),
		file.PathParam{Name: "sensor"},
	)
	sensorReader := sensorFile.WithRead(file.Read{})
	sensorCreator := sensorFile.WithCreate(file.Create{})
	sensorDeleter := sensorFile.WithDelete(file.Delete{})

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
	if _, err := client.Create(ctx, sensorCreator, m, vars); err != nil {
		slog.Error("Create failed", "err", err)
	} else {
		fmt.Printf("  written: sensor=%s value=%.1f%s\n", m.SensorID, m.Value, m.Unit)
	}

	// Read it back.
	gotAny, err := client.Read(ctx, sensorReader, vars)
	var got Measurement
	if err != nil {
		slog.Error("Read failed", "err", err)
	} else {
		got = gotAny.(Measurement)
		fmt.Printf("  read back: sensor=%s value=%.1f%s at %s\n",
			got.SensorID, got.Value, got.Unit, got.Timestamp.Format(time.RFC3339))
	}

	// ── Section 2b: MatchPath — the inverse of BuildPath ──────────────────────
	//
	// The original motivating scenario for this pattern: information is
	// ENCODED IN THE FILENAME (date, sensor) rather than the file body —
	// MatchPath is the missing inverse of BuildPath, extracting vars from
	// an already-discovered path (the caller's own filepath.WalkDir/Glob;
	// go-codex stays discovery-agnostic). file.NewPathParam declares BOTH
	// the spec PathParam (validation, unchanged) AND a merge field in ONE
	// call, so codex.DecodeVars can merge the extracted vars straight
	// into a typed struct alongside the resource's own body fields.
	fmt.Println("\n── Section 2b: MatchPath — extracting metadata from the filename ──")

	// ReadingMeta mirrors Measurement, but SensorID/Date come from the
	// PATH (merge fields) while Value comes from the file BODY — two
	// distinct sources merged into one struct.
	type ReadingMeta struct {
		SensorID string
		Date     string
		Value    float64
	}

	// metaFile is used ONLY for MatchPath/BuildPath/MergeFields in this
	// section (no Read/Write) — its body codec is a placeholder empty
	// Struct since MatchPath never touches the format, only the path
	// template.
	metaFile := file.NewResource[ReadingMeta](
		dataDir+"/data/{date}/{sensor}.json",
		format.JSON(codex.Struct[ReadingMeta]()),
		file.NewPathParam("date", codex.String().Refine(validate.Date),
			func(r ReadingMeta) string { return r.Date },
			func(r *ReadingMeta, v string) { r.Date = v }),
		file.NewPathParam("sensor", codex.String().Refine(validate.NonEmptyString),
			func(r ReadingMeta) string { return r.SensorID },
			func(r *ReadingMeta, v string) { r.SensorID = v }),
	)

	// path is the concrete path sensorFile's Create already produced above.
	discoveredVars, err := metaFile.MatchPath(path)
	if err != nil {
		slog.Error("MatchPath failed", "err", err)
	} else {
		fmt.Printf("  MatchPath extracted: %v\n", discoveredVars)
	}

	var meta ReadingMeta
	if err := codex.DecodeVars(&meta, discoveredVars, metaFile.MergeFields()...); err != nil {
		slog.Error("DecodeVars failed", "err", err)
	} else {
		fmt.Printf("  merged from path: sensor=%q date=%q\n", meta.SensorID, meta.Date)
	}

	// The body's own field (Value) is read separately and merged manually
	// — Resource's body codec and path merge fields are DISTINCT struct
	// shapes here (Measurement vs. ReadingMeta), so this last step stays
	// explicit rather than automatic (unlike REST's DecodeMerged, which
	// merges body+vars into the SAME struct in one call).
	meta.Value = got.Value
	fmt.Printf("  full merged metadata: %+v\n", meta)

	// A path that doesn't match the template's structure → FilePathMismatchError.
	_, err = metaFile.MatchPath(dataDir + "/data/2024-01-15/extra/segment.json")
	var mismatchErr file.FilePathMismatchError
	if errors.As(err, &mismatchErr) {
		fmt.Printf("  FilePathMismatchError: template=%q path=%q\n", mismatchErr.Template, mismatchErr.Path)
	}

	// ── Section 2c: CreateDirs — auto-creating missing parent directories ─────
	//
	// By default Create writes the FILE if missing but NOT its parent
	// directories (matches os.WriteFile exactly) — writing to a brand-new
	// sensor/date combination whose directory doesn't exist yet would fail
	// with FileWriteError unless WriteOptions.CreateDirs opts in.
	fmt.Println("\n── Section 2c: CreateDirs — auto-creating missing parent directories ──")

	newVars := map[string]string{"date": "2024-02-01", "sensor": "temp-99"}

	// Without CreateDirs: the "2024-02-01" directory doesn't exist yet.
	if _, err := client.Create(ctx, sensorCreator, m, newVars); err != nil {
		var writeErr file.FileWriteError
		if errors.As(err, &writeErr) {
			fmt.Printf("  without CreateDirs: FileWriteError (missing parent dir), as expected\n")
		}
	}

	// DryRun + CreateDirs: preview which directories WOULD be created,
	// without creating them or writing the file.
	createdDirs, err := client.Create(ctx, sensorCreator, m, newVars, file.WriteOptions{CreateDirs: true, DryRun: true})
	if err != nil {
		slog.Error("DryRun write failed", "err", err)
	} else {
		fmt.Printf("  DryRun + CreateDirs: would create %d dir(s): %v (nothing written)\n", len(createdDirs), createdDirs)
	}

	// With CreateDirs: the missing "2024-02-01" directory is created first.
	createdDirs, err = client.Create(ctx, sensorCreator, m, newVars, file.WriteOptions{CreateDirs: true})
	if err != nil {
		slog.Error("Write with CreateDirs failed", "err", err)
	} else {
		fmt.Printf("  with CreateDirs: created %d dir(s): %v, write succeeded\n", len(createdDirs), createdDirs)
	}

	// Strict: refuse to overwrite the file we just wrote (O_CREATE|O_EXCL).
	if _, err := client.Create(ctx, sensorCreator, m, newVars, file.WriteOptions{Strict: true}); err != nil {
		var existsErr file.FileAlreadyExistsError
		if errors.As(err, &existsErr) {
			fmt.Printf("  Strict: FileAlreadyExistsError (file already exists), as expected\n")
		}
	}

	// ── Section 2d: Delete — idempotent by default, Strict/DryRun opt-ins ────
	//
	// Delete's default is idempotent "ensure absent": deleting an
	// already-missing file succeeds. DeleteOptions.Strict opts OUT of
	// that, requiring the file to have existed. DeleteOptions.DryRun
	// previews the outcome without touching the filesystem.
	fmt.Println("\n── Section 2d: Client.Delete — idempotent by default ──")

	existed, err := client.Delete(ctx, sensorDeleter, newVars, file.DeleteOptions{DryRun: true})
	if err != nil {
		slog.Error("DryRun delete failed", "err", err)
	} else {
		fmt.Printf("  DryRun: existed=%v (nothing removed)\n", existed)
	}

	existed, err = client.Delete(ctx, sensorDeleter, newVars)
	if err != nil {
		slog.Error("Delete failed", "err", err)
	} else {
		fmt.Printf("  Delete: existed=%v, file removed\n", existed)
	}

	// Deleting again: idempotent success (no error, existed=false).
	existed, err = client.Delete(ctx, sensorDeleter, newVars)
	if err != nil {
		slog.Error("second Delete failed unexpectedly", "err", err)
	} else {
		fmt.Printf("  Delete again: existed=%v, err=nil (idempotent)\n", existed)
	}

	// Strict: the file is already gone — now it's an error.
	if _, err := client.Delete(ctx, sensorDeleter, newVars, file.DeleteOptions{Strict: true}); err != nil {
		var notFoundErr file.FileNotFoundError
		if errors.As(err, &notFoundErr) {
			fmt.Printf("  Strict Delete: FileNotFoundError (already absent), as expected\n")
		}
	}

	// ── Section 2e: Glob path template segments — matching, not building ─────
	//
	// file.Resource has NO glob-DISCOVERY method of its own (see
	// examples/dir-io's Section 8 for that — Client.ListDir's
	// glob-discovery mode). Resource's own role in the glob feature is
	// narrower: [file.Resource.MatchPath] validates/extracts vars from a
	// path ALREADY discovered by ListDir (or the caller's own
	// filepath.WalkDir), while [file.Resource.BuildPath] rejects a
	// glob-enabled template outright — building a SINGLE concrete path
	// from a template that can match MULTIPLE paths is undefined.
	fmt.Println("\n── Section 2e: glob path template segments — Resource.MatchPath/BuildPath ──")

	// A glob-enabled template: "*" shares a segment with literal text
	// ("app-*"), "**" matches zero-or-more WHOLE segments anywhere, and
	// a named "{level}" segment still captures normally — but ONLY
	// because it occupies a WHOLE segment on its own here. A
	// glob-enabled template requires "{varName}" segments to be
	// whole-segment (mutually exclusive with literal text/glob chars in
	// the SAME segment) — unlike a non-glob template's "{date}.json"
	// convenience, "{level}.log" would NOT work in a glob-enabled
	// template. Glob segments themselves never capture — only named
	// "{varName}" segments do.
	logGlobFile := file.NewResource[struct{}]("logs/app-*/**/{level}/trace.log", format.JSON(codex.Struct[struct{}]()))

	discoveredLogPath := "logs/app-web/a/b/fatal/trace.log"
	logVars, err := logGlobFile.MatchPath(discoveredLogPath)
	if err != nil {
		slog.Error("MatchPath on glob template failed", "err", err)
	} else {
		fmt.Printf("  MatchPath(%q): vars=%v (only {level} captured — \"*\"/\"**\" never capture)\n", discoveredLogPath, logVars)
	}

	// BuildPath has no meaning for a glob-enabled template — rejected
	// with FileWildcardBuildError instead of silently picking one match.
	_, err = logGlobFile.BuildPath(map[string]string{"level": "fatal"})
	var fileWildcardErr file.FileWildcardBuildError
	if errors.As(err, &fileWildcardErr) {
		fmt.Printf("  BuildPath on a glob-enabled template: FileWildcardBuildError (template=%q), as expected\n", fileWildcardErr.Template)
	}

	// ── Section 3: Error handling ─────────────────────────────────────────────

	fmt.Println("\n── Section 3: Typed error handling ────────────────────────────")

	// Missing path variable → MissingFilePathVarError.
	_, err = client.Read(ctx, sensorReader, map[string]string{"date": "2024-01-15"})
	if err != nil {
		var missingErr file.MissingFilePathVarError
		if errors.As(err, &missingErr) {
			fmt.Printf("  MissingFilePathVarError: param=%q\n", missingErr.Name)
		}
	}

	// Invalid path variable → FilePathParamError (date codec rejects this).
	_, err = sensorFile.BuildPath(map[string]string{"date": "not-a-date", "sensor": "s1"})
	if err != nil {
		var paramErr file.FilePathParamError
		if errors.As(err, &paramErr) {
			fmt.Printf("  FilePathParamError: param=%q value=%q\n", paramErr.Name, paramErr.Value)
		}
	}

	// File not found → FileReadError.
	absent := file.NewResource[Measurement]("/nonexistent/dir/item.json", format.JSON(measurementCodec))
	_, err = client.Read(ctx, absent.WithRead(file.Read{}), nil)
	if err != nil {
		var readErr file.FileReadError
		if errors.As(err, &readErr) {
			fmt.Printf("  FileReadError: path=%q\n", readErr.Path)
			slog.Warn("file not found", "path", readErr.Path, "cause", readErr.Err)
		}
	}

	// ── Section 4: Binary file I/O with format.Binary ────────────────────────

	fmt.Println("\n── Section 4: Binary (PNG) file I/O ───────────────────────────")

	// pngFile is a typed resource descriptor for raw PNG data.
	// format.Binary writes and reads []byte as-is (unlike Gob, which adds framing).
	// validate.PNG checks the 8-byte PNG magic bytes on every read and write.
	pngFile := file.NewResource[[]byte](
		dataDir+"/images/{name}.png",
		format.Binary(
			codex.Bytes().
				Refine(validate.MaxBytes(10*1024*1024)).
				Refine(validate.PNG),
		).WithContentType("image/png"),
		file.PathParam{Name: "name", Description: "Image name (no extension)."},
	)
	pngReader := pngFile.WithRead(file.Read{})
	pngCreator := pngFile.WithCreate(file.Create{})

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

	// Create — FileObserver.RecordFileWrite is called after the write.
	if _, err := client.Create(ctx, pngCreator, validPNG, pngVars); err != nil {
		slog.Error("PNG write failed", "err", err)
	} else {
		fmt.Printf("  written PNG: %d bytes\n", len(validPNG))
	}

	// Read — FileObserver.RecordFileRead is called after the read.
	dataAny, err := client.Read(ctx, pngReader, pngVars)
	if err != nil {
		slog.Error("PNG read failed", "err", err)
	} else {
		data := dataAny.([]byte)
		fmt.Printf("  read PNG: %d bytes, first 4: %X\n", len(data), data[:4])
	}

	// Write invalid data (missing PNG magic bytes) — constraint failure.
	notPNG := []byte("this is not a PNG file")
	if _, err := client.Create(ctx, pngCreator, notPNG, pngVars); err != nil {
		var encErr file.FileEncodeError
		if errors.As(err, &encErr) {
			fmt.Printf("  FileEncodeError (constraint): path=%q\n", encErr.Path)
			slog.Warn("PNG write rejected", "path", encErr.Path, "cause", encErr.Err)
		}
	}

	// ── Section 5: Partial file updates — typed Patch ────────────────────────

	fmt.Println("\n── Section 5: Partial file updates (typed Patch) ─────")

	// ── Choosing the right write operation ────────────────────────────────────
	//
	//   Client.Create(ctx, cr, T, vars, opts)         — full overwrite, no re-read
	//   updateConfig-style composition                — read current → transform → write
	//   Client.Patch(ctx, p, P, vars, opts)            — typed partial update via patch codec
	//
	// If you already have the decoded value and want to write it back: use Create.
	// A read-modify-write composition re-reads the file — only use it when
	// you need the latest file state. Patch preserves fields not in the
	// patch codec's own declared field set (see the field-survival rules
	// below) — unlike ports.File.Patch, there is no raw, undeclared
	// map[string]any convenience (see this file's own top-level NOTE).

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
	appCfgFile := file.NewResource[appCfg](dataDir+"/app-config.json", format.JSON(appCfgCodec))
	appCfgCreator := appCfgFile.WithCreate(file.Create{})
	appCfgReader := appCfgFile.WithRead(file.Read{})

	initial := appCfg{Port: 8080, LogLevel: "info", MaxWorkers: 4}
	if _, err := client.Create(ctx, appCfgCreator, initial, nil); err != nil {
		slog.Error("Write initial config failed", "err", err)
		os.Exit(1)
	}
	fmt.Printf("  initial: port=%d log_level=%q max_workers=%d\n",
		initial.Port, initial.LogLevel, initial.MaxWorkers)

	// ── Typed Patch via a separate patch codec ────────────────────────────────
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
	appCfgPatcher := file.WithPatch(appCfgFile, appCfgPatchCodec, file.Patch{})

	// PatchEncoded-equivalent: writes all patchCodec fields — including
	// debug_mode which is not in appCfgCodec. After this call, "debug_mode"
	// will be in the JSON file.
	if err := client.Patch(ctx, appCfgPatcher, appCfgPatch{Port: 7777, LogLevel: "warn", NewDebugMode: true}, nil); err != nil {
		slog.Error("Patch failed", "err", err)
	} else {
		afterAny, _ := client.Read(ctx, appCfgReader, nil)
		after := afterAny.(appCfg)
		fmt.Printf("  after Patch: port=%d log_level=%q max_workers=%d (always unchanged)\n",
			after.Port, after.LogLevel, after.MaxWorkers)
		// Read raw bytes to show debug_mode was persisted despite not being in appCfgCodec
		raw, _ := os.ReadFile(dataDir + "/app-config.json")
		fmt.Printf("  raw JSON includes debug_mode: %v\n", strings.Contains(string(raw), `"debug_mode":true`))
	}

	// ── Caveat: already decoded struct → use Create ───────────────────────────
	// If you've already decoded the struct and modified it, Create is the
	// right choice. A read-modify-write composition re-reads unnecessarily
	// if you already have the value in memory.
	decodedAny, _ := client.Read(ctx, appCfgReader, nil)
	decoded := decodedAny.(appCfg)
	decoded.Port = 5050                                    // modify in memory
	_, _ = client.Create(ctx, appCfgCreator, decoded, nil) // Create directly — no re-read
	fmt.Printf("  after Write (decoded→modified): port=%d\n", decoded.Port)

	// ── FilePatchNotSupportedError for Gob ────────────────────────────────────
	gobFile := file.NewResource[appCfg]("/tmp/example.gob", format.Gob(appCfgCodec))
	gobPatcher := file.WithPatch(gobFile, appCfgPatchCodec, file.Patch{})
	if err := client.Patch(ctx, gobPatcher, appCfgPatch{Port: 1234}, nil); err != nil {
		var patchErr file.FilePatchNotSupportedError
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
