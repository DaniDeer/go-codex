# File API — `api/file`

> **Status:** Phase 1 partially implemented. The `api/file` package and its
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

## Further reading

- `docs/roadmap/file-api.md` — the full design document (Phase 1 design,
  Phase 2 outline, Phase 3/4 plan) and the authoritative source for this
  package's complete API surface.
- [Protocol-Native Capabilities](capabilities.md)
- [Metrics Observer](observer.md) — `stats.FileObserver`'s new
  `RecordFileList`/`RecordFileExists`/`RecordFileCopy`/`RecordFileMove`/
  `RecordFileMkdir`/`RecordFileBatchDelete` methods
