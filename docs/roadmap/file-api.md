# File API — `api/file`

> **Status:** Phase 1 PARTIALLY IMPLEMENTED. The `api/file` package (all 10
> roles, `Client`, dual-accept dispatch, `Capability` model, `ErrorPattern`,
> Observer integration) and `adapters/file.Backend` (the filesystem
> implementation, including `CopyingBackend`/`MovingBackend` fast paths) are
> fully built and tested — see `docs/features/file-api.md` and
> `examples/file-api/main.go`. **NOT yet done**: migrating `ports.File[T]`/
> `ports.Dir`/`ports/pattern.go`'s existing `os.*`-calling code to delegate
> onto this new stack — that is a separate, higher-risk follow-up (the
> existing `ports.File`/`adapters/file` functions continue to work exactly
> as before, UNCHANGED, in the meantime). Two documented Phase 1 scope
> reductions vs. the design below: (1) Router-group integration is a
> SIMPLIFIED plain-prefix mechanism (`router.go`'s `WithRouter(prefix)`),
> not yet the full `internal/router.Routable[Target]` integration
> `api/rest`/`api/events`/`api/reqreply` use; (2) `ErrorPattern[E,B]` is
> implemented but NOT auto-invoked by the dispatch core (unlike
> `rest`/`events`/`reqreply`'s `ErrorPattern`, which IS auto-consulted).
> Phase 2 outlined (full design deferred until Phase 1's ports migration
> ships). Phases 3-4 planned. See "Roadmap phases" below for the full
> picture.
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
Patch/Delete/List/Update/Exists/Copy/Move/Mkdir/BatchDelete instead of
GET/POST/PATCH/DELETE/GET-collection), full Router-group and declarative-
Middleware parity, Observer integration, a cross-backend permission
capability, and — the ultimate goal — true backend transparency: the
SAME declared `Resource[T]` works unchanged whether the concrete storage
is the local filesystem, S3, or an SMB share, with the backend chosen
entirely at attach time, never baked into the declaration.

**Refinement round 2**: expanded the verb set by directly surveying what
Amazon S3's own API provides (`PutObject`/`GetObject`/`DeleteObject`/
`ListObjectsV2`/`HeadObject`/`CopyObject`), added 4 verbs this surfaced
that `ports.File` has no equivalent for today (`Exists`/`Copy`/`Move`/
`Mkdir`), promoted directory creation from an implicit `Write` side
effect to its own first-class operation, and designed a cross-backend
permission capability (`AccessMode`) after confirming via direct
research that POSIX mode bits and Windows/S3 permission models are NOT
meaningfully unifiable beyond a binary read-only/writable distinction —
the SAME simplification Go's own `os.Chmod` already makes on Windows.
Refinement round 3 closed 3 major call-time/attach-time API gaps a
critical review found (see "`file.Client`" below). **Refinement round 4
(this revision)**: cross-referenced the FULL, authoritative AWS S3 API
operations list (not just the 6 core ops surveyed in round 2) — found
one more genuine verb gap (`DeleteObjects`' real batch-delete, closed as
`BatchDeleter[T]` below) and reorganized the doc's own scope into 4
explicit phases, tracked in this ONE roadmap doc rather than splitting
backend-adapter work into separate future docs.

## Roadmap phases

This doc now spans 4 phases — all tracked HERE, not split across
multiple roadmap docs (a deliberate reversal of round 2's original
"`adapters/s3`/`adapters/smb` get their own future roadmap doc" call,
made explicitly this round):

| Phase | Content | Design depth | Status |
|---|---|---|---|
| **1** | `api/file` package itself: `Resource[T]` + 10 role builders, `file.Client` (attach+dispatch), `file.Backend` seam, `file.Capability`, Router/Middleware/Observer/ErrorPattern integration, `adapters/file` migration (filesystem backend) | Full Go-signature design (this doc's main body, below) | Design complete — implementation not started |
| **2** | `adapters/s3`, `adapters/smb` implementations; streaming/chunked I/O; conditional writes (ETag); recursive `WithList` | Outline only (rationale + key design questions per item, NOT full signatures) — full design deliberately deferred until Phase 1 ships and its abstractions are proven against a real implementation | Outlined this round (see "Phase 2" section below) |
| **3** | Adapt existing examples (`examples/*`) from `ports.File`/`adapters/file` direct usage to the new `api/file.Client` approach | Checklist-level (which examples, what "adapted" means) — necessarily light since Phase 1 isn't implemented yet | Planned (see "Phase 3" section below) |
| **4** | Promote this roadmap doc to `docs/design/d-NNNN-*.md` (per the `plan-a-new-codex-feature` skill's delete/keep/promote 3-way policy) + sync `.github/instructions/go-codex.instructions.md`, `docs/` pages, and relevant skills | Mechanism named, exact number/content deferred until Phase 1-3 actually ship | Planned (see "Phase 4" section below) |

Phase 1 is this doc's main body (everything through "Open design
decisions"); Phases 2-4 each get their own dedicated section near the
end, after "Out of scope."

## Scope decisions

| In scope (Phase 1) | Out of scope |
|---|---|
| New `api/file` package: `Resource[T]` declaration + 10 role builders — Read, Create, Patch, Delete, List, **Exists** (NEW round 2 — mirrors S3's `HeadObject`: metadata-only, no body download), **Copy** (NEW round 2 — mirrors S3's `CopyObject`: duplicate without a client-side round-trip), **Move**/Rename (NEW round 2 — filesystem's atomic `os.Rename`; S3 has no native equivalent for standard buckets, composed from Copy+Delete there — see "Backend semantic differences" for the S3 Express directory-bucket caveat), **Mkdir** (NEW round 2 — promoted from `WriteOptions.CreateDirs`'s implicit side effect to its own first-class operation), **BatchDelete** (NEW round 4 — mirrors S3's real `DeleteObjects`: delete N arbitrary, individually-addressed resources in ONE call, distinct from `ports.Dir.Delete`'s single-directory recursive delete) | A genuinely new HTTP-shaped protocol — `api/file` is NOT a network protocol, it has no wire format of its own; "transport" here means storage BACKEND (filesystem/S3/SMB), not a byte-stream protocol |
| `Update` stays a CLIENT-SIDE-ONLY Read+transform+Write composition (unchanged from round 1) | `RestoreObject`/`SelectObjectContent`/Object-Lock-Legal-Hold-Retention/Object-Annotations/bucket-level-infra operations — surfaced during the round-4 full-S3-API survey but have NO filesystem/SMB equivalent or operate above the per-Resource level `api/file` targets (see "Out of scope" below for the full breakdown) |
| A new `file.Backend` interface — the transparency seam adapters implement, now including `Mkdir`/`Stat`, plus 3 OPTIONAL extension interfaces (`CopyingBackend`/`MovingBackend`/`BatchDeletingBackend`, NEW round 4) a backend may implement for a native fast path | Phase 2 (`adapters/s3`/`adapters/smb` implementations, streaming I/O, conditional writes, recursive `WithList`) — tracked in THIS doc's own "Phase 2" section at OUTLINE depth, not designed to the same Go-signature rigor as Phase 1 yet |
| Migrating `adapters/file` onto the new `Backend` interface (the ONE Phase 1 backend, proving the abstraction against a REAL adapter, not just a design) — including implementing `CopyingBackend`/`MovingBackend` via `io.Copy`/`os.Rename` to prove the optional-fast-path mechanism, not just the required interface. Deliberately NOT implementing `BatchDeletingBackend` on `adapters/file` — a POSIX filesystem has no native batch-delete syscall, so that "implementation" would be the IDENTICAL loop the generic fallback already runs; proven via a fake `Backend` in tests instead, its first REAL implementation arrives with Phase 2's `adapters/s3` | Any change to `adapters/file`'s OWN existing stream-bridge functions (`ScanAdapter`/`WatchAdapter`/`ReadEachAdapter`/etc.) beyond what's needed to also implement `file.Backend` — those keep their existing signatures |
| A new `file.Capability` sealed interface (mirrors `d-0006`'s established `mqtt5.Capability`/`Apply`/`CheckCapabilityCoverage` mechanism exactly): `AccessMode` (ReadOnly/ReadWrite — the ONE permission concept genuinely portable across filesystem/S3/SMB) + `NativePOSIXMode` (escape hatch, filesystem/SMB-only) | A full POSIX-mode-bits-to-Windows-ACL or POSIX-to-S3-ACL TRANSLATOR — researched directly and confirmed NOT meaningfully achievable: Windows collapses ALL of `os.FileMode` to one bit via Go's own `os.Chmod` (confirmed via Go's stdlib docs), and S3's ACL/bucket-policy model is grantee+action-based, not bit-based at all — there is no lossless translation to invent, only an honest, portable common denominator |
| `ports.File[T]` becomes a thin wrapper around an `api/file`-declared `Resource[T]` Handle + an attached `file.Backend` — mirrors `ports.SourcePort`/`SinkPort`/`IOPort` wrapping adapters today; `ports.FilePattern`/`*Port.PluginFilePattern` is the EXISTING slot this plugs into (confirmed via code: `ports/pattern.go`'s `FilePattern`, `ports/io_port.go`'s `PluginFilePattern(pattern FilePattern) (File[Resp], error)`) | `ports.Dir`/`DirEntry` — stays EXACTLY as-is (untyped, metadata-only directory enumeration); `Resource[T].WithList` is a new, complementary, TYPED+decoded sibling, not a replacement |
| `file.Client` — the attach-time `Backend` wiring (`Attach`, exclusive) AND call-time dispatch (`Read`/`Create`/`Patch`/`Delete`/`List`/`Exists`/`Copy`/`Move`/`Mkdir`/`BatchDelete`, the last NEW round 4) surface, mirroring `events.Client`/`reqreply.Client`'s established `Attach`+reflective-`any`-dispatch shape exactly | A generic, type-safe `Client.Read[T]`-style free-function wrapper around the `any`-typed dispatch methods — deliberately NOT designed, since `reqreply.Client.Call`'s own established convention is a plain caller-side type assertion (`respAny.(T)`), not a generic wrapper; inventing one here would diverge from the precedent this doc otherwise mirrors |
| Router groups (`d-0008`'s "one shape, N packages" pattern) — `api/file` becomes the 4th instance | `d-0008`'s core Router mechanism itself — reused unchanged, only a new "Design decisions — resolved for `api/file`" subsection is added to that doc once this ships |
| Declarative per-role middleware (`ReadMW`/`CreateMW`/`PatchMW`/`DeleteMW`/`ListMW`/`ExistsMW`/`CopyMW`/`MoveMW`/`MkdirMW`/`BatchDeleteMW`, the last NEW round 4 — see "Why verb-specific MW names, not `HandleMW`/`ClientMW`" below) + Security schemes | A NEW security-SCHEME TYPE — reuses `route.SecurityScheme`/`route.SecurityRequirement` unchanged, same as `rest`/`events`/`reqreply` |
| `ErrorPattern` (reusing the established `errors.As` + first-rule-wins + direct/mapped convention) | Inventing a new error-response shape |
| Observer (`stats.FileObserver`, extended with 6 new methods — see below) | A new top-level `stats.Observer` extension unrelated to file lifecycle |

## Toolchain / dependency decisions

No new dependency for Phase 1 — `adapters/file`'s migration uses the SAME
stdlib `os`/`io` calls it (transitively, via `ports.File`) already uses
today, just reached through the new `file.Backend` interface instead of
inline. **Corrected this round** (was stale after the round-4 phase
restructuring): `adapters/s3`/`adapters/smb` no longer get their own
future roadmap doc — their toolchain/dependency decisions (which AWS SDK,
which SMB client library) are deferred to Phase 2's own full design pass,
tracked in THIS doc's "Phase 2 — outline" section, not a separate one.

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

// WithBatchDelete mirrors S3's real DeleteObjects operation (delete up
// to 1000 individually-addressed keys in ONE request) — added round 4
// after cross-referencing the FULL S3 API operations list, not just the
// 6-op core survey. Deliberately DISTINCT from ports.Dir.Delete (which
// recursively deletes ONE directory's contents matched by ONE path
// template — unchanged, kept separate): BatchDeleter[T] takes N
// INDEPENDENTLY addressed path-variable sets, which may not share a
// common parent directory at all, mirroring DeleteObjects' own
// arbitrary-key-list shape.
func (r Resource[T]) WithBatchDelete(op BatchDelete) BatchDeleter[T]
type BatchDelete struct{ Summary, Description string }
```

### Per-role attachment surface — mirrors `Subscriber[T]`/`Publisher[T]` exactly

Each role type (`Reader[T]`, `Creator[T]`, `Patcher[T,P]`, `Deleter[T]`,
`Lister[T]`, `Exister[T]`, `Copier[T]`, `Mover[T]`, `MkdirCreator[T]`,
`BatchDeleter[T]`) is independent — a Security requirement on `Deleter`
does NOT imply one on `Reader`, mirroring how a `Publish.Security`
requirement never implies one on the SAME channel's `Subscribe` side
today. `Mover[T]`'s own Security is checked ONCE, covering both the
logical read-the-source and write-the-destination halves — it does NOT
separately re-check `Deleter`'s or `Creator`'s own declared Security,
since `Move` is its OWN declared role with its OWN Security requirement,
not a composition of two OTHER roles' declarations (a Resource is not
required to declare `Delete`/`Create` at all to declare `Move`).
`BatchDeleter[T]`'s own Security is likewise checked ONCE per
`BatchDelete` call, covering the WHOLE batch — NOT once per individual
key, since a `BatchDelete` call is one declared role invocation, not N
separate `Delete` invocations composed together:

```go
// Reader[T] — every role type follows this SAME shape; shown once,
// the other 9 are structurally identical (Patcher[T,P] additionally
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

**Added round 3** — closes a gap the first 2 drafting rounds left
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
the type assertion. `api/file` mirrors this EXACT shape, not a new one.

**Correction (Phase 1 pre-implementation review)**: an earlier draft of
this section claimed every dispatch method's `any`-typed role parameter
MUST be a pre-built `*ReadHandle[T]` (i.e. the caller always calls
`.Handle(client)` first). Re-verified against the ACTUAL precedent and
found this backwards: `events.Transport.Publish`'s own doc comment
states `pub`'s dynamic type is `Publisher[T]` — the RAW, pre-`.Handle()`
builder — confirmed by a real example
(`routes.PlainReadingsPub = PlainReadingsChannel.WithPublish(events.Publish{...})`
passed DIRECTLY to `Client.Publish`, no `.Handle()` call anywhere);
`reqreply.Client.CallAsync`'s own doc example does the same
(`client.CallAsync(ctx, ComputeRoute, ComputeReq{...})` — the raw
declared `Route`). `.Handle(client, opts...)`/`.ClientHandle(opts...)`
are OPTIONAL steps, used ONLY when extra composition is needed (Router
grouping, middleware stacking — confirmed via `reqreply`'s OWN
`routes.ComputeRoute.ClientHandle(reqreply.WithRouter(...))` usage).
`file.Client`'s dispatch methods are corrected below to accept EITHER
form — a **dual-accept** contract, mirroring `reqreply.Client.Call`'s
confirmed dual-acceptance exactly:

- **The common case** (no Router grouping, no extra per-call
  middleware/options): pass the bare role builder directly —
  `client.Read(ctx, myResource.WithRead(file.Read{}), vars)` — zero
  ceremony, matching `events.Client.Publish`'s own common-case usage.
- **The Router-grouped case**: register the role's Handle ONCE via
  `.Handle(client, file.WithRouter(router))` (mirrors
  `reqreply.ClientHandle(reqreply.WithRouter(...))` exactly) and pass
  THAT built `*ReadHandle[T]` to every subsequent `Client.Read` call
  instead of the bare builder — the Handle already carries the
  resolved, router-prefixed path, so path composition has exactly ONE
  construction point (at `.Handle()` time), never re-derived per call.
  This is the direct resolution of the `plan-a-new-codex-feature`
  skill's own mandatory "Router composition" pre-implementation
  check (the `rest.Redirect` real-bug precedent it cites): there is
  only ONE place a Router-grouped path gets built, and every dispatch
  call downstream of it automatically inherits the correct path — no
  call site can forget to re-supply routing, because there is nothing
  to re-supply.

`Client`'s dispatch methods type-switch internally: if the `any`
argument is the bare builder type (`Reader[T]`), they call
`.Handle(c)` themselves with ZERO additional options (equivalent to
the non-Router-grouped case); if it's already a `*ReadHandle[T]`, they
use it directly. A caller therefore NEVER needs to call `.Handle()`
manually unless they need Router grouping or extra per-call options —
exactly mirroring how a `reqreply` caller only reaches for
`.ClientHandle(opts...)` when `opts` is non-empty:

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
// r is DUAL-ACCEPT (any, reflectively dispatched — SAME shape as
// Client.Publish's pub parameter): EITHER the bare Reader[T] returned
// by Resource.WithRead(...) (the common case — Read builds the Handle
// internally with no extra options) OR an explicitly pre-built
// *ReadHandle[T] from Reader[T].Handle(c, opts...) (the Router-grouped/
// extra-options case — see the dual-accept discussion above). vars
// supplies the path template's variables (mirrors ports.File[T].Read's
// own existing vars map[string]string parameter). Returns the decoded
// T boxed as any — caller asserts it, exactly like reqreply.Call's
// respAny.(ComputeResp) idiom:
//
//	// Common case — bare builder, no Router grouping:
//	entryAny, err := client.Read(ctx, myResource.WithRead(file.Read{}), map[string]string{"id": "42"})
//	entry := entryAny.(MyRecord)
//
//	// Router-grouped case — pre-built Handle carries the resolved path:
//	readHandle, _ := myResource.WithRead(file.Read{}).Handle(client, file.WithRouter(router))
//	entryAny, err := client.Read(ctx, readHandle, map[string]string{"id": "42"})
func (c *Client) Read(ctx context.Context, r any, vars map[string]string) (any, error)

// Create's cr is dual-accept exactly like Read's r (bare Creator[T] or
// pre-built *CreateHandle[T]) — this dual-accept note applies IDENTICALLY
// to every method below; not repeated per-method.
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
// scope creep this doc deliberately avoids (see "Out of scope"). If
// cp/mv is a Router-grouped *CopierHandle[T]/*MoverHandle[T], BOTH
// srcVars and dstVars are built against that SAME resolved,
// router-prefixed path template — there is no way to mix a
// Router-grouped source with a non-grouped destination, since both
// sides come from the ONE Handle passed in.
func (c *Client) Copy(ctx context.Context, cp any, srcVars, dstVars map[string]string) error
func (c *Client) Move(ctx context.Context, mv any, srcVars, dstVars map[string]string) error

// Mkdir returns created=false (nil error) for the idempotent "already
// existed" case — mirrors Backend.Mkdir's own contract exactly (this
// method is a thin pass-through to it).
func (c *Client) Mkdir(ctx context.Context, m any, vars map[string]string) (created bool, err error)

// BatchDelete mirrors S3's real DeleteObjects — ADDED round 4 after a
// full-S3-API-list cross-reference surfaced this gap. UNLIKE every
// other verb (which is all-or-nothing), a batch call can PARTIALLY
// fail: BatchDeleteResult carries ONE result per varsList entry,
// returned IN ORDER, mirroring DeleteObjects' own per-key
// success/failure response shape. err is non-nil ONLY for a
// call-level failure (e.g. Security rejection before ANY key is
// attempted) — a per-key failure lives in its own BatchDeleteResult.Err,
// never escalated to the call-level err. UNLIKE Move (which DELEGATES
// its fallback to Copier[T]'s own dispatch function), BatchDelete's
// fallback calls Backend.DeleteBytes DIRECTLY in a loop, NOT through
// Deleter[T]'s own dispatch — deliberate, not an oversight: Security is
// checked ONCE per BATCH (see "Per-role attachment surface" above), so
// routing each key through Deleter[T]'s FULL dispatch (which would
// re-check Security per key) would be wrong, not just redundant.
func (c *Client) BatchDelete(ctx context.Context, bd any, varsList []map[string]string) ([]BatchDeleteResult, error)

// BatchDeleteResult is one varsList entry's outcome — Existed mirrors
// Deleter's own existed-bool idempotent-absence convention; Err is
// nil for both "deleted" and "already absent," set only for a genuine
// per-key backend failure.
type BatchDeleteResult struct {
    Path    string
    Existed bool
    Err     error
}
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
    // agnostic subset of today's FileOptions that EVERY backend can
    // plausibly honor (see the WriteOptions struct below) — a backend
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
// Resolved round 3 — the fallback's "Copy" half is a full
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

// BatchDeletingBackend is an OPTIONAL extension (ADDED round 4) for a
// native, single-wire-call batch delete (e.g. S3's real DeleteObjects —
// up to 1000 keys per request, ONE round trip, with its own per-key
// success/failure response). Type-asserted by BatchDeleter[T]'s dispatch
// — mirrors CopyingBackend/MovingBackend's EXACT type-assertion-guard
// idiom. A Backend NOT implementing this falls back to N SEQUENTIAL
// DeleteBytes calls (one per varsList entry, each independently
// collected into its own BatchDeleteResult) — filesystem and (Phase 2)
// SMB backends both take this fallback, since neither protocol has a
// genuine batch-delete wire operation; this is the CORRECT, not merely
// adequate, implementation for them (there is nothing faster to fall
// back FROM).
type BatchDeletingBackend interface {
    // DeleteBytesBatch mirrors DeleteObjects' own per-key result shape
    // directly — results[i] corresponds to paths[i], IN ORDER. A
    // non-nil err here is a CALL-LEVEL failure (e.g. the whole request
    // was rejected) — per-key failures live in results[i].Err, exactly
    // like Client.BatchDelete's own contract.
    DeleteBytesBatch(ctx context.Context, paths []string) (results []BatchDeleteResult, err error)
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

// WriteOptions (ADDED explicitly this round — previously only
// prose-described, an inconsistency with MkdirOptions's own full block
// below, now fixed) mirrors today's ports.File FileOptions as closely
// as possible for Create/Patch. Perm/DirPerm/CreateDirs/Strict/DryRun
// are kept VERBATIM from FileOptions (NOT replaced by Capability) —
// see "Capability vs. per-call option precedence" below for how Perm
// interacts with an attach-time AccessMode/NativePOSIXMode Capability
// when BOTH are supplied for the same role.
type WriteOptions struct {
    // Perm is the permission used when creating a new file. Defaults to
    // 0644 when zero — IDENTICAL default to today's FileOptions.Perm.
    Perm os.FileMode
    // CreateDirs, when true, creates any missing parent directories
    // (via Backend.Mkdir) before writing — IDENTICAL semantics to
    // today's FileOptions.CreateDirs, now implemented IN TERMS OF
    // Backend.Mkdir (see Mkdir's own doc comment above) rather than a
    // separate inline os.MkdirAll call.
    CreateDirs bool
    // DirPerm is the permission used for directories CreateDirs creates.
    // Defaults to 0755 when zero. Has no effect when CreateDirs is false.
    DirPerm os.FileMode
    // Strict, when true, requires path to NOT already exist
    // (FileAlreadyExistsError instead of silently overwriting) —
    // IDENTICAL to today's FileOptions.Strict Write-side semantics.
    Strict bool
    // DryRun, when true, reports which directories WOULD be created
    // WITHOUT creating them or writing the file — IDENTICAL to today's
    // FileOptions.DryRun Write-side semantics.
    DryRun bool
}

// DeleteOptions mirrors the backend-agnostic subset of today's
// FileOptions relevant to Delete.
type DeleteOptions struct {
    // Strict, when true, requires path to have existed (FileNotFoundError
    // instead of idempotent "ensure absent" success) — IDENTICAL to
    // today's FileOptions.Strict Delete-side semantics.
    Strict bool
    // DryRun, when true, reports what WOULD happen WITHOUT removing the
    // file — IDENTICAL to today's FileOptions.DryRun Delete-side
    // semantics.
    DryRun bool
}

// MkdirOptions mirrors the backend-agnostic subset of today's
// FileOptions.DirPerm — see "Capability vs. per-call option precedence"
// below for how NativePOSIXMode here interacts with an attach-time
// NativePOSIXMode Capability.
type MkdirOptions struct {
    // NativePOSIXMode, when non-nil, is passed to the backend's own
    // permission-setting call when (and ONLY when) the backend
    // implements POSIX-style permissions at all — see [NativePOSIXMode]
    // capability below. nil means "use the backend's own default."
    NativePOSIXMode *os.FileMode
}
```

**Capability vs. per-call option precedence (resolved this round — Phase
1 pre-implementation review)**: `WriteOptions.Perm`/`MkdirOptions.
NativePOSIXMode` and the `Capability`-based `AccessMode`/`NativePOSIXMode`
(declared at role-attach time, see "`file.Capability`" below) are BOTH
kept — this is NOT redundant, it mirrors `ports.File`'s own existing
per-call `FileOptions` exactly while ALSO offering the new, declarative
attach-time mechanism. When a role has an attach-time `Capability`
value, **the Capability ALWAYS wins** over whatever the per-call option
says — a declared, attach-time contract (visible to anyone reading the
`Resource`'s declaration) must not be silently overridable by a
less-visible per-call option. Concretely: if a role declares
`Capabilities: []Capability{AccessModeReadOnly}` at attach time, a
`Create`/`Delete`/`Move`/`Mkdir` call is rejected at the dispatch layer
regardless of what `WriteOptions`/`DeleteOptions`/`MkdirOptions` says —
there is no per-call escape hatch around a declared `AccessMode`. The
SAME precedence applies to `NativePOSIXMode`: if BOTH an attach-time
`NativePOSIXMode` Capability AND a per-call `MkdirOptions.
NativePOSIXMode` are supplied, the Capability's value is what actually
gets passed to `Backend.Mkdir` — the per-call field is only consulted
when NO attach-time Capability was declared for that role.

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
// purposes — named explicitly round 3 (was previously implied but
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

**Correction (round 3)**: `events.CheckCapabilityCoverage(topic
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
respect" — 4 real, acknowledged divergences, each resolved explicitly
rather than papered over:

| Operation | Filesystem | S3 | SMB |
|---|---|---|---|
| `Mkdir` | Real directory, enforced (`os.MkdirAll`) | NO-OP success — S3 has NO real directories at all; "folders" are a pure key-prefix convention in `ListObjectsV2`'s own delimiter handling, not a backend entity `Mkdir` could meaningfully create | Real directory, enforced (native SMB `MKDIR`) |
| `Move` | ONE atomic syscall (`os.Rename`) — either fully succeeds or the source is untouched | TWO non-atomic calls (`CopyObject` then `DeleteObject`) for STANDARD S3 buckets — a crash/failure between them can leave BOTH copies present; `api/file`'s dispatch core surfaces this as a DOCUMENTED caveat on `Mover[T]`'s own godoc for any backend lacking `MovingBackend`, never silently. **Caveat (found during the round-4 full-API-list cross-reference)**: S3 Express One Zone "directory buckets" (a distinct, less-common bucket type) support a genuinely atomic `RenameObject` operation — Phase 1's `adapters/file`/Phase 2's planned `adapters/s3` do NOT attempt to special-case this rarer bucket type; the blanket "2 non-atomic calls" description remains accurate for the overwhelmingly common STANDARD S3 bucket case this doc targets | ONE native, typically-atomic protocol operation (SMB2's own rename) |
| Permission granularity | Full POSIX mode bits (`NativePOSIXMode`) honored | `AccessMode` only — enforced at `api/file`'s dispatch level, never translated into an S3 ACL/policy call (explicitly out of scope) | `AccessMode` only in Phase 1 (same reasoning — SMB's native ACL model is as rich as Windows's own, not usefully bit-mappable either) |
| `BatchDelete` | Generic fallback ONLY — sequential `DeleteBytes` loop; no native batch-delete syscall exists to fall back FROM | NATIVE — S3's real `DeleteObjects` operation deletes up to 1000 keys in ONE wire call, with its own per-key success/failure response (Phase 2's `adapters/s3` is `BatchDeletingBackend`'s first genuine beneficiary) | Generic fallback in Phase 1/2 scope — no native SMB2 batch-delete operation surveyed; same reasoning as filesystem |

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

**Added round 3** (alongside `file.Client` above): 3 NEW error
types, each a structurally identical sibling of an EXISTING `api/events`
error — reusing the convention, not the code (same reasoning as the
`CheckCapabilityCoverage` correction below: each pattern family owns its
own copy):
- `BackendAlreadyAttachedError{}` — mirrors
  `events.TransportAlreadyAttachedError{}`; returned by a 2nd
  `Client.Attach` call.
- `NoBackendAttachedError{}` — mirrors
  `events.NoTransportAttachedError{}`; returned by `Read`/`Create`/
  `Patch`/`Delete`/`List`/`Exists`/`Copy`/`Move`/`Mkdir`/`BatchDelete`
  when `Attach` was never called.
- `BackendTypeMismatchError{Expected, Got string}` — mirrors
  `events.TransportTypeMismatchError`; returned when `val`/`patch`'s
  dynamic type doesn't match the role's declared `T`/`P`.

No further NEW error types needed for the 4 round-2 verbs — `Exists`
reuses `FileNotFoundError`'s ABSENCE of an error (the `(Entry{}, false,
nil)` case) rather than introducing one; `Copy`/`Move` reuse
`FileReadError`/`FileWriteError`/`FileDeleteError` for their respective
halves (whichever actually failed, fast path or fallback); `Mkdir` reuses
`FileWriteError` (mirrors `ports.File.Write`'s own existing
`CreateDirs`-failure error type exactly, since `Mkdir` was always
implicitly part of that error's scope). `BatchDelete` (round 4) ALSO
needs no new type — its CALL-LEVEL `err` reuses whatever role-level
failure applies (e.g. a Security rejection), and its PER-KEY
`BatchDeleteResult.Err` reuses `FileDeleteError` exactly like a single
`Delete` call would, since each batched key is, individually, the same
operation `Deleter[T]` already performs — only the fan-out/collection
wrapper (`BatchDeleteResult`) is new, not the underlying failure
vocabulary.

## Observer integration

Reuses `stats.FileObserver` (`RecordFileRead`/`RecordFileWrite`/
`RecordFileDelete`) for Read/Create/Patch/Delete — identical call sites
to `ports.File`'s existing ones, just now fired from `api/file`'s
dispatch core instead of inline. The expanded verb set across rounds 2
and 4 needs 6 new methods total — batched into ONE breaking round rather
than spread across future revisions, since `FileObserver` already
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
    // RecordFileBatchDelete is called ONCE per WithBatchDelete call
    // (NOT once per key) — mirrors RecordFileList's own
    // once-per-call-not-once-per-item convention. successCount/
    // failureCount are derived from the returned []BatchDeleteResult;
    // a call-level failure (err != nil from Client.BatchDelete) reports
    // successCount=0, failureCount=len(varsList).
    RecordFileBatchDelete(paths []string, successCount, failureCount int, duration time.Duration)
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
  **Resolved this round (Phase 1 pre-implementation review) — the
  skill's own mandatory "Router composition" pre-implementation check**
  (`plan-a-new-codex-feature`'s worked `rest.Redirect` bug precedent:
  never let a construction site silently skip Router-group prefixing):
  path composition has exactly ONE construction point, matching
  `file.Client`'s dual-accept design above — a Router-grouped role is
  registered via `reader.Handle(client, file.WithRouter(router))` ONCE,
  producing a `*ReadHandle[T]` that already carries the resolved,
  prefixed path; every `Client.Read(ctx, readHandle, vars)` call
  downstream reuses THAT Handle, never re-deriving the path or
  re-supplying `WithRouter(...)`. A caller CANNOT forget to apply
  Router-group composition at a later call site, because there is no
  later construction site at all — passing the bare, non-Router-grouped
  `Reader[T]` builder directly to `Client.Read` (the common case) is the
  ONLY other option, and it is unambiguous: no Router group was ever
  involved. Needs its own "Design decisions — resolved for `api/file`"
  subsection appended to `d-0008` once implemented (the SAME
  per-pattern-closed-decisions structure that doc already uses 3 times).
- **Declarative Middleware**: reuses `middleware.Middleware[In,Out]`/
  `BoundMiddleware`-style codec-backed attachment (`d-0003`/`d-0007`),
  realized here as `ReadBoundMW`/`CreateBoundMW`/`PatchBoundMW`/
  `DeleteBoundMW`/`ListBoundMW`/`ExistsBoundMW`/`CopyBoundMW`/
  `MoveBoundMW`/`MkdirBoundMW`/`BatchDeleteBoundMW` (the last added round
  4, alongside `BatchDeleter[T]`) — one per role, mirroring
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
| `TestBatchDeleter_Handle_PartialFailure_ReturnsPerKeyResults` | A `varsList` with a mix of existing/absent/permission-denied paths returns ONE `BatchDeleteResult` per entry, IN ORDER, with the call-level `err` staying `nil` |
| `TestBatchDeleter_UsesNativeBatchDeletingBackendWhenAvailable` | A fake `Backend` ALSO implementing `BatchDeletingBackend` has `DeleteBytesBatch` called ONCE with all paths, never N individual `DeleteBytes` calls |
| `TestBatchDeleter_FallsBackToSequentialDeleteBytes` | A fake `Backend` NOT implementing `BatchDeletingBackend` gets N individual `DeleteBytes` calls, one per `varsList` entry, each independently collected into its own result |
| `TestFileObserver_RecordFileBatchDelete_CalledOnceNotPerKey` | `RecordFileBatchDelete` fires exactly once per `BatchDelete` call (mirrors `RecordFileList`'s own once-per-call convention), with correct `successCount`/`failureCount` |
| `TestClient_Read_AcceptsRawBuilderOrPreBuiltHandle` | `Client.Read` produces the IDENTICAL result whether passed a bare `Reader[T]` or a pre-built `*ReadHandle[T]` for a NON-Router-grouped `Resource` — proves dual-accept is a true no-op distinction in the common case |
| `TestClient_Read_RouterGroupedResource_RequiresPreBuiltHandleForCorrectPath` | A `Resource` registered under a `Router.Group` built via `reader.Handle(client, file.WithRouter(router))` resolves the CORRECT, prefixed path; passing the BARE (non-Router-grouped) builder for the SAME resource resolves the UN-prefixed path instead — proves the dual-accept design's path-composition contract is exactly as documented, not accidentally identical in both cases |
| `TestWriteOptions_Perm_OverriddenByAttachTimeAccessModeCapability` | A role declaring `AccessModeReadOnly` at attach time rejects a `Create`/`Write` call regardless of what `WriteOptions` says — proves the "Capability always wins over per-call option" precedence rule |
| `ExampleResource_read` / `ExampleResource_create` / `ExampleResource_copy` | pkg.go.dev-facing worked examples |

## Files to create

| File | Responsibility |
|---|---|
| `api/file/builder.go` | `Resource[T]`, `PathParam`/`MergedPathParam`, `Reader`/`Creator`/`Patcher`/`Deleter`/`Lister`/`Exister`/`Copier`/`Mover`/`MkdirCreator`/`BatchDeleter`, `Read`/`Create`/`Patch`/`Delete`/`List`/`Exists`/`Copy`/`Move`/`Mkdir`/`BatchDelete` metadata structs, per-role `*Handle[T]` registry types |
| `api/file/client.go` | `Client`/`NewClient`/`ClientOpt`, `Client.Attach(Backend) error` (exclusive attach-time wiring), `Client.Read`/`Create`/`Patch`/`Delete`/`List`/`Exists`/`Copy`/`Move`/`Mkdir`/`BatchDelete` (reflective `any`-typed call-time dispatch), `BatchDeleteResult` — mirrors `events.Client`/`reqreply.Client`'s established `Attach`+dispatch shape |
| `api/file/backend.go` | `Backend` interface (incl. `Stat`/`Mkdir`), `CopyingBackend`/`MovingBackend`/`BatchDeletingBackend` optional extensions, `Entry`, `WriteOptions`/`DeleteOptions`/`MkdirOptions` |
| `api/file/capability.go` | `Capability`/`CapabilityName`/`CheckCapabilityCoverage`/`WireAttributes`/`AccessMode`/`NativePOSIXMode` — mirrors `adapters/mqtt5/capability.go`+`api/events/capability.go`'s combined shape, one boundary over |
| `api/file/dispatch.go` | The shared dispatch core every `Client` call-time method delegates to — path build/validate, codec encode/decode, merge-field application, `AccessMode` enforcement, `CopyingBackend`/`MovingBackend`/`BatchDeletingBackend` type-assertion-guarded fast paths with generic fallback (incl. `Move`'s 3-level cascade delegating to `Copier[T]`'s own fallback, and `BatchDelete`'s native-vs-sequential-loop fallback), Observer calls, ErrorPattern dispatch |
| `api/file/errors.go` | Relocated error types (from `ports/file.go`) + `BackendUnsupportedOptionError` + `BackendAlreadyAttachedError`/`NoBackendAttachedError`/`BackendTypeMismatchError` |
| `api/file/error_pattern.go` | `ErrorPattern[E,B]` — mirrors `rest.ErrorPattern`/`events.ErrorPattern`/`reqreply.ErrorPattern`'s identical shape |
| `api/file/doc.go` | Package godoc |
| `adapters/file/backend.go` | `os.*`-backed `file.Backend` implementation, ALSO implementing `CopyingBackend` (`io.Copy`) and `MovingBackend` (`os.Rename`) — a genuine fast path for both. Deliberately does NOT implement `BatchDeletingBackend`: a POSIX filesystem has no native batch-delete syscall, so a filesystem-side "implementation" would be the IDENTICAL sequential loop the generic fallback already runs — pure redundant code with zero benefit. `BatchDeletingBackend`'s interface itself is proven via a fake `Backend` in unit tests (`TestBatchDeleter_UsesNativeBatchDeletingBackendWhenAvailable`); its first REAL implementation arrives with Phase 2's `adapters/s3` (S3's actual `DeleteObjects` wire call) — new file; existing `binding.go`/`adapter.go` untouched |
| `ports/file.go` | `File[T]`/`Dir` methods rewritten to delegate to an attached `api/file` Handle + `Backend`, instead of calling `os.*` inline; public call signatures UNCHANGED |
| `ports/pattern.go` | `FilePattern`/`PluginFilePattern` updated to build/bind against the new `api/file.Resource` Handle |

## Phase 2 — outline (further backends + feature depth)

**Status: outlined this round, NOT designed to Phase 1's Go-signature
rigor yet** — per an explicit choice: full design is deferred until
Phase 1 ships and its abstractions (`Backend`, the 3 optional extension
interfaces, `Capability`) are proven against a real implementation
first, rather than designing a 2nd/3rd backend against an UNPROVEN seam.
This section replaces round 2's original "`adapters/s3`/`adapters/smb`
get their own future roadmap doc" call — ALL of Phase 2 is now tracked
in THIS doc, reversed this round.

### `adapters/s3`

The REAL, primary beneficiary of every optional extension interface
designed in Phase 1: `CopyingBackend.CopyBytes` → `CopyObject` (a true
server-side copy, zero bytes through the caller — Phase 1's filesystem
`io.Copy` already avoids a decode/re-encode round-trip but still reads
every byte through the process; S3's `CopyObject` doesn't even do that);
`MovingBackend` → deliberately NOT implemented (see "Backend semantic
differences" — standard S3 buckets have no atomic rename; the 3-level
`Move` fallback cascade's level 2 output, Copy+Delete, is the CORRECT
behavior for S3, not a missing feature); `BatchDeletingBackend.
DeleteBytesBatch` → `DeleteObjects` (the real motivating case for this
whole interface). Key open design question for the eventual full
design: how does `AccessMode`'s dispatch-level enforcement (reject
Write/Delete/Move/Mkdir before any Backend call, per Phase 1's design)
interact with S3's OWN IAM-level authorization — does a `ReadOnly`
`AccessMode` additionally verify the attached AWS credentials actually
lack write permission, or does it trust the declared `Capability`
unconditionally? Not resolved here.

### `adapters/smb`

Needs its own driver-selection research (Go's SMB client library
landscape) before a design can begin — not yet investigated. Expected to
implement `MovingBackend` (SMB2 has a native rename) but NOT
`BatchDeletingBackend` (no native SMB2 batch-delete surveyed, per
"Backend semantic differences"). Permission handling is the more
interesting open question: SMB shares typically inherit the HOST's own
permission model (Windows ACLs on a Windows-hosted share, POSIX-mapped
on a Samba-hosted one) — whether `adapters/smb` should attempt BOTH
`AccessMode` AND a best-effort `NativePOSIXMode` passthrough (for
Samba-hosted shares specifically) is an open question for the eventual
full design, not resolved here.

### Streaming/chunked reads and writes

Phase 1's `Backend.ReadBytes`/`WriteBytes` are whole-file, `[]byte`-at-
a-time, matching `ports.File`'s own existing all-at-once contract — this
remains correct for Phase 1's target use cases (configuration files,
small-to-medium records) but doesn't scale to large media/archive
files. A future `io.Reader`/`io.Writer`-based `Backend` variant (likely
a SEPARATE optional extension interface, following the SAME
type-assertion-guard idiom as `CopyingBackend`/`MovingBackend`/
`BatchDeletingBackend` — a `Backend` not implementing it keeps working
via whole-file reads) is the likely shape, but the exact interface
(a single `io.Reader`-returning method vs. a chunked offset/length
pagination API) is not decided.

### Conditional writes (ETag/If-Match optimistic concurrency)

No backend-agnostic concept decided yet. S3 has native `ETag`/
`If-Match` support built into `PutObject`/`GetObject`/`CopyObject`;
filesystem has no native equivalent (would need to be emulated via a
content hash compared before `WriteBytes`, which is NOT atomic the way
S3's server-side `If-Match` check is — a real cross-backend semantic
gap, not just an API-shape question). Likely surfaces as a new
`WriteOptions.IfMatch *string` field plus a matching optional extension
interface for backends with a genuine atomic check, but this is a
bigger design question than the other Phase 2 items and is flagged as
such, not pre-decided.

### Recursive `WithList`

Today's `ports.Dir.List` already supports a recursive mode — Phase 1's
`Lister[T]` is single-level only. The most straightforward Phase 2 item:
likely a `ListOptions{Recursive bool}` parameter on `Client.List`, no new
`Backend` method needed (composed via repeated `ListDir` calls exactly
like `ports.Dir.List` already does it) — the lowest-risk, most
"obviously how it would work" item in this whole section.

## Phase 3 — adapt examples to the new `api/file` approach

**Status: planned, necessarily light since Phase 1 isn't implemented
yet.** Once Phase 1 ships, every existing example currently using
`ports.File`/`ports.Dir`/`adapters/file` directly needs review against
the "Removing an old API" checklist (`plan-a-new-codex-feature` skill) —
`ports.File[T]`'s public call signatures stay UNCHANGED per this doc's
own Scope decisions, so most examples should need ZERO changes; any
example relying on an internal detail (e.g. direct `os.*` error
matching instead of `errors.As` against `ports.File`'s own typed
errors) would need updating. A concrete example inventory (which files,
which patterns) is deferred to when Phase 1 is actually implemented,
since grepping for `ports.File`/`adapters/file` usage NOW, before any
code changes, would produce a list that's likely stale by the time
Phase 3 actually starts.

## Phase 4 — promote to `docs/design/` + sync instructions/skills

**Status: planned, mechanism named, exact content deferred.** Once
Phases 1-3 ship, this roadmap doc qualifies for the
`plan-a-new-codex-feature` skill's "Promote to `docs/design/`" outcome
(not "Delete" — this establishes a pattern `ports`/future adapters are
expected to follow, the skill's own stated bar for promotion): renumber
to `docs/design/d-NNNN-file-api-backend-transparency.md` (exact `NNNN`
assigned at promotion time, per `docs/design/index.md`'s own
chronological-assignment convention — NOT reserved now, since other
design docs may be added before this ships), rewrite the `> Status:`
header to "Implemented — architectural foundation," remove from
`docs/roadmap/index.md`/`zensical.toml`'s roadmap nav, add to
`docs/design/index.md` + `zensical.toml`'s `[nav."Design Documents"]`.
Sync targets: `.github/instructions/go-codex.instructions.md`'s Package
Structure table (new `api/file` row + `adapters/file`'s updated
responsibility description), `docs/reference/project-structure.md`,
`docs/features/`/`docs/guides/` pages for the new package, and a check
of `add-a-new-adapter`/`plan-a-new-codex-feature` skill cross-references
for staleness once the pattern has shipped.

## Out of scope (genuinely excluded, not deferred to Phase 2)

Cross-referencing the FULL AWS S3 API operations list (round 4, not just
the 6-op core survey from round 2) surfaced several object-level
operations with NO filesystem/SMB equivalent, or that operate ABOVE the
per-`Resource[T]` level `api/file` targets — these are genuinely
excluded, not merely deferred:

- `RestoreObject` — a storage-CLASS/tiering concept (restoring from
  Glacier); no filesystem/SMB equivalent at all.
- `SelectObjectContent` (S3 Select) — a SQL-like QUERY capability over
  object content, categorically different from a file I/O verb.
- Object Lock / Legal Hold / Retention (`Put/GetObjectLegalHold`,
  `Put/GetObjectRetention`, `Put/GetObjectLockConfiguration`) — a
  compliance/WORM feature, no filesystem/SMB equivalent.
- Object Annotations (`Put/Get/DeleteObjectAnnotation`,
  `ListObjectAnnotations`) — a newer, structured queryable-metadata
  feature; same exclusion rationale as object tagging (both are
  metadata-sidecar concepts, not file content/structure operations).
- Object Tagging (`Put/Get/DeleteObjectTagging`) and fine-grained
  ACL/grantee modeling (`Put/GetObjectAcl`) — carried over from round 2,
  unchanged.
- The entire BUCKET-level configuration family (CORS, lifecycle,
  replication, encryption-at-rest configuration, logging, notifications,
  static-website hosting, bucket policy, request-payer, versioning-at-
  the-bucket-level, analytics/inventory/metrics configuration, S3
  Express session management, bucket metadata-table configuration) plus
  S3-infrastructure-specific plumbing (`GetObjectTorrent`,
  `WriteGetObjectResponse`, `UpdateObjectEncryption`, `CreateSession`) —
  all operate at the BUCKET or S3-SERVICE level, never the per-Resource
  level `api/file` is designed around; `api/file` has no concept of "a
  bucket" at all, by design.

## Open design decisions

Mostly resolved for Phase 1. Genuinely open items — 1 carried over, plus
every Phase 2 item is DELIBERATELY left at outline depth (see "Phase 2"
above for each item's own specific open questions, not repeated here):

- **`MkdirOptions`'s exact field set beyond `NativePOSIXMode`** — e.g.
  whether a `DryRun`-equivalent (mirrors `WriteOptions.DryRun`'s existing
  "report what would happen" contract) is worth adding to `Mkdir`
  directly, versus relying on `WithMkdir`'s caller composing their own
  dry-run check via `WithExists` first. Leaning toward adding `DryRun`
  for parity with every other verb's option struct, but not pre-decided
  — resolve during Implement mode once the full option-struct shapes are
  drafted together.
- Phase 2's 5 items each carry their OWN specific open question (S3's
  `AccessMode`-vs-IAM interaction, `adapters/smb`'s driver/permission
  choices, the streaming-`Backend`-extension's exact interface shape,
  conditional-writes' atomicity gap, none for recursive `WithList` which
  is the lowest-risk item) — see "Phase 2 — outline" above; not resolved
  here by design, since full Phase 2 design is explicitly deferred until
  Phase 1 ships.

**Resolved round 5** (a critical pre-implementation consistency review
of Phase 1's ENTIRE design, done explicitly BEFORE planning Phase 1's
implementation, per the skill's own Mode-3 pre-implementation checklist
— found 3 major + 4 minor issues accumulated across rounds 2-4):

- **`file.Client`'s dispatch-method parameter contract was factually
  wrong about its own cited precedent** — claimed every role parameter
  MUST be pre-built via `.Handle(client)`; re-verified against the REAL
  `events`/`reqreply` code and found the opposite (the raw builder is
  passed directly in the common case). **Resolved as "dual-accept"**:
  every dispatch method accepts EITHER the bare role builder OR an
  explicitly pre-built Handle, mirroring `reqreply.Client.Call`'s own
  confirmed dual-acceptance exactly — see "`file.Client`" above for the
  corrected doc comments.
- **The skill's own mandatory "Router composition" pre-implementation
  check was left completely unanswered** — resolved as a direct
  consequence of the dual-accept design above: a Router-grouped role is
  registered via `.Handle(client, file.WithRouter(router))` ONCE,
  producing a Handle that already carries the resolved path; every
  subsequent dispatch call reuses THAT Handle — exactly ONE path-
  construction point, eliminating the `rest.Redirect`-shaped bug class
  the skill's check exists to catch. See "Router/Middleware integration"
  above for the full resolution.
- **`NativePOSIXMode` had two entry points (`Capability` vs. per-call
  `MkdirOptions` field) with no stated precedence**, and
  `WriteOptions`/`DeleteOptions` were referenced constantly but never
  actually shown as Go structs (an inconsistency with `MkdirOptions`'s
  own full block). **Resolved**: both `WriteOptions`/`DeleteOptions` now
  get explicit struct blocks (keeping `Perm`/`DirPerm`/`CreateDirs`/
  `Strict`/`DryRun`, matching today's `ports.File.FileOptions` closely);
  "**Capability always wins over per-call option**" is now the
  explicitly stated precedence rule for BOTH `AccessMode` (vs. any
  write/delete attempt) and `NativePOSIXMode` (vs.
  `MkdirOptions.NativePOSIXMode`) — see "`file.Backend`"'s new
  "Capability vs. per-call option precedence" subsection above.
- 4 minor staleness fixes also applied: Motivation's verb list and
  `NoBackendAttachedError`'s enumeration both now include `BatchDelete`;
  the "Toolchain" section no longer contradicts the Phase 2
  restructuring; `BatchDelete`'s intentionally-non-delegating fallback
  (unlike `Move`'s delegation to `Copier[T]`) is now explicitly
  contrasted and justified in `Client.BatchDelete`'s own doc comment.

**Resolved round 4** (an S3 verb-completeness audit against the FULL,
authoritative AWS API operations list — not just the round-2 6-op core
survey):

- **`BatchDeleter[T]`** added as a genuine new Phase 1 verb, mirroring
  S3's real `DeleteObjects` batch operation — distinct from
  `ports.Dir.Delete`'s single-directory recursive delete; see
  "`file.Client`"/"`file.Backend`" above for the full design
  (`BatchDeletingBackend` optional extension, `BatchDeleteResult`
  per-key outcome type, 3-level-cascade-shaped Observer method).
- **Roadmap restructured into 4 phases**, all tracked in this ONE doc —
  reverses round 2's "`adapters/s3`/`adapters/smb` get their own future
  roadmap doc" call. Phase 2 (further backends + feature depth) is
  OUTLINED, not fully designed; Phase 3 (examples) and Phase 4
  (`docs/design/` promotion + instructions/skills sync) are named and
  planned, not yet started.
- **`Move`'s "S3 has no native move" claim caveated**: S3 Express One
  Zone directory buckets support a genuine atomic `RenameObject` — the
  blanket claim remains accurate for the overwhelmingly common standard-
  S3-bucket case this doc targets, but is no longer stated as a
  universal S3 fact.
- **"Out of scope" expanded and re-grounded** in the FULL S3 API survey:
  `RestoreObject`, `SelectObjectContent`, Object Lock/Legal Hold/
  Retention, and Object Annotations are now named explicitly (previously
  only a vague "versioning, tagging, ACL" existed), alongside a
  collective exclusion of the entire bucket-level/S3-infrastructure
  operation family.

**Resolved round 3** (a dedicated critical review pass found these 3
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
- Verb scope (refined round 2, extended round 4 with `BatchDeleter[T]`): Read, Create, Patch, Delete, List,
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
