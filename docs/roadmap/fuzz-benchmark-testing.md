# Fuzz & Benchmark Testing Infrastructure

> **Status:** Design complete — not yet implemented.
> [← Back to Roadmap](index.md)

## Motivation

go-codex has zero `func Fuzz*` targets and zero `func Benchmark*` functions
anywhere in the repository (confirmed via repo-wide grep), and no fuzz/bench
job in CI (`.github/workflows/ci.yml`). This is a real gap given how much
hand-rolled parsing the library ships: `validate/format.go` has 9
`regexp.MustCompile`-backed constraints (`Email`, `UUID`, `Slug`, `SemVer`,
`Hostname`, `ContainerImage`, `MQTTTopic`/`MQTTPublishTopic`, `EnvVarName`,
`HTTPPath`), `codex.HexColor()` has a fully hand-written byte-level hex
parser (`codex/color.go`), and `validate.CIDR`/`IPv4`/`IPv6` wrap
`net`-package parsing at a string boundary. None of these have ever been
exercised with adversarial/malformed input beyond the fixed example strings
in table-driven unit tests. Fuzzing closes that gap cheaply — Go's built-in
`testing.F` fuzzing requires no new dependency — and benchmarks establish a
performance baseline for the hottest paths (`codex.Struct[T]` encode/decode,
`format.JSON` marshal/unmarshal) before the library gets materially larger,
so future changes can be checked against a regression baseline instead of
discovered informally.

This is an internal-quality initiative, not a user-facing feature: no new
exported API, no new package. It strengthens what already ships rather than
adding surface area — the opposite kind of work from the adapter roadmap
entries.

## Scope decisions (what's in Phase 1, what's deferred)

| In scope | Out of scope |
|---|---|
| `testing.F`-based fuzz targets for every hand-rolled string/byte parser reachable from a public `Decode`/`Constraint.Check` (see table below) | Fuzzing `net`-delegated parsing internals (`net.ParseCIDR`, `net.ParseIP`) — those are stdlib-tested already; fuzz only go-codex's own regex/byte-parsing code |
| `testing.B`-based benchmarks for the hottest declared paths: `codex.Struct[T].Encode`/`Decode` (N-field struct), `format.JSON.Marshal`/`Unmarshal`, `codex.SliceOf`/`StringMap` encode/decode at representative sizes, `Refine`/constraint-chain overhead | Benchmarking every constructor — only the paths on the "declare once, called per-request" hot path matter; one-shot constructors (`codex.Struct` itself building the schema) are not per-call hot paths |
| A `benchstat`-comparable baseline commit (`docs/reference/benchmarks.md` or a checked-in `.bench` baseline file) so future PRs can `benchstat old.txt new.txt` | A dedicated performance-regression CI gate that fails builds on regression — Phase 1 ships the baseline and the manual `benchstat` workflow; an automated gate is a Phase 2 decision once real baseline numbers exist and false-positive noise (CI runner variance) can be evaluated |
| CI: a new `fuzz` job running each fuzz target for a short, fixed duration (`-fuzztime=30s` per target) on every PR — fast enough to run inline, not a substitute for a longer scheduled/nightly run | A long-running nightly/scheduled fuzz job with corpus persistence across runs (`testdata/fuzz/` committed) — worth adding once Phase 1's short-duration job has run clean for a while and proven the harness is stable |
| Fixing any crash/panic a fuzz target discovers, adding the failing input as a permanent regression seed corpus entry | Fixing crashes discovered in EXTERNAL dependencies (paho, go-redis, zmq4, goose) — out of go-codex's control; report upstream instead |

## Fuzz targets (Phase 1)

| Target | Package | Function under test | What a crash would mean |
|---|---|---|---|
| `FuzzEmail` | `validate` | `Email.Check` | regex panic or pathological backtracking on adversarial input |
| `FuzzUUID` | `validate` | `UUID.Check` | same |
| `FuzzSlug` | `validate` | `Slug.Check` | same |
| `FuzzSemVer` | `validate` | `SemVer.Check` | same (this regex is the most complex in the file — highest ReDoS risk) |
| `FuzzHostname` | `validate` | `Hostname.Check` | same |
| `FuzzContainerImage` | `validate` | `ContainerImage.Check` | same (second-most-complex regex) |
| `FuzzMQTTTopic` | `validate` | `MQTTTopic.Check`, `MQTTPublishTopic.Check` | UTF-8 boundary handling, null-byte/length-limit edge cases |
| `FuzzHTTPPath` | `validate` | `HTTPPath.Check` | same |
| `FuzzHexColor` | `codex` | `HexColor().Decode` | out-of-bounds slice access in the hand-written hex parser (`hexPair`/`dupNibble`/`isHexDigits` in `codex/color.go`) — the highest-value fuzz target in this list since it is BYTE-LEVEL manual parsing, not just a regex |
| `FuzzJSONUnmarshal` | `format` | `format.JSON(structCodec).Unmarshal` | panics on malformed JSON reaching the codec boundary (map[string]any intermediate) rather than a clean decode error |
| `FuzzTOMLUnmarshal` | `format` | `format.TOML(structCodec).Unmarshal` | same, TOML intermediate |

Each fuzz target seeds its corpus with: the existing table-driven unit test
cases (valid AND invalid strings already in `*_test.go`), plus a handful of
adversarial strings (empty, single character, maximum realistic length,
strings with embedded null bytes / control characters / multi-byte UTF-8
sequences at truncation boundaries).

## Benchmark targets (Phase 1)

| Target | Package | Measures |
|---|---|---|
| `BenchmarkStructEncode` / `BenchmarkStructDecode` | `codex` | A representative N-field struct (mirrors the `User`/`Order`-shaped codecs used across examples) — the single most-called path in the whole library |
| `BenchmarkSliceOfEncode` / `BenchmarkSliceOfDecode` | `codex` | Collection codecs at 10/100/1000 elements — establishes how encode/decode cost scales |
| `BenchmarkStringMapEncode` / `BenchmarkStringMapDecode` | `codex` | Same, for map-shaped codecs |
| `BenchmarkRefineChain` | `codex` | Cost of N chained `Refine` constraints on a single field — establishes whether constraint-chain length matters in practice |
| `BenchmarkJSONMarshal` / `BenchmarkJSONUnmarshal` | `format` | End-to-end `format.JSON(structCodec)` round trip — the most common format in every example |
| `BenchmarkHexColorDecode` | `codex` | The hand-written parser vs. a hypothetical `regexp`-based alternative — informs whether the manual parser is worth its complexity |

Each benchmark reports `ns/op`, `B/op`, `allocs/op` (`b.ReportAllocs()`) —
allocation counts matter more than raw ns/op for a codec library, since the
intermediate `map[string]any`/`any` boxing is the most likely allocation
hotspot.

## Structured errors

None — this is test/benchmark infrastructure, not runtime API. No new
exported error types.

## Observer integration

None — fuzz targets and benchmarks call `Decode`/`Encode`/`Check` directly,
bypassing any adapter or observer layer entirely (same as existing unit
tests).

## Unit test plan

N/A in the usual sense — the fuzz targets and benchmarks ARE the deliverable.
Verification for this roadmap item is:

| Check | What it confirms |
|---|---|
| `go test -fuzz=FuzzXxx -fuzztime=30s ./validate/...` for each of the 8 `validate` fuzz targets | No panics in 30s of adversarial input per target |
| `go test -fuzz=FuzzHexColor -fuzztime=30s ./codex/...` | No panics in the hand-written hex parser |
| `go test -fuzz=FuzzJSONUnmarshal -fuzztime=30s ./format/...` (+ TOML) | No panics at the format-boundary decode path |
| `go test -bench=. -benchmem ./codex/... ./format/...` | All benchmarks run and report allocs/op; establishes the baseline file |
| `just check` | Confirms new files don't introduce staticcheck/gosec findings (fuzz/bench files are still real Go code subject to the same linting) |

## Files to create

| File | Responsibility |
|---|---|
| `validate/fuzz_test.go` | 8 fuzz targets for `Email`/`UUID`/`Slug`/`SemVer`/`Hostname`/`ContainerImage`/`MQTTTopic`+`MQTTPublishTopic`/`HTTPPath` |
| `codex/color_fuzz_test.go` | `FuzzHexColor` (co-located with `color_test.go`'s existing table-driven cases, which become the seed corpus) |
| `format/fuzz_test.go` | `FuzzJSONUnmarshal`, `FuzzTOMLUnmarshal` against a shared representative struct codec |
| `codex/bench_test.go` | `BenchmarkStructEncode`/`Decode`, `BenchmarkSliceOfEncode`/`Decode`, `BenchmarkStringMapEncode`/`Decode`, `BenchmarkRefineChain` |
| `format/bench_test.go` | `BenchmarkJSONMarshal`/`Unmarshal` |
| `codex/color_bench_test.go` | `BenchmarkHexColorDecode` (co-located with the fuzz target) |
| `docs/reference/benchmarks.md` | Baseline numbers (from the CI runner or a documented reference machine) + `benchstat` usage instructions for comparing future PRs |
| `.github/workflows/ci.yml` | New `fuzz` job: `go test -fuzz=FuzzXxx -fuzztime=30s` per target, run in parallel or sequentially depending on CI time budget |
| `.github/instructions/go-codex.instructions.md` | Brief mention under a "Testing conventions" note: fuzz targets required for new hand-rolled parsers; benchmarks required for new hot-path codec constructors |
| `.github/skills/plan-a-new-codex-feature/SKILL.md` | Optional: add a 6th "mandatory requirement" bullet (or a note under the existing Unit Tests requirement) — "any new hand-rolled string/byte parser needs a fuzz target" — so this becomes a durable convention, not a one-time cleanup |

## Out of scope (Phase 2)

- **Automated CI regression gate on benchmark results** — Phase 1 ships the
  baseline and manual `benchstat` workflow only; an automated fail-the-build
  gate needs real baseline data first to tune acceptable variance thresholds
  (shared CI runners are noisy).
- **Long-running/nightly fuzz job with persisted corpus** — Phase 1's
  30-second-per-target inline job is a smoke test, not exhaustive fuzzing.
  A scheduled nightly job with `testdata/fuzz/` corpus persistence across
  runs is a natural Phase 2 once the harness has proven stable.
- **Fuzzing adapter-layer code** (`adapters/*`) — those parse
  transport-specific wire formats (MQTT packets via paho, HTTP via net/http)
  where the underlying libraries already have their own fuzzing/hardening;
  go-codex's adapter code mostly wires codecs to those libraries rather than
  parsing bytes itself. Revisit only if a specific adapter is found to do
  meaningful hand-rolled parsing (none currently do, per this doc's audit).

## Open design decisions (to resolve before/during implementation)

1. **Where does the benchmark baseline live?** A checked-in `docs/reference/benchmarks.md`
   with numbers from a documented reference machine/CI runner (simple, but
   numbers drift as CI hardware changes), or a `.bench-baseline` raw
   `go test -bench` output file for `benchstat old vs new` comparison
   (more precise, but not human-readable in a docs page). Leaning: both —
   the `.md` page for human context ("here's roughly what to expect and
   why"), a raw baseline file for exact `benchstat` comparisons.
2. **Representative struct shape for `BenchmarkStructEncode`/`Decode`** —
   should it mirror an existing example's domain type (e.g. `sensor-service`'s
   `Reading`) for realism, or a purpose-built synthetic struct with a fixed,
   documented field count/type mix (e.g. 2 strings, 2 ints, 1 nested struct,
   1 slice) for a controlled, comparable-over-time benchmark? Leaning:
   synthetic — real domain structs change shape over time as examples
   evolve, which would silently invalidate historical benchmark comparisons.
3. **CI job duration budget** — `-fuzztime=30s` per target × 11 targets is
   ~5.5 minutes sequential, or under a minute if run in parallel (fuzz
   targets are independent, embarrassingly parallelizable). Leaning:
   parallel job matrix (one CI job per fuzz target, or grouped by package) to
   keep the PR feedback loop fast — resolve based on actual CI runner
   availability/cost at implementation time.
