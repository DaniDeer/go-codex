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

### Reference implementation

`examples/go-edge-models` has two real files following this recipe —
read them directly rather than a synthesized snippet:

- [`models/iotedge/manifesttemplate/keys.go`](https://github.com/DaniDeer/go-codex/blob/main/examples/go-edge-models/models/iotedge/manifesttemplate/keys.go) — wire-key constants (#1) + dotted-key codecs via `PrefixedKeyCodec` (#2), for a deployment manifest's `$edgeAgent`/`$edgeHub` buckets.
- [`models/iotedge/usecase/config.go`](https://github.com/DaniDeer/go-codex/blob/main/examples/go-edge-models/models/iotedge/usecase/config.go) — path templates via `fmt.Sprintf` (#3) + named identifier types (#4), for a use case's on-disk file layout.

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
