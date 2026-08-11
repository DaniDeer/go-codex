// Package versioning classifies and orders version-like strings —
// domain-agnostic, no dependency on docker/iotedge or any other package
// in this module. It generalizes a pattern first built for Docker image
// tags: real-world version strings are rarely pure semver (Docker tags,
// OS release channels, and many other ecosystems mix strict semver with
// looser conventions and fully opaque labels), so this package classifies
// any string into one of three buckets — strict semver, "semver-like"
// (leading dotted integers plus an optional suffix), or opaque — and
// orders them accordingly.
//
// See this package's own README.md for the full design rationale, usage
// examples, and a list of proposed-but-not-yet-implemented extensions.
//
// Files:
//
//   - version.go — Version (the SemVer/SemVerLike/Other union),
//     SemVerRank/SemVerLikeRank, VersionCodec, Parse, Compare.
//   - filter.go — SortMode, FilterOpt, WithSort/WithLimit, Filter.
package versioning
