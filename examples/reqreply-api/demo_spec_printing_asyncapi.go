package main

import (
	"fmt"
	"os"

	"github.com/DaniDeer/go-codex/api/reqreply"
)

// demoSpecPrintingAsyncAPI proves the AsyncAPI 3.0 document accumulated by
// [reqreply.Server] across ALL registered routes (mirroring
// examples/rest-api's OpenAPISpec printing convention) is derived
// entirely from the route declarations already made — no separate spec
// authoring step.
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
