# versioning

Classifies and orders version-like strings — domain-agnostic, no
dependency on `docker`/`iotedge` or any other package in this module.
This package started as a Docker-image-tag-specific mechanism
(`docker.TagRank`/`docker.FilterTags`) and was generalized here once it
became clear the same problem shows up for any versioned identifier: Helm
chart versions, git tags, npm-style package tags, OS release channels,
and so on. `models/docker` now delegates to this package (see its
`tagfilter.go`) rather than duplicating the logic.

It is deliberately self-contained enough that it could become its own
standalone library one day — that's why it has its own README instead of
only a package-map row in the umbrella `examples/go-edge-models/README.md`.

## The problem

A registry/tool's plain `tags/list`-style response is an unordered list
of strings, and real-world tagging conventions are NOT uniform:

- **Strict semver**: `1.2.3`, `v2.0.0-rc.1+build.5`
- **"Semver-like"**: `3.1-debian`, `18.04`, `2024.01.15` — leading dotted
  integers, optionally with a suffix, common in Docker image tags and OS
  release channels, but not valid strict semver (wrong number of parts)
- **Opaque**: `latest`, `stable`, `edge`, a random hash — no version
  information at all

A caller wanting "the N most recent tags" needs to classify each string
into the right bucket and order accordingly — this package does exactly
that, and nothing else (no I/O, no registry-specific knowledge).

## Design

`Version` is a pointer-discriminated union with exactly one non-nil
field:

```go
type Version struct {
    SemVer     *SemVerRank
    SemVerLike *SemVerLikeRank
    Other      *string
}
```

`VersionCodec` (a `codex.UntaggedUnion`) classifies a string by trying,
**in order**: `validate.SemVer`, then `validate.SemVerLike`, then an
unconditional opaque fallback. Order matters — e.g. `"3.1.15-18"`
satisfies both `SemVer` and `SemVerLike`'s grammars, and must be claimed
by the first (stricter) branch.

`Compare(a, b Version) int` orders two values for "most version-recent
first": bucket precedence (SemVer > SemVerLike > Other), then numeric
comparison within a bucket, then alphabetical as a final tiebreaker for
opaque values.

## Usage

```go
import "github.com/DaniDeer/go-codex/examples/go-edge-models/models/versioning"

// Parse and classify a single value:
v := versioning.Parse("3.1-debian")
// v.SemVerLike != nil

// Sort + limit a whole list — generic over any named string type,
// works directly on docker.Tag with no conversion:
tags := []docker.Tag{"latest", "1.0.0", "2.0.0", "18.04"}
top := versioning.Filter(tags, versioning.WithLimit(2))
// top == []docker.Tag{"2.0.0", "1.0.0"}

// Plain strings work too:
names := []string{"v1", "v2", "v10"}
sorted := versioning.Filter(names, versioning.WithSort(versioning.SortAlphabetical))
```

## Important caveat: version-order, not chronological order

`SortByVersionDesc` (the default) has no access to any timestamp — it
infers "recent" purely from each string's own text. A registry could
re-tag an older build under a newer-looking version string, and this
package cannot detect that. If you need real chronological ordering,
you need a data source that actually reports timestamps (e.g. Docker
Hub's own `hub.docker.com/v2/repositories/.../tags` API, which includes
`last_updated` — unlike the plain OCI Distribution Spec `tags/list`
endpoint).

## Future ideas (not implemented)

These are proposed extensions, noted here so they're easy to pick up
later without re-deriving the rationale — none of them are needed for
this package's current scope:

- `versioning.MostRecent[T ~string](values []T) (T, bool)` — a one-shot
  "just give me the single most recent value" convenience over
  `Filter(values, WithLimit(1))[0]`, for callers who don't want to think
  about slices at all.
- `versioning.IsSemVer(s string) bool` / `IsSemVerLike(s string) bool` —
  classification-only helpers (no full `Version` struct) for callers who
  just want a yes/no check.
- Reuse in a hypothetical future `models/helm` (Helm chart versions) or
  `models/npm` (npm package tags) package — this is exactly why this
  package was generalized out of `models/docker` in the first place; see
  `filter_test.go`'s/`version_test.go`'s `fakeTag` generic tests, which
  already prove a non-Docker named string type flows through `Parse`/
  `Filter` unchanged.
