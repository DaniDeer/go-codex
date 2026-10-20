package s3

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"

	"github.com/aws/smithy-go"
)

// S3Error wraps an S3 API failure api/file has NO existing typed error
// for at all (access denied, bucket not found, throttling, ...) — NOT
// used for the cases api/file already covers (FileNotFoundError,
// FileAlreadyExistsError, ConditionalWriteMismatchError), which this
// Backend returns translated-but-unwrapped, letting dispatch-core's own
// wrapping apply exactly like adapters/file already does. Unwrap()-
// reachable back to the original AWS SDK error (itself often a
// smithy-go *smithy.GenericAPIError or a typed *types.NoSuchBucket/
// etc.).
type S3Error struct {
	Operation string // "GetObject", "PutObject", ...
	Bucket    string
	Key       string
	Err       error
}

func (e S3Error) Error() string {
	return fmt.Sprintf("s3: %s bucket=%q key=%q: %s", e.Operation, e.Bucket, e.Key, e.Err)
}

// Unwrap allows [errors.Is]/[errors.As] to reach the original AWS error.
func (e S3Error) Unwrap() error { return e.Err }

// LogValue implements [slog.LogValuer] for structured logging.
func (e S3Error) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("operation", e.Operation),
		slog.String("bucket", e.Bucket),
		slog.String("key", e.Key),
		slog.Any("cause", e.Err),
	)
}

// notFoundError wraps an S3 not-found condition (NoSuchKey/NotFound/a
// generic 404) so it satisfies BOTH errors.Is(err, os.ErrNotExist)
// (preserving api/file/dispatch.go's existing check unchanged, zero
// dispatch-core changes needed) AND errors.Unwrap back to the original
// AWS SDK error — so a caller doing deeper diagnostics (e.g.
// distinguishing throttling from a genuine not-found) can still reach
// it. See docs/design/d-0011-file-api-backend-transparency.md's "adapters/s3" section, Structured
// errors subsection, for the full rationale (including the documented
// caveat that dispatch-core's own FileNotFoundError construction
// discards this Unwrap chain one level up — a pre-existing,
// cross-backend fact, not an S3-specific limitation).
type notFoundError struct{ err error }

func (e notFoundError) Error() string { return e.err.Error() }
func (e notFoundError) Unwrap() error { return e.err }

// Is reports true for os.ErrNotExist, matching the sentinel
// api/file/dispatch.go's isNotFound helper checks via errors.Is.
func (e notFoundError) Is(target error) bool {
	return target == os.ErrNotExist
}

// wrapNotFound returns err wrapped so it satisfies
// errors.Is(wrapped, os.ErrNotExist).
func wrapNotFound(err error) error { return notFoundError{err: err} }

// httpStatusCoder is satisfied by smithy-go's own
// *smithyhttp.ResponseError — matched via errors.As against this local,
// narrow interface instead of importing the concrete type directly,
// consistent with this package's narrow-interface philosophy.
type httpStatusCoder interface {
	HTTPStatusCode() int
}

// isS3NotFound reports whether err represents an S3 not-found condition
// — a typed smithy API error with code "NoSuchKey"/"NotFound"
// (HeadObject's own 404 shape, which the SDK does NOT wrap as
// NoSuchKey) OR a generic smithy API error with HTTP status 404.
func isS3NotFound(err error) bool {
	var api smithy.APIError
	if errors.As(err, &api) {
		switch api.ErrorCode() {
		case "NoSuchKey", "NotFound":
			return true
		}
	}
	var sc httpStatusCoder
	if errors.As(err, &sc) && sc.HTTPStatusCode() == http.StatusNotFound {
		return true
	}
	return false
}

// isPreconditionFailed reports whether err represents S3's 412
// Precondition Failed response — returned by PutObject when IfMatch/
// IfNoneMatch does not hold.
func isPreconditionFailed(err error) bool {
	var sc httpStatusCoder
	if errors.As(err, &sc) && sc.HTTPStatusCode() == http.StatusPreconditionFailed {
		return true
	}
	var api smithy.APIError
	if errors.As(err, &api) && api.ErrorCode() == "PreconditionFailed" {
		return true
	}
	return false
}
