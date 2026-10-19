# D-0011 — File API Backend Transparency

> Status: Implemented — architectural foundation.

## Summary

`api/file` is a new top-level `api/*` package applying the SAME
three-layer declare → attach → dispatch model `api/rest`/`api/events`/
`api/reqreply` already establish, to file-shaped verbs instead of
HTTP/pub-sub/req-reply ones. The crux of the design is `file.Backend` —
a transparency seam that lets the SAME declared `Resource[T]` work
unchanged whether the concrete storage is the local filesystem, S3, or
an SMB share, with the backend chosen entirely at attach time, never
baked into the declaration. Three real backends now prove this:
`adapters/file` (the original, filesystem-backed proof), `adapters/s3`
(AWS S3 and any S3-compatible store), and `adapters/smb` (SMB2/3
shares). `ports.File[T]`/`ports.Dir` — go-codex's older, filesystem-only
declarative file abstraction — are now thin wrappers delegating to this
same seam, with their own public call signatures completely unchanged.

This document graduates `docs/roadmap/file-api.md` (now deleted) per
the `plan-a-new-codex-feature` skill's promotion policy — the design
fundamentally changes how `ports.File`/`ports.Dir` work internally and
establishes the `Backend`-transparency pattern future storage adapters
are expected to follow.

## Motivation

`ports.File[T]` bundled two genuinely different concerns into one type:
the DECLARATION (a path template + codec + per-variable merge fields —
the same shape `rest.Route`/`events.Channel`/`reqreply.Route` already
establish) and the actual I/O (`Read`/`Write`/`Update`/`Patch`/`Delete`
called `os.ReadFile`/`os.WriteFile`/`os.Remove` directly, inline, inside
the same methods). There was no backend interface anywhere in the
type — a declared `ports.File[T]` value could ONLY ever be backed by
the local filesystem, and `adapters/file`'s own stream-bridge functions
never became an independent transport the way `adapters/nethttp`/
`adapters/mqtt5` are for `api/rest`/`api/events`.

`api/file` designs the missing piece: file-shaped verbs (Read, Create,
Patch, Delete, List, Exists, Copy, Move, Mkdir, BatchDelete, plus the
raw byte-stream pair StreamRead/StreamWrite — 11 roles total, instead
of GET/POST/PATCH/DELETE/GET-collection), full Router-group and
declarative-Middleware parity, Observer integration, a cross-backend
permission capability, and true backend transparency.

## Why the role-builder shape, not a single `rest.Route`-style type

`rest.Route[Req, Resp]` works because every REST route is a uniform
Req→Resp pair. File verbs are not uniform: Read has no request body;
Create has a body but no distinct response; Patch needs a SECOND codec
for the partial-patch shape (already established by
`ports.File.Patch`/`PatchEncoded[T,P]`, reused verbatim); Delete has
neither body nor response; List returns a collection, not one value.
`api/file` mirrors `events.Channel[T]`'s `WithSubscribe`/`WithPublish`
asymmetric role-builder pattern, not `rest.Route`'s uniform one.

## API surface

```go
// Resource[T] declares the file shape once: a path template (mirrors
// rest.Path/events.Topic) + codec + optional per-variable merge
// fields. Infallible — validation runs at role-attach time.
func NewResource[T any](pathTemplate string, codec codex.Codec[T], opts ...ResourceOpt) Resource[T]

type ResourceOpt interface{ applyResource(*resourceBuilder) }

type PathParam struct {
	Name, Description string
	Codec              *codex.Codec[string] // optional per-variable validation
}
func (p PathParam) WithCodec(c codex.Codec[string]) PathParam

// MergedPathParam[T] decodes an extracted path var directly into one
// of T's own fields — mirrors events.NewTopicParam/rest.NewPathParam.
func NewPathParam[T, V any](name string, codec codex.Codec[V], get func(T) V, set func(*T, V)) MergedPathParam[T]

// Resource[T] itself has no I/O methods — role-attach methods only.
type Resource[T any] struct{ /* unexported */ }

func (r Resource[T]) WithRead(op Read) Reader[T]
func (r Resource[T]) WithCreate(op Create) Creator[T]
func (r Resource[T]) WithDelete(op Delete) Deleter[T]
func (r Resource[T]) WithList(op List) Lister[T]
func (r Resource[T]) WithExists(op Exists) Exister[T]
func (r Resource[T]) WithCopy(op Copy) Copier[T]
func (r Resource[T]) WithMove(op Move) Mover[T]
func (r Resource[T]) WithMkdir(op Mkdir) MkdirCreator[T]
func (r Resource[T]) WithBatchDelete(op BatchDelete) BatchDeleter[T]
func (r Resource[T]) WithStreamRead(op StreamRead) StreamReader[T]
func (r Resource[T]) WithStreamWrite(op StreamWrite) StreamWriter[T]

// WithPatch needs its OWN type parameter P (the partial-patch shape) —
// Go forbids type parameters on methods beyond the receiver's own
// (pre-Go-1.27), so this is a free function, mirroring NewRoute's own
// "free function because of generics" precedent exactly.
func WithPatch[T, P any](r Resource[T], patchCodec codex.Codec[P], op Patch) Patcher[T, P]
```

Each role type (`Reader[T]`/`Creator[T]`/`Deleter[T]`/`Lister[T]`/
`Exister[T]`/`Copier[T]`/`Mover[T]`/`MkdirCreator[T]`/
`BatchDeleter[T]`/`StreamReader[T]`/`StreamWriter[T]`/`Patcher[T,P]`)
gets its OWN independent attachment surface — `.Use(mws
...RouteMiddleware)`, `.<Role>BoundMW(bm any)`, `.WithErrorPatterns(...)`,
`.WithCapabilities(...)`/`.RequireCapabilities(...)`, and
`.Handle(client *Client, opts ...HandleOpt) (*<Role>Handle[T], error)`
— mirroring `Subscriber[T]`/`Publisher[T]`'s independent Security/
middleware attachment exactly (a Security requirement on Delete does
not imply one on Read).

### `file.Client` — attach-time Backend wiring + call-time dispatch

Mirrors `events.Client`/`reqreply.Server`'s "dispatch IS on the same
value" shape (not `rest.Server`'s spec-accumulation/Attach split — file
resources have no OpenAPI/AsyncAPI-equivalent spec target):

```go
type Client struct{ /* unexported */ }
func NewClient(opts ...ClientOpt) *Client

// Attach is EXCLUSIVE — a 2nd call returns BackendAlreadyAttachedError;
// every dispatch method below returns NoBackendAttachedError before it.
func (c *Client) Attach(backend Backend) error

// Every dispatch method is `any`-typed, dual-accept: the bare role
// builder (common case, zero ceremony) OR an explicitly pre-built
// *XxxHandle[T] (Router-grouped/middleware-composed case — path
// resolution happens ONCE at .Handle() time, never re-derived per
// call). Mirrors reqreply.Client.Call's own confirmed dual-acceptance.
func (c *Client) Read(ctx context.Context, r any, vars map[string]string) (any, error)
func (c *Client) Create(ctx context.Context, cr any, val any, vars map[string]string, opts ...WriteOptions) (createdDirs []string, err error)
func (c *Client) Patch(ctx context.Context, p any, patch any, vars map[string]string, opts ...WriteOptions) error
func (c *Client) Delete(ctx context.Context, d any, vars map[string]string, opts ...DeleteOptions) (existed bool, err error)
func (c *Client) List(ctx context.Context, l any, vars map[string]string, opts ...ListOptions) (any, error) // any is []ListResult[T]
func (c *Client) Exists(ctx context.Context, e any, vars map[string]string) (Entry, bool, error)
func (c *Client) Copy(ctx context.Context, cp any, srcVars, dstVars map[string]string) error
func (c *Client) Move(ctx context.Context, mv any, srcVars, dstVars map[string]string) error
func (c *Client) Mkdir(ctx context.Context, m any, vars map[string]string, opts ...MkdirOptions) (created bool, err error)
func (c *Client) BatchDelete(ctx context.Context, bd any, varsList []map[string]string) ([]BatchDeleteResult, error)
func (c *Client) OpenRead(ctx context.Context, sr any, vars map[string]string, opts ...StreamReadOptions) (io.ReadCloser, error)
func (c *Client) OpenWrite(ctx context.Context, sw any, vars map[string]string, opts ...WriteOptions) (io.WriteCloser, error)
```

A caller does `respAny.(T)`/`respAny.([]ListResult[T])` — the SAME
plain, manual type-assertion idiom `reqreply.Client.Call` established
(Go generics can't add a type parameter to a method, so `Client`'s own
methods stay `any`-typed and the caller supplies the assertion; see the
"Future work" section for how Go 1.27's generic methods may revisit
this).

Copy/Move take TWO var sets (`srcVars`/`dstVars`) rather than a second
declared destination template — the SAME declared path template,
instantiated twice with different values; chosen because it requires
zero change to `Resource[T]`'s declaration shape and mirrors how every
other role already takes one `vars` map, just twice.

### `file.Backend` — the transparency seam

```go
type Backend interface {
	// ReadBytes returns path's raw content, or a Backend-specific
	// not-found signal (errors.Is against os.ErrNotExist for
	// filesystem-family backends) the dispatch core translates into
	// the SAME FileNotFoundError every backend reports identically.
	ReadBytes(ctx context.Context, path string) ([]byte, error)

	// WriteBytes writes path's content. opts carries the
	// backend-agnostic subset of FileOptions every backend can
	// plausibly honor — one that genuinely cannot (e.g. S3 has no
	// directory-permission concept) documents it as a no-op, never an
	// error.
	WriteBytes(ctx context.Context, path string, data []byte, opts WriteOptions) (createdDirs []string, err error)

	// DeleteBytes removes path. existed=false + err=nil is the
	// idempotent "already absent" success case.
	DeleteBytes(ctx context.Context, path string, opts DeleteOptions) (existed bool, err error)

	// ListDir enumerates path's immediate children (non-recursive).
	ListDir(ctx context.Context, path string) ([]Entry, error)

	// Stat returns path's metadata WITHOUT reading its content —
	// mirrors S3's HeadObject exactly.
	Stat(ctx context.Context, path string) (Entry, error)

	// Mkdir creates path as a directory, including missing parents
	// (mirrors os.MkdirAll). A backend with NO real directory concept
	// (S3) MUST still implement this as a no-op success — never an
	// error.
	Mkdir(ctx context.Context, path string, opts MkdirOptions) (created bool, err error)

	BackendName() string
}
```

Plus 10 OPTIONAL extension interfaces, each following the SAME
type-assertion-guard idiom (`if x, ok := backend.(XxxBackend); ok {
/* native fast path */ } else { /* generic fallback, always correct */ }`
— "no false portability, optionally faster when the Backend supports
it"): `CopyingBackend` (native server-side copy, e.g. S3's
`CopyObject`), `MovingBackend` (native atomic rename), `MetadataBackend`
(read/write a path's metadata map — a JSON sidecar file on
`adapters/file`/`adapters/smb`, S3's own object metadata on
`adapters/s3`), `ConditionalWriteBackend` + `NativeConditionalWriteBackend`
(an atomic compare-and-swap write gated by `WriteOptions.IfMatch` — see
"Conditional writes" below), `BatchDeletingBackend` (native N-key batch
delete, e.g. S3's `DeleteObjects`), `StreamWriteBackend`/
`StreamReadBackend`/`RangeReadingBackend` (streaming/chunked I/O — see
below), `WalkingBackend` (a recursive, callback-based directory walk,
mirrors `filepath.WalkDir`), `DirDeletingBackend` (recursive/
non-empty-check/dry-run directory delete).

### `file.Capability` — the cross-backend permission model

Mirrors `d-0006`'s established `mqtt5.Capability`/`Apply`/
`CheckCapabilityCoverage` mechanism exactly (its OWN copy, not a shared
generic type — `api/file` has no coupling to `api/events`):

```go
type Capability interface{ isFileCapability() }
type CapabilityName interface{ CapabilityName() string } // optional, mirrors mqtt5's identical split

// AccessMode is the ONE permission concept genuinely portable across
// filesystem/S3/SMB — researched directly: Windows collapses ALL of
// os.FileMode to one bit via Go's own os.Chmod; S3's ACL/bucket-policy
// model is grantee+action-based, not bit-based at all. There is no
// lossless cross-backend translation to invent, only this honest
// common denominator.
type AccessMode int
const (
	AccessModeReadWrite AccessMode = iota
	AccessModeReadOnly
)

// NativePOSIXMode is an escape hatch — filesystem/SMB-only, explicitly
// ignored on S3, collapsed to Go's own os.Chmod binary semantics on
// Windows.
type NativePOSIXMode os.FileMode
```

**Capability always wins over a conflicting per-call option** — a
declared, attach-time contract is never silently overridable by a
less-visible per-call `WriteOptions.Perm`/`MkdirOptions.NativePOSIXMode`
value; `checkAccessMode` runs in EVERY mutating role's dispatch core
(`createCore`/`patchCore`/`deleteCore`/`moveCore`/`mkdirCore`/
`copyCore`/`batchDeleteCore`) BEFORE any Backend call — a declared
`AccessModeReadOnly` rejects a write before it ever reaches disk/S3/SMB.
`AccessMode` trusts the declared Capability unconditionally (no IAM
policy simulation, no OS-permission-bit verification) — the SAME
precedent every other Backend already follows; authorization is a
middleware/proxy-layer concern, kept out of the Backend implementation
entirely.

## Structured errors

All implement `slog.LogValuer`. The original `ports/file.go` error
types (`FileNotFoundError`, `FileReadError`, `FileWriteError`,
`FileDecodeError`, `FileEncodeError`, `FileAlreadyExistsError`,
`FileDeleteError`, `FilePathParamError`, `FilePathMismatchError`,
`MissingFilePathVarError`, `FileWildcardBuildError`,
`FilePatchNotSupportedError`) relocated to `api/file` as the new
canonical home, with `type XError = file.XError` aliases left in
`ports` for backward `errors.As` compatibility. New types added across
this design: `BackendUnsupportedOptionError`, `BackendAlreadyAttachedError`/
`NoBackendAttachedError` (mirror `events.TransportAlreadyAttachedError`/
`NoTransportAttachedError`), `BackendTypeMismatchError` (mirrors
`events.TransportTypeMismatchError`), `BoundMiddlewareReqMismatchError`,
`SecurityRejectedError`, `ConditionalWriteMismatchError`/
`ConditionalWriteUnsupportedError`/`ConditionalWriteLockTimeoutError`,
and `file.Dir`'s own parallel vocabulary (`DirPathParamError`/
`MissingDirPathVarError`/`DirPathMismatchError`/`DirReadError`/
`DirEntryParamError`/`DirAlreadyExistsError`/`DirNotFoundError`/
`DirWildcardBuildError`/`DirDeleteError`/`DirNotEmptyError`, ALSO
aliased by `ports.Dir`'s own identically-named historical types).

## Observer integration

`stats.FileObserver` grew from its original 2 methods
(`RecordFileRead`/`RecordFileWrite`) to 11, across this design's own
successive rounds — an intentional, acknowledged breaking addition
each time (mirrors `RecordFileDelete`'s own original precedent: "one
consumer repo-wide, churn is cheaper than fragmenting into a new
observer extension per method"):

```go
type FileObserver interface {
	RecordFileRead(path string, success bool, duration time.Duration)
	RecordFileWrite(path string, success bool, duration time.Duration)
	RecordFileDelete(path string, success bool, duration time.Duration)
	RecordFileList(path string, success bool, count int, duration time.Duration)
	RecordFileExists(path string, success bool, duration time.Duration)
	RecordFileCopy(srcPath, dstPath string, success bool, duration time.Duration)
	RecordFileMove(srcPath, dstPath string, success bool, duration time.Duration)
	RecordFileMkdir(path string, success bool, duration time.Duration)
	RecordFileBatchDelete(paths []string, successCount, failureCount int, duration time.Duration)
	// RecordFileRangeRead applies D-0010's "transport-guaranteed vs.
	// adapter-approximated" capability axis to OpenRead's own 3-level
	// range-read fallback cascade — native=true only for a Backend
	// implementing RangeReadingBackend (S3/SMB; false for the 2
	// approximated filesystem-only fallback tiers).
	RecordFileRangeRead(path string, native bool, offset, length int64, duration time.Duration)
	// RecordFileConditionalWrite applies the SAME axis to
	// ConditionalWriteBackend — native=true for S3's real server-side
	// If-Match, false for the lock-file-emulated filesystem/SMB path.
	RecordFileConditionalWrite(path string, native bool, ifMatch string, duration time.Duration)
}
```

`Capability.Apply` reporting reuses `stats.CapabilityObserver
.RecordCapabilityApplied` unchanged — the same interface
`mqtt5.QoS`/`Retained`/`MessageExpiry` already report through.
`TraceObserver`/`SecurityObserver` integration mirrors `rest`/`events`/
`reqreply`'s identical type-assertion-guard pattern.

## Router/Middleware integration

- **Router groups**: `api/file` is the 4th package implementing
  `d-0008`'s "one shape, N packages" Router pattern
  (`file.Router{inner router.Router[*Client]}`, `NewRouter`/`.Use`/
  `.Group`/`.Route`/`.Register`). Every role implements
  `internal/router.Routable[*Client]` (`WithRouterPrefix`/
  `MiddlewareNames`/`RouteTags`/`RegisterAny`) + `router.MethodReporter`
  (`RouteMethod`). Path composition has exactly ONE construction point
  — a Router-grouped role is registered via
  `reader.Handle(client, file.WithRouter(router))` ONCE, producing a
  `*ReadHandle[T]` that already carries the resolved, prefixed path;
  every subsequent `Client.Read(ctx, readHandle, vars)` call reuses
  THAT Handle, never re-deriving the path — satisfying the
  `plan-a-new-codex-feature` skill's own mandatory Router-composition
  check (the `rest.Redirect` precedent: never let a construction site
  silently skip Router-group prefixing).
- **Declarative Middleware**: `file.Middleware[In,Out]` (embeds
  `middleware.Declaration[In,Out]`) + 11 `Bound<Role>Middleware[T,In,Out]`
  types — `Read`/`Create`/`Patch`/`List` keep a `*T` parameter (a
  natural domain value exists); `Delete`/`Exists`/`Copy`/`Move`/
  `Mkdir`/`BatchDelete`/`StreamRead`/`StreamWrite` drop it (no single
  domain value exists for these 7). `List`'s own `BoundMiddleware`
  invocation passes the ACTUAL DECODED per-entry value (see "`List`/
  `BatchDelete` per-entry Security" below), not `nil`. A file-metadata
  merge-field axis (`MetadataParam`/`MergedMetadataParam[T]`, mirrors
  `rest.HeaderParam`) lets `Create`/`Patch`'s `BoundMiddleware` read/
  write a resource's metadata map directly via
  `Middleware.WithMetadataIn`/`WithMetadataOut`; `Read`/`List` stay
  permanently zero-valued on this axis (their own base value isn't
  available before the Backend call completes).
- **Security schemes**: reuses `route.SecurityScheme`/
  `route.SecurityRequirement` unchanged — the same `CheckScopes`/
  `CheckCoverage` dispatch every other pattern's role-level Security
  already uses.
- **`ErrorPattern`**: reuses the established `errors.As` +
  first-rule-wins + direct/mapped convention, auto-invoked by every
  dispatch-core function on a Backend-level error (closing a Phase-1
  gap found during this design's own first `deep-dive-bug-hunt` pass —
  see "Hardening rounds" below).

## `List`/`BatchDelete` per-entry Security (post-ship amendment)

A retroactive, deliberate behavior change discovered necessary after
Phase 1 initially shipped `List`'s Security gate as once-per-call: a
`BoundListMiddleware`'s `Fn` could never reason about an entry's own
decoded content, only "is List allowed to run at all." Resolved by
introducing `ListResult[T]{Value T, Err error}` — `Client.List`'s boxed
`any` became `[]ListResult[T]` instead of `[]T` (a breaking change,
intentional), with Security now gated once PER DISCOVERED ENTRY,
passing that entry's actual decoded value. Every per-entry failure
(decode error, merge-field mismatch, metadata-read failure, Security
rejection) becomes that entry's own `ListResult[T].Err` — `List` never
aborts mid-walk for a per-entry problem anymore, always returning every
discovered entry, successes and failures together; a WALK-LEVEL error
(the parent directory itself can't be read at all) still fails the
whole call. `BatchDelete` got the parallel per-PATH Security change —
no new type needed, `BatchDeleteResult{Path, Existed, Err}` already had
a per-path `Err` field. `ports.Dir` is unaffected (its own, separate,
untyped `List` never calls `api/file.Client.List`).

## Conditional writes (ETag / `WriteOptions.IfMatch`)

`Entry` gained `ETag string` and `WriteOptions` gained `IfMatch *string`
— a non-nil `IfMatch` makes a write conditional: it commits only if
`path`'s CURRENT strong ETag equals `*IfMatch`. ETag computation: a
fallback ETag is computed BY `api/file` itself for every Backend — a
strong, content-hash ETag (SHA-256) when content is already in hand, a
weak, ModTime+Size-derived approximation when only metadata is
available (`Stat`/`ListDir`). `adapters/s3` is the one exception: S3
computes and stores a REAL, strong, server-side ETag at write time
(returned by `HeadObject`/`GetObject`/`ListObjectsV2` without a body
read) — a genuine capability upgrade over the filesystem/SMB
approximation, not just parity.

`ConditionalWriteBackend.WriteBytesIfMatch` is the optional extension a
Backend implements for an atomic compare-and-swap write.
`adapters/file`/`adapters/smb` emulate atomicity via a lock-file
(`<path>.lock`, `O_CREATE|O_EXCL`, bounded retry+backoff with an
internal ~5s ceiling independent of the caller's own ctx deadline) +
temp-file-and-rename commit — genuinely atomic with respect to
COOPERATING conditional-write callers only (disclosed scope, not a
universal filesystem-level lock). `adapters/s3` needs no lock at all —
S3's own `PutObject` `If-Match`/`If-None-Match` is natively atomic
server-side (`NativeConditionalWriteBackend.NativeConditionalWrite()`
reports `true` for S3, `false` for the lock-emulated backends — see
Observer integration above). `Strict:true` always wins over `IfMatch`
when both are set on the same call; a `Create` with `IfMatch` against
a nonexistent target returns `ConditionalWriteMismatchError` (mirrors
S3's own real `If-Match`-on-nonexistent-object behavior).

**Hardening finding, applied retroactively**: both lock-file
implementations' own commit temp files (`<path>.tmp-*`) were leaking
into `List`/`WalkDir` results (a spurious per-entry error, not a silent
wrong match) — fixed by extending the SAME control-file filter that
already excludes `.meta.json`/`.lock` sidecar files to also exclude
`.tmp-*`, in both `adapters/file` and `ports.osBackend`.

**Deliberately deferred** (disclosed, not silently dropped): whether
`Client.Read`/`List` should ALSO expose the ETag they could compute
in-flight on the decoded `T` itself — the feature works end-to-end via
`Client.Exists`/`ports.File.Stat` without it; this stays open for a
future round.

## Streaming/chunked reads and writes

Two independent mechanisms, motivated by S3's own `GetObject`/
`PutObject` body-streaming and Range-header precedents:

- **Mechanism 1 (automatic, no new role)**: reuses the EXISTING
  `format.IsStreamable()`/`MarshalTo` mechanism unchanged — when a
  `Resource[T]`'s format is streamable AND the attached Backend
  implements the new optional `StreamWriteBackend.OpenWriter`,
  `createCore` streams `MarshalTo(v, writer)` instead of
  `Marshal`+`WriteBytes`; falls back to the whole-file path otherwise.
  `OpenWriter`'s `Close` MUST commit atomically (temp-file-in-the-same-
  directory + rename) — a reader must never observe a partially-written
  file; `adapters/s3` achieves this via an `io.Pipe` bridge into a
  single `PutObject` call (S3 has no partial-object visibility — atomic
  by construction), `adapters/smb` via a temp file + `Share.Rename`
  (NOT an `io.Pipe` bridge — genuinely streaming to the temp handle,
  found during a dedicated pre-implementation review round that the
  design's first draft would have violated this exact atomicity
  contract by writing directly to the final path).
- **Mechanism 2 (new, codec-bypassing roles)**: `StreamReader[T]`/
  `StreamWriter[T]` + `Client.OpenRead`/`OpenWrite` — `T` still declares
  path templating/Security/Capability at declare time, but the actual
  bytes bypass the codec entirely (mirrors S3's own `GetObject`/
  `PutObject` body-streaming). This is structurally necessary, not a
  design choice: `codex.Codec[T].Decode`/`Validate` require a fully
  materialized `T` to run `Refine` constraints against — there is no
  partial-decode concept anywhere in `codex`/`format`, confirmed via
  `format.NewStreamed` itself still being `[]byte`-based on its own
  decode side. `OpenRead` supports read-only byte-range access
  (`StreamReadOptions{Offset, Length int64}`) via a 3-level Backend
  fallback cascade: a native `RangeReadingBackend.OpenReaderRange`
  (S3/SMB — the primary real-world motivating case) → a generic
  discard-then-limit wrapper over `StreamReadBackend.OpenReader` → a
  `bytes.Reader` slice over `Backend.ReadBytes` (works on every
  existing Backend unchanged). Both new roles get the SAME full Router/
  BoundMiddleware integration every other role has
  (`BoundStreamReadMiddleware`/`BoundStreamWriteMiddleware`, no-T
  shape, mirrors `BoundDeleteMiddleware`) — a gap found and closed
  during this item's own 2nd gap-review pass.

## Recursive `WithList`

`ListOptions{Recursive bool}` — a 0-or-1 trailing variadic on
`Client.List`, mirroring the established Create/Delete/Mkdir
convention (omitted = today's single-level behavior, byte-for-byte
unchanged). When `true`, composes via the `WalkingBackend.WalkDir`
primitive the `ports.Dir` migration (below) already built, or a manual
repeated-`ListDir` fallback for a Backend not implementing it. A
depth-pruning rule (reusing `ports.Dir.listGlobDiscovery`'s own
existing precedent) excludes any entry whose actual depth exceeds the
resource template's own exact declared segment count — found necessary
during this item's own pre-implementation gap-review (the original
design was permissive, matching only a leaf filename regardless of
structural depth). Security stays gated once-per-call against the
parent directory (mirrors `BatchDelete`'s own "once per batch, not once
per item" precedent) — reconfirmed, not re-opened, by the `List`/
`BatchDelete` per-entry-Security amendment above, which predates this
item.

> **Known limitation, confirmed via live reproduction (a 2nd
> `deep-dive-bug-hunt` pass, not yet fixed):** `Recursive: true` is
> currently a **complete no-op** for every possible `Resource[T]`
> declaration — it never discovers anything `Recursive: false` doesn't
> already discover. Two independently-compounding causes: (1) the
> depth-pruning rule above always equals non-recursive's own natural
> scope, because `buildParentDirWithRouting` ALREADY requires every
> dir-portion path variable to be supplied up front (a separate,
> pre-existing constraint), making the resolved parent directory always
> a single, fully-concrete path with exactly one remaining template
> segment past it, for any `Resource[T]` that could ever be declared;
> (2) the one scenario where recursive discovery would be meaningful —
> walking a directory whose own sub-segment NAMES aren't known up front
> (e.g. `"notes/{category}/{id}.json"`, discovering every `(category,
> id)` pair without the caller enumerating `category` values) — cannot
> even be invoked, since `buildParentDirWithRouting` has no
> Recursive-aware relaxation of its own "every dir-portion var
> required" rule and fails immediately with `MissingFilePathVarError`
> before `Recursive` ever gets a chance to matter. A genuine fix needs
> a real design decision (likely porting `file.Dir`'s own
> `matchDirGlob`-style glob-discovery machinery into `Resource[T]`'s
> own dispatch, relaxing the "every dir-portion var required" rule
> specifically under `Recursive: true`) — out of scope for a bug-hunt
> pass; tracked as a dedicated future design round, not silently
> deferred.

## `adapters/s3`

Wraps `github.com/aws/aws-sdk-go-v2/service/s3` behind a narrow client
interface (`s3API`: `GetObject`/`PutObject`/`DeleteObject`/
`DeleteObjects`/`HeadObject`/`ListObjectsV2`/`CopyObject` —
`var _ s3API = (*s3.Client)(nil)` compile-time proof); the caller
constructs/authenticates the real `*s3.Client` themselves
(`s3.NewFromConfig`) — this package never owns credential loading,
mirroring `adapters/sql` never owning `*sql.DB` construction. Works
against any S3-compatible endpoint (MinIO, Ceph, R2, GCS interop) via
`s3.Options.BaseEndpoint`.

Implements `Backend` core + `CopyingBackend` (`CopyObject`) +
`MetadataBackend` (`HeadObject`'s user metadata to read; a `CopyObject`
self-copy to write — NOT free, a real billed API call, disclosed) +
`ConditionalWriteBackend`+`NativeConditionalWriteBackend` (`true` — S3's
own native server-side `If-Match`/`If-None-Match`) +
`BatchDeletingBackend` (`DeleteObjects`, chunked at S3's 1000-key
limit — the primary real-world motivating case for this interface) +
`StreamWriteBackend`/`StreamReadBackend`/`RangeReadingBackend`
(`GetObject`'s native `Range` header is `RangeReadingBackend`'s
primary real-world motivating case) + `WalkingBackend`/
`DirDeletingBackend` (prefix-based directory synthesis — S3 has no real
directories; `Mkdir` is a no-op success). Deliberately does NOT
implement `MovingBackend` — S3 has no atomic rename for standard
buckets (the generic Copy+Delete fallback IS the correct behavior, not
a gap; a caveat is disclosed for S3 Express One Zone directory
buckets' newer `RenameObject` operation, out of scope). `AccessMode`
trusts the declared Capability unconditionally, same as every other
backend (no IAM policy simulation — confirmed via the user's own
broader philosophy that authorization is a middleware/proxy-layer
concern). `S3Error{Operation, Bucket, Key, Err}` wraps genuinely
S3-specific failures only (access denied, throttling, bucket-not-found)
— every failure `api/file` already has a typed error for is translated
directly, matching `adapters/file`'s own precedent instead of
double-wrapping. `WriteOptions.BackendOptions any` — a generalized,
opaque per-call escape hatch `api/file` gained specifically for this
adapter (`file.Capability` is sealed to `api/file` itself, so
`adapters/s3` has no way to add its own sealed Capability the way
`mqtt5`/`zeromq` do for `events`) — carries `adapters/s3.PutOptions
{StorageClass, ServerSideEncryption, ACL, Tags, ContentType}`,
type-asserted out; every other Backend simply never reads it. The
mandatory runnable example uses an in-process `github.com/
johannesboyne/gofakes3` fake S3 server (example-only dependency, never
imported by `adapters/s3` itself), proving the "same declarations,
different backend" promise fully offline.

## `adapters/smb`

Wraps `github.com/hirochachacha/go-smb2/v2` behind a narrow client
interface (`shareOps`/`fileOps`) — `*smb2.Share`'s real `Open`/`Create`/
`OpenFile` return `*smb2.File`, not `fileOps`, directly, so
`NewBackend(share *smb2.Share, opts ...BackendOpt) *Backend` wraps it
internally via an unexported `shareAdapter`, giving a real caller ZERO
glue code to write (mirroring `adapters/s3`'s own convenience even
though the underlying mechanism differs). **Disclosed, accepted
dependency risk**: `go-smb2/v2` has NO tagged `v2.x.y` release at all
(only legacy `v1.0.x`/`v1.1.0`) — pinned to an exact commit SHA, a
genuinely still-in-flux, untagged dependency, re-verified fresh at
implementation time rather than assumed stable.

Implements `Backend` core (`WriteBytes`'s `CreateDirs` honored via
`Share.MkdirAll` — native recursive creation, an implementation-time
correction over the design's originally-planned no-op) +
`CopyingBackend` (`io.Copy` auto-engaging the library's own
server-side copy fast path) + `MovingBackend` (`Share.Rename` — the
FIRST of the 3 Phase 1/2 backends to implement this interface; neither
`adapters/file` nor `adapters/s3` do, each for its own disclosed
reason) + `ConditionalWriteBackend`+`NativeConditionalWriteBackend`
(`false` — the SAME lock-file mechanism `adapters/file` uses, SMB2 has
no CAS primitive, PLUS the strong/weak ETag-comparison step the lock
alone doesn't provide — a gap found during review before
implementation) + `MetadataBackend` (JSON sidecar, same as
`adapters/file`, NOT NTFS Alternate Data Streams) +
`BatchDeletingBackend` deliberately NOT implemented (no native SMB2
batch-delete) + `StreamWriteBackend` (writes to a temp file via
`Share.Create`, commits via `Share.Rename` on Close — genuinely
streaming, no `io.Pipe` bridge needed, unlike `adapters/s3`; the
atomicity-via-rename requirement was itself a gap found and fixed
during a 4th pre-implementation review round) +
`StreamReadBackend`/`RangeReadingBackend` (`io.NewSectionReader` over
the bound file's own `io.ReaderAt` — NATIVE, `native=true`, arguably
more natural here than for S3) + `WalkingBackend` (`fs.WalkDir` over
the Share's own `io/fs.FS` bridge) + `DirDeletingBackend`. `Entry.ETag`
is a WEAK (ModTime+Size-derived) approximation, same as
`adapters/file` — an implementation-time correction from the original
design's "left empty" decision, which would have broken the standard
`Exists`→`IfMatch` conditional-write workflow entirely for every
SMB-backed resource. `SMBError{Operation, Share, Path, Err}` mirrors
`S3Error`'s narrowed scope exactly (the library translates not-found/
already-exists/permission-denied to `os.ErrNotExist`/`os.ErrExist`/
`os.ErrPermission` directly). The mandatory runnable example (an
in-process `github.com/sonroyaalmerol/go-smb-server`, example-only
dependency) hits a confirmed signing-interop gap between `go-smb2/v2`
and that young, niche server library — the demo dials/mounts
successfully, then gracefully skips with a clear, disclosed message on
the first real file operation, rather than hiding or faking success;
`adapters/smb`'s own correctness is proven independently and
exhaustively (33 unit tests against a hand-written fake, the full
9-scenario `api/file/filetest` conformance suite).

## `file.Dir` — directory-listing + deletion capability

`ports.Dir`'s generic, `EntryPattern`-based directory-listing
capability had NO `api/file` equivalent — discovered as a real,
structural gap while migrating `examples/go-edge-models` (Phase 3's own
`go-edge-models` use of `ports.Dir` for directory discovery). `file.Dir`
closes it: a pure declaration (a directory path template + its own
path-param codecs + an optional `EntryPattern`/`Recursive` — mirrors
`Resource[T]`'s own role exactly, declare-once, no I/O, no baked-in
backend). Unlike `Resource[T]`, `Dir` derives TWO independent role
values from the same declaration (`WithList`/`WithDelete` → `ListDirer`/
`DeleteDirer` — found necessary during this item's own gap-review: `Dir`
cannot be one `Routable` with 2 verbs, since Router/Capability/
Middleware/ErrorPattern are all attached PER-ROLE everywhere else in
this package too). Each role gets the SAME full attachment surface
every other role has — `Use`/`<Role>BoundMW`/`WithErrorPatterns`/
`WithCapabilities`/`RequireCapabilities`/`Handle` — mirroring
`Deleter[T]`'s own no-T shape, twice. `DirOptions` mirrors
`ports.DirOptions` minus `Observer`/`Context` (Observer resolves from
`ctx` uniformly, per the established precedent). A `WithBaseDir` option
was sketched then REMOVED before shipping — found during a dedicated
Router-integration review that `file.Router`'s own prefix composition
already serves that anchoring purpose, with no second,
independently-configured mechanism needed; its removal also fixed a
real bug where glob-discovery mode silently ignored the Router's
prefix under the `WithRouter` HandleOpt workflow (only the
`Route`+`Register` workflow had been exercised by tests). `ports.Dir`
itself (Stage C2) was migrated to delegate to `file.Dir`'s `Backend`-
level primitives directly (`WalkingBackend.WalkDir`/
`DirDeletingBackend.DeleteDir`, both NEW `Backend` extensions this
stage added) — bypassing `api/file.Client`/dispatch.go entirely, since
`Dir` is untyped (no codec, no Capability/Middleware/ErrorPattern
concern of its own at the `ports` level) — with public call signatures
completely unchanged, proven via the full, pre-existing
`ports/dir_test.go` suite passing UNMODIFIED. A genuine TOCTOU
atomicity regression (Strict-write's `os.Stat`-then-`os.WriteFile`
sequence, introduced during an earlier migration stage) was found and
fixed in the same round, restoring the original atomic
`O_CREATE|O_EXCL` guarantee in both `adapters/file.Backend` and
`ports`'s private `osBackend`.

## `ports.File`/`ports.Dir` migration (Stage F)

Both became thin wrappers delegating to `api/file.Client`/`Resource[T]`/
`Dir` internally, with public call signatures UNCHANGED — proven via
the full, pre-existing `ports/file_test.go`/`ports/dir_test.go` suites
passing without a single assertion change (the hard regression gate
this stage, and every `ports` migration stage before it, used
throughout). One confirmed, intentional behavior deviation:
`FileDeleteError`'s `existed` value on a generic (non-not-found)
`os.Remove` failure changed from the original code's hardcoded `true`
to `false` (more semantically honest) — a deliberate, user-approved
choice, not an oversight. `ports`'s own private `osBackend` (built to
satisfy `ports.File`/`ports.Dir` against `api/file.Backend` without an
import cycle with `adapters/file`) mirrors `adapters/file.Backend`'s
own implementation closely — confirmed byte-for-byte parity via a
dedicated `deep-dive-bug-hunt` round, zero drift found.

## Example migration (Phase 3)

`examples/dir-io`/`examples/file-io` (Stage D1) were fully rewritten
onto `api/file.Client`/`Resource[T]`/`Dir`/`Router` directly,
demonstrating `api/file`'s own declarative surface rather than
`ports`'s thinner wrapper — every original teaching scenario preserved.
`examples/go-edge-models` (Stage D2) was migrated onto a
receiver-style `usecase.Store`/`usecase.Scope` split: `Store` is the
long-lived connection (one `*file.Client`, built once per process,
mirrors `app/registry/internal.RegistryClient`'s own construction
shape); `Scope` is a `Store` bound to ONE `BasePath` via `Store.For`,
eagerly caching every non-generic `file.Handle` it dispatches through
(since none of these resources declare Capability requirements, a
Handle build can't actually fail in practice) — a later refinement
round that removed `BasePath` from every method's own parameter list
entirely (previously threaded through every call, since it's a genuine
per-MCP-request wire field, not a fixed, process-lifetime value).

## Hardening rounds (selected highlights)

A first-ever `deep-dive-bug-hunt` pass across `api/file`+`adapters/file`+
`ports.osBackend` found and fixed 3 real bugs: `stats.ReportErrors` was
never called anywhere in `api/file`'s own dispatch core (a
repo-wide-mandatory Observer requirement `api/file` had simply missed);
`patchCore` never enforced a declared `AccessModeReadOnly` Capability —
the one mutating role (of 7) missing the gate entirely; `Copy`/`Move`
(both the native fast paths AND the generic fallback) silently dropped
a resource's declared metadata entirely. Each was reproduced live via a
failing test before being fixed, per this skill's own core discipline.
Separately, `api/file/filetest` — the 5th API-defined conformance-test
package alongside `resttest`/`eventstest`/`reqreplytest`/`portstest`
(see `d-0010`) — was mutation-tested (a deliberately reversed
`BoundMiddleware` ordering confirmed `AssertOrderedMiddlewareChain`
correctly fails) rather than merely trusted to pass.

A SECOND `deep-dive-bug-hunt` pass, covering everything built after
the first pass above (`file.Dir`, the `List`/`BatchDelete` per-entry
Security amendment, Recursive `WithList`, Conditional writes,
Streaming I/O, `adapters/s3`, `adapters/smb`), found and fixed 2 more
real issues: `createCore`/`patchCore` silently discarded a caller's own
directly-supplied `WriteOptions.Metadata` whenever the `Resource`
declared no metadata merge fields — fixed via a 3-way layered merge
(caller's value as the base, overridable by the resource's own declared
fields, overridable again by `BoundMiddleware`'s `WithMetadataOut`
contribution); and `adapters/file.Backend` had zero compile-time
interface assertions for 3 extension interfaces it genuinely
implements (`ConditionalWriteBackend`/`StreamWriteBackend`/
`StreamReadBackend`), unlike every other backend. This pass also
mutation-tested `filetest`'s 2 newest scenarios
(`AssertStreamRoundTrip`/`AssertMetadataIntegration`, both confirmed
rigorous) and surfaced the Recursive `WithList` no-op finding disclosed
above.

## Cross-references

- `d-0006` — the `Capability`/`Apply`/`CheckCapabilityCoverage`
  mechanism `file.Capability` mirrors.
- `d-0008` — the Router-group pattern `file.Router` is the 4th
  implementer of.
- `d-0009` — the `internal/router`/`internal/middleware` mechanics
  `api/file` reuses via its own thin public wrapper, per that doc's
  own "shared cross-pattern mechanics belong in `internal/`" rule.
- `d-0010` — `api/file/dispatch.go` is already the single shared
  dispatch core serving all 3 backends (no `restdispatch`-style
  extraction was needed, since there was only ever one dispatch core to
  begin with); `api/file/filetest` is this design's own 5th
  conformance-test package in that doc's inventory; `file.Capability`'s
  `AccessMode`/optional-Backend-extension split was cross-checked
  against that doc's "capabilities are requirements, not transport
  options" framing and confirmed consistent (the TWO mechanisms are
  deliberately different: `Capability` is a declared, coverage-checked
  requirement; the optional extension interfaces are an
  always-correct, sometimes-faster graceful-degradation idiom — not to
  be conflated).

## Future work (not designed or scheduled here)

- Whether `Client.Read`/`List` should expose the ETag computed
  in-flight on the decoded `T` (deliberately deferred during
  Conditional writes' own design).
- Go 1.27 shipped generic methods after this design's own
  implementation rounds — `Client`'s `any`-typed dispatch methods
  (along with `events.Client.Publish`/`Subscribe` and
  `reqreply.Client.Call`) were all `any`-typed specifically because
  methods could not previously introduce their own type parameters;
  revisiting this is tracked as its own, separate roadmap item
  (`docs/roadmap/go127-generic-methods.md`), not part of this design.
