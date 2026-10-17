package s3

import (
	"context"
	"fmt"
	"io"

	apifile "github.com/DaniDeer/go-codex/api/file"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// pipeWriteCloser bridges api/file's io.WriteCloser contract onto a
// single in-flight PutObject call via io.Pipe. See [Backend.OpenWriter]
// for the full design (ctx-cancellation handling, disclosed
// goroutine-leak risk).
type pipeWriteCloser struct {
	pw   *io.PipeWriter
	errc chan error // buffered(1) — written exactly once by the upload goroutine
}

func (w *pipeWriteCloser) Write(p []byte) (int, error) { return w.pw.Write(p) }

func (w *pipeWriteCloser) Close() error {
	closeErr := w.pw.Close()
	err := <-w.errc
	if err != nil {
		return err
	}
	return closeErr
}

// OpenWriter implements [apifile.StreamWriteBackend] via an io.Pipe
// bridge: a goroutine calls PutObject immediately, reading from the
// pipe's read side as the caller writes; Close() closes the pipe
// writer and BLOCKS until the in-flight PutObject call returns,
// surfacing its error (if any) from Close().
//
// ctx cancellation: a second goroutine watches ctx.Done() and, if it
// fires before the upload finishes, calls pr.CloseWithError(ctx.Err())
// so a blocked Write() on the writer side unblocks with that error too,
// rather than deadlocking.
//
// Disclosed goroutine-leak risk: if the caller never calls Close() on
// the returned io.WriteCloser (and ctx is never cancelled), the upload
// goroutine blocks forever waiting for EOF from the pipe — the SAME
// "caller must Close()" discipline every other io.WriteCloser in this
// package already requires, now stated for this specific bridge too.
func (b *Backend) OpenWriter(ctx context.Context, path string, opts apifile.WriteOptions) (io.WriteCloser, error) {
	key := b.key(path)
	pr, pw := io.Pipe()
	errc := make(chan error, 1)
	finished := make(chan struct{})

	in := &s3.PutObjectInput{
		Bucket: aws.String(b.bucket),
		Key:    aws.String(key),
		Body:   pr,
	}
	if opts.Metadata != nil {
		in.Metadata = opts.Metadata
	}
	applyPutOptions(in, opts.BackendOptions)

	go func() {
		defer close(finished)
		_, err := b.client.PutObject(ctx, in)
		if err != nil {
			_ = pr.CloseWithError(err)
			errc <- S3Error{Operation: "PutObject", Bucket: b.bucket, Key: key, Err: err}
			return
		}
		errc <- nil
	}()
	go func() {
		select {
		case <-ctx.Done():
			_ = pr.CloseWithError(ctx.Err())
		case <-finished:
		}
	}()

	return &pipeWriteCloser{pw: pw, errc: errc}, nil
}

// OpenReader implements [apifile.StreamReadBackend] — GetObject's own
// Body io.ReadCloser, returned DIRECTLY: genuinely streaming, no
// buffering at all.
func (b *Backend) OpenReader(ctx context.Context, path string) (io.ReadCloser, error) {
	key := b.key(path)
	out, err := b.client.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(b.bucket), Key: aws.String(key)})
	if err != nil {
		if isS3NotFound(err) {
			return nil, wrapNotFound(err)
		}
		return nil, S3Error{Operation: "GetObject", Bucket: b.bucket, Key: key, Err: err}
	}
	return out.Body, nil
}

// OpenReaderRange implements [apifile.RangeReadingBackend] — the
// PRIMARY real-world motivating case for this interface: GetObject with
// a native, server-side Range header.
func (b *Backend) OpenReaderRange(ctx context.Context, path string, offset, length int64) (io.ReadCloser, error) {
	key := b.key(path)
	var rng string
	if length > 0 {
		rng = fmt.Sprintf("bytes=%d-%d", offset, offset+length-1)
	} else {
		rng = fmt.Sprintf("bytes=%d-", offset)
	}
	out, err := b.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(b.bucket),
		Key:    aws.String(key),
		Range:  aws.String(rng),
	})
	if err != nil {
		if isS3NotFound(err) {
			return nil, wrapNotFound(err)
		}
		return nil, S3Error{Operation: "GetObject", Bucket: b.bucket, Key: key, Err: err}
	}
	return out.Body, nil
}

var (
	_ apifile.StreamWriteBackend  = (*Backend)(nil)
	_ apifile.StreamReadBackend   = (*Backend)(nil)
	_ apifile.RangeReadingBackend = (*Backend)(nil)
)
