// Package templ provides a [format.Format] factory that renders a
// [github.com/a-h/templ] component as a text/html response.
//
// Use [Format] to create a templ format and add it to the ResponseFormats
// of a [rest.Route]. The existing [adapters/nethttp] and [adapters/chi]
// adapters handle content negotiation: browsers that send Accept: text/html
// receive the rendered component; API clients that send Accept: application/json
// receive JSON — all from the same handler and the same route definition.
//
//	route, _ := rest.AddRoute(b, "GET", "/articles", reqCodec, propsCodec,
//	    rest.RouteConfig{...},
//	    adapttempl.Format(propsCodec, ArticleList),  // Accept: text/html
//	    format.JSON(propsCodec),                      // Accept: application/json
//	)
//
//	// Same handler, same route — nethttp handles both formats:
//	nethttp.Register(mux, route, func(ctx context.Context, req SearchReq) (Props, error) {
//	    return svc.Search(ctx, req.Query)
//	}, nethttp.Options{Observer: obs})
//
// Props are validated via the route's response codec before the component
// renders. Invalid props return HTTP 500 and the template is never reached.
//
// The templ component receives [context.Background] during rendering; data
// needed by the component should be included in the Props struct.
//
// For chi routers, use [adapters/chi] with the same route and add the format
// via ResponseFormats — no chi-specific variant is needed.
package templ

import (
	"bytes"
	"context"
	"fmt"

	atempl "github.com/a-h/templ"

	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/format"
)

// DecodeNotSupportedError is returned by the templ format's Unmarshal function.
// HTML responses cannot be decoded back into a typed value.
// Use [errors.As] to detect this error when inspecting format decode failures.
type DecodeNotSupportedError struct {
	// ContentType is the content type of the response that cannot be decoded.
	ContentType string
}

func (e DecodeNotSupportedError) Error() string {
	return fmt.Sprintf("templ: %q response cannot be decoded as a typed value", e.ContentType)
}

// Format wraps a templ component as a [format.Format] with ContentType
// "text/html; charset=utf-8". Add it to a route's ResponseFormats to enable
// HTML rendering via the existing [adapters/nethttp] or [adapters/chi] adapters.
//
// Props are validated via c (all Refine constraints run) before the component
// renders. If validation fails, the adapter returns HTTP 500 before any HTML
// is written.
//
// The component is rendered with [context.Background]. Pass all data the
// component needs through the Props struct.
func Format[Props any](c codex.Codec[Props], component func(Props) atempl.Component) format.Format[Props] {
	const ct = "text/html; charset=utf-8"
	return format.NewTyped(
		c,
		func(props Props) ([]byte, error) {
			var buf bytes.Buffer
			if err := component(props).Render(context.Background(), &buf); err != nil {
				return nil, err
			}
			return buf.Bytes(), nil
		},
		func([]byte) (Props, error) {
			var zero Props
			return zero, DecodeNotSupportedError{ContentType: ct}
		},
		ct,
	)
}
