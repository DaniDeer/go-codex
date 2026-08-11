package versioning

import (
	"fmt"
	"log/slog"
)

// This file holds this package's structured error types — kept separate
// from version.go/filter.go/classify.go per this module's own
// one-concept-per-file convention, and to mirror go-codex core's own
// errors.go layout (every error type implements both the standard error
// interface and slog.LogValuer for structured logging).

// InvalidVersionError reports that a hand-built Version could not be
// encoded because the branch VersionCodec's dispatch selected (via
// which()) turned out not to actually be set — in practice this is only
// reachable for a Version with ALL THREE fields nil (a zero-value
// Version{}, or one deliberately cleared after construction), since
// which() only ever selects the SemVer/SemVerLike branch when that exact
// field is non-nil. Implements slog.LogValuer for structured logging —
// HasSemVer/HasSemVerLike/HasOther report which fields (if any) were set
// on the invalid value, without dumping the full struct (which may
// contain a large Other string).
type InvalidVersionError struct {
	Version Version
}

func (e InvalidVersionError) Error() string {
	return fmt.Sprintf(
		"versioning: invalid Version: no branch set (SemVer=%v, SemVerLike=%v, Other=%v)",
		e.Version.SemVer != nil, e.Version.SemVerLike != nil, e.Version.Other != nil,
	)
}

// LogValue implements slog.LogValuer for structured logging.
func (e InvalidVersionError) LogValue() slog.Value {
	return slog.GroupValue(
		slog.Bool("has_semver", e.Version.SemVer != nil),
		slog.Bool("has_semver_like", e.Version.SemVerLike != nil),
		slog.Bool("has_other", e.Version.Other != nil),
	)
}
