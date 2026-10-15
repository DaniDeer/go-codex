package file

import (
	"context"
	"errors"
	"os"
	"path"
	"time"

	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/stats"
)

// ── Observer helpers ────────────────────────────────────────────────────

func obsFromCtx(ctx context.Context) stats.Observer {
	obs := stats.ObserverFromContext(ctx)
	if obs == nil {
		return stats.NoopObserver{}
	}
	return obs
}

func recordRead(obs stats.Observer, path string, success bool, d time.Duration) {
	if fo, ok := obs.(stats.FileObserver); ok {
		fo.RecordFileRead(path, success, d)
	}
}

func recordWrite(obs stats.Observer, path string, success bool, d time.Duration) {
	if fo, ok := obs.(stats.FileObserver); ok {
		fo.RecordFileWrite(path, success, d)
	}
}

func recordDelete(obs stats.Observer, path string, success bool, d time.Duration) {
	if fo, ok := obs.(stats.FileObserver); ok {
		fo.RecordFileDelete(path, success, d)
	}
}

func recordList(obs stats.Observer, path string, success bool, count int, d time.Duration) {
	if fo, ok := obs.(stats.FileObserver); ok {
		fo.RecordFileList(path, success, count, d)
	}
}

func recordExists(obs stats.Observer, path string, success bool, d time.Duration) {
	if fo, ok := obs.(stats.FileObserver); ok {
		fo.RecordFileExists(path, success, d)
	}
}

func recordCopy(obs stats.Observer, src, dst string, success bool, d time.Duration) {
	if fo, ok := obs.(stats.FileObserver); ok {
		fo.RecordFileCopy(src, dst, success, d)
	}
}

func recordMove(obs stats.Observer, src, dst string, success bool, d time.Duration) {
	if fo, ok := obs.(stats.FileObserver); ok {
		fo.RecordFileMove(src, dst, success, d)
	}
}

func recordMkdir(obs stats.Observer, path string, success bool, d time.Duration) {
	if fo, ok := obs.(stats.FileObserver); ok {
		fo.RecordFileMkdir(path, success, d)
	}
}

func recordBatchDelete(obs stats.Observer, paths []string, successCount, failureCount int, d time.Duration) {
	if fo, ok := obs.(stats.FileObserver); ok {
		fo.RecordFileBatchDelete(paths, successCount, failureCount, d)
	}
}

// ── TraceObserver spans ─────────────────────────────────────────────────

// startSpan starts a [stats.TraceObserver] span when obs implements it,
// mirroring ports.File's identical span-name convention ("file.read"/
// "file.write"/"file.delete", extended here with "file.list"/
// "file.exists"/"file.copy"/"file.move"/"file.mkdir"/"file.batchdelete"
// for the roles ports.File never had). Returns ctx unchanged and a no-op
// end func when obs doesn't implement TraceObserver, so every call site
// can call this unconditionally, exactly like ports.File's own
// `if to, ok := obs.(stats.TraceObserver); ok { ... }` guard.
func startSpan(ctx context.Context, obs stats.Observer, operation, path string) (context.Context, func(err error)) {
	to, ok := obs.(stats.TraceObserver)
	if !ok {
		return ctx, func(error) {}
	}
	spanCtx := to.StartSpan(ctx, operation, path)
	return spanCtx, func(err error) { to.EndSpan(spanCtx, err) }
}

// ── Capability resolution ───────────────────────────────────────────────

// resolveWire applies core's declared capabilities, returning the
// resulting WireAttributes. The Capability-overrides-option precedence
// rule (see docs/roadmap/file-api.md) is implemented by dispatch
// call sites: when wire.AccessMode/NativePOSIXMode is set (non-zero/
// non-nil), it is used INSTEAD OF whatever a per-call Options field
// says.
func resolveWire[T any](core roleCore[T]) (WireAttributes, error) {
	return ApplyCapabilities(core.capabilities)
}

// checkAccessMode enforces AccessModeReadOnly BEFORE any Backend call —
// the dispatch-layer rejection the roadmap doc's Capability section
// describes.
func checkAccessMode(wire WireAttributes) error {
	if wire.AccessMode == AccessModeReadOnly {
		return BackendUnsupportedOptionError{Backend: "<dispatch>", Option: "AccessMode=ReadOnly: write/delete rejected"}
	}
	return nil
}

// ── not-found translation ───────────────────────────────────────────────

func isNotFound(err error) bool {
	return errors.Is(err, os.ErrNotExist)
}

// ── Path-var merge (MergedPathParam) ────────────────────────────────────

// applyPathMergeFields decodes vars (the SAME map already used to build
// path) into v's own mergeFields-declared fields — mirrors
// rest.PathParam/events.TopicParam's identical "merge extracted vars
// directly into T" promise. A complete no-op when core.resource has no
// mergeFields declared (the common case for a plain [PathParam]).
//
// Read-only for now: List's own per-entry vars would need MATCHING each
// discovered child path against the template (not simply reusing the
// caller's own vars, which address the PARENT directory, not any one
// entry) — a genuinely separate mechanism, scoped out of this fix;
// tracked as a named follow-up, not silently dropped.
func applyPathMergeFields[T any](vars map[string]string, core roleCore[T], v *T) error {
	if len(core.resource.mergeFields) == 0 {
		return nil
	}
	return codex.DecodeVars(v, vars, core.resource.mergeFields...)
}

// entryListVars extends vars (the caller's own PARENT-directory vars)
// with the per-entry vars extracted by matching entryName against the
// resource's own FINAL path-template segment (e.g. "{id}.json") — the
// mechanism List needs to resolve MergedPathParam per DISCOVERED entry,
// since (unlike Read/Create) List's caller-supplied vars address the
// parent directory, not any one entry. A complete no-op (vars returned
// unchanged, nil error) when core.resource has no mergeFields declared —
// the common case today, zero overhead.
//
// Entry-specific vars win over the caller's own on a key collision
// (more specific — mirrors every other "more specific wins" convention
// already established in this package, e.g. Capability-overrides-option
// precedence).
func entryListVars[T any](routing routingInfo, core roleCore[T], vars map[string]string, entryName string) (map[string]string, error) {
	if len(core.resource.mergeFields) == 0 {
		return vars, nil
	}
	fileTemplate := path.Base(routing.resolvedTemplate(core.resource.template))
	entryVars, err := matchFileTemplate(fileTemplate, entryName)
	if err != nil {
		return nil, err
	}
	merged := make(map[string]string, len(vars)+len(entryVars))
	for k, v := range vars {
		merged[k] = v
	}
	for k, v := range entryVars {
		merged[k] = v
	}
	return merged, nil
}

// ── Metadata merge (the file-metadata axis — see MetadataBackend's doc
// comment in backend.go) ───────────────────────────────────────────────

// applyMetadataMergeFields decodes path's stored metadata (via the
// attached Backend's optional [MetadataBackend] extension) into v's own
// metadataMergeFields-declared fields — a complete no-op (v unchanged,
// nil error) when core.resource has no metadataMergeFields declared, OR
// backend doesn't implement MetadataBackend, OR ReadMetadata reports no
// stored metadata for path. Mirrors CopyingBackend/MovingBackend's
// "optionally present, harmless absence" idiom exactly.
func applyMetadataMergeFields[T any](ctx context.Context, backend Backend, path string, core roleCore[T], v *T) error {
	if len(core.resource.metadataMergeFields) == 0 {
		return nil
	}
	mb, ok := backend.(MetadataBackend)
	if !ok {
		return nil
	}
	md, err := mb.ReadMetadata(ctx, path)
	if err != nil {
		return FileReadError{Path: path, Err: err}
	}
	if md == nil {
		return nil
	}
	if err := codex.DecodeVars(v, md, core.resource.metadataMergeFields...); err != nil {
		return FileDecodeError{Path: path, Err: err}
	}
	return nil
}

// deriveWriteMetadata encodes v's metadataMergeFields-declared fields
// into a map[string]string ready for WriteOptions.Metadata — (nil, nil)
// when core.resource has no metadataMergeFields declared. The caller
// passes the result to WriteOptions.Metadata unconditionally; a Backend
// without MetadataBackend already silently ignores that field (see
// WriteOptions.Metadata's own doc comment).
func deriveWriteMetadata[T any](path string, core roleCore[T], v T) (map[string]string, error) {
	if len(core.resource.metadataMergeFields) == 0 {
		return nil, nil
	}
	md, err := codex.EncodeVars(v, core.resource.metadataMergeFields...)
	if err != nil {
		return nil, FileEncodeError{Path: path, Err: err}
	}
	return md, nil
}

// mergeMetadata merges override OVER base (override wins on a key
// collision) — used to layer a BoundMiddleware's own
// WithMetadataOut-contributed metadata atop the resource's own derived
// metadata before the final Backend.WriteBytes call. Returns nil when
// both are empty (never an empty, non-nil map).
func mergeMetadata(base, override map[string]string) map[string]string {
	if len(base) == 0 && len(override) == 0 {
		return nil
	}
	merged := make(map[string]string, len(base)+len(override))
	for k, v := range base {
		merged[k] = v
	}
	for k, v := range override {
		merged[k] = v
	}
	return merged
}

// ── Read ─────────────────────────────────────────────────────────────────

type readDispatcher interface {
	dispatchRead(ctx context.Context, backend Backend, vars map[string]string) (any, error)
}

func (r Reader[T]) dispatchRead(ctx context.Context, backend Backend, vars map[string]string) (any, error) {
	return readCore[T](ctx, backend, r.core, routingInfo{}, vars)
}
func (h *ReadHandle[T]) dispatchRead(ctx context.Context, backend Backend, vars map[string]string) (any, error) {
	return readCore[T](ctx, backend, h.core, h.routing, vars)
}

func readCore[T any](ctx context.Context, backend Backend, core roleCore[T], routing routingInfo, vars map[string]string) (result any, err error) {
	defer func() { err = applyErrorPattern(core, err) }()
	var zero T
	obs := obsFromCtx(ctx)
	path, err := core.resource.buildPathWithRouting(routing, vars)
	if err != nil {
		return zero, err
	}
	if _, err := runBoundMiddleware(ctx, path, core, nil, nil); err != nil {
		return zero, err
	}
	ctx, endSpan := startSpan(ctx, obs, "file.read", path)
	defer func() { endSpan(err) }()
	start := time.Now()
	data, err := backend.ReadBytes(ctx, path)
	if err != nil {
		if isNotFound(err) {
			recordRead(obs, path, false, time.Since(start))
			return zero, FileNotFoundError{Path: path}
		}
		recordRead(obs, path, false, time.Since(start))
		return zero, FileReadError{Path: path, Err: err}
	}
	v, err := core.resource.format.Unmarshal(data)
	if err != nil {
		recordRead(obs, path, false, time.Since(start))
		return zero, FileDecodeError{Path: path, Err: err}
	}
	if err := applyPathMergeFields(vars, core, &v); err != nil {
		recordRead(obs, path, false, time.Since(start))
		return zero, err
	}
	if err := applyMetadataMergeFields(ctx, backend, path, core, &v); err != nil {
		recordRead(obs, path, false, time.Since(start))
		return zero, err
	}
	recordRead(obs, path, true, time.Since(start))
	return v, nil
}

func dispatchRead(ctx context.Context, backend Backend, r any, vars map[string]string) (any, error) {
	v, ok := r.(readDispatcher)
	if !ok {
		return nil, BackendTypeMismatchError{Expected: "file.Reader[T] or *file.ReadHandle[T]", Got: typeName(r)}
	}
	return v.dispatchRead(ctx, backend, vars)
}

// ── Create ───────────────────────────────────────────────────────────────

type createDispatcher interface {
	dispatchCreate(ctx context.Context, backend Backend, val any, vars map[string]string, opts WriteOptions) (createdDirs []string, err error)
}

func (c Creator[T]) dispatchCreate(ctx context.Context, backend Backend, val any, vars map[string]string, opts WriteOptions) ([]string, error) {
	return createCore[T](ctx, backend, c.core, routingInfo{}, val, vars, opts)
}
func (h *CreateHandle[T]) dispatchCreate(ctx context.Context, backend Backend, val any, vars map[string]string, opts WriteOptions) ([]string, error) {
	return createCore[T](ctx, backend, h.core, h.routing, val, vars, opts)
}

func createCore[T any](ctx context.Context, backend Backend, core roleCore[T], routing routingInfo, val any, vars map[string]string, callerOpts WriteOptions) (createdDirs []string, err error) {
	defer func() { err = applyErrorPattern(core, err) }()
	obs := obsFromCtx(ctx)
	v, ok := val.(T)
	if !ok {
		return nil, BackendTypeMismatchError{Expected: typeNameOf[T](), Got: typeName(val)}
	}
	wire, err := resolveWire(core)
	if err != nil {
		return nil, err
	}
	if err := checkAccessMode(wire); err != nil {
		return nil, err
	}
	path, err := core.resource.buildPathWithRouting(routing, vars)
	if err != nil {
		return nil, err
	}
	// md (the caller-intended metadata, derived from v's own
	// metadataMergeFields-tagged fields) is computed BEFORE
	// runBoundMiddleware so an attached BoundCreateMiddleware's
	// WithMetadataIn can decode it into its own In — see
	// Middleware's own doc comment for the full rationale.
	md, err := deriveWriteMetadata(path, core, v)
	if err != nil {
		return nil, err
	}
	boundOutMD, err := runBoundMiddleware(ctx, path, core, &v, md)
	if err != nil {
		return nil, err
	}
	ctx, endSpan := startSpan(ctx, obs, "file.write", path)
	defer func() { endSpan(err) }()
	start := time.Now()
	data, err := core.resource.format.Marshal(v)
	if err != nil {
		recordWrite(obs, path, false, time.Since(start))
		return nil, FileEncodeError{Path: path, Err: err}
	}
	opts := callerOpts
	if wire.NativePOSIXMode != nil {
		opts.Perm = *wire.NativePOSIXMode
	}
	// A bound middleware's own metadata contribution (via
	// WithMetadataOut) wins over the resource-derived md on a key
	// collision — it represents a more specific, runtime-computed value
	// layered on top of the resource's own declared fields.
	opts.Metadata = mergeMetadata(md, boundOutMD)
	createdDirs, err = backend.WriteBytes(ctx, path, data, opts)
	if err != nil {
		recordWrite(obs, path, false, time.Since(start))
		// A Backend already reporting a specific, typed condition (e.g.
		// Strict + already-exists) is surfaced as-is, not double-wrapped
		// in FileWriteError — matches ports.File.Write's own Strict
		// semantics (bare FileAlreadyExistsError, not a wrapped one).
		var alreadyExists FileAlreadyExistsError
		if errors.As(err, &alreadyExists) {
			return createdDirs, alreadyExists
		}
		return createdDirs, FileWriteError{Path: path, Err: err}
	}
	recordWrite(obs, path, true, time.Since(start))
	return createdDirs, nil
}

func dispatchCreate(ctx context.Context, backend Backend, cr any, val any, vars map[string]string, opts WriteOptions) ([]string, error) {
	v, ok := cr.(createDispatcher)
	if !ok {
		return nil, BackendTypeMismatchError{Expected: "file.Creator[T] or *file.CreateHandle[T]", Got: typeName(cr)}
	}
	return v.dispatchCreate(ctx, backend, val, vars, opts)
}

// ── Patch ────────────────────────────────────────────────────────────────

type patchDispatcher interface {
	dispatchPatch(ctx context.Context, backend Backend, patch any, vars map[string]string, opts WriteOptions) error
}

func (p Patcher[T, P]) dispatchPatch(ctx context.Context, backend Backend, patch any, vars map[string]string, opts WriteOptions) error {
	return patchCore[T, P](ctx, backend, p.core, p.patchCodec, routingInfo{}, patch, vars, opts)
}
func (h *PatchHandle[T, P]) dispatchPatch(ctx context.Context, backend Backend, patch any, vars map[string]string, opts WriteOptions) error {
	return patchCore[T, P](ctx, backend, h.core, h.patchCodec, h.routing, patch, vars, opts)
}

func patchCore[T, P any](ctx context.Context, backend Backend, core roleCore[T], patchCodec codex.Codec[P], routing routingInfo, patch any, vars map[string]string, callerOpts WriteOptions) (err error) {
	defer func() { err = applyErrorPattern(core, err) }()
	obs := obsFromCtx(ctx)
	p, ok := patch.(P)
	if !ok {
		return BackendTypeMismatchError{Expected: typeNameOf[P](), Got: typeName(patch)}
	}
	path, err := core.resource.buildPathWithRouting(routing, vars)
	if err != nil {
		return err
	}
	if !core.resource.format.IsPatchable() {
		return FilePatchNotSupportedError{Path: path}
	}
	// md is nil here: unlike Create, Patch's BASE T value (the thing
	// metadata would be derived FROM) doesn't exist yet at this point —
	// it's only known after reading+merging below, which is a Backend
	// call that must come AFTER this Security gate. So Patch's bound
	// middleware gets the SAME permanently-zero-valued In as Read/List
	// (a discovered, honest correction to the original "Create/Patch
	// both get full metadata integration" assumption — Patch structurally
	// cannot, for the same reason Read/List cannot). See
	// BoundPatchMiddleware's own doc comment.
	boundOutMD, err := runBoundMiddleware(ctx, path, core, nil, nil)
	if err != nil {
		return err
	}
	ctx, endSpan := startSpan(ctx, obs, "file.write", path)
	defer func() { endSpan(err) }()
	start := time.Now()
	intermediate, err := patchCodec.Encode(p)
	if err != nil {
		recordWrite(obs, path, false, time.Since(start))
		return FileEncodeError{Path: path, Err: err}
	}
	patchMap, ok := intermediate.(map[string]any)
	if !ok {
		return FilePatchNotSupportedError{Path: path}
	}
	data, err := backend.ReadBytes(ctx, path)
	if err != nil {
		if isNotFound(err) {
			recordWrite(obs, path, false, time.Since(start))
			return FileNotFoundError{Path: path}
		}
		recordWrite(obs, path, false, time.Since(start))
		return FileReadError{Path: path, Err: err}
	}
	merged, err := core.resource.format.PatchInto(data, patchMap)
	if err != nil {
		recordWrite(obs, path, false, time.Since(start))
		return FileDecodeError{Path: path, Err: err}
	}
	newData, err := core.resource.format.Marshal(merged)
	if err != nil {
		recordWrite(obs, path, false, time.Since(start))
		return FileEncodeError{Path: path, Err: err}
	}
	md, err := deriveWriteMetadata(path, core, merged)
	if err != nil {
		recordWrite(obs, path, false, time.Since(start))
		return err
	}
	// boundOutMD may be non-nil even though In was never populated — a
	// bound middleware's Fn can still unconditionally compute an Out
	// value (ignoring In) and declare WithMetadataOut independently.
	// Patch reuses the caller's own WriteOptions for the write-back step
	// (Perm/Strict/DryRun) exactly like ports.File.Patch delegating to
	// File.Write — CreateDirs is harmless-but-moot here since Patch
	// requires the file to already exist (it just Read it above).
	writeOpts := callerOpts
	writeOpts.Metadata = mergeMetadata(md, boundOutMD)
	if _, err := backend.WriteBytes(ctx, path, newData, writeOpts); err != nil {
		recordWrite(obs, path, false, time.Since(start))
		return FileWriteError{Path: path, Err: err}
	}
	recordWrite(obs, path, true, time.Since(start))
	return nil
}

func dispatchPatch(ctx context.Context, backend Backend, p any, patch any, vars map[string]string, opts WriteOptions) error {
	v, ok := p.(patchDispatcher)
	if !ok {
		return BackendTypeMismatchError{Expected: "file.Patcher[T,P] or *file.PatchHandle[T,P]", Got: typeName(p)}
	}
	return v.dispatchPatch(ctx, backend, patch, vars, opts)
}

// ── Delete ───────────────────────────────────────────────────────────────

type deleteDispatcher interface {
	dispatchDelete(ctx context.Context, backend Backend, vars map[string]string, opts DeleteOptions) (bool, error)
}

func (d Deleter[T]) dispatchDelete(ctx context.Context, backend Backend, vars map[string]string, opts DeleteOptions) (bool, error) {
	return deleteCore[T](ctx, backend, d.core, routingInfo{}, vars, opts)
}
func (h *DeleteHandle[T]) dispatchDelete(ctx context.Context, backend Backend, vars map[string]string, opts DeleteOptions) (bool, error) {
	return deleteCore[T](ctx, backend, h.core, h.routing, vars, opts)
}

func deleteCore[T any](ctx context.Context, backend Backend, core roleCore[T], routing routingInfo, vars map[string]string, callerOpts DeleteOptions) (existed bool, err error) {
	defer func() { err = applyErrorPattern(core, err) }()
	obs := obsFromCtx(ctx)
	wire, err := resolveWire(core)
	if err != nil {
		return false, err
	}
	if err := checkAccessMode(wire); err != nil {
		return false, err
	}
	path, err := core.resource.buildPathWithRouting(routing, vars)
	if err != nil {
		return false, err
	}
	if _, err := runBoundMiddleware(ctx, path, core, nil, nil); err != nil {
		return false, err
	}
	ctx, endSpan := startSpan(ctx, obs, "file.delete", path)
	defer func() { endSpan(err) }()
	start := time.Now()
	existed, err = backend.DeleteBytes(ctx, path, callerOpts)
	if err != nil {
		recordDelete(obs, path, false, time.Since(start))
		// callerOpts.Strict + not-found is reported by Backend.DeleteBytes
		// as its underlying not-found error (not a generic failure) —
		// surface it as FileNotFoundError, matching ports.File.Delete's
		// own Strict semantics exactly, not a generic FileDeleteError.
		if isNotFound(err) {
			return false, FileNotFoundError{Path: path}
		}
		return false, FileDeleteError{Path: path, Err: err}
	}
	recordDelete(obs, path, true, time.Since(start))
	return existed, nil
}

func dispatchDelete(ctx context.Context, backend Backend, d any, vars map[string]string, opts DeleteOptions) (bool, error) {
	v, ok := d.(deleteDispatcher)
	if !ok {
		return false, BackendTypeMismatchError{Expected: "file.Deleter[T] or *file.DeleteHandle[T]", Got: typeName(d)}
	}
	return v.dispatchDelete(ctx, backend, vars, opts)
}

// ── List ─────────────────────────────────────────────────────────────────

type listDispatcher interface {
	dispatchList(ctx context.Context, backend Backend, vars map[string]string) (any, error)
}

func (l Lister[T]) dispatchList(ctx context.Context, backend Backend, vars map[string]string) (any, error) {
	return listCore[T](ctx, backend, l.core, routingInfo{}, vars)
}
func (h *ListHandle[T]) dispatchList(ctx context.Context, backend Backend, vars map[string]string) (any, error) {
	return listCore[T](ctx, backend, h.core, h.routing, vars)
}

func listCore[T any](ctx context.Context, backend Backend, core roleCore[T], routing routingInfo, vars map[string]string) (result any, err error) {
	defer func() { err = applyErrorPattern(core, err) }()
	obs := obsFromCtx(ctx)
	path, err := core.resource.buildParentDirWithRouting(routing, vars)
	if err != nil {
		return nil, err
	}
	if _, err := runBoundMiddleware(ctx, path, core, nil, nil); err != nil {
		return nil, err
	}
	ctx, endSpan := startSpan(ctx, obs, "file.list", path)
	defer func() { endSpan(err) }()
	start := time.Now()
	entries, err := backend.ListDir(ctx, path)
	if err != nil {
		recordList(obs, path, false, 0, time.Since(start))
		return nil, FileReadError{Path: path, Err: err}
	}
	out := make([]T, 0, len(entries))
	for _, e := range entries {
		if e.IsDir {
			continue
		}
		childPath := joinPath(path, e.Name)
		data, err := backend.ReadBytes(ctx, childPath)
		if err != nil {
			recordList(obs, path, false, len(out), time.Since(start))
			return nil, FileReadError{Path: childPath, Err: err}
		}
		v, err := core.resource.format.Unmarshal(data)
		if err != nil {
			recordList(obs, path, false, len(out), time.Since(start))
			return nil, FileDecodeError{Path: childPath, Err: err}
		}
		entryVars, err := entryListVars(routing, core, vars, e.Name)
		if err != nil {
			recordList(obs, path, false, len(out), time.Since(start))
			return nil, err
		}
		if err := applyPathMergeFields(entryVars, core, &v); err != nil {
			recordList(obs, path, false, len(out), time.Since(start))
			return nil, err
		}
		if err := applyMetadataMergeFields(ctx, backend, childPath, core, &v); err != nil {
			recordList(obs, path, false, len(out), time.Since(start))
			return nil, err
		}
		out = append(out, v)
	}
	recordList(obs, path, true, len(out), time.Since(start))
	return out, nil
}

func dispatchList(ctx context.Context, backend Backend, l any, vars map[string]string) (any, error) {
	v, ok := l.(listDispatcher)
	if !ok {
		return nil, BackendTypeMismatchError{Expected: "file.Lister[T] or *file.ListHandle[T]", Got: typeName(l)}
	}
	return v.dispatchList(ctx, backend, vars)
}

// ── Exists ───────────────────────────────────────────────────────────────

type existsDispatcher interface {
	dispatchExists(ctx context.Context, backend Backend, vars map[string]string) (Entry, bool, error)
}

func (e Exister[T]) dispatchExists(ctx context.Context, backend Backend, vars map[string]string) (Entry, bool, error) {
	return existsCore[T](ctx, backend, e.core, routingInfo{}, vars)
}
func (h *ExistsHandle[T]) dispatchExists(ctx context.Context, backend Backend, vars map[string]string) (Entry, bool, error) {
	return existsCore[T](ctx, backend, h.core, h.routing, vars)
}

func existsCore[T any](ctx context.Context, backend Backend, core roleCore[T], routing routingInfo, vars map[string]string) (entry Entry, exists bool, err error) {
	defer func() { err = applyErrorPattern(core, err) }()
	obs := obsFromCtx(ctx)
	path, err := core.resource.buildPathWithRouting(routing, vars)
	if err != nil {
		return Entry{}, false, err
	}
	if _, err := runBoundMiddleware(ctx, path, core, nil, nil); err != nil {
		return Entry{}, false, err
	}
	ctx, endSpan := startSpan(ctx, obs, "file.exists", path)
	defer func() { endSpan(err) }()
	start := time.Now()
	entry, err = backend.Stat(ctx, path)
	if err != nil {
		if isNotFound(err) {
			recordExists(obs, path, true, time.Since(start))
			return Entry{}, false, nil
		}
		recordExists(obs, path, false, time.Since(start))
		return Entry{}, false, FileReadError{Path: path, Err: err}
	}
	recordExists(obs, path, true, time.Since(start))
	return entry, true, nil
}

func dispatchExists(ctx context.Context, backend Backend, e any, vars map[string]string) (Entry, bool, error) {
	v, ok := e.(existsDispatcher)
	if !ok {
		return Entry{}, false, BackendTypeMismatchError{Expected: "file.Exister[T] or *file.ExistsHandle[T]", Got: typeName(e)}
	}
	return v.dispatchExists(ctx, backend, vars)
}

// ── Copy ─────────────────────────────────────────────────────────────────

type copyDispatcher interface {
	dispatchCopy(ctx context.Context, backend Backend, srcVars, dstVars map[string]string) error
}

func (c Copier[T]) dispatchCopy(ctx context.Context, backend Backend, srcVars, dstVars map[string]string) error {
	return copyCore[T](ctx, backend, c.core, routingInfo{}, srcVars, dstVars)
}
func (h *CopyHandle[T]) dispatchCopy(ctx context.Context, backend Backend, srcVars, dstVars map[string]string) error {
	return copyCore[T](ctx, backend, h.core, h.routing, srcVars, dstVars)
}

// copyBytesCore performs the actual Copy operation (native fast path or
// generic Read+Write fallback) — factored out so Move's 3-level cascade
// can delegate to it directly, per the roadmap doc's own design.
func copyBytesCore[T any](ctx context.Context, backend Backend, core roleCore[T], routing routingInfo, srcVars, dstVars map[string]string) (srcPath, dstPath string, err error) {
	wire, err := resolveWire(core)
	if err != nil {
		return "", "", err
	}
	if err := checkAccessMode(wire); err != nil {
		return "", "", err
	}
	srcPath, err = core.resource.buildPathWithRouting(routing, srcVars)
	if err != nil {
		return "", "", err
	}
	dstPath, err = core.resource.buildPathWithRouting(routing, dstVars)
	if err != nil {
		return "", "", err
	}
	if cb, ok := backend.(CopyingBackend); ok {
		if err := cb.CopyBytes(ctx, srcPath, dstPath); err != nil {
			return srcPath, dstPath, FileWriteError{Path: dstPath, Err: err}
		}
		return srcPath, dstPath, nil
	}
	data, err := backend.ReadBytes(ctx, srcPath)
	if err != nil {
		if isNotFound(err) {
			return srcPath, dstPath, FileNotFoundError{Path: srcPath}
		}
		return srcPath, dstPath, FileReadError{Path: srcPath, Err: err}
	}
	if _, err := backend.WriteBytes(ctx, dstPath, data, WriteOptions{}); err != nil {
		return srcPath, dstPath, FileWriteError{Path: dstPath, Err: err}
	}
	return srcPath, dstPath, nil
}

func copyCore[T any](ctx context.Context, backend Backend, core roleCore[T], routing routingInfo, srcVars, dstVars map[string]string) (err error) {
	defer func() { err = applyErrorPattern(core, err) }()
	obs := obsFromCtx(ctx)
	precheckPath, err := core.resource.buildPathWithRouting(routing, srcVars)
	if err != nil {
		return err
	}
	if _, err := runBoundMiddleware(ctx, precheckPath, core, nil, nil); err != nil {
		return err
	}
	ctx, endSpan := startSpan(ctx, obs, "file.copy", precheckPath)
	defer func() { endSpan(err) }()
	start := time.Now()
	srcPath, dstPath, err := copyBytesCore(ctx, backend, core, routing, srcVars, dstVars)
	recordCopy(obs, srcPath, dstPath, err == nil, time.Since(start))
	return err
}

func dispatchCopy(ctx context.Context, backend Backend, cp any, srcVars, dstVars map[string]string) error {
	v, ok := cp.(copyDispatcher)
	if !ok {
		return BackendTypeMismatchError{Expected: "file.Copier[T] or *file.CopyHandle[T]", Got: typeName(cp)}
	}
	return v.dispatchCopy(ctx, backend, srcVars, dstVars)
}

// ── Move ─────────────────────────────────────────────────────────────────

type moveDispatcher interface {
	dispatchMove(ctx context.Context, backend Backend, srcVars, dstVars map[string]string) error
}

func (m Mover[T]) dispatchMove(ctx context.Context, backend Backend, srcVars, dstVars map[string]string) error {
	return moveCore[T](ctx, backend, m.core, routingInfo{}, srcVars, dstVars)
}
func (h *MoveHandle[T]) dispatchMove(ctx context.Context, backend Backend, srcVars, dstVars map[string]string) error {
	return moveCore[T](ctx, backend, h.core, h.routing, srcVars, dstVars)
}

// moveCore implements the 3-level cascade documented in
// docs/roadmap/file-api.md: (1) MovingBackend.MoveBytes if implemented;
// (2) otherwise DELEGATE to copyBytesCore (which itself tries
// CopyingBackend first) followed by DeleteBytes; (3) which transparently
// collapses to pure Read+Write+Delete on a Backend implementing neither
// optional extension.
func moveCore[T any](ctx context.Context, backend Backend, core roleCore[T], routing routingInfo, srcVars, dstVars map[string]string) (err error) {
	defer func() { err = applyErrorPattern(core, err) }()
	obs := obsFromCtx(ctx)
	start := time.Now()

	wire, err := resolveWire(core)
	if err != nil {
		return err
	}
	if err := checkAccessMode(wire); err != nil {
		return err
	}
	srcPath, err := core.resource.buildPathWithRouting(routing, srcVars)
	if err != nil {
		return err
	}
	dstPath, err := core.resource.buildPathWithRouting(routing, dstVars)
	if err != nil {
		return err
	}
	if _, err := runBoundMiddleware(ctx, srcPath, core, nil, nil); err != nil {
		return err
	}
	ctx, endSpan := startSpan(ctx, obs, "file.move", srcPath)
	defer func() { endSpan(err) }()

	if mb, ok := backend.(MovingBackend); ok {
		err := mb.MoveBytes(ctx, srcPath, dstPath)
		if err != nil {
			err = FileWriteError{Path: dstPath, Err: err}
		}
		recordMove(obs, srcPath, dstPath, err == nil, time.Since(start))
		return err
	}

	// Level 2/3: delegate to Copier's own fallback, then Delete.
	_, _, err = copyBytesCore(ctx, backend, core, routing, srcVars, dstVars)
	if err != nil {
		recordMove(obs, srcPath, dstPath, false, time.Since(start))
		return err
	}
	if _, err := backend.DeleteBytes(ctx, srcPath, DeleteOptions{}); err != nil {
		recordMove(obs, srcPath, dstPath, false, time.Since(start))
		return FileDeleteError{Path: srcPath, Err: err}
	}
	recordMove(obs, srcPath, dstPath, true, time.Since(start))
	return nil
}

func dispatchMove(ctx context.Context, backend Backend, mv any, srcVars, dstVars map[string]string) error {
	v, ok := mv.(moveDispatcher)
	if !ok {
		return BackendTypeMismatchError{Expected: "file.Mover[T] or *file.MoveHandle[T]", Got: typeName(mv)}
	}
	return v.dispatchMove(ctx, backend, srcVars, dstVars)
}

// ── Mkdir ────────────────────────────────────────────────────────────────

type mkdirDispatcher interface {
	dispatchMkdir(ctx context.Context, backend Backend, vars map[string]string, opts MkdirOptions) (bool, error)
}

func (m MkdirCreator[T]) dispatchMkdir(ctx context.Context, backend Backend, vars map[string]string, opts MkdirOptions) (bool, error) {
	return mkdirCore[T](ctx, backend, m.core, routingInfo{}, vars, opts)
}
func (h *MkdirHandle[T]) dispatchMkdir(ctx context.Context, backend Backend, vars map[string]string, opts MkdirOptions) (bool, error) {
	return mkdirCore[T](ctx, backend, h.core, h.routing, vars, opts)
}

func mkdirCore[T any](ctx context.Context, backend Backend, core roleCore[T], routing routingInfo, vars map[string]string, callerOpts MkdirOptions) (created bool, err error) {
	defer func() { err = applyErrorPattern(core, err) }()
	obs := obsFromCtx(ctx)
	wire, err := resolveWire(core)
	if err != nil {
		return false, err
	}
	if err := checkAccessMode(wire); err != nil {
		return false, err
	}
	path, err := core.resource.buildPathWithRouting(routing, vars)
	if err != nil {
		return false, err
	}
	if _, err := runBoundMiddleware(ctx, path, core, nil, nil); err != nil {
		return false, err
	}
	opts := callerOpts
	if wire.NativePOSIXMode != nil {
		opts.NativePOSIXMode = wire.NativePOSIXMode
	}
	ctx, endSpan := startSpan(ctx, obs, "file.mkdir", path)
	defer func() { endSpan(err) }()
	start := time.Now()
	created, err = backend.Mkdir(ctx, path, opts)
	if err != nil {
		recordMkdir(obs, path, false, time.Since(start))
		return false, FileWriteError{Path: path, Err: err}
	}
	recordMkdir(obs, path, true, time.Since(start))
	return created, nil
}

func dispatchMkdir(ctx context.Context, backend Backend, m any, vars map[string]string, opts MkdirOptions) (bool, error) {
	v, ok := m.(mkdirDispatcher)
	if !ok {
		return false, BackendTypeMismatchError{Expected: "file.MkdirCreator[T] or *file.MkdirHandle[T]", Got: typeName(m)}
	}
	return v.dispatchMkdir(ctx, backend, vars, opts)
}

// ── BatchDelete ──────────────────────────────────────────────────────────

type batchDeleteDispatcher interface {
	dispatchBatchDelete(ctx context.Context, backend Backend, varsList []map[string]string) ([]BatchDeleteResult, error)
}

func (b BatchDeleter[T]) dispatchBatchDelete(ctx context.Context, backend Backend, varsList []map[string]string) ([]BatchDeleteResult, error) {
	return batchDeleteCore[T](ctx, backend, b.core, routingInfo{}, varsList)
}
func (h *BatchDeleteHandle[T]) dispatchBatchDelete(ctx context.Context, backend Backend, varsList []map[string]string) ([]BatchDeleteResult, error) {
	return batchDeleteCore[T](ctx, backend, h.core, h.routing, varsList)
}

// batchDeleteCore implements the native-vs-sequential-loop fallback —
// UNLIKE Move, it does NOT delegate to Deleter[T]'s own dispatch:
// Security is checked ONCE per BATCH here, not once per key, so routing
// each key through Deleter[T]'s full dispatch (which would re-check
// Security per key) would be incorrect, not just redundant.
func batchDeleteCore[T any](ctx context.Context, backend Backend, core roleCore[T], routing routingInfo, varsList []map[string]string) (result []BatchDeleteResult, err error) {
	defer func() { err = applyErrorPattern(core, err) }()
	obs := obsFromCtx(ctx)
	wire, err := resolveWire(core)
	if err != nil {
		return nil, err
	}
	if err := checkAccessMode(wire); err != nil {
		return nil, err
	}
	paths := make([]string, len(varsList))
	for i, vars := range varsList {
		path, err := core.resource.buildPathWithRouting(routing, vars)
		if err != nil {
			return nil, err
		}
		paths[i] = path
	}
	// BatchDelete has no single path to label the Security gate with
	// (unlike every other role) — runs ONCE for the whole batch, not
	// once per path, consistent with this role's own established
	// "Security checked once per batch, not once per item" design (see
	// Client.BatchDelete's doc comment).
	if _, err := runBoundMiddleware(ctx, "", core, nil, nil); err != nil {
		return nil, err
	}
	ctx, endSpan := startSpan(ctx, obs, "file.batchdelete", "")
	defer func() { endSpan(err) }()

	start := time.Now()
	if bdb, ok := backend.(BatchDeletingBackend); ok {
		results, err := bdb.DeleteBytesBatch(ctx, paths)
		if err != nil {
			recordBatchDelete(obs, paths, 0, len(paths), time.Since(start))
			return nil, err
		}
		success, failure := countResults(results)
		recordBatchDelete(obs, paths, success, failure, time.Since(start))
		return results, nil
	}

	results := make([]BatchDeleteResult, len(paths))
	for i, path := range paths {
		existed, deleteErr := backend.DeleteBytes(ctx, path, DeleteOptions{})
		results[i] = BatchDeleteResult{Path: path, Existed: existed}
		if deleteErr != nil {
			results[i].Err = FileDeleteError{Path: path, Err: deleteErr}
		}
	}
	success, failure := countResults(results)
	recordBatchDelete(obs, paths, success, failure, time.Since(start))
	return results, nil
}

func countResults(results []BatchDeleteResult) (success, failure int) {
	for _, r := range results {
		if r.Err != nil {
			failure++
		} else {
			success++
		}
	}
	return success, failure
}

func dispatchBatchDelete(ctx context.Context, backend Backend, bd any, varsList []map[string]string) ([]BatchDeleteResult, error) {
	v, ok := bd.(batchDeleteDispatcher)
	if !ok {
		return nil, BackendTypeMismatchError{Expected: "file.BatchDeleter[T] or *file.BatchDeleteHandle[T]", Got: typeName(bd)}
	}
	return v.dispatchBatchDelete(ctx, backend, varsList)
}
