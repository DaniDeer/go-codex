package s3

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	apifile "github.com/DaniDeer/go-codex/api/file"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
)

// WalkDir implements [apifile.WalkingBackend] via a full-prefix
// ListObjectsV2 (NO delimiter, auto-paginated), synthesizing directory
// boundaries from each key's own path segments. root ITSELF is visited
// FIRST (a purely logical/synthetic visit — S3 needs no real object for
// this), matching the interface's documented "root included" contract.
// filepath.SkipDir returned for a directory Entry prunes its entire
// subtree from further visits (tracked via a skip-prefix list, since
// every key for this prefix is already in hand from one listing call —
// unlike filepath.WalkDir's own lazy per-directory reads).
func (b *Backend) WalkDir(ctx context.Context, root string, fn apifile.DirWalkFunc) error {
	prefix := dirPrefix(b.key(root))
	rootTrimmed := strings.TrimSuffix(root, "/")

	if err := fn(root, apifile.Entry{Name: baseName(rootTrimmed), IsDir: true}, nil); err != nil {
		if err == filepath.SkipDir {
			return nil
		}
		return err
	}

	paginator := s3.NewListObjectsV2Paginator(b.client, &s3.ListObjectsV2Input{
		Bucket: aws.String(b.bucket),
		Prefix: aws.String(prefix),
	})

	seenDirs := map[string]bool{}
	var skipPrefixes []string
	isSkipped := func(p string) bool {
		for _, sp := range skipPrefixes {
			if p == sp || strings.HasPrefix(p, sp+"/") {
				return true
			}
		}
		return false
	}

	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			return S3Error{Operation: "ListObjectsV2", Bucket: b.bucket, Key: prefix, Err: err}
		}
		for _, obj := range page.Contents {
			if obj.Key == nil || *obj.Key == prefix {
				continue // the directory-marker object itself
			}
			rel := strings.TrimPrefix(*obj.Key, prefix)
			segments := strings.Split(rel, "/")

			cur := rootTrimmed
			skipped := false
			for i := 0; i < len(segments)-1; i++ {
				cur += "/" + segments[i]
				if isSkipped(cur) {
					skipped = true
					continue
				}
				if seenDirs[cur] {
					continue
				}
				seenDirs[cur] = true
				dirErr := fn(cur, apifile.Entry{Name: segments[i], IsDir: true}, nil)
				if dirErr != nil {
					if dirErr == filepath.SkipDir {
						skipPrefixes = append(skipPrefixes, cur)
						skipped = true
						continue
					}
					return dirErr
				}
			}
			if skipped || isSkipped(cur) {
				continue
			}
			filePath := cur + "/" + segments[len(segments)-1]
			fileErr := fn(filePath, apifile.Entry{
				Name:    segments[len(segments)-1],
				IsDir:   false,
				Size:    int64Value(obj.Size),
				ModTime: timeValue(obj.LastModified),
				ETag:    trimETagQuotes(obj.ETag),
			}, nil)
			if fileErr != nil && fileErr != filepath.SkipDir {
				return fileErr
			}
		}
	}
	return nil
}

// DeleteDir implements [apifile.DirDeletingBackend].
//
// Non-recursive: a 1-page, delimited ListObjectsV2 determines
// emptiness — any CommonPrefix, or any Contents entry other than the
// directory-marker object itself, means non-empty ([apifile.DirNotEmptyError]).
//
// Recursive: a full-prefix (no delimiter) ListObjectsV2, auto-paginated,
// collects every key, then (unless dryRun) deletes them via chunked
// DeleteObjects (the SAME chunking [Backend.DeleteBytesBatch] uses).
// affected ALWAYS includes path itself (gap-review fix: a synthetic/
// logical entry, regardless of whether a literal S3 directory-marker
// object exists for it) for a recursive call.
func (b *Backend) DeleteDir(ctx context.Context, path string, recursive, dryRun bool) ([]string, error) {
	prefix := dirPrefix(b.key(path))

	if !recursive {
		out, err := b.client.ListObjectsV2(ctx, &s3.ListObjectsV2Input{
			Bucket:    aws.String(b.bucket),
			Prefix:    aws.String(prefix),
			Delimiter: aws.String("/"),
		})
		if err != nil {
			return nil, S3Error{Operation: "ListObjectsV2", Bucket: b.bucket, Key: prefix, Err: err}
		}
		nonMarker := false
		for _, obj := range out.Contents {
			if obj.Key != nil && *obj.Key != prefix {
				nonMarker = true
				break
			}
		}
		if len(out.CommonPrefixes) > 0 || nonMarker {
			return nil, apifile.DirNotEmptyError{Path: path}
		}
		if dryRun {
			return []string{path}, nil
		}
		// Delete the directory-marker object, if one exists — a
		// best-effort cleanup (S3 may have no such object at all, and
		// that's fine, there's nothing else to remove for an empty
		// "directory").
		_, _ = b.client.DeleteObject(ctx, &s3.DeleteObjectInput{Bucket: aws.String(b.bucket), Key: aws.String(prefix)})
		return []string{path}, nil
	}

	rootTrimmed := strings.TrimSuffix(path, "/")
	affected := []string{path}
	var keys []string
	paginator := s3.NewListObjectsV2Paginator(b.client, &s3.ListObjectsV2Input{
		Bucket: aws.String(b.bucket),
		Prefix: aws.String(prefix),
	})
	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			return nil, S3Error{Operation: "ListObjectsV2", Bucket: b.bucket, Key: prefix, Err: err}
		}
		for _, obj := range page.Contents {
			if obj.Key == nil {
				continue
			}
			keys = append(keys, *obj.Key)
			if *obj.Key != prefix {
				rel := strings.TrimPrefix(*obj.Key, prefix)
				affected = append(affected, rootTrimmed+"/"+rel)
			}
		}
	}
	if dryRun || len(keys) == 0 {
		return affected, nil
	}
	if err := b.deleteKeysChunked(ctx, keys); err != nil {
		return nil, err
	}
	return affected, nil
}

const deleteObjectsChunkSize = 1000

func chunkStrings(ss []string, size int) [][]string {
	if len(ss) == 0 {
		return nil
	}
	var chunks [][]string
	for i := 0; i < len(ss); i += size {
		end := i + size
		if end > len(ss) {
			end = len(ss)
		}
		chunks = append(chunks, ss[i:end])
	}
	return chunks
}

// deleteKeysChunked deletes keys via chunked DeleteObjects calls (S3's
// own hard 1000-keys-per-request limit), aggregating any per-key
// failures into a single call-level error.
func (b *Backend) deleteKeysChunked(ctx context.Context, keys []string) error {
	for _, chunk := range chunkStrings(keys, deleteObjectsChunkSize) {
		objs := make([]types.ObjectIdentifier, len(chunk))
		for i, k := range chunk {
			objs[i] = types.ObjectIdentifier{Key: aws.String(k)}
		}
		out, err := b.client.DeleteObjects(ctx, &s3.DeleteObjectsInput{
			Bucket: aws.String(b.bucket),
			Delete: &types.Delete{Objects: objs},
		})
		if err != nil {
			return S3Error{Operation: "DeleteObjects", Bucket: b.bucket, Err: err}
		}
		if len(out.Errors) > 0 {
			return S3Error{Operation: "DeleteObjects", Bucket: b.bucket, Err: deleteObjectsErrorsToErr(out.Errors)}
		}
	}
	return nil
}

func deleteObjectsErrorsToErr(errs []types.Error) error {
	if len(errs) == 0 {
		return nil
	}
	e := errs[0]
	key, code, msg := "", "", ""
	if e.Key != nil {
		key = *e.Key
	}
	if e.Code != nil {
		code = *e.Code
	}
	if e.Message != nil {
		msg = *e.Message
	}
	return &deleteObjectsError{key: key, code: code, message: msg, count: len(errs)}
}

type deleteObjectsError struct {
	key, code, message string
	count              int
}

func (e *deleteObjectsError) Error() string {
	if e.count <= 1 {
		return fmt.Sprintf("s3: DeleteObjects: key %q failed: %s: %s", e.key, e.code, e.message)
	}
	return fmt.Sprintf("s3: DeleteObjects: %d keys failed (first: %q: %s: %s)", e.count, e.key, e.code, e.message)
}

// DeleteBytesBatch implements [apifile.BatchDeletingBackend] via
// DeleteObjects, chunked at 1000 keys per call (S3's own hard limit) —
// transparent to the caller, who sees ONE logical batch result
// regardless of how many chunk-calls it took.
func (b *Backend) DeleteBytesBatch(ctx context.Context, paths []string) ([]apifile.BatchDeleteResult, error) {
	results := make([]apifile.BatchDeleteResult, len(paths))
	keyToIdx := make(map[string][]int, len(paths))
	keys := make([]string, len(paths))
	for i, p := range paths {
		key := b.key(p)
		keys[i] = key
		keyToIdx[key] = append(keyToIdx[key], i)
		results[i] = apifile.BatchDeleteResult{Path: p}
	}

	for _, chunk := range chunkStrings(keys, deleteObjectsChunkSize) {
		objs := make([]types.ObjectIdentifier, len(chunk))
		for i, k := range chunk {
			objs[i] = types.ObjectIdentifier{Key: aws.String(k)}
		}
		out, err := b.client.DeleteObjects(ctx, &s3.DeleteObjectsInput{
			Bucket: aws.String(b.bucket),
			Delete: &types.Delete{Objects: objs},
		})
		if err != nil {
			return nil, S3Error{Operation: "DeleteObjects", Bucket: b.bucket, Err: err}
		}
		for _, d := range out.Deleted {
			if d.Key == nil {
				continue
			}
			for _, idx := range keyToIdx[*d.Key] {
				results[idx].Existed = true
			}
		}
		for _, e := range out.Errors {
			if e.Key == nil {
				continue
			}
			code, msg := "", ""
			if e.Code != nil {
				code = *e.Code
			}
			if e.Message != nil {
				msg = *e.Message
			}
			perKeyErr := &deleteObjectsError{key: *e.Key, code: code, message: msg, count: 1}
			for _, idx := range keyToIdx[*e.Key] {
				results[idx].Err = perKeyErr
			}
		}
	}
	return results, nil
}

var (
	_ apifile.WalkingBackend       = (*Backend)(nil)
	_ apifile.DirDeletingBackend   = (*Backend)(nil)
	_ apifile.BatchDeletingBackend = (*Backend)(nil)
)
