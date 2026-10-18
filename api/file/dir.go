package file

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/format"
	"github.com/DaniDeer/go-codex/internal/middleware"
	"github.com/DaniDeer/go-codex/internal/templatematch"
)

// dirT is the phantom type Dir's underlying [roleCore] is parameterized
// over — Dir carries no domain value at all (it enumerates raw entries,
// never decodes a resource body), so every piece of roleCore[T]'s
// existing machinery (Use/WithCapabilities/WithSecurity/
// RequireCapabilities/ErrorPatterns/checkCoverage/applyRouting/
// middlewareNames/routeTags/buildPathWithRouting) is reused UNCHANGED by
// pinning T to this zero-size type, instead of hand-duplicating a
// parallel, Dir-specific core.
type dirT = struct{}

// EntryKind distinguishes a listed [DirEntry]'s own kind — mirrors
// [github.com/DaniDeer/go-codex/ports.EntryKind] exactly.
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

// EntryParam is a plain alias for [codex.Param] — mirrors
// [github.com/DaniDeer/go-codex/ports.EntryParam] exactly (no wrapper
// type needed; EntryParam is a plain data element inside
// [EntryPattern.Params], never itself a [DirOpt]).
type EntryParam = codex.Param

// EntryPattern optionally declares the expected SHAPE for entries inside
// a [Dir], matched against each entry's RelPath — mirrors
// [github.com/DaniDeer/go-codex/ports.EntryPattern] field-for-field. See
// that type's own doc comment for the full matching/filtering semantics
// (reused verbatim, not redesigned).
type EntryPattern struct {
	Template string
	Params   []EntryParam
}

// DirEntry is one listed filesystem entry — mirrors
// [github.com/DaniDeer/go-codex/ports.DirEntry] field-for-field.
type DirEntry struct {
	Name    string
	RelPath string
	Kind    EntryKind
	Vars    map[string]string
}

// Dir is a PURE declaration — a directory path template, its own
// path-param codecs, and an optional EntryPattern/Recursive/BaseDir —
// mirrors [Resource][T]'s own role exactly (declare-once, no I/O, no
// baked-in backend). ListDir/DeleteDir capability is derived via
// [Dir.WithList]/[Dir.WithDelete] below, NOT methods on Dir itself: a
// single Dir value cannot honestly report ONE [router.Routable]
// RouteMethod for two different verbs — see
// docs/roadmap/file-api.md's "file.Dir" section for the full writeup of
// this correction.
type Dir struct {
	template  string
	params    []PathParam
	entry     EntryPattern
	recursive bool
	baseDir   string
}

type dirBuilder struct {
	resourceOpts []ResourceOpt
	entry        EntryPattern
	recursive    bool
	baseDir      string
}

// DirOpt is the sealed option interface for [NewDir] — [PathParam]
// implements this (a 2nd method alongside its existing [ResourceOpt]
// satisfaction), as do [WithEntryPattern]/[WithRecursive]/[WithBaseDir]'s
// own return values.
type DirOpt interface{ applyDir(*dirBuilder) }

func (p PathParam) applyDir(db *dirBuilder) { db.resourceOpts = append(db.resourceOpts, p) }

type entryPatternDirOpt struct{ pattern EntryPattern }

func (o entryPatternDirOpt) applyDir(db *dirBuilder) { db.entry = o.pattern }

// WithEntryPattern declares the per-entry filename pattern for a [Dir] —
// see [EntryPattern]'s own doc for matching/filtering semantics.
func WithEntryPattern(p EntryPattern) DirOpt { return entryPatternDirOpt{pattern: p} }

type recursiveDirOpt struct{ recursive bool }

func (o recursiveDirOpt) applyDir(db *dirBuilder) { db.recursive = o.recursive }

// WithRecursive sets whether [Client.ListDir] descends into
// subdirectories (like "ls -R"/"find") instead of listing only the
// immediate directory (the default, like plain "ls").
func WithRecursive(recursive bool) DirOpt { return recursiveDirOpt{recursive: recursive} }

type baseDirOpt struct{ path string }

func (o baseDirOpt) applyDir(db *dirBuilder) { db.baseDir = o.path }

// WithBaseDir sets the filesystem root [Client.ListDir]'s glob-discovery
// mode (triggered by a glob-enabled template) walks from. Defaults to
// "." when unset. Has no effect on a non-glob template.
func WithBaseDir(path string) DirOpt { return baseDirOpt{path: path} }

// NewDir creates a [Dir] descriptor from a path template and optional
// [DirOpt] values. Infallible — only captures the spec; validation runs
// at dispatch time.
//
// NewDir PANICS if template contains more than one "**" glob segment
// (see [internal/templatematch.ValidateGlobstarCount]) — mirrors
// [NewResource]'s identical declare-time structural-template safety
// net.
func NewDir(template string, opts ...DirOpt) Dir {
	if err := templatematch.ValidateGlobstarCount(template); err != nil {
		panic("api/file: NewDir: " + err.Error())
	}
	var db dirBuilder
	for _, o := range opts {
		o.applyDir(&db)
	}
	r := NewResource[dirT](template, format.Format[dirT]{}, db.resourceOpts...)
	dirNames := make(map[string]bool, len(r.params))
	for _, p := range r.params {
		dirNames[p.Name] = true
	}
	for _, p := range db.entry.Params {
		if dirNames[p.Name] {
			panic(fmt.Sprintf("api/file: NewDir: PathParam and EntryParam share the name %q — rename one to avoid an ambiguous merged Vars map", p.Name))
		}
	}
	return Dir{template: r.template, params: r.params, entry: db.entry, recursive: db.recursive, baseDir: db.baseDir}
}

// resource builds the Resource[dirT] roleCore's path-templating machinery
// needs (buildPathWithRouting/applyWithRouterPrefix) — zero format, zero
// mergeFields, since Dir never decodes a value.
func (d Dir) resource() Resource[dirT] {
	return Resource[dirT]{template: d.template, params: d.params}
}

// BuildPath substitutes {varName} placeholders in d's template with vars,
// validating each against its registered [PathParam.Codec]. Returns
// [DirWildcardBuildError] if the template is glob-enabled.
func (d Dir) BuildPath(vars map[string]string) (string, error) {
	if templatematch.IsGlobEnabled(d.template) {
		return "", DirWildcardBuildError{Template: d.template}
	}
	return d.resource().buildPathWithRouting(routingInfo{}, vars)
}

// MatchPath is the inverse of [Dir.BuildPath] — matches a concrete,
// already-discovered directory path against d's own template.
func (d Dir) MatchPath(path string) (map[string]string, error) {
	if templatematch.IsGlobEnabled(d.template) {
		return nil, DirWildcardBuildError{Template: d.template}
	}
	vars, err := templatematch.MatchNonWildcard(d.template, path, func(template, path string) error {
		return DirPathMismatchError{Template: template, Path: path}
	})
	if err != nil {
		return nil, err
	}
	if err := convertParamErr(codex.ValidateParams(toCodexParams(d.params), vars)); err != nil {
		return nil, err
	}
	return vars, nil
}

// dirEntryMismatch is an internal sentinel distinguishing "this entry's
// RelPath does not match EntryPattern's template shape at all" (silently
// excluded) from a real error. Never leaves this file.
type dirEntryMismatch struct{}

func (dirEntryMismatch) Error() string { return "dir entry does not match pattern" }

// matchEntry applies d's EntryPattern (if any) to relPath — mirrors
// [github.com/DaniDeer/go-codex/ports.Dir]'s identical matchEntry logic.
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

// dirGlobMismatch is an internal sentinel distinguishing "this candidate
// directory does not match d's own glob-enabled template at all" (keep
// walking, not an error) from a real error. Never leaves this file.
type dirGlobMismatch struct{}

func (dirGlobMismatch) Error() string { return "dir path does not match glob template" }

// matchDirGlob mirrors [github.com/DaniDeer/go-codex/ports.Dir]'s
// identical matchDirGlob logic.
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

// ── Role metadata + role-deriving methods ───────────────────────────────

type ListDir struct{ Summary, Description string }
type DeleteDir struct{ Summary, Description string }

// WithList derives an independent [ListDirer] role value from d —
// mirrors [Resource.WithRead] exactly.
func (d Dir) WithList(op ListDir) ListDirer {
	return ListDirer{core: roleCore[dirT]{resource: d.resource()}, dir: d, op: op}
}

// WithDelete derives an independent [DeleteDirer] role value from d —
// mirrors [Resource.WithDelete] exactly.
func (d Dir) WithDelete(op DeleteDir) DeleteDirer {
	return DeleteDirer{core: roleCore[dirT]{resource: d.resource()}, dir: d, op: op}
}

// DirOptions configures [Client.ListDir]/[Client.DeleteDir] — mirrors
// [github.com/DaniDeer/go-codex/ports.DirOptions] MINUS Observer/Context
// (api/file resolves Observer from ctx uniformly via
// [stats.ObserverFromContext]).
type DirOptions struct {
	CreateIfMissing bool
	CreatePerm      os.FileMode
	DeleteRecursive bool
	DryRun          bool
	Strict          bool
}

// ── ListDirer ────────────────────────────────────────────────────────────

type ListDirer struct {
	core roleCore[dirT]
	dir  Dir
	op   ListDir
}

func (l ListDirer) Use(mws ...RouteMiddleware) ListDirer {
	l.core = l.core.withUse(mws)
	return l
}
func (l ListDirer) ErrorPatterns(patterns any) ListDirer {
	l.core = l.core.withErrorPatterns(patterns)
	return l
}
func (l ListDirer) WithCapabilities(caps ...Capability) ListDirer {
	l.core = l.core.withCapabilities(caps)
	return l
}
func (l ListDirer) WithSecurity(reqs ...SecurityRequirement) ListDirer {
	l.core = l.core.withSecurity(reqs)
	return l
}
func (l ListDirer) RequireCapabilities(reqs ...CapabilityRequirement) ListDirer {
	l.core = l.core.withRequirements(reqs)
	return l
}

func (l ListDirer) Handle(client *Client, opts ...HandleOpt) (*ListDirHandle, error) {
	if l.core.buildErr != nil {
		return nil, l.core.buildErr
	}
	if err := l.core.checkCoverage(); err != nil {
		return nil, err
	}
	h := &ListDirHandle{dir: l.dir, core: l.core, client: client}
	for _, o := range opts {
		o.applyHandle(&h.routing)
	}
	h.core = h.core.applyRouting(h.routing)
	return h, nil
}

type ListDirHandle struct {
	dir     Dir
	core    roleCore[dirT]
	client  *Client
	routing routingInfo
}

// ── DeleteDirer ──────────────────────────────────────────────────────────

type DeleteDirer struct {
	core roleCore[dirT]
	dir  Dir
	op   DeleteDir
}

func (d DeleteDirer) Use(mws ...RouteMiddleware) DeleteDirer {
	d.core = d.core.withUse(mws)
	return d
}
func (d DeleteDirer) ErrorPatterns(patterns any) DeleteDirer {
	d.core = d.core.withErrorPatterns(patterns)
	return d
}
func (d DeleteDirer) WithCapabilities(caps ...Capability) DeleteDirer {
	d.core = d.core.withCapabilities(caps)
	return d
}
func (d DeleteDirer) WithSecurity(reqs ...SecurityRequirement) DeleteDirer {
	d.core = d.core.withSecurity(reqs)
	return d
}
func (d DeleteDirer) RequireCapabilities(reqs ...CapabilityRequirement) DeleteDirer {
	d.core = d.core.withRequirements(reqs)
	return d
}

func (d DeleteDirer) Handle(client *Client, opts ...HandleOpt) (*DeleteDirHandle, error) {
	if d.core.buildErr != nil {
		return nil, d.core.buildErr
	}
	if err := d.core.checkCoverage(); err != nil {
		return nil, err
	}
	h := &DeleteDirHandle{dir: d.dir, core: d.core, client: client}
	for _, o := range opts {
		o.applyHandle(&h.routing)
	}
	h.core = h.core.applyRouting(h.routing)
	return h, nil
}

type DeleteDirHandle struct {
	dir     Dir
	core    roleCore[dirT]
	client  *Client
	routing routingInfo
}

// ── router.Routable[*Client] implementations ─────────────────────────────

func applyDirWithRouterPrefix(dir Dir, core roleCore[dirT], prefix string, mws []middleware.RouteMiddleware, tags []string) (Dir, roleCore[dirT]) {
	dir.template = joinRouterPath(prefix, dir.template)
	core = applyWithRouterPrefix(core, prefix, mws, tags)
	core.resource.template = dir.template
	return dir, core
}

func (l ListDirer) WithRouterPrefix(prefix string, mws []middleware.RouteMiddleware, tags []string) (routable, string) {
	l.dir, l.core = applyDirWithRouterPrefix(l.dir, l.core, prefix, mws, tags)
	return l, l.dir.template
}
func (l ListDirer) MiddlewareNames() []string { return l.core.middlewareNames() }
func (l ListDirer) RouteTags() []string       { return l.core.routeTags() }
func (l ListDirer) RegisterAny(c *Client) error {
	_, err := l.Handle(c)
	return err
}
func (l ListDirer) RouteMethod() string { return "list-dir" }

func (d DeleteDirer) WithRouterPrefix(prefix string, mws []middleware.RouteMiddleware, tags []string) (routable, string) {
	d.dir, d.core = applyDirWithRouterPrefix(d.dir, d.core, prefix, mws, tags)
	return d, d.dir.template
}
func (d DeleteDirer) MiddlewareNames() []string { return d.core.middlewareNames() }
func (d DeleteDirer) RouteTags() []string       { return d.core.routeTags() }
func (d DeleteDirer) RegisterAny(c *Client) error {
	_, err := d.Handle(c)
	return err
}
func (d DeleteDirer) RouteMethod() string { return "delete-dir" }

var (
	_ routable = ListDirer{}
	_ routable = DeleteDirer{}
)

// ── Client dispatch ───────────────────────────────────────────────────────

type listDirDispatcher interface {
	dispatchListDir(ctx context.Context, backend Backend, vars map[string]string, opts DirOptions) ([]DirEntry, error)
}

func (l ListDirer) dispatchListDir(ctx context.Context, backend Backend, vars map[string]string, opts DirOptions) ([]DirEntry, error) {
	return listDirCore(ctx, backend, l.core, l.dir, routingInfo{}, vars, opts)
}
func (h *ListDirHandle) dispatchListDir(ctx context.Context, backend Backend, vars map[string]string, opts DirOptions) ([]DirEntry, error) {
	return listDirCore(ctx, backend, h.core, h.dir, h.routing, vars, opts)
}

type deleteDirDispatcher interface {
	dispatchDeleteDir(ctx context.Context, backend Backend, vars map[string]string, opts DirOptions) ([]string, error)
}

func (d DeleteDirer) dispatchDeleteDir(ctx context.Context, backend Backend, vars map[string]string, opts DirOptions) ([]string, error) {
	return deleteDirCore(ctx, backend, d.core, d.dir, routingInfo{}, vars, opts)
}
func (h *DeleteDirHandle) dispatchDeleteDir(ctx context.Context, backend Backend, vars map[string]string, opts DirOptions) ([]string, error) {
	return deleteDirCore(ctx, backend, h.core, h.dir, h.routing, vars, opts)
}

func dispatchListDir(ctx context.Context, backend Backend, l any, vars map[string]string, opts DirOptions) ([]DirEntry, error) {
	v, ok := l.(listDirDispatcher)
	if !ok {
		return nil, BackendTypeMismatchError{Expected: "file.ListDirer or *file.ListDirHandle", Got: typeName(l)}
	}
	return v.dispatchListDir(ctx, backend, vars, opts)
}

func dispatchDeleteDir(ctx context.Context, backend Backend, d any, vars map[string]string, opts DirOptions) ([]string, error) {
	v, ok := d.(deleteDirDispatcher)
	if !ok {
		return nil, BackendTypeMismatchError{Expected: "file.DeleteDirer or *file.DeleteDirHandle", Got: typeName(d)}
	}
	return v.dispatchDeleteDir(ctx, backend, vars, opts)
}

// ── Dispatch cores ────────────────────────────────────────────────────────

func dirBuildPath(dir Dir, routing routingInfo, vars map[string]string) (string, error) {
	r := dir.resource()
	path, err := r.buildPathWithRouting(routing, vars)
	if err != nil {
		return "", err
	}
	if path == "" {
		return ".", nil
	}
	return path, nil
}

func listDirCore(ctx context.Context, backend Backend, core roleCore[dirT], dir Dir, routing routingInfo, vars map[string]string, opts DirOptions) (entries []DirEntry, err error) {
	defer func() { err = applyErrorPattern(core, err) }()
	obs := obsFromCtx(ctx)

	if templatematch.IsGlobEnabled(dir.template) {
		walkRoot := dirGlobWalkRoot(dir)
		if _, err := runBoundMiddleware(ctx, walkRoot, core, nil, nil); err != nil {
			return nil, err
		}
		ctx, endSpan := startSpan(ctx, obs, "file.listdir", walkRoot)
		defer func() { endSpan(err) }()
		start := time.Now()
		out, gerr := listDirGlobDiscovery(ctx, backend, dir, vars)
		if gerr != nil {
			recordList(obs, walkRoot, false, len(out), time.Since(start))
			return nil, gerr
		}
		recordList(obs, walkRoot, true, len(out), time.Since(start))
		return out, nil
	}

	path, buildErr := dirBuildPath(dir, routing, vars)
	if buildErr != nil {
		return nil, buildErr
	}

	if _, err := runBoundMiddleware(ctx, path, core, nil, nil); err != nil {
		return nil, err
	}

	ctx, endSpan := startSpan(ctx, obs, "file.listdir", path)
	defer func() { endSpan(err) }()
	start := time.Now()

	if opts.CreateIfMissing {
		wire, werr := resolveWire(core)
		if werr != nil {
			recordList(obs, path, false, 0, time.Since(start))
			return nil, werr
		}
		if cerr := checkAccessMode(wire); cerr != nil {
			recordList(obs, path, false, 0, time.Since(start))
			return nil, cerr
		}
		mkOpts := MkdirOptions{DryRun: opts.DryRun}
		if opts.CreatePerm != 0 {
			perm := opts.CreatePerm
			mkOpts.NativePOSIXMode = &perm
		}
		if opts.Strict {
			if _, statErr := backend.Stat(ctx, path); statErr == nil {
				recordList(obs, path, false, 0, time.Since(start))
				return nil, DirAlreadyExistsError{Path: path}
			} else if !isNotFound(statErr) {
				recordList(obs, path, false, 0, time.Since(start))
				return nil, DirReadError{Path: path, Err: statErr}
			}
		}
		if !opts.DryRun {
			if _, merr := backend.Mkdir(ctx, path, mkOpts); merr != nil {
				recordList(obs, path, false, 0, time.Since(start))
				return nil, DirReadError{Path: path, Err: merr}
			}
		}
	}

	var out []DirEntry
	if dir.recursive {
		if wb, ok := backend.(WalkingBackend); ok {
			out, err = listDirRecursiveWalking(ctx, wb, dir, path)
		} else {
			out, err = listDirRecursiveManual(ctx, backend, dir, path)
		}
	} else {
		out, err = listDirSingleLevel(ctx, backend, dir, path)
	}
	if err != nil {
		recordList(obs, path, false, len(out), time.Since(start))
		return nil, err
	}
	recordList(obs, path, true, len(out), time.Since(start))
	return out, nil
}

func listDirSingleLevel(ctx context.Context, backend Backend, dir Dir, path string) ([]DirEntry, error) {
	apiEntries, err := backend.ListDir(ctx, path)
	if err != nil {
		return nil, DirReadError{Path: path, Err: err}
	}
	var out []DirEntry
	for _, e := range apiEntries {
		kind := EntryFile
		if e.IsDir {
			kind = EntryDir
		}
		entryVars, included, merr := dir.matchEntry(e.Name)
		if merr != nil {
			return nil, merr
		}
		if !included {
			continue
		}
		out = append(out, DirEntry{Name: e.Name, RelPath: e.Name, Kind: kind, Vars: entryVars})
	}
	return out, nil
}

func listDirRecursiveWalking(ctx context.Context, wb WalkingBackend, dir Dir, root string) ([]DirEntry, error) {
	var out []DirEntry
	walkErr := wb.WalkDir(ctx, root, func(p string, e Entry, err error) error {
		if err != nil {
			return err
		}
		if p == root {
			return nil
		}
		rel, relErr := filepath.Rel(root, p)
		if relErr != nil {
			return relErr
		}
		rel = filepath.ToSlash(rel)
		kind := EntryFile
		if e.IsDir {
			kind = EntryDir
		}
		entryVars, included, merr := dir.matchEntry(rel)
		if merr != nil {
			return merr
		}
		if !included {
			return nil
		}
		out = append(out, DirEntry{Name: e.Name, RelPath: rel, Kind: kind, Vars: entryVars})
		return nil
	})
	if walkErr != nil {
		var paramErr DirEntryParamError
		if errors.As(walkErr, &paramErr) {
			return nil, paramErr
		}
		return nil, DirReadError{Path: root, Err: walkErr}
	}
	return out, nil
}

func listDirRecursiveManual(ctx context.Context, backend Backend, dir Dir, root string) ([]DirEntry, error) {
	var walk func(path, rel string) ([]DirEntry, error)
	walk = func(path, rel string) ([]DirEntry, error) {
		apiEntries, err := backend.ListDir(ctx, path)
		if err != nil {
			return nil, DirReadError{Path: path, Err: err}
		}
		var out []DirEntry
		for _, e := range apiEntries {
			childRel := e.Name
			if rel != "" {
				childRel = rel + "/" + e.Name
			}
			childPath := joinPath(path, e.Name)
			if e.IsDir {
				sub, err := walk(childPath, childRel)
				if err != nil {
					return out, err
				}
				out = append(out, sub...)
				continue
			}
			entryVars, included, merr := dir.matchEntry(childRel)
			if merr != nil {
				return out, merr
			}
			if !included {
				continue
			}
			out = append(out, DirEntry{Name: e.Name, RelPath: childRel, Kind: EntryFile, Vars: entryVars})
		}
		return out, nil
	}
	return walk(root, "")
}

// dirGlobWalkRoot computes the glob-discovery walk's anchor directory —
// [WithBaseDir] (default ".") joined with the template's literal prefix
// (see [internal/templatematch.LiteralPrefix]) — factored out so
// listDirCore can use the SAME value for Observer/span/BoundMiddleware
// logging before listDirGlobDiscovery's own walk even starts.
func dirGlobWalkRoot(dir Dir) string {
	baseDir := dir.baseDir
	if baseDir == "" {
		baseDir = "."
	}
	prefix := templatematch.LiteralPrefix(dir.template)
	return joinPath(baseDir, prefix)
}

func listDirGlobDiscovery(ctx context.Context, backend Backend, dir Dir, vars map[string]string) ([]DirEntry, error) {
	wb, ok := backend.(WalkingBackend)
	if !ok {
		return nil, BackendUnsupportedOptionError{Backend: backend.BackendName(), Option: "WalkingBackend (glob-discovery ListDir)"}
	}
	baseDir := dir.baseDir
	if baseDir == "" {
		baseDir = "."
	}
	walkRoot := dirGlobWalkRoot(dir)

	tmplSegs := strings.Split(dir.template, "/")
	hasGlobstar := false
	for _, seg := range tmplSegs {
		if seg == "**" {
			hasGlobstar = true
			break
		}
	}

	var out []DirEntry
	walkErr := wb.WalkDir(ctx, walkRoot, func(p string, e Entry, err error) error {
		if err != nil {
			return err
		}
		if !e.IsDir {
			return nil
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
		dirVars, matched, merr := dir.matchDirGlob(rel, vars)
		if merr != nil {
			return merr
		}
		if matched {
			var entries []DirEntry
			var lerr error
			if dir.recursive {
				entries, lerr = listDirRecursiveWalking(ctx, wb, dir, p)
			} else {
				entries, lerr = listDirSingleLevel(ctx, backend, dir, p)
			}
			if lerr != nil {
				return lerr
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
		var paramErr DirPathParamError
		if errors.As(walkErr, &paramErr) {
			return nil, paramErr
		}
		return nil, DirReadError{Path: walkRoot, Err: walkErr}
	}
	return out, nil
}

func deleteDirCore(ctx context.Context, backend Backend, core roleCore[dirT], dir Dir, routing routingInfo, vars map[string]string, opts DirOptions) (deleted []string, err error) {
	defer func() { err = applyErrorPattern(core, err) }()
	obs := obsFromCtx(ctx)

	if templatematch.IsGlobEnabled(dir.template) {
		return nil, DirWildcardBuildError{Template: dir.template}
	}

	path, buildErr := dirBuildPath(dir, routing, vars)
	if buildErr != nil {
		return nil, buildErr
	}

	wire, err := resolveWire(core)
	if err != nil {
		return nil, err
	}
	if err := checkAccessMode(wire); err != nil {
		return nil, err
	}

	if _, err := runBoundMiddleware(ctx, path, core, nil, nil); err != nil {
		return nil, err
	}

	ctx, endSpan := startSpan(ctx, obs, "file.deletedir", path)
	defer func() { endSpan(err) }()
	start := time.Now()

	db, ok := backend.(DirDeletingBackend)
	if !ok {
		recordDelete(obs, path, false, time.Since(start))
		return nil, BackendUnsupportedOptionError{Backend: backend.BackendName(), Option: "DirDeletingBackend"}
	}
	affected, delErr := db.DeleteDir(ctx, path, opts.DeleteRecursive, opts.DryRun)
	if delErr != nil {
		if isNotFound(delErr) {
			if opts.Strict {
				recordDelete(obs, path, false, time.Since(start))
				return nil, DirNotFoundError{Path: path}
			}
			recordDelete(obs, path, true, time.Since(start))
			return nil, nil
		}
		var notEmpty DirNotEmptyError
		if errors.As(delErr, &notEmpty) {
			recordDelete(obs, path, false, time.Since(start))
			return nil, notEmpty
		}
		recordDelete(obs, path, false, time.Since(start))
		return nil, DirDeleteError{Path: path, Err: delErr}
	}
	recordDelete(obs, path, true, time.Since(start))
	return affected, nil
}
