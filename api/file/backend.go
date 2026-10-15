package file

import (
	"context"
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
