package file

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/format"
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

// recordConditionalWrite reports stats.FileObserver.RecordFileConditionalWrite
// alongside (not instead of) recordWrite's own call. native is derived from
// backend's optional NativeConditionalWriteBackend extension — a Backend not
// implementing it is treated as native=false (the conservative default).
func recordConditionalWrite(obs stats.Observer, backend Backend, path, ifMatch string, d time.Duration) {
	fo, ok := obs.(stats.FileObserver)
	if !ok {
		return
	}
	var native bool
	if ncw, ok := backend.(NativeConditionalWriteBackend); ok {
		native = ncw.NativeConditionalWrite()
	}
	fo.RecordFileConditionalWrite(path, native, ifMatch, d)
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

func recordRangeRead(obs stats.Observer, path string, native bool, offset, length int64, d time.Duration) {
	if fo, ok := obs.(stats.FileObserver); ok {
		fo.RecordFileRangeRead(path, native, offset, length, d)
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
// rule (see docs/design/d-0011-file-api-backend-transparency.md) is implemented by dispatch
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
func applyMetadataMergeFields[T any](ctx context.Context, backend Backend, path string, core roleCore[T], v *T, obs stats.Observer) error {
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
		stats.ReportErrors(obs, "file", err)
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
func deriveWriteMetadata[T any](path string, core roleCore[T], v T, obs stats.Observer) (map[string]string, error) {
	if len(core.resource.metadataMergeFields) == 0 {
		return nil, nil
	}
	md, err := codex.EncodeVars(v, core.resource.metadataMergeFields...)
	if err != nil {
		stats.ReportErrors(obs, "file", err)
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

// readDispatcher is a genuine Go 1.27 generic method candidate — T
// appears directly in dispatchRead's OWN return type, so making the
// interface itself generic over T lets Client.Read return the decoded
// T directly, with zero reflection/boxing (see
// docs/design/d-0012-go127-generic-methods.md's Tier 2 section).
type readDispatcher[T any] interface {
	dispatchRead(ctx context.Context, backend Backend, vars map[string]string) (T, error)
}

func (r Reader[T]) dispatchRead(ctx context.Context, backend Backend, vars map[string]string) (T, error) {
	return readCore[T](ctx, backend, r.core, routingInfo{}, vars)
}
func (h *ReadHandle[T]) dispatchRead(ctx context.Context, backend Backend, vars map[string]string) (T, error) {
	return readCore[T](ctx, backend, h.core, h.routing, vars)
}

func readCore[T any](ctx context.Context, backend Backend, core roleCore[T], routing routingInfo, vars map[string]string) (result T, err error) {
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
		stats.ReportErrors(obs, "file", err)
		recordRead(obs, path, false, time.Since(start))
		return zero, FileDecodeError{Path: path, Err: err}
	}
	if err := applyPathMergeFields(vars, core, &v); err != nil {
		recordRead(obs, path, false, time.Since(start))
		return zero, err
	}
	if err := applyMetadataMergeFields(ctx, backend, path, core, &v, obs); err != nil {
		recordRead(obs, path, false, time.Since(start))
		return zero, err
	}
	recordRead(obs, path, true, time.Since(start))
	return v, nil
}

// ── Create ───────────────────────────────────────────────────────────────

// createDispatcher is NOT generic over a return-typed T — Create's
// return is always []string, regardless of T; T instead types the
// role AND val's input shape. It still gains a type parameter here so
// Client.Create[T] can require val to be the SAME T as cr's role at
// compile time, eliminating the former runtime val.(T) type assertion
// (and its BackendTypeMismatchError raise site) entirely — see
// docs/design/d-0012-go127-generic-methods.md's Tier 2 section.
type createDispatcher[T any] interface {
	dispatchCreate(ctx context.Context, backend Backend, val T, vars map[string]string, opts WriteOptions) (createdDirs []string, err error)
}

func (c Creator[T]) dispatchCreate(ctx context.Context, backend Backend, val T, vars map[string]string, opts WriteOptions) ([]string, error) {
	return createCore[T](ctx, backend, c.core, routingInfo{}, val, vars, opts)
}
func (h *CreateHandle[T]) dispatchCreate(ctx context.Context, backend Backend, val T, vars map[string]string, opts WriteOptions) ([]string, error) {
	return createCore[T](ctx, backend, h.core, h.routing, val, vars, opts)
}

func createCore[T any](ctx context.Context, backend Backend, core roleCore[T], routing routingInfo, v T, vars map[string]string, callerOpts WriteOptions) (createdDirs []string, err error) {
	defer func() { err = applyErrorPattern(core, err) }()
	obs := obsFromCtx(ctx)
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
	// Conditional write is only ever consulted when Strict is false —
	// Strict's OWN existence check always wins when both are set on the
	// same call (see docs/design/d-0011-file-api-backend-transparency.md's "Conditional writes"
	// Fork 2): Strict routes through the existing unconditional
	// WriteBytes path below regardless of IfMatch. A capability check
	// happens as EARLY as possible (before Marshal/metadata/
	// BoundMiddleware) so an unsupported Backend fails loud and fast,
	// with zero partial work done.
	if callerOpts.IfMatch != nil && !callerOpts.Strict {
		if _, ok := backend.(ConditionalWriteBackend); !ok {
			return nil, ConditionalWriteUnsupportedError{Path: path, Backend: backend.BackendName()}
		}
	}
	// md (the caller-intended metadata, derived from v's own
	// metadataMergeFields-tagged fields) is computed BEFORE
	// runBoundMiddleware so an attached BoundCreateMiddleware's
	// WithMetadataIn can decode it into its own In — see
	// Middleware's own doc comment for the full rationale.
	md, err := deriveWriteMetadata(path, core, v, obs)
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
	opts := callerOpts
	if wire.NativePOSIXMode != nil {
		opts.Perm = *wire.NativePOSIXMode
	}
	// 3-way layering, each more specific than the last: the caller's OWN
	// directly-supplied WriteOptions.Metadata (the plain escape hatch —
	// works even with no declared MetadataParam at all) is the BASE;
	// the resource's own derived md (from declared metadataMergeFields)
	// layers on top; a bound middleware's own contribution (via
	// WithMetadataOut) wins LAST on any key collision — it represents
	// the most specific, runtime-computed value. Found via a deep-dive-
	// bug-hunt live-reproduction test: this used to be a 2-way merge of
	// ONLY (md, boundOutMD), silently DISCARDING callerOpts.Metadata
	// entirely whenever the resource declared no metadata merge fields
	// — directly contradicting WriteOptions.Metadata's own documented,
	// unconditional "user-defined key/value metadata to persist
	// alongside the write" contract.
	opts.Metadata = mergeMetadata(mergeMetadata(callerOpts.Metadata, md), boundOutMD)
	// Mechanism 1 (automatic format-level write streaming — see
	// docs/design/d-0011-file-api-backend-transparency.md's "Streaming/chunked reads and writes"
	// section): a streamable format + a StreamWriteBackend-implementing
	// Backend streams via MarshalTo instead of Marshal+WriteBytes — NOT
	// combined with IfMatch this round (falls back to the existing
	// non-streaming conditional-write path below, unchanged).
	streamable := core.resource.format.IsStreamable()
	useAutoStream := streamable && !(opts.IfMatch != nil && !opts.Strict)
	swb, canStream := backend.(StreamWriteBackend)
	if useAutoStream && canStream {
		var w io.WriteCloser
		w, err = swb.OpenWriter(ctx, path, opts)
		if err == nil {
			err = core.resource.format.MarshalTo(v, w)
			if closeErr := w.Close(); err == nil {
				err = closeErr
			}
		}
		if err != nil {
			stats.ReportErrors(obs, "file", err)
			recordWrite(obs, path, false, time.Since(start))
			return nil, FileWriteError{Path: path, Err: err}
		}
		recordWrite(obs, path, true, time.Since(start))
		return nil, nil
	}
	var data []byte
	if streamable {
		// A streamable format's Marshal is OFTEN unsupported (NewStreamed
		// formats frequently carry no non-streaming marshal func at all
		// — see format.ErrNotMarshalable's own doc comment) — buffer via
		// MarshalTo (the format's own, universally-present writable
		// mechanism) instead of calling Marshal, so a Backend WITHOUT
		// StreamWriteBackend still works correctly (just without the
		// memory benefit), rather than failing on a format that
		// genuinely has no non-streaming Marshal.
		var buf bytes.Buffer
		err = core.resource.format.MarshalTo(v, &buf)
		data = buf.Bytes()
	} else {
		data, err = core.resource.format.Marshal(v)
	}
	if err != nil {
		stats.ReportErrors(obs, "file", err)
		recordWrite(obs, path, false, time.Since(start))
		return nil, FileEncodeError{Path: path, Err: err}
	}
	conditional := opts.IfMatch != nil && !opts.Strict
	if conditional {
		cwb := backend.(ConditionalWriteBackend) // already confirmed above
		createdDirs, err = cwb.WriteBytesIfMatch(ctx, path, data, *opts.IfMatch, opts)
	} else {
		createdDirs, err = backend.WriteBytes(ctx, path, data, opts)
	}
	if conditional {
		recordConditionalWrite(obs, backend, path, *opts.IfMatch, time.Since(start))
	}
	if err != nil {
		recordWrite(obs, path, false, time.Since(start))
		// A Backend already reporting a specific, typed condition (e.g.
		// Strict + already-exists, or a conditional-write
		// mismatch/timeout) is surfaced as-is, not double-wrapped in
		// FileWriteError — matches ports.File.Write's own Strict
		// semantics (bare FileAlreadyExistsError, not a wrapped one).
		var alreadyExists FileAlreadyExistsError
		if errors.As(err, &alreadyExists) {
			return createdDirs, alreadyExists
		}
		var mismatch ConditionalWriteMismatchError
		if errors.As(err, &mismatch) {
			return createdDirs, mismatch
		}
		var lockTimeout ConditionalWriteLockTimeoutError
		if errors.As(err, &lockTimeout) {
			return createdDirs, lockTimeout
		}
		return createdDirs, FileWriteError{Path: path, Err: err}
	}
	recordWrite(obs, path, true, time.Since(start))
	return createdDirs, nil
}

// ── Patch ────────────────────────────────────────────────────────────────

// patchDispatcher is a 2-type-param dispatcher — T types the base
// resource's role, P types the patch's own shape; neither appears in
// dispatchPatch's return (plain error), but making the interface
// generic over both still eliminates the former runtime patch.(P) type
// assertion (and its BackendTypeMismatchError raise site) at compile
// time, mirroring createDispatcher's identical rationale.
type patchDispatcher[T, P any] interface {
	dispatchPatch(ctx context.Context, backend Backend, patch P, vars map[string]string, opts WriteOptions) error
}

func (p Patcher[T, P]) dispatchPatch(ctx context.Context, backend Backend, patch P, vars map[string]string, opts WriteOptions) error {
	return patchCore[T, P](ctx, backend, p.core, p.patchCodec, routingInfo{}, patch, vars, opts)
}
func (h *PatchHandle[T, P]) dispatchPatch(ctx context.Context, backend Backend, patch P, vars map[string]string, opts WriteOptions) error {
	return patchCore[T, P](ctx, backend, h.core, h.patchCodec, h.routing, patch, vars, opts)
}

func patchCore[T, P any](ctx context.Context, backend Backend, core roleCore[T], patchCodec codex.Codec[P], routing routingInfo, p P, vars map[string]string, callerOpts WriteOptions) (err error) {
	defer func() { err = applyErrorPattern(core, err) }()
	obs := obsFromCtx(ctx)
	// Patch writes to the Backend (a read-modify-write), so it must
	// honor a declared AccessModeReadOnly Capability exactly like every
	// other mutating role (Create/Delete/Copy/Move/Mkdir/BatchDelete) —
	// this was missing entirely (deep-dive-bug-hunt Round 217: a
	// Patcher.WithCapabilities(AccessModeReadOnly) declaration was
	// silently inert, never enforced). Checked BEFORE any Backend call,
	// matching the established "fail before backend" convention.
	wire, err := resolveWire(core)
	if err != nil {
		return err
	}
	if err := checkAccessMode(wire); err != nil {
		return err
	}
	path, err := core.resource.buildPathWithRouting(routing, vars)
	if err != nil {
		return err
	}
	// Patch has no Strict concept — IfMatch is always consulted when
	// set. Same early, fail-fast capability check as createCore's own.
	if callerOpts.IfMatch != nil {
		if _, ok := backend.(ConditionalWriteBackend); !ok {
			return ConditionalWriteUnsupportedError{Path: path, Backend: backend.BackendName()}
		}
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
		stats.ReportErrors(obs, "file", err)
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
	// Stay in MAP space for the merge/marshal round trip — mirrors
	// ports.File.PatchEncoded's own, established, already-tested contract
	// exactly (confirmed via examples/file-io: using format.PatchInto,
	// which decodes the merge result into T before marshaling, silently
	// DROPS any patchCodec-only field absent from T, e.g. a "debug_mode"
	// field present only in the patch codec — a real, now-fixed bug).
	// Re-decoding into T below is still done, but ONLY to validate the
	// file codec's own known fields and to obtain a typed value for
	// metadata derivation — the bytes actually written come from
	// MarshalRaw(existingMap), never Marshal(T), so patchCodec-only
	// fields survive in the written file exactly like ports.File.PatchEncoded.
	rawExisting, err := core.resource.format.UnmarshalRaw(data)
	if err != nil {
		stats.ReportErrors(obs, "file", err)
		recordWrite(obs, path, false, time.Since(start))
		return FileDecodeError{Path: path, Err: err}
	}
	existingMap, ok := rawExisting.(map[string]any)
	if !ok {
		recordWrite(obs, path, false, time.Since(start))
		return FilePatchNotSupportedError{Path: path}
	}
	format.DeepMerge(existingMap, patchMap)
	merged, err := core.resource.format.Codec().Decode(existingMap)
	if err != nil {
		stats.ReportErrors(obs, "file", err)
		recordWrite(obs, path, false, time.Since(start))
		return FileDecodeError{Path: path, Err: err}
	}
	newData, err := core.resource.format.MarshalRaw(existingMap)
	if err != nil {
		stats.ReportErrors(obs, "file", err)
		recordWrite(obs, path, false, time.Since(start))
		return FileEncodeError{Path: path, Err: err}
	}
	md, err := deriveWriteMetadata(path, core, merged, obs)
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
	if wire.NativePOSIXMode != nil {
		writeOpts.Perm = *wire.NativePOSIXMode
	}
	// Same 3-way layering fix as createCore's own (see its doc comment
	// for the full rationale) — callerOpts.Metadata as the base, md
	// (resource-derived) next, boundOutMD (bound middleware) wins last.
	writeOpts.Metadata = mergeMetadata(mergeMetadata(callerOpts.Metadata, md), boundOutMD)
	var writeErr error
	patchConditional := writeOpts.IfMatch != nil
	if patchConditional {
		cwb := backend.(ConditionalWriteBackend) // already confirmed above
		_, writeErr = cwb.WriteBytesIfMatch(ctx, path, newData, *writeOpts.IfMatch, writeOpts)
	} else {
		_, writeErr = backend.WriteBytes(ctx, path, newData, writeOpts)
	}
	if patchConditional {
		recordConditionalWrite(obs, backend, path, *writeOpts.IfMatch, time.Since(start))
	}
	if writeErr != nil {
		recordWrite(obs, path, false, time.Since(start))
		var mismatch ConditionalWriteMismatchError
		if errors.As(writeErr, &mismatch) {
			return mismatch
		}
		var lockTimeout ConditionalWriteLockTimeoutError
		if errors.As(writeErr, &lockTimeout) {
			return lockTimeout
		}
		return FileWriteError{Path: path, Err: writeErr}
	}
	recordWrite(obs, path, true, time.Since(start))
	return nil
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

// ── List ─────────────────────────────────────────────────────────────────

// listDispatcher is a genuine Go 1.27 generic method candidate — per
// its own doc comment, List decodes a []ListResult[T], NOT a bare T —
// so the interface's own method returns []ListResult[T] directly
// rather than boxing it as any.
type listDispatcher[T any] interface {
	dispatchList(ctx context.Context, backend Backend, vars map[string]string, opts ListOptions) ([]ListResult[T], error)
}

func (l Lister[T]) dispatchList(ctx context.Context, backend Backend, vars map[string]string, opts ListOptions) ([]ListResult[T], error) {
	return listCore[T](ctx, backend, l.core, routingInfo{}, vars, opts)
}
func (h *ListHandle[T]) dispatchList(ctx context.Context, backend Backend, vars map[string]string, opts ListOptions) ([]ListResult[T], error) {
	return listCore[T](ctx, backend, h.core, h.routing, vars, opts)
}

// listEntry decodes+merges one discovered file (at childPath, leaf name
// name) into a ListResult, running that entry's OWN per-entry Security
// gate (passing the decoded value, once available) — see the "Phase 1
// amendment" section of docs/design/d-0011-file-api-backend-transparency.md. Every failure
// (decode, merge-field, metadata-read, OR Security rejection) becomes
// this entry's own Err; the caller (listCore's single-level/recursive
// discovery loops) never aborts because of this function's return.
func listEntry[T any](ctx context.Context, backend Backend, core roleCore[T], routing routingInfo, vars map[string]string, childPath, name string, obs stats.Observer) ListResult[T] {
	data, err := backend.ReadBytes(ctx, childPath)
	if err != nil {
		return ListResult[T]{Err: applyErrorPattern(core, FileReadError{Path: childPath, Err: err})}
	}
	v, err := core.resource.format.Unmarshal(data)
	if err != nil {
		stats.ReportErrors(obs, "file", err)
		return ListResult[T]{Err: applyErrorPattern(core, FileDecodeError{Path: childPath, Err: err})}
	}
	entryVars, err := entryListVars(routing, core, vars, name)
	if err != nil {
		return ListResult[T]{Err: applyErrorPattern(core, err)}
	}
	if err := applyPathMergeFields(entryVars, core, &v); err != nil {
		return ListResult[T]{Err: applyErrorPattern(core, err)}
	}
	if err := applyMetadataMergeFields(ctx, backend, childPath, core, &v, obs); err != nil {
		return ListResult[T]{Err: applyErrorPattern(core, err)}
	}
	// Per-entry Security gate — passes the ACTUAL decoded value (unlike
	// every other role's once-per-call gate, which has no single T to
	// offer yet at gate time).
	if _, err := runBoundMiddleware(ctx, childPath, core, &v, nil); err != nil {
		return ListResult[T]{Err: applyErrorPattern(core, err)}
	}
	return ListResult[T]{Value: v}
}

// recursiveListDepth is the ONLY structurally-valid depth (relative to
// the resource's resolved parent directory) a Recursive List entry can
// sit at. A Resource's path template always has the shape
// "<dir-with-vars>/<final-leaf-segment>" (buildParentDirWithRouting
// strips exactly the final segment, and every var in the dir portion
// is REQUIRED up front — see that method's own doc comment) — so,
// given a fully-resolved parent directory, the template's own
// declared structure always has EXACTLY ONE remaining segment (the
// leaf) past it, regardless of how many segments the template has in
// total. Reuses ports.Dir.listGlobDiscovery's own depth-pruning
// precedent ("once a candidate directory's depth already equals the
// template's own segment count, does not descend further"), applied
// here to this package's always-single-remaining-segment shape.
const recursiveListDepth = 1

// listCore implements Client.List's single-level (default) and
// Recursive discovery.
func listCore[T any](ctx context.Context, backend Backend, core roleCore[T], routing routingInfo, vars map[string]string, opts ListOptions) (result []ListResult[T], err error) {
	defer func() { err = applyErrorPattern(core, err) }()
	obs := obsFromCtx(ctx)
	dirPath, err := core.resource.buildParentDirWithRouting(routing, vars)
	if err != nil {
		return nil, err
	}
	ctx, endSpan := startSpan(ctx, obs, "file.list", dirPath)
	defer func() { endSpan(err) }()
	start := time.Now()

	var out []ListResult[T]
	if opts.Recursive {
		if wb, ok := backend.(WalkingBackend); ok {
			out, err = listRecursiveWalking(ctx, backend, wb, core, routing, vars, dirPath, obs)
		} else {
			out, err = listRecursiveManual(ctx, backend, core, routing, vars, dirPath, 0, obs)
		}
	} else {
		out, err = listSingleLevel(ctx, backend, core, routing, vars, dirPath, obs)
	}
	if err != nil {
		recordList(obs, dirPath, false, len(out), time.Since(start))
		return nil, err
	}
	recordList(obs, dirPath, true, len(out), time.Since(start))
	return out, nil
}

// listSingleLevel is today's (pre-Recursive, and Recursive==false)
// discovery: one ListDir call, immediate children only.
func listSingleLevel[T any](ctx context.Context, backend Backend, core roleCore[T], routing routingInfo, vars map[string]string, dirPath string, obs stats.Observer) ([]ListResult[T], error) {
	entries, err := backend.ListDir(ctx, dirPath)
	if err != nil {
		return nil, FileReadError{Path: dirPath, Err: err}
	}
	out := make([]ListResult[T], 0, len(entries))
	for _, e := range entries {
		if e.IsDir {
			continue
		}
		out = append(out, listEntry(ctx, backend, core, routing, vars, joinPath(dirPath, e.Name), e.Name, obs))
	}
	return out, nil
}

// listRecursiveWalking implements Recursive List via WalkingBackend.WalkDir
// — walks dirPath's entire subtree, pruning (via filepath.SkipDir) any
// directory whose OWN depth already reaches recursiveListDepth (nothing
// inside it could satisfy a SHALLOWER structurally-valid depth), and
// feeding every FILE entry found at EXACTLY recursiveListDepth into the
// same per-entry pipeline listSingleLevel already uses.
func listRecursiveWalking[T any](ctx context.Context, backend Backend, wb WalkingBackend, core roleCore[T], routing routingInfo, vars map[string]string, dirPath string, obs stats.Observer) ([]ListResult[T], error) {
	var out []ListResult[T]
	walkErr := wb.WalkDir(ctx, dirPath, func(p string, e Entry, err error) error {
		if err != nil {
			return err
		}
		if p == dirPath {
			return nil // root itself — never a candidate entry, always descend into it
		}
		rel, relErr := filepath.Rel(dirPath, p)
		if relErr != nil {
			return relErr
		}
		depth := len(strings.Split(filepath.ToSlash(rel), "/"))
		if e.IsDir {
			if depth >= recursiveListDepth {
				return filepath.SkipDir
			}
			return nil
		}
		if depth != recursiveListDepth {
			return nil // structurally too deep (or too shallow) — pruned, not an error
		}
		out = append(out, listEntry(ctx, backend, core, routing, vars, p, e.Name, obs))
		return nil
	})
	if walkErr != nil {
		return out, FileReadError{Path: dirPath, Err: walkErr}
	}
	return out, nil
}

// listRecursiveManual is the equivalent-power fallback for a Backend
// NOT implementing WalkingBackend — composes the SAME recursiveListDepth
// pruning via repeated ListDir calls instead of one walk. Mirrors
// listRecursiveWalking's own semantics exactly (byte-for-byte equivalent
// output), just driven by ListDir instead of WalkDir.
func listRecursiveManual[T any](ctx context.Context, backend Backend, core roleCore[T], routing routingInfo, vars map[string]string, dirPath string, depth int, obs stats.Observer) ([]ListResult[T], error) {
	entries, err := backend.ListDir(ctx, dirPath)
	if err != nil {
		return nil, FileReadError{Path: dirPath, Err: err}
	}
	var out []ListResult[T]
	for _, e := range entries {
		childDepth := depth + 1
		childPath := joinPath(dirPath, e.Name)
		if e.IsDir {
			if childDepth >= recursiveListDepth {
				continue // pruned — nothing inside could be shallower
			}
			sub, err := listRecursiveManual(ctx, backend, core, routing, vars, childPath, childDepth, obs)
			if err != nil {
				return out, err
			}
			out = append(out, sub...)
			continue
		}
		if childDepth != recursiveListDepth {
			continue
		}
		out = append(out, listEntry(ctx, backend, core, routing, vars, childPath, e.Name, obs))
	}
	return out, nil
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
	// Metadata carry-over (deep-dive-bug-hunt Round 219: Copy silently
	// lost the resource's metadata entirely — neither the native
	// CopyingBackend fast path nor the generic Read+Write fallback
	// below ever consulted MetadataBackend at all). Read src's metadata
	// BEFORE the copy so it can be written to dst AFTER a successful
	// copy, uniformly for BOTH paths below.
	mb, hasMetadata := backend.(MetadataBackend)
	var srcMD map[string]string
	if hasMetadata {
		srcMD, err = mb.ReadMetadata(ctx, srcPath)
		if err != nil {
			return srcPath, dstPath, FileReadError{Path: srcPath, Err: err}
		}
	}
	if cb, ok := backend.(CopyingBackend); ok {
		if err := cb.CopyBytes(ctx, srcPath, dstPath); err != nil {
			return srcPath, dstPath, FileWriteError{Path: dstPath, Err: err}
		}
		if hasMetadata && srcMD != nil {
			if err := mb.WriteMetadata(ctx, dstPath, srcMD); err != nil {
				return srcPath, dstPath, FileWriteError{Path: dstPath, Err: err}
			}
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
	writeOpts := WriteOptions{}
	if hasMetadata && srcMD != nil {
		writeOpts.Metadata = srcMD
	}
	if _, err := backend.WriteBytes(ctx, dstPath, data, writeOpts); err != nil {
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
// docs/design/d-0011-file-api-backend-transparency.md: (1) MovingBackend.MoveBytes if implemented;
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
		// Metadata carry-over (deep-dive-bug-hunt Round 219, same gap as
		// Copy's): os.Rename-style native moves relocate the main file
		// only — a Backend's metadata sidecar (if any) is left orphaned
		// at srcPath and missing at dstPath unless explicitly relocated
		// here. Read BEFORE the move (srcPath still exists), write AFTER
		// (dstPath now exists) — the source's own now-orphaned sidecar
		// is a disclosed, separate, lower-priority cleanup gap (no
		// MetadataBackend.DeleteMetadata method exists to remove it;
		// tracked as a follow-up, not fixed here).
		metaBackend, hasMetadata := backend.(MetadataBackend)
		var srcMD map[string]string
		if hasMetadata {
			srcMD, err = metaBackend.ReadMetadata(ctx, srcPath)
			if err != nil {
				recordMove(obs, srcPath, dstPath, false, time.Since(start))
				return FileReadError{Path: srcPath, Err: err}
			}
		}
		err := mb.MoveBytes(ctx, srcPath, dstPath)
		if err != nil {
			err = FileWriteError{Path: dstPath, Err: err}
			recordMove(obs, srcPath, dstPath, false, time.Since(start))
			return err
		}
		if hasMetadata && srcMD != nil {
			if err := metaBackend.WriteMetadata(ctx, dstPath, srcMD); err != nil {
				recordMove(obs, srcPath, dstPath, false, time.Since(start))
				return FileWriteError{Path: dstPath, Err: err}
			}
		}
		recordMove(obs, srcPath, dstPath, true, time.Since(start))
		return nil
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
// Security is checked per-PATH here directly (not via Deleter[T]'s full
// dispatch, which would redundantly re-resolve the path/wire/Capability
// machinery per key). See docs/design/d-0011-file-api-backend-transparency.md's "Phase 1
// amendment" section for the per-path Security-gating rationale.
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
	ctx, endSpan := startSpan(ctx, obs, "file.batchdelete", "")
	defer func() { endSpan(err) }()

	// Per-PATH Security gate — each path in the batch gets its own
	// runBoundMiddleware call (no single T to offer, mirrors the other
	// no-T roles' invoke shape), independent of its siblings. A
	// rejected path becomes its own BatchDeleteResult.Err up front;
	// surviving paths proceed to the native/fallback delete call below.
	// Tracked by ORIGINAL INDEX (not path string) so duplicate paths in
	// varsList are each handled independently, not collapsed.
	start := time.Now()
	merged := make([]BatchDeleteResult, len(paths))
	survivorIdx := make([]int, 0, len(paths))
	survivors := make([]string, 0, len(paths))
	for i, path := range paths {
		if _, err := runBoundMiddleware(ctx, path, core, nil, nil); err != nil {
			merged[i] = BatchDeleteResult{Path: path, Err: applyErrorPattern(core, err)}
			continue
		}
		survivorIdx = append(survivorIdx, i)
		survivors = append(survivors, path)
	}

	if bdb, ok := backend.(BatchDeletingBackend); ok {
		if len(survivors) > 0 {
			nativeResults, err := bdb.DeleteBytesBatch(ctx, survivors)
			if err != nil {
				recordBatchDelete(obs, paths, 0, len(paths), time.Since(start))
				return nil, err
			}
			for j, r := range nativeResults {
				merged[survivorIdx[j]] = r
			}
		}
	} else {
		for j, path := range survivors {
			existed, deleteErr := backend.DeleteBytes(ctx, path, DeleteOptions{})
			r := BatchDeleteResult{Path: path, Existed: existed}
			if deleteErr != nil {
				r.Err = applyErrorPattern(core, FileDeleteError{Path: path, Err: deleteErr})
			}
			merged[survivorIdx[j]] = r
		}
	}

	success, failure := countResults(merged)
	recordBatchDelete(obs, paths, success, failure, time.Since(start))
	return merged, nil
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

// ── Streaming/chunked reads and writes (Mechanism 2) ────────────────────
//
// See docs/design/d-0011-file-api-backend-transparency.md's "Streaming/chunked reads and writes"
// section for the full design — codec-bypassing raw byte-stream roles,
// distinct from Mechanism 1 (createCore's automatic format-level write
// streaming, above).

// openReadDispatcher is deliberately NON-generic (Group A2, not a Go
// 1.27 generic method) — T only types StreamReader[T]/*StreamReadHandle[T]'s
// own role (which codec-bypassing raw stream to serve), but dispatchOpenRead's
// own return type (io.ReadCloser) never mentions T anywhere (this role
// is the codec-bypassing raw-stream mechanism per its own doc comment
// in client.go, so there is no decoded T to return at all) — making the
// interface generic over T would add a type parameter purely for show,
// with zero compile-time-safety benefit over the existing concrete
// interface type. See docs/design/d-0012-go127-generic-methods.md's Tier 2
// section for the full classification rationale.
type openReadDispatcher interface {
	dispatchOpenRead(ctx context.Context, backend Backend, vars map[string]string, opts StreamReadOptions) (io.ReadCloser, error)
}

func (sr StreamReader[T]) dispatchOpenRead(ctx context.Context, backend Backend, vars map[string]string, opts StreamReadOptions) (io.ReadCloser, error) {
	return openReadCore[T](ctx, backend, sr.core, routingInfo{}, vars, opts)
}
func (h *StreamReadHandle[T]) dispatchOpenRead(ctx context.Context, backend Backend, vars map[string]string, opts StreamReadOptions) (io.ReadCloser, error) {
	return openReadCore[T](ctx, backend, h.core, h.routing, vars, opts)
}

// observedReadCloser wraps a Backend-returned io.ReadCloser so
// RecordFileRead fires on Close() (not Open()) — the natural "operation
// complete" moment for a stream (see the design doc's own "Observer
// integration" subsection). Guards against a double Close() reporting
// twice.
type observedReadCloser struct {
	io.ReadCloser
	obs   stats.Observer
	path  string
	start time.Time
	once  sync.Once
}

func (o *observedReadCloser) Close() error {
	err := o.ReadCloser.Close()
	o.once.Do(func() {
		recordRead(o.obs, o.path, err == nil, time.Since(o.start))
	})
	return err
}

// limitedReadCloser pairs a size-limited io.Reader with the UNDERLYING
// stream's own Closer — used by OpenRead's 2nd-tier (StreamReadBackend)
// fallback, which must still close the raw, un-limited reader.
type limitedReadCloser struct {
	io.Reader
	io.Closer
}

// bytesReaderSlice slices data to [offset:offset+length] (length == 0
// meaning "to end"), clamped to data's own bounds — the 3rd, universal
// OpenRead fallback tier (works on every Phase 1 Backend unchanged).
func bytesReaderSlice(data []byte, offset, length int64) *bytes.Reader {
	if offset < 0 {
		offset = 0
	}
	if offset > int64(len(data)) {
		offset = int64(len(data))
	}
	end := int64(len(data))
	if length > 0 && offset+length < end {
		end = offset + length
	}
	return bytes.NewReader(data[offset:end])
}

// openReadCore implements Client.OpenRead's dispatch — the Security/
// Capability gate runs BEFORE any reader is returned (fail-fast,
// matching every other role's "fail before Backend call" convention),
// then the 3-level fallback cascade: RangeReadingBackend (native) ->
// StreamReadBackend + discard-then-limit (approximated) -> ReadBytes +
// bytes.Reader (universal).
func openReadCore[T any](ctx context.Context, backend Backend, core roleCore[T], routing routingInfo, vars map[string]string, callerOpts StreamReadOptions) (rc io.ReadCloser, err error) {
	defer func() { err = applyErrorPattern(core, err) }()
	obs := obsFromCtx(ctx)
	path, err := core.resource.buildPathWithRouting(routing, vars)
	if err != nil {
		return nil, err
	}
	if _, err := runBoundMiddleware(ctx, path, core, nil, nil); err != nil {
		return nil, err
	}
	ctx, endSpan := startSpan(ctx, obs, "file.openread", path)
	defer func() { endSpan(err) }()
	ranged := callerOpts.Offset != 0 || callerOpts.Length != 0
	start := time.Now()
	var reader io.ReadCloser
	native := false
	switch {
	case ranged:
		switch b := backend.(type) {
		case RangeReadingBackend:
			native = true
			reader, err = b.OpenReaderRange(ctx, path, callerOpts.Offset, callerOpts.Length)
		case StreamReadBackend:
			var raw io.ReadCloser
			raw, err = b.OpenReader(ctx, path)
			if err == nil && callerOpts.Offset > 0 {
				if _, discardErr := io.CopyN(io.Discard, raw, callerOpts.Offset); discardErr != nil {
					_ = raw.Close()
					err = discardErr
				}
			}
			if err == nil {
				if callerOpts.Length > 0 {
					reader = limitedReadCloser{Reader: io.LimitReader(raw, callerOpts.Length), Closer: raw}
				} else {
					reader = raw
				}
			}
		default:
			var data []byte
			data, err = backend.ReadBytes(ctx, path)
			if err == nil {
				reader = io.NopCloser(bytesReaderSlice(data, callerOpts.Offset, callerOpts.Length))
			}
		}
		if err == nil {
			recordRangeRead(obs, path, native, callerOpts.Offset, callerOpts.Length, time.Since(start))
		}
	default:
		if srb, ok := backend.(StreamReadBackend); ok {
			reader, err = srb.OpenReader(ctx, path)
		} else {
			var data []byte
			data, err = backend.ReadBytes(ctx, path)
			if err == nil {
				reader = io.NopCloser(bytes.NewReader(data))
			}
		}
	}
	if err != nil {
		if isNotFound(err) {
			return nil, FileNotFoundError{Path: path}
		}
		return nil, FileReadError{Path: path, Err: err}
	}
	return &observedReadCloser{ReadCloser: reader, obs: obs, path: path, start: start}, nil
}

// openWriteDispatcher is deliberately NON-generic, for the exact same
// reason as [openReadDispatcher] above — dispatchOpenWrite's own return
// type (io.WriteCloser) never mentions T.
type openWriteDispatcher interface {
	dispatchOpenWrite(ctx context.Context, backend Backend, vars map[string]string, opts WriteOptions) (io.WriteCloser, error)
}

func (sw StreamWriter[T]) dispatchOpenWrite(ctx context.Context, backend Backend, vars map[string]string, opts WriteOptions) (io.WriteCloser, error) {
	return openWriteCore[T](ctx, backend, sw.core, routingInfo{}, vars, opts)
}
func (h *StreamWriteHandle[T]) dispatchOpenWrite(ctx context.Context, backend Backend, vars map[string]string, opts WriteOptions) (io.WriteCloser, error) {
	return openWriteCore[T](ctx, backend, h.core, h.routing, vars, opts)
}

// observedWriteCloser wraps a StreamWriteBackend-returned io.WriteCloser
// so RecordFileWrite fires on Close() (the real commit moment —
// StreamWriteBackend.OpenWriter's contract requires Close to commit
// atomically), with success reflecting Close's own return value (a
// mid-stream Write error does NOT by itself flip success to false; only
// Close's own outcome is authoritative, mirroring io.Closer's own
// contract).
type observedWriteCloser struct {
	io.WriteCloser
	obs   stats.Observer
	path  string
	start time.Time
	once  sync.Once
}

func (o *observedWriteCloser) Close() error {
	err := o.WriteCloser.Close()
	o.once.Do(func() {
		recordWrite(o.obs, o.path, err == nil, time.Since(o.start))
	})
	return err
}

// bufferedWriteCloser is OpenWrite's fallback for a Backend without
// StreamWriteBackend — works everywhere (buffers the full write in
// memory, committing via Backend.WriteBytes on Close), just without the
// memory benefit, mirroring every other optional-extension fallback's
// "no false portability, always correct, sometimes slower" idiom.
type bufferedWriteCloser struct {
	backend Backend
	ctx     context.Context
	path    string
	opts    WriteOptions
	obs     stats.Observer
	start   time.Time
	buf     bytes.Buffer
	closed  bool
}

func (b *bufferedWriteCloser) Write(p []byte) (int, error) {
	return b.buf.Write(p)
}

func (b *bufferedWriteCloser) Close() error {
	if b.closed {
		return nil
	}
	b.closed = true
	_, err := b.backend.WriteBytes(b.ctx, b.path, b.buf.Bytes(), b.opts)
	recordWrite(b.obs, b.path, err == nil, time.Since(b.start))
	if err != nil {
		return FileWriteError{Path: b.path, Err: err}
	}
	return nil
}

// openWriteCore implements Client.OpenWrite's dispatch. IfMatch is NOT
// supported on this role this round (see the design doc's own
// disclosure) — callerOpts.IfMatch is simply never consulted here.
func openWriteCore[T any](ctx context.Context, backend Backend, core roleCore[T], routing routingInfo, vars map[string]string, callerOpts WriteOptions) (wc io.WriteCloser, err error) {
	defer func() { err = applyErrorPattern(core, err) }()
	obs := obsFromCtx(ctx)
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
	if _, err := runBoundMiddleware(ctx, path, core, nil, nil); err != nil {
		return nil, err
	}
	ctx, endSpan := startSpan(ctx, obs, "file.openwrite", path)
	defer func() { endSpan(err) }()
	start := time.Now()
	opts := callerOpts
	if wire.NativePOSIXMode != nil {
		opts.Perm = *wire.NativePOSIXMode
	}
	if swb, ok := backend.(StreamWriteBackend); ok {
		var w io.WriteCloser
		w, err = swb.OpenWriter(ctx, path, opts)
		if err != nil {
			return nil, FileWriteError{Path: path, Err: err}
		}
		return &observedWriteCloser{WriteCloser: w, obs: obs, path: path, start: start}, nil
	}
	return &bufferedWriteCloser{backend: backend, ctx: ctx, path: path, opts: opts, obs: obs, start: start}, nil
}
