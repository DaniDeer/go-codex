# File API — `api/file`

> **Status:** Design complete — not yet implemented.
> [← Back to Roadmap](index.md)

## Motivation

`ports.File[T]` is go-codex's only declarative file abstraction today, and
it bundles two genuinely different concerns into one type: the
DECLARATION (a path template + codec + per-variable merge fields — the
SAME shape `api/rest.Route`/`api/events.Channel`/`api/reqreply.Route`
already establish for their own boundaries) and the actual I/O (`Read`/
`Write`/`Update`/`Patch`/`Delete` call `os.ReadFile`/`os.WriteFile`/
`os.Remove` DIRECTLY, inline, inside the same methods). Confirmed via
code (`ports/file.go`): there is no backend interface anywhere in this
type — a declared `ports.File[T]` value can ONLY ever be backed by the
local filesystem. `adapters/file`'s own `binding.go` doesn't change this;
it wraps `ports.File`'s methods as `ports.SourceAdapter`/`SinkAdapter`/
`IOAdapter` STREAM bridges, it never becomes an independent transport
implementation the way `adapters/nethttp`/`adapters/mqtt5` are for
`api/rest`/`api/events`.

This doc designs `api/file`: a new `api/*` package applying the SAME
three-layer model (declare → attach adapter → dispatch) every other
go-codex boundary already uses, with file-shaped verbs (Read/Create/
Patch/Delete/List instead of GET/POST/PATCH/DELETE/GET-collection), full
Router-group and declarative-Middleware parity, Observer integration, and
— the ultimate goal — true backend transparency: the SAME declared
`Resource[T]` works unchanged whether the concrete storage is the local
filesystem, S3, or an SMB share, with the backend chosen entirely at
attach time, never baked into the declaration.

## Scope decisions

| In scope (Phase 1) | Out of scope |
|---|---|
| New `api/file` package: `Resource[T]` declaration + 5 role builders (Read/Create/Patch/Delete/List) | A genuinely new HTTP-shaped protocol — `api/file` is NOT a network protocol, it has no wire format of its own; "transport" here means storage BACKEND (filesystem/S3/SMB), not a byte-stream protocol |
| A new `file.Backend` interface — the transparency seam adapters implement | An actual `adapters/s3`/`adapters/smb` implementation — each gets its OWN future roadmap doc once there's a concrete driver, mirroring `amqp-adapter.md`/`tcp-adapter.md`'s existing precedent; this doc designs the SEAM they will plug into, not the adapters themselves |
| Migrating `adapters/file` onto the new `Backend` interface (the ONE Phase 1 backend, proving the abstraction against a REAL adapter, not just a design) | Any change to `adapters/file`'s OWN existing stream-bridge functions (`ScanAdapter`/`WatchAdapter`/`ReadEachAdapter`/etc.) beyond what's needed to also implement `file.Backend` — those keep their existing signatures |
| `ports.File[T]` becomes a thin wrapper around an `api/file`-declared `Resource[T]` Handle + an attached `file.Backend` — mirrors `ports.SourcePort`/`SinkPort`/`IOPort` wrapping adapters today; `ports.FilePattern`/`*Port.PluginFilePattern` is the EXISTING slot this plugs into (confirmed via code: `ports/pattern.go`'s `FilePattern`, `ports/io_port.go`'s `PluginFilePattern(pattern FilePattern) (File[Resp], error)`) | `ports.Dir`/`DirEntry` — stays EXACTLY as-is (untyped, metadata-only directory enumeration); `Resource[T].WithList` is a new, complementary, TYPED+decoded sibling, not a replacement |
| Router groups (`d-0008`'s "one shape, N packages" pattern) — `api/file` becomes the 4th instance | `d-0008`'s core Router mechanism itself — reused unchanged, only a new "Design decisions — resolved for `api/file`" subsection is added to that doc once this ships |
| Declarative per-role middleware (`ReadMW`/`CreateMW`/`PatchMW`/`DeleteMW`/`ListMW` — see "Why verb-specific MW names, not `HandleMW`/`ClientMW`" below) + Security schemes | A NEW security-SCHEME TYPE — reuses `route.SecurityScheme`/`route.SecurityRequirement` unchanged, same as `rest`/`events`/`reqreply` |
| `ErrorPattern` (reusing the established `errors.As` + first-rule-wins + direct/mapped convention) | Inventing a new error-response shape |
| Observer (`stats.FileObserver`, extended with one new method — see below) | A new top-level `stats.Observer` extension unrelated to file lifecycle |

## Toolchain / dependency decisions

No new dependency for Phase 1 — `adapters/file`'s migration uses the SAME
stdlib `os`/`io` calls it (transitively, via `ports.File`) already uses
today, just reached through the new `file.Backend` interface instead of
inline. A future `adapters/s3`/`adapters/smb` adapter would each bring
their OWN dependency decision in their OWN roadmap doc.

## API surface

### Why the role-builder shape, not a single `rest.Route`-style type

`rest.Route[Req, Resp]` works because EVERY REST route is a uniform
Req→Resp pair. File verbs are NOT uniform: Read has no request body;
Create has a body but no distinct response (the written value, or
nothing); Patch needs a SECOND codec for the partial-patch shape (already
established by `ports.File.Patch`/`PatchEncoded[T,P]` today — reused
verbatim); Delete has neither body nor response; List returns a
COLLECTION, not one value. Forcing these into one generic `Route[Req,
Resp]` shape would need `Req`/`Resp` to be `struct{}` for half the verbs
— exactly the asymmetry `events.Channel[T]`'s `WithSubscribe`/
`WithPublish` role-builder pattern already solves for pub/sub's own
Publish/Subscribe asymmetry. `api/file` mirrors THAT pattern, not
`rest.Route`'s.

```go
// api/file — Resource[T] declares the file shape once: a path template
// (mirrors api/rest.Path/api/events.Topic exactly) + codec + optional
// per-variable merge fields. Infallible — validation runs at role-attach
// time, mirroring every other NewX constructor in this codebase.
func NewResource[T any](pathTemplate string, codec codex.Codec[T], opts ...ResourceOpt) Resource[T]

// ResourceOpt — PathParam/MergedPathParam (mirrors rest.PathParam/
// events.TopicParam's own ChannelOpt/RouteOpt interface pattern exactly).
type ResourceOpt interface{ applyResource(*resourceBuilder) }

type PathParam struct {
    Name        string
    Description string
    Codec       *codex.Codec[string] // optional per-variable validation
}
func (p PathParam) applyResource(*resourceBuilder) {}

// MergedPathParam[T] is PathParam's merge-capable sibling — extracted
// path variables are DECODED DIRECTLY into one of T's own fields, no
// manual assignment needed. Mirrors events.NewTopicParam/
// rest.NewPathParam's existing merge-field pattern.
func NewPathParam[T, V any](name string, codec codex.Codec[V],
    get func(T) V, set func(*T, V)) MergedPathParam[T]

// Resource[T] itself — no I/O methods. Role-attach methods only.
type Resource[T any] struct{ /* unexported */ }

// WithRead returns a Reader[T] for this resource's read role, carrying
// Read as its operation metadata — mirrors events.Channel.WithSubscribe
// returning Subscriber[T] exactly.
func (r Resource[T]) WithRead(op Read) Reader[T]

// Read is Read-role operation metadata (mirrors events.Subscribe/
// events.Publish's own metadata-struct shape) — currently just
// documentation fields; Security is attached separately, per the
// "declare role, then .Use()" convention every other pattern uses.
type Read struct {
    Summary     string
    Description string
}

func (r Resource[T]) WithCreate(op Create) Creator[T]
type Create struct{ Summary, Description string }

// WithPatch needs its OWN type parameter P (the partial-patch shape) —
// Go requires type parameters on free functions, not methods, so this
// is a free function taking the Resource, mirroring NewRoute's own
// "free function, not a method, because of generics" precedent exactly.
func WithPatch[T, P any](r Resource[T], patchCodec codex.Codec[P], op Patch) Patcher[T, P]
type Patch struct{ Summary, Description string }

func (r Resource[T]) WithDelete(op Delete) Deleter[T]
type Delete struct{ Summary, Description string }

// WithList operates over the resource's PARENT directory (derived from
// pathTemplate the same way ports.Dir derives its own listing root) —
// returns the FULL decoded []T for every matching file, the typed+
// decoded sibling of the existing untyped ports.Dir/DirEntry (which
// stays unchanged for metadata-only enumeration).
func (r Resource[T]) WithList(op List) Lister[T]
type List struct{ Summary, Description string }
```

### Per-role attachment surface — mirrors `Subscriber[T]`/`Publisher[T]` exactly

Each role type (`Reader[T]`, `Creator[T]`, `Patcher[T,P]`, `Deleter[T]`,
`Lister[T]`) is independent — a Security requirement on `Deleter` does
NOT imply one on `Reader`, mirroring how a `Publish.Security` requirement
never implies one on the SAME channel's `Subscribe` side today:

```go
// Reader[T] — every role type follows this SAME shape; shown once,
// the other 4 are structurally identical (Patcher[T,P] additionally
// carries P throughout).
type Reader[T any] struct{ /* unexported */ }

// Use attaches general-purpose or security-shaped middleware — mirrors
// Subscriber.Use/Publisher.Use exactly (AttachedMiddleware +
// eventsMiddlewareContributor widening).
func (r Reader[T]) Use(mws ...route.RouteMiddleware) Reader[T]

// ReadMW is the LEGACY-shaped general-purpose/security implementation
// attachment point — mirrors SubscribeMW/PublishMW's naming convention
// (verb-specific, not HandleMW/ClientMW) because file has no separate
// "server handler vs. client caller" role split the way REST/reqreply
// do: there is no user-written business-logic handler for a file read,
// only the backend adapter itself. One MW name per verb, not two.
func (r Reader[T]) ReadMW(mw route.RouteMiddleware, fn any) Reader[T]

// ReadBoundMW attaches a codec-backed, channel-bound middleware.Middleware
// (mirrors SubscribeBoundMW/PublishBoundMW) — the PREFERRED mechanism per
// d-0003's codec-declared-middleware convention.
func (r Reader[T]) ReadBoundMW(bm BoundReadMiddleware[T, any, any]) Reader[T]

// WithOptions carries adapter-specific, type-erased options (mirrors
// Subscriber.WithOptions/Publisher.WithOptions) — e.g. a future S3
// adapter's own per-call options (storage class, SSE settings).
func (r Reader[T]) WithOptions(opts any) Reader[T]

// Handle builds the final, immutable ReadHandle[T] — mirrors
// Subscriber.Handle/Publisher.Handle's "builder → Handle" transition
// exactly; client optionally registers against a spec/dedup registry.
func (r Reader[T]) Handle(client *Client) (*ReadHandle[T], error)
```

### `file.Backend` — the transparency seam

```go
// Backend is the ONE interface a storage backend must implement to make
// EVERY declared Resource[T] work against it, regardless of verb —
// mirrors adapters/internal/restdispatch's shared dispatch-core pattern,
// one layer earlier: the SHARED mechanics (path templating, codec
// encode/decode, merge-field application, Observer calls, ErrorPattern
// dispatch) live ONCE in api/file's own dispatch core; Backend carries
// ONLY the byte-level, backend-specific operations that core calls into.
type Backend interface {
    // ReadBytes returns path's raw content, or a Backend-specific
    // not-found signal the dispatch core recognizes via errors.Is/As
    // (e.g. os.ErrNotExist for adapters/file) and translates into the
    // SAME FileNotFoundError every backend reports identically.
    ReadBytes(ctx context.Context, path string) ([]byte, error)

    // WriteBytes writes path's content. opts carries the backend-
    // agnostic subset of today's FileOptions (CreateDirs/DirPerm/Perm/
    // Strict/DryRun) that EVERY backend can plausibly honor — a backend
    // that genuinely cannot honor one (e.g. S3 has no directory-
    // permission concept) documents it as a no-op, never an error.
    WriteBytes(ctx context.Context, path string, data []byte, opts WriteOptions) (createdDirs []string, err error)

    // DeleteBytes removes path. existed=false + err=nil is the
    // idempotent "already absent" success case (mirrors ports.File.
    // Delete's existing contract exactly).
    DeleteBytes(ctx context.Context, path string, opts DeleteOptions) (existed bool, err error)

    // ListDir enumerates path's immediate children (non-recursive —
    // Resource[T].WithList composes recursion the SAME way ports.Dir.List
    // already does, via repeated ListDir calls, not a Backend
    // responsibility).
    ListDir(ctx context.Context, path string) ([]Entry, error)

    // BackendName returns a descriptor for observability — mirrors
    // every ports.SourceAdapter/SinkAdapter/IOAdapter's own AdapterName()
    // convention exactly.
    BackendName() string
}

// Entry mirrors ports.DirEntry's existing shape (Name/IsDir/Size/ModTime)
// — kept as a SEPARATE type (not reusing ports.DirEntry directly) so
// api/file has zero import-time dependency on the ports package,
// consistent with api/* never importing ports (the dependency runs the
// OTHER direction: ports imports api/*, never the reverse).
type Entry struct {
    Name    string
    IsDir   bool
    Size    int64
    ModTime time.Time
}
```

`adapters/file` is migrated to implement `file.Backend` against `os.*` —
its EXISTING stream-bridge functions (`ScanAdapter`/`DrainWriteAdapter`/
etc.) are unaffected; they keep calling `ports.File`'s methods exactly as
today, since `ports.File` itself becomes the thing that now delegates to
`file.Backend` internally rather than calling `os.*` directly — a
backend-swap below `ports.File`'s existing public surface, not a breaking
change to `ports.File`'s own call signatures.

## Structured errors (all implement `slog.LogValuer`)

`ports/file.go`'s EXISTING error types (`FileNotFoundError`,
`FileReadError`, `FileWriteError`, `FileDecodeError`, `FileEncodeError`,
`FileAlreadyExistsError`, `FileDeleteError`, `FilePathParamError`,
`FilePathMismatchError`, `MissingFilePathVarError`,
`FileWildcardBuildError`, `FilePatchNotSupportedError`) are RELOCATED to
`api/file` (the new canonical home — this boundary's own vocabulary file,
mirroring `rest`/`events`/`reqreply`'s own `errors.go` convention) with a
`type XError = file.XError` alias left in `ports` for any existing
`errors.As` caller — per this skill's own "internal-package errors must
be aliased publicly" rule, applied here in the OPPOSITE direction (a
`ports`-owned type moving OUT to its new `api/*` home, `ports` keeping the
alias for backward `errors.As` compatibility, since `ports` is a public
package real callers already type-assert against).

One NEW error type, `BackendUnsupportedOptionError{Backend, Option
string}` — returned when a `WriteOptions`/`DeleteOptions` field a backend
genuinely cannot honor is set to a non-default value AND the backend
author chose to fail loudly rather than silently no-op (an explicit,
per-backend, per-option choice — not a core-dispatch decision).

## Observer integration

Reuses `stats.FileObserver` (`RecordFileRead`/`RecordFileWrite`/
`RecordFileDelete`) for Read/Create/Patch/Delete — identical call sites
to `ports.File`'s existing ones, just now fired from `api/file`'s
dispatch core instead of inline. ONE new method needed for the NEW List
verb:

```go
type FileObserver interface {
    RecordFileRead(path string, success bool, duration time.Duration)
    RecordFileWrite(path string, success bool, duration time.Duration)
    RecordFileDelete(path string, success bool, duration time.Duration)
    // RecordFileList is called once per WithList call (not once per
    // entry) — success is false only on a backend-level listing
    // failure, never because the directory was merely empty (an empty
    // result is a successful, zero-length list).
    RecordFileList(path string, success bool, count int, duration time.Duration)
}
```

Added DIRECTLY to the existing `FileObserver` interface (an intentional,
acknowledged breaking change — mirrors `RecordFileDelete`'s own addition
precedent exactly, documented in `stats/observer.go`'s own doc comment:
"go-codex has one consumer," so this repo accepts the churn rather than
fragmenting into yet another observer extension for one new method).
`TraceObserver`/`SecurityObserver` integration mirrors `rest`/`events`/
`reqreply`'s identical type-assertion-guard pattern — no new observer
extension needed for either.

## Router/Middleware integration

- **Router groups**: `api/file` adopts `d-0008`'s existing `Router`
  value type UNCHANGED — a `Router.Group("files/{tenant}")` composes a
  path PREFIX onto every `Resource[T]` registered under it, mirroring
  `rest`/`events`/`reqreply`'s identical prefix-composition semantics.
  Needs its own "Design decisions — resolved for `api/file`" subsection
  appended to `d-0008` once implemented (the SAME per-pattern-closed-
  decisions structure that doc already uses 3 times).
- **Declarative Middleware**: reuses `middleware.Middleware[In,Out]`/
  `BoundMiddleware`-style codec-backed attachment (`d-0003`/`d-0007`),
  realized here as `ReadBoundMW`/`CreateBoundMW`/`PatchBoundMW`/
  `DeleteBoundMW`/`ListBoundMW` — one per role, mirroring
  `SubscribeBoundMW`/`PublishBoundMW`'s existing per-role split. The
  underlying MECHANICS (dispatch, `Satisfies`/`CheckCoverage`,
  `GrantedScopes`) are the SAME shared `internal/middleware` machinery
  every other pattern already uses via its OWN public wrapper — `api/file`
  gets its own thin wrapper, per `d-0009`'s established convention, not a
  new mechanism.
- **Security schemes**: reuses `route.SecurityScheme`/
  `route.SecurityRequirement` unchanged. A `Deleter[T]` declaring
  `Security: []route.SecurityRequirement{...}` enforces it via the SAME
  `CheckScopes`/`CheckCoverage` dispatch every other pattern's role-level
  Security already uses.

## Unit test plan

| Test | Verifies |
|---|---|
| `TestResource_WithRead_BuildsReader` | `Resource.WithRead` returns a `Reader[T]` carrying the declared path/codec |
| `TestReader_Handle_Read_HappyPath` | A built `ReadHandle` reads+decodes against a fake `Backend`, returns `T` |
| `TestReader_Handle_Read_BackendNotFound_ReturnsFileNotFoundError` | A `Backend` reporting not-found translates to `FileNotFoundError`, `errors.As` reaches it |
| `TestCreator_Handle_Create_EncodesAndWrites` | `Creator` encodes `T` via the declared codec, calls `Backend.WriteBytes` with the built path |
| `TestPatcher_Handle_Patch_AppliesPartialUpdate` | `Patcher[T,P]` round-trips a partial `P` patch onto an existing `T`, mirrors `ports.File.PatchEncoded`'s existing proof |
| `TestDeleter_Handle_Delete_IdempotentAbsent` | Deleting an already-absent path returns `existed=false, err=nil` |
| `TestLister_Handle_List_ReturnsDecodedEntries` | `Lister[T]` decodes every matching file in a directory into `[]T` |
| `TestFileObserver_RecordFileList_CalledOnce` | `RecordFileList` fires exactly once per `List` call, with the correct count |
| `TestReadMW_SecurityRejection_ReturnsSecurityError` | A `Reader` with an unmet Security requirement fails BEFORE any `Backend` call |
| `TestErrorPattern_MatchedOnBackendError` | A declared `file.ErrorPattern` maps a `Backend`-specific error to its typed response |
| `TestRouterGroup_PrefixesResourcePath` | A `Resource[T]` registered under a `Router.Group` gets the group's path prefix applied |
| `TestPortsFile_StillWorksViaPluginFilePattern` | `ports.File[T]` built via `PluginFilePattern` against a migrated `adapters/file` Backend behaves IDENTICALLY to today's direct-`os.*` behavior — the critical "no silent behavior change" regression proof |
| `ExampleResource_read` / `ExampleResource_create` | pkg.go.dev-facing worked examples |

## Files to create

| File | Responsibility |
|---|---|
| `api/file/builder.go` | `Resource[T]`, `PathParam`/`MergedPathParam`, `Reader`/`Creator`/`Patcher`/`Deleter`/`Lister`, `Read`/`Create`/`Patch`/`Delete`/`List` metadata structs, `Client`/`Handle` registry (mirrors `events.Client`/`ChannelHandle`) |
| `api/file/backend.go` | `Backend` interface, `Entry`, `WriteOptions`/`DeleteOptions` |
| `api/file/dispatch.go` | The shared dispatch core every `*Handle[T]`'s Read/Create/Patch/Delete/List call delegates to — path build/validate, codec encode/decode, merge-field application, Observer calls, ErrorPattern dispatch |
| `api/file/errors.go` | Relocated error types (from `ports/file.go`) + `BackendUnsupportedOptionError` |
| `api/file/error_pattern.go` | `ErrorPattern[E,B]` — mirrors `rest.ErrorPattern`/`events.ErrorPattern`/`reqreply.ErrorPattern`'s identical shape |
| `api/file/doc.go` | Package godoc |
| `adapters/file/backend.go` | `os.*`-backed `file.Backend` implementation (new file; existing `binding.go`/`adapter.go` untouched) |
| `ports/file.go` | `File[T]`/`Dir` methods rewritten to delegate to an attached `api/file` Handle + `Backend`, instead of calling `os.*` inline; public call signatures UNCHANGED |
| `ports/pattern.go` | `FilePattern`/`PluginFilePattern` updated to build/bind against the new `api/file.Resource` Handle |

## Out of scope (Phase 2+)

- `adapters/s3`, `adapters/smb` — each gets its OWN future roadmap doc
  once there's a concrete driver (mirrors `amqp-adapter.md`/
  `tcp-adapter.md`'s existing precedent); this doc designs the `Backend`
  seam they will implement, nothing more.
- Streaming/chunked reads or writes for very large files — Phase 1's
  `Backend.ReadBytes`/`WriteBytes` are whole-file, `[]byte`-at-a-time,
  matching `ports.File`'s own existing all-at-once contract; a future
  streaming variant (`io.Reader`/`io.Writer`-based) is a separate,
  larger design question not resolved here.
- Conditional writes (ETag/If-Match-style optimistic concurrency) — no
  backend-agnostic concept decided; flagged as a candidate `WriteOptions`
  field for a future round once a concrete backend (S3 has native ETag
  support) motivates it.
- Recursive `WithList` (today's `ports.Dir.List` already supports a
  recursive mode) — Phase 1's `Lister[T]` is single-level only; recursive
  listing is a straightforward additive follow-on once Phase 1 ships,
  deliberately deferred to keep this round's scope bounded.

## Open design decisions

None remaining — all decisions below were resolved via `ask_user` before
drafting this doc:

- Package name: `api/file` (not `api/storage`).
- `ports.File[T]`'s fate: thin wrapper over the new `Backend` seam, not
  replaced or left untouched.
- Verb scope: Read, Create, Patch, Delete, List, Update (Update stays a
  CLIENT-SIDE-ONLY Read+transform+Write composition, no new wire verb).
- Backend scope: design the full seam now; implement only
  `adapters/file` (filesystem) in Phase 1.
- Router/Middleware depth: full parity designed for Phase 1 (not
  deferred to Phase 2).
