package s3

import (
	"bytes"
	"context"
	"io"
	"net/url"
	"strings"
	"time"

	apifile "github.com/DaniDeer/go-codex/api/file"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
)

// s3API is the minimal subset of *s3.Client this package calls — the
// narrow-interface rule (the real client is a concrete struct, not
// interface-shaped), accepted by [NewBackend] so tests run against a
// hand-written fake, never a live AWS account or localstack/moto.
type s3API interface {
	GetObject(ctx context.Context, in *s3.GetObjectInput, opts ...func(*s3.Options)) (*s3.GetObjectOutput, error)
	PutObject(ctx context.Context, in *s3.PutObjectInput, opts ...func(*s3.Options)) (*s3.PutObjectOutput, error)
	DeleteObject(ctx context.Context, in *s3.DeleteObjectInput, opts ...func(*s3.Options)) (*s3.DeleteObjectOutput, error)
	DeleteObjects(ctx context.Context, in *s3.DeleteObjectsInput, opts ...func(*s3.Options)) (*s3.DeleteObjectsOutput, error)
	HeadObject(ctx context.Context, in *s3.HeadObjectInput, opts ...func(*s3.Options)) (*s3.HeadObjectOutput, error)
	ListObjectsV2(ctx context.Context, in *s3.ListObjectsV2Input, opts ...func(*s3.Options)) (*s3.ListObjectsV2Output, error)
	CopyObject(ctx context.Context, in *s3.CopyObjectInput, opts ...func(*s3.Options)) (*s3.CopyObjectOutput, error)
}

// compile-time proof *s3.Client satisfies s3API.
var _ s3API = (*s3.Client)(nil)

// BackendOpt configures [NewBackend] — reserved extension point
// (mirrors api/file.Router's own prefix-composition role, but at the
// Backend level instead).
type BackendOpt interface{ applyBackend(*Backend) }

type keyPrefixOpt string

func (o keyPrefixOpt) applyBackend(b *Backend) { b.prefix = string(o) }

// KeyPrefix scopes every operation under a fixed key prefix (e.g. a
// multi-tenant "tenant-123/" namespace within one shared bucket) —
// transparently joined onto every Resource-declared path, invisible to
// dispatch.
func KeyPrefix(prefix string) BackendOpt { return keyPrefixOpt(prefix) }

// Backend is an [apifile.Backend] implementation backed by Amazon S3 (or
// any S3-compatible object store). See package doc.go for the full
// interface-mapping table.
type Backend struct {
	client s3API
	bucket string
	prefix string
}

// NewBackend wraps client (caller-constructed, caller-authenticated —
// see package doc.go's "Client construction" note) scoped to bucket.
// client is NOT *s3.Client directly but the narrow [s3API] interface
// above — pass an actual *s3.Client built via s3.NewFromConfig(awsCfg),
// or a fake in tests.
func NewBackend(client s3API, bucket string, opts ...BackendOpt) *Backend {
	b := &Backend{client: client, bucket: bucket}
	for _, opt := range opts {
		opt.applyBackend(b)
	}
	return b
}

// key joins the Backend's own KeyPrefix (if any) onto path.
func (b *Backend) key(path string) string {
	path = strings.TrimPrefix(path, "/")
	if b.prefix == "" {
		return path
	}
	return strings.TrimSuffix(b.prefix, "/") + "/" + path
}

// BackendName implements [apifile.Backend].
func (*Backend) BackendName() string { return "adapters/s3" }

// trimETagQuotes strips S3's wire-convention surrounding quote
// characters from an ETag header value (e.g. `"d41d8cd9..."` ->
// `d41d8cd9...`). Returns "" for a nil ETag.
func trimETagQuotes(etag *string) string {
	if etag == nil {
		return ""
	}
	return strings.Trim(*etag, `"`)
}

func int64Value(p *int64) int64 {
	if p == nil {
		return 0
	}
	return *p
}

func timeValue(p *time.Time) time.Time {
	if p == nil {
		return time.Time{}
	}
	return *p
}

// applyPutOptions applies opts (expected to be a [PutOptions] value, or
// nil/any other type — silently ignored) onto in. Mirrors api/file's
// own "a Backend recognizing its own concrete type type-asserts it out;
// every OTHER value is simply never read" idiom.
func applyPutOptions(in *s3.PutObjectInput, backendOptions any) {
	po, ok := backendOptions.(PutOptions)
	if !ok {
		return
	}
	if po.StorageClass != "" {
		in.StorageClass = types.StorageClass(po.StorageClass)
	}
	if po.ServerSideEncryption != "" {
		in.ServerSideEncryption = types.ServerSideEncryption(po.ServerSideEncryption)
	}
	if po.ACL != "" {
		in.ACL = types.ObjectCannedACL(po.ACL)
	}
	if po.ContentType != "" {
		in.ContentType = aws.String(po.ContentType)
	}
	if po.hasTags() {
		in.Tagging = aws.String(encodeTagging(po.Tags))
	}
}

func encodeTagging(tags map[string]string) string {
	v := url.Values{}
	for k, val := range tags {
		v.Set(k, val)
	}
	return v.Encode()
}

// ReadBytes implements [apifile.Backend].
func (b *Backend) ReadBytes(ctx context.Context, path string) ([]byte, error) {
	key := b.key(path)
	out, err := b.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(b.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		if isS3NotFound(err) {
			return nil, wrapNotFound(err)
		}
		return nil, S3Error{Operation: "GetObject", Bucket: b.bucket, Key: key, Err: err}
	}
	defer out.Body.Close()
	return io.ReadAll(out.Body)
}

// WriteBytes implements [apifile.Backend]. opts.CreateDirs/DirPerm/Perm
// are no-ops (documented, never an error — S3 has no directory/
// permission concept). opts.Strict=true -> IfNoneMatch: "*" (native S3
// create-if-absent); a 412 Precondition Failed maps to
// [apifile.FileAlreadyExistsError]. createdDirs is ALWAYS nil (nothing
// was created).
func (b *Backend) WriteBytes(ctx context.Context, path string, data []byte, opts apifile.WriteOptions) ([]string, error) {
	key := b.key(path)
	in := &s3.PutObjectInput{
		Bucket: aws.String(b.bucket),
		Key:    aws.String(key),
		Body:   bytes.NewReader(data),
	}
	if opts.Strict {
		in.IfNoneMatch = aws.String("*")
	}
	if opts.Metadata != nil {
		in.Metadata = opts.Metadata
	}
	applyPutOptions(in, opts.BackendOptions)

	_, err := b.client.PutObject(ctx, in)
	if err != nil {
		if opts.Strict && isPreconditionFailed(err) {
			return nil, apifile.FileAlreadyExistsError{Path: path}
		}
		return nil, S3Error{Operation: "PutObject", Bucket: b.bucket, Key: key, Err: err}
	}
	return nil, nil
}

// DeleteBytes implements [apifile.Backend]. S3's own DELETE is already
// idempotent (succeeds on a missing key) — existed is determined via a
// preceding HeadObject ONLY when opts.Strict (to report
// FileNotFoundError correctly) or opts.DryRun; otherwise a single
// DeleteObject call suffices (existed reported as best-effort true).
func (b *Backend) DeleteBytes(ctx context.Context, path string, opts apifile.DeleteOptions) (bool, error) {
	key := b.key(path)
	if opts.Strict || opts.DryRun {
		_, err := b.client.HeadObject(ctx, &s3.HeadObjectInput{Bucket: aws.String(b.bucket), Key: aws.String(key)})
		if err != nil {
			if !isS3NotFound(err) {
				return false, S3Error{Operation: "HeadObject", Bucket: b.bucket, Key: key, Err: err}
			}
			if opts.Strict {
				return false, wrapNotFound(err)
			}
			return false, nil
		}
		if opts.DryRun {
			return true, nil
		}
	}
	_, err := b.client.DeleteObject(ctx, &s3.DeleteObjectInput{Bucket: aws.String(b.bucket), Key: aws.String(key)})
	if err != nil {
		return false, S3Error{Operation: "DeleteObject", Bucket: b.bucket, Key: key, Err: err}
	}
	return true, nil
}

// ListDir implements [apifile.Backend] via ListObjectsV2 with
// Delimiter="/", auto-paginating through ALL pages. CommonPrefixes
// become Entry{IsDir:true}; Contents become Entry{IsDir:false}. The
// object representing the directory marker itself (key == the search
// prefix) is excluded from results.
func (b *Backend) ListDir(ctx context.Context, path string) ([]apifile.Entry, error) {
	prefix := dirPrefix(b.key(path))

	var out []apifile.Entry
	paginator := s3.NewListObjectsV2Paginator(b.client, &s3.ListObjectsV2Input{
		Bucket:    aws.String(b.bucket),
		Prefix:    aws.String(prefix),
		Delimiter: aws.String("/"),
	})
	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			return nil, S3Error{Operation: "ListObjectsV2", Bucket: b.bucket, Key: prefix, Err: err}
		}
		for _, cp := range page.CommonPrefixes {
			if cp.Prefix == nil {
				continue
			}
			name := strings.TrimSuffix(strings.TrimPrefix(*cp.Prefix, prefix), "/")
			if name == "" {
				continue
			}
			out = append(out, apifile.Entry{Name: name, IsDir: true})
		}
		for _, obj := range page.Contents {
			if obj.Key == nil || *obj.Key == prefix {
				continue // the directory-marker object itself
			}
			name := strings.TrimPrefix(*obj.Key, prefix)
			if name == "" || strings.Contains(name, "/") {
				continue // defensive — Delimiter="/" should prevent this
			}
			out = append(out, apifile.Entry{
				Name:    name,
				IsDir:   false,
				Size:    int64Value(obj.Size),
				ModTime: timeValue(obj.LastModified),
				ETag:    trimETagQuotes(obj.ETag),
			})
		}
	}
	return out, nil
}

// dirPrefix returns key as a directory-style prefix (trailing "/",
// empty key stays "").
func dirPrefix(key string) string {
	if key == "" {
		return ""
	}
	if strings.HasSuffix(key, "/") {
		return key
	}
	return key + "/"
}

func baseName(path string) string {
	path = strings.TrimSuffix(path, "/")
	if idx := strings.LastIndex(path, "/"); idx >= 0 {
		return path[idx+1:]
	}
	return path
}

// Stat implements [apifile.Backend] via HeadObject — no body read.
func (b *Backend) Stat(ctx context.Context, path string) (apifile.Entry, error) {
	key := b.key(path)
	out, err := b.client.HeadObject(ctx, &s3.HeadObjectInput{Bucket: aws.String(b.bucket), Key: aws.String(key)})
	if err != nil {
		if isS3NotFound(err) {
			return apifile.Entry{}, wrapNotFound(err)
		}
		return apifile.Entry{}, S3Error{Operation: "HeadObject", Bucket: b.bucket, Key: key, Err: err}
	}
	return apifile.Entry{
		Name:    baseName(path),
		IsDir:   false,
		Size:    int64Value(out.ContentLength),
		ModTime: timeValue(out.LastModified),
		ETag:    trimETagQuotes(out.ETag),
	}, nil
}

// Mkdir implements [apifile.Backend] as a no-op success — S3 has no
// real directory concept at all.
func (*Backend) Mkdir(_ context.Context, _ string, _ apifile.MkdirOptions) (bool, error) {
	return true, nil
}

// CopyBytes implements [apifile.CopyingBackend] via CopyObject — a true
// server-side copy, zero bytes through the caller process.
func (b *Backend) CopyBytes(ctx context.Context, srcPath, dstPath string) error {
	srcKey := b.key(srcPath)
	dstKey := b.key(dstPath)
	copySource := b.bucket + "/" + url.QueryEscape(srcKey)
	_, err := b.client.CopyObject(ctx, &s3.CopyObjectInput{
		Bucket:     aws.String(b.bucket),
		Key:        aws.String(dstKey),
		CopySource: aws.String(copySource),
	})
	if err != nil {
		if isS3NotFound(err) {
			return wrapNotFound(err)
		}
		return S3Error{Operation: "CopyObject", Bucket: b.bucket, Key: dstKey, Err: err}
	}
	return nil
}

// ReadMetadata implements [apifile.MetadataBackend] via HeadObject's own
// Metadata field (S3 user metadata, x-amz-meta-*) — direct, free (no
// extra call beyond the Stat/Read path already needs HeadObject/
// GetObject anyway).
func (b *Backend) ReadMetadata(ctx context.Context, path string) (map[string]string, error) {
	key := b.key(path)
	out, err := b.client.HeadObject(ctx, &s3.HeadObjectInput{Bucket: aws.String(b.bucket), Key: aws.String(key)})
	if err != nil {
		if isS3NotFound(err) {
			return nil, nil
		}
		return nil, S3Error{Operation: "HeadObject", Bucket: b.bucket, Key: key, Err: err}
	}
	return out.Metadata, nil
}

// WriteMetadata implements [apifile.MetadataBackend] via a SELF-copy
// (CopyObject with CopySource == destination, MetadataDirective
// REPLACE) — S3's ONLY mechanism for a metadata-only update. DISCLOSED
// COST: NOT free like the filesystem adapter's own sidecar-file write —
// this re-transfers the WHOLE object body internally (server-side, so
// no bytes leave AWS's network, but still a real, billed S3 API call
// with its own latency).
func (b *Backend) WriteMetadata(ctx context.Context, path string, md map[string]string) error {
	key := b.key(path)
	copySource := b.bucket + "/" + url.QueryEscape(key)
	_, err := b.client.CopyObject(ctx, &s3.CopyObjectInput{
		Bucket:            aws.String(b.bucket),
		Key:               aws.String(key),
		CopySource:        aws.String(copySource),
		Metadata:          md,
		MetadataDirective: types.MetadataDirectiveReplace,
	})
	if err != nil {
		if isS3NotFound(err) {
			return wrapNotFound(err)
		}
		return S3Error{Operation: "CopyObject", Bucket: b.bucket, Key: key, Err: err}
	}
	return nil
}

// currentETagBestEffort returns key's current ETag via HeadObject,
// best-effort — "" on any failure (including not-found), since this is
// used only to populate ConditionalWriteMismatchError's informational
// Actual field, never to make a correctness decision.
func (b *Backend) currentETagBestEffort(ctx context.Context, key string) string {
	out, err := b.client.HeadObject(ctx, &s3.HeadObjectInput{Bucket: aws.String(b.bucket), Key: aws.String(key)})
	if err != nil {
		return ""
	}
	return trimETagQuotes(out.ETag)
}

// WriteBytesIfMatch implements [apifile.ConditionalWriteBackend] via
// PutObject's native, server-side If-Match — a TRUE atomic conditional
// write, no lock-file mechanism needed at all (S3 handles the
// atomicity server-side).
func (b *Backend) WriteBytesIfMatch(ctx context.Context, path string, data []byte, ifMatch string, opts apifile.WriteOptions) ([]string, error) {
	key := b.key(path)
	in := &s3.PutObjectInput{
		Bucket:  aws.String(b.bucket),
		Key:     aws.String(key),
		Body:    bytes.NewReader(data),
		IfMatch: aws.String(ifMatch),
	}
	if opts.Metadata != nil {
		in.Metadata = opts.Metadata
	}
	applyPutOptions(in, opts.BackendOptions)

	_, err := b.client.PutObject(ctx, in)
	if err != nil {
		if isPreconditionFailed(err) || isS3NotFound(err) {
			return nil, apifile.ConditionalWriteMismatchError{
				Path:     path,
				Expected: ifMatch,
				Actual:   b.currentETagBestEffort(ctx, key),
			}
		}
		return nil, S3Error{Operation: "PutObject", Bucket: b.bucket, Key: key, Err: err}
	}
	return nil, nil
}

// NativeConditionalWrite implements [apifile.NativeConditionalWriteBackend]
// — S3's PutObject If-Match is genuinely, transport-guaranteed atomic.
func (*Backend) NativeConditionalWrite() bool { return true }

var (
	_ apifile.Backend                       = (*Backend)(nil)
	_ apifile.CopyingBackend                = (*Backend)(nil)
	_ apifile.MetadataBackend               = (*Backend)(nil)
	_ apifile.ConditionalWriteBackend       = (*Backend)(nil)
	_ apifile.NativeConditionalWriteBackend = (*Backend)(nil)
)
