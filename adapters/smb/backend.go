package smb

import (
	"context"
	"encoding/json"
	"io"
	"io/fs"
	"os"
	stdpath "path"
	"strings"

	apifile "github.com/DaniDeer/go-codex/api/file"
	smb2 "github.com/hirochachacha/go-smb2/v2"
)

// shareOps is the narrow slice of *smb2.Share this package depends on —
// covers only the operations api/file.Backend (+ its optional
// extensions) actually need, per the add-a-new-adapter skill's
// narrow-interface rule. Deliberately excludes Symlink/Readlink (no
// api/file.Backend method concerns symlinks), Lock/Unlock (no
// byte-range locking concept in api/file), WaitForChange (no watch/
// notify concept in api/file), and GetSecurityDescriptor/
// SetSecurityDescriptor (ACL manipulation is out of scope — the
// Capability model's AccessMode/NativePOSIXMode axis is the sole
// permission surface, per api/file Phase 1's own established decision).
type shareOps interface {
	Open(ctx context.Context, name string) (fileOps, error)
	Create(ctx context.Context, name string) (fileOps, error)
	OpenFile(ctx context.Context, name string, flag int, perm os.FileMode) (fileOps, error)
	Mkdir(ctx context.Context, name string, perm os.FileMode) error
	MkdirAll(ctx context.Context, path string, perm os.FileMode) error
	Remove(ctx context.Context, name string) error
	RemoveAll(ctx context.Context, path string) error
	Rename(ctx context.Context, oldpath, newpath string) error
	Stat(ctx context.Context, name string) (os.FileInfo, error)
	ReadDir(ctx context.Context, dirname string) ([]os.FileInfo, error)
	Chmod(ctx context.Context, name string, mode os.FileMode) error
	// WithContext returns an io/fs.FS adapter — used ONLY by WalkDir.
	WithContext(ctx context.Context) interface {
		fs.FS
		fs.StatFS
		fs.ReadFileFS
		fs.ReadDirFS
		fs.ReadLinkFS
	}
}

// fileOps is the narrow slice of *smb2.File this package depends on —
// returned by shareOps.Open/Create/OpenFile above. *smb2.File itself
// does NOT satisfy this directly (its Read/Write/etc. take ctx as a
// parameter, not matching io.Reader's signature) — shareAdapter calls
// f.WithContext(ctx) once per open, then uses the result, which DOES
// satisfy fileOps structurally (*boundFile implements
// io.ReadWriteCloser + io.Seeker + io.ReaderAt + io.WriterAt +
// io.ReaderFrom + io.WriterTo + Stat() (fs.FileInfo, error)).
type fileOps interface {
	io.ReadWriteCloser
	io.Seeker
	io.ReaderAt
	io.WriterAt
	io.ReaderFrom
	io.WriterTo
	Stat() (fs.FileInfo, error)
}

// shareAdapter wraps a real *smb2.Share to satisfy shareOps — the
// narrowing glue this package owns so a real caller never has to
// hand-write it themselves (mirrors adapters/s3's own zero-glue-code
// convenience: *smb2.Share's real Open/Create/OpenFile return *smb2.File,
// not fileOps, so it does NOT structurally satisfy shareOps directly).
type shareAdapter struct{ share *smb2.Share }

func (s shareAdapter) Open(ctx context.Context, name string) (fileOps, error) {
	f, err := s.share.Open(ctx, name)
	if err != nil {
		return nil, err
	}
	return f.WithContext(ctx), nil
}

func (s shareAdapter) Create(ctx context.Context, name string) (fileOps, error) {
	f, err := s.share.Create(ctx, name)
	if err != nil {
		return nil, err
	}
	return f.WithContext(ctx), nil
}

func (s shareAdapter) OpenFile(ctx context.Context, name string, flag int, perm os.FileMode) (fileOps, error) {
	f, err := s.share.OpenFile(ctx, name, flag, perm)
	if err != nil {
		return nil, err
	}
	return f.WithContext(ctx), nil
}

func (s shareAdapter) Mkdir(ctx context.Context, name string, perm os.FileMode) error {
	return s.share.Mkdir(ctx, name, perm)
}

func (s shareAdapter) MkdirAll(ctx context.Context, path string, perm os.FileMode) error {
	return s.share.MkdirAll(ctx, path, perm)
}

func (s shareAdapter) Remove(ctx context.Context, name string) error {
	return s.share.Remove(ctx, name)
}

func (s shareAdapter) RemoveAll(ctx context.Context, path string) error {
	return s.share.RemoveAll(ctx, path)
}

func (s shareAdapter) Rename(ctx context.Context, oldpath, newpath string) error {
	return s.share.Rename(ctx, oldpath, newpath)
}

func (s shareAdapter) Stat(ctx context.Context, name string) (os.FileInfo, error) {
	return s.share.Stat(ctx, name)
}

func (s shareAdapter) ReadDir(ctx context.Context, dirname string) ([]os.FileInfo, error) {
	return s.share.ReadDir(ctx, dirname)
}

func (s shareAdapter) Chmod(ctx context.Context, name string, mode os.FileMode) error {
	return s.share.Chmod(ctx, name, mode)
}

func (s shareAdapter) WithContext(ctx context.Context) interface {
	fs.FS
	fs.StatFS
	fs.ReadFileFS
	fs.ReadDirFS
	fs.ReadLinkFS
} {
	return s.share.WithContext(ctx)
}

// BackendOpt configures [NewBackend] — a reserved extension point
// (mirrors adapters/s3.BackendOpt). No concrete option is defined yet:
// unlike adapters/s3's KeyPrefix (useful for scoping one shared bucket
// across tenants), one SMB Share is typically already a dedicated,
// pre-scoped mount point — a genuine, requested use case would need to
// surface before inventing one here.
type BackendOpt interface{ applyBackend(*Backend) }

// Backend is an [apifile.Backend] implementation backed by an SMB2/3
// share via [github.com/hirochachacha/go-smb2/v2]. ALSO implements
// [apifile.CopyingBackend] (io.Copy, server-side fast path auto-engages),
// [apifile.MovingBackend] (Share.Rename — the FIRST Phase 1/2 Backend to
// implement this interface), [apifile.ConditionalWriteBackend] +
// [apifile.NativeConditionalWriteBackend] (lock-file mechanism, same as
// adapters/file, native=false), [apifile.MetadataBackend] (JSON
// sidecar, same as adapters/file), [apifile.StreamWriteBackend]/
// [apifile.StreamReadBackend]/[apifile.RangeReadingBackend] (genuinely
// streaming, native range reads), and [apifile.WalkingBackend]/
// [apifile.DirDeletingBackend]. Deliberately does NOT implement
// [apifile.BatchDeletingBackend]: no native SMB2 batch-delete operation
// exists — see docs/roadmap/file-api.md's "Backend semantic
// differences" table for the full rationale.
type Backend struct {
	ops shareOps
}

// NewBackend wraps share (caller-constructed, caller-authenticated — the
// caller builds *smb2.Dialer{Credentials: ...}, calls Dial/Mount
// themselves) into an [apifile.Backend]. adapters/smb never owns
// credential loading or connection establishment, mirroring
// adapters/s3's identical "narrow-interface-only, caller constructs/
// authenticates the client" precedent — unlike adapters/s3 (where
// *s3.Client directly satisfies the unexported narrow interface with
// zero glue code), *smb2.Share needs this package's own shareAdapter
// internally (its real Open/Create/OpenFile return *smb2.File, not this
// package's fileOps) — NewBackend hides that entirely so a real caller
// never has to hand-write it themselves.
func NewBackend(share *smb2.Share, opts ...BackendOpt) *Backend {
	return newBackendFromOps(shareAdapter{share: share}, opts...)
}

// newBackendFromOps is the test-only entry point — accepts the narrow
// shareOps interface directly so unit tests run against a hand-written
// fake, never a live share (per the add-a-new-adapter skill's own
// Gotcha).
func newBackendFromOps(ops shareOps, opts ...BackendOpt) *Backend {
	b := &Backend{ops: ops}
	for _, opt := range opts {
		opt.applyBackend(b)
	}
	return b
}

// BackendName implements [apifile.Backend].
func (*Backend) BackendName() string { return "adapters/smb" }

// ReadBytes implements [apifile.Backend].
func (b *Backend) ReadBytes(ctx context.Context, path string) ([]byte, error) {
	f, err := b.ops.Open(ctx, path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(f)
}

// missingParentDirs walks UP from dir (forward-slash SMB paths, not
// OS-native filepath), collecting every ancestor directory that does
// not yet exist, returned in root-to-leaf order — mirrors
// adapters/file's own identically-named helper, using b.ops.Stat
// instead of os.Stat.
func (b *Backend) missingParentDirs(ctx context.Context, dir string) []string {
	var missing []string
	cur := dir
	for cur != "" && cur != "." {
		if _, err := b.ops.Stat(ctx, cur); err == nil {
			break // exists already
		}
		missing = append(missing, cur)
		parent := stdpath.Dir(cur)
		if parent == cur || parent == "." {
			break
		}
		cur = parent
	}
	for i, j := 0, len(missing)-1; i < j; i, j = i+1, j-1 {
		missing[i], missing[j] = missing[j], missing[i]
	}
	return missing
}

// WriteBytes implements [apifile.Backend]. opts.CreateDirs uses
// Share.MkdirAll for native recursive parent-dir creation — same
// mechanism Mkdir itself already uses, NOT a manual ancestor walk the
// way adapters/file needs (go-smb2/v2's MkdirAll handles this natively).
// opts.Strict uses an atomic OpenFile(O_CREATE|O_EXCL) at the wire
// level — the SAME guarantee local O_EXCL provides, just
// server-enforced.
func (b *Backend) WriteBytes(ctx context.Context, path string, data []byte, opts apifile.WriteOptions) ([]string, error) {
	var createdDirs []string
	if opts.CreateDirs {
		dir := stdpath.Dir(path)
		createdDirs = b.missingParentDirs(ctx, dir)
		if len(createdDirs) > 0 && !opts.DryRun {
			perm := opts.DirPerm
			if perm == 0 {
				perm = 0755
			}
			if err := b.ops.MkdirAll(ctx, dir, perm); err != nil {
				return createdDirs, err
			}
		}
	}

	if opts.DryRun {
		if opts.Strict {
			if _, err := b.ops.Stat(ctx, path); err == nil {
				return createdDirs, apifile.FileAlreadyExistsError{Path: path}
			} else if !os.IsNotExist(err) {
				return createdDirs, err
			}
		}
		return createdDirs, nil
	}

	perm := opts.Perm
	if perm == 0 {
		perm = 0644
	}

	var f fileOps
	var err error
	if opts.Strict {
		f, err = b.ops.OpenFile(ctx, path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, perm)
		if err != nil {
			if os.IsExist(err) {
				return nil, apifile.FileAlreadyExistsError{Path: path}
			}
			return nil, err
		}
	} else {
		f, err = b.ops.Create(ctx, path)
		if err != nil {
			return nil, err
		}
	}
	_, writeErr := f.Write(data)
	closeErr := f.Close()
	if writeErr != nil {
		return nil, writeErr
	}
	if closeErr != nil {
		return nil, closeErr
	}
	// Share.Create takes no mode parameter at all, and even the Strict
	// path's OpenFile(..., perm) mode is honored only best-effort (a
	// plain Windows SMB share with no Unix-extension support ignores it
	// entirely) — so an explicitly-requested Perm would otherwise be
	// silently dropped on EVERY write, not just an overwrite of an
	// existing file (worse than adapters/file's own equivalent bug,
	// found via the same deep-dive-bug-hunt live-reproduction
	// discipline). Chmod best-effort applies the requested mode
	// afterward when one was explicitly asked for (opts.Perm != 0) —
	// mirrors NativePOSIXMode's own documented degraded-capability
	// precedent elsewhere in this adapter (Samba-hosted shares honor
	// it; plain Windows SMB collapses to its own ACL model and the
	// Chmod call is a harmless best-effort no-op there).
	if opts.Perm != 0 {
		if err := b.ops.Chmod(ctx, path, perm); err != nil {
			return nil, err
		}
	}
	if opts.Metadata != nil {
		if err := b.WriteMetadata(ctx, path, opts.Metadata); err != nil {
			return nil, err
		}
	}
	return nil, nil
}

// DeleteBytes implements [apifile.Backend]. SMB2 DELETE is idempotent on
// a missing file (mirrors os.Remove's own IsNotExist shape through the
// library's NTSTATUS translation), so existed is reported via
// errors.Is(err, os.ErrNotExist) just like adapters/file's own
// DeleteBytes.
func (b *Backend) DeleteBytes(ctx context.Context, path string, opts apifile.DeleteOptions) (bool, error) {
	if opts.DryRun {
		_, statErr := b.ops.Stat(ctx, path)
		existed := statErr == nil
		if !existed && opts.Strict {
			return false, os.ErrNotExist
		}
		return existed, nil
	}
	err := b.ops.Remove(ctx, path)
	if err == nil {
		return true, nil
	}
	if os.IsNotExist(err) {
		if opts.Strict {
			return false, err
		}
		return false, nil
	}
	return false, err
}

// controlFileSuffixes/controlFileInfix mirror adapters/file's own
// identically-named mechanism exactly (same rationale: without this,
// internal/templatematch.MatchNonWildcard's greedy {var} capture would
// silently match a sidecar/lock/temp-commit file against a resource
// template like "{name}.json") — see docs/roadmap/file-api.md's
// "Conditional writes"/"Streaming" sections for the full investigation.
var controlFileSuffixes = []string{".meta.json", ".lock"}

const controlFileInfix = ".tmp-"

func isControlFile(name string) bool {
	for _, suffix := range controlFileSuffixes {
		if strings.HasSuffix(name, suffix) {
			return true
		}
	}
	return strings.Contains(name, controlFileInfix)
}

// toEntry converts an os.FileInfo (as returned by Share.Stat/ReadDir)
// into an apifile.Entry. ETag is a WEAK (ModTime+Size-derived)
// approximation for file entries, empty for directories — mirrors
// adapters/file's own entryETag helper exactly.
//
// Implementation-time correction: earlier design rounds recorded
// "Entry.ETag left empty (SMB has no server-computed content-hash
// concept)" as a disclosed capability gap relative to S3. That framing
// didn't hold up once Conditional writes' own standard workflow
// (Client.Exists/ports.File.Stat -> WriteOptions.IfMatch) was actually
// exercised: SMB has no server-computed hash, but NEITHER does the
// filesystem backend — adapters/file's own Stat/ListDir compute a LOCAL
// ModTime+Size-derived WeakETag instead of leaving it empty, and
// WriteBytesIfMatch already accepts a weak ETag match. Leaving SMB's
// ETag empty would make the Exists-then-IfMatch workflow
// unconditionally fail for every SMB-backed resource — a real,
// user-facing regression this implementation round found and corrected,
// not a design choice to preserve as-is.
func toEntry(info os.FileInfo) apifile.Entry {
	e := apifile.Entry{
		Name:    info.Name(),
		IsDir:   info.IsDir(),
		Size:    info.Size(),
		ModTime: info.ModTime(),
	}
	if !info.IsDir() {
		e.ETag = apifile.WeakETag(info.ModTime(), info.Size())
	}
	return e
}

// ListDir implements [apifile.Backend].
func (b *Backend) ListDir(ctx context.Context, path string) ([]apifile.Entry, error) {
	infos, err := b.ops.ReadDir(ctx, path)
	if err != nil {
		return nil, err
	}
	out := make([]apifile.Entry, 0, len(infos))
	for _, info := range infos {
		if isControlFile(info.Name()) {
			continue
		}
		out = append(out, toEntry(info))
	}
	return out, nil
}

// Stat implements [apifile.Backend].
func (b *Backend) Stat(ctx context.Context, path string) (apifile.Entry, error) {
	info, err := b.ops.Stat(ctx, path)
	if err != nil {
		return apifile.Entry{}, err
	}
	return toEntry(info), nil
}

// Mkdir implements [apifile.Backend]. Uses Share.MkdirAll for native
// recursive parent-dir creation — no manual ancestor walk needed, unlike
// adapters/file's own.
func (b *Backend) Mkdir(ctx context.Context, path string, opts apifile.MkdirOptions) (bool, error) {
	if _, err := b.ops.Stat(ctx, path); err == nil {
		return false, nil
	}
	if opts.DryRun {
		return true, nil
	}
	perm := os.FileMode(0755)
	if opts.NativePOSIXMode != nil {
		perm = *opts.NativePOSIXMode
	}
	if err := b.ops.MkdirAll(ctx, path, perm); err != nil {
		return false, err
	}
	return true, nil
}

// CopyBytes implements [apifile.CopyingBackend] via io.Copy between 2
// open file handles — the library's own ReadFrom/WriteTo special-cases
// automatically engage a server-side copy fast path (FSCTL_SRV_COPYCHUNK)
// when both handles are on the same share; falls back to ordinary
// reads/writes transparently otherwise (the library's own documented
// behavior, not this adapter's concern).
func (b *Backend) CopyBytes(ctx context.Context, srcPath, dstPath string) error {
	src, err := b.ops.Open(ctx, srcPath)
	if err != nil {
		return err
	}
	defer src.Close()
	dst, err := b.ops.Create(ctx, dstPath)
	if err != nil {
		return err
	}
	defer dst.Close()
	_, err = io.Copy(dst, src)
	return err
}

// MoveBytes implements [apifile.MovingBackend] via Share.Rename — a
// direct, native SMB2 rename, typically atomic at the protocol level.
// adapters/smb is the FIRST Phase 1/2 Backend to implement this
// interface (adapters/file chose not to despite os.Rename being equally
// available; adapters/s3 genuinely cannot, since standard S3 buckets
// have no atomic rename at all).
func (b *Backend) MoveBytes(ctx context.Context, srcPath, dstPath string) error {
	return b.ops.Rename(ctx, srcPath, dstPath)
}

// metadataPath mirrors adapters/file's identically-named helper exactly
// — e.g. "notes/alice.json" -> "notes/alice.json.meta.json".
func metadataPath(path string) string { return path + ".meta.json" }

// ReadMetadata implements [apifile.MetadataBackend] via the SAME JSON
// sidecar-file mechanism adapters/file uses — NOT NTFS Alternate Data
// Streams (an NTFS-specific, not-universally-supported feature; see
// docs/roadmap/file-api.md's "2 further technical questions" subsection
// for the full investigation). Returns (nil, nil) — NOT an error — when
// path has no sidecar file.
func (b *Backend) ReadMetadata(ctx context.Context, path string) (map[string]string, error) {
	f, err := b.ops.Open(ctx, metadataPath(path))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(f)
	if err != nil {
		return nil, err
	}
	var md map[string]string
	if err := json.Unmarshal(data, &md); err != nil {
		return nil, err
	}
	return md, nil
}

// WriteMetadata implements [apifile.MetadataBackend] — overwrites path's
// sidecar file entirely (not a merge).
func (b *Backend) WriteMetadata(ctx context.Context, path string, md map[string]string) error {
	data, err := json.Marshal(md)
	if err != nil {
		return err
	}
	f, err := b.ops.Create(ctx, metadataPath(path))
	if err != nil {
		return err
	}
	_, writeErr := f.Write(data)
	closeErr := f.Close()
	if writeErr != nil {
		return writeErr
	}
	return closeErr
}

var (
	_ apifile.Backend         = (*Backend)(nil)
	_ apifile.CopyingBackend  = (*Backend)(nil)
	_ apifile.MovingBackend   = (*Backend)(nil)
	_ apifile.MetadataBackend = (*Backend)(nil)
)
