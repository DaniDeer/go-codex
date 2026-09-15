package main

import (
	"context"
	"errors"
	"fmt"

	mqtt5adapter "github.com/DaniDeer/go-codex/adapters/mqtt5"
	"github.com/DaniDeer/go-codex/api/events"
	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/examples/events-api/mqtt5broker"
	"github.com/DaniDeer/go-codex/examples/events-api/routes"
	"github.com/DaniDeer/go-codex/route"
	"github.com/DaniDeer/go-codex/validate"
)

// connectBearerAuth is a CONNECTION-level security scheme — validated
// ONCE at construction (via mqtt5.NewSecuredClient), distinct from the
// MESSAGE-level SubscribeMW/PublishMW security demonstrated by
// demo_security_subscribemw.go. See docs/features/security.md's
// "Connection-level vs message-level security".
var connectBearerAuth = mqtt5adapter.ConnectSecurityScheme{SecurityScheme: route.BasicScheme()}.
	WithCodec(codex.String().Refine(validate.MinLen(3)))

// demoConnectLevelSecurity demonstrates mqtt5.NewSecuredClient: the
// resulting *SecuredClient is a drop-in replacement for the raw broker
// client — every Publish/Subscribe call site is UNCHANGED from how it
// would look with the raw client.
func demoConnectLevelSecurity(ctx context.Context) {
	fmt.Println("--- Demo: mqtt5 connect-level security (NewSecuredClient) ---")

	router := mqtt5broker.NewMockRouter()
	broker := mqtt5broker.NewMockBroker(router)

	fmt.Println("  → wrapping an already-connected client with a valid credential:")
	secured, err := mqtt5adapter.NewSecuredClient(broker, connectBearerAuth, "svc-account", "s3cr3t-token")
	if err != nil {
		fmt.Printf("  [error] unexpected: %v\n", err)
		return
	}
	fmt.Println("  ✓ credential accepted — secured is a drop-in replacement for broker")

	securedTransport := mqtt5adapter.NewPublishTransport[routes.SensorReading](secured, 1, false,
		mqtt5adapter.PublishOptions[routes.SensorReading]{})
	if err := events.PublishHandle(ctx, routes.PlainReadingsPub, securedTransport,
		routes.SensorReading{SensorID: "f47ac10b-58cc-4372-a567-0e02b2c3d479", Value: 22.5}); err != nil {
		fmt.Printf("  [error] Publish: %v\n", err)
	} else {
		fmt.Println("  ✓ Publish through the wrapper works exactly like the raw client")
	}

	fmt.Println("  → wrapping the SAME client with a malformed (empty) credential:")
	_, err = mqtt5adapter.NewSecuredClient(broker, connectBearerAuth, "", "")
	var credErr mqtt5adapter.ConnectSecurityCredentialError
	if errors.As(err, &credErr) {
		fmt.Printf("  ✓ rejected at construction: scheme_type=%q (client never used)\n", credErr.Scheme.Type)
	} else {
		fmt.Printf("  [error] expected ConnectSecurityCredentialError, got: %v\n", err)
	}
	fmt.Println()
}
