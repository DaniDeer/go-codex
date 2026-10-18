package ports

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	apifile "github.com/DaniDeer/go-codex/api/file"
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

// DirPathParam mirrors [codex.Param]'s shape field-for-field — exact mirror
// of [FilePathParam], but validates the DIRECTORY's own location rather
// than a file's; no merge variant exists (unlike [FilePathParam]/
// [MergedFilePathParam]) since a [Dir] never decodes content, so there is
// no T to merge a variable into. Kept as a flat, non-embedded struct
// (rather than embedding codex.Param) so existing
// `ports.DirPathParam{Name: "id"}` struct literals keep compiling
// unchanged (Go requires keyed literal fields to be the struct's OWN
// fields, not promoted ones).
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

// toParam converts p to the shared [codex.Param] shape.
func (p DirPathParam) toParam() codex.Param {
	return codex.Param{Name: p.Name, Description: p.Description, Codec: p.Codec}
}

// toCodexDirParams converts params to []codex.Param for
// [codex.BuildFromParams]/[codex.ValidateParams].
func toCodexDirParams(params []DirPathParam) []codex.Param {
	out := make([]codex.Param, len(params))
	for i, p := range params {
		out[i] = p.toParam()
	}
	return out
}

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
	return buildFromDirTemplate(t.Template, vars, toCodexDirParams(t.Params))
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
	if err := convertDirParamErr(codex.ValidateParams(toCodexDirParams(t.Params), vars)); err != nil {
		return nil, err
	}
	return vars, nil
}

// ── EntryKind / EntryParam / EntryPattern / DirEntry ─────────────────────────

// EntryKind distinguishes a listed [DirEntry]'s kind — a file or a
// directory. Phase 1 makes no further distinction (symlinks, sockets,
// devices are all reported as EntryFile, matching [os.DirEntry.IsDir]'s
// own binary distinction). Aliased to [apifile.EntryKind] (Stage C2's
// ports.Dir-delegates-to-api/file.Dir migration) — field-for-field,
// const-for-const identical; apifile.EntryKind already carries the
// SAME String() method this type used to define locally.
type EntryKind = apifile.EntryKind

const (
	EntryFile = apifile.EntryFile
	EntryDir  = apifile.EntryDir
)

// EntryParam describes a {varName} placeholder in an [EntryPattern]'s
// template — exact mirror of [DirPathParam], scoped to one entry's
// RelPath rather than the directory's own location.
// EntryParam is a type ALIAS for [codex.Param] (the shared, cross-package
// path/topic parameter primitive — see codex/param.go's own doc comment).
// No method is needed here (EntryParam is a plain data element inside
// [EntryPattern.Params], not a [DirOpt]), so — unlike [FilePathParam]/
// [DirPathParam], which need a ports-owned wrapper type to satisfy their
// own sealed opt interfaces — EntryParam can be a straight alias with zero
// wrapper boilerplate; existing `ports.EntryParam{Name: "x", Codec: &c}`
// struct literals keep compiling unchanged (codex.Param has the same
// Name/Codec fields, plus an unused Description field).
type EntryParam = codex.Param

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
// alongside "{useCase}.json" files never appears in the result). Aliased
// to [apifile.EntryPattern] (Stage C2) — field-for-field identical.
type EntryPattern = apifile.EntryPattern

// DirEntry is one listed filesystem entry. When [Dir]'s EntryPattern is
// set, every DirEntry in a [Dir.List] result has ALREADY matched it —
// non-matching entries are excluded, never returned. Aliased to
// [apifile.DirEntry] (Stage C2) — field-for-field identical.
type DirEntry = apifile.DirEntry

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

// WithBaseDir sets the filesystem root [Dir.List]/[Dir.Delete] anchor
// to — applied via [apifile.Router]'s own prefix-composition mechanism
// (Stage C2 of the file-api roadmap's ports.Dir migration: the SAME
// mechanism [apifile.Dir]'s own glob-discovery mode relies on
// internally, reused here rather than a separate, ports-local
// path-joining step). Defaults to "" (a complete no-op prefix,
// equivalent to "." — the current working directory) when unset — this
// keeps every existing non-WithBaseDir caller's behavior completely
// unchanged.
//
// Anchors BOTH glob-enabled [Dir.List] glob-discovery mode AND
// non-glob [Dir.List]/[Dir.Delete] calls (a disclosed, non-breaking
// widening versus this option's original, glob-only scope — nothing
// ever depended on the old restriction, since a non-glob Dir had no
// reason to ever set [WithBaseDir] under the prior implementation). Has
// no effect on [Dir.BuildPath]/[Dir.MatchPath] (pure, no-I/O functions
// that never attach a Backend/Router at all).
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
	return buildFromDirTemplate(d.template, vars, toCodexDirParams(d.params))
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
	if err := convertDirParamErr(codex.ValidateParams(toCodexDirParams(d.params), vars)); err != nil {
		return nil, err
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
// path defaults to "."), and lists its entries — delegating to the
// attached filesystem [apifile.Backend] (via a fresh, internally-held
// [apifile.Client]) for the actual I/O, EntryPattern matching, and
// glob-discovery logic, all of which now live in [apifile.Dir]/
// [apifile.ListDirer] (Stage C2 of the file-api roadmap's ports.Dir
// migration — mirrors [File]'s own earlier delegation to [apifile.Client]
// exactly). d's own [WithBaseDir] is applied via [apifile.Router]'s
// prefix-composition mechanism (the SAME mechanism [apifile.Dir]'s own
// glob-discovery mode relies on internally) rather than a separate,
// ports-local path-joining step — an empty (default) baseDir composes a
// complete no-op prefix, so every existing non-[WithBaseDir] caller's
// behavior is unchanged; a non-empty baseDir now anchors BOTH glob and
// non-glob List calls (previously a no-op for non-glob templates — a
// disclosed, non-breaking widening, since nothing relied on the old
// restriction).
//
// When d's OWN template is glob-enabled (see
// [internal/templatematch.IsGlobEnabled]), List switches to
// glob-discovery mode instead: it discovers EVERY directory matching the
// glob template and aggregates their entries into one result. In this
// mode vars remains a filter for named "{varName}" segments only (glob
// segments contribute no vars): a named segment supplied in vars narrows
// discovery to that literal value; an unsupplied named segment becomes a
// per-match capture instead of triggering [MissingDirPathVarError]. Each
// returned [DirEntry]'s Vars merges the directory-level captured vars
// with any [EntryPattern]-captured vars.
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
//   - [DirReadError] — the attached Backend's own Mkdir/ListDir/WalkDir failure
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
	baseCtx := opts.Context
	if baseCtx == nil {
		baseCtx = context.Background()
	}
	// RecordFileList -> RecordFileRead: ports.Dir.List's own documented,
	// tested contract reuses File.Read's Observer method (predates
	// apifile.FileObserver.RecordFileList, which api/file.Client.ListDir
	// calls internally) — see dirListObserverShim's own doc comment.
	ctx := stats.WithObserver(baseCtx, dirListObserverShim{Observer: obs})

	client := apifile.NewClient()
	_ = client.Attach(osBackend{}) // a fresh Client never fails to Attach

	lister := d.toFileDir().WithList(apifile.ListDir{})
	rt := apifile.NewRouter(d.baseDir)
	handle, err := lister.Handle(client, apifile.WithRouter(rt))
	if err != nil {
		return nil, err
	}

	perm := opts.CreatePerm
	if perm == 0 {
		perm = 0755
	}
	entries, err := client.ListDir(ctx, handle, vars, apifile.DirOptions{
		CreateIfMissing: opts.CreateIfMissing,
		CreatePerm:      perm,
		DryRun:          opts.DryRun,
		Strict:          opts.Strict,
	})
	if err != nil {
		return nil, err
	}
	return entries, nil
}

// dirListObserverShim preserves [Dir.List]'s own pre-existing, tested
// Observer contract (stats.FileObserver.RecordFileRead — the SAME method
// [File.Read] uses) even though [apifile.Client.ListDir]'s own dispatch
// core calls RecordFileList internally (a method that didn't exist yet
// when Dir.List's own convention was established). Embeds
// [stats.NoopObserver] so it satisfies [stats.FileObserver] in full,
// overriding ONLY RecordFileList to forward to the wrapped Observer's
// own RecordFileRead — every other method (including RecordFileDelete,
// used unchanged by [Dir.Delete]) is simply never called by List's own
// dispatch path, so no further forwarding is needed here.
type dirListObserverShim struct {
	stats.NoopObserver
	Observer stats.Observer
}

func (s dirListObserverShim) RecordFileList(path string, success bool, count int, d time.Duration) {
	recordFileRead(s.Observer, path, success, d)
}

// Delete removes the directory at the built path (and, if
// [DirOptions.DeleteRecursive] is set, everything inside it) —
// delegating to the attached filesystem [apifile.Backend] (via a fresh,
// internally-held [apifile.Client]) for the actual I/O, mirroring
// [Dir.List]'s own Stage C2 delegation. By default, Delete is idempotent
// "ensure absent" semantics: a missing directory is not an error
// (returns a nil, empty slice). Set [DirOptions.Strict] to require the
// directory to have existed — a missing directory then returns
// [DirNotFoundError] instead. A non-empty directory is refused with
// [DirNotEmptyError] UNLESS DeleteRecursive is true.
//
// deleted is the list of concrete paths removed — or, under
// [DirOptions.DryRun], the list that WOULD have been removed, with
// nothing actually deleted. Non-recursive: at most one path (the
// directory itself, once confirmed empty). Recursive: every path under
// the tree (root included).
//
// Delete has no glob-discovery mode of its own — a glob-enabled template
// (see [internal/templatematch.IsGlobEnabled]) is rejected outright with
// [DirWildcardBuildError]: deleting MULTIPLE directories matched by one
// template in a single call is deliberately not supported; discover
// matches via [Dir.List]'s glob-discovery mode first, then call Delete
// per discovered path.
//
// Errors:
//   - [DirPathParamError] / [MissingDirPathVarError] / [DirWildcardBuildError] — directory path variable validation failure (no I/O)
//   - [DirNotFoundError] — directory did not exist AND [DirOptions.Strict] is set
//   - [DirNotEmptyError] — non-empty directory, DeleteRecursive not set
//   - [DirDeleteError] — the attached Backend's own DeleteDir failure (e.g. permission denied)
func (d Dir) Delete(vars map[string]string, opts DirOptions) (deleted []string, err error) {
	obs := opts.Observer
	if obs == nil && opts.Context != nil {
		obs = stats.ObserverFromContext(opts.Context)
	}
	if obs == nil {
		obs = stats.NoopObserver{}
	}
	baseCtx := opts.Context
	if baseCtx == nil {
		baseCtx = context.Background()
	}
	// RecordFileDelete matches on BOTH sides already (Dir.Delete's own
	// pre-existing convention and apifile.Client.DeleteDir's own
	// dispatch core both call RecordFileDelete) — no shim needed, unlike
	// List above.
	ctx := stats.WithObserver(baseCtx, obs)

	client := apifile.NewClient()
	_ = client.Attach(osBackend{}) // a fresh Client never fails to Attach

	deleter := d.toFileDir().WithDelete(apifile.DeleteDir{})
	rt := apifile.NewRouter(d.baseDir)
	handle, err := deleter.Handle(client, apifile.WithRouter(rt))
	if err != nil {
		return nil, err
	}

	return client.DeleteDir(ctx, handle, vars, apifile.DirOptions{
		DeleteRecursive: opts.DeleteRecursive,
		DryRun:          opts.DryRun,
		Strict:          opts.Strict,
	})
}

// toFileDir converts d's own declared shape into an [apifile.Dir] value
// — the Stage C2 migration target [Dir.List]/[Dir.Delete] now dispatch
// through. d's own [baseDir] is deliberately NOT folded in here (it is
// applied later, via [apifile.Router], at each call site above) —
// toFileDir only carries the template/params/entry/recursive shape.
func (d Dir) toFileDir() apifile.Dir {
	opts := make([]apifile.DirOpt, 0, len(d.params)+2)
	for _, p := range d.params {
		opts = append(opts, apifile.PathParam{Name: p.Name, Description: p.Description, Codec: p.Codec})
	}
	if d.entry.Template != "" {
		opts = append(opts, apifile.WithEntryPattern(d.entry))
	}
	opts = append(opts, apifile.WithRecursive(d.recursive))
	return apifile.NewDir(d.template, opts...)
}

// ── Template engine (Dir's own path template — mirrors file.go's) ───────────
//
// buildFromDirTemplate used to have its own private regexp-substitution
// loop (sharing file.go's fileTemplateVarRe) — now delegates the actual
// substitution to [codex.BuildFromParams]/[templatematch.Build], the SAME
// canonical function file.go's own buildFromFileTemplate now uses.

// convertDirParamErr converts a [codex.MissingParamError]/[codex.ParamError]
// (returned by [codex.BuildFromParams]/[codex.ValidateParams]) into ports'
// own [MissingDirPathVarError]/[DirPathParamError]. Passes any other error
// (including nil) through unchanged.
func convertDirParamErr(err error) error {
	var me codex.MissingParamError
	if errors.As(err, &me) {
		return MissingDirPathVarError{Name: me.Name}
	}
	var pe codex.ParamError
	if errors.As(err, &pe) {
		return DirPathParamError{Name: pe.Name, Value: pe.Value, Err: pe.Err}
	}
	return err
}

func buildFromDirTemplate(
	template string,
	vars map[string]string,
	params []codex.Param,
) (string, error) {
	result, err := codex.BuildFromParams(template, params, vars)
	if err != nil {
		return "", convertDirParamErr(err)
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
//
// All 10 Dir error types are aliased to their [apifile] counterparts
// (Stage C2 of the file-api roadmap's ports.Dir migration) — mirrors
// [ports/file.go]'s own already-shipped FileXxxError aliasing precedent
// exactly. apifile's own message text was corrected to match THIS
// package's pre-existing, already-tested text verbatim before aliasing
// (ports' contract is the older, already-shipped one; api/file's Dir
// error types were new this session and not yet depended on elsewhere
// in a way that made changing their text risky).

// DirPathParamError is returned by [Dir.BuildPath]/[Dir.MatchPath] when a
// {varName} value fails its [DirPathParam.Codec] validation. Mirrors
// [FilePathParamError] exactly.
type DirPathParamError = apifile.DirPathParamError

// MissingDirPathVarError is returned by [Dir.BuildPath] when a {varName}
// placeholder has no corresponding entry in the vars map. Mirrors
// [MissingFilePathVarError] exactly.
type MissingDirPathVarError = apifile.MissingDirPathVarError

// DirPathMismatchError is returned by [Dir.MatchPath] when a concrete
// directory path does not match the template's structure. Mirrors
// [FilePathMismatchError] exactly.
type DirPathMismatchError = apifile.DirPathMismatchError

// DirReadError is returned by [Dir.List] on a Backend-level ListDir/
// WalkDir/Mkdir/Stat failure. Mirrors [FileReadError].
type DirReadError = apifile.DirReadError

// DirAlreadyExistsError is returned by [Dir.List] (with CreateIfMissing
// set) when [DirOptions.Strict] is set and the directory already exists
// — no creation is attempted.
type DirAlreadyExistsError = apifile.DirAlreadyExistsError

// DirDeleteError is returned by [Dir.Delete] on a genuine Backend-level
// deletion failure OTHER than "already absent" (e.g. permission denied).
type DirDeleteError = apifile.DirDeleteError

// DirNotFoundError is returned by [Dir.Delete] when [DirOptions.Strict]
// is set and the directory did not exist — Delete's default is
// idempotent "ensure absent" success; Strict opts out of that.
type DirNotFoundError = apifile.DirNotFoundError

// DirNotEmptyError is returned by [Dir.Delete] when the directory
// contains entries and [DirOptions.DeleteRecursive] is not set.
type DirNotEmptyError = apifile.DirNotEmptyError

// DirEntryParamError is returned by [Dir.List] when an entry's RelPath
// matches [EntryPattern.Template]'s shape but a param's codec fails.
// An entry that does not match the template's shape at all is silently
// excluded — this error only fires for a STRUCTURAL match with a bad
// value, e.g. "{useCase}.json" matching "42.json" but "useCase" failing a
// NonEmptyString/slug constraint.
type DirEntryParamError = apifile.DirEntryParamError

// DirWildcardBuildError is returned by [Dir.BuildPath]/[Dir.MatchPath]/
// [Dir.Delete] when the template contains a glob segment ("**", or a
// segment with '*'/'?'/'[') — see [internal/templatematch.IsGlobEnabled].
// Building, matching, or deleting a SINGLE concrete path from a template
// that can match multiple paths is undefined; use [Dir.List]'s
// glob-discovery mode instead.
type DirWildcardBuildError = apifile.DirWildcardBuildError
