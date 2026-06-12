// Package templ provides a [format.Format] factory that renders a
// [github.com/a-h/templ] component as a text/html response.
//
// Use [Format] to create a templ format and add it to the ResponseFormats
// of a [rest.Route]. The existing [adapters/nethttp] and [adapters/chi]
// adapters handle content negotiation: browsers that send Accept: text/html
// receive the rendered component; API clients that send Accept: application/json
// receive JSON — all from the same handler and the same route definition.
//
//	route, _ := rest.NewRoute[Req, Props]("GET", "/articles", reqCodec, propsCodec,
//	    rest.RouteMeta{},
//	    adapttempl.Format(propsCodec, ArticleList),  // Accept: text/html
//	    format.JSON(propsCodec),                      // Accept: application/json
//	).Register(b)
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
