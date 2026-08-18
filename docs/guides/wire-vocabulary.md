# Guide: Declarative wire-format vocabulary

A package that owns a wire/file format (a JSON manifest, a device config
patch, a directory of per-device files, …) accumulates its OWN small
vocabulary of constants and conventions as it grows: top-level wire-key
names, dotted-key prefixes, file/directory path templates, and named
identifier types wrapping validated strings. Left undeclared, these tend
to get copy-pasted (and drift) across every file that touches the same
format. This guide is the recipe for consolidating them into ONE file per
package — a single source of truth — built entirely from existing
go-codex primitives (no new package required).

## The recipe: one `keys.go`/`config.go` per package

Put ALL of the following in one file, grouped by concern:

1. **Top-level wire-key constants** — plain `const` strings for the fixed
   keys a wire format uses (e.g. `"modulesContent"`, `"$edgeAgent"`).
2. **Dotted-key prefixes + codecs** — for keys shaped "prefix + validated
   name segment", use [`codex.PrefixedKeyCodec`](../concepts/codec.md#prefixedkeycodec--a-prefix--validated-name-segment-convenience)
   instead of hand-rolling a `Constraint` + `MapCodecValidated` pair.
3. **File/directory path templates** — build these with plain
   `fmt.Sprintf` (readable, and a `var` rather than `const` since
   `fmt.Sprintf` isn't a constant expression — evaluated once at package
   init, functionally identical to a const for every call site), then
   pass the resulting template string to `ports.NewFile`/`ports.NewDir`
   with `ports.FilePathParam`/`ports.DirPathParam` declarations.
4. **Named identifier types** — wrap a validated string in a named Go
   type (`type ModuleName string`) via `codex.MapCodecSafe`, with a smart
   constructor (`NewModuleName(s string) (ModuleName, error)`) built on
   the codec's own `.New(...)`.
5. **Fixed-value ("constant") fields** — for a field that may ONLY ever
   equal one specific value at a specific place in the format (not one
   of several, and not user-configurable), use `codex.Eq(base, value)`
   instead of a degenerate single-element `validate.OneOf(value)`. See
   [Fixed-value fields: `Eq`/`Pure`](#fixed-value-fields-eqpure) below.

### Reference implementation

`examples/go-edge-models` has two real files following this recipe —
read them directly rather than a synthesized snippet:

- [`models/iotedge/manifesttemplate/keys.go`](https://github.com/DaniDeer/go-codex/blob/main/examples/go-edge-models/models/iotedge/manifesttemplate/keys.go) — wire-key constants (#1) + dotted-key codecs via `PrefixedKeyCodec` (#2), for a deployment manifest's `$edgeAgent`/`$edgeHub` buckets. See the sibling [`lifecycle.go`](https://github.com/DaniDeer/go-codex/blob/main/examples/go-edge-models/models/iotedge/manifesttemplate/lifecycle.go)'s `TypeCodec` for #5 (fixed-value fields).
- [`models/iotedge/usecase/config.go`](https://github.com/DaniDeer/go-codex/blob/main/examples/go-edge-models/models/iotedge/usecase/config.go) — path templates via `fmt.Sprintf` (#3) + named identifier types (#4), for a use case's on-disk file layout. This file has no fixed-value fields of its own (no `codex.Struct` at all — only path templates and identifier types), so #5 doesn't apply here; see `lifecycle.go` instead.

Both files are the FIRST thing a maintainer reads to answer "what does
this wire key actually look like" or "where do these files live on disk"
— every other file in the package imports these constants/codecs instead
of re-hardcoding the same literal strings.

## Is this a go-codex library gap, or just usage?

**Just usage of existing primitives, with one small addition.** Everything
in the recipe above composes primitives that already exist:

| Concern | Primitive | New in this guide? |
|---|---|---|
| Wire-key constants | plain Go `const` | no |
| Dotted-key codec | `codex.PrefixedKeyCodec` | **yes** — generalizes a recipe that used to be hand-rolled per package |
| Path templates | `ports.FilePathParam`/`DirPathParam`, `fmt.Sprintf` | no |
| Named identifier types | `codex.MapCodecSafe` + smart constructor (`.New`) | no |
| REST path / event topic vars | `rest.NewPathParam`/`events.NewTopicParam` | no |
| Fixed-value ("constant") fields | `codex.Eq`/`codex.Pure` | no |

The ONE genuine gap closed here is `codex.PrefixedKeyCodec` — a small,
purely-compositional addition to the `codex` package (see
[Codec concepts](../concepts/codec.md#prefixedkeycodec--a-prefix--validated-name-segment-convenience)).
Path/route/topic-variable declaration ALREADY has a fully declarative,
consistent story across boundaries (`rest.PathParam`, `events.TopicParam`,
`ports.FilePathParam`/`DirPathParam`, `ports.CacheKeyParam`) — this guide
does not introduce anything new there, only documents how to USE what
already exists well.

## Reusing a Topic/Path/FilePathTemplate

`events.ChannelHandle.BuildTopic`, `rest.RouteHandle.BuildPath`, and
`ports.File.BuildPath`/`ports.Dir.BuildPath` are all logically independent
of their generic payload type — they only touch the template string plus
the declared variable params. `events.Topic`, `rest.Path`,
`ports.FilePathTemplate`, and `ports.DirPathTemplate` extract that
payload-independent shape into its own reusable value.

**This is opt-in, not the default.** The plain-string form
(`events.NewChannel[T]("devices/{deviceID}/status", codec, opts...)`, and
the `rest`/`ports` equivalents) remains completely unchanged and stays
the default, primary way to declare a one-off channel/route/file/dir.
Reach for `Topic`/`Path`/`FilePathTemplate`/`DirPathTemplate` ONLY when
you find yourself declaring the SAME template+params shape for two or
more channels/routes/files (of different payload types) and want that
shape to have exactly one source of truth:

```go
var deviceStatusTopic = events.NewTopic("devices/{deviceID}/status",
    events.TopicParam{Name: "deviceID", Codec: &deviceIDCodec},
)

var deviceOnlineChannel = events.NewChannelFromTopic(deviceStatusTopic, deviceOnlineCodec,
    events.Subscribe{Summary: "Receive device online event"},
)
var deviceOfflineChannel = events.NewChannelFromTopic(deviceStatusTopic, deviceOfflineCodec,
    events.Subscribe{Summary: "Receive device offline event"},
)

// Standalone use — no payload codec involved at all:
topic, err := deviceStatusTopic.BuildTopic(map[string]string{
    "deviceID": "f47ac10b-58cc-4372-a567-0e02b2c3d479",
})
```

A channel/route/file declared this way is byte-for-byte identical to one
declared with the plain-string constructor and the same params passed
inline — nothing downstream (adapters, `Register`, spec generation) can
tell the difference. Every other option (`ChannelMeta`, `Subscribe`/
`Publish`, `RouteMeta`, `WithSecurityScheme`, formats, error patterns, …)
is passed through to `NewChannelFromTopic`/`NewRouteFromPath`/
`NewFileFromPathTemplate`/`NewDirFromPathTemplate` exactly as it would be
to the plain constructor — using a bundle for the template+params portion
restricts nothing else about the declaration.

See `examples/api-events`, `examples/api-rest`, and `examples/file-io`
for runnable demonstrations of each.

### Does this restrict richer route/channel declarations (e.g. HATEOAS)?

No. `Path`/`Topic`/`FilePathTemplate` capture ONLY the template+params
slice — they are not a competing or parallel declaration model. A future
codec-defined-HATEOAS feature (see `docs/roadmap/idea-codec-defined-hateoas.md`)
would reference full `rest.Route` VALUES (method, codecs, meta — the
whole thing), regardless of whether that Route's path portion was built
via `NewRoute` directly or via `NewRouteFromPath` — adopting a bundle
where convenient changes nothing about how the resulting `Route`/
`Channel`/`File` looks or behaves afterward.

## Fixed-value fields: `Eq`/`Pure`

"Define a constant with a codec, use it in exactly one place, and only
this constant is valid there" is already fully supported — see
[Codec concepts: Pure and Eq](../concepts/codec.md#pure-and-eq--fixed-and-single-value-codecs)
for the full mechanics. This section covers WHEN to reach for it as part
of a package's wire-format vocabulary:

- **`codex.Eq(base, value)`** — the field only accepts exactly `value`;
  anything else is a `ConstraintError`. Use for a field that is always
  one specific value BY DEFINITION of the format (not user-configurable,
  not one of several valid choices).
- **`codex.Pure(value)`** — the field ALWAYS decodes/encodes to `value`
  regardless of wire input (no error, just forced). Use for a field that
  is automatically/always set by the format itself (e.g. a protocol
  version marker), never meaningfully supplied by a caller.

**Spotting the case**: a `validate.OneOf(...)` constraint called with
EXACTLY ONE argument is a degenerate single-element enum — a strong
signal the field is actually a fixed constant, not a genuine multiple-
choice enum, and `codex.Eq` communicates that intent more directly:

```go
// Before: a single-value OneOf reads like there COULD be more choices.
var TypeCodec = codex.MapCodecSafe(
    codex.String().Refine(validate.OneOf("docker")),
    func(s string) Type { return Type(s) },
    func(t Type) (string, error) { return string(t), nil },
)

// After: Eq states directly that "docker" is the ONLY valid value.
var TypeCodec = codex.MapCodecSafe(
    codex.Eq(codex.String(), "docker"),
    func(s string) Type { return Type(s) },
    func(t Type) (string, error) { return string(t), nil },
)
```

Same wire shape, same validation, same error on mismatch — purely a
clearer expression of intent. See
[`models/iotedge/manifesttemplate/lifecycle.go`](https://github.com/DaniDeer/go-codex/blob/main/examples/go-edge-models/models/iotedge/manifesttemplate/lifecycle.go)'s
`TypeCodec` for the real, in-repo version of this exact refactor —
contrast with the SAME file's `StatusCodec`/`RestartPolicyCodec`, which
keep `validate.OneOf` because they are genuine multi-value enums
(`"running"`/`"stopped"`, `"always"`/`"on-failure"`/…).

Two ideas considered and rejected for this recipe:
- A thin `codex.ConstString(value)` wrapper over `Eq(codex.String(), value)`
  — real but marginal (saves a few characters over an already-one-line
  call); not worth its own constructor.
- Enforcing that a constant-codec pair may only be referenced from
  exactly one call site in the whole codebase — NOT achievable as a
  codec/library feature at all (a `Codec[T]` value has no visibility
  into its own static call graph); would require an unrelated
  static-analysis/lint tool, and would work against `Eq`/`Pure`'s own
  reuse benefit (the same fixed value validated identically wherever the
  format legitimately reuses it).
