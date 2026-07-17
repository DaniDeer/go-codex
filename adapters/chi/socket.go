package chi

import (
	"net/http"
	"strings"

	gochi "github.com/go-chi/chi/v5"

	"github.com/DaniDeer/go-codex/adapters/websocket"
	"github.com/DaniDeer/go-codex/ports"
)

// socketMux adapts a constructor-time-registered [swapHandler] to the
// [websocket.Mux] interface: the websocket adapters call Handle from
// Activate (potentially while chi is already serving — unsafe for real chi
// registration), and the shim turns that into an atomic handler install.
// Requests before installation get 503 (the R54 swap-handler pattern).
type socketMux struct{ sh *swapHandler }

func (m socketMux) Handle(_ string, h http.Handler) { m.sh.h.Store(h) }

// registerSocket registers the swap handler on r at CONSTRUCTOR time for
// the handle's GET upgrade route. chi uses the same {var} placeholder
// syntax as Go 1.22 ServeMux patterns, and chi ≥ v5.1 populates
// http.Request.PathValue — the websocket package's path-var extraction
// works unchanged.
func registerSocket(r gochi.Router, path string) socketMux {
	sh := &swapHandler{}
	r.Method(http.MethodGet, strings.TrimPrefix(path, "GET "), sh)
	return socketMux{sh: sh}
}

// IngestSocketAdapter is the chi variant of [websocket.IngestSocketAdapter]:
// identical behaviour, chi-safe registration (swap handler registered at
// constructor time; requests before Activate get 503).
//
//	domain.Commands.Bind(ctx, chi.IngestSocketAdapter(
//	    router, hub, websocket.NewUpgrader(websocket.UpgraderOptions{}),
//	    handle, websocket.IngestSocketAdapterOptions{}))
func IngestSocketAdapter[T any](
	r gochi.Router,
	hub *websocket.Hub,
	upgrader websocket.Upgrader,
	handle ports.Socket[T, struct{}],
	opts websocket.IngestSocketAdapterOptions,
) ports.SourceAdapter[T] {
	return chiSourceName[T]{
		SourceAdapter: websocket.IngestSocketAdapter(registerSocket(r, handle.Path), hub, upgrader, handle, opts),
		name:          "chi.IngestSocketAdapter",
	}
}

// BroadcastSocketAdapter is the chi variant of
// [websocket.BroadcastSocketAdapter] — chi-safe registration, otherwise
// identical.
func BroadcastSocketAdapter[T any](
	r gochi.Router,
	hub *websocket.Hub,
	upgrader websocket.Upgrader,
	handle ports.Socket[struct{}, T],
	opts websocket.BroadcastSocketAdapterOptions,
) ports.SinkAdapter[T] {
	return chiSinkName[T]{
		SinkAdapter: websocket.BroadcastSocketAdapter(registerSocket(r, handle.Path), hub, upgrader, handle, opts),
		name:        "chi.BroadcastSocketAdapter",
	}
}

// DuplexSocketAdapter is the chi variant of [websocket.DuplexSocketAdapter]
// — chi-safe registration, otherwise identical.
func DuplexSocketAdapter[In, Out any](
	r gochi.Router,
	hub *websocket.Hub,
	upgrader websocket.Upgrader,
	handle ports.Socket[In, Out],
	opts websocket.DuplexSocketAdapterOptions,
) ports.DuplexAdapter[In, Out] {
	return chiDuplexName[In, Out]{
		DuplexAdapter: websocket.DuplexSocketAdapter(registerSocket(r, handle.Path), hub, upgrader, handle, opts),
		name:          "chi.DuplexSocketAdapter",
	}
}

// Naming shims: behaviour delegates entirely to the websocket adapters; only
// AdapterName reflects the chi binding for observability.

type chiSourceName[T any] struct {
	ports.SourceAdapter[T]
	name string
}

func (a chiSourceName[T]) AdapterName() string { return a.name }

type chiSinkName[T any] struct {
	ports.SinkAdapter[T]
	name string
}

func (a chiSinkName[T]) AdapterName() string { return a.name }

type chiDuplexName[In, Out any] struct {
	ports.DuplexAdapter[In, Out]
	name string
}

func (a chiDuplexName[In, Out]) AdapterName() string { return a.name }
