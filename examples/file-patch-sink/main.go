// Package main demonstrates [file.DrainPatchAdapter] and
// [file.DrainPatchEncodedAdapter]: [ports.SinkPort]-bound sink adapters that
// apply each stream item as a PARTIAL update to an existing typed file,
// instead of the whole-file overwrite [file.DrainWriteFileAdapter] performs.
//
// Scenario: a service config file on disk receives a stream of incremental
// updates (e.g. from an admin API or config-management pipeline) and must be
// patched in place — untouched fields must survive.
//
// Two scenes:
//   - DrainPatchAdapter: untyped map[string]any patches (JSON Merge Patch
//     semantics via [ports.File.Patch]) — a SinkPort[map[string]any].
//   - DrainPatchEncodedAdapter: a typed patch struct via a dedicated patch
//     codec (via [ports.PatchEncoded]) — a SinkPort[ConfigPatch]; fields in
//     the patch codec but NOT in the file's own codec are still persisted.
//
// Both adapters stay handle-first (a hand-built [ports.File][T] passed
// directly, not declared via [ports.FilePattern]) because the patch item's
// type is deliberately different from the file's own whole-record type — the
// same reason [file.ReadEachAdapter] stays handle-first for its independent
// content type.
//
// Run with: go run ./examples/file-patch-sink
package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/DaniDeer/go-codex/adapters/file"
	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/format"
	"github.com/DaniDeer/go-codex/ports"
	gstream "github.com/DaniDeer/go-codex/stream"
)

// AppConfig is the file's whole-record shape.
type AppConfig struct {
	Port     int
	LogLevel string
}

var appConfigCodec = codex.Struct[AppConfig](
	codex.RequiredField("port", codex.Int(),
		func(c AppConfig) int { return c.Port },
		func(c *AppConfig, v int) { c.Port = v },
	),
	codex.RequiredField("log_level", codex.String(),
		func(c AppConfig) string { return c.LogLevel },
		func(c *AppConfig, v string) { c.LogLevel = v },
	),
)

// ConfigPatch is a typed partial update — only the fields callers may patch.
type ConfigPatch struct {
	LogLevel string
}

var configPatchCodec = codex.Struct[ConfigPatch](
	codex.RequiredField("log_level", codex.String(),
		func(p ConfigPatch) string { return p.LogLevel },
		func(p *ConfigPatch, v string) { p.LogLevel = v },
	),
)

func main() {
	dir, err := os.MkdirTemp("", "file-patch-sink")
	if err != nil {
		panic(err)
	}
	defer os.RemoveAll(dir)

	path := filepath.Join(dir, "config.json")
	configFile := ports.NewFile(path, format.JSON(appConfigCodec))

	// Seed the file with a whole record before any patches arrive.
	if _, err := configFile.Write(nil, AppConfig{Port: 8080, LogLevel: "info"}, ports.FileOptions{}); err != nil {
		panic(err)
	}
	fmt.Println("─── Scene 1: DrainPatchAdapter (untyped map[string]any patch)")

	ctx := context.Background()
	mapCodec := codex.Map(codex.String(), codex.Any())
	patches, err := ports.NewSinkPort[map[string]any]("config-patches", mapCodec, ports.PortOptions{Buffer: 4})
	if err != nil {
		panic(err)
	}
	patches.Bind(ctx, file.DrainPatchAdapter(configFile,
		func(map[string]any) map[string]string { return nil }, // no path vars — single file
		file.DrainPatchAdapterOptions{
			OnError: func(err error) { fmt.Println("  patch error:", err) },
		}))

	ch := make(chan map[string]any, 1)
	ch <- map[string]any{"log_level": "debug"} // "port" is absent — must survive untouched
	close(ch)
	patches.Feed(ctx, gstream.From(ctx, ch))

	got, err := configFile.Read(nil, ports.FileOptions{})
	if err != nil {
		panic(err)
	}
	fmt.Printf("  after patch: port=%d (untouched), log_level=%q (patched)\n", got.Port, got.LogLevel)

	// ── Scene 2: DrainPatchEncodedAdapter (typed patch via a patch codec) ─
	fmt.Println("\n─── Scene 2: DrainPatchEncodedAdapter (typed patch codec)")

	typedPatches, err := ports.NewSinkPort[ConfigPatch]("config-typed-patches", configPatchCodec, ports.PortOptions{Buffer: 4})
	if err != nil {
		panic(err)
	}
	typedPatches.Bind(ctx, file.DrainPatchEncodedAdapter(configFile, configPatchCodec,
		func(ConfigPatch) map[string]string { return nil },
		file.DrainPatchEncodedAdapterOptions{
			OnError: func(err error) { fmt.Println("  patch error:", err) },
		}))

	tch := make(chan ConfigPatch, 1)
	tch <- ConfigPatch{LogLevel: "warn"}
	close(tch)
	typedPatches.Feed(ctx, gstream.From(ctx, tch))

	got, err = configFile.Read(nil, ports.FileOptions{})
	if err != nil {
		panic(err)
	}
	fmt.Printf("  after typed patch: port=%d (still untouched), log_level=%q\n", got.Port, got.LogLevel)

	// ── Constraint: Gob (not map-based) rejects Patch entirely ────────────
	fmt.Println("\n─── Format restriction: Patch requires a map-based format")
	gobPath := filepath.Join(dir, "config.gob")
	gobFile := ports.NewFile(gobPath, format.Gob(appConfigCodec))
	if _, err := gobFile.Write(nil, AppConfig{Port: 9090, LogLevel: "info"}, ports.FileOptions{}); err != nil {
		panic(err)
	}
	gobPatches, err := ports.NewSinkPort[map[string]any]("gob-patches", mapCodec, ports.PortOptions{Buffer: 4})
	if err != nil {
		panic(err)
	}
	gobPatches.Bind(ctx, file.DrainPatchAdapter(gobFile,
		func(map[string]any) map[string]string { return nil },
		file.DrainPatchAdapterOptions{
			OnError: func(err error) { fmt.Println("  Gob file correctly rejects Patch:", err) },
		}))
	gch := make(chan map[string]any, 1)
	gch <- map[string]any{"log_level": "debug"}
	close(gch)
	gobPatches.Feed(ctx, gstream.From(ctx, gch))
}
