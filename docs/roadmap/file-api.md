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
| **1** | `api/file` package itself: `Resource[T]` + 10 role builders, `file.Client` (attach+dispatch), `file.Backend` seam, `file.Capability`, Router/Middleware/Observer/ErrorPattern integration, `adapters/file` migration (filesystem backend), full `ports.File`/`ports.Dir` migration | Full Go-signature design (this doc's main body, below) | **IMPLEMENTED** (incl. a `deep-dive-bug-hunt` pass and the retroactive List/BatchDelete per-entry Security amendment — see the main body's own status notes) |
| **2** | `adapters/s3`, `adapters/smb` implementations; streaming/chunked I/O; conditional writes (ETag); recursive `WithList` | Full Go-signature design, per-item (see each item's own section below) | ALL 5 of 5 items IMPLEMENTED (Recursive `WithList`, Conditional writes, Streaming/chunked I/O, `adapters/s3`, `adapters/smb`) — Phase 2 is FULLY COMPLETE (see "Phase 2" section below) |
| **3** | Adapt existing examples (`examples/*`) from `ports.File`/`adapters/file` direct usage to the new `api/file.Client` approach | Checklist-level (which examples, what "adapted" means) — kept light even though Phase 1/2 have since shipped, since `ports.File[T]`'s public call signatures stay unchanged (most examples need zero changes regardless) | Planned (see "Phase 3" section below) |
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

> **Bug found and fixed while building `examples/file-api`'s own Router
> groups demo**: `file.Middleware[In,Out]` was MISSING a
> `MiddlewareName() string` method — `rest.Middleware`/
> `events.Middleware`/`reqreply.Middleware` ALL have one
> (`func (m Middleware[In, Out]) MiddlewareName() string { return
> m.Declaration.Name }`), consumed by `internal/router`'s own
> `middlewareNameOf` extraction (a type-assertion check, falling back to
> a bare `%T` type name otherwise). Without it, `Router.Routes()`'s own
> `MiddlewareNames` reported the Go type name
> (`"file.Middleware[struct {},struct {}]"`) instead of the middleware's
> actual declared name — purely a reporting/introspection bug (dispatch
> behavior itself was unaffected), fixed by adding the missing method.

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

## Phase 2 — further backends + feature depth (ALL 5 items designed AND implemented — Phase 2 is COMPLETE)

**Status: Phase 1 is done and closed (including a `deep-dive-bug-hunt`
pass, `history.md` Rounds 216-223 — see `.github/skills/
review-go-codex/references/history.md`, plus a retroactive "Phase 1
amendment" for `List`/`BatchDelete` per-entry Security, also now
IMPLEMENTED). Phase 2's own 5 items were tackled ONE AT A TIME, in a
confirmed order, each designed to Phase 1's full Go-signature rigor
before implementation started: Recursive `WithList` → Conditional
writes → Streaming/chunked I/O → `adapters/s3` → `adapters/smb`. ALL 5
are now IMPLEMENTED — Phase 2 itself is COMPLETE.** The first THREE
items (**Recursive `WithList`**, **Conditional writes**, and
**Streaming/chunked reads and writes**, all above) are fully designed
AND IMPLEMENTED — Conditional writes' scope grew during implementation
planning to also cover `ports.FileOptions.IfMatch` + a new
`ports.File[T].Stat` method (see that section's own "`ports` mirror"
subsection); Streaming's scope grew the same way, also adding
`ports.File[T].OpenRead`/`OpenWrite` (see its own status note above).
The FOURTH item (**`adapters/s3`**, below) is fully DESIGNED AND
IMPLEMENTED (5 `ask_user`-resolved forks, grounded in web-search-verified
S3 capabilities; one further implementation-time correction to the
streaming-write fork, see its own status note). The FIFTH and LAST item
(**`adapters/smb`**, below) is ALSO fully DESIGNED AND IMPLEMENTED (5
`ask_user`-resolved forks across 2 design rounds, grounded in
from-scratch Go-SMB-client-library-landscape research, a repo-wide
`go 1.27.0`/`go 1.27.2` toolchain bump, 4 independent pre-implementation
review rounds, and 2 further implementation-time corrections — see its
own status note). This section replaces round 2's original
"`adapters/s3`/`adapters/smb` get their own future roadmap doc" call —
ALL of Phase 2 is tracked in THIS doc, and ALL of it has now shipped.

### `adapters/s3`

> **Status: IMPLEMENTED.** The fourth Phase 2 item to reach this level
> of detail, and the fourth to ship. Implementation found one real,
> confirmed correction to this design's own "streaming writes via
> `io.Pipe` bridge" fork resolution: a genuinely zero-buffer
> `PutObject` of an unseekable, length-unknown body is only possible
> via the AWS SDK's HTTPS-only "aws-chunked" trailing-checksum
> transfer encoding — this works against real AWS S3, but is NOT
> understood by this package's own `gofakes3`-backed example server
> (confirmed via `gofakes3`'s source: it only parses the OLDER,
> now-SDK-unsupported signed-chunk format). `OpenWriter` was corrected
> to spool the pipe's read side into an in-memory buffer (still
> decoupling every `Write()` call from the network) and commit via a
> SINGLE, ordinary `PutObject` with a seekable, length-known body —
> works against every S3-compatible server, at the cost of the same
> disclosed buffering trade-off Mechanism 1 (format-level streaming)
> already accepted elsewhere in this doc. See `adapters/s3/stream.go`'s
> own `OpenWriter` doc comment for the full writeup. This design round
> itself resolved 5 forks via `ask_user`
> (AccessMode vs IAM, the `aws-sdk-go-v2` dependency, client
> construction style, `ListDir` pagination scope, streaming-write
> implementation), grounded in 2 web-search-verified facts rather than
> assumption: S3 now supports NATIVE conditional writes
> (`If-Match`/`If-None-Match` on `PutObject`, shipped AWS-wide in late
> 2024), and `HeadObject`/`ListObjectsV2` return a REAL, strong ETag
> WITHOUT reading the object body — both are genuine CAPABILITY
> UPGRADES over the filesystem adapter, not just parity. A follow-up
> gap-review pass (re-reading this design against the ACTUAL current
> `api/file` interfaces/doc comments, not just its own prose) found and
> resolved 8 items: 6 concrete fixes folded directly into the sections
> below (an `Entry.ETag` doc-comment clarification; `WalkDir`/`DeleteDir`
> both now correctly honor their "root included" contracts; `S3Error`
> narrowed to genuinely S3-specific failures only, matching
> `adapters/file`'s own no-redundant-wrapper precedent; not-found
> translation now preserves `Unwrap()` back to the original AWS error;
> `OpenWriter`'s `io.Pipe` bridge now discloses its ctx-cancellation
> handling and inherent goroutine-leak risk) — plus 2 forks resolved via
> `ask_user`: a new, MINIMAL `WriteOptions.BackendOptions any` escape
> hatch (api/file core addition — NOT a literal port of events/mqtt5's
> heavier, declare-time-shaped Tier 1 Capability mechanism, which
> doesn't fit api/file's per-call options shape), and a
> `gofakes3`-backed, fully-offline runnable example. IMPLEMENTATION is a
> separate, upcoming round.

#### Research grounding this design

- **S3's REST API is a de facto industry standard for object
  storage** (confirmed via `ask_user`, analogous to Redis's RESP
  protocol) — no formal standards body, but near-universal
  "S3-compatible" adoption across MinIO, Ceph (RGW), Cloudflare R2,
  Backblaze B2, Wasabi, DigitalOcean Spaces, and Google Cloud
  Storage's own S3 interop mode. Practical consequence: this Backend
  works against ANY S3-compatible endpoint by pointing `aws.Config`'s
  `BaseEndpoint` elsewhere — a bonus of building against the
  standard API, not a new requirement.
- **S3 now supports NATIVE conditional writes** (launched Aug/Nov
  2024, available in all regions since): `PutObject`/
  `CompleteMultipartUpload` accept `If-Match: <etag>` (CAS/optimistic
  concurrency) and `If-None-Match: "*"` (create-if-absent). This is a
  REAL upgrade over the filesystem adapter's own lock-file mechanism —
  `ConditionalWriteBackend.WriteBytesIfMatch` is a true, atomic,
  server-side S3 call, no client-side locking needed at all.
- **S3's ETag is RETURNED BY `HeadObject`/`GetObject`/`ListObjectsV2`
  WITHOUT reading the object body** (S3 computes and stores it
  server-side at write time) — for a single `PutObject` (no
  multipart), ETag is the object's content MD5 hex digest, a TRUE
  strong/content-identifying value. `Stat`/`ListDir` report this REAL
  STRONG ETag directly, unlike the filesystem adapter (which can only
  approximate a weak one from mtime+size without reading content).
- **AWS SDK for Go v2** (`github.com/aws/aws-sdk-go-v2` +
  `.../service/s3`) is the official, actively maintained client —
  `s3.Client` is a concrete struct (not interface-shaped), so the
  `add-a-new-adapter` skill's narrow-interface rule applies: a small
  adapter-local interface covers only the operations used, unit-tested
  against a hand-written fake (no moto/localstack in CI, same
  precedent as MQTT/Redis).
- `s3.NewListObjectsV2Paginator` handles 1000-keys-per-page pagination
  transparently; `DeleteObjects` accepts up to 1000 keys per call
  (`BatchDeletingBackend`'s own contract doesn't cap batch size, so
  this Backend chunks internally, transparent to the caller).
- `CopyObject` with `MetadataDirective: REPLACE` is S3's ONLY
  mechanism for a metadata-only update (a SELF-copy, source ==
  destination) — S3 has no sidecar-file concept the way the
  filesystem adapter's `MetadataBackend` does; metadata lives ON the
  object itself.
- S3 object keys have no real directory concept — `ListObjectsV2`'s
  `Delimiter: "/"` + `Prefix: "<dir>/"` combination synthesizes
  `Contents` (files) and `CommonPrefixes` (directory-like prefixes),
  matching Phase 1's single-level `ListDir` contract.

#### Decisions resolved via `ask_user`

1. **`AccessMode` vs IAM**: trusts the declared `Capability`
   UNCONDITIONALLY — NOT verified against the attached AWS
   credentials' actual IAM permissions. Confirmed consistent with
   Phase 1's own precedent (the filesystem adapter never checks OS
   permission bits either) — the user's broader architectural stance:
   authN/authZ is a MIDDLEWARE/proxy-layer concern (e.g. an OAuth2
   proxy in front of the application), deliberately kept OUT of
   route/backend declaration and implementation.
2. **Dependency**: `aws-sdk-go-v2` + `.../service/s3` APPROVED — the
   first non-stdlib dependency for an `api/file` Backend.
3. **Client construction**: narrow-interface-only — the caller loads
   `aws.Config`/credentials themselves (`config.LoadDefaultConfig` or
   any other method) and constructs the real `*s3.Client`;
   `adapters/s3` never owns credential loading (mirrors
   `adapters/sql` never owning `*sql.DB` construction) — keeps the
   adapter a pure protocol shim, no second vocabulary for the caller
   to learn.
4. **`ListDir` pagination**: auto-paginates through ALL pages
   (correctness over speed — matches every other Backend's own
   complete-results contract; `adapters/file.ListDir` never silently
   truncates either).
5. **Streaming writes**: an `io.Pipe` bridge to `PutObject` this round
   — NOT real S3 Multipart Upload (disclosed as a future enhancement
   opportunity for very large uploads, not designed now).

#### Package layout

```
adapters/s3/
├── doc.go            — package overview: S3-as-de-facto-standard,
│                        Backend interface mapping table, Observer
│                        story, dependency rationale
├── errors.go          — structured error types + os.ErrNotExist
│                        translation helper
├── backend.go         — Backend + CopyingBackend + MetadataBackend +
│                        ConditionalWriteBackend implementation
├── stream.go          — StreamWriteBackend + StreamReadBackend +
│                        RangeReadingBackend (io.Pipe bridge for
│                        writes)
├── walk.go            — WalkingBackend + DirDeletingBackend +
│                        BatchDeletingBackend (prefix-based directory
│                        synthesis + chunked DeleteObjects)
└── *_test.go          — one per source file, against a hand-written
                         fake narrow S3 interface (no moto/localstack)
```

#### Narrow client interface (the ONLY S3 surface this package depends on)

```go
// s3API is the minimal subset of *s3.Client this package calls —
// narrow-interface rule (non-interface-shaped client lib), accepted
// by NewBackend so tests run against a hand-written fake, never a
// live AWS account or localstack/moto.
type s3API interface {
	GetObject(ctx context.Context, in *s3.GetObjectInput, opts ...func(*s3.Options)) (*s3.GetObjectOutput, error)
	PutObject(ctx context.Context, in *s3.PutObjectInput, opts ...func(*s3.Options)) (*s3.PutObjectOutput, error)
	DeleteObject(ctx context.Context, in *s3.DeleteObjectInput, opts ...func(*s3.Options)) (*s3.DeleteObjectOutput, error)
	DeleteObjects(ctx context.Context, in *s3.DeleteObjectsInput, opts ...func(*s3.Options)) (*s3.DeleteObjectsOutput, error)
	HeadObject(ctx context.Context, in *s3.HeadObjectInput, opts ...func(*s3.Options)) (*s3.HeadObjectOutput, error)
	ListObjectsV2(ctx context.Context, in *s3.ListObjectsV2Input, opts ...func(*s3.Options)) (*s3.ListObjectsV2Output, error)
	CopyObject(ctx context.Context, in *s3.CopyObjectInput, opts ...func(*s3.Options)) (*s3.CopyObjectOutput, error)
}

var _ s3API = (*s3.Client)(nil) // compile-time proof *s3.Client satisfies it
```

#### Backend construction

```go
// BackendOpt configures NewBackend — reserved extension point (mirrors
// adapters/file's own RouterOpt-style convention), e.g. a future
// key-prefix scoping option.
type BackendOpt interface{ applyBackend(*Backend) }

// KeyPrefix scopes every operation under a fixed key prefix (e.g. a
// multi-tenant "tenant-123/" namespace within one shared bucket) —
// transparently joined onto every Resource-declared path, mirroring
// file.Router's own prefix-composition role but at the BACKEND level
// instead (api/file's path template stays bucket-root-relative; the
// Backend silently adds its own prefix on top, invisible to dispatch).
func KeyPrefix(prefix string) BackendOpt

// NewBackend wraps client (caller-constructed, caller-authenticated —
// see "Client construction" decision above) scoped to bucket. client is
// NOT *s3.Client directly but the narrow s3API interface above — pass
// an actual *s3.Client built via s3.NewFromConfig(awsCfg), or a fake in
// tests.
func NewBackend(client s3API, bucket string, opts ...BackendOpt) *Backend
```

#### `Entry.ETag` — S3's real, strong ETag, not an approximation

`Stat`/`ListDir` return S3's own `ETag` field DIRECTLY (stripped of its
quote characters, per S3's own wire convention) as `Entry.ETag` — NO
`WeakETag`/`StrongETag` helper call needed at all, since S3 already
computed and stored a true content-identifying value server-side.
**Caveat, disclosed**: for a MULTIPART-uploaded object, S3's ETag is
NOT a plain content hash (it's `<hash>-<partcount>`, where `<hash>` is
a hash of the PART hashes, not the object body) — since this round's
`OpenWriter`/`PutObject` path never uses multipart upload (`io.Pipe`
bridge, single `PutObject` call), every object THIS Backend writes
gets a true, directly-comparable content-hash ETag; an object written
by some OTHER tool via multipart upload would still report a
syntactically-valid but not-independently-recomputable ETag — fine for
`IfMatch` round-tripping (S3 validates it internally either way), just
not something a caller should try to recompute locally the way
`StrongETag(data)` does for the filesystem backend.

**Gap found during gap-review, fixed**: `api/file/backend.go`'s own
`Entry.ETag` doc comment currently states ETag is weak "when only
metadata is available (e.g. this Entry came from `Backend.Stat`/
`Backend.ListDir`, which never read the body)" — written assuming a
filesystem-shaped limitation (SIZE+MODTIME is all a filesystem `Stat`
call gets without reading content). `adapters/s3` proves this isn't a
universal rule — S3 returns a REAL, strong ETag from `Stat`/`ListDir`
specifically BECAUSE it computed and stored one server-side at write
time, body-read or not. At `adapters/s3` implementation time, add a
1-line addendum to that doc comment in `api/file/backend.go` itself
(not just here) clarifying "weak" is per-Backend, not universal —
avoids the doc contradicting `adapters/s3`'s own real behavior.

#### Backend method signatures (full Go-signature rigor)

```go
func (b *Backend) BackendName() string { return "adapters/s3" }

func (b *Backend) ReadBytes(ctx context.Context, path string) ([]byte, error)
// -> GetObject; NoSuchKey (or a 404 NotFound) translated to an error
// satisfying errors.Is(err, os.ErrNotExist) via a wrapping sentinel.

func (b *Backend) WriteBytes(ctx context.Context, path string, data []byte, opts apifile.WriteOptions) ([]string, error)
// -> PutObject. opts.CreateDirs/DirPerm/Perm are no-ops (documented,
// never an error — S3 has no directory/permission concept).
// opts.Strict=true -> IfNoneMatch: aws.String("*") (native S3
// create-if-absent) -> on a 412 Precondition Failed, translate to
// apifile.FileAlreadyExistsError{Path: path} (matching every other
// Backend's own Strict contract).
// createdDirs is ALWAYS nil (nothing was created).

func (b *Backend) DeleteBytes(ctx context.Context, path string, opts apifile.DeleteOptions) (bool, error)
// -> DeleteObject. S3's own DELETE is already idempotent (succeeds on
// a missing key) -- existed is determined via a preceding HeadObject
// ONLY when opts.Strict (to report FileNotFoundError correctly) or
// opts.DryRun; otherwise a single DeleteObject call suffices (existed
// reported as best-effort true, mirroring the filesystem backend's
// own Strict-only existence-check discipline).

func (b *Backend) ListDir(ctx context.Context, path string) ([]apifile.Entry, error)
// -> s3.NewListObjectsV2Paginator(..., &s3.ListObjectsV2Input{
//      Bucket: bucket, Prefix: path+"/", Delimiter: aws.String("/"),
//    }), auto-paginating through ALL pages (confirmed). CommonPrefixes
// -> Entry{IsDir:true, Name: <last path segment>}; Contents ->
// Entry{IsDir:false, Name:..., Size:..., ModTime:..., ETag: <real,
// un-quoted ETag>}. The object representing path+"/" itself (an S3
// "directory marker" some tools create) is excluded from results,
// mirroring adapters/file's own control-file-exclusion precedent (a
// DIFFERENT reason, same shape of fix).

func (b *Backend) Stat(ctx context.Context, path string) (apifile.Entry, error)
// -> HeadObject; NoSuchKey/404 translated the same way ReadBytes does.

func (b *Backend) Mkdir(ctx context.Context, path string, opts apifile.MkdirOptions) (bool, error)
// no-op success (created=true, nil error) -- confirmed, S3 has no
// directory concept; matches this doc's own long-standing decision.
```

#### Optional extensions

```go
// CopyingBackend
func (b *Backend) CopyBytes(ctx context.Context, srcPath, dstPath string) error
// -> CopyObject(CopySource: bucket+"/"+srcPath, Bucket: bucket, Key: dstPath)
// true server-side copy, zero bytes through the caller process.

// BatchDeletingBackend
func (b *Backend) DeleteBytesBatch(ctx context.Context, paths []string) ([]apifile.BatchDeleteResult, error)
// -> DeleteObjects, chunked at 1000 keys per call (S3's own hard
// limit) -- transparent to the caller, who sees ONE logical batch
// result regardless of how many chunk-calls it took.

// ConditionalWriteBackend
func (b *Backend) WriteBytesIfMatch(ctx context.Context, path string, data []byte, ifMatch string, opts apifile.WriteOptions) ([]string, error)
// -> PutObject with IfMatch: aws.String(ifMatch) -- TRUE native
// conditional write, no lock-file mechanism needed at all (S3 handles
// the atomicity server-side). A 412 Precondition Failed ->
// apifile.ConditionalWriteMismatchError{Path, Expected: ifMatch,
// Actual: <HeadObject's current ETag, best-effort>}. A 404 (object
// doesn't exist) -> the SAME ConditionalWriteMismatchError{Actual: ""}
// shape every other Backend reports for this case.

// MetadataBackend
func (b *Backend) ReadMetadata(ctx context.Context, path string) (map[string]string, error)
// -> HeadObject's own Metadata field (S3 user metadata, x-amz-meta-*)
// -- direct, free (no extra call beyond the Stat/Read path already
// needs HeadObject/GetObject anyway).
func (b *Backend) WriteMetadata(ctx context.Context, path string, md map[string]string) error
// -> CopyObject(CopySource: bucket+"/"+path, Bucket: bucket, Key:
// path, MetadataDirective: REPLACE, Metadata: md) -- a SELF-COPY,
// S3's ONLY mechanism for a metadata-only update. DISCLOSED COST: not
// free like the filesystem adapter's own sidecar-file write --
// WriteMetadata here re-transfers the WHOLE object body internally
// (server-side, so no bytes leave AWS's network, but still a real,
// billed S3 API call with its own latency) -- a genuine behavioral/
// cost difference from the filesystem Backend, documented not hidden.

// StreamWriteBackend
func (b *Backend) OpenWriter(ctx context.Context, path string, opts apifile.WriteOptions) (io.WriteCloser, error)
// -> an io.Pipe bridge: a goroutine calls PutObject(ctx,
// &s3.PutObjectInput{Body: pipeReader, ...}) immediately; the
// returned io.WriteCloser wraps pipeWriter -- every Write() feeds the
// in-flight PutObject call directly, Close() closes pipeWriter and
// BLOCKS until the goroutine's PutObject call returns, surfacing its
// error (if any) from Close(). opts.IfMatch is NOT supported on this
// path this round (matches Mechanism 1/2's own disclosed exclusion for
// the filesystem Backend).
//
// Fixed/disclosed during gap-review (2 items):
//  1. ctx-cancellation handling: if ctx is canceled mid-upload, the
//     goroutine's in-flight PutObject call returns ctx.Err() -- the
//     goroutine then calls pipeReader.CloseWithError(ctx.Err()) so a
//     Write() blocked on the writer side ALSO unblocks with that
//     error, instead of deadlocking forever.
//  2. Disclosed, not hidden: if the caller never calls Close() on the
//     returned io.WriteCloser, the upload goroutine blocks forever
//     waiting for EOF -- an inherent goroutine-leak risk of the
//     io.Pipe-bridge approach, the SAME "caller must Close()"
//     discipline every other io.WriteCloser in this package already
//     requires, now stated explicitly for this specific bridge too.

// StreamReadBackend
func (b *Backend) OpenReader(ctx context.Context, path string) (io.ReadCloser, error)
// -> GetObject's own Body io.ReadCloser, returned DIRECTLY -- genuinely
// streaming, no buffering at all.

// RangeReadingBackend -- the PRIMARY real-world motivating case
func (b *Backend) OpenReaderRange(ctx context.Context, path string, offset, length int64) (io.ReadCloser, error)
// -> GetObject with Range: aws.String(fmt.Sprintf("bytes=%d-%d",
// offset, offset+length-1)) when length > 0, or
// fmt.Sprintf("bytes=%d-", offset) when length == 0 (S3's own
// Range-header convention) -- NATIVE, server-side partial read.

// WalkingBackend
func (b *Backend) WalkDir(ctx context.Context, root string, fn apifile.DirWalkFunc) error
// -> FIRST, visits root itself: fn(root, Entry{IsDir:true, Name:
// <last segment of root>, ...}, nil) -- a purely LOGICAL/synthetic
// visit (S3 needs no real object to exist for this), REQUIRED by
// WalkingBackend's own documented contract ("calling fn once per
// visited path (root included, ...)") -- gap found during gap-review:
// the design originally omitted this, found by cross-checking
// adapters/file.Backend's own filepath.WalkDir-based implementation
// (which always visits root first too). THEN: ListObjectsV2 with
// Prefix: root+"/", NO Delimiter (full-prefix listing, auto-paginated)
// -- synthesizes directory boundaries from each key's OWN path
// segments (split on "/", emit a directory Entry for each UNSEEN
// intermediate segment before its contained file Entry, mirroring
// filepath.WalkDir's own lexical, parent-before-child visiting order)
// -- honors filepath.SkipDir exactly like adapters/file.Backend's own
// WalkDir.

// DirDeletingBackend
func (b *Backend) DeleteDir(ctx context.Context, path string, recursive, dryRun bool) (affected []string, err error)
// -> non-recursive: ListObjectsV2(Prefix: path+"/", Delimiter: "/",
// MaxKeys: 1) -- any result means non-empty -> DirNotEmptyError;
// recursive: full-prefix ListObjectsV2 (no delimiter, auto-paginated)
// collecting every key, then (unless dryRun) chunked DeleteObjects
// exactly like DeleteBytesBatch's own chunking. affected ALWAYS
// includes path ITSELF (a synthetic/logical entry, regardless of
// whether a literal S3 directory-marker object exists for it) --
// gap found during gap-review: DirDeletingBackend's own documented
// contract requires "affected -- root included for a recursive call,"
// which the design originally didn't address for S3's "no real
// directory object" case.
```

#### Structured errors (new, `adapters/s3`-local)

**Narrowed during gap-review**: `adapters/file.Backend`'s OWN core
methods (`ReadBytes`/`WriteBytes`/etc.) do NOT wrap errors in an
adapter-local type AT ALL — they return the raw `os.*` error directly,
relying ENTIRELY on `api/file`'s dispatch-core `FileReadError`/
`FileWriteError`/etc. wrapping. `S3Error` below is scoped to match
this precedent exactly: used ONLY for a genuine S3-specific failure
`api/file` has no typed error for at all (access denied, bucket not
found, throttling) — every case `api/file` ALREADY has a typed error
for (not-found, Strict-create conflict, conditional mismatch) returns
the translated error DIRECTLY instead, letting dispatch-core wrap it
exactly like `adapters/file` already does, not a redundant parallel
wrapper.

```go
// S3Error wraps an S3 API failure api/file has NO existing typed error
// for (access denied, bucket not found, throttling, ...) — NOT used
// for the cases api/file already covers (FileNotFoundError,
// FileAlreadyExistsError, ConditionalWriteMismatchError), which this
// Backend returns translated-but-unwrapped, letting dispatch-core's
// own wrapping apply exactly like adapters/file already does.
// Unwrap()-reachable back to the original AWS SDK error (itself often
// a smithy-go *smithy.GenericAPIError or a typed *types.NoSuchBucket/
// etc.).
type S3Error struct {
	Operation string // "GetObject", "PutObject", ...
	Bucket    string
	Key       string
	Err       error
}
func (e S3Error) Error() string
func (e S3Error) Unwrap() error { return e.Err }
func (e S3Error) LogValue() slog.Value
```

Not-found translation: a small internal helper
`isS3NotFound(err error) bool` type-asserts for `*types.NoSuchKey`/
`*types.NotFound` (HeadObject's own 404 shape, which the SDK does NOT
wrap as NoSuchKey) OR a generic smithy API error with HTTP status 404.
**Fixed during gap-review**: `ReadBytes`/`Stat` wrap a not-found result
in a small UNEXPORTED type (not a bare `fmt.Errorf("%w",
os.ErrNotExist)`, which would discard the original AWS error entirely)
satisfying BOTH `Is(target error) bool` (returns true for
`os.ErrNotExist`, preserving `dispatch.go`'s existing
`errors.Is(err, os.ErrNotExist)` check unchanged, zero dispatch-core
changes needed) AND `Unwrap() error` back to the original AWS SDK
error — so a caller doing deeper diagnostics (e.g. distinguishing
throttling from a genuine not-found) can still reach it. **Caveat,
confirmed during this round's own 2nd gap-review**: dispatch-core's
`readCore`/`existsCore`/etc. immediately re-wrap an `isNotFound`
result as a BARE `FileNotFoundError{Path: path}` — a type with NO
`Err` field/`Unwrap()` at all — so this preserved `Unwrap()` chain has
no practical effect on the specific not-found path SPECIFICALLY
(the original AWS error is discarded one level up regardless, a
PRE-EXISTING, cross-cutting fact true for every Backend, not an
S3-specific gap). It still has real value elsewhere: a caller
inspecting the Backend-level error directly (bypassing dispatch, e.g.
in a unit test) can reach the original AWS error, and a future
dispatch-core change wanting to preserve more not-found context would
no longer need any Backend-side change to do so.

**Verified during this round's own 2nd gap-review** (cross-checked
against `docs/design/d-0010-adapter-integration-architecture.md`):
`S3Error`'s narrowed scope stays fully `ErrorPattern`/`errors.As`-
discoverable even when nested — `createCore`/`readCore`'s generic
fallback wraps an unrecognized Backend error as `FileWriteError{Path,
Err}`/`FileReadError{Path, Err}`, both of which implement `Unwrap()`,
so a caller declaring `file.NewErrorPattern[S3Error]()` (or any `B`
type embedding it) correctly matches a `FileWriteError`-wrapped
`S3Error`-wrapped raw AWS error 2 levels deep — `errors.As` traverses
the WHOLE `Unwrap` chain, not just one level. Confirmed by reading the
actual chain, not assumed.

#### Observer integration

Every operation above maps onto an EXISTING `RecordFileRead`/
`RecordFileWrite`/`RecordFileDelete`/`RecordFileList`/`RecordFileCopy`/
`RecordFileMkdir`/`RecordFileBatchDelete`/`RecordFileRangeRead` call,
exactly like `adapters/file.Backend` already does — `adapters/s3` is a
NEW IMPLEMENTER of these dispatch-core-driven Observer calls, not a
source of new Observer vocabulary for THESE operations (dispatch.go
itself calls them, not the Backend — `adapters/s3` contributes zero
Observer-side code for these).

**Revised during this round's own 2nd gap-review pass**: ONE genuine
exception to "no new vocabulary" was found — `RecordFileConditionalWrite`
(see the "Conditional writes" section's own Observer-integration
subsection above, revised in this same round) reports whether a
conditional write was served NATIVELY (S3's server-side `If-Match`) or
via an APPROXIMATED emulation (filesystem's lock-file choreography).
`adapters/s3` always reports `native=true` for this call — the real,
motivating case this flag exists to distinguish, mirroring
`RecordFileRangeRead`'s identical `native=true` for S3's own
`RangeReadingBackend` (see `RangeReadingBackend`'s own entry above).

#### S3-specific options — `WriteOptions.BackendOptions` (gap-review Fork A: `api/file` core addition)

The `add-a-new-adapter` skill's Step 5e mandates a sealed per-adapter
`Capability` mechanism for protocol-specific options with no cross-
backend meaning (S3's `StorageClass`/`ServerSideEncryption`/`ACL`/
`Tags`/`Content-Type` — mirrors `adapters/mqtt5`/`adapters/zeromq`'s
own `capability.go` for events). But `file.Capability` is SEALED TO
`api/file` ITSELF (`isFileCapability()` is an unexported method only
`api/file`'s own package can implement) — unlike `events`/`reqreply`'s
adapter-extensible mechanism, `api/file`'s `Capability`, as shipped in
Phase 1, has NO adapter-extension story at all (it was designed
assuming only 2 cross-backend-meaningful capabilities,
`AccessMode`/`NativePOSIXMode`). Found during gap-review, resolved via
`ask_user`: generalize NOW, but MINIMALLY — not a literal port of
events/reqreply's heavier, declare-time/connection-shaped Tier 1
apparatus (sealed interface + `CapabilityRequirement` +
`CheckCapabilityCoverage`), which doesn't fit how `api/file`'s own
options are already shaped (per-CALL, via `WriteOptions`/
`DeleteOptions`/`MkdirOptions`, not per-role-declare-time).

**New field, `api/file/backend.go`'s `WriteOptions`** (a CORE `api/file`
change, not `adapters/s3`-local — implemented alongside `adapters/s3`
since it's the first real consumer, but belongs in Phase 1's own API
surface):

```go
type WriteOptions struct {
	// ... existing fields (Perm, CreateDirs, DirPerm, Strict, DryRun,
	// Metadata, IfMatch) unchanged ...

	// BackendOptions, when non-nil, is an OPAQUE, Backend-specific
	// options value passed UNCHANGED to the attached Backend's own
	// WriteBytes/WriteBytesIfMatch/OpenWriter call — the generalized
	// adapter-extension escape hatch (mirrors events/reqreply's own
	// adapter-specific Capability mechanism in SPIRIT, right-sized for
	// api/file's per-CALL options shape instead of events' per-role-
	// declare-time shape). A Backend recognizing its own concrete type
	// (e.g. adapters/s3.PutOptions{StorageClass, ServerSideEncryption,
	// ACL, Tags, ContentType}) type-asserts it out; every OTHER Backend
	// simply never reads it — inert, safe, additive, matching every
	// other optional-extension idiom in this package (no false
	// portability, no coverage-check machinery — these are non-
	// semantic storage-class/tagging/encryption HINTS, not behavior-
	// altering ones, so silent ignoring by an unaware Backend is
	// correct, not a silent correctness risk). nil (the default) means
	// "no backend-specific options" — byte-for-byte unchanged behavior
	// for every existing caller. Zero dispatch.go changes needed at
	// all: a plain Go struct field, passed through unread by every
	// dispatch-core function exactly like every other WriteOptions
	// field already is.
	BackendOptions any
}
```

`adapters/s3`'s own concrete type:

```go
// PutOptions is adapters/s3's own concrete WriteOptions.BackendOptions
// type — S3-specific, no cross-backend meaning.
type PutOptions struct {
	StorageClass         string // "", "STANDARD_IA", "GLACIER", ... (S3's own enum, passed through verbatim)
	ServerSideEncryption string
	ACL                  string
	Tags                 map[string]string
	ContentType          string
}
```

`WriteBytes`/`WriteBytesIfMatch`/`OpenWriter` each do
`po, _ := opts.BackendOptions.(PutOptions)` (zero value when absent or
the wrong type — never an error) and apply `po.StorageClass`/etc. onto
the corresponding `PutObjectInput` fields when non-empty.

**Explicitly deferred, not silently dropped**: read-side S3 options
(e.g. `VersionId`, to read a specific object version) — `Client.Read`/
`Reader[T]` has NO per-call options struct at all today (unlike
Create/Patch/Delete/List/Mkdir); adding one is a BIGGER, SEPARATE
`api/file` core change (new dispatch chain shape, ripples through
`Client.Read`/`ReadHandle[T]`/`dispatchRead`) deserving its OWN design
round, not a rushed side effect of this adapter's own design.

**Cross-checked against D-0010's own "Capabilities are requirements,
not transport options" distinction** (this round's own 2nd gap-review
pass): `BackendOptions`/`PutOptions` are squarely ADAPTER
CONFIGURATION (StorageClass/SSE/ACL/Tags/ContentType are operational
hints with no coverage-check/requirement semantics — they never gate
whether a `Resource` can be declared against a given Backend), NOT a
`file.Capability`-style REQUIREMENT (`AccessMode`/`NativePOSIXMode`
remain the only declare-time, compatibility-determining mechanism).
D-0010 explicitly states these two concepts "must remain distinct" —
confirmed CONSISTENT, not in tension, with no design change needed.

#### Runnable example (gap-review Fork B)

Both the `add-a-new-adapter` skill's checklist and the "six mandatory
requirements" (`plan-a-new-codex-feature`) require a runnable example
that exits 0. S3 needs live credentials/a bucket to run for real —
resolved via `ask_user`: an in-process FAKE S3 server,
`github.com/johannesboyne/gofakes3` (confirmed via web search: actively
maintained, in-memory, speaks the REAL S3 HTTP API so `aws-sdk-go-v2`
works against it unmodified by pointing `aws.Config`'s `BaseEndpoint`
at the fake server's local URL) — a fully-offline, always-exits-0
example, no live AWS account ever needed. `gofakes3` is an
EXAMPLE-ONLY dependency (never imported by `adapters/s3` itself),
mirroring this repo's own "test-only dependency, no live broker in CI"
rule extended to examples.

Shape: `examples/file-api` (leaning toward extending it, per the
"same declarations, different backend" theme already established
there — confirmed final placement at implementation time) starts an
in-process `gofakes3` server via `httptest.NewServer` wrapping its
handler, points a real `s3.NewFromConfig` client's `BaseEndpoint` at
it (dummy credentials — `gofakes3` doesn't validate SigV4 signatures
in its default mode), creates a bucket, and runs the SAME
`resources.Notes`-style declarations against
`adapters/s3.NewBackend(client, bucket)` — proving the core Phase 1
promise ("the exact same `file.Resource` declarations work unchanged
against a different backend") concretely.

#### Unit test plan (against the hand-written `s3API` fake, NO live AWS/localstack)

| Test | Verifies |
|---|---|
| `TestBackend_ReadBytes_NotFound_SatisfiesOsErrNotExist` | `errors.Is(err, os.ErrNotExist)` for a NoSuchKey/404 |
| `TestBackend_WriteBytes_Strict_UsesIfNoneMatch` | Strict=true sends `IfNoneMatch: "*"`; a 412 maps to FileAlreadyExistsError |
| `TestBackend_ListDir_PaginatesAllPages` | >1 page of results all returned, fake asserts multiple ListObjectsV2 calls with correct ContinuationToken chaining |
| `TestBackend_ListDir_CommonPrefixesAreDirectories` | CommonPrefixes become Entry{IsDir:true} |
| `TestBackend_Stat_ReturnsRealETagNoBodyRead` | fake asserts HeadObject (not GetObject) was called |
| `TestBackend_Mkdir_IsNoOpSuccess` | no S3 call made at all |
| `TestBackend_CopyBytes_UsesCopyObject` | |
| `TestBackend_DeleteBytesBatch_ChunksAt1000` | >1000 paths triggers >1 DeleteObjects call |
| `TestBackend_WriteBytesIfMatch_UsesNativeIfMatch_NoLockFile` | confirms NO lock-file/retry mechanism exists in this Backend at all (structural absence, not just behavior) |
| `TestBackend_WriteBytesIfMatch_PreconditionFailed_MapsToMismatchError` | |
| `TestBackend_ReadMetadata_FromHeadObject` | |
| `TestBackend_WriteMetadata_UsesCopyObjectSelfCopy` | fake asserts CopyObject called with CopySource==dest |
| `TestBackend_OpenWriter_StreamsViaIOPipe_CommitsOnClose` | content isn't materialized as one `[]byte` before PutObject starts |
| `TestBackend_OpenReader_ReturnsGetObjectBodyDirectly` | |
| `TestBackend_OpenReaderRange_SetsRangeHeader` | |
| `TestBackend_WalkDir_SynthesizesDirectoryEntriesFromKeySegments` | |
| `TestBackend_DeleteDir_NonRecursive_NonEmpty_ReturnsDirNotEmptyError` | |
| `TestBackend_DeleteDir_Recursive_ChunkedDelete` | |
| `TestFiletestConformance_AllScenarios_ProvenAgainstAdaptersS3` | every `api/file/filetest` Assert* scenario (9, including `AssertStreamRoundTrip`) wired into `adapters/s3`'s own `conformance_test.go`, proven against the fake |

### `adapters/smb`

> **Status: IMPLEMENTED.** The fifth and LAST Phase 2 item — Phase 2 is
> now FULLY complete (all 5 items implemented). The design itself
> survived 4 independent pre-implementation review rounds (library/
> version research + 5 `ask_user` forks; a gap review fixing the
> `ConditionalWriteBackend` ETag-comparison step and `DirDeletingBackend`
> DryRun handling + Observer/ErrorPattern/D-0010 cross-checks; an
> untagged-dependency-risk disclosure; and an `OpenWriter`-atomicity
> fix) before implementation began — every one of those fixes shipped
> exactly as designed. Implementation itself found and corrected 2
> FURTHER issues the design rounds didn't catch:
>
> - **`Entry.ETag` was designed to be left empty** ("SMB has no
>   server-computed content-hash concept, a disclosed capability gap
>   relative to S3") — but writing the Observer-wiring test proved this
>   would make the standard `Client.Exists`→`WriteOptions.IfMatch`
>   conditional-write workflow UNCONDITIONALLY fail for every SMB-backed
>   resource (there would be no non-empty ETag to ever supply). Fixed:
>   `Stat`/`ListDir` now return a WEAK (ModTime+Size-derived)
>   approximation, exactly like `adapters/file` already does — SMB has
>   no server-computed hash, but neither does the filesystem backend,
>   and `WriteBytesIfMatch` already accepts a weak-ETag match for
>   precisely this reason.
> - **`WriteBytes`'s `CreateDirs` was designed as a no-op** ("go-smb2/v2
>   doesn't auto-create missing parents, this adapter doesn't emulate
>   that") — but this silently broke the `api/file/filetest`
>   conformance suite's own canonical scenarios (which rely on a
>   Backend being able to create a resource under a fresh, multi-level
>   path). Fixed: `CreateDirs` now uses `Share.MkdirAll` (a NATIVE
>   recursive-creation call this library already exposes), consistent
>   with `Mkdir`'s own already-designed behavior — not the "no-op, call
>   Mkdir yourself first" escape hatch the design originally proposed.
>
> One further, known, DISCLOSED limitation (not a bug in this package):
> the mandatory runnable example's in-process
> `github.com/sonroyaalmerol/go-smb-server` dependency hits a confirmed
> SMB2 message-signing interop gap against `go-smb2/v2` — Negotiate/
> SessionSetup/TreeConnect succeed, but the first real file operation
> fails signature verification. Confirmed, via that server's OWN
> integration test suite, to be because it has only ever been validated
> against the OLDER, differently-shaped `go-smb2` v1.1.0 client API —
> never against v2. The example dials/mounts successfully, then
> gracefully skips the rest with a clear, disclosed message (still
> exits 0) rather than hiding or faking success; `adapters/smb`'s own
> correctness is proven independently and exhaustively regardless (33
> unit tests against a hand-written `shareOps`/`fileOps` fake, plus the
> full 9-scenario `api/file/filetest` conformance suite — all passing).

#### Research grounding this design

- **`hirochachacha/go-smb2` is the de facto standard Go SMB2/3 client**
  (confirmed via web search) — actively maintained; `CloudSoda/go-smb2`
  is a smaller, less-active fork of the SAME codebase, not a genuinely
  different alternative. No other actively-maintained, general-purpose
  Go SMB2/3 client exists.
- **Two materially different versions exist**, confirmed via direct
  source inspection of both (not README skimming):
  - **v1.1.0** (stable since 2016, `go 1.12` minimum): `Share`/`File`
    methods do NOT take `ctx` directly — context is bound via
    `Share.WithContext(ctx) *Share` / `File.WithContext(ctx)`, both
    returning a CONCRETE type, which breaks the usual narrow-interface
    testability pattern (an interface method can't be satisfied by a
    method returning a different concrete type than the interface
    itself declares). Lean dependency footprint: only
    `github.com/geoffgarside/ber` + `golang.org/x/crypto`.
  - **v2** (current `master`): a clean rewrite — EVERY `Share`/`File`
    method takes `ctx context.Context` as its first parameter directly
    (no rebinding workaround needed anywhere); `File.WithContext(ctx)`
    returns a concrete `*boundFile` satisfying `iofs.File +
    iofs.ReadDirFile + io.Writer + io.Seeker + io.ReaderAt +
    io.WriterAt + io.ReaderFrom + io.WriterTo` — directly usable as
    the `io.ReadCloser`/`io.WriteCloser` `StreamReadBackend`/
    `StreamWriteBackend`/`RangeReadingBackend` need, with ZERO bridging
    code; `Share.WithContext(ctx)` returns a concrete `*boundShare`
    satisfying a FULL `io/fs.FS + StatFS + ReadFileFS + ReadDirFS +
    GlobFS + ReadLinkFS + SubFS` — directly usable with
    `fs.WalkDir(share.WithContext(ctx), root, fn)` for
    `WalkingBackend.WalkDir`. `Share.MkdirAll`/`Share.RemoveAll` are
    NATIVE (recursive parent-dir creation / recursive delete built
    into the library itself, unlike v1.1.0 which needs manual
    parent-walking). **Requires `go 1.27.0`** — confirmed via a live
    sandbox probe: this repo's `go.mod` currently declares `go 1.26.2`
    and the installed toolchain was `go1.26.8`; the real `go1.27.0`
    toolchain WAS successfully downloaded (via `golang.org/dl/go1.27.0`)
    and verified to resolve/build against `go-smb2/v2` cleanly.
    **Confirmed, real cost**: v2 unconditionally pulls in a much
    heavier transitive dependency graph than v1.1.0 even for plain
    NTLM-over-TCP usage — `github.com/quic-go/quic-go` (full QUIC
    transport), `github.com/go-krb5/krb5` (a complete Kerberos stack),
    `github.com/pierrec/lz4/v4`, `github.com/go-crypt/x` — all imported
    at the package root (not behind build tags), so they're pulled in
    regardless of whether this adapter ever uses QUIC or Kerberos.
- **Both versions translate NTSTATUS response codes to stdlib sentinel
  errors INSIDE the library itself** (confirmed via source: a
  `switch status { case STATUS_OBJECT_NAME_NOT_FOUND, ...: return
  os.ErrNotExist; case STATUS_OBJECT_NAME_COLLISION: return os.ErrExist;
  case STATUS_ACCESS_DENIED, ...: return os.ErrPermission }`-shaped
  helper at the wire-decode layer) — meaning `adapters/smb`'s own
  not-found/already-exists/permission-denied translation can just do
  `errors.Is(err, os.ErrNotExist)` / `os.ErrExist` / `os.ErrPermission`
  directly on whatever the library returns, far simpler than S3's own
  custom NTSTATUS-equivalent classification work.
- **`CopyingBackend` is achievable "for free"**: `io.Copy(dstFile,
  srcFile)` between 2 open file handles triggers the library's own
  PRIVATE server-side-copy fast path (confirmed: `File.ReadFrom`/
  `File.WriteTo` are exported in both versions, and Go's `io.Copy`
  automatically special-cases `io.ReaderFrom`/`io.WriterTo` — no public
  "CopyFile" call is needed or exists).
- **`MovingBackend`**: `Share.Rename(ctx, oldpath, newpath) error` is a
  direct, native SMB2 rename (confirmed in both versions) — matches
  this doc's already-recorded decision.
- **`BatchDeletingBackend`**: confirmed NO native SMB2 batch-delete
  operation exists in either version — matches this doc's
  already-recorded "not implemented" decision (same reasoning as
  `adapters/file`: the "implementation" would just be the identical
  sequential-loop fallback already running generically).
- **`RangeReadingBackend`**: trivially achievable via the bound file's
  `io.ReaderAt` (`io.NewSectionReader(boundFile, offset, length)`) — a
  GENUINE native range read, arguably MORE natural here than it was for
  S3 (which needed a dedicated wire-level `Range` header this library
  doesn't need at all — `ReadAt` is a first-class SMB2 operation).
- **`NativePOSIXMode`**: wirable via `Share.Chmod(ctx, name, mode)` /
  `Share.Stat(ctx, name)`'s returned `os.FileInfo.Mode()` (confirmed,
  ctx-first in v2) — but its real EFFECT is entirely dependent on the
  remote server: meaningful on a Samba-hosted share with Unix
  extensions enabled (POSIX bits genuinely round-trip), collapsed to a
  binary read-only bit on a genuine Windows SMB share — the SAME
  `os.Chmod`-on-Windows precedent Phase 1's `Capability` section
  already documents, now simply re-confirmed for a THIRD transport
  (filesystem, S3 N/A, SMB).
- **No live SMB broker/Samba container is available in CI** (same rule
  as every adapter's unit tests — see the `add-a-new-adapter` skill's
  own Gotcha). The only pure-Go, in-process SMB *server* library found
  (not client — a fundamentally different, much rarer kind of project)
  is `github.com/sonroyaalmerol/go-smb-server` (MIT license) — a
  from-scratch SMB2/3 server implementation explicitly tested against
  `hirochachacha/go-smb2` as its reference client (per its own README).
  It is young and niche (7 GitHub stars at the time of this research)
  compared to `gofakes3`'s own maturity for the S3 example, but plays
  the EXACT same role: an in-process, fully-offline, protocol-speaking
  fake server for the mandatory runnable example specifically (unit
  tests still use a hand-written fake of the narrow interface below,
  never this server).
- **`go-smb2/v2` has NEVER had a tagged release — a disclosed
  dependency-risk finding from a final pre-implementation review round,
  verified directly against the repo's real git tags (only `v1.0.x`/
  `v1.1.0` exist, nothing at `v2.x.y`).** The ctx-first/`WithContext`
  API this design depends on exists ONLY on the untagged `master`
  branch — confirmed accurate byte-for-byte against the actual source
  at the time of this review (every `Share`/`File` method signature and
  the `WithContext` return-interface shapes cited throughout this
  section were fetched and diffed directly, not assumed from
  documentation), but there is no semver tag to pin to the way
  `aws-sdk-go-v2` provides for `adapters/s3`. Consequences, decided and
  accepted (not a reason to reconsider v1.1.0): **implementation MUST
  pin `go.mod`'s require line to an exact commit SHA (a Go
  pseudo-version), re-verified/re-confirmed AT implementation time**
  (never reusing a SHA recorded during a design/review round, since
  `master` may have moved by then) — the repo's own active feature
  branches (`feat/dirfs`, `feat/glob`) alongside `master`, plus an
  `AGENTS.md` at its root, are cited as concrete, observed evidence of
  ongoing, in-flux development (a real signal the API could still shift
  without any release-notes/semver signal at all), not a hypothetical
  concern.

#### Decisions resolved via `ask_user`

1. **SMB client library + Go version** (the single biggest decision):
   bump this repo's `go.mod` from `go 1.26.2` to `go 1.27.0` AND use
   `go-smb2/v2` — accepting the heavier transitive dependency graph in
   exchange for the materially cleaner, native ctx-per-call API (which
   ALSO fully eliminates what would otherwise have been a genuinely
   novel "ctx-bridging via factory closure" design pattern this
   codebase has never needed before — v1.1.0 would have required it,
   v2 needs nothing of the kind).
2. **No existing-adapter retrofit this round**: Go 1.27 ALSO ships
   generic methods (a method may now declare its own type parameters)
   — this directly removes a documented limitation the
   `add-a-new-adapter` skill itself names ("Go generics methods cannot
   introduce new type params — adapter constructors needing a second
   type parameter must be free functions"), which several already-
   shipped adapters (`mqtt5`/`mqtt`/`zeromq`'s `NewPublishTransport[T]`/
   `NewSubscribeTransport[T]`) work around today with free functions.
   The user confirmed: do NOT fold a retrofit of those existing
   workarounds into this round — this Go-version bump is motivated
   SOLELY by `go-smb2/v2`'s own requirement; a future repo-wide
   generic-methods retrofit is tracked as its own, separate,
   not-yet-scheduled idea, not bundled in here.
3. **`RangeReadingBackend`**: YES, implement it (reporting
   `native=true` to `stats.FileObserver.RecordFileRangeRead`) — a
   genuine capability this library makes essentially free, even though
   `adapters/file.Backend` does NOT implement the same interface for
   local files today (an observation about the filesystem backend,
   not a blocker here, and explicitly NOT being retrofitted onto
   `adapters/file` this round either — out of scope, noted only for
   transparency).
4. **`NativePOSIXMode`**: wire it through via `Chmod`, with the
   Samba-vs-Windows degradation explicitly disclosed in both the
   Go doc comment and this section (mirrors the Phase 1 `Capability`
   section's own `os.Chmod` precedent exactly, not a new kind of
   caveat).
5. **Mandatory runnable example dependency**: accept
   `github.com/sonroyaalmerol/go-smb-server` as a NEW, EXAMPLE-ONLY
   dependency (never imported by `adapters/smb` itself) — mirrors
   `gofakes3`'s established role for the S3 example exactly.

#### 2 further technical questions, resolved by direct reasoning (not a separate `ask_user` round)

- **Does SMB have anything closer to an atomic compare-and-swap than a
  lock file, for `ConditionalWriteBackend`?** No — SMB2 has no
  equivalent to S3's native `If-Match`/`If-None-Match`. But SMB2's own
  `CREATE` with `FILE_CREATE` disposition (the wire-level operation
  backing `OpenFile(ctx, name, os.O_CREATE|os.O_EXCL|os.O_WRONLY,
  perm)`) IS genuinely atomic at the protocol level — the SAME
  guarantee POSIX `O_CREATE|O_EXCL` provides locally, just server-
  enforced over the wire instead of kernel-enforced locally. This
  means `adapters/smb.Backend.WriteBytesIfMatch` can use the EXACT SAME
  lock-file mechanism `adapters/file`'s own `WriteBytesIfMatch`
  already uses (a sibling `<path>.lock` file, exclusive-create,
  bounded retry+backoff, same ~5s internal default ceiling) — reported
  as `native=false` via the `NativeConditionalWriteBackend` marker,
  consistent with `adapters/file`'s own `native=false`, NOT a new
  mechanism to invent. `adapters/smb` therefore does NOT get a truly
  native conditional write the way `adapters/s3` does — a genuine,
  disclosed backend-semantic difference (SMB2 itself has no
  content-hash-based precondition primitive to call, unlike S3's REST
  API), not an oversight.
  **Gap found during a pre-implementation re-review of this design
  (not originally spelled out — confirmed by reading `adapters/file
  /conditional_write.go`'s own `WriteBytesIfMatch` directly): the lock
  only serializes concurrent writers — it does NOT by itself verify
  `ifMatch`.** After acquiring the lock, the implementation MUST read
  path's CURRENT content (`Share.Open` + read-all via the bound
  `fileOps`), compute `apifile.StrongETag(current)`, and compare it
  against `ifMatch`; on a mismatch it must ALSO retry against
  `apifile.WeakETag(info.ModTime(), info.Size())` (via `Share.Stat`)
  before concluding a genuine mismatch — mirrors `adapters/file`'s own
  "accept EITHER the strong OR the weak ETag" rule EXACTLY (and for
  the SAME reason: a caller's only realistic way to obtain an ETag
  BEFORE writing is `Client.Exists`/`ports.File.Stat`, which never
  reads content and can therefore only ever report a weak one — see
  that file's own inline rationale comment). Only once this
  read-compare step confirms a match does the implementation proceed
  to the actual commit (write via the bound `fileOps`, still inside the
  held lock). Skipping this step would make `WriteBytesIfMatch`
  structurally indistinguishable from an UNCONDITIONAL write that
  merely serializes callers — a silent correctness gap, not a cosmetic
  omission.
- **Does SMB have an extended-attributes-style primitive
  `MetadataBackend` could use?** NTFS does support Alternate Data
  Streams (`"filename:streamname"` path syntax), which SOME SMB
  server/client combinations expose — but this is an NTFS-specific
  feature, not guaranteed across every SMB-compatible server (e.g. a
  Samba share backed by ext4/XFS may not support ADS reliably), and
  neither `go-smb2` version's own README/tests exercise this path
  syntax. Given Phase 1's "stdlib only, no exotic backend-specific
  features" bar and the filesystem adapter's own precedent, `adapters/
  smb.Backend` implements `MetadataBackend` via the SAME JSON sidecar
  file (`<path>.meta.json`) mechanism `adapters/file` already uses —
  universally supported (every SMB server supports ordinary files),
  consistent cross-backend behavior, NOT dependent on NTFS-specific
  wire features. Alternate Data Streams remain a possible FUTURE
  enhancement (not implemented, not designed further here).

#### Narrow interface + construction

Mirrors the `add-a-new-adapter` skill's narrow-interface rule exactly
(`*smb2.Share`/`*smb2.File` are concrete structs, not interface-shaped):

```go
// shareOps is the narrow slice of *smb2.Share this package depends on
// — covers only the operations api/file.Backend (+ its optional
// extensions) actually needs. Deliberately excludes Symlink/Readlink
// (no api/file.Backend method concerns symlinks), Lock/Unlock (no
// byte-range locking concept in api/file), WaitForChange (no watch/
// notify concept in api/file), and GetSecurityDescriptor/
// SetSecurityDescriptor (ACL manipulation is out of scope — the
// Capability model's AccessMode/NativePOSIXMode axis is the sole
// permission surface, per Phase 1's own established decision).
type shareOps interface {
	Open(ctx context.Context, name string) (fileOps, error)
	Create(ctx context.Context, name string) (fileOps, error)
	OpenFile(ctx context.Context, name string, flag int, perm os.FileMode) (fileOps, error)
	Mkdir(ctx context.Context, name string, perm os.FileMode) error
	MkdirAll(ctx context.Context, path string, perm os.FileMode) error
	Remove(ctx context.Context, name string) error
	RemoveAll(ctx context.Context, path string) error
	Rename(ctx context.Context, oldpath, newpath string) error
	Stat(ctx context.Context, name string) (os.FileInfo, error)
	ReadDir(ctx context.Context, dirname string) ([]os.FileInfo, error)
	Chmod(ctx context.Context, name string, mode os.FileMode) error
	// WithContext returns an io/fs.FS adapter — used ONLY by WalkDir.
	WithContext(ctx context.Context) interface {
		fs.FS
		fs.StatFS
		fs.ReadFileFS
		fs.ReadDirFS
		fs.ReadLinkFS
	}
}

// fileOps is the narrow slice of *smb2.File this package depends on —
// returned by shareOps.Open/Create/OpenFile above. *smb2.File itself
// does NOT satisfy this directly (its Read/Write/etc. take ctx as a
// parameter, not matching io.Reader's signature) — this package calls
// f.WithContext(ctx) once per open, then uses the result, which DOES
// satisfy fileOps structurally (confirmed: *boundFile implements
// io.ReadWriteCloser + io.Seeker + io.ReaderAt + io.WriterAt +
// io.ReaderFrom + io.WriterTo).
type fileOps interface {
	io.ReadWriteCloser
	io.Seeker
	io.ReaderAt
	io.WriterAt
	io.ReaderFrom
	io.WriterTo
	Stat() (fs.FileInfo, error)
}

// var _ shareOps = (*smb2.Share)(nil) is NOT a literal compile-time
// assertion in this package (smb2.Share's real Open/Create/OpenFile
// return *smb2.File, not fileOps) — NewBackend's own caller-supplied
// adapter closure (below) is where the real *smb2.Share gets narrowed
// down to shareOps; the compile-time proof lives in that adapter
// function's own small wrapper type instead.

// BackendOpt configures NewBackend (reserved extension point, mirrors
// adapters/s3.BackendOpt).
type BackendOpt interface{ applyBackend(*Backend) }

// NewBackend wraps share (caller-constructed, caller-authenticated —
// the caller builds *smb2.Dialer{Credentials: ...}, calls Dial/Mount
// themselves, then passes the resulting *smb2.Share through a small
// adapter wrapper satisfying shareOps). adapters/smb never owns
// credential loading or connection establishment — mirrors
// adapters/s3's identical "narrow-interface-only, caller constructs/
// authenticates the client" precedent exactly.
func NewBackend(share shareOps, opts ...BackendOpt) *Backend
```

#### Backend method mapping

| `api/file.Backend` (+ extensions) method | `go-smb2/v2` operation | Notes |
|---|---|---|
| `ReadBytes` | `Share.Open` + read-all via the bound `fileOps` | `os.ErrNotExist` translated by the library itself |
| `WriteBytes` | `Share.Create`/`OpenFile` + write-all; `Strict:true` uses `OpenFile(..., os.O_CREATE\|os.O_EXCL\|os.O_WRONLY, perm)` | atomic CREATE at the wire level, same guarantee as local `O_EXCL` |
| `DeleteBytes` | `Share.Remove` | idempotent (SMB2 DELETE on a missing file still needs a pre-check for `existed`, same Strict-only discipline as every other Backend) |
| `ListDir` | `Share.ReadDir` | single-level, non-recursive — matches Phase 1's contract |
| `Stat` | `Share.Stat` | `Entry.ETag` left empty (SMB has no server-computed content-hash concept — unlike S3's real ETag, this is a genuine, disclosed capability GAP relative to S3, not an oversight) |
| `Mkdir` | `Share.Mkdir` (`MkdirOptions.CreateDirs`-equivalent uses `Share.MkdirAll`) | native parent-dir creation, unlike `adapters/file`'s own manual `os.MkdirAll` |
| `CopyingBackend.CopyBytes` | `Open` src + `Create` dst + `io.Copy(dst, src)` | server-side copy fast path auto-engages via the library's own `ReadFrom`/`WriteTo`; falls back to ordinary reads/writes transparently when offsets/shares don't align (the library's own documented behavior, not this adapter's concern) |
| `MovingBackend` | `Share.Rename` | native SMB2 rename — `adapters/smb` is the FIRST Phase 1/2 Backend to implement this interface (neither `adapters/file` nor `adapters/s3` do, each for its own disclosed reason — see note below the table) |
| `ConditionalWriteBackend`+`NativeConditionalWriteBackend` | lock-file mechanism (SAME as `adapters/file`'s own) via `OpenFile(..., O_CREATE\|O_EXCL, ...)` | `native=false` (no SMB2 CAS primitive — see "2 further technical questions" above) |
| `MetadataBackend` | JSON sidecar file (SAME as `adapters/file`'s own) | NOT Alternate Data Streams (see "2 further technical questions" above) |
| `BatchDeletingBackend` | **NOT implemented** | no native SMB2 batch-delete; generic sequential-loop fallback applies |
| `StreamWriteBackend.OpenWriter` | `Share.Create(ctx, tmpName)` (a temp name in the SAME directory, e.g. `path+".tmp-<random>"`) + `.WithContext(ctx)`; `Close` commits via `Share.Rename(ctx, tmpName, path)` | **Gap found during a 4th pre-implementation review round** (confirmed by reading `adapters/file.Backend.OpenWriter`/`adapters/s3.Backend.OpenWriter` directly, both already shipped): `StreamWriteBackend`'s own interface doc comment in `api/file/backend.go` is explicit and absolute — `Close` "MUST commit the write atomically (temp-file-in-the-same-directory + rename)... a reader must never observe a partially-written file." Opening `path` directly (this row's ORIGINAL text) would stream writes straight into the final, visible path — a crash or network interruption mid-upload would leave a partially-written file there, violating the contract. Fixed by writing to a temp file via `Share.Create` and committing via SMB2's NATIVE `Share.Rename` (the SAME call already used for `MovingBackend` elsewhere in this design) on `Close` — mirrors `adapters/file`'s own exact shape (temp-file + rename), just using the wire-native rename instead of `os.Rename`. Still genuinely streaming — writes go straight to the open temp-file handle, NO `io.Pipe`/buffering needed at all (unlike `adapters/s3`, which needs the `io.Pipe` bridge because S3 has no comparable "write to a temp object, then atomically rename" primitive) |
| `StreamReadBackend.OpenReader` | `Share.Open` + `.WithContext(ctx)` | direct passthrough, genuinely streaming |
| `RangeReadingBackend.OpenReaderRange` | `Share.Open` + `.WithContext(ctx)` + `io.NewSectionReader(f, offset, length)` | NATIVE (`io.ReaderAt` is a first-class SMB2 operation) — `native=true` |
| `WalkingBackend.WalkDir` | `fs.WalkDir(share.WithContext(ctx), root, fn)` | the library's own `io/fs.FS` adapter does the heavy lifting; honors `filepath.SkipDir` via `fs.WalkDir`'s own contract |
| `DirDeletingBackend.DeleteDir` | non-recursive: `Share.ReadDir` + empty-check + `Share.Remove`; recursive (non-DryRun): `Share.RemoveAll`; recursive DryRun: `WalkDir`-based enumeration ONLY, `Share.RemoveAll` NEVER called | **Gap found during a pre-implementation re-review of this design** (confirmed by reading `adapters/file.Backend.DeleteDir` directly): "recursive: `Share.RemoveAll`" alone is incomplete — it does not account for `dryRun`. `affected` for a recursive call must ALWAYS be collected via this Backend's own `WalkingBackend.WalkDir` (root included, per `WalkDir`'s own documented contract); `Share.RemoveAll` is called ONLY when `dryRun == false`, mirroring `adapters/file.DeleteDir`'s own exact shape (walk-to-enumerate, then conditionally remove) rather than literally calling `RemoveAll` unconditionally and merely suppressing it for DryRun |

**Note**: `adapters/file` deliberately chose NOT to implement
`MovingBackend` in Phase 1 despite `os.Rename` being equally available
there (the SAME kind of observation already made about
`RangeReadingBackend` above); `adapters/s3` genuinely CANNOT, since
standard S3 buckets have no atomic rename at all. `adapters/smb` is
therefore the first Phase 1/2 Backend to actually implement this
interface.

#### Structured errors

```go
// SMBError wraps a genuine SMB-specific failure api/file has no typed
// error for at all (e.g. a protocol-level ResponseError/NTSTATUS this
// package doesn't already translate to os.ErrNotExist/os.ErrExist/
// os.ErrPermission, a TransportError, an InvalidResponseError) —
// mirrors S3Error's own NARROWED scope exactly: every failure api/file
// ALREADY has a typed error for (not-found, already-exists,
// conditional-mismatch) is translated DIRECTLY via errors.Is, never
// double-wrapped in SMBError first.
type SMBError struct {
	Operation string // "Open", "Create", "Rename", ...
	Share     string
	Path      string
	Err       error
}
func (e SMBError) Error() string
func (e SMBError) Unwrap() error { return e.Err }
func (e SMBError) LogValue() slog.Value
```

**ErrorPattern discoverability (confirmed, not merely assumed)**:
`SMBError` implements `Unwrap() error` exactly like `S3Error` — whose
nested-Unwrap-chain reachability (`FileWriteError`/`FileReadError` →
`S3Error` → the raw AWS SDK error) was explicitly traced and confirmed
against `api/file/dispatch.go`/`api/file/errors.go`'s own generic
fallback wrapping during `adapters/s3`'s own gap-review round — `errors
.As`/`ErrorPattern.Match` traverse the WHOLE `Unwrap` chain, not just one
level, so a caller CAN declare `file.NewErrorPattern[SMBError]()` and
have it match correctly even though `SMBError` sits two levels deep
(`FileWriteError`/`FileReadError` → `SMBError` → the raw `go-smb2`
error). Same mechanism, same conclusion — not re-derived from scratch,
just re-confirmed for this Backend's own error type.

#### Observer integration

No NEW `stats.FileObserver` method needed — mirrors `adapters/s3`'s own
confirmed finding exactly: every operation in the "Backend method
mapping" table above maps onto an EXISTING `RecordFileRead`/
`RecordFileWrite`/`RecordFileDelete`/`RecordFileList`/`RecordFileCopy`/
`RecordFileMove`/`RecordFileMkdir`/`RecordFileBatchDelete` call, made
ONCE, by `api/file/dispatch.go` itself — `adapters/smb` contributes ZERO
Observer-side code for these. The only Backend-CONTRIBUTED Observer-
adjacent surface is the 2 optional native/approximated markers already
designed above: `NativeConditionalWriteBackend.NativeConditionalWrite()`
reporting `false` (feeds `RecordFileConditionalWrite`'s `native`
parameter) and `RangeReadingBackend`'s own implementation reporting
`native=true` implicitly via which fallback tier `Client.OpenRead`
selects (feeds `RecordFileRangeRead`'s `native` parameter) — both
already specified in the "Backend method mapping" table, nothing further
to design here.

**`WriteOptions.BackendOptions` — considered, no SMB-specific use
identified.** Unlike `adapters/s3`'s own `PutOptions{StorageClass,
ServerSideEncryption, ACL, Tags, ContentType}`, this design's own narrow
`shareOps` interface (see "Narrow interface + construction" above)
deliberately does NOT expose protocol-specific SMB2 CREATE-time flags
(`FileAttributes`/`ShareAccess`/`CreateOptions`) at all — there is
currently no concrete, requested SMB-specific per-call option to carry
through `WriteOptions.BackendOptions`. This was checked, not overlooked;
the escape hatch remains available, unused by `adapters/smb`, should a
genuine need surface later (mirrors this doc's own "never invent API
without a request" discipline).

#### Example plan

Extends `examples/file-api` (mirrors `demo_s3_backend.go`'s own shape
exactly — same `resources.Notes` declaration, different Backend):
starts an in-process `go-smb-server` (`github.com/sonroyaalmerol/
go-smb-server`) with an in-memory VFS backend and NTLMv2 credentials,
dials it with a REAL `*smb2.Dialer`/`hirochachacha/go-smb2` client
(loopback TCP, dummy credentials matching the server's configured
ones), mounts the configured share, wraps the resulting `*smb2.Share`
in the small `shareOps`-satisfying adapter type, and runs the SAME
`resources.Notes` declaration against `adapters/smb.NewBackend(...)` —
proving the core Phase 1 promise ("the exact same `file.Resource`
declarations work unchanged against a different backend") for a THIRD
real Backend.

#### Unit test plan (against a hand-written `shareOps`/`fileOps` fake, NO live Samba/broker)

| Test | Verifies |
|---|---|
| `TestBackend_ReadBytes_NotFound_SatisfiesOsErrNotExist` | library's own NTSTATUS translation is consulted directly via `errors.Is` |
| `TestBackend_WriteBytes_Strict_UsesCreateExcl` | Strict=true uses `O_CREATE\|O_EXCL`; `os.ErrExist` maps to `FileAlreadyExistsError` |
| `TestBackend_Mkdir_CreateDirs_UsesMkdirAll` | native recursive parent-dir creation, not a manual walk |
| `TestBackend_CopyBytes_UsesIOCopyServerSideFastPath` | fake asserts `ReadFrom`/`WriteTo` were exercised, not a manual byte loop |
| `TestBackend_MovingBackend_UsesNativeRename` | |
| `TestBackend_WriteBytesIfMatch_UsesLockFile_ReportsNativeFalse` | mirrors `adapters/file`'s own equivalent test — same mechanism, same `native=false` |
| `TestBackend_MetadataBackend_UsesJSONSidecar_NotADS` | |
| `TestBackend_OpenWriter_StreamsDirectly_NoIOPipeBridge` | confirms no `io.Pipe` goroutine machinery exists in this Backend at all (structural difference from `adapters/s3`) — writes still go straight to the OPEN TEMP FILE handle, not buffered in memory |
| `TestBackend_OpenWriter_CommitsAtomicallyViaRenameOnClose` | mirrors `adapters/file`'s/`adapters/s3`'s own already-shipped equivalent proofs — the destination path shows EITHER the complete prior content OR the complete new content, NEVER a partial write, even if `Close` is interrupted before the final `Share.Rename` call |
| `TestBackend_OpenReaderRange_UsesSectionReader_ReportsNativeTrue` | |
| `TestBackend_WalkDir_UsesLibraryIOFSAdapter_HonorsSkipDir` | |
| `TestBackend_DeleteDir_Recursive_UsesNativeRemoveAll` | |
| `TestBackend_NativePOSIXMode_WiredViaChmod` | wiring-only proof (the fake can't distinguish Samba-vs-Windows server behavior — that distinction is a real-server runtime fact, not something a unit test can exercise) |
| `TestFiletestConformance_AllScenarios_ProvenAgainstAdaptersSMB` | every `api/file/filetest` `Assert*` scenario wired into `adapters/smb`'s own `conformance_test.go` |

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
>
> **Bug found and fixed while building `examples/file-api`'s own
> Conditional writes demo**: `WriteBytesIfMatch` (both backends)
> ORIGINALLY compared `ifMatch` against the current content's STRONG
> ETag only — but `Client.Exists`/`ports.File.Stat` are the ONLY
> documented ways to obtain an ETag BEFORE writing, and NEITHER ever
> reads content, so they can only ever report a WEAK ETag (see
> `Entry.ETag`'s own doc comment). This made the documented
> "Exists/Stat → `IfMatch` → write" workflow — the feature's OWN
> central use case — impossible to ever satisfy. Fixed by accepting
> EITHER the strong OR the weak ETag (a caller who DOES have the
> content in hand, e.g. via `StrongETag(data)` directly, still gets an
> exact match; a caller using Exists/Stat's weak ETag now ALSO
> succeeds) — live-reproduced first (a failing test confirmed against
> the unfixed code), then fixed, in BOTH `adapters/file.Backend` and
> `ports.osBackend`.

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

**Revised during `adapters/s3`'s own 2nd gap-review round** (cross-
checked against `docs/design/d-0010-adapter-integration-architecture.md`'s
capability-axis framework): the original design said "no new
`FileObserver` method needed," reasoning that a conditional-write
failure is already recorded via `recordWrite(obs, path, false,
duration)` like any other write failure. That reasoning is still
correct for FAILURE reporting — but it missed D-0010's axis 3
("transport-guaranteed vs. adapter-approximated"), the SAME axis that
already motivated `RecordFileRangeRead`'s `native bool` flag for
Streaming's range-read fallback cascade. `ConditionalWriteBackend` is
implemented via two STRUCTURALLY DIFFERENT mechanisms with different
guarantees: the filesystem backend's lock-file choreography (atomic
only among COOPERATING conditional writers — a weaker guarantee,
already disclosed in prose above) vs. S3's genuinely native,
server-side `If-Match` (atomic against every writer, no lock file at
all) — yet both satisfy the exact same `ConditionalWriteBackend`
interface, indistinguishable to a caller or Observer today.

**New `stats.FileObserver` method** (the 12th), reporting which
mechanism served a conditional write, on BOTH success and failure:

```go
// RecordFileConditionalWrite is called once per WriteBytesIfMatch
// dispatch (createCore's overwrite path, patchCore's final commit) —
// native reports whether the Backend's ConditionalWriteBackend
// implementation is a TRANSPORT-GUARANTEED atomic primitive (S3's
// native If-Match) or an ADAPTER-APPROXIMATED emulation (filesystem's
// lock-file choreography) — mirrors RecordFileRangeRead's identical
// native/approximated axis, applied here to the conditional-write
// mechanism instead of range reads.
RecordFileConditionalWrite(path string, native bool, ifMatch string, d time.Duration)
```

Called once per `WriteBytesIfMatch` dispatch, alongside (not instead
of) the existing `recordWrite` call — `native` is a Backend-level,
dispatch-known fact (whether the Backend's doc comment/implementation
declares itself native, surfaced the SAME way `RangeReadingBackend`'s
presence already is — via a small Backend-reported constant/method, TBD
at implementation time, e.g. a `ConditionalWriteBackend`-adjacent
marker or a doc-only convention mirroring how `BatchDeletingBackend`
vs. the generic fallback is already distinguished structurally).
`adapters/file.Backend` always reports `native=false` (lock-file
emulation); `adapters/s3` always reports `native=true` (genuine
server-side atomicity) — see each backend's own "Backend semantic
differences" row below. 3 implementers need the new method:
`stats.NoopObserver`/`stats.LoggingObserver`/the internal `fanout`
type (mirrors every prior new `FileObserver` method's own blast
radius).

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
| `TestConditionalWrite_Observer_ReportsNativeFalse_OnFilesystemBackend` | `RecordFileConditionalWrite`'s `native` parameter is `false` for `adapters/file.Backend`/`ports.osBackend`'s lock-file-emulated conditional write (added during `adapters/s3`'s own 2nd gap-review round) |
| `TestConditionalWrite_Observer_ReportsNativeTrue_OnS3Backend` | Same proof, `native=true`, against `adapters/s3`'s future `ConditionalWriteBackend` implementation (deferred until `adapters/s3` is actually implemented — tracked here so it isn't forgotten) |

#### Backend semantic differences (new row)

| Operation | Filesystem (Phase 1/2) | S3 (Phase 2, future) |
|---|---|---|
| Conditional write | `ConditionalWriteBackend` via a `<path>.lock` file — atomic among COOPERATING conditional writers only; no auto-recovery from a crashed holder (manual `.lock` removal required); `RecordFileConditionalWrite`'s `native` flag is always `false` | `ConditionalWriteBackend` via `PutObject`'s native, server-side `If-Match` — atomic against EVERY writer (not just cooperating ones), no lock file, no stale-lock concept at all; `RecordFileConditionalWrite`'s `native` flag is always `true` |

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
`WalkingBackend` implementation is exercised by this feature. At the
time of THIS item's own implementation, the remaining 4 Phase 2 items
were still at outline depth only — since then, Conditional writes and
Streaming/chunked I/O have ALSO shipped, and `adapters/s3` has reached
full design status (see each section's own, current status header
above for the up-to-date picture; only `adapters/smb` remains at
outline depth as of this doc's latest revision).

## Phase 3 — adapt examples to the new `api/file` approach

**Status: planned, not yet started.** Phase 1 and ALL of Phase 2 have
now shipped (see each phase's own status above) — Phase 3 remains
deliberately NOT started, not because of any blocker, but because it
simply hasn't been picked up yet. Every existing example currently
using `ports.File`/`ports.Dir`/`adapters/file` directly needs review
against the "Removing an old API" checklist (`plan-a-new-codex-feature`
skill) — `ports.File[T]`'s public call signatures stay UNCHANGED per
this doc's own Scope decisions (confirmed true after Stage F's full
migration — `ports/file_test.go`/`ports/dir_test.go` both passed
UNMODIFIED), so most examples should need ZERO changes; any example
relying on an internal detail (e.g. direct `os.*` error matching
instead of `errors.As` against `ports.File`'s own typed errors) would
need updating. A concrete example inventory (which files, which
patterns) is deferred to when Phase 3 actually starts, since grepping
for `ports.File`/`adapters/file` usage NOW would produce a list that's
likely stale by the time anyone acts on it.

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
the 2 closed this round. **ALL 5 Phase 2 items are now fully DESIGNED
AND IMPLEMENTED** (Recursive `WithList`, Conditional writes,
Streaming/chunked I/O, `adapters/s3`, and `adapters/smb` — Phase 2 is
FULLY COMPLETE) — see each item's own section above for its own
resolved decisions and unit test plan; nothing from the earlier "4
remaining items deliberately left at outline depth" framing is still
accurate, superseded by each item's own subsequent design AND
implementation round.

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
