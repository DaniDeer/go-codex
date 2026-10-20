// Package file provides a stdlib-only local filesystem
// [github.com/DaniDeer/go-codex/api/file.Backend] implementation, PLUS the
// original protocol-agnostic file IO adapter bindings for the ports package.
// These are two distinct roles sharing one package.
//
// # Backend — api/file.Backend implementation
//
// [Backend] (built via [NewBackend]) wraps os.* directly — no external
// dependencies. It implements the core Backend interface
// (ReadBytes/WriteBytes/DeleteBytes/ListDir/Stat/Mkdir/BackendName) plus
// 6 optional extensions:
//
//	api/file interface                    Local filesystem operation
//	-------------------------------------  ----------------------------------
//	CopyingBackend.CopyBytes                io.Copy
//	MovingBackend.MoveBytes                 os.Rename
//	MetadataBackend.ReadMetadata/Write       JSON sidecar file
//	ConditionalWriteBackend.WriteBytesIfMatch  lock-file mechanism (non-native) +
//	                                         strong/weak ETag compare
//	StreamWriteBackend.OpenWriter            temp file + os.Rename commit
//	StreamReadBackend.OpenReader              plain os.Open
//	WalkingBackend.WalkDir                   filepath.WalkDir bridge
//	DirDeletingBackend.DeleteDir              os.RemoveAll (recursive) /
//	                                         os.Remove (non-recursive, non-empty check)
//
// Deliberately NOT implemented: BatchDeletingBackend (no native filesystem
// batch-delete syscall — falls back to api/file's generic sequential loop)
// and RangeReadingBackend (a real os.File.Seek-based range read would be a
// reasonable, cheap future enhancement, not required to ship — see
// [OpenReader]'s own doc comment). [ConditionalWriteBackend]'s
// NativeConditionalWriteBackend always reports false (the lock-file
// mechanism is a client-side emulation, not a transport-guaranteed atomic
// operation — contrast with [github.com/DaniDeer/go-codex/adapters/s3]'s
// native S3 If-Match).
//
// See docs/design/d-0011-file-api-backend-transparency.md for the full
// design history.
//
// # ports IO adapter bindings
//
// All ports bindings are stdlib-only (no external dependencies). They
// implement the [ports.SourceAdapter], [ports.SinkAdapter], and
// [ports.IOAdapter] interfaces and are wired to pipelines via
// [ports.SourcePort.Bind], [ports.SinkPort.Bind], and [ports.IOPort.Bind].
//
// Sources (use with [ports.SourcePort]):
//   - [ScanAdapter] — decodes a newline-delimited file (NDJSON, CSV, etc.) line by line
//   - [WatchAdapter] — emits file paths for new files created in a directory
//
// Intermediate (use with [ports.IOPort]):
//   - [ReadEachAdapter] — reads a complete typed file for each upstream item (enrichment)
//
// Sinks (use with [ports.SinkPort]):
//   - [DrainWriteAdapter] — encodes each item and writes it as a line to an [io.Writer]
//   - [DrainWriteFileAdapter] — writes each item as a complete typed file (whole-file overwrite)
//   - [DrainPatchAdapter] — applies each item as an untyped map[string]any partial
//     update (JSON Merge Patch semantics) via [ports.File.Patch]; map-based formats
//     (JSON/YAML/TOML/[format.New]) only
//   - [DrainPatchEncodedAdapter] — applies each item as a typed partial update via
//     [ports.File.PatchEncoded]; same map-based-format restriction
package file
