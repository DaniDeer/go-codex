package main

import (
	"fmt"
	"io"
	"net/http"
)

// demoSpecEndpoint proves the hand-rolled GET /openapi.yaml handler
// (registered in chiserver/nethttpserver — see docs/roadmap/openapi-spec-endpoint.md
// for the declarative convenience this stands in for today) is actually
// reachable over the wire on BOTH servers, not just printable from the
// in-memory Document value at the end of main().
func demoSpecEndpoint(chiAddr, nethttpAddr string) {
	fmt.Println("=== GET /openapi.yaml — reachable on both servers ===")
	targets := []struct {
		label string
		addr  string
	}{
		{"chi", chiAddr},
		{"net/http", nethttpAddr},
	}
	for _, t := range targets {
		resp, err := http.Get("http://" + t.addr + "/openapi.yaml") //nolint:noctx
		if err != nil {
			fmt.Printf("  [%s] error: %v\n", t.label, err)
			continue
		}
		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		fmt.Printf("  [%s] Status: %s, Content-Type: %s, bytes: %d\n",
			t.label, resp.Status, resp.Header.Get("Content-Type"), len(body))
	}
	fmt.Println()
}
