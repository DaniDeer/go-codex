package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/DaniDeer/go-codex/api/reqreply"
)

// demoSpecPrintingAsyncAPI proves the AsyncAPI 3.0 document accumulated by
// [reqreply.Server] across ALL registered routes (mirroring
// examples/rest-api's OpenAPISpec printing convention) is derived
// entirely from the route declarations already made — no separate spec
// authoring step. It ALSO demonstrates [reqreply.Server.ServeSpec]'s
// native self-serving endpoint end-to-end: a real [reqreply.Client.Call]
// against mqtt5Built.SpecHandle, once per [reqreply.SpecReq.Format]
// value — reqreply has no `Accept`-header equivalent, so format
// selection travels in the request body instead.
func demoSpecPrintingAsyncAPI(server *reqreply.Server) {
	fmt.Println("\n── Demo 7: AsyncAPI 3.0 spec, derived from route declarations ──")

	doc, err := server.AsyncAPISpec()
	if err != nil {
		fmt.Fprintf(os.Stderr, "AsyncAPISpec error: %v\n", err)
		os.Exit(1)
	}
	yamlBytes, err := doc.MarshalYAML()
	if err != nil {
		fmt.Fprintf(os.Stderr, "MarshalYAML error: %v\n", err)
		os.Exit(1)
	}
	fmt.Println(string(yamlBytes))
}

// demoServeSpecCall fetches server's self-served AsyncAPI spec via a REAL
// reqreply.Call (not just local printing) — once per format, since
// SpecReq.Format travels in the request body (no Accept header here).
func demoServeSpecCall(ctx context.Context, mqtt5Client *reqreply.Client, specHandle *reqreply.RouteHandle[reqreply.SpecReq, []byte]) {
	fmt.Println("\n── Demo 7b: ServeSpec — fetching the spec via a real Call ──")

	for _, format := range []string{"yaml", "json"} {
		respAny, err := mqtt5Client.Call(ctx, specHandle, reqreply.SpecReq{Format: format})
		if err != nil {
			fmt.Fprintf(os.Stderr, "Call(%s) error: %v\n", format, err)
			os.Exit(1)
		}
		body := respAny.([]byte)
		snippet := strings.TrimSpace(string(body))
		if len(snippet) > 60 {
			snippet = snippet[:60] + "..."
		}
		fmt.Printf("  [%s] bytes: %d, snippet: %q\n", format, len(body), snippet)
	}
}
