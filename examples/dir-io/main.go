// Package main demonstrates [ports.Dir]: the declarative directory-listing
// port — a codec-validated "ls", not "cat" — and how it composes into a
// pipeline via [ports.SourcePort].
//
// Scenario: a directory of per-sensor reading files, one JSON file per
// sensor (the filename IS the sensor ID, mirroring how iotedge's own
// config-files directory names each file after a "use case" —
// see examples/go-edge-models/models/iotedge.NewConfigDir for that exact
// consumer). An "archive/{year}/" subtree holds older readings, and a
// stray README.md sits alongside the sensor files (never matches the
// naming convention).
//
// Key patterns shown:
//   - [ports.NewDir] + [ports.WithEntryPattern] — declare once, list many:
//     each entry's filename is parsed/validated the SAME way
//     [ports.File.MatchPath] parses a full file path; non-matching entries
//     (the stray README.md) are silently excluded, not returned as errors
//   - [ports.Dir.BuildPath] / [ports.Dir.MatchPath] — validate the
//     DIRECTORY's own location (an {year} path segment), independent of
//     what's found inside it
//   - [ports.WithRecursive] — descend into the archive/{year}/ subtree,
//     with an [ports.EntryPattern] spanning subdirectory segments
//   - Composing [ports.Dir.List]'s one-shot discovery with a
//     [ports.SourcePort] pipeline: List finds WHICH sensors exist, then a
//     channel-backed source streams that discovery through a pipeline that
//     reads each sensor's own reading file — Dir has no adapter binding of
//     its own (by design — a directory listing has no single typed
//     payload), so it composes via the SAME [ports.ChanSourceAdapter] any
//     in-memory data source would use
//   - Typed errors: [ports.DirReadError], [ports.DirEntryParamError],
//     [ports.DirPathMismatchError] — navigate via [errors.As]
//   - [ports.DirOptions.CreateIfMissing] — opt-in auto-creation of a
//     missing directory before listing (default off, matches
//     [ports.File]'s own "never auto-create" default)
//
// Run with: go run ./examples/dir-io
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/format"
	"github.com/DaniDeer/go-codex/ports"
	gstream "github.com/DaniDeer/go-codex/stream"
	"github.com/DaniDeer/go-codex/validate"
)

// ── Domain model ──────────────────────────────────────────────────────────────

// Reading is one sensor's JSON reading file content.
type Reading struct {
	Value float64
	Unit  string
}

var readingCodec = codex.Struct[Reading](
	codex.RequiredField("value", codex.Float64(),
		func(r Reading) float64 { return r.Value },
		func(r *Reading, v float64) { r.Value = v },
	),
	codex.RequiredField("unit", codex.String(),
		func(r Reading) string { return r.Unit },
		func(r *Reading, v string) { r.Unit = v },
	),
)

var readingFormat = format.JSON(readingCodec)

// sensorIDCodec mirrors a realistic slug constraint — reused unchanged for
// BOTH the sensor file's own EntryPattern var AND the pipeline's discovered
// IDs; "inheriting" one validated concept rather than re-deriving it.
var sensorIDCodec = codex.String().Refine(validate.MinLen(3))

func main() {
	root, err := os.MkdirTemp("", "go-codex-dir-io-demo")
	if err != nil {
		panic(err)
	}
	defer os.RemoveAll(root)

	reportsDir := filepath.Join(root, "reports")
	archiveDir := filepath.Join(reportsDir, "archive", "2023")
	if err := os.MkdirAll(archiveDir, 0o755); err != nil {
		panic(err)
	}

	writeReading := func(path string, r Reading) {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			panic(err)
		}
		raw, err := readingFormat.Marshal(r)
		if err != nil {
			panic(err)
		}
		if err := os.WriteFile(path, raw, 0o644); err != nil {
			panic(err)
		}
	}

	// Current sensor readings — filename IS the sensor ID.
	writeReading(filepath.Join(reportsDir, "temp-01.json"), Reading{Value: 21.5, Unit: "celsius"})
	writeReading(filepath.Join(reportsDir, "temp-02.json"), Reading{Value: 19.8, Unit: "celsius"})
	// A structurally-matching but INVALID sensor ID (fails MinLen(3)) —
	// demonstrates DirEntryParamError, distinct from silent exclusion.
	writeReading(filepath.Join(reportsDir, "ab.json"), Reading{Value: 0, Unit: "celsius"})
	// Never matches "{sensorID}.json" at all — silently excluded, not an error.
	if err := os.WriteFile(filepath.Join(reportsDir, "README.md"), []byte("# reports\n"), 0o644); err != nil {
		panic(err)
	}
	// Archived readings, one subdirectory per year.
	writeReading(filepath.Join(archiveDir, "temp-01.json"), Reading{Value: 18.2, Unit: "celsius"})

	// ── Section 1: List — single-level, EntryPattern filters + parses ────────
	fmt.Println("── Section 1: Dir.List — discover sensor files (like \"ls\") ──")

	sensorFilePattern := ports.EntryPattern{
		Template: "{sensorID}.json",
		Params:   []ports.EntryParam{{Name: "sensorID", Codec: &sensorIDCodec}},
	}
	reportsListing := ports.NewDir(reportsDir, ports.WithEntryPattern(sensorFilePattern))

	// The invalid "ab.json" file is temporarily removed for this first
	// listing so Section 1 shows only the successful-discovery path;
	// Section 5 below re-introduces it to demonstrate DirEntryParamError.
	if err := os.Remove(filepath.Join(reportsDir, "ab.json")); err != nil {
		panic(err)
	}

	entries, err := reportsListing.List(nil, ports.DirOptions{})
	if err != nil {
		panic(err)
	}
	for _, e := range entries {
		fmt.Printf("  found sensor %q (file=%q, kind=%s)\n", e.Vars["sensorID"], e.Name, e.Kind)
	}
	fmt.Printf("  README.md and archive/ excluded automatically: %d entries total\n", len(entries))

	// ── Section 2: BuildPath/MatchPath — validating the DIRECTORY itself ────
	fmt.Println("\n── Section 2: Dir.BuildPath/MatchPath — the directory's OWN location ──")

	yearCodec := codex.String().Refine(validate.NonEmptyString)
	archiveYearDir := ports.NewDir(
		filepath.Join(reportsDir, "archive", "{year}"),
		ports.DirPathParam{Name: "year"}.WithCodec(yearCodec),
	)

	archivePath, err := archiveYearDir.BuildPath(map[string]string{"year": "2023"})
	if err != nil {
		panic(err)
	}
	fmt.Printf("  BuildPath(year=2023): %s\n", archivePath)

	matchedVars, err := archiveYearDir.MatchPath(archivePath)
	if err != nil {
		panic(err)
	}
	fmt.Printf("  MatchPath(%q): year=%q\n", filepath.Base(archivePath), matchedVars["year"])

	_, err = archiveYearDir.MatchPath(filepath.Join(reportsDir, "not-the-archive-shape"))
	var mismatchErr ports.DirPathMismatchError
	if errors.As(err, &mismatchErr) {
		fmt.Printf("  DirPathMismatchError: template=%q path=%q\n", mismatchErr.Template, mismatchErr.Path)
	}

	// ── Section 3: Recursive listing — EntryPattern spans subdirectories ────
	fmt.Println("\n── Section 3: Dir.List — recursive, EntryPattern spans subdirectories ──")

	archiveListing := ports.NewDir(filepath.Join(reportsDir, "archive"),
		ports.WithRecursive(true),
		ports.WithEntryPattern(ports.EntryPattern{
			Template: "{year}/{sensorID}.json",
			Params: []ports.EntryParam{
				{Name: "year", Codec: &yearCodec},
				{Name: "sensorID", Codec: &sensorIDCodec},
			},
		}),
	)
	archiveEntries, err := archiveListing.List(nil, ports.DirOptions{})
	if err != nil {
		panic(err)
	}
	for _, e := range archiveEntries {
		fmt.Printf("  archived: year=%q sensor=%q (relPath=%s)\n", e.Vars["year"], e.Vars["sensorID"], e.RelPath)
	}

	// ── Section 4: Pipeline capability — Dir.List discovery feeds a
	// SourcePort ───────────────────────────────────────────────────────────
	//
	// ports.Dir has NO adapter binding of its own (a directory listing has
	// no single typed payload the way ports.File's contents do) — but its
	// ONE-SHOT List result composes with a pipeline exactly like any other
	// in-memory data source: feed the discovered sensor IDs through
	// [ports.ChanSourceAdapter], then a downstream stage reads each
	// sensor's OWN reading file via [ports.File.Read]. Dir decides WHICH
	// files exist; File.Read decides WHAT's inside each one — two
	// declarative surfaces, cleanly composed.
	fmt.Println("\n── Section 4: Dir.List discovery feeding a SourcePort pipeline ──")

	discovered, err := reportsListing.List(nil, ports.DirOptions{})
	if err != nil {
		panic(err)
	}

	ctx := context.Background()
	sensors := codex.Must(ports.NewSourcePort[string]("discovered-sensors", codex.String(), ports.PortOptions{Buffer: len(discovered)}))

	ch := make(chan string, len(discovered))
	for _, e := range discovered {
		ch <- e.Vars["sensorID"]
	}
	close(ch)
	sensors.Bind(ctx, ports.ChanSourceAdapter(ch))

	sensorIDs, _ := gstream.Collect(ctx, sensors.Stream(ctx))
	for _, id := range sensorIDs {
		readingFile := ports.NewFile(filepath.Join(reportsDir, id+".json"), readingFormat)
		reading, err := readingFile.Read(nil, ports.FileOptions{})
		if err != nil {
			panic(err)
		}
		fmt.Printf("  pipeline read sensor=%q value=%.1f%s\n", id, reading.Value, reading.Unit)
	}

	// ── Section 5: Typed errors ──────────────────────────────────────────────
	fmt.Println("\n── Section 5: Typed error handling ──")

	// A non-existent directory → DirReadError.
	missingDir := ports.NewDir(filepath.Join(root, "does-not-exist"))
	_, err = missingDir.List(nil, ports.DirOptions{})
	var readErr ports.DirReadError
	if errors.As(err, &readErr) {
		fmt.Printf("  DirReadError: path=%q\n", readErr.Path)
	}

	// A structurally-matching but codec-invalid entry → DirEntryParamError
	// (distinct from README.md's silent exclusion above — EntryPattern
	// acts as BOTH a filter for non-matching shapes AND a validator for
	// matching-but-invalid ones).
	writeReading(filepath.Join(reportsDir, "ab.json"), Reading{Value: 0, Unit: "celsius"})
	_, err = reportsListing.List(nil, ports.DirOptions{})
	var paramErr ports.DirEntryParamError
	if errors.As(err, &paramErr) {
		fmt.Printf("  DirEntryParamError: entry=%q param=%q value=%q\n", paramErr.Entry, paramErr.Name, paramErr.Value)
	}

	// ── Section 6: CreateIfMissing — auto-creating a missing directory ──────
	//
	// By default List on a non-existent directory returns DirReadError
	// (shown just above via missingDir) — DirOptions.CreateIfMissing opts
	// into creating it first, so a first-run "workspace" directory doesn't
	// need a separate os.MkdirAll call in application code.
	fmt.Println("\n── Section 6: DirOptions.CreateIfMissing — auto-creating a missing directory ──")

	freshDir := ports.NewDir(filepath.Join(root, "reports", "2024-fresh"))
	freshEntries, err := freshDir.List(nil, ports.DirOptions{CreateIfMissing: true})
	if err != nil {
		panic(err)
	}
	fmt.Printf("  freshly-created directory lists %d entries (empty, not an error)\n", len(freshEntries))
}
