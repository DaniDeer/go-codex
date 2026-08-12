package ports

import (
	"context"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/internal/templatematch"
	"github.com/DaniDeer/go-codex/stats"
)

// This file implements the "list a directory's entries" port — the
// [File]-equivalent declarative surface for LISTING files/subdirectories
// (like the "ls" shell command) instead of reading one file's typed
// content. See docs/roadmap/directory-listing-port.md for the full design
// rationale (retired once shipped — this file + its godoc are the
// up-to-date reference from then on).

// ── DirPathParam ──────────────────────────────────────────────────────────────

// DirPathParam describes a {varName} placeholder in a [Dir] path template —
// exact mirror of [FilePathParam], but validates the DIRECTORY's own
// location rather than a file's. No Required field, same rationale as
// [FilePathParam]/[TopicParam]/[PathParam]: every template variable must
// always be present.
//
// DirPathParam implements the [DirOpt] interface: pass it directly to [NewDir].
type DirPathParam struct {
	// Name is the placeholder name (without braces) in the path template.
	Name string

	// Description enriches the documentation for this path variable.
	Description string

	// Codec validates the variable value at [Dir.BuildPath] and [Dir.List]
	// time. Nil means no runtime validation.
	Codec *codex.Codec[string]
}

// WithCodec sets the validation codec and returns the updated DirPathParam.
//
//	ports.DirPathParam{Name: "env"}.WithCodec(codex.String().Refine(validate.NonEmptyString))
func (p DirPathParam) WithCodec(c codex.Codec[string]) DirPathParam {
	p.Codec = &c
	return p
}

func (p DirPathParam) applyDir(db *dirBuilder) { db.params = append(db.params, p) }

// ── EntryKind / EntryParam / EntryPattern / DirEntry ─────────────────────────

// EntryKind distinguishes a listed [DirEntry]'s kind — a file or a
// directory. Phase 1 makes no further distinction (symlinks, sockets,
// devices are all reported as EntryFile, matching [os.DirEntry.IsDir]'s
// own binary distinction).
type EntryKind int

const (
	EntryFile EntryKind = iota
	EntryDir
)

// String implements [fmt.Stringer].
func (k EntryKind) String() string {
	if k == EntryDir {
		return "dir"
	}
	return "file"
}

// EntryParam describes a {varName} placeholder in an [EntryPattern]'s
// template — exact mirror of [DirPathParam], scoped to one entry's
// RelPath rather than the directory's own location.
type EntryParam struct {
	Name  string
	Codec *codex.Codec[string]
}

// EntryPattern optionally declares the expected SHAPE for entries inside a
// [Dir], matched against each entry's RelPath (not just its leaf Name) —
// e.g. Template: "{useCase}.json" for a non-recursive listing, or
// "{env}/{useCase}.json" to span subdirectory segments when [WithRecursive]
// is set. RelPath == Name for a non-recursive listing, so a leaf-only
// template behaves identically whether or not the [Dir] is recursive — one
// matching rule, no special-casing.
//
// When a [Dir] has no EntryPattern set, [Dir.List] returns every entry with
// Vars == nil (no parsing attempted, nothing filtered). When set, List uses
// the SAME [internal/templatematch] core [File.MatchPath] uses today to
// extract + validate each entry's variables, and SILENTLY EXCLUDES any
// entry whose RelPath does not match the template's shape at all —
// EntryPattern acts as both a filter AND a parser (e.g. a stray ".gitkeep"
// alongside "{useCase}.json" files never appears in the result).
type EntryPattern struct {
	Template string
	Params   []EntryParam
}

// DirEntry is one listed filesystem entry. When [Dir]'s EntryPattern is
// set, every DirEntry in a [Dir.List] result has ALREADY matched it —
// non-matching entries are excluded, never returned.
type DirEntry struct {
	// Name is the leaf filename, e.g. "temp-sensor.json".
	Name string
	// RelPath is the entry's path relative to the listed directory —
	// equal to Name unless the Dir is Recursive.
	RelPath string
	Kind    EntryKind
	// Vars holds the variables extracted by EntryPattern, keyed by
	// EntryParam.Name. Nil when the Dir has no EntryPattern set.
	Vars map[string]string
}

// ── Dir ───────────────────────────────────────────────────────────────────────

// Dir is a declarative directory-listing descriptor — the [File]-equivalent
// surface for enumerating a directory's entries (files and subdirectories)
// instead of reading one file's typed content. It bundles a path template,
// optional per-variable codecs (mirrors [FilePathParam]), and an optional
// per-entry filename pattern into one reusable value.
//
// Dir mirrors File's declare-once pattern:
//
//   - [NewDir] declares the directory descriptor as a value — no side effects.
//   - [Dir.List] performs the I/O (like the "ls" shell command).
//   - [Dir.BuildPath]/[Dir.MatchPath] substitute/match variables without I/O.
//
// Typical usage — iotedge's config-files directory, where each filename IS
// a use-case name:
//
//	var useCaseDir = ports.NewDir("configs",
//	    ports.WithEntryPattern(ports.EntryPattern{
//	        Template: "{useCase}.json",
//	        Params:   []ports.EntryParam{{Name: "useCase", Codec: &nonEmptyString}},
//	    }),
//	)
//	entries, err := useCaseDir.List(nil, ports.DirOptions{})
//	// entries[i].Vars["useCase"] == "temp-sensor" for "configs/temp-sensor.json"
type Dir struct {
	template  string
	params    []DirPathParam
	entry     EntryPattern
	recursive bool
}

type dirBuilder struct {
	params    []DirPathParam
	entry     EntryPattern
	recursive bool
}

// DirOpt is the sealed option interface for [NewDir].
type DirOpt interface{ applyDir(*dirBuilder) }

type entryPatternOpt struct{ pattern EntryPattern }

func (o entryPatternOpt) applyDir(db *dirBuilder) { db.entry = o.pattern }

// WithEntryPattern declares the per-entry filename pattern for a [Dir] —
// see [EntryPattern]'s own doc for matching/filtering semantics.
func WithEntryPattern(p EntryPattern) DirOpt { return entryPatternOpt{pattern: p} }

type recursiveOpt struct{ recursive bool }

func (o recursiveOpt) applyDir(db *dirBuilder) { db.recursive = o.recursive }

// WithRecursive sets whether [Dir.List] descends into subdirectories (like
// "ls -R"/"find") instead of listing only the immediate directory (the
// default, like plain "ls").
func WithRecursive(recursive bool) DirOpt { return recursiveOpt{recursive: recursive} }

// NewDir creates a [Dir] descriptor from a path template and optional
// [DirOpt] values ([DirPathParam], [WithEntryPattern], [WithRecursive]).
//
// NewDir is infallible — it only captures the spec. Validation of template
// variable names against registered params runs at [Dir.BuildPath]/
// [Dir.List] time.
//
// A template with no {var} placeholders and no vars passed to [Dir.List]
// resolves to that literal path — declare NewDir(".") for "the current
// directory," the same way [NewFile] has no separate "default path"
// mechanism beyond a literal template.
func NewDir(template string, opts ...DirOpt) Dir {
	var db dirBuilder
	for _, opt := range opts {
		opt.applyDir(&db)
	}
	return Dir{
		template:  template,
		params:    db.params,
		entry:     db.entry,
		recursive: db.recursive,
	}
}

// BuildPath substitutes {varName} placeholders with the values in vars and
// validates each against its registered [DirPathParam.Codec]. Returns the
// concrete directory path on success — mirrors [File.BuildPath] exactly.
//
// All template variables must be present in vars; missing variables return
// a [MissingDirPathVarError]. Values are validated before substitution;
// codec failures return a [DirPathParamError].
func (d Dir) BuildPath(vars map[string]string) (string, error) {
	codecMap := make(map[string]*codex.Codec[string], len(d.params))
	for i := range d.params {
		if d.params[i].Codec != nil {
			codecMap[d.params[i].Name] = d.params[i].Codec
		}
	}
	return buildFromDirTemplate(d.template, vars, codecMap)
}

// MatchPath is the inverse of [Dir.BuildPath]: it matches a concrete,
// already-discovered DIRECTORY path (e.g. from the caller's own
// filepath.WalkDir) against the Dir's OWN path template — NOT the
// [EntryPattern], which matches entries INSIDE the directory — and
// returns the extracted variable values, validated against each
// registered [DirPathParam.Codec]. Mirrors [File.MatchPath] exactly.
//
// Returns [DirPathMismatchError] if path does not match the template's
// structure. Returns [DirPathParamError] if an extracted variable fails
// its registered codec.
func (d Dir) MatchPath(path string) (map[string]string, error) {
	vars, err := matchDirTemplate(d.template, path)
	if err != nil {
		return nil, err
	}
	for i := range d.params {
		p := &d.params[i]
		if p.Codec == nil {
			continue
		}
		value := vars[p.Name]
		if err := p.Codec.Validate(value); err != nil {
			return nil, DirPathParamError{Name: p.Name, Value: value, Err: err}
		}
	}
	return vars, nil
}

// DirOptions configures the behaviour of [Dir.List] — mirrors [FileOptions]
// (Observer, Context); a directory listing has no typed payload, so there
// is no format-related option.
type DirOptions struct {
	// Observer, when non-nil, receives a [stats.FileObserver.RecordFileRead]
	// call after List (a directory listing IS a read operation). Type-asserted
	// to [stats.FileObserver] — existing Observer implementations need not
	// implement it. Defaults to [stats.NoopObserver] when nil.
	Observer stats.Observer

	// Context is an optional context. When non-nil and Observer is nil, the
	// observer is resolved via [stats.ObserverFromContext].
	Context context.Context

	// CreateIfMissing, when true, creates the directory (and any missing
	// parents) via [os.MkdirAll] before [Dir.List] reads its entries,
	// instead of returning [DirReadError] for a non-existent path. A
	// freshly-created directory is empty, so List then returns a
	// zero-length slice, not an error. Default false — existing callers
	// see no change unless they opt in.
	CreateIfMissing bool

	// CreatePerm is the permission used when CreateIfMissing creates the
	// directory. Defaults to 0755 when zero. Has no effect when
	// CreateIfMissing is false.
	CreatePerm os.FileMode
}

// List builds the concrete directory path from vars (an empty resolved
// path defaults to "."), reads its entries via [os.ReadDir] (or
// [filepath.WalkDir] when the Dir is [WithRecursive]), and — if an
// [EntryPattern] is declared — matches/validates each entry's RelPath via
// the same core [File.MatchPath] uses, silently excluding non-matching
// entries.
//
// Errors:
//   - [DirPathParamError] / [MissingDirPathVarError] — directory path variable validation failure (no I/O)
//   - [DirReadError] — os.MkdirAll (when CreateIfMissing is set) or os.ReadDir/filepath.WalkDir failure
//   - [DirEntryParamError] — an entry's RelPath matched EntryPattern's template shape but failed a param's codec
func (d Dir) List(vars map[string]string, opts DirOptions) ([]DirEntry, error) {
	obs := opts.Observer
	if obs == nil && opts.Context != nil {
		obs = stats.ObserverFromContext(opts.Context)
	}
	if obs == nil {
		obs = stats.NoopObserver{}
	}

	path, err := d.BuildPath(vars)
	if err != nil {
		return nil, err // DirPathParamError or MissingDirPathVarError — no I/O
	}
	if path == "" {
		path = "."
	}

	start := time.Now()
	if opts.CreateIfMissing {
		perm := opts.CreatePerm
		if perm == 0 {
			perm = 0755
		}
		if err := os.MkdirAll(path, perm); err != nil {
			recordFileRead(obs, path, false, time.Since(start))
			return nil, DirReadError{Path: path, Err: err}
		}
	}

	var entries []DirEntry
	if d.recursive {
		entries, err = d.listRecursive(path)
	} else {
		entries, err = d.listSingleLevel(path)
	}
	if err != nil {
		recordFileRead(obs, path, false, time.Since(start))
		return nil, err
	}
	recordFileRead(obs, path, true, time.Since(start))
	return entries, nil
}

func (d Dir) listSingleLevel(path string) ([]DirEntry, error) {
	osEntries, err := os.ReadDir(path)
	if err != nil {
		return nil, DirReadError{Path: path, Err: err}
	}
	var out []DirEntry
	for _, de := range osEntries {
		name := de.Name()
		kind := EntryFile
		if de.IsDir() {
			kind = EntryDir
		}
		entryVars, included, merr := d.matchEntry(name)
		if merr != nil {
			return nil, merr
		}
		if !included {
			continue
		}
		out = append(out, DirEntry{Name: name, RelPath: name, Kind: kind, Vars: entryVars})
	}
	return out, nil
}

func (d Dir) listRecursive(root string) ([]DirEntry, error) {
	var out []DirEntry
	walkErr := filepath.WalkDir(root, func(p string, de fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if p == root {
			return nil // never list the root directory itself as an entry
		}
		rel, relErr := filepath.Rel(root, p)
		if relErr != nil {
			return relErr
		}
		rel = filepath.ToSlash(rel)
		kind := EntryFile
		if de.IsDir() {
			kind = EntryDir
		}
		entryVars, included, merr := d.matchEntry(rel)
		if merr != nil {
			return merr
		}
		if !included {
			return nil
		}
		out = append(out, DirEntry{Name: de.Name(), RelPath: rel, Kind: kind, Vars: entryVars})
		return nil
	})
	if walkErr != nil {
		if paramErr, ok := walkErr.(DirEntryParamError); ok {
			return nil, paramErr
		}
		return nil, DirReadError{Path: root, Err: walkErr}
	}
	return out, nil
}

// dirEntryMismatch is an internal sentinel distinguishing "this entry's
// RelPath does not match EntryPattern's template shape at all" (silently
// excluded) from a real error. Never leaves this file.
type dirEntryMismatch struct{}

func (dirEntryMismatch) Error() string { return "dir entry does not match pattern" }

// matchEntry applies d's EntryPattern (if any) to relPath. included == true
// means the entry belongs in the List result (with vars populated only if
// an EntryPattern is set); included == false with a nil err means the
// entry's RelPath didn't match the pattern's shape and should be silently
// skipped; a non-nil err is a real [DirEntryParamError] to propagate.
func (d Dir) matchEntry(relPath string) (vars map[string]string, included bool, err error) {
	if d.entry.Template == "" {
		return nil, true, nil
	}
	v, merr := templatematch.MatchNonWildcard(d.entry.Template, relPath, func(_, _ string) error {
		return dirEntryMismatch{}
	})
	if merr != nil {
		if _, ok := merr.(dirEntryMismatch); ok {
			return nil, false, nil
		}
		return nil, false, merr
	}
	for _, p := range d.entry.Params {
		if p.Codec == nil {
			continue
		}
		value := v[p.Name]
		if cerr := p.Codec.Validate(value); cerr != nil {
			return nil, false, DirEntryParamError{Entry: relPath, Name: p.Name, Value: value, Err: cerr}
		}
	}
	return v, true, nil
}

// ── Template engine (Dir's own path template — mirrors file.go's) ───────────

func buildFromDirTemplate(
	template string,
	vars map[string]string,
	codecs map[string]*codex.Codec[string],
) (string, error) {
	var buildErr error
	result := fileTemplateVarRe.ReplaceAllStringFunc(template, func(placeholder string) string {
		if buildErr != nil {
			return placeholder
		}
		name := placeholder[1 : len(placeholder)-1] // strip { and }
		value, ok := vars[name]
		if !ok {
			buildErr = MissingDirPathVarError{Name: name}
			return placeholder
		}
		if c, hasCdc := codecs[name]; hasCdc {
			if err := c.Validate(value); err != nil {
				buildErr = DirPathParamError{Name: name, Value: value, Err: err}
				return placeholder
			}
		}
		return value
	})
	if buildErr != nil {
		return "", buildErr
	}
	return result, nil
}

// matchDirTemplate is the inverse of buildFromDirTemplate — delegates to
// [templatematch.MatchNonWildcard], the same shared core [File.MatchPath]
// uses.
func matchDirTemplate(template, path string) (map[string]string, error) {
	return templatematch.MatchNonWildcard(template, path, func(template, path string) error {
		return DirPathMismatchError{Template: template, Path: path}
	})
}

// ── Typed errors ──────────────────────────────────────────────────────────────

// DirPathParamError is returned by [Dir.BuildPath]/[Dir.MatchPath] when a
// {varName} value fails its [DirPathParam.Codec] validation. Mirrors
// [FilePathParamError] exactly.
type DirPathParamError struct {
	Name  string
	Value string
	Err   error
}

func (e DirPathParamError) Error() string {
	return fmt.Sprintf("dir path variable {%s}: invalid value %q: %s", e.Name, e.Value, e.Err)
}

// Unwrap allows [errors.Is] and [errors.As] to traverse the underlying error.
func (e DirPathParamError) Unwrap() error { return e.Err }

// LogValue implements [slog.LogValuer] for structured logging.
func (e DirPathParamError) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("param", e.Name),
		slog.String("value", e.Value),
		slog.Any("cause", e.Err),
	)
}

// MissingDirPathVarError is returned by [Dir.BuildPath] when a {varName}
// placeholder has no corresponding entry in the vars map. Mirrors
// [MissingFilePathVarError] exactly.
type MissingDirPathVarError struct {
	Name string
}

func (e MissingDirPathVarError) Error() string {
	return fmt.Sprintf("missing value for dir path variable {%s}", e.Name)
}

// LogValue implements [slog.LogValuer] for structured logging.
func (e MissingDirPathVarError) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("param", e.Name),
	)
}

// DirPathMismatchError is returned by [Dir.MatchPath] when a concrete
// directory path does not match the template's structure. Mirrors
// [FilePathMismatchError] exactly.
type DirPathMismatchError struct {
	Template string
	Path     string
}

func (e DirPathMismatchError) Error() string {
	return fmt.Sprintf("dir path %q does not match template %q", e.Path, e.Template)
}

// LogValue implements [slog.LogValuer] for structured logging.
func (e DirPathMismatchError) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("template", e.Template),
		slog.String("path", e.Path),
	)
}

// DirReadError is returned by [Dir.List] when [os.ReadDir]/
// [filepath.WalkDir] fails. Mirrors [FileReadError].
type DirReadError struct {
	Path string
	Err  error
}

func (e DirReadError) Error() string { return fmt.Sprintf("read dir %q: %s", e.Path, e.Err) }

// Unwrap allows [errors.Is] and [errors.As] to traverse the underlying error.
func (e DirReadError) Unwrap() error { return e.Err }

// LogValue implements [slog.LogValuer] for structured logging.
func (e DirReadError) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("path", e.Path),
		slog.Any("cause", e.Err),
	)
}

// DirEntryParamError is returned by [Dir.List] when an entry's RelPath
// matches [EntryPattern.Template]'s shape but a param's codec fails.
// An entry that does not match the template's shape at all is silently
// excluded — this error only fires for a STRUCTURAL match with a bad
// value, e.g. "{useCase}.json" matching "42.json" but "useCase" failing a
// NonEmptyString/slug constraint.
type DirEntryParamError struct {
	Entry string // the entry's RelPath
	Name  string // the failing EntryParam name
	Value string
	Err   error
}

func (e DirEntryParamError) Error() string {
	return fmt.Sprintf("dir entry %q: variable {%s}: invalid value %q: %s", e.Entry, e.Name, e.Value, e.Err)
}

// Unwrap allows [errors.Is] and [errors.As] to traverse the underlying error.
func (e DirEntryParamError) Unwrap() error { return e.Err }

// LogValue implements [slog.LogValuer] for structured logging.
func (e DirEntryParamError) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("entry", e.Entry),
		slog.String("param", e.Name),
		slog.String("value", e.Value),
		slog.Any("cause", e.Err),
	)
}
