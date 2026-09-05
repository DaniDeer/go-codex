package chi

import (
	"context"
	"errors"
	"net/http"
	"time"

	gochi "github.com/go-chi/chi/v5"

	"github.com/DaniDeer/go-codex/api/rest"
)

// serverTransport implements [rest.ServerTransport], wiring builder's
// routes onto r (reusing the unexported [serve]/[serveSSE] internally)
// and owning its own [*http.Server] — built by [AttachRouter]. Mirrors
// [adapters/nethttp]'s identical transport exactly ([gochi.Router]
// already satisfies [http.Handler], so the same *http.Server{Handler: ...}
// pattern applies unchanged). See
// docs/design/d-0001-rest-middleware-workflow-simplification.md's Addendum 5 for the full design.
//
// Fixes a real gap confirmed while scoping Decision 6 (see
// docs/design/d-0002-pubsub-workflow-simplification.md): earlier, [AttachRouter]'s
// [serverTransport.Serve] wired ONLY plain routes via [serve], never SSE
// routes registered via [rest.SSERoute.Register]/[rest.SSERoute.RegisterHandle]
// — an SSE route was silently unreachable through the Attach workflow.
// [serverTransport.Serve] now calls BOTH [serve] and [serveSSE] against the
// SAME router/builder pair — safe to call back-to-back since each walks
// only its OWN entry kind (plain vs. SSE) and returns nil, wiring nothing,
// when that kind is simply absent from builder (see
// adapters/chi/servertransport_test.go's plain-only/SSE-only/mixed
// coverage).
type serverTransport struct {
	builder *rest.Server
	router  gochi.Router
	addr    string
}

// AttachRouter binds builder+r+addr as builder's [rest.ServerTransport] —
// the "attach the adapter to the builder" step behind
// [rest.Server.Serve]. Returns [rest.ServerTransportAlreadyAttachedError]
// if builder already has a transport attached.
//
// Unlike [serve] (wire-only, non-blocking, caller owns their own
// *http.Server), [rest.Server.Serve] (after AttachRouter) BLOCKS, owning
// its OWN *http.Server{Addr: addr, Handler: r}, until ctx is cancelled
// (graceful [http.Server.Shutdown]) — a NEW, ADDITIVE, opt-in convenience.
// [serve] itself remains completely unchanged for callers needing full
// *http.Server control.
//
//	builder := rest.NewServer(rest.Info{Title: "My API", Version: "1.0.0"})
//	if err := createUserRoute.Register(builder); err != nil { ... }
//	r := gochi.NewRouter()
//	if err := chi.AttachRouter(builder, r, ":8080"); err != nil { ... }
//	err := builder.Serve(ctx) // blocks, owns its own http.Server
func AttachRouter(builder *rest.Server, r gochi.Router, addr string) error {
	return builder.Attach(&serverTransport{builder: builder, router: r, addr: addr})
}

// Serve implements [rest.ServerTransport]. Wires BOTH plain routes (via
// [serve]) and SSE routes (via [serveSSE]) onto t.router before starting
// t's *http.Server — see this type's doc comment for the gap this fixes.
func (t *serverTransport) Serve(ctx context.Context) error {
	if err := serve(t.router, t.builder); err != nil {
		return err
	}
	if err := serveSSE(t.router, t.builder); err != nil {
		return err
	}

	srv := &http.Server{Addr: t.addr, Handler: t.router, ReadHeaderTimeout: 10 * time.Second}
	errCh := make(chan error, 1)
	go func() { errCh <- srv.ListenAndServe() }()

	select {
	case <-ctx.Done():
		return srv.Shutdown(context.Background())
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}

var _ rest.ServerTransport = (*serverTransport)(nil)
