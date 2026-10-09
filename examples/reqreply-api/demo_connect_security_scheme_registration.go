package main

import (
	"fmt"
	"strings"

	"github.com/DaniDeer/go-codex/api/reqreply"
)

// demoConnectSecuritySchemeRegistration demonstrates
// reqreply.Server.AddConnectSecurityScheme (docs/design/
// d-0007-declarative-middleware-layering.md's Phase 4) — mirrors
// events.Client.AddConnectSecurityScheme byte-for-byte. Closes a gap
// where a connection-level scheme referenced ONLY via a Server.Security
// list (never by any individual route's own declaration) previously
// could not be spec-registered in components/securitySchemes at all.
func demoConnectSecuritySchemeRegistration() {
	fmt.Println("\n── Demo: Server.AddConnectSecurityScheme (connection-level spec registration) ──")

	server := reqreply.NewServer(reqreply.Info{Title: "Connect-scheme registration demo", Version: "1.0.0"})
	server.AddConnectSecurityScheme("brokerAuth", reqreply.BasicScheme().SecurityScheme)
	server.AddServer("production", reqreply.ServerEntry{
		URL: "mqtts://broker.example.com:8883", Protocol: "mqtt5",
		Security: []reqreply.SecurityRequirement{reqreply.Require("brokerAuth")},
	})

	doc, err := server.AsyncAPISpec()
	if err != nil {
		fmt.Printf("  [error] AsyncAPISpec: %v\n", err)
		return
	}
	yamlBytes, err := doc.MarshalYAML()
	if err != nil {
		fmt.Printf("  [error] MarshalYAML: %v\n", err)
		return
	}
	spec := string(yamlBytes)

	if strings.Contains(spec, "brokerAuth:") && strings.Contains(spec, "scheme: basic") {
		fmt.Println("  ✓ 'brokerAuth' appears in components/securitySchemes — ZERO routes reference it,")
		fmt.Println("    only the \"production\" server's own Security list does")
	} else {
		fmt.Println("  ✗ 'brokerAuth' MISSING from components/securitySchemes — BUG")
	}
}
