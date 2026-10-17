package s3_test

import (
	"bytes"
	"context"
	"crypto/md5" //#nosec G501 -- S3 ETags are MD5-based by convention; not used for any security purpose.
	"fmt"
	"io"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"
)

// fakeAPIError simulates a real smithy-go API error (ErrorCode/
// ErrorMessage/ErrorFault) AND an HTTP status code
// (HTTPStatusCode() int) — both interfaces adapters/s3's own error-
// classification helpers (isS3NotFound/isPreconditionFailed) type-
// assert against via errors.As, exactly as they would against the real
// AWS SDK's own error types.
type fakeAPIError struct {
	code    string
	message string
	status  int
}

func (e *fakeAPIError) Error() string                 { return fmt.Sprintf("%s: %s", e.code, e.message) }
func (e *fakeAPIError) ErrorCode() string             { return e.code }
func (e *fakeAPIError) ErrorMessage() string          { return e.message }
func (e *fakeAPIError) ErrorFault() smithy.ErrorFault { return smithy.FaultClient }
func (e *fakeAPIError) HTTPStatusCode() int           { return e.status }

var (
	errFakeNoSuchKey = &fakeAPIError{code: "NoSuchKey", message: "The specified key does not exist.", status: 404}
	errFakeNotFound  = &fakeAPIError{code: "NotFound", message: "Not Found", status: 404}
	errFakePrecond   = &fakeAPIError{code: "PreconditionFailed", message: "At least one of the pre-conditions you specified did not hold.", status: 412}
	errFakeDenied    = &fakeAPIError{code: "AccessDenied", message: "Access Denied", status: 403}
)

type fakeObject struct {
	data         []byte
	etag         string // hex md5, NO surrounding quotes (trimmed by trimETagQuotes on read)
	metadata     map[string]string
	modTime      time.Time
	storageClass string
	sse          string
	acl          string
	contentType  string
	tagging      string
}

func computeETag(data []byte) string {
	sum := md5.Sum(data) //#nosec G401 -- matches AWS S3's own real ETag convention for non-multipart objects.
	return fmt.Sprintf("%x", sum)
}

// fakeS3 is a hand-written, in-memory fake implementing exactly the
// narrow s3API surface adapters/s3 depends on — no moto/localstack, no
// live AWS account, per the add-a-new-adapter skill's narrow-interface
// testing rule.
type fakeS3 struct {
	mu      sync.Mutex
	bucket  string
	objects map[string]*fakeObject

	// pageSize, when > 0, caps ListObjectsV2's own per-page result
	// count (both Contents+CommonPrefixes combined for simplicity),
	// forcing real multi-page pagination in tests.
	pageSize int

	listCalls, headCalls, getCalls, putCalls, deleteObjectsCalls int
}

func newFakeS3(bucket string) *fakeS3 {
	return &fakeS3{bucket: bucket, objects: make(map[string]*fakeObject)}
}

func (f *fakeS3) checkBucket(b *string) error {
	if b == nil || *b != f.bucket {
		return errFakeDenied
	}
	return nil
}

func (f *fakeS3) GetObject(_ context.Context, in *s3.GetObjectInput, _ ...func(*s3.Options)) (*s3.GetObjectOutput, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.getCalls++
	if err := f.checkBucket(in.Bucket); err != nil {
		return nil, err
	}
	obj, ok := f.objects[aws.ToString(in.Key)]
	if !ok {
		return nil, errFakeNoSuchKey
	}
	data := obj.data
	if in.Range != nil {
		start, end, ok := parseRange(*in.Range, len(data))
		if ok {
			data = data[start:end]
		}
	}
	return &s3.GetObjectOutput{
		Body:          io.NopCloser(bytes.NewReader(data)),
		ContentLength: aws.Int64(int64(len(data))),
		ETag:          aws.String(`"` + obj.etag + `"`),
		LastModified:  aws.Time(obj.modTime),
	}, nil
}

var rangeRe = regexp.MustCompile(`^bytes=(\d+)-(\d*)$`)

func parseRange(hdr string, total int) (start, end int, ok bool) {
	m := rangeRe.FindStringSubmatch(hdr)
	if m == nil {
		return 0, total, false
	}
	start, _ = strconv.Atoi(m[1])
	if m[2] == "" {
		end = total
	} else {
		e, _ := strconv.Atoi(m[2])
		end = e + 1
	}
	if start > total {
		start = total
	}
	if end > total {
		end = total
	}
	return start, end, true
}

func (f *fakeS3) PutObject(_ context.Context, in *s3.PutObjectInput, _ ...func(*s3.Options)) (*s3.PutObjectOutput, error) {
	data, err := io.ReadAll(in.Body)
	if err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.putCalls++
	if cerr := f.checkBucket(in.Bucket); cerr != nil {
		return nil, cerr
	}
	key := aws.ToString(in.Key)
	existing, exists := f.objects[key]

	if in.IfNoneMatch != nil && aws.ToString(in.IfNoneMatch) == "*" && exists {
		return nil, errFakePrecond
	}
	if in.IfMatch != nil {
		want := aws.ToString(in.IfMatch)
		if !exists || existing.etag != want {
			return nil, errFakePrecond
		}
	}

	etag := computeETag(data)
	f.objects[key] = &fakeObject{
		data:         data,
		etag:         etag,
		metadata:     in.Metadata,
		modTime:      time.Now(),
		storageClass: string(in.StorageClass),
		sse:          string(in.ServerSideEncryption),
		acl:          string(in.ACL),
		contentType:  aws.ToString(in.ContentType),
		tagging:      aws.ToString(in.Tagging),
	}
	return &s3.PutObjectOutput{ETag: aws.String(`"` + etag + `"`)}, nil
}

func (f *fakeS3) HeadObject(_ context.Context, in *s3.HeadObjectInput, _ ...func(*s3.Options)) (*s3.HeadObjectOutput, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.headCalls++
	if err := f.checkBucket(in.Bucket); err != nil {
		return nil, err
	}
	obj, ok := f.objects[aws.ToString(in.Key)]
	if !ok {
		return nil, errFakeNotFound
	}
	return &s3.HeadObjectOutput{
		ContentLength: aws.Int64(int64(len(obj.data))),
		ETag:          aws.String(`"` + obj.etag + `"`),
		LastModified:  aws.Time(obj.modTime),
		Metadata:      obj.metadata,
	}, nil
}

func (f *fakeS3) DeleteObject(_ context.Context, in *s3.DeleteObjectInput, _ ...func(*s3.Options)) (*s3.DeleteObjectOutput, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.checkBucket(in.Bucket); err != nil {
		return nil, err
	}
	delete(f.objects, aws.ToString(in.Key)) // idempotent, mirrors real S3
	return &s3.DeleteObjectOutput{}, nil
}

func (f *fakeS3) DeleteObjects(_ context.Context, in *s3.DeleteObjectsInput, _ ...func(*s3.Options)) (*s3.DeleteObjectsOutput, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deleteObjectsCalls++
	if err := f.checkBucket(in.Bucket); err != nil {
		return nil, err
	}
	out := &s3.DeleteObjectsOutput{}
	for _, obj := range in.Delete.Objects {
		key := aws.ToString(obj.Key)
		delete(f.objects, key)
		out.Deleted = append(out.Deleted, types.DeletedObject{Key: aws.String(key)})
	}
	return out, nil
}

func (f *fakeS3) ListObjectsV2(_ context.Context, in *s3.ListObjectsV2Input, _ ...func(*s3.Options)) (*s3.ListObjectsV2Output, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.listCalls++
	if err := f.checkBucket(in.Bucket); err != nil {
		return nil, err
	}
	prefix := aws.ToString(in.Prefix)
	delimiter := aws.ToString(in.Delimiter)

	var keys []string
	for k := range f.objects {
		if strings.HasPrefix(k, prefix) {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)

	// Apply ContinuationToken (a plain numeric offset into the sorted
	// key list, for this fake's own bookkeeping only).
	start := 0
	if tok := aws.ToString(in.ContinuationToken); tok != "" {
		if n, err := strconv.Atoi(tok); err == nil {
			start = n
		}
	}

	out := &s3.ListObjectsV2Output{}
	commonPrefixesSeen := map[string]bool{}
	emitted := 0
	i := start
	pageSize := f.pageSize
	for ; i < len(keys); i++ {
		if pageSize > 0 && emitted >= pageSize {
			break
		}
		key := keys[i]
		rest := strings.TrimPrefix(key, prefix)
		if delimiter != "" {
			if idx := strings.Index(rest, delimiter); idx >= 0 {
				cp := prefix + rest[:idx+len(delimiter)]
				if !commonPrefixesSeen[cp] {
					commonPrefixesSeen[cp] = true
					out.CommonPrefixes = append(out.CommonPrefixes, types.CommonPrefix{Prefix: aws.String(cp)})
					emitted++
				}
				continue
			}
		}
		obj := f.objects[key]
		out.Contents = append(out.Contents, types.Object{
			Key:          aws.String(key),
			Size:         aws.Int64(int64(len(obj.data))),
			LastModified: aws.Time(obj.modTime),
			ETag:         aws.String(`"` + obj.etag + `"`),
		})
		emitted++
	}
	if i < len(keys) {
		out.IsTruncated = aws.Bool(true)
		out.NextContinuationToken = aws.String(strconv.Itoa(i))
	} else {
		out.IsTruncated = aws.Bool(false)
	}
	return out, nil
}

func (f *fakeS3) CopyObject(_ context.Context, in *s3.CopyObjectInput, _ ...func(*s3.Options)) (*s3.CopyObjectOutput, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.checkBucket(in.Bucket); err != nil {
		return nil, err
	}
	src := aws.ToString(in.CopySource)
	// CopySource is "bucket/url-escaped-key".
	parts := strings.SplitN(src, "/", 2)
	if len(parts) != 2 {
		return nil, errFakeNoSuchKey
	}
	srcKey, err := url.QueryUnescape(parts[1])
	if err != nil {
		srcKey = parts[1]
	}
	srcObj, ok := f.objects[srcKey]
	if !ok {
		return nil, errFakeNoSuchKey
	}
	dstKey := aws.ToString(in.Key)
	newObj := *srcObj // copy contents by default
	if in.MetadataDirective == types.MetadataDirectiveReplace {
		newObj.metadata = in.Metadata
	}
	newObj.modTime = time.Now()
	f.objects[dstKey] = &newObj
	return &s3.CopyObjectOutput{}, nil
}

var _ interface {
	GetObject(context.Context, *s3.GetObjectInput, ...func(*s3.Options)) (*s3.GetObjectOutput, error)
	PutObject(context.Context, *s3.PutObjectInput, ...func(*s3.Options)) (*s3.PutObjectOutput, error)
	DeleteObject(context.Context, *s3.DeleteObjectInput, ...func(*s3.Options)) (*s3.DeleteObjectOutput, error)
	DeleteObjects(context.Context, *s3.DeleteObjectsInput, ...func(*s3.Options)) (*s3.DeleteObjectsOutput, error)
	HeadObject(context.Context, *s3.HeadObjectInput, ...func(*s3.Options)) (*s3.HeadObjectOutput, error)
	ListObjectsV2(context.Context, *s3.ListObjectsV2Input, ...func(*s3.Options)) (*s3.ListObjectsV2Output, error)
	CopyObject(context.Context, *s3.CopyObjectInput, ...func(*s3.Options)) (*s3.CopyObjectOutput, error)
} = (*fakeS3)(nil)
