package file

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"time"
)

// Entry mirrors [github.com/DaniDeer/go-codex/ports.DirEntry]'s existing
// shape (Name/IsDir/Size/ModTime) — kept as a SEPARATE type (not reusing
// ports.DirEntry directly) so api/file has zero import-time dependency on
// the ports package, consistent with api/* never importing ports (the
// dependency runs the OTHER direction: ports imports api/*, never the
// reverse).
type Entry struct {
	Name    string
	IsDir   bool
	Size    int64
	ModTime time.Time
	// Metadata carries any user-defined key/value metadata the backend
	// has stored alongside this entry (nil when none, or when the
	// attached Backend doesn't implement [MetadataBackend] at all) —
	// populated by [MetadataBackend.ReadMetadata], NEVER by ReadBytes/
	// ListDir/Stat directly. See [MetadataBackend]'s own doc comment for
	// the full design (the "file metadata" merge-field axis
	// BoundMiddleware draws from — api/file's analogue of REST headers/
	// events User Properties).
	Metadata map[string]string
	// ETag is a content-identifying string for optimistic concurrency
	// (see [WriteOptions.IfMatch] / [ConditionalWriteBackend]) — a
	// STRONG (content-hash) value when computed from content already in
	// hand, a WEAK (`W/"..."`, HTTP-style, ModTime+Size-derived)
	// approximation when only metadata is available (e.g. this Entry
	// came from [Backend.Stat]/[Backend.ListDir], which never read the
	// body). Empty for directory entries (IsDir == true — no content to
	// identify). See docs/roadmap/file-api.md's "Conditional writes"
	// section for the full strong-vs-weak computation design.
	ETag string
}

// WriteOptions mirrors today's ports.File FileOptions as closely as
// possible for Create/Patch. Perm/DirPerm/CreateDirs/Strict/DryRun are
// kept VERBATIM from FileOptions (NOT replaced by [Capability]) — see
// "Capability vs. per-call option precedence" in
// docs/roadmap/file-api.md for how Perm interacts with an attach-time
// AccessMode/NativePOSIXMode Capability when BOTH are supplied for the
// same role: the Capability ALWAYS wins.
type WriteOptions struct {
	// Perm is the permission used when creating a new file. Defaults to
	// 0644 when zero.
	Perm os.FileMode
	// CreateDirs, when true, creates any missing parent directories (via
	// Backend.Mkdir) before writing.
	CreateDirs bool
	// DirPerm is the permission used for directories CreateDirs creates.
	// Defaults to 0755 when zero. Has no effect when CreateDirs is false.
	DirPerm os.FileMode
	// Strict, when true, requires path to NOT already exist
	// (FileAlreadyExistsError instead of silently overwriting).
	Strict bool
	// DryRun, when true, reports which directories WOULD be created
	// WITHOUT creating them or writing the file.
	DryRun bool
	// Metadata, when non-nil, is user-defined key/value metadata to
	// persist alongside the write — passed to [MetadataBackend
	// .WriteMetadata] when the attached Backend implements it; silently
	// ignored (never an error) otherwise, mirroring every other
	// optional-extension fallback in this package. See [MetadataBackend]
	// for the full design.
	Metadata map[string]string
	// IfMatch, when non-nil, makes this write CONDITIONAL: it only
	// commits if path's CURRENT strong ETag equals *IfMatch — see the
	// "Conditional writes" section of docs/roadmap/file-api.md for the
	// full cross-backend design. nil (the default) means "unconditional
	// write," byte-for-byte today's existing behavior — zero overhead,
	// zero behavior change for every existing caller. Only consulted
	// when Strict is false — Strict's OWN existence check always wins
	// when both are set on the same call (see that section's Fork 2).
	// Requires the attached Backend to implement
	// [ConditionalWriteBackend]; returns [ConditionalWriteUnsupportedError]
	// otherwise, before any write is attempted.
	IfMatch *string
}

// ConditionalWriteBackend is an OPTIONAL Backend extension for an atomic
// conditional write — mirrors CopyingBackend/MovingBackend/
// BatchDeletingBackend/WalkingBackend/DirDeletingBackend's exact
// type-assertion-guard idiom. A Backend NOT implementing this cannot
// honor [WriteOptions.IfMatch] at all — the dispatch core returns
// [ConditionalWriteUnsupportedError] rather than emulating a non-atomic
// check with a real race window. adapters/file.Backend AND ports's
// private osBackend BOTH implement this (via a lock-file mechanism); a
// future adapters/s3 would implement it via S3's own native, server-side
// If-Match. See docs/roadmap/file-api.md's "Conditional writes" section
// for the full design.
type ConditionalWriteBackend interface {
	// WriteBytesIfMatch behaves like WriteBytes, but commits ONLY if
	// path's CURRENT strong ETag equals ifMatch — atomically with
	// respect to every OTHER conditional write attempt through this
	// same Backend (see each implementation's own doc comment for its
	// exact atomicity scope). Returns
	// [ConditionalWriteMismatchError]{Path, Expected, Actual} (not a
	// generic error) on a mismatch, including when path does not exist
	// at all — no write performed. ifMatch is never "" (dispatch only
	// calls this when WriteOptions.IfMatch is non-nil).
	WriteBytesIfMatch(ctx context.Context, path string, data []byte, ifMatch string, opts WriteOptions) (createdDirs []string, err error)
}

// DeleteOptions mirrors the backend-agnostic subset of today's
// FileOptions relevant to Delete.
type DeleteOptions struct {
	// Strict, when true, requires path to have existed (FileNotFoundError
	// instead of idempotent "ensure absent" success).
	Strict bool
	// DryRun, when true, reports what WOULD happen WITHOUT removing the
	// file.
	DryRun bool
}

// MkdirOptions mirrors the backend-agnostic subset of today's
// FileOptions.DirPerm — see "Capability vs. per-call option precedence"
// in docs/roadmap/file-api.md for how NativePOSIXMode here interacts
// with an attach-time NativePOSIXMode Capability (the Capability always
// wins).
type MkdirOptions struct {
	// NativePOSIXMode, when non-nil, is passed to the backend's own
	// permission-setting call when (and ONLY when) the backend
	// implements POSIX-style permissions at all. nil means "use the
	// backend's own default."
	NativePOSIXMode *os.FileMode
	// DryRun, when true, reports what WOULD happen WITHOUT creating the
	// directory — parity with WriteOptions.DryRun/DeleteOptions.DryRun.
	DryRun bool
}

// ListOptions configures [Client.List]'s traversal depth. Mirrors the
// established 0-or-1 trailing variadic convention Create/Patch/Delete/
// Mkdir already use — backward compatible with every existing List
// call site (opts omitted = today's single-level behavior, byte-for-
// byte unchanged).
type ListOptions struct {
	// Recursive, when true, descends into every subdirectory under the
	// resource's parent directory, not just its immediate children.
	// Default false — existing callers see NO behavior change unless
	// they opt in. See the "Recursive WithList" section of
	// docs/roadmap/file-api.md for the full dispatch/depth-pruning
	// design this field drives.
	Recursive bool
}

// Backend is the ONE interface a storage backend must implement to make
// EVERY declared Resource[T] work against it, regardless of verb. The
// SHARED mechanics (path templating, codec encode/decode, merge-field
// application, Observer calls, ErrorPattern dispatch) live ONCE in
// api/file's own dispatch core; Backend carries ONLY the byte-level,
// backend-specific operations that core calls into.
type Backend interface {
	// ReadBytes returns path's raw content, or a Backend-specific
	// not-found signal the dispatch core recognizes (via errors.Is
	// against os.ErrNotExist for filesystem-family backends) and
	// translates into the SAME FileNotFoundError every backend reports
	// identically.
	ReadBytes(ctx context.Context, path string) ([]byte, error)

	// WriteBytes writes path's content. opts carries the backend-
	// agnostic subset of today's FileOptions that EVERY backend can
	// plausibly honor — a backend that genuinely cannot honor one (e.g.
	// S3 has no directory-permission concept) documents it as a no-op,
	// never an error.
	WriteBytes(ctx context.Context, path string, data []byte, opts WriteOptions) (createdDirs []string, err error)

	// DeleteBytes removes path. existed=false + err=nil is the
	// idempotent "already absent" success case.
	DeleteBytes(ctx context.Context, path string, opts DeleteOptions) (existed bool, err error)

	// ListDir enumerates path's immediate children (non-recursive).
	ListDir(ctx context.Context, path string) ([]Entry, error)

	// Stat returns path's metadata WITHOUT reading its content — mirrors
	// S3's HeadObject exactly. Not-found reports the SAME not-found
	// signal ReadBytes does.
	Stat(ctx context.Context, path string) (Entry, error)

	// Mkdir creates path as a directory, including any missing parents
	// (mirrors os.MkdirAll). created=false (nil error) is the idempotent
	// "already existed" success case. A backend with NO real directory
	// concept (S3) MUST still implement this as a no-op success
	// (created=true, nil error) — never an error.
	Mkdir(ctx context.Context, path string, opts MkdirOptions) (created bool, err error)

	// BackendName returns a descriptor for observability — mirrors every
	// ports.SourceAdapter/SinkAdapter/IOAdapter's own AdapterName()
	// convention.
	BackendName() string
}

// CopyingBackend is an OPTIONAL extension a Backend may implement for a
// native, server-side copy (e.g. S3's CopyObject). A Backend NOT
// implementing this falls back to the dispatch core's own generic
// Read(src)+Write(dst) composition automatically — CopyingBackend only
// ever makes Copy FASTER/cheaper, never changes its observable result.
type CopyingBackend interface {
	CopyBytes(ctx context.Context, srcPath, dstPath string) error
}

// MovingBackend is CopyingBackend's sibling for atomic rename/move (e.g.
// filesystem's os.Rename). A Backend NOT implementing this falls back to
// a generic Copy(src,dst)+Delete(src) composition — delegating to
// Copier's OWN CopyingBackend-or-generic fallback decision, not
// reimplementing it (a 3-level cascade: MovingBackend -> Copier's own
// fallback + Delete -> pure Read+Write+Delete).
type MovingBackend interface {
	MoveBytes(ctx context.Context, srcPath, dstPath string) error
}

// BatchDeletingBackend is an OPTIONAL extension for a native,
// single-wire-call batch delete (e.g. S3's real DeleteObjects). A
// Backend NOT implementing this falls back to N SEQUENTIAL DeleteBytes
// calls, one per requested path, each independently collected into its
// own BatchDeleteResult.
type BatchDeletingBackend interface {
	// DeleteBytesBatch mirrors DeleteObjects' own per-key result shape —
	// results[i] corresponds to paths[i], IN ORDER. A non-nil err here is
	// a CALL-LEVEL failure — per-key failures live in results[i].Err.
	DeleteBytesBatch(ctx context.Context, paths []string) (results []BatchDeleteResult, err error)
}

// BatchDeleteResult is one requested path's outcome from a BatchDelete
// call — Existed mirrors Deleter's own existed-bool idempotent-absence
// convention; Err is nil for both "deleted" and "already absent," set
// only for a genuine per-key backend failure.
type BatchDeleteResult struct {
	Path    string
	Existed bool
	Err     error
}

// DirWalkFunc is called once per visited path (files AND directories, root
// included) during a [WalkingBackend.WalkDir] call. Returning
// [filepath.SkipDir] from fn skips the rest of the current directory's
// contents — mirrors [filepath.WalkDir]'s own sentinel exactly (reused,
// not reinvented, since every Phase 1 WalkingBackend implementation is
// itself filesystem-backed). Any other non-nil error aborts the walk
// and is returned by WalkDir unchanged. err, when non-nil, reports a
// failure reading path itself (mirrors [fs.WalkDirFunc]'s own err
// parameter) — fn may still return filepath.SkipDir to ignore it.
type DirWalkFunc func(path string, entry Entry, err error) error

// WalkingBackend is an OPTIONAL Backend extension for a recursive,
// callback-based directory walk — the primitive behind
// [github.com/DaniDeer/go-codex/ports.Dir]'s recursive-listing and
// glob-discovery modes, and its Delete's recursive affected-path
// collection. A Backend NOT implementing this cannot support those 3
// ports.Dir capabilities (ports.Dir's own callers consume WalkingBackend
// directly — this is NOT wired into api/file's own Client.List in Phase
// 1; see docs/roadmap/file-api.md's "Recursive WithList" Phase 2 outline
// for the forward-looking connection).
type WalkingBackend interface {
	// WalkDir walks the tree rooted at root, calling fn once per visited
	// path (root included, files AND directories) in the SAME order
	// [filepath.WalkDir] would (lexical, depth-first). Returns the first
	// non-nil, non-SkipDir error fn returns, or a walk-level I/O error.
	WalkDir(ctx context.Context, root string, fn DirWalkFunc) error
}

// DirDeletingBackend is an OPTIONAL Backend extension for directory
// deletion with recursive/non-empty-check/dry-run semantics — mirrors
// [github.com/DaniDeer/go-codex/ports.Dir.Delete]'s existing, tested
// contract exactly (relocated from inline os.* calls into the Backend,
// not redesigned).
type DirDeletingBackend interface {
	// DeleteDir removes path. recursive=false requires path to be
	// EMPTY — a non-empty path then returns [DirNotEmptyError] instead
	// of removing anything. recursive=true removes path and everything
	// inside it (mirrors os.RemoveAll). dryRun=true reports what WOULD
	// be removed (affected — root included for a recursive call) WITHOUT
	// removing anything; errors (e.g. DirNotEmptyError) are still
	// reported exactly as a real call would.
	DeleteDir(ctx context.Context, path string, recursive, dryRun bool) (affected []string, err error)
}

// DirNotEmptyError is returned by [DirDeletingBackend.DeleteDir] when
// path is non-empty and recursive is false.
type DirNotEmptyError struct {
	Path string
}

func (e DirNotEmptyError) Error() string {
	return fmt.Sprintf("directory %q is not empty (recursive delete not requested)", e.Path)
}

// LogValue implements [slog.LogValuer] for structured logging.
func (e DirNotEmptyError) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("path", e.Path),
	)
}

// MetadataBackend is an OPTIONAL extension a Backend may implement to
// persist/retrieve USER-DEFINED key/value metadata alongside a
// resource's own bytes — api/file's analogue of REST's headers or
// events' MQTT User Properties: the "extra wire data" axis
// BoundMiddleware's merge-field vocabulary (MetadataParam/
// MergedMetadataParam[T]) extracts from. Grounded in 3 real, well-known
// precedents: S3's object metadata (PutObject/GetObject's x-amz-meta-*
// headers — metadata IS already "out of band" from the object's bytes
// there), POSIX extended attributes, and SMB's own extended-attribute
// support.
//
// A Backend NOT implementing this (or ReadMetadata reporting the path
// has no metadata) is NOT an error — every metadata-merge-field caller
// treats "no MetadataBackend" and "MetadataBackend returned nil" the
// SAME way: the relevant T field(s) stay at their zero value. This
// mirrors CopyingBackend/MovingBackend/BatchDeletingBackend's exact
// "optionally present, harmless absence" idiom — never a NEW required
// method every Backend must implement.
//
// adapters/file's Phase 1 filesystem implementation stores metadata in a
// JSON SIDECAR file (path+".meta.json"), NOT real OS extended attributes
// — see adapters/file/backend.go's own doc comment for why (keeps
// Phase 1's stdlib-only toolchain decision intact, and is arguably a
// closer analogue to S3's "metadata lives beside the object, not in it"
// model than real xattrs would be).
type MetadataBackend interface {
	// ReadMetadata returns path's stored metadata, or (nil, nil) — NOT
	// an error — when path has none (including when path itself doesn't
	// exist; a caller that cares whether path exists at all should use
	// ReadBytes/Stat's own not-found signal, not infer it from this
	// method).
	ReadMetadata(ctx context.Context, path string) (map[string]string, error)

	// WriteMetadata persists md alongside path, overwriting any
	// previously stored metadata for path entirely (not a merge).
	WriteMetadata(ctx context.Context, path string, md map[string]string) error
}

// StreamReadOptions configures [Client.OpenRead]'s byte-range behavior
// (HTTP-Range-style). The zero value (Offset == 0 && Length == 0) means
// "whole file" — byte-for-byte the content ReadBytes would return.
// Range is READ-ONLY: there is no cross-backend semantic for a
// partial-offset WRITE (S3 objects, like filesystem files opened via
// StreamWriteBackend, are only ever written in full — the SAME
// exclusion basis as this package's "no Append" decision) — see
// docs/roadmap/file-api.md's "Streaming/chunked reads and writes"
// section for the full design.
type StreamReadOptions struct {
	// Offset is the first byte to read, 0-based. A non-zero Offset with
	// Length == 0 means "from Offset to end of file."
	Offset int64
	// Length is the number of bytes to read starting at Offset. 0 means
	// "to end of file" when Offset is non-zero, or "whole file" when
	// Offset is also 0.
	Length int64
}

// StreamWriteBackend is an OPTIONAL Backend extension for streaming
// writes — mirrors CopyingBackend/MovingBackend/BatchDeletingBackend/
// WalkingBackend/DirDeletingBackend/ConditionalWriteBackend's exact
// type-assertion-guard idiom. A Backend NOT implementing this keeps
// working via createCore's existing whole-file Marshal+WriteBytes path
// (Mechanism 1) and Client.OpenWrite's buffered fallback (Mechanism
// 2) — just without the memory-usage benefit. See
// docs/roadmap/file-api.md's "Streaming/chunked reads and writes"
// section for the full design, including why this is a DIFFERENT
// mechanism from [Capability] (optional graceful degradation, never
// rejected — not a declare-time hard requirement).
type StreamWriteBackend interface {
	// OpenWriter returns a writer whose Close MUST commit the write
	// atomically (temp-file-in-the-same-directory + rename, mirroring
	// [ConditionalWriteBackend.WriteBytesIfMatch]'s own commit
	// mechanism — ONE atomicity story for every streaming write path in
	// this package) — a reader must never observe a partially-written
	// file. opts carries the same CreateDirs/DirPerm/Perm/Strict/
	// Metadata fields WriteBytes already honors; IfMatch is never set
	// when this is called (dispatch falls back to the non-streaming
	// conditional-write path instead — see the design doc's "IfMatch
	// interaction" note).
	OpenWriter(ctx context.Context, path string, opts WriteOptions) (io.WriteCloser, error)
}

// StreamReadBackend is an OPTIONAL Backend extension for streaming,
// whole-file reads — the 2nd tier of [Client.OpenRead]'s 3-level
// fallback cascade (see [RangeReadingBackend] for the 1st, native tier).
type StreamReadBackend interface {
	OpenReader(ctx context.Context, path string) (io.ReadCloser, error)
}

// RangeReadingBackend is an OPTIONAL Backend extension for a NATIVE
// byte-range read — mirrors S3's own GetObject Range support, the
// primary real-world motivating case for this interface existing at
// all (adapters/file.Backend deliberately does NOT implement this in
// Phase 2 — see the Backend semantic differences table). The 1st,
// fastest tier of [Client.OpenRead]'s 3-level fallback cascade.
type RangeReadingBackend interface {
	OpenReaderRange(ctx context.Context, path string, offset, length int64) (io.ReadCloser, error)
}
