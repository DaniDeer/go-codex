# Declaring APIs and Ports: The Full Picture

> See also: [API Contracts](api-contracts.md) (one-struct-one-call convenience
> matrix) · [Codec — declare once](codec.md) (`Param`/`MergedParam[T]`/
> `Template[T]` mechanics) · [Ports, Plugins, and Adapters](ports-and-adapters.md)

This page compares how you **declare** a route, channel, request/reply pair,
MCP resource, file, or directory across all six boundaries go-codex ships
today: `api/rest`, `api/events`, `api/reqreply`, `api/mcp`, `ports.File`, and
`ports.Dir`. [API Contracts](api-contracts.md) answers "once declared, how do
I call it with one struct?" — this page answers "what does the declaration
itself look like, what's the default way, what escape hatches exist, and
where/why do the six boundaries diverge?"

## The two declaration workflows

Every boundary falls into one of two workflows, split by whether it produces
a machine-readable spec (OpenAPI/AsyncAPI/MCP manifest) that needs to
accumulate across many declarations into one document.

**Spec-backed (`api/rest`, `api/events`, `api/reqreply`, `api/mcp`) — three steps:**

```
NewRoute / NewChannel / NewResource / NewTool
    │
    └─ .Register(builder) ──→ Handle ──→ adapter (nethttp / mqtt5 / mcpgo)
                         └──→ builder.OpenAPISpec() / AsyncAPISpec() / MCPSpec()
```

A `*Builder` accumulates every declaration in a package/service so one call
produces the whole spec document. `.ClientHandle()` is available on
`rest.Route`, `events.Channel`, `reqreply.Route`, and `mcp.Tool` as a
builder-free shortcut when you only need the codec/merge helpers (e.g. an
HTTP client that never generates its own spec) — `mcp.Resource`/`mcp.Prompt`
do not offer `ClientHandle`, since resources/prompts are inherently
server-declared concepts with no independent client-side spec use case.

**Non-spec (`ports.File`, `ports.Dir`) — two steps:**

```
NewFile / NewDir
    │
    └─→ File[T] / Dir  ──→ adapter (file, or embedded in a ports.Pattern)
```

There is no `Builder`, no `Register`, and no `Handle` type — `NewFile`/
`NewDir` return the directly usable value. File I/O has no OpenAPI/AsyncAPI
analogue to accumulate, so the extra indirection the spec-backed boundaries
need doesn't apply here.

## Comparison table

| Boundary | Primary constructor (bare string) | Escape hatch (reuse a pre-built shape) | Vars/param type | Merge-capable into content type? | Builder/Register? |
|---|---|---|---|---|---|
| REST (`api/rest`) | `rest.NewRoute[Req,Resp]("GET", "/users/{id}", ...)` | `rest.NewPath("/users/{id}", params...)` + `rest.NewRouteFromPath(path, ...)` | `rest.PathParam` / `rest.MergedPathParam[T]` | ✅ via `rest.NewPathParam[T]` (merges into `Req`) | ✅ `Route.Register(builder)` |
| Events (`api/events`) | `events.NewChannel[T]("sensors/{id}/readings", ...)` | `events.NewTopic("sensors/{id}/readings", params...)` + `events.NewChannelFromTopic(topic, ...)` | `events.TopicParam` / `events.MergedTopicParam[T]` | ✅ via `events.NewTopicParam[T]` (merges into payload `T`) | ✅ `Channel.Register(builder)` |
| Req/reply (`api/reqreply`) | `reqreply.NewRoute[Req,Resp]("device/{id}/cmd", ...)` | `reqreply.NewTopic("device/{id}/cmd", params...)` + `reqreply.NewRouteFromTopic(topic, ...)` | `reqreply.TopicParam` / `reqreply.MergedTopicParam[Req]` | ✅ via `reqreply.NewTopicParam[Req]` (merges into `Req` only, never `Resp`) | ✅ `Route.Register(builder)` |
| MCP (`api/mcp`) | `mcp.NewResource[V,T]("items://{id}", codec, mcp.URIParam(field), ...)` | `mcp.NewResourceFromTemplate(template, codec, ...)` | `codex.Template[V]` (typed vars container, built from `codex.FieldCodec[V]`s via `URIParam`) | ❌ — `V` is independent of `T` by design (see below) | ✅ `Resource.Register(builder)` |
| File I/O (`ports.File`) | `ports.NewFile[T]("data/{id}.json", format, ...)` | `ports.FilePathTemplate{...}` + `ports.NewFileFromPathTemplate(t, format, ...)` | `ports.FilePathParam` / `ports.MergedFilePathParam[T]` | ✅ via `ports.NewFilePathParam[T]` (merges into decoded content `T`) | ❌ no builder — `NewFile` returns `File[T]` directly |
| Dir I/O (`ports.Dir`) | `ports.NewDir("data/{id}/", ...)` | `ports.DirPathTemplate{...}` + `ports.NewDirFromPathTemplate(t, ...)` | `ports.DirPathParam` (validate-only — no merge variant) | ❌ — `Dir` has no content type to merge into | ❌ no builder — `NewDir` returns `Dir` directly |

`mcp.Tool`'s input/output are plain typed `In`/`Out` codecs with no path/URI
vars at all (tools are invoked with a single JSON args object, not a
templated address) — it isn't a row above because it has no vars-declaration
story to compare; see [MCP tools](api-contracts.md#mcp-tools-apimcp) for its
shape. `mcp.Prompt` sits a tier below even `mcp.Resource`: its arguments are
validated (`ValidateArgs`) but handed to the app as a raw
`map[string]string`, never a typed/merged container — prompts are the
simplest, least-typed of the four `api/mcp` concepts.

## What's consistent

- **Bare-string-first is the default everywhere.** `NewRoute`, `NewChannel`,
  `NewRoute` (reqreply), `NewResource`, `NewFile`, and `NewDir` all accept
  the template as a plain string first, with param/field declarations as
  trailing variadic opts. You never have to pre-build a shape object just to
  declare one route/channel/resource/file — that's reserved for the reuse
  case below.
- **A reusable shape + `NewXFromY` escape hatch exists for all 6
  boundaries** (`rest.Path`+`NewRouteFromPath`, `events.Topic`+
  `NewChannelFromTopic`, `reqreply.Topic`+`NewRouteFromTopic`, `mcp`
  Template+`NewResourceFromTemplate`, `ports.FilePathTemplate`+
  `NewFileFromPathTemplate`, `ports.DirPathTemplate`+
  `NewDirFromPathTemplate`). Reach for it only when the *same* template and
  param declarations are shared by two or more
  routes/channels/routes/resources/files of *different* content types — see
  [API Contracts — Reusing a Topic/Path/FilePathTemplate](api-contracts.md#reusing-a-topicpathfilepathtemplate)
  for the full recipe. Declaring a single route/channel/resource/file never
  needs it.
- **A validate-only param escape hatch exists everywhere.** Every boundary
  lets you declare a var that is validated against its codec but never
  merged into any Go struct (`rest.PathParam{Name:"x"}`,
  `events.TopicParam{Name:"x"}`, `reqreply.TopicParam{Name:"x"}`,
  `ports.FilePathParam{Name:"x"}`, `ports.DirPathParam{Name:"x"}`, or a
  `codex.Template[V]` field the app chooses not to read after extraction).
  Use it when a var must be well-formed (e.g. matches a UUID codec) but the
  handler has no use for its value.
- **`codex.IdentityField`/`RequiredField`/`OptionalField`/`DefaultField`**
  underlie every merge-capable param's `get`/`set` closures across all
  boundaries — see [Codec — Reusing Field declarations](codec.md#reusing-field-declarations-for-pathtopicheaderquery-vars).

## Where it differs, and why

**`api/reqreply` now has a `Topic`+`NewRouteFromTopic` escape hatch, matching
the other five boundaries.** This was the one genuine, no-workaround gap
found during the original audit — reqreply was the only spec-backed
boundary without a reusable-shape escape hatch, even though the use case
(e.g. one topic family used for several distinct command types with
different `Req`/`Resp` pairs) is just as real for req/reply as it is for
REST paths or event topics. It now mirrors `events.Topic`/
`NewChannelFromTopic` exactly: `reqreply.Topic`, `reqreply.NewTopic`,
`reqreply.NewRouteFromTopic[Req,Resp]`. `reqreply.Builder` also gained the
matching `WithTopicCodec`/`WithTopicConstraints` builder-level override
(mirroring `events.WithTopicCodec`/`WithTopicConstraints`), closing a
second, related gap in the same pass — see `reqreply.InvalidTopicError`.

**`api/mcp` Resource uses `codex.Template[V]` instead of `codex.Param`/
`MergedParam[T]`.** Every other boundary's vars can *optionally* merge into
an existing `Req`/content type, so `Param`/`MergedParam[T]` (validate-only
OR merge-into-T) is the right shared primitive. MCP resources have **no**
merge target at all — `T` (the resource content) is application-produced
when the handler runs, not wire-decoded from the URI the way a REST body or
event payload is — so there's nothing for the vars to merge *into*. `V` is
therefore always a real, independent, typed vars container built from
`codex.FieldCodec[V]` declarations (via `mcp.URIParam`), exactly the same
mechanism `codex.Template[T]` itself already provides. This was a deliberate
design conclusion, not an oversight: forcing `Param`/`MergedParam[T]` onto
MCP resources would add a merge target that's never usable.

**`ports.Dir` has no merge-capable param variant.** `ports.FilePathParam`
comes in both a validate-only form and a merge-capable
`MergedFilePathParam[T]`/`NewFilePathParam[T]` form, because `File[T]` has a
decoded content type `T` for vars to merge into. `Dir` has no content type
at all (it lists/matches entries, it doesn't decode one payload) — so
`DirPathParam` is validate-only only, the same reasoning that keeps MCP
Resource vars from merging into `T`.

**`ports.File`/`ports.Dir` have no `Builder`/`Register`/`Handle`.** They
don't generate a spec document, so the extra indirection the four `api/*`
boundaries need (accumulate declarations into a builder, then ask the
builder for a handle and a spec) has nothing to accumulate into. `NewFile`/
`NewDir` return the directly usable value; adapters (`adapters/file`) and
`ports.Pattern` bindings consume it as-is.

**`api/mcp.Tool.ClientHandle()` returns `(*ToolHandle, error)`; the other
three boundaries' `ClientHandle()` return just the handle.** This is a
narrow, mechanical difference (JSON schema generation for tool args/results
can fail at `ClientHandle` time) rather than a design divergence — treat it
as a signature detail, not a workflow difference.

**Builder-level global codec/constraint overrides
(`WithPathCodec`/`WithPathConstraints`, `WithTopicCodec`/
`WithTopicConstraints`) exist for `api/rest`, `api/events`, AND
`api/reqreply`.** All three let you set a default var codec/constraint set
for every path/topic param in a `Builder` that doesn't specify its own —
`reqreply.WithTopicCodec`/`WithTopicConstraints` were added to close the
gap that existed here, mirroring `events`'s shape exactly (same option
names, same `InvalidTopicError` semantics).

**Error-response declarations use two different names for the same idea.**
`api/rest`, `api/reqreply`, and `api/mcp` name it `ErrorPattern[E,B]`;
`api/events` names the identical mechanism `ErrorChannel[E,B]` — matching
its own domain vocabulary (routes/tools reply with a *pattern* of status or
topic, a channel replies by being *itself* a channel). `ports.File`/
`ports.Dir` have no equivalent at all: file I/O errors are returned directly
to the caller, never "replied" to a remote peer, so there's no declaration
to make.

## See also

- [API Contracts](api-contracts.md) — the one-struct-one-call convenience
  matrix once a boundary is declared
- [Codec — declare once](codec.md#reusing-field-declarations-for-pathtopicheaderquery-vars) —
  the `codex.Param`/`MergedParam[T]`/`Template[T]`/`IdentityField` mechanics
  underneath every param type in the table above
- [Feature: REST API](../features/rest-api.md) · [Feature: Event Channels & MQTT](../features/events.md) ·
  [Feature: MCP Server](../features/mcp.md) · [Feature: Ports](../features/ports.md)
