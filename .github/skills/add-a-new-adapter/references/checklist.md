# New Adapter Checklist

Copy this into todos when implementing a new adapter `adapters/<name>`.
Planning items happen first (roadmap doc); the rest only after approval.

## Planning (roadmap doc — plan-a-new-codex-feature Explore mode)

- [ ] `docs/roadmap/<name>-adapter.md` written from the template: motivation,
      scope table (in/out), toolchain decision (accepted + rejected libs with
      rationale), API surface (exact signatures), structured errors, observer
      integration, unit test plan, files table, out of scope, open decisions
- [ ] Boundary classified → port types listed (Source/Sink/IO/Latest/Tool)
- [ ] Pattern decision recorded (reuse / metadata-only / new Pattern type)
- [ ] Narrow-interface + fake decision recorded if client lib is not
      interface-shaped
- [ ] `docs/roadmap/index.md` row + `zensical.toml` `[nav]` Roadmap entry

## Package skeleton

- [ ] `adapters/<name>/doc.go` — paradigm, port mapping, observer story
- [ ] `adapters/<name>/errors.go` — every error type has `Error()`,
      `Unwrap()` (when wrapping), `LogValue()`
- [ ] `adapters/<name>/binding.go` — one constructor per supported port type,
      each returning the `ports.XxxAdapter` interface; `XxxAdapterOptions`
      structs with `Observer stats.Observer` ("Resolved from ctx when nil")
- [ ] `AdapterName()` returns `"<pkg>.<Constructor>"`
- [ ] Stream bridges in `stream.go` (if the transport has a continuous mode)
- [ ] `go.mod` dependency added (implementation time only)

## Ports integration

- [ ] New Pattern type (if decided): struct + `isPortPattern()` in
      `ports/pattern.go`; build/store logic in `ports/handle.go`; plugin/access
      path in `ports/*_port.go` (`PluginXxxPattern` + `patternSpec`) +
      `MissingPatternError` path (for `Register*` replay helpers);
      per-port-type acceptance validation at Plugin time; spec rendering
      decision in `ports/spec.go`
- [ ] Constructor names added to the "Implemented by" lists in the
      `ports/*.go` adapter interface godocs

## Tests (per plan-a-new-codex-feature matrix)

- [ ] Happy path per constructor (hand-written fake, no live server)
- [ ] Error path → typed error, correct fields; `errors.As` reaches inner
- [ ] `LogValue()` → `slog.KindGroup` + ALL field keys checked
- [ ] Observer called on success AND every error branch; nil Observer safe;
      plain Observer (no extension) falls back gracefully
- [ ] New observer extension (if any): Noop/Logging/fanout implement it +
      compile-time assertion in `stats/observer_test.go`
- [ ] `Example...()` functions for key constructors (deterministic)
- [ ] `go test -race ./adapters/<name>/` clean

## Documentation surfaces

- [ ] `.github/instructions/go-codex.instructions.md` — adapter table row
      (full API vocabulary + semantics)
- [ ] `docs/features/<name>.md` (+ guide if there is a workflow)
- [ ] `docs/reference/project-structure.md` — new directory row
- [ ] `zensical.toml` nav — feature/guide entries added; roadmap entry
      removed when shipped
- [ ] `docs/roadmap/<name>-adapter.md` deleted or marked shipped (user
      decides); `docs/roadmap/index.md` row removed
- [ ] Example: `examples/<name>/main.go` runs and exits 0 (or existing
      example extended)
- [ ] Review skill sync: `references/history.md` new round +
      known-facts bullet for every intentional design quirk

## Verification ritual

```bash
gofmt -l .                 # empty
go build ./...
go test ./...
go test -race ./adapters/<name>/ ./ports/
just check                 # 0 issues, no new suppressions
for d in examples/*/; do go run ./$d; done
```

- [ ] Markdown fence balance on every edited .md
- [ ] `zensical.toml` parses; all nav paths exist
- [ ] Zero dangling references to retired roadmap docs
