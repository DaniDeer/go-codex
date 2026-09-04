// Package mqtt adapts [api/events] channel handles to [Paho MQTT] callbacks.
//
// [NewSubscribeTransport] and [NewPublishTransport] build the generic,
// per-T [events.SubscribeTransport]/[events.PublishTransport] values
// consumed by [events.SubscribeHandle]/[events.PublishHandle] — the
// no-*Client, handle-based call surface Decision 7
// (docs/design/d-0002-pubsub-workflow-simplification.md) inverts into api/events
// itself. Internally these still decode/validate/encode payloads using the
// channel handle's codec exactly as before; only the call shape changed.
//
// Typical handle-based usage:
//
//	b := events.NewClient(events.WithInfo(events.Info{Title: "My Events", Version: "1.0.0"}))
//	userCreated, _ := events.NewChannel[UserCreated]("user/created", codec,
//	    events.ChannelMeta{}).WithSubscribe(events.Subscribe{}).Handle(b)
//
//	// Wire to Paho on connect (JSON, the default):
//	subTransport := mqtt.NewSubscribeTransport[UserCreated](client, 1, mqtt.SubscribeOptions{
//	    OnError: func(e mqtt.SubscribeError) { log.Println("event error:", e) },
//	})
//	err := events.SubscribeHandle(ctx, userCreated, subTransport, func(ctx context.Context, e UserCreated) error {
//	    return svc.HandleUserCreated(ctx, e)
//	})
//
//	// Subscribe with a custom format (e.g. YAML):
//	subTransport := mqtt.NewSubscribeTransport[UserCreated](client, 1, opts, format.YAML(codec))
//	err := events.SubscribeHandle(ctx, userCreated, subTransport, handler)
//
//	// Publish an event (JSON, the default):
//	notification := NotificationCommand{Recipient: "alice@example.com", ...}
//	pubTransport := mqtt.NewPublishTransport[NotificationCommand](client, 1, false, opts)
//	err = events.PublishHandle(ctx, notifChannel.WithPublish(events.Publish{}), pubTransport, notification)
//
//	// Publish with a custom format (e.g. YAML):
//	pubTransport := mqtt.NewPublishTransport[NotificationCommand](client, 1, false, opts, format.YAML(codec))
//	err = events.PublishHandle(ctx, notifChannel.WithPublish(events.Publish{}), pubTransport, notification)
//
// # Connect — owning the broker connection
//
// [Connect] wraps [pahomqtt.NewClientOptions]/[pahomqtt.Client.Connect] for
// callers who want go-codex to own the broker connection lifecycle
// (credentials, keep-alive, TLS, clean-session) instead of constructing a
// [pahomqtt.Client] by hand:
//
//	client, err := mqtt.Connect(ctx, "tcp://broker:1883", mqtt.ConnectOptions{
//	    ClientID: "svc-1", KeepAlive: 30 * time.Second,
//	})
//
// [Attach] (below) and [NewPublishTransport]/[NewSubscribeTransport] accept
// any [pahomqtt.Client] — a [Connect]-returned client or a hand-built one
// work identically at every call site.
//
// # Attach — the single-workflow entry point
//
// [Attach] binds an already-connected [pahomqtt.Client] to an
// [events.Client] registry and returns an [events.Transport], giving the
// [events.Client] a literal `Publish(ctx, pub, msg)`/`Subscribe(ctx, sub,
// fn)`/`ServeSubscribers(ctx)` call shape — the same workflow available
// uniformly across every pub/sub adapter (mqtt5, zeromq). Internally, an
// unexported caller type still bundles the [pahomqtt.Client] with the
// [events.Client] registry and wires each subscription onto a
// [subscribeHandler] closure (unlike a router-based transport, MQTT 3.1.1
// has no router concept at all, so this wiring is genuinely new work for
// this package, not a mechanical rename); none of that is publicly
// reachable — call [Attach] and use the returned [events.Client] methods
// instead.
//
//	_ = mqtt.Attach(eventsClient, client)
//	sub := userCreated.WithSubscribe(events.Subscribe{})
//	err := eventsClient.Subscribe(ctx, sub, func(ctx context.Context, e UserCreated) error {
//	    return svc.HandleUserCreated(ctx, e)
//	})
//
// [events.Client.ServeSubscribers] walks every [events.Subscriber]
// registered against the bound [events.Client] via
// [events.Subscriber.Register] and subscribes each one in one call —
// useful for an application with many channels declared up front.
//
// Publishing goes through [events.Client.Publish], which satisfies
// [events.PublisherClient] with just (ctx, msg) via the same [Attach]-
// returned [events.Transport] — the publish-side mirror of the
// subscribe-side abstractions above.
//
// A templated topic (e.g. "sensors/{sensorID}/data") is automatically
// translated to a broker-compatible wildcard filter ("sensors/+/data")
// via [SubscribeOptions.TopicFilter] — set explicitly to override, or
// leave empty for the auto-derived filter.
//
// [PublishOptions.CredentialFunc] closes MQTT 3.1.1's message-level
// credential gap: unlike MQTT 5's User Properties, 3.1.1 carries no
// per-message metadata channel at all, so CredentialFunc grants
// write-access directly into the outgoing payload (an ordinary struct
// field) instead of a protocol-native side channel.
//
// [events.Subscriber.SubscribeMW]/[events.Publisher.PublishMW]-attached
// Fns recognize two shapes, validated eagerly: the security shape
// (func(context.Context, pahomqtt.Message, *T) (map[string][]string, error)
// on subscribe; func(context.Context, *T, []route.SecurityRequirement)
// error on publish) and a general-purpose wrapping shape
// (func(next func(context.Context, T) error) func(context.Context, T)
// error) for cross-cutting concerns like observability — see
// [Observability].
package mqtt
