package versioning

import (
	"strconv"
	"strings"

	c "github.com/DaniDeer/go-codex/codex"
	v "github.com/DaniDeer/go-codex/validate"
)

// ── Version ───────────────────────────────────────────────────────────────────

// Version classifies a bare version-like string into exactly one of three
// buckets, in order of "how much version information do we actually
// have": SemVer (fully structured, comparable numerically), SemVerLike (a
// looser, common convention — leading dotted integers plus an optional
// suffix, e.g. "3.1-debian"/"18.04"), or Other (fully opaque, e.g.
// "latest"/"stable"/a random hash — no version signal at all). Exactly
// one field is non-nil; see [VersionCodec]'s doc comment for how a value
// is classified, and [Compare] for how two Version values are ordered.
//
// Version is domain-agnostic on purpose — it works on any version-like
// string, not just Docker image tags (see [Parse]'s `~string` type
// parameter). See this package's README.md for the design rationale and
// usage examples.
type Version struct {
	SemVer     *SemVerRank
	SemVerLike *SemVerLikeRank
	Other      *string
}

// SemVerRank is a fully parsed strict-semver value (validated by
// validate.SemVer before this is ever constructed) — Major/Minor/Patch
// are always present; Prerelease is "" for a plain release.
type SemVerRank struct {
	Major, Minor, Patch int
	Prerelease          string
	// Raw is the original text, kept for display/round-trip — Encode
	// reconstructs a wire string by formatting Major.Minor.Patch[-Prerelease]
	// as a NORMALIZED form (any "v" prefix or build-metadata suffix the
	// original text had is deliberately dropped in the reconstruction;
	// Raw is the only place the exact original text survives).
	Raw string
}

// SemVerLikeRank is a "close enough to a version" value that does not
// satisfy strict semver's exactly-three-numeric-parts shape — e.g.
// "3.1-debian" (2 parts + suffix) or "18.04" (2 parts, no suffix).
type SemVerLikeRank struct {
	// Parts holds the leading dot-separated integers, in order (e.g.
	// []int{3, 1} for "3.1-debian").
	Parts []int
	// Suffix is everything after the numeric parts (e.g. "debian" for
	// "3.1-debian", "" for "18.04") — compared alphabetically only as a
	// deterministic tiebreaker (see Compare; there is no semantic
	// ordering between, say, "-alpine" and "-debian").
	Suffix string
	// Raw is the original text (see SemVerRank.Raw's doc comment for the
	// same round-trip caveat).
	Raw string
}

// parseSemVerRank parses a string already validated by validate.SemVer
// into a SemVerRank. validate.SemVer's own regex guarantees the
// "MAJOR.MINOR.PATCH[-pre][+build]" shape (with an optional leading "v"),
// so strconv.Atoi on the numeric segments cannot fail here.
func parseSemVerRank(s string) SemVerRank {
	raw := s
	core := strings.TrimPrefix(s, "v")
	// Build metadata (after "+") plays no role in ordering — drop it before
	// splitting out the prerelease.
	if i := strings.IndexByte(core, '+'); i >= 0 {
		core = core[:i]
	}
	prerelease := ""
	if i := strings.IndexByte(core, '-'); i >= 0 {
		prerelease = core[i+1:]
		core = core[:i]
	}
	parts := strings.SplitN(core, ".", 3)
	major, _ := strconv.Atoi(parts[0])
	minor, _ := strconv.Atoi(parts[1])
	patch, _ := strconv.Atoi(parts[2])
	return SemVerRank{Major: major, Minor: minor, Patch: patch, Prerelease: prerelease, Raw: raw}
}

// formatSemVerRank reconstructs a normalized "major.minor.patch[-prerelease]"
// string from a SemVerRank — used only by VersionCodec.Encode (Raw is not
// consulted; see SemVerRank.Raw's own doc comment).
func formatSemVerRank(r SemVerRank) (string, error) {
	s := strconv.Itoa(r.Major) + "." + strconv.Itoa(r.Minor) + "." + strconv.Itoa(r.Patch)
	if r.Prerelease != "" {
		s += "-" + r.Prerelease
	}
	return s, nil
}

// parseSemVerLikeRank parses a string already validated by
// validate.SemVerLike into a SemVerLikeRank.
func parseSemVerLikeRank(s string) SemVerLikeRank {
	raw := s
	core := strings.TrimPrefix(s, "v")
	suffix := ""
	if i := strings.IndexByte(core, '-'); i >= 0 {
		suffix = core[i+1:]
		core = core[:i]
	}
	segs := strings.Split(core, ".")
	parts := make([]int, len(segs))
	for i, seg := range segs {
		n, _ := strconv.Atoi(seg)
		parts[i] = n
	}
	return SemVerLikeRank{Parts: parts, Suffix: suffix, Raw: raw}
}

// formatSemVerLikeRank reconstructs a "parts[-suffix]" string from a
// SemVerLikeRank (see SemVerRank.Raw's doc comment for the same
// normalized-not-Raw caveat).
func formatSemVerLikeRank(r SemVerLikeRank) (string, error) {
	segs := make([]string, len(r.Parts))
	for i, p := range r.Parts {
		segs[i] = strconv.Itoa(p)
	}
	s := strings.Join(segs, ".")
	if r.Suffix != "" {
		s += "-" + r.Suffix
	}
	return s, nil
}

// semverVariant / semverLikeVariant / otherVariant are VersionCodec's
// three UntaggedUnion branches, tried in this exact order — order is
// significant: a value like "3.1.15-18" satisfies BOTH validate.SemVer
// (3 numeric parts + numeric prerelease "18") and validate.SemVerLike (it
// would also match as parts=[3,1,15], suffix="18"), so it must be
// claimed by the FIRST matching branch (semverVariant) or it would be
// misclassified as SemVerLike — see validate.SemVerLike's own doc
// comment for this exact ordering requirement. otherVariant is a total
// catch-all (never fails), so it MUST be last.
var semverVariant = c.MapCodecSafe(
	c.String().Refine(v.SemVer),
	func(s string) Version { r := parseSemVerRank(s); return Version{SemVer: &r} },
	func(t Version) (string, error) {
		if t.SemVer == nil {
			return "", InvalidVersionError{Version: t}
		}
		return formatSemVerRank(*t.SemVer)
	},
)

var semVerLikeVariant = c.MapCodecSafe(
	c.String().Refine(v.SemVerLike),
	func(s string) Version { r := parseSemVerLikeRank(s); return Version{SemVerLike: &r} },
	func(t Version) (string, error) {
		if t.SemVerLike == nil {
			return "", InvalidVersionError{Version: t}
		}
		return formatSemVerLikeRank(*t.SemVerLike)
	},
)

var otherVariant = c.MapCodecSafe(
	c.String(),
	func(s string) Version { return Version{Other: &s} },
	func(t Version) (string, error) {
		if t.Other == nil {
			return "", InvalidVersionError{Version: t}
		}
		return *t.Other, nil
	},
)

// VersionCodec classifies a bare version-like string into a [Version] by
// trying, in order: strict semver (validate.SemVer), semver-like
// (validate.SemVerLike), then an unconditional opaque-string fallback —
// so Decode never actually fails for this codec (the last branch always
// succeeds). Encode dispatches on whichever field of Version is non-nil;
// if none are set (e.g. a zero-value Version{}), Encode returns a typed
// error rather than dereferencing a nil pointer.
//
// [Parse] is the recommended — and, for the common case, the ONLY
// realistic — way to construct a Version: it never fails, since Other is
// a total catch-all. Version implements [codex.HasCodec][Version] (see
// [Version.Codec]) purely as a DEFENSIVE mechanism: its three fields are
// exported, so nothing stops a caller from hand-building an invalid
// value (e.g. the zero value, or two branches set at once); the generic
// codex.Validate/New/EncodeSelf/DecodeAs helpers give such a caller a way
// to check a hand-built Version before using it. There is deliberately
// NO hand-written NewVersion(...) constructor — Version's three fields
// are mutually-exclusive union branches, not orthogonal fields, so a
// positional constructor doesn't fit; use [Parse] instead.
var VersionCodec = c.UntaggedUnion[Version](
	func(t Version) int {
		switch {
		case t.SemVer != nil:
			return 0
		case t.SemVerLike != nil:
			return 1
		default:
			return 2
		}
	},
	c.UntaggedVariant[Version]{Name: "semver", Codec: semverVariant},
	c.UntaggedVariant[Version]{Name: "semver-like", Codec: semVerLikeVariant},
	c.UntaggedVariant[Version]{Name: "other", Codec: otherVariant},
)

// Codec implements [codex.HasCodec][Version], returning [VersionCodec] —
// see VersionCodec's own doc comment for why this exists (defensive
// validation of a hand-built Version, not a smart constructor — [Parse]
// remains the recommended way to construct one).
func (Version) Codec() c.Codec[Version] { return VersionCodec }

// Parse classifies s via VersionCodec — never fails (see VersionCodec's
// own doc comment), so this wrapper drops the impossible error return
// for ergonomics at call sites that only ever sort/compare. Generic over
// any named string type (`~string`) — e.g. a caller can pass a
// docker.Tag directly, with no manual string(...) conversion.
func Parse[T ~string](s T) Version {
	rank, _ := VersionCodec.Decode(string(s))
	return rank
}

// Compare orders two Version values for "most version-recent first"
// (descending) sorting — see [SortByVersionDesc]'s own doc comment for
// the important caveat that this is version-order, NOT chronological
// order. Returns a negative number if a should sort before b (i.e. a is
// "more recent"), positive if b should sort before a, and 0 if they
// compare equal.
//
// Ordering rules:
//  1. Bucket precedence: any SemVer value ranks before any SemVerLike
//     value, which ranks before any Other value — a value with more
//     structure is always considered "more recent" than one with less,
//     regardless of the numbers/text involved.
//  2. Within SemVer: Major, then Minor, then Patch, compared
//     numerically descending; then Prerelease — a release (empty
//     Prerelease) ranks before any prerelease (matches semver's own
//     precedence rule); two non-empty Prerelease values compare as
//     plain strings (a documented simplification, not full semver
//     dot-identifier precedence).
//  3. Within SemVerLike: Parts compared element-wise numerically
//     descending (a missing trailing part is treated as 0); then
//     Suffix alphabetically ascending — purely a deterministic
//     tiebreaker, NOT a recency signal (there is no ordering
//     relationship between e.g. "-alpine" and "-debian").
//  4. Within Other: alphabetically ascending — opaque values carry no
//     recency signal at all, so this is deterministic ordering only.
func Compare(a, b Version) int {
	aBucket, bBucket := bucketOf(a), bucketOf(b)
	if aBucket != bBucket {
		return aBucket - bBucket
	}
	switch aBucket {
	case 0:
		return compareSemVerRank(*a.SemVer, *b.SemVer)
	case 1:
		return compareSemVerLikeRank(*a.SemVerLike, *b.SemVerLike)
	default:
		return strings.Compare(*a.Other, *b.Other)
	}
}

// bucketOf returns Version's bucket index (0=SemVer, 1=SemVerLike,
// 2=Other) — lower is "more recent" per Compare's rule 1.
func bucketOf(t Version) int {
	switch {
	case t.SemVer != nil:
		return 0
	case t.SemVerLike != nil:
		return 1
	default:
		return 2
	}
}

func compareSemVerRank(a, b SemVerRank) int {
	if a.Major != b.Major {
		return b.Major - a.Major
	}
	if a.Minor != b.Minor {
		return b.Minor - a.Minor
	}
	if a.Patch != b.Patch {
		return b.Patch - a.Patch
	}
	// A release (empty Prerelease) ranks before any prerelease.
	switch {
	case a.Prerelease == "" && b.Prerelease == "":
		return 0
	case a.Prerelease == "":
		return -1
	case b.Prerelease == "":
		return 1
	default:
		return strings.Compare(a.Prerelease, b.Prerelease)
	}
}

func compareSemVerLikeRank(a, b SemVerLikeRank) int {
	n := len(a.Parts)
	if len(b.Parts) > n {
		n = len(b.Parts)
	}
	for i := 0; i < n; i++ {
		var ap, bp int
		if i < len(a.Parts) {
			ap = a.Parts[i]
		}
		if i < len(b.Parts) {
			bp = b.Parts[i]
		}
		if ap != bp {
			return bp - ap
		}
	}
	return strings.Compare(a.Suffix, b.Suffix)
}
