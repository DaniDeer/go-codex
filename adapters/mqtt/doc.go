// Package mqtt adapts [api/events] channel handles to [Paho MQTT] callbacks.
//
// [SubscribeHandler] turns a [events.ChannelHandle] into an [mqtt.MessageHandler]
// that decodes and validates incoming payloads before calling the application
// handler. [Publish] encodes a value and publishes it to the broker.
//
// Typical usage:
//
//	b := events.NewBuilder(events.Info{Title: "My Events", Version: "1.0.0"})
//	userCreated, _ := events.NewChannel[UserCreated]("user/created", codec,
//	    events.ChannelMeta{}, events.Subscribe{}).Register(b)
//
//	// Wire to Paho on connect (JSON, the default):
//	client.Subscribe(userCreated.Topic, 1,
//	    mqtt.SubscribeHandler(ctx, userCreated, func(ctx context.Context, e UserCreated) error {
//	        return svc.HandleUserCreated(ctx, e)
//	    }, mqtt.SubscribeOptions{
//	        OnError: func(e mqtt.SubscribeError) { log.Println("event error:", e) },
//	    }),
//	)
//
//	// Subscribe with a custom format (e.g. YAML):
//	client.Subscribe(userCreated.Topic, 1,
//	    mqtt.SubscribeHandler(ctx, userCreated, handler, opts, format.YAML(codec)))
//
//	// Publish an event (JSON, the default):
//	notification := NotificationCommand{Recipient: "alice@example.com", ...}
//	mqtt.Publish(ctx, client, notifChannel, 1, false, notification, nil, opts)
//
//	// Publish with a custom format (e.g. YAML):
//	mqtt.Publish(ctx, client, notifChannel, 1, false, notification, nil, opts, format.YAML(codec))
package mqtt
