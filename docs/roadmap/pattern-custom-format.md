# `CustomFormat` — General Binary/Custom Wire-Format Escape Hatch for `ports.Pattern`

> **Status:** Design complete — not yet implemented.
> [← Back to Roadmap](index.md)

## Motivation

`format.Gob` (typed binary) and `format.Binary` (raw `[]byte` — PNG, PDF, any
opaque blob) already exist and already work everywhere in go-codex **except**
through a declared `ports.Pattern`. `FilePattern`, `CachePattern`, and
`SocketPattern` each carry a `Format FileFormatKind` field — a closed enum of
`FileFormatJSON`/`FileFormatYAML`/`FileFormatTOML` — because a generic
`format.Format[T]` cannot live in the non-generic `Pattern` struct (patterns
are stored heterogeneously in a `[]Pattern` slice; `FilePattern` itself must
stay monomorphic).

Consequence: a port whose declared payload is a PNG, a protobuf message, or
any Gob-encoded struct cannot use the declarative `Pattern` shortcut for
`FilePattern`/`CachePattern`/`SocketPattern` — it must fall back to
handle-first wiring (bypass `Pattern` entirely, build the `format.Format[T]`
by hand). This is a real gap: `REST`/`Event`/`ReqReply` patterns do NOT have
this restriction — their built handles expose `.WithFormats(...
format.Format[Resp])` / `.WithRequestFormats(...)` /
`.WithSubscribeFormats(...)`/`.WithPublishFormats(...)`, which accept **any**
`format.Format[T]` with zero enum restriction. `FileFormatKind` is the only
place in go-codex where wire format is closed to three names.

Rather than growing `FileFormatKind` one binary format at a time (Gob today,
protobuf/msgpack/CBOR tomorrow — a repo change for every new format forever),
add ONE general escape hatch that closes the gap permanently: a field that
accepts an already-built `format.Format[T]` value, stored type-erased (`any`)
exactly like every other heterogeneous `Pattern`-internal value, resolved
generically at build time (the build functions are already generic over
`T`).

## Scope decisions

| In scope | Out of scope |
|---|---|
| `CustomFormat any` field on `FilePattern`, `CachePattern`, `SocketPattern` | REST/Event/ReqReply/MCP patterns — already unrestricted via handle `.WithFormats()` family, not touched |
| Type-erased storage of a pre-built `format.Format[T]` value | A factory closure (`func(codex.Codec[T]) format.Format[T]`) — rejected, see below |
| Build-time type assertion + `PatternRegisterError` on mismatch | A dedicated `FileFormatGob` enum value — rejected, see below |
| Precedence: `CustomFormat` (when non-nil) overrides `Format` entirely | Spec/schema rendering changes — `format.Format[T].Schema()` already exists and is unaffected |

**Rejected: factory closure instead of a pre-built value.** A closure
(`func(codex.Codec[T]) format.Format[T])` would defer construction to build
time, but the caller already has the concrete codec in hand at
`Pattern`-declaration time (it's the same codec passed to
`NewSourcePort[T]`/`NewIOPort[Req,Resp]`/etc. moments earlier) — there is
nothing to defer. A pre-built value is simpler, requires no wrapper, and
matches how `Pattern`-internal `any` storage already works everywhere else
(`patternHandle`/`patternSpec` maps are exactly this pattern).

**Rejected: a dedicated `FileFormatGob` enum value.** JSON, YAML, and TOML
share one construction shape: `format.New`-style marshal/unmarshal through a
`map[string]any` intermediate (`codec.Encode`/`Decode` round trip) — that's
why `fileFormatFor`'s switch cleanly dispatches between them. Gob (and every
other binary format) is architecturally different: `NewTyped`-style, the
typed value encoded directly with NO intermediate representation. It does
not belong in the "select one of N structurally-identical siblings" enum —
the general escape hatch is the right home for it, and for everything after
it (protobuf, msgpack, CBOR, a custom framed protocol) with zero repo
changes required for the next one.

## API surface

```go
// FilePattern (ports/pattern.go) — CustomFormat added, Format/Opts unchanged.
type FilePattern struct {
    Path string
    Format FileFormatKind
    // CustomFormat, when non-nil, overrides Format entirely: it must hold a
    // format.Format[T] value matching the port's payload type T (Format[Resp]
    // on an IOPort — the file's content is the port's response, same as
    // today; Format[T] on a SinkPort). A type mismatch returns
    // PatternRegisterError at construction.
    //
    // Use this for binary/custom formats FileFormatKind cannot express:
    //
    //	ports.FilePattern{
    //	    Path: "images/{id}.png",
    //	    CustomFormat: format.Binary(pngCodec).WithContentType("image/png"),
    //	}
    //	ports.FilePattern{
    //	    Path: "cache/{id}.bin",
    //	    CustomFormat: format.Gob(myStructCodec),
    //	}
    CustomFormat any
    Opts []format.FileOpt
}

// CachePattern and SocketPattern gain the identical field + doc comment
// (same precedence rule, same error path). SocketPattern's CustomFormat
// applies to BOTH directions when the port is one-directional (In=T or
// Out=T); a DuplexPort with asymmetric wire formats (e.g. JSON in, binary
// out) is Phase 2 — see Open design decisions.
```

```go
// ports/handle.go — fileFormatFor gains a customFormat parameter; callers
// (buildEventPatternHandles, buildDualCodecPatternHandles, the SocketPattern
// build path) pass pat.CustomFormat through unchanged.
func fileFormatFor[T any](
    portName, kind string, // kind used only in the error message
    fileKind FileFormatKind,
    customFormat any,
    codec codex.Codec[T],
) (format.Format[T], error) {
    if customFormat != nil {
        f, ok := customFormat.(format.Format[T])
        if !ok {
            return format.Format[T]{}, PatternRegisterError{
                Port: portName, Kind: kind,
                Err: fmt.Errorf("CustomFormat: want format.Format[%T], got %T", *new(T), customFormat),
            }
        }
        return f, nil
    }
    switch fileKind {
    case FileFormatYAML:
        return format.YAML(codec), nil
    case FileFormatTOML:
        return format.TOML(codec), nil
    default:
        return format.JSON(codec), nil
    }
}
```

`fileFormatFor` becomes fallible (it returns `(format.Format[T], error)`
instead of `format.Format[T]`) — a signature change confined to
`ports/handle.go`; all three call sites already sit inside functions that
return `(handles, specs, error)`, so propagating the new error path is a
one-line change per call site (`f, err := fileFormatFor(...); if err != nil
{ return nil, nil, err }`).

## Structured errors

No new error type. Reuses `PatternRegisterError{Port, Kind, Err}` (already
implements `Error()`, `Unwrap()`, `slog.LogValuer`) — the same type
`SocketPattern`'s port-role rejection already returns for "this Pattern
value is invalid for this port/config" failures. `Kind` is `"file"`/
`"cache"`/`"socket"` per the pattern being built; `Err` carries the
want/got type mismatch message.

## Observer integration

None required. This is a construction-time (build) concern only — no
adapter `Activate`/`Transform`/`Bind` code changes. Once built, the
resulting `format.Format[T]` flows through the exact same runtime paths
(file read/write, cache get/set, socket frame encode/decode) that already
fire their existing observer events (`FileObserver`, `CacheObserver`,
`RecordSubscribe`/`RecordPublish`) — those call sites are unaware whether
the format came from the enum or `CustomFormat`.

## Unit test plan

| ID | Test | Verifies |
|---|---|---|
| CF1 | `FilePattern{CustomFormat: format.Gob(codec)}` on an `IOPort` | built `format.File[Resp]` round-trips Gob-encoded data |
| CF2 | `FilePattern{CustomFormat: format.Binary(pngCodec)}` on a `SinkPort` | raw bytes pass through unchanged, `validate.PNG`-style constraint enforced |
| CF3 | `CachePattern{CustomFormat: format.Gob(codec)}` | `Cache[T].Format` round-trips through `GetAdapter`/`SetAdapter` |
| CF4 | `SocketPattern{CustomFormat: format.Gob(codec)}` | `Socket[In,Out]` frame codec is the custom format on both directions |
| CF5 | Type mismatch (`CustomFormat` holds `format.Format[OtherType]`) | `PatternRegisterError` returned, `errors.As` reaches it, `LogValue` has `port`/`kind`/`err` keys |
| CF6 | `CustomFormat` nil, `Format` set to `FileFormatYAML` | unchanged existing behavior (regression guard) |
| CF7 | `CustomFormat` non-nil AND `Format` also non-zero | `CustomFormat` wins, `Format` silently ignored (documented precedence) |
| — | `ExampleFilePattern_customFormat` or similar | deterministic, demonstrates PNG-via-Pattern |

## Files to create / modify

| File | Change |
|---|---|
| `ports/pattern.go` | Add `CustomFormat any` field + godoc to `FilePattern`, `CachePattern`, `SocketPattern` |
| `ports/handle.go` | `fileFormatFor` becomes fallible; all 3 call sites updated |
| `ports/pattern_test.go` / `ports/port_test.go` | CF1–CF7 |
| `docs/features/ports.md` | Pattern section: document `CustomFormat`, link to `format.Gob`/`format.Binary` |
| `docs/guides/ports.md` | One worked example (PNG via `FilePattern`) |
| `.github/instructions/go-codex.instructions.md` | `ports` row: extend `FilePattern`/`CachePattern`/`SocketPattern` entries with `CustomFormat` |
| `examples/file-io` or a new example | Demonstrate `CustomFormat` with `format.Binary` (PNG) end-to-end through a `Pattern` |

## Out of scope (Phase 2)

- **Asymmetric `SocketPattern` formats** (different `CustomFormat` for In vs
  Out on a `DuplexPort`) — today one `CustomFormat` value applies to
  whichever side(s) are non-`struct{}`; splitting into `CustomInFormat`/
  `CustomOutFormat` is a natural follow-up if a real duplex use case needs
  JSON-in/binary-out (or vice versa). No use case yet.
- **Spec/schema documentation of custom formats** — `format.Format[T].Schema()`
  already exists and works; whether the AsyncAPI/OpenAPI renderers should
  render a different `contentType` (e.g. `image/png` from
  `format.Binary(...).WithContentType(...)`) instead of assuming JSON is a
  pre-existing question, not introduced by this feature.

## Open design decisions

1. **Error message wording for the type mismatch** — the sketch above emits
   `want format.Format[%T], got %T` using `*new(T)` for the zero value;
   confirm this reads clearly in practice (Go's `%T` on a zero-value
   interface can be verbose for large struct types) — revisit if the error
   text proves hard to read in tests.
2. **Should `CustomFormat` also gain a same-named counterpart on `Cache[T]`/
   `Socket[In,Out]`/`format.File[T]` handles for POST-build override** (mirroring
   `RouteHandle.WithFormats`) — deferred: the Pattern-declaration-time field
   already covers the "declare once in domain code" philosophy these three
   patterns share; a handle-level mutator would be a second way to do the
   same thing. Add only if a use case demands changing the format AFTER
   the port is already constructed (today: not needed, the pattern is
   declared once at port construction).
