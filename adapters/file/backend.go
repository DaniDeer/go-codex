package file

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	apifile "github.com/DaniDeer/go-codex/api/file"
)

// Backend is an [apifile.Backend] implementation backed directly by the
// local filesystem's os.* calls — the ONE Phase 1 backend, proving the
// api/file abstraction against a REAL adapter. ALSO implements
// [apifile.CopyingBackend] (via io.Copy), [apifile.MovingBackend] (via
// os.Rename), and [apifile.MetadataBackend] (via a JSON sidecar file —
// see metadataPath's doc comment) — genuine fast/real paths for all
// three. Deliberately does NOT implement [apifile.BatchDeletingBackend]:
// a POSIX filesystem has no native batch-delete syscall, so a
// filesystem-side "implementation" would be the IDENTICAL sequential
// loop the generic fallback already runs — see
// docs/design/d-0011-file-api-backend-transparency.md's "Files to create" table for the full
// rationale.
type Backend struct{}

// NewBackend returns a new filesystem-backed [apifile.Backend].
func NewBackend() *Backend { return &Backend{} }

// BackendName implements [apifile.Backend].
func (*Backend) BackendName() string { return "adapters/file" }

// ReadBytes implements [apifile.Backend].
func (*Backend) ReadBytes(_ context.Context, path string) ([]byte, error) {
	return os.ReadFile(path)
}

// missingParentDirs walks UP from dir, collecting every ancestor
// directory that does not yet exist, returned in root-to-leaf order —
// the IDENTICAL multi-level walk [ports.File.Write]'s own
// (now-unexported, pre-migration) missingParentDirs helper performed,
// ported here so CreateDirs' createdDirs report EVERY missing level
// (e.g. 2 entries for a two-level-missing "a/b"), not just the
// immediate parent.
func missingParentDirs(dir string) []string {
	var missing []string
	cur := dir
	for {
		if _, err := os.Stat(cur); err == nil {
			break // exists already
		}
		missing = append(missing, cur)
		parent := filepath.Dir(cur)
		if parent == cur {
			break // reached filesystem root
		}
		cur = parent
	}
	for i, j := 0, len(missing)-1; i < j; i, j = i+1, j-1 {
		missing[i], missing[j] = missing[j], missing[i]
	}
	return missing
}

// WriteBytes implements [apifile.Backend].
func (b *Backend) WriteBytes(ctx context.Context, path string, data []byte, opts apifile.WriteOptions) ([]string, error) {
	var createdDirs []string
	if opts.CreateDirs {
		dir := filepath.Dir(path)
		createdDirs = missingParentDirs(dir)
		if len(createdDirs) > 0 && !opts.DryRun {
			perm := opts.DirPerm
			if perm == 0 {
				perm = 0755
			}
			if err := os.MkdirAll(dir, perm); err != nil {
				return nil, err
			}
		}
	}
	perm := opts.Perm
	if perm == 0 {
		perm = 0644
	}

	if opts.Strict {
		// DryRun+Strict: a preview-only existence check (os.Stat) is
		// correct here since nothing is actually written — matches
		// ports.File.Write's own tested DryRun+Strict precedence (see
		// docs/design/d-0011-file-api-backend-transparency.md's Stage F migration notes).
		if opts.DryRun {
			if _, err := os.Stat(path); err == nil {
				return createdDirs, apifile.FileAlreadyExistsError{Path: path}
			} else if !os.IsNotExist(err) {
				return createdDirs, err
			}
			return createdDirs, nil
		}
		// Real write: use an ATOMIC O_CREATE|O_EXCL open rather than
		// stat-then-write, closing the TOCTOU race window a separate
		// Stat-then-WriteFile sequence would have between the
		// existence check and the actual write.
		f, openErr := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, perm)
		if openErr != nil {
			if os.IsExist(openErr) {
				return createdDirs, apifile.FileAlreadyExistsError{Path: path}
			}
			return createdDirs, openErr
		}
		_, writeErr := f.Write(data)
		closeErr := f.Close()
		if writeErr != nil {
			return createdDirs, writeErr
		}
		if closeErr != nil {
			return createdDirs, closeErr
		}
		if opts.Metadata != nil {
			if err := b.WriteMetadata(ctx, path, opts.Metadata); err != nil {
				return createdDirs, err
			}
		}
		return createdDirs, nil
	}

	if opts.DryRun {
		return createdDirs, nil
	}
	if err := os.WriteFile(path, data, perm); err != nil {
		return createdDirs, err
	}
	// os.WriteFile's own perm argument is ONLY honored when the file is
	// being CREATED — overwriting an already-existing file (the common
	// case for an ordinary Write/Patch call) silently preserves that
	// file's EXISTING mode bits, standard POSIX open() semantics. This
	// would silently defeat the documented "attach-time Capability
	// always wins over a conflicting per-call option" precedence rule
	// (NativePOSIXMode/opts.Perm) for every overwrite — found via a
	// live-reproduction test during the api/file deep-dive-bug-hunt
	// pass (TestPatcher_HandlePatch_NativePOSIXModeCapability_
	// OverridesCallerPerm). Force the EXPLICITLY-requested mode via
	// Chmod only when one was actually requested (opts.Perm != 0) —
	// preserves today's exact behavior for the common case (nobody
	// sets Perm): new files still get the 0644 default, an existing
	// file's mode stays untouched when no explicit mode was asked for.
	if opts.Perm != 0 {
		if err := os.Chmod(path, perm); err != nil {
			return createdDirs, err
		}
	}
	if opts.Metadata != nil {
		if err := b.WriteMetadata(ctx, path, opts.Metadata); err != nil {
			return createdDirs, err
		}
	}
	return createdDirs, nil
}

// DeleteBytes implements [apifile.Backend].
func (*Backend) DeleteBytes(_ context.Context, path string, opts apifile.DeleteOptions) (bool, error) {
	if opts.DryRun {
		_, statErr := os.Stat(path)
		existed := statErr == nil
		if !existed && opts.Strict {
			return false, os.ErrNotExist
		}
		return existed, nil
	}
	err := os.Remove(path)
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

// ListDir implements [apifile.Backend].
// controlFileSuffixes lists this Backend's OWN internal sidecar/lock
// file naming conventions — ".meta.json" ([MetadataBackend]'s sidecar)
// and ".lock" (WriteBytesIfMatch's conditional-write lock file). Every
// entry ending in one of these is excluded from ListDir/WalkDir
// results: without this, internal/templatematch.MatchNonWildcard's
// GREEDY {var} capture would otherwise silently match a sidecar/lock
// file against a resource template like "{name}.json" (e.g.
// "widget.json.lock" or "widget.json.meta.json" both end in the
// literal ".json" the template anchors on), producing a bogus List
// entry with a corrupted captured value — not an error, a silent wrong
// match. See docs/design/d-0011-file-api-backend-transparency.md's "Conditional writes" section,
// "Gap found during pre-implementation review," for the full
// investigation. This is Backend-specific knowledge (a future
// adapters/s3 has neither sidecar objects nor lock files at all), so
// the fix lives here, not in the shared dispatch core.
var controlFileSuffixes = []string{".meta.json", ".lock"}

// controlFileInfix is the ConditionalWriteBackend/StreamWriteBackend
// commit temp-file marker (e.g. "item.json.tmp-823719035", from
// os.CreateTemp(dir, base+".tmp-*")) — a RANDOM numeric suffix follows
// it, so this is a substring (Contains) check, not a HasSuffix one like
// controlFileSuffixes above. Found during Streaming's own
// pre-implementation gap-review (docs/design/d-0011-file-api-backend-transparency.md's
// "Streaming/chunked reads and writes" section) — retroactively applies
// to WriteBytesIfMatch's ALREADY-SHIPPED temp file too, not just
// OpenWrite's new one (both share the identical naming convention).
const controlFileInfix = ".tmp-"

func isControlFile(name string) bool {
	for _, suffix := range controlFileSuffixes {
		if strings.HasSuffix(name, suffix) {
			return true
		}
	}
	return strings.Contains(name, controlFileInfix)
}

// entryETag returns "" for directory entries (no content to identify)
// and a WEAK ([apifile.WeakETag]) ETag otherwise — Stat/ListDir never
// read the body, so a strong (content-hash) ETag isn't available here.
func entryETag(isDir bool, modTime time.Time, size int64) string {
	if isDir {
		return ""
	}
	return apifile.WeakETag(modTime, size)
}

func (*Backend) ListDir(_ context.Context, path string) ([]apifile.Entry, error) {
	if path == "" {
		path = "."
	}
	des, err := os.ReadDir(path)
	if err != nil {
		return nil, err
	}
	out := make([]apifile.Entry, 0, len(des))
	for _, de := range des {
		if isControlFile(de.Name()) {
			continue
		}
		info, err := de.Info()
		if err != nil {
			continue
		}
		out = append(out, apifile.Entry{
			Name:    de.Name(),
			IsDir:   de.IsDir(),
			Size:    info.Size(),
			ModTime: info.ModTime(),
			ETag:    entryETag(info.IsDir(), info.ModTime(), info.Size()),
		})
	}
	return out, nil
}

// Stat implements [apifile.Backend].
func (*Backend) Stat(_ context.Context, path string) (apifile.Entry, error) {
	info, err := os.Stat(path)
	if err != nil {
		return apifile.Entry{}, err
	}
	return apifile.Entry{
		Name:    info.Name(),
		IsDir:   info.IsDir(),
		Size:    info.Size(),
		ModTime: info.ModTime(),
		ETag:    entryETag(info.IsDir(), info.ModTime(), info.Size()),
	}, nil
}

// Mkdir implements [apifile.Backend].
func (*Backend) Mkdir(_ context.Context, path string, opts apifile.MkdirOptions) (bool, error) {
	if _, err := os.Stat(path); err == nil {
		return false, nil
	}
	if opts.DryRun {
		return true, nil
	}
	perm := os.FileMode(0755)
	if opts.NativePOSIXMode != nil {
		perm = *opts.NativePOSIXMode
	}
	if err := os.MkdirAll(path, perm); err != nil {
		return false, err
	}
	return true, nil
}

// CopyBytes implements [apifile.CopyingBackend] via io.Copy between 2
// open *os.File handles — not a true reflink/CoW fast path in Phase 1,
// but still avoids a full decode/re-encode round-trip through the
// caller.
func (*Backend) CopyBytes(_ context.Context, srcPath, dstPath string) error {
	src, err := os.Open(srcPath)
	if err != nil {
		return err
	}
	defer src.Close()
	dst, err := os.Create(dstPath)
	if err != nil {
		return err
	}
	defer dst.Close()
	_, err = io.Copy(dst, src)
	return err
}

// MoveBytes implements [apifile.MovingBackend] via os.Rename — genuinely
// atomic on a single filesystem.
func (*Backend) MoveBytes(_ context.Context, srcPath, dstPath string) error {
	return os.Rename(srcPath, dstPath)
}

// metadataPath returns path's JSON sidecar file path — e.g.
// "notes/alice.json" -> "notes/alice.json.meta.json". A sidecar file,
// NOT real OS extended attributes: keeps this adapter's stdlib-only
// toolchain decision intact (no golang.org/x/sys/unix dependency), is
// portable across Linux/Windows/macOS identically, and arguably mirrors
// S3's OWN "metadata lives beside the object, not inside it" model more
// closely than real xattrs would.
func metadataPath(path string) string { return path + ".meta.json" }

// ReadMetadata implements [apifile.MetadataBackend]. Returns (nil, nil)
// — NOT an error — when path has no sidecar file, mirroring every other
// optional-extension's "harmless absence" idiom in this package.
func (*Backend) ReadMetadata(_ context.Context, path string) (map[string]string, error) {
	data, err := os.ReadFile(metadataPath(path))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var md map[string]string
	if err := json.Unmarshal(data, &md); err != nil {
		return nil, err
	}
	return md, nil
}

// WriteMetadata implements [apifile.MetadataBackend] — overwrites path's
// sidecar file entirely (not a merge), mirroring the interface's own
// documented "overwriting... entirely" contract.
func (*Backend) WriteMetadata(_ context.Context, path string, md map[string]string) error {
	data, err := json.Marshal(md)
	if err != nil {
		return err
	}
	return os.WriteFile(metadataPath(path), data, 0644)
}

// WalkDir implements [apifile.WalkingBackend] via [filepath.WalkDir] —
// the SAME os-level mechanism [ports.Dir]'s own (pre-migration)
// listRecursive/listGlobDiscovery/collectAllPaths helpers used directly,
// now exposed through an interface.
func (b *Backend) WalkDir(ctx context.Context, root string, fn apifile.DirWalkFunc) error {
	return filepath.WalkDir(root, func(p string, de fs.DirEntry, err error) error {
		if err != nil {
			return fn(p, apifile.Entry{}, err)
		}
		// Control files (".meta.json"/".lock") are NEVER reported to fn
		// at all — fully invisible to every WalkDir consumer (Recursive
		// WithList, ports.Dir's recursive/glob-discovery modes), not
		// just call-but-ignored. See isControlFile's own doc comment.
		if !de.IsDir() && isControlFile(de.Name()) {
			return nil
		}
		info, infoErr := de.Info()
		if infoErr != nil {
			return fn(p, apifile.Entry{}, infoErr)
		}
		return fn(p, apifile.Entry{
			Name:    de.Name(),
			IsDir:   de.IsDir(),
			Size:    info.Size(),
			ModTime: info.ModTime(),
			ETag:    entryETag(de.IsDir(), info.ModTime(), info.Size()),
		}, nil)
	})
}

// DeleteDir implements [apifile.DirDeletingBackend]. Mirrors
// [ports.Dir.Delete]'s existing, tested contract exactly (relocated
// from that method's own inline os.* calls, not redesigned).
func (b *Backend) DeleteDir(ctx context.Context, path string, recursive, dryRun bool) ([]string, error) {
	info, statErr := os.Stat(path)
	if statErr != nil {
		return nil, statErr
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("%s is not a directory", path)
	}

	if recursive {
		var affected []string
		if err := b.WalkDir(ctx, path, func(p string, _ apifile.Entry, err error) error {
			if err != nil {
				return err
			}
			affected = append(affected, p)
			return nil
		}); err != nil {
			return nil, err
		}
		if dryRun {
			return affected, nil
		}
		if err := os.RemoveAll(path); err != nil {
			return nil, err
		}
		return affected, nil
	}

	// Non-recursive: path must be empty.
	entries, err := os.ReadDir(path)
	if err != nil {
		return nil, err
	}
	if len(entries) > 0 {
		return nil, apifile.DirNotEmptyError{Path: path}
	}
	if dryRun {
		return []string{path}, nil
	}
	if err := os.Remove(path); err != nil {
		return nil, err
	}
	return []string{path}, nil
}

var _ apifile.Backend = (*Backend)(nil)
var _ apifile.CopyingBackend = (*Backend)(nil)
var _ apifile.MovingBackend = (*Backend)(nil)
var _ apifile.MetadataBackend = (*Backend)(nil)
var _ apifile.WalkingBackend = (*Backend)(nil)
var _ apifile.DirDeletingBackend = (*Backend)(nil)
