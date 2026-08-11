package versioning

import "sort"

// ── Filter ────────────────────────────────────────────────────────────────────

// SortMode selects how [Filter] orders its result.
type SortMode int

const (
	// SortByVersionDesc orders values by highest-version-first, using
	// [Compare] (strict semver, then semver-like, then opaque values
	// last, each compared numerically/alphabetically within its own
	// bucket). This is [Filter]'s default.
	//
	// IMPORTANT: this package has no access to any timestamp — the
	// caller supplies plain strings, and this is "highest version first",
	// inferred purely from each value's own text, NOT actual
	// chronological recency. Two values compared this way may not
	// reflect which one was actually published/built more recently (e.g.
	// a registry may re-tag an older build under a newer-looking tag).
	SortByVersionDesc SortMode = iota
	// SortAlphabetical ignores version structure entirely and sorts every
	// value as a plain string, ascending — useful when you want a
	// deterministic order without any version semantics (e.g. displaying
	// every value in a picker).
	SortAlphabetical
	// SortNone passes values through in whatever order the caller
	// supplied them — no sorting at all.
	SortNone
)

// FilterOpt configures [Filter] — the functional-option pattern used
// throughout go-edge-models (mirrors rest.RouteOpt/events.ChannelOpt).
type FilterOpt interface{ applyFilter(*filterConfig) }

type filterConfig struct {
	sort  SortMode
	limit int
}

type filterOptFunc func(*filterConfig)

func (f filterOptFunc) applyFilter(cfg *filterConfig) { f(cfg) }

// WithSort selects the sort mode (default [SortByVersionDesc]).
func WithSort(mode SortMode) FilterOpt {
	return filterOptFunc(func(cfg *filterConfig) { cfg.sort = mode })
}

// WithLimit caps the number of values Filter returns to n, keeping the
// first n after sorting. n <= 0 means "no limit" (the default — every
// value is returned).
func WithLimit(n int) FilterOpt {
	return filterOptFunc(func(cfg *filterConfig) { cfg.limit = n })
}

// Filter sorts and optionally limits values — pure, no I/O, does not
// mutate the input slice. Defaults: [SortByVersionDesc], no limit (every
// value returned). See [SortByVersionDesc]'s doc comment for the
// important "version-order, not chronological order" caveat that applies
// to the default mode. Generic over any named string type (`~string`) —
// e.g. []docker.Tag works directly, with no manual conversion.
func Filter[T ~string](values []T, opts ...FilterOpt) []T {
	cfg := filterConfig{sort: SortByVersionDesc, limit: 0}
	for _, opt := range opts {
		opt.applyFilter(&cfg)
	}

	out := make([]T, len(values))
	copy(out, values)

	switch cfg.sort {
	case SortByVersionDesc:
		// Pair each value with its parsed rank so the two stay in sync
		// through sorting (sort.SliceStable only permutes ONE slice by
		// index — computing ranks up front and sorting `out` alone would
		// desync them the moment any swap happened).
		type ranked struct {
			value T
			rank  Version
		}
		rs := make([]ranked, len(out))
		for i, val := range out {
			rs[i] = ranked{value: val, rank: Parse(val)}
		}
		sort.SliceStable(rs, func(i, j int) bool {
			return Compare(rs[i].rank, rs[j].rank) < 0
		})
		for i, r := range rs {
			out[i] = r.value
		}
	case SortAlphabetical:
		sort.SliceStable(out, func(i, j int) bool { return out[i] < out[j] })
	case SortNone:
		// no-op — passthrough order.
	}

	if cfg.limit > 0 && cfg.limit < len(out) {
		out = out[:cfg.limit]
	}
	return out
}

// MostRecent returns the single most version-recent value from values
// (per [SortByVersionDesc]'s ordering — see its own doc comment for the
// important "version-order, not chronological order" caveat), and true.
// Returns the zero value and false if values is empty. A one-shot
// convenience over `Filter(values, WithLimit(1))[0]` for callers who
// don't want to think about slices at all.
func MostRecent[T ~string](values []T) (T, bool) {
	if len(values) == 0 {
		var zero T
		return zero, false
	}
	return Filter(values, WithLimit(1))[0], true
}
