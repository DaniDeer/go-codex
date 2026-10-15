package file

import (
	"fmt"
	"path"
)

// typeName returns a human-readable %T-derived name for v — used by
// BackendTypeMismatchError's Got field.
func typeName(v any) string {
	return fmt.Sprintf("%T", v)
}

// typeNameOf returns a human-readable name for T's zero value — used by
// BackendTypeMismatchError's Expected field.
func typeNameOf[T any]() string {
	var zero T
	return fmt.Sprintf("%T", zero)
}

// joinPath joins a parent directory path and a child entry name —
// mirrors path.Join's forward-slash convention (api/file paths are
// backend-addressed strings, not necessarily OS filepaths).
func joinPath(parent, name string) string {
	return path.Join(parent, name)
}
