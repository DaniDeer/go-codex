package file

import (
	"context"
	"io"
	"sync"
)

// ClientOpt configures a [Client] built via [NewClient].
type ClientOpt interface{ applyClient(*Client) }

// Client mirrors
// [github.com/DaniDeer/go-codex/api/events.Client]/
// [github.com/DaniDeer/go-codex/api/reqreply.Client]'s own role exactly:
// the ONE entity both Attach and every call-time dispatch method hang
// off of.
type Client struct {
	mu      sync.RWMutex
	backend Backend
}

// NewClient creates a Client with no Backend attached — call [Client.Attach]
// before any dispatch method.
func NewClient(opts ...ClientOpt) *Client {
	c := &Client{}
	for _, o := range opts {
		o.applyClient(c)
	}
	return c
}

// Attach binds backend to c — EXCLUSIVE, mirrors
// events.Client.Attach/reqreply.Client.Attach's identical "one
// Transport/Backend per Client" rule: a 2nd Attach call returns
// [BackendAlreadyAttachedError].
func (c *Client) Attach(backend Backend) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.backend != nil {
		return BackendAlreadyAttachedError{}
	}
	c.backend = backend
	return nil
}

func (c *Client) attachedBackend() (Backend, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.backend == nil {
		return nil, NoBackendAttachedError{}
	}
	return c.backend, nil
}

// ── Variadic per-call options helpers ───────────────────────────────────
//
// Create/Patch/Delete/Mkdir accept an OPTIONAL per-call options value
// (variadic, 0 or 1) rather than a required parameter, so the original
// zero-opts call shape keeps working unchanged. Only the first value is
// used; this mirrors the Go stdlib's own "variadic as optional single
// value" idiom (e.g. [context.WithTimeout]-adjacent patterns) rather
// than inventing a new convention.

func firstWriteOptions(opts []WriteOptions) WriteOptions {
	if len(opts) == 0 {
		return WriteOptions{}
	}
	return opts[0]
}

func firstDeleteOptions(opts []DeleteOptions) DeleteOptions {
	if len(opts) == 0 {
		return DeleteOptions{}
	}
	return opts[0]
}

func firstMkdirOptions(opts []MkdirOptions) MkdirOptions {
	if len(opts) == 0 {
		return MkdirOptions{}
	}
	return opts[0]
}

func firstListOptions(opts []ListOptions) ListOptions {
	if len(opts) == 0 {
		return ListOptions{}
	}
	return opts[0]
}

func firstStreamReadOptions(opts []StreamReadOptions) StreamReadOptions {
	if len(opts) == 0 {
		return StreamReadOptions{}
	}
	return opts[0]
}

func firstDirOptions(opts []DirOptions) DirOptions {
	if len(opts) == 0 {
		return DirOptions{}
	}
	return opts[0]
}

// ── Dual-accept dispatch methods ──────────────────────────────────────
//
// Every method below is DUAL-ACCEPT: its role parameter accepts EITHER
// the bare role builder (the common case — the method builds the Handle
// internally with zero extra options) OR an explicitly pre-built
// *XxxHandle[T] (the Router-grouped/extra-options case, built via
// role.Handle(client, file.WithRouter(...))). This mirrors
// reqreply.Client.Call's own confirmed dual-acceptance exactly — see
// docs/roadmap/file-api.md's "file.Client" section for the full research
// and rationale.

// Read performs r's declared Read role against c's attached Backend.
// Returns the decoded T boxed as any — caller asserts it, exactly like
// reqreply.Call's respAny.(ComputeResp) idiom.
func (c *Client) Read(ctx context.Context, r any, vars map[string]string) (any, error) {
	backend, err := c.attachedBackend()
	if err != nil {
		return nil, err
	}
	return dispatchRead(ctx, backend, r, vars)
}

// Create encodes val via the resource's declared codec and writes it.
//
// opts is variadic (0 or 1 value accepted) to stay backward compatible
// with the original zero-opts signature — pass a [WriteOptions] to
// control CreateDirs/DirPerm/Strict/DryRun/Perm for this call,
// exactly like [ports.FileOptions] does for [ports.File.Write]. An
// attach-time [NativePOSIXMode] Capability, when declared, always wins
// over opts.Perm (see docs/roadmap/file-api.md's "Capability vs.
// per-call option precedence").
//
// Returns createdDirs — the directories CreateDirs created (or, under
// DryRun, WOULD create) — mirroring [ports.File.Write]'s identical
// return shape.
func (c *Client) Create(ctx context.Context, cr any, val any, vars map[string]string, opts ...WriteOptions) (createdDirs []string, err error) {
	backend, err := c.attachedBackend()
	if err != nil {
		return nil, err
	}
	return dispatchCreate(ctx, backend, cr, val, vars, firstWriteOptions(opts))
}

// Patch round-trips a partial patch value onto the existing resource.
//
// opts is variadic (0 or 1 value accepted); its Perm/Strict/DryRun
// fields are forwarded to the write-back step exactly like
// [ports.File.Patch] delegating to [ports.File.Write] with the same
// [ports.FileOptions] — CreateDirs is accepted but has no effect (Patch
// requires the file to already exist).
func (c *Client) Patch(ctx context.Context, p any, patch any, vars map[string]string, opts ...WriteOptions) error {
	backend, err := c.attachedBackend()
	if err != nil {
		return err
	}
	return dispatchPatch(ctx, backend, p, patch, vars, firstWriteOptions(opts))
}

// Delete returns existed=false (nil error) for the idempotent "already
// absent" case.
//
// opts is variadic (0 or 1 value accepted) — pass a [DeleteOptions] to
// set Strict/DryRun for this call, exactly like [ports.FileOptions]
// does for [ports.File.Delete].
func (c *Client) Delete(ctx context.Context, d any, vars map[string]string, opts ...DeleteOptions) (existed bool, err error) {
	backend, err := c.attachedBackend()
	if err != nil {
		return false, err
	}
	return dispatchDelete(ctx, backend, d, vars, firstDeleteOptions(opts))
}

// List returns the decoded []ListResult[T] boxed as any — one result
// per discovered entry, success or failure (see [ListResult] and the
// "Phase 1 amendment" section of docs/roadmap/file-api.md); only a
// walk-level failure (the directory itself can't be read/walked) fails
// the whole call via the plain error return.
//
// opts is variadic (0 or 1 value accepted) — pass a [ListOptions] with
// Recursive set to descend into every subdirectory under the
// resource's parent directory, not just its immediate children.
// Omitted (or Recursive: false) keeps today's single-level discovery,
// byte-for-byte unchanged.
func (c *Client) List(ctx context.Context, l any, vars map[string]string, opts ...ListOptions) (any, error) {
	backend, err := c.attachedBackend()
	if err != nil {
		return nil, err
	}
	return dispatchList(ctx, backend, l, vars, firstListOptions(opts))
}

// Exists returns (Entry{}, false, nil) for the idempotent "absent" case
// — never an error.
func (c *Client) Exists(ctx context.Context, e any, vars map[string]string) (Entry, bool, error) {
	backend, err := c.attachedBackend()
	if err != nil {
		return Entry{}, false, err
	}
	return dispatchExists(ctx, backend, e, vars)
}

// Copy and Move BOTH take TWO var sets — srcVars/dstVars — the SAME
// declared template instantiated twice with different values.
func (c *Client) Copy(ctx context.Context, cp any, srcVars, dstVars map[string]string) error {
	backend, err := c.attachedBackend()
	if err != nil {
		return err
	}
	return dispatchCopy(ctx, backend, cp, srcVars, dstVars)
}

func (c *Client) Move(ctx context.Context, mv any, srcVars, dstVars map[string]string) error {
	backend, err := c.attachedBackend()
	if err != nil {
		return err
	}
	return dispatchMove(ctx, backend, mv, srcVars, dstVars)
}

// Mkdir returns created=false (nil error) for the idempotent "already
// existed" case.
//
// opts is variadic (0 or 1 value accepted) — pass a [MkdirOptions] to
// set DryRun for this call. An attach-time [NativePOSIXMode] Capability,
// when declared, always wins over opts.NativePOSIXMode.
func (c *Client) Mkdir(ctx context.Context, m any, vars map[string]string, opts ...MkdirOptions) (created bool, err error) {
	backend, err := c.attachedBackend()
	if err != nil {
		return false, err
	}
	return dispatchMkdir(ctx, backend, m, vars, firstMkdirOptions(opts))
}

// BatchDelete mirrors S3's real DeleteObjects — a batch call can
// PARTIALLY fail: the returned []BatchDeleteResult carries ONE result
// per varsList entry, IN ORDER. err is non-nil ONLY for a call-level
// failure.
func (c *Client) BatchDelete(ctx context.Context, bd any, varsList []map[string]string) ([]BatchDeleteResult, error) {
	backend, err := c.attachedBackend()
	if err != nil {
		return nil, err
	}
	return dispatchBatchDelete(ctx, backend, bd, varsList)
}

// OpenRead returns a codec-BYPASSING raw byte stream (Mechanism 2 — see
// docs/roadmap/file-api.md's "Streaming/chunked reads and writes"
// section). The caller is responsible for calling Close — RecordFileRead
// fires at Close time, not Open time.
//
// opts is variadic (0 or 1 value accepted) — pass a [StreamReadOptions]
// for an HTTP-Range-style byte-range read; omitted (or the zero value)
// reads the whole file, served via a 3-level fallback cascade
// ([RangeReadingBackend] native -> [StreamReadBackend] approximated ->
// [Backend.ReadBytes] universal).
func (c *Client) OpenRead(ctx context.Context, sr any, vars map[string]string, opts ...StreamReadOptions) (io.ReadCloser, error) {
	backend, err := c.attachedBackend()
	if err != nil {
		return nil, err
	}
	return dispatchOpenRead(ctx, backend, sr, vars, firstStreamReadOptions(opts))
}

// OpenWrite returns a codec-BYPASSING raw byte stream (Mechanism 2). The
// caller is responsible for writing content and calling Close — Close
// MUST commit the write atomically (temp-file+rename, the SAME
// atomicity story as [ConditionalWriteBackend.WriteBytesIfMatch]);
// RecordFileWrite fires at Close time, with success reflecting Close's
// own return value.
//
// opts is variadic (0 or 1 value accepted) — WriteOptions.IfMatch is
// NOT supported on this role this round (never consulted); Metadata is
// a DIRECT passthrough (there is no decoded T to derive it from, unlike
// Create/Patch's deriveWriteMetadata).
func (c *Client) OpenWrite(ctx context.Context, sw any, vars map[string]string, opts ...WriteOptions) (io.WriteCloser, error) {
	backend, err := c.attachedBackend()
	if err != nil {
		return nil, err
	}
	return dispatchOpenWrite(ctx, backend, sw, vars, firstWriteOptions(opts))
}

// ListDir dispatches ld (a bare [ListDirer] OR a pre-built
// [*ListDirHandle] — the SAME dual-accept contract every other Client
// dispatch method already honors) against c's attached Backend. vars
// supplies the underlying [Dir]'s OWN directory path template's
// variables (NOT [EntryPattern]'s — those are populated per-discovered-
// entry).
//
// opts is variadic (0 or 1 value accepted) — pass a [DirOptions] to
// control CreateIfMissing/CreatePerm/DryRun/Strict for this call.
func (c *Client) ListDir(ctx context.Context, ld any, vars map[string]string, opts ...DirOptions) ([]DirEntry, error) {
	backend, err := c.attachedBackend()
	if err != nil {
		return nil, err
	}
	return dispatchListDir(ctx, backend, ld, vars, firstDirOptions(opts))
}

// DeleteDir mirrors [Client.ListDir]'s dual-accept contract, dispatching
// dd (a bare [DeleteDirer] OR a pre-built [*DeleteDirHandle]).
//
// opts is variadic (0 or 1 value accepted) — pass a [DirOptions] to
// control DeleteRecursive/DryRun/Strict for this call.
func (c *Client) DeleteDir(ctx context.Context, dd any, vars map[string]string, opts ...DirOptions) ([]string, error) {
	backend, err := c.attachedBackend()
	if err != nil {
		return nil, err
	}
	return dispatchDeleteDir(ctx, backend, dd, vars, firstDirOptions(opts))
}
