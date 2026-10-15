package file

import (
	"context"
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
func (c *Client) Create(ctx context.Context, cr any, val any, vars map[string]string) error {
	backend, err := c.attachedBackend()
	if err != nil {
		return err
	}
	return dispatchCreate(ctx, backend, cr, val, vars)
}

// Patch round-trips a partial patch value onto the existing resource.
func (c *Client) Patch(ctx context.Context, p any, patch any, vars map[string]string) error {
	backend, err := c.attachedBackend()
	if err != nil {
		return err
	}
	return dispatchPatch(ctx, backend, p, patch, vars)
}

// Delete returns existed=false (nil error) for the idempotent "already
// absent" case.
func (c *Client) Delete(ctx context.Context, d any, vars map[string]string) (existed bool, err error) {
	backend, err := c.attachedBackend()
	if err != nil {
		return false, err
	}
	return dispatchDelete(ctx, backend, d, vars)
}

// List returns the decoded []T boxed as any.
func (c *Client) List(ctx context.Context, l any, vars map[string]string) (any, error) {
	backend, err := c.attachedBackend()
	if err != nil {
		return nil, err
	}
	return dispatchList(ctx, backend, l, vars)
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
func (c *Client) Mkdir(ctx context.Context, m any, vars map[string]string) (created bool, err error) {
	backend, err := c.attachedBackend()
	if err != nil {
		return false, err
	}
	return dispatchMkdir(ctx, backend, m, vars)
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
