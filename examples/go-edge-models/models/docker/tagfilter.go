package docker

import (
	"github.com/DaniDeer/go-codex/examples/go-edge-models/models/versioning"
)

// ── FilterTags ────────────────────────────────────────────────────────────────
//
// This file is a thin, docker.Tag-typed re-export of the generalized
// models/versioning package — the actual classification/comparison/sort
// logic lives there (it is domain-agnostic; see its own README.md for the
// full design rationale). Kept here under docker's own vocabulary since
// "sorting/filtering Docker tags" is exactly where a caller of THIS
// package would naturally look for it.

// TagRank is versioning.Version under docker's own name — classifies a
// bare [Tag] into strict-semver/semver-like/opaque buckets. See
// versioning.Version's doc comment for the full design.
type TagRank = versioning.Version

// SortMode selects how [FilterTags] orders its result. See
// versioning.SortMode's doc comment for the full design.
type SortMode = versioning.SortMode

const (
	// SortByVersionDesc orders tags by highest-version-first. See
	// versioning.SortByVersionDesc's doc comment for the important
	// "version-order, not chronological order" caveat.
	SortByVersionDesc = versioning.SortByVersionDesc
	// SortAlphabetical sorts every tag as a plain string, ascending.
	SortAlphabetical = versioning.SortAlphabetical
	// SortNone passes tags through in the registry's own order.
	SortNone = versioning.SortNone
)

// FilterTagsOpt configures [FilterTags]. See versioning.FilterOpt's doc
// comment for the full design.
type FilterTagsOpt = versioning.FilterOpt

// WithSort selects the sort mode (default [SortByVersionDesc]).
func WithSort(mode SortMode) FilterTagsOpt { return versioning.WithSort(mode) }

// WithLimit caps the number of tags FilterTags returns to n. n <= 0 means
// "no limit" (the default — every tag returned).
func WithLimit(n int) FilterTagsOpt { return versioning.WithLimit(n) }

// FilterTags sorts and optionally limits tags — pure, no I/O, does not
// mutate the input slice. Defaults: [SortByVersionDesc], no limit. See
// versioning.Filter's doc comment for the full design.
func FilterTags(tags []Tag, opts ...FilterTagsOpt) []Tag {
	return versioning.Filter(tags, opts...)
}

// ParseTagRank classifies t into a [TagRank]. See versioning.Parse's doc
// comment — never fails (Other is the catch-all).
func ParseTagRank(t Tag) TagRank { return versioning.Parse(t) }

// CompareTagRank orders two TagRank values for "most version-recent
// first" sorting. See versioning.Compare's doc comment for the full
// ordering rules.
func CompareTagRank(a, b TagRank) int { return versioning.Compare(a, b) }
