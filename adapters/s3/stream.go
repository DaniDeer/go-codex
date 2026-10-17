package s3

import (
	"bytes"
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
// bridge: a goroutine spools the pipe's read side into an in-memory
// buffer as the caller writes (decoupling every Write() call from the
// network — no call blocks waiting for a round trip), then commits a
// SINGLE, ordinary PutObject call (a seekable, length-known
// bytes.Reader) once Close() signals EOF.
//
// Disclosed, deliberate trade-off (found while building this package's
// own runnable example): true zero-buffer PutObject streaming of an
// unseekable, length-unknown body is only possible via the AWS SDK's
// HTTPS-only "aws-chunked" trailing-checksum transfer encoding — this
// works correctly against real AWS S3, but is NOT understood by every
// S3-compatible server (the in-memory gofakes3 fake this package's own
// example uses only parses the OLDER, now-SDK-unsupported
// "STREAMING-AWS4-HMAC-SHA256-PAYLOAD" signed-chunk format, confirmed
// via its own source — https://github.com/johannesboyne/gofakes3 as of
// this writing). Spooling to a buffer before the single commit call
// sidesteps this gap entirely and works against EVERY S3-compatible
// server, at the cost of holding the full write in memory for the
// duration of OpenWriter's own commit — consistent with Mechanism 1's
// own already-disclosed caveat elsewhere in this design ("this only
// avoids memory buffering if T's own marshalTo closure itself
// streams... if T holds the payload as an in-memory []byte, this skips
// one intermediate buffer but doesn't fix fundamental memory usage") —
// buffering SOMEWHERE was already an accepted reality in this design,
// not a broken promise.
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

	go func() {
		defer close(finished)
		buf, readErr := io.ReadAll(pr)
		if readErr != nil {
			errc <- readErr
			return
		}

		in := &s3.PutObjectInput{
			Bucket:        aws.String(b.bucket),
			Key:           aws.String(key),
			Body:          bytes.NewReader(buf),
			ContentLength: aws.Int64(int64(len(buf))),
		}
		if opts.Metadata != nil {
			in.Metadata = opts.Metadata
		}
		applyPutOptions(in, opts.BackendOptions)

		if _, err := b.client.PutObject(ctx, in); err != nil {
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
