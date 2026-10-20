package file

// ListResult is one discovered entry's outcome from a [Client.List]
// call — mirrors [BatchDeleteResult]'s own "one requested item, one
// independent per-item result" idiom, applied here to List's
// DISCOVERED items instead of a caller-SUPPLIED path list.
//
// Client.List's boxed return value is []ListResult[T] (never a plain
// []T) — see the "Phase 1 amendment" section of
// docs/design/d-0011-file-api-backend-transparency.md for the full rationale. A per-entry failure
// (decode error, merge-field mismatch, metadata-read failure, or a
// declared BoundListMiddleware's Security rejection) is captured in
// THAT entry's own Err, never aborting the whole call — only a
// walk-level failure (the parent directory itself can't be
// read/walked at all) fails the call via List's own plain error
// return, with a nil []ListResult[T].
type ListResult[T any] struct {
	// Value is the decoded entry — the zero value when Err is set.
	Value T
	// Err is nil on success. When set, Value is the zero value for T.
	Err error
}
