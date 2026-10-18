// Package smb provides an [github.com/DaniDeer/go-codex/api/file.Backend]
// implementation for SMB2/3 file shares, built on
// [github.com/hirochachacha/go-smb2/v2] — the de facto standard,
// actively-maintained Go SMB2/3 client library.
//
// # Why SMB
//
// SMB (Server Message Block) is the dominant file-sharing protocol on
// Windows networks and a first-class citizen on Linux/macOS via Samba —
// a genuinely enterprise-standard transport, alongside the local
// filesystem ([github.com/DaniDeer/go-codex/adapters/file]) and S3
// ([github.com/DaniDeer/go-codex/adapters/s3]) that Phase 1/2 already
// cover. This is the THIRD real Backend proving api/file's "the exact
// same Resource[T] declarations work unchanged against a different
// backend" promise.
//
// # Dependency disclosure — go-smb2/v2 has no tagged release
//
// go-smb2/v2 (module github.com/hirochachacha/go-smb2/v2) has NEVER had
// a tagged v2.x.y release — only legacy v1.0.x/v1.1.0 tags exist. The
// ctx-first API this package depends on (every Share/File method taking
// context.Context directly, plus the WithContext bridge to stdlib io/fs
// interfaces) lives ONLY on the untagged master branch. go.mod therefore
// pins an exact commit SHA (a Go pseudo-version), not a semver tag —
// re-verified at implementation time per docs/roadmap/file-api.md's own
// disclosed risk. The repository shows signs of active, in-flux
// development (feature branches alongside master, an AGENTS.md at its
// root) — a real signal this API could still shift without any
// release-notes/semver signal at all, a materially different risk
// profile than adapters/s3's tagged aws-sdk-go-v2 dependency.
//
// # Backend interface mapping
//
//	api/file.Backend (+ extensions)            go-smb2/v2 operation
//	ReadBytes                                   Share.Open + read-all
//	WriteBytes                                  Share.Create/OpenFile (Strict: O_CREATE|O_EXCL)
//	DeleteBytes                                 Share.Remove
//	ListDir                                      Share.ReadDir
//	Stat                                          Share.Stat (ETag left empty — no server-computed hash)
//	Mkdir                                         Share.Mkdir / Share.MkdirAll
//	CopyingBackend.CopyBytes                     Open src + Create dst + io.Copy (server-side fast path auto-engages)
//	MovingBackend.MoveBytes                      Share.Rename (native SMB2 rename)
//	ConditionalWriteBackend.WriteBytesIfMatch     lock-file + ETag compare (SAME mechanism as adapters/file; native=false)
//	MetadataBackend                               JSON sidecar file (SAME mechanism as adapters/file; NOT Alternate Data Streams)
//	BatchDeletingBackend                           NOT implemented — no native SMB2 batch-delete
//	StreamWriteBackend.OpenWriter                  temp file via Share.Create + commit via Share.Rename on Close (genuinely streaming, no io.Pipe)
//	StreamReadBackend.OpenReader                    Share.Open + WithContext (direct passthrough)
//	RangeReadingBackend.OpenReaderRange              io.NewSectionReader over the bound file (native=true — ReadAt is first-class SMB2)
//	WalkingBackend.WalkDir                           fs.WalkDir(share.WithContext(ctx), root, fn)
//	DirDeletingBackend.DeleteDir                      non-recursive: ReadDir+empty-check+Remove; recursive: WalkDir enumerates affected, RemoveAll only when !dryRun
//
// # Observer integration
//
// No NEW stats.FileObserver method is needed — every operation above
// maps onto an EXISTING Record* call made ONCE by api/file/dispatch.go
// itself. This package contributes zero Observer-side code beyond the 2
// already-shipped native/approximated markers:
// NativeConditionalWriteBackend.NativeConditionalWrite() reports false
// (no SMB2 compare-and-swap primitive exists); RangeReadingBackend's own
// implementation causes Client.OpenRead to report native=true (ReadAt is
// a first-class SMB2 operation, unlike S3's dedicated wire-level Range
// header).
//
// # Construction
//
// NewBackend accepts the REAL, concrete *smb2.Share directly — the
// caller constructs/authenticates the client themselves
// (*smb2.Dialer{Credentials: ...}, Dial, Mount) and never needs to
// hand-write any glue code; this package wraps the concrete type
// internally to satisfy its own narrow, test-friendly interface.
//
// See docs/roadmap/file-api.md's "adapters/smb" section for the full
// design history (4 review rounds: library/version research, a gap
// review, an untagged-dependency-risk disclosure, and an
// OpenWriter-atomicity fix).
package smb
