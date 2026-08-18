package ports

import (
	"context"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/internal/templatematch"
	"github.com/DaniDeer/go-codex/stats"
)

// This file implements the directory-listing/deletion port — the
// [File]-equivalent declarative surface for LISTING files/subdirectories
// (like the "ls" shell command) and DELETING a directory (like "rm"/
// "rm -r"), instead of reading/writing one file's typed content. See
// docs/features/ports.md's "Dir" subsection for the full design
// rationale (the original roadmap docs were retired once shipped — this
// file + its godoc, plus docs/features/ports.md, are the up-to-date
// reference from then on).

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

// DirPathTemplate bundles a path template with its declared [DirPathParam]
// variables — the "shape" of a [Dir]'s own path (the SAME state
// [Dir.BuildPath]/[Dir.MatchPath] already use internally, extracted into
// its own value). Mirrors [FilePathTemplate] exactly, one type over —
// this does NOT bundle [WithEntryPattern]/[WithRecursive]/[WithBaseDir],
// which stay separate [DirOpt] values passed to [NewDirFromPathTemplate]
// exactly as they would be to [NewDir].
//
// The plain-string form remains the default and primary way to declare a
// directory — pass a path template string directly to [NewDir], exactly
// as always. Reach for DirPathTemplate ONLY when you find yourself
// declaring the SAME template+params shape for two or more directories
// and want that shape to have exactly one source of truth, or when you
// need to build/match a directory path standalone.
//
// A directory declared via [NewDirFromPathTemplate] is byte-for-byte
// identical to one declared via [NewDir] with the same template and
// [DirPathParam] values passed inline — nothing downstream can tell the
// difference.
type DirPathTemplate struct {
	// Template is the path template, e.g. "devices/{usecase_name}".
	Template string
	// Params holds the path template's variable declarations.
	Params []DirPathParam
}

// NewDirPathTemplate declares a DirPathTemplate from a template and its
// DirPathParam variables.
func NewDirPathTemplate(template string, params ...DirPathParam) DirPathTemplate {
	return DirPathTemplate{Template: template, Params: params}
}

// BuildPath substitutes {varName} placeholders in t.Template with the
// values in vars, validating each against its registered
// [DirPathParam.Codec] (if any). Mirrors [Dir.BuildPath] exactly (same
// underlying engine, same error types).
func (t DirPathTemplate) BuildPath(vars map[string]string) (string, error) {
	if templatematch.IsGlobEnabled(t.Template) {
		return "", DirWildcardBuildError{Template: t.Template}
	}
	codecMap := make(map[string]*codex.Codec[string], len(t.Params))
	for i := range t.Params {
		if t.Params[i].Codec != nil {
			codecMap[t.Params[i].Name] = t.Params[i].Codec
		}
	}
	return buildFromDirTemplate(t.Template, vars, codecMap)
}

// MatchPath is the inverse of [DirPathTemplate.BuildPath] — mirrors
// [Dir.MatchPath] exactly (same underlying engine, same error types).
func (t DirPathTemplate) MatchPath(path string) (map[string]string, error) {
	if templatematch.IsGlobEnabled(t.Template) {
		return nil, DirWildcardBuildError{Template: t.Template}
	}
	vars, err := matchDirTemplate(t.Template, path)
	if err != nil {
		return nil, err
	}
	for i := range t.Params {
		p := &t.Params[i]
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
	baseDir   string
}

type dirBuilder struct {
	params    []DirPathParam
	entry     EntryPattern
	recursive bool
	baseDir   string
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

type baseDirOpt struct{ path string }

func (o baseDirOpt) applyDir(db *dirBuilder) { db.baseDir = o.path }

// WithBaseDir sets the filesystem root [Dir.List]'s glob-discovery mode
// (see [IsGlobEnabled] via the template) walks from, prepended (via
// [path/filepath.Join]) to the template's own literal prefix (the leading
// run of segments containing no "{varName}" placeholder or glob
// metacharacter — see [internal/templatematch.LiteralPrefix]). Defaults
// to "." (the current working directory) when unset — this keeps
// existing literal-prefixed templates' behavior completely unchanged
// (filepath.Join(".", "configs") == "configs"), and gives a
// wildcard-first template (e.g. "*/errors", "**/secret.json") an
// explicit, safe anchor instead of an implicit, unbounded scan from cwd.
//
// Only meaningful for glob-enabled [Dir] templates in [Dir.List]'s
// glob-discovery mode — has no effect on a non-glob template, or on
// [Dir.BuildPath]/[Dir.MatchPath] (which already resolve relative to cwd
// exactly as today).
func WithBaseDir(path string) DirOpt { return baseDirOpt{path: path} }

// NewDir creates a [Dir] descriptor from a path template and optional
// [DirOpt] values ([DirPathParam], [WithEntryPattern], [WithRecursive],
// [WithBaseDir]).
//
// NewDir is infallible — it only captures the spec. Validation of template
// variable names against registered params runs at [Dir.BuildPath]/
// [Dir.List] time.
//
// A template with no {var} placeholders and no vars passed to [Dir.List]
// resolves to that literal path — declare NewDir(".") for "the current
// directory," the same way [NewFile] has no separate "default path"
// mechanism beyond a literal template.
//
// NewDir PANICS if template contains more than one "**" glob segment
// (see [internal/templatematch.ValidateGlobstarCount]), or if a
// [DirPathParam] name collides with an [EntryParam] name declared via
// [WithEntryPattern] — both are structural declaration errors (the
// latter would make a glob-discovery match's merged [DirEntry.Vars]
// map ambiguous), caught immediately rather than silently clobbering a
// value at match time.
func NewDir(template string, opts ...DirOpt) Dir {
	if err := templatematch.ValidateGlobstarCount(template); err != nil {
		panic("ports: NewDir: " + err.Error())
	}
	var db dirBuilder
	for _, opt := range opts {
		opt.applyDir(&db)
	}
	dirNames := make(map[string]bool, len(db.params))
	for _, p := range db.params {
		dirNames[p.Name] = true
	}
	for _, p := range db.entry.Params {
		if dirNames[p.Name] {
			panic(fmt.Sprintf("ports: NewDir: DirPathParam and EntryParam share the name %q — rename one to avoid an ambiguous merged Vars map", p.Name))
		}
	}
	return Dir{
		template:  template,
		params:    db.params,
		entry:     db.entry,
		recursive: db.recursive,
		baseDir:   db.baseDir,
	}
}

// NewDirFromPathTemplate creates a [Dir] descriptor using a pre-built
// [DirPathTemplate] instead of a raw path-template string — see
// [DirPathTemplate]'s doc comment for when to reach for this. Produces
// the IDENTICAL [Dir] [NewDir] would produce from t.Template plus
// t.Params passed inline, since [DirPathParam] already implements
// [DirOpt]. Same panics as [NewDir] apply.
func NewDirFromPathTemplate(t DirPathTemplate, opts ...DirOpt) Dir {
	allOpts := make([]DirOpt, 0, len(t.Params)+len(opts))
	for _, p := range t.Params {
		allOpts = append(allOpts, p)
	}
	allOpts = append(allOpts, opts...)
	return NewDir(t.Template, allOpts...)
}

// BuildPath substitutes {varName} placeholders with the values in vars and
// validates each against its registered [DirPathParam.Codec]. Returns the
// concrete directory path on success — mirrors [File.BuildPath] exactly.
//
// All template variables must be present in vars; missing variables return
// a [MissingDirPathVarError]. Values are validated before substitution;
// codec failures return a [DirPathParamError].
//
// Returns [DirWildcardBuildError] if the template is glob-enabled (see
// [internal/templatematch.IsGlobEnabled]) — a glob-enabled template does
// not describe a single concrete path; use [Dir.List]'s glob-discovery
// mode instead.
func (d Dir) BuildPath(vars map[string]string) (string, error) {
	if templatematch.IsGlobEnabled(d.template) {
		return "", DirWildcardBuildError{Template: d.template}
	}
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
// its registered codec. Returns [DirWildcardBuildError] if the template
// is glob-enabled (see [internal/templatematch.IsGlobEnabled]) — use
// [Dir.List]'s glob-discovery mode instead of matching a single path.
func (d Dir) MatchPath(path string) (map[string]string, error) {
	if templatematch.IsGlobEnabled(d.template) {
		return nil, DirWildcardBuildError{Template: d.template}
	}
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

// DirOptions configures the behaviour of [Dir.List] and [Dir.Delete] —
// mirrors [FileOptions] (Observer, Context); a directory listing has no
// typed payload, so there is no format-related option. Not every field
// applies to every method — e.g. DeleteRecursive only affects Delete,
// CreateIfMissing/CreatePerm only affect List; DryRun/Strict apply to
// both with a method-specific meaning documented on each field below.
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

	// DeleteRecursive, when true, allows [Dir.Delete] to remove a
	// NON-empty directory and everything inside it (os.RemoveAll).
	// Default false: Delete on a non-empty directory returns
	// [DirNotEmptyError] instead of silently recursing. Deliberately
	// SEPARATE from the Dir's own declared [WithRecursive] (a
	// listing-depth option) — a destructive operation must be opted into
	// explicitly, per call, never inherited from an unrelated listing
	// declaration.
	DeleteRecursive bool

	// DryRun, when true:
	//   - on [Dir.Delete]: returns the list of paths that WOULD be
	//     removed WITHOUT actually removing anything. Reports the SAME
	//     errors a real call would (e.g. [DirNotEmptyError]).
	//   - on [Dir.List] (with CreateIfMissing set): skips the mutating
	//     os.MkdirAll call only — a genuinely-missing directory then
	//     naturally surfaces [DirReadError] from List's own os.ReadDir
	//     (the informative "creation would have been needed" signal);
	//     the Strict existence check below still runs regardless.
	// Default false.
	DryRun bool

	// Strict, when true:
	//   - on [Dir.Delete]: requires the directory to have existed —
	//     [DirNotFoundError] instead of idempotent "ensure absent" success.
	//   - on [Dir.List] (only meaningful together with CreateIfMissing):
	//     requires the directory to NOT already exist — [DirAlreadyExistsError]
	//     instead of silently reusing it (the create-side
	//     precondition-mirror of Delete's Strict). Checked via os.Stat
	//     BEFORE os.MkdirAll, so it still fires correctly under DryRun.
	//     Has no effect when CreateIfMissing is false.
	// Default false — existing callers see no change unless they opt in.
	Strict bool
}

// List builds the concrete directory path from vars (an empty resolved
// path defaults to "."), reads its entries via [os.ReadDir] (or
// [filepath.WalkDir] when the Dir is [WithRecursive]), and — if an
// [EntryPattern] is declared — matches/validates each entry's RelPath via
// the same core [File.MatchPath] uses, silently excluding non-matching
// entries.
//
// When d's OWN template is glob-enabled (see
// [internal/templatematch.IsGlobEnabled]), List switches to
// glob-discovery mode instead: it discovers EVERY directory matching the
// glob template (via [filepath.WalkDir], anchored at [WithBaseDir]
// joined with the template's literal prefix) and aggregates their
// entries into one result. In this mode vars remains a filter for named
// "{varName}" segments only (glob segments contribute no vars): a named
// segment supplied in vars narrows discovery to that literal value; an
// unsupplied named segment becomes a per-match capture instead of
// triggering [MissingDirPathVarError]. Each returned [DirEntry]'s Vars
// merges the directory-level captured vars with any [EntryPattern]-
// captured vars. See [Dir.listGlobDiscovery] for the implementation.
//
// [DirOptions.CreateIfMissing]/[DirOptions.DryRun]/[DirOptions.Strict]
// have NO EFFECT in glob-discovery mode — a glob-enabled template
// describes MULTIPLE possible directories, so "create the (one) missing
// directory" has no meaning; List silently ignores all three fields for
// a glob-enabled template instead of erroring.
//
// Errors:
//   - [DirPathParamError] / [MissingDirPathVarError] — directory path variable validation failure (no I/O; non-glob templates only)
//   - [DirAlreadyExistsError] — CreateIfMissing and Strict are both set, and the directory already exists (non-glob templates only)
//   - [DirReadError] — os.MkdirAll (when CreateIfMissing is set) or os.ReadDir/filepath.WalkDir failure
//   - [DirEntryParamError] — an entry's RelPath matched EntryPattern's template shape but failed a param's codec
//   - [DirPathParamError] — (glob-discovery mode) a captured named var failed its registered codec
func (d Dir) List(vars map[string]string, opts DirOptions) ([]DirEntry, error) {
	obs := opts.Observer
	if obs == nil && opts.Context != nil {
		obs = stats.ObserverFromContext(opts.Context)
	}
	if obs == nil {
		obs = stats.NoopObserver{}
	}

	if templatematch.IsGlobEnabled(d.template) {
		return d.listGlobDiscovery(vars, obs)
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
		if opts.Strict {
			if _, statErr := os.Stat(path); statErr == nil {
				recordFileRead(obs, path, false, time.Since(start))
				return nil, DirAlreadyExistsError{Path: path}
			} else if !os.IsNotExist(statErr) {
				recordFileRead(obs, path, false, time.Since(start))
				return nil, DirReadError{Path: path, Err: statErr}
			}
		}
		if !opts.DryRun {
			if err := os.MkdirAll(path, perm); err != nil {
				recordFileRead(obs, path, false, time.Since(start))
				return nil, DirReadError{Path: path, Err: err}
			}
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

// dirGlobMismatch is an internal sentinel distinguishing "this candidate
// directory does not match d's own glob-enabled template at all" (keep
// walking, not an error) from a real error. Never leaves this file.
type dirGlobMismatch struct{}

func (dirGlobMismatch) Error() string { return "dir path does not match glob template" }

// matchDirGlob tests a candidate directory's path (relative to the
// glob-discovery walk's base directory) against d's own glob-enabled
// template. vars acts as a filter for named "{varName}" segments: a
// supplied name must match literally, or the candidate is excluded (not
// an error — the walk continues into/past it). matched == false with a
// nil error means "does not match, keep walking"; a non-nil error is a
// real [DirPathParamError] (a captured named var failed its codec) to
// propagate and abort the walk.
func (d Dir) matchDirGlob(rel string, vars map[string]string) (captured map[string]string, matched bool, err error) {
	captured, merr := templatematch.MatchGlob(d.template, rel, func(_, _ string) error {
		return dirGlobMismatch{}
	})
	if merr != nil {
		if _, ok := merr.(dirGlobMismatch); ok {
			return nil, false, nil
		}
		return nil, false, merr
	}
	for name, want := range vars {
		if got, ok := captured[name]; !ok || got != want {
			return nil, false, nil
		}
	}
	for i := range d.params {
		p := &d.params[i]
		if p.Codec == nil {
			continue
		}
		value := captured[p.Name]
		if cerr := p.Codec.Validate(value); cerr != nil {
			return nil, false, DirPathParamError{Name: p.Name, Value: value, Err: cerr}
		}
	}
	return captured, true, nil
}

// listGlobDiscovery implements [Dir.List]'s glob-discovery mode: it walks
// from [WithBaseDir] (default ".") joined with the template's literal
// prefix (see [internal/templatematch.LiteralPrefix]), testing every
// visited directory against d's own glob-enabled template, and
// aggregates the matching directories' entries into one result. Each
// returned [DirEntry]'s Vars merges the directory-level captured vars
// (from the matched template) with any [EntryPattern]-captured vars.
//
// A template with no "**" is pruned: once a candidate directory's depth
// already equals the template's own segment count, [Dir.listGlobDiscovery]
// does not descend further (that subtree cannot satisfy a fixed-length
// template). A template containing "**" disables this pruning by design
// — its zero-or-more-segments semantics means no fixed maximum depth
// exists to prune against.
func (d Dir) listGlobDiscovery(vars map[string]string, obs stats.Observer) ([]DirEntry, error) {
	start := time.Now()

	baseDir := d.baseDir
	if baseDir == "" {
		baseDir = "."
	}
	prefix := templatematch.LiteralPrefix(d.template)
	walkRoot := filepath.Join(baseDir, prefix)

	tmplSegs := strings.Split(d.template, "/")
	hasGlobstar := false
	for _, seg := range tmplSegs {
		if seg == "**" {
			hasGlobstar = true
			break
		}
	}

	var out []DirEntry
	walkErr := filepath.WalkDir(walkRoot, func(p string, de fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !de.IsDir() {
			return nil // only directories are candidates for d's own template
		}
		rel, relErr := filepath.Rel(baseDir, p)
		if relErr != nil {
			return relErr
		}
		rel = filepath.ToSlash(rel)
		if rel == "." {
			rel = ""
		}

		var relSegLen int
		if rel != "" {
			relSegLen = len(strings.Split(rel, "/"))
		}

		dirVars, matched, merr := d.matchDirGlob(rel, vars)
		if merr != nil {
			return merr
		}
		if matched {
			var entries []DirEntry
			if d.recursive {
				entries, err = d.listRecursive(p)
			} else {
				entries, err = d.listSingleLevel(p)
			}
			if err != nil {
				return err
			}
			for i := range entries {
				if len(dirVars) == 0 {
					continue
				}
				merged := make(map[string]string, len(dirVars)+len(entries[i].Vars))
				for k, v := range dirVars {
					merged[k] = v
				}
				for k, v := range entries[i].Vars {
					merged[k] = v
				}
				entries[i].Vars = merged
			}
			out = append(out, entries...)
		}
		if !hasGlobstar && relSegLen >= len(tmplSegs) {
			return filepath.SkipDir
		}
		return nil
	})
	if walkErr != nil {
		recordFileRead(obs, walkRoot, false, time.Since(start))
		if paramErr, ok := walkErr.(DirPathParamError); ok {
			return nil, paramErr
		}
		return nil, DirReadError{Path: walkRoot, Err: walkErr}
	}
	recordFileRead(obs, walkRoot, true, time.Since(start))
	return out, nil
}

// Delete removes the directory at the built path (and, if
// [DirOptions.DeleteRecursive] is set, everything inside it). By
// default, Delete is idempotent "ensure absent" semantics: a missing
// directory is not an error (returns a nil, empty slice). Set
// [DirOptions.Strict] to require the directory to have existed — a
// missing directory then returns [DirNotFoundError] instead. A
// non-empty directory is refused with [DirNotEmptyError] UNLESS
// DeleteRecursive is true.
//
// deleted is the list of concrete paths removed — or, under
// [DirOptions.DryRun], the list that WOULD have been removed, with
// nothing actually deleted. Non-recursive: at most one path (the
// directory itself, once confirmed empty). Recursive: every path under
// the tree (root included).
//
// Delete has no glob-discovery mode of its own — a glob-enabled template
// (see [internal/templatematch.IsGlobEnabled]) is rejected outright with
// [DirWildcardBuildError] (via Delete's internal [Dir.BuildPath] call):
// deleting MULTIPLE directories matched by one template in a single call
// is deliberately not supported; discover matches via [Dir.List]'s
// glob-discovery mode first, then call Delete per discovered path.
//
// Errors:
//   - [DirPathParamError] / [MissingDirPathVarError] / [DirWildcardBuildError] — directory path variable validation failure (no I/O)
//   - [DirNotFoundError] — directory did not exist AND [DirOptions.Strict] is set
//   - [DirNotEmptyError] — non-empty directory, DeleteRecursive not set
//   - [DirDeleteError] — os.ReadDir/os.Remove/os.RemoveAll failure (e.g. permission denied)
func (d Dir) Delete(vars map[string]string, opts DirOptions) (deleted []string, err error) {
	obs := opts.Observer
	if obs == nil && opts.Context != nil {
		obs = stats.ObserverFromContext(opts.Context)
	}
	if obs == nil {
		obs = stats.NoopObserver{}
	}

	path, buildErr := d.BuildPath(vars)
	if buildErr != nil {
		return nil, buildErr
	}
	if path == "" {
		path = "."
	}

	start := time.Now()

	info, statErr := os.Stat(path)
	if statErr != nil {
		if os.IsNotExist(statErr) {
			if opts.Strict {
				recordFileDelete(obs, path, false, time.Since(start))
				return nil, DirNotFoundError{Path: path}
			}
			recordFileDelete(obs, path, true, time.Since(start))
			return nil, nil
		}
		recordFileDelete(obs, path, false, time.Since(start))
		return nil, DirDeleteError{Path: path, Err: statErr}
	}
	if !info.IsDir() {
		recordFileDelete(obs, path, false, time.Since(start))
		return nil, DirDeleteError{Path: path, Err: fmt.Errorf("%s is not a directory", path)}
	}

	if opts.DeleteRecursive {
		affected, walkErr := collectAllPaths(path)
		if walkErr != nil {
			recordFileDelete(obs, path, false, time.Since(start))
			return nil, DirDeleteError{Path: path, Err: walkErr}
		}
		if opts.DryRun {
			recordFileDelete(obs, path, true, time.Since(start))
			return affected, nil
		}
		if err := os.RemoveAll(path); err != nil {
			recordFileDelete(obs, path, false, time.Since(start))
			return nil, DirDeleteError{Path: path, Err: err}
		}
		recordFileDelete(obs, path, true, time.Since(start))
		return affected, nil
	}

	// Non-recursive: the directory must be empty.
	osEntries, readErr := os.ReadDir(path)
	if readErr != nil {
		recordFileDelete(obs, path, false, time.Since(start))
		return nil, DirDeleteError{Path: path, Err: readErr}
	}
	if len(osEntries) > 0 {
		recordFileDelete(obs, path, false, time.Since(start))
		return nil, DirNotEmptyError{Path: path}
	}

	if opts.DryRun {
		recordFileDelete(obs, path, true, time.Since(start))
		return []string{path}, nil
	}

	if err := os.Remove(path); err != nil {
		recordFileDelete(obs, path, false, time.Since(start))
		return nil, DirDeleteError{Path: path, Err: err}
	}
	recordFileDelete(obs, path, true, time.Since(start))
	return []string{path}, nil
}

// collectAllPaths walks root and returns every path found, root included
// first (filepath.WalkDir's own visiting order) — used by [Dir.Delete]'s
// DeleteRecursive mode to report the full affected-paths list, for both
// a real removal and a DryRun preview.
func collectAllPaths(root string) ([]string, error) {
	var all []string
	err := filepath.WalkDir(root, func(p string, _ fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		all = append(all, p)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return all, nil
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

// DirAlreadyExistsError is returned by [Dir.List] (with CreateIfMissing
// set) when [DirOptions.Strict] is set and the directory already exists
// — no creation is attempted.
type DirAlreadyExistsError struct {
	Path string
}

func (e DirAlreadyExistsError) Error() string {
	return fmt.Sprintf("dir %q already exists (Strict)", e.Path)
}

// LogValue implements [slog.LogValuer] for structured logging.
func (e DirAlreadyExistsError) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("path", e.Path),
	)
}

// DirDeleteError is returned by [Dir.Delete] when os.ReadDir/os.Remove/
// os.RemoveAll fails for a reason OTHER than "already absent" (e.g.
// permission denied).
type DirDeleteError struct {
	Path string
	Err  error
}

func (e DirDeleteError) Error() string { return fmt.Sprintf("delete dir %q: %s", e.Path, e.Err) }

// Unwrap allows [errors.Is] and [errors.As] to traverse the underlying error.
func (e DirDeleteError) Unwrap() error { return e.Err }

// LogValue implements [slog.LogValuer] for structured logging.
func (e DirDeleteError) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("path", e.Path),
		slog.Any("cause", e.Err),
	)
}

// DirNotFoundError is returned by [Dir.Delete] when [DirOptions.Strict]
// is set and the directory did not exist — Delete's default is
// idempotent "ensure absent" success; Strict opts out of that.
type DirNotFoundError struct {
	Path string
}

func (e DirNotFoundError) Error() string {
	return fmt.Sprintf("dir %q does not exist (Strict)", e.Path)
}

// LogValue implements [slog.LogValuer] for structured logging.
func (e DirNotFoundError) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("path", e.Path),
	)
}

// DirNotEmptyError is returned by [Dir.Delete] when the directory
// contains entries and [DirOptions.DeleteRecursive] is not set.
type DirNotEmptyError struct {
	Path string
}

func (e DirNotEmptyError) Error() string {
	return fmt.Sprintf("dir %q is not empty (DeleteRecursive not set)", e.Path)
}

// LogValue implements [slog.LogValuer] for structured logging.
func (e DirNotEmptyError) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("path", e.Path),
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

// DirWildcardBuildError is returned by [Dir.BuildPath]/[Dir.MatchPath]
// when the template contains a glob segment ("**", or a segment with
// '*'/'?'/'[') — see [internal/templatematch.IsGlobEnabled]. Building or
// matching a SINGLE concrete path from a template that can match
// multiple paths is undefined; use [Dir.List]'s glob-discovery mode
// instead.
type DirWildcardBuildError struct {
	Template string
}

func (e DirWildcardBuildError) Error() string {
	return fmt.Sprintf("dir path template %q is glob-enabled: BuildPath/MatchPath has no meaning for a template that can match multiple paths", e.Template)
}

// LogValue implements [slog.LogValuer] for structured logging.
func (e DirWildcardBuildError) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("template", e.Template),
	)
}
