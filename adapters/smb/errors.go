package smb

import (
	"fmt"
	"log/slog"
)

// SMBError wraps a genuine SMB-specific failure api/file has no typed
// error for at all (e.g. a protocol-level error this package doesn't
// already translate to os.ErrNotExist/os.ErrExist/os.ErrPermission, a
// transport-level error, an authentication failure) — mirrors
// adapters/s3.S3Error's own NARROWED scope exactly: every failure
// api/file ALREADY has a typed error for (not-found, already-exists,
// conditional-mismatch) is translated DIRECTLY via errors.Is, never
// double-wrapped in SMBError first. Both go-smb2/v2 versions translate
// NTSTATUS response codes to stdlib sentinel errors INSIDE the library
// itself (confirmed via source), so — unlike adapters/s3's own
// isS3NotFound classification helper — this package needs no custom
// not-found/already-exists/permission-denied translation logic at all;
// errors.Is(err, os.ErrNotExist)/os.ErrExist/os.ErrPermission work
// directly against whatever the library (or a test fake) returns.
type SMBError struct {
	Operation string // "Open", "Create", "Rename", ...
	Share     string
	Path      string
	Err       error
}

func (e SMBError) Error() string {
	return fmt.Sprintf("smb: %s share=%q path=%q: %s", e.Operation, e.Share, e.Path, e.Err)
}

// Unwrap allows [errors.Is]/[errors.As] to reach the original error.
func (e SMBError) Unwrap() error { return e.Err }

// LogValue implements [slog.LogValuer] for structured logging.
func (e SMBError) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("operation", e.Operation),
		slog.String("share", e.Share),
		slog.String("path", e.Path),
		slog.Any("cause", e.Err),
	)
}
