package main

import (
	"fmt"
	"strings"

	"github.com/DaniDeer/go-codex/api/events"
)

// demoConnectSecuritySchemeRegistration demonstrates
// events.Client.AddConnectSecurityScheme (docs/design/
// d-0007-declarative-middleware-layering.md's Phase 4) — DISTINCT from
// demoConnectLevelSecurity above (that demo is about
// mqtt5.NewSecuredClient/ConnectSecurityScheme, an adapter-level
// RUNTIME mechanism). AddConnectSecurityScheme is the API-LEVEL
// AsyncAPI SPEC-registration half: it closes a gap where a connection-
// level scheme referenced ONLY via a Server.Security list (never by any
// individual channel's own Subscribe/Publish requirement) previously
// could not be spec-registered in components/securitySchemes at all.
func demoConnectSecuritySchemeRegistration() {
	fmt.Println("--- Demo: Client.AddConnectSecurityScheme (connection-level spec registration) ---")

	client := events.NewClient(events.WithInfo(events.Info{Title: "Connect-scheme registration demo", Version: "1.0.0"}))
	client.AddConnectSecurityScheme("brokerAuth", events.BasicScheme().SecurityScheme)
	client.AddServer("production", events.Server{
		URL: "mqtts://broker.example.com:8883", Protocol: "mqtt5",
		Security: []events.SecurityRequirement{events.Require("brokerAuth")},
	})

	doc, err := client.AsyncAPISpec()
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
		fmt.Println("  ✓ 'brokerAuth' appears in components/securitySchemes — ZERO channels reference it,")
		fmt.Println("    only the \"production\" server's own Security list does")
	} else {
		fmt.Println("  ✗ 'brokerAuth' MISSING from components/securitySchemes — BUG")
	}
	fmt.Println()
}
