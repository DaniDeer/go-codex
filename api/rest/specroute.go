package rest

import (
	"context"
	"fmt"
	"log/slog"
	"sync"

	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/format"
	"github.com/DaniDeer/go-codex/render/openapi"
	"github.com/DaniDeer/go-codex/schema"
)

// SpecPathRequiredError is returned by [Server.ServeSpec] when path is
// empty — ServeSpec has no default path, mirroring every other Register
// call's "explicit, required" convention.
type SpecPathRequiredError struct{}

func (e SpecPathRequiredError) Error() string {
	return "rest: ServeSpec requires a non-empty path"
}

// LogValue implements [slog.LogValuer] for structured logging.
func (e SpecPathRequiredError) LogValue() slog.Value {
	return slog.GroupValue(slog.String("op", "ServeSpec"))
}

// SpecDocumentNotDecodableError is returned by the [format.Format] values
// [Server.ServeSpec] registers when something attempts to DECODE a spec
// document from bytes (e.g. as a request body) — the spec endpoint is
// serve-only, there is no supported reverse direction.
type SpecDocumentNotDecodableError struct {
	// Format is the wire format that was attempted ("yaml" or "json").
	Format string
}

func (e SpecDocumentNotDecodableError) Error() string {
	return fmt.Sprintf("rest: spec document is not decodable from %s", e.Format)
}

// LogValue implements [slog.LogValuer] for structured logging.
func (e SpecDocumentNotDecodableError) LogValue() slog.Value {
	return slog.GroupValue(slog.String("format", e.Format))
}

// SpecOpt configures [Server.ServeSpec].
type SpecOpt interface{ applySpecOpt(*specOptions) }

type specOptions struct {
	middlewares []specMiddleware
}

type specMiddleware struct {
	mw RouteMiddleware
	fn any
}

type specMiddlewareOpt specMiddleware

func (o specMiddlewareOpt) applySpecOpt(so *specOptions) {
	so.middlewares = append(so.middlewares, specMiddleware(o))
}

// WithSpecMiddleware attaches a general-purpose (unpaired) middleware
// implementation to the internal route [Server.ServeSpec] registers —
// identical nilable-mw semantics as [Route.HandleMW] (e.g. attach
// an observer/timing middleware to the spec route exactly as you would
// to any other route). Demonstrates that the spec endpoint is "just
// another route" — it composes with the same middleware capabilities.
func WithSpecMiddleware(mw RouteMiddleware, fn any) SpecOpt {
	return specMiddlewareOpt{mw: mw, fn: fn}
}

// specDocCodec is an opaque passthrough [codex.Codec] for [openapi.Document]
// — mirrors [codex.Bytes]'s shape: Encode/Decode pass the value through
// unchanged. The real (de)serialization happens in each [format.Format]'s
// own marshal function (see [Server.ServeSpec]'s yamlSpecFormat/
// jsonSpecFormat), via real `Accept`-header content negotiation.
func specDocCodec() codex.Codec[openapi.Document] {
	return codex.Codec[openapi.Document]{
		Schema: schema.Schema{Type: "object"},
		Encode: func(v openapi.Document) (any, error) { return v, nil },
		Decode: func(v any) (openapi.Document, error) {
			d, ok := v.(openapi.Document)
			if !ok {
				return openapi.Document{}, codex.TypeMismatchError{Expected: "openapi.Document", Got: fmt.Sprintf("%T", v)}
			}
			return d, nil
		},
	}
}

// ServeSpec registers an internal GET route at path serving b's own
// [Server.OpenAPISpec] — computed lazily on the first request and cached
// thereafter (subsequent requests reuse the cached document; b's own
// route set never changes after [Server.Attach], so the cache never goes
// stale during normal operation).
//
// Supports real `Accept`-header content negotiation between
// "application/yaml" (the default, used for an empty or "*/*" Accept
// header) and "application/json" — the SAME negotiation algorithm and
// [format.Format] machinery any other route's [RouteHandle.WithFormats]
// declaration uses; there is nothing spec-route-specific about it.
//
// path is REQUIRED — [SpecPathRequiredError] is returned if empty. Like
// every other Register call, ServeSpec must be called BEFORE
// [Server.Attach].
//
// A duplicate path is reported the SAME way any other route collision is
// — by the adapter, at [Server.Attach] time (each adapter's own
// DuplicateRouteError) — ServeSpec does not duplicate that check.
//
//	b := rest.NewServer(rest.Info{Title: "My API", Version: "1.0.0"})
//	// ... register routes ...
//	if err := b.ServeSpec("/openapi.yaml"); err != nil {
//	    log.Fatal(err)
//	}
//	b.Attach(nethttp.NewServerTransport(...))
func (b *Server) ServeSpec(path string, opts ...SpecOpt) error {
	if path == "" {
		return SpecPathRequiredError{}
	}
	var so specOptions
	for _, o := range opts {
		o.applySpecOpt(&so)
	}

	docCodec := specDocCodec()
	yamlFormat := format.NewTyped(docCodec,
		func(d openapi.Document) ([]byte, error) { return d.MarshalYAML() },
		func([]byte) (openapi.Document, error) {
			return openapi.Document{}, SpecDocumentNotDecodableError{Format: "yaml"}
		},
		"application/yaml",
	)
	jsonFormat := format.NewTyped(docCodec,
		func(d openapi.Document) ([]byte, error) { return d.MarshalJSON() },
		func([]byte) (openapi.Document, error) {
			return openapi.Document{}, SpecDocumentNotDecodableError{Format: "json"}
		},
		"application/json",
	)

	var once sync.Once
	var cached openapi.Document
	var cacheErr error

	r := NewRoute[struct{}, openapi.Document]("GET", path,
		codex.Struct[struct{}](), docCodec,
		RouteMeta{
			OperationID: "getSpec",
			Summary:     "This API's own OpenAPI specification",
			Description: "Serves this API's own OpenAPI 3.1 document, in YAML (default) or JSON via the Accept header.",
			Tags:        []string{"spec"},
			RespStatus:  "200",
			// Security is an explicit, non-nil EMPTY slice — the spec
			// document is public by default, even when the Server
			// declares a [Server.AddGlobalSecurity] requirement for
			// every other route (nil Security would otherwise INHERIT
			// that global requirement, like any other route — see
			// [RouteMeta.Security]'s doc comment).
			Security: []SecurityRequirement{},
		},
	)
	for _, m := range so.middlewares {
		r = r.HandleMW(m.mw, m.fn)
	}

	handle, err := r.RegisterHandle(b)
	if err != nil {
		return err
	}
	handle.WithHandler(func(_ context.Context, _ struct{}) (openapi.Document, error) {
		once.Do(func() { cached, cacheErr = b.OpenAPISpec() })
		return cached, cacheErr
	}).WithFormats(yamlFormat, jsonFormat)
	return nil
}
