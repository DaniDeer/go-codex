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
Patch/Delete/List/Update/Exists/Copy/Move/Mkdir instead of GET/POST/
PATCH/DELETE/GET-collection), full Router-group and declarative-
Middleware parity, Observer integration, a cross-backend permission
capability, and — the ultimate goal — true backend transparency: the
SAME declared `Resource[T]` works unchanged whether the concrete storage
is the local filesystem, S3, or an SMB share, with the backend chosen
entirely at attach time, never baked into the declaration.

**Refinement round (this revision)**: expanded the verb set by directly
surveying what Amazon S3's own API provides (`PutObject`/`GetObject`/
`DeleteObject`/`ListObjectsV2`/`HeadObject`/`CopyObject`), added 4 verbs
this surfaced that `ports.File` has no equivalent for today (`Exists`/
`Copy`/`Move`/`Mkdir`), promoted directory creation from an implicit
`Write` side effect to its own first-class operation, and designed a
cross-backend permission capability (`AccessMode`) after confirming via
direct research that POSIX mode bits and Windows/S3 permission models are
NOT meaningfully unifiable beyond a binary read-only/writable distinction
— the SAME simplification Go's own `os.Chmod` already makes on Windows.

## Scope decisions

| In scope (Phase 1) | Out of scope |
|---|---|
| New `api/file` package: `Resource[T]` declaration + 9 role builders — Read, Create, Patch, Delete, List, **Exists** (NEW — mirrors S3's `HeadObject`: metadata-only, no body download), **Copy** (NEW — mirrors S3's `CopyObject`: duplicate without a client-side round-trip), **Move**/Rename (NEW — filesystem's atomic `os.Rename`; S3 has no native equivalent, composed from Copy+Delete there), **Mkdir** (NEW — promoted from `WriteOptions.CreateDirs`'s implicit side effect to its own first-class operation) | A genuinely new HTTP-shaped protocol — `api/file` is NOT a network protocol, it has no wire format of its own; "transport" here means storage BACKEND (filesystem/S3/SMB), not a byte-stream protocol |
| `Update` stays a CLIENT-SIDE-ONLY Read+transform+Write composition (unchanged from the original round) | S3 object versioning, tagging, or fine-grained ACL/grantee modeling — surfaced during the S3 survey but NOT requested; would be inventing API without a driver, flagged only as a possible future extension if a real use case appears |
| A new `file.Backend` interface — the transparency seam adapters implement, now including `Mkdir`/`Stat`, plus 2 OPTIONAL extension interfaces (`CopyingBackend`/`MovingBackend`) a backend may implement for a native fast path | An actual `adapters/s3`/`adapters/smb` implementation — each gets its OWN future roadmap doc once there's a concrete driver, mirroring `amqp-adapter.md`/`tcp-adapter.md`'s existing precedent; this doc designs the SEAM they will plug into, not the adapters themselves |
| Migrating `adapters/file` onto the new `Backend` interface (the ONE Phase 1 backend, proving the abstraction against a REAL adapter, not just a design) — including implementing `CopyingBackend`/`MovingBackend` via `io.Copy`/`os.Rename` to prove the optional-fast-path mechanism, not just the required interface | Any change to `adapters/file`'s OWN existing stream-bridge functions (`ScanAdapter`/`WatchAdapter`/`ReadEachAdapter`/etc.) beyond what's needed to also implement `file.Backend` — those keep their existing signatures |
| A new `file.Capability` sealed interface (mirrors `d-0006`'s established `mqtt5.Capability`/`Apply`/`CheckCapabilityCoverage` mechanism exactly): `AccessMode` (ReadOnly/ReadWrite — the ONE permission concept genuinely portable across filesystem/S3/SMB) + `NativePOSIXMode` (escape hatch, filesystem/SMB-only) | A full POSIX-mode-bits-to-Windows-ACL or POSIX-to-S3-ACL TRANSLATOR — researched directly and confirmed NOT meaningfully achievable: Windows collapses ALL of `os.FileMode` to one bit via Go's own `os.Chmod` (confirmed via Go's stdlib docs), and S3's ACL/bucket-policy model is grantee+action-based, not bit-based at all — there is no lossless translation to invent, only an honest, portable common denominator |
| `ports.File[T]` becomes a thin wrapper around an `api/file`-declared `Resource[T]` Handle + an attached `file.Backend` — mirrors `ports.SourcePort`/`SinkPort`/`IOPort` wrapping adapters today; `ports.FilePattern`/`*Port.PluginFilePattern` is the EXISTING slot this plugs into (confirmed via code: `ports/pattern.go`'s `FilePattern`, `ports/io_port.go`'s `PluginFilePattern(pattern FilePattern) (File[Resp], error)`) | `ports.Dir`/`DirEntry` — stays EXACTLY as-is (untyped, metadata-only directory enumeration); `Resource[T].WithList` is a new, complementary, TYPED+decoded sibling, not a replacement |
| **`file.Client` (NEW this revision)** — the attach-time `Backend` wiring (`Attach`, exclusive) AND call-time dispatch (`Read`/`Create`/`Patch`/`Delete`/`List`/`Exists`/`Copy`/`Move`/`Mkdir`) surface, mirroring `events.Client`/`reqreply.Client`'s established `Attach`+reflective-`any`-dispatch shape exactly — closes the gap the first 2 drafting rounds left open (neither specified HOW a Backend gets attached or what a built role Handle exposes to actually run) | A generic, type-safe `Client.Read[T]`-style free-function wrapper around the `any`-typed dispatch methods — deliberately NOT designed, since `reqreply.Client.Call`'s own established convention is a plain caller-side type assertion (`respAny.(T)`), not a generic wrapper; inventing one here would diverge from the precedent this doc otherwise mirrors |
| Router groups (`d-0008`'s "one shape, N packages" pattern) — `api/file` becomes the 4th instance | `d-0008`'s core Router mechanism itself — reused unchanged, only a new "Design decisions — resolved for `api/file`" subsection is added to that doc once this ships |
| Declarative per-role middleware (`ReadMW`/`CreateMW`/`PatchMW`/`DeleteMW`/`ListMW`/`ExistsMW`/`CopyMW`/`MoveMW`/`MkdirMW` — see "Why verb-specific MW names, not `HandleMW`/`ClientMW`" below) + Security schemes | A NEW security-SCHEME TYPE — reuses `route.SecurityScheme`/`route.SecurityRequirement` unchanged, same as `rest`/`events`/`reqreply` |
| `ErrorPattern` (reusing the established `errors.As` + first-rule-wins + direct/mapped convention) | Inventing a new error-response shape |
| Observer (`stats.FileObserver`, extended with 5 new methods — see below) | A new top-level `stats.Observer` extension unrelated to file lifecycle |

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

// WithExists mirrors S3's HeadObject: proves presence/absence and
// returns metadata (size, modtime) WITHOUT downloading/decoding the
// body — the one verb ports.File has NO equivalent for today. Returns
// (Entry, bool, error): bool is false (zero Entry, nil error) for the
// idempotent "absent" case, mirroring Delete's own existed-bool
// convention exactly — absence is not itself an error here.
func (r Resource[T]) WithExists(op Exists) Exister[T]
type Exists struct{ Summary, Description string }

// WithCopy mirrors S3's CopyObject: duplicates the resource at a new
// set of path vars without round-tripping bytes through the caller when
// the attached Backend implements the optional CopyingBackend fast path
// (falls back to Read+Write generically otherwise — see "Backend
// semantic differences" below).
func (r Resource[T]) WithCopy(op Copy) Copier[T]
type Copy struct{ Summary, Description string }

// WithMove mirrors filesystem's atomic os.Rename — S3 has NO native
// move (composed from Copy+Delete there instead, 2 non-atomic calls;
// see "Backend semantic differences"). Uses the optional MovingBackend
// fast path when the attached Backend implements it.
func (r Resource[T]) WithMove(op Move) Mover[T]
type Move struct{ Summary, Description string }

// WithMkdir is the NEW first-class directory-creation operation —
// PROMOTED from WriteOptions.CreateDirs's implicit side effect (which
// keeps working unchanged, now implemented IN TERMS OF this same
// Backend.Mkdir call, not a separate code path). Operates over the
// resource's parent directory, mirroring WithList's own derivation.
func (r Resource[T]) WithMkdir(op Mkdir) MkdirCreator[T]
type Mkdir struct{ Summary, Description string }
```

### Per-role attachment surface — mirrors `Subscriber[T]`/`Publisher[T]` exactly

Each role type (`Reader[T]`, `Creator[T]`, `Patcher[T,P]`, `Deleter[T]`,
`Lister[T]`, `Exister[T]`, `Copier[T]`, `Mover[T]`, `MkdirCreator[T]`) is
independent — a Security requirement on `Deleter` does NOT imply one on
`Reader`, mirroring how a `Publish.Security` requirement never implies
one on the SAME channel's `Subscribe` side today. `Mover[T]`'s own
Security is checked ONCE, covering both the logical read-the-source and
write-the-destination halves — it does NOT separately re-check
`Deleter`'s or `Creator`'s own declared Security, since `Move` is its OWN
declared role with its OWN Security requirement, not a composition of
two OTHER roles' declarations (a Resource is not required to declare
`Delete`/`Create` at all to declare `Move`):

```go
// Reader[T] — every role type follows this SAME shape; shown once,
// the other 8 are structurally identical (Patcher[T,P] additionally
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

### `file.Client` — attach-time `Backend` wiring + call-time dispatch

**Added this revision** — closes a gap the first 2 drafting rounds left
open: neither round specified HOW a `Backend` actually gets attached, or
what a built `*ReadHandle[T]` (etc.) exposes to perform the operation.
Researched the ACTUAL existing pattern first rather than inventing one:
`events.ChannelHandle[T]`/`reqreply`'s built handles have NO call-time
method on themselves at all — the real call-time surface lives on
`*events.Client`/`*reqreply.Client` (the SAME entity `Attach` is called
on), confirmed directly from `api/events/builder.go`'s
`Client.Attach(t Transport) error` + `Client.Publish(ctx, pub any, msg
any, opts ...ClientPublishOptions) error` and
`api/reqreply/client.go`'s `Client.Call(ctx, route any, req any, opts
...) (any, error)` — further confirmed via
`examples/reqreply-api/demo_basic_call_and_serve.go` that the REAL
calling convention is `respAny, err := client.Call(ctx, route, req)`
then a plain `resp := respAny.(ComputeResp)` by the caller — Go
generics cannot add a type parameter to a method, so `Client.Call`/
`Client.Publish` stay `any`-typed by necessity, and the caller supplies
the type assertion. `api/file` mirrors this EXACT shape, not a new one:

```go
// Client mirrors events.Client/reqreply.Client's own role exactly: the
// ONE entity both Attach and every call-time dispatch method hang off
// of.
type Client struct{ /* unexported */ }
func NewClient(opts ...ClientOpt) *Client

// Attach binds backend to c — EXCLUSIVE, mirrors events.Client.Attach/
// reqreply.Client.Attach's identical "one Transport per Client" rule
// exactly: a 2nd Attach call returns BackendAlreadyAttachedError
// (structurally identical to TransportAlreadyAttachedError). Every
// call-time method below returns NoBackendAttachedError
// (structurally identical to NoTransportAttachedError) if called
// before Attach.
func (c *Client) Attach(backend Backend) error

// Read performs r's declared Read role against c's attached Backend.
// r is the role value built via Resource.WithRead(...).Handle(c) (any,
// reflectively dispatched — SAME shape as Client.Publish's pub
// parameter); vars supplies the path template's variables (mirrors
// ports.File[T].Read's own existing vars map[string]string parameter).
// Returns the decoded T boxed as any — caller asserts it, exactly like
// reqreply.Call's respAny.(ComputeResp) idiom:
//
//	entryAny, err := client.Read(ctx, reader, map[string]string{"id": "42"})
//	entry := entryAny.(MyRecord)
func (c *Client) Read(ctx context.Context, r any, vars map[string]string) (any, error)

func (c *Client) Create(ctx context.Context, cr any, val any, vars map[string]string) error

func (c *Client) Patch(ctx context.Context, p any, patch any, vars map[string]string) error

// Delete returns existed=false (nil error) for the idempotent "already
// absent" case — mirrors ports.File.Delete's existing contract.
func (c *Client) Delete(ctx context.Context, d any, vars map[string]string) (existed bool, err error)

// List returns the decoded []T boxed as any (caller asserts
// entriesAny.([]MyRecord)) — vars supplies the PARENT directory's path
// variables (the same ones WithList's own doc comment describes).
func (c *Client) List(ctx context.Context, l any, vars map[string]string) (any, error)

// Exists returns (Entry{}, false, nil) for the idempotent "absent" case
// — never an error — mirrors Delete's existed-bool convention.
func (c *Client) Exists(ctx context.Context, e any, vars map[string]string) (Entry, bool, error)

// Copy and Move BOTH take TWO var sets — srcVars/dstVars — rather than
// a second declared path template. Resource[T]'s path template stays
// SINGULAR; srcVars and dstVars are the SAME template instantiated
// twice with different values (not two different shapes). Chosen over
// a 2nd declared destination template because: (a) it requires ZERO
// change to Resource[T]'s declaration shape or WithCopy/WithMove's
// already-drafted builder signatures; (b) it mirrors how every OTHER
// role already takes one vars map[string]string, just twice; (c) a 2nd
// template would only be needed if source/destination could have
// STRUCTURALLY different path shapes (different param names/types) —
// not a requirement anyone has asked for, and inventing it now would be
// scope creep this doc deliberately avoids (see "Out of scope").
func (c *Client) Copy(ctx context.Context, cp any, srcVars, dstVars map[string]string) error
func (c *Client) Move(ctx context.Context, mv any, srcVars, dstVars map[string]string) error

// Mkdir returns created=false (nil error) for the idempotent "already
// existed" case — mirrors Backend.Mkdir's own contract exactly (this
// method is a thin pass-through to it).
func (c *Client) Mkdir(ctx context.Context, m any, vars map[string]string) (created bool, err error)
```

`BackendAlreadyAttachedError`/`NoBackendAttachedError` are new,
structurally identical siblings of `events.TransportAlreadyAttachedError`/
`events.NoTransportAttachedError` (see Structured errors below).
`TransportTypeMismatchError`'s `api/file` sibling,
`BackendTypeMismatchError`, is returned when `val`/`patch`'s concrete
type doesn't match the role's declared `T`/`P` — same narrowly-scoped
runtime-type-safety cost `events.Client.Publish` already accepts for the
exact same reason (an `any`-typed call-time parameter).

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

    // Stat returns path's metadata WITHOUT reading its content — the
    // backend operation WithExists/Exister[T] delegates to directly
    // (mirrors S3's HeadObject exactly; adapters/file implements this
    // via os.Stat). Not-found reports the SAME not-found signal
    // ReadBytes does — Exister[T] translates it to (Entry{}, false, nil)
    // rather than propagating an error, per Exists's own "absence is not
    // an error" contract.
    Stat(ctx context.Context, path string) (Entry, error)

    // Mkdir creates path as a directory, including any missing parents
    // (mirrors os.MkdirAll) — the operation WithMkdir/MkdirCreator[T]
    // AND WriteOptions.CreateDirs both delegate to, so there is only
    // ONE directory-creation code path backends implement, not two.
    // created=false (nil error) is the idempotent "already existed"
    // success case. A backend with NO real directory concept (S3) MUST
    // still implement this as a no-op success (created=true, nil error)
    // — never an error — since failing would make EVERY WriteOptions.
    // CreateDirs=true call fail on that backend; see "Backend semantic
    // differences" below for why this is an acknowledged, not hidden,
    // divergence.
    Mkdir(ctx context.Context, path string, opts MkdirOptions) (created bool, err error)

    // BackendName returns a descriptor for observability — mirrors
    // every ports.SourceAdapter/SinkAdapter/IOAdapter's own AdapterName()
    // convention exactly.
    BackendName() string
}

// CopyingBackend is an OPTIONAL extension a Backend may implement for a
// native, server-side copy (e.g. S3's CopyObject — no bytes round-trip
// through the caller at all; a local filesystem's reflink/CoW fast path
// where supported). Type-asserted by the dispatch core's Copier[T]
// handling — mirrors stats.TraceObserver/SecurityObserver's EXACT
// type-assertion-guard idiom, just applied to a Backend capability
// instead of an Observer one. A Backend NOT implementing this falls back
// to the dispatch core's own generic Read(src)+Write(dst) composition
// automatically — CopyingBackend only ever makes Copy FASTER/cheaper,
// never changes its observable result.
type CopyingBackend interface {
    CopyBytes(ctx context.Context, srcPath, dstPath string) error
}

// MovingBackend is CopyingBackend's sibling for atomic rename/move
// (e.g. filesystem's os.Rename, SMB's native rename) — S3 has NO native
// move at all (see "Backend semantic differences"), so an S3 Backend
// simply does not implement this interface; the dispatch core falls
// back to a generic Copy(src,dst)+Delete(src) composition, which for S3
// is the ONLY correct implementation anyway (2 non-atomic calls, exactly
// matching what a caller would have to do by hand against the raw S3
// API).
//
// Resolved this revision — the fallback's "Copy" half is a full
// 3-LEVEL cascade, reusing Copier[T]'s OWN dispatch-core function
// directly rather than re-implementing a 2nd copy of its fallback
// logic: Move tries, in order, (1) MovingBackend.MoveBytes if
// implemented — one atomic/native call; (2) otherwise,
// Copier[T]'s dispatch function (which ITSELF tries CopyingBackend.
// CopyBytes first, falling back to generic Read+Write) followed by
// Deleter[T]'s DeleteBytes; (3) which transparently collapses to pure
// Read+Write+Delete on a Backend implementing neither optional
// extension. A Backend implementing CopyingBackend but NOT
// MovingBackend (plausible: cheap server-side copy, no atomic rename)
// gets the FASTEST available path automatically — Move never
// reimplements Copy's own fallback decision, it DELEGATES to it.
type MovingBackend interface {
    MoveBytes(ctx context.Context, srcPath, dstPath string) error
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

// MkdirOptions mirrors the backend-agnostic subset of today's
// FileOptions.DirPerm — see the Capability section below for why raw
// POSIX permission bits are NOT the primary mechanism going forward.
type MkdirOptions struct {
    // NativePOSIXMode, when non-nil, is passed to the backend's own
    // permission-setting call when (and ONLY when) the backend
    // implements POSIX-style permissions at all — see [NativePOSIXMode]
    // capability below. nil means "use the backend's own default."
    NativePOSIXMode *os.FileMode
}
```

`adapters/file` is migrated to implement `file.Backend` against `os.*` —
its EXISTING stream-bridge functions (`ScanAdapter`/`DrainWriteAdapter`/
etc.) are unaffected; they keep calling `ports.File`'s methods exactly as
today, since `ports.File` itself becomes the thing that now delegates to
`file.Backend` internally rather than calling `os.*` directly — a
backend-swap below `ports.File`'s existing public surface, not a breaking
change to `ports.File`'s own call signatures. `adapters/file`'s `Backend`
implementation ALSO implements `CopyingBackend` (via `io.Copy` between 2
open `*os.File` handles — not a true reflink/CoW fast path in Phase 1,
but still avoids a full decode/re-encode round-trip through the caller)
and `MovingBackend` (via `os.Rename`, genuinely atomic on a single
filesystem) — proving BOTH optional extension interfaces against a real
backend, not just the required one.

### `file.Capability` — the cross-backend permission model

Mirrors `d-0006-protocol-native-capabilities.md`'s ALREADY-ESTABLISHED
sealed `Capability`/`Apply`/`CheckCapabilityCoverage` mechanism EXACTLY
(the same one `mqtt5.QoS`/`Retained`/`MessageExpiry` use) — a NEW
instance of a PROVEN pattern, not a new mechanism:

```go
// Capability is the sealed interface for api/file-specific protocol-
// native declarations — mirrors mqtt5.Capability's own sealing pattern
// (isFileCapability() instead of isMQTT5Capability()), for the SAME
// reason: a Capability meant for one boundary should not compile against
// an unrelated one.
type Capability interface {
    isFileCapability()
    Apply(wire *WireAttributes) (applied bool, err error)
}

// CapabilityName is an OPTIONAL interface a Capability may additionally
// implement for CheckCapabilityCoverage/stats.CapabilityObserver
// purposes — named explicitly this revision (was previously implied but
// unnamed). Mirrors events.CapabilityName EXACTLY: deliberately NOT part
// of the sealed Capability interface itself, since an external
// implementer of a future adapter-specific Capability should not be
// FORCED to supply a name — CapabilityNameOf falls back to a %T-derived
// name when a capability doesn't implement this (same fallback
// events.CapabilityNameOf already provides).
type CapabilityName interface{ CapabilityName() string }

// WireAttributes is the ONE intermediate value every Capability.Apply
// targets — mirrors mqtt5.WireAttributes's exact rationale.
type WireAttributes struct {
    AccessMode      AccessMode // zero value AccessMode(0) means "unset, use backend default"
    NativePOSIXMode *os.FileMode
}

// AccessMode is a sealed Capability declaring READ-ONLY vs. READ-WRITE
// access for one Resource role — the ONE permission concept confirmed
// (via direct research, not assumption) to be genuinely portable across
// ALL 3 backends:
//   - filesystem: os.Chmod's OWN Windows behavior ALREADY collapses the
//     entire POSIX mode matrix down to exactly this binary distinction
//     (confirmed via Go's stdlib docs: "on Windows, only the 0200 bit...
//     controls whether the file's read-only attribute is set or
//     cleared" — every other bit is ignored) — AccessMode is not a
//     simplification THIS doc invents, it is the simplification Go's
//     OWN standard library already settled on for cross-platform code.
//   - S3: enforced at the api/file DISPATCH level (reject Write/Delete/
//     Move/Mkdir before ever calling the Backend) when ReadOnly is
//     declared — S3's OWN ACL/bucket-policy model is grantee+action
//     based, not bit-based, so there is nothing finer-grained to
//     usefully expose here without inventing an S3-specific capability
//     (explicitly out of scope — see Scope decisions).
//   - SMB: same dispatch-level enforcement as S3 (SMB shares typically
//     inherit the HOST's native permission model — Windows ACLs on a
//     Windows-hosted share, POSIX-mapped on a Samba-hosted one — neither
//     of which this doc's Phase 1 attempts to model precisely).
type AccessMode int

const (
    AccessModeUnset     AccessMode = 0
    AccessModeReadOnly  AccessMode = 1
    AccessModeReadWrite AccessMode = 2
)

func (AccessMode) isFileCapability() {}
func (AccessMode) CapabilityName() string { return "AccessMode" }
func (m AccessMode) Apply(wire *WireAttributes) (bool, error) {
    wire.AccessMode = m
    return true, nil
}

// NativePOSIXMode is a sealed Capability escape hatch for callers who
// genuinely need literal POSIX mode bits (e.g. 0755) — ONLY meaningful
// on filesystem-family backends. Explicitly a NO-OP (applied=false, nil
// error — "doesn't apply here," NOT a failure) on S3; on Windows
// filesystems it is silently collapsed to the SAME binary distinction
// AccessMode already provides, via os.Chmod's own behavior — NOT
// duplicated logic, the SAME call. Declaring BOTH AccessMode and
// NativePOSIXMode on one role is allowed (NativePOSIXMode further
// refines what AccessMode leaves backend-default on a POSIX backend);
// declaring ONLY NativePOSIXMode against an S3-attached Resource is
// valid and simply inert, never an error — mirrors Retained's existing
// "harmless no-op on a path that doesn't read it" precedent exactly.
type NativePOSIXMode os.FileMode

func (NativePOSIXMode) isFileCapability() {}
func (NativePOSIXMode) CapabilityName() string { return "NativePOSIXMode" }
func (m NativePOSIXMode) Apply(wire *WireAttributes) (bool, error) {
    v := os.FileMode(m)
    wire.NativePOSIXMode = &v
    return true, nil
}
```

**Correction (this revision)**: `events.CheckCapabilityCoverage(topic
string, declared []CapabilityRequirement, supplied []any) error` is an
`api/events`-OWNED function with an events-specific signature (`topic
string` is baked into it) — `api/file` CANNOT literally call it. What's
reused is the MECHANISM, not the function: `api/file` gets its OWN
`file.CheckCapabilityCoverage(path string, declared
[]CapabilityRequirement, supplied []any) error`, a structural mirror
(identical first-rule-wins matching logic, identical error shape) but
its own, separately-owned copy — exactly how `rest.ErrorPattern`/
`events.ErrorPattern`/`reqreply.ErrorPattern` are already 3 independently
-owned types sharing one convention, not one shared generic type. A
`Resource` role declaring `RequireAccessMode(AccessModeReadOnly)` against
a `Backend` that hasn't been told it's read-only-safe (i.e., no
`AccessMode` Capability was supplied at attach time) fails loudly at
attach time via THIS `file`-owned function, mirroring
`events.RequireQoS`'s identical "fail fast, not silently" contract in
spirit, not in shared code.

### Backend semantic differences — documented, not hidden

"Transparent across backends" does NOT mean "identical in every
respect" — 3 real, acknowledged divergences, each resolved explicitly
rather than papered over:

| Operation | Filesystem | S3 | SMB |
|---|---|---|---|
| `Mkdir` | Real directory, enforced (`os.MkdirAll`) | NO-OP success — S3 has NO real directories at all; "folders" are a pure key-prefix convention in `ListObjectsV2`'s own delimiter handling, not a backend entity `Mkdir` could meaningfully create | Real directory, enforced (native SMB `MKDIR`) |
| `Move` | ONE atomic syscall (`os.Rename`) — either fully succeeds or the source is untouched | TWO non-atomic calls (`CopyObject` then `DeleteObject`) — a crash/failure between them can leave BOTH copies present; `api/file`'s dispatch core surfaces this as a DOCUMENTED caveat on `Mover[T]`'s own godoc for any backend lacking `MovingBackend`, never silently | ONE native, typically-atomic protocol operation (SMB2's own rename) |
| Permission granularity | Full POSIX mode bits (`NativePOSIXMode`) honored | `AccessMode` only — enforced at `api/file`'s dispatch level, never translated into an S3 ACL/policy call (explicitly out of scope) | `AccessMode` only in Phase 1 (same reasoning — SMB's native ACL model is as rich as Windows's own, not usefully bit-mappable either) |

`Mkdir`'s S3 no-op in particular means `WriteOptions.CreateDirs=true`
behaves identically to `CreateDirs=false` on an S3-attached `Resource` —
NOT a bug, a direct, honest consequence of S3 genuinely having no
directory concept; documented on `Backend.Mkdir`'s own godoc (above) and
cross-referenced from `WriteOptions.CreateDirs`'s doc comment, so a
caller migrating a `Resource` from filesystem to S3 learns this from the
type they're already reading, not by surprise at runtime.

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
string}` — returned when a `WriteOptions`/`DeleteOptions`/`MkdirOptions`
field a backend genuinely cannot honor is set to a non-default value AND
the backend author chose to fail loudly rather than silently no-op (an
explicit, per-backend, per-option choice — not a core-dispatch decision).

**Added this revision** (alongside `file.Client` above): 3 NEW error
types, each a structurally identical sibling of an EXISTING `api/events`
error — reusing the convention, not the code (same reasoning as the
`CheckCapabilityCoverage` correction below: each pattern family owns its
own copy):
- `BackendAlreadyAttachedError{}` — mirrors
  `events.TransportAlreadyAttachedError{}`; returned by a 2nd
  `Client.Attach` call.
- `NoBackendAttachedError{}` — mirrors
  `events.NoTransportAttachedError{}`; returned by `Read`/`Create`/
  `Patch`/`Delete`/`List`/`Exists`/`Copy`/`Move`/`Mkdir` when `Attach`
  was never called.
- `BackendTypeMismatchError{Expected, Got string}` — mirrors
  `events.TransportTypeMismatchError`; returned when `val`/`patch`'s
  dynamic type doesn't match the role's declared `T`/`P`.

No further NEW error types needed for the 4 added verbs — `Exists`
reuses `FileNotFoundError`'s ABSENCE of an error (the `(Entry{}, false,
nil)` case) rather than introducing one; `Copy`/`Move` reuse
`FileReadError`/`FileWriteError`/`FileDeleteError` for their respective
halves (whichever actually failed, fast path or fallback); `Mkdir` reuses
`FileWriteError` (mirrors `ports.File.Write`'s own existing
`CreateDirs`-failure error type exactly, since `Mkdir` was always
implicitly part of that error's scope).

## Observer integration

Reuses `stats.FileObserver` (`RecordFileRead`/`RecordFileWrite`/
`RecordFileDelete`) for Read/Create/Patch/Delete — identical call sites
to `ports.File`'s existing ones, just now fired from `api/file`'s
dispatch core instead of inline. The expanded verb set (this revision)
needs 5 new methods — batched into ONE breaking round rather than 5
separate ones across future revisions, since `FileObserver` already
accepts this kind of direct, no-separate-extension churn (see
`RecordFileDelete`'s own precedent):

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
    // RecordFileExists is called once per WithExists call — success is
    // false ONLY on a genuine backend-level Stat failure; the "absent"
    // case (Entry{}, false, nil) is success=true, mirroring
    // RecordFileDelete's own "idempotent absence is still success"
    // convention exactly.
    RecordFileExists(path string, success bool, duration time.Duration)
    // RecordFileCopy is called once per WithCopy call, regardless of
    // whether the CopyingBackend fast path or the generic Read+Write
    // fallback actually ran — callers observing lifecycle events should
    // not need to know which path was taken.
    RecordFileCopy(srcPath, dstPath string, success bool, duration time.Duration)
    // RecordFileMove mirrors RecordFileCopy for WithMove — fires once
    // regardless of MovingBackend fast path vs. generic Copy+Delete
    // fallback.
    RecordFileMove(srcPath, dstPath string, success bool, duration time.Duration)
    // RecordFileMkdir is called once per WithMkdir call AND once per
    // WriteOptions.CreateDirs=true Write call that actually invokes
    // Backend.Mkdir — success=true for S3's documented no-op case too
    // (a no-op IS the correct, successful outcome there, not a failure).
    RecordFileMkdir(path string, success bool, duration time.Duration)
}
```

Added DIRECTLY to the existing `FileObserver` interface (an intentional,
acknowledged breaking change — mirrors `RecordFileDelete`'s own addition
precedent exactly, documented in `stats/observer.go`'s own doc comment:
"go-codex has one consumer," so this repo accepts the churn rather than
fragmenting into yet another observer extension per method). `Capability`
Apply reporting reuses `stats.CapabilityObserver.RecordCapabilityApplied`
UNCHANGED — the SAME interface `mqtt5.QoS`/`Retained`/`MessageExpiry`
already report through, no new observer concept needed for `AccessMode`/
`NativePOSIXMode`. `TraceObserver`/`SecurityObserver` integration mirrors
`rest`/`events`/`reqreply`'s identical type-assertion-guard pattern — no
new observer extension needed for either.

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
  `DeleteBoundMW`/`ListBoundMW`/`ExistsBoundMW`/`CopyBoundMW`/
  `MoveBoundMW`/`MkdirBoundMW` (corrected this revision — the original
  list only named 5 of the 9 roles, missed the 4 added in the
  verb-expansion round) — one per role, mirroring `SubscribeBoundMW`/
  `PublishBoundMW`'s existing per-role split. The
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
| `TestExister_Handle_Exists_ReturnsEntryWithoutReadingBody` | `Exister[T]` calls ONLY `Backend.Stat`, never `ReadBytes` — proves no accidental body download |
| `TestExister_Handle_Exists_AbsentReturnsFalseNotError` | A not-found `Stat` result returns `(Entry{}, false, nil)`, NOT an error |
| `TestCopier_Handle_Copy_UsesNativeFastPathWhenAvailable` | A fake `Backend` ALSO implementing `CopyingBackend` has its `CopyBytes` called directly, never `ReadBytes`/`WriteBytes` |
| `TestCopier_Handle_Copy_FallsBackToReadWriteWhenBackendLacksCopyingBackend` | A fake `Backend` NOT implementing `CopyingBackend` is copied via the generic `ReadBytes`+`WriteBytes` composition — same observable result either way |
| `TestMover_Handle_Move_UsesNativeRenameOnFilesystem` | `adapters/file`'s `MovingBackend` implementation (`os.Rename`) is actually invoked, not the generic fallback |
| `TestMover_Handle_Move_FallsBackToCopyThenDeleteWithoutMovingBackend` | A fake `Backend` without `MovingBackend` is moved via Copy+Delete — proving the S3-shaped fallback path works correctly even on a non-S3 fake |
| `TestMkdirCreator_Handle_Mkdir_CreatesMissingParents` | `WithMkdir` creates a multi-level missing parent chain in one call, mirrors `ports.File.Write`'s existing `CreateDirs` proof |
| `TestWriteOptions_CreateDirs_DelegatesToBackendMkdir` | `WriteOptions.CreateDirs=true` internally calls the SAME `Backend.Mkdir` a direct `WithMkdir` call would — proving there is only ONE directory-creation code path, not two |
| `TestAccessMode_ReadOnly_RejectsWriteBeforeAnyBackendCall` | A `Resource` with `AccessMode: AccessModeReadOnly` supplied at attach time rejects a `Creator`/`Deleter`/`Mover` call BEFORE the `Backend` is ever invoked — fails at the dispatch layer, not the backend |
| `TestNativePOSIXMode_Apply_SetsWireField` | Mirrors `TestMessageExpiry_Apply_SetsWireField`'s own direct-`Apply` proof pattern |
| `TestNativePOSIXMode_NoOpOnBackendsThatDontHonorIt` | A fake non-POSIX `Backend` receiving a `NativePOSIXMode` capability reports `applied=false`, never an error — proves the "harmless no-op" contract |
| `TestResourceRequireAccessMode_NoCapabilitySupplied_FailsCoverageAtAttachTime` | Mirrors `events.RequireQoS`'s identical "fail fast, not silently" proof, applied to `AccessMode` |
| `TestClient_Attach_Exclusive_SecondCallReturnsBackendAlreadyAttachedError` | Mirrors `TestClient_Attach_Exclusive` (events) for `file.Client` |
| `TestClient_Read_NoBackendAttached_ReturnsNoBackendAttachedError` | Calling any dispatch method before `Attach` fails loudly, not silently |
| `TestClient_Read_ValDoesNotMatchDeclaredT_ReturnsBackendTypeMismatchError` | `Create`/`Patch`'s `val`/`patch` dynamic-type mismatch is caught, mirrors `TransportTypeMismatchError`'s proof |
| `TestClient_Copy_SrcVarsDstVarsBuildDifferentPaths` | `Copy`'s `srcVars`/`dstVars` each build a DIFFERENT concrete path from the SAME declared template — proves the "one template, two instantiations" design decision actually works |
| `TestClient_Move_PrefersMovingBackendOverCopyingBackendFallback` | A fake `Backend` implementing BOTH `MovingBackend` and `CopyingBackend` uses `MoveBytes`, never `CopyBytes`+`DeleteBytes` — proves cascade level 1 wins |
| `TestClient_Move_FallsBackToCopyingBackendPlusDeleteWhenNoMovingBackend` | A fake `Backend` implementing ONLY `CopyingBackend` (not `MovingBackend`) has `Move` call `CopyBytes` then `DeleteBytes` — proves cascade level 2, delegating to `Copier[T]`'s own fast path rather than reimplementing it |
| `TestClient_Move_FallsBackToReadWriteDeleteWithNeitherExtension` | A fake `Backend` implementing NEITHER optional extension still succeeds via pure `ReadBytes`+`WriteBytes`+`DeleteBytes` — proves cascade level 3 |
| `TestFileCheckCapabilityCoverage_MirrorsEventsStructurally` | `file.CheckCapabilityCoverage`'s matching/error-shape behavior is verified independently against its own fixtures — proves it is a genuine, working COPY, not a stale unimplemented reference to `events`'s function |
| `ExampleResource_read` / `ExampleResource_create` / `ExampleResource_copy` | pkg.go.dev-facing worked examples |

## Files to create

| File | Responsibility |
|---|---|
| `api/file/builder.go` | `Resource[T]`, `PathParam`/`MergedPathParam`, `Reader`/`Creator`/`Patcher`/`Deleter`/`Lister`/`Exister`/`Copier`/`Mover`/`MkdirCreator`, `Read`/`Create`/`Patch`/`Delete`/`List`/`Exists`/`Copy`/`Move`/`Mkdir` metadata structs, per-role `*Handle[T]` registry types |
| `api/file/client.go` (**NEW this revision**) | `Client`/`NewClient`/`ClientOpt`, `Client.Attach(Backend) error` (exclusive attach-time wiring), `Client.Read`/`Create`/`Patch`/`Delete`/`List`/`Exists`/`Copy`/`Move`/`Mkdir` (reflective `any`-typed call-time dispatch) — mirrors `events.Client`/`reqreply.Client`'s established `Attach`+dispatch shape |
| `api/file/backend.go` | `Backend` interface (incl. `Stat`/`Mkdir`), `CopyingBackend`/`MovingBackend` optional extensions, `Entry`, `WriteOptions`/`DeleteOptions`/`MkdirOptions` |
| `api/file/capability.go` | `Capability`/`CapabilityName`/`CheckCapabilityCoverage`/`WireAttributes`/`AccessMode`/`NativePOSIXMode` — mirrors `adapters/mqtt5/capability.go`+`api/events/capability.go`'s combined shape, one boundary over |
| `api/file/dispatch.go` | The shared dispatch core every `Client` call-time method delegates to — path build/validate, codec encode/decode, merge-field application, `AccessMode` enforcement, `CopyingBackend`/`MovingBackend` type-assertion-guarded fast paths with generic fallback (incl. `Move`'s 3-level cascade delegating to `Copier[T]`'s own fallback), Observer calls, ErrorPattern dispatch |
| `api/file/errors.go` | Relocated error types (from `ports/file.go`) + `BackendUnsupportedOptionError` + `BackendAlreadyAttachedError`/`NoBackendAttachedError`/`BackendTypeMismatchError` |
| `api/file/error_pattern.go` | `ErrorPattern[E,B]` — mirrors `rest.ErrorPattern`/`events.ErrorPattern`/`reqreply.ErrorPattern`'s identical shape |
| `api/file/doc.go` | Package godoc |
| `adapters/file/backend.go` | `os.*`-backed `file.Backend` implementation, ALSO implementing `CopyingBackend` (`io.Copy`) and `MovingBackend` (`os.Rename`) — new file; existing `binding.go`/`adapter.go` untouched |
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

Mostly resolved. One genuinely open item remains from this refinement
round, flagged rather than silently decided:

- **`MkdirOptions`'s exact field set beyond `NativePOSIXMode`** — e.g.
  whether a `DryRun`-equivalent (mirrors `WriteOptions.DryRun`'s existing
  "report what would happen" contract) is worth adding to `Mkdir`
  directly, versus relying on `WithMkdir`'s caller composing their own
  dry-run check via `WithExists` first. Leaning toward adding `DryRun`
  for parity with every other verb's option struct, but not pre-decided
  — resolve during Implement mode once the full option-struct shapes are
  drafted together.

**Resolved this round** (a dedicated critical review pass found these 3
gaps — none had been designed in either prior round — plus 4 smaller
inaccuracies/inconsistencies; all 7 are now fixed above, not merely
flagged):

- **Call-time dispatch API** was completely unspecified before this
  round — no built role Handle exposed any method to actually perform
  its operation. Resolved: a `file.Client` type, mirroring
  `events.Client`/`reqreply.Client`'s real, confirmed `Attach`+reflective
  `any`-in/`any`-out dispatch shape exactly (researched directly from
  `api/events/builder.go` and `api/reqreply/client.go`, not invented) —
  see "`file.Client` — attach-time `Backend` wiring + call-time
  dispatch" above.
- **Copy/Move's destination path** was unaddressed — resolved as two
  `map[string]string` var sets (`srcVars`/`dstVars`) against the SAME
  singular declared `Resource[T]` template, not a second declared
  template — zero changes needed to the already-drafted `WithCopy`/
  `WithMove` builder signatures.
- **Attach-time `Backend` wiring** was unaddressed — resolved as
  `Client.Attach(backend Backend) error`, exclusive, mirroring
  `events.Client.Attach`'s identical one-per-Client rule.
- **`CheckCapabilityCoverage` "reused unchanged"** was factually wrong
  (it's an `events`-owned function with an events-specific signature) —
  corrected to "`api/file` owns a structurally-mirrored copy," consistent
  with `ErrorPattern`'s own per-pattern-family-owns-its-own-copy
  precedent.
- **Router/Middleware's `*BoundMW` list** was stale (named only 5 of 9
  roles) — corrected to all 9.
- **`Move`'s fallback cascade ordering** (when a `Backend` implements
  `CopyingBackend` but not `MovingBackend`) was ambiguous — resolved as
  an explicit 3-level cascade (`MovingBackend` → delegate to `Copier[T]`'s
  own `CopyingBackend`-or-generic fallback + `Delete` → pure
  `Read`+`Write`+`Delete`), with `Move` DELEGATING to `Copier[T]`'s
  dispatch rather than reimplementing its fallback decision.
- **An implied `CapabilityName` interface** was never named — now an
  explicit, documented optional interface mirroring `events.CapabilityName`.

Everything else resolved via `ask_user` across all 3 rounds (original +
2 refinements):

- Package name: `api/file` (not `api/storage`).
- `ports.File[T]`'s fate: thin wrapper over the new `Backend` seam, not
  replaced or left untouched.
- Verb scope (refined this round): Read, Create, Patch, Delete, List,
  Update, Exists, Copy, Move, Mkdir — Update stays a CLIENT-SIDE-ONLY
  Read+transform+Write composition (no new wire verb); Exists/Copy/Move/
  Mkdir are the S3-survey-informed additions, each traced to a specific
  S3 operation or the user's explicit directory-creation/permission ask,
  not invented speculatively.
- Backend scope: design the full seam now (now including `Stat`/`Mkdir`
  + the `CopyingBackend`/`MovingBackend` optional extensions); implement
  only `adapters/file` (filesystem) in Phase 1, proving ALL of it
  (including both optional extensions) against one real backend.
- Router/Middleware depth: full parity designed for Phase 1 (not
  deferred to Phase 2).
- Permission model: a sealed `AccessMode` Capability (ReadOnly/ReadWrite)
  is the ONE universally portable concept — confirmed via direct research
  (Go's own `os.Chmod` Windows behavior, S3's ACL/policy model) that a
  full POSIX-to-Windows/S3 bit-level translator is not meaningfully
  achievable, so none was designed; `NativePOSIXMode` is the explicit,
  honest escape hatch for POSIX-specific callers instead.
- `Move`'s cross-backend atomicity difference (filesystem's 1 atomic
  syscall vs. S3's 2 non-atomic calls) is DOCUMENTED, not hidden or
  papered over with a false "it's the same everywhere" claim.
