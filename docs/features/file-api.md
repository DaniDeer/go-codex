# File API — `api/file`

> **Status:** Phase 1 implemented with full Router + Declarative Middleware
> parity (matches `api/rest`/`api/events`), including the migration of
> `ports.File`/`ports.Dir` to delegate onto this stack internally (their own
> public call signatures are unchanged). Phase 2's first four items are
> implemented: Recursive `WithList`, Conditional writes, Streaming/chunked
> I/O, `adapters/s3`, and `adapters/smb` (a THIRD real Backend, for SMB2/3
> shares) — see `docs/roadmap/file-api.md`.

`api/file` is a transport-agnostic API layer for file-shaped resources,
mirroring `api/rest`/`api/events`/`api/reqreply`'s declare → attach → dispatch
model. A declared `Resource[T]` describes WHAT a file looks like (its path
template, codec, and path parameters); a `Client` with an attached `Backend`
describes WHERE it actually lives — the local filesystem
(`adapters/file.NewBackend()`), S3/any S3-compatible store
(`adapters/s3.NewBackend(...)`), or an SMB2/3 share
(`adapters/smb.NewBackend(...)`) — the SAME declared `Resource[T]` works
UNCHANGED against any of them; see `docs/roadmap/file-api.md`'s Phase 2
section for each Backend's own capability notes.

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

## All 12 verbs

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
| StreamRead | `Client.OpenRead` | `Resource.WithStreamRead` |
| StreamWrite | `Client.OpenWrite` | `Resource.WithStreamWrite` |

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

`Client.List`'s boxed return value is `[]file.ListResult[T]` — one
result PER DISCOVERED ENTRY, success or failure, never a plain `[]T`:

```go
gotAny, err := client.List(ctx, notes.WithList(file.List{}), nil)
if err != nil {
    // A WALK-LEVEL failure (the parent directory itself can't be
    // read) — there's no entry to attach this to.
}
for _, r := range gotAny.([]file.ListResult[Note]) {
    if r.Err != nil {
        // THIS entry's own failure (decode error, merge-field
        // mismatch, or a declared BoundListMiddleware's Security
        // rejection) — siblings still succeed independently.
        continue
    }
    fmt.Println(r.Value)
}
```

`List` also accepts an optional trailing `file.ListOptions{Recursive:
true}` to descend into every subdirectory under the resource's parent
directory, not just its immediate children:

```go
gotAny, err := client.List(ctx, notes.WithList(file.List{}), nil,
    file.ListOptions{Recursive: true})
```

`Recursive` walks via the Backend's optional `WalkingBackend` extension
when available (falling back to an equivalent repeated-`ListDir`
composition otherwise), pruning any entry nested DEEPER than the
resource's own declared path template describes — an unexpected extra
directory level is silently excluded, never decoded.

## Backends

A `Backend` implements `ReadBytes`/`WriteBytes`/`DeleteBytes`/`ListDir`/
`Stat`/`Mkdir`/`BackendName`. `adapters/file.NewBackend()` is the Phase 1
filesystem implementation, built against `os.*` — it also implements the
optional `CopyingBackend` (`io.Copy`) and `MovingBackend` (`os.Rename`)
extensions for a real fast path; it deliberately does NOT implement
`BatchDeletingBackend` (no native filesystem batch-delete syscall — a
backend without it falls back to a generic sequential loop automatically).
It also implements `ConditionalWriteBackend` — see "Conditional writes"
below.

`adapters/s3.NewBackend(client, bucket, ...)` is the second real Backend
(Phase 2 item 4, implemented): wraps `github.com/aws/aws-sdk-go-v2/service/s3`
behind a narrow client interface — the caller constructs/authenticates the
`*s3.Client` itself, this package never owns credential loading. It proves
the Backend abstraction's portability concretely: the EXACT SAME declared
`Resource[T]` works unchanged against it. S3 also delivers genuine
capability UPGRADES over the filesystem backend — a real, strong,
server-computed `Entry.ETag` (no body read, via `HeadObject`) and a
TRULY NATIVE, atomic `ConditionalWriteBackend` (S3's own server-side
`If-Match`/`If-None-Match`, no lock-file emulation). It implements
`CopyingBackend`/`BatchDeletingBackend`/`MetadataBackend`/
`StreamWriteBackend`/`StreamReadBackend`/`RangeReadingBackend`/
`WalkingBackend`/`DirDeletingBackend`, but deliberately NOT
`MovingBackend` (S3 has no atomic rename for standard buckets — the
generic Copy+Delete fallback is the correct behavior). It works against
any S3-compatible endpoint (MinIO, Ceph, Cloudflare R2, GCS interop
mode, …), not just AWS itself.

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

## Conditional writes (ETag/If-Match)

`Entry.ETag` identifies a resource's current content — strong (a real
content hash) when `Read`/`List` already have the bytes in hand, weak
(an HTTP-style `W/"..."` approximation, derived from mtime+size) when
only metadata is available (`Exists`/`Stat`). Retrieve the current
ETag via `Client.Exists`, then supply it as `WriteOptions.IfMatch` on a
later `Create`/`Patch` call to make the write CONDITIONAL — it only
commits if the resource's content hasn't changed since:

```go
entry, exists, err := client.Exists(ctx, notes.WithExists(file.Exists{}), vars)
// ... entry.ETag is the note's current content hash ...

ifMatch := entry.ETag
err = client.Patch(ctx, patcher, patch, vars, file.WriteOptions{IfMatch: &ifMatch})
var mismatch file.ConditionalWriteMismatchError
if errors.As(err, &mismatch) {
    // someone else changed the note first — reread and retry
}
```

`adapters/file.NewBackend()` implements this via a lock file
(`<path>.lock`) that serializes the compare-and-commit step — genuinely
atomic among conditional writers, with a bounded retry (and an internal
default timeout, so it never blocks forever) if the lock is already
held. `Strict:true` always wins when both `Strict` and `IfMatch` are
set on the same `Create` call. `ports.File`'s own `FileOptions.IfMatch`
+ a new `File.Stat` method mirror this exactly.

## Streaming/chunked reads and writes

`Read`/`Create` decode/encode a full value `T` via the resource's codec
— correct for small-to-medium records, but it means the whole content
sits in memory at once. Two mechanisms avoid that:

**Mechanism 1 — automatic format-level write streaming.** If a
resource's `format.Format[T]` was built via `format.NewStreamed` (i.e.
`format.IsStreamable()` is true) AND the attached Backend implements the
optional `StreamWriteBackend` extension, `Client.Create`/`Patch` stream
the encoded output directly into the Backend's own writer instead of
buffering a `[]byte` first — zero declare-time changes needed, it just
happens automatically. A Backend without `StreamWriteBackend` still
works correctly (buffers via `MarshalTo` into memory first), just
without the memory benefit.

**Mechanism 2 — raw, codec-bypassing byte streams.** `StreamRead`/
`StreamWrite` are 2 new roles whose actual BYTES never touch the
resource's codec at all (only path templating/Security/Capability still
apply) — mirrors S3's own `GetObject`/`PutObject` body-streaming:

```go
sr := notes.WithStreamRead(file.StreamRead{})
sw := notes.WithStreamWrite(file.StreamWrite{})

w, err := client.OpenWrite(ctx, sw, vars)
// ... w.Write(...) any number of times ...
err = w.Close() // commits atomically — a reader never sees a partial file

r, err := client.OpenRead(ctx, sr, vars)
defer r.Close()
data, err := io.ReadAll(r)
```

`Client.OpenRead` also accepts an optional trailing
`file.StreamReadOptions{Offset, Length}` for an HTTP-Range-style partial
read (read-only — there's no cross-backend concept of a partial-offset
WRITE):

```go
r, err := client.OpenRead(ctx, sr, vars, file.StreamReadOptions{Offset: 100, Length: 50})
```

A ranged read is served by whichever of 3 Backend tiers is available,
fastest first: a native `RangeReadingBackend` (e.g. a future
`adapters/s3`'s `GetObject` `Range` header) → a `StreamReadBackend`'s
whole-file stream with a discard-then-limit wrapper → `ReadBytes` sliced
in memory (works on every Backend unchanged). `stats.FileObserver
.RecordFileRangeRead` reports which tier actually served a given
request. Both `RecordFileRead`/`RecordFileWrite` fire when the returned
stream is `Close`d, not when it's opened — `Close`'s own return value is
what determines success.

`ports.File[T]` has its own `OpenRead`/`OpenWrite` methods mirroring
`Client.OpenRead`/`OpenWrite` exactly (with a `ports.OpenReadOptions`
type for the byte-range parameter).

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
