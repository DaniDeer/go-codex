package s3

// PutOptions is adapters/s3's own concrete
// [github.com/DaniDeer/go-codex/api/file.WriteOptions.BackendOptions]
// type — S3-specific operational hints with no cross-backend meaning
// (storage class, server-side encryption, ACL, tags, content type).
// Supply it via WriteOptions.BackendOptions on Create/Patch calls (or
// the equivalent Client.OpenWrite call); every OTHER Backend simply
// never reads it. NOT consulted by Mkdir — S3's Mkdir is a no-op that
// doesn't accept WriteOptions at all (Mkdir takes MkdirOptions, which
// has no BackendOptions field), so PutOptions can never reach it.
type PutOptions struct {
	// StorageClass is passed through verbatim to S3's own enum
	// ("", "STANDARD_IA", "GLACIER", "INTELLIGENT_TIERING", ...). Empty
	// means "use S3's own default (STANDARD)."
	StorageClass string
	// ServerSideEncryption selects S3-managed encryption ("", "AES256",
	// "aws:kms", "aws:kms:dsse"). Empty means "use the bucket's own
	// default encryption configuration, if any."
	ServerSideEncryption string
	// ACL is a canned ACL string ("", "private", "public-read", ...).
	// Empty means "use the bucket's own default (typically private)."
	ACL string
	// Tags are object tags (S3's key/value tagging feature) — DIFFERENT
	// from Metadata (api/file's own, backend-agnostic metadata axis,
	// which maps onto S3's x-amz-meta-* user metadata instead).
	Tags map[string]string
	// ContentType overrides the object's stored Content-Type. Empty
	// lets the SDK apply its own default ("application/octet-stream").
	ContentType string
}

func (po PutOptions) hasTags() bool { return len(po.Tags) > 0 }
