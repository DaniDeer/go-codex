package smb

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"path"
	"sort"
	"strings"
	"sync"
	"time"
)

// fakeNode is one entry (file or directory) in fakeShare's in-memory
// tree. Paths are stored/looked-up with forward slashes, trimmed of any
// leading "/" — mirrors how go-smb2/v2's own io/fs bridge presents SMB
// paths.
type fakeNode struct {
	name    string
	isDir   bool
	data    []byte
	modTime time.Time
	mode    os.FileMode
}

// fakeShare is a hand-written, in-memory fake implementing exactly the
// narrow shareOps/fileOps surface adapters/smb depends on — no live
// Samba/broker, per the add-a-new-adapter skill's narrow-interface
// testing rule. Simulates go-smb2/v2's own confirmed behavior of
// translating not-found/already-exists conditions to os.ErrNotExist/
// os.ErrExist directly (this package's own code never needs custom
// NTSTATUS-style classification, unlike adapters/s3's own
// isS3NotFound helper).
type fakeShare struct {
	mu    sync.Mutex
	nodes map[string]*fakeNode // key: clean path, "" is the root directory

	openCalls, createCalls, renameCalls, mkdirAllCalls int
}

func newFakeShare() *fakeShare {
	s := &fakeShare{nodes: make(map[string]*fakeNode)}
	s.nodes[""] = &fakeNode{name: "", isDir: true, modTime: time.Now(), mode: 0755}
	return s
}

func clean(p string) string {
	p = strings.TrimPrefix(path.Clean("/"+p), "/")
	if p == "." {
		return ""
	}
	return p
}

func (s *fakeShare) parentDir(p string) string {
	dir := path.Dir(p)
	if dir == "." || dir == "/" {
		return ""
	}
	return dir
}

func (s *fakeShare) Open(_ context.Context, name string) (fileOps, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.openCalls++
	n, ok := s.nodes[clean(name)]
	if !ok {
		return nil, &os.PathError{Op: "open", Path: name, Err: os.ErrNotExist}
	}
	return &fakeFile{share: s, node: n}, nil
}

// vivifyAncestors lazily creates any missing ancestor directory nodes
// for cp — a test-fake-only simplification standing in for a
// pre-existing directory tree (mirrors how adapters/file's own
// conformance test relies on t.TempDir() having ALREADY created a real
// OS directory before any Backend write happens; this in-memory fake
// has no equivalent pre-existing tree of its own, so it lazily
// vivifies one instead). Caller must hold s.mu.
func (s *fakeShare) vivifyAncestors(cp string) {
	parent := s.parentDir(cp)
	if parent == "" {
		return
	}
	var parts []string
	for _, part := range strings.Split(parent, "/") {
		parts = append(parts, part)
		cur := strings.Join(parts, "/")
		if _, ok := s.nodes[cur]; !ok {
			s.nodes[cur] = &fakeNode{name: part, isDir: true, modTime: time.Now(), mode: 0755}
		}
	}
}

func (s *fakeShare) Create(_ context.Context, name string) (fileOps, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.createCalls++
	cp := clean(name)
	s.vivifyAncestors(cp)
	n := &fakeNode{name: path.Base(cp), modTime: time.Now(), mode: 0644}
	s.nodes[cp] = n
	return &fakeFile{share: s, node: n}, nil
}

func (s *fakeShare) OpenFile(ctx context.Context, name string, flag int, perm os.FileMode) (fileOps, error) {
	s.mu.Lock()
	cp := clean(name)
	_, exists := s.nodes[cp]
	s.mu.Unlock()

	switch {
	case flag&(os.O_CREATE|os.O_EXCL) == (os.O_CREATE | os.O_EXCL):
		if exists {
			return nil, &os.PathError{Op: "open", Path: name, Err: os.ErrExist}
		}
		f, err := s.Create(ctx, name)
		if err != nil {
			return nil, err
		}
		s.mu.Lock()
		s.nodes[cp].mode = perm
		s.mu.Unlock()
		return f, nil
	case flag&os.O_CREATE != 0:
		if !exists {
			return s.Create(ctx, name)
		}
		return s.Open(ctx, name)
	default:
		return s.Open(ctx, name)
	}
}

func (s *fakeShare) Mkdir(_ context.Context, name string, perm os.FileMode) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := clean(name)
	if _, ok := s.nodes[cp]; ok {
		return &os.PathError{Op: "mkdir", Path: name, Err: os.ErrExist}
	}
	parent := s.parentDir(cp)
	if parent != "" {
		if pn, ok := s.nodes[parent]; !ok || !pn.isDir {
			return &os.PathError{Op: "mkdir", Path: name, Err: os.ErrNotExist}
		}
	}
	s.nodes[cp] = &fakeNode{name: path.Base(cp), isDir: true, modTime: time.Now(), mode: perm}
	return nil
}

func (s *fakeShare) MkdirAll(_ context.Context, p string, perm os.FileMode) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.mkdirAllCalls++
	cp := clean(p)
	if cp == "" {
		return nil
	}
	var parts []string
	for _, part := range strings.Split(cp, "/") {
		parts = append(parts, part)
		cur := strings.Join(parts, "/")
		if n, ok := s.nodes[cur]; ok {
			if !n.isDir {
				return &os.PathError{Op: "mkdirall", Path: p, Err: errors.New("not a directory")}
			}
			continue
		}
		s.nodes[cur] = &fakeNode{name: part, isDir: true, modTime: time.Now(), mode: perm}
	}
	return nil
}

func (s *fakeShare) Remove(_ context.Context, name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := clean(name)
	if _, ok := s.nodes[cp]; !ok {
		return &os.PathError{Op: "remove", Path: name, Err: os.ErrNotExist}
	}
	delete(s.nodes, cp)
	return nil
}

func (s *fakeShare) RemoveAll(_ context.Context, p string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := clean(p)
	prefix := cp + "/"
	for k := range s.nodes {
		if k == cp || strings.HasPrefix(k, prefix) {
			delete(s.nodes, k)
		}
	}
	return nil
}

func (s *fakeShare) Rename(_ context.Context, oldpath, newpath string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.renameCalls++
	co, cn := clean(oldpath), clean(newpath)
	n, ok := s.nodes[co]
	if !ok {
		return &os.PathError{Op: "rename", Path: oldpath, Err: os.ErrNotExist}
	}
	delete(s.nodes, co)
	n.name = path.Base(cn)
	s.nodes[cn] = n
	return nil
}

func (s *fakeShare) Stat(_ context.Context, name string) (os.FileInfo, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	n, ok := s.nodes[clean(name)]
	if !ok {
		return nil, &os.PathError{Op: "stat", Path: name, Err: os.ErrNotExist}
	}
	return fakeFileInfo{n}, nil
}

func (s *fakeShare) ReadDir(_ context.Context, dirname string) ([]os.FileInfo, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := clean(dirname)
	if n, ok := s.nodes[cp]; !ok || !n.isDir {
		return nil, &os.PathError{Op: "readdir", Path: dirname, Err: os.ErrNotExist}
	}
	var out []os.FileInfo
	prefix := cp
	if prefix != "" {
		prefix += "/"
	}
	for k, n := range s.nodes {
		if k == cp || !strings.HasPrefix(k, prefix) {
			continue
		}
		rest := strings.TrimPrefix(k, prefix)
		if strings.Contains(rest, "/") {
			continue // not immediate child
		}
		out = append(out, fakeFileInfo{n})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name() < out[j].Name() })
	return out, nil
}

func (s *fakeShare) Chmod(_ context.Context, name string, mode os.FileMode) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	n, ok := s.nodes[clean(name)]
	if !ok {
		return &os.PathError{Op: "chmod", Path: name, Err: os.ErrNotExist}
	}
	n.mode = mode
	return nil
}

func (s *fakeShare) WithContext(ctx context.Context) interface {
	fs.FS
	fs.StatFS
	fs.ReadFileFS
	fs.ReadDirFS
	fs.ReadLinkFS
} {
	return fakeFS{share: s, ctx: ctx}
}

// fakeFileInfo implements os.FileInfo (== fs.FileInfo) over a *fakeNode.
type fakeFileInfo struct{ n *fakeNode }

func (i fakeFileInfo) Name() string       { return i.n.name }
func (i fakeFileInfo) Size() int64        { return int64(len(i.n.data)) }
func (i fakeFileInfo) Mode() os.FileMode  { return i.n.mode }
func (i fakeFileInfo) ModTime() time.Time { return i.n.modTime }
func (i fakeFileInfo) IsDir() bool        { return i.n.isDir }
func (i fakeFileInfo) Sys() any           { return nil }

// fakeFile implements fileOps over a *fakeNode — a simple []byte buffer
// with a read/write offset (no concurrency safety needed beyond what
// fakeShare's own mutex already provides for metadata).
type fakeFile struct {
	share  *fakeShare
	node   *fakeNode
	offset int64
}

func (f *fakeFile) Read(p []byte) (int, error) {
	n, err := f.ReadAt(p, f.offset)
	f.offset += int64(n)
	return n, err
}

func (f *fakeFile) ReadAt(p []byte, off int64) (int, error) {
	f.share.mu.Lock()
	defer f.share.mu.Unlock()
	if off >= int64(len(f.node.data)) {
		return 0, io.EOF
	}
	n := copy(p, f.node.data[off:])
	if n < len(p) {
		return n, io.EOF
	}
	return n, nil
}

func (f *fakeFile) Write(p []byte) (int, error) {
	n, err := f.WriteAt(p, f.offset)
	f.offset += int64(n)
	return n, err
}

func (f *fakeFile) WriteAt(p []byte, off int64) (int, error) {
	f.share.mu.Lock()
	defer f.share.mu.Unlock()
	end := off + int64(len(p))
	if end > int64(len(f.node.data)) {
		grown := make([]byte, end)
		copy(grown, f.node.data)
		f.node.data = grown
	}
	copy(f.node.data[off:end], p)
	f.node.modTime = time.Now()
	return len(p), nil
}

func (f *fakeFile) Seek(offset int64, whence int) (int64, error) {
	switch whence {
	case io.SeekStart:
		f.offset = offset
	case io.SeekCurrent:
		f.offset += offset
	case io.SeekEnd:
		f.offset = int64(len(f.node.data)) + offset
	}
	return f.offset, nil
}

func (f *fakeFile) ReadFrom(r io.Reader) (int64, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return 0, err
	}
	n, writeErr := f.Write(data)
	return int64(n), writeErr
}

func (f *fakeFile) WriteTo(w io.Writer) (int64, error) {
	f.share.mu.Lock()
	data := append([]byte(nil), f.node.data[f.offset:]...)
	f.share.mu.Unlock()
	n, err := w.Write(data)
	f.offset += int64(n)
	return int64(n), err
}

func (f *fakeFile) Close() error { return nil }

func (f *fakeFile) Stat() (fs.FileInfo, error) { return fakeFileInfo{f.node}, nil }

// fakeFS adapts fakeShare to fs.FS/StatFS/ReadFileFS/ReadDirFS/
// ReadLinkFS for [Backend.WalkDir]'s own fs.WalkDir call — symlinks are
// never used by this fake (ReadLinkFS methods are never exercised, but
// must exist to satisfy shareOps.WithContext's declared return type).
type fakeFS struct {
	share *fakeShare
	ctx   context.Context
}

// Open returns ff directly: fileOps' own method set (Read/Close from
// io.ReadWriteCloser, Stat() (fs.FileInfo, error)) already satisfies
// fs.File exactly — no extra wrapper type needed.
func (f fakeFS) Open(name string) (fs.File, error) {
	ff, err := f.share.Open(f.ctx, name)
	if err != nil {
		return nil, err
	}
	return ff, nil
}

func (f fakeFS) Stat(name string) (fs.FileInfo, error) { return f.share.Stat(f.ctx, name) }

func (f fakeFS) ReadFile(name string) ([]byte, error) {
	ff, err := f.share.Open(f.ctx, name)
	if err != nil {
		return nil, err
	}
	defer ff.Close()
	return io.ReadAll(ff)
}

func (f fakeFS) ReadDir(name string) ([]fs.DirEntry, error) {
	infos, err := f.share.ReadDir(f.ctx, name)
	if err != nil {
		return nil, err
	}
	out := make([]fs.DirEntry, 0, len(infos))
	for _, info := range infos {
		out = append(out, fs.FileInfoToDirEntry(info))
	}
	return out, nil
}

func (f fakeFS) ReadLink(name string) (string, error) {
	return "", &os.PathError{Op: "readlink", Path: name, Err: errors.ErrUnsupported}
}

func (f fakeFS) Lstat(name string) (fs.FileInfo, error) { return f.Stat(name) }

var (
	_ shareOps = (*fakeShare)(nil)
	_ fileOps  = (*fakeFile)(nil)
)
