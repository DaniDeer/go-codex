package reqreply

import (
	"context"
	"log/slog"
	"sync"

	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/format"
	"github.com/DaniDeer/go-codex/middleware"
	asyncapi "github.com/DaniDeer/go-codex/render/asyncapi/v3"
	"github.com/DaniDeer/go-codex/route"
	"github.com/DaniDeer/go-codex/validate"
)

// SpecTopicRequiredError is returned by [Server.ServeSpec] when topic is
// empty — ServeSpec has no default topic, mirroring every other Register
// call's "explicit, required" convention.
type SpecTopicRequiredError struct{}

func (e SpecTopicRequiredError) Error() string {
	return "reqreply: ServeSpec requires a non-empty topic"
}

// LogValue implements [slog.LogValuer] for structured logging.
func (e SpecTopicRequiredError) LogValue() slog.Value {
	return slog.GroupValue(slog.String("op", "ServeSpec"))
}

// SpecReq is [Server.ServeSpec]'s request body — reqreply has no
// `Accept`-header equivalent, so format selection moves into the body
// instead of a header. Format is "yaml" (the default, used when empty) or
// "json". A body field (rather than an mqtt5-only User-Property) is used
// because reqreply routes are expected to stay portable across BOTH
// mqtt5 (which has User Properties) AND zeromq (which has no property
// mechanism at all) — a body field works identically on both adapters.
type SpecReq struct {
	// Format selects the spec document's serialization: "yaml" (default,
	// used when empty) or "json".
	Format string
}

var specReqCodec = codex.Struct[SpecReq](
	codex.OptionalField("format",
		codex.String().Refine(validate.OneOf("", "yaml", "json")),
		func(r SpecReq) string { return r.Format },
		func(r *SpecReq, v string) { r.Format = v },
	),
)

// SpecOpt configures [Server.ServeSpec].
type SpecOpt interface{ applySpecOpt(*specOptions) }

type specOptions struct {
	middlewares []specMiddleware
}

type specMiddleware struct {
	mw middleware.RouteMiddleware
	fn any
}

type specMiddlewareOpt specMiddleware

func (o specMiddlewareOpt) applySpecOpt(so *specOptions) {
	so.middlewares = append(so.middlewares, specMiddleware(o))
}

// WithSpecMiddleware attaches a general-purpose (unpaired) middleware
// implementation to the internal route [Server.ServeSpec] registers —
// identical nilable-mw semantics as [Route.HandleMW].
func WithSpecMiddleware(mw middleware.RouteMiddleware, fn any) SpecOpt {
	return specMiddlewareOpt{mw: mw, fn: fn}
}

// ServeSpec registers an internal reply-topic serving b's own
// [Server.AsyncAPISpec] — the underlying document is computed lazily on
// the first [Call] and cached thereafter; each call re-marshals the
// cached document into the requested format (cheap).
//
// Since reqreply has no `Accept`-header equivalent, format selection is
// part of the request body: [SpecReq.Format] is "yaml" (default, used
// when empty) or "json". The response is the raw, pre-marshaled document
// bytes (via [format.Binary]/[codex.Bytes] — there is nothing to
// negotiate against, since the handler itself picks the format from the
// request).
//
// topic is REQUIRED — [SpecTopicRequiredError] is returned if empty. Like
// every other Register call, ServeSpec must be called BEFORE
// [Server.Attach]. A duplicate topic is reported the same way any other
// route collision is — [DuplicateRouteError], from the underlying
// [Route.Register] call ServeSpec makes internally.
//
// ServeSpec returns the registered [*RouteHandle] — unlike
// [rest.Server.ServeSpec]/[events.Client.ServeSpec] (which have no
// client-side handle-reuse convention of their own), a reqreply caller
// needs this handle to [Client.Call] the SAME route from the client
// side, exactly like any other server-side handle in this package (see
// [CallWithTransport]'s doc comment for the shared pattern):
//
//	b := reqreply.NewServer(reqreply.Info{Title: "My API", Version: "1.0.0"})
//	// ... register routes ...
//	specHandle, err := b.ServeSpec("spec")
//	if err != nil {
//	    log.Fatal(err)
//	}
//	b.Attach(mqtt5transport.NewServerTransport(...))
//
//	// A caller fetches the spec via a normal Call, reusing specHandle:
//	respAny, err := client.Call(ctx, specHandle, reqreply.SpecReq{Format: "json"})
func (b *Server) ServeSpec(topic string, opts ...SpecOpt) (*RouteHandle[SpecReq, []byte], error) {
	if topic == "" {
		return nil, SpecTopicRequiredError{}
	}
	var so specOptions
	for _, o := range opts {
		o.applySpecOpt(&so)
	}

	specFormat := format.Binary(codex.Bytes())

	var once sync.Once
	var cachedYAML, cachedJSON []byte
	var cacheErr error

	r := NewRoute[SpecReq, []byte](topic, specReqCodec, codex.Bytes(),
		RouteMeta{
			OperationID: "getSpec",
			Summary:     "This API's own AsyncAPI specification",
			Description: "Serves this API's own AsyncAPI 3.0 document, in YAML (default) or JSON per SpecReq.Format.",
			Tags:        []string{"spec"},
			// Security is an explicit, non-nil EMPTY slice — the spec
			// document is public by default, even when the Server
			// declares a [Server.AddGlobalSecurity] requirement for
			// every other route (nil Security would otherwise INHERIT
			// that global requirement, like any other route — see
			// [RouteMeta.Security]'s doc comment).
			Security: []route.SecurityRequirement{},
		},
	).WithHandler(func(_ context.Context, req SpecReq) ([]byte, error) {
		// Pre-marshal BOTH formats once, inside the SAME once.Do, rather
		// than caching the asyncapi.Document value and re-marshaling it
		// per request: Document.MarshalYAML/MarshalJSON read channels/
		// schemas maps that ALIAS b's own internal, persistent
		// docBuilder (asyncapi.DocumentBuilder.Build() assigns its maps
		// by reference, never copies them) — a route registered on b
		// AFTER this cache is filled would otherwise silently leak into
		// a "cached" response, contradicting this method's own "cached
		// thereafter" contract. Marshaling immediately, once, freezes
		// the OUTPUT bytes rather than relying on the Document staying
		// unmutated.
		once.Do(func() {
			var doc asyncapi.Document
			doc, cacheErr = b.AsyncAPISpec()
			if cacheErr != nil {
				return
			}
			cachedYAML, cacheErr = doc.MarshalYAML()
			if cacheErr != nil {
				return
			}
			cachedJSON, cacheErr = doc.MarshalJSON()
		})
		if cacheErr != nil {
			return nil, cacheErr
		}
		if req.Format == "json" {
			return cachedJSON, nil
		}
		return cachedYAML, nil
	})
	for _, m := range so.middlewares {
		r = r.HandleMW(m.mw, m.fn)
	}

	handle, err := r.Register(b)
	if err != nil {
		return nil, err
	}
	handle.WithFormats(specFormat)
	return handle, nil
}
