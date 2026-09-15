// Package events-api demonstrates go-codex's declarative pub/sub workflow
// as a small, real, multi-package project — not one big file — mirroring
// examples/reqreply-api's own layout:
//
//	routes/              — THE shared contract package: domain models,
//	                       codecs, and channel declarations (plain
//	                       channel, channel-level security via
//	                       .Use()+SubscribeMW/PublishMW, wildcard-topic
//	                       channel, error-path ergonomics channel) — every
//	                       events.Channel/Subscriber/Publisher here is an
//	                       UNATTACHED spec value, no adapter yet. It is
//	                       the ONE package every broker package, client
//	                       package, and demo file imports — the Go
//	                       compiler enforces byte-identical channel/codec
//	                       declarations across all three transports
//	                       simultaneously (see demo_spec_printing_asyncapi.go,
//	                       which makes this concrete).
//	handlers/            — SERVER-side business logic (subscribe
//	                       handlers, domain transforms, security
//	                       implementations), adapter-agnostic — except
//	                       handlers/security.go, which necessarily has ONE
//	                       function PER ADAPTER since each pub/sub
//	                       adapter's SubscribeMW-recognized security shape
//	                       is genuinely different (mqtt v3's raw
//	                       pahomqtt.Message vs. mqtt5's *pahomqtt5.Publish
//	                       vs. zeromq's no-raw-message-at-all).
//	observability/       — this example's OWN [stats.Observer]
//	                       implementation ([observability.DemoObserver]),
//	                       kept separate from handlers/ (per-channel
//	                       business logic) — the shipped, library-owned
//	                       [events.Observability] (core)/thinned
//	                       [adapters/mqtt5.Observability]/
//	                       [adapters/mqtt.Observability] is used directly
//	                       at the demo attachment points, not reimplemented
//	                       here.
//	mqtt5broker/          — assembles routes/+handlers/ onto adapters/mqtt5
//	                       (in-process mock broker, no real MQTT 5 broker
//	                       needed).
//	zeromqbroker/         — assembles routes/+handlers/ onto adapters/zeromq
//	                       PUB/SUB (in-process PipeSocket pair, no real
//	                       ZeroMQ broker/socket library needed).
//	mqttbroker/           — assembles routes/+handlers/ onto adapters/mqtt
//	                       (v3, Paho) — the escape-hatch handle-based
//	                       workflow demo, plus the domain-boundary
//	                       pipeline.
//	client/              — Publisher/Subscriber constructors for each
//	                       adapter-attachment mode above (both publish AND
//	                       subscribe roles) — a SEPARATE connection
//	                       sharing the SAME in-process mock transport a
//	                       *broker package's own Build already attached,
//	                       since events.Client is symmetric (unlike
//	                       reqreply's genuinely asymmetric Server/Client
//	                       pair).
//
// Package names avoid implying reqreply's asymmetric Server/Client split
// — events pub/sub's Client is symmetric (one value handles BOTH publish
// AND subscribe), so the per-adapter assembly packages are named
// "*broker/", not "*server/": each one builds the SUBSCRIBE/receiving
// side (running ServeSubscribers against an in-process mock broker/
// socket), while client/ builds constructors used for both roles across
// all three transports.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/DaniDeer/go-codex/examples/events-api/observability"
	"github.com/DaniDeer/go-codex/stats"
)

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	obs := observability.NewDemoObserver(logger)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ctx = stats.WithObserver(ctx, obs)

	fmt.Println("═══════════════════════════════════════════════════════")
	fmt.Println(" events-api — declarative pub/sub across mqtt v3, mqtt5, zeromq")
	fmt.Println("═══════════════════════════════════════════════════════")
	fmt.Println()

	demoClientAttachWorkflow(ctx)
	demoEscapeHatchWorkflow(ctx)
	demoWildcardSubscription(ctx)
	demoDomainBoundaryPipeline(ctx)
	demoUserPropertyMiddleware(ctx)
	demoPropertyMergeDirectAttachment(ctx)
	demoConnectLevelSecurity(ctx)
	demoErrorPathErgonomics(ctx)
	demoSecuritySubscribeMW(ctx, obs)
	demoZeromqPubSubRoundtrip(ctx)
	demoObservabilityMiddleware(ctx, obs)
	demoSpecPrintingAsyncAPI()

	subCount, pubCount, rejCount := obs.Summary()
	fmt.Println("═══════════════════════════════════════════════════════")
	fmt.Printf(" Observer summary: subscribed=%d published=%d rejected=%d\n", subCount, pubCount, rejCount)
	fmt.Println("═══════════════════════════════════════════════════════")
}
