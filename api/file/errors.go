package file

import (
	"fmt"
	"log/slog"
)

// FileNotFoundError is returned by [Deleter]'s dispatch when
// [DeleteOptions.Strict] is set and the file did not exist — Delete's
// default is idempotent "ensure absent" success; Strict opts out of that.
// Also returned by [Reader]'s dispatch when the attached [Backend] reports
// the path as absent.
type FileNotFoundError struct {
	Path string
}

func (e FileNotFoundError) Error() string {
	return fmt.Sprintf("file %q not found", e.Path)
}

// LogValue implements [slog.LogValuer] for structured logging.
func (e FileNotFoundError) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("path", e.Path),
	)
}

// FileReadError is returned when a [Backend.ReadBytes] call fails for a
// reason other than "not found".
type FileReadError struct {
	Path string
	Err  error
}

func (e FileReadError) Error() string { return fmt.Sprintf("read file %q: %s", e.Path, e.Err) }

// Unwrap allows [errors.Is] and [errors.As] to traverse the underlying error.
func (e FileReadError) Unwrap() error { return e.Err }

// LogValue implements [slog.LogValuer] for structured logging.
func (e FileReadError) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("path", e.Path),
		slog.Any("cause", e.Err),
	)
}

// FileWriteError is returned when a [Backend.WriteBytes] call fails after
// successful encoding.
type FileWriteError struct {
	Path string
	Err  error
}

func (e FileWriteError) Error() string { return fmt.Sprintf("write file %q: %s", e.Path, e.Err) }

// Unwrap allows [errors.Is] and [errors.As] to traverse the underlying error.
func (e FileWriteError) Unwrap() error { return e.Err }

// LogValue implements [slog.LogValuer] for structured logging.
func (e FileWriteError) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("path", e.Path),
		slog.Any("cause", e.Err),
	)
}

// FileDecodeError is returned by [Reader]'s dispatch when format decoding
// or codec validation fails after a successful [Backend.ReadBytes].
//
// The wrapped error is typically a [codex.ValidationErrors] — use
// [errors.As] to extract per-field details.
type FileDecodeError struct {
	Path string
	Err  error
}

func (e FileDecodeError) Error() string {
	return fmt.Sprintf("decode file %q: %s", e.Path, e.Err)
}

// Unwrap allows [errors.Is] and [errors.As] to traverse the underlying error.
func (e FileDecodeError) Unwrap() error { return e.Err }

// LogValue implements [slog.LogValuer] for structured logging.
func (e FileDecodeError) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("path", e.Path),
		slog.Any("cause", e.Err),
	)
}

// FileEncodeError is returned by [Creator]'s dispatch when format encoding
// or codec validation fails before any [Backend.WriteBytes] call.
type FileEncodeError struct {
	Path string
	Err  error
}

func (e FileEncodeError) Error() string {
	return fmt.Sprintf("encode for file %q: %s", e.Path, e.Err)
}

// Unwrap allows [errors.Is] and [errors.As] to traverse the underlying error.
func (e FileEncodeError) Unwrap() error { return e.Err }

// LogValue implements [slog.LogValuer] for structured logging.
func (e FileEncodeError) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("path", e.Path),
		slog.Any("cause", e.Err),
	)
}

// FileAlreadyExistsError is returned by [Creator]'s dispatch when
// [WriteOptions.Strict] is set and the file already exists (O_CREATE|
// O_EXCL semantics) — no write is performed.
type FileAlreadyExistsError struct {
	Path string
}

func (e FileAlreadyExistsError) Error() string {
	return fmt.Sprintf("file %q already exists (Strict)", e.Path)
}

// LogValue implements [slog.LogValuer] for structured logging.
func (e FileAlreadyExistsError) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("path", e.Path),
	)
}

// FileDeleteError is returned when a [Backend.DeleteBytes] call fails for
// a reason OTHER than "already absent" (e.g. permission denied).
type FileDeleteError struct {
	Path string
	Err  error
}

func (e FileDeleteError) Error() string { return fmt.Sprintf("delete file %q: %s", e.Path, e.Err) }

// Unwrap allows [errors.Is] and [errors.As] to traverse the underlying error.
func (e FileDeleteError) Unwrap() error { return e.Err }

// LogValue implements [slog.LogValuer] for structured logging.
func (e FileDeleteError) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("path", e.Path),
		slog.Any("cause", e.Err),
	)
}

// FilePathParamError is returned when a path template variable's value
// fails its registered codec.
type FilePathParamError struct {
	Name  string // placeholder name (without braces)
	Value string // the value that failed validation
	Err   error  // underlying constraint or codec error
}

func (e FilePathParamError) Error() string {
	return fmt.Sprintf("file path variable {%s}: invalid value %q: %s", e.Name, e.Value, e.Err)
}

// Unwrap allows [errors.Is] and [errors.As] to traverse the underlying error.
func (e FilePathParamError) Unwrap() error { return e.Err }

// LogValue implements [slog.LogValuer] for structured logging.
func (e FilePathParamError) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("param", e.Name),
		slog.String("value", e.Value),
		slog.Any("cause", e.Err),
	)
}

// FilePathMismatchError is returned when a concrete path does not match
// a Resource's declared template's structure (wrong number of segments,
// or literal text does not match).
type FilePathMismatchError struct {
	Template string
	Path     string
}

func (e FilePathMismatchError) Error() string {
	return fmt.Sprintf("file path %q does not match template %q", e.Path, e.Template)
}

// LogValue implements [slog.LogValuer] for structured logging.
func (e FilePathMismatchError) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("template", e.Template),
		slog.String("path", e.Path),
	)
}

// MissingFilePathVarError is returned when a {varName} placeholder in a
// declared path template has no corresponding entry in the supplied vars.
type MissingFilePathVarError struct {
	Name string // placeholder name (without braces)
}

func (e MissingFilePathVarError) Error() string {
	return fmt.Sprintf("missing value for file path variable {%s}", e.Name)
}

// LogValue implements [slog.LogValuer] for structured logging.
func (e MissingFilePathVarError) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("param", e.Name),
	)
}

// FileWildcardBuildError is returned when a declared path template
// contains a glob segment ("**", or a segment with '*'/'?'/'[') — building
// a SINGLE concrete path from a template that can match multiple paths is
// undefined.
type FileWildcardBuildError struct {
	Template string
}

func (e FileWildcardBuildError) Error() string {
	return fmt.Sprintf("file path template %q is glob-enabled: a single concrete path has no meaning for a template that can match multiple paths", e.Template)
}

// LogValue implements [slog.LogValuer] for structured logging.
func (e FileWildcardBuildError) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("template", e.Template),
	)
}

// FilePatchNotSupportedError is returned by [Patcher]'s dispatch when the
// resource's format does not use a map[string]any intermediate. Only
// JSON, YAML, TOML, and formats created with [format.New] support
// patching.
type FilePatchNotSupportedError struct {
	// Path is the concrete file path after template substitution.
	Path string
}

func (e FilePatchNotSupportedError) Error() string {
	return fmt.Sprintf("patch file %q: format does not support partial patch (use JSON, YAML, or TOML)", e.Path)
}

// LogValue implements [slog.LogValuer] for structured logging.
func (e FilePatchNotSupportedError) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("path", e.Path),
	)
}

// BackendUnsupportedOptionError is returned when a [WriteOptions]/
// [DeleteOptions]/[MkdirOptions] field a [Backend] genuinely cannot
// honor is set to a non-default value AND the backend author chose to
// fail loudly rather than silently no-op.
type BackendUnsupportedOptionError struct {
	Backend string
	Option  string
}

func (e BackendUnsupportedOptionError) Error() string {
	return fmt.Sprintf("backend %q does not support option %q", e.Backend, e.Option)
}

// LogValue implements [slog.LogValuer] for structured logging.
func (e BackendUnsupportedOptionError) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("backend", e.Backend),
		slog.String("option", e.Option),
	)
}

// BackendAlreadyAttachedError is returned by a 2nd [Client.Attach] call —
// mirrors [github.com/DaniDeer/go-codex/api/events.TransportAlreadyAttachedError]'s
// identical "one Backend per Client" rule.
type BackendAlreadyAttachedError struct{}

func (e BackendAlreadyAttachedError) Error() string {
	return "api/file: Client already has a Backend attached (Attach is exclusive; build a fresh Client for a different backend)"
}

// LogValue implements [slog.LogValuer] for structured logging.
func (e BackendAlreadyAttachedError) LogValue() slog.Value {
	return slog.GroupValue()
}

// NoBackendAttachedError is returned by [Client.Read]/[Client.Create]/
// [Client.Patch]/[Client.Delete]/[Client.List]/[Client.Exists]/
// [Client.Copy]/[Client.Move]/[Client.Mkdir]/[Client.BatchDelete] when
// [Client.Attach] was never called.
type NoBackendAttachedError struct{}

func (e NoBackendAttachedError) Error() string {
	return "api/file: Client has no Backend attached (build a backend and call Attach yourself, e.g. client.Attach(adapterfile.NewBackend(...)))"
}

// LogValue implements [slog.LogValuer] for structured logging.
func (e NoBackendAttachedError) LogValue() slog.Value {
	return slog.GroupValue()
}

// BackendTypeMismatchError is returned when a call-time `any`-typed
// argument's dynamic type doesn't match the role's declared T/P —
// mirrors [github.com/DaniDeer/go-codex/api/events.TransportTypeMismatchError]'s
// identical rationale.
type BackendTypeMismatchError struct {
	Expected string
	Got      string
}

func (e BackendTypeMismatchError) Error() string {
	return fmt.Sprintf("api/file: value type mismatch: expected %s, got %s", e.Expected, e.Got)
}

// LogValue implements [slog.LogValuer] for structured logging.
func (e BackendTypeMismatchError) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("expected", e.Expected),
		slog.String("got", e.Got),
	)
}

// BoundMiddlewareReqMismatchError is returned (via roleCore.buildErr,
// surfaced at .Handle() time) when a value passed to a role's own
// `XxxBoundMW(bm any)` method isn't a Bound<Role>Middleware matching that
// role's own T type parameter — mirrors
// [github.com/DaniDeer/go-codex/api/events.BoundMiddlewareReqMismatchError]'s
// identical rationale and recorded-not-panicked timing.
type BoundMiddlewareReqMismatchError struct {
	Path string
	Got  any
	Name string
}

func (e BoundMiddlewareReqMismatchError) Error() string {
	if e.Name != "" {
		return fmt.Sprintf("api/file: path %q: middleware %q: not attachable via XxxBoundMW here — either its T type parameter doesn't match this role's own, or it isn't a Bound<Role>Middleware value at all (got %T)", e.Path, e.Name, e.Got)
	}
	return fmt.Sprintf("api/file: path %q: XxxBoundMW requires a Bound<Role>Middleware value matching this role's own T (got %T)", e.Path, e.Got)
}

// LogValue implements [slog.LogValuer] for structured logging.
func (e BoundMiddlewareReqMismatchError) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("path", e.Path),
		slog.String("got", fmt.Sprintf("%T", e.Got)),
		slog.String("name", e.Name),
	)
}

// SecurityRejectedError is returned when a role's declared Security
// requirements are not satisfied by its attached BoundMiddleware's
// collected GrantedScopes — mirrors
// [github.com/DaniDeer/go-codex/api/events.SecurityError]'s identical
// rationale (wraps [internal/middleware.CheckScopes]'s own error).
type SecurityRejectedError struct {
	Path string
	Err  error
}

func (e SecurityRejectedError) Error() string {
	return fmt.Sprintf("api/file: path %q: security requirements not satisfied: %s", e.Path, e.Err)
}

func (e SecurityRejectedError) Unwrap() error { return e.Err }

// LogValue implements [slog.LogValuer] for structured logging.
func (e SecurityRejectedError) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("path", e.Path),
		slog.Any("err", e.Err),
	)
}

// ConditionalWriteMismatchError is returned by [ConditionalWriteBackend
// .WriteBytesIfMatch] (surfaced via [createCore]/[patchCore]'s
// [WriteOptions.IfMatch] check) when the supplied IfMatch value does
// not equal path's current ETag — no write was performed. Also returned
// when path does not exist at all (there is nothing to match, so any
// non-empty IfMatch necessarily "doesn't match" — mirrors S3's own real
// If-Match behavior against a nonexistent object).
type ConditionalWriteMismatchError struct {
	Path     string
	Expected string
	Actual   string
}

func (e ConditionalWriteMismatchError) Error() string {
	return fmt.Sprintf("api/file: path %q: If-Match mismatch: expected ETag %q, got %q", e.Path, e.Expected, e.Actual)
}

// LogValue implements [slog.LogValuer] for structured logging.
func (e ConditionalWriteMismatchError) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("path", e.Path),
		slog.String("expected", e.Expected),
		slog.String("actual", e.Actual),
	)
}

// ConditionalWriteUnsupportedError is returned when [WriteOptions.IfMatch]
// is set but the attached Backend does NOT implement
// [ConditionalWriteBackend] — raised BEFORE any write is attempted
// (dispatch fails loud and fast rather than emulating a non-atomic
// check with a real race window).
type ConditionalWriteUnsupportedError struct {
	Path    string
	Backend string
}

func (e ConditionalWriteUnsupportedError) Error() string {
	return fmt.Sprintf("api/file: path %q: conditional write (IfMatch) is not supported by backend %q", e.Path, e.Backend)
}

// LogValue implements [slog.LogValuer] for structured logging.
func (e ConditionalWriteUnsupportedError) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("path", e.Path),
		slog.String("backend", e.Backend),
	)
}

// ConditionalWriteLockTimeoutError is returned by a lock-file-based
// [ConditionalWriteBackend] implementation (e.g. adapters/file.Backend,
// ports's private osBackend) when its bounded acquire-retry loop could
// not obtain path's lock file within its bound (either the caller's own
// ctx deadline, or the implementation's internal default ceiling,
// whichever is reached first) — see docs/roadmap/file-api.md's
// "Conditional writes" section, Fork 3, for the full retry/timeout
// design.
type ConditionalWriteLockTimeoutError struct {
	Path string
	Err  error
}

func (e ConditionalWriteLockTimeoutError) Error() string {
	return fmt.Sprintf("api/file: path %q: timed out acquiring conditional-write lock: %s", e.Path, e.Err)
}

func (e ConditionalWriteLockTimeoutError) Unwrap() error { return e.Err }

// LogValue implements [slog.LogValuer] for structured logging.
func (e ConditionalWriteLockTimeoutError) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("path", e.Path),
		slog.Any("err", e.Err),
	)
}
