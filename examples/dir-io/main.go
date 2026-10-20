// Package main demonstrates [file.Dir]: the declarative directory-listing
// capability — a codec-validated "ls", not "cat" — and how it composes
// into a pipeline via [ports.SourcePort].
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
//   - [file.NewDir] + [file.WithEntryPattern] — declare once, list many:
//     each entry's filename is parsed/validated the SAME way
//     [file.Resource.MatchPath] parses a full file path; non-matching
//     entries (the stray README.md) are silently excluded, not returned
//     as errors
//   - [file.Dir.BuildPath] / [file.Dir.MatchPath] — validate the
//     DIRECTORY's own location (an {year} path segment), independent of
//     what's found inside it; PURE, no-I/O methods on [file.Dir] itself
//     (no Client/Backend needed for these two)
//   - [file.WithRecursive] — descend into the archive/{year}/ subtree,
//     with an [file.EntryPattern] spanning subdirectory segments
//   - Composing [file.Dir]'s one-shot [Client.ListDir] discovery with a
//     [ports.SourcePort] pipeline: List finds WHICH sensors exist, then a
//     channel-backed source streams that discovery through a pipeline
//     that reads each sensor's own reading file via [Client.Read] — Dir
//     has no adapter binding of its own (by design — a directory listing
//     has no single typed payload), so it composes via the SAME
//     [ports.ChanSourceAdapter] any in-memory data source would use
//     (ports.SourcePort/ChanSourceAdapter are generic pipeline
//     primitives, unrelated to the File/Dir-vs-ports.File/ports.Dir
//     migration this example otherwise demonstrates)
//   - Typed errors: [file.DirReadError], [file.DirEntryParamError],
//     [file.DirPathMismatchError] — navigate via [errors.As]
//   - [file.DirOptions.CreateIfMissing] — opt-in auto-creation of a
//     missing directory before listing (default off, matches
//     [file.Resource]'s own "never auto-create" default)
//   - [Client.DeleteDir] — idempotent "ensure absent" by default;
//     [file.DirOptions.Strict]/[file.DirOptions.DryRun]/
//     [file.DirOptions.DeleteRecursive] opt-ins for both Delete and
//     List+CreateIfMissing (Strict on Create means the REVERSE
//     precondition of Strict on Delete: the path must NOT already exist)
//   - Glob path template segments (shell-glob "*"/"?"/"[...]" plus "**"
//     globstar, NOT MQTT's "+"/"#") — [Client.ListDir]'s glob-discovery
//     mode finds EVERY matching directory and aggregates their entries;
//     a named "{var}" segment combined with a glob segment acts as a
//     PARTIAL FILTER (supplied narrows, unsupplied is captured per
//     match, merged with any EntryPattern captures into one Vars map);
//     anchoring the glob-discovery walk is done via [file.Router]/
//     [file.WithRouter] (file.Dir has NO WithBaseDir of its own — Router
//     composition is the ONE anchoring mechanism, reused from the same
//     mechanism every other api/file role already uses for Router-group
//     path-prefix composition; an unanchored wildcard-first template
//     walks from cwd instead, exactly as it did before);
//     [file.DirWildcardBuildError] rejects BuildPath/MatchPath/Delete on
//     a glob-enabled template
//
// Run with: go run ./examples/dir-io
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	adapterfile "github.com/DaniDeer/go-codex/adapters/file"
	"github.com/DaniDeer/go-codex/api/file"
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

	ctx := context.Background()
	client := file.NewClient()
	if err := client.Attach(adapterfile.NewBackend()); err != nil {
		panic(err)
	}

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
	fmt.Println("── Section 1: Client.ListDir — discover sensor files (like \"ls\") ──")

	sensorFilePattern := file.EntryPattern{
		Template: "{sensorID}.json",
		Params:   []file.EntryParam{file.EntryParam{Name: "sensorID"}.WithCodec(sensorIDCodec)},
	}
	reportsListing := file.NewDir(reportsDir, file.WithEntryPattern(sensorFilePattern))
	reportsLister := reportsListing.WithList(file.ListDir{})

	// The invalid "ab.json" file is temporarily removed for this first
	// listing so Section 1 shows only the successful-discovery path;
	// Section 5 below re-introduces it to demonstrate DirEntryParamError.
	if err := os.Remove(filepath.Join(reportsDir, "ab.json")); err != nil {
		panic(err)
	}

	entries, err := client.ListDir(ctx, reportsLister, nil)
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
	archiveYearDir := file.NewDir(
		filepath.Join(reportsDir, "archive", "{year}"),
		file.PathParam{Name: "year"}.WithCodec(yearCodec),
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
	var mismatchErr file.DirPathMismatchError
	if errors.As(err, &mismatchErr) {
		fmt.Printf("  DirPathMismatchError: template=%q path=%q\n", mismatchErr.Template, mismatchErr.Path)
	}

	// ── Section 3: Recursive listing — EntryPattern spans subdirectories ────
	fmt.Println("\n── Section 3: Client.ListDir — recursive, EntryPattern spans subdirectories ──")

	archiveListing := file.NewDir(filepath.Join(reportsDir, "archive"),
		file.WithRecursive(true),
		file.WithEntryPattern(file.EntryPattern{
			Template: "{year}/{sensorID}.json",
			Params: []file.EntryParam{
				file.EntryParam{Name: "year"}.WithCodec(yearCodec),
				file.EntryParam{Name: "sensorID"}.WithCodec(sensorIDCodec),
			},
		}),
	)
	archiveLister := archiveListing.WithList(file.ListDir{})
	archiveEntries, err := client.ListDir(ctx, archiveLister, nil)
	if err != nil {
		panic(err)
	}
	for _, e := range archiveEntries {
		fmt.Printf("  archived: year=%q sensor=%q (relPath=%s)\n", e.Vars["year"], e.Vars["sensorID"], e.RelPath)
	}

	// ── Section 4: Pipeline capability — Dir.List discovery feeds a
	// SourcePort ───────────────────────────────────────────────────────────
	//
	// file.Dir has NO adapter binding of its own (a directory listing has
	// no single typed payload the way a Resource's contents do) — but its
	// ONE-SHOT ListDir result composes with a pipeline exactly like any
	// other in-memory data source: feed the discovered sensor IDs through
	// [ports.ChanSourceAdapter], then a downstream stage reads each
	// sensor's OWN reading file via [Client.Read]. Dir decides WHICH files
	// exist; the declared Resource[Reading] decides WHAT's inside each
	// one — two declarative surfaces, cleanly composed.
	fmt.Println("\n── Section 4: Client.ListDir discovery feeding a SourcePort pipeline ──")

	discovered, err := client.ListDir(ctx, reportsLister, nil)
	if err != nil {
		panic(err)
	}

	sensors := codex.Must(ports.NewSourcePort[string]("discovered-sensors", codex.String(), ports.PortOptions{Buffer: len(discovered)}))

	ch := make(chan string, len(discovered))
	for _, e := range discovered {
		ch <- e.Vars["sensorID"]
	}
	close(ch)
	sensors.Bind(ctx, ports.ChanSourceAdapter(ch))

	sensorIDs, _ := gstream.Collect(ctx, sensors.Stream(ctx))
	sensorFile := file.NewResource[Reading](filepath.Join(reportsDir, "{sensorID}.json"), readingFormat,
		file.PathParam{Name: "sensorID"})
	sensorReader := sensorFile.WithRead(file.Read{})
	for _, id := range sensorIDs {
		reading, err := client.Read(ctx, sensorReader, map[string]string{"sensorID": id})
		if err != nil {
			panic(err)
		}
		fmt.Printf("  pipeline read sensor=%q value=%.1f%s\n", id, reading.Value, reading.Unit)
	}

	// ── Section 5: Typed errors ──────────────────────────────────────────────
	fmt.Println("\n── Section 5: Typed error handling ──")

	// A non-existent directory → DirReadError.
	missingDir := file.NewDir(filepath.Join(root, "does-not-exist"))
	missingLister := missingDir.WithList(file.ListDir{})
	_, err = client.ListDir(ctx, missingLister, nil)
	var readErr file.DirReadError
	if errors.As(err, &readErr) {
		fmt.Printf("  DirReadError: path=%q\n", readErr.Path)
	}

	// A structurally-matching but codec-invalid entry → DirEntryParamError
	// (distinct from README.md's silent exclusion above — EntryPattern
	// acts as BOTH a filter for non-matching shapes AND a validator for
	// matching-but-invalid ones).
	writeReading(filepath.Join(reportsDir, "ab.json"), Reading{Value: 0, Unit: "celsius"})
	_, err = client.ListDir(ctx, reportsLister, nil)
	var paramErr file.DirEntryParamError
	if errors.As(err, &paramErr) {
		fmt.Printf("  DirEntryParamError: entry=%q param=%q value=%q\n", paramErr.Entry, paramErr.Name, paramErr.Value)
	}

	// ── Section 6: CreateIfMissing — auto-creating a missing directory ──────
	//
	// By default ListDir on a non-existent directory returns DirReadError
	// (shown just above via missingDir) — DirOptions.CreateIfMissing opts
	// into creating it first, so a first-run "workspace" directory doesn't
	// need a separate os.MkdirAll call in application code.
	fmt.Println("\n── Section 6: DirOptions.CreateIfMissing — auto-creating a missing directory ──")

	freshDir := file.NewDir(filepath.Join(root, "reports", "2024-fresh"))
	freshLister := freshDir.WithList(file.ListDir{})
	freshEntries, err := client.ListDir(ctx, freshLister, nil, file.DirOptions{CreateIfMissing: true})
	if err != nil {
		panic(err)
	}
	fmt.Printf("  freshly-created directory lists %d entries (empty, not an error)\n", len(freshEntries))

	// Strict: refuse to reuse the directory we just created.
	_, err = client.ListDir(ctx, freshLister, nil, file.DirOptions{CreateIfMissing: true, Strict: true})
	var existsErr file.DirAlreadyExistsError
	if errors.As(err, &existsErr) {
		fmt.Printf("  Strict: DirAlreadyExistsError (path=%q), as expected\n", existsErr.Path)
	}

	// DryRun: preview whether creation WOULD be needed, without creating anything.
	dryRunDir := file.NewDir(filepath.Join(root, "reports", "2024-dryrun"))
	dryRunLister := dryRunDir.WithList(file.ListDir{})
	_, err = client.ListDir(ctx, dryRunLister, nil, file.DirOptions{CreateIfMissing: true, DryRun: true})
	var dryRunReadErr file.DirReadError
	if errors.As(err, &dryRunReadErr) {
		fmt.Printf("  DryRun: DirReadError (creation would have been needed), nothing created\n")
	}

	// ── Section 7: Client.DeleteDir — idempotent by default, Strict/DeleteRecursive/DryRun opt-ins ──
	fmt.Println("\n── Section 7: Client.DeleteDir — idempotent by default ──")

	freshDeleter := freshDir.WithDelete(file.DeleteDir{})

	// Delete the empty directory we created above.
	deleted, err := client.DeleteDir(ctx, freshDeleter, nil)
	if err != nil {
		panic(err)
	}
	fmt.Printf("  Delete empty dir: removed %v\n", deleted)

	// Deleting again: idempotent success (no error, empty slice).
	deleted, err = client.DeleteDir(ctx, freshDeleter, nil)
	if err != nil {
		panic(err)
	}
	fmt.Printf("  Delete again: deleted=%v, err=nil (idempotent)\n", deleted)

	// Strict: the directory is already gone — now it's an error.
	_, err = client.DeleteDir(ctx, freshDeleter, nil, file.DirOptions{Strict: true})
	var notFoundErr file.DirNotFoundError
	if errors.As(err, &notFoundErr) {
		fmt.Printf("  Strict Delete: DirNotFoundError (already absent), as expected\n")
	}

	// Non-empty directory without DeleteRecursive: refused.
	reportsDeleter := file.NewDir(reportsDir).WithDelete(file.DeleteDir{})
	_, err = client.DeleteDir(ctx, reportsDeleter, nil)
	var notEmptyErr file.DirNotEmptyError
	if errors.As(err, &notEmptyErr) {
		fmt.Printf("  Delete non-empty without DeleteRecursive: DirNotEmptyError, as expected\n")
	}

	// DryRun + DeleteRecursive: preview every path that WOULD be removed.
	previewDeleted, err := client.DeleteDir(ctx, reportsDeleter, nil, file.DirOptions{DeleteRecursive: true, DryRun: true})
	if err != nil {
		panic(err)
	}
	fmt.Printf("  DryRun + DeleteRecursive: would remove %d path(s), nothing actually removed\n", len(previewDeleted))

	// DeleteRecursive for real: removes the directory and everything inside it.
	finalDeleted, err := client.DeleteDir(ctx, reportsDeleter, nil, file.DirOptions{DeleteRecursive: true})
	if err != nil {
		panic(err)
	}
	fmt.Printf("  DeleteRecursive: removed %d path(s) total\n", len(finalDeleted))

	// ── Section 8: Glob path template segments — discovery across MULTIPLE
	// matching directories ───────────────────────────────────────────────────
	//
	// Unlike Sections 1-3 (one directory, matched exactly), a glob-enabled
	// template — "*"/"?"/"[...]" via path/filepath.Match, plus "**"
	// globstar matching zero-or-more WHOLE segments anywhere — makes
	// Client.ListDir DISCOVER every directory matching the template and
	// aggregate their entries into one result. This mirrors shell glob,
	// NOT MQTT's topic wildcards.
	fmt.Println("\n── Section 8: glob path template segments — Client.ListDir discovery ──")

	logsRoot := filepath.Join(root, "logs")
	for _, app := range []string{"web", "api", "other"} {
		appErrorsDir := filepath.Join(logsRoot, "app-"+app, "errors")
		if err := os.MkdirAll(appErrorsDir, 0o755); err != nil {
			panic(err)
		}
		if err := os.WriteFile(filepath.Join(appErrorsDir, "trace.log"), []byte("boom\n"), 0o644); err != nil {
			panic(err)
		}
	}

	// "*" matches exactly one path segment and may share it with literal
	// text ("app-*"); file.Router/file.WithRouter anchors the
	// glob-discovery walk — a glob-enabled Dir's own template stays
	// UNPREFIXED (declared relative), and the Router's prefix is resolved
	// in ONLY at dispatch time, via .Handle(client, file.WithRouter(rt)) —
	// so an unanchored wildcard-first template never scans unboundedly
	// from cwd by accident.
	appErrorLogs := file.NewDir("logs/app-*/errors")
	rootRouter := file.NewRouter(root)
	appErrorLister := appErrorLogs.WithList(file.ListDir{})
	appErrorHandle, err := appErrorLister.Handle(client, file.WithRouter(rootRouter))
	if err != nil {
		panic(err)
	}
	globEntries, err := client.ListDir(ctx, appErrorHandle, nil)
	if err != nil {
		panic(err)
	}
	fmt.Printf("  \"logs/app-*/errors\" discovered %d entries across every matching app directory\n", len(globEntries))
	for _, e := range globEntries {
		fmt.Printf("    entry=%q vars=%v (glob segments never capture — Vars stays empty unless a named {var} is also present)\n", e.RelPath, e.Vars)
	}

	// "**" (globstar) matches zero or more WHOLE segments, usable anywhere
	// in the template — at most ONE per template (NewDir would PANIC on a
	// second "**"), which keeps matching deterministic prefix/suffix
	// segment arithmetic instead of a general backtracking matcher.
	deepDir := filepath.Join(logsRoot, "app-web", "a", "b", "errors")
	if err := os.MkdirAll(deepDir, 0o755); err != nil {
		panic(err)
	}
	if err := os.WriteFile(filepath.Join(deepDir, "deep.log"), []byte("deep\n"), 0o644); err != nil {
		panic(err)
	}
	globstarLogs := file.NewDir("logs/**/errors")
	globstarLister := globstarLogs.WithList(file.ListDir{})
	globstarHandle, err := globstarLister.Handle(client, file.WithRouter(rootRouter))
	if err != nil {
		panic(err)
	}
	globstarEntries, err := client.ListDir(ctx, globstarHandle, nil)
	if err != nil {
		panic(err)
	}
	fmt.Printf("  \"logs/**/errors\" discovered %d entries across every depth (0+ segments matched by **)\n", len(globstarEntries))

	// "?" (single char) and "[...]" (character class) round out the
	// path/filepath.Match vocabulary — "app-?" matches exactly one char
	// after "app-"; "app-[wa]*" matches a char class before more text.
	// Each directory gets one marker file so List's entry COUNT directly
	// reflects how many directories matched (List returns entries INSIDE
	// matched directories, not the directories themselves).
	for _, name := range []string{"app-1", "app-2", "app-10"} {
		d := filepath.Join(logsRoot, name)
		if err := os.MkdirAll(d, 0o755); err != nil {
			panic(err)
		}
		if err := os.WriteFile(filepath.Join(d, "marker.log"), []byte("x\n"), 0o644); err != nil {
			panic(err)
		}
	}
	questionMarkLogs := file.NewDir("logs/app-?")
	questionMarkLister := questionMarkLogs.WithList(file.ListDir{})
	questionMarkHandle, err := questionMarkLister.Handle(client, file.WithRouter(rootRouter))
	if err != nil {
		panic(err)
	}
	qEntries, err := client.ListDir(ctx, questionMarkHandle, nil)
	if err != nil {
		panic(err)
	}
	fmt.Printf("  \"logs/app-?\" (single char) discovered %d dirs (app-1, app-2) — not \"app-10\" (two chars)\n", len(qEntries))

	charClassLogs := file.NewDir("logs/app-[wa]*")
	charClassLister := charClassLogs.WithList(file.ListDir{})
	charClassHandle, err := charClassLister.Handle(client, file.WithRouter(rootRouter))
	if err != nil {
		panic(err)
	}
	classEntries, err := client.ListDir(ctx, charClassHandle, nil)
	if err != nil {
		panic(err)
	}
	fmt.Printf("  \"logs/app-[wa]*\" (char class) discovered %d entries across app-web/app-api — app-other/app-1/app-2/app-10 don't match\n", len(classEntries))

	// A named "{env}" segment combined with a glob segment in the SAME
	// template: vars acts as a PARTIAL FILTER. Supplied → narrows
	// discovery to that literal value. Unsupplied → each match's value is
	// CAPTURED per directory instead of raising MissingDirPathVarError.
	// Directory-level captures merge with any EntryPattern captures into
	// ONE flat DirEntry.Vars map.
	envLogsRoot := filepath.Join(root, "env-logs")
	for _, env := range []string{"prod", "staging"} {
		appDir := filepath.Join(envLogsRoot, env, "app-web")
		if err := os.MkdirAll(appDir, 0o755); err != nil {
			panic(err)
		}
		if err := os.WriteFile(filepath.Join(appDir, "trace-1.log"), []byte("x\n"), 0o644); err != nil {
			panic(err)
		}
	}
	envAppLogs := file.NewDir("env-logs/{env}/app-*",
		file.PathParam{Name: "env"},
		file.WithEntryPattern(file.EntryPattern{
			Template: "trace-{seq}.log",
			Params:   []file.EntryParam{{Name: "seq"}},
		}),
	)
	envAppLister := envAppLogs.WithList(file.ListDir{})
	envAppHandle, err := envAppLister.Handle(client, file.WithRouter(rootRouter))
	if err != nil {
		panic(err)
	}
	// Supplied "env" filters discovery to ONLY that literal value.
	prodOnly, err := client.ListDir(ctx, envAppHandle, map[string]string{"env": "prod"})
	if err != nil {
		panic(err)
	}
	fmt.Printf("  \"env-logs/{env}/app-*\" with env=prod: %d entries (staging excluded)\n", len(prodOnly))

	// Unsupplied "env" is captured per match — merged with EntryPattern's
	// own "seq" capture into ONE flat Vars map per entry.
	allEnvs, err := client.ListDir(ctx, envAppHandle, nil)
	if err != nil {
		panic(err)
	}
	for _, e := range allEnvs {
		fmt.Printf("    discovered env=%q seq=%q (dir-level + EntryPattern vars merged into one map)\n", e.Vars["env"], e.Vars["seq"])
	}

	// A wildcard-first-segment template (no literal prefix at all) has no
	// safe anchor — WITHOUT a Router prefix it walks from the current
	// working directory, which can be slow on a large tree. Chdir here so
	// the unanchored walk stays scoped to this demo's own temp root.
	oldWD, err := os.Getwd()
	if err != nil {
		panic(err)
	}
	if err := os.Chdir(root); err != nil {
		panic(err)
	}
	unanchoredLogs := file.NewDir("logs/app-*/errors") // no Router — walks from cwd
	unanchoredLister := unanchoredLogs.WithList(file.ListDir{})
	unanchoredEntries, err := client.ListDir(ctx, unanchoredLister, nil)
	if err != nil {
		panic(err)
	}
	fmt.Printf("  \"logs/app-*/errors\" with NO Router: %d entries, walked from cwd (%s)\n", len(unanchoredEntries), root)
	if err := os.Chdir(oldWD); err != nil {
		panic(err)
	}

	// BuildPath/MatchPath reject a glob-enabled template outright — a
	// glob-enabled template can match MULTIPLE paths, so building/matching
	// a SINGLE concrete path is undefined. Use Client.ListDir's
	// glob-discovery mode instead.
	_, err = appErrorLogs.BuildPath(nil)
	var wildcardErr file.DirWildcardBuildError
	if errors.As(err, &wildcardErr) {
		fmt.Printf("  BuildPath on a glob-enabled template: DirWildcardBuildError (template=%q), as expected\n", wildcardErr.Template)
	}
	_, err = appErrorLogs.MatchPath(filepath.Join(root, "logs", "app-web", "errors"))
	if errors.As(err, &wildcardErr) {
		fmt.Printf("  MatchPath on a glob-enabled template: DirWildcardBuildError (template=%q), as expected\n", wildcardErr.Template)
	}

	// Client.DeleteDir ALSO has no glob-discovery mode of its own —
	// deleting MULTIPLE directories matched by one template in a single
	// call is deliberately not supported; rejected the same way BuildPath
	// is.
	appErrorDeleter := appErrorLogs.WithDelete(file.DeleteDir{})
	_, err = client.DeleteDir(ctx, appErrorDeleter, nil)
	if errors.As(err, &wildcardErr) {
		fmt.Printf("  Delete on a glob-enabled template: DirWildcardBuildError (template=%q), as expected\n", wildcardErr.Template)
	}
}
