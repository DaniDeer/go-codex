package versioning

import v "github.com/DaniDeer/go-codex/validate"

// ── Classification-only helpers ─────────────────────────────────────────────
//
// IsSemVer/IsSemVerLike are for callers who just want a yes/no answer
// without constructing a full [Version] via [Parse] — e.g. filtering a
// list before deciding whether to bother parsing it at all.

// IsSemVer reports whether s satisfies strict semver (validate.SemVer).
func IsSemVer(s string) bool { return v.SemVer.Check(s) }

// IsSemVerLike reports whether s satisfies the semver-like shape
// (validate.SemVerLike). NOTE this does NOT mean "semver-like and not
// strict semver": validate.SemVerLike's grammar overlaps validate.SemVer's
// for the common case (see validate.SemVerLike's own doc comment), so a
// strict-semver string like "1.2.3" also satisfies IsSemVerLike. Check
// IsSemVer first if you need mutually-exclusive classification — the
// same order [VersionCodec] itself uses internally.
func IsSemVerLike(s string) bool { return v.SemVerLike.Check(s) }
