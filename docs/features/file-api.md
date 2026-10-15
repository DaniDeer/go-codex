# File API — `api/file`

> **Status:** Phase 1 implemented with full Router + Declarative Middleware
> parity (matches `api/rest`/`api/events`). The `api/file` package and its
> `adapters/file.Backend` are fully built, tested, and usable TODAY — but
> `ports.File`/`ports.Dir` have NOT yet been migrated to delegate onto this
> stack (that migration remains open; see `docs/roadmap/file-api.md`). Use
> `api/file` directly for new code; `ports.File`/`adapters/file`'s existing
> `os.*`-based functions continue to work unchanged.

`api/file` is a transport-agnostic API layer for file-shaped resources,
mirroring `api/rest`/`api/events`/`api/reqreply`'s declare → attach → dispatch
model. A declared `Resource[T]` describes WHAT a file looks like (its path
template, codec, and path parameters); a `Client` with an attached `Backend`
describes WHERE it actually lives (local filesystem today; S3/SMB are
future backends — see `docs/roadmap/file-api.md`'s Phase 2 outline).

## Declaring a resource

```go
type Note struct {
    Title string
    Body  string
}

noteCodec := codex.Struct[Note](
    codex.RequiredField("title", codex.String(),
        func(n Note) string { return n.Title },
        func(n *Note, v string) { n.Title = v }),
    codex.RequiredField("body", codex.String(),
        func(n Note) string { return n.Body },
        func(n *Note, v string) { n.Body = v }),
)

notes := file.NewResource[Note]("notes/{id}.json", format.JSON(noteCodec),
    file.PathParam{Name: "id"},
)
```

`PathParam` accepts a `codex.Codec[string]` validator via `.WithCodec`,
mirroring `rest.PathParam.WithCodec`/`events.TopicParam.WithCodec` exactly —
a value failing the codec is rejected at path-build time, before any
Backend call:

```go
notes := file.NewResource[Note]("notes/{id}.json", format.JSON(noteCodec),
    file.PathParam{Name: "id"}.WithCodec(codex.String().Refine(validate.Slug)),
)
```

Use `file.NewPathParam[T, V](name, codec, get, set)` instead when the
validated value should ALSO be merged directly into one of `T`'s own
fields (mirrors `rest.NewPathParam`/`events.NewTopicParam`'s merge-field
pattern).

## Attaching a backend and dispatching

```go
client := file.NewClient()
if err := client.Attach(adapterfile.NewBackend()); err != nil {
    log.Fatal(err)
}

ctx := context.Background()
vars := map[string]string{"id": "123"}

// Create
err := client.Create(ctx, notes.WithCreate(file.Create{}), Note{Title: "Hi", Body: "..."}, vars)

// Read
respAny, err := client.Read(ctx, notes.WithRead(file.Read{}), vars)
note := respAny.(Note)

// Delete
existed, err := client.Delete(ctx, notes.WithDelete(file.Delete{}), vars)
```

Every dispatch method's role parameter (`Read`'s, `Create`'s, etc.) accepts
EITHER the raw role builder returned by `.WithRead(...)`/`.WithCreate(...)`/
etc., OR a pre-built `*XxxHandle[T]` (via `.Handle(client, opts...)`) — the
same dual-accept contract `reqreply.Client.Call` uses. Pass a pre-built
Handle when you need Router-group path composition or extra per-call
options; pass the bare builder otherwise.

## Router groups

`file.Router` is a genuine value type (mirrors `events.Router`
method-for-method) composing a path prefix + shared middleware onto any
number of declared roles:

```go
rt := file.NewRouter("tenants/acme").
    Use(authMiddleware).
    Route(notes.WithRead(file.Read{})).
    Route(notes.WithCreate(file.Create{}))
err := rt.Register(client)
```

Or, when you'd rather build Handles directly (the common case — no
separate registration step), pass the Router value to `WithRouter` at an
individual role's own `.Handle` call — the path/middleware composition
happens at exactly ONE construction point:

```go
rt := file.NewRouter("tenants/acme")
readHandle, err := notes.WithRead(file.Read{}).Handle(client, file.WithRouter(rt))
```

Both are equally valid, coexisting workflows (same precedent as
`Client.Attach`'s two Attach styles in `adapters/mqtt5`).

## Declarative Middleware

Every role has a `Bound<Role>Middleware` type + `XxxBoundMW(bm any)` attach
method, genuinely EXECUTED before any Backend call (unlike a plain `.Use()`
attachment, which is declare-time metadata only). `Read`/`Create`/`Patch`/
`List` keep a `*T` domain-value parameter; the other 6 roles (no single
domain value exists for them) drop it:

```go
type grantedScopes struct {
    GrantedScopes map[string][]string
}

bm := file.BoundSecurityReadMiddleware[Note, struct{}, grantedScopes](
    "apiKey", scheme, []string{"read"},
    func(ctx context.Context, v *Note, in struct{}) (grantedScopes, error) {
        return grantedScopes{GrantedScopes: map[string][]string{"apiKey": {"read"}}}, nil
    },
)
reader := notes.WithRead(file.Read{}).ReadBoundMW(bm).WithSecurity(file.SecurityRequirement{"apiKey": {"read"}})
```

An unmet Security requirement is rejected BEFORE the Backend is ever
called (`SecurityRejectedError`) — `GrantedScopes` is extracted from the
bound function's `Out` value via reflection, the same convention
`api/rest`/`api/events` already use.

`Create`/`Patch`'s bound middleware can ALSO declare
`WithMetadataIn`/`WithMetadataOut` to read/contribute file metadata (see
"File metadata" below):

```go
bm := file.NewBoundCreateMiddleware[Note, metaIn, metaOut](decl, fn).
    WithMetadataOut(file.NewMetadataParam("owner", codex.String(),
        func(m metaOut) string { return m.Owner },
        func(m *metaOut, v string) { m.Owner = v }))
```

`Create`'s `In` is populated from the resource's own derived metadata
(available before any Backend call, since `Create`'s full value is
supplied directly); its `Out` contribution is merged OVER the
resource's own metadata before the write. `Patch`'s `Out` works the
same way, but its `In` — like `Read`/`List`'s — stays permanently
zero-valued, since the base value metadata would derive from isn't
known until after a Backend call the Security gate must run before.

## All 10 verbs

| Verb | Client method | Role builder |
|---|---|---|
| Read | `Client.Read` | `Resource.WithRead` |
| Create | `Client.Create` | `Resource.WithCreate` |
| Patch | `Client.Patch` | `file.WithPatch(res, patchCodec, Patch{})` |
| Delete | `Client.Delete` | `Resource.WithDelete` |
| List | `Client.List` | `Resource.WithList` |
| Exists | `Client.Exists` | `Resource.WithExists` |
| Copy | `Client.Copy` | `Resource.WithCopy` |
| Move | `Client.Move` | `Resource.WithMove` |
| Mkdir | `Client.Mkdir` | `Resource.WithMkdir` |
| BatchDelete | `Client.BatchDelete` | `Resource.WithBatchDelete` |

`Copy`/`Move` take TWO `map[string]string` var sets (source, destination) —
the SAME declared template instantiated twice, never a second template.
`BatchDelete` takes a `[]map[string]string` and returns a per-key
`[]BatchDeleteResult` (a batch call can partially fail, mirroring S3's own
`DeleteObjects`).

`List` automatically extracts any declared `MergedPathParam` value from
each discovered entry's own filename (matched against the resource's
template) — no separate declaration needed; the SAME `PathParam`/
`MergedPathParam` you already use for `Read`/`Create` just works for
`List` too.

## Backends

A `Backend` implements `ReadBytes`/`WriteBytes`/`DeleteBytes`/`ListDir`/
`Stat`/`Mkdir`/`BackendName`. `adapters/file.NewBackend()` is the Phase 1
filesystem implementation, built against `os.*` — it also implements the
optional `CopyingBackend` (`io.Copy`) and `MovingBackend` (`os.Rename`)
extensions for a real fast path; it deliberately does NOT implement
`BatchDeletingBackend` (no native filesystem batch-delete syscall — a
backend without it falls back to a generic sequential loop automatically).

## Capabilities

`file.Capability` (`AccessMode`, `NativePOSIXMode`) mirrors
`adapters/mqtt5`'s sealed Capability model — see
[Protocol-Native Capabilities](capabilities.md) for the general mechanism.
A Capability supplied at declare/attach time ALWAYS overrides a conflicting
per-call `WriteOptions`/`DeleteOptions`/`MkdirOptions` field.

## Declared error responses (ErrorPattern)

`file.ErrorPattern`/`ErrorPatterns[B]` is AUTO-INVOKED on every
Backend-originated error (the same `errors.As`-matching, first-rule-wins
convention `api/rest`/`api/events`/`api/reqreply` already use):

```go
pattern := file.NewMappedErrorPattern(func(e file.FileNotFoundError) (NotFoundResponse, error) {
    return NotFoundResponse{Code: "NOT_FOUND"}, nil
})
patterns := file.Append(file.ErrorPatterns[NotFoundResponse]{}, pattern)
reader := notes.WithRead(file.Read{}).ErrorPatterns(patterns)

_, err := client.Read(ctx, reader, vars)
var matched file.ErrorPatternMatchedError
if errors.As(err, &matched) {
    resp := matched.Payload.(NotFoundResponse) // {Code: "NOT_FOUND"}
}
```

## File metadata (the merge-field axis)

`api/file`'s analogue of REST headers / MQTT User Properties — path vars
are already fully covered by `PathParam`, so this is the one OTHER "extra
wire data" axis a `Backend` can carry alongside a resource's own bytes.
`adapters/file.NewBackend()` implements it via a JSON sidecar file
(`<path>.meta.json`, stdlib-only):

```go
notes := file.NewResource[Note]("notes/{id}.json", format.JSON(noteCodec),
    file.PathParam{Name: "id"},
    file.NewMetadataParam("owner", codex.String(),
        func(n Note) string { return n.Owner },
        func(n *Note, v string) { n.Owner = v }),
)
```

`Owner` is decoded from the backend's stored metadata on every `Read`/
`List`, and encoded back into metadata on every `Create`/`Patch` —
entirely automatic once declared.

## Further reading

- `docs/roadmap/file-api.md` — the full design document (Phase 1 design,
  Phase 2 outline, Phase 3/4 plan) and the authoritative source for this
  package's complete API surface.
- [Protocol-Native Capabilities](capabilities.md)
- [Declarative Router Groups](router-groups.md)
- [Codec-Declared Middleware](codec-declared-middleware.md)
- [Metrics Observer](observer.md) — `stats.FileObserver`'s new
  `RecordFileList`/`RecordFileExists`/`RecordFileCopy`/`RecordFileMove`/
  `RecordFileMkdir`/`RecordFileBatchDelete` methods
- `api/file/filetest` — the API-defined conformance-test suite (mirrors
  `api/rest/resttest`/`api/events/eventstest`/`api/reqreply/reqreplytest`)
  any `file.Backend` implementation can run its 7 canonical scenarios
  against; see `adapters/file/conformance_test.go` for a worked example.
