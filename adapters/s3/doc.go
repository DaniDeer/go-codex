// Package s3 provides an [github.com/DaniDeer/go-codex/api/file.Backend]
// implementation for Amazon S3 (and any S3-compatible object store —
// MinIO, Ceph RGW, Cloudflare R2, Backblaze B2, Wasabi, DigitalOcean
// Spaces, Google Cloud Storage's own interop mode) via
// [github.com/aws/aws-sdk-go-v2].
//
// S3's REST API is a de facto industry standard for object storage — no
// formal standards body, but near-universal "S3-compatible" adoption.
// This Backend works against ANY S3-compatible endpoint by pointing
// aws.Config's BaseEndpoint elsewhere, a bonus of building against the
// standard API rather than a new requirement.
//
// Backend interface mapping:
//
//	api/file interface                    S3 operation
//	-------------------------------------  ----------------------------------
//	Backend.ReadBytes                      GetObject
//	Backend.WriteBytes                     PutObject (Strict -> IfNoneMatch)
//	Backend.DeleteBytes                    DeleteObject
//	Backend.ListDir                        ListObjectsV2 (Delimiter="/")
//	Backend.Stat                           HeadObject
//	Backend.Mkdir                          no-op success (S3 has no directories)
//	CopyingBackend.CopyBytes                CopyObject
//	MetadataBackend.ReadMetadata            HeadObject's Metadata field
//	MetadataBackend.WriteMetadata           CopyObject (self-copy, MetadataDirective=REPLACE)
//	ConditionalWriteBackend.WriteBytesIfMatch  PutObject with IfMatch (TRUE native atomicity)
//	NativeConditionalWriteBackend           always reports true
//	StreamWriteBackend.OpenWriter            PutObject via an io.Pipe bridge
//	StreamReadBackend.OpenReader             GetObject's Body, direct passthrough
//	RangeReadingBackend.OpenReaderRange      GetObject with a Range header (TRUE native)
//	WalkingBackend.WalkDir                   full-prefix ListObjectsV2 (no delimiter)
//	DirDeletingBackend.DeleteDir             ListObjectsV2 + (chunked) DeleteObjects
//	BatchDeletingBackend.DeleteBytesBatch    DeleteObjects, chunked at 1000 keys
//
// MovingBackend is deliberately NOT implemented — S3 has no atomic
// rename; the generic Copy+Delete fallback cascade is the correct S3
// behavior, not a gap.
//
// Client construction is narrow-interface-only: the caller loads
// aws.Config/credentials themselves (config.LoadDefaultConfig or any
// other method) and constructs the real *s3.Client — this package never
// owns credential loading (mirrors adapters/sql never owning *sql.DB
// construction). [NewBackend] accepts the narrow [s3API] interface
// (satisfied transparently by *s3.Client), so unit tests run entirely
// against a hand-written fake — no moto/localstack in CI.
//
// Observer story: every operation maps onto an EXISTING
// stats.FileObserver call dispatch.go itself makes (RecordFileRead/
// RecordFileWrite/RecordFileDelete/RecordFileList/RecordFileCopy/
// RecordFileMkdir/RecordFileBatchDelete/RecordFileRangeRead) — this
// package contributes zero Observer-side code for those. The ONE
// exception is RecordFileConditionalWrite's native flag:
// [Backend.NativeConditionalWrite] always reports true (S3's PutObject
// If-Match is genuinely, transport-guaranteed atomic), the real
// motivating case that flag exists to distinguish from the filesystem
// backend's own lock-file emulation.
//
// See docs/roadmap/file-api.md's "adapters/s3" section for the full
// design history (research grounding, resolved forks, 2 gap-review
// rounds).
package s3
