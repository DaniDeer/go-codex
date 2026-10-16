package file

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"time"
)

// StrongETag returns a STRONG (content-hash-based) ETag for data — a
// full SHA-256 hex digest. Exported so Backend implementations (e.g.
// adapters/file.Backend, ports's private osBackend) can compute it
// directly for their own Stat/ListDir/WriteBytesIfMatch use — used
// whenever content is already in hand: inside WriteBytesIfMatch's own
// current-content comparison, and anywhere a Backend reads a file's
// full bytes anyway. Not cryptographically significant here (ETags are
// never compared ACROSS backends/calls for security purposes) —
// SHA-256 is simply a convenient, collision-resistant-enough content
// identifier, same spirit as HTTP's own strong validators.
func StrongETag(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// WeakETag returns a WEAK (approximate, NOT a content hash) ETag,
// computed from modTime+size alone — used when only metadata is
// available (Backend.Stat/Backend.ListDir, which deliberately never
// read the body). Mirrors HTTP's own weak-validator syntax (RFC 7232
// §2.3's `W/"..."` prefix) literally — reused, not reinvented. Exported
// for the same reason as [StrongETag].
//
// Being WEAK means it is an APPROXIMATION, not an exact content
// identity: two different writes within the same ModTime resolution
// window (e.g. a filesystem with 1-second mtime granularity) that
// happen to produce the same Size would collide on an identical weak
// ETag despite genuinely different content — an accepted, inherent
// imprecision of "weak" validators, not a bug.
func WeakETag(modTime time.Time, size int64) string {
	return fmt.Sprintf(`W/"%x-%x"`, modTime.UnixNano(), size)
}
