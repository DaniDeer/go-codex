package file

import (
	"fmt"
	"path"

	"github.com/DaniDeer/go-codex/internal/templatematch"
)

// typeName returns a human-readable %T-derived name for v — used by
// BackendTypeMismatchError's Got field.
func typeName(v any) string {
	return fmt.Sprintf("%T", v)
}

// joinPath joins a parent directory path and a child entry name —
// mirrors path.Join's forward-slash convention (api/file paths are
// backend-addressed strings, not necessarily OS filepaths).
func joinPath(parent, name string) string {
	return path.Join(parent, name)
}

// matchFileTemplate is the inverse of codex.BuildFromParams — extracts
// "{varName}" placeholder values from concrete (a discovered entry name)
// against template (the resource's own, non-glob path template — api/file
// has no glob support, unlike ports.Dir), mirroring
// ports.File.MatchPath/ports.Dir.MatchPath's identical engine
// (internal/templatematch.MatchNonWildcard). Used by List to recover
// per-entry path vars for MergedPathParam's merge-field decode — see
// listCore's own doc comment in dispatch.go.
func matchFileTemplate(template, concrete string) (map[string]string, error) {
	return templatematch.MatchNonWildcard(template, concrete, func(template, concrete string) error {
		return FilePathMismatchError{Template: template, Path: concrete}
	})
}
