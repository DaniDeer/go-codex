package file

import (
	"fmt"
	"log/slog"
)

// DirPathParamError is returned by [Dir.BuildPath]/[Dir.MatchPath] when
// a {varName} value fails its [PathParam.Codec] validation. Mirrors
// [FilePathParamError] exactly.
type DirPathParamError struct {
	Name  string
	Value string
	Err   error
}

func (e DirPathParamError) Error() string {
	return fmt.Sprintf("dir path variable {%s}: invalid value %q: %s", e.Name, e.Value, e.Err)
}

// Unwrap allows [errors.Is]/[errors.As] to traverse the underlying error.
func (e DirPathParamError) Unwrap() error { return e.Err }

// LogValue implements [slog.LogValuer].
func (e DirPathParamError) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("param", e.Name),
		slog.String("value", e.Value),
		slog.Any("cause", e.Err),
	)
}

// MissingDirPathVarError is returned by [Dir.BuildPath] when a {varName}
// placeholder has no corresponding entry in vars. Mirrors
// [MissingFilePathVarError] exactly.
type MissingDirPathVarError struct {
	Name string
}

func (e MissingDirPathVarError) Error() string {
	return fmt.Sprintf("missing value for dir path variable {%s}", e.Name)
}

// LogValue implements [slog.LogValuer].
func (e MissingDirPathVarError) LogValue() slog.Value {
	return slog.GroupValue(slog.String("param", e.Name))
}

// DirPathMismatchError is returned by [Dir.MatchPath] when a concrete
// directory path does not match the template's structure. Mirrors
// [FilePathMismatchError] exactly.
type DirPathMismatchError struct {
	Template string
	Path     string
}

func (e DirPathMismatchError) Error() string {
	return fmt.Sprintf("dir path %q does not match template %q", e.Path, e.Template)
}

// LogValue implements [slog.LogValuer].
func (e DirPathMismatchError) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("template", e.Template),
		slog.String("path", e.Path),
	)
}

// DirReadError is returned by [Client.ListDir] on a Backend-level
// ListDir/WalkDir/Mkdir/Stat failure. Mirrors [FileReadError].
type DirReadError struct {
	Path string
	Err  error
}

func (e DirReadError) Error() string { return fmt.Sprintf("read dir %q: %s", e.Path, e.Err) }

// Unwrap allows [errors.Is]/[errors.As] to traverse the underlying error.
func (e DirReadError) Unwrap() error { return e.Err }

// LogValue implements [slog.LogValuer].
func (e DirReadError) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("path", e.Path),
		slog.Any("cause", e.Err),
	)
}

// DirEntryParamError is returned by [Client.ListDir] when a discovered
// entry's RelPath matched [EntryPattern]'s template shape but failed a
// param's codec. Mirrors [FilePathParamError]'s shape, scoped to one
// entry.
type DirEntryParamError struct {
	Entry string
	Name  string
	Value string
	Err   error
}

func (e DirEntryParamError) Error() string {
	return fmt.Sprintf("dir entry %q variable {%s}: invalid value %q: %s", e.Entry, e.Name, e.Value, e.Err)
}

// Unwrap allows [errors.Is]/[errors.As] to traverse the underlying error.
func (e DirEntryParamError) Unwrap() error { return e.Err }

// LogValue implements [slog.LogValuer].
func (e DirEntryParamError) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("entry", e.Entry),
		slog.String("param", e.Name),
		slog.String("value", e.Value),
		slog.Any("cause", e.Err),
	)
}

// DirAlreadyExistsError is returned by [Client.ListDir] when
// [DirOptions.CreateIfMissing] and [DirOptions.Strict] are both set and
// the directory already exists. Mirrors [FileAlreadyExistsError].
type DirAlreadyExistsError struct {
	Path string
}

func (e DirAlreadyExistsError) Error() string {
	return fmt.Sprintf("dir %q already exists", e.Path)
}

// LogValue implements [slog.LogValuer].
func (e DirAlreadyExistsError) LogValue() slog.Value {
	return slog.GroupValue(slog.String("path", e.Path))
}

// DirNotFoundError is returned by [Client.DeleteDir] when
// [DirOptions.Strict] is set and the directory did not exist. Mirrors
// [FileNotFoundError].
type DirNotFoundError struct {
	Path string
}

func (e DirNotFoundError) Error() string { return fmt.Sprintf("dir %q not found", e.Path) }

// LogValue implements [slog.LogValuer].
func (e DirNotFoundError) LogValue() slog.Value {
	return slog.GroupValue(slog.String("path", e.Path))
}

// DirWildcardBuildError is returned by [Dir.BuildPath]/[Dir.MatchPath]/
// [Client.DeleteDir] when the Dir's template is glob-enabled — a
// glob-enabled template describes MULTIPLE possible directories, so
// "build/match/delete the (one) directory" has no meaning (use
// [Client.ListDir]'s glob-discovery mode instead, then act per
// discovered path). Mirrors [FileWildcardBuildError].
type DirWildcardBuildError struct {
	Template string
}

func (e DirWildcardBuildError) Error() string {
	return fmt.Sprintf("dir template %q is glob-enabled — build/match/delete a single path is not supported", e.Template)
}

// LogValue implements [slog.LogValuer].
func (e DirWildcardBuildError) LogValue() slog.Value {
	return slog.GroupValue(slog.String("template", e.Template))
}

// DirDeleteError is returned by [Client.DeleteDir] on a genuine
// Backend-level deletion failure (not covered by [DirNotFoundError]/
// [DirNotEmptyError]). Mirrors [FileDeleteError].
type DirDeleteError struct {
	Path string
	Err  error
}

func (e DirDeleteError) Error() string { return fmt.Sprintf("delete dir %q: %s", e.Path, e.Err) }

// Unwrap allows [errors.Is]/[errors.As] to traverse the underlying error.
func (e DirDeleteError) Unwrap() error { return e.Err }

// LogValue implements [slog.LogValuer].
func (e DirDeleteError) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("path", e.Path),
		slog.Any("cause", e.Err),
	)
}
