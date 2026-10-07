package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// demoSpecEndpoint proves rest.Server.ServeSpec's GET /openapi.yaml route
// (registered natively in chiserver/nethttpserver via b.ServeSpec — see
// docs/features/spec-endpoint.md) is reachable over the wire on BOTH
// servers, AND demonstrates real Accept-header content negotiation
// between YAML (default) and JSON — the SAME negotiation machinery any
// other route's WithFormats declaration uses, nothing spec-specific.
func demoSpecEndpoint(chiAddr, nethttpAddr string) {
	fmt.Println("=== GET /openapi.yaml — ServeSpec, reachable on both servers ===")
	targets := []struct {
		label string
		addr  string
	}{
		{"chi", chiAddr},
		{"net/http", nethttpAddr},
	}
	accepts := []string{"application/yaml", "application/json"}
	for _, t := range targets {
		for _, accept := range accepts {
			req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, "http://"+t.addr+"/openapi.yaml", nil)
			if err != nil {
				fmt.Printf("  [%s %s] request build error: %v\n", t.label, accept, err)
				continue
			}
			req.Header.Set("Accept", accept)
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				fmt.Printf("  [%s %s] error: %v\n", t.label, accept, err)
				continue
			}
			body, _ := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			snippet := strings.TrimSpace(string(body))
			if len(snippet) > 60 {
				snippet = snippet[:60] + "..."
			}
			fmt.Printf("  [%s] Accept: %-16s -> Status: %s, Content-Type: %s, bytes: %d, snippet: %q\n",
				t.label, accept, resp.Status, resp.Header.Get("Content-Type"), len(body), snippet)
		}
	}
	fmt.Println()
}
