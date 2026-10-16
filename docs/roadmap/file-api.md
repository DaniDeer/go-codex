# File API — `api/file`

> **Status:** Phase 1 IMPLEMENTED WITH FULL ROUTER + DECLARATIVE MIDDLEWARE
> PARITY (a dedicated round closed both of this doc's own named Phase 1
> scope reductions). The `api/file` package now has: all 10 roles, `Client`
> dual-accept dispatch, the `Capability` model, `ErrorPattern` — now
> **AUTO-INVOKED** by the dispatch core (matches `rest`/`events`/`reqreply`
> exactly, closing the scope reduction this doc previously noted) —
> Observer integration, a genuine **`file.Router`** value type (mirrors
> `events.Router` method-for-method; all 10 roles implement
> `internal/router.Routable[*Client]`, closing the OTHER previously-noted
> scope reduction), full **Declarative Middleware** (`Bound<Role>Middleware`
> per role + `XxxBoundMW` attach methods, genuinely executed via
> `runBoundMiddleware` before every Backend call, with real
> Security/GrantedScopes dispatch), and a NEW **file-metadata merge-field
> axis** (`MetadataBackend`/`MetadataParam`/`MergedMetadataParam[T]` — the
> "extra wire data" axis BoundMiddleware's `In`/`Out` draws from, since path
> vars are already fully covered by `PathParam`). `adapters/file.Backend`
> (the filesystem implementation, including `CopyingBackend`/
> `MovingBackend`/`MetadataBackend` fast/real paths) is fully built and
> tested. A new 5th API-defined conformance-test package,
> `api/file/filetest` (mirrors `resttest`/`eventstest`/`reqreplytest`'s
> 7-scenario taxonomy), is wired into `adapters/file` and caught 2 real,
> previously-undetected bugs along the way (see "Open design decisions").
> See `docs/features/file-api.md` and `examples/file-api/main.go` for the
> full, updated worked example.
>
> **Stage F — DONE for `ports.File[T]`'s `Read`/`Write`/`Delete`** (the
> only 3 methods that called `os.*` directly; `Update`/`Patch`/
> `ReadMerged`/`WriteHandle` compose them and needed no changes of their
> own). Before this could be migrated, a "Stage F0" sub-round closed
> several real parity gaps `api/file`'s dispatch core had relative to
> `ports.File`'s existing, tested behavior: TraceObserver spans were
> entirely unwired (now fixed — every dispatch-core function starts/ends
> a span); `createdDirs` only reported the immediate parent directory,
> not every missing ancestor (now fixed, matches `ports.File.Write`'s own
> multi-level walk); `Client.Create`/`Patch`/`Delete`/`Mkdir` had NO
> per-call options parameter at all — only attach-time `Capability` could
> set `Perm`/`CreateDirs`/`Strict`/`DryRun` (now fixed via an optional
> trailing variadic `...WriteOptions`/`...DeleteOptions`/`...MkdirOptions`
> parameter on each); `DryRun`+`Strict` on Write/Delete didn't combine
> correctly (now fixed to check Strict even under DryRun, in BOTH
> `adapters/file.Backend` and the migration's own private backend);
> `NewResource` didn't panic on multiple `"**"` glob segments like
> `ports.NewFile` does (now fixed). `ports.File[T]`'s 12 error types are
> now `type X = file.X` aliases onto `api/file`'s own identically-shaped
> ones (verified field-for-field first). Because `adapters/file/binding.go`
> itself imports `ports` (for its `SourceAdapter`/`SinkAdapter`/`IOAdapter`
> bindings), `ports` cannot import `adapters/file` without an import cycle
> — `ports/file_backend.go` defines a small, private, unexported
> `osBackend` implementing `file.Backend`, a deliberate, disclosed
> near-duplicate of `adapters/file.Backend`'s os.*-backed logic, used ONLY
> by this delegation. `File[T]` builds a FRESH `Resource[T]`+`Client` on
> every call rather than caching one (confirmed: `File[T]` is an immutable
> value struct with value-receiver methods, so a cached `*Client`/
> `sync.Once` field would risk a copy-after-first-use hazard; building
> fresh is cheap — no I/O, pure Go struct construction). The full
> pre-existing `ports/file_test.go` (2281 lines) suite passes UNMODIFIED —
> the regression proof. **Two small, disclosed, untested-by-existing-tests
> behavior differences remain** (neither test-enforced, so neither blocks
> this migration): (1) `FileDeleteError`'s underlying `existed` value on a
> generic (non-not-found) `os.Remove` failure was hardcoded `true` in
> `ports.File.Delete`'s original code; the delegated path returns `false`
> instead (more semantically honest) — **user explicitly confirmed KEEPING
> this as an intentional, accepted deviation** rather than restoring the
> old quirk; (2) ~~the original `Strict` Write used an atomic
> `O_CREATE|O_EXCL` open...~~ **RESOLVED (see below)**.
>
> **Strict-Write atomicity fix (this round)**: the `os.Stat`-then-
> `os.WriteFile` TOCTOU race noted above is now FIXED in BOTH
> `ports`'s private `osBackend` and the public `adapters/file.Backend` —
> both now use the original atomic `os.OpenFile(path,
> os.O_WRONLY|os.O_CREATE|os.O_EXCL, perm)` open for a `Strict` write
> (DryRun+Strict still uses a preview-only `os.Stat` check, correctly,
> since nothing is actually written under DryRun). A genuine correctness
> fix, applied to both backends since the race was never ports-specific.
>
> **Stage F — DONE for `ports.Dir` too (this round)**. Two NEW optional
> `api/file.Backend` extensions were designed and implemented to make
> this possible — `ports.Dir` is UNTYPED (no codec/Resource/Client), so
> it consumes these DIRECTLY, bypassing `api/file.Client`/dispatch.go
> entirely (no Capability/BoundMiddleware/Router/ErrorPattern involved,
> unlike `ports.File`'s migration):
> - **`WalkingBackend.WalkDir(ctx, root, fn DirWalkFunc) error`** — a
>   generic, callback-based recursive directory walk mirroring
>   `filepath.WalkDir`'s own shape (including honoring
>   `filepath.SkipDir` returned from `fn`). ONE shared primitive serves
>   THREE different `ports.Dir` callers: `Dir.List`'s recursive mode,
>   `Dir.List`'s glob-discovery mode (walks looking for directories
>   matching a glob-enabled template), and `Dir.Delete`'s recursive
>   affected-path collection (replacing the old standalone
>   `collectAllPaths` helper, now removed as redundant).
> - **`DirDeletingBackend.DeleteDir(ctx, path string, recursive, dryRun
>   bool) (affected []string, err error)`** — directory delete with
>   recursive/non-empty-check/dry-run semantics, relocating
>   `Dir.Delete`'s existing, tested logic into the Backend unchanged. A
>   new `DirNotEmptyError{Path}` type lives in `api/file` for this
>   (Backend-facing); `ports.Dir.Delete` converts it back to its OWN,
>   unchanged, public `ports.DirNotEmptyError` via `errors.As` — NOT
>   aliased (unlike `ports.File`'s error types), since `ports.Dir` never
>   goes through `api/file`'s dispatch/error-wrapping machinery at all.
> - Both implemented in `adapters/file.Backend` (public) AND `ports`'s
>   private `osBackend` (same deliberate, disclosed near-duplication
>   rationale as every other `osBackend` method — the `adapters/file`
>   `->` `ports` import-cycle constraint is unchanged).
> - `Dir.List`'s single-level, non-glob case needed NO new Backend
>   method at all — it already mapped cleanly onto the EXISTING
>   `Backend.ListDir`/`Backend.Stat`/`Backend.Mkdir`.
> - The full pre-existing `ports/dir_test.go` (1089 lines) passes
>   UNMODIFIED — the regression proof, mirroring `ports/file_test.go`'s
>   role in the prior round.
> - `ports/pattern.go` (`FilePattern`/`PluginFilePattern`) needed ZERO
>   code changes — it already builds a `File[T]` via `NewFile(...)`,
>   which was ALREADY migrated; confirmed, not re-implemented.
> - **NOT changed this round**: `api/file.Client.List` itself stays
>   single-level-only — `WalkingBackend` is a new primitive `ports.Dir`
>   consumes directly; wiring recursive listing into `Client.List`
>   remains Phase 2 scope (the "Recursive WithList" outline below),
>   now easier thanks to this primitive existing, but not implemented
>   here.
>
> **Phase 1's own "Open design
> decisions" is now EMPTY** —
> round 7 closed both remaining items (BoundMiddleware's metadata
> integration for `Create`/`Patch`; `List`'s path-var merge field
> support) — see "Resolved round 7" below. Phase 2 outlined (full design
> deferred until Phase 1's ports migration ships). Phases 3-4 planned.
> See "Roadmap phases" below for the full picture.
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
`Capabilities: []Capability{AccessModeReadOnly}` at attach time, ANY
mutating role's call — `Create`/`Patch`/`Delete`/`Copy`/`Move`/`Mkdir`/
`BatchDelete` (every role whose dispatch calls a Backend write/delete
method; this list is deliberately EXHAUSTIVE, not illustrative — a
`deep-dive-bug-hunt` pass found `Patch` silently missing from the
dispatch-layer enforcement and fixed it, see `history.md` Round 217) —
is rejected at the dispatch layer regardless of what
`WriteOptions`/`DeleteOptions`/`MkdirOptions` says — there is no
per-call escape hatch around a declared `AccessMode`. The
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

## Phase 1 amendment — `List`/`BatchDelete` per-entry Security (post-ship revision)

> **Status: IMPLEMENTED.** `ListResult[T]`, `listCore`'s per-entry
> Security gating (passing the actual decoded value), and
> `batchDeleteCore`'s per-path Security gating all shipped exactly as
> designed below — see the "Existing-test impact" subsection for the
> concrete rewrites this required. This was a retroactive amendment to
> ALREADY-SHIPPED Phase 1 behavior (`List` and `BatchDelete`'s
> Security-gate semantics), surfaced while re-reviewing Recursive
> `WithList`'s design (below) for gaps — not new Phase 2 scope, tracked
> here as its own section since it changed existing, tested code.
> Recursive `WithList` is DESIGNED ON TOP of this amended model, not the
> original once-per-call one — its own implementation is a separate,
> later round.

### Motivation

Today, `List`'s Security/`BoundMiddleware` gate (`runBoundMiddleware`)
runs EXACTLY ONCE per `Client.List` call, against the single resolved
parent directory's path, with `v=nil` (no entry-specific value — `List`
has never given `BoundMiddleware`'s `Fn` the actual decoded value for
any entry). `BatchDelete`'s gate is identical in spirit: once per
*batch*, not once per path, with `path=""` (no single path to label
it). Both were deliberate, documented Phase 1 choices (mirroring
`BatchDelete`'s own "security checked once per batch, not once per
item" doc comment) — but re-examining them surfaced a real gap:
`List`/`BatchDelete` are the ONLY 2 roles that touch MULTIPLE,
independently-addressable resources in one call, yet their Security
model has no way to express "resource A in this call is fine, resource
B is not" — a single check covers everything, or nothing.

### `List`'s new per-entry model

- **New public type**: `ListResult[T]{Value T, Err error}` —
  `Client.List`'s boxed `any` return value is now `[]ListResult[T]`
  instead of `[]T`. **Breaking change**: every existing caller doing
  `gotAny.([]T)` must change to `gotAny.([]ListResult[T])` and extract
  `.Value`/`.Err` per entry.
- **Security gating moves from once-per-call to once-PER-ENTRY**:
  `runBoundMiddleware` is invoked once per discovered+decoded entry,
  passing that entry's ACTUAL DECODED VALUE as `v` (not `nil`) — a
  genuinely new capability: `BoundMiddleware`'s `Fn` can finally reason
  about an entry's own decoded content (e.g. a `Sensitive bool` field),
  not just "is this role allowed to run at all."
- **ALL per-entry failures become that entry's own `ListResult[T].Err`**
  — decode errors (`FileDecodeError`), merge-field mismatches
  (`FilePathMismatchError`/codec errors), metadata-read failures
  (`FileReadError`/`FileDecodeError`), AND Security rejections
  (`SecurityRejectedError`) all follow ONE consistent model: `List`
  NEVER aborts mid-walk for a per-entry problem anymore — it always
  returns every discovered entry, successes and failures together, in
  declaration/discovery order. A WALK-LEVEL error (the parent directory
  itself can't be read/walked at all — `FileReadError` on the top-level
  `ListDir`/`WalkDir` call) still fails the WHOLE call with a plain
  `error` return (`nil` for the `[]ListResult[T]` slice) — there is no
  entry to attach a walk-level failure to.
- **Observer**: `RecordFileList(path, success, count, duration)`'s
  `count` now reflects the TOTAL number of `ListResult[T]` entries
  returned (successes AND failures together, not just successes);
  `success` reflects whether the WALK itself completed (a call-level
  concern — `false` only on a walk-level abort, never because one
  entry's own `Err` is set). `stats.ReportErrors` is called once per
  per-entry decode/encode-shaped failure (reusing Round 216's fix),
  keyed to each entry's own path, same as every other per-entry
  validation-error report in this package.
- **`ports.Dir` is UNAFFECTED** — it has its own, separate, untyped
  `List` method and never calls `api/file.Client.List` at all.

### `BatchDelete`'s new per-path model

- Per-PATH Security check (not once-per-batch) — each path in the
  batch gets its OWN `runBoundMiddleware` invocation (still `v=nil`;
  `Delete` never decodes a value, so there is nothing richer to pass
  here, unlike `List`).
- **NO new type needed** — `BatchDeleteResult{Path, Existed, Err}`
  already has a per-path `Err` field; a Security rejection for one path
  becomes `BatchDeleteResult{Path: p, Existed: false, Err:
  SecurityRejectedError{Path: p, ...}}`, exactly like any other
  per-path `DeleteBytes` failure already works today — other paths in
  the same batch proceed independently, unaffected.

### Existing-test impact (explicitly flagged, not silently absorbed)

Both changes are DELIBERATE behavior changes to shipped code, not
silent regressions — but they DO mean existing tests asserting the OLD
once-per-call/fail-whole-call semantics need REWRITING, not just
additive new tests:
- Any existing `List` test asserting "a single bad entry fails the
  whole call" must be rewritten to assert that entry's own
  `ListResult[T].Err` is set while sibling entries still succeed.
- Any existing `List`/`BatchDelete` Security test asserting "rejected
  before any Backend call, call-wide" needs re-examining: for `List`,
  Security rejection is now ONLY observable in that entry's own
  `ListResult[T].Err` — the Backend's `ListDir`/`WalkDir` call(s)
  themselves still happen BEFORE any per-entry Security check (an
  entry must be DISCOVERED AND DECODED before `BoundMiddleware` has a
  value to evaluate), a real, necessary change from the "security
  checked before any Backend call" convention every other role still
  follows — `List` is now the one documented exception, since its own
  Security check is inherently per-RESULT, not per-REQUEST, the same
  way `Exists`'s absence-is-not-an-error idiom is `Exists`'s own
  documented exception to "errors are always typed."
- `api/file/filetest`'s conformance suite and `examples/file-api/
  main.go` both call `List` and need updating to the new
  `[]ListResult[T]` shape.

### Unit test plan (amendment)

| Test | Verifies |
|---|---|
| `TestListCore_PerEntrySecurityRejection_OtherEntriesStillSucceed` | One entry's declared `BoundReadMiddleware`-equivalent-for-List rejects it; sibling entries in the same directory still decode and return successfully |
| `TestListCore_BoundMiddleware_ReceivesActualDecodedValue` | `Fn` observes the entry's real decoded `T`, not a zero value, proving the `v=nil` → `v=&decoded` upgrade |
| `TestListCore_DecodeFailure_BecomesEntryErr_NotWholeCallFailure` | A single malformed entry's `ListResult[T].Err` is set; the call itself returns `nil` error and every OTHER entry's `Value` is populated |
| `TestListCore_WalkLevelError_StillFailsWholeCall` | An unreadable parent directory still returns `(nil, error)` — no per-entry partial result is fabricated |
| `TestBatchDeleteCore_PerPathSecurityRejection_OtherPathsStillProceed` | One path's Security check rejects it (`BatchDeleteResult.Err`); sibling paths in the same batch still delete successfully |
| `TestListCore_Observer_CountReflectsTotalEntriesIncludingFailures` | `RecordFileList`'s `count` includes failed entries, not just successes |

## Phase 2 — outline (further backends + feature depth)

**Status: Phase 1 is done and closed (including a `deep-dive-bug-hunt`
pass, `history.md` Rounds 216-223 — see `.github/skills/
review-go-codex/references/history.md`, plus a retroactive "Phase 1
amendment" for `List`/`BatchDelete` per-entry Security, also now
IMPLEMENTED); Phase 2's own 5 items are being tackled ONE AT A TIME, in
a confirmed order, each designed to Phase 1's full Go-signature rigor
before implementation starts. Confirmed order: Recursive `WithList` →
Conditional writes → Streaming/chunked I/O → `adapters/s3` →
`adapters/smb`.** The first THREE items (**Recursive `WithList`**,
**Conditional writes**, and **Streaming/chunked reads and writes**, all
above) are fully designed AND IMPLEMENTED — Conditional writes' scope
grew during implementation planning to also cover
`ports.FileOptions.IfMatch` + a new `ports.File[T].Stat` method (see
that section's own "`ports` mirror" subsection); Streaming's scope grew
the same way, also adding `ports.File[T].OpenRead`/`OpenWrite` (see its
own status note above). The remaining 2 items (`adapters/s3`,
`adapters/smb`) are still at outline depth only, each noted with its
own open design question, unchanged from this doc's prior round. This
section replaces round 2's original "`adapters/s3`/`adapters/smb` get
their own future roadmap doc" call — ALL of Phase 2 is tracked in THIS
doc.

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
whole interface); `StreamWriteBackend`/`StreamReadBackend` →
`PutObject`/`GetObject`'s own streaming request/response bodies;
`RangeReadingBackend` → `GetObject`'s native `Range` header, the
PRIMARY real-world motivating case that interface exists for at all
(see "Streaming/chunked reads and writes," above, now fully designed).
Key open design question for the eventual full
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

> **Status: DESIGNED to Phase 1's Go-signature rigor — NOT yet
> implemented.** The third Phase 2 item to reach this level of detail
> (after Recursive `WithList` and Conditional writes). This design
> round resolved 3 forks via `ask_user` (write mechanism, read scope,
> byte-range need), plus one structural clarification ("why does
> streaming bypass the codec?") the user specifically asked to have
> justified before proceeding — recorded below, not just asserted. A
> follow-up pre-implementation review pass found and resolved 2 more
> gaps (a `.tmp-*`-commit-temp-file leak into `List` — the SAME bug
> class as Conditional writes' own `.lock`/`.meta.json` fix, now
> retroactively extended to a third suffix; and a missing Router/
> Declarative-Middleware integration for the 2 new roles, now given
> full parity) — all folded into the sections below, not tracked
> separately. A SECOND review pass verified that round's Router/
> Middleware signatures against the real code (clean, no errors found)
> and cross-checked the whole design against
> `docs/design/d-0010-adapter-integration-architecture.md`, finding and
> resolving 3 more items: a clarifying note distinguishing
> `file.Capability` from the optional-Backend-extension idiom; a new
> `stats.FileObserver.RecordFileRangeRead` method (D-0010's
> "transport-guaranteed vs. adapter-approximated" capability axis,
> applied here); and a new `filetest.AssertStreamRoundTrip` conformance
> scenario (D-0010's own established adapter-compliance mechanism,
> extended for Streaming — Recursive `WithList`/Conditional writes'
> own `filetest` gaps remain a separate, explicitly-named follow-up).
>
> **IMPLEMENTED.** Both mechanisms shipped exactly per the design above,
> with 2 implementation-time findings beyond what the design anticipated:
> (1) a format created via `format.NewStreamed` structurally carries NO
> non-streaming `Marshal` (only `MarshalTo`) — `createCore`'s whole-file
> fallback (when the Backend lacks `StreamWriteBackend`) now buffers via
> `MarshalTo` into memory instead of calling `Marshal` directly, so a
> streamable format works correctly on ANY Backend, not just
> `StreamWriteBackend`-implementing ones; `format.Marshal` itself also
> gained a graceful `ErrNotMarshalable` instead of panicking on a
> `NewStreamed` format with no `marshal`/`marshalTyped` func, as a
> safety net for any other future caller. (2) Scope grew during
> implementation planning, resolved via `ask_user`: `ports.File[T]` ALSO
> gained `OpenRead`/`OpenWrite` methods (mirroring `Client.OpenRead`/
> `OpenWrite`, plus a new `ports.OpenReadOptions` type for the byte-range
> parameter) — not originally decided in the design rounds above, added
> for parity with the `Stat`/`IfMatch` precedent from Conditional writes.
> The `.tmp-*` control-file-filter fix was applied with the same
> live-reproduction discipline as the `.meta.json` fix (a failing test
> against the ALREADY-SHIPPED `WriteBytesIfMatch` temp file was written
> and confirmed failing BEFORE the fix landed, in both
> `adapters/file.Backend` and `ports`'s `osBackend`).

#### Research grounding this design

Phase 1's `Backend.ReadBytes`/`WriteBytes` are whole-file, `[]byte`-at-
a-time, matching `ports.File`'s own existing all-at-once contract —
correct for Phase 1's target use cases (configuration files, small-to-
medium records) but not for large media/archive files. Two facts,
confirmed by reading the actual code rather than assumed, shaped this
design:

1. **`format.Format[T]` ALREADY has a streaming-WRITE primitive** —
   `format.NewStreamed(codec, marshalTo func(T, io.Writer) error,
   unmarshal, contentType)` + `Format[T].IsStreamable()`/`MarshalTo()`
   — used TODAY only by REST's HTTP response path
   (`adapters/internal/restdispatch`, writing directly into the
   `http.ResponseWriter`, no `[]byte` buffering). 100% unused anywhere
   in `api/file`/`ports` before this design.
2. **There is NO streaming-DECODE primitive anywhere in `codex`/
   `format`** — even `format.NewStreamed`'s OWN `unmarshal` parameter
   is still `func([]byte) (T, error)`. `codex.Codec[T]`'s `Decode`/
   `Validate` fundamentally require a FULLY MATERIALIZED `T` to run
   `Refine` constraints against — there is no partial/incremental
   decode concept in this codebase at all.

#### Why streaming bypasses the codec (structural, not arbitrary — a design clarification the user specifically asked for)

Decode-side streaming isn't a missing convenience this design chose to
skip — it is impossible to express with `codex.Codec[T]`'s current
shape without a much larger change. `Decode`/`Validate` need the
COMPLETE data to populate struct fields and run `Refine` constraints;
there is no meaningful way to "partially validate" a struct from a
prefix of its bytes. Building a genuine incremental `StreamingCodec[T]`
(decode-as-bytes-arrive, with validation necessarily deferred or
partial) would be a cross-cutting change to `codex`/`format` affecting
EVERY `api/*` package that uses them (`rest`, `events`, `reqreply`,
`mcp`), not something scoped to `api/file` — confirmed out of scope for
this round. For genuinely large/opaque binary content (video,
archives), a "codec" would often be a no-op anyway — forcing it
through `Decode`/`Validate` would just be ceremony around a full
in-memory `[]byte`, defeating streaming's whole purpose. The resolution
(confirmed): streaming roles DECOUPLE path declaration (which never
needed full content — just `vars`, already working today) from content
decode (which does need full content, by the codec's own design) — `T`
stays useful for the FORMER even when bypassed for the latter.

#### Mechanism 1 — format-level write streaming (automatic, no new role)

Reuses the EXISTING `format.IsStreamable()`/`MarshalTo` mechanism —
zero `format`-package changes, zero new declare-time API (the format
itself already signals streamability via how it was constructed):

```go
// StreamWriteBackend is an OPTIONAL Backend extension for streaming
// writes — mirrors CopyingBackend/MovingBackend/BatchDeletingBackend/
// WalkingBackend/DirDeletingBackend/ConditionalWriteBackend's exact
// type-assertion-guard idiom. A Backend NOT implementing this keeps
// working via createCore's existing whole-file Marshal+WriteBytes path
// — just without the memory-usage benefit.
type StreamWriteBackend interface {
	// OpenWriter returns a writer whose Close MUST commit the write
	// atomically (temp-file+rename, mirroring
	// ConditionalWriteBackend.WriteBytesIfMatch's own commit mechanism
	// — ONE atomicity story for every streaming write path in this
	// package) — a reader must never observe a partially-written file.
	// opts carries the same CreateDirs/DirPerm/Perm/Strict/Metadata
	// fields WriteBytes already honors.
	OpenWriter(ctx context.Context, path string, opts WriteOptions) (io.WriteCloser, error)
}
```

`createCore`'s dispatch behavior: when `core.resource.format
.IsStreamable()` AND the attached Backend implements
`StreamWriteBackend`, stream `format.MarshalTo(v, writer)` instead of
`format.Marshal(v)` + `backend.WriteBytes(...)`; falls back to today's
whole-file path otherwise (non-streamable format, OR a Backend without
`StreamWriteBackend`) — the SAME graceful-degradation idiom every other
optional extension in this package already uses.

**Caveat, disclosed not hidden**: this only avoids memory buffering if
`T`'s own `marshalTo` closure itself streams (e.g. `T` holds an
`io.Reader` field the closure `io.Copy`s from) — if `T` holds the
payload as an in-memory `[]byte`, this skips one intermediate buffer
but doesn't fix the underlying memory usage. `format.NewStreamed` was
designed for streaming OUTPUT generation (e.g. rendering a template),
not for passing an externally-sourced large blob through unchanged —
that case is Mechanism 2's job.

**`IfMatch` interaction (resolved, not combined this round)**: when a
streamable format is ALSO used with `WriteOptions.IfMatch` set,
dispatch falls back to the NON-streaming path
(`Marshal`+`WriteBytesIfMatch`) — conditional writes and format-level
streaming both still WORK independently, just not combined yet;
combining the lock-file mechanism with a progressive `MarshalTo` write
is real additional complexity, explicitly deferred, not designed now.

#### Mechanism 2 — raw byte-stream roles (NEW, codec-bypassing)

Two new roles, mirroring S3's own real `GetObject`/`PutObject`
body-streaming precedent exactly — `T` still declares path
templating/Security/Capability at declare time; the ACTUAL bytes
bypass the codec entirely (see "Why streaming bypasses the codec"
above):

```go
// StreamRead/StreamWrite are role metadata structs — mirror
// Read/Create/etc.'s own currently-documentation-only shape.
type StreamRead struct{ Summary, Description string }
type StreamWrite struct{ Summary, Description string }

func (r Resource[T]) WithStreamRead(op StreamRead) StreamReader[T]
func (r Resource[T]) WithStreamWrite(op StreamWrite) StreamWriter[T]

// StreamReader[T]/StreamWriter[T] get the SAME .Use()/.WithCapabilities()/
// .WithSecurity()/.Handle(client, opts...) surface every other role
// already has — full parity, not a reduced role.

// StreamReadOptions configures Client.OpenRead's byte-range (Fork 3).
// Offset/Length == 0 means "whole file" (the default, byte-for-byte
// today's... well, THIS role's own baseline, since it's brand new).
// RANGE IS READ-ONLY (resolved): S3 has no native partial-object WRITE
// at an offset (objects are immutable except full overwrite — the SAME
// exclusion basis as this doc's existing "no Append" decision), so
// range semantics apply ONLY to OpenRead, never OpenWrite.
type StreamReadOptions struct {
	Offset int64
	Length int64 // 0 means "to end of file" when Offset is set
}

// Client gains 2 new dispatch methods — SAME dual-accept contract
// (bare role builder OR pre-built Handle) every other dispatch method
// already follows.
func (c *Client) OpenRead(ctx context.Context, sr any, vars map[string]string, opts ...StreamReadOptions) (io.ReadCloser, error)
func (c *Client) OpenWrite(ctx context.Context, sw any, vars map[string]string, opts ...WriteOptions) (io.WriteCloser, error)
```

**`OpenRead`'s 3-level Backend fallback cascade** (mirrors `Move`'s own
3-level cascade precedent exactly — reused convention, not invented):

```go
// StreamReadBackend is an OPTIONAL Backend extension for streaming,
// whole-file reads.
type StreamReadBackend interface {
	OpenReader(ctx context.Context, path string) (io.ReadCloser, error)
}

// RangeReadingBackend is an OPTIONAL Backend extension for a NATIVE
// byte-range read — mirrors S3's own GetObject Range support, the
// primary real-world motivating case for this interface.
type RangeReadingBackend interface {
	OpenReaderRange(ctx context.Context, path string, offset, length int64) (io.ReadCloser, error)
}
```

1. `RangeReadingBackend.OpenReaderRange` — a Backend with a genuine
   native range read.
2. `StreamReadBackend.OpenReader` + a generic discard-then-limit
   wrapper (`io.CopyN` to a discard sink for `offset` bytes, then
   `io.LimitReader` for `length`) — correct but not a true seek, for a
   Backend implementing ONLY whole-file streaming.
3. `Backend.ReadBytes` (today's existing method) + a `bytes.Reader`
   wrapper sliced to `[offset:offset+length]` — works on EVERY existing
   Phase 1 Backend unchanged, zero new requirement — the SAME
   "no false portability, always correct, sometimes slower" idiom this
   doc's every other optional-extension fallback already uses.

**Relationship to `file.Capability` (clarified, found during this
round's D-0010 cross-check — not a conflict, a distinct mechanism)**:
`StreamWriteBackend`/`StreamReadBackend`/`RangeReadingBackend` are
OPTIONAL BACKEND EXTENSIONS (the SAME `CopyingBackend`/`MovingBackend`/
`BatchDeletingBackend`/`ConditionalWriteBackend` idiom — type-assertion-
guarded, ALWAYS works via a universal fallback, never rejected) — a
DIFFERENT mechanism from `file.Capability`/`CheckCapabilityCoverage`
(D-0010's "Capabilities are requirements, not transport options"
section — declare-time, semantic REQUIREMENTS that get REJECTED
outright when unsupported, e.g. `RequireAccessMode()`). Both are
legitimate, complementary mechanisms in this package, not competing
ones: a `Capability` requirement says "this role CANNOT run at all
without X"; an optional Backend extension says "this role ALWAYS
works, just faster/more-precisely when X is available." Range reads
are a graceful-degradation concern (always correct, sometimes slower),
not a hard requirement — the right fit is the optional-extension
idiom, not a new `file.Capability`.

**`OpenWrite`'s commit**: the SAME temp-file+rename atomicity as
`StreamWriteBackend.OpenWriter` above — ONE atomicity story for every
streaming write path in this package, not two competing ones. A
Backend without `StreamWriteBackend` falls back to buffering the FULL
write into memory, then calling `WriteBytes` on `Close()` — works
everywhere, just without the memory benefit.

**`OpenWrite` does NOT support `IfMatch` this round** (explicitly
deferred, same rationale as Mechanism 1's own `IfMatch` exclusion —
combining the lock-file mechanism with an open-ended streaming write is
real additional complexity, not designed now).

**Security/Capability**: `StreamRead`/`StreamWrite` get the SAME
Capability/`AccessMode` gate + Security check BEFORE the reader/writer
is ever handed back (fail-fast, matches every other mutating role's own
"fail before Backend call" convention) — since there's no decoded `T`
value at gate time, `BoundMiddleware` for these 2 roles uses the
established NO-T shape, named and shaped EXACTLY like
`BoundDeleteMiddleware`'s own (not a new pattern — confirmed via code,
not just asserted, during this round's own pre-implementation review):

```go
type BoundStreamReadMiddleware[In, Out any] struct{ /* same shape as BoundDeleteMiddleware[In,Out] */ }
func NewBoundStreamReadMiddleware[In, Out any](decl middleware.Declaration[In, Out], fn func(ctx context.Context, in In) (Out, error)) BoundStreamReadMiddleware[In, Out]
func BoundSecurityStreamReadMiddleware[In, Out any](schemeName string, scheme SecurityScheme, scopes []string, fn func(ctx context.Context, in In) (Out, error)) BoundStreamReadMiddleware[In, Out]
func (sr StreamReader[T]) StreamReadBoundMW(bm any) StreamReader[T]

type BoundStreamWriteMiddleware[In, Out any] struct{ /* same shape as BoundDeleteMiddleware[In,Out] */ }
func NewBoundStreamWriteMiddleware[In, Out any](decl middleware.Declaration[In, Out], fn func(ctx context.Context, in In) (Out, error)) BoundStreamWriteMiddleware[In, Out]
func BoundSecurityStreamWriteMiddleware[In, Out any](schemeName string, scheme SecurityScheme, scopes []string, fn func(ctx context.Context, in In) (Out, error)) BoundStreamWriteMiddleware[In, Out]
func (sw StreamWriter[T]) StreamWriteBoundMW(bm any) StreamWriter[T]
```

**Router integration (resolved — full parity, found missing during
this round's own pre-implementation review, not silently scoped out)**:
the design's first pass didn't address Router composition for these 2
roles at all — a real gap, given Phase 1's own "Router/Middleware
depth: FULL PARITY" commitment. `StreamReader[T]`/`StreamWriter[T]`
implement `router.Routable[*Client]` exactly like all 10 Phase 1 roles
already do — named and shaped identically to `Deleter[T]`'s own
(another no-domain-value role, the closest existing precedent):

```go
func (sr StreamReader[T]) WithRouterPrefix(prefix string, mws []middleware.RouteMiddleware, tags []string) (routable, string)
func (sr StreamReader[T]) MiddlewareNames() []string
func (sr StreamReader[T]) RouteTags() []string
func (sr StreamReader[T]) RegisterAny(client *Client) error
func (sr StreamReader[T]) RouteMethod() string { return "stream-read" }
// StreamWriter[T] gets the identical 5 methods, RouteMethod() == "stream-write".
```

`file.WithRouter(rt Router) HandleOpt` composes onto a built
`*StreamReadHandle[T]`/`*StreamWriteHandle[T]` exactly like every other
role's Handle — no special-casing needed.

**Metadata handling (clarified, found during this round's review)**:
`OpenWrite`'s `WriteOptions.Metadata` is a DIRECT passthrough of
whatever the caller explicitly supplies — unlike `Create`/`Patch`,
there is no decoded (or caller-supplied-as-a-domain-value) `T` for
`deriveWriteMetadata` to derive metadata FROM (that mechanism reads
metadata off `v T`'s own `metadataMergeFields`-tagged struct fields,
and `StreamWrite` has no `v T` at all, by design — see "Why streaming
bypasses the codec" above). This is a genuinely different
metadata-handling shape from every other mutating role, stated
explicitly here rather than left implied.

**`ctx` cancellation is not honored mid-stream (disclosed, not
silently assumed to work)**: `ctx` is only meaningful at `Open`-time
dispatch (the Security/Capability gate, and the `OpenReader`/
`OpenReaderRange`/`OpenWriter` Backend call itself) — once the stream
is handed back to the caller, Go's stdlib blocking file I/O on a
regular `*os.File` does NOT honor context cancellation at all; an
in-progress `Read`/`Write` call on the returned stream runs to
completion (or its own I/O error) regardless of `ctx`.

#### Structured errors

No NEW structured error types needed — `Open`-time failures surface via
the EXISTING `FileReadError`/`FileWriteError`/`BackendUnsupportedOptionError`
(`AccessMode` rejection)/`SecurityRejectedError` exactly as every other
role's dispatch-level failures already do. A mid-stream read/write
error (after the `io.ReadCloser`/`io.WriteCloser` has already been
handed to the caller) is returned DIRECTLY by that `Read`/`Write` call
on the stream itself — plain Go `error`, NOT wrapped in any `api/file`
structured type, since it happens entirely outside dispatch's own call
stack (see "Observer integration" and "scope limits" below for why this
is structurally necessary, not an oversight).

#### Observer integration

The returned `io.ReadCloser`/`io.WriteCloser` is WRAPPED so
`RecordFileRead`/`RecordFileWrite` fires on `Close()` — NOT on `Open()`
— with the real elapsed duration from Open to Close, the natural
"operation complete" moment for a stream (unlike every other role,
where Open-call and completion are the same moment). `success` reflects
whether `Close()` itself returned an error (e.g. a failed final
`os.Rename` for `OpenWrite`'s commit) — a mid-stream `Read`/`Write`
error on the stream does NOT by itself flip `success` to false (the
caller may still choose to call `Close()` cleanly after handling a
recoverable mid-stream error); only `Close()`'s own return value is
authoritative, mirroring how a plain `io.Closer`'s contract already
works in Go generally.

**`RecordFileRangeRead` — a NEW `stats.FileObserver` method (found
during this round's D-0010 cross-check)**: D-0010 names an
"observable-to-application vs. implementation-detail-only" capability
axis, paired with "transport-guaranteed vs. adapter-approximated" —
whether a degraded/approximated capability's satisfaction mode should
be queryable. `OpenRead`'s own 3-level range fallback cascade had NO
equivalent before this round — a caller had no way to tell whether
their ranged request was served by a real native seek or one of the
2 approximated fallback tiers. Resolved by adding:

```go
// RecordFileRangeRead is called ONCE per range-requesting OpenRead
// call, at dispatch time (the fallback tier used is an Open-time
// fact — independent of the overall RecordFileRead-on-Close duration
// tracking above). native reports whether RangeReadingBackend served
// the request natively (true) vs. one of the 2 approximated fallback
// tiers (false) — mirrors D-0010's own "transport-guaranteed vs.
// adapter-approximated" capability axis, applied here to Backend
// optional-extension fallback tiers instead of file.Capability.
RecordFileRangeRead(path string, native bool, offset, length int64, d time.Duration)
```

Blast radius confirmed bounded (same scope every prior new
`FileObserver` method addition this session has had): exactly 3
existing `stats.FileObserver` implementers in this repo
(`LoggingObserver`/`fanout`/`NoopObserver`).

#### Scope limits (disclosed, not silently hidden)

- **`ErrorPattern` only covers `Open`/`Close`-time dispatch-level
  failures** — a mid-stream read/write error is NOT something dispatch
  can intercept or map, by construction (it happens after the stream
  has already left dispatch's own call stack) — mirrors how `Patch`'s
  conditional-write lock only protects the FINAL commit step, not the
  whole read-modify-write sequence; the SAME "protects what's
  structurally possible to protect, discloses what isn't" precedent.
- **Byte-range is READ-ONLY** (restated from Mechanism 2 above) — no
  cross-backend semantic exists for partial-offset WRITES.
- **Neither streaming mechanism combines with `WriteOptions.IfMatch`
  this round** — both still work independently; combining them is a
  real, nontrivial follow-up, explicitly deferred rather than
  half-designed.
- **`format.NewStreamed`'s memory benefit depends on `T`'s own shape**
  (restated from Mechanism 1) — this design does not change that; it's
  an inherent property of how `T`'s `marshalTo` closure is written.

#### Gap found during this round's own pre-implementation review: `.tmp-*` commit temp files leak into `List`

Re-reviewing this design before planning its implementation surfaced a
real gap — the SAME bug CLASS Conditional writes' own gap-review round
already found and fixed for `.lock`/`.meta.json`, now found for a
THIRD control file: confirmed via code
(`adapters/file/conditional_write.go:128`, `ports/file_backend.go:386`)
that `WriteBytesIfMatch`'s commit step already creates its temp file as
`os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".tmp-*")` — e.g.
`item.json.tmp-823719035` — in the SAME directory as the final path.
This temp file is NOT excluded from `ListDir`/`WalkDir` by the existing
control-file filter (which only knows about `.meta.json`/`.lock`) — a
concurrent `List` call while the temp file exists sees a spurious
per-entry error (the random NUMERIC suffix never coincidentally ends in
`.json`, so this manifests as a decode/mismatch error on that entry,
not a silent wrong match the way `.meta.json`/`.lock` did — still a
real, disclosed-worthy exposure, not a cosmetic one).

For `WriteBytesIfMatch` (Conditional writes, already shipped) this
exposure window is brief — one `tmp.Write` call. `OpenWrite`'s ENTIRE
purpose is to keep that temp file open for a potentially LONG streaming
upload, making the SAME exposure far more likely to actually be hit in
production.

**Resolved: extend the control-file filter to ALSO exclude the
`.tmp-*` pattern** — in BOTH `adapters/file.Backend` and `ports`'s
private `osBackend`, in BOTH `ListDir` and `WalkDir` (the SAME
mechanism the `.lock`/`.meta.json` fix already established, one more
suffix added to the SAME list). This retroactively improves the
ALREADY-SHIPPED `WriteBytesIfMatch` commit path too, not just
`OpenWrite`'s new one — both paths share the identical temp-file
naming convention and therefore the identical fix.

No new structured error or API surface change — purely a `ListDir`/
`WalkDir` behavior correction, same class as the Conditional writes
fix. The eventual implementation round should apply the SAME
live-reproduction discipline used for the `.meta.json` retroactive fix
(reproduce the leak against `WriteBytesIfMatch`'s EXISTING temp file
first, confirm it fails, then apply the fix and confirm it passes) —
not just add a `OpenWrite`-specific test without ever proving the
ALREADY-SHIPPED path was actually affected too.

#### Conformance suite (`filetest`) — new scenario (found during this round's D-0010 cross-check)

D-0010 (`docs/design/d-0010-adapter-integration-architecture.md`)
establishes `api/file/filetest`'s own 8-scenario taxonomy as THE
mechanism FUTURE adapters (`adapters/s3`/`adapters/smb`) are meant to
prove compliance against — but it has never been extended for ANY of
Phase 2's 3 designed items so far (Recursive `WithList`, Conditional
writes, Streaming). **Resolved: Streaming gets a new scenario NOW** —
`AssertStreamRoundTrip(t *testing.T, backend file.Backend)`: declares a
resource, builds a `StreamWriter`+`StreamReader`, round-trips content
via `OpenWrite`→`OpenRead` (whole-file) AND a ranged `OpenRead` (folded
into the SAME scenario, not a separate one, for scope discipline) —
proves ROLE-DISPATCH correctness (works on ANY `file.Backend`, via
whatever fallback tier applies), mirroring `AssertReadWriteRoundTrip`'s
own universality principle — NOT specifically proving a fast-path
optional extension is implemented (that's `RecordFileRangeRead`'s own
job, above, observed separately). Wired into `adapters/file`'s own
`conformance_test.go` as the real, independent proof, same as every
other scenario.

**Recursive `WithList`/Conditional writes' own `filetest` gaps remain
explicitly OUT of scope for this round** — tracked as their own,
separate, not-yet-scheduled follow-up, not silently folded in here nor
silently forgotten.

#### Unit test plan

| Test | Verifies |
|---|---|
| `TestCreate_StreamableFormat_StreamingBackend_UsesMarshalTo` | A streamable format + `StreamWriteBackend`-implementing Backend writes via `MarshalTo`, not `Marshal`+`WriteBytes` |
| `TestCreate_StreamableFormat_NonStreamingBackend_FallsBackToWholeFile` | A streamable format against a Backend WITHOUT `StreamWriteBackend` falls back correctly, byte-for-byte identical result |
| `TestCreate_NonStreamableFormat_UnaffectedRegression` | A plain (non-streamed) format's `Create` behavior is BYTE-FOR-BYTE unchanged — zero overhead, zero behavior change |
| `TestCreate_StreamableFormat_IfMatchSet_FallsBackToNonStreamingConditionalPath` | `IfMatch` set alongside a streamable format correctly falls back to `Marshal`+`WriteBytesIfMatch`, not an unsupported combination error |
| `TestStreamReader_OpenRead_WholeFile_RoundTrips` | `Client.OpenRead` with no `StreamReadOptions` returns the full content, byte-for-byte |
| `TestStreamReader_OpenRead_Range_NativeRangeReadingBackend` | A `RangeReadingBackend`-implementing Backend's `OpenReaderRange` is called directly for a ranged request |
| `TestStreamReader_OpenRead_Range_StreamReadBackendFallback_DiscardThenLimit` | A Backend implementing ONLY `StreamReadBackend` (not `RangeReadingBackend`) still returns the correct byte range via the discard-then-limit wrapper |
| `TestStreamReader_OpenRead_Range_ReadBytesFallback_WorksOnEveryBackend` | A Backend implementing NEITHER streaming extension still returns the correct byte range via `ReadBytes`+`bytes.Reader` |
| `TestStreamWriter_OpenWrite_CommitsAtomically` | A reader concurrently `Stat`-ing the path never observes a partially-written file while `OpenWrite`'s writer is still open |
| `TestStreamWriter_OpenWrite_NonStreamingBackend_BuffersThenWrites` | A Backend without `StreamWriteBackend` still works correctly (buffered fallback) |
| `TestStreamRead_SecurityUnmet_RejectsBeforeStreamReturned` | A declared Security requirement rejects BEFORE any `io.ReadCloser` is returned to the caller |
| `TestStreamWrite_AccessModeReadOnly_RejectsBeforeStreamReturned` | A declared `AccessModeReadOnly` Capability rejects BEFORE any `io.WriteCloser` is returned |
| `TestStreamRead_Observer_RecordsOnCloseNotOpen` | `RecordFileRead` fires on `Close()`, with duration measured from `Open` to `Close`, not at `Open()` time |
| `TestStreamWrite_MidStreamError_NotWrappedByErrorPattern` | A mid-stream write error on the returned `io.WriteCloser` is a plain error, NOT an `ErrorPatternMatchedError`, even when the role has `ErrorPatterns` declared |
| `TestListDir_ExcludesTempFiles` | A `.tmp-*` commit temp file left behind by an in-flight (or crashed) `WriteBytesIfMatch`/`OpenWrite` call is excluded from `List` — proven against BOTH `adapters/file.Backend` and `ports.osBackend`, and against BOTH the already-shipped `WriteBytesIfMatch` path and the new `OpenWrite` path (retroactive regression proof + new-feature proof, same discipline as the `.meta.json` fix) |
| `TestStreamReader_Router_GroupedResourceComposesPrefix` | A `StreamReader[T]` registered under a `Router.Group(...)` prefix resolves the CORRECT, prefixed path when dispatched via `Client.OpenRead` — proves Router integration genuinely works end-to-end, not just that the methods exist |
| `TestStreamReader_OpenRead_Range_ObserverReportsNativeTrueWithRangeReadingBackend` | `RecordFileRangeRead`'s `native` reports `true` when a `RangeReadingBackend`-implementing Backend serves the ranged request |
| `TestStreamReader_OpenRead_Range_ObserverReportsNativeFalseOnFallback` | `RecordFileRangeRead`'s `native` reports `false` when EITHER fallback tier (discard-then-limit or whole-file-then-slice) serves the ranged request |
| `TestFiletestConformance_StreamRoundTrip_ProvenAgainstAdaptersFile` | `filetest.AssertStreamRoundTrip` is wired into `adapters/file`'s own `conformance_test.go` and passes — the real, independent proof every other scenario already has |

#### Backend semantic differences (new rows)

| Operation | Filesystem (Phase 1/2) | S3 (Phase 2, future) |
|---|---|---|
| Format-level write streaming | `StreamWriteBackend` via `os.CreateTemp`+`os.Rename` (same atomicity pattern as `ConditionalWriteBackend`) | Would implement via `PutObject`'s own streaming body upload |
| Raw byte-stream read | `StreamReadBackend` via a plain `os.Open` handle | Would implement via `GetObject`'s own streaming response body |
| Byte-range read | `RangeReadingBackend` NOT implemented by `adapters/file.Backend` in Phase 2 (falls back to the discard-then-limit `StreamReadBackend` wrapper — a real seek via `os.File.Seek` would be a reasonable, cheap future enhancement, not required to ship this design); `RecordFileRangeRead`'s `native` is always `false` | `GetObject`'s native `Range` header — the PRIMARY real-world motivating case for `RangeReadingBackend` existing at all; `RecordFileRangeRead`'s `native` is always `true` |
| Raw byte-stream write | `StreamWriteBackend` (same as format-level streaming — ONE mechanism serves both) | Would implement via `PutObject`'s own streaming body upload |

### Conditional writes (ETag/If-Match optimistic concurrency)

> **Status: IMPLEMENTED.** The second Phase 2 item to ship (after
> Recursive `WithList`). This design resolved 3 forks via `ask_user`,
> in order: (1) the portable ETag concept, (2) which roles get
> conditional-write support, (3) filesystem's atomicity model — each is
> recorded below with its resolution and rationale, not just the final
> answer, since the reasoning matters for `adapters/s3`'s eventual
> implementation of the SAME interfaces. A pre-implementation review
> pass found and resolved 3 more gaps (a real `.lock`/`.meta.json`-leak-
> into-`List` bug — see "Gap found during pre-implementation review"
> below — plus 2 minor clarifications on the lock-acquire timeout and a
> nonexistent-target edge case) — all folded into the sections below.
>
> **Scope grew further during the implementation-PLANNING round
> itself** (3 more `ask_user` rounds, each surfacing a real gap when
> checked against the ACTUAL current code rather than assumed):
> `ports.FileOptions` gained its own `IfMatch *string` field (so
> `ports.osBackend`'s `ConditionalWriteBackend` implementation is
> genuinely reachable, not dead code); the control-file filter was
> extended to `WalkDir` (not just `ListDir`) in BOTH backends, since
> Recursive `WithList` and `ports.Dir`'s own recursive modes both
> consume `WalkDir` directly; and a brand-new `ports.File[T].Stat`
> method (+ a public `ports.Entry` alias) was added, since
> `ports.File` had no `Stat`/`Exists` method at all and extending
> `IfMatch` without a way to RETRIEVE the current ETag would have been
> unusable. All 3 are IMPLEMENTED — see `ports/file.go`/
> `ports/file_backend.go`. The design doc's own "Open question" below
> (does `Client.Read`/`List` expose the ETag it computes in-flight on
> the decoded `T` itself?) remains DELIBERATELY DEFERRED — not resolved
> by this implementation, since the feature works end-to-end via
> `Client.Exists`/`ports.File.Stat` without it.

#### Fork 1 (resolved): a fallback ETag, computed by api/file itself

S3 has a native `ETag` (a content identifier returned by `PutObject`/
`GetObject`/`HeadObject` without necessarily re-reading the body).
Filesystem has no equivalent concept at all. **Resolved: api/file
computes its OWN ETag for every Backend** (not delegated to the
Backend, and not Backend-native-only) — this keeps `Entry.ETag`
populated on Phase 1's filesystem backend today, not just once
`adapters/s3` ships:

- **Strong ETag** (a real content hash — SHA-256, full hex digest) —
  computed whenever content is already in hand: after `Read`/`List`'s
  own `ReadBytes` call, and inside `Patch`'s existing
  read-before-merge step.
- **Weak ETag** (HTTP's own `W/"..."` convention — an APPROXIMATION,
  not a content hash) — computed from `ModTime`+`Size` when only
  metadata is available: `Stat`/`Exists`/`List`'s own entries (which
  deliberately never read the body — that is their whole point).
  Format: `W/"<hex(ModTime.UnixNano())>-<Size>"`, mirroring HTTP's own
  weak-validator syntax literally (reused, not reinvented).
- `Entry` gains a new `ETag string` field, populated by every code path
  that already produces an `Entry` (`Stat`/`ListDir`'s entries) or a
  decoded `T` (`Read`/`List`'s per-entry pipeline, where the ETag is
  computed but not currently exposed on the decoded `T` itself — see
  "Open question" below for where it's actually surfaced to the
  caller).
- **Future refinement, explicitly NOT needed now**: a Backend-native
  ETag override (e.g. a `ETagBackend` optional extension letting S3's
  `HeadObject` report its REAL stored ETag instead of api/file's own
  weak approximation) is a reasonable future enhancement once
  `adapters/s3` exists, but is NOT required to make Fork 2/3's design
  work today — flagged here as a forward-looking note, not a decision
  this round needs to make.

#### Fork 2 (resolved): `Patch` + `Create`'s overwrite path

`Create`'s existing `WriteOptions.Strict` already gives
If-None-Match-style "fail if ALREADY exists" semantics — that part of
S3's conditional-write story is already covered, unrelated to this
item. The genuinely missing piece is optimistic concurrency on an
EXISTING resource:

- **`Patch`** — a real, pre-existing TOCTOU window already exists
  between `patchCore`'s own `ReadBytes` (reads the current content to
  merge against) and its final `WriteBytes` (commits the merged
  result) — exactly where `If-Match` matters most.
- **`Create`'s OVERWRITE path** (`Strict: false`, i.e. the caller
  explicitly allows overwriting an existing resource) — conditional
  "only overwrite if it still matches what I last saw" is a natural,
  S3-mirroring addition to the SAME overwrite path.
- **Resolved**: both get `WriteOptions.IfMatch *string` support.
  **Precedence when BOTH `Strict: true` AND `IfMatch` are set on the
  same call** (a somewhat contradictory combination — `Strict` means
  "must NOT already exist," `IfMatch` presumes it DOES): `Strict`'s
  existence check wins — `FileAlreadyExistsError` is returned
  regardless of whether `IfMatch` would have matched, since `Strict`'s
  check runs first and is unconditional. Documented explicitly so this
  isn't mistaken for an inconsistency.

#### Fork 3 (resolved): filesystem gets a GENUINELY ATOMIC emulation, via the SAME interface S3 will use

The naive "read ETag, compare, then write" sequence is NOT atomic on
its own (another writer could act in between) — simply emulating it
that way would be a real, silent race, not a true conditional write.
**Resolved: filesystem implements the exact SAME
`ConditionalWriteBackend` interface `adapters/s3` will later implement
— no API difference between backends** — via a **lock file**
(`<path>.lock`, created with `os.OpenFile(..., O_CREATE|O_EXCL|O_WRONLY,
...)` — pure stdlib, no new dependency, consistent with Phase 1's
"stdlib only" toolchain decision):

1. Acquire `<path>.lock` (bounded retry + backoff, respecting `ctx`
   cancellation — see "Acquire-retry behavior" below).
2. Once locked: compute `path`'s CURRENT strong ETag (read existing
   content if present; absence is handled the same way every other
   role's not-found case is).
3. Compare to the caller's `ifMatch` — a mismatch releases the lock and
   returns `ConditionalWriteMismatchError` immediately, NO write
   performed.
4. A match performs the write via the SAME temp-file+rename pattern
   the existing Strict-write path already established (atomicity with
   respect to concurrent READERS), then releases the lock (removes
   `<path>.lock`).

**3 explicitly confirmed tradeoffs (not silently glossed over):**

- **Scope of the guarantee**: this is atomic ONLY among callers going
  through `WriteBytesIfMatch`/`WriteOptions.IfMatch` — a CONCURRENT
  PLAIN `WriteBytes`/`Create` call (not using `IfMatch` at all) never
  touches the lock file and can still race with a conditional writer.
  This is NOT a hard, OS-enforced guarantee against every possible
  writer — Go's stdlib has no portable mandatory file locking — it is
  "atomic among cooperating conditional-write callers," the same
  general shape every lock-file-based scheme has. Confirmed acceptable.
- **Stale lock handling: NO auto-recovery.** If a process crashes while
  holding `<path>.lock`, the lock file persists — that path's
  conditional-write support is PERMANENTLY stuck until an operator
  manually removes `<path>.lock`. Chosen over a TTL-based force-steal
  (which risks incorrectly stealing a legitimately-slow holder's lock)
  — safer, at the cost of requiring manual intervention after a crash.
  This operational remedy (delete the `.lock` file) is documented
  plainly, not left to be discovered.
- **Acquire-retry behavior: bounded retry + backoff, respecting `ctx`
  cancellation, PLUS an internal default ceiling.** When the lock is
  already held, the acquirer retries (exponential backoff, capped)
  until EITHER it acquires the lock, OR `ctx` is cancelled/times out,
  OR an internal default timeout (~5s) elapses — whichever comes
  FIRST — returning `ConditionalWriteLockTimeoutError{Path, Err}` on
  any of the latter two. The internal ~5s ceiling exists so the loop
  NEVER blocks indefinitely even when the caller supplies a
  `ctx` with no deadline of its own (e.g. `context.Background()`) — the
  caller's own ctx can still cut the wait SHORTER than ~5s, never
  longer. Never fails on the very first sign of contention either.

**This closes the race for the FINAL compare-and-swap step** — the
same step S3's own `PutObject` `If-Match` protects atomically
server-side. It does **NOT** protect `Patch`'s own EARLIER
`ReadBytes`-then-merge computation (another writer could still change
the file between Patch's initial read and its final conditional
write) — this is NOT a gap specific to this design, it is the
universally-accepted, expected shape of OPTIMISTIC concurrency
everywhere, including S3's own real-world usage pattern (`GetObject` →
compute new value client-side → `PutObject` with `If-Match` derived
from the `GetObject`'s own ETag) — the lock only ever needs to
guarantee the FINAL commit step is atomic, exactly matching S3's own
behavior, not a weaker version of it.

#### Gap found during pre-implementation review: `.lock` files (and the PRE-EXISTING `.meta.json` sidecar) leak into `List`

Re-reviewing this design before planning its implementation surfaced a
real gap, confirmed by tracing the actual matching code (not
speculation): `ListDir` has never excluded ANY file by name pattern —
only directory entries (`e.IsDir`) are skipped. `internal/templatematch
.MatchNonWildcard`'s `{name}`-to-`([^/]+)` capture is GREEDY and
crosses `.` freely, stopping only at `/` — so for a template like
`"{name}.json"`, a sidecar file named `widget.json.lock` (this round's
NEW lock file) or `widget.json.meta.json` (the metadata-merge-field
axis's OWN, ALREADY-SHIPPED sidecar file) **silently matches**, since
BOTH names end in the literal `.json` the template's own pattern
anchors on. This is not a decode error or a `FilePathMismatchError` —
it is a SILENT WRONG MATCH, producing a bogus `ListResult[T]` entry
with a corrupted captured var value (e.g. `name="widget.json.meta"`).

This is confirmed to affect BOTH:
1. The NEW `.lock` file this Conditional writes design introduces.
2. The PRE-EXISTING `.meta.json` sidecar file (shipped with the
   file-metadata merge-field axis, earlier in Phase 1) — unnoticed
   until now because no existing test calls `List` on a directory
   containing both a resource file and its metadata sidecar
   simultaneously.

**Resolved: fix BOTH in this same round**, since they share the
identical root cause and fix. `adapters/file.Backend.ListDir` AND
`ports`'s private `osBackend.ListDir` both gain a filter excluding any
entry whose name ends in a BACKEND-KNOWN control-file suffix —
`".meta.json"` and `".lock"` — before returning results. This is
Backend-specific knowledge (a future `adapters/s3` has NEITHER sidecar
objects NOR lock files at all, per this doc's own "Backend semantic
differences" disclosures), so the fix lives at the Backend level, not
in the shared `listCore`/dispatch-core logic.

**Extended during implementation planning: the SAME filter also
applies to `WalkDir`, not just `ListDir`**, in BOTH backends — Recursive
`WithList` (shipped) and `ports.Dir`'s own recursive/glob-discovery
modes BOTH consume `WalkDir` directly, with the IDENTICAL exposure a
`ListDir`-only fix would have left completely unaddressed. A filtered
entry is NEVER passed to the walk callback at all — fully invisible to
every `WalkDir` consumer, not just call-but-ignored.

**Extended AGAIN, during the Streaming/chunked I/O design's own
pre-implementation review (below): the SAME filter also excludes the
`.tmp-*` commit-temp-file pattern** `WriteBytesIfMatch`'s own commit
step already creates (`<path>.tmp-<random>`, e.g.
`item.json.tmp-823719035`) — found while reviewing Streaming's
`OpenWrite` (which keeps an analogous temp file open far longer, making
the SAME exposure far more likely to be hit in practice), but it
retroactively applies to the ALREADY-SHIPPED `WriteBytesIfMatch` too.
See the "Gap found during this round's pre-implementation review"
subsection under "Streaming/chunked reads and writes," below, for the
full finding — not repeated twice here to avoid drift between two
copies of the same fix description.

No new structured error or API surface change — this is purely a
`ListDir`/`WalkDir` BEHAVIOR CORRECTION. Because it retroactively fixes
ALREADY-SHIPPED Phase 1 behavior (the `.meta.json` half), the
implementation applied the SAME discipline this session's
`deep-dive-bug-hunt` rounds use for any shipped-code fix: the leak was
reproduced live via a FAILING test first (confirmed failing against the
unfixed `ListDir`), THEN the fix was applied and the SAME test
confirmed passing — not just added after the fact without ever seeing
it fail.

New unit test plan rows for this fix are listed in the "Unit test
plan" table below (`TestListDir_ExcludesMetadataSidecarFiles` and
`TestListDir_ExcludesLockFiles`), proven against BOTH
`adapters/file.Backend` and `ports.osBackend` (N-way parity, same
convention this session's `deep-dive-bug-hunt` rounds already
established for every other `Backend` method).

#### API surface

```go
// Entry gains ETag (see Fork 1 above for strong-vs-weak computation).
type Entry struct {
	Name     string
	IsDir    bool
	Size     int64
	ModTime  time.Time
	Metadata map[string]string
	ETag     string // NEW
}

// WriteOptions gains IfMatch.
type WriteOptions struct {
	Perm       os.FileMode
	CreateDirs bool
	DirPerm    os.FileMode
	Strict     bool
	DryRun     bool
	Metadata   map[string]string
	// IfMatch, when non-nil, makes this write CONDITIONAL: it only
	// commits if path's CURRENT strong ETag equals *IfMatch — see the
	// "Conditional writes" section of docs/roadmap/file-api.md for the
	// full cross-backend design. nil (the default) means "unconditional
	// write," byte-for-byte today's existing behavior — zero overhead,
	// zero behavior change for every existing caller.
	IfMatch *string // NEW
}

// ConditionalWriteBackend is an OPTIONAL Backend extension for an
// atomic conditional write — mirrors CopyingBackend/MovingBackend/
// BatchDeletingBackend/WalkingBackend/DirDeletingBackend's exact
// type-assertion-guard idiom. A Backend NOT implementing this cannot
// honor WriteOptions.IfMatch at all — the dispatch core returns
// ConditionalWriteUnsupportedError rather than emulating a non-atomic
// check with a real race window. adapters/file.Backend AND ports's
// private osBackend BOTH implement this (via the lock-file mechanism
// in Fork 3 above); a future adapters/s3 implements it via S3's own
// native, server-side If-Match.
type ConditionalWriteBackend interface {
	// WriteBytesIfMatch behaves like WriteBytes, but commits ONLY if
	// path's CURRENT strong ETag equals ifMatch — atomically with
	// respect to every OTHER conditional write attempt through this
	// same Backend (see each implementation's own doc comment for its
	// exact atomicity scope — e.g. filesystem's "cooperating
	// conditional writers only" disclosure above). Returns
	// ConditionalWriteMismatchError{Path, Expected, Actual} (not a
	// generic error) on a mismatch — no write performed. ifMatch is
	// never "" (dispatch only calls this when WriteOptions.IfMatch is
	// non-nil).
	WriteBytesIfMatch(ctx context.Context, path string, data []byte, ifMatch string, opts WriteOptions) (createdDirs []string, err error)
}
```

#### Dispatch behavior

1. `createCore`'s overwrite path (`opts.Strict == false`) and
   `patchCore`'s final commit both check `callerOpts.IfMatch` BEFORE
   calling `WriteBytes`:
   - `callerOpts.IfMatch == nil`: unconditional write, today's
     existing `WriteBytes` call, BYTE-FOR-BYTE unchanged — zero
     overhead for every existing caller.
   - `callerOpts.IfMatch != nil` AND the Backend implements
     `ConditionalWriteBackend`: call `WriteBytesIfMatch` instead.
   - `callerOpts.IfMatch != nil` AND the Backend does NOT implement
     `ConditionalWriteBackend`: return `ConditionalWriteUnsupportedError`
     immediately — BEFORE any write is attempted, no partial write, no
     race attempted at all (fails loud and fast, never silently races).
2. `createCore`'s `Strict: true` path is UNCHANGED — its existence
   check still runs first and unconditionally; see Fork 2's precedence
   note above for the `Strict: true` + `IfMatch` combination.
3. **`createCore`'s overwrite path (`Strict: false`) with `IfMatch` set
   against a path that does NOT currently exist at all**: resolved as
   `ConditionalWriteMismatchError` (there is nothing to match, so any
   non-empty `IfMatch` necessarily "doesn't match" — mirrors S3's own
   real `PutObject`/`If-Match` behavior, which fails the same way
   against a nonexistent object; the alternative — silently treating
   "doesn't exist" as satisfying any `IfMatch` — would be a surprising,
   S3-inconsistent special case). `WriteBytesIfMatch`'s own
   implementation (both the lock-file-based filesystem one and a
   future S3 one) is responsible for this check — dispatch itself
   doesn't need a separate not-found branch before calling it.
4. A caller obtains the CURRENT ETag to supply as `IfMatch` via
   `Client.Exists` (returns `Entry`, which now carries `ETag`) — this
   is the standard optimistic-concurrency workflow (read the current
   version, then conditionally write only if it is still unchanged),
   and requires ZERO new return values on `Create`/`Patch` themselves
   (both keep their existing signatures — the caller supplies `IfMatch`
   via `WriteOptions`, same mechanism `Metadata`/`CreateDirs`/`Strict`
   already use).

#### Structured errors

- `ConditionalWriteMismatchError{Path, Expected, Actual string}` — the
  supplied `IfMatch` didn't equal `path`'s current ETag; no write was
  performed.
- `ConditionalWriteUnsupportedError{Path, Backend string}` — the
  attached Backend doesn't implement `ConditionalWriteBackend` at all;
  raised BEFORE any write attempt.
- `ConditionalWriteLockTimeoutError{Path string, Err error}` —
  filesystem-specific (both `adapters/file.Backend` and `ports`'s
  private `osBackend`): the lock-file acquire-retry loop exceeded its
  bound (or `ctx` was cancelled) without acquiring `<path>.lock`.
- All 3 are `slog.LogValuer` + `Unwrap()` where applicable, matching
  every other error in this package.

#### Observer integration

No NEW `FileObserver` method needed — a conditional-write failure
(mismatch, unsupported Backend, or lock timeout) is recorded via the
EXISTING `recordWrite(obs, path, false, duration)` call, exactly like
any other write failure; `stats.Observer` doesn't need granular
"which reason" detail beyond what the returned structured error type
already provides via `errors.As`.

#### Unit test plan

| Test | Verifies |
|---|---|
| `TestEntry_ETag_StrongWhenContentAvailable_WeakWhenMetadataOnly` | `Read`/`List`'s entries get a strong (content-hash) ETag; `Stat`/`Exists`'s get a weak (`W/`-prefixed, ModTime+Size) one |
| `TestPatch_IfMatch_Match_Succeeds` | A `Patch` call whose supplied `IfMatch` equals the current ETag commits normally |
| `TestPatch_IfMatch_Mismatch_RejectsWithConditionalWriteMismatchError_NoWritePerformed` | A stale `IfMatch` is rejected; the file's content is CONFIRMED unchanged afterward (no partial write) |
| `TestCreate_Overwrite_IfMatch_Match_Succeeds` | `Create` with `Strict:false` + a matching `IfMatch` overwrites normally |
| `TestCreate_Overwrite_IfMatch_Mismatch_Rejected` | `Create` with `Strict:false` + a stale `IfMatch` is rejected, no write performed |
| `TestCreate_StrictTrueAndIfMatchBothSet_StrictTakesPrecedence` | `Strict:true` + ANY `IfMatch` value still returns `FileAlreadyExistsError` when the path already exists, regardless of whether `IfMatch` would have matched |
| `TestConditionalWrite_UnsupportedBackend_ReturnsConditionalWriteUnsupportedError_BeforeAnyWrite` | A fake `Backend` without `ConditionalWriteBackend` rejects immediately; no write attempted |
| `TestConditionalWriteBackend_LockFile_ConcurrentConditionalWriters_OnlyOneCommits` | Two concurrent conditional writers against the SAME path: exactly one commits, the other gets a (correctly recomputed) mismatch — proves the lock genuinely serializes the compare-and-swap step |
| `TestConditionalWriteBackend_LockAlreadyHeld_RetriesThenTimesOut` | An artificially-held lock causes the acquirer to retry (observable via elapsed time), then return `ConditionalWriteLockTimeoutError` once its bound is exceeded |
| `TestConditionalWriteBackend_StaleLock_RequiresManualRemoval` | A lock file left behind by a simulated crash is NOT auto-removed by a later acquirer — documents the accepted "no auto-recovery" behavior explicitly, not just in prose |
| `TestConditionalWriteBackend_LockAcquire_InternalDefaultTimeout_WhenCtxHasNoDeadline` | With `context.Background()` (no deadline) and the lock already held, the acquirer still times out around the internal ~5s ceiling — never blocks indefinitely |
| `TestCreate_Overwrite_IfMatch_NonexistentTarget_ReturnsConditionalWriteMismatchError` | `Create` with `Strict:false` + any non-empty `IfMatch` against a path that doesn't exist at all is rejected, not silently treated as an unconditional create |
| `TestListDir_ExcludesMetadataSidecarFiles` | A retroactive regression proof: a directory containing BOTH a resource file and its `.meta.json` sidecar — `List` returns ONLY the resource entry, never a bogus entry derived from the sidecar's own name (proven against both `adapters/file.Backend` and `ports.osBackend`) |
| `TestListDir_ExcludesLockFiles` | Same proof for a `.lock` file left behind by an in-flight (or crashed) conditional write — `List` never returns a bogus entry derived from it (proven against both `adapters/file.Backend` and `ports.osBackend`) |

#### Backend semantic differences (new row)

| Operation | Filesystem (Phase 1/2) | S3 (Phase 2, future) |
|---|---|---|
| Conditional write | `ConditionalWriteBackend` via a `<path>.lock` file — atomic among COOPERATING conditional writers only; no auto-recovery from a crashed holder (manual `.lock` removal required) | `ConditionalWriteBackend` via `PutObject`'s native, server-side `If-Match` — atomic against EVERY writer (not just cooperating ones), no lock file, no stale-lock concept at all |

#### `ports` mirror (scope grown during implementation planning — IMPLEMENTED)

Conditional writes was originally designed as an `api/file.Client`-only
feature. While PLANNING its implementation, checking the ACTUAL current
code surfaced that `ports.osBackend` implementing
`ConditionalWriteBackend` (as the original design already said it
would) would be unreachable DEAD CODE — `ports.FileOptions` had no
`IfMatch` field at all. Resolved (via `ask_user`) to extend
`ports.File`'s own public surface instead of leaving it dead:

- `ports.FileOptions.IfMatch *string` — threaded into `File.Write`'s
  existing `file.WriteOptions{...}` construction; `File.Patch` gets it
  "for free" via its own `fh.Write(...)` delegation at the end of its
  read-modify-write sequence (the SAME final atomic compare-and-swap
  step protects it, exactly like `api/file.Client.Patch`'s own
  `patchCore`).
- A NEW `ports.File[T].Stat(vars, opts) (Entry, bool, error)` method —
  mirrors `Client.Exists` exactly. Added because extending `IfMatch`
  without a way to RETRIEVE the current ETag (`ports.File` had no
  `Stat`/`Exists` method at all before this) would have made the field
  reachable but practically unusable.
- A new public `ports.Entry` (alias for `file.Entry`) and 3 new error
  aliases (`ConditionalWriteMismatchError`/
  `ConditionalWriteUnsupportedError`/`ConditionalWriteLockTimeoutError`).
- `ports.osBackend` implements `WriteBytesIfMatch` via the SAME
  lock-file mechanism as `adapters/file.Backend` — a deliberate
  near-duplicate (not shared code), the SAME import-cycle-driven
  precedent every other `osBackend` method already follows.

#### Open question (deliberately NOT resolved this round)

`Entry.ETag` is populated on values returned by `Stat`/`Exists`/`List`
(which already return/wrap `Entry`), but `Read`'s own return value is
the decoded `T` itself, with NO `Entry` wrapper at all — so a caller
wanting the ETag of a value they just `Read` has no way to get it from
that SAME call today (they'd need a separate `Exists`/`Stat` call just
for the ETag). Whether `Client.Read`/`File.Read` should ALSO somehow
expose the ETag computed in-flight (and if so, how — a second return
value would change `Read`'s signature, a sibling method would add new
API surface) remains OPEN — implementation deliberately did NOT resolve
this (the feature works end-to-end via `Client.Exists`/`ports.File.Stat`
without it), left as a genuine future follow-up, not silently dropped.

### Recursive `WithList`

> **Status: IMPLEMENTED.** `ListOptions{Recursive bool}`,
> `Client.List`'s optional trailing variadic, the `WalkingBackend`-based
> walk, the manual equivalent-power `ListDir` fallback, and the
> `recursiveListDepth`-based pruning rule all shipped exactly as
> designed below — see `api/file/dispatch.go`'s `listRecursiveWalking`/
> `listRecursiveManual`. The first Phase 2 item to reach this level of
> detail and the first to actually ship; the remaining 4 outlined items
> are still at outline depth only. **Builds ON TOP of the "Phase 1
> amendment" above** — Recursive `WithList` is implemented against the
> AMENDED per-entry-Security/`ListResult[T]` model, not the original
> once-per-call one.

Today's `ports.Dir.List` already supports a recursive mode — Phase 1's
`Lister[T]` is single-level only. The `WalkingBackend.WalkDir` primitive
added for `ports.Dir`'s own Stage F migration (a `deep-dive-bug-hunt`
round confirmed it byte-for-byte correct against `ports`'s own private
`osBackend` copy) already does exactly the kind of recursive walk this
item needs — this design REUSES it rather than inventing a new
mechanism, exactly as previously outlined.

#### API surface

```go
// ListOptions configures [Client.List]'s traversal depth. Mirrors the
// established 0-or-1 trailing variadic convention Create/Patch/Delete/
// Mkdir already use (api/file/client.go) — backward compatible with
// every existing List call site (opts omitted = today's single-level
// behavior, byte-for-byte unchanged).
type ListOptions struct {
	// Recursive, when true, descends into every subdirectory under the
	// resource's parent directory (see Resource.buildParentDirWithRouting),
	// not just its immediate children. Default false — existing callers
	// see NO behavior change unless they opt in.
	Recursive bool
}

// Client.List gains an optional trailing ListOptions parameter.
func (c *Client) List(ctx context.Context, l any, vars map[string]string, opts ...ListOptions) (any, error)
```

`listCore`'s own signature gains a `callerOpts ListOptions` parameter,
threaded through `dispatchList`/`Lister[T].dispatchList`/
`ListHandle[T].dispatchList` exactly like `WriteOptions`/`DeleteOptions`/
`MkdirOptions` already are for Create/Delete/Mkdir — no NEW dispatch
chain shape to invent, this is the 5th application of an
already-proven pattern.

#### Dispatch behavior

1. `callerOpts.Recursive == false` (the default): identical DISCOVERY
   to today (one `ListDir` call, immediate children only) — but now
   feeds the AMENDED per-entry pipeline above (each entry becomes its
   own `ListResult[T]`), not the original fail-whole-call one. This is
   the amendment's own behavior change, not something Recursive itself
   adds.
2. `callerOpts.Recursive == true` AND the attached `Backend` implements
   `WalkingBackend`: walk the ENTIRE subtree rooted at the resource's
   parent directory via `WalkingBackend.WalkDir`, skipping directory
   entries (`entry.IsDir`) AND any entry failing the depth-pruning rule
   below, decoding every remaining FILE entry found at ANY (permitted)
   depth through the resource's own codec — the exact same
   per-entry pipeline the non-recursive path already runs
   (`core.resource.format.Unmarshal` → `entryListVars` →
   `applyPathMergeFields` → `applyMetadataMergeFields` → per-entry
   `runBoundMiddleware`), just fed by a multi-level walk instead of one
   `ListDir` call, and collecting a `ListResult[T]` per visited file
   (pruned/skipped entries are simply never visited — they produce NO
   `ListResult[T]` at all, not an error one).
3. `callerOpts.Recursive == true` AND the attached `Backend` does NOT
   implement `WalkingBackend`: fall back to a manual, EQUIVALENT-POWER
   recursive composition inside `listCore` itself — call `ListDir` on
   the parent dir, then recurse into every discovered subdirectory
   entry via further `ListDir` calls (subject to the SAME depth-pruning
   rule below), flattening results as they're found — so `Recursive`
   keeps working on ANY `Backend`, not just `WalkingBackend`-
   implementing ones (mirrors `Move`'s own native-then-generic-fallback
   cascade precedent: a capability degrades gracefully, never
   hard-fails, when the Backend lacks the optional fast-path
   extension). **Disclosed, non-blocking limitation**: this manual
   fallback inherits the same no-symlink-loop-protection characteristic
   any recursive filesystem walk has (same as `WalkingBackend.WalkDir`'s
   own `filepath.WalkDir`-based implementation, which does not follow
   symlinked directories either) — not a new risk this design
   introduces, just worth stating plainly rather than leaving implicit.

#### Depth-pruning rule (resolved gap — reuses an existing precedent)

A permissive "match the leaf name at ANY depth" rule would let a
structurally-inconsistent entry slip through — e.g. for template
`"notes/{category}/{id}.json"` (2 segments past the resource's parent
base), a file at `"notes/work/2024/id.json"` (an EXTRA, unexpected
nesting level) would still match `"{id}.json"` on its leaf name alone,
even though the template never describes a 3-segment shape. **Resolved:
recursive discovery is constrained to the template's OWN exact declared
segment count** — an entry whose depth (relative to the resource's
parent directory) does not exactly match the final template segment's
own position is excluded before ever reaching the codec, not silently
decoded and returned. This reuses `ports.Dir.listGlobDiscovery`'s
existing, proven pruning convention verbatim ("a template with no `**`
is pruned: once a candidate directory's depth already equals the
template's own segment count, does not descend further") rather than
inventing a new rule — the SAME mechanism, applied here to entry
matching instead of directory-glob matching.

**Implementation note (resolved during Stage 2 implementation, not a
new open question)**: `buildParentDirWithRouting` REQUIRES every var in
the template's dir portion up front (same as non-recursive `List`
already requires — see the "Scope limit" subsection below), which means
the resolved parent directory ALWAYS corresponds to the template string
up to, but not including, its own final segment — i.e. the template's
"OWN declared segment count" past the resolved parent directory is
ALWAYS exactly 1 (the final leaf segment), for every `Resource[T]`
Phase 1's template shape can express. `recursiveListDepth` (in
`dispatch.go`) encodes this as a named constant rather than a
recomputed-every-call value, with a doc comment explaining why it's
always 1 given this shape — this is a faithful, direct implementation
of the rule above, not a simplification of it; it would only need to
become a per-call COMPUTED value if `Resource[T]`'s template shape ever
grows a multi-segment leaf structure (not in Phase 1 or Phase 2's
current scope).

#### Scope limit — deliberately NOT full glob-discovery (flagged, not silently expanded)

`entryListVars`'s existing per-entry `MergedPathParam` matching only
ever matches an entry's LEAF NAME against the resource's own FINAL
template segment (e.g. `"{id}.json"` in `"notes/{category}/{id}.json"`)
— this design keeps that exact behavior UNCHANGED for recursive
entries too: recursion changes WHICH directories get walked, not how
an individual entry's vars get extracted. A caller whose template has
MULTIPLE path segments (like the `{category}` example above) still
supplies `category` via the caller's own `vars` map, same as
non-recursive `List` does today — every discovered entry at any depth
gets the SAME caller-supplied parent vars merged with its own leaf
match, regardless of which actual subdirectory it was found in.

This means Recursive `WithList` does **NOT** let a caller omit a
multi-segment template's own parent vars and have them be
PER-DIRECTORY-DISCOVERED instead (the way `ports.Dir`'s SEPARATE
glob-discovery mode does, matching `{category}` itself against each
candidate directory). That is a strictly BIGGER feature — effectively
porting `ports.Dir.List`'s glob-discovery mode into `api/file` — and is
explicitly OUT OF SCOPE for this item, flagged here as a possible
FUTURE Phase 2 candidate of its own rather than silently folded into
this design.

#### Structured errors

No NEW error types needed beyond the amendment's own (`ListResult[T].Err`
carries `FileDecodeError`/`FilePathMismatchError`/`SecurityRejectedError`
etc., exactly as that section describes) — a walk-level I/O error (the
parent directory itself, or an intermediate subdirectory, can't be
read/walked at all) still fails the WHOLE call via the plain `error`
return, wrapped as `FileReadError{Path: <failing dir>}`, mirroring
`DirReadError`'s own `ports.Dir.List` precedent for a failed
`filepath.WalkDir`/`WalkingBackend.WalkDir` call.

#### Observer integration

Builds on the amendment's own Observer note (above) — `recordList`/the
`"file.list"` `TraceObserver` span still fire exactly once per
`Client.List` call (not once per directory visited), regardless of
`Recursive`; `count` reflects the TOTAL number of `ListResult[T]`
entries across every visited (post-pruning) depth, successes and
failures together, not a per-directory count.

#### Unit test plan

| Test | Verifies |
|---|---|
| `TestListCore_Recursive_DiscoversNestedEntries` | A 2+-level-deep tree of files all get decoded and returned as `ListResult[T]{Value: ...}`, via `WalkingBackend` |
| `TestListCore_Recursive_SkipsDirectoryEntries` | Directory entries encountered mid-walk are never passed to the codec |
| `TestListCore_Recursive_NonWalkingBackend_FallsBackToRepeatedListDir` | A Backend implementing only `ListDir` (not `WalkingBackend`) still returns every nested entry, via the manual fallback composition |
| `TestListCore_Recursive_MergeFieldAppliesPerEntry_LeafOnly` | `MergedPathParam` still extracts only the LEAF segment's var at any depth; a multi-segment template's parent var comes from the caller's own `vars`, unchanged across every discovered entry |
| `TestListCore_Recursive_DepthPruning_ExcludesStructurallyTooDeepEntries` | An entry nested DEEPER than the template's own declared segment count (e.g. an extra unexpected directory level) is pruned before reaching the codec — never appears in the result at all, not even as a `ListResult[T].Err` |
| `TestListCore_Recursive_NonMatchingLeafName_Excluded` | An entry at a structurally-valid depth whose leaf name doesn't match the resource's final template segment is silently excluded, mirroring non-recursive `List`'s existing behavior |
| `TestListCore_NonRecursive_DiscoveryUnchanged_ResultShapeAmended` | `ListOptions{}` (or omitted entirely) discovers the SAME entries as pre-this-feature `List` (byte-for-byte unchanged discovery), now wrapped in the amended `[]ListResult[T]` shape — the "Recursive adds no NEW discovery behavior when false" proof, distinct from the amendment's OWN regression tests (tracked separately, above) |
| `TestListCore_Recursive_ObserverRecordsTotalCountAcrossDepths` | `RecordFileList`'s `count` reflects every (post-pruning) depth's entries, not just the top level |
| `TestListCore_Recursive_WalkLevelError_StillFailsWholeCall` | A walk-level I/O failure (e.g. permission denied on a nested dir) surfaces as `FileReadError` via the call's plain `error` return, not a fabricated per-entry result |

#### Phase 2 implementation order (per the user's confirmed sequencing)

This item is first in the confirmed Phase 2 order: Recursive `WithList`
→ Conditional writes → Streaming/chunked I/O → `adapters/s3` →
`adapters/smb`. **IMPLEMENTED** (8 new tests in `api/file/client_test.go`,
all passing, including the non-`WalkingBackend` manual-fallback proof
via a test-only Backend wrapper that deliberately does not promote
`WalkDir`) — `ports`-side `osBackend` was NOT given a parallel
Recursive-List wiring this round, since `ports.Dir`'s OWN recursive
mode already exists independently (Stage F) and does not call through
`api/file.Client.List` at all; only `adapters/file.Backend`'s existing
`WalkingBackend` implementation is exercised by this feature. The
remaining 4 Phase 2 items (Conditional writes → Streaming/chunked I/O →
`adapters/s3` → `adapters/smb`) are still at outline depth only.

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

**Phase 1 now has ZERO remaining open design decisions.** Every item
previously listed here is resolved — see "Resolved round 7" below for
the 2 closed this round. The "Phase 1 amendment" section above (`List`/
`BatchDelete` per-entry Security + `ListResult[T]`) and Recursive
`WithList` (the first Phase 2 item) are BOTH now fully designed too —
see their own sections for the complete picture, including their own
unit test plans. The remaining 4 Phase 2 items are DELIBERATELY left at
outline depth (see "Phase 2 — outline" above for each item's own
specific open questions, not repeated here): S3's `AccessMode`-vs-IAM
interaction, `adapters/smb`'s driver/permission choices, the
streaming-`Backend`-extension's exact interface shape, conditional-
writes' atomicity gap — not resolved here by design, since full Phase 2
design is explicitly deferred until Phase 1 ships.

**Resolved round 7** (closing the 2 items this doc carried from round 6):

- **BoundMiddleware's `In`/`Out` metadata-merge-field integration** —
  implemented for `Create` (BOTH directions) via
  `Middleware.WithMetadataIn`/`WithMetadataOut` +
  `BoundCreateMiddleware.WithMetadataIn`/`WithMetadataOut` forwarding
  methods: `In` is populated from the SAME caller-intended metadata
  `deriveWriteMetadata` already derives (available pre-Backend-call
  since `Create`'s full value is supplied directly), and `Out`'s
  encoded contribution is merged OVER the resource-derived metadata
  (last-wins, mirrors `ApplyCapabilities`'s identical convention)
  before the final write.
  **A genuine correction discovered DURING implementation**: the
  original round-6 assumption that `Patch` could ALSO get full `In`
  population was wrong — `Patch`'s BASE `T` value (the thing metadata
  would be derived FROM) doesn't exist at invoke time either; it's only
  known after reading+merging the existing resource, a `Backend` call
  that must happen AFTER the Security gate — the SAME ordering
  constraint `Read`/`List` already have. So `Patch`'s `In` stays
  permanently zero-valued too (proven by
  `TestBoundPatchMiddleware_MetadataIn_AlwaysZeroValue`), while its
  `Out`-side `WithMetadataOut` DOES still work (Fn can compute an `Out`
  value independent of `In` — proven by
  `TestBoundPatchMiddleware_MetadataOut_MergesIntoPersistedMetadata`).
  `Read`/`List` remain unchanged (permanently zero-valued `In`, no
  `WithMetadataIn`/`Out` methods offered on their Bound types at all —
  only `Create`/`Patch` expose them, matching what actually works).
  New `api/file/filetest` conformance scenario,
  `AssertMetadataIntegration`, proves both the resource-level metadata
  axis AND the `BoundCreateMiddleware` override end-to-end against a
  real `Backend`.
- **`List`'s own path-var merge field support** — implemented via a new
  `matchFileTemplate` helper (`internal/templatematch.MatchNonWildcard`,
  the SAME engine `ports.File.MatchPath`/`ports.Dir.MatchPath` already
  use — confirmed the closer, proven precedent over REST's own
  router-delegated matching, since `api/file` has no external router to
  delegate to). `List` now matches each discovered entry's filename
  against the resource's own FINAL path-template segment (e.g.
  `"{id}.json"`), merging the extracted vars over the caller's own
  parent-directory vars (entry-specific wins on collision), then
  decodes via the SAME `codex.DecodeVars` call `Read`'s own fix already
  established. A non-matching stray file aborts the whole `List` call
  with `FilePathMismatchError`, consistent with `listCore`'s existing
  abort-on-first-error semantics. Zero overhead when no `mergeFields`
  are declared (the common case, explicitly regression-tested).

**Resolved round 6** (a dedicated "full Router + Declarative Middleware
parity" implementation round, closing this doc's own previously-named
Phase 1 scope reductions):

- **`file.Router`** upgraded from a simplified plain-string-prefix
  mechanism to a genuine value type (`Router{inner router.Router[*Client]}`),
  mirroring `events.Router` method-for-method (`NewRouter`/`.Use`/`.Tags`/
  `.With`/`.Route`/`.Mount`/`.Group`/`.Walk`/`.Routes`/`.Register`). All
  10 roles implement `internal/router.Routable[*Client]`
  (`WithRouterPrefix`/`MiddlewareNames`/`RouteTags`/`RegisterAny`) +
  `router.MethodReporter` (`RouteMethod`). `RegisterAny`'s per-role
  implementation performs the closest faithful equivalent to REST/events'
  real spec-registration step (which `api/file` has no analogue of): a
  fail-fast Capability-coverage check via `.Handle`, discarding the
  handle. TWO coexisting workflows now exist (mirrors `Client.Attach`'s
  own "two valid Attach workflows" precedent): `Router.Route(leaf)` N
  times + `Router.Register(client)` once; or passing a `Router` value to
  `WithRouter` at an individual role's own `.Handle(client,
  file.WithRouter(rt))` call. `WithRouter`'s signature changed from
  `WithRouter(prefix string)` to `WithRouter(rt Router)` — a safe,
  in-flight break (no external consumers existed yet).
- **Declarative Middleware** — implemented for real, for all 10 roles:
  `file.Middleware[In,Out]` (embeds `middleware.Declaration`) +
  `Declaration[In,Out]`/`NewDeclaration` public aliases (per the "an
  `internal/*` type needs a per-pattern public alias" mandatory
  requirement) + 10 `Bound<Role>Middleware[T,In,Out]` types (`Read`/
  `Create`/`Patch`/`List` keep a `*T` parameter — a natural domain value
  exists; `Delete`/`Exists`/`Copy`/`Move`/`Mkdir`/`BatchDelete` drop it —
  no single domain value exists for these 6, a well-justified adaptation)
  + matching `BoundSecurity<Role>Middleware` constructors (GrantedScopes
  extracted via reflection, the SAME convention `rest`/`events` already
  use) + per-role `XxxBoundMW(bm any)` attach methods. Genuinely
  EXECUTED via a new `runBoundMiddleware` helper, called BEFORE any
  Backend call by all 10 dispatch-core functions — a declared Security
  requirement unmet by the collected GrantedScopes now actually rejects
  the call (`SecurityRejectedError`), proven via a real test asserting
  the Backend was never invoked. Caught and fixed a real bug along the
  way: a "witness method" bug class already documented in
  `internal/middleware/bound.go`'s `BoundContributor` doc comment — the
  4 T-carrying role-shape interfaces needed a deliberate
  `boundTWitness(T)` no-op method to make T appear in a method
  SIGNATURE, otherwise EVERY `Bound<Role>Middleware[X,...]` satisfied
  the shape for ANY T.
- **`file.ErrorPattern`/`ErrorPatterns[B]` is now AUTO-INVOKED** by the
  dispatch core on every Backend-originated error (closing this doc's
  own previously-named scope reduction) — a match wraps the error in a
  new `ErrorPatternMatchedError{Payload any, Err error}`, `errors.As`-
  navigable back to the original (additive, not a replacement). Attached
  per-role via a new `.ErrorPatterns(patterns any)` method.
- **A NEW file-metadata merge-field axis** was added — `api/file`'s
  analogue of REST headers / MQTT User Properties, since path vars are
  already fully covered by `PathParam`/`MergedPathParam` and file
  genuinely had no OTHER "extra wire data" axis to extract a
  BoundMiddleware's `In` from. Grounded choice: a JSON SIDECAR file
  (`<path>.meta.json`), NOT real OS extended attributes — keeps Phase
  1's "stdlib only" toolchain decision intact and is arguably a closer
  analogue to S3's own "metadata lives beside the object, not inside
  it" model than real xattrs would be. New `MetadataBackend` optional
  extension interface (`ReadMetadata`/`WriteMetadata`, mirrors
  `CopyingBackend`/`MovingBackend`'s exact idiom), `MetadataParam`/
  `MergedMetadataParam[T]`/`NewMetadataParam` (mirrors `PathParam`/
  `MergedPathParam` exactly), `Entry.Metadata`/`WriteOptions.Metadata`
  fields, `Resource[T].metadataMergeFields` wired into `Read`/`List`/
  `Create`/`Patch` via the existing `codex.DecodeVars`/`EncodeVars`
  machinery. NOT (yet) integrated into BoundMiddleware's own `In`/`Out`
  — see the new "Open design decisions" bullet above for why.
  **`deep-dive-bug-hunt` finding (`history.md` Round 219)**: `Copy`/
  `Move` silently DROPPED a resource's metadata entirely — neither
  `copyBytesCore`'s native `CopyingBackend` path nor its generic
  `Read`+`Write` fallback (nor `moveCore`'s own native `MovingBackend`
  path) ever consulted `MetadataBackend` at all. Fixed: both now read
  the source's metadata before the copy/move and write it to the
  destination after a successful one — mirrors S3's real `CopyObject`
  operation, which has an explicit COPY-vs-REPLACE metadata directive
  (this package's fix is equivalent to the COPY/default behavior; a
  REPLACE-style override is not currently exposed and would be a
  natural Phase 2 addition if ever requested). **Disclosed, NOT fixed**:
  a `Move`'s SOURCE metadata sidecar is left orphaned after a successful
  move (no `MetadataBackend.DeleteMetadata` method exists to clean it up)
  — a disk-hygiene gap, not a data-integrity one (the destination's
  metadata is correct); tracked as a follow-up for whoever designs Phase
  2's S3/SMB backends, since a real object-storage backend wouldn't have
  this exact sidecar-file shape to begin with.
- **New 5th conformance-test package, `api/file/filetest`** (D-0010
  alignment) — mirrors `resttest`/`eventstest`/`reqreplytest`'s
  established 7-scenario taxonomy, parameterized over a `file.Backend`.
  Wired into `adapters/file`'s new `conformance_test.go` as the real,
  independent proof. Writing it caught 2 real, previously-undetected
  bugs: `MergedPathParam`'s "decode extracted path vars directly into
  T" promise was declared (`Resource[T].mergeFields` existed, built via
  `NewPathParam`) but NEVER actually wired into dispatch — fixed via a
  new `applyPathMergeFields` helper in `readCore` (see the "List's own
  path-var merge field support" bullet above for the scope boundary);
  and `CapabilityCoverageError`'s pointer-receiver `Error()` method (a
  correct, pre-existing design — just an easy-to-get-wrong `errors.As`
  call-site detail in the new conformance helper itself).
- **Dispatch-core consolidation** (D-0010's OTHER main theme) was
  checked and found ALREADY satisfied: `api/file/dispatch.go` is
  already the one and only dispatch core — only `adapters/file`
  consumes it today (no N-way per-backend duplication exists yet), so
  no `restdispatch`-style extraction was needed.
- `examples/file-api/main.go` rewritten end-to-end: the Router
  mechanism replaces manual `filepath.Join` entirely, and a genuine
  Security-gated `BoundReadMiddleware` demo proves the mechanism live
  (a Note value returned as a properly-typed Go struct, asserted once
  into a named variable).

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
